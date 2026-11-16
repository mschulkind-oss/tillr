---
title: "The coverage claim: every owed sentence in the tillr design documents, and where it ends"
status: in-review
stage: CURRENT
updated: 2026-11-13
tags:
  - tillr
  - coverage
  - queue
  - evidence
summary: "Fifteen tillr design documents were walked in full, not by their next: lines, and every sentence claiming work is owed was checked against today's tree and given exactly one disposition: a new item, an existing item that already covers it, an owner ruling, or a deletion naming the commit, file or removal. 147 claims: 10 new items, 2 new rulings, 21 claims covered by 17 of the seed's existing 29 items, 111 deleted. The three that would be dispatched first are the composed-pipeline case on the real registry, the planner's question hold, and the lane-rooted branch name."
next: "Splice the twelve payloads in the durable scratch directory into config/tillr/seed-items.json in another pass, rank them, and dispatch item-pipe-real-registry, item-drain-question-hold and item-lane-branch-scope."
---

# The coverage claim

**What this is.** The owner's goal state is that *all work we know of that has been talked
about, planned or is still valid is described by an item in the tillr queue*. This document is
the proof attempt for that: a walk of every tillr design document, sentence by sentence, looking
for the places each one says work is owed.

**The rule, applied without exception.** Every owed claim ends in exactly one of four places:

