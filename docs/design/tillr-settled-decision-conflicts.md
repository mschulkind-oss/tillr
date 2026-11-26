---
title: "A conflict that cites a settled decision goes to the operator or owner first"
status: in-review
stage: DESIGN
updated: 2026-11-25
tags:
  - tillr
  - drain
  - conflict
  - decisions
  - operator
  - design
summary: "When a worker's definition conflict cites a settled decision that a filed request did not name, or when a non-owner request cites an owner ruling, the design seat has no authority either to drop what was asked or to rewrite the settled decision for a blind filing. Routing it to design spends a design turn and returns the unchanged request to the next worker until the identical-repeat check fires. This design routes such conflicts straight to a person on the first report, resolves cited decision IDs against a closed index of active store decisions and tracked Markdown ledgers, snapshots the filer and broken decisions onto item.conflict, alerts immediately, and shows the request words beside the broken decisions. Nothing is built."
next: "Rule OQ-SDC1 (operator vs owner on a blind non-owner filing) and OQ-SDC2 (who moves an operator hold when no foreground session is attached), and review D-SDC1 through D-SDC7 in the Decision Ledger. Slices 1 and 2 of Section 6 wait on neither question."
depends-on:
  - operator-decisions-inside-tillr.md
  - tillr-request-amendment.md
  - tillr-judge-and-lifecycle.md
  - landing-principles.md
  - ../reference/authority-not-approval.md
vantage:
  status-chip: true
---

# A conflict that cites a settled decision goes to the operator or owner first

