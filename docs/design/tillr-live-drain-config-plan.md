---
title: "Live Drain Capacity and Allocation Changes — Plan Seed"
status: draft
stage: SKETCH
updated: 2026-11-23
next: "Complete remaining build slices against tillr-live-drain-config.md (including D-LD10 fresh-start --lanes replacement and startup lane-count reporting)"
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

**Status:** 2026-11-23 — seeded during design; [OQ-LD1](./tillr-live-drain-config.md#OQ-LD1)–[OQ-LD4](./tillr-live-drain-config.md#OQ-LD4) are ruled, initial ordinary-lane scaling (`tillr drain scale --lanes` and `drain_runtime_config['ordinary_lanes']`) landed in `fef19a1fae`, and `D-LD10` (designer's decision from the 2026-11-23 operator request; the owner may overturn it) amends `D-LD6` on fresh-start `--lanes` replacement and startup lane-count reporting.

Seed for the implementation plan of [tillr-live-drain-config.md](./tillr-live-drain-config.md). On any conflict about behavior, the design wins.

---

## Proposed build phases (as surfaced during design)

The phases below are ordered so each slice compiles, passes its module test suite on `main`, and keeps non-overlapping file fences where independent slices can run in parallel.

| Phase | Scope & target files | Gated on | Done when |
| :--- | :--- | :--- | :--- |
| **1. Runtime config store, precedence, and keeper sync** | [`tools/tillr/store/schema.go`](../../tools/tillr/store/schema.go) and [`tools/tillr-drain/drain/schema.go`](../../tools/tillr-drain/drain/schema.go) (migrations for `drain_runtime_config`, `lanes.lifecycle`, `seat_share_epochs`), [`tools/tillr-drain/drain/live_scale.go`](../../tools/tillr-drain/drain/live_scale.go), [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go), [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go) (`tillr-drain config` read/reset face + startup lane-count line and `D-LD10` fresh-start `--lanes` replacement), [`tools/tillr-roll/tillr-keep`](../../tools/tillr-roll/tillr-keep) (read `tillr-drain config --json` instead of `/proc/<pid>/cmdline`, and omit boot-time `--lanes` on post-launch crash recovery). | Ledger review (`D-LD4`, `D-LD6`, `D-LD10`) | Explicit `--lanes N` on a fresh start (`TILLR_RESTART_REQUEST` unset, `--check` false) replaces any saved `ordinary_lanes` target in `drain_runtime_config` and logs `N` and its flag source on the start line (`D-LD10`); `drain_runtime_config` persists operator values across in-place `tillr-drain restart` (`syscall.Exec` carrying `TILLR_RESTART_REQUEST` with stale `--lanes` argv), unflagged starts, and post-launch `tillr-keep` crash relaunches (`D-LD3`, `D-LD6`); `tillr drain config [--json]` prints effective values and sources. |
| **2. Live lane scaling (`tillr drain scale`) and graceful slot retirement** | [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go) (`scale` face), [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (unbounded completion queue + pass-start slot reconciliation), [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go), [`tools/tillr-drain/drain/lane_base.go`](../../tools/tillr-drain/drain/lane_base.go), [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) (`active` vs `draining` vs `retired` eligibility and 7 retirement preconditions), [`tools/tillr-drain/why.go`](../../tools/tillr-drain/why.go). | [OQ-LD1](./tillr-live-drain-config.md#OQ-LD1) (`--pause` mode vs graceful-only) | Scaling ordinary or express lanes up stages/reactivates slots on the next pass (<100 ms idle wake); scaling down retires idle slots immediately, holds busy/workflow/candidate-pinned slots in `draining` until all 7 preconditions clear without deleting `lanes` rows or killing runs, allows `0`, and flips `draining -> active` first on re-increase. |
| **3. View and Observatory draining-lane surfaces** | [`tools/tillr-view/lanes.go`](../../tools/tillr-view/lanes.go), [`tools/tillr-view/serveread.go`](../../tools/tillr-view/serveread.go), [`tools/tillr-view/web/src/components/LaneStrip.tsx`](../../tools/tillr-view/web/src/components/LaneStrip.tsx), header/status components and tests under [`tools/tillr-view/`](../../tools/tillr-view/). | [OQ-LD4](./tillr-live-drain-config.md#OQ-LD4) (read-only display vs Observatory scale popover) | `retired` rows no longer inflate `lanes_total` or `lanes_offered`; `tillr view status`, `tillr view lanes`, and the Observatory `LaneStrip` show target counts, active counts, and each `draining` lane with why it is still finishing. |
| **4. Live policy switching, `seat_shares` epochs, and concurrency caps** | [`tools/tillr-drain/drain/policy.go`](../../tools/tillr-drain/drain/policy.go), [`tools/tillr-drain/drain/policy_reload.go`](../../tools/tillr-drain/drain/policy_reload.go), [`tools/tillr-drain/drain/seat_shares.go`](../../tools/tillr-drain/drain/seat_shares.go), [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go), [`tools/tillr-drain/drain/parallel.go`](../../tools/tillr-drain/drain/parallel.go), [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go) (`policy set` and `alloc` faces). | [OQ-LD2](./tillr-live-drain-config.md#OQ-LD2) (wait vs spillover on backend cap) and [OQ-LD3](./tillr-live-drain-config.md#OQ-LD3) (where caps live) | `tillr drain policy set <path>` swaps active policy with refusal protection; `seat_shares` changes take effect from a fresh epoch without lifetime-count starvation; `backend_caps`, `seat_caps`, and `work_caps` enforce in-pass claim accounting, parallel scout child accounting, continuation priority, and `backend-capped` / `seat-capped` / `work-capped` reasons in `why`. |
| **5. `.tillr.toml` and `gate-policy.json` pass-boundary reload and operating docs** | [`tools/tillr/settings/settings.go`](../../tools/tillr/settings/settings.go), [`tools/tillr-drain/redcap/policy.go`](../../tools/tillr-drain/redcap/policy.go), [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go), [`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md), [`tools/tillr-view/README.md`](../../tools/tillr-view/README.md), and the `tillr-operating` skill (verified by [`tests/tillr-docs.test.py`](../../tests/tillr-docs.test.py)). | Ledger review (`D-LD9`) | Editing any of the 23 keys in `.tillr.toml` or the 4 keys in `config/tillr/gate-policy.json` reloads cleanly at pass boundaries (refusing invalid files with `drain.settings-refused`), and every new CLI face answers `--help` and passes `tests/tillr-docs.test.py`. |

---

## Checks for the planner when turning this sketch into build slices

- **Pool boundary isolation for item-owned worktrees:** keep numbered slot reconciliation (`active` / `draining` / `retired`) behind one narrow pool interface in `tools/tillr-drain/drain/` so that when [the vocabulary design's item-owned worktrees](./tillr-vocabulary.md#3-what-a-lane-was-and-the-worktree-as-a-resource) land, `drain_runtime_config`, `tillr drain scale`, `tillr drain alloc`, and `tillr drain config` carry straight over to the running-agent limit without schema churn.
- **Migration compatibility with read-only faces:** [`tools/tillr-drain/lanestate/read.go`](../../tools/tillr-drain/lanestate/read.go), [`tools/tillr-view/lanes.go`](../../tools/tillr-view/lanes.go), and [`tools/tillr-view/serveread.go`](../../tools/tillr-view/serveread.go) open older fixture databases in read-only mode without running migrations; guard queries on `lanes.lifecycle` and `drain_runtime_config` with schema introspection (`PRAGMA table_info` / `sqlite_master`, matching existing read-only guards in `serveread.go`) so unmigrated test fixtures continue to read cleanly as `lifecycle = 'active'`.
- **Single-writer overlap between Phases 2 and 4:** both touch [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) and [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go), so schedule Phase 4 `after` Phase 2 (while Phase 3 in `tools/tillr-view/` can run in parallel once Phase 1's schema lands).
- **Documentation contract test:** any slice that documents a new `tillr drain <face>` invocation in [`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md) or the `tillr-operating` skill must ship in the same commit as (or after) the binary flag implementation so `case_faces_answer_help` in [`tests/tillr-docs.test.py`](../../tests/tillr-docs.test.py) stays green.
