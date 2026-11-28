---
title: "Live Drain Capacity and Allocation Changes"
status: accepted
stage: DECIDED
next: "Build the remaining slices: express scaling, the lane lifecycle and graceful retirement, the pause option, concurrency caps, share epochs, settings reload, and the config read face."
related:
  - ../reference/tillr-restart.md
  - ./tillr-vocabulary.md
  - ./tillr-graph-is-the-path.md
  - ../reference/tillr-claude-backend.md
---

# Live Drain Capacity and Allocation Changes

**Status:** Partly built. Built: `tillr drain scale --lanes N`, the `ordinary_lanes` row of
`drain_runtime_config`, the `drain.config-changed` event, `.tillr.toml` reload between passes
(`refreshSettings` in [`settings_reload.go`](../../tools/tillr-drain/drain/settings_reload.go)),
and the startup lane-count line with fresh-start flag replacement (`Scale` and
`refreshLaneCount` in [`live_scale.go`](../../tools/tillr-drain/drain/live_scale.go)). Not
built: uniform `TILLR_DRAIN_<OPTION>` and `[drain] <option>` start defaults (`D-LD11`;
`rg 'TILLR_DRAIN_LANES|TILLR_DRAIN_POLICY' tools -g '!*.md'` returns nothing), express
scaling, concurrency caps, share epochs, and the `config` read face
(`rg 'express_lanes|seat_share_epochs|backend_caps|seat_caps|work_caps|policy_path' tools config -g '!*.md'`
returns nothing, and `scaleFace` in [`main.go`](../../tools/tillr-drain/main.go) takes no
`--express`, `--pause` or `--wait`).