**Status:** Nothing is built: `rg -n 'cited_decisions|CitedDecision' tools/tillr-item tools/tillr-drain` returns nothing. Inherited owner rulings (`R-OD1`, `R-RA1`, and the provenance rule of [the provenance review](../tillr/decision-provenance-review.md)) remain owned by their home ledgers ([§2](#2-standing-rulings-and-authority-boundaries)); `D-SDC1` through `D-SDC7` are the designer's decisions (prompted by an operator-filed request) and await the owner's review in the [Decision Ledger](#decision-ledger), and [`OQ-SDC1`](#OQ-SDC1) and [`OQ-SDC2`](#OQ-SDC2) await the owner's ruling.

> **In short.** When a worker stops with a `definition` conflict that cites a settled decision — and either the filed request did not name that decision or a non-owner request collides with an owner ruling — the drain bypasses the `design` seat on the **first** conflict, snapshots the filer and the broken decisions onto `item.conflict`, fires an immediate watch alert, and shows the person both what was asked and which settled decisions it broke.

**Why it matters.** On 2026-11-25 a consumer repository reported that an operator-filed request went against two recorded design decisions. The first worker caught it as a `definition` conflict and named both decisions in its conflict text, yet the item spent 40 minutes in a `design` turn and a second worker run before a repeat watch rule fired ([§1](#1-the-failure-and-why-todays-code-produces-it)). The filing itself was the mistake, and `design` had authority neither to withdraw the request nor to overturn the decisions for it.

**The shape.** Three decisions, matching the three questions of the request:
- **When to bypass `design`** ([§3](#3-when-a-conflict-bypasses-the-design-seat)): bypass `design` on the first conflict whenever a filed request collides blindly with a settled decision (`request_words` does not name the cited ID), whenever a non-owner filing collides with an owner ruling, or whenever a document-backed item collides with an owner ruling (`D-SDC1`, `D-SDC2`). Route to `design` when a request explicitly targets a designer decision (`D-*`) for revision or cites no settled decision, with `requestObstacleDesignInstruction` repaired so it never tells `design` to overwrite settled decisions unconditionally (`D-SDC3`).
- **How the drain tells from recorded data** ([§4](#4-how-the-drain-tells-from-recorded-data)): accept an optional `DECISIONS:` field on `OBSTACLE: definition` and scan both `DECISIONS:` and conflict prose against a closed in-memory index of active store `decisions` rows and settled Markdown ledger / answered-question IDs (`D-SDC4`); record `filed_by` on `request_words` and snapshot `obstacle_kind`, `mover`, `filed_by`, `cited_decision_ids`, and `cited_decisions` onto `item.conflict` (`D-SDC5`), closing the conflict cleanly when `tillr drain retry` releases it (`D-SDC6`).
- **What the operator or owner is shown** ([§5](#5-what-the-operator-or-owner-is-shown)): open an immediate `predicate_match` watch alert on the first person-held settled-decision conflict, and display `filed_by`, `request_words`, each broken decision (`id`, `authority`, `source`, `summary`), the worker's evidence, and the three exit paths (close, retry within the decisions, or supersede) across `tillr drain why`, `tillr view waiting`, `tillr item show`, and Observatory (`D-SDC7`).

**Cost.** Zero extra model runs: the settled-decision index reads SQLite and tracked Markdown under `docs/` in Go during conflict settlement, saving one `design` run and one repeat worker run per mistaken filing.

**Start at** [§3](#3-when-a-conflict-bypasses-the-design-seat), then [§4](#4-how-the-drain-tells-from-recorded-data) and [§5](#5-what-the-operator-or-owner-is-shown).

**Needs your ruling:** [`OQ-SDC1`](#OQ-SDC1) (whether a blind non-owner filing holds on `operator` first or goes straight to `owner`) and [`OQ-SDC2`](#OQ-SDC2) (who moves an `operator` hold when no foreground session is attached).

**Awaits your review:** the designer's decisions `D-SDC1` through `D-SDC7` in the [Decision Ledger](#decision-ledger). You may overturn any of them.

**Reads with:** [operator decisions inside tillr](./operator-decisions-inside-tillr.md) (whose `D-OD3` and `D-OD11` this design narrows for blind filing collisions), [request amendment](./tillr-request-amendment.md) (whose flaw `F7` covers requests against recorded rulings), [the escalation ladder](./escalation-ladder.md#11-the-motivating-case) (why a lower rung never rewrites settled policy to match a mistaken brief), [the judge and lifecycle design](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11) (worker obstacle routing), and [authority, not approval](../reference/authority-not-approval.md) (what is reserved to the owner).

**Words.** Item, lane, seat, candidate and verdict keep their meanings in [the vocabulary](./tillr-vocabulary.md#5-the-words); filed request keeps its meaning in [request amendment](./tillr-request-amendment.md); owner and operator keep their meanings in [authority, not approval](../reference/authority-not-approval.md#terms). Three terms are used with exact meanings here:

- **Settled decision** *(coined here)*: any of three recorded entries — an active row in the store's `decisions` table ([`decision.go`](../../tools/tillr-item/item/decision.go)) whose `status` is `active`, an unsuperseded row in an `ID`-first `Decision Ledger` table in tracked Markdown under `docs/`, or an in-place `OQ-*` question in tracked Markdown under `docs/` carrying a non-placeholder owner answer. An open `💬 OQ-*` question, a `superseded` or `revoked` store decision, or prose with no ID is not a settled decision.
- **Decision authority** *(coined here)*: `"owner"` when the settled decision was made by the owner (`decided_by == "owner"`, an `R-*` ledger row, a ledger row marked `, owner` / `**Owner ruling.**` / `**Owner direction.**`, or an answered `OQ-*` question), and `"designer"` otherwise (`decided_by == "whale"` or a `D-*` / unattributed ledger row).
- **Blind collision** *(coined here)*: a conflict on a filed request that cites at least one settled decision whose ID does not appear in the item's saved `request_words`.

---

## 1. The failure, and why today's code produces it

On 2026-11-25 a consumer repository reported:

> a request the operator filed went against two recorded design decisions. The worker caught it correctly as a definition conflict, twice, and the first conflict text already named the decisions it broke. The item then spent 40 minutes at the design seat before a watch rule fired on the repeat. The filing was the mistake, and only the operator or owner could correct it.

Four behaviors in the current codebase combine to produce that path on every blind request-vs-decision collision:

1. **Unconditional `design` routing for filed-request `definition` obstacles.** When a worker on a filed request (`isFiledRequest` in [`retry_brief.go`](../../tools/tillr-drain/drain/retry_brief.go)) ends `RESULT: blocked` with `OBSTACLE: definition` and the loop policy maps a `design` seat, `reportedBlock` in [`blocked.go`](../../tools/tillr-drain/drain/blocked.go) writes `item.Conflict{Status: "raised", WaitingOn: "design"}` regardless of what the conflict text cites.
2. **A `design` prompt that tells the designer to overwrite recorded decisions while forbidding provenance corruption.** Both `requestObstacleDesignInstruction` in [`obstacle.go`](../../tools/tillr-drain/drain/obstacle.go) and `briefValuesFor` in [`stage.go`](../../tools/tillr-drain/drain/stage.go) instruct the `design` seat: *"Update the design document's decision ledger under docs/ so the recorded decision matches the request"*, while injecting `DesignProvenanceRule` (`stage.go`), which forbids presenting a filed request or designer decision as an owner ruling. At the same time, `FileRequest` in [`request.go`](../../tools/tillr-item/item/request.go) inserts `request_words` once with no update ([request amendment](./tillr-request-amendment.md) is not built). When `design` refuses to corrupt settled decisions for a mistaken request and commits a note in `docs/` (or returns `NEXT-ACTOR: build`), `recordBlockAnswer` (`blocked.go`) sets `Status: "answered", WaitingOn: "build"` and dispatches the next worker against the identical `request_words`.
3. **Escalation to a person waits for an identical repeat.** Only after the second worker raises `OBSTACLE: definition` with matching `CRITERIA` and `EVIDENCE` does `repeatedAnsweredObstacle` in [`answered_obstacle.go`](../../tools/tillr-drain/drain/answered_obstacle.go) set `Status: "owner", WaitingOn: "owner"` with `repeatedObstacleMarker` (`repeatedObstacleActor` returning `"operator"`).
4. **Watch rules ignore a first `item.conflict`.** Under [`config/tillr/watch/`](../../tools/tillr-watch/rules/), [`drain-obstacle-hold.toml`](../../tools/tillr-watch/rules/drain-obstacle-hold.toml) reads `drain.obstacle-hold` (which `reportedBlock` skips whenever a filed request routes to `design`), [`drain-repeated-item-conflicts.toml`](../../tools/tillr-watch/rules/drain-repeated-item-conflicts.toml) requires `threshold = 2` on `event.status == "raised"`, and `rg 'item\.conflict' config/tillr/watch` finds no rule matching `event.status == "owner"`.

---

## 2. Standing rulings and authority boundaries

Four standing rulings bound this design. None is redefined here:

- **Escalate inside tillr when a problem needs neither the owner nor the filer's intent (`R-OD1`, 2026-11-21).** Quoted verbatim in [operator decisions inside tillr §1](./operator-decisions-inside-tillr.md#1-the-ruling) and [landing principles §16](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr): a problem the operating agent could solve without escalating to the owner escalates to a responsible agent, process or ticket inside tillr rather than popping up to the operator. In that document's ledger, `D-OD3` (routing every `definition` obstacle on a filed request to `design`) and `D-OD11` (eliminating `operator` holds) are **designer** decisions, not owner rulings.
- **Refine wording within the spirit of the request without dropping what was asked (`R-RA1`, 2026-11-21).** Recorded in [request amendment §1](./tillr-request-amendment.md#1-the-ruling): how a request is worded should not decide how hard the work is, while the split of responsibilities between seats stays enforced. Under its rules `P3` and `P4` ([request amendment §4](./tillr-request-amendment.md#4-the-rules-an-amendment-keeps), designer decisions), only the owner releases a requested case outright, and no amendment may contradict a recorded owner ruling.
- **Provenance and owner-only policy ([authority, not approval](../reference/authority-not-approval.md#what-owner-only-is-for), [the provenance review](../tillr/decision-provenance-review.md#decision-ledger), and `DesignProvenanceRule` in [`stage.go`](../../tools/tillr-drain/drain/stage.go)).** Only the owner may overturn an owner ruling or choose between two conflicting statements of his own; a filed request is not an owner ruling unless it quotes dated owner words verbatim.
- **A held state names a mover that can still act ([landing principles §13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) and [unattended drain §6.7](./unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves)).** An item held on an absent session with nobody alerted violates Principle 13.

---

## 3. When a conflict bypasses the design seat

### 3.1 Why `design` has no move on a blind request-vs-decision collision (`D-SDC1`)

When a filed request (`request_words` present) hits a `definition` conflict citing a settled decision that `request_words` did **not** name, only two resolutions exist:

1. **Withdraw or rewrite the request** so it stays within the settled decision, because the filer missed the standing rule ("the filing was the mistake"). `design` has no authority to close an item or drop what the request asked for (`T5` in [operator decisions inside tillr §4](./operator-decisions-inside-tillr.md#4-the-rules); `P3` in [request amendment §4](./tillr-request-amendment.md#4-the-rules-an-amendment-keeps)).
2. **Supersede the settled decision** so the request proceeds as written. When the decision has `authority == "owner"`, `design` is forbidden to overturn it (`DesignProvenanceRule`). Even when the decision has `authority == "designer"` (`D-*`), rewriting settled architecture to accommodate a one-off request that never named it is the exact failure described in [the escalation ladder §1.1](./escalation-ladder.md#11-the-motivating-case): changing policy to match a mistaken brief.

Because neither valid resolution is within `design`'s authority on a blind collision, `R-OD1`'s precondition ("a similarly responsible agent... within Tiller" can settle it) fails. Dispatching `design` spends a Tier-3 run only to object or bounce back to the worker.

By contrast, `design` **does** have a valid move in three situations (`D-SDC1`):
- **Zero settled decisions cited** (a tree-fact error, a missing symbol, or an open `OQ-*` question): `design` answers from the tree or amends.
- **A filed request that explicitly names every cited `D-*` decision** (and cites no `owner` decision from a non-owner filer): revising that named designer decision was the point of the filing, and `design` holds authority over `D-*` rows.
- **A document-backed item (`request_words` absent) citing only `designer` decisions:** no ad-hoc filing mistake occurred; two tracked documents disagree within owner policy, and `design` reconciles them ([`D-OD3`](./operator-decisions-inside-tillr.md#decision-ledger)).

### 3.2 First-conflict routing matrix (`D-SDC1`, `D-SDC2`)

The designer's decision `D-SDC1` settles **when** a conflict bypasses `design` on its first report (`Status: "owner"`, `WaitingOn: "owner"`, `DesignRunID: ""`). Who receives a non-owner filing's hold (`operator` vs `owner`, and unattended behavior) is left to the owner in [`OQ-SDC1`](#OQ-SDC1) and [`OQ-SDC2`](#OQ-SDC2), with `D-SDC2` recording the designer's leaning:

| Item shape | Cited settled decisions | Every cited ID named in `request_words`? | `filed_by` | First-conflict route | Why |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Filed request** | ≥ 1 (`owner` or `designer`) | **No** (blind collision) | `owner` | **Bypass `design` → `owner`** (`mover: "owner"`) | Two statements of the owner disagree, or an owner request hit a settled decision it did not name; only the owner chooses whether to drop the request or supersede the decision |
| **Filed request** | ≥ 1 (`owner` or `designer`) | **No** (blind collision) | Non-owner (`operator`, unknown) | **Bypass `design` → person** (`mover` per [`OQ-SDC1`](#OQ-SDC1) / [`OQ-SDC2`](#OQ-SDC2)) | The filing overlooked a settled decision; the filer can withdraw or refile a mistake without paging the owner (under [`OQ-SDC1`](#OQ-SDC1) Option A), while overturning an owner ruling still requires the owner |
| **Filed request** | Includes ≥ 1 `authority == "owner"` | **Yes** | Non-owner (`operator`, unknown) | **Bypass `design` → person** (`mover` per [`OQ-SDC1`](#OQ-SDC1) / [`OQ-SDC2`](#OQ-SDC2)) | Naming an `R-*` or `decided_by: "owner"` ID in an operator request does not confer authority to overturn an owner ruling (`DesignProvenanceRule`) |
| **Filed request** | Includes ≥ 1 `authority == "owner"` | **Yes** | `owner` | **Route to `design`** (`status: "raised"`, `waiting_on: "design"`) | When an owner-filed request explicitly names an earlier ruling, `design` updates the ledger quoting the dated owner words, or ends `NEXT-ACTOR: owner` if the words do not settle the choice |
| **Filed request** | Only `authority == "designer"` | **Yes** (targeted revision) | Any | **Route to `design`** (`status: "raised"`, `waiting_on: "design"`) | Revising the named `D-*` decision was explicitly asked for, within `design`'s authority |
| **Document-backed item** (`request_words` absent) | Includes ≥ 1 `authority == "owner"` | N/A | N/A | **Bypass `design` → `owner`** (`mover: "owner"`) | A tracked block or plan contradicts an owner ruling; `design` cannot overturn the ruling |
| **Document-backed item** (`request_words` absent) | Only `authority == "designer"` | N/A | N/A | **Route to `design`** (`status: "raised"`, `waiting_on: "design"`) | Two tracked design artifacts disagree within owner rulings; `design` reconciles them |
| **Generated repair item** | Any | N/A | N/A | **Existing `generatedQuestion` route → `owner`** | Unchanged (`reportedBlock` in [`blocked.go`](../../tools/tillr-drain/drain/blocked.go)) |
| **Any non-generated item** | Zero verified settled decisions | N/A | N/A | **Existing `design` route** | Unchanged: tree-fact mismatch or open question |

### 3.3 Repairing the `design` brief instruction (`D-SDC3`)

For `definition` conflicts on filed requests that *do* route to `design` (targeted `D-*` revisions and tree-fact conflicts), `requestObstacleDesignInstruction` in [`obstacle.go`](../../tools/tillr-drain/drain/obstacle.go) and `briefValuesFor` in [`stage.go`](../../tools/tillr-drain/drain/stage.go) stop telling `design` to *"Update the design document's decision ledger under docs/ so the recorded decision matches the request."* In its place (`D-SDC3`), both prompts state:

- When the obstacle is a tree-fact mismatch or missing detail, answer it in `docs/` within settled decisions ([`R-RA1`](./tillr-request-amendment.md#1-the-ruling)).
- Revise a `D-*` designer decision in `docs/` only when the request explicitly named that `D-*` ID for revision or when repository evidence shows the `D-*` row is wrong, marking the row as the designer's (`DesignProvenanceRule`).
- Never alter or supersede an owner ruling (`R-*`, answered `OQ-*`, or active `decided_by: "owner"` decision) unless the request carries dated verbatim owner words superseding it.
- Otherwise commit nothing and end with `NEXT-ACTOR: build` (when the request can be built within the existing decision) or `NEXT-ACTOR: owner` (when only the owner can settle the collision).

---

## 4. How the drain tells from recorded data

### 4.1 Optional `DECISIONS:` field plus closed-index verification (`D-SDC4`)

Relying on structured worker declarations alone misses workers that name broken decisions only in `EVIDENCE` (as in the 2026-11-25 incident); relying on an unverified regular expression over `EVIDENCE` either false-matches ordinary hyphenated tokens (`utf-8`, `run-pi`, `non-zero`) or hardcodes `R-*`/`D-*` prefixes that miss a consumer repository's `ADR-*` or `DEC-*` IDs. `D-SDC4` combines both inputs and verifies every candidate token against a closed index of actually settled decisions:

1. **Optional `DECISIONS:` field on `OBSTACLE: definition`.** Register `"DECISIONS"` in `obstacleFields` ([`obstacle.go`](../../tools/tillr-drain/drain/obstacle.go)) while keeping `obstacleRequired[ObstacleDefinition]` at `{"CRITERIA", "EVIDENCE"}` so omitting `DECISIONS:` never turns a valid obstacle into a missing output. Update [`brief-template.md`](../../tools/tillr-run/backends/brief-template.md) and `ObstacleContract` (`obstacle.go`) so the `definition` row instructs workers to list broken decision IDs on `DECISIONS: <id>, ...` (or `<path>#<id>`) whenever a conflict cites a recorded decision.
2. **Closed settled-decision index, built in Go at `reportedBlock`.** When settling an `OBSTACLE: definition` report, the drain builds an in-memory map `id -> CitedDecision` from two authoritative sources:
   - **Active store decisions (`decisions` table in [`decision.go`](../../tools/tillr-item/item/decision.go)):** `SELECT decision_id, text, decided_by FROM decisions WHERE status = 'active'`. Rows with `superseded` or `revoked` are excluded. Each row maps `id = decision_id`, `source = "store:decisions"`, `summary = collapseSingleLine(text)`, and `authority = "owner"` when `decided_by == "owner"` else `"designer"`.
   - **Tracked Markdown under `docs/` at `TrunkRef`:** walking tracked `docs/**/*.md` blobs at `TrunkRef` (excluding delivered snapshots under `yolo-packs/`) with the pure-Go Markdown readers in [`retry_brief.go`](../../tools/tillr-drain/drain/retry_brief.go):
     - *Decision Ledger rows:* every row in an `ID`-first table under a `Decision Ledger` heading whose cells do not mark it superseded or withdrawn. `id` is column 1 stripped of backticks and emphasis; `summary` is column 2 collapsed to one line (truncated to 240 runes); `source` is `<path>#decision-ledger`; `authority` is `"owner"` when `id` starts with `R-` or the row's `Date` or `Decision` cell contains `, owner`, `**Owner ruling.**`, or `**Owner direction.**`, and `"designer"` otherwise.
     - *Answered `OQ-*` questions:* every question directive `<!-- vantage: question id=<ID> ... -->` for which `extractVantageAnswer` (`retry_brief.go`) returns a non-empty owner answer (`✅` or filled `**Answer:**`). `id` is the question ID, `authority` is `"owner"`, `source` is `<path>#<id>`, and `summary` is the collapsed answer text. Open `💬` questions with empty answers are excluded.
     - When the same `id` exists in both `store:decisions` and `docs/`, `authority = "owner"` wins if either source marks it `"owner"`, and the `docs/` path is kept in `source` so UI links open the document.
3. **Candidate token extraction and exact membership check.** Extract candidate IDs from:
   - `obstacle.List("DECISIONS")` (stripping any `<path>#` prefix before `#`), and
   - identifier tokens (`[A-Za-z][A-Za-z0-9_.-]*[0-9A-Za-z]`) in `obstacle.Field("EVIDENCE")` (and, for legacy reports, `c.Text`).
   Keep **only** tokens that match an exact key in the settled-decision index, deduplicated and sorted by `id`.
   - For each matched `id` on a filed request, set `named_in_request = true` iff `request_words` contains `id` bounded by non-identifier runes.
   - If `obstacle.List("DECISIONS")` explicitly listed a token absent from the settled-decision index (for instance an open `OQ-*` question, a `revoked` store decision, or a typo), record that token in `unverified_decisions` (`[]string, omitempty`) on `item.Conflict` rather than treating it as settled. When `len(cited_decisions) == 0`, normal `design` routing applies.

### 4.2 Recording `filed_by` and snapshotting conflict fields (`D-SDC5`)

1. **`filed_by` on `request_words`.** `RequestFiling` in [`request.go`](../../tools/tillr-item/item/request.go) already receives `By` and writes `placed_by` on the `item.update` placement event (`Place`), while `request_words` stores `(item_id, words_hash, words, input_hash)`. Register an additive column via `store.RegisterColumn("request-words", "request_words", "filed_by", "TEXT NOT NULL DEFAULT ''")` populated from `strings.TrimSpace(f.By)` in `insertRequest`. For rows written before the column existed (where `filed_by == ""`), a helper `RequestFiler(r, itemID)` falls back to the earliest non-empty `placed_by` on `item.update` for `itemID`, returning `""` when unrecorded. Routing treats `""` as non-owner (`DesignProvenanceRule`: never assume unrecorded provenance is the owner's).
2. **Additive fields on `item.Conflict` ([`conflict.go`](../../tools/tillr-item/item/conflict.go)).** `item.Conflict` is serialized as JSON in `events` (`kind = 'item.conflict'`), with no separate `conflicts` table (`ReadConflicts` unmarshals event payloads directly). Add optional fields so every consumer (`conflictDecision`, `why.go`, `tillr-view`, and `tillr-watch`) reads one self-contained event without re-opening Markdown files:

```go
type CitedDecision struct {
	ID             string `json:"id"`
	Authority      string `json:"authority"` // "owner" | "designer"
	Source         string `json:"source"`    // "store:decisions" | "docs/...#decision-ledger" | "docs/...#OQ-..."
	Summary        string `json:"summary"`
	NamedInRequest bool   `json:"named_in_request,omitempty"`
}
```

Added to `item.Conflict` (and mirrored on `waitingConflict` in [`waiting_conflict.go`](../../tools/tillr-view/waiting_conflict.go)):
- `ObstacleKind string` (`json:"obstacle_kind,omitempty"`): `"definition"`, `"owner"`, `"seat"`, or `"tooling"`.
- `Mover string` (`json:"mover,omitempty"`): `"operator"`, `"owner"`, or `"design"`.
- `FiledBy string` (`json:"filed_by,omitempty"`): the request filer when the item has `request_words`.
- `CitedDecisionIDs []string` (`json:"cited_decision_ids,omitempty"`): flat ID list (`["D-OD3", "R-OD1"]`) so `tillr-watch` template interpolation (`{{event.cited_decision_ids}}` in [`bundle.go`](../../tools/tillr/watch/bundle.go), which formats scalar slices and renders object slices as `unknown`) prints the exact IDs.
- `CitedDecisions []CitedDecision` (`json:"cited_decisions,omitempty"`): full snapshot of each broken settled decision at conflict time.
- `UnverifiedDecisions []string` (`json:"unverified_decisions,omitempty"`): tokens from `DECISIONS:` that were open, superseded, revoked, or absent.

**`items.waiting_on` compatibility.** `checkWaitingOn` in [`waiting.go`](../../tools/tillr-item/item/waiting.go) restricts `WaitingOn.Waiter` on `items` to `owner | design | plan | item:<id>`. A conflict that bypasses `design` keeps `Status: "owner"` and `WaitingOn: "owner"` on `item.Conflict` while setting `Mover: "operator"` or `"owner"`. `conflictDecision` in [`blocked.go`](../../tools/tillr-drain/drain/blocked.go) sets `d.Reason = ReasonConflictOwner` and `d.Seat = c.Mover` whenever `c.Mover != ""` (falling back to the existing `repeatedObstacleActor` / `why.go` rules on historical events where `c.Mover == ""`).

### 4.3 Closing a bypassed conflict on `tillr drain retry` (`D-SDC6`)

When a conflict bypasses `design`, `c.Status` is `"owner"` and `c.DesignRunID` is `""`. Today in `AuthorizeRetry` ([`retries.go`](../../tools/tillr-drain/drain/retries.go)), superseding `c.RunID` on such an item sets `releaseBlocked = true` and calls `item.SetState(..., Ready, at)` **without** appending a closing `item.Conflict` transition. That unblocks `plan.go`, but leaves `c.Status == "owner"` as the latest `item.conflict` event in `ReadConflicts`, `waitingConflicts` (`waiting_conflict.go`), and `tillr-watch`.

Under `D-SDC6`, when `AuthorizeRetry` releases a blocked item whose latest conflict has `c.Status == "owner"` and `c.DesignRunID == ""`, it writes `c.Status = "closed"`, `c.WaitingOn = "worker"`, `c.Mover = ""` via `item.RecordConflict(tx, c, at)` in the same transaction (which moves `items.state` to `in-queue` atomically and emits the closing `item.conflict` event that clears the watch alert).

---

## 5. What the operator or owner is shown

Today `rg 'RequestWords|request_words' tools/tillr-view tools/tillr-item/item/show.go tools/tillr-drain/drain/why.go` returns nothing: none of the status commands or Observatory pages displays a filed request's words beside a conflict, and `why.go` prints only `firstParagraph(c.Text)` with a generic retry/close hint. `D-SDC7` updates four surfaces together:

### 5.1 Immediate `tillr-watch` alert (`config/tillr/watch/drain-settled-decision-conflict.toml`)

Add a standing core rule that fires on the **first** settled-decision conflict reaching `status == "owner"` (`pattern = "predicate_match"`, no repeat threshold) and clears when a closing or re-queued `item.conflict` transition is appended or the item leaves `blocked`:

```toml
[[rule]]
id = "drain-settled-decision-conflict"
scope = "core"
severity = "warning"

[rule.reads]
events = ["item.conflict"]
projections = ["items", "charter"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'event.status == "owner" and not empty(event.cited_decisions) and item.state != charter.closed and item.state not in charter.finished'
clear_when = 'event.status != "owner"'
window = "24h"

[rule.guidance]
summary = "Item {{subject}} (filed by {{event.filed_by}}) conflicts with settled decision(s) {{event.cited_decision_ids}}; waiting on {{event.mover}}."
check = [
  "Run tillr drain why {{subject}} --store <path> to read the filed request words beside {{event.cited_decision_ids}} and the worker's evidence."
]
do = [
  "If the filing was a mistake, close it with tillr item close --item {{subject}} --by <who> --reason <text> (and refile within {{event.cited_decision_ids}} if still wanted); if it can be delivered within {{event.cited_decision_ids}}, run tillr drain retry --item {{subject}} --run {{event.run_id}} --by <who> --why <revised instruction>; overturning an owner ruling requires the owner."
]

[rule.delivery]
receiver = "foreground"
```

In keeping with [tillr-watch's standing ruling](./tillr-watch.md#32-standing-ruling-an-alert-never-fails-benches-or-caps-work-d-tw2), the rule only notifies and applies no automatic remedy.

### 5.2 `tillr drain why` (`tools/tillr-drain/drain/why.go`)

In `why.go`'s `ReasonConflictOwner` branch, when `len(c.CitedDecisions) > 0`:
- **Mover attribution:** read `actor` directly from `c.Mover` (`"operator"` or `"owner"`), updating `Summary.OperatorNext` when `actor == "operator"` and `Summary.OwnerWaiting` when `actor == "owner"`.
- **`Why` detail:** format four structured blocks instead of `firstParagraph(c.Text)`:
  1. **Request (`filed_by`):** who filed the request (`filed_by`, or `"unrecorded"`) and the saved `request_words` (or `body_ref` on a document-backed item).
  2. **Settled decisions broken:** one line per `CitedDecision`: `<id> [<authority>, <source>]: <summary>` (marking `(named in request)` when `NamedInRequest` is true).
  3. **Worker conflict (`c.RunID`):** `obstacle.Field("EVIDENCE")`.
  4. **`Mover` actions:** three explicit paths:
     - *Withdraw mistaken filing:* `tillr item close --item <id> --by <actor> --reason "..."` (omitted if `hasLanded`).
     - *Keep `<ids>` and narrow/clarify instruction:* `acceptedConflictRetryCommand(r, it.ItemID, c)` (`tillr drain retry --item <id> --run <c.RunID> --by <actor> --why "<instruction keeping <ids>>"`).
     - *Supersede `<ids>`:* when any entry has `authority == "owner"`, state that only the owner may supersede it (by recording dated owner words in `<source>` or `tillr item decision supersede`); when all entries have `authority == "designer"`, state that revising `<ids>` requires refiling or dispatching a design revision that explicitly names `<ids>`.

### 5.3 `tillr view waiting` (`tools/tillr-view/waiting.go`, `waiting_conflict.go`)

- Live mode (`askPlannerAt` in [`waiting.go`](../../tools/tillr-view/waiting.go)) inherits `Why` and `Mover` from `tillr-drain why`.
- History mode (`readWaitingAt` in `waiting.go` and `conflictChange` in [`waiting_conflict.go`](../../tools/tillr-view/waiting_conflict.go)) uses `c.Mover` when non-empty instead of defaulting every `conflict-owner` hold to `"the owner"`, and appends `cited decisions: <ids>` to the conflict transition summary.

### 5.4 `tillr item show` and Observatory (`show.go`, `item_history.go`, `Home.tsx`, `ItemDetailPane.tsx`)

- **`tillr item show` ([`show.go`](../../tools/tillr-item/item/show.go)) and `/api/v1/items/<id>` ([`item_history.go`](../../tools/tillr-view/item_history.go)):** include `request_words` (`string, omitempty`), `filed_by` (`string, omitempty`), and the active conflict's `cited_decisions` and `mover`.
- **Observatory cards ([`Home.tsx`](../../tools/tillr-view/web/src/components/Home.tsx) and [`ItemDetailPane.tsx`](../../tools/tillr-view/web/src/components/ItemDetailPane.tsx)):** when a held item has `cited_decisions.length > 0`, render a two-part comparison card above the raw run report:
  1. **What was asked:** `filed_by` badge (`operator` / `owner`) and the verbatim `request_words` block.
  2. **Settled decisions broken:** each `CitedDecision` with its `id`, an `owner ruling` vs `designer decision` badge (`EpistemicBadge`), its one-line `summary`, and a link to `source` (opening Markdown sources in `DocumentPane.tsx`).
  3. **Worker evidence and exit commands:** the worker's `EVIDENCE` text and copyable `tillr item close` / `tillr drain retry` commands.

---

## 6. The build, in slices that each land alone

Each slice lands as its own commit and leaves the trunk working if later slices wait (`D-SDC7`). Slices 1 and 2 wait on neither open question; Slice 3 waits on [`OQ-SDC1`](#OQ-SDC1) and [`OQ-SDC2`](#OQ-SDC2).

| Slice | What lands | Why it is useful alone | Follows |
| ---: | :--- | :--- | :--- |
| 1 | `request_words` records `filed_by` (`store.RegisterColumn`) with `placed_by` fallback for older rows; `tillr item show` and Observatory's item payload expose `request_words` and `filed_by` | Any reader of a request item sees who filed it and its exact words without opening a run brief (delivers Slice 1 of [request amendment §11](./tillr-request-amendment.md#11-the-build-in-slices-that-each-land-alone) with filer provenance) | — |
| 2 | Optional `DECISIONS:` field in `obstacle.go` and worker brief template; closed settled-decision index over active `decisions` rows and tracked `docs/` ledgers + answered `OQ-*` questions; `obstacle_kind`, `filed_by`, `cited_decision_ids`, `cited_decisions`, and `unverified_decisions` snapshotted onto `item.Conflict`; `requestObstacleDesignInstruction` and `stage.go` repaired so `design` is never told to overwrite settled decisions blindly | Conflict events carry structured decision citations, and `design` stops rewriting settled ledgers to match blind requests | 1 |
| 3 | First-conflict bypass in `reportedBlock` (`blocked.go`) and `Mover` propagation in `conflictDecision` per [`OQ-SDC1`](#OQ-SDC1) and [`OQ-SDC2`](#OQ-SDC2); `AuthorizeRetry` (`retries.go`) closes `item.Conflict` (`Status: "closed"`, `WaitingOn: "worker"`) when releasing a bypassed conflict (`DesignRunID == ""`) | Settled-decision conflicts skip the wasted `design` turn and second worker run, and `tillr drain retry` cleanly closes the conflict event | 2, [`OQ-SDC1`](#OQ-SDC1), [`OQ-SDC2`](#OQ-SDC2) |
| 4 | `config/tillr/watch/drain-settled-decision-conflict.toml`; structured `request_words` + `cited_decisions` + exit-path rendering in `why.go`, `waiting.go` / `waiting_conflict.go`, `Home.tsx`, and `ItemDetailPane.tsx` | Immediate alert on the first conflict and side-by-side display of the request and the broken decisions | 2, 3 |

---

## 7. Edge cases, alternatives considered, and amendments owed

### 7.1 Failure and edge cases

| Case | What happens |
| :--- | :--- |
| Worker omits `DECISIONS:` and names `R-OD1` and `D-OD3` only in `EVIDENCE` prose (the 2026-11-25 case) | Exact token lookup against the closed settled-decision index resolves both IDs, populates `cited_decisions`, and bypasses `design` on the first conflict |
| Conflict prose contains ordinary hyphenated words (`utf-8`, `run-pi`, `non-zero`, `item-request-abc`) | None exists in the settled-decision index; zero false positives |
| Consumer repository uses `ADR-04` or `DEC-12` in its `Decision Ledger` table or `tillr item decision record` | Indexed from column 1 of `Decision Ledger` and `decisions.decision_id`; works with no `R-*`/`D-*` prefix hardcoding |
| Worker writes `DECISIONS:` naming [`OQ-RA1`](./tillr-request-amendment.md#OQ-RA1) while [`OQ-RA1`](./tillr-request-amendment.md#OQ-RA1) is still open (`💬`), or cites a `superseded` / `revoked` store decision | Excluded from `cited_decisions` and noted in `unverified_decisions`; if no verified settled decision remains, routes to `design` as normal unsettled work |
| Operator files a request that explicitly names `D-OD3` to revise it | `named_in_request == true` and `authority == "designer"`; routes to `design` (`status: "raised"`, `waiting_on: "design"`) |
| Operator files a request that explicitly names `R-OD1` (`authority == "owner"`, `filed_by != "owner"`) | Still bypasses `design` to a person: naming an owner ruling in an operator request does not authorize overturning it |
| Person answers with `tillr drain retry` keeping the decisions, and the next worker hits the same settled decision again | `AuthorizeRetry` closed the first conflict (`D-SDC6`); the next worker's report opens a fresh conflict with `cited_decisions`, bypassing `design` and re-opening the watch alert immediately |
| Pre-existing `item.conflict` events in the store with `mover == ""` and `cited_decisions == nil` | Read with existing fallback logic in `conflictDecision`, `why.go`, and `waiting_conflict.go`; no store migration of historical events |

### 7.2 Alternatives considered

| Alternative | Why rejected |
| :--- | :--- |
| **Send every settled-decision conflict to `design` first, relying on `design` to emit `NEXT-ACTOR: owner`** | Still pays for a full Tier-3 `design` turn on every mistaken filing, cannot route to `operator` (`designNextActor` in [`blocked.go`](../../tools/tillr-drain/drain/blocked.go) accepts only `owner` or `build`), and failed in the 2026-11-25 incident because `requestObstacleDesignInstruction` told `design` to edit `docs/` to match the request |
| **Detect decision citations with a standalone regex (`\b[RD]-[A-Z]+-\d+\b`) without reading the store or `docs/`** | Misses consumer repositories whose ledger IDs use other prefixes (`ADR-*`, `DEC-*`, `P1`, or arbitrary `decision_id` strings in `decisions`), cannot tell `"owner"` from `"designer"` authority, and treats open `OQ-*` or `revoked` IDs as settled |
| **Require `DECISIONS:` and ignore IDs written only in `EVIDENCE`** | Fails whenever a worker writes a clear `EVIDENCE` explanation naming the broken decisions without adding the optional field — the exact 2026-11-25 report shape |
| **Add `"operator"` to `items.waiting_on.waiter` (`checkWaitingOn` in `waiting.go`)** | Breaks existing `checkWaitingOn` invariants and external readers of `items.waiting_on`; keeping `WaitingOn: "owner"` with `Conflict.Mover: "operator" \| "owner"` preserves store invariants while eliminating text-reparsing heuristics |
| **Have `tillr-watch` close or reroute the item when it sees a cited decision** | Violates [tillr-watch's standing ruling](./tillr-watch.md#32-standing-ruling-an-alert-never-fails-benches-or-caps-work-d-tw2): watch rules only alert; routing belongs in `tillr-drain` |

### 7.3 What this does not cover, and amendments owed once ruled

**Not covered here:**
- Pre-worker reading of newly filed requests before any worker runs (owned by [`OQ-RA1`](./tillr-request-amendment.md#OQ-RA1) in [request amendment](./tillr-request-amendment.md)). If [`OQ-RA1`](./tillr-request-amendment.md#OQ-RA1) is ruled Option A, that filing reader can reuse the settled-decision index of [§4.1](#41-optional-decisions-field-plus-closed-index-verification-d-sdc4) to flag flaw `F7` before dispatch; this design still governs every conflict a worker raises.
- In-store request amendments (`D-RA2` in [request amendment](./tillr-request-amendment.md)), which remain unbuilt.

**Amendments owed once [`OQ-SDC1`](#OQ-SDC1), [`OQ-SDC2`](#OQ-SDC2), and `D-SDC1`–`D-SDC7` are reviewed:**

| Document | Amendment owed |
| :--- | :--- |
| [operator-decisions-inside-tillr.md](./operator-decisions-inside-tillr.md) ([§5.1](./operator-decisions-inside-tillr.md#51-a-hold-answered-with-a-technical-decision), `D-OD3`, `D-OD11`) | Carve out blind settled-decision collisions and non-owner collisions with owner rulings from `D-OD3`'s unconditional `design` route, and cross-link [`OQ-SDC1`](#OQ-SDC1)'s ruling alongside `D-OD11` |
| [tillr-request-amendment.md](./tillr-request-amendment.md) ([§5](./tillr-request-amendment.md#5-the-flaws) `F7`, [§6.3](./tillr-request-amendment.md#63-what-an-amendment-may-not-do)) | Cross-link this design for worker `definition` obstacles citing settled decisions (`F7`) when the request did not name the broken decision |
| [tillr-judge-and-lifecycle.md](./tillr-judge-and-lifecycle.md) ([§4.5](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)) | Record optional `DECISIONS:` on `OBSTACLE: definition` and the first-conflict person hold when settled decisions are cited |
| [`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md) and [`tools/tillr-view/README.md`](../../tools/tillr-view/README.md) | Document settled-decision conflict routing and the updated `why` / `waiting` output when Slices 3 and 4 land |

---

## Open Questions

1. 💬 **OQ-SDC1: When a non-owner request conflicts with a settled decision it did not name, who gets the hold first?**

   In the 2026-11-25 consumer report, the operator filed a request that broke two settled decisions without naming them. Closing or refiling that mistaken request within the existing decisions changes no policy of yours, whereas [`D-OD11`](./operator-decisions-inside-tillr.md#decision-ledger) in [operator decisions inside tillr](./operator-decisions-inside-tillr.md) proposes keeping zero `operator` holds, and only you may overturn an owner ruling if the conflict was meant to challenge one. Requests you file yourself (`filed_by == "owner"`) always go straight to you (`mover: "owner"`).

   - **A — The operator first whenever `filed_by` is not `"owner"`.** Every blind collision on an operator-filed request (and any operator-filed collision with an owner ruling) holds on `mover: "operator"` first so the operator can withdraw or refile its own mistake at check-in without paging you, escalating to you only if the operator genuinely wants a ruling of yours overturned.
   - **B — Split by whose decision was broken.** When any cited decision has `authority == "owner"`, the hold goes straight to you (`mover: "owner"`); when every cited decision has `authority == "designer"` (`D-*` or `decided_by == "whale"`), the hold goes to `mover: "operator"`.
   - **C — Always you (`mover: "owner"`).** Keep zero `operator` holds (`D-OD11` strict): every conflict that bypasses `design` pages you, including mistaken operator filings.

   <!-- vantage: question id=OQ-SDC1 leaning="A — in the 2026-11-25 incident the filing itself was the operator's mistake; letting the operator close or refile its own bad request avoids paging you when no ruling is being challenged, while still reserving any change to an owner ruling to you." -->

   _Leaning:_ A — in the 2026-11-25 incident the filing itself was the operator's mistake; letting the operator close or refile its own bad request avoids paging you when no ruling is being challenged, while still reserving any change to an owner ruling to you.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-SDC2: When a settled-decision conflict names `mover: "operator"` and no foreground operator session is attached, who moves it?**

   Under [Landing Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) and [the unattended-drain design](./unattended-drain.md#67-rule-7-a-hold-names-the-operator-only-for-a-decision-the-rulebook-reserves), a held item must name a mover that can still act. If [`OQ-SDC1`](#OQ-SDC1) chooses Option A or B and the drain is running overnight with no foreground operator session watching, an item held on `operator` has no active operator until the next attended session starts (other lanes keep draining). Under [`OQ-SDC1`](#OQ-SDC1) Option C this question is moot.

   - **A — Surface to `mover: "owner"` when unattended, keeping `filed_by: "operator"` visible.** When the loop runs in unattended mode (or no foreground watch session is attached), the hold and alert address you with `filed_by: "operator"` shown on the card so no item sits on an absent mover.
   - **B — Keep `mover: "operator"` until the next operator session attaches.** The held item waits quietly for the next operator check-in without alerting you, while every other lane continues draining.

   <!-- vantage: question id=OQ-SDC2 leaning="A — Landing Principle 13 forbids a hold addressed to a mover that cannot act, and showing filed_by: operator on the card lets you see immediately that an operator filing hit a settled rule." -->

   _Leaning:_ A — [Landing Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) forbids a hold addressed to a mover that cannot act, and showing `filed_by: "operator"` on the card lets you see immediately that an operator filing hit a settled rule.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

**Who decided each row.** Inherited owner rulings (`R-OD1`, `R-RA1`, and the provenance rule) remain in their home ledgers and are summarized in [§2](#2-standing-rulings-and-authority-boundaries). Every `D-SDC*` row below is the designer's decision (written in response to an operator-filed request), never an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-SDC1` | **Designer's decision; the owner may overturn it.** A `definition` conflict bypasses `design` on its **first** report (`Status: "owner"`, `WaitingOn: "owner"`, `DesignRunID: ""`) when: (1) the item is a filed request and `request_words` omits at least one cited settled decision ID (a blind collision); (2) the item is a filed request with `filed_by != "owner"` and any cited settled decision has `authority == "owner"`; or (3) the item is document-backed (`request_words` absent) and any cited settled decision has `authority == "owner"`. It routes to `design` when zero verified settled decisions are cited, when a filed request explicitly names every cited `D-*` decision (with no non-owner collision against an `owner` ruling), or when a document-backed item cites only `designer` decisions. **Rejected:** routing every settled-decision conflict through `design` first. | 2026-11-25, designer | [§3.1](#31-why-design-has-no-move-on-a-blind-request-vs-decision-collision-d-sdc1), [§3.2](#32-first-conflict-routing-matrix-d-sdc1-d-sdc2) | — |
| `D-SDC2` | **Designer's decision; the owner may overturn it.** A bypassed conflict sets `Mover = "owner"` whenever `filed_by == "owner"` or the item has no `request_words`; for non-owner filed requests, `Mover` (`"operator"` vs `"owner"`) and unattended behavior follow the owner's rulings on [`OQ-SDC1`](#OQ-SDC1) and [`OQ-SDC2`](#OQ-SDC2) (designer leaning: `operator` when attended, `owner` when unattended). | 2026-11-25, designer | [§3.2](#32-first-conflict-routing-matrix-d-sdc1-d-sdc2), [`OQ-SDC1`](#OQ-SDC1), [`OQ-SDC2`](#OQ-SDC2) | — |
| `D-SDC3` | **Designer's decision; the owner may overturn it.** Replace the ledger-overwriting sentence in `requestObstacleDesignInstruction` ([`obstacle.go`](../../tools/tillr-drain/drain/obstacle.go)) and `briefValuesFor` ([`stage.go`](../../tools/tillr-drain/drain/stage.go)) with provenance-respecting guidance: answer tree-fact obstacles within settled decisions, revise `D-*` rows only when explicitly targeted or disproved by the tree (marked as designer decisions), never overwrite an owner ruling without dated verbatim owner words, and otherwise end with `NEXT-ACTOR: build` or `NEXT-ACTOR: owner`. | 2026-11-25, designer | [§3.3](#33-repairing-the-design-brief-instruction-d-sdc3) | — |
| `D-SDC4` | **Designer's decision; the owner may overturn it.** Add optional `DECISIONS:` to `OBSTACLE: definition` (`CRITERIA` and `EVIDENCE` stay the only required fields) and resolve candidate IDs from both `DECISIONS:` and conflict prose (`EVIDENCE`) by exact membership in a closed settled-decision index built at `reportedBlock` from (a) active rows in `decisions` (`status = 'active'`) and (b) unsuperseded `ID`-first `Decision Ledger` rows and answered `OQ-*` blocks in tracked `docs/**/*.md` at `TrunkRef`. Open `💬 OQ-*` questions and `superseded`/`revoked` rows are excluded. **Rejected:** unverified regex matching; requiring `DECISIONS:` and ignoring `EVIDENCE`. | 2026-11-25, designer | [§4.1](#41-optional-decisions-field-plus-closed-index-verification-d-sdc4) | — |
| `D-SDC5` | **Designer's decision; the owner may overturn it.** Add `filed_by` to `request_words` via `store.RegisterColumn` (falling back to earliest `placed_by` on `item.update` for older rows, and treating `""` as non-owner), and add optional `obstacle_kind`, `mover`, `filed_by`, `cited_decision_ids`, `cited_decisions`, and `unverified_decisions` fields to `item.Conflict` while keeping `WaitingOn: "owner"` so `checkWaitingOn` invariants hold. **Rejected:** widening `items.waiting_on.waiter` to include `"operator"`. | 2026-11-25, designer | [§4.2](#42-recording-filed_by-and-snapshotting-conflict-fields-d-sdc5) | — |
| `D-SDC6` | **Designer's decision; the owner may overturn it.** When `AuthorizeRetry` ([`retries.go`](../../tools/tillr-drain/drain/retries.go)) releases a blocked item whose latest conflict has `Status == "owner"` and `DesignRunID == ""`, it appends `item.Conflict{Status: "closed", WaitingOn: "worker"}` in the same transaction so `ReadConflicts`, `waitingConflicts`, and `tillr-watch` observe the closure. | 2026-11-25, designer | [§4.3](#43-closing-a-bypassed-conflict-on-tillr-drain-retry-d-sdc6) | — |
| `D-SDC7` | **Designer's decision; the owner may overturn it.** Ship an immediate `predicate_match` watch rule (`drain-settled-decision-conflict.toml`) on `event.status == "owner" and not empty(event.cited_decisions)` with `clear_when = 'event.status != "owner"'`, and show `filed_by`, `request_words`, `cited_decisions` (`id`, `authority`, `source`, `summary`), worker `EVIDENCE`, and the close / retry / supersede paths across `tillr drain why`, `tillr view waiting`, `tillr item show`, and Observatory (`Home.tsx`, `ItemDetailPane.tsx`), delivered in the four slices of [§6](#6-the-build-in-slices-that-each-land-alone). **Rejected:** automatic remedies in `tillr-watch`; generic one-line conflict summaries. | 2026-11-25, designer | [§5](#5-what-the-operator-or-owner-is-shown), [§6](#6-the-build-in-slices-that-each-land-alone) | — |
