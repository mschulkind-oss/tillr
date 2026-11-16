---
title: "Tillr Drain Acceptance: The Two-Item Run of 2026-11-13"
status: active
stage: BUILT
board: "613"
updated: 2026-11-13
tags: [tillr, drain, acceptance, observability, evidence, qa]
summary: "An unattended `tillr drain loop` ran the two dispatchable seed items in two lanes on 2026-11-13, landed one real candidate, and left the other unjudged after its run hit the 10-minute timeout. This records the six day-2 predicates as measured — two pass, three fail, one not attempted — plus the eight defects the run exposed, each with the surface that shows it. The defect that matters most is not a missing feature: the run-to-item link is written in the event payload and dropped by the projection the planner reads, so a completed candidate is reported as `run-link-missing` and abandoned."
next: "The three failing predicates are the night's own findings, not scope: route attribution is unrecorded, Layer B is unindexed after a tick, and only one of the seed's two dispatchable items has a lane a human can trust. The projection's dropped `item_id` is the one defect that both explains a false `run-link-missing` and hides every run's item in the UI, so it is the first repair."
---

# Tillr Drain Acceptance: The Two-Item Run of 2026-11-13

This is the acceptance evidence for the unattended queue loop named in
[`tillr-day2.md`](./tillr-day2.md), taken from one real run rather than from a fixture. It records what
the loop did, which of the plan's six numerical predicates the run met, and every defect the run
exposed — including the ones that make the observatory's own view wrong. Nothing here is inferred from
the loop's summary line: each claim names the surface it was read from.

## 1. What ran

```
tillr drain loop --root /workspace --lanes 2 \
  --lane-root /workspace/.yolo/durable/tillr-lanes \
  --gate    "tools/tillr-loop/tillr-loop gate --root /workspace" \
  --land    "tools/tillr-loop/tillr-loop land" \
  --verdict "tools/tillr-loop/tillr-loop verdict"
```

- **Store**: `/workspace/.tillr/state.db`, schema 4, created for this run and seeded from
  [`config/tillr/seed-items.json`](../../config/tillr/seed-items.json) (5 items, 2 `in-queue`).
- **Outcome**: one tick. `dispatched 2, landed 1, rejected 0, infra failures 0`, then
  `queue-drained` — a full tick planned nothing dispatchable.
- **Spend**: `$0.162990` over `38 744 131` tokens, from two `pi` runs
  (`run-item-seed-02-20261113T062156Z` $0.109994, `run-item-seed-04-20261113T062156Z` $0.052996).
- **Landed**: `e85442494d feat(seats): a yolo profile names its default seat set` — +242/−4 over five
  files, authored as `Matthew Schulkind <owner@example.invalid>` by the lander's identity
  normalization. Its own suite passes in the landed tree (19 cases, including a real
  `yolo config dump` read back for `seat_set`), and the repository gate answers 0 on it.
