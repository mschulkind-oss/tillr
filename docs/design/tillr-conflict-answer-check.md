---
title: "Before a designer's conflict answer lands, a fresh review checks its code and config claims against the tree"
status: in-review
stage: DESIGN
tags:
  - tillr
  - drain
  - conflict
  - review
  - workflows
  - design
summary: "A designer's answer to a raised definition conflict lands after only the fast document checks, so a specification that names a missing Go symbol, a rejected workflow key, a wrong hook name or a strict-JSON rule the tracked config files fail lands on main and stops the next planner or builder. This design runs conflict-design dispatches as a two-stage workflow (conflict-answer@v1: answer on design, then check on review) without overwriting the blocked item's own workflow selection. check reads the raised conflict, the committed diff and the live tree across five closed factual categories; any contradiction withholds landing and returns exact tree proof to the warm designer session, stopping for the owner only on an identical repeat. Nothing is built."
next: "The owner reviews D-CA1 through D-CA6 in the Decision Ledger; nothing here is built until reviewed."
depends-on:
  - tillr-review-rules.md
  - tillr-diff-review.md
  - tillr-judge-and-lifecycle.md
  - ../reference/tillr-item-graphs.md
vantage:
  status-chip: true
---

# Before a designer's conflict answer lands, a fresh review checks its code and config claims against the tree

**Status:** Nothing is built: `rg -n 'conflict-answer@|answer-contradiction' tools config` returns nothing. The rulings this design builds within are the owner's and live in their home ledgers ([Section 2.2](#22-inherited-owner-rulings-and-principles)); `D-CA1` through `D-CA6` are the designer's decisions and await the owner's review in the [Decision Ledger](#decision-ledger). No companion plan is opened until they are reviewed.

> **In short.** Every committing `conflict-design` answer runs through a two-stage conflict workflow (`conflict-answer@v1`: `answer` on `design` followed by `check` on `review`) before the fast gate squashes it onto `main` and unblocks the waiting item. `check` verifies every code symbol, Go type shape, decoder allowlist, hook table and tracked configuration file the answer relies on; any contradiction blocks landing and returns exact tree proof to the warm `answer` session, stopping for the owner only on an identical repeat.

**Why it matters.** A conflict answer prescribes exact Go types, CLI flags, JSON schemas and config invariants for a planner or builder that is blocked waiting on it, and it lands with no code inspection. An answer that contradicts the tree costs the resumed step a full run and a second block.