**Needs your ruling:** None. Eight designer's decisions (`D-LD4` to `D-LD11`) in
[Section 7](#7-decision-ledger) may be overturned by the owner.

**Implementation plan:** none; [Section 8](#8-build-phases-sketch) holds the seed for one.

---

## 1. The request, what is true in the tree today, and how it fits the vocabulary

The owner asked that lane counts and allocations be changeable without restarting the drain,
that adding and removing lanes be graceful rather than a kill, and that the design survey
what else should be changeable without a restart.

What the tree does:

- **The ordinary lane count is live; the express count is not.** `tillr drain scale --lanes N`
  saves a target that the loop reads at each pass boundary. `[lanes] express` in `.tillr.toml`
  is read once at startup.
- **Seat-to-backend routing reloads on file edits, with three gaps.** `refreshPolicy` in
  [`policy_reload.go`](../../tools/tillr-drain/internal/policy/policy_reload.go) hashes the active
  backend policy file each pass and reloads it when it changes. Which file the loop watches
  is fixed at startup; the policy schema in [`policy.go`](../../tools/tillr-drain/internal/policy/policy.go)
  has no concurrency limits; and the share count in
  [`seat_shares.go`](../../tools/tillr-drain/drain/seat_shares.go) runs over the whole history
  of `seat_dispatches`, so a changed ratio on a live store starves one backend until lifetime
  totals catch up.
- **A restart re-passes the original command line.**
  [`restart.go`](../../tools/tillr-drain/restart/restart.go) re-executes the loop with its
  original arguments, so a live change survives only if the store owns it.

### Relationship to item-owned worktrees in the vocabulary design

[The vocabulary design](./tillr-vocabulary.md) replaces the fixed lane pool with worktrees
that belong to items and a running-agent limit, keeping `--lanes` as an alias. That is not
built, and the drain dispatches into numbered slots (`lane-1..N`, `express-1..M`).

This design does not create surfaces to throw away. What the operator sets and reads
(overall and express concurrency, the active policy, and caps per backend, seat and kind of
work) is the same under numbered slots and under item-owned worktrees. Only slot retirement
changes: with item-owned worktrees, lowering the concurrency setting simply stops new claims
until running work falls below the limit.

---

## 2. Choices behind lane add and remove

| Choice | Why |
| :--- | :--- |
| Raising capacity stages new slots and dispatches on the next pass | Staging in [`stage.go`](../../tools/tillr-drain/drain/stage.go) is idempotent, and a store event wakes the loop. |
| Lowering finishes in-flight work above the target before retiring slots | The owner's request; it keeps work in progress and lands candidates already produced. |
| The views say which lanes are draining out | Without it, 18 busy lanes after setting 16 looks like a failed command. |
| An idle slot retires before a busy one; among equals, the highest number first | Frees capacity at once and keeps lane names compact. |
| Desired state lives in the store, whether the loop is running or stopped | A process-id liveness check fails across container process namespaces, and a stopped drain must still accept the change. Loop presence is the file lease in [`presence.go`](../../tools/tillr-drain/presence/presence.go). |
| No tracked configuration file is the mutation path | Editing a tracked file dirties the trunk checkout and collides with landings. |
| Zero lanes is allowed | It drains ordinary work to idle while the loop keeps landing, proving and running express items. |
| No terminal hotkeys | The loop runs detached under its keeper with no terminal. |
| A slot does not retire the moment a subprocess exits | An idle slot can still hold a multi-step workflow, an unlanded candidate, a fork checkout or a paused segment. |
| No kill option; an opt-in pause instead | A kill orphans state and discards paid work. Session pause ([`tillr-restart.md`](../reference/tillr-restart.md#pausing-a-session)) reclaims capacity within seconds and loses nothing. |

---

## 3. Live lane scaling and graceful slot retirement

### 3.1 Operator command surface

```text
tillr drain scale [--lanes N] [--express M] [--pause] [--wait] [--json]
```

- `--lanes N` (`N >= 0`) sets the target count of ordinary lanes. Zero stops new ordinary
  starts while in-flight ordinary work, landings, rolling checks and express lanes continue.
- `--express M` (`M >= 0`) sets the target count of express lanes, the lanes reserved for
  work the owner is waiting on.
- At least one of the two is required; omitting one leaves that pool unchanged.
- **Graceful by default.** Without `--pause`, no running worker is interrupted; excess slots
  finish their work and then retire. With `--pause` on a lowered pool, excess running workers
  above the new target, newest first, are asked to pause
  ([Section 3.4](#34-removing-lanes-gracefully-target--active)). `--pause` has no effect when
  a pool is raised or unchanged, or when no loop is running.
- `--wait` blocks until a running loop acknowledges the target on its next pass, and times
  out cleanly if the loop is in a slow step; the stored target stays in force.
- **Works whether the loop is running or stopped.** The command writes the target to
  `drain_runtime_config` and appends a `drain.config-changed` event, then reports either that
  a running loop will apply it on the next pass or that it is saved for the next start
  ([Section 5.4](#54-startup-lane-count-reporting-and-fresh-start-flag-replacement)).

### 3.2 Slot lifecycle in `.tillr/state.db`

Run rows reference `lanes(lane_id)`, so **a `lanes` row is never deleted when scaling down.**
An additive migration adds one column:

```sql
ALTER TABLE lanes ADD COLUMN lifecycle TEXT NOT NULL DEFAULT 'active'
  CHECK (lifecycle IN ('active', 'draining', 'retired'));
```

| `lifecycle` | `state` | Planner behavior | View accounting |
| :--- | :--- | :--- | :--- |
| `active` | `idle`, `leased` or `dirty` | Eligible for new work and continuations when idle | Counted in the active target and offered capacity |
| `draining` | `leased`, `idle` or `dirty` | Closed to fresh items. May run continuations of the item pinned to its worktree until the retirement preconditions clear | Shown with active work and a `draining` badge; excluded from the target |
| `retired` | `idle` | Ignored; the row stays for history and the worktree stays clean on disk | Hidden from the lane strip and from offered counts |

### 3.3 Adding lanes (`target > active_or_draining`)

At the top of each pass:

1. **Cancel draining first.** A `draining` slot within the raised target returns to `active`
   with no worktree operation, and any run paused by scaling there becomes eligible to
   resume.
2. **Reactivate retired slots, then stage new ones**, in ascending index order.
3. **No fixed completion buffer.** Completions queue behind a mutex with a wake signal, so
   scaling up while workers are in flight never blocks a finishing worker.
4. **Dispatch on the same pass.** Reconciliation runs before planning, and a `drain.lanes`
   event records the before and after counts and what was staged, draining and retired.

### 3.4 Removing lanes gracefully (`target < active`)

Lowering a pool from `N` to `N'` moves `N - N'` slots out of `active`:

1. **Which slots leave first.**
   - **Tier 1, idle with nothing held** (highest number first): no workflow pin, no unlanded
     candidate, no paused segment, no warm session. These retire in the same pass.
   - **Tier 2, idle holding only a warm session for a refused single-step worker.** The slot
     retires; the refused item starts a fresh worker on another lane with no committed work
     lost.
   - **Tier 3, a running run or a pinned worktree obligation.** The slot becomes `draining`.
     Nothing is signaled, and while running workers in the pool meet or exceed `N'`, idle
     active slots wait instead of starting fresh items.
2. **With `--pause`.** Tiers 1 and 2 retire first. If running workers `R` still exceed `N'`,
   the `R - N'` newest-started workers have their slots set to `draining` and receive a
   bounded session-pause request (the bound is `roll.pause_timeout_seconds`):
   - An adapter that stops at a gap finishes its current turn or tool call and pauses with
     nothing lost.
   - A resumable adapter that stops where it stands is terminated and redoes the turn in
     progress when resumed.
   - An adapter that cannot resume is terminated and continues the same attempt in a fresh
     session from the worktree and transcript.

   > [!WARNING]
   > A run paused by scaling must not fall through to the immediate continuation that
   > `resumeAfterWall` gives a paused run, which would put running concurrency back above
   > `N'`. A durable scale-pause hold, parallel to `quota_pauses` in
   > [`quota_continue.go`](../../tools/tillr-drain/drain/quota_continue.go), keeps the segment
   > dormant until running concurrency drops below `N'` or the target is raised; it then
   > resumes in place as the same attempt, ahead of fresh starts.

3. **Seven preconditions before `draining` becomes `retired`**, checked at each pass and
   after each settlement:
   - no run presence lease on the lane;
   - no open writer on the run's stream;
   - no multi-node workflow pinned to the lane (a workflow in progress runs its remaining
     steps on the draining lane);
   - no item keeping the lane for an unlanded candidate
     ([`read.go`](../../tools/tillr-drain/lanestate/read.go));
   - no paused run bound to the lane, whatever paused it;
   - no dirty fork checkout;
   - a clean reset of the worktree to the trunk base.
4. **Retired worktrees stay on disk.** Removing a worktree inside the pass loop contends with
   the landing lock, changes what [`boundary.go`](../../tools/tillr-drain/internal/boundary/boundary.go)
   reads, and forces a full checkout on the next scale-up. Disk bounds belong to the worktree
   collector in [the vocabulary design](./tillr-vocabulary.md).

### 3.5 View, Observatory, and `why` representation

- **Counts.** `lanes_target` and `express_target` are the configured targets; `lanes_active`
  and `lanes_draining` (and the express equivalents) count rows by lifecycle. Each draining
  row carries its lane, its run or item, and why it has not retired: running, paused by
  scaling, finishing a workflow, or holding a candidate. Retired rows are excluded from every
  offered and idle count.
- **Display.** The status faces print, for example,
  `ordinary: 16 target, 18 present (16 active, 2 draining: lane-19, lane-20); express: 2 target, 2 active`.
  The Observatory's lane strip
  ([`LaneStrip.tsx`](../../tools/tillr-view/web/src/components/LaneStrip.tsx)) keeps draining
  lanes visible with a badge naming what they are finishing.
- **Controls ([OQ-LD4](#OQ-LD4)).** The Observatory's lane summary opens a control for lane
  counts, the pause option and caps. It writes the same store records and events as the CLI,
  under the Observatory's write-access rule
  ([`OQ-PR16`](../tillr/decision-provenance-review.md#OQ-PR16)). The view server's own store
  handle stays read-only: the write endpoints run the drain's commands as subprocesses.

---

## 4. Changing allocations on a running loop

Allocations have two halves: **routing** (which seats go to which backends, models, fallback
chains and shares) and **concurrency limits** (how many simultaneous runs a backend, a seat
or a kind of work may hold).

### 4.1 Live seat-to-backend and model routing

1. **Switching or editing the policy.**
   - Editing the active policy file reloads it on the next pass.
   - `tillr drain policy set <path>` stores `policy_path` in `drain_runtime_config`, in place
     of the file `PolicyStateFile` names; a start that finds that file migrates it once and
     warns. `tillr drain policy show` prints the active path, its digest, and the resolved
     seats, shares and caps.
   - **Validation before activation.** `policy set` refuses an invalid policy synchronously.
     A bad edit to the watched file leaves the previous valid policy in force and records a
     `drain.policy-refused` event that the status faces show.
2. **In-flight runs and warm sessions.** A run never changes backend or model mid-run;
   routing applies at each new claim. A worker returned after its seat's route changed starts
   fresh on the new route (`workerSessionReason` in
   [`worker_session.go`](../../tools/tillr-drain/drain/worker_session.go) answers
   `seat-changed`). Designer sessions are looked up by backend, so switching the design
   backend starts or resumes that backend's own session.
3. **Shares take effect from the change.** A `seat_share_epochs` row records, per seat, the
   point at which its normalized share map last changed, and the deficit count runs only over
   dispatches after that point. History stays intact for audit.

### 4.2 Concurrency limits per backend, per seat, and per kind of work

**A concurrency limit never fails, cancels or rewrites an item; it only holds new claims
until a holder finishes.** Three optional maps cap simultaneous dispatches. Zero pauses new
dispatches on a key while active ones finish; an absent key is uncapped.

| Cap map | Keys | Example | What counts |
| :--- | :--- | :--- | :--- |
| `backend_caps` | Declared backends | `{"claude-designer": 2}` | Every active run on that backend, including parallel children |
| `seat_caps` | Declared seats | `{"design": 3, "build": 14}` | Every active run under that seat, across backends |
| `work_caps` | Workflow names or built-in work kinds | `{"scouted-design@v1": 2, "fixer": 1}` | Every active item or run of that workflow or kind |

- **Baseline in the policy file, overrides in the store ([OQ-LD3](#OQ-LD3)).** Standing caps
  live in the tracked backend policy beside the seats and backends they govern. Temporary
  per-checkout throttles are set with `tillr drain alloc` and stored in
  `drain_runtime_config`, never touching a tracked file. A per-key override wins over the
  baseline for that key; clearing it restores the baseline
  ([Section 5.2](#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep)).
- **No spillover ([OQ-LD2](#OQ-LD2)).** Spilling from a capped backend onto a fallback, and
  multi-backend setups generally, are outside this design and parked on
  [the tillr roadmap's Icebox](../tillr/roadmap.md#icebox). Hitting a cap holds new claims
  with the reason `backend-capped`, `seat-capped` or `work-capped` (`D-LD8`).

### 4.3 Four scheduling invariants required so caps work cleanly

1. **Claims are counted inside the pass.** The planner decides for every idle lane before any
   subprocess starts, so active counts are seeded from in-flight runs and incremented as each
   decision is emitted. Otherwise several lanes in one pass each see room under the cap.
2. **Parallel children count.** A parallel workflow step runs several child runs on one lane
   ([`parallel.go`](../../tools/tillr-drain/drain/parallel.go)). Each live child takes a slot
   before it starts; the rest queue; at least one child is always allowed when the cap is one
   or more.
3. **Continuations before fresh starts.** An item already holding a lane takes a free cap
   slot ahead of a fresh item, so lanes pinned by workflows cannot deadlock behind new work.
4. **Lowering a cap is graceful, and the wait reason is its own.** Runs over a lowered cap
   keep running and new claims wait. A capped item reports its cap, the count and the
   holders, never `backend-unavailable`, which means exhausted quota.

---

## 5. Where running configuration lives, restart persistence, and reading current values

### 5.1 Canonical storage in `.tillr/state.db`

```sql
CREATE TABLE IF NOT EXISTS drain_runtime_config (
  key        TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  source     TEXT NOT NULL CHECK (source IN ('operator', 'migrated')),
  updated_at TEXT NOT NULL,
  updated_by TEXT NOT NULL
);
```

Keys: `ordinary_lanes` and `express_lanes` (integers, zero or more), `policy_path`, and
`backend_caps`, `seat_caps`, `work_caps` (maps of per-key overrides; a `null` value clears the
baseline cap for that key on this checkout).

Every mutation runs in one write transaction and appends a `drain.config-changed` event
carrying the key, the previous and new values, the source, who made it, and whether a loop
was present. The loop wakes on store events, so no socket, signal or tracked-file edit is
involved.

### 5.2 Precedence across startups, `tillr-drain restart`, and `tillr-keep`

| Priority | Source | When it wins |
| :--- | :--- | :--- |
| **1** | An explicit command-line option (`--lanes N` / `--max-agents N`, `--policy`, `--profile`, and the other standing start flags) on a fresh `tillr-drain loop` start (no `TILLR_RESTART_REQUEST` handoff in the environment, not `--check`) | On that start; for options backed by `drain_runtime_config` (`ordinary_lanes` today; `seat_profile` once built), it also replaces the saved target so later passes, unflagged starts and in-place restarts inherit it (`D-LD10`, `D-LD11`). |
| **2** | `drain_runtime_config` (saved scale target / operator runtime override, a deliberate later act) | Across every pass boundary, across an in-place `exec` restart (`TILLR_RESTART_REQUEST` set) even when inherited `argv` holds an earlier `--lanes` or `--policy`, across keeper relaunches after a crash, and on any start that omits the CLI option. |
| **3** | `.tillr.toml` ([`settings.go`](../../tools/tillr/settings/settings.go)), including `[drain] <snake_case>` keys (with path keys resolved relative to `<root>`) | For settings neither a fresh-start CLI option nor `drain_runtime_config` sets; overrides `TILLR_DRAIN_*` (`D-LD11`). |
| **4** | `TILLR_DRAIN_<OPTION>` environment variable (`os.LookupEnv`) | Standing per-machine / jail defaults when none of priorities 1–3 sets the option; an invalid active value refuses startup with exit 2 naming the variable and value (`D-LD11`). |
| **5** | Built-in defaults | Two ordinary lanes, no express lanes, `<root>/config/tillr/backend-policy.json`, and each flag's compiled default. |

The keeper ([`tillr-drain keep`](../../tools/tillr-drain/keep/keep.go)) and `tillr-drain keep --restart`
(`just tillr-restart`) pass the environment (`TILLR_DRAIN_*`) through unchanged, pass an explicit
`--lanes` / `--max-agents` only on the first launch (omitting it on crash relaunches after a runtime
scale change so `tillr drain scale` survives recovery), synthesize no unset `--policy` or `--profile`
flag, and supply `LoopCommand`'s default helper flags (`--gate`, `--land`, `--verdict`, `--brief-template`)
only when not already provided in `extra`, `.tillr.toml` `[drain]`, or `TILLR_DRAIN_*` (`D-LD11`).
`tillr drain config --reset [lanes|express|policy|alloc|all]` deletes the named keys and falls back
to the lower sources.

### 5.3 How an operator reads the current value

`tillr drain config [--json]` prints every runtime setting, its effective value, any
transition in progress, and its source; the status faces and the Observatory header
summarize it.

```text
$ tillr drain config
ordinary_lanes   20        source: operator (via tillr drain scale)
                           status: 20 active, 0 draining
express_lanes    2         source: .tillr.toml ([lanes] express)
policy_path      config/tillr/backend-policy.json (sha256: 9f3c1a2e…)
backend_caps     claude-designer=2 (2/2 active)
                           source: operator override
seat_caps        (none)
work_caps        fixer=1 (0/1 active)
                           source: policy file
```

### 5.4 Startup lane-count reporting and fresh-start flag replacement

`D-LD10`, which amends `D-LD6`. A saved target that silently overrode an explicit launch flag
would let the loop run a lane count nobody asked for and nobody was told about.

1. **The start lines state each effective start value and its source.** After the build line and
   `.tillr.toml` summary, the loop reports the ordinary lane count and its source:
   - `loop: ordinary lanes: N (from --lanes flag; replaced saved target M)` (or `--max-agents`;
     the replacement clause appears only when a different target was saved);
   - `loop: ordinary lanes: N (from saved target)`;
   - `loop: ordinary lanes: N (from .tillr.toml)`;
   - `loop: ordinary lanes: N (from environment variable TILLR_DRAIN_LANES)` (or `TILLR_DRAIN_MAX_AGENTS`);
   - `loop: ordinary lanes: N (from default)`.

   Alongside it (`D-LD11`), the loop prints a start-options summary line and writes the resolved
   start options and their sources (`option`, `saved scale target`, `.tillr.toml`, `environment variable`,
   or `built-in default`) into `PolicyState` (`.tillr/loop/backend-policy.json`) so `tillr drain policy`
   (text and `--json`) shows every start value in force and where it came from. A live lane change
   between passes reports `loop: ordinary lane target is now N`.
2. **An explicit `--lanes N` (or `--max-agents N`) on a fresh start is the lane count and replaces
   the saved target**, with a `drain.config-changed` event when it changes a saved value. Standing
   defaults in `.tillr.toml` and `TILLR_DRAIN_*` never overwrite `drain_runtime_config`. `N < 0` is a
   usage error; zero offers no ordinary lanes. `tillr-drain loop --check` never writes the store.
3. **Restart persistence holds.** An in-place restart (`TILLR_RESTART_REQUEST` set) ignores the
   inherited `--lanes` / `--max-agents` when a target is saved; an unflagged start uses the saved
   target before `.tillr.toml` and `TILLR_DRAIN_*`; a keeper crash relaunch omits the boot flag.

| Alternative | Verdict |
| :--- | :--- |
| The saved target overrides `--lanes` on a fresh start | Rejected: the loop runs a count the launch command contradicts, without saying so. |
| Refuse to start when the flag and the saved target disagree | Rejected: a keeper launched with `--lanes` would crash-loop until someone intervened. |
| Let inherited `--lanes` override the store on an in-place restart or `--check` | Rejected: every binary roll would wipe live scale changes (`D-LD3`). |
| Honor `--lanes` in memory only | Rejected: the first pass re-reads the saved target and snaps back to it. |

### 5.5 Uniform environment and `.tillr.toml` start defaults

`D-LD11` (designer's decision from an operator request, paired with
[`D-DSP19`](./tillr-seat-profiles.md#44-uniform-environment-and-tillrtoml-start-defaults-for-tillr-drain-loop);
not an owner ruling — the owner may overturn it):

1. **Uniform mapping:** Every standing `tillr-drain loop` start option (`25` options:
   `--exit-when-drained`, `--store`, `--max-agents`, `--lanes`, `--worktree-count`,
   `--worktree-disk-budget`, `--max-concurrent-triage`, `--lane-root`, `--backoff-base`,
   `--fallback-poll`, `--no-notify`, `--policy`, `--profile`, `--run-bin`, `--item-bin`,
   `--brief-template`, `--gate`, `--land`, `--verdict`, `--replay`, `--heavy-gate`,
   `--rolling`, `--fixer-gate`, `--questions-cmd`, `--questions-index`) derives environment
   variable `TILLR_DRAIN_<UPPER_SNAKE>` and `.tillr.toml` key `[drain] <snake_case>`, with no
   per-option exception. Only `--check`, `--migrate-owner-questions`, `--now`, and `--root` are
   excluded (one-shot modes, injected test clock, and checkout selector). Existing `[lanes] express`
   stays in `[lanes]` (not a `loop` CLI flag), and existing `[brief] template` and `[worktrees]`
   `max_count` / `disk_budget_bytes` remain `.tillr.toml`-rung aliases that refuse conflicting
   values; conflicting `max-agents` and `lanes` values at the same rung also refuse with exit 2.
2. **Portable paths in `.tillr.toml` and strict env validation:** Path-valued `[drain]` keys in
   `.tillr.toml` (`policy`, `brief_template`, `store`, `questions_index` via `validRepoFilePath`,
   and `lane_root` via `cleanChecksDir`) forbid absolute paths, `..` traversal, and `.git`, and
   resolve against `<root>`. Any active `TILLR_DRAIN_<OPTION>` value that the option refuses exits
   2 naming the variable and value — never falling through. `tools/tillr-drain/README.md` documents
   the variables, excluded flags, and precedence order.

---

## 6. Survey of everything fixed at loop start today

The rule (`D-LD9`): a setting consulted afresh for each run, candidate, landing or pass is
changeable while running; a setting that identifies the drain instance or is compiled into
the process needs a restart. A reloaded file is hashed at the top of each pass; a malformed
edit is refused with a `drain.settings-refused` event and the last valid settings stay in
force; an operator override in `drain_runtime_config` wins.

| Surface | Class | Reason |
| :--- | :--- | :--- |
| `--lanes`, `[lanes] express` | Changeable while running | [Section 3](#3-live-lane-scaling-and-graceful-slot-retirement) |
| `--policy` and the policy file's contents, shares and caps | Changeable while running | [Section 4](#4-changing-allocations-on-a-running-loop) |
| `--backoff-base`, `--brief-template`, `--gate`, `--land`, `--verdict`, `--replay`, `--heavy-gate`, `--rolling` (non-empty only), `--fixer-gate`, `--questions-cmd` | Changeable while running | Each is a wait or a command used for the next subprocess; a step in flight finishes with what it started with. |
| Every key of `.tillr.toml` and of `config/tillr/gate-policy.json` | Changeable while running | Each is consulted at a pass, staging, landing or proof boundary. |
| Workflow files, the brief template, backend adapter scripts, gate check declarations | Already live | Read per item, per run or per gate process. An item in flight keeps the graph it started with. |
| `--root`, `--store`, `--lane-root`, `--run-bin`, `--item-bin`, `--exit-when-drained` | Restart required | They identify the repository, the store, the worktree root and the process's lifetime. |
| The process environment | Restart required | It cannot be changed from outside a running process. Credential files it points at are re-read per use. |
| The compiled `tillr-drain` binary | Restart required | New code needs a re-execution; [graceful restart](../reference/tillr-restart.md) pauses and resumes runs in place. |
| Binaries run as subprocesses (gate, land, the CLI) | Live once reinstalled | Each invocation runs the installed binary. |
| The keeper's own flags and the Observatory server | Their own restart | Neither restart touches the loop or a running lane. |

---

## 7. Decision Ledger

### Settled by the owner

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-LD1` | Ordinary and express lane counts change on a running drain: raising takes effect at the next start decision, lowering is graceful, and the views show lanes draining out. | 2026-11-19 | [Section 3](#3-live-lane-scaling-and-graceful-slot-retirement) | Partial: ordinary lanes only, no lifecycle column |
| `D-LD2` | Allocations, both routing and concurrency caps, change on a running drain. | 2026-11-19 | [Section 4](#4-changing-allocations-on-a-running-loop) | Policy file reload only |
| `D-LD3` | Running configuration persists across restarts and keeper relaunches, and operators have a read surface showing effective values and their sources. | 2026-11-19 | [Section 5](#5-where-running-configuration-lives-restart-persistence-and-reading-current-values) | Partial: the lane target persists; no `config` face |
| <a id="OQ-LD1"></a>[`OQ-LD1`](#OQ-LD1) | **Owner ruling:** lowering lanes is graceful by default, and `--pause` is an opt-in that pauses excess running workers, newest first, through session pause instead of killing processes. | 2026-11-19 | [Section 3.1](#31-operator-command-surface), [Section 3.4](#34-removing-lanes-gracefully-target--active) | No |
| <a id="OQ-LD2"></a>[`OQ-LD2`](#OQ-LD2) | **Owner ruling:** multi-backend setups and cap spillover are deferred to a separate design on the roadmap's Icebox; no spillover option was chosen. "I'm not ready for this. I will manually switch them right now and restart. But I do want to do this eventually". | 2026-11-20 | [Section 4.2](#42-concurrency-limits-per-backend-per-seat-and-per-kind-of-work) | Not applicable |
| <a id="OQ-LD3"></a>[`OQ-LD3`](#OQ-LD3) | **Owner ruling:** baseline caps live in the tracked backend policy, with live per-checkout overrides in the store, "standing subscription protections belong in git, while one-hour machine throttles must never dirty the trunk checkout." | 2026-11-20 | [Section 4.2](#42-concurrency-limits-per-backend-per-seat-and-per-kind-of-work), [Section 5.1](#51-canonical-storage-in-tillrstatedb) | No |
| <a id="OQ-LD4"></a>[`OQ-LD4`](#OQ-LD4) | **Owner ruling:** both CLI commands and Observatory controls adjust lane counts and caps, writing the same store records, under the Observatory's write-access rule. | 2026-11-20 | [Section 3.5](#35-view-observatory-and-why-representation) | No |

### The designer's decisions, awaiting the owner's review

None of these is the owner's ruling; the owner may overturn any of them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-LD4` | A `lifecycle` column on `lanes` (`active`, `draining`, `retired`); rows are never deleted, retired worktrees stay on disk, idle excess slots retire at once, and a draining slot waits for seven preconditions. **Rejected:** deleting rows; retiring between nodes of a workflow; removing worktrees in the pass loop. | 2026-11-19 | [Section 3.2](#32-slot-lifecycle-in-tillrstatedb), [Section 3.4](#34-removing-lanes-gracefully-target--active) | No |
| `D-LD5` | Zero is a valid lane count for either pool, and completions queue without a fixed buffer. **Rejected:** a minimum of one; a buffer sized at startup. | 2026-11-19 | [Section 3.1](#31-operator-command-surface), [Section 3.3](#33-adding-lanes-target--active_or_draining) | Partial: ordinary lanes |
| `D-LD6` | Runtime overrides live in `drain_runtime_config` with `drain.config-changed` events and work whether the loop runs or not. Amended by `D-LD10` and `D-LD11`. **Rejected:** mutating tracked files; letting inherited arguments overwrite stored changes on restart. | 2026-11-19; amended 2026-11-23, 2026-11-28 | [Section 5.1](#51-canonical-storage-in-tillrstatedb), [Section 5.2](#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep) | Partial: `ordinary_lanes` |
| `D-LD7` | Share deficits count from a per-seat epoch recorded when that seat's share map changes. **Rejected:** deleting dispatch history; all-time counting. | 2026-11-19 | [Section 4.1](#41-live-seat-to-backend-and-model-routing) | No |
| `D-LD8` | Caps count claims inside the pass, count parallel children, put continuations ahead of fresh starts, and report their own wait reasons. **Rejected:** counting only after spawn; ignoring parallel children; reusing `backend-unavailable`. | 2026-11-19 | [Section 4.3](#43-four-scheduling-invariants-required-so-caps-work-cleanly) | No |
| `D-LD9` | Settings consulted per pass, run or landing reload live and refuse invalid edits; settings that identify the instance or are compiled in need a restart. The owner may overturn this row by row. | 2026-11-19 | [Section 6](#6-survey-of-everything-fixed-at-loop-start-today) | Partial: `.tillr.toml` reload |
| `D-LD10` | The start line states the ordinary lane count and its source; an explicit `--lanes` on a fresh start is the count and replaces the saved target; restarts, unflagged starts and keeper relaunches use the saved target (extended across `.tillr.toml` and `TILLR_DRAIN_*` by `D-LD11`). **Rejected:** the alternatives in Section 5.4. | 2026-11-23; extended 2026-11-28 | [Section 5.2](#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep), [Section 5.4](#54-startup-lane-count-reporting-and-fresh-start-flag-replacement) | Yes |
| `D-LD11` | **From an operator request; the designer's decision, not an owner ruling (the owner may overturn it).** Extends `D-LD6` and `D-LD10` and pairs with [`D-DSP19`](./tillr-seat-profiles.md#decision-ledger): every standing `tillr-drain loop` start option (`25` options; excluding only `--check`, `--migrate-owner-questions`, `--now`, and `--root`) maps uniformly to `TILLR_DRAIN_<UPPER_SNAKE>` (`os.LookupEnv`) and `[drain] <snake_case>` in `.tillr.toml` (with path keys validated as clean `<root>`-relative paths and resolved against `<root>`, and existing `[brief] template` / `[worktrees]` keys kept as `.tillr.toml`-rung aliases). Precedence on a fresh start: CLI option > saved runtime target in `drain_runtime_config` (outranks inherited boot `argv` on `TILLR_RESTART_REQUEST` in-place `exec` restarts) > `.tillr.toml` > `TILLR_DRAIN_<OPTION>` > built-in default. Invalid active `TILLR_DRAIN_*` values exit 2 naming the variable and value; loop start output and `tillr drain policy` (`PolicyState`) report each value and its source; `tillr-drain keep` and `just tillr-restart` pass `TILLR_DRAIN_*` through unchanged without synthesizing unset CLI flags. **Rejected:** `environment > .tillr.toml`; skipping invalid `TILLR_DRAIN_*` values; letting `.tillr.toml` or `TILLR_DRAIN_*` overwrite `drain_runtime_config`. | 2026-11-28, designer, from an operator request | [Section 5.2](#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep), [Section 5.4](#54-startup-lane-count-reporting-and-fresh-start-flag-replacement), [Section 5.5](#55-uniform-environment-and-tillrtoml-start-defaults) | No |

---

## 8. Build phases (sketch)

A seed for an implementation plan, not a hand-off: it holds what the design work surfaced and is
incomplete until a planner reads the tree. On any conflict about behavior, the sections above win.
`tillr-keep` already reads the saved ordinary lane target from the store.

The phases are ordered so each slice compiles and passes its module's suite on the trunk, and so independent slices touch different files.

| Phase | Scope and target files | Rests on | Done when |
| :--- | :--- | :--- | :--- |
| **1. Runtime config store, precedence and keeper sync** | [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) and [`tools/tillr-drain/internal/storelog/schema.go`](../../tools/tillr-drain/internal/storelog/schema.go) (the lane lifecycle column and `seat_shares` epochs), [`live_scale.go`](../../tools/tillr-drain/drain/live_scale.go), [`loop.go`](../../tools/tillr-drain/drain/loop.go), [`main.go`](../../tools/tillr-drain/main.go) (a `config` read and reset face, and the lane count on the start line), [`tools/tillr-drain/keep/keep.go`](../../tools/tillr-drain/keep/keep.go) (read every runtime value from the store, and omit the boot-time `--lanes` on a crash relaunch) | `D-LD3`, `D-LD4`, `D-LD6`, `D-LD10`, `D-LD11` | Runtime values persist across an in-place `tillr drain restart`, an unflagged start and a `tillr-keep` crash relaunch; `tillr drain config [--json]` prints each effective value and its source |
| **2. Lane scaling and graceful slot retirement** | [`main.go`](../../tools/tillr-drain/main.go) (the `scale` face), [`loop.go`](../../tools/tillr-drain/drain/loop.go) (an unbounded completion queue and slot reconciliation at the start of a pass), [`stage.go`](../../tools/tillr-drain/drain/stage.go), [`lane_base.go`](../../tools/tillr-drain/drain/lane_base.go), [`plan.go`](../../tools/tillr-drain/drain/plan.go) (eligibility by lifecycle, and the retirement preconditions the design lists), [`why.go`](../../tools/tillr-drain/why.go) | [OQ-LD1](#OQ-LD1) | Scaling ordinary or express lanes up stages or reactivates slots on the next pass; scaling down retires idle slots at once and holds busy slots in `draining` until every precondition clears, without deleting `lanes` rows or ending runs; `0` is allowed; a re-increase flips `draining` slots back to `active` first |
| **3. Draining lanes in the view and the Observatory** | [`tools/tillr-view/lanes.go`](../../tools/tillr-view/lanes.go), [`serveread.go`](../../tools/tillr-view/serveread.go), [`LaneStrip.tsx`](../../tools/tillr-view/web/src/components/LaneStrip.tsx), and the header and status components and tests under [`tools/tillr-view/`](../../tools/tillr-view/) | [OQ-LD4](#OQ-LD4) | `retired` rows do not count toward the lane totals; `tillr view status`, `tillr view lanes` and the Observatory show target counts, active counts, and each `draining` lane with why it is still finishing |
| **4. Policy switching, `seat_shares` epochs and concurrency caps** | [`policy.go`](../../tools/tillr-drain/internal/policy/policy.go), [`policy_reload.go`](../../tools/tillr-drain/internal/policy/policy_reload.go), [`seat_shares.go`](../../tools/tillr-drain/drain/seat_shares.go), [`plan.go`](../../tools/tillr-drain/drain/plan.go), [`parallel.go`](../../tools/tillr-drain/drain/parallel.go), [`main.go`](../../tools/tillr-drain/main.go) (the `policy set` and `alloc` faces) | [OQ-LD2](#OQ-LD2), [OQ-LD3](#OQ-LD3) | `tillr drain policy set <path>` swaps the active policy and refuses an invalid one; a `seat_shares` change counts from a fresh epoch; `backend_caps`, `seat_caps` and `work_caps` count claims made within a pass and parallel child runs, and `tillr drain why` names `backend-capped`, `seat-capped` or `work-capped` |
| **5. Reload of `.tillr.toml` and `gate-policy.json` between passes, and operating documents** | [`settings.go`](../../tools/tillr/settings/settings.go), [`redcap/policy.go`](../../tools/tillr-drain/redcap/policy.go), [`loop.go`](../../tools/tillr-drain/drain/loop.go), [the drain README](../../tools/tillr-drain/README.md), [the view README](../../tools/tillr-view/README.md) and the `tillr-operating` skill | `D-LD9` | Editing `.tillr.toml` or `config/tillr/gate-policy.json` takes effect at the next pass, an invalid file is refused with `drain.settings-refused` and the last valid values stay in force, and every new face answers `--help` |

### Checks for the planner

- **Keep slot reconciliation behind one narrow interface** in `tools/tillr-drain/drain/`. When [item-owned worktrees](./tillr-vocabulary.md#3-what-a-lane-was-and-the-worktree-as-a-resource) replace numbered lanes, the runtime config table and the `scale`, `alloc` and `config` faces should carry over to the running-agent limit without a schema change.
- **Read-only faces open stores that were never migrated.** [`lanestate/read.go`](../../tools/tillr-drain/lanestate/read.go), [`lanes.go`](../../tools/tillr-view/lanes.go) and [`serveread.go`](../../tools/tillr-view/serveread.go) open a store read-only and run no migration, so a query on a new column or table is guarded by schema introspection, and a store without the lifecycle column reads as every lane `active`.
- **Phases 2 and 4 share files.** Both change [`plan.go`](../../tools/tillr-drain/drain/plan.go) and [`main.go`](../../tools/tillr-drain/main.go), so phase 4 follows phase 2. Phase 3 can run beside either once phase 1's schema is in.
- **A documented face must exist.** `FacesHelpError` in [`tools/tillr/tillrdocs`](../../tools/tillr/tillrdocs) runs each documented `tillr drain <face>` with `--help`, so a slice that documents a new face ships with, or after, the face itself.

---

## Open Questions

None. All four questions are compacted into [Section 7](#7-decision-ledger).