- **Left unjudged**: `item-seed-04`, whose run committed `b583a03f7b` [unlanded] and whose record says
  `state: timed-out`. The loop then reset `tillr/lane-2` to `main`, so the commit left no branch of its own;
  it is preserved here as `fanout/1131-lane2-candidate`, which is [Section 4.9](#49-an-unjudged-candidate-loses-its-branch).

## 2. The six predicates, as measured

Predicates are quoted from [`tillr-day2.md`](./tillr-day2.md)'s end-of-day condition.

| # | Predicate | Result | Evidence |
| :-- | :--- | :--- | :--- |
| 1 | `schema_version == 3` and `runs.item_id` populated on 100% of terminal `run.record` rows | **fail** | Schema is 4, not 3. The event payload carries `item_id` (`run.finish` seq 8 and 9 of `tillr view events`), but the projection the snapshot renders reports `item_id: null` for both runs. |
| 2 | ≥30 migrated items, ≥15 starting `ready` | **not attempted** | The seed holds 5 items, 2 dispatchable. The producer question is [a closed question](./tillr-day2.md), open in [Section 9.2](./tillr-day2.md#92-open-questions), and this run is not evidence about it. |
| 3 | ≥5 consecutive unattended ticks, 2 lane worktrees, ≥5 terminal `run.record`s with per-request route attribution | **fail** | One tick, two runs, two lanes. Attribution is absent: the run report states `route: unknown`, and the run log's `responseModel` is `deepseek/deepseek-v4.1-flash-1017` with no route suffix. |
| 4 | ≥3 items carried `run` → `boundary.check` → `gate.verdict` → `land` → `item.verdict` | **fail** | One item carried the whole way. `item-seed-04` stopped at `run.finish`. |
| 5 | ≥1 non-happy-path outcome isolated without halting the loop or retrying a failed gate | **pass** | `item-seed-04` terminated `timed-out`; the loop recorded it, dispatched the other lane's candidate, landed it, and stopped on `queue-drained` rather than retrying or halting. |
| 6 | `active_leases == 0` and no dirty files in `/workspace` outside the store | **pass** | `tillr run lanes` reports both lanes `idle` with `run_id: null`. Working tree clean after the repair in [Section 4.1](#41-the-landing-inverts-the-working-tree). |

## 3. Every tool call: unobserved, and honestly so

The run's timeline renders one span before any tool activity:

```json
{"span_id": "run-item-seed-02-…/seam/index/gap", "category": "gap",
 "label": "tillr-trace has not indexed this run: every Layer B row is unknown, not empty",
 "epistemic_state": "unobserved_seam", "status": "unobserved"}
```

That is the correct rendering of a real absence. The trace index at
`~/.local/state/tillr-trace/v1/index.db` held **one** row after the run, and it was a probe row from an
earlier session (`obs-store-unreadable`, 00:39:50Z, `run_id: unattributed`); `runs_index` was empty.
So nothing indexed either run: the seam is wired, and no step of the loop invokes the indexer after a
tick, which leaves Layer B empty until a human runs it. The day-2 "every tool call" requirement is
therefore unmet — not because the tracer failed, but because nothing ran it.

## 4. Defects the run exposed

Neither of the first two is a missing feature. Both are the mechanism disagreeing with itself.

### 4.1 The landing inverts the working tree

After `land.land` advanced `main` to `e85442494d`, `/workspace`'s index and working tree still held the
pre-landing content, so `git status` reported the landing's own diff **in reverse** — `4 insertions,
242 deletions` — over the five files it had just added. The tree's content was correct in `HEAD` and
stale in the worktree: the land moved the ref without a checkout. Repaired with a mixed
`git reset` and `git checkout --` on exactly those five paths. A human watching that status would
conclude the loop had deleted what it landed.

### 4.2 The projection drops the run's item link

`run.finish` carries `item_id` in its payload; `/api/v1/runs/<id>` reports the correct `item_id`; the
snapshot's `recent_runs` reports `item_id: null` for both runs. The planner reads the same projection,
which is why `item-seed-04` was reported `run-link-missing` and abandoned — the link existed the whole
time. This single defect both fails predicate 1 and hides every run's item from the run list.

### 4.3 Judgment is recorded as files, not as events

The loop's run directory holds `gate.plan.json`, `gate.verdict.json`, `land.candidate.json`,
`item.verdict.c1.json`, `item.verdict.c2.json` and `steps.jsonl`, and the artifact endpoint serves them
(`/api/v1/runs/<id>/artifacts/gate.verdict` → `present: true`, 780 bytes, sha256 recorded). The store's
event table holds **no** `gate.*` or `boundary.*` event: 14 events, of which the judgment steps are
`land.enqueue` and `land.land` only. So a reader reconstructing from `tillr view events` sees a
candidate pass from `run.finish` to `land.enqueue` with no verdict between, while the observatory's
artifact view shows the verdict. Two recording paths exist and only one emits events.

### 4.4 The run report contradicts its own record

`item-seed-04`'s report states `complete: yes`, `result: done`, 11 271 events, and it committed a
candidate — while `run.finish` says `timed-out` and the loop step took 30 minutes of wall clock for a
10-minute run. The report header states the cause: `model: unknown, route: unknown, effort: unknown,
exit code: unknown`. The adapter never observed an exit code, so "complete" is inferred from log text
and the record is a timeout. A later consumer reading the report alone would judge a killed run
finished.

### 4.5 The stop reason is not written

The observatory's snapshot reports `last_stop_reason: null` with
`stop_note: "a stop reason is written by the loop's own result file, which OBS-1 adds"` — a gap
rendered as a gap, and correct to do so, but it means the UI cannot distinguish `queue-drained` from
`operator-stop` or a fault.

### 4.6 Lane branch names are global

`git worktree add -B tillr/lane-1` failed outright because the canary experiment's lane still held the
branch name in another lane root:

```
fatal: cannot force update the branch 'tillr/lane-1' used by worktree at …/canary/lanes/lane-1/worktree
```

A lane root does not scope the branch name, so a stale lane anywhere blocks every future drain. The
leftover commit is archived as `canary/archive-lane-1-6751a137` [unlanded]; the coupling remains.

### 4.7 The lane adapter's suite is PATH-sensitive

`tools/tillr-loop/test.py::test_missing_judge_is_not_a_failure` prepends an empty directory to `PATH`
instead of removing the directory the real binaries live in, so it passes under the gate's scrubbed
environment and fails in any login shell where `tillr-gate` is installed:
`tillr-gate plan: reading the check registry: open …/registry.json: no such file or directory` where the
case asserts `not on PATH`. The assertion is right and the fixture cannot reach the state it claims.

### 4.8 `tillr view status` answers nothing it holds

`tillr view status --store .tillr/state.db` returns `item: null, questions: null, candidates: null,
runs: null` against a store holding 5 items, 1 candidate and 2 runs, while the served snapshot answers
all of them. The CLI face and the HTTP face read differently.

### 4.9 An unjudged candidate loses its branch

The commit names its lane, and the lane does not keep it. `item-seed-04` finished with
`b583a03f7b` on `tillr/lane-2`; the loop's next tick reset that branch to `main`
(`tillr/lane-2@{0}: branch: Reset to main`), leaving the candidate reachable only from the branch's
reflog and therefore removable by any `git gc`. The design's rule — "a lane released is not a candidate
retried" — is about not retrying, and it must not also mean *not keeping*: a candidate the loop declined
to judge is exactly the work a human needs to inspect. The commit here is preserved by creating
`fanout/1131-lane2-candidate` at it, which is an operator's repair, not the loop's behaviour.

## 5. What stays open

- **Lane-2's candidate is unlanded and unjudged.** `b583a03f7b` [unlanded] is real work (the item's
  profiler consolidation) that the loop declined to judge, and the branch it was committed on is gone
  ([Section 4.9](#49-an-unjudged-candidate-loses-its-branch)). A lane released is not a candidate
  retried, so retrying it is an operator's decision, not the loop's.
- **`item-seed-04`'s item state is `run-link-missing`**, which [Section 4.2](#42-the-projection-drops-the-runs-item-link) says is a projection artifact
  rather than a fact about the run.
- **Route attribution** is the predicate that most needs a decision, because the routes are chosen per
  dispatch and nothing records which one answered. `:stepfun` refuses this client's requests outright
  (400 `input_invalid`, two unrelated prompts), so a dispatch that draws it spends a run to learn
  nothing.
