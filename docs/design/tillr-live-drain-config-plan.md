---
title: "Live Drain Capacity and Allocation Changes — Plan Seed"
status: draft
stage: SKETCH
updated: 2026-11-24
next: "Complete this seed into build slices against tillr-live-drain-config.md; ordinary-lane scaling and fresh-start --lanes replacement are in the tree, everything else below is not"
depends-on:
  - ./tillr-live-drain-config.md#OQ-LD1
  - ./tillr-live-drain-config.md#OQ-LD2
  - ./tillr-live-drain-config.md#OQ-LD3
  - ./tillr-live-drain-config.md#OQ-LD4
related:
  - ./tillr-live-drain-config.md
  - ../reference/tillr-restart.md
  - ./tillr-vocabulary.md
---

# Live Drain Capacity and Allocation Changes — Plan Seed

**Status:** a seed, not a hand-off: it holds what the design work surfaced and is incomplete until a planner reads the tree. [OQ-LD1](./tillr-live-drain-config.md#OQ-LD1) to [OQ-LD4](./tillr-live-drain-config.md#OQ-LD4) are ruled.

Seed for the implementation plan of [tillr-live-drain-config.md](./tillr-live-drain-config.md). On any conflict about behavior, the design wins.

**In the tree today:**

- `tillr drain scale --lanes` changes the ordinary lane count of a running loop, stores the target under the `ordinary_lanes` key of the `drain_runtime_config` table, and records a `drain.config-changed` event ([`live_scale.go`](../../tools/tillr-drain/drain/live_scale.go)).
- An explicit `--lanes` on a fresh `tillr drain loop` start replaces the saved target (`ExplicitLanes` in [`main.go`](../../tools/tillr-drain/main.go); `D-LD10`).
- [`tillr-keep`](../../tools/tillr-roll/tillr-keep) reads the saved ordinary lane target from the store.

**Not in the tree:** a `config` read face, an `alloc` face, express-lane scaling, a per-lane lifecycle (`active`, `draining`, `retired`), concurrency caps, `seat_shares` epochs, and reload of `.tillr.toml` and `gate-policy.json` between passes. `rg -n 'backend_caps|seat_share_epochs|settings-refused' tools -g '*.go'` prints nothing.

---

## Proposed build phases

The phases are ordered so each slice compiles and passes its module's suite on the trunk, and so independent slices touch different files.

| Phase | Scope and target files | Rests on | Done when |
| :--- | :--- | :--- | :--- |
| **1. Runtime config store, precedence and keeper sync** | [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) and [`tools/tillr-drain/drain/schema.go`](../../tools/tillr-drain/drain/schema.go) (the lane lifecycle column and `seat_shares` epochs), [`live_scale.go`](../../tools/tillr-drain/drain/live_scale.go), [`loop.go`](../../tools/tillr-drain/drain/loop.go), [`main.go`](../../tools/tillr-drain/main.go) (a `config` read and reset face, and the lane count on the start line), [`tillr-keep`](../../tools/tillr-roll/tillr-keep) (read every runtime value from the store, and omit the boot-time `--lanes` on a crash relaunch) | `D-LD3`, `D-LD4`, `D-LD6`, `D-LD10` | Runtime values persist across an in-place `tillr drain restart`, an unflagged start and a `tillr-keep` crash relaunch; `tillr drain config [--json]` prints each effective value and its source |
| **2. Lane scaling and graceful slot retirement** | [`main.go`](../../tools/tillr-drain/main.go) (the `scale` face), [`loop.go`](../../tools/tillr-drain/drain/loop.go) (an unbounded completion queue and slot reconciliation at the start of a pass), [`stage.go`](../../tools/tillr-drain/drain/stage.go), [`lane_base.go`](../../tools/tillr-drain/drain/lane_base.go), [`plan.go`](../../tools/tillr-drain/drain/plan.go) (eligibility by lifecycle, and the retirement preconditions the design lists), [`why.go`](../../tools/tillr-drain/why.go) | [OQ-LD1](./tillr-live-drain-config.md#OQ-LD1) | Scaling ordinary or express lanes up stages or reactivates slots on the next pass; scaling down retires idle slots at once and holds busy slots in `draining` until every precondition clears, without deleting `lanes` rows or ending runs; `0` is allowed; a re-increase flips `draining` slots back to `active` first |
| **3. Draining lanes in the view and the Observatory** | [`tools/tillr-view/lanes.go`](../../tools/tillr-view/lanes.go), [`serveread.go`](../../tools/tillr-view/serveread.go), [`LaneStrip.tsx`](../../tools/tillr-view/web/src/components/LaneStrip.tsx), and the header and status components and tests under [`tools/tillr-view/`](../../tools/tillr-view/) | [OQ-LD4](./tillr-live-drain-config.md#OQ-LD4) | `retired` rows do not count toward the lane totals; `tillr view status`, `tillr view lanes` and the Observatory show target counts, active counts, and each `draining` lane with why it is still finishing |
| **4. Policy switching, `seat_shares` epochs and concurrency caps** | [`policy.go`](../../tools/tillr-drain/drain/policy.go), [`policy_reload.go`](../../tools/tillr-drain/drain/policy_reload.go), [`seat_shares.go`](../../tools/tillr-drain/drain/seat_shares.go), [`plan.go`](../../tools/tillr-drain/drain/plan.go), [`parallel.go`](../../tools/tillr-drain/drain/parallel.go), [`main.go`](../../tools/tillr-drain/main.go) (the `policy set` and `alloc` faces) | [OQ-LD2](./tillr-live-drain-config.md#OQ-LD2), [OQ-LD3](./tillr-live-drain-config.md#OQ-LD3) | `tillr drain policy set <path>` swaps the active policy and refuses an invalid one; a `seat_shares` change counts from a fresh epoch; `backend_caps`, `seat_caps` and `work_caps` count claims made within a pass and parallel child runs, and `tillr drain why` names `backend-capped`, `seat-capped` or `work-capped` |
| **5. Reload of `.tillr.toml` and `gate-policy.json` between passes, and operating documents** | [`settings.go`](../../tools/tillr/settings/settings.go), [`redcap/policy.go`](../../tools/tillr-drain/redcap/policy.go), [`loop.go`](../../tools/tillr-drain/drain/loop.go), [the drain README](../../tools/tillr-drain/README.md), [the view README](../../tools/tillr-view/README.md) and the `tillr-operating` skill | `D-LD9` | Editing `.tillr.toml` or `config/tillr/gate-policy.json` takes effect at the next pass, an invalid file is refused with `drain.settings-refused` and the last valid values stay in force, and every new face answers `--help` |

---

## Checks for the planner

- **Keep slot reconciliation behind one narrow interface** in `tools/tillr-drain/drain/`. When [item-owned worktrees](./tillr-vocabulary.md#3-what-a-lane-was-and-the-worktree-as-a-resource) replace numbered lanes, the runtime config table and the `scale`, `alloc` and `config` faces should carry over to the running-agent limit without a schema change.
- **Read-only faces open stores that were never migrated.** [`lanestate/read.go`](../../tools/tillr-drain/lanestate/read.go), [`lanes.go`](../../tools/tillr-view/lanes.go) and [`serveread.go`](../../tools/tillr-view/serveread.go) open a store read-only and run no migration, so a query on a new column or table is guarded by schema introspection, and a store without the lifecycle column reads as every lane `active`.
- **Phases 2 and 4 share files.** Both change [`plan.go`](../../tools/tillr-drain/drain/plan.go) and [`main.go`](../../tools/tillr-drain/main.go), so phase 4 follows phase 2. Phase 3 can run beside either once phase 1's schema is in.
- **A documented face must exist.** `case_faces_answer_help` in [`tests/tillr-docs.test.py`](../../tests/tillr-docs.test.py) runs each documented `tillr drain <face>` with `--help`, so a slice that documents a new face ships with, or after, the face itself.