**The shape.** Three decisions:
- **What check, and on which seat** ([Section 3](#3-what-check-a-conflict-answer-gets-and-on-which-seat)): a post-write, read-only `check` stage on a fresh `review` session auditing five closed categories of falsifiable tree and config claims (`D-CA1`, `D-CA3`, `D-CA4`), never design taste or scope ([OQ-RJ4](./tillr-review-rules.md#decision-ledger)).
- **In which workflow** ([Section 4](#4-which-workflow-runs-it-and-how-it-fits-the-blocked-item)): a tracked workflow `conflict-answer@v1` (`answer` -> `check`) attached to `conflict-design` dispatches, leaving the blocked item's own `workflow_selections` row untouched (`D-CA2`, `D-CA5`). A non-committing objection (`NEXT-ACTOR: owner` or `NEXT-ACTOR: build`) settles directly after `answer` without running `check`.
- **When a contradiction is found** ([Section 5](#5-what-happens-when-the-check-finds-a-contradiction)): `main` does not move, `item.conflict` stays open on `design`, and the structured contradiction table returns uncounted to `answer`'s warm session at its pinned candidate commit; an identical repeat stops for the owner ([OQ-DR2](./tillr-diff-review.md#decision-ledger), `D-CA6`).

**Cost.** One read-only `review` run of a minute or two per committing conflict answer, and none on a non-committing objection. A wrong answer costs a `plan` run, a second `conflict-design` run and a second `plan` run.

**Needs your ruling:** None.

**Awaits your review:** the designer's decisions `D-CA1` through `D-CA6` in the [Decision Ledger](#decision-ledger). You may overturn any of them.

**Reads with:** [review rules](./tillr-review-rules.md) (the narrow `review`-seat fact-and-ledger audit), [landing diff review](./tillr-diff-review.md) (the fresh reader, the evidence key, the repeated objection), [the judge and lifecycle design](./tillr-judge-and-lifecycle.md) ([`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9) and [`D-JL32`](./tillr-judge-and-lifecycle.md#419-a-worker-returned-by-a-design-answer-starts-from-a-trunk-that-holds-the-answer-d-jl32)), [item graphs](../reference/tillr-item-graphs.md) (workflow stage, edge and output rules), [designer scouts](./tillr-designer-scouts.md) (in-session `scout` subagents during drafting), and [request amendments](./tillr-request-amendment.md) (when a blocked filed request also edits `docs/`).

---

## 1. The gap

### 1.1 How a conflict answer lands unchecked

A raised `definition` obstacle moves through five steps with no code verification:

1. **Conflict recorded.** When a `plan` or `build` stage stops with `OBSTACLE: definition`, `reportedBlock` in [blocked.go](../../tools/tillr-drain/drain/blocked.go) appends an `item.conflict` event with `status: "raised"` and `waiting_on: "design"`.
2. **Dispatch with no workflow.** `conflictDecision` in [blocked.go](../../tools/tillr-drain/drain/blocked.go) creates a `design` dispatch with reason `conflict-design`. `Plan` in [plan.go](../../tools/tillr-drain/drain/plan.go) skips workflow lookup whenever `decision.BlockConflict != nil`, so the blocked item's own workflow does not attach to the conflict turn and `conflict-design` runs with no workflow.
3. **Documents-only fence.** `blockConflictPaths` in [stage.go](../../tools/tillr-drain/drain/stage.go) restricts the candidate's write fence to `docs/` (for a filed request) or the blocked item's design documents (for a block-backed item).
4. **Evaluator skipped, fast gate only.** `verify` in [loop.go](../../tools/tillr-drain/drain/loop.go) runs `judgeCandidate` only when `candidate.BlockConflict == nil`, and because every changed file is Markdown under `docs/`, the gate runs only the document checks, which take seconds.
5. **Trunk advances and wakes the worker.** The candidate is squashed onto `main` and `recordBlockAnswer` in [blocked.go](../../tools/tillr-drain/drain/blocked.go) sets `item.conflict` to `status: "answered"`, `waiting_on: "build"`, so the blocked item re-enters `plan` or `build`.

Nothing in that path opens a `.go` file or a tracked file under `config/tillr/` to confirm that the functions, struct fields, JSON tags, decoder allowlists or configuration invariants the answer names exist. Conflict design is also the busiest `design` path: in a two-day sample it was 97 of 178 `design` runs ([designer scouts Section 1.2](./tillr-designer-scouts.md#12-where-design-runs-and-tool-calls-went)).

### 1.2 The kinds of claim that go wrong

Four kinds of contradiction have been observed in one landed answer, each mechanical to falsify by comparing the answer's diff with the files it names. They are the basis of the categories in [Section 3.3](#33-the-five-closed-check-categories-d-ca3):

| # | What the answer states | What the tree holds |
| :- | :--- | :--- |
| **1** | A stage `kind` value and a field name for a repeating stage. | The loader's allowlist (`workflowFields` in [workflow_check.go](../../tools/tillr-drain/workflow/workflow_check.go)) accepts neither; repetition is spelled another way. |
| **2** | Two hook names in a list of the hook flag table's entries. | `hookFlags` in [hooks.go](../../tools/tillr-drain/drain/hooks.go) has no such cases. |
| **3** | A rule that every policy file must decode with unknown keys rejected. | Every tracked `config/tillr/backend-policy*.json` carries a top-level key the Go type does not declare, so all of them fail the rule. |
| **4** | A container shape (a list, `len(...) > 0`) and field names for a config entry. | The Go type is a struct, declared through a type alias in another package, with different JSON tags. |

---

## 2. The words and settled rulings

### 2.1 Vocabulary

Node, seat, workflow, ending, evidence key and identical repeat keep the meanings in [item graphs](../reference/tillr-item-graphs.md) and [landing diff review Section 2](./tillr-diff-review.md#2-the-words). Candidate, gate, lander and trunk keep the meanings in [tillr-vocabulary.md Section 5.4](./tillr-vocabulary.md#54-checking-and-landing). Three terms are coined here:

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **Conflict answer** *(coined here)* | A candidate commit under `docs/` written by a `conflict-design` dispatch to resolve a raised `item.conflict` (`OBSTACLE: definition` on a document block or filed request). | Not a non-committing `NEXT-ACTOR: owner` or `NEXT-ACTOR: build` objection, and not the blocked item's own code commit. |
| **Conflict-answer check** *(coined here)* | The read-only `check` stage on `review` in `conflict-answer@v1` that verifies every code, type, decoder, hook and tracked-config claim in a conflict answer against the repository at `head_sha` before landing. | Not criterion retirement on the blocked item, and not critique of design direction or trade-offs. |
| **Tree contradiction** *(coined here)* | One falsified factual or config-compatibility claim in a conflict answer's diff, carrying a closed category, the quoted claim, the inspected repository path or symbol, and what `head_sha` actually holds. | Not a Markdown or style finding, and not an opinion on scope or architecture. |

### 2.2 Inherited owner rulings and principles

Four owner rulings settle the policy boundaries this design builds within. Each is owned by its home ledger:

- **A narrow `review`-seat fact audit on design changes** ([OQ-RJ4](./tillr-review-rules.md#decision-ledger)): a design change gets a narrow fact-and-ledger audit on the `review` seat, not a second opinion on the design.
- **A fresh reader on the named `review` seat** (`D-DR1` and [OQ-DR1](./tillr-diff-review.md#decision-ledger)): the reviewer does not rely on memory of having made the change and reads the whole diff; `review` is a named seat whose backend is chosen in the policy file.
- **An uncounted return to the previous session** ([tillr-graph-is-the-path.md Section 1](./tillr-graph-is-the-path.md#1-the-direction-and-the-rules) and [`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)): a node is not restarted; a resume node continues the previous session. No numeric attempt cap applies to conflict design, and only an identical repeat stops the loop.
- **No automatic dismissal on a repeated disagreement** ([OQ-DR2](./tillr-diff-review.md#decision-ledger)): a repeated objection stops and waits for the owner.

Within those rulings, five principles (`P1`–`P5`, the designer's synthesis) govern the mechanism:

- **P1. Verify the committed diff, not pre-draft exploration.** A pre-write scout cannot check names, struct shapes or strict-decoding rules that the designer invents mid-draft; the check must inspect `base_sha..head_sha` against `head_sha`.
- **P2. Falsifiable tree facts only.** Every finding cites an inspected path or symbol and actual tree content; subjective design critique is forbidden by brief and rejected by schema.
- **P3. Never touch the blocked item's criteria or workflow selection.** Answering a conflict unblocks an item without finishing it; `check` never calls `item.RecordJudge` and never overwrites `workflow_selections(item_id)`.
- **P4. Never advance `main` or mark `answered` on a contradicted specification.** While any contradiction stands, `item.conflict` stays off `build`, so no planner or builder wakes on false premises (`D-JL32`).
- **P5. Resume the warm `answer` session with exact locators; stop for the owner on an identical repeat.**

---

## 3. What check a conflict answer gets, and on which seat

### 3.1 Why a post-write check on `review` beats a pre-write `scout` (`D-CA1`)

Three shapes were weighed for checking a conflict answer against the codebase:

| Candidate check | Catches claims invented while drafting? | Cross-file Go type and config reasoning | Verdict |
| :--- | :--- | :--- | :--- |
| **Pre-write `scout` pass (`scout -> answer`)** | **No.** The contradictions in [Section 1.2](#12-the-kinds-of-claim-that-go-wrong) are written while drafting; a pre-write scout has no draft to check. | Not applicable before a draft exists. | Rejected as the landing check; designers keep in-session `scout` subagents ([designer scouts](./tillr-designer-scouts.md)). |
| **Post-write `scout` stage (`answer -> scout`)** | Reads the diff, but on the cheapest seat. | Weak on a type alias that crosses packages and on invariants that span several config files; diverges from [OQ-RJ4](./tillr-review-rules.md#decision-ledger). | Rejected. |
| **Post-write `review` stage (`answer -> check`)** | **Yes.** A fresh session reads `base_sha..head_sha` against the repository at `head_sha`. | Runs on `review`, the seat [OQ-RJ4](./tillr-review-rules.md#decision-ledger) chose. | **Chosen (`D-CA1`).** |

### 3.2 What `check` is given — and what is withheld (`D-CA3`)

`check` runs with `access: "read_only"` in a fresh `review` session on the candidate worktree at `answer`'s committed `head_sha`. Its staged inputs are:

- **`conflict.json`:** the raised `item.conflict` payload (`OBSTACLE: definition`, `CRITERIA`, and the worker's `EVIDENCE` explaining why the item stopped), which staging writes whenever the dispatch carries a conflict.
- **The blocked item's statement:** the saved request words or the item's block section, for scope context only, not for retiring item criteria.
- **The cumulative conflict-answer diff (`item_base_sha..head_sha`) and the live repository tree at `head_sha`,** read with read-only tools (`git diff`, file reads and `rg`).
- **Prior contradiction records on a re-check:** when `answer` repaired an earlier refusal, the prior `contradictions[]` and `answer`'s updated diff, so the fresh session verifies whether each contradiction was resolved or repeated.

**Withheld:** `answer`'s narrative report. Verification reads the candidate and the tree, not the author's account of it ([landing diff review, "The worker's report"](./tillr-diff-review.md#the-workers-report)).

### 3.3 The five closed check categories (`D-CA3`)

`check` audits every concrete code, schema and configuration claim added or relied on by `item_base_sha..head_sha` across five closed categories:

| Category ID | What `check` verifies against `head_sha` | Explicit carve-out |
| :--- | :--- | :--- |
| `missing-path` | Every repository file, directory, Go package path, CLI subcommand path, gate check script or test file the answer cites as existing, or tells the worker to read, call or edit, exists in the tree. | Paths the answer explicitly marks as **new files or subcommands to create** in the blocked item's build. |
| `wrong-symbol-or-type` | Every existing symbol, struct, type alias (resolved across imports), function signature, constant, flag table or switch enumeration exists with the stated container kind (struct, slice or map), field names and literal cases. | New types, functions or flags the answer explicitly specifies for the builder to add, provided they do not collide with an incompatible existing declaration. |
| `wrong-schema-or-decoder` | Every JSON or TOML field name, stage `kind`, enum literal, allowlist string or strict unknown-key rule attributed to an existing loader or validator matches that loader's code (for example `workflowFields` in [workflow_check.go](../../tools/tillr-drain/workflow/workflow_check.go) and `Policy.UnmarshalJSON` in [policy.go](../../tools/tillr-drain/drain/policy.go)). | Schema extensions the answer explicitly instructs the builder to add to that loader in the same build. |
| `breaks-tracked-config` | Whenever the answer specifies a validation, lint or decoding invariant that existing tracked files must pass (`config/tillr/workflows/*.json`, `config/tillr/backend-policy*.json`, `config/tillr/watch/**`, `config/tillr/commands.json`, `tools/tillr/definition/*.json`), every tracked file in scope satisfies that rule at `head_sha`. | Tracked config files the answer explicitly instructs the builder to change (when within the blocked item's write scope) or explicitly exempts in the rule. |
| `ledger-or-conflict-mismatch` | New or amended `D-*` rows do not silently contradict a settled owner ruling in the same or a `depends-on` Decision Ledger ([OQ-RJ4](./tillr-review-rules.md#decision-ledger)), and no factual error named in `conflict.json`'s `EVIDENCE` remains asserted as true in the amended section. | An explicit `Superseded by` or amendment note where the owner has ruled, or an owner-only conflict raised with `NEXT-ACTOR: owner`. |

**Forbidden critiques.** `check` never objects to architectural taste, decomposition granularity, the naming style of newly proposed symbols, prose tone, or whether an alternative design would be cleaner ([OQ-RJ4](./tillr-review-rules.md#decision-ledger)).

### 3.4 Verdict artifact contract (`D-CA4`)

`check` declares `outputs: {"verdict": "json"}` and writes `artifacts/verdict` in its run directory. The file is checked first by `checkNodeOutputs` in [node_outputs.go](../../tools/tillr-drain/drain/node_outputs.go) and then by a Go schema validator before settlement. An example:

```json
{
  "verdict": "contradiction",
  "read_paths": [
    "docs/design/example.md",
    "tools/tillr-drain/drain/hooks.go"
  ],
  "contradictions": [
    {
      "category": "wrong-symbol-or-type",
      "path": "docs/design/example.md",
      "claim": "Section 7.2 lists preserve and worktree among the hookFlags entries.",
      "checked": "tools/tillr-drain/drain/hooks.go#hookFlags",
      "symbol_or_key": "hookFlags:preserve,worktree",
      "actual": "hookFlags has no preserve or worktree case; both return nil."
    }
  ]
}
```

Validation rules enforced in Go on `artifacts/verdict`:
- `verdict` is `"pass"` or `"contradiction"`.
- `read_paths` includes every file changed in `item_base_sha..head_sha` (`D-DR11`); when `verdict == "pass"`, `contradictions` is empty and `read_paths` also includes at least one path outside `docs/` whenever the diff cites repository code or `config/` paths.
- When `verdict == "contradiction"`, `contradictions` is non-empty, and every element carries a `category` from the five closed values in [Section 3.3](#33-the-five-closed-check-categories-d-ca3) plus non-empty `path`, `claim`, `checked`, `symbol_or_key` and `actual` strings. An entry without an inspected `checked` locator and an `actual` tree state is rejected as a malformed verdict rather than sent to the designer.

---

## 4. Which workflow runs it, and how it fits the blocked item

### 4.1 The `conflict-answer@v1` workflow (`D-CA2`, `D-CA5`)

Every `conflict-design` dispatch for a raised conflict waiting on `design` runs a tracked two-stage workflow `conflict-answer@v1` under `config/tillr/workflows/`, using only keys that `workflowFields` in [workflow_check.go](../../tools/tillr-drain/workflow/workflow_check.go) and `validateOutputWiring` in [node_outputs.go](../../tools/tillr-drain/drain/node_outputs.go) accept:

```json
{
  "name": "conflict-answer",
  "version": 1,
  "stages": [
    {
      "id": "answer",
      "kind": "agent",
      "seat": "design",
      "access": "write",
      "prompt": "Answer the raised definition conflict in conflict.json. Confirm every existing code symbol, Go type, decoder allowlist, hook flag, and tracked config file your answer relies on before committing; if only the owner can decide or the existing spec already answers the worker, commit nothing and end blocked naming NEXT-ACTOR: owner or NEXT-ACTOR: build."
    },
    {
      "id": "check",
      "kind": "agent",
      "seat": "review",
      "access": "read_only",
      "outputs": {
        "verdict": "json"
      },
      "prompt": "Audit the committed conflict answer against conflict.json and the live repository tree across the five factual categories (missing-path, wrong-symbol-or-type, wrong-schema-or-decoder, breaks-tracked-config, ledger-or-conflict-mismatch). Write artifacts/verdict as strict JSON; never critique design taste or scope."
    }
  ],
  "edges": [
    {
      "from_node": "answer",
      "to_node": "check"
    }
  ],
  "exit": {
    "requires_nodes": ["answer", "check"],
    "requires_candidate": "commit_required"
  }
}
```

```mermaid
flowchart LR
    A["answer (seat: design, write)"] -- "no commit + NEXT-ACTOR: owner/build" --> S["settle objection directly (no check run)"]
    A -- "commits docs/ answer" --> F["fence + item-block + fast doc checks"]
    F -- "refused" --> A
    F -- "green" --> C["check (seat: review, read_only)"]
    C -- "contradiction (first or changed key)" --> A
    C -- "contradiction (identical repeat)" --> O["conflict-owner (needs you)"]
    C -- "pass" --> L["gate verdict + squash land + recordBlockAnswer"]
```

Three execution rules in `settleNode` ([workflow.go](../../tools/tillr-drain/drain/workflow.go)) make `conflict-answer@v1` fit conflict resolution:

- **A non-committing objection bypasses `check` (`D-CA5`).** When `answer` commits nothing and either ends `RESULT: blocked` or names `NEXT-ACTOR: owner` or `NEXT-ACTOR: build` (or omits an actor on an empty diff), `settleNode` delegates straight to `reportedBlock` and `settleDesignStatement` ([blocked.go](../../tools/tillr-drain/drain/blocked.go), [design_repeat.go](../../tools/tillr-drain/drain/design_repeat.go)) and ends the workflow attempt without dispatching `check`. Only a committing answer advances to `check`.
- **Mechanical checks that spend no model run come first (`D-CA5`).** Before advancing from `answer` to `check`, `settleNode` runs the conflict write-fence check (`blockConflictPaths`), the guard that the item's block was neither removed nor forbidden content added, and the fast document checks. Any mechanical failure returns to `answer`'s warm session without spending a `review` run, matching the rule that free checks filter broken candidates before a paid reviewer runs ([landing diff review Section 4.3](./tillr-diff-review.md#43-the-position-after-the-gate)).
- **`check` never uses the evaluator slot or `item.RecordJudge` (`D-CA2`).** `judgeCandidate` in [judge.go](../../tools/tillr-drain/drain/judge.go) checks `ReadEvaluatorBinding` against `workflow_selections` and calls `item.RecordJudge` to retire the item's own criteria, and `verifyBuildSession` in [verify_return.go](../../tools/tillr-drain/drain/verify_return.go) splices a `build`-seat repair node when the writer was not `build`. None of that belongs on a conflict answer: `check` settles through its `verdict` artifact, leaving the blocked item's criteria untouched.

### 4.2 Keeping the blocked item's own workflow intact (`D-CA2`)

`workflow_selections` is keyed by `item_id` and holds the blocked item's own delivery workflow. Overwriting it when a conflict is raised would erase the item's build workflow once the conflict is answered. Instead (`D-CA2`):

- `Plan` in [plan.go](../../tools/tillr-drain/drain/plan.go) keeps leaving `workflow_selections(item_id)` untouched whenever `decision.BlockConflict != nil`.
- For a `conflict-design` dispatch, `Plan` loads `conflict-answer@v1` from `config/tillr/workflows/` and attaches it directly to `decision.Workflow`, keyed in `node_runs` by the conflict attempt. Because `conflictDecision` is the only planner entry point for a raised `definition` conflict, no conflict answer can bypass `check` by selecting another workflow.
- **Exemptions:** the document-triage dispatch (`ReasonTriageNoBlock` in [document_prerequisite.go](../../tools/tillr-drain/drain/document_prerequisite.go)) and the `ownerMigration` command in [plan.go](../../tools/tillr-drain/drain/plan.go) keep their single-step behavior. A `conflict-design` run in flight when the workflow ships finishes under its recorded snapshot; any new or re-raised conflict starts `conflict-answer@v1`.
- **Composition with sibling designs:** when a filed-request amendment also edits `docs/` ([tillr-request-amendment.md Section 6.4](./tillr-request-amendment.md#64-when-a-design-document-also-changes)), `conflict-answer@v1` checks both the `docs/` diff and any code or config names in the amendment text; an amendment that commits nothing and names no tree path or symbol skips `check`. Once the post-gate router in [tillr-review-rules.md](./tillr-review-rules.md) ([OQ-RJ4](./tillr-review-rules.md#decision-ledger)) is built, a candidate carrying a passing `check` artifact on `head_sha` skips a duplicate post-gate `review` fact audit.

---

## 5. What happens when the check finds a contradiction

### 5.1 Settlement across all `check` outcomes (`D-CA6`)

| `check` outcome | Trunk and `item.conflict` state | Next action |
| :--- | :--- | :--- |
| **`verdict == "pass"`** (all changed paths read, `contradictions` empty) | `check` settles passed. The fast gate records `gate.verdict` on `(base_sha, head_sha^{tree})`, `tillr-land` squashes onto `main` with `answer`'s commit message, and `recordBlockAnswer` marks `item.conflict` `answered` (`waiting_on: "build"`). | The blocked item resumes `plan` or `build` on a trunk holding the verified answer (`D-JL32`). |
| **`verdict == "contradiction"`, first or changed contradiction key** | **Nothing lands**; `main` does not move; `item.conflict` stays `status: "raised"`, `waiting_on: "design"` with `DesignRunID` naming the `answer` run; `drain.design-unanswered` is recorded with cause `answer-contradiction`. | The next attempt's `answer` stage **resumes the warm `design` session** at its pinned `head_sha` with the formatted contradiction table, then a **fresh `check` session** runs over `item_base_sha..head2`. |
| **`verdict == "contradiction"`, identical repeat of an earlier key on this conflict chain** | **Nothing lands**; `routeDesignRepeat` ([design_repeat.go](../../tools/tillr-drain/drain/design_repeat.go)) moves `item.conflict` to `status: "owner"`, `waiting_on: "owner"` (`ReasonConflictOwner`). | Stops with no retry count ([OQ-DR2](./tillr-diff-review.md#decision-ledger), [`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)), showing the owner both `check`'s contradiction proof and `answer`'s response on the Item page. |
| **Malformed `artifacts/verdict` or an unread changed path** | The candidate does **not** land and `answer` is **not** blamed. | `check`'s own session is resumed once naming the schema or unread-path defect; a second malformed output holds on `ReasonJudgeUnreadable` (`D-DR11`). |
| **`check` moves `HEAD` or leaves dirty files** | The read-only guard in [workflow.go](../../tools/tillr-drain/drain/workflow.go) cleans the worktree back to `answer`'s `head_sha`. | One fresh `check` session runs; a repeat holds as a tooling defect (`D-DR11`). |

### 5.2 Warm-session return and deterministic repeat key

Two mechanisms in [design_continuation.go](../../tools/tillr-drain/drain/design_continuation.go) and [design_repeat.go](../../tools/tillr-drain/drain/design_repeat.go) each need one invariant to work under a two-stage workflow:

1. **`answer`'s session and candidate ref survive `check`.** When `answer` commits `head1`, `settleNode` pins `refs/tillr/runs/<answer-run-id>` at `head1` and keeps `answer`'s session id in its run directory. `check` writes its own session id and `artifacts/verdict` under its own run directory. When `check` reports a contradiction, `DesignRunID` is set to the `answer` run, never the `check` run. On the next pass `prepareDesignContinuation` resolves that run, checks out `head1`, sets `KeptItemBaseSHA` so the following `check` still diffs the entire conflict answer (`item_base_sha..head2`), and resumes `answer`'s warm conversation with every `contradictions[]` entry in the turn prompt.
2. **The repeat key does not depend on wording.** Reviewers reword prose between fresh sessions, so hashing `claim` or `actual` would miss an identical repeat (`D-DR10`). For cause `answer-contradiction`, `designEndingKey` in [design_repeat.go](../../tools/tillr-drain/drain/design_repeat.go) canonicalizes the sorted unique tuples `(category, checked, symbol_or_key)` across `contradictions[]`:
   ```text
   answer-contradiction\x00<category>\x00<checked>\x00<symbol_or_key>\n...
   ```
   - If `answer` fixes three of four contradictions, the tuple set shrinks — **not** an identical repeat, so `answer` gets another turn.
   - If `answer` fixes all four and introduces a new error in another symbol, the tuple set changes — **not** an identical repeat.
   - If `answer` leaves the same contradicted claims in place, or commits an unchanged tree insisting `check` is wrong, and the fresh `check` emits the identical tuple set, `designRepeatOf` matches the prior `drain.design-unanswered` event and `routeDesignRepeat` stops for the owner ([OQ-DR2](./tillr-diff-review.md#decision-ledger)).

---

## 6. Worked example

An answer carrying the four contradictions of [Section 1.2](#12-the-kinds-of-claim-that-go-wrong) runs through `conflict-answer@v1` as follows:

1. **The conflict is dispatched.** `plan` stops with `OBSTACLE: definition`. `Plan` keeps the item's `workflow_selections` row and dispatches `conflict-answer@v1`.
2. **`answer` commits `head1`.** `settleNode` confirms a commit exists, the write fence holds, the item's block stands and the fast document checks are green, pins `refs/tillr/runs/<answer-run-id>`, and advances to `check`.
3. **`check` audits `base..head1` against `head1`.** In a fresh read-only session without `answer`'s report, it reads `conflict.json`, the diff, and every Go loader and config file the new specification relies on.
4. **`check` emits `"verdict": "contradiction"` with four entries:** one `wrong-schema-or-decoder` (the stage `kind` and field name), two `wrong-symbol-or-type` (the hook names; the container shape and field names), and one `breaks-tracked-config` (the strict-decoding rule that every tracked policy file fails).
5. **The refusal returns to the warm `answer` session; `main` does not move.** `item.conflict` stays on `design` and no `plan` run starts on the wrong specification. `answer` resumes on `head1` with the four locators, rewrites the contract against the real types, passes a fresh `check`, and lands once.

---

## 7. Alternatives considered

| Alternative | Why rejected |
| :--- | :--- |
| **Pre-write `scout -> answer` workflow without a post-write check** | The designer invents concrete symbol names, hook lists and strict-JSON rules *while drafting* `answer`. Pre-write scouting has no candidate diff to verify against the tree. |
| **Post-write check on the `scout` seat** | Contradicts [OQ-RJ4](./tillr-review-rules.md#decision-ledger), which placed design fact audits on `review`. The cheapest seat misses a type alias that crosses packages and config interactions that span files. |
| **Wiring `check` through the evaluator slot and `judgeCandidate`** | `judgeCandidate` validates the evaluator binding against the item's `workflow_selections` row and calls `item.RecordJudge` to retire the blocked item's criteria; `verifyBuildSession` in [verify_return.go](../../tools/tillr-drain/drain/verify_return.go) inserts a `build`-seat repair stage that cannot edit `docs/design/*.md`. |
| **A loop-only post-gate review that keeps conflicts without a workflow** | Leaves `conflict-design` as the only multi-seat flow that workflow graphs (`node_runs`, `tillr drain workflow check`) cannot see, instead of declaring `answer -> check` in `config/tillr/workflows/` like the other workflows. |
| **Letting `answer` dismiss a `check` contradiction on its own** | Violates [OQ-DR2](./tillr-diff-review.md#decision-ledger): no dismissal mechanism is built, and a repeated disagreement stops for the owner. |

---

## 8. What this does not cover and amendments owed

**Not covered here:**
- Replacing the document-triage dispatch (owned by [tillr-queue-mirrors-designs.md](./tillr-queue-mirrors-designs.md)).
- The lasting rule for a repeated disagreement between reviewer and writer (open for the owner as [OQ-DR3](./tillr-diff-review.md#open-questions)).

**Amendments owed once `D-CA1`–`D-CA6` are reviewed:**

| Document | Amendment owed |
| :--- | :--- |
| [tillr-review-rules.md](./tillr-review-rules.md) | Cross-link `conflict-answer@v1` as the workflow for `conflict-design` answers and note that a passing `check` artifact on `head_sha` satisfies the post-gate fact-and-ledger audit of [OQ-RJ4](./tillr-review-rules.md#decision-ledger) without running `review` twice. |
| [tillr-judge-and-lifecycle.md](./tillr-judge-and-lifecycle.md) ([Section 4.3](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)) | Add `answer-contradiction` to `D-JL9`'s conflict-design continuation causes, routing a first or changed key back to `answer` and an identical repeat to the owner. |
| [tillr-designer-scouts.md](./tillr-designer-scouts.md) | Note that `conflict-design` runs as the `answer` stage of `conflict-answer@v1`, pairing in-session `scout` subagents during drafting with a post-commit `check` on `review`. |
| [tillr-item-graphs.md](../reference/tillr-item-graphs.md) and [tools/tillr-drain/README.md](../../tools/tillr-drain/README.md) | Document `conflict-answer@v1` alongside the shipped workflows once built. |

---

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| **False-positive contradiction on a newly proposed symbol or file.** `check` flags a function, flag or file as missing when the answer is instructing the builder to create it. | [Section 3.3](#33-the-five-closed-check-categories-d-ca3) carves out paths, symbols and schema additions marked as new work; every contradiction quotes `claim` beside `actual`, and if a misread repeats, [OQ-DR2](./tillr-diff-review.md#decision-ledger) shows both sides to the owner instead of looping. |
| **The reviewer misses an indirect Go type alias or embed.** `check` reads one file without following an alias into the package that declares the type. | `check` runs on `review`, not `scout`; `read_paths` must include the inspected source files; and the brief requires resolving every cross-package alias and struct to its declaration before passing. |
| **Added cost and latency on every committing conflict answer.** One `review` run of a minute or two. | Non-committing objections and mechanical document-gate failures skip `check`, and one caught answer saves a `plan` run, a second `conflict-design` run and a second `plan` run. |

---

## Open Questions

None. The policy choices — a narrow `review`-seat fact audit on design changes ([OQ-RJ4](./tillr-review-rules.md#decision-ledger)), the `review` seat's backend ([OQ-DR1](./tillr-diff-review.md#decision-ledger)), uncounted returns to the warm designer session ([`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)), and stopping for the owner on an identical repeat ([OQ-DR2](./tillr-diff-review.md#decision-ledger)) — are the owner's rulings. The architectural choices below await the owner's review.

---

## Decision Ledger

Inherited owner rulings remain owned by their home ledgers and are summarized in [Section 2.2](#22-inherited-owner-rulings-and-principles). Every `D-CA` row below is a designer's decision; none is an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-CA1` | Every committing `conflict-design` answer is checked after its commit and before landing by a fresh read-only `review`-seat run (`check`), not by a pre-write `scout` pass alone and not on the `scout` seat. **Evidence:** the contradictions are introduced while drafting; [OQ-RJ4](./tillr-review-rules.md#decision-ledger) places narrow design fact audits on `review`. **Rejected:** pre-write `scout -> answer` only; a post-write `scout` stage. | 2026-11-22 | [Section 3.1](#31-why-a-post-write-check-on-review-beats-a-pre-write-scout-d-ca1) | — |
| `D-CA2` | A raised `definition` conflict dispatches a tracked two-stage workflow `conflict-answer@v1` (`answer` on `design` with `write`, then `check` on `review` with `read_only` and `outputs: {"verdict": "json"}`), attached to the conflict attempt without touching `workflow_selections(item_id)` and without the evaluator slot or `item.RecordJudge`. **Evidence:** `workflow_selections` is keyed by `item_id` for the blocked item's own workflow, and `item.RecordJudge` retires item criteria. **Rejected:** overwriting `workflow_selections`; wiring `check` through the evaluator slot; leaving `conflict-design` without a workflow. | 2026-11-22 | [Section 4.1](#41-the-conflict-answerv1-workflow-d-ca2-d-ca5), [Section 4.2](#42-keeping-the-blocked-items-own-workflow-intact-d-ca2) | — |
| `D-CA3` | `check` receives `conflict.json`, the blocked item's statement, the cumulative diff `item_base_sha..head_sha`, the repository tree at `head_sha`, and any prior `contradictions[]` on a re-check, with `answer`'s report withheld; it checks five closed categories (`missing-path`, `wrong-symbol-or-type`, `wrong-schema-or-decoder`, `breaks-tracked-config`, `ledger-or-conflict-mismatch`), carving out explicitly marked new symbols and files and forbidding subjective design critique. **Rejected:** open-ended design critique; passing `answer`'s self-report as evidence. | 2026-11-22 | [Section 3.2](#32-what-check-is-given--and-what-is-withheld-d-ca3), [Section 3.3](#33-the-five-closed-check-categories-d-ca3) | — |
| `D-CA4` | `check` emits a strict JSON `artifacts/verdict` file validated in Go before settlement: `verdict` is `pass` or `contradiction`, `read_paths` covers every changed path, and a `contradiction` carries a non-empty `contradictions[]` whose items each state `category`, `path`, `claim`, `checked`, `symbol_or_key` and `actual`. A malformed or unread-path verdict resumes `check` once and then holds on `ReasonJudgeUnreadable`; it neither lands nor blames `answer`. **Rejected:** free-text verdicts; a claim returned without a `checked` locator. | 2026-11-22 | [Section 3.4](#34-verdict-artifact-contract-d-ca4), [Section 5.1](#51-settlement-across-all-check-outcomes-d-ca6) | — |
| `D-CA5` | A non-committing `answer` ending settles immediately after `answer` without running `check`, and the mechanical checks that spend no model run (conflict write fence, item-block guard, fast document checks) run on `answer`'s commit before advancing to `check`. **Rejected:** running `check` on a non-committing objection or on broken Markdown. | 2026-11-22 | [Section 4.1](#41-the-conflict-answerv1-workflow-d-ca2-d-ca5) | — |
| `D-CA6` | On `contradiction`, nothing lands and `item.conflict` stays `raised` on `design` with `DesignRunID` naming the `answer` run; a first or changed contradiction key (the `(category, checked, symbol_or_key)` tuple set) resumes `answer`'s warm session at `head_sha`, and an identical repeat stops for the owner with both sides shown. **Evidence:** [`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9), [`D-JL32`](./tillr-judge-and-lifecycle.md#419-a-worker-returned-by-a-design-answer-starts-from-a-trunk-that-holds-the-answer-d-jl32) and [OQ-DR2](./tillr-diff-review.md#decision-ledger). **Rejected:** landing before `check` passes; numeric retry caps; a cold `answer` session on refusal; hashing reviewer prose as the repeat key. | 2026-11-22 | [Section 5.1](#51-settlement-across-all-check-outcomes-d-ca6), [Section 5.2](#52-warm-session-return-and-deterministic-repeat-key) | — |
