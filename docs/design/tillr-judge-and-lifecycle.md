---
title: "The Item Judge and Lifecycle in Tillr"
status: accepted
stage: DECIDED
updated: 2026-11-16
tags:
  - tillr
  - judge
  - verdict
  - lifecycle
  - state-machine
  - grants
  - drain
  - design
summary: "How pre-approved item graphs with evaluator nodes and opt-in generic judging issue grant-backed item.verdict records, block mechanical retirement of owner-only criteria, enforce an owner-disposition state machine alongside derived execution state, and sunset -v2 failure-cloning scaffolding."
next: "Build the first block of the build order: a gate pass no longer retires a criterion only the owner can settle."
depends-on:
  - tillr-item-graphs.md
  - board-item-state-model.md
  - work-control-judge-in-tillr.md
  - escalation-ladder.md
---

# The Item Judge and Lifecycle in Tillr

**Status:** 2026-11-16. Decided, and cut into the [build order](#8-build-order) that ends this page. Sections 1 to 5 were verified on 2026-11-14 against `tools/tillr-item/`, `tools/tillr-drain/`, `tools/tillr-land/`, `tools/tillr-view/`, `tools/tillr-loop/tillr-loop`, and `config/tillr/seed-items.json` as the tree stood that day. The tree has moved since: [Section 7](#7-what-the-tree-already-holds) measures what is built at `bdd6b3c82`, and where it and an earlier section disagree about a name or a mechanism, Section 7 and the build order are what a builder follows.

> **In short.** Put independent judgment inside pre-approved item graphs ([tillr-item-graphs.md](./tillr-item-graphs.md)) as a read-only `evaluator_slot` leaf **before** `tillr-land pass` so bad candidates never move `main`, trust `tillr-gate` + `tillr-land` without a second judge pass when the graph already judged, and keep standalone generic judging over the exact same intake contract for non-pipeline completions and opted-in single-node items. Store `claim_class` on `item_criteria` so `actor_kind: "mechanical"` can never retire `owner-only` or `direction` criteria, restrict `items.state` to owner disposition while deriving `working` from live runs beside `mayBeARetry` holds, stop writing `not-done` criterion freezes on candidate failures, and retire `ActorAutoQA` / `qa-verification` / `DecidedByAutoQA` so external AutoQA uses standard `mechanical` and grant-backed `agent` verdicts.

**Why it matters.** Three live defects stem from one missing seam between `tillr-drain`, `tillr-loop`, and `tillr-item`:

1. **Mechanical stamping of owner rulings (`8778c98ace`).** `compose_verdict` (`tools/tillr-loop/tillr-loop`) hashes criterion text without reading it and stamps every criterion `done` (`actor_kind: "mechanical"`, `claim_class: "verification-only"`) on gate exit `0`. When `item-pile-507-allowlist-hunt` left `blocked` in `b5882ce2df` still carrying `c1: "Owner decision recorded..."`, a 5.6 s Markdown gate check retired an owner decision mechanically; **9 blocked owner-ruling rows** (plus `item-host-render-pi-roadmap`) in `config/tillr/seed-items.json` still carry that exact `c1` pattern.
2. **Premature criterion freeze and bricked item IDs.** `failCandidate` (`tools/tillr-drain/drain/loop.go`) writes `not-done` `item.verdict` records and `items.state = "in-progress"` on every non-landing candidate. Because `carryCriteria` (`tools/tillr-item/item/apply.go`) freezes criterion text once `state != ""` and `AuthorizeRetry` (`tools/tillr-drain/drain/retries.go`) refuses gate-judged and timed-out runs, unjudged spec blocks (`item-or-routing`, `item-seat-bounds-wiring`) and gate failures (`item-land-3b` at `98cc8cfec4` `[unlanded]`, `item-bar-ironbar-config` at `207a0699d4` `[unlanded]`, `item-obs-pause` at `7327b9704c` `[unlanded]`) could not be repaired in place — forcing `-v2` item clones, manual edge repointing, prose `decision.record` pointers, and `resolved-elsewhere` tombstones.
3. **Un-granted AutoQA bypass.** `ActorAutoQA` (`tools/tillr-item/item/verdict.go`) bypasses `checkDelegation`, licensing expiry-free, grantor-free agent sign-off under client-specific store enums that violate [board-utilities.md](./board-utilities.md) `D-UT15`.

**The shape.** One `eval.output` -> `item.verdict` settlement contract shared by in-graph `evaluator_slot` leaves and opt-in standalone judging; `item_criteria.claim_class` enforced in `RecordVerdict` and `IssueGrant`; a six-state stored owner-disposition machine (`in-progress` retired from storage, SM-3 pointers/reasons enforced) paired with derived `working` and explicit `tillr drain rework`; and client-identity attribution via `verdict.actor` in place of `ActorAutoQA`.

**Cost.** Additive columns on `items` (`waiting_on`, `underlying_state`, `resolves_via`, `close_reason`, `closed_at`, `closed_by`, `closed_from`) and `item_criteria` (`claim_class`), a settlement intake pass in `tillr-drain`, `close`/`reopen`/`rework` CLI faces, and backfilling `claim_class: "owner-only"` + SM-3 metadata in `config/tillr/seed-items.json`. Zero new utilities.

**Needs your ruling:** None.

**Next:** build [the first block](#prevent-a-gate-pass-from-retiring-an-owners-criterion) of the build order. It waits on nothing.

**Reads with:** [tillr-item-graphs.md](./tillr-item-graphs.md) ([§3.3](./tillr-item-graphs.md#33-producer-bound-artifacts-exit-conditions-and-the-evaluator-slot) and [§6](./tillr-item-graphs.md#6-boundary-with-judge-policy-and-item-lifecycle), fixed upstream graph and `evaluator_slot` contract), [board-item-state-model.md](./board-item-state-model.md) ([§2](./board-item-state-model.md#2-classification-of-every-status-the-board-can-show)–[§8](./board-item-state-model.md#8-implementation-sequence), closed seven-state projector and SM-3 records), [work-control-judge-in-tillr.md](./work-control-judge-in-tillr.md) ([Ruling: MIGRATE](./work-control-judge-in-tillr.md#ruling-migrate) and [Retirement in the end state](./work-control-judge-in-tillr.md#retirement-in-the-end-state)), [agent-runtime.md](./agent-runtime.md) ([§12](./agent-runtime.md#12-the-three-owner-rulings)–[§14](./agent-runtime.md#14-adopting-guilds-graph-algebra), [§28](./agent-runtime.md#28-authority-not-approval), [§30](./agent-runtime.md#30-a-cut-off-is-evidence-about-fit-never-about-priority)), [escalation-ladder.md](./escalation-ladder.md) ([§3](./escalation-ladder.md#3-the-ladder)–[§5.2](./escalation-ladder.md#52-the-fence-what-a-lane-may-not-edit)), [criterion-repair.md](./criterion-repair.md), and companion design `` `docs/design/tillr-autoqa.md` ``.

---

## 1. Existing Machinery and Where the Loop Bypasses It

Before adding lifecycle or judge rules, surveying `tools/tillr*` shows that `tillr-item` already implements most of the delegated-verdict contract, while `tillr-loop` and `tillr-drain` route around it:

| Capability | Existing code or document | Current scope and why it is insufficient |
| :--- | :--- | :--- |
| **Delegated verdict & grant intake** | `tools/tillr-item/item/verdict.go` (`RecordVerdict`, `checkIndependence`, `checkDelegation`); `grant.go` (`IssueGrant`) | Enforces `CriterionHash(text)`, non-empty `evidence`, `Actor != JudgedActor` (for `agent`/`autoqa`), and unexpired scoped `item_grants` (for `agent`). **Gap:** `claim_class` lives only on the incoming verdict, not on `item_criteria`, so `actor_kind: "mechanical"` retires any criterion by claiming `verification-only`, and `IssueGrant` permits `owner-only` in grants. |
| **Mechanical retirement adapter** | `tools/tillr-loop/tillr-loop` (`compose_verdict`) | Stamps every criterion `done` on `--verdict pass` and `not-done` on `--verdict fail` with `actor_kind: "mechanical"`, `claim_class: "verification-only"`. **Gap:** Never checks criterion `claim_class` and stamps `not-done` on unjudged gate/attempt failures (`8778c98ace`). |
| **Criterion text & state freeze** | `tools/tillr-item/item/apply.go` (`carryCriteria`) | Refuses `item apply` edits to `item_criteria.state` and freezes `text` whenever `stored.State != ""`. **Gap:** Coupled with `failCandidate`'s `not-done` write, any failed attempt freezes broken criteria before a judge ever reads them. |
| **Item state writes & checks** | `tools/tillr-item/item/apply.go` (`checkState`), `state.go` (`SetState`); `tools/tillr-land/land/attempt.go` | `checkState` is a 7-string membership test; `SetState` runs an unconditional `UPDATE items SET state = ?`. **Gap:** Zero `from -> to` rules, no land receipt check on `landed`, no SM-3 pointer on `resolved-elsewhere`, no `close`/`reopen` verbs, and `tillr-land` never checks if an item is `closed`. |
| **Candidate failure & retry holds** | `tools/tillr-drain/drain/loop.go` (`settle`, `failCandidate`), `plan.go` (`decide`, `mayBeARetry`), `retries.go` (`AuthorizeRetry`), `failover.go` (`ClassifyAttempt`) | `ClassifyAttempt` checks `strings.Contains(log, "RESULT:")` without parsing `RESULT: blocked`; `failCandidate` writes `StateInProgress` (`ReasonNotReady` in `plan.go`); `AuthorizeRetry` refuses gate-judged and `AttemptUnjudged` runs with no rework verb on the same `item_id`. |
| **Display state projector** | `tools/tillr-view/serveread.go` (`ProjectItemState`), `counts.go` (`boardCountsOf`) | Single 7-state projector per [board-item-state-model.md](./board-item-state-model.md) [§7](./board-item-state-model.md#7-the-structural-rule-one-projector-a-closed-enum). **Gap:** Maps stored `in-progress` unconditionally to `"working"` when zero runs are live, omits verdict `actor` on read surfaces, and `Dispatchable` ignores `mayBeARetry`. |
| **AutoQA store enums** | `tools/tillr-item/item/verdict.go` (`ActorAutoQA`), `grant.go` (`ClaimQAVerification`), `decision.go` (`DecidedByAutoQA`); [autoqa-tillr-seams.md](./autoqa-tillr-seams.md) (`D-AQA1`) | Added 2026-11-13 explicitly *"without a designer pass: collected for post-hoc designer advice"* ([OQ-AQA1](./autoqa-tillr-seams.md#OQ-AQA1)). **Gap:** Exempts an automated agent reviewer from `item_grants` and adds client-specific store enums. |

---

## 2. Judge Nodes Inside Pre-Approved Graphs vs Surviving Generic Judging

### 2.1 Topology Relative to Gate and Land (`D-JL1`)

Per [tillr-item-graphs.md](./tillr-item-graphs.md) ([§3.3](./tillr-item-graphs.md#33-producer-bound-artifacts-exit-conditions-and-the-evaluator-slot), [§4.2](./tillr-item-graphs.md#42-worktree-sequencing-at-single-lane-concurrency), `D-IG8`, `D-IG9`), intra-graph landing is forbidden and `Loop.Verify` runs at most once at root graph exit. In any pre-approved graph carrying an `evaluator_slot` (such as `build-judged@v1` or `design-draft@v1`), the judge leaf sits **topologically after all `access: "write"` leaves and any in-graph preflight `check` leaves, and strictly before root `exit` and `Loop.Verify` (`tillr-gate` -> `tillr-land pass`)**:

```
[write leaves: edit, pack-sync] ──► [optional check leaf (command, read_only)]
                                              │
                                              ▼
                                    [evaluator_slot leaf (agent, read_only)]
                                              │ emits .tillr/loop/<run_id>/artifacts/eval.output
                     ┌────────────────────────┴────────────────────────┐
          outcome == "fail"                                  outcome == "pass" (all criteria `done`)
    (not-done | blocked | defective)                                   │
                     │                                                 ▼
                     ▼                                       Serial `Loop.Verify`
     Settlement Hook (NO Gate, NO Land;                      1. `fenceViolation` (outer `AllowedPaths`)
     `main` untouched; lane reset)                           2. `tillr-gate` (`gate.verdict`)
       • `not-done`  -> `in-queue` (held `eval-failed`,      3. `tillr-land pass` (fast-forward or carry)
                        `item_criteria` untouched)           4. Settlement Hook commits `done`
       • `blocked`   -> `blocked` (`waiting_on` set,            `item.verdict` records (citing landed sha
                        `item_criteria` untouched)              + `gate.verdict`) -> `SetState(landed)`
       • `defective` -> `item.verdict(defective)` +
                        `Close(item, "defective: ...")`
```

**Why pre-land evaluation paired with post-land verdict commit is mandatory:**
- Running the judge **before** `Loop.Verify` catches spec drift, unaddressed clauses, and `defective` premises in the scratch lane worktree before `tillr-gate` spends full suite wall time and before `tillr-land pass` moves `main`.
- Committing `done` `item.verdict` rows to `.tillr/state.db` **only after `tillr-land pass` succeeds** prevents [board-item-state-model.md](./board-item-state-model.md) [§2](./board-item-state-model.md#2-classification-of-every-status-the-board-can-show)'s pre-land completion leak ([OQ-1](./board-item-state-model.md#9-open-questions)). If `tillr-gate` fails or `tillr-land pass` isolates on a carry conflict after the judge passed, **zero** `item.verdict` rows are written and the item stays `in-queue` (held as `gate-failed` or `land-isolated`). When `tillr-land pass` carries (`Reapply`) onto an advanced `main`, `tillr-land` has already verified tree/patch equivalence (`D-UT18`), so the settlement hook binds both `evaluated_head` (candidate sha) and `landed: [<trunk_after>]` onto each `done` `item.verdict`.

**Judge leaf output contract and deterministic settlement intake.** Lanes never open `.tillr/state.db` directly. The `evaluator_slot` leaf (`role: "evaluator"`, `access: "read_only"`) writes `.tillr/loop/<run_id>/artifacts/eval.output` validated by `tillr-drain`'s settlement hook before calling `RecordVerdict`:

| `eval.output` / intake field | Requirement enforced by `tillr-drain` + `RecordVerdict` |
| :--- | :--- |
| **`evaluated_head` & `evaluated_tree`** | Must match `Candidate.HeadSHA` and `Candidate.HeadSHA^{tree}` (or `item_base_sha` when `requires_candidate == "no_commit"`). |
| **`verdicts[]` completeness** | Exactly one entry per stored criterion of `item_id`, each carrying `criterion_id`, `criterion_hash == CriterionHash(frozen_text)`, `verdict` in `{done, not-done, blocked, defective}`, `claim_class`, non-empty `reason`, and non-empty `evidence[]` (artifact/diff/commit locators, never prose-only). |
| **Provenance-stamped `actor` & `judged_actor`** | Never trusted from model output. `tillr-drain` stamps `actor = "<evaluator_node_id>:<evaluator_run_id>:<evaluator_route>"` and `judged_actor = "<writer_run_ids>:<writer_routes>"` from W1's `node_runs` table ([tillr-item-graphs.md](./tillr-item-graphs.md) [§6](./tillr-item-graphs.md#6-boundary-with-judge-policy-and-item-lifecycle)), refusing any shared `run_id` or session handle (`checkIndependence`; [OQ-JL2](#61-decision-ledger)). |
| **Store-enforced `grant_id`** | Resolved from active `item_grants` (`tillr-item/item/grant.go`); `RecordVerdict` refuses missing, expired, or out-of-scope grants (`grant-absent`, `grant-expired`, `grant-scope`) inside the SQLite transaction. |
| **Multi-criterion aggregation** | `outcome == "pass"` iff **every** criterion is `done` (any claim of `"pass"` with a non-`done` entry is refused as malformed intake). On `"fail"`, precedence is: any `defective` -> record `defective` verdict(s) and `Close` item (`close_reason: "defective: <id>: <reason>"`); else any `blocked` -> move item to `blocked` with structured `waiting_on` from the judge's blocker payload; else (`not-done`) -> keep item `in-queue` held as `eval-failed` with `eval.output` indexed on the run. |

### 2.2 When Generic Judging Survives — and Closing the Mechanical-Stamp Hole (`D-JL2`)

In-graph judging and generic retirement partition cleanly without duplicate judge passes:

1. **Pre-approved graph with `evaluator_slot != nil` (`build-judged@v1`, `design-draft@v1`):** No generic judge runs after `Gate`/`Land`. The pre-exit `evaluator_slot` leaf already judged the criteria; `Gate` + `Land` verify and publish the tree, and the settlement hook commits the pre-exit `done` verdicts.
2. **Graph with `evaluator_slot == nil` (`single-worker@v1`) and pure `verification-only` criteria:** By default ([OQ-JL1](#61-decision-ledger)), `Gate` + `Land` exit `0` triggers mechanical retirement (`actor_kind: "mechanical"`, `claim_class: "verification-only"`, citing `gate.verdict` + `land.land`) at zero judge token spend, matching [work-control-judge-in-tillr.md](./work-control-judge-in-tillr.md).
3. **Opt-in and non-pipeline generic judging (same `evaluator` role, outside the build graph):** Generic judging survives as a standalone read-only `evaluator` invocation over the **exact same `eval.output` intake contract** — never a second codebase and never an automatic Pi turn-end hook ([work-control-judge-in-tillr.md](./work-control-judge-in-tillr.md) `Ruling: MIGRATE`) — in four cases:
   - `single-worker@v1` items carrying `judge_mode: "agent"` or a delegable non-mechanical criterion (`direction`, `red-gate-triage`) under an active `item.grant`;
   - foreground or out-of-band commits on `main` (`tillr item judge --item <id>`), where no drain pipeline landing occurred;
   - pre-dispatch premise triage (`requires_candidate: "no_commit"`) to retire a stale item as `defective` without running a worker; and
   - post-hoc audit sampling of mechanically retired items ([agent-runtime.md](./agent-runtime.md) [§12](./agent-runtime.md#12-the-three-owner-rulings)).

**Mechanically preventing `actor_kind: "mechanical"` from retiring `owner-only` or `direction` criteria.** Today `claim_class` is chosen by the caller inside `verdictPayload` (`verdict.go`), so `compose_verdict` overwrites every criterion's intent with `"claim_class": "verification-only"`. Three store-and-planner rules close the `8778c98ace` hole permanently:

1. **Store `claim_class` on `item_criteria`:** Register `claim_class TEXT NOT NULL DEFAULT 'verification-only'` on `item_criteria` (`schema.go`). In `carryCriteria` (`apply.go`), `claim_class` may harden monotonically (`verification-only` -> `red-gate-triage` / `direction` -> `owner-only`) per [criterion-repair.md](./criterion-repair.md) [§1](./criterion-repair.md#1-the-invariant-the-freeze-protects) `P1`, and may never be weakened through `item apply`.
2. **Enforce Actor × `ClaimClass` compatibility in `RecordVerdict` and `IssueGrant`:** `RecordVerdict` loads `(text, storedClaimClass)` from `item_criteria`, refuses `payload.ClaimClass != storedClaimClass` (`claim-class-mismatch`), and enforces actor authority before writing:

   | Stored `item_criteria.claim_class` | `actor_kind: mechanical` | `actor_kind: agent` | `actor_kind: human` |
   | :--- | :--- | :--- | :--- |
   | `verification-only` | **Allowed** (with `gate.verdict` + commit receipt) | **Allowed** (`checkIndependence` + unexpired `item.grant`) | **Allowed** |
   | `red-gate-triage` | **Refused** (`claim-class-actor`) | **Allowed** (`checkIndependence` + `item.grant` naming `red-gate-triage`) | **Allowed** |
   | `direction` | **Refused** (`claim-class-actor`) | **Allowed** (`checkIndependence` + `item.grant` naming `direction`) | **Allowed** |
   | `owner-only` | **Refused** (`claim-class-actor`) | **Refused** (`claim-class-actor`; `IssueGrant` also refuses `owner-only` as `grant-owner-only`) | **Allowed** (sole writer) |

3. **Backfill seed rows, refuse untyped owner prose, and hold in `drain plan`:** Backfill `"claim_class": "owner-only"` onto `c1` of all 9 blocked owner-ruling rows (`item-claude-whale-counterpart`, `item-oq-1-judge-refusal`, `item-oq-cr2-supersede`, `item-oq-mqr-retirement`, `item-oq-sr1-stranding`, `item-oq-st1-trigger-count`, `item-oq-td-pack-layout`, `item-ruling-approver-job`, `item-ruling-blocked-resume`) plus `item-host-render-pi-roadmap` in `config/tillr/seed-items.json`. `Apply` refuses any `verification-only` criterion matching `(?i)^owner decision recorded\b` (`owner-criterion-untyped`), and `drain/plan.go` holds any `in-queue` item with an unretired `owner-only` criterion as `owner-only-owed` so premature unblocking (`b5882ce2df`) spends zero lane tokens.

---

## 3. The Owner-Disposition State Machine and Derived Execution State

### 3.1 Stored Owner Disposition vs Derived Execution Projection (`D-JL3`)

Reconciling [board-item-state-model.md](./board-item-state-model.md) ([§2](./board-item-state-model.md#2-classification-of-every-status-the-board-can-show), [§6](./board-item-state-model.md#6-blocked-modifier-in-the-store-a-state-on-the-board), [§7](./board-item-state-model.md#7-the-structural-rule-one-projector-a-closed-enum)) with `tillr-item` separates **stored owner disposition** (`items.state`) from **derived execution activity**:

- **`in-progress` is retired from `writableStates`:** Moving `"in-progress"` to `frozenStates` (`Rejectionf("state-derived", ...)`) eliminates the split-brain where `state.go` called it *"Being worked on"* while `plan.go` held it as `ReasonNotReady` and `serveread.go` rendered stopped items as `"8 working"` over zero live lanes. Existing unretired `in-progress` rows migrate to `in-queue`.
- **Six stored `items.state` dispositions:** `not-started` (draft, un-admitted), `in-queue` (admitted to autonomous dispatch), `blocked` (held on structured `waiting_on` over `underlying_state` in `{not-started, in-queue}`), `landed` (this item's work landed), `resolved-elsewhere` (intent satisfied via `resolves_via` pointer), and `closed` (taken out of work with `close_reason`, `closed_at`, `closed_by`, `closed_from`; reopenable). Legacy `abandoned` and `deleted` stay frozen read-only.
- **One exhaustive projector (`ProjectItemState` in `tools/tillr-view/serveread.go`):** Emits the closed seven-state display enum `not_started | in_queue | working | blocked | landed | resolved_elsewhere | closed`. `working` is emitted **iff** the item is non-terminal, unblocked, and owns a live lease in `liveRuns`, an active multi-node `item_graphs` epoch with running leaves, or an active `land.candidate` in `Loop.Verify` (annotating merge-queue verification as a sub-stage of `working` while keeping `in_queue` unambiguous for queued backlog items). Unmet `blocked_by` edges, `question-open` design holds ([tillr-idea-pipeline.md](./tillr-idea-pipeline.md) [§3](./tillr-idea-pipeline.md#3-the-questions-path)), and `owner-only-owed` holds project to `blocked` with a distinct alarm when a `blocked_by` target is `closed` ([board-item-state-model.md](./board-item-state-model.md) [§5](./board-item-state-model.md#5-closed-replaces-deleted)).

### 3.2 Transition Preconditions and Attempt Holds (`D-JL4`)

`Apply` and `SetState` replace unconditional state overwrites with explicit transition and receipt refusals (enforced identically on live writes and `seed-items.json` materialization once seed metadata is backfilled):

| Target / Transition | Permitted `from` states | Store precondition enforced (`item.rejection` rule on failure) |
| :--- | :--- | :--- |
| **`not-started` <-> `in-queue`** | `not-started`, `in-queue`, `blocked` (via unblock) | Clears `waiting_on`; `in-queue` requires non-empty `criteria` (or `kind == "idea"`). |
| **`* -> in-progress`** | *(none)* | Always refused (`state-derived`); execution state is never stored on `items`. |
| **`* -> blocked`** | `not-started`, `in-queue`, `blocked` | Non-empty structured `waiting_on` (`waiter` + `reason`) required (`blocked-waiter-missing`); snapshots `underlying_state`. |
| **`* -> landed`** | `not-started`, `in-queue`, `blocked` | Every `item_criteria` row (`>= 1`) has `state == "done"` (`landed-criteria-unmet`) **and** a `land.land` event or verified `no_commit`/`landed[]` sha receipt exists (`landed-receipt-missing`). Terminal (`state-terminal` on exit). |
| **`* -> resolved-elsewhere`** | `not-started`, `in-queue`, `blocked` | Non-empty `resolves_via` (`item:<id>` existing and `!= item_id`, `commit:<sha>`, or `doc:<path>`) required (`resolved-pointer-missing`), and no `land.land` exists for `item_id` (`already-landed`). Terminal (`state-terminal` on exit). |
| **`* -> closed` (`tillr item close`)** | `not-started`, `in-queue`, `blocked` | Non-empty `close_reason` required (`close-reason-missing`); refuses if `land.land` already succeeded (`already-landed` — *"Landed wins"*); cancels any enqueued `land.candidate` (`withdrawn-by-close`), and `tillr-land pass` refuses `closed`/`resolved-elsewhere` items (`item-not-landable`). |
| **`closed -> <closed_from>` (`reopen`)** | `closed` only | Refused on raw `SetState`/`Apply` (`closed-use-reopen`); `tillr item reopen --reason <text>` restores `closed_from` (`not-started`, `in-queue`, or `blocked`). |

**Why `mayBeARetry` stays beside the state machine rather than being subsumed by it.** Owner disposition (`items.state == "in-queue"`) says the item is wanted; it does **not** say a failed attempt should immediately re-run on the next tick. Subsuming `mayBeARetry` into *"no live lease and `state == in-queue`"* would turn every 30-minute timeout (`AttemptUnjudged`), `boundary-scope-escape`, `candidate-no-op`, and red gate into an infinite token-burning loop. Instead, failed attempts leave `items.state = "in-queue"` while `decide` (`drain/plan.go`) holds them under specific, visible attempt reasons — `run-infra-exhausted`, `eval-failed`, `gate-failed`, `land-isolated`, `scope-escape`, `candidate-no-op`, and `retry-unjudged` (reading `runs.item_id` plus `run.allocate` events rather than `in-progress` item events) — and `boardCountsOf(...).Dispatchable` (`tillr-view/counts.go`) evaluates the exact same planner predicate so header counts and `drain plan` never disagree.

---

## 4. Ending Premature Criterion Freezes and Sunsetting `-v2` Clones

### 4.1 Attempt Outcomes Are Not Criterion Verdicts (`D-JL5`)

`carryCriteria` (`apply.go`) freezes criterion text once `item_criteria.state != ""` so a worker cannot lower a bar a judge has already graded. `failCandidate` (`drain/loop.go`) violated that premise by calling `Steps.Verdict(..., VerdictFailed)` whenever a run produced no landable candidate — stamping `not-done` onto `item_criteria` when **no criterion judge had run at all**:

| Non-landing outcome | Where detected | New settlement behavior (`item_criteria.state` untouched) |
| :--- | :--- | :--- |
| **`RESULT: blocked` report footer** | `ClassifyAttempt` / `Loop.settle` (parsing footer value after `RESULT:`) | Appends conflict/blocker detail from the report, transitions `items.state -> blocked` (`waiting_on` naming plan/design/owner rung per [escalation-ladder.md](./escalation-ladder.md) [§4](./escalation-ladder.md#4-routing-as-a-recorded-transition)), and writes **no** `item.verdict`. Unjudged specs (`item-or-routing`, `item-seat-bounds-wiring`) remain editable via `tillr item apply`. |
| **`candidate-no-op` / `fenceViolation`** | `Loop.settle` / `Loop.Verify` before `Gate` | Records `boundary.check` / run outcome on `run_id`, leaves `items.state = "in-queue"`, writes **no** `item.verdict`; held by `decide` as `candidate-no-op` or `scope-escape`. |
| **`GateFailed` / `LandRefused`** | `Loop.Verify` (`tillr-gate` or `tillr-land pass`) | Appends `gate.verdict` (`fail`) or `land.attempt` (`isolated`/`refused`), leaves `items.state = "in-queue"`, writes **no** `item.verdict`; held by `decide` as `gate-failed` or `land-isolated`. |
| **`EvalFailed` (`not-done` in graph)** | Post-graph settlement before `Verify` | Stores `eval.output` artifact on `run_id`, leaves `items.state = "in-queue"`, writes **no** `item.verdict`; held by `decide` as `eval-failed`. |
| **`AttemptUnjudged` (wall timeout)** | `Loop.settle` (`StateTimedOut` / exit `124`) | Leaves `items.state = "in-queue"`, writes **no** `item.verdict`; held as `retry-unjudged` (escalating to `needs-resize` / `blocked` after two consecutive cut-offs per [agent-runtime.md](./agent-runtime.md) [§30.4](./agent-runtime.md#304-the-second-consecutive-cut-off-resizes-the-item-d5)). |

Only actual criterion rulings — `done` (after `Land` or `no_commit` verification), `defective` (void premise under grant or human), or an explicit foreground `item.verdict` — write `item_criteria.state` and trigger `carryCriteria`'s text freeze.

### 4.2 Inspected Rework (`tillr drain rework`) and the `-v2` Cutover (`D-JL6`)

Even with unfrozen criteria, `AuthorizeRetry` (`drain/retries.go`) forced `-v2` clones (`item-land-3b-v2`, `item-bar-ironbar-config-v2`, `item-obs-pause-v2`, `item-obs-unranked-section-v2`, `item-obs-questions-source-v2`, `item-seed-04-v2`) because it accepts only unjudged harness failures and refuses every `gate-failed` and `AttemptUnjudged` run, leaving no verb to re-admit the original `item_id`.

Tillr splits **harness retry** from **inspected rework** on the same `item_id`:
- **`tillr drain retry`** keeps its strict guard: valid only for `AttemptInfrastructure` (`run-infra-exhausted`) and pre-gate harness casualties where the item's spec and fence did not change.
- **`tillr drain rework --item <id> --run <supersedes_run_id> --basis <basis> --by <actor> --why <reason>`** re-queues a held `gate-failed`, `eval-failed`, `land-isolated`, `scope-escape`, `candidate-no-op`, or `retry-unjudged` item on the **same `item_id`** ([OQ-JL3](#61-decision-ledger)), refusing unless the store shows the matching basis: an updated `allowed_paths`, appended criterion, [criterion-repair.md](./criterion-repair.md) criterion supersession (for already-frozen text), updated `graph_ref`, advanced `main` tip (for `carry-rebased`), recorded red-gate triage packet ([agent-runtime.md](./agent-runtime.md) [§13.1](./agent-runtime.md#131-a-red-candidate-is-triaged-not-retried)), or explicit timeout inspection (`timeout-inspected`).

**Explicit sunset condition for `-v2` + `decision.record` + `resolved-elsewhere` recovery.** Cloning a failed item to `<id>-v2` is prohibited once three things hold:

- no failed attempt writes a failing verdict or the `in-progress` state;
- SM-3's `items.resolves_via` column and its refusal are in `tillr-item`;
- a failed item can be sent again under its own id, by one command.

`resolved-elsewhere` then goes back to meaning only what [board-item-state-model.md](./board-item-state-model.md) [§4.1](./board-item-state-model.md#41-semantics) says: *intent satisfied by another path*. [Section 7](#7-what-the-tree-already-holds) records which of the three already hold, and the [last block](#prevent-cloning-a-held-item-under-a-new-id) of the build order is the refusal that enforces the prohibition.

---

## 5. Core Boundary for External AutoQA (`D-JL7`)

Answering [autoqa-tillr-seams.md](./autoqa-tillr-seams.md) [OQ-AQA1](./autoqa-tillr-seams.md#OQ-AQA1)'s request for post-hoc designer review: **yes — `ActorAutoQA`, `ClaimQAVerification` (`qa-verification`), and `DecidedByAutoQA` in `tools/tillr-item/` are redundant and are retired, superseding `D-AQA1` while keeping `D-AQA2`–`D-AQA4`'s standalone `gate change`, `run request`, and `run exec` exit-code contracts.**

1. **Authority kind vs client identity.** `verdictPayload.ActorKind` (`mechanical | agent | human`) names the **epistemic and delegation rule** `RecordVerdict` checks, whereas `verdictPayload.Actor` names **who ran**. `ActorAutoQA` conflated the two and exempted an automated reviewer from `checkDelegation` (`verdict.go`), creating a non-expiring, grantor-free sign-off path and coupling `tillr-item`'s closed vocabularies to one external client ([board-utilities.md](./board-utilities.md) [§13.1](./board-utilities.md#131-what-a-second-consumer-would-reuse-and-what-it-would-not) `D-UT15`). Surfacing `verdict.actor` and `verdict.actor_kind` in `tillr-view`'s `EvidencePane` makes `"actor": "autoqa/<suite>"` visibly distinct in every read view without a fourth `actor_kind`.
2. **Single unambiguous rule for Seat 3 (`` `docs/design/tillr-autoqa.md` ``):** Sectioned off as an external client, AutoQA owns its own exploratory queue, coverage state, bug-dedup, and false-positive memory outside `tillr-item`, and crosses into `tillr` through four existing faces only:
   - **Originate bug/remedy items:** `tillr item apply` (`item.record`).
   - **Record deterministic QA test verdicts:** `tillr item verdict record` with `actor_kind: "mechanical"`, `actor: "autoqa/<runner>"`, `claim_class: "verification-only"`, and non-empty `evidence`.
   - **Record agentic QA review verdicts:** `tillr item verdict record` with `actor_kind: "agent"`, `actor: "autoqa/<reviewer>"`, `judged_actor: "<producer>"`, `claim_class: "verification-only"`, and an unexpired `grant_id` issued in `item_grants`.
   - **Escalate ambiguous findings to a human:** `tillr item mail send` or `tillr item state --state blocked --waiting-on "owner: autoqa review ..."`, never a fifth `needs-review` verdict and never `decisions` rows with `decided_by: "autoqa"`.
3. **Store cleanup:** Move `ActorAutoQA`, `ClaimQAVerification`, and `DecidedByAutoQA` out of writable maps into legacy-readable frozen sets (`Rejectionf("actor-retired", ...)` on new writes), and close stale `in-queue` item `item-decided-by-autoqa` (`rank: "g"` in `config/tillr/seed-items.json`).

---

## 6. Decisions, Open Questions, and Amendments Owed

### 6.1 Decision Ledger

| ID | Ruling | Why |
| :--- | :--- | :--- |
| `D-JL1` | Place pre-approved graph judge leaves (`evaluator_slot`) before root `exit` and `Loop.Verify`, validate `eval.output` deterministically with `node_runs`-stamped `actor`/`judged_actor` and `item_grants`, and commit `done` `item.verdict` rows only after `tillr-land pass` succeeds. | Stops drifted or `defective` candidates before `main` moves while preventing pre-land completion leaks ([OQ-1](./board-item-state-model.md#9-open-questions)). |
| `D-JL2` | Skip post-gate generic judging when `evaluator_slot != nil`, retain standalone generic judging over the same `eval.output` intake for opted-in / foreground / audit work, store `claim_class` on `item_criteria`, and refuse `actor_kind: "mechanical"` on non-`verification-only` criteria (`owner-only` human-only, refused in `IssueGrant`). | Eliminates duplicate judging on judged graphs and closes the `8778c98ace` mechanical-stamp hole across all 10 `"Owner decision recorded..."` seed rows. |
| `D-JL3` | Retire `"in-progress"` from `writableStates` (`state-derived`), keep six stored owner-disposition states (`not-started`, `in-queue`, `blocked`, `landed`, `resolved-elsewhere`, `closed`), and project `working` strictly from live leases/active candidates in `ProjectItemState`. | Ends the `"8 working"` over zero live lanes bug and the `ReasonNotReady` dead end while preserving [board-item-state-model.md](./board-item-state-model.md) [§7](./board-item-state-model.md#7-the-structural-rule-one-projector-a-closed-enum). |
| `D-JL4` | Enforce `from -> to` and SM-3 receipt/pointer/reason preconditions in `Apply`, `SetState`, and `tillr-land pass` (*"Landed wins"* over close), while keeping `mayBeARetry` attempt holds beside derived execution state and aligning `boardCountsOf.Dispatchable` with `drain.Plan`. | Prevents receipt-free `landed`, pointer-free `resolved-elsewhere`, and landing of `closed` items without causing infinite re-dispatch loops on failed attempts. |
| `D-JL5` | Stop `failCandidate` from writing `not-done` `item.verdict` records on attempt/gate failures (` fenceViolation`, `candidate-no-op`, `GateFailed`, `EvalFailed`, `LandRefused`, `AttemptUnjudged`, and parsed `RESULT: blocked`). | Keeps unjudged criteria editable before a real criterion verdict is recorded, routing `RESULT: blocked` cleanly to `blocked`. |
| `D-JL6` | Add `tillr drain rework` (requiring a recorded input, fence, criterion, rebase, or triage basis on the same `item_id`) alongside harness-only `tillr drain retry`, and sunset `-v2` + `decision.record` + `resolved-elsewhere` failure cloning once `D-JL3`–`D-JL6` land. | Preserves item identity, `blocked_by` edges, and run history across repairs while keeping [agent-runtime.md](./agent-runtime.md) [§13.1](./agent-runtime.md#131-a-red-candidate-is-triaged-not-retried)'s ban on blind red-gate retries. |
| `D-JL7` | Retire `ActorAutoQA`, `ClaimQAVerification`, and `DecidedByAutoQA` from writable `tillr-item` vocabularies (superseding `D-AQA1`), surface `verdict.actor` in `tillr-view`, and require external AutoQA to use `mechanical` or grant-backed `agent` verdicts plus `item.record` and `mail.send`. | Closes the un-granted `checkDelegation` bypass and gives Seat 3 (`` `docs/design/tillr-autoqa.md` ``) a clean external-client boundary (`D-UT15`). |
| OQ-JL1 | An item with one node whose criteria are all checkable by a test is retired mechanically when the gate and the landing succeed, at no judge spend. An agent judge runs only where a workflow template or the item asks for one. **Rejected:** an agent judge on every item by default. | [The runtime design](./agent-runtime.md#1-purpose-and-scope) rules that what can be mechanical is mechanical, and the owner asks for token efficiency throughout. Settled on 2026-11-15 by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it. |
| OQ-JL2 | The store refuses a judge that shares a run or a session with the writer. Whether the judge also uses a different model is set in the backend policy, never refused by the store. **Rejected:** refusing a verdict when the judge and the writer used the same model route. | The owner changes providers often and requires the tooling to work whatever models a machine has; a hard refusal would stop every judged item on a machine with one route. Settled on 2026-11-15 by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it. |
| OQ-JL3 | The loop may authorize one rework of an item by itself when a recorded basis exists: a triage of the red gate, a changed input, or a carry repair. A second failure is not retried; it goes up [the escalation ladder](./escalation-ladder.md), to the design seat before the owner. **Rejected:** every rework waiting for a person. | The owner ruled on 2026-11-15 that the loop does not wait for approvals and that a refusal by a worker goes to a seat that can answer it; one bounded rework keeps spend bounded. Settled on 2026-11-15 by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it. |

| `D-JL8` | The build order is cut against the tree as it stood on 2026-11-16, and four spellings change to fit it. A criterion's claim class is declared in its item block by the directive attribute `claims`, and an item asks for an agent judge with the attribute `judge="agent"`. The owner-ruling rows already in a store are typed by one migration when the column is added, in place of an edit to the frozen seed file. No `tillr drain rework` command is added: `tillr drain retry --acknowledge` and the one automatic re-admission on a changed input are that command. The judge-before-gate step is built in the loop for an item that opts in, and the item graphs build order connects a workflow's evaluator leaf to that same step. **Rejected:** building a second re-queue command beside the one that exists; waiting for item graphs before any judge runs. | Items are defined in documents since [tillr-convergence.md](./tillr-convergence.md), so a class nobody can write in a document would never be set. The seed file is frozen. A second command for one act is two places to be wrong. A judge that runs before the gate needs nothing the graph runtime adds, and [tillr-item-graphs.md](./tillr-item-graphs.md) [§6](./tillr-item-graphs.md#6-boundary-with-judge-policy-and-item-lifecycle) already puts the verdict contract on this side of the boundary. Settled on 2026-11-16 by the design seat while cutting the build order; the owner may overturn it. |

### 6.2 Open Questions

None. Every question this design carried is a row of the ledger above.

### 6.3 Amendments Owed

Three tracked documents owe targeted updates. Each is made by the build block named beside it, in the same change as the code it describes, so no document claims a rule the tree does not yet enforce:

1. **[autoqa-tillr-seams.md](./autoqa-tillr-seams.md) ([Decision Ledger](./autoqa-tillr-seams.md#decision-ledger) and [OQ-AQA1](./autoqa-tillr-seams.md#OQ-AQA1)):** Record `D-AQA1` superseded by `D-JL7` (`ActorAutoQA`, `qa-verification`, and `DecidedByAutoQA` retired in favor of `verdict.actor` identity on `mechanical` and grant-backed `agent` verdicts); keep `D-AQA2`–`D-AQA4` in force. Owed by [the AutoQA block](#move-autoqa-onto-the-ordinary-verdict-kinds).
2. **[board-utilities.md](./board-utilities.md) ([§5.3](./board-utilities.md#53-item-alone) and [§14](./board-utilities.md#14-decision-ledger) `D-UT16`):** Add `item_criteria.claim_class`, the `RecordVerdict` actor-to-claim-class refusal table (`D-JL2`), and the SM-3 transition/receipt preconditions on `Apply` and `SetState` (`D-JL3`, `D-JL4`). The claim class is owed by [the first block](#prevent-a-gate-pass-from-retiring-an-owners-criterion), the preconditions by [the terminal-state block](#prevent-landed-and-resolved-states-without-their-evidence).
3. **[work-control-judge-in-tillr.md](./work-control-judge-in-tillr.md) ([Retirement in the end state](./work-control-judge-in-tillr.md#retirement-in-the-end-state)):** Cross-reference `D-JL1` and `D-JL2` for how pre-approved graphs carry the `evaluator` role pre-land via `evaluator_slot` while standalone generic judging shares the same `eval.output` intake. Owed by [the block that routes a judge's blocked and defective findings](#route-a-judges-blocked-and-defective-findings-to-their-states).

---

## 7. What the Tree Already Holds

Sections 1 to 5 were written against the tree of 2026-11-14. Two days of landings built part of this design under other items, some of it with different names. This section is the measurement the build order is cut from, read on 2026-11-16 at `bdd6b3c82`. Each row names the file a reader can open to check it.

| Decision | Built at `bdd6b3c82` | Still to build |
| :--- | :--- | :--- |
| `D-JL1` judge before the gate | Nothing. `rg -n 'eval\.output\|evaluator' tools/tillr-drain tools/tillr-item -g '*.go'` returns nothing. | The intake, the judge run before the gate, and its three failing outcomes: blocks 9 to 11. |
| `D-JL2` claim class on the criterion | Nothing. `item_criteria` has four columns and no `claim_class` (`tools/tillr-item/item/schema.go`), and `compose_verdict` in `tools/tillr-loop/tillr-loop` still writes `"claim_class": "verification-only"` for every criterion. | Blocks 1 and 2. |
| `D-JL3` stored disposition, derived `working` | Built. `in-progress` is in `frozenStates` (`tools/tillr-item/item/apply.go`), and the refusal's rule is `state-frozen`, not the `state-derived` that [Section 3.2](#32-transition-preconditions-and-attempt-holds-d-jl4) names. `ProjectItemState` (`tools/tillr-view/serveread.go`) reads a stored `in-progress` as queued and answers `working` only for a running run. The header's dispatchable count is the planner's own rule (`tools/tillr-view/counts.go`). | Nothing. The name `state-frozen` stands. |
| `D-JL4` transition preconditions | Nothing. `SetState` (`tools/tillr-item/item/state.go`) writes any writable state from any state, `items` has none of the seven columns, there is no `close` or `reopen` command (`tools/tillr-item/main.go`), and `rg -n 'closed\|item-not-landable' tools/tillr-land/land -g '*.go'` finds only a comment about file descriptors. | Blocks 4 to 6. |
| `D-JL5` an attempt is not a verdict | Mostly built. A gate failure, a fence refusal and a run with no commit each record a `drain.refusal` event and no verdict (`refuse` in `tools/tillr-drain/drain/refusal.go`). A run cut off by its wall is held as `run-unjudged`. A run that reports `blocked` blocks the item and is routed to the design seat with its conflict as the brief (commit `ed5cb51a2e`). | One case: a landing that did not take still calls `failCandidate`, which records `not-done` against every criterion. Block 3. |
| `D-JL6` send a failed item again under its own id | Built under other names. `tillr drain retry --acknowledge gate-failed` and `--acknowledge unjudged` authorize a second run of a judged or cut-off run (`tools/tillr-drain/drain/retries.go`). The planner re-admits an item once by itself when its item block, fence, backend policy or tillr build changed since the failed run, and holds it as `readmission-exhausted` after that (`tools/tillr-drain/drain/readmit.go`). | The second failure reaches nobody: block 12. The prohibition on clones has no check: block 13. No `rework` command is added (`D-JL8`). |
| `D-JL7` AutoQA uses the ordinary kinds | Nothing. `ActorAutoQA`, `ClaimQAVerification` and `DecidedByAutoQA` are still writable in `tools/tillr-item/item/`. `rg -n 'actor_kind\|ActorKind' tools/tillr-view -g '*.go' -g '!*_test.go'` returns nothing, so no page shows who recorded a verdict. | Blocks 7 and 8. |

Three facts about the tree change how a block is written, and each is ruled in `D-JL8`:

- **Items are defined in documents.** `tillr item sync` reads item blocks from tracked Markdown, and its directive takes five attributes: `id`, `paths`, `blocked_by`, `seat` and `prose` (`syncAttributes` in `tools/tillr-item/item/sync.go`). A claim class that can only be set in the store would never be set, so the class is declared in the block.
- **The seed file is frozen.** `config/tillr/seed-items.json` was frozen by commit `0b774f677`. It still holds eleven criteria that begin "Owner decision recorded" (`rg -c 'Owner decision recorded' config/tillr/seed-items.json` answers 11). Rows like them that a store already holds are typed by a migration, not by editing that file.
- **A new hold must be named in the view.** The waiting face shows a hold it has no rule for as `unclassified` (`waitingRuleFor` in `tools/tillr-view/`), so every block that adds a hold to the planner also names the view in its fence and adds the rule.

---

## 8. Build Order

Thirteen blocks. Each is one run inside one fence, and each names what it waits on. A block's fence includes the pack mirror under `yolo-packs/tillr/` because every change under `tools/tillr*` is delivered there too.

The first block is the smallest slice that runs from end to end: an author marks a criterion as the owner's in a document, the store keeps the mark, and the loop's own verdict step then leaves that criterion alone when the gate passes. It closes the defect this design opened with and waits on nothing.

The order after it follows what each block needs:

| # | Block | Waits on |
| :--- | :--- | :--- |
| 1 | A gate pass does not retire an owner's criterion | nothing |
| 2 | An item that owes an owner's ruling is held | 1 |
| 3 | A failed landing freezes no criterion | nothing |
| 4 | A blocked item names its waiter and reason | nothing |
| 5 | `landed` and `resolved-elsewhere` need their evidence | 2 |
| 6 | Close and reopen commands | 4 |
| 7 | AutoQA uses the ordinary verdict kinds | nothing |
| 8 | The item page shows who recorded a verdict | nothing |
| 9 | One intake for a judge's written verdicts | 1 |
| 10 | An opted-in item is judged before the gate | 3, 9 |
| 11 | A judge's blocked and defective findings | 4, 6, 10 |
| 12 | A second failed attempt goes to the design seat | nothing |
| 13 | A held item is not cloned under a new id | 12 |

One piece of `D-JL1` is not a block here. A workflow's evaluator leaf is a node of the graph runtime, which [tillr-item-graphs.md](./tillr-item-graphs.md) owns and has not yet cut into items. Blocks 9 to 11 build everything on this side of that boundary: the file a judge writes, the check of it, the step before the gate, and what each outcome does. The item graphs build order connects its leaf to that step, and adds no second intake.

### Prevent a gate pass from retiring an owner's criterion

Some criteria are not checks of the code. "The owner has recorded a decision" is true only when the owner says so, and no test can stand in for that. Today the loop cannot tell the two kinds apart: when the gate passes, it marks every criterion of the item done. That is how a five-second Markdown check once retired an owner's decision. This block gives each criterion a class, lets an author state it in the item's own block, and makes both the store and the loop's verdict step respect it. After it, a criterion marked as the owner's can be retired by a person and by nothing else.

The pack mirror this block owes is the item utility's alone. `just pack-sync` mirrors eleven modules and `tools/tillr-loop/` is not one of them: the loop adapter is this repository's own program, run from `tools/tillr-loop/tillr-loop` by the command that starts the loop, and it has no copy under `yolo-packs/tillr/` (measured on 2026-11-16 at `e65a45ace`: the module list in the `pack-sync` recipe and `MODULES` in `tests/pack-sync.test.py` both omit it, and `ls yolo-packs/tillr/files/local/share/tillr/` shows no `tillr-loop`). So the change to the loop is complete when `tools/tillr-loop/` holds it and `python3 tools/tillr-loop/test.py` passes. Run `just pack-sync` once, commit the `tillr-item` mirror it regenerates, and add no loop mirror, no recipe change and no change to the mirror test: a missing `tillr-loop` directory in the pack is the expected state, and not a reason to stop.

A run that re-applies this block's change after a squash conflict (the lander could not replay the change onto a trunk that had moved) re-applies the worker's commit and nothing above it. Its brief names that commit as the change's head, and says the head is pinned at a ref under `refs/tillr/runs/`. That ref points one commit higher, at `chore: retire landed document block`, which the loop itself adds on top of the worker's commit to remove this block from the document before the gate runs (`retireDocumentBlock` in `tools/tillr-drain/drain/retire.go`; the conflict-fix step in `tools/tillr-drain/drain/conflict.go` passes the worker's head to the brief and keeps the pin on the commit above it). So the two differing is the expected state, and not a reason to stop: the head the brief names is the worker's commit, and it is the pinned ref's first parent. Measured on 2026-11-16: the first such run for this block stopped on that difference, with the brief naming `711e6bbfe` `[unlanded]` (`fix(tillr): enforce criterion claim authority`) and the ref pointing at `cbd43ec09` `[unlanded]`, whose only change is the removal of this block and of the two `blocked_by` entries that name it. Do not re-apply the retirement commit, and do not remove or edit this block by hand: resolve the conflicts in the worker's change, keep both sides' tests where two tests conflict, and leave this block in the document. The loop retires it again after the repaired change passes. The recipes are in `.justfile` at the repository root: a hidden file, with a leading dot and a lowercase j. It is the only justfile the tree tracks (measured on 2026-11-17 at `41f9260e8`: `git ls-files | rg -i '(^|/)\.?justfile$'` prints `.justfile` and nothing else, and `just --summary` run at the root lists `pack-sync`). No `Justfile` exists, so a search of that name finds nothing; that is the expected state, and not a reason to stop. Run `just pack-sync` from the worktree root and read a recipe with `just --show pack-sync` or in `.justfile`.



### Hold an item that still owes an owner's ruling

Once the store knows which criteria are the owner's, the planner can stop spending on items that cannot finish. An item whose only unmet criterion is an owner's ruling gains nothing from a worker: the run would end, the gate would pass, and the item would still be waiting for the owner. On 2026-11-14 an item like that was unblocked early and dispatched. This block makes the planner hold such an item before any lane is used, and makes the waiting face say plainly that the owner is the one it waits on.

A note for the next run on this block: commit `e476f99f5` [unlanded] never reached the trunk, so do not cite it as evidence; describe the behavior it built in words instead (the planner holds the item with `owner-only-owed` before leasing a lane, and the waiting face names the owner and the owed criterion), and build afresh from the current trunk tip, because the earlier candidate is stale against trunk.

<!-- tillr: item id="item-jl-owner-criterion-hold" paths="tools/tillr-drain/,tools/tillr-view/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** The planner holds an `in-queue` item that has an `owner-only` criterion not yet `done`, with the reason `owner-only-owed`. It leases no lane and starts no run for it. A test with two free lanes asserts zero dispatches and the reason.
- **c2:** The hold lifts on the first pass after a human verdict marks that criterion `done`. A test records the verdict and asserts the item is dispatched.
- **c3:** The waiting face shows the item as waiting on the owner, with the criterion's id and the first line of its text, and the header's dispatchable count leaves it out. A test asserts the hold's class on the waiting face is not `unclassified`.

### Prevent a failed landing from freezing the item's criteria

A criterion's text is frozen once a judge has ruled on it, so that nobody lowers a bar after the fact. A landing that did not take is not such a ruling. The gate passed the work, and then the trunk could not accept it, usually because another landing got there first. Today that outcome still marks every criterion of the item as not done, which freezes text nobody judged and is the last way a failed attempt can make an item unrepairable. This block makes a failed landing behave like a failed gate already does: it is recorded against the run, and the item's criteria are left alone.



### Add the waiter and reason to every blocked item

"Blocked" is only useful when it says who has to act. Today an item can be moved to blocked with no record of who it waits on or why, and the board fills the gap with the words "the store marks it blocked". The loop already knows the answer when it blocks an item itself, because the run's report named the conflict and the seat that can settle it. This block stores the waiter and the reason beside the state, requires them on every new move to blocked, and remembers which state the item was in so that unblocking puts it back.



### Prevent landed and resolved states without their evidence

The board's two "finished" states are claims about the world. `landed` claims this item's work is on the trunk, and `resolved-elsewhere` claims something else satisfied it. Today either can be written by one command with nothing to back it, and either can be undone the same way. This block makes the store ask for the evidence: a landing record and finished criteria for the first, a pointer to the other path for the second. It also makes both final, so a finished item is never quietly put back in the queue.

<!-- tillr: item id="item-jl-terminal-state-receipts" paths="tools/tillr-item/,yolo-packs/tillr/,docs/" blocked_by="item-jl-owner-criterion-hold" prose="2" -->

- **c1:** A move to `landed` is refused as `landed-criteria-unmet`, naming the criteria, unless the item has at least one criterion and every one is `done`. It is refused as `landed-receipt-missing` unless the store holds a landing record for the item. A test covers each refusal and the accepted move.
- **c2:** A move to `resolved-elsewhere` requires `--resolves-via`, which is `item:<id>` for another item the store holds, `commit:<sha>` or `doc:<path>`. Without it the move is refused as `resolved-pointer-missing`, and when the item already has a landing record it is refused as `already-landed`. The pointer is stored and `tillr item show` prints it.
- **c3:** A move out of `landed` or `resolved-elsewhere` to any other state is refused as `state-terminal`. Writing the state the item already has still answers the item and appends no event.
- **c4:** A test runs the loop's own sequence for a landed candidate, verdicts first and the state move second, against a store that holds the landing record, and asserts the item ends `landed`.
- **c5:** Items that were `landed` or `resolved-elsewhere` before this change stay readable as they are, with or without evidence. A test opens such a store.
- **c6:** [board-utilities.md](./board-utilities.md) states these preconditions in its section 5.3.

### Add commands to close an item and reopen it

Sometimes the right answer to an item is "not doing this". The store has a `closed` state for that, but no command that records why an item was closed, who closed it, or what state it was in, so a closed item cannot be reopened to where it was. Nothing stops a candidate for a closed item from landing either. This block adds the two commands, keeps what a reopen needs, and makes the lander refuse work for an item that is no longer wanted. An item that has already landed cannot be closed: the landing wins.



### Move AutoQA onto the ordinary verdict kinds

AutoQA is a client of tillr, not a part of it. When its seams were added, the store was given an `autoqa` actor kind that skips the check every other agent must pass: a recorded grant from someone with the authority to delegate. That is a sign-off path with no grantor and no expiry. The store already has what AutoQA needs. A test runner's result is a mechanical verdict, and a reviewing agent's judgment is an agent verdict under a grant; the actor's name says it was AutoQA. This block retires the special kinds for new writes and leaves old records readable.



The queue row `item-decided-by-autoqa` asks for the opposite of this block. Closing it is an operator's act on the store, done with the close command once [that block](#add-commands-to-close-an-item-and-reopen-it) lands; no run writes the store.

### Add the verdict's author to the item page

A verdict is only as good as whoever recorded it, and the observatory does not say who that was. A reader cannot tell a criterion the gate retired from one a judge ruled on or one the owner signed. Once AutoQA records under the ordinary kinds, its name in the actor field is the only thing that marks its verdicts, so the page has to show that field. This block puts the author on the page and adds the one alarm the state model asks for: an item waiting on something that has been closed.



### Add one intake for a judge's written verdicts

A judge is an agent, and an agent's output is text that may be wrong, incomplete or about the wrong commit. Before any of it becomes a record, something has to check it: that it covers every criterion, that it read the text the store holds, that it looked at the candidate it claims to, and that it cites evidence. That check must be one piece of code, used by the loop, by a person judging a commit by hand and later by a workflow's evaluator, or the three will drift. This block builds that one intake as a command. It never trusts the file to say who the judge was; the caller states that.



### Route an opted-in item through a judge before the gate

The gate proves the tree is healthy. It does not prove the change is what the item asked for, and for some items that is the question that matters. Such an item asks for a judge in its own block. The judge runs after the worker has committed and before the gate, so that work which misses the point costs neither the gate's time nor a landing. It reads and never writes. If it passes the work, the gate and the landing run as they do today, and only after the landing are the judge's rulings recorded, so the board never shows an item as judged done while its change is not on the trunk.



### Route a judge's blocked and defective findings to their states

A judge can find more than "not yet done". It can find that a criterion cannot be judged until someone answers a question, or that the item's premise is false and no amount of work will satisfy it. Both are worth more than a failed attempt, because they say who must act. A blocked finding sends the item to the seat that can answer. A defective finding is a ruling: it is recorded, and the item is closed with the judge's reason, so the queue stops spending on work that was wrong from the start. This block finishes the judge step with those two outcomes.



**What this block builds on:** the judge step of the block before it in the build order, which is on the trunk. It landed on 2026-11-17 under the subject `feat(tillr): judge opted-in candidates before gating`. The first run of this block on the same day was staged from a base older than that landing, found no judge step in the loop, and stopped. It was right to stop, and the block stands as written: it does not build the judge step, and it is not built on a tree without one. The check is one command, `rg -n 'ReasonEvalFailed' tools/tillr-drain/drain/`. A tree where it prints nothing is staged too early. A run reports that as a staging fault, and the answer is a restage on the current trunk, not another change to this block.

What that landing left for this block to extend, as read on the trunk on 2026-11-17:

- The loop's judge step is `judgeCandidate` in `tools/tillr-drain/drain/judge.go`. It hands the judge's file to the intake and holds the item as `eval-failed` for every outcome that is not `pass`. This block replaces that one branch with three: `not-done` keeps the hold it has, and `blocked` and `defective` get the routes of c1 and c2. It adds no second judge run and no second read of the file.
- The order of c3 is already the intake's. `tillr-item judge` answers one outcome for a file, and it picks defective before blocked before not-done (`tools/tillr-item/item/judge.go`). So c3 is met by the loop routing on the outcome the intake returns and never ranking the entries again. Its test hands the loop the three mixed files and asserts the route each takes.
- The intake's answer keeps the judge's waiter for a blocked entry, for the caller's use. c1 moves the item to blocked through the store's existing move, which already requires a waiter and a reason and remembers the state the item was in. The reason is the blocked entry's own.
- c2 closes the item through the existing close command's code path, so the closed item carries who closed it and the state it was in, and the lander's refusal of a closed item's candidate applies with no new rule.
- The waiting face's rules are the table in `tools/tillr-view/waiting.go`, which the landing extended with `judge-grant-absent`, `judge-unreadable` and `eval-failed`. c4 adds its two rules to that table.

### Route a second failed attempt to the design seat

The loop may try a failed item once more by itself when something it recorded has changed, and an operator may authorize one more run by name. What happens after that second failure is, today, nothing: the item sits in a hold until someone notices. The owner's ruling is that a failure the loop cannot resolve goes to a seat that can, and to the design seat before the owner. Two failures of the same item are evidence that the item is wrong, not the worker. This block sends the item, with what both runs reported, to the design seat in the way a run that reports blocked is already sent.



### Prevent cloning a held item under a new id

When an item could not be repaired in place, the workaround was to file a copy with `-v2` on its id and mark the original as resolved elsewhere. Six items went that way on 2026-11-14. Each copy lost the original's run history and its place in other items' dependencies, and each "resolved elsewhere" was untrue. With a failed attempt no longer freezing criteria, and with retry, re-admission and the design seat all working on the original id, the workaround has no remaining use. This block makes the sync refuse it, so the rule does not depend on anyone remembering.