- **item** — new dispatchable work, delivered with this lane as a JSON array of item payloads
  (`config/tillr/seed-items.json`'s shape) that another pass splices in and ranks;
- **covered** — one of the 29 items already in [`config/tillr/seed-items.json`](../../config/tillr/seed-items.json)
  already owns it, named by id;
- **ruling** — an owner decision, seeded as a `blocked` item whose fence is the document that
  records it;
- **deleted** — already built (a commit in `HEAD`, a file, a test), a surface that is gone (the
  removal named), or a later ruling or measurement that superseded it (named).

There is no "later" and no "noted but unqueued" here. A claim whose status could not be settled
from the tree became a ruling, and the two that did are `item-ruling-approver-job` and
`item-ruling-blocked-resume`.

**How a claim was checked.** Against the code (`rg`, `ls`, the module suites' own comments),
against `git log` and `git merge-base --is-ancestor` for commits (every commit below is an
ancestor of `HEAD`), and against the two later documents that re-measured the same ground:
[`tillr-drain-acceptance-2026-11-13.md`](./tillr-drain-acceptance-2026-11-13.md) and
[`tillr-seed-triage.md`](./tillr-seed-triage.md). A prior pass
([`queue-inventory-2026-11-13.md`](./queue-inventory-2026-11-13.md)) screened 14 sources for new
items; this pass walks the documents themselves, so it reads the *claims* rather than the
frontmatter, and it disagrees with that pass in one place (section 7).

---

## 1. The sources walked, and how many owed claims each carries

| # | Document | Read | Owed claims |
| ---: | :--- | :--- | ---: |
| 1 | [`tillr-day2.md`](./tillr-day2.md) | fully, 456 lines | 14 |
| 2 | [`tillr-observatory.md`](./tillr-observatory.md) | fully, 545 lines | 11 |
| 3 | [`tillr-distribution.md`](./tillr-distribution.md) | fully, 317 lines | 9 |
| 4 | [`tillr-loop-readiness.md`](./tillr-loop-readiness.md) | fully, 431 lines | 10 |
| 5 | [`tillr-chunking-plan.md`](./tillr-chunking-plan.md) | fully, 266 lines | 21 |
| 6 | [`tillr-muse-backend.md`](./tillr-muse-backend.md) | fully, 356 lines | 9 |
| 7 | [`tillr-next-steps.md`](./tillr-next-steps.md) | fully, 181 lines | 10 |
| 8 | [`tillr-today.md`](./tillr-today.md) | fully, 545 lines | 12 |
| 9 | [`tillr-board-reshape.md`](./tillr-board-reshape.md) | fully, 426 lines | 7 |
| 10 | [`tillr-seed-triage.md`](./tillr-seed-triage.md) | fully, 240 lines | 6 |
| 11 | [`tillr-drain-acceptance-2026-11-13.md`](./tillr-drain-acceptance-2026-11-13.md) | fully, 167 lines | 12 |
| 12 | [`work-control-judge-in-tillr.md`](./work-control-judge-in-tillr.md) | fully, 378 lines | 7 |
| 13 | [`autoqa-tillr-seams.md`](./autoqa-tillr-seams.md) | fully, 465 lines | 10 |
| 14 | [`queue-inventory-2026-11-13.md`](./queue-inventory-2026-11-13.md) | fully, 195 lines | 5 |
| 15 | [`pi-pile-recovery-2026-11-13.md`](./pi-pile-recovery-2026-11-13.md) | fully, 252 lines | 4 |
| | **total** | 5,220 lines, fifteen documents | **147** |

Also read as authority, not as sources: [`landing-principles.md`](./landing-principles.md)
(the carrying and gating rulings that delete three claims below),
[`momentum-principle.md`](./momentum-principle.md) (why the tick budget is gone), and
[`board-utilities.md`](./board-utilities.md) section 5 rulings quoted by several documents.

---

## 2. Every claim, and where it lands

Disposition column: `item:<id>` is a new payload in this pass; `covered:<id>` is an existing seed
item; `ruling:<id>` is a new blocked payload; `deleted` names the commit, file, test or ruling
that ends it.

### 2.1 [`tillr-day2.md`](./tillr-day2.md) — 14

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| `LEASE` (section 2.2): reaping an expired lease and a `run release` face | deleted | `ReapExpiredLeases` is called at `tools/tillr-drain/drain/once.go:53` and `drain/loop.go:466`; `tools/tillr-run/main.go:43` carries the `release` face |
| `DRAIN-2A`: the drawn route is recorded for eval attribution | covered | `item-drain-2a` |
| `DRAIN-2B`: one-hop failover, exercised | covered | `item-drain-2b` |
| `D-D2-4`: path-disjoint pairing in a tick | covered | `item-loop-1` |
| `LAND-3B`: one lander named by the landing document | covered | `item-land-3b` |
| `LOOP-1` (section 2.8): the tick state machine, serial landing, stopping conditions | deleted | `tillr drain loop` is built: `tools/tillr-drain/drain/loop.go`, `looprecord.go`, and the `queue-drained`/operator stop constants |
| section 3.4: the item schema has no `allowed_paths`, so a disjoint planner cannot read one | deleted | the field shipped: all 29 seed items carry `allowed_paths` and `stage.go`'s `briefValues` refuses an item without one |
| section 3.4: no provenance field on an item | deleted | resolved as designed — provenance is words in the `body_ref` target plus [`tillr-seed-triage.md`](./tillr-seed-triage.md)'s concordance |
| section 3.4: no way to express a question hold or a program hold | deleted | `state: blocked` carries both, which is how the eight question rows and the two determination rows are seeded |
| `D-D2-5`: the 15-ready / 30-total seed floor | deleted | superseded by section 3.2's own measurement (the archive yields five) and by the producer question)'s ruling that the chunks and observatory rows become the producer |
| section 4.3: `.tillr/STOP`, `operator-stop`, and the capped infrastructure backoff | deleted | built in `drain/loop.go`'s stop constants and backoff |
| section 8.7: lost eval attribution is a failure criterion | covered | `item-drain-2a` |
| the producer question): which main-tree work is the queue's producer | deleted | ruled A and B on 2026-11-13, recorded in the document's section 9.2; `D-D2-7` in the ledger |
| section 9.2: the question block carries a filled answer and a second, empty `Answer:` stub | deleted | no work owed: a duplicated stub in the record, not a claim; the ruling is recorded above it |

### 2.2 [`tillr-observatory.md`](./tillr-observatory.md) — 11

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| `OBS-1`: per-run `brief`/`fence`/`base_sha`, planTick decisions, `steps.jsonl` | deleted | `tools/tillr-drain/drain/{looprecord,spans,steps}.go`, with `observability_test.go` |
| `OBS-2`: the read-only Go API | deleted | `tools/tillr-view/serve.go`, every `/api/v1` route (`bb40334a1f`) |
| `OBS-3`: the Vite/React screen, `just observatory`, the path-scoped gate check | deleted | `tools/tillr-view/web/`, the `observatory` recipe, `observatory-web` in `tools/fork-check.py` (`6c125be501`) |
| `OBS-4`: the `BASH_ENV` and `GIT_TRACE2_EVENT` emitters | deleted | `tools/tillr-run/backends/pi/run-pi:108-111`, `tools/tillr-trace/bash-shim.sh` (`a972d90c70`) |
| `OBS-5`: WebSocket invalidations | deleted | `tools/tillr-view/servews.go`, its handler and test (`a972d90c70`) |
| section 2.2: `planTick`'s decisions are in memory, not in `dispatches` | deleted | `tools/tillr-drain/drain/dispatches.go` writes them |
| section 6: the two-item acceptance walkthrough, recorded | covered | `item-obs-verify` |
| section 6.2 / section 4.1: `last_stop_reason` renders `null` until a slice adds it | covered | `item-tillr-stop-reason` |
| The first observatory question: does the server live inside `tools/tillr-view`? | deleted | settled by the build: `tools/tillr-view/serve.go` is the eighth-face-inside-`view` form (option A) |
| The second observatory question: does `OBS-1` land before the UI? | deleted | settled by the order the commits landed in: `OBS-1` before `OBS-3` |
| The third and fourth observatory questions: who indexes, and do two cards get promoted upstream | deleted | the third is settled by the build (the Go server triggers `tillr-trace` ingest); the fourth is a `matt-craft`/host-config edit, external to this repository, and section 2.3 enforces both principles locally until it lands |

### 2.3 [`tillr-distribution.md`](./tillr-distribution.md) — 9

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| Step 1: the pack, its eight `program` entries and the digest launcher | deleted | `yolo-packs/tillr/pack.json`, `yolo-packs/tillr/programs/*/run`, `just pack-sync`'s `mirror` loop |
| Step 2: a `README` per module with the Known Gaps banner, and a `--help` conformance test | item | `item-dist-module-readmes` |
| Step 3: the `tillr` composition skill | deleted | `yolo-packs/tillr/skills/tillr-operating/SKILL.md` |
| The AutoQA-handoff question: the separate `tillr-autoqa` skill | covered | `item-oq-td-pack-layout` |
| Step 4: machine selection in `config.toml` | deleted | `config/yolo-jail/config.toml:21`, `tag-personal/…/personal.toml:1`, `tag-work/…/work.toml:2` |
| The pack-scope question: every workspace or opt-in | deleted | settled by the tree (option A): the pack is in all three scopes |
| The cold-build question: lazy per-binary or batch | deleted | settled by the launcher: it builds one binary per digest when the fast path misses |
| The launcher question, and section 9.2: ship the source launcher | deleted | shipped — `programs/tillr/run` builds from the tree and caches by digest, which is the requirement section 9 states |
| section 9.3: a schema-compatibility policy so two versions can share one store | deleted | conditional by its own text ("only if two versions must share one store"); the launcher removes the two-version case |

### 2.4 [`tillr-loop-readiness.md`](./tillr-loop-readiness.md) — 10

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| `B1`: `tillr-loop gate` delegates to `fork-check.py` | deleted | `tools/tillr-loop/tillr-loop` defaults its registry export (`--registry-json`) and gate program |
| `B2`: seed fences, and the `patch/pi` items held | deleted | all seed items carry `allowed_paths`; the three patch items are `blocked` |
| `B3`: the pre-gate fence and clean-tree check | deleted | `fenceViolation` at `tools/tillr-drain/drain/loop.go:793,860` |
| `B4`: a sterile worktree before `worktree add` | deleted | `clearWorktree` at `drain/stage.go:279` |
| `D-LR4`: a per-run timeout inside the lease | deleted | `tools/tillr-run/run/exec.go:119` bounds the run by `req.TimeoutMS`, stamped from the policy's `timeout_seconds` |
| `D-LR6`: the seven gaps left broken tonight | deleted | a standing ruling: each gap names its own trigger, and none has fired (no multi-tenant lane root, no report consumer, no second writer of an item) |
| section 3: a fork-series item needs a dedicated fork-lane mode | item | `item-fork-lane-fence` |
| section 4: path-disjoint pairing is not implemented | covered | `item-loop-1` |
| section 4: a two-lane batch lands its second candidate by an un-gated carry | deleted | superseded by [`landing-principles.md`](./landing-principles.md) [Section 3](./landing-principles.md#3-gate-what-will-be-the-tip-once): `Trunk.Squash` ([`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go)) three-way merges onto the moved tip and records `gated_tree` and `landed_tree` on `landings`, pinned by `TestAMovedTrunkSquashesTheChangeOntoTheNewTip` in [`tools/tillr-land/main_test.go`](../../tools/tillr-land/main_test.go) |
| section 7: the pre-burn checklist | deleted | its five steps are `B1`–`B4` and the timeout above, all built |

### 2.5 [`tillr-chunking-plan.md`](./tillr-chunking-plan.md) — 21

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| `H0`: the host yolo upgrade that every landing queued behind | deleted | the gate answers on an unchanged trunk today (the tree's own landings), so the precondition it gated is satisfied; the host note stays a note |
| `DRAIN-Q`: the planner holds an item whose document carries an open question | item | `item-drain-question-hold` |
| `MM-1S`: the `backend` field on request, record and row | deleted | `tools/tillr-run/run/exec.go`, `apply.go`; the live store reads `schema_version` 4 |
| `MM-1A`: brief template, three adapters, extractors, policy, test | deleted | `tools/tillr-run/backends/`, `config/tillr/backend-policy.json`, `tests/tillr-muse-backend.test.py` |
| `GATE-5` / `Q1`: who executes the in-process checks | deleted | the shell-out option landed as the `--gate` face; `D-LR2` rules it |
| `ITEM-V`: criterion state moves only on a verdict | deleted | `tools/tillr-item/item/verdict.go`, `apply.go:260,280` (`criterion-frozen`, `criterion-state`) |
| `GRANT`: the delegated sign-off grant | deleted | `tools/tillr-item/item/grant.go` |
| `LEASE` | deleted | the reaper and the `release` face (see section 2.1) |
| `SUB-2`: `env_refs` resolved through a substrate loader | deleted | deferred by `D-D2-6`; `TestExecRefusesARequestThatNeedsEnvRefs` (`tools/tillr-run/main_test.go:481`) refuses a non-empty list by naming the seam |
| `MM-2`: a specimen request against the echo provider | deleted | dropped with the muse backend (`D-D2-6`, day2 section 6): the subscription lapsed and the adapter is dormant |
| `MM-3` and its conditional `MM-3W`: the feeder script and the cold-prefix measurement | deleted | same deferral; the measurement needs live muse runs that cannot happen while the backend is dormant |
| `DRAIN-2A` | covered | `item-drain-2a` |
| `DRAIN-2B` | covered | `item-drain-2b` |
| `LAND-3I`: the one-lander inventory | covered | `item-land-3b` |
| `LAND-3B`: one lander, built | covered | `item-land-3b` |
| `PIPE-REAL`: E1/E2's proof from the real registry | item | `item-pipe-real-registry` |
| E2's second clause: release, reuse and an explicit `retry` | deleted | `ReapExpiredLeases` plus `ReasonFailover`/`ReasonRunLinkMissing` in `drain/schema.go` |
| E2's third clause: per-item backend selection under the policy | deleted | built: `tools/tillr-drain/drain/policy.go:34` (`Overrides`), with an unknown item or backend refused by name at `:88` |
| `SUB-3`: the gate's store-named mode | deleted | deferred indefinitely by [`tillr-next-steps.md`](./tillr-next-steps.md) section 4; no consumer has appeared |
| section 6's remaining deferrals: landing-velocity's slices, `GAP7`'s document pass, `SPEC-*`, `RET-*`/`MISC-2`/`WRAP-1` | deleted | each is deferred or moot by its own ruling; the shadow slice alone is live and is `item-landing-tier-shadow` |
| section 7's `Q1` question row | deleted | same ruling as `GATE-5` above |

### 2.6 [`tillr-muse-backend.md`](./tillr-muse-backend.md) — 9

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| `MM-1S` and `MM-1A` | deleted | the document's own header records both built, and each file it names exists |
| `MM-2`: Stage 2's specimen run | deleted | dropped with the backend (`D-D2-6`) |
| `D-MM7`: the four-run cold-prefix measurement | deleted | Stage 3's move-on condition, and Stage 3 is dropped with the backend; no warm host is wanted at two items a day |
| `D-MM8`: per-item selection in the policy, refused mechanically | deleted | built at `drain/policy.go:34,88` |
| `D-MM9`: the boundary snapshot checked in the loop | deleted | `checkBoundary`/`fenceViolation` in `drain/loop.go`; a non-empty diff stops the tick |
| `D-MM11`: a lane-worktree run commits, a main-checkout run never does | deleted | the candidate is the lane's HEAD: `tools/tillr-drain/drain/steps.go`, `attempt.go` |
| section 1: the boundary canary is owed by the first live run | deleted | the canary rides the first live run of whichever backend answers; with `muse` dormant and `pi` live, the snapshot half is exercised by `pi` and the muse half returns with the switch (`backend-policy.json`'s `about`) |
| section 8: [`agent-runtime.md`](./agent-runtime.md) owes the process-and-records amendment | item | `item-agent-runtime-backend` |
| section 8: `SUB-2`'s newly named trigger | deleted | same deferral as its chunking-plan row |

### 2.7 [`tillr-next-steps.md`](./tillr-next-steps.md) — 10

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| section 1's wave: `GATE-4`, `ITEM-PKG`, `LAND-1/2`, `GATE-3`, `RUN-2`, `ITEM-2+3`, `DRAIN-1`, `VIEW-2R` | deleted | all eight landed: `GO_TOOL_DIRS` names every tillr module, `tools/tillr-item/item/` is importable, `tillr-land` has its three faces, `drain plan` and `drain once` exist, `view status --store` reads the store |
| `Q1`: who runs the in-process checks | deleted | the shell-out face, `D-LR2` |
| `Q2`: who fixes the `gate.verdict` spellings | deleted | `tools/tillr-gate/verdict.go` fixes them and `land` consumes them |
| `Q3`: where the question parser lives | deleted | the parser is item's (`tools/tillr-item/item/questions.go`); what it feeds is `item-drain-question-hold` |
| `Q4`: lease semantics | deleted | ruled in [`board-utilities.md`](./board-utilities.md) section 5.6 and built |
| `Q5`: breakage and flake-override table owners; any store-named gate mode | deleted | deferred with `SUB-3`; no successor table is introduced and no consumer asks |
| `Q6`: may a verdict cross machines | deleted | the document's own assumption holds — verdicts are consumed where they were produced, and no cross-machine landing exists |
| section 4: stop `MISC-2`, defer `SUB-3`, defer `WRAP-1`, stop scheduling `RET-*` | deleted | all four are rulings already recorded; the monolith they named is deleted |
| section 5: the `LAND-3` inventory brief | deleted | `docs/design/land-3i-inventory.md` exists — the brief this section waited for |
| section 5: the `DRAIN-2` supervision brief | deleted | superseded by the build: `tillr drain loop` is the brief's answer, and its shape is now code |

### 2.8 [`tillr-today.md`](./tillr-today.md) — 12

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| Task 1: enforce frozen criterion text | deleted | `tools/tillr-item/item/apply.go:260,280` refuses a reword and a dropped criterion |
| Task 4: the question parser must read the numbered declaration form | deleted | `TestShowReadsTheIdentifierFirstDeclarationForm` (`tools/tillr-item/questions_test.go:143`) |
| Task 4: `DRAIN-Q`, the planner's question hold | item | `item-drain-question-hold` |
| Task 5: `PIPE-REAL`'s one-tick half | item | `item-pipe-real-registry` |
| section 7: the day's evidence block | deleted | superseded by [`tillr-drain-acceptance-2026-11-13.md`](./tillr-drain-acceptance-2026-11-13.md), which records the run with the same surfaces |
| section 8: the `runs` row does not name its item | deleted | `runs.item_id` is populated on the schema-4 store the acceptance run read |
| section 8: `tillr-land` refuses where the design says carry | deleted | built as `Trunk.Squash` and `Land` ([`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go), [`tools/tillr-land/land/landing.go`](../../tools/tillr-land/land/landing.go)), pinned in [`tools/tillr-land/main_test.go`](../../tools/tillr-land/main_test.go) and [`tools/tillr-land/land/trunk_test.go`](../../tools/tillr-land/land/trunk_test.go) |
| section 6: `LEASE` deferred | deleted | built |
| section 6: `DRAIN-2A`/`DRAIN-2B` deferred | covered | `item-drain-2a`, `item-drain-2b` |
| section 6: `MM-3`, `SUB-2`, `LAND-3B`, the Pi and Agy proof runs deferred | deleted | `MM-3`/`SUB-2` dropped or deferred by ruling; `LAND-3B` is `item-land-3b`; the two proof runs are the first live run of each adapter, recorded as a condition on the pi and agy adapters rather than an item |
| section 1: the real-drain definition — two items landed and retired | covered | its two open halves are `item-seed-04` (the unjudged lane-2 candidate) and `item-drain-2a` (attribution) |
| The two "today" questions, whose ledger rows are marked unbuilt | deleted | both are rulings with no build behind them; the second's landing half is `Trunk.Squash` |

### 2.9 [`tillr-board-reshape.md`](./tillr-board-reshape.md) — 7

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| section 1: criterion text is not frozen against edits | deleted | built (see [`tillr-today.md`](./tillr-today.md) Task 1) |
| section 5.3: a concordance, one line per triaged archived item | deleted | [`tillr-seed-triage.md`](./tillr-seed-triage.md) carries it — 56 rows, one disposition each |
| section 4/section 6: the four blocked questions route to their own documents | deleted | dropped as `not-an-item` in the triage, under `D-UT4`: questions are document state, and each names its document |
| section 6: preserve the content dump in the repository | deleted | `docs/archive/board-2026-11-12/board-content.json`, committed whole under the verbatim-preservation ruling |
| section 6: check the 5 questions whose text did not match verbatim | deleted | resolved as designed — the triage's archive reading records the drift, and `D-UT4` makes the document the copy that matters |
| section 6: transcribe the 2 answered questions into a tracked record | deleted | the preserved dump is that record: the questions, their answers and dates are in `board-content.json`'s `questions` array, which is what that ruling committed |
| The two board-reshape questions, whose ledger rows carry no built column | deleted | both are rulings; the second's approval is [`tillr-seed-triage.md`](./tillr-seed-triage.md) itself |

### 2.10 [`tillr-seed-triage.md`](./tillr-seed-triage.md) — 6

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| The floor (`D-D2-5`): how the queue reaches 15 ready and 30 total | deleted | superseded by the producer question) A and B: the chunks and observatory rows are the producer, and this pass adds ten more |
| Drop 612's class re-measured against `tillr-drain` | item | `item-drain-fatal-record` |
| Drop 622's class re-measured against `tillr-land` | deleted | surface gone: `tillr-land` has `pass` and `one` and no status face, so no report counts its own pass |
| Drop 619's class re-measured against the tillr suites | deleted | measured absent: `rg -n 'time.Since\|elapsed' tools/tillr-*/*_test.go` returns nothing, and `tools/tillr-gate/gate_test.go:186` states the assertion is the verdict, never an elapsed time |
| What the seed cannot hold: `allowed_paths` | deleted | built: the field exists and staging refuses an item without one |
| What the seed cannot hold: provenance, a question hold, a program hold | deleted | provenance is words in the `body_ref` target; both holds are `state: blocked`, which is how the rulings are seeded |

### 2.11 [`tillr-drain-acceptance-2026-11-13.md`](./tillr-drain-acceptance-2026-11-13.md) — 12

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| 4.1: the landing inverts the working tree | deleted | `TestALandingMovesTheTrunkWorktree` in [`tools/tillr-land/main_test.go`](../../tools/tillr-land/main_test.go) asserts the worktree is clean after a landing |
| 4.2: the projection drops the run's item link | deleted | `ab14e1b7a6` — "a run's item link survives the snapshot summary" |
| 4.3: judgment is recorded as files, not events | deleted | `drain/events.go` registers `boundary.check`, `gate.plan` and `gate.verdict` (`2050271d4a`) |
| 4.4: the run report contradicts its own record | deleted | the report is given the facts: `--exit-code` and `--route` are staging arguments now (`tools/tillr-run/backends/pi/README.md:152-156`) |
| 4.5: the stop reason is not written | covered | `item-tillr-stop-reason` |
| 4.6: lane branch names are global | item | `item-lane-branch-scope` |
| 4.7: the lane adapter's suite is PATH-sensitive | deleted | `tools/tillr-loop/test.py` strips every directory holding a `tillr-*` binary (line 118) |
| 4.8: `tillr view status` answers nothing it holds | deleted | `tools/tillr-view/status_store.go` reads items, criteria and runs from the store (VIEW-2) |
| 4.9: an unjudged candidate loses its branch | deleted | `refuseUnlandedCommit` (`drain/stage.go:242`) refuses to reset over a commit the base does not contain (`1c742dcbd0`) |
| section 5: lane-2's candidate is unlanded and unjudged | covered | `item-seed-04`, whose work that candidate is |
| section 5: route attribution | covered | `item-drain-2a` |
| section 5: `:stepfun` refuses this client | covered | `item-drain-2a` — no route can be attributed, or kept, without the drawn-route record it builds |

### 2.12 [`work-control-judge-in-tillr.md`](./work-control-judge-in-tillr.md) — 7

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| The `item.verdict` record kind and its writer rules | deleted | `tools/tillr-item/item/verdict.go`, `apply.go` (`f8c0320288`) |
| `GRANT`: the grant record and its refusal | deleted | `tools/tillr-item/item/grant.go` |
| The mechanical retirement path | deleted | `loop.go`'s verdict step record `item.verdict` citing the landed attempt |
| The in-process judge, the turn-end pass, the shadow approver and `review_items`, all deleted | deleted | the surface is gone: `yolo-packs/matt/files/extensions/work-control.ts` is not in the pack |
| "What has to be true" (1): seat-gate `review_items` and the shadow launch message | deleted | moot — neither exists any more |
| "What has to be true" (3): the equivalence audit that sizes the approver job | ruling | `item-ruling-approver-job` |
| "What has to be true" (5): name what happens to resume conditions the turn-end trigger observed | ruling | `item-ruling-blocked-resume` |

### 2.13 [`autoqa-tillr-seams.md`](./autoqa-tillr-seams.md) — 10

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| `D-AQA1`: the `autoqa` actor kind and claim class | deleted | `ActorAutoQA` at `tools/tillr-item/item/verdict.go:37` |
| `D-AQA2`: the loop publishes the gate observation | deleted | `2050271d4a`; the ruling's own answer records it built |
| `D-AQA3`: `tillr-run exec` propagates the run's exit code | deleted | `runExit` at `tools/tillr-run/main.go:112` |
| `D-AQA4`: `tillr-gate change` and `tillr-run request` | deleted | `tools/tillr-gate/change.go`, the `request` face in `tools/tillr-run/main.go` |
| section 3.5 (1) / finding 2: `correlation_id` and the trace key on the tick's record and events | item | `item-trace-correlation` |
| section 3.5 (2) / finding 3: `gate.verdict` as an event | deleted | `GateVerdictKind` in `drain/events.go` |
| section 3.5 (3): `land.attempt` as an event for `refused` and `isolated` | item | `item-land-attempt-event` |
| Finding 4: `tillr-store init` creates a store three binaries cannot use | deleted | the acceptance run opened a schema-4 store from every face, and `store.SchemaVersion` agreement is checked at `tools/tillr-store/main.go:329-333`; the two-exit-code disagreement it named is not reproducible |
| Finding 5: no verdict actor kind for an automated reviewer | deleted | `ActorAutoQA` |
| Finding 6: `decided_by` has no `autoqa` value | item | `item-decided-by-autoqa` |

### 2.14 [`queue-inventory-2026-11-13.md`](./queue-inventory-2026-11-13.md) — 5

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| Its eight kept payloads are applied and ranked | deleted | the seed holds 29 items with `rank` on the eight |
| The queue is one short of the floor and needs a producer | deleted | superseded by the producer question)'s A-and-B ruling; this pass adds ten |
| The finding: a patch-branch fork has no fence a lane can name | item | `item-fork-lane-fence`, which carries the fence vocabulary and the lane mode together |
| "It did not seed the store" | deleted | a launch step, not a lane: the seed file is the source of truth and the live store takes it at the next relaunch |
| "It did not rank anything" | deleted | the ranking is recorded in the seed's `rank` fields, and this pass does not re-rank |

### 2.15 [`pi-pile-recovery-2026-11-13.md`](./pi-pile-recovery-2026-11-13.md) — 4

| Claim | Disposition | Where it lands |
| :--- | :--- | :--- |
| The nine still-wanted rows: `#11`, `#63`, `#112`, `#148`, `#161`, `#507`, `#617`, `#618`, `#623` | covered | `item-bell-coalesce`, `item-pile-63-resume-prefix`, `item-or-routing`, `item-seed-01`, `item-pile-161-picker-raw-id`, `item-designer-pin` + `item-pile-507-allowlist-hunt`, `item-seed-03`, `item-seed-04`, `item-seed-05` — nine rows, nine items |
| `#11` and `#112`, which the seed triage called built | covered | `item-bell-coalesce`, `item-or-routing` |
| The proposed dispatch order, "not applied" | deleted | the order is recorded as `rank` on the seed items; no separate work |
| Its `next:` — the owner rules on the nine | deleted | superseded: each of the nine is an item, and a ruling would add nothing a dispatch does not |

---

## 3. The new payloads

Ten items and two rulings, in the JSON array delivered beside this lane, each with a
criterion that fails on a command run against this tree and an exact fence. The array is not applied to the seed by this pass: the seed file and every other document are
unchanged, and the splice is another pass's.

| id | state | fence | criterion that fails today |
| :--- | :--- | :--- | :--- |
| `item-drain-question-hold` | in-queue | `tools/tillr-drain/` | `rg -n 'question-open\|design-doc-missing' tools/tillr-drain/drain/schema.go` answers only the A-NOQ comment |
| `item-pipe-real-registry` | in-queue | `tools/tillr-drain/` | `rg -ln 'fork-check' tools/tillr-drain -g '*_test.go'` returns nothing |
| `item-fork-lane-fence` | in-queue | `tools/tillr-drain/`, `tools/tillr-land/` | `rg -n 'patch/\|forks/lanes' tools/tillr-drain -g '*.go'` matches one comment and no code |
| `item-lane-branch-scope` | in-queue | `tools/tillr-drain/` | `drain/stage.go:296` is `laneBranch = "tillr/" + lane` |
| `item-dist-module-readmes` | in-queue | the eight binary modules, `tools/fork-check.py`, `tests/` | `ls tools/tillr-cli/README.md tools/tillr-drain/README.md` fails for both |
| `item-agent-runtime-backend` | in-queue | `docs/design/agent-runtime.md` | section 3 still rules the in-process sub-interface |
| `item-trace-correlation` | in-queue | `tools/tillr/record/`, `tools/tillr-drain/`, `tools/tillr-run/` | `rg -n 'CorrelationID' tools/tillr-drain/` returns nothing |
| `item-land-attempt-event` | in-queue | `tools/tillr-land/`, `tools/tillr-drain/` | `tools/tillr-land/land/schema.go:113` says `land.attempt` is never an event |
| `item-decided-by-autoqa` | in-queue | `tools/tillr-item/` | `item/schema.go:105` refuses any `decided_by` but `owner` and `whale` |
| `item-drain-fatal-record` | in-queue | `tools/tillr-drain/` | `rg -n 'recordResult\|LoopResultFile' tools/tillr-drain/main.go` answers nothing |
| `item-ruling-approver-job` | blocked | `docs/design/work-control-judge-in-tillr.md` | a ruling to record, not work to do |
| `item-ruling-blocked-resume` | blocked | `docs/design/tillr-day2.md` | a ruling to record, not work to do |

---

## 4. The reconciliation

| Line | Count |
| :--- | ---: |
| Owed claims found, fifteen documents | **147** |
| Claims that become a new item (`in-queue`, `items.json`) | 13 |
| Claims that become a new ruling (`blocked`, `items.json`) | 2 |
| Claims already covered by one of the seed's 29 items | 21 |
| Claims deleted — already built, surface gone, or superseded | 111 |
| **Sum of dispositions** | **147** |

The arithmetic, written out: `13 + 2 + 21 + 111 = 147`. Nothing is counted twice: a claim
appears on exactly one row of section 2, and a row carries exactly one disposition. The seed's 29 items
are untouched by this pass; the payloads are new ids, none of them in
`config/tillr/seed-items.json`.

Two of the four counts are smaller than their claim counts, and both reasons are structural
rather than arithmetic slack:

- **13 claim rows become 10 distinct items**, because two claims are the same work named by two
documents: `item-drain-question-hold` is [`tillr-chunking-plan.md`](./tillr-chunking-plan.md)'s `DRAIN-Q` and
[`tillr-today.md`](./tillr-today.md)'s Task 4, `item-pipe-real-registry` is that plan's `PIPE-REAL` and that day's
Task 5, and `item-fork-lane-fence` is [`tillr-loop-readiness.md`](./tillr-loop-readiness.md) section 3 and
[`queue-inventory-2026-11-13.md`](./queue-inventory-2026-11-13.md)'s fence finding. An item is authored once and cited twice.
- **21 covered claims map onto 17 distinct seed items**, because three items absorb more than one
claim: `item-drain-2a` (route attribution in [`tillr-day2.md`](./tillr-day2.md), again in the chunking plan's
`DRAIN-2A` row, and again in the acceptance document's section 5), `item-land-3b` (`LAND-3I` and
`LAND-3B`), `item-drain-2b` and `item-loop-1` (each claimed by two documents), and
`item-bell-coalesce`/`item-or-routing` (each claimed once by the pile document and again by its
own "already built" reversal).

---

## 5. What would falsify this

The claim is "every owed sentence in these fifteen documents has a disposition". It is falsified
by any of the following, and the owner should read the list as what the 100% excludes:

- **Work nobody wrote down.** This pass can only see what a document says. A defect a session
  noticed and did not file, a decision taken in conversation, or a task that lives only in the
  owner's head is outside the count — and is the largest known gap. The queue's producer question
  (the producer question)) is the mechanism for that, not this document.
- **A claim phrased as prose nobody would read as owed.** I counted the sentences that say
  "unbuilt", "owed", "remains", "not yet", and the `next:` lines; a paragraph that describes a
  missing mechanism in the present tense without flagging it reads as description. The three
  documents with the most prose per claim ([`tillr-day2.md`](./tillr-day2.md), [`tillr-observatory.md`](./tillr-observatory.md),
  [`autoqa-tillr-seams.md`](./autoqa-tillr-seams.md)) are where a missed claim would hide.
- **A surface that moved between the document and the tree.** Two dispositions here turned on a
  file that changed *during* this pass's own week: `work-control.ts` left the pack (which deleted
  four claims at once), and `runs.item_id` landed (which deleted a claim that a document written
  two days earlier says is missing). A deletion naming a file is only true of the tree it was read
  at.
- **A criterion that passes on first contact.** Each new criterion names a command that fails
  today, and the evidence is in the lane's report rather than in this document. If any of those
  commands passes when the item is first staged, the item is a `candidate-no-op` and this
  document's item count is wrong by one.
- **A "deleted" that names a ruling which has since been reversed.** Several deletions rest on
  `D-D2-6`, `D-D2-7` or [`landing-principles.md`](./landing-principles.md) section 3. Reversing any of those resurrects its row.
- **The one disagreement with the earlier pass.** [`queue-inventory-2026-11-13.md`](./queue-inventory-2026-11-13.md) counts the
  acceptance run's defect 4.6 as built (`clearWorktree`). It is not: `clearWorktree` clears one
  lane root's registration and prunes only registrations whose directories are gone, while the
  branch a lane commits onto is still `tillr/<lane>` in the repository — so a stale lane in a
  *second* lane root still makes `worktree add -B` fail, which is exactly what the acceptance run
  measured. That is `item-lane-branch-scope`, and if I am wrong it is a deletion instead.

---

## 6. The three I would dispatch first

1. **`item-pipe-real-registry`** — the composed-pipeline case on the real registry. It is the
   proof the whole pipeline rests on ([`tillr-chunking-plan.md`](./tillr-chunking-plan.md) E1), it needs no model spend, it
   touches one test file, and its passing number is stated in the document: zero production
   files. Every later claim about "the gate answered `pass`" is a claim about a stub until it
   lands.
2. **`item-drain-question-hold`** — the planner's missing reason. [`tillr-day2.md`](./tillr-day2.md) names
   `question-open` in its per-tick pipeline, `drain/schema.go` says in its own comment that it is
   not among the reasons, and this whole pass is a document that says a great deal about what is
   owed: a drain that dispatches straight past a design carrying an open ruling will do exactly
   that to the next document anybody writes.
3. **`item-lane-branch-scope`** — the cheapest defect on the list with a measured failure. One
   function at `stage.go:296` made a real run stop before its first tick, and it is the only new
   item whose failure mode recurs on every subsequent drain.

`item-decided-by-autoqa` and `item-dist-module-readmes` are the cheapest to verify and belong in
the second wave; `item-fork-lane-fence` is the largest and unblocks a body of designed work the
queue currently cannot name.

---

## 7. What this pass did not do

- It did not re-open the 623-item archive. Two documents have already triaged it in full
  ([`tillr-seed-triage.md`](./tillr-seed-triage.md), [`pi-pile-recovery-2026-11-13.md`](./pi-pile-recovery-2026-11-13.md)) and this pass reads their
  conclusions and re-measures only the rows they left open.
- It did not edit the seed, any other document, or any code. The payloads sit outside the
  repository for another pass to splice and rank.
- It did not rank the ten items beyond the dispatch order in section 6, which is a judgment and says so.