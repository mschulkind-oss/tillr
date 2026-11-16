---
title: "AutoQA on the tillr component seams: standalone invocation, verdict payloads, missing emitters, refusal contracts"
status: draft
stage: SUPERSEDED
next: "All four ruled 2026-11-13 without a designer pass (D-AQA1 A, D-AQA2 B, D-AQA3 B, D-AQA4 A): build the `autoqa` actor, propagate `run exec`'s exit code, and add the `gate change` and `run request` faces. D-AQA2 is the one to put in front of the next designer pass, because its accepted cost is that a standalone gate run stays unobserved."
tags: [design, tillr, autoqa]
summary: "Answers the pack's four AutoQA handoff questions against the eight `tillr` binaries as they measure today: which faces drive standalone and what record each needs planted, the exact `item.verdict` and `decision.record` payloads AutoQA must author, the events `tillr-gate`, `tillr-run` and `tillr-land` must add before a run is reconstructable from `tillr-view events`, and the exit-code holes that let an unattended harness read a failure as a pass."
depends-on:
  - tillr-observatory.md
  - session-recording.md
  - tillr-distribution.md
superseded-by: ./tillr-autoqa.md
archived: 2026-11-16
---

# AutoQA on the `tillr` component seams

> **Retired 2026-11-16.** [The AutoQA design](./tillr-autoqa.md) supersedes this document, as its own text asks. All four questions here were ruled on 2026-11-13: the first was later reversed by `D-JL7` in [the judge and lifecycle design](./tillr-judge-and-lifecycle.md), and the other three are built.

**Status:** 2026-11-13. Draft design comments. Measured against the eight binaries installed on `PATH` from `yolo-packs/tillr` and against `tools/tillr*` in this tree. No source, test or binary was edited; every scratch store lives outside this repository.

**In short.** Every face the handoff names drives standalone today, and the two couplings that matter are not `tillr-drain`: they are the **input records nobody but an adapter produces** (`gate.change`, `run.request`) and the **store schema, which is a function of which binaries have opened the store**. AutoQA can post QA verdicts through `tillr-item verdict record` with `actor_kind: "mechanical"`, but it has no vocabulary for "request human review" and no `decided_by` value it can legitimately use on a decision. The recording gap is narrower than the handoff implies and worse than it sounds: `tillr-gate` writes **no** event at all, and the store's own append primitive (`event.Event`) has **no field for a correlation id**, so no event row today can carry the `trace_key` that [`session-recording.md`](./session-recording.md#42-the-five-part-hierarchical-trace_key) specifies. Finally, `tillr-run exec` answers **exit 0 for a run whose command exited nonzero**, which is the one refusal defect that would make an unattended QA harness report a green on a red run.

---

## 1. Standalone invocation: what AutoQA can drive without `tillr-drain`

**Answer: yes for every face the handoff names, and the drain is not in the loop.** The dependence AutoQA actually hits is on two *record producers* and on the store's migration story, not on the loop.

### 1.1 The gate: three faces, no store, one missing input

`tillr-gate` takes no `--store` flag on any face. Measured, and confirmed in [`tools/tillr-gate/main.go`](../../tools/tillr-gate/main.go): `planFace` declares only `--registry`, `--input`, `--output` and `--root`, and its doc comment states the design rule it implements ("There is no `--store` flag and no store is opened"). `runFace` declares `--input`, `--worktree`, `--output`, `--logs`, `--gate`, `--gate-arg`, `--gate-timeout`. `dualRunFace` declares `--registry`, `--report`, `--work`, `--root`, `--self`, `--python`, `--python-exe`, `--candidate`, `--timeout`.

- **`plan` is fully standalone and cheap.** It ran green against the tree's own registry with nothing planted:

  ```console
  $ tillr-gate plan --registry /workspace/tools/tillr-gate/testdata/registry.json \
      --input ./change.json --root /workspace --output ./gate.plan.json
  $ echo $?
  0
  ```

  Its **required state precondition** is a `gate.change` record on `--input`. No binary in the suite has a `gate.change` face. The two producers in-tree are `tillr-gate dualrun`, which writes one inside its own work directory ([`tools/tillr-gate/dualrun.go`](../../tools/tillr-gate/dualrun.go), `writeChange`), and the loop's gate adapter, [`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop), whose README's `gate` row says it derives "the `gate.change` record: base, `head^{tree}` and `git diff --name-only base...head`". An AutoQA driver that does not want `tillr-loop` must therefore author the record itself. That is an **authored input**, not a flag: there is no face to add a flag to.

