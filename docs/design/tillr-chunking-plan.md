---
title: "Tillr — delivery chunking: the remaining work as dispatchable lanes"
status: proposed
stage: DESIGN
updated: 2026-11-12
tags: [tillr, delivery, lanes, schedule]
next: "Upgrade the host yolo (H0), then dispatch DRAIN-Q, GATE-5's ruling and LAND-3I in parallel; MM-1S follows DRAIN-Q in the run/drain spine"
---

# Tillr — delivery chunking: the remaining work as dispatchable lanes

**Status:** 2026-11-12. A delivery plan, not a design: every design claim below cites the
document that owns it, and every tree claim was re-read from the working tree on this date
(clean, on `main`). Where the coordinator's brief and the tree disagree, this plan follows
the tree and says so in [Section 2](#2-the-fact-base-re-read).

> **In short.** Most of the wave plan has landed, and the gate already selects every tillr
> module, so what remains is one serial spine (`run`/`drain`), two independent module lanes
> (`gate`, `item`), and one host action that every landing queues behind. The fastest
> honest order is three writers deep, never more: the gate is one serial resource, and a
> fourth writer only lengthens the landing queue.

**Terms.** A *chunk* *(coined here)* is one landable unit in this plan: one writer, one
worktree, one coherent commit, dispatched as one lane. *Lane* and *fence* are used as
[`tillr-next-steps.md`](tillr-next-steps.md) defines them: a lane is one dispatchable unit
of work, and a fence is the exact set of paths it may touch, with lanes in one module
directory serial even when their files are disjoint. *Backend*, *adapter*, *brief*,
*driver* and *write boundary* are used in the senses defined in
[`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#adapter-contract-and-write-boundary). The *gate* is `just done`,
the verifier of record, run by the coordinator at landing — never inside a lane, per
[`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#staged-lane-and-brief-contract).

## 1. The end state

"The rest of the tillr work is done" means both tiers below hold. Each clause is checkable
by the named measurement; a clause with no measurement is not part of the goal.

**E1 — the composed pipeline, supervised, on a real registry.** The eight steps of
[`board-utilities.md`](board-utilities.md) [§5.9](board-utilities.md#59-the-composed-pipeline)
run end to end on a fixture with three strengthenings over the stub case that already
passes (see [Section 2](#2-the-fact-base-re-read)):

- the gate plans from the real exported registry, in-process checks included, and returns
  verdict `pass` — not the `error`, exit 4 that
  [`tillr-next-steps.md`](tillr-next-steps.md)
  [Section 3](tillr-next-steps.md#3-the-design-decisions-still-open)'s interim assumption
  produces today;
- the `run.request` the drain emits names a `backend`, is executed through that backend's
  adapter (a stub harness in the test; one recorded real run per adapter outside it), and
  the terminal `run.record` carries the same `backend`;
- `land pass` moves the fixture trunk on that verdict, and `view status` reassembles the
  item, its runs with their backends, its candidate and its verdict from the same store.

*Measurement:* the extended composed-pipeline case (chunk PIPE-REAL) green inside `just done`,
plus `tillr-gate dualrun` classifying at least the empty evidence candidate `agree`, per GAP3
in `gap-analysis-2026-11-11.md`.

**E2 — repeatable machinery: Ember as a drain selection.** A second tick against the same
store dispatches again: a finished run's lane is released and reused under a stated rule, a
previously run item is planned as an explicit `retry` rather than held forever, an item
whose design document carries an open question holds with reason `question-open`, and one
tick may send different items to different backends under the policy — the Stage 4 shape
ruled in [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#backend-policy-selection-and-failover). Criterion state moves only on an
`item.verdict` (GAP1), and what the tick lands retires on the mechanical verdict its landing
produces, with no hand in the loop, per
[`work-control-judge-in-tillr.md`](work-control-judge-in-tillr.md)
([Retirement in the end state](work-control-judge-in-tillr.md#retirement-in-the-end-state)).
ITEM-V is therefore on E2's critical path. Agent-issued verdicts for completions with no
landing behind them are not part of E2: they wait for the GRANT chunk's store-enforced
sign-off grant, off this path, and until then those claims stand awaiting a verdict.

*Measurement:* the PIPE-REAL case extended to two ticks (dispatch, finish, release,
re-dispatch or retry, one `question-open` hold) green inside `just done`, and one supervised
real tick per adapter recorded by the driver under the checklist in
[`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#report-extraction-and-attempt-classification).

## 2. The fact base, re-read

The coordinator's brief is stale in five load-bearing places. Each row was re-read from the
tree on 2026-11-12; the plan is built on the right-hand column.

| Claim in the brief or an older plan | What the tree says |
| :--- | :--- |
| GATE-4 has not landed, so every tillr lane lands ungated | **Landed.** [`tools/fork-check.py`](../../tools/fork-check.py)'s `GO_TOOL_DIRS` lists all nine tillr modules beside `tools/pi-profiler` and `tools/mq`; `gap-analysis-2026-11-11.md` records the same. Tillr lanes are gated. The hazard is the inverse one, priced in [Section 7](#7-risks-and-owner-questions): while the gate is environmentally red, a chunk's own red cannot be told from the environment's |
| The wave plan's next lanes are still to dispatch | **Mostly landed.** Present and tested in the tree: ITEM-PKG, ITEM-2+3, LAND-1/2, GATE-3, RUN-2's read faces, DRAIN-1 (`plan` and `once`), VIEW-2R, and the `tillr` wrapper. Of [`tillr-next-steps.md`](tillr-next-steps.md) [Section 1](tillr-next-steps.md#1-the-next-wave-named), only the deferred halves remain: `question-open`, leases, in-process gate parity, DRAIN-2 and LAND-3 |
| The goal is the composed pipeline running on a fixture | **Already true, in stub form.** `TestTheComposedPipelineRunsEndToEndOnAFixture` in [`tools/tillr-drain/pipeline_test.go`](../../tools/tillr-drain/drain/pipeline_test.go) drives all eight steps as separate binary invocations — with `/bin/true` as the run command and a one-check registry whose in-process list is empty; run on 2026-11-12, it passes. E1 above is that case with the two stubs removed, which is why GATE-5 and MM-1S are on its path |
| Ember drain selection still waits on five settlements | **The settlements are ruled; the builds remain.** The gap analysis's Ember list predates the 2026-11-12 revision, which compacted [OQ-MM3](../reference/tillr-muse-backend.md#why-its-this-way) into per-item selection in the policy and [OQ-MM5](../reference/tillr-muse-backend.md#why-its-this-way) into no-sealing-before-unattended. The lease rules and the correction worktree are ruled in [`board-utilities.md`](board-utilities.md) [§5.6](board-utilities.md#56-run-alone) and [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#steering-continuation-and-session-modes) ([Section 14](board-utilities.md#14-decision-ledger), D-UT17): a correction keeps its lane's worktree. What still gates selection is building GAP4's lane lifecycle (chunk LEASE) and GAP2's `question-open` (chunk DRAIN-Q) |
| The gate cannot go green; the installed yolo predates the fix | **Confirmed for one suite.** `yolo --version` in this jail reads `0.17.1+196`, and running `yolo-packs/matt/tests/bedrock-context.test.mjs` on 2026-11-12 fails its `[1m]`-twin case. The brief names a second pack-suite red this plan did not run; [Section 7](#7-risks-and-owner-questions) carries the consequence. The trunk is `main`, not `master` — `master` survives only as a fixture trunk name inside tests |

## 3. The chunks

H0 is a host action, not a lane, and is listed because every landing queues behind it.
Sizes are estimates, labelled as such, with their basis; the module line counts behind
them were measured on 2026-11-12 (`tools/tillr-run` 1,745 lines, `tools/tillr-drain`
2,303, `tools/tillr-gate` 3,436, `tools/tillr-item` 3,085, `tools/mq` 14,521 across 41
files). S/M/L/XL mean, roughly: a handful of files in one module; one module's face plus
its fixtures; several new files or two modules; unpriced until an inventory exists.

| Chunk | One-line outcome | Fence (exact) |
| :--- | :--- | :--- |
| **H0** host yolo upgrade | The host runs a yolo at or past the `[1m]`-twin fix, so `just done` can go green | No repository files. Host only |
| **DRAIN-Q** (GAP2) | `drain plan` holds an item whose design document has an open question, with reason `question-open` | `tools/tillr-drain/**` |
| **MM-1S** muse substrate | `run.request` and `run.record` carry a required `backend`; drain stamps it from the policy; the `runs` row names its item | `tools/tillr-run/**`, `tools/tillr-drain/**`, [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#why-its-this-way) (`D-MM10` amended to two landings) |
| **MM-1A** the three adapters | Brief template, muse adapter, Pi and Agy Flash proof adapters, extractors and backend policy land per [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#shipped-adapters) | `tools/tillr-run/backends/**` (new), `config/tillr/backend-policy.json` (new), `tests/tillr-muse-backend.test.py` (new) |
| **GATE-5** (GAP3, question Q1) | `tillr-gate run` executes the in-process checks it plans, so a real candidate can pass | `tools/tillr-gate/**` |
| **ITEM-V** (GAP1) | Criterion state moves only on an `item.verdict`; frozen criterion text cannot be eased | `tools/tillr-item/**`, `tools/tillr/conformance/**`, `tools/tillr/record/**` |
| **GRANT** | A store-enforced delegated sign-off grant: `item` refuses an agent-issued verdict whose grant is absent, expired, out of scope, or self-judging | `tools/tillr-item/**`, `tools/tillr/conformance/**`, `tools/tillr/record/**` |
| **LEASE** (GAP4 remainder, question Q4) | A lane gets a lease and a release face; a previously run item plans as an explicit `retry` | `tools/tillr-run/**`, `tools/tillr-drain/**` |
| **SUB-2** (GAP6) | `env_refs` resolve through a substrate config loader; values reach the child and never a record | `tools/tillr/config/**` (new), `tools/tillr-run/**` |
| **MM-2** Stage 2 | A specimen request drives `tillr-run exec` against the echo provider end to end with no model spend | `tools/tillr-run/backends/**`, `tests/tillr-muse-backend.test.py` |
| **MM-3** Stage 3 | A feeder script hands drain's sink output to `tillr-run exec` in order; the cold-prefix measurement is run and recorded | `tools/tillr-run/backends/**`, `config/tillr/**` |
| **DRAIN-2A** subprocess runner | `drain once` can hand requests to a spawn-and-return runner that launches `tillr-run exec` outside the write transaction | `tools/tillr-drain/**` |
| **DRAIN-2B** selection and supervision | Per-item backend selection in the policy, per-item argv or the lane-directory convention made permanent, and the tick loop that consumes outcomes | `tools/tillr-drain/**` |
| **LAND-3I** inventory (GAP5) | The one-lander route is priced from an inventory of `tools/mq`, under the carrying ruling recorded in [`landing-principles.md`](landing-principles.md) [Section 3](landing-principles.md#3-gate-what-will-be-the-tip-once) | [`board-utilities.md`](board-utilities.md), [`landing-principles.md`](landing-principles.md), one new brief under `docs/design/` |
| **LAND-3B** one lander, built | Exactly one program can move the trunk for pipeline candidates | Set by LAND-3I; not dispatchable before it |
| **PIPE-REAL** the E1/E2 proof | The composed-pipeline case runs the real registry, a named backend and two ticks | [`tools/tillr-drain/pipeline_test.go`](../../tools/tillr-drain/drain/pipeline_test.go), [`tools/tillr-drain/drain/main_cli_test.go`](../../tools/tillr-drain/drain/main_cli_test.go) — tests only |

Two chunking decisions need their reasons stated once:

- **MM-1 is split into MM-1S and MM-1A, against the original first-slice "one lane, one landing" rule ([`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#why-its-this-way), `D-MM10`).**
  That fence — a substrate field, three adapter directories, a policy file and a test — is
  too wide to review as one commit, and its halves fail differently: the substrate half
  breaks fixtures across three modules, the adapter half is proved by a live canary. The
  split preserves the ruling's substance in
  [the decision table](../reference/tillr-muse-backend.md#why-its-this-way) — the field and all
  three adapters land before anything is grown on the shape, with no chunk dispatched
  between them — and MM-1S carries the one-line amendment (`D-MM10`) in
  [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#why-its-this-way) in its own fence so
  the deviation is recorded where the original stands. The item-to-run link from GAP4 is
  merged into MM-1S because both are one schema-and-record change in the same files: one
  migration and one fixture sweep, not two.
- **PIPE-REAL may touch no production file.** If the proof needs one changed, that is a
  defect in the owning chunk, charged back to it and re-landed there — the same discipline
  [`tillr-next-steps.md`](tillr-next-steps.md) [Section 2](tillr-next-steps.md#2-the-critical-path)
  set for swapping a fixture verdict for a gate-produced one and counting the non-test
  files that move. Zero is the passing number.

| Chunk | Preconditions | Verification it must pass | Size | Parallel? |
| :--- | :--- | :--- | :--- | :--- |
| H0 | None. The human upgrades the host and relaunches jails | `just done` on the host reaches a verdict on an unchanged tree; the `[1m]`-twin case passes. This plan cannot verify it from inside the jail — the jail's yolo and the host's can differ under one version string | Host action | Blocks every *landing*, no writing |
| DRAIN-Q | Item's question parser landed (yes: `ReadQuestions` in `tools/tillr-item/item/questions.go`), and `tools/tillr-drain/go.mod` already requires the item module (yes, re-read) | GAP2's criterion verbatim: a fixture item with an unanswered question holds as `question-open` with no request, dispatches once answered, `go test ./...` in `tools/tillr-drain` exits 0; then `just done` at landing | ESTIMATE S — basis: one planner input widened (`readItems` selects id and state today) plus a call into a parser that exists | Yes, against GATE-5 and ITEM-V. Serial against MM-1S, LEASE and DRAIN-2* (same module) |
| MM-1S | DRAIN-Q landed (fence order only; no code dependency) | A request with an empty or mismatched `backend` refuses before any lane is created; the field round-trips request → record → `runs` row; the row names its item; suites green in `tools/tillr-run`, `tools/tillr-drain` and `tools/tillr-view` (view copies run records and must not break on the new field); then `just done` | ESTIMATE M — basis: one field, one column, one stamp, and a fixture sweep across the three modules that mint requests | Serial in the `run`/`drain` spine. Parallel against GATE-5, ITEM-V, LAND-3I |
| MM-1A | MM-1S landed | [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#shipped-adapters)'s own proof: `python3 tests/tillr-muse-backend.test.py` (its negative control is the truncated, footerless fixture), the echo-provider refusal run, then one real run per adapter with the muse canary denied and the boundary snapshot clean, each landed through `just done`. A succeeding canary fails the landing | ESTIMATE L — basis: the adapter fence ([`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#shipped-adapters): a template, three adapters, three extractors, a policy and a test), three stream dialects, no existing code to extend | Serial after MM-1S (same module directory). Its live runs need a driver and spend model tokens |
| GATE-5 | The owner rules Q1 in [`tillr-next-steps.md`](tillr-next-steps.md) [Section 3](tillr-next-steps.md#3-the-design-decisions-still-open): Go port, shell-out, or nobody. This is the smallest blocking question in [Section 7](#7-risks-and-owner-questions) | GAP3's criterion: the ruling recorded, `tillr-gate dualrun` over the three evidence candidates of [`gate-dual-run-evidence.md`](gate-dual-run-evidence.md) [Section 6](gate-dual-run-evidence.md#6-what-a-retirement-decision-would-still-need) classifies at least the empty one `agree`, `go test ./...` in `tools/tillr-gate` exits 0; then `just done`. A shell-out ruling widens the fence to [`tools/fork-check.py`](../../tools/fork-check.py) and the chunk is re-priced before dispatch | ESTIMATE L, XL if a full port — basis: the in-process set is whatever the registry export names, `file-size` owed by every change, and no inventory of the set exists yet | Yes — the one fully independent code lane. Starts the day Q1 is ruled |
| ITEM-V | None. The MIGRATE ruling and the writer contract are recorded in [`work-control-judge-in-tillr.md`](work-control-judge-in-tillr.md) ([Retirement in the end state](work-control-judge-in-tillr.md#retirement-in-the-end-state)) and [`board-utilities.md`](board-utilities.md) [§5.3](board-utilities.md#53-item-alone) | GAP1's criterion verbatim: easing frozen criterion text refuses by name, a state change with no `item.verdict` refuses, a verdict naming the wrong criterion hash refuses, `go test ./...` in `tools/tillr-item` exits 0; then `just done` | ESTIMATE M — basis: one new record kind, one writer rule in `tools/tillr-item/item/apply.go`, one conformance case | Yes, against everything but a second `tools/tillr-item/**` writer, of which there is none |
| GRANT | ITEM-V landed | An agent-issued verdict with an absent, expired or out-of-scope grant refuses by name, a self-judging verdict refuses, a mechanical verdict needs no grant and lands without one, `go test ./...` in `tools/tillr-item` exits 0; then `just done` | ESTIMATE M — basis: one grant record kind, one intake check in `tillr item`, one conformance case | Serial after ITEM-V (same module). Off the E1/E2 path |
| LEASE | MM-1S landed. The lease rules are recorded in [`board-utilities.md`](board-utilities.md) [§5.5](board-utilities.md#55-drain-alone) and [§5.6](board-utilities.md#56-run-alone): the runner sets the lease at claim for `timeout_ms` plus grace on the store host's clock, the terminal record and `run release` release it, `drain once` reaps the expired, and a correction keeps its lane's worktree | GAP4's criterion: a finished run's lane dispatches again under the stated release rule, a previously run item plans as `retry`, the `runs` row names its item (landed in MM-1S, asserted here end to end), suites green in `tools/tillr-run` and `tools/tillr-drain`; then `just done` | ESTIMATE M — basis: one column already exists (`leased_until`, always `NULL` today), one release face, one planner action replacing a permanent hold | Serial in the spine, after MM-1S |
| SUB-2 | MM-1A landed — the trigger [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#why-its-this-way) names: a lane that must resolve `env_refs`. Not dispatchable earlier, because argv carries model data by design until then | GAP6's criterion: named values present in the child and absent from every emitted record, an unresolvable ref refuses before any lane is created, the `tools/tillr-run` suite exits 0; then `just done` | ESTIMATE M — basis: one new substrate package plus replacing a refusal that already names its own seam in `tools/tillr-run/run/exec.go` | Serial against other `tools/tillr-run/**` chunks; parallel against GATE-5, ITEM-V, DRAIN-Q |
| MM-2 | MM-1A landed, plus its move-on evidence recorded by the driver: three consecutive real items through the template with no improvisation absorbed afterward, one real item per proof adapter ([`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#why-its-this-way), Stage 1) | One run replayed against a fixture lane root with the echo provider producing a comparable `run.record`, and one real Ember run recorded end to end (Stage 2's own move-on condition); then `just done` | ESTIMATE S — basis: a specimen record and an end-to-end test over faces that already exist | Serial after MM-1A (same directory) |
| MM-3 | MM-2 landed and its move-on evidence recorded | A tick of items processed through one backend with dispatches and per-run backends recorded; the four-run cold-prefix measurement's number recorded with the lane's evidence, and acted on — a material cold prefix spawns conditional chunk **MM-3W** (the warm host inside the muse adapter, fence `tools/tillr-run/backends/muse/**`, ESTIMATE L) before DRAIN-2B dispatches | ESTIMATE M — basis: one script, one specimen policy, four measured runs | Serial after MM-2 |
| DRAIN-2A | LEASE and DRAIN-Q landed. The lane's lead writes the DRAIN-2 brief first, per [`tillr-next-steps.md`](tillr-next-steps.md) [Section 5](tillr-next-steps.md#5-the-spec-lanes) — DRAIN-1 has landed, so that brief is owed now, inside this chunk, not as a spec lane | A dispatch launches `tillr-run exec` and returns without holding the store's write transaction (the constraint [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#backend-policy-selection-and-failover) Stage 4 names); a runner that cannot launch records no dispatch as final; suite green; then `just done` | ESTIMATE M — basis: one runner beside `record-sink` in a module whose hand-over seam already exists | Serial in the spine |
| DRAIN-2B | DRAIN-2A landed; the four-run cold-prefix measurement acted on (inside MM-3, or standalone if MM-3 was skipped); SUB-2 landed if — and only if — E2 includes credential-by-environment; ITEM-V landed, because E2 retires what it lands on the mechanical verdict ([Section 1](#1-the-end-state)). GRANT is not a precondition | The planner refuses the whole plan on an unknown backend or an unknown item override, per [the decision table](../reference/tillr-muse-backend.md#why-its-this-way); a non-empty boundary-snapshot diff stops the tick before anything stages or lands; the two-tick half of PIPE-REAL passes against it | ESTIMATE L — basis: policy schema, planner selection, a supervision loop whose shape the brief must still fix | Serial in the spine; last code chunk on the E2 path |
| LAND-3I | None. Carrying is ruled and recorded in [`landing-principles.md`](landing-principles.md) [Section 3](landing-principles.md#3-gate-what-will-be-the-tip-once) and [`board-utilities.md`](board-utilities.md) [§5.4](board-utilities.md#54-land-alone): `tools/mq` carries today, `tillr-land` carries when its build lands | An inventory brief that prices LAND-3B's routes (absorb `tools/mq`, or retire `tillr-land` and keep `tools/mq` as the one lander) and states whether the `tillr-land` carry is built standalone or absorbed; [`landing-principles.md`](landing-principles.md) [Section 3](landing-principles.md#3-gate-what-will-be-the-tip-once)'s scope already names the two programs that exist | ESTIMATE S as a document — basis: an inventory read of a measured 14,521-line module, no code | Yes, any time; it contends only for a landing slot |
| LAND-3B | LAND-3I landed and priced | GAP5's criterion, second half: exactly one program can move the trunk for pipeline candidates afterward, named in [`landing-principles.md`](landing-principles.md)'s scope lines | ESTIMATE XL, unpriced — basis: measured `tools/mq` size above; any tighter number before the inventory would be invented | Unknown until priced; assume it serializes against landing work generally |
| PIPE-REAL | For E1: GATE-5, MM-1S and MM-1A landed. For E2's second tick: DRAIN-Q, LEASE and DRAIN-2B landed | [Section 1](#1-the-end-state)'s measurements, with zero production files changed in this chunk | ESTIMATE S–M — basis: one existing test file extended, no new machinery | Serial last in the spine |

## 4. The order

```text
H0 ── gates every landing below (writing is unblocked)

Q1 ruling ──> GATE-5 ───────────────┐
ITEM-V ──> GRANT (off the E1/E2 path)
LAND-3I ──> LAND-3B (priced by 3I; off the E1/E2 path)

DRAIN-Q ──> MM-1S ──> MM-1A ──> MM-2 ──> MM-3 ──> [MM-3W if measured]
                 └────> LEASE ──> DRAIN-2A ──> DRAIN-2B
                 └────> SUB-2 ──────────────┘ (E2 only, if credentials by env)

GATE-5 + MM-1A ──> PIPE-REAL (E1) ; + DRAIN-Q + LEASE + DRAIN-2B ──> PIPE-REAL (E2)
```

**Ready today**, needing no ruling and no predecessor: DRAIN-Q, ITEM-V and LAND-3I.
**Ready the moment the owner answers one question:** GATE-5 (question Q1). Everything
else waits on a named landing in its own fence.

**The critical path to E1** is GATE-5 in parallel with the spine DRAIN-Q → MM-1S → MM-1A →
PIPE-REAL, and GATE-5 is its long pole: it is the only chunk whose size the ruling itself
decides, and until it lands every real candidate's Go verdict is `error` while the Python
gate stays the verifier of record — which is tolerable for hand landings and fatal to E1's
first clause. **The critical path to E2** extends the spine through LEASE → DRAIN-2A →
DRAIN-2B, with ITEM-V in parallel: E2's tick retires what it lands on the mechanical verdict,
so DRAIN-2B cannot close E2 before ITEM-V lands, and GRANT is no part of that path. MM-2 and
MM-3 sit off that path (they remove hand steps; DRAIN-2A removes the same hand step
permanently), so if the coordinator must choose, the spine's `run`/`drain` writer goes
MM-1A → LEASE → DRAIN-2A and MM-2/MM-3 dispatch only into spine gaps — their move-on evidence
(real runs, recorded by the driver) is what actually paces them, not code.

**Against the wave plan:** its order was right and was followed — Group A landed first,
Group B and C behind it, and its one structural move (deleting the GATE-3 → LAND-2 edge in
[`tillr-next-steps.md`](tillr-next-steps.md) [Section 2](tillr-next-steps.md#2-the-critical-path))
is confirmed by the stub pipeline case passing with a gate-produced verdict in the loop.
Three things change, each forced since 2026-11-10. First, A-NOQ has expired: the parser
landed, so DRAIN-Q is dispatchable now rather than a follow-up of indefinite date. Second,
the pre-graduation Muse backend design ([`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#why-its-this-way)) claimed
no lane is displaced by the muse insertion, and in fence terms that is not quite
true: MM-1S and LEASE share the `tools/tillr-run/**` and `tools/tillr-drain/**` fences, so
the owner's priority bump puts MM-1S physically ahead of the RUN-2 → DRAIN-2 chain the wave
plan drew. The cost is one serial slot, not a wave. Third, VIEW-2's store-mode remainder
appears already served — `tools/tillr-view` assembles the subject from both a store
snapshot and a record stream through one code path — so no VIEW chunk is scheduled; MM-1S
verifies that claim by running view's suite against the widened record, and a failure there
re-opens a VIEW chunk at M size rather than being absorbed silently.

## 5. The parallelism plan

**Three writers at most, and usually two.** The binding resources are not writers: they are
the one gate (a single record lock, so landings and their `just done` runs queue serially
whatever the writers do) and the same-module rule. A fourth concurrent writer buys a longer
landing queue and staler bases to rebase, and no wall-clock.

| Dispatch wave | Writers running together | The coordinator waits on |
| :--- | :--- | :--- |
| Wave 1, today | DRAIN-Q; LAND-3I as a document lane; GATE-5 the day Q1 is ruled | H0 for the first landing of anything. Until then writers produce commits on their branches and the queue only grows — so H0's clock, not writer speed, sets the first landing date |
| Wave 2 | MM-1S (spine) beside GATE-5 and ITEM-V | Landing slots. Land in unblocking order: MM-1S, then DRAIN-Q if it somehow trails, then GATE-5, then ITEM-V — each cherry-picked onto `main` one at a time, `just done` after each, the next candidate rebased before its gate run |
| Wave 3 | MM-1A (spine) beside GATE-5's remainder; then LEASE takes the spine | MM-1A's live runs: one driver, serialized by construction, spending model tokens. This wait is elapsed time no writer count changes |
| Wave 4 | DRAIN-2A, then DRAIN-2B (spine); MM-2/MM-3 only into spine gaps; SUB-2 ahead of DRAIN-2B if E2 needs it; GRANT only into an idle `tillr-item` slot, never ahead of the spine | DRAIN-2B's brief, written inside DRAIN-2A, and ITEM-V's landing before DRAIN-2B can close E2 |
| Last | PIPE-REAL (E1 half as soon as GATE-5 and MM-1A have landed; E2 half after DRAIN-2B) | Nothing. A red here is a defect charged to an owning chunk, per [Section 3](#3-the-chunks) |

Standing mechanics for every wave: each lane gets its own `git worktree add --lock` under
the durable scratch directory (never `/tmp`), commits there and exits; the coordinator
alone cherry-picks serially onto `main` and runs `just done` per landing. No fork-series
lane runs beside any of this: no chunk here touches a patch series, but every landing's
gate replays the shared `.forks/` checkouts, so a concurrent fork-series export would make
a gate verdict describe a series nobody landed. Lane scratch and evidence stay in the
durable scratch directory, and tracked documents state the command, the result and the
date rather than citing it.

## 6. Deferred or dropped

| Item | Disposition and reason |
| :--- | :--- |
| SUB-3, the gate's store-named mode | **Deferred indefinitely**, as [`tillr-next-steps.md`](tillr-next-steps.md) [Section 4](tillr-next-steps.md#4-what-to-delete-or-stop) ruled: plans and verdicts already travel as records, and `gate_plans`/`gate_verdicts` tables would be a second copy. Returns only behind a named consumer that must query verdicts inside a store |
| LAND-3B until LAND-3I prices it | **Deferred behind its inventory.** E1 and E2 land fixture candidates through `tillr-land` and real ones through `tools/mq` today. Carrying is ruled for both ([`landing-principles.md`](landing-principles.md) [Section 3](landing-principles.md#3-gate-what-will-be-the-tip-once)); `tools/mq` enforces it today, and whether the `tillr-land` carry is built standalone or the consolidation retires the need for it is LAND-3I's pricing question. Absorbing a measured 14,521-line lander unpriced is the opposite of the owner's pare-back direction — retiring `tillr-land` in favour of `tools/mq` may be the cheaper one-lander route, and only the inventory can say |
| Landing velocity's remaining slices (the rolling tier's consumers) | **Deferred.** The tier machinery is built in the Python gate and has no tillr consumer; [`landing-velocity.md`](landing-velocity.md) [Section 12](landing-velocity.md#12-what-this-does-not-license) forbids wiring it to a lander before the one-lander consolidation supplies the watermark machinery, so these wait behind LAND-3B by their own ruling |
| GAP7, re-stamping the deleted board's accepted shelf | **Deferred under the owner's hold on the document pass.** The gap is real — accepted documents still present a deleted system as settled — but no chunk here is dispatched from that shelf, and this plan schedules no document pass by direction |
| SPEC-LAND and SPEC-DRAIN as lanes | **Dropped**, per [`tillr-next-steps.md`](tillr-next-steps.md) [Section 5](tillr-next-steps.md#5-the-spec-lanes): each lane's lead writes its brief as its first task. The DRAIN-2 brief rides inside DRAIN-2A; the LAND-3 inventory *is* LAND-3I |
| A machine-read capability set, a namespace wrapper, selection by item kind or label | **Deferred by their owning rulings:** capabilities stay documented per adapter until a supervisor must key policy on one, the wrapper is not a precondition for unattended running, and selection keys finer than item id wait for a consumer that names the key — all in [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#adapter-contract-and-write-boundary) and [backend policy selection](../reference/tillr-muse-backend.md#backend-policy-selection-and-failover) |
| RET-* retirements, MISC-2, WRAP-1 | **Moot.** The monolith these retired pieces of was deleted outright on 2026-11-11, and the wrapper the wave plan deferred is in the tree. Nothing replaces any of them because nothing needs to |

## 7. Risks and owner questions

| Risk | What it costs and what contains it |
| :--- | :--- |
| H0 drags | Every chunk can be written and none can be landed with a trustworthy verdict, because `just done` stays red for an environmental reason. The failure mode to refuse is landing on module-suite green alone while calling it gated: tillr modules *are* gate-selected now (see [Section 2](#2-the-fact-base-re-read)), so a red gate that cannot distinguish the chunk's failure from the environment's makes any such landing ungated in fact. Containment: queue commits, rebase them as they wait, and land nothing until H0's check passes on an unchanged tree. The brief's second pack-suite red was not run for this plan; H0's verification must find it, or find it gone, before the first landing |
| MM-1S breaks every existing request at once | Making `backend` required invalidates every fixture that mints a `run.request` today, in three modules at once, and a request drain emits that `run` then refuses would be a pipeline that dispatches into a wall. Containment is structural: the field, drain's stamp and the fixture sweep are one chunk by design, and the gate now builds all nine tillr modules, so a missed fixture is a red landing, not a silent one |
| GATE-5 is the plan's largest unknown | Its size is whatever the in-process check set turns out to be, and no inventory of that set exists. Its ESTIMATE is the softest number in [Section 3](#3-the-chunks), and it sits on E1's critical path. Containment: the Q1 ruling is asked first, and an inventory of the exported in-process set is GATE-5's first task, re-pricing the chunk before its fence closes |
| The live-run evidence cannot be suite-verified | MM-1A's canary, MM-2 and MM-3's move-on conditions and E2's real tick are driver-recorded runs, not tests. A coordinator can check that the artifact set exists and diffs clean; the judgment that a template needed no improvisation is a person's. The plan names these as recorded evidence with dates precisely so they are not mistaken for measurements a suite made |
| ITEM-V without a sign-off grant | ITEM-V enforces *that* a verdict is required and *which* criterion it names; it does not enforce *whose* agent-issued verdict counts, because no grant mechanism exists in the store. That is safe for E2 only under the split in [Section 1](#1-the-end-state): E2's verdicts are mechanical and need no grant, and no agent-issued verdict is counted until GRANT lands. The risk to refuse is crediting an agent-issued verdict on convention in the meantime — a claim with no landing behind it stands awaiting a verdict, however inconvenient that queue looks |
| One gate, measured by nobody yet | No `just done` duration for a tillr-only change was measured for this plan, so no landing-queue arithmetic is offered. The coordinator should time the first two landings and re-plan writer counts from that number, not from this document |

**The smallest question the owner must still answer** is filed in its owning document:

| # | Question | Blocks |
| :--- | :--- | :--- |
| 1 | Q1: who executes in-process gate checks in `tillr-gate run` — a Go port, a shell-out, or nobody? Filed in [`tillr-next-steps.md`](tillr-next-steps.md) [Section 3](tillr-next-steps.md#3-the-design-decisions-still-open) | GATE-5's dispatch and price; E1's first clause |

No other chunk waits on an unmade decision. Where a chunk's brief still needs writing
(DRAIN-2 above all), the facts it needs are produced by its named predecessors, which is
the one case [`tillr-next-steps.md`](tillr-next-steps.md)
[Section 5](tillr-next-steps.md#5-the-spec-lanes) already priced.
