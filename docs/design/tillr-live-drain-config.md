---
title: "Live Drain Capacity and Allocation Changes"
status: in-review
stage: DESIGN
updated: 2026-11-19
next: "Rule OQ-LD2, OQ-LD3, and OQ-LD4 (OQ-LD1 ruled 2026-11-19); complete tillr-live-drain-config-plan.md into fenced build slices once open questions settle"
related:
  - ./tillr-restart.md
  - ./tillr-vocabulary.md
  - ./tillr-graph-is-the-path.md
  - ./tillr-claude-backend.md
  - ./tillr-live-drain-config-plan.md
---

# Live Drain Capacity and Allocation Changes

**Status:** 2026-11-19 — nothing built; audited against `84474e8a1`. [OQ-LD1](#OQ-LD1) ruled on 2026-11-19; three questions open for the owner, and six designer's decisions (`D-LD4`–`D-LD9`) await his review in [Section 7](#7-decision-ledger).
**Needs your ruling:** [OQ-LD2](#OQ-LD2), [OQ-LD3](#OQ-LD3), [OQ-LD4](#OQ-LD4)
**Implementation plan:** [tillr-live-drain-config-plan.md](./tillr-live-drain-config-plan.md) (sketch until open questions settle)

---

## 1. The request, what is true in the tree today, and how it fits the vocabulary

On 2026-11-19, raising the drain from 16 lanes to 20 required stopping and restarting `tillr-drain loop`. The owner asked:

> I also want you to be able to change the number of lanes and allocations and things like that without restarting the drain. We used to have this with board, and we've got to get this back with tiller. There's no reason to burn a restart on stuff like this. Also explore what other things we should be able to change without a restart. And I want you to have the same graceful adding lane, removing lane support that we had, or whatever makes sense now, so we're not just outright killing things.

Checked against the tree at `84474e8a1`, three pieces of that picture stand in different places today:

- **Ordinary and express lane counts are frozen at process start.** [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go) reads `--lanes N` on `tillr-drain loop` and `[lanes] express` from `.tillr.toml` once at startup, builds fixed `lane-1..lane-N` and `express-1..express-M` slices in `LoopConfig`, stages those worktrees before entering the pass loop, and sizes `completions := make(chan completed, len(cfg.Lanes))` to the startup lane count (`35526a762`). Changing either count today requires a process restart.
- **Seat-to-backend routing already reloads on file edits, with three gaps.** [`tools/tillr-drain/drain/policy_reload.go`](../../tools/tillr-drain/drain/policy_reload.go) (`refreshPolicy`, landed in `59211351a` on 2026-11-16) hashes the active backend policy file at the top of every pass and reloads its JSON body when the file changes on disk. What is still missing or broken:
  1. Which policy file the loop watches (`--policy <path>`) is fixed at startup unless an undocumented `.tillr/loop/backend-policy.json` file is written on disk.
  2. The policy schema in [`tools/tillr-drain/drain/policy.go`](../../tools/tillr-drain/drain/policy.go) has **no concurrency limits** on how many simultaneous lanes a backend, a seat, or a kind of work may hold.
  3. Proportional `seat_shares` in [`tools/tillr-drain/drain/seat_shares.go`](../../tools/tillr-drain/drain/seat_shares.go) counts `SELECT backend, count(*) FROM seat_dispatches WHERE seat=? GROUP BY backend` over the entire history of `.tillr/state.db`, so editing `seat_shares` on a live store starves one backend for hundreds of dispatches until lifetime totals catch up to the new ratio.
- **Restarting clobbers runtime changes unless the store owns them.** Both [`tools/tillr-drain/restart/restart.go`](../../tools/tillr-drain/restart/restart.go) (which calls `syscall.Exec` with the loop's original `os.Args`) and [`tools/tillr-roll/tillr-keep`](../../tools/tillr-roll/tillr-keep) (which relaunches `tillr-drain loop` with the boot-time `--lanes` and `--policy` flags) pass the original command line back into the restarted process. Any live change kept only in memory — or overridden by startup flags on re-exec — would revert on the next `tillr-drain restart`.

### Relationship to item-owned worktrees in the vocabulary design

[The vocabulary design](./tillr-vocabulary.md) ([Section 3](./tillr-vocabulary.md#3-what-a-lane-was-and-the-worktree-as-a-resource), `D-VO5`, `D-VO6`, `D-VO9`) records the owner's 2026-11-18 ruling that a worktree should belong to an item rather than a fixed pool, retiring the word "lane" into **running-agent limit**, **worktree**, **scratch**, and **lease** while keeping existing `--lanes` flags, stored rows, and events as documented aliases. None of item-owned worktrees is built yet ([the landing call](./tillr-landing-call.md) notes [`OQ-LC1`](./tillr-landing-call.md#OQ-LC1) is blocked on it), and the running drain dispatches into numbered slots (`lane-1..N`, `express-1..M`) today.

This design solves the running drain as built today without creating throwaway surfaces:

- **What the operator sets and reads** — overall concurrency, express concurrency, active backend policy, and per-backend / per-seat / per-work concurrency caps in `.tillr/state.db` — is identical under numbered lane slots and under item-owned worktrees.
- **Only the slot-retirement mechanics change when item-owned worktrees land.** Today, lowering concurrency marks excess numbered slots `draining` until their worktree obligations finish, then `retired`. Once worktrees belong to items, the exact same concurrency setting becomes the running-agent limit of [the vocabulary design](./tillr-vocabulary.md#35-what-limits-how-many-agents-run-at-once) and [the graph design](./tillr-graph-is-the-path.md#6-scheduling): lowering it simply stops new claims until in-flight runs finish below the new limit, and the numbered-slot retirement code deletes cleanly behind one pool boundary.

---

## 2. How the retired `board` handled lane add and remove

The retired `board` drain implemented live lane scaling across four commits (`8b5e1a944`, `7e5440ca3`, `6c8042fe6`, and `cafd2ca2f`) before `board` was deleted in `3f02e3b91` and `afdc9a957`. Its operator behavior is also recorded in [the landing runbook](../reference/landing-runbook.md) and [hand-drain tactics](./hand-drain-tactics-2026-10-31.md).

### What `board` built

1. **Three input surfaces feeding one ceiling.**
   - CLI: `board drain lanes [--lanes] N [--mode gentle|kill]` (`gentle` default; `N < 1` refused).
   - Config polling: `board.jsonc` / `board.toml` `[lanes] count` polled from disk on every loop tick when no CLI override superseded it (`7e5440ca3`).
   - Raw terminal hotkeys when attached to a TTY: `+` / `=` raised the ceiling by 1, `-` lowered gently by 1, and `k` lowered by 1 while killing the newest active worker (`8b5e1a944`).
2. **Request/ack event handshake correlated by `(pid, started)`.**
   - `board drain lanes` inspected `drain.started` / `drain.stopped` events and `kill(pid, 0)` to find one live drain, wrote a `drain.lanes-request` event carrying `(request_id, pid, started, lanes, mode)`, and polled `events` for up to 15 seconds for a matching `drain.lanes` acknowledgment carrying `(before, after, active, retiring, killed)` (`6c8042fe6`).
   - If no drain was running, `board drain lanes` failed with an error instead of recording the new count for the next start.
3. **Scaling up and scaling down inside the loop.**
   - Raising `targetLanes` from `N` to `N'` materialized `lane-(N+1)..lane-N'` via `StageLane` and immediately dispatched ready items into them on the same tick.
   - Lowering `targetLanes` in `gentle` mode left every in-flight worker untouched, restricted new dispatches to lane indices `<= targetLanes` while also guarding `len(inFlight) < targetLanes`, and retired higher-numbered slots as each worker exited.
   - Lowering `targetLanes` in `kill` mode sorted active workers by `(startedAt DESC, slot DESC)` and sent `SIGKILL` (process-group kill) to `len(inFlight) - targetLanes` newest workers so slots freed immediately (`8b5e1a944`). Single-run surgery also existed as `board drain kill <run-id>` (`cafd2ca2f`).
4. **Status visibility while draining down.**
   - `drain.lanes` events and `board drain status` printed `target N, active A (R still finishing above the ceiling)` so the operator never mistook `A > N` during a gentle reduction for a broken command (`6c8042fe6`).
5. **No live allocation controls.**
   - `board` had `--agent` and `--model` startup flags, and zero live commands to switch backend routing or cap concurrency per backend, seat, or workflow.

### What carries over and what does not

| Mechanism in `board` | Verdict for `tillr` | Why |
| :--- | :--- | :--- |
| Raising capacity stages new slots and dispatches on the very next pass | **Carries over** | Staging in [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go) is idempotent; waking the loop within one 100 ms store poll makes new lanes available immediately. |
| Gentle lowering finishes in-flight work above the ceiling before retiring excess slots | **Carries over** | Directly requested by the owner; preserves work in progress and lands candidates already produced. |
| Explicit `"draining"` / `"still finishing above the ceiling"` reporting in CLI and web views | **Carries over** | Without explicit draining state, seeing 18 busy lanes after setting 16 looks like the command failed. |
| Preferring highest-numbered slots when retiring idle capacity | **Carries over (refined)** | Keeps active lane names compact (`lane-1..N`), refined so an **idle** lower-numbered slot retires immediately ahead of a **busy** higher-numbered slot rather than keeping excess lanes running unnecessarily. |
| `(pid, started)` `kill(pid, 0)` liveness and failing when no loop is running | **Does not carry over** | Fails across container PID namespaces (already solved in [`tools/tillr-drain/drain/presence.go`](../../tools/tillr-drain/drain/presence.go) via Linux `F_RDLCK` file leases) and violates persistence across restarts if a stopped store rejects changes. Desired state belongs in `.tillr/state.db` whether the loop is live or stopped. |
| Polling a tracked working-tree config file (`board.jsonc`) as the primary mutation path | **Does not carry over** | Editing tracked files on the host dirties `/workspace` and collides with `tillr-land` trunk advances. Operational mutations belong in `.tillr/state.db`. |
| Refusing `0` lanes (`minimum 1`) | **Does not carry over** | Setting ordinary `--lanes 0` is the clean way to drain all ordinary work to idle (for maintenance or cost pause) while keeping the loop alive for landings, rolling checks, or express items. |
| Raw terminal hotkeys (`+` / `-` / `k`) | **Does not carry over** | `tillr-drain loop` runs detached under `tillr-keep` with stdin redirected from `/dev/null`, not in an interactive terminal. |
| Single flat integer pool | **Does not carry over** | `tillr` runs two pools (`lane-1..N` ordinary and `express-1..M` express, `35526a762`), plus per-backend, per-seat, and per-work allocations. |
| Retiring a slot the instant one subprocess exits | **Does not carry over** | In `tillr`, an idle slot can still hold a multi-step workflow (`workerLanes`), an unlanded candidate (`lanestate` `"kept"`), a fork checkout (`BaseRef != ""`), or a quota-paused segment. Retiring before those clear destroys work. |
| `SIGKILL` (`--mode kill`) on excess workers | **Replaced by opt-in `--pause`** | `SIGKILL` orphans state and throws away tokens. Lowering lanes is graceful by default and never kills processes; `tillr drain scale ... --pause` reclaims capacity quickly using session pause instead ([`tillr-restart.md`](./tillr-restart.md), [Section 3.4](#34-removing-lanes-gracefully-target--active)). |

---

## 3. Live lane scaling and graceful slot retirement

### 3.1 Operator command surface

One subcommand on `tillr-drain` (reachable via `tillr drain scale`) adjusts either or both pools:

```text
tillr drain scale [--lanes N] [--express M] [--pause] [--wait] [--json]
```

- `--lanes N` (`N >= 0`) sets the target count of ordinary lanes (`lane-1..lane-N`). Setting `--lanes 0` stops new ordinary starts while allowing in-flight ordinary work, landings, rolling checks, and express lanes to run.
- `--express M` (`M >= 0`) sets the target count of express lanes (`express-1..express-M`) reserved for owner-waiting work (`35526a762`).
- At least one of `--lanes` or `--express` is required; omitting one leaves that pool's target unchanged.
- **Graceful by default, opt-in `--pause`:** Plain `tillr drain scale` without `--pause` is strictly non-signalling — no running worker is interrupted, and excess slots finish their active work before retiring. Passing `--pause` when lowering a pool asks excess running workers above the new target (newest-started first) to pause via session pause ([`tillr-restart.md`](./tillr-restart.md#pausing-a-session)) instead of killing processes, dropping machine load within seconds while holding paused work to resume as remaining lanes open ([Section 3.4](#34-removing-lanes-gracefully-target--active)). When raising or leaving a pool's target unchanged, or when no loop is running, `--pause` has no effect on that pool.
- `--wait` blocks until an active loop acknowledges the new target on its next pass (typically under 100 ms; times out cleanly if the loop is mid-pass in a slow synchronous step, while the stored target remains in force).
- **Works whether the loop is running or stopped.** The command writes the target atomically into `drain_runtime_config` in `.tillr/state.db` and appends a `drain.config-changed` event. It checks `drain.LoopPresent(root)` ([`tools/tillr-drain/drain/presence.go`](../../tools/tillr-drain/drain/presence.go)) to report either `loop active: target 20 ordinary, 2 express (staging 4 lanes on next pass)` or `no loop running: target 20 ordinary, 2 express saved for next start`.

### 3.2 Slot lifecycle in `.tillr/state.db`

Today `lanes` has `(lane_id TEXT PRIMARY KEY, worktree_path TEXT NOT NULL, branch TEXT NOT NULL, base_sha TEXT, head_sha TEXT, state TEXT NOT NULL, leased_by TEXT, leased_at TEXT, updated_at TEXT)`, and both `runs.lane_id` and `read_only_runs.lane_id` carry foreign keys referencing `lanes(lane_id)` ([`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) and [`tools/tillr-drain/drain/schema.go`](../../tools/tillr-drain/drain/schema.go)). **Rows in `lanes` must never be deleted when scaling down.**

Instead, an additive migration adds one column to `lanes`:

```sql
ALTER TABLE lanes ADD COLUMN lifecycle TEXT NOT NULL DEFAULT 'active'
  CHECK (lifecycle IN ('active', 'draining', 'retired'));
```

| `lifecycle` | `state` | Planner behavior | View / `why` accounting |
| :--- | :--- | :--- | :--- |
| `active` | `idle`, `leased`, or `dirty` | Eligible for new work and continuations when `idle` | Counted in active target and offered capacity |
| `draining` | `leased`, `idle`, or `dirty` | **Closed to fresh items.** May run continuations of the item already pinned to its worktree until retirement preconditions clear, then transitions to `retired` | Shown in active work + explicit `draining` badge (`2 draining out`); excluded from target ceiling |
| `retired` | `idle` | Ignored by planner; row kept for foreign-key history; worktree stays clean and dormant on disk | Hidden from active lane strip and excluded from offered lane counts |

### 3.3 Adding lanes (`target > active_or_draining`)

At the top of every pass in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (which `waitForTrigger` in [`tools/tillr-drain/drain/trigger.go`](../../tools/tillr-drain/drain/trigger.go) wakes within `DefaultStorePollInterval = 100ms` whenever an event lands in `.tillr/state.db`):

1. **Cancel `draining` lanes first.** If the operator lowered from 20 to 14 and then raised to 18 while `lane-17` and `lane-18` were still `draining` (including slots holding a scale-paused run), flipping `draining -> active` reclaims already-staged slots with zero git worktree operations before activating any `retired` or brand-new slot, and any scale-paused runs within the raised target become eligible to resume immediately.
2. **Reactivate `retired` slots or stage new ones.** To reach `N` active ordinary lanes (or `M` active express lanes), the loop activates dormant `retired` slots in ascending index order (`lane-1`, `lane-2`, …) and creates any never-before-seen indices (`lane-21`, …) via staging in [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go) and [`tools/tillr-drain/drain/lane_base.go`](../../tools/tillr-drain/drain/lane_base.go), upserting each row with `lifecycle = 'active'`.
3. **Remove the fixed startup channel bound.** `completions := make(chan completed, len(cfg.Lanes))` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) is replaced by a mutex-guarded completion queue plus a `chan struct{}` wake signal (`make(chan struct{}, 1)`), so scaling from 2 lanes to 40 while workers are in flight never blocks a finishing worker goroutine.
4. **Immediate dispatch.** Because reconciliation runs at the start of `pass()` right before `Plan()`, newly added lanes participate in the very next start decision and emit a `drain.lanes` event recording `(ordinary_before, ordinary_after, express_before, express_after, staged, draining, retired)`.

### 3.4 Removing lanes gracefully (`target < active`)

When an operator lowers a pool's target from `N` to `N'`, exactly `N - N'` currently `active` slots in that pool must leave `active`:

1. **Which slots leave `active` first (least disruption, then highest index):**
   - **Tier 1 — Immediately retireable idle slots** (highest lane number first): `state = 'idle'` with no workflow pin, no unlanded candidate, no paused segment, and no warm refusal session. These transition straight from `active -> retired` in the same pass, so lowering from 20 to 16 when 4 lanes are idle frees capacity immediately even if `lane-19` happens to be busy and `lane-15` is idle.
   - **Tier 2 — Idle slots holding only a warm refusal `WorkerSession`** (highest lane number first): no unlanded candidate and no multi-node workflow pin, only an opportunistic session handle for a refused single-step worker. Retiring the slot transitions it to `retired` cleanly; when the refused item next dispatches on an active lane, `workerSessionReason` sees a different lane and starts a fresh worker run without losing any committed work.
   - **Tier 3 — Slots with an active run or pinned worktree obligation:** transition `active -> draining`. By default (without `--pause`), selection among Tier 3 slots takes highest lane number first, **nothing in flight is killed or signaled**, and while running workers across `active` and `draining` slots in the pool still meet or exceed `N'`, idle `active` slots wait rather than starting fresh items until running concurrency drops below `N'`.
2. **Opt-in fast resource reclaim (`--pause`):**
   When `tillr drain scale` is invoked with `--pause` on a lowered pool, Tier 1 and Tier 2 idle retirements run first; if active running workers `R` in that pool are already `<= N'`, no running worker is signaled. When `R > N'`, the `R - N'` **newest-started** running workers in that pool (`started_at DESC`, ties broken by highest lane index) have their slots moved `active -> draining` (with any remaining `active -> draining` transitions needed to reach `N'` `active` slots taken by highest lane index) and receive bounded session-pause requests (`run.RequestPause` bounded by `roll.pause_timeout_seconds`, [`tillr-restart.md`](./tillr-restart.md#pausing-a-session)) instead of `SIGKILL`:
   - Gap-stopping adapters (`stops-at-a-gap`, such as `pi`) finish their current model turn or tool call (up to `roll.pause_timeout_seconds`) and pause at the next gap between tool calls with zero lost work.
   - Resumable non-gap adapters (`stops-where-it-stands`, such as `run-claude`) stop on `SIGTERM` (10 s grace before kill) and redo the turn or tool call in progress when resumed, preserving earlier turns and uncommitted worktree state.
   - Non-resumable adapters (`cannot-resume`) stop on `SIGTERM` and continue the same attempt on a fresh session from the worktree and prior transcript.

   > [!WARNING]
   > Do not let a scale-paused run fall through to build-restart's immediate continuation in [`tools/tillr-drain/drain/pass.go`](../../tools/tillr-drain/drain/pass.go) (`resumeAfterWall`), which resumes `StatePaused` runs immediately on settlement and would put running concurrency right back above `N'`. Record a durable scale-pause hold (parallel to `quota_pauses` in [`tools/tillr-drain/drain/quota_continue.go`](../../tools/tillr-drain/drain/quota_continue.go)) so paused segments stay dormant with zero running processes while running workers in the pool are `>= N'`, then resume in place as the same attempt (spending no retry) ahead of fresh item starts once running concurrency drops below `N'` or the target is raised.

3. **Seven preconditions before a `draining` slot becomes `retired`:**
   A `draining` slot is checked at the top of each pass and after each run settlement, and transitions to `retired` only when **all seven** hold:
   - **No run presence lease:** `RunPresent(root, runID) == false` for any run last leased to the lane ([`tools/tillr-drain/drain/presence.go`](../../tools/tillr-drain/drain/presence.go)).
   - **No open stream writers:** `StreamWriters(streamPath) == 0`, matching orphan recovery's check that no child subprocess still holds the run pipe open.
   - **No active multi-node workflow pin:** `workerLanes[laneID] == ""` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go). While an item is mid-workflow on `lane-19` (for example between `scout` and `join`), `lane-19` **continues running the remaining steps of that workflow** while `draining`, and retires only after the item's workflow settles or hands off.
   - **No kept candidate obligation:** [`tools/tillr-drain/lanestate/read.go`](../../tools/tillr-drain/lanestate/read.go) reports no item keeping the lane (`Status != "kept"`), protecting fork deliveries (`base_ref != ""`), boundary-violation checkouts awaiting inspection, candidate-record mismatches, and unverified pins.
   - **No lane-bound paused run:** no quota-held, scale-paused, or restart-paused segment is bound to `laneID`.
   - **Clean fork checkout:** any `.forks/lanes/<laneID>/...` checkout materialized for the lane has no dirty working tree.
   - **Clean reset to trunk base:** `resetLaneToBase` resets `<lane-root>/<laneID>/worktree` cleanly to trunk HEAD and records `state = 'idle', lifecycle = 'retired'`.
4. **Keep dormant worktrees on disk on retirement.**
   `retired` leaves `<lane-root>/<laneID>/worktree` registered and clean on disk (~62 MB per worktree, measured in [the vocabulary design](./tillr-vocabulary.md#31-where-the-disk-goes)) rather than running `git worktree remove` inside the pass loop. Removing git worktrees live contends with `land.lock`, alters `git worktree list` under [`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go), and forces a full re-checkout when scaling back up. Disk bounds and scratch collection belong to the worktree collector designed in [the vocabulary design, Section 3.3](./tillr-vocabulary.md#33-the-life-of-a-worktree).

### 3.5 View, Observatory, and `why` representation

Today [`tools/tillr-view/lanes.go`](../../tools/tillr-view/lanes.go), [`tools/tillr-view/serveread.go`](../../tools/tillr-view/serveread.go), and [`tools/tillr-drain/why.go`](../../tools/tillr-drain/why.go) query `SELECT lane_id, state FROM lanes` with no lifecycle filter, so retired historical rows would inflate `lanes_total` and `lanes_offered` if left unfiltered.

- **Active vs draining vs retired counts:**
  - `lanes_target` / `express_target`: the configured targets from `drain_runtime_config`.
  - `lanes_active` / `express_active`: rows with `lifecycle = 'active'`.
  - `lanes_draining` / `express_draining`: rows with `lifecycle = 'draining'` (each carrying its `lane_id`, current `run_id` / `item_id`, and why it has not yet retired: `running <run_id>`, `scale-paused <run_id>`, `finishing workflow for <item_id>`, or `holding candidate for <item_id>`).
  - `retired` rows are excluded from `lanes_offered`, `lanes_idle`, and the Observatory `LaneStrip`.
- **CLI and Observatory display:**
  - `tillr view status`, `tillr view lanes`, and `tillr drain status` print `ordinary: 16 target, 18 present (16 active, 2 draining: lane-19, lane-20); express: 2 target, 2 active`.
  - In [`tools/tillr-view/web/src/components/LaneStrip.tsx`](../../tools/tillr-view/web/src/components/LaneStrip.tsx) and the Observatory header, `draining` lanes stay visible beside active lanes with an amber `draining` pill and tooltip naming the run or workflow they are finishing, then disappear once `retired`. Whether the Observatory also gets interactive scale controls is [OQ-LD4](#OQ-LD4).

---

## 4. Changing allocations on a running loop

Allocations have two halves: **routing** (which seats go to which backends, models, fallback chains, and proportional shares) and **concurrency limits** (how many simultaneous lanes a backend, a seat, or a kind of work may hold).

### 4.1 Live seat-to-backend and model routing

1. **Switching or editing the backend policy without restarting:**
   - Editing the active policy file in place continues to reload automatically on the next pass via SHA-256 check (`refreshPolicy` in [`tools/tillr-drain/drain/policy_reload.go`](../../tools/tillr-drain/drain/policy_reload.go), `59211351a`).
   - A new CLI face `tillr drain policy set <path>` updates `policy_path` in `drain_runtime_config` (replacing the undocumented `.tillr/loop/backend-policy.json` file convention with an explicit, inspected store record; if `.tillr/loop/backend-policy.json` exists on upgrade, startup migrates it once and warns). `tillr drain policy show` prints the active policy path, its SHA-256, resolved seats, shares, and caps.
   - **Validation before activation:** both `tillr drain policy set <path>` (synchronously, exiting non-zero on invalid JSON or undeclared backends) and `refreshPolicy` (asynchronously on file edits, keeping the previous valid `Policy` in force and recording a `drain.policy-refused` event surfaced in `tillr drain status` and the Observatory) refuse broken policies before they can crash or stall the loop.
2. **Effect on in-flight runs and warm sessions:**
   - In-flight runs never change backend or model mid-run; routing applies at each new claim (`selectSeatDispatch`).
   - If a worker run finishes with a gate refusal after its seat's backend or model changed, `workerSessionReason` in [`tools/tillr-drain/drain/worker_session.go`](../../tools/tillr-drain/drain/worker_session.go) already returns `"seat-changed"` when `s.Backend != req.Backend || s.Route != req.Route()`, cleanly starting a fresh worker on the newly configured backend/model on the same branch head rather than resuming the old backend's session.
   - Multi-step designer sessions ([`tools/tillr-drain/drain/comments.go`](../../tools/tillr-drain/drain/comments.go) and [`tools/tillr-drain/drain/session.go`](../../tools/tillr-drain/drain/session.go)) already scope lookup by `backend`, so switching `design` from `claude-designer` to `pi-designer` starts or resumes a `pi-designer` session cleanly without corrupting the Claude session transcript.
3. **Fixing `seat_shares` reload inertia:**
   - Today [`tools/tillr-drain/drain/seat_shares.go`](../../tools/tillr-drain/drain/seat_shares.go) sums `seat_dispatches` since the beginning of the database. After 1,000 lifetime dispatches on `codex`, changing `build` `seat_shares` from `{"codex": 100}` to `{"codex": 50, "claude-worker": 50}` routes the next **1,000 consecutive build runs** exclusively to `claude-worker` to equalize all-time totals.
   - **Fix:** record a `seat_share_epochs(seat TEXT PRIMARY KEY, since_seq INTEGER NOT NULL, shares_json TEXT NOT NULL)` row whenever a seat's normalized `seat_shares` map changes (on startup or live reload), and query `SELECT backend, count(*) FROM seat_dispatches WHERE seat=? AND rowid > ? GROUP BY backend`. All historical `seat_dispatches` rows stay intact for audit, while a live ratio change takes effect cleanly from the very next dispatch.

### 4.2 Concurrency limits per backend, per seat, and per kind of work

In [the graph design](./tillr-graph-is-the-path.md#6-scheduling), capacity limits are scheduling constraints: **a concurrency limit never fails, cancels, or rewrites an item; it only holds new claims until an active holder finishes.**

Three optional cap maps govern simultaneous active dispatches (`0` pauses new dispatches on that key while active ones drain out; omitting a key or setting `null` means uncapped up to total lane capacity):

| Cap map | Keys | Example | What counts against the cap |
| :--- | :--- | :--- | :--- |
| `backend_caps` | Declared backend names in `backends` | `{"claude-designer": 2, "claude-reviewer": 2, "codex": 12}` | Every active agent run (ordinary, express, and parallel fan-out sub-runs) currently executing on that backend |
| `seat_caps` | Declared seat names in `seats` | `{"design": 3, "scout": 8, "build": 14}` | Every active run dispatched under that logical seat across all backends |
| `work_caps` | Workflow names (`<name>@v<N>`) or built-in work kinds (`build`, `fixer`, `read-only`, `design`, `triage`) | `{"scouted-design@v1": 2, "fixer": 1, "read-only": 8}` | Every active item/run of that workflow or work class |

Where these caps are authored and overridden (`backend-policy.json` baseline plus live store overrides via `tillr drain alloc`, store-only, or policy-file-only) is [OQ-LD3](#OQ-LD3). Whether hitting a `backend_caps` limit waits for that backend or spills over to the seat's next fallback backend is [OQ-LD2](#OQ-LD2).

### 4.3 Four scheduling invariants required so caps work cleanly

Tracing [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go), [`tools/tillr-drain/drain/policy.go`](../../tools/tillr-drain/drain/policy.go), and [`tools/tillr-drain/drain/parallel.go`](../../tools/tillr-drain/drain/parallel.go) surfaces four subtle failure modes that the implementation must enforce from day one:

1. **In-pass claim accounting.** `Plan()` builds decisions for every idle lane in one pass before any new subprocess spawns. Active counts per `(backend, seat, work_kind)` must be seeded from in-flight runs at pass start and **incremented immediately inside `Plan()` as each decision is emitted**; otherwise four idle lanes in one pass all observe `active == 1 < 2` and launch five concurrent runs on a backend capped at 2.
2. **Parallel fan-out accounting (`parallel.go`).** A parallel workflow step (such as the four scouts in `scouted-design@v1`) runs on one lane but spawns `K` concurrent `tillr-run` child processes in `runParallelNode`. Backend and seat caps must count each live child sub-run (acquiring slots from the loop's live concurrency tracker before spawning each child in the `parallel` step, queueing remaining children of that node until a slot opens, and always allowing at least 1 child when `cap >= 1`), so two `scouted-design@v1` items cannot put eight simultaneous processes onto a backend capped at 2.
3. **Continuation priority over fresh starts (preventing lane-pinning deadlock).** Suppose 16 lanes are pinned to multi-step workflows whose next step needs seat `build` (`seat_caps.build = 4`), or 2 items hold lanes waiting for `claude-designer`. `Plan()` already evaluates workflow continuations (`planWorkflowStep`) and refusal continuations before fresh ready items; cap accounting must preserve that exact order so fresh items never consume the last backend/seat cap slot needed by an item already holding a lane.
4. **Graceful cap lowering and distinct wait reasons.** Lowering `claude-designer` from 4 to 2 while 3 runs are active leaves all 3 running and blocks new claims (`3/2, draining to cap`). Items held by a cap report `backend-capped` (`claude-designer: 2/2, held by run-a, run-b`), `seat-capped`, or `work-capped` in `tillr drain why` and the Observatory — never `backend-unavailable`, which means quota exhaustion.

---

## 5. Where running configuration lives, restart persistence, and reading current values

### 5.1 Canonical storage in `.tillr/state.db`

All mutable runtime configuration lives in `.tillr/state.db` in one typed table paired with append-only audit events:

```sql
CREATE TABLE IF NOT EXISTS drain_runtime_config (
  key        TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  source     TEXT NOT NULL CHECK (source IN ('operator', 'migrated')),
  updated_at TEXT NOT NULL,
  updated_by TEXT NOT NULL
);
```

Keys stored in `drain_runtime_config`:

- `ordinary_lanes`: integer `>= 0`
- `express_lanes`: integer `>= 0`
- `policy_path`: string path (relative to repo root or absolute)
- `backend_caps`, `seat_caps`, `work_caps`: JSON maps when operator-set (under [OQ-LD3](#OQ-LD3)'s store or hybrid options)

Every mutation via `tillr drain scale`, `tillr drain policy set`, `tillr drain alloc`, or `tillr drain config --reset` runs inside `db.WriteTx` and appends a `drain.config-changed` event recording `{key, previous, value, source, updated_by, loop_present}`. Because `waitForTrigger` already wakes on new store events (`100ms` poll), the running loop picks up every change with no IPC socket, signal hazard, or tracked-file edit.

### 5.2 Precedence across startups, `tillr-drain restart`, and `tillr-keep`

To satisfy the requirement that **a restart keeps the last value set** across `tillr-drain restart` (`syscall.Exec`), `tillr-keep` crash/exit recovery, and manual restarts, every runtime-configurable setting resolves in this exact order:

| Priority | Source | When it wins |
| :--- | :--- | :--- |
| **1 (highest)** | `drain_runtime_config` (`source = 'operator'`) in `.tillr/state.db` | Always, once set by `tillr drain scale` / `policy set` / `alloc`, until cleared by `tillr drain config --reset [<key>]`. Survives `tillr-drain restart` (`syscall.Exec` where `os.Args` still holds `--lanes 16`) and `tillr-keep` relaunches. |
| **2** | Explicit startup CLI flags on `tillr-drain loop` (`--lanes`, `--policy`, etc.) | Seed value when `drain_runtime_config` has no operator row for that key (including every fresh store and unit test fixture). |
| **3** | `.tillr.toml` repository settings ([`tools/tillr/settings/settings.go`](../../tools/tillr/settings/settings.go)) | Seed and live-reloaded value for settings not overridden in `drain_runtime_config` or startup CLI flags. |
| **4 (lowest)** | Compiled defaults | `--lanes 2`, `express = 0`, etc. |

Two companion adjustments close the keeper gap found in [`tools/tillr-roll/tillr-keep`](../../tools/tillr-roll/tillr-keep):

- Today `tillr-keep --status` scrapes `/proc/<pid>/cmdline` (`extract_policy_and_lanes`) and prints the boot-time `--lanes` and `--policy` flags, which goes stale the moment `tillr drain scale` or `tillr drain policy set` runs. `tillr-keep` is updated to read effective values from `tillr-drain config --json` (falling back to `.tillr/keeper.launch.json` only if the binary fails), and `tillr drain scale` / `policy set` also refreshes `.tillr/keeper.launch.json` when present so human inspection of that file never misleads.
- If an operator deliberately wants to clear runtime overrides back to `.tillr.toml` / startup flags, `tillr drain config --reset [lanes|express|policy|alloc|all]` deletes the corresponding key(s) from `drain_runtime_config` and logs `drain.config-changed`.

### 5.3 How an operator reads the current value

One dedicated read-only face, `tillr drain config [--json]` (also summarized in `tillr drain status`, `tillr view status`, and the Observatory header), prints every runtime setting, its effective value, any in-progress transition, and where the value came from:

```text
$ tillr drain config
ordinary_lanes   20        source: operator (2026-11-19T16:04:12Z via tillr drain scale)
                           status: 20 active, 0 draining (startup flag --lanes=16)
express_lanes    2         source: .tillr.toml ([lanes] express)
                           status: 2 active, 0 draining
policy_path      config/tillr/backend-policy.json (sha256: 9f3c1a2e…, loaded 2026-11-19T16:01:00Z)
                           source: flag --policy
backend_caps     claude-designer=2 (2/2 active), claude-reviewer=2 (0/2 active)
                           source: operator override on claude-designer; policy file on claude-reviewer
seat_caps        (none)
work_caps        fixer=1 (0/1 active)
                           source: policy file
settings_file    .tillr.toml (sha256: 41ab809d…, auto-reloaded)
```

---

## 6. Survey of everything fixed at loop start today

Every flag, `.tillr.toml` setting, `gate-policy.json` key, auxiliary config file, environment variable, keeper option, and binary read by the drain stack at `84474e8a1` was audited against [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go), [`tools/tillr/settings/settings.go`](../../tools/tillr/settings/settings.go), [`tools/tillr-drain/redcap/policy.go`](../../tools/tillr-drain/redcap/policy.go), [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/), [`tools/tillr-run/`](../../tools/tillr-run/), [`tools/tillr-land/`](../../tools/tillr-land/), and [`tools/tillr-roll/`](../../tools/tillr-roll/).

### 6.1 `tillr-drain loop` CLI flags (all 21 flags of `loopFace`)

| Flag | Today | Recommendation | Reason |
| :--- | :--- | :--- | :--- |
| `--lanes <N>` | Read once at start (`LoopConfig.Lanes`, default `2`) | **Changeable while running** (`tillr drain scale --lanes N [--pause]`) | Primary capacity knob; stages new lanes on increase and drains/retires excess slots gracefully by default, with opt-in `--pause` for fast session-pause reclaim ([Section 3](#3-live-lane-scaling-and-graceful-slot-retirement)). |
| `--policy <path>` | Path fixed at start; file contents reloaded per pass (`59211351a`) | **Changeable while running** (`tillr drain policy set <path>` + content auto-reload) | Switching between policy profiles (such as Bedrock fallback vs subscription-only) only swaps the `Policy` struct consulted at `selectSeatDispatch` ([Section 4.1](#41-live-seat-to-backend-and-model-routing)). |
| `--backoff-base <dur>` | Read once at start (`DefaultBackoffBase`) | **Changeable while running** | Only sets the initial infrastructure-failure retry wait (doubling up to `5m`); updating between passes has zero state hazard. |
| `--brief-template <path>` | Path read once at start; template file read per staged run | **Changeable while running** (contents already live; path reloadable) | Staging renders each run's brief from the template file on disk; updating the path between passes affects only newly staged runs. |
| `--gate <cmd>`, `--land <cmd>`, `--verdict <cmd>` | Read once at start (when store charter `storeHooks` does not supply them) | **Changeable while running** (when flag-driven; charter hooks stay Charter-owned) | Invoked as fresh subprocesses per candidate judgment or landing; in-flight steps finish with the command they started with. |
| `--replay <cmd>` | Read once at start (`DefaultReplayProgram`) | **Changeable while running** | Subprocess run on base SHA to acquit trunk-red gate failures; safe to swap between passes. |
| `--heavy-gate <bin>` | Read once at start (default `"tillr-gate"`) | **Changeable while running** | Binary name invoked for after-merge heavy proof runs ([`tools/tillr-drain/drain/heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go)); safe to update when no heavy run is mid-spawn. |
| `--rolling <cmd>` | Read once at start (`DefaultRollingProgram`; disabling refused by policy) | **Changeable while running** (non-empty program only) | Subprocess command run for the post-merge rolling tier over `{since}`; refusal of empty/disabling values enforced by [`tests/tillr-docs.test.py`](../../tests/tillr-docs.test.py) stays in force. |
| `--fixer-gate <cmd>` | Read once at start (`DefaultFixerGateProgram`) | **Changeable while running** | Subprocess command handed a fixer candidate in place of `--gate`; affects only future fixer gate runs. |
| `--questions-cmd <cmd>` | Read once at start | **Changeable while running** | Optional hook command for owner-question projection; stateless across passes. |
| `--run-bin <bin>`, `--item-bin <bin>` | Read once at start (defaults `"tillr-run"`, `"tillr-item"`) | **Restart required** | Select the core suite binaries executed for runs and item-state transitions; in practice deployed atomically with `tillr-drain` via `tillr-roll` + `tillr-drain restart`. |
| `--root <dir>` | Read once at start (required) | **Restart required** | Anchors the git repository, trunk checkout, `.tillr/` state directory, and loop presence lease `.present`. Moving `--root` is a different drain instance. |
| `--store <path>` | Read once at start (default `<root>/.tillr/state.db`) | **Restart required** | Opens the SQLite database, event cache (`CacheCommittedEvents`), and store charter hooks (`storeHooks`) that identify the queue. |
| `--lane-root <dir>` | Read once at start (default `<root>/.tillr/lanes`) | **Restart required** | Base directory for all staged git worktrees and run presence leases; moving it while workers run invalidates paths and `.git/worktrees` links. |
| `--exit-when-drained` | Startup daemon vs drain-and-exit mode | **Restart required** | Controls process lifetime semantics for bounded scripted invocations versus `tillr-keep` daemon operation (`--lanes 0` covers live draining without exiting). |
| `--check`, `--migrate-owner-questions` | One-shot validation / migration modes that exit before the loop | **Restart required (not applicable)** | Both flags exit before starting a pass; there is no running loop to reconfigure. |
| `--now <rfc3339>` | Injected deterministic test clock | **Restart required (test-only)** | Deterministic test harness clock fixture; production loops always read the wall clock per tick. |

### 6.2 `.tillr.toml` repository settings (all 23 keys across 9 tables in `settings.go`) and `gate-policy.json`

[`tools/tillr/settings/settings.go`](../../tools/tillr/settings/settings.go) (`d327cf079`) and [`tools/tillr-drain/redcap/policy.go`](../../tools/tillr-drain/redcap/policy.go) (`config/tillr/gate-policy.json`) are read once in `main.go` today. **Every key in `.tillr.toml` and `gate-policy.json` is safe to live-reload between passes** by hashing both files at the top of `pass()` alongside `refreshPolicy` (refusing malformed edits with a `drain.settings-refused` event while keeping the previous valid settings active, and letting `drain_runtime_config` win whenever an operator override exists):

| File, table & key(s) | Today | Recommendation | Reason |
| :--- | :--- | :--- | :--- |
| `.tillr.toml` `[lanes] express` | Read once at start | **Changeable while running** (`tillr drain scale --express M` + `.tillr.toml` reload when not operator-overridden) | Uses the exact same active/draining/retired slot reconciliation as ordinary lanes ([Section 3](#3-live-lane-scaling-and-graceful-slot-retirement)). |
| `.tillr.toml` `[sync] roadmaps` | Read once at start | **Changeable while running** | Passed to `tillr item sync` at the top of each pass; adding or removing a roadmap takes effect on the next pass without a restart. |
| `.tillr.toml` `[order] record`, `settle_minutes` | Read once at start | **Changeable while running** | Controls queue-order persistence path (`docs/tillr/queue-order.txt`) and settle window after hand reorders ([`tools/tillr-drain/orderrecord/read.go`](../../tools/tillr-drain/orderrecord/read.go)); safe to re-read per pass. |
| `.tillr.toml` `[intake] dir`, `paths` | Read once at start | **Changeable while running** | Scopes tracked idea/words directory and allowed fence paths for newly created idea items. |
| `.tillr.toml` `[prose] rewrite` | Read once at start | **Changeable while running** | Toggles whether prose-guide findings queue automatic rewrite work on future syncs. |
| `.tillr.toml` `[workflows] default` (`[workflows.default.<seat>]`) | Read once at start | **Changeable while running** | Sets default workflow per seat only when an item initializes its graph (already-started items keep their pinned `item_graphs` row). |
| `.tillr.toml` `[roll] command`, `paths`, `pause_timeout_seconds` | Read once at start | **Changeable while running** | Read after landings that touch `roll.paths` and when waiting for gap-paused runs during a roll. |
| `.tillr.toml` `[push] what`, `remote`, `ref`, `after_landing` | Read once at start | **Changeable while running** | Controls whether `"none"`, `"tip"`, or `"green"` pushes after landings/proofs; switching `"tip"` <-> `"green"` <-> `"none"` between passes is completely stateless. |
| `.tillr.toml` `[comments] vantage_url`, `repo`, `paths`, `quiet_seconds`, `poll_seconds`, `max_running`, `compact_over_tokens`, `refresh_hours` | Read once at start | **Changeable while running** | Consulted by automatic design-comment intake and designer session compaction/rotation in [`tools/tillr-drain/drain/comments.go`](../../tools/tillr-drain/drain/comments.go) and [`tools/tillr-drain/drain/comment_intake.go`](../../tools/tillr-drain/drain/comment_intake.go). |
| `config/tillr/gate-policy.json`: `landing_strategy`, `lane_base`, `auto_revert`, `budget_ms` | Read once at start (`ReadGatePolicy`) | **Changeable while running** | `lane_base` (`"green"` vs `"tip"`) applies at next lane staging, `auto_revert` applies at next attributed red proof ([`tools/tillr-drain/drain/heavy_loop.go`](../../tools/tillr-drain/drain/heavy_loop.go)), `budget_ms` informs gate budgeting, and `landing_strategy` applies to newly gated candidates (ahead of [`OQ-GP7`](./tillr-graph-is-the-path.md#OQ-GP7)'s input-disjointness build). |

### 6.3 Auxiliary config files, workflows, and templates

| File / directory | Today | Recommendation | Reason |
| :--- | :--- | :--- | :--- |
| `config/tillr/backend-policy*.json` | Contents reloaded per pass (`59211351a`); path fixed | **Changeable while running** (both contents and active path, plus epoch-scoped `seat_shares` and new caps) | Covered in [Section 4](#4-changing-allocations-on-a-running-loop). |
| `tools/gate/policy.json` & `tools/gate/checks/*.json` | Re-read on every gate subprocess invocation | **Already changeable while running** | Each candidate gate and rolling suite spawns a fresh gate process that reads the current check declarations and budgets. |
| `config/tillr/commands.json` | Re-read by `tillr-loop.py` on every gate/land hook | **Already changeable while running** | External hook script reads it per invocation. |
| `config/tillr/workflows/*.json` | Read per item when `ResolveWorkflow` initializes an item's graph | **Already changeable while running** | New items pick up edited workflows immediately; in-flight items safely finish the frozen graph snapshot stored in `item_graphs`. |
| `tools/tillr-run/backends/brief-template.md` & backend adapter scripts (`backends/pi/*`, `backends/claude/*`, `backends/codex/*`) | Read / executed per `tillr-run` dispatch | **Already changeable while running** | Read from disk on each new agent run without restarting the drain loop. |
| `config/tillr/definitions/git-landing.json` (charter) | Read once at start | **Restart required** | Governs state-machine legality and store identity; use `tillr-drain restart`. |

### 6.4 Environment variables

| Variable(s) | Read by | Recommendation | Reason |
| :--- | :--- | :--- | :--- |
| `BOARD_DB`, `XDG_STATE_HOME` | Store path resolution at startup | **Restart required** | Selects which `.tillr/state.db` the loop opens and leases. |
| `TILLR_LAUNCHER`, `TILLR_RESTART_REQUEST` | `tillr-drain` startup & `restart/restart.go` | **Restart required** | Process supervision and `syscall.Exec` handoff handshake metadata. |
| `PATH`, `HOME`, `YOLO_DATA_DIR`, `TILLR_SUBPROCESS_ENV_SCRUB` | Process environment inherited by child runs | **Restart required** | OS process environment cannot be mutated from outside a running UNIX process; changing toolchain `PATH` or credential home uses `tillr-drain restart`. |
| `TILLR_OPENAI_USAGE_URL`, `TILLR_CLAUDE_USAGE_URL`, `TILLR_CLAUDE_CREDENTIALS`, `TILLR_COPILOT_HOSTS_FILE`, `TILLR_COPILOT_USAGE_URL` | Quota probes in `tillr-run/quota/` | **Restart required** (env) / credential file contents **already live** | Test/override URLs are process env vars, whereas the OAuth/token files they point to (`~/.claude/.credentials.json`, Copilot hosts file) are re-read on each probe so refreshed tokens already work without a restart. |
| `TILLR_GIT_COMMITTER_NAME`, `TILLR_GIT_COMMITTER_EMAIL` | `tillr-land` subprocess (`trunk.go`) | **Restart required** (if passed via loop env) | Inherited from the loop's environment; git config in user/global scope is already re-read per `tillr-land` subprocess. |
| `TILLR_ROLL_REMOTE`, `TILLR_ROLL_BRANCH`, `TILLR_DISABLE_ROLLING` | `tools/tillr-roll` (invoked periodically by `tillr-keep`, not `tillr-drain`) | **Keeper restart required** (env) | Belongs to the outer `tillr-keep` supervisor, not `tillr-drain loop`. |

### 6.5 Keeper (`tillr-keep`), rolling deployer (`tillr-roll`), and compiled binaries

| Surface | Today | Recommendation | Reason |
| :--- | :--- | :--- | :--- |
| `tillr-keep` `--lanes` / `--policy` passthrough args | Passed on initial launch; re-passed on crash relaunch | **Superseded at runtime by `drain_runtime_config`** | Once set via `tillr drain scale` / `policy set`, stored operator values take precedence across keeper relaunches and `tillr-keep --status` reads `tillr-drain config --json` ([Section 5.2](#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep)). |
| `tillr-keep` supervisor flags (`--interval`, `--max-crash-streak`, `--cooldown`, `--no-roll`, `--root`) | Read once by `tillr-keep` Python supervisor | **Keeper restart required** (which leaves `tillr-drain` and workers running if desired, or takes <1 s) | Outer watchdog loop settings; rarely changed, and separate from `tillr-drain loop`. |
| Compiled `tillr-drain` binary (including linked `tools/tillr`, `tools/tillr-item`, and `tools/tillr-run` Go packages) | Loaded in process memory until `tillr-drain restart` | **Restart required (`tillr-drain restart`)** | Go compiles `tillr-drain`, `tillr-run`, `tillr-item`, and `tillr/store` into one static executable; new code requires `syscall.Exec`. Because [graceful restart](./tillr-restart.md) gap-pauses `pi` runs and resumes them in place, binary upgrades remain the one legitimate reason to run `tillr-drain restart`. |
| Compiled `tillr-gate`, `tillr-land`, and `tillr` CLI binaries | Executed as fresh child processes per gate / landing / CLI command | **Already live on rebuild** (once `tillr-roll` installs the rebuilt binary) | Invoked by subprocess per check or landing, provided the wire JSON contract with `tillr-drain` did not change. |
| Compiled `tillr-view` (`tillr-view serve` Observatory server) | Separate long-lived HTTP server process | **Observatory process restart required** (independent of `tillr-drain loop`) | Restarting `tillr-view serve` never touches `tillr-drain loop` or any running lane. |

---

## 7. Decision Ledger

### Settled by the owner

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-LD1` | Ordinary and express lane counts can be changed on a running drain without restarting it: raising takes effect at the next start decision, lowering is graceful so excess lanes finish in-flight work before retiring without killing anything, and the views show lanes that are draining out. | 2026-11-19 | [Section 3](#3-live-lane-scaling-and-graceful-slot-retirement) | — |
| `D-LD2` | Allocations — both seat-to-backend/model routing and concurrency caps on how many lanes a backend, a seat, or a kind of work may hold — can be changed on a running drain without restarting it. | 2026-11-19 | [Section 4](#4-changing-allocations-on-a-running-loop) | — |
| `D-LD3` | Running configuration persists across loop restarts (`tillr-drain restart` and `tillr-keep` relaunches) so a restart keeps the last value set, and operators have a direct read surface showing current effective values and their sources. | 2026-11-19 | [Section 5](#5-where-running-configuration-lives-restart-persistence-and-reading-current-values) | — |
| <a id="OQ-LD1"></a>[`OQ-LD1`](#OQ-LD1) | **Owner ruling:** Lowering lanes is graceful by default so normal scaling never interrupts work in flight, and `tillr drain scale` adds an opt-in `--pause` flag that asks excess running workers above the new target (newest-started first) to pause via session pause instead of killing processes, holding paused segments until pool running concurrency drops below the target. Ruled on 2026-11-19 (~6:45 PM ET): *"B — graceful by default, plus an opt-in --pause flag: normal scaling never interrupts work, while --pause restores the retired board's fast resource reclaim using session pause instead of killing processes."* | 2026-11-19 | [Section 3.1](#31-operator-command-surface), [Section 3.4](#34-removing-lanes-gracefully-target--active) | — |

### The designer's decisions, awaiting the owner's review

Each `D-LD` row below is a decision the designer made on 2026-11-19 from the code and git-history evidence above. None is the owner's ruling, the owner may overturn any of them, and nothing is built before the open questions settle.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-LD4` | Add `lanes.lifecycle IN ('active', 'draining', 'retired')` in `.tillr/state.db`; never delete `lanes` rows, keep clean `retired` worktrees dormant on disk, retire idle excess slots immediately, and hold `draining` slots until all seven worktree/workflow/candidate/lease preconditions clear. **Evidence:** `runs.lane_id` and `read_only_runs.lane_id` foreign keys forbid row deletion; live `git worktree remove` contends with `land.lock` and `boundary.go`; retiring mid-workflow strands staged node state. **Rejected:** deleting `lanes` rows; retiring between nodes of an active workflow; removing worktrees inside the pass loop. The owner may overturn this. | 2026-11-19 | [Section 3.2](#32-slot-lifecycle-in-tillrstatedb), [Section 3.4](#34-removing-lanes-gracefully-target--active) | — |
| `D-LD5` | Allow `--lanes 0` and `--express 0` (overturning `board`'s `minimum 1` rule), and replace `LoopConfig`'s fixed-size `completions` channel with an unbounded mutex-backed completion queue + wake channel. **Evidence:** `0` ordinary lanes is the clean way to drain ordinary work to idle without stopping rolling checks, landings, or express items; fixed channel buffer `len(cfg.Lanes)` deadlocks or blocks workers when scaling up. **Rejected:** refusing `0`; keeping a fixed buffer size. The owner may overturn this. | 2026-11-19 | [Section 3.1](#31-operator-command-surface), [Section 3.3](#33-adding-lanes-target--active_or_draining) | — |
| `D-LD6` | Canonical runtime overrides live in `drain_runtime_config` in `.tillr/state.db` (paired with `drain.config-changed` events), work whether the loop is running or stopped, and take precedence over `tillr-drain loop` CLI startup flags and `.tillr.toml` across `syscall.Exec` restarts and `tillr-keep` relaunches until cleared by `tillr drain config --reset`. `tillr-keep` reads `tillr-drain config --json` instead of scraping `/proc/<pid>/cmdline`. **Evidence:** `restart/restart.go` and `tillr-keep` re-pass the boot-time `os.Args`, which would silently overwrite live changes on every restart if CLI flags beat the store. **Rejected:** mutating tracked files in `/workspace`; letting boot-time `--lanes` overwrite stored operator changes on restart. The owner may overturn this. | 2026-11-19 | [Section 5.1](#51-canonical-storage-in-tillrstatedb), [Section 5.2](#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep) | — |
| `D-LD7` | Scope `seat_shares` deficit counts to a per-seat epoch recorded in `.tillr/state.db` whenever that seat's `seat_shares` map changes, rather than counting all-time rows in `seat_dispatches`. **Evidence:** [`tools/tillr-drain/drain/seat_shares.go`](../../tools/tillr-drain/drain/seat_shares.go) currently sums all-time rows, causing multi-hundred-dispatch starvation on live ratio edits. **Rejected:** deleting `seat_dispatches` history; leaving all-time cumulative counting in place. The owner may overturn this. | 2026-11-19 | [Section 4.1](#41-live-seat-to-backend-and-model-routing) | — |
| `D-LD8` | Concurrency caps (`backend_caps`, `seat_caps`, `work_caps`) increment in-pass claimed counts inside `Plan()`, count parallel fan-out child runs in `parallel.go`, evaluate lane-holding continuations ahead of fresh item starts, and emit distinct `backend-capped` / `seat-capped` / `work-capped` wait reasons. **Evidence:** multi-lane single-pass dispatch and 4-way scout fan-out otherwise overshoot caps, and fresh starts could otherwise deadlock lanes pinned by workflows. **Rejected:** post-spawn-only counting; ignoring parallel scout children; reusing `backend-unavailable`. The owner may overturn this. | 2026-11-19 | [Section 4.3](#43-four-scheduling-invariants-required-so-caps-work-cleanly) | — |
| `D-LD9` | Auto-reload `.tillr.toml` (all 23 keys across 9 tables) and `config/tillr/gate-policy.json` (all 4 keys) between passes on SHA-256 change, plus allow live updates to `--backoff-base`, `--brief-template`, `--gate`, `--land`, `--verdict`, `--replay`, `--heavy-gate`, `--rolling` (non-empty), `--fixer-gate`, and `--questions-cmd`, refusing invalid edits with `drain.settings-refused` while keeping the last valid config; keep `--root`, `--store`, `--lane-root`, `--run-bin`, `--item-bin`, `--exit-when-drained`, the store's charter (`git-landing.json`), env vars, and compiled `tillr-drain` code as restart-required ([Section 6](#6-survey-of-everything-fixed-at-loop-start-today)). **Evidence:** pass-boundary reload matches `refreshPolicy` (`59211351a`), while locator/charter/binary changes alter open store handles, worktree roots, or linked Go code. **Rejected:** restarting for timeout/push/comment/workflow/gate-policy edits; live-swapping `--store` or the store charter mid-process. The owner may overturn this, row by row. | 2026-11-19 | [Section 6](#6-survey-of-everything-fixed-at-loop-start-today) | — |

---

## Open Questions

1. 💬 **OQ-LD2: When a backend reaches its concurrent-lane cap, should extra work wait for that backend or spill over to the seat's next fallback backend?**

   Suppose your `design` seat lists your flat-rate Claude subscription first and paid per-token AWS Bedrock second (`["claude-designer", "pi-designer"]`). You cap `claude-designer` at 2 concurrent lanes so bursts do not trip rate limits, and a third design item becomes ready while two are running on Claude and Claude's quota window is still open. Today fallback to the second backend happens only when the first backend's quota window closes.

   - **A — Wait for the capped backend.** The third design item waits in the queue (`backend-capped` on `claude-designer`) until one of the two finishes, and only moves to Bedrock if Claude's quota window closes. Your subscription covers the whole burst for $0, at the cost of the third item waiting.
   - **B — Spill over to the next backend in the seat's list.** The third design item starts right away on `pi-designer` while `claude-designer` stays at 2. Ready work never waits behind a cap when a fallback backend is open, at the cost of every burst above 2 spending per-token Bedrock dollars.
   - **C — Wait by default, with a per-seat spillover switch in the backend policy.** Subscription-first seats like `design` and `review` wait for the preferred backend by default, while seats where speed matters or both backends are paid (such as `build` or `scout`) can opt in to spill over onto the next backend.

   <!-- vantage: question id=OQ-LD2 leaning="C — wait by default, with a per-seat spillover switch: capping a flat-rate subscription backend should not silently push bursts onto per-token Bedrock spend unless that seat opted in." -->

   _Leaning:_ C — wait by default, with a per-seat spillover switch: capping a flat-rate subscription backend should not silently push bursts onto per-token Bedrock spend unless that seat opted in.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-LD3: Where should concurrency caps on backends, seats, and kinds of work live — in the tracked backend policy file with live store overrides, only in the store, or only in the policy file?**

   You have two kinds of allocation changes: standing rules you want on every checkout (such as keeping `claude-designer` at 2 lanes or `fixer` at 1 lane), and temporary operational throttles on one machine (such as capping `build` at 4 lanes for an hour while running benchmarks). The drain already reloads `config/tillr/backend-policy.json` from disk, but editing a tracked file for a temporary throttle dirties the main checkout where the lander advances `main`.

   - **A — Baseline caps in `backend-policy.json`, with live CLI overrides in `.tillr/state.db`.** Standing caps are versioned in git beside the seats and backends they govern, and `tillr drain alloc` sets or clears temporary per-checkout overrides in `.tillr/state.db` without dirtying git; `tillr drain config` prints both and marks which one is active. Two sources exist, reconciled in one status view.
   - **B — Only in `.tillr/state.db`, set by CLI commands.** Git stays clean on every change and caps live in only one place, at the cost that standing limits like `claude-designer = 2` are not tracked in git and must be re-applied on a fresh store or second machine.
   - **C — Only in `backend-policy.json` (plus switching files via `tillr drain policy set`).** Every routing rule and cap lives in one JSON file with no override layer, at the cost that a temporary throttle either dirties the tracked policy file or requires maintaining a local untracked copy.

   <!-- vantage: question id=OQ-LD3 leaning="A — baseline caps in backend-policy.json with live overrides in .tillr/state.db: standing subscription protections belong in git, while one-hour machine throttles must never dirty the trunk checkout." -->

   _Leaning:_ A — baseline caps in `backend-policy.json` with live overrides in `.tillr/state.db`: standing subscription protections belong in git, while one-hour machine throttles must never dirty the trunk checkout.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-LD4: Should the observatory web page include controls to change lane counts and caps, or only display them while CLI commands make changes?**

   When you open the observatory in your browser (`tillr view serve`), the header and lane strip will show the current target, active lanes, any lanes draining out, and any capped backends. The observatory already has two write actions (reordering queue items and saving a new idea) and runs on localhost with no login ([`OQ-PR16`](../tillr/decision-provenance-review.md#OQ-PR16) asks whether unauthenticated local web writes stay).

   - **A — CLI mutations only; read-only display in the observatory.** You change lane counts and allocations with `tillr drain scale`, `tillr drain policy`, and `tillr drain alloc` in a terminal, and the observatory displays targets, draining lanes, and active caps with zero web endpoints that can alter machine load or spend.
   - **B — Both CLI commands and controls in the observatory.** Clicking the lane summary in the observatory opens a small control to adjust ordinary and express lane counts (writing the same store record as the CLI), subject to whatever write-access rule you set for the observatory.

   <!-- vantage: question id=OQ-LD4 leaning="A — CLI mutations first, with read-only display in the observatory: one CLI command solves live scaling immediately without expanding unauthenticated browser writes, and a web control can be layered onto the same store record later if wanted." -->

   _Leaning:_ A — CLI mutations first, with read-only display in the observatory: one CLI command solves live scaling immediately without expanding unauthenticated browser writes, and a web control can be layered onto the same store record later if wanted.

   **Answer:**
   > _(empty — fill in when decided)_