- **`run` is standalone but refuses a plan it cannot honour.** The registry in this repository declares in-process checks, and the Go face cannot run them, so the plan it just produced is un-runnable by the same binary:

  ```console
  $ tillr-gate run --input ./gate.plan.json --worktree /workspace --output ./gate.verdict.json --logs ./logs
  tillr-gate run: the plan owes 1 in-process check(s) this face cannot run (file-size): a verdict that omitted them would assert a transition that did not happen
  $ echo $?
  4
  ```

  The refusal is a **state precondition of the plan**, not a flag: any plan computed against a registry with `in_process` entries is refused by [`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go) (`Run`, the `len(plan.InProcess) > 0` branch). To get a verdict at all, the caller passes `--gate` and the plan's own commands are ignored:

  ```console
  $ tillr-gate run --input ./gate.plan.json --worktree /workspace --output ./v2.json --logs ./logs2 --gate /bin/true
  $ echo $?
  0
  ```

  and the resulting verdict carries **one** check, `gate:true`, and none of the plan's. See [§3.2](#32-what-the-gate-emits-today-nothing) and [§4.4](#44-verdicts-are-two-shapes-with-one-kind) for why that matters to reconstruction.

- **`dualrun` is standalone and needs the Python gate on disk.** It defaults `--python` to `<root>/tools/fork-check.py`; it requires `--registry`, `--report`, `--work` and at least one `--candidate name=base`, and it answers 0 when it produced its report even when the two gates disagreed.

### 1.2 The run: standalone, and the lane is created by the run itself

`tillr-run exec` needs `--store`, `--lane-root`, `--input` and optionally `--now` and `--backends-dir`. It **claims the lane itself**: `claimLane` in [`tools/tillr-run/run/lease.go`](../../tools/tillr-run/run/lease.go) inserts the lane row inside the same transaction that appends `run.allocate`, so no drain step is required to have created a lane first. Measured against a store created by `tillr-store init`:

```console
$ tillr-run exec --store ./state.db --lane-root ./lanes --input ./req.json --now 2026-11-13T00:00:00Z
{"record_version":1,"kind":"run.record",...,"payload":{"run_id":"run-1","item_id":"item-1","lane_id":"lane-1","backend":"pi","state":"exited","exit_code":0,...
$ echo $?
0
$ tillr-run lanes --store ./state.db
{"record_version":1,"kind":"run.lanes",...,"payload":{"lanes":[{"lane_id":"lane-1","state":"idle","run_id":null,"leased_until":null}]}}
```

`status` is a pure read and answers a negative decision rather than an error for a run it does not hold (measured: exit 1, and a `run.status` record with `"found":false`). `release` needs `--store`, `--lane` and `--reason`, and the reason is a **flag-level precondition**: [`tools/tillr-run/main.go`](../../tools/tillr-run/main.go) refuses before reaching the package.

The one **authored input** here is `run.request`. Its only producer is `tillr-drain`'s `ProcessSteps.Run` ([`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go)), which writes the record into `<lane-root>/.requests/<run-id>.request.json`. Nothing else in the tree writes one.

### 1.3 The real coupling: the store's schema is a function of who opened it

This is the coupling that will bite AutoQA first, and it is not documented as a precondition anywhere.

`tillr-store init` links only [`tools/tillr/store`](../../tools/tillr/store) (`tools/tillr-store/main.go`'s import block names `tillr/event`, `tillr/record` and `tillr/store`, and nothing else). Every other utility's tables arrive through that utility's own `init` calling `store.RegisterDDL`. So a freshly initialized store has **three** tables and no more:

```console
$ tillr-store init --store ./fresh.db
$ tillr-store check --store ./fresh.db
{"record_version":1,"kind":"store.report",...,"payload":{"action":"check","schema_version":4,...,"table_counts":{"events":0,"meta":0,"schema_migrations":1},"integrity":"ok"}}
```

`integrity: "ok"` is true and useless: three binaries then fail against that store, with **three different exit codes for one condition**:

```console
$ tillr-land list --store ./fresh.db
tillr-land: reading the queue: SQL logic error: no such table: candidates (1)     # exit 5
$ tillr-item show --store ./fresh.db --item x
tillr-item: reading item "x": SQL logic error: no such table: items (1)          # exit 5
$ tillr-run lanes --store ./fresh.db
tillr-run: SQL logic error: no such table: lanes (1)                             # exit 3
```

The 3 is deliberate and local: [`tools/tillr-run/run/read.go`](../../tools/tillr-run/run/read.go) maps a message containing `no such table` to `Incompatiblef`. The other two binaries have no such mapping, so the store's own missing-table error falls through `ExitCode` to the exit-5 default. **Precondition for a standalone driver: create the store with the binary that owns the tables you will use, or have `tillr-store init` link every utility's `DDL`.** The first is a workaround, the second is a change, and it is a decision, not an inference ([§4.2](#42-one-condition-three-exit-codes)).

### 1.4 The land and the item

`tillr land enqueue` is standalone and validates against the repository (it refuses a `head_sha` the repository does not hold). `tillr land check` and `tillr land pass` take `--store`, `--repo`, `--candidate` and `--input <verdict.json>`, and `pass` takes the land lock (`--lock`, defaulting under the git common dir; `--lock-timeout`). `tillr land one` composes enqueue, the gate program and pass under that one lock, and is the closest thing to a documented AutoQA entry point for a manual landing.

`tillr land list` also fails on a store that has never been migrated, with the same exit 5 shown above.

---

## 2. Verdict and decision payloads

### 2.1 `tillr-item verdict`: the record is the interface, there are no value flags

`verdict` has exactly one verb, `record`, and it takes **`--store`, `--input` and `--now`, all three required**; the values live in the record on `--input`, not on argv. Source: `verdictRecordFace` in [`tools/tillr-item/main.go`](../../tools/tillr-item/main.go).

The payload AutoQA must author is [`verdictPayload`](../../tools/tillr-item/item/verdict.go) in [`tools/tillr-item/item/verdict.go`](../../tools/tillr-item/item/verdict.go). It exists today; the shape below is the one the store reads, not a proposal:

```json
{
  "item_id": "1130",
  "criterion_id": "c1",
  "criterion_hash": "<sha256 of the frozen criterion text, lowercase hex>",
  "verdict": "done",
  "claim_class": "verification-only",
  "reason": "the suite named by the criterion passes at the evaluated head",
  "landed": ["<sha>"],
  "evidence": ["/workspace/.tillr/loop/run-1130-lane-1/gate.verdict.json"],
  "evaluated_head": "<sha>",
  "actor_kind": "mechanical",
  "actor": "",
  "judged_actor": "",
  "grant_id": ""
}
```

Four constraints AutoQA will hit, each with its source:

1. **`criterion_hash` is not a free field.** `RecordVerdict` recomputes `sha256(frozen criterion text)` and refuses a mismatch, so a QA verdict has to read the criterion from the store and hash it. `item.CriterionHash` is the only writer of that spelling.
2. **`evidence` may not be empty**, for any actor kind. A QA verdict that judges a gate verdict must name the file it read.
3. **`claim_class` is a closed set of four**: `verification-only`, `red-gate-triage`, `direction`, `owner-only` ([`tools/tillr-item/item/grant.go`](../../tools/tillr-item/item/grant.go)). A malformed one is a usage error with no record:
   ```console
   $ tillr-item verdict record --store ./state.db --input ./v.json --now 2026-11-13T00:00:00Z
   tillr-item: "nonsense" is not a claim class: want one of verification-only, red-gate-triage, direction, owner-only
   $ echo $?
   2
   ```
4. **`actor_kind: "mechanical"` needs no grant and no independence check**; `"agent"` needs a `grant_id` that exists, is unexpired, and covers the claim class, plus `actor != judged_actor`, and `"human"` needs neither. So a QA pass/fail posted by an automated harness has **exactly one legal spelling today: `actor_kind: "mechanical"`**, which the source defines as "the composed pipeline retiring what it landed … with no agent judging anything" ([`tools/tillr-item/item/verdict.go`](../../tools/tillr-item/item/verdict.go)). An AutoQA harness *is* an agent judging, so this vocabulary does not describe it.

**Output.** On success the stored record is echoed on stdout and the exit is 0. On a rejection (unknown item, frozen-hash mismatch, no evidence, self-judging, bad grant) an `item.rejection` record is printed on stdout with `rule` and `reason`, and the exit is 1; measured on the sibling face `show`:

```console
$ tillr-item show --store ./state.db --item item-1
{"record_version":1,"kind":"item.rejection",...,"payload":{"item_id":"item-1","reason":"no item \"item-1\" in this store","rule":"not-found"}}
$ echo $?
1
```

On a usage or vocabulary error there is **no record at all**, only stderr and exit 2. That distinction is the good half of the contract and worth relying on.

### 2.2 "Request human review" has no verdict

The four verdicts are `done`, `not-done`, `blocked`, `defective`, and all four move the **criterion's** state. There is no `needs-review`, no `escalate`, and no way to record "a machine could not decide" without asserting that the criterion is `blocked`, which is a claim about the work rather than about the reviewer.

Two existing faces are the nearest thing, and neither is a verdict:

- **`tillr-item mail send`** takes `--store`, `--input` (a `mail.delivery` record) and `--now`, and writes a delivery to a target. `mail list --to <target>` and `mail ack` are the reader side. This is the only "ask a human" channel the suite has.
- **`tillr-item state --store --item --state blocked --now`** moves the item, not the criterion, and its writable vocabulary is `not-started`, `in-progress`, `blocked`, `in-queue`, `landed`, `resolved-elsewhere`, `closed` ([`tools/tillr-item/item/apply.go`](../../tools/tillr-item/item/apply.go), `checkState`).

Proposed shape, **not implemented**, for the escalation AutoQA needs. A `request_review` verdict value plus a fifth claim class keeps every escalation in one log instead of splitting it across mail and item state:

```json
{
  "item_id": "1130",
  "criterion_id": "c1",
  "criterion_hash": "<sha256>",
  "verdict": "needs-review",
  "claim_class": "qa-escalation",
  "reason": "the gate verdict is `error`: the plan owed an in-process check the Go face cannot run",
  "evidence": ["/workspace/.tillr/loop/run-1130-lane-1/gate.verdict.json"],
  "actor_kind": "agent",
  "actor": "autoqa",
  "judged_actor": "lane-1130-worker"
}
```

That adds a fifth value to a closed four-value set and a grant requirement to a class that has no grantor. It is a design decision, so it is [OQ-AQA1](#OQ-AQA1), not a change made here.

### 2.3 `tillr-item decision`: two of its fields exclude AutoQA by construction

`decision` has four verbs: `record`, `supersede`, `revoke`, `list`. `record` and `supersede` take `--store`, `--input` and `--now` (`supersede` also takes `--decision`); `revoke` takes `--decision` instead of `--input`; `list` takes `--store` and an optional `--status` (`active`, `superseded` or `revoked`) and nothing else. Source: `decisionListFace` and its siblings in [`tools/tillr-item/main.go`](../../tools/tillr-item/main.go).

The payload is [`decisionPayload`](../../tools/tillr-item/item/decision.go):

```json
{
  "decision_id": "dec-1130-a",
  "text": "the QA harness may re-run a failed check once before escalating",
  "scope": {"kind": "items", "ref": "1130,1141"},
  "decided_by": "whale",
  "session_id": "<the session that decided it>"
}
```

Two problems for AutoQA specifically:

- **`decided_by` is `owner` or `whale`, and nothing else.** Both names denote a *seat*, and [`tools/tillr-item/item/decision.go`](../../tools/tillr-item/item/decision.go) says why in as many words: the vocabulary is "taken from the monolith's decision.go and db.go rather than re-invented". An AutoQA harness writing a decision must claim to be the whale, which is a false attribution in a table whose whole purpose is to record who bound what.
- **`scope` is a typed struct, not a free string**, and `ref` is a subsystem name or a comma-separated id list. An AutoQA rule scoped to "every criterion of this claim class" has no spelling.

`decision list` is the read side and is safe to poll: it answers `{ "decisions": [...] }` in decided-at order.

---

## 3. Missing event emitters: the exact events, fields and correlation IDs

### 3.1 What the store actually records today

The store's event kinds are whatever each utility's package `init` registered, and there is no face that lists them (nothing in `tools/` calls `event.Kinds()` outside the package). Read from source, the registered set is:

| Emitter | Event kinds | Source |
| :--- | :--- | :--- |
| `run` | `run.allocate`, `run.finish`, `run.usage`, `run.release` | [`tools/tillr-run/run/schema.go`](../../tools/tillr-run/run/schema.go) |
| `land` | `land.enqueue`, `land.land`, `land.cancel` | [`tools/tillr-land/land/schema.go`](../../tools/tillr-land/land/schema.go) |
| `item` | `item.add`, `item.update`, `item.verdict`, `item.grant`, `decision.record`, `decision.revoke`, `mail.send`, `mail.ack` | [`tools/tillr-item/item/schema.go`](../../tools/tillr-item/item/schema.go) |
| `store` | `fixture.seed` | [`tools/tillr/store/kinds.go`](../../tools/tillr/store/kinds.go) |

Measured, the six events a one-command run produces are three:

```console
$ tillr-view events --store ./state.db
{"record_version":1,"kind":"view.events",...,"payload":{"generated_at":"...","snapshot":{"schema_version":4,"event_head_seq":3,...},"since":0,"events":[{"seq":1,"kind":"run.allocate",...
```

`run.allocate`, `run.usage` (here: "the extractor answered exit status 4 and its report carries no spend"), `run.finish`.

### 3.2 What the gate emits today: nothing

`tillr-gate` has no `--store` flag on any face, so it appends no event, and its two record kinds are *deliberately* not events. Both source comments say so:

- [`tools/tillr-gate/plan.go`](../../tools/tillr-gate/plan.go), above `PlanKind`: "It is a stdout record kind, never an event: a plan is a function of a change and a registry, so appending it would claim a state transition that does not exist."
- [`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go), above `VerdictKind`: the same sentence with "a verdict is a function of a plan and a tree".

One thing softens the cost: `gate.plan` and `gate.verdict` are already registered as event kinds by the utility that owns them ([`tools/tillr-gate/plan.go`](../../tools/tillr-gate/plan.go), [`run.go`](../../tools/tillr-gate/run.go)); the registration exists and `event.Append` is simply never called. The first two emitters of [§3.4](#34-proposed-events-and-fields) are a `tx.Append` away rather than a re-plumbing.

That ruling is right about **authority** and wrong about **observation**, and the difference is what AutoQA needs decided. A `gate.plan` is not a state transition; but "this run's gate planned 14 checks at 19:04:12 and then took 6m41s to answer `fail`" is an observation, and the store has no row for it. Today the only copy is a file: `.tillr/loop/<run-id>/gate.plan.json`, `gate.verdict.json`, and the `steps.jsonl` spans that `tillr-drain`'s `ProcessSteps` appends ([`tools/tillr-drain/drain/spans.go`](../../tools/tillr-drain/drain/spans.go), `StepSpan`). `tillr-view events --records <file>` can read such a stream, but only if the caller already collected it.

### 3.3 The correlation gap is in the substrate, not the emitters

[`session-recording.md`](./session-recording.md#42-the-five-part-hierarchical-trace_key) specifies `<tick_id>/<item_id>/<run_id>/<session_id>/<span_id>` and says `tillr-drain` populates `correlation_id: tick.TickID` on the `run.request` envelope. **Corrected against measurement on 2026-11-13: it does not.** `rg CorrelationID tools/` matches exactly one Go file, the envelope's own definition ([`tools/tillr/record/envelope.go`](../../tools/tillr/record/envelope.go)), and `requestEnvelope` ([`tools/tillr-drain/drain/once.go`](../../tools/tillr-drain/drain/once.go)) sets no correlation id, so the envelope a run receives carries none and the gap is *wider* than the substrate: a substrate fix alone still leaves the tick unrecoverable. There is a second gap beside it: [`session-recording.md`](./session-recording.md)'s env propagation is not built either — `env_refs` is the empty list ([`tools/tillr-drain/drain/policy.go`](../../tools/tillr-drain/drain/policy.go)) and `run exec` refuses a non-empty one (SUB-2), so the only identity a run receives is `TILLR_RUN_ID` and `TILLR_LANE_ID`.

The store's append primitive has no such field either:

```go
type Event struct {
	Seq        int64
	Kind       string
	ObjectType string
	ObjectID   *string
	Revision   *int64
	Payload    json.RawMessage
	At         time.Time
}
```

That is [`tools/tillr/event/append.go`](../../tools/tillr/event/append.go), `Event`, in full. There is no `CorrelationID`, no `TraceKey`, no `SessionID`. `record.Envelope` carries `correlation_id` and `idempotency_key`; `event.Event` carries neither. So an event row carries the trace **only if its emitter puts it in `payload`**, and no emitter does today: the measured `run.allocate` payload is `{"run_id","item_id","backend","lane_id","started_at","leased_until"}` — no tick, no session, no span.

What a join can recover from the tillr store today, part by part: **`run_id`** yes (`runs.run_id`, the `run.allocate`/`run.finish` payloads, `obs_events.run_id`); **`item_id`** yes (`runs.item_id`, the `item.verdict` payload); **`tick_id`, `session_id`, `span_id`** no. And an `item.verdict` cannot smuggle a run id in its payload: `RecordVerdict` re-marshals `verdictPayload` ([`tools/tillr-item/item/verdict.go`](../../tools/tillr-item/item/verdict.go)), so an unknown key is dropped. The practical consequence is that `proc.exec` and `llm.turn` tie to a run by `run_id`, while `item.verdict` ties only by `item_id` plus a timestamp — ambiguous the moment an item has more than one run. One thing is already in place: the `events` table has a `session_id` column, retained and never written ([`tools/tillr/event/append.go`](../../tools/tillr/event/append.go)), and the only schema that keeps all five columns, `obs_events`, has ingested nothing yet.

Everything in [§3.4](#34-proposed-events-and-fields) therefore depends on a substrate change first: an optional `CorrelationID string` and `TraceKey string` on `event.Event`, written into columns (one of the five, `session_id`, already exists and is never written) and surfaced by `eventRecord` in [`tools/tillr-view/source.go`](../../tools/tillr-view/source.go). Without it, an emitter that "carries a correlation id" can only smuggle it through `payload`, which is what makes a join a string search rather than a join.

### 3.4 Proposed events and fields

**Proposed; none of this exists.** Four events, one per seam an AutoQA harness must reconstruct, each `kind` registered by the utility that owns the transition, each carrying `trace_key` and `correlation_id` as envelope-level fields once [§3.3](#33-the-correlation-gap-is-in-the-substrate-not-the-emitters) lands.

**`gate.plan` as an observed event, emitted by `tillr-gate`** (kind name collides with the record kind on purpose: same fact, two carriers). Fields: `plan_id`, `base_sha`, `tree_sha`, `registry_digest`, `checks_count`, `check_ids[]`, `in_process[]`, `duration_ms`. Proposed:

```json
{
  "kind": "gate.plan",
  "object_type": "run",
  "object_id": "run-1130-lane-1",
  "correlation_id": "tick-20261113-190001",
  "payload": {
    "trace_key": "tick-20261113-190001/1130/run-1130-lane-1/01a1c78c/sh-00007",
    "plan_id": "7ddd8a82...69b1",
    "base_sha": "<sha>",
    "tree_sha": "<sha>",
    "registry_digest": "1118dba5...6446",
    "checks_count": 3,
    "check_ids": ["documents", "structural/the-integrity-set-is-planned-every-run"],
    "in_process": ["file-size"],
    "duration_ms": 41
  }
}
```

Note the `created_at` problem this avoids: measured, the `gate.plan` record's envelope carries `"created_at":"0001-01-01T00:00:00Z"`, because `Plan` derives `plan_id` from `(registry, change, root)` and deliberately has no clock. A plan **record** is clockless by design; a plan **event** cannot be.

**`gate.verdict` as an observed event, emitted by `tillr-gate`.** Fields: `verdict_id`, `plan_id`, `registry_digest`, `base_sha`, `tree_sha`, `result`, `check_ids[]`, `failed_check_ids[]`, `errored_check_ids[]`, `check_timings_ms{}`, `gate_program`, `logs_dir`. The three-way split of `failed` from `errored` is the load-bearing part: `ResultError` exists precisely so a candidate that was not judged is never read as a candidate that was rejected ([`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go), the `ResultPass`/`ResultFail`/`ResultError` block).

**`run.exec` as a start event, emitted by `tillr-run`.** This one already has half a home: `run.allocate` is appended in the transaction that claims the lane. What it lacks is `argv`, `cwd`, `backend`, `timeout_ms` and the trace. Proposal: extend the `run.allocate` payload rather than add a sixth kind, so the claim and the command are one row and cannot disagree.

**`land.attempt` as an observed event, emitted by `tillr-land`.** Today the attempt is a record only, and the source says why: "`land.attempt` is NOT registered: it is a stdout record kind, never an event — the transition it describes is `land.land`". A **refused** or **isolated** attempt is therefore invisible to `tillr-view events`, even though it is the single most interesting thing an AutoQA harness wants to count. Fields: `candidate_id`, `attempt_id`, `result` (`landed` / `isolated` / `refused`), `reason`, `carried`, `gated_tree`, `landed_tree`, `verdict_id`, `trunk_before`, `trunk_after`.

### 3.5 The minimum set for reconstruction

If only three things are built, these three, in this order:

1. **`correlation_id` and `trace_key` on `event.Event`** ([§3.3](#33-the-correlation-gap-is-in-the-substrate-not-the-emitters)). Without it everything else is a string search.
2. **`gate.verdict` as an event.** It is the only judgment an AutoQA harness exists to second-guess, and today it lives only in a file whose path the harness must be told.
3. **`land.attempt` for the `refused` and `isolated` results.** A landing that declined is the answer to "why is this item still in the queue", and `land.land` alone cannot express it.

`gate.plan` and a start event are for the timeline and its durations; they make a reconstruction legible, not possible.

---

## 4. Refusal and exit contracts

### 4.1 `tillr-run exec` answers 0 for a failed run

This is the defect that matters most to an unattended harness — and, measured on 2026-11-13, it is a **deliberate contract rather than a bug**: [`tools/tillr-run/main_test.go`](../../tools/tillr-run/main_test.go)'s `TestExecRecordsANonzeroExitAsData` pins it with the sentence "the run's failure is data, not the utility's".

```console
$ tillr-run exec --store ./state.db --lane-root ./lanes --input ./req2.json --now 2026-11-13T00:00:00Z
{"record_version":1,"kind":"run.record",...,"payload":{"run_id":"run-2",...,"state":"exited","exit_code":3,...
$ echo $?
0
```

The request's command was `/bin/sh -c 'echo boom; exit 3'`. The run's exit status is `3` **in the record** and the face's own exit status is `0`. The reading is defensible (the utility ran and answered the question "what happened?"), and it is exactly wrong for a harness: `set -e`, a `&&` chain, and a CI step all read `0` as a green.

The accurate sentence is: **`tillr-run exec` exits 0 for any completed run, whatever the run's own exit code; that code is in the record and in the store.** Exit 0 means the utility answered, so a shell-driven caller must decode the record to learn the run's outcome.

`tillr-drain` does not hit this because it decodes the record and never reads the face's exit ([`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go), `decodeRunRecord`; `ClassifyAttempt` reads `rec.State`/`rec.ExitCode`). An AutoQA driver that shells out has to do the same, and nothing in the flag contract says so. Propagating the code is cheaper than it looks — `steps.go` fails only when the process errored *and* printed no record, so a non-zero face exit beside a record on stdout is already tolerated — but it would rewrite a pinned contract, which is why it is a ruling rather than a repair. See [OQ-AQA3](#OQ-AQA3).

### 4.2 One condition, three exit codes

Measured in [§1.3](#13-the-real-coupling-the-stores-schema-is-a-function-of-who-opened-it): a store missing the caller's tables answers **5** from `tillr-item show` and `tillr-land list`, and **3** from `tillr-run lanes`. Only `tillr-run` maps it, and by substring match on SQLite's message text ([`tools/tillr-run/run/read.go`](../../tools/tillr-run/run/read.go)):

```go
if strings.Contains(err.Error(), "no such table") {
    return Incompatiblef("%v", err)
}
```

A harness cannot write one branch for "the store is not prepared for this utility". Two ways out, both decisions: give every utility that mapping, or make `tillr-store init` link all four DDL registrations so the condition stops existing. The second is strictly better and cannot be decided here.

### 4.3 Where the codes are already good

Worth stating, because it is most of the contract and it is right:

- **The five codes are defined once and mean one thing each**: `0` positive, `1` negative decision, `2` usage or malformed input, `3` incompatible, `4` external, `5` bug ([`tools/tillr/record/exit.go`](../../tools/tillr/record/exit.go)). Six of the eight binaries map their errors through a per-binary `exitError` so the code is fixed where the outcome is known.
- **`tillr-gate plan` distinguishes a corrupt registry from a bad invocation.** Measured:
  ```console
  $ tillr-gate plan --registry ./badreg.json --input ./change.json --root /workspace --output ./p2.json
  tillr-gate plan: the check registry ./badreg.json carries digest "000...0" but its declarations and in-process checks hash to "bac7b01f...3ef"; regenerate it with `python3 tools/fork-check.py --registry-json`
  $ echo $?
  3
  ```
- **`tillr-run status` answers 1, not 4, for a run it does not hold**, and still prints a record (`found: false`). That is the pattern the whole suite should follow: a negative decision is an answer, not a diagnostic.
- **`tillr land check` and `pass` answer 1 for `isolated` and `refused`** and 0 only for `landed` ([`tools/tillr-land/main.go`](../../tools/tillr-land/main.go), `attemptFace`).

### 4.4 Verdicts are two shapes with one kind

`tillr-gate run` produces a `gate.verdict` whose `checks[]` holds either the plan's own checks (no `--gate`) or exactly one synthetic entry (`--gate`, measured: `check_id: "gate:true"`). `tillr-land` reads only `verdict_id`, `base_sha`, `tree_sha` and `result` ([`tools/tillr-land/land/verdict.go`](../../tools/tillr-land/land/verdict.go), `checkVerdict`) and says out loud why it will not arbitrate the rest. So the shape is legal and unspecified at the same time: an AutoQA harness that wants per-check outcomes cannot require them, because a legal producer omits them.

A proposal that widens nothing: add a `checks_source` field to the verdict payload with values `plan` or `gate_program`, so a consumer can tell "the plan's checks, all of them" from "one opaquer step and no per-check detail" without inferring it from `checks[0].check_id`.

### 4.5 A negative decision with no record; a record with no clock

Two smaller gaps, both cheap to close:

- **`tillr-item`'s usage and vocabulary refusals print to stderr and emit no record**, while its rejections emit an `item.rejection`. Both are visible in [§2.1](#21-tillr-item-verdict-the-record-is-the-interface-there-are-no-value-flags). A harness that wants one parseable stream for every outcome has to merge stdout and stderr and special-case the format.
- **`tillr-view status` answers `null` where `tillr-view events` answers `[]`.** Measured on a store with no items: `{"item":null,"questions":null,"questions_source":"","candidates":null,"runs":null}`. The sibling face's source comment states the opposite rule outright: "An empty read is an empty list, not null: a caller that iterates the field should not have to special-case the absence of events" ([`tools/tillr-view/main.go`](../../tools/tillr-view/main.go), `eventsFace`). Two faces, two rules.

### 4.6 Two faces print a usage line no parser can attribute

`tillr-drain` answers `a face is required: plan, once, boundary or loop` with **no binary prefix**, where the other seven answer `tillr-<noun>: ...`. A harness that classifies refusals by `^(tillr-[a-z]+): ` classifies this one as an unknown format. Also worth noting for the pack's own docs: the measured `tillr-land` line is `a face is required: enqueue, check, pass, one, list, show or cancel` and the measured `tillr-view` line is `a face is required: status, events or serve`, while [`tillr-distribution.md`](./tillr-distribution.md#1-one-pack-eight-programs-and-what-path-sees)'s measured table records `enqueue, check, pass` and `status or events`. The table is stale; the binaries are the contract.

---

## 5. Findings that want a ruling

Ordered by how much they cost an unattended harness.

1. **`tillr-run exec` exits 0 on a failed run** ([§4.1](#41-tillr-run-exec-answers-0-for-a-failed-run)). A deliberate contract, pinned by a test, that a shell driver reads as green. [OQ-AQA3](#OQ-AQA3).
2. **`event.Event` has no correlation field, and nothing populates the one it would carry** ([§3.3](#33-the-correlation-gap-is-in-the-substrate-not-the-emitters)). The `trace_key` can only ride in `payload`, no emitter puts it there, and three of its five parts (`tick_id`, `session_id`, `span_id`) are not stored anywhere, so every "reconstruct the run" query starts with a string search through payloads.
3. **`tillr-gate` emits no event, and `land.attempt` is not one** ([§3.2](#32-what-the-gate-emits-today-nothing)). The gate's judgment and the land's refusals are the two facts an AutoQA harness exists to inspect and neither is in `tillr-view events`. [OQ-AQA2](#OQ-AQA2).
4. **`tillr-store init` creates a store three binaries cannot use**, and they say so with two different exit codes ([§1.3](#13-the-real-coupling-the-stores-schema-is-a-function-of-who-opened-it), [§4.2](#42-one-condition-three-exit-codes)).
5. **There is no verdict, actor kind or claim class for an automated reviewer** ([§2.1](#21-tillr-item-verdict-the-record-is-the-interface-there-are-no-value-flags), [§2.2](#22-request-human-review-has-no-verdict)). `mechanical` is the only legal spelling and it means something else. [OQ-AQA1](#OQ-AQA1).
6. **`decided_by` has no AutoQA value**, so a decision recorded by a harness attributes itself to the whale ([§2.3](#23-tillr-item-decision-two-of-its-fields-exclude-autoqa-by-construction)).

---

## Decision Ledger

Owner-decided 2026-11-13. On 2026-11-16, `D-AQA1` was superseded by `D-JL7` in
[the judge and lifecycle design](./tillr-judge-and-lifecycle.md#5-core-boundary-for-external-autoqa-d-jl7).
AutoQA now uses `mechanical` verdicts for runners and grant-backed `agent` verdicts for
reviewers, with its identity in `actor`; new writes of the retired values are refused,
while existing records remain unchanged and readable. `D-AQA2`, `D-AQA3`, and `D-AQA4`
stand. The question blocks below preserve the original deliberation, not live rulings.

| ID | Decision | Rationale | Enforced By |
|---|---|---|---|
| `D-AQA1` | **Superseded by `D-JL7` on 2026-11-16: AutoQA uses the ordinary verdict kinds and claim classes, not `actor_kind: "autoqa"`, `qa-verification`, or `decided_by: "autoqa"` on new writes.** | An automated reviewer must use the same recorded, unexpired grant as every other agent; the `actor` identity distinguishes AutoQA without a special kind. | `tools/tillr-item` (new-record validation; legacy records and the historical schema check are preserved) |
| `D-AQA2` | **The gate stays store-free; the loop publishes the gate observation.** | The no-store face is the gate's stated adoptability claim, and the loop already takes a span per gate invocation. | `tools/tillr-drain` (`ProcessSteps`), `tools/tillr-gate` (unchanged) |
| `D-AQA3` | **`tillr-run exec` answers the run's own exit code, reserving 2/3/4/5 for the face's own failures.** | A standalone driver's only portable signal is the exit status, and today it reads a failed run as a green one. | `tools/tillr-run` (`exec`), the module READMEs, `tools/tillr-drain` (unchanged: it decodes the record) |
| `D-AQA4` | **`tillr-gate change` and `tillr-run request` are faces, not a shared library.** | Both records currently have exactly one producer, and it is the loop — the one thing a standalone driver is trying not to run. | `tools/tillr-gate`, `tools/tillr-run` |

## Open questions

- 💬 **[OQ-AQA1](#OQ-AQA1) — Does AutoQA get its own verdict actor kind and claim class, or does it record every QA judgment as `mechanical`?**

  A QA harness judging a landed candidate is an agent judging work it did not do, which is exactly what `actor_kind: "agent"` describes, except that `agent` requires a `grant_id` naming the claim classes it may sign off, and no grantor exists for a QA harness. Recording as `mechanical` is legal today and makes every QA verdict indistinguishable from the pipeline retiring its own work. Recording as `agent` needs a grant that nothing issues.

  - **A — Add `actor_kind: "autoqa"` plus a `qa-verification` claim class, with no grant required and `judged_actor` mandatory.** Every QA verdict becomes visible as a QA verdict in `tillr-view`, at the cost of a fifth actor in a three-actor vocabulary that [`agent-runtime.md`](./agent-runtime.md) treats as settled.
  - **B — Record every QA verdict as `mechanical` and carry the harness's identity in `reason` and `evidence`.** No vocabulary change and no migration; the cost is that "a machine checked the machine" and "the pipeline retired its own work" look the same in every read view and every query.

  <!-- vantage: question id=OQ-AQA1 leaning="A — add an autoqa actor kind and a qa-verification claim class, because a mechanical verdict today already means the pipeline retiring its own landed work, and folding an independent reviewer into that name makes QA unrecoverable from the read model." -->

  _Leaning:_ **A (add an `autoqa` actor kind and a `qa-verification` claim class).** The distinction AutoQA produces is the one the read model most needs: an independent judgment, not the pipeline confirming itself.

  **Answer:**
  > **A — add `actor_kind: "autoqa"` and a `qa-verification` claim class, with no grant required and `judged_actor` mandatory.** Owner-decided 2026-11-13, without a designer pass: collected for post-hoc designer advice rather than settled by one. An independent QA verdict must stay distinguishable from the pipeline retiring its own work in every read view.

- 💬 **[OQ-AQA2](#OQ-AQA2) — Does `tillr-gate` gain a `--store` sink so its plan and verdict become observable events, or does the observation stay a file plus an adapter that publishes it?**

  [`tools/tillr-gate/plan.go`](../../tools/tillr-gate/plan.go) and [`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go) both state that their record kinds are "never an event: … appending it would claim a state transition that does not exist". That is right about authority. It also leaves the gate's judgment reachable only through `.tillr/loop/<run-id>/gate.verdict.json`, so a reconstruction has to be told where the file is.

  - **A — Add an optional `--store` to `tillr-gate run`, appending a distinct `gate.observed` event and leaving `gate.plan`/`gate.verdict` as record kinds.** The gate keeps the no-store default that [`board-utilities.md`](./board-utilities.md) [§5.7](./board-utilities.md#57-gate-alone) and [§5.11](./board-utilities.md#511-where-the-burden-of-proof-sits) make its adoptability claim, and a caller that wants observation asks for it.
  - **B — Leave the gate store-free and have `tillr-drain`'s `ProcessSteps` append the observation, as it already appends `steps.jsonl` spans.** No new flag on a utility whose point is standing alone, at the cost that a gate run outside the loop is still unobserved.

  <!-- vantage: question id=OQ-AQA2 leaning="B — keep the gate store-free and have the loop's ProcessSteps publish the gate observation, because the no-store face is the gate's stated adoptability claim and the loop already records a step span for every gate invocation." -->

  _Leaning:_ **B (the loop publishes the gate's observation).** It preserves the ruling both gate faces state in their own source and reuses the span the loop already takes.

  **Answer:**
  > **B — the gate stays store-free, and the loop's `ProcessSteps` publishes the gate observation.** Owner-decided 2026-11-13, without a designer pass. Already built: the loop appends `boundary.check`, `gate.plan` and `gate.verdict` with `run_id`/`item_id`/`lane_id`/`tick_id` (commit `2050271d4a`). **Flagged for the next designer pass:** the cost this ruling accepts is that a gate run *outside* the loop is still unobserved, which is exactly the standalone driver AutoQA is, so a designer should say whether option A's `--store` sink is worth adding beside this.

- 💬 **[OQ-AQA3](#OQ-AQA3) — Does `tillr-run exec` keep answering 0 for a run whose command failed?**

  Measured, a request whose command exits 3 produces a `run.record` with `exit_code: 3` and a face exit of 0. `tillr-drain` reads the record and is unaffected; anything shell-driven reads a green.

  - **A — Keep 0 and document it as the record-not-judgment rule, with the record's `exit_code` named in every module README's Known Gaps.** No behavior change and no risk to `tillr-drain`, at the cost that the one contract an unattended harness gets wrong is the one that has to be read from prose.
  - **B — Answer the run's own exit code when the request did not name one to reserve, and keep 2/3/4/5 for the face's own failures.** A shell driver then behaves; the cost is that the face's exit is sometimes the run's and sometimes the utility's, which needs a stated rule to be readable at all.

  <!-- vantage: question id=OQ-AQA3 leaning="B — propagate the run's exit code and reserve 2/3/4/5 for the face's own failures, because a standalone driver's only portable signal is the exit status and today it reads a red run as a green one." -->

  _Leaning:_ **B (propagate the run's exit code).** The current contract is only safe for a caller that decodes the record, which is a property of `tillr-drain` rather than of the interface.

  **Answer:**
  > **B — propagate the run's own exit code, keeping 2/3/4/5 for the face's own failures.** Owner-decided 2026-11-13, without a designer pass. `tillr-drain` decodes the record and already tolerates a non-zero face exit beside one, so the only caller whose behaviour changes is the shell driver that currently reads a red run as a green.

- 💬 **[OQ-AQA4](#OQ-AQA4) — Who produces `gate.change` and `run.request` for a driver that will not run `tillr-drain`?**

  Both records have exactly one producer each, and both are the loop: `tillr-loop`'s gate adapter derives `gate.change` ([`tools/tillr-loop/README.md`](../../tools/tillr-loop/README.md), the `gate` row) and `ProcessSteps.Run` writes `run.request` ([`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go)). An AutoQA driver that drives the binaries directly must author both by hand, which means two more places that can spell a record wrong.

  - **A — Add two small faces, `tillr-gate change` and `tillr-run request`, each deriving the record from a worktree and a candidate.** One record shape, one spelling, one place to keep it right; the cost is two more faces on two binaries whose standalone claim is their face count.
  - **B — Extract the derivation into the library each binary already imports and have `tillr-loop` call it.** No new faces; the cost is that a Go-only library is not reachable from a shell driver, which is the caller that needs it.

  <!-- vantage: question id=OQ-AQA4 leaning="A — add a change face to tillr-gate and a request face to tillr-run, because both records currently have exactly one producer and it is the loop, which is the one thing a standalone driver is trying not to run." -->

  _Leaning:_ **A (a `change` face and a `request` face).** "Drive the binaries without the loop" is only true if every record the binaries consume has a producer outside the loop.

  **Answer:**
  > **A — add `tillr-gate change` and `tillr-run request`, each deriving its record from a worktree and a candidate.** Owner-decided 2026-11-13, without a designer pass. A shell driver cannot import a Go library, so the two faces are what makes "drive the binaries without the loop" true.
