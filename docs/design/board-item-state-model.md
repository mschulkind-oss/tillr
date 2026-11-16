---
title: Board item state model
status: accepted
stage: DECIDED
date: 2026-11-03
tags: [tillr, board, item, state, model, design]
summary: "The four owner states an item is in, the six stored dispositions and the derived working projection they became, and the rules that keep run and candidate vocabulary out of the default item presentation. Written against the board CLI, whose command names appear below as history; tillr implements the model through `tillr item`."
---

# Board item state model

> **The board CLI this document was written against was deleted on 2026-11-11**
> (`3f02e3b916`, `afdc9a9571`). The model is live: tillr implements it in `tools/tillr-item` and
> projects it through `ProjectItemState` in `tools/tillr-view/serveread.go`. The `board item ...`
> and `board --archived` spellings below are the retired program's names for the model's
> operations, kept so the ruling reads as it was argued; the live faces are `tillr item ...`.
> [The document retirement](./board-docs-retirement.md) records why this document survives while
> its neighbours were superseded.

## 1. The owner's model

An item is in exactly one of four states: **Not started → Being worked on → In the merge queue → Landed**, with one loop — a failure in the merge queue sends it back to Being worked on. There is no candidate, run, repair lane, or readmission in this model, and this document exists to keep it that way.

The owner's 2026-11-03 ruling adds exactly three things to that model, none of them a fifth step in the sequence: **Resolved elsewhere**, a second terminal done outcome ([Section 4](#4-resolved-elsewhere)); **Closed**, which replaces deletion entirely ([Section 5](#5-closed-replaces-deleted)); and **Blocked**, confirmed as real and presented as a first-class status while remaining a modifier in storage ([Section 6](#6-blocked-modifier-in-the-store-a-state-on-the-board)).

## 2. Classification of every status the board can show

| Board status | Class | Owner-facing meaning |
|---|---|---|
| `not-started` | (a) Owner state | **Not started** |
| `in-progress` | (a) Owner state | **Being worked on** |
| *(no item status)* | (a) Owner state, unrepresented on the item | **In the merge queue** — derived from "this item has a live landing-queue entry" ([Section 2](#2-classification-of-every-status-the-board-can-show), below). This is the one owner state the store does not name, and its absence is why queue-machinery words filled the space instead. |
| `completed` | (a) Owner state, if set only on landing | **Landed**. Present it as "Landed," never "completed," which invites the reading "work finished, not yet landed." If any path sets `completed` before the land succeeds, that path is a leak and the item is In the merge queue until land (see [`OQ-1`](#9-open-questions)). |
| `blocked` | (b) Real, first-class presentation, modifier in storage | **Blocked — waiting on X since T**, over an underlying Not started or Being worked on ([Section 6](#6-blocked-modifier-in-the-store-a-state-on-the-board)) |
| `resolved-elsewhere` *(new)* | (b) Real terminal outcome the model was missing | **Resolved elsewhere**: the item as written will not be built, but its intent was satisfied by another path ([Section 4](#4-resolved-elsewhere)). This replaces the meaning of `abandoned`. |
| `abandoned` | Legacy, frozen | Written under an undefined meaning; no transition may write it again. Rendered only in archive/audit views as "Abandoned (legacy — unclassified)" until the 66 existing rows are triaged ([Section 4.2](#42-migrating-the-66-existing-abandoned-items)). |
| `closed` *(new)* | (b) Real state the model was missing | **Closed**: no longer to be worked on, everything retained, reopenable ([Section 5](#5-closed-replaces-deleted)). |
| `deleted` | **Removed from the model** | Not (a), (b), or (c) — abolished. Nothing is ever deleted; items are closed/archived. Existing rows are legacy data to migrate ([Section 5.1](#51-the-26-currently-deleted-items)). |
| `running` (run) | (c) Internal | A run is executing. Projects to Being worked on, or In the merge queue if that run holds a queue entry. Never appears on an item. |
| `terminal` (run) | (c) Internal | A run ended; says nothing about the item, which landed, looped back, or is waiting. Never an item status. |
| `needs-repair`, `repairing` (run/candidate) | (c) Internal | The queue-failure loop seen from inside. To the owner this is simply **Being worked on** again — repair *is* work. |
| `repaired`, `readmitted` (run/candidate) | (c) Internal | Past-tense events in a candidate's lineage, not states of anything the owner tracks. `readmitted` projects to In the merge queue. |
| Queue entry `repaired` / `replaced by run-…` / retired candidate | (c) Internal | Disposition of a superseded queue entry and a pointer to its successor — candidate genealogy. The item never retired; one of its attempts did. |

Net: four strings are the item's story; Blocked, Resolved elsewhere, and Closed are real but are not lifecycle steps; `deleted` and new writes of `abandoned` are abolished; and the entire run/candidate vocabulary — `running`, `terminal`, `needs-repair`, `repairing`, `repaired`, `readmitted`, `replaced by`, retired — is machinery that leaked.

**In the merge queue is derived, not stored on the item.** The item is In the merge queue exactly while it has a live queue entry. When a gate fails and the entry closes, the derived state falls back to Being worked on automatically — no code path has to remember to "send it back," and the owner's loop is a consequence of the derivation rather than a transition someone maintains.

## 3. Where the internal machinery lives

| Entity | What it records | Who sees it |
|---|---|---|
| **Item** | Intent, owner-lifecycle position, the waiting-on modifier, its terminal outcome (Landed / Resolved elsewhere) or closure record, with dates and reasons | The board, by default. This is the only thing the default view draws. |
| **Run / candidate** | One attempt at the item: branch/tree, gate results, `needs-repair`/`repaired`/`readmitted`, supersession chain (`replaced by run-…`) | Item detail on request — attempt history under an item, in plain words ("Attempt 2 failed the gate; being fixed"). Run ids and raw statuses only under `--all`/debug. |
| **Landing-queue entry** | Queue position and disposition, including retired, superseded, and withdrawn entries | A queue inspector view, on request — never as a bucket on the item board. |

Attempts are subordinate, never siblings: a retired candidate appears only *inside* its item's history or *inside* the queue inspector. The moment it is a top-level bucket next to items, it reads as a peer of an item, which is the confusion that prompted this ruling.

What the operator can see on request is therefore complete but layered: the board for items, item detail for attempts, the queue inspector for entries, and `--all`/archive views for closed items and legacy rows. Nothing is hidden from the operator; it is hidden from the *default item presentation*, which is a different thing.

## 4. Resolved elsewhere

### 4.1 Semantics

**Word:** owner-facing **"Resolved elsewhere"**, store value `resolved-elsewhere`. Not "superseded" — that word belongs to candidate lineage, and [Section 7](#7-the-structural-rule-one-projector-a-closed-enum) forbids sharing vocabulary between entities. It is a terminal *done* outcome, peer to Landed: Landed means this item's work landed; Resolved elsewhere means another item, commit, or path satisfied the intent and this item's own work will not be built. It is not "won't do" — deciding not to satisfy the intent at all is a closure reason under Closed ([Section 5](#5-closed-replaces-deleted)), not this.

**Required evidence:** entering Resolved elsewhere requires a pointer — the item, commit, or named path that resolved it. No pointer, no transition. This is what makes the state auditable rather than a polite word for dropping things.

In reporting, Resolved elsewhere counts as intent-delivered but must not inflate landed throughput: Landed and Resolved elsewhere are always reported as separate numbers.

### 4.2 Migrating the 66 existing `abandoned` items

They were abandoned under the old, undefined meaning, so a bulk rename would silently assert that all 66 intents were satisfied elsewhere. Do not do that.

1. Introduce `resolved-elsewhere`; freeze `abandoned` so no transition can write it, and render it only in archive/audit views as "Abandoned (legacy — unclassified)."
2. Triage each of the 66 against one test — *can a resolving pointer be produced* (a landed item, a commit, or a note naming the alternate path)?
   - Pointer found → `resolved-elsewhere`, pointer recorded.
   - Record shows the intent was simply dropped → `closed`, reason `won't-do`.
   - Neither can be established → stays legacy `abandoned`, untouched.
3. The legacy remainder is visible migration debt — a count in the archive view — until it reaches zero. A machine may *suggest* a classification from notes and links; a human confirms each one, because the difference between the two outcomes is exactly the claim being made about the work.

## 5. Closed replaces deleted

The owner's question is the argument: a deleted item the system can still describe was never deleted — it was hidden and its work destroyed. So `deleted` is removed from the enum, `board item delete` is removed (or, during transition, exits non-zero with "use `board item close`" — never as a destructive alias), and **`board item close <id> --reason <text>`** is the only way to take an item out of work.

| Aspect | Delete (old) | Close (new) |
|---|---|---|
| Item row & history | Tombstone | **Retained in full**: intent, notes, state history, closure reason, `closedAt`, `closedBy`, and the state it was closed from |
| Branch | Destroyed | **Untouched** |
| Worktree | Destroyed | **Untouched**, path shown in item detail; removing it later is a separate, explicit workspace-hygiene act, never a side-effect of closing |
| Candidates / runs | All retired | Records retained as attempt history; none are marked or counted retired because of the close |
| Landing queue | Entry retired | Live entry is *withdrawn* (internal disposition `withdrawn-by-close`) and landing eligibility is revoked before anything else — a closed item can never land |
| Running run | — | Cancellation requested; if the land genuinely completed before the close was processed, **Landed wins** and the close fails with that stated |
| Workable pool | Gone | Excluded from dispatch, drain, aging, and counts — the same exclusion as a terminal state, achieved without destruction |
| Dependents | — | Any item waiting on this one immediately surfaces as "waiting on a closed item" ([Section 6](#6-blocked-modifier-in-the-store-a-state-on-the-board)), so a human repoints or unblocks it instead of it waiting forever on something that will never land |
| Reversal | Impossible | `board item reopen <id>` restores the closed-from state; if closed from In-the-merge-queue it reopens to Being worked on, because queue membership is derived and the entry was withdrawn — it must earn a new one |

**What the operator sees.** The default board excludes closed items and shows a single archive count; `board --archived` (or the Closed section under `--all`) lists them as *Closed · reason · date*. Item detail for a closed item looks like any other item's — history, attempts, branch, worktree — plus the closure record and a reopen action. A closed item is findable by id forever; that is the point.

**Reasons.** Close requires a reason. Free text is acceptable; common bases (`won't-do`, `duplicate-of <id>`, `mistaken-entry`, `obsolete`) may be offered as conveniences but are recorded *as the reason*, not as sub-states. `duplicate-of` as a close reason means this row was redundant bookkeeping; an item whose intent is genuinely being delivered by the other copy is Resolved elsewhere with that item as its pointer — the difference is whether the intent is still on track to be satisfied.

### 5.1 The 26 currently-deleted items

They cannot be fully restored: the old delete already destroyed their branches and worktrees and retired their candidates. What survives is the tombstone and its recorded basis, so they migrate rather than resurrect.

- Each `deleted` row becomes `closed`, its recorded basis carried over as the closure reason, the original deletion date as `closedAt`, and an internal `artifactsDestroyed` flag set.
- The archive view renders them honestly: "Closed — deleted under the old operation; branch and worktree were destroyed," so nobody goes looking for a worktree that does not exist.
- Reopen is allowed but weaker than for a normal closed item: identity, intent, and history are restored from Not started with a fresh attempt, and the reopen output says the destroyed work is gone.
- After migration the `deleted` value is dropped from the store entirely. There is no purge operation behind it.

## 6. Blocked: modifier in the store, a state on the board

Storage stays as originally argued — `waitingOn` (on whom or what, since when, what action unblocks it) is an orthogonal field on an item that also has a lifecycle state, because a blocked item resumes *to where it was*, and a `blocked` state would either erase that destination or smuggle it back as a "previously" field. But that is an implementation fact, and it must not demote blocked in what the owner sees: the projector emits **Blocked as a first-class display status** whenever `waitingOn` is set, carrying the underlying state with it.

- Row rendering leads with the block, not a footnote: **"Blocked — waiting on owner's decision, 3d · underneath: Being worked on."**
- The default board carries a Blocked count in its header, and blocked items form their own visible grouping or sort to the top of their section — visible at the same glance-cost as the in-progress set.
- "Waiting on X since T" is structured and always shown — who/what, elapsed time, the unblocking action — with aging past a threshold escalating visually; setting a block without naming the waiter is rejected, because an unspecified wait is not actionable and actionability is the point of showing it. Waiting on a now-closed item ([Section 5](#5-closed-replaces-deleted)) is rendered as its own alarm, not an ordinary wait.
- Clearing the block is one action on the row, and the item visibly returns to its underlying state — the demonstration, in the UI itself, of why it is stored as a modifier.

## 7. The structural rule: one projector, a closed enum

1. The item presentation type is a closed set — `NotStarted | Working | Blocked(underlying, waitingOn) | InQueue | Landed | ResolvedElsewhere | Closed` — with no passthrough string field for status; frozen legacy `abandoned` is a migration input rendered only in archive/audit views, never a value new code can produce.
2. Exactly one projector function translates (item + live queue entry + latest run outcome) into that enum; it is the only code allowed to mention both vocabularies, and views consuming the projection **cannot reference run or queue-entry status constants** — enforced by package boundary/lint, not discipline.
3. The projector's switch over internal statuses is exhaustive, so adding a new run status fails compilation until someone decides which owner state it means; an unmapped internal status degrades to the nearest owner state (default: Being worked on) and never renders raw.
4. A leak test pins it: enumerate every status string in the run/candidate/queue enums and assert none can appear in a rendered default item row; assert no rendered row can contain `deleted`; and assert no write path can produce `abandoned` or `deleted` — new internal statuses join the enumeration automatically, so the test fails the day one leaks.

One-sentence version: **status words belong to one entity only; translation happens in exactly one projector; a view that can read a run status is a bug even if it doesn't print it.** This invariant constrains the store, the drain, the CLI, and every future view, which is why it lives in this standalone document for consumers such as the whale-interaction design to cite, rather than as a section inside any one of them.

## 8. Implementation sequence

| ID | Work |
|---|---|
| SM-1 | The projector: closed owner enum ([Section 7](#7-the-structural-rule-one-projector-a-closed-enum)), derived In-the-merge-queue, exhaustive mapping, views repointed to consume the projection |
| SM-2 | Stop the bleeding: freeze all writes of `abandoned` and `deleted`; disable `board item delete` so it can no longer destroy a branch, worktree, or candidate |
| SM-3 | Schema and operations: `closed` with its closure record, `resolved-elsewhere` with its required pointer, structured `waitingOn`, plus `board item close` / `reopen` with the [Section 5](#5-closed-replaces-deleted) semantics |
| SM-4 | Legacy migration: triage the 66 `abandoned` ([Section 4.2](#42-migrating-the-66-existing-abandoned-items)), convert the 26 `deleted` ([Section 5.1](#51-the-26-currently-deleted-items)), then drop both values from the enum |
| SM-5 | Presentation: Blocked as a first-class display status, archive/closed views, plain-words attempt history, and the leak/write tests of [Section 7](#7-the-structural-rule-one-projector-a-closed-enum) |

**Order: SM-2 → SM-3 → SM-1 → SM-5 → SM-4.**

SM-2 goes first because it is the only item whose delay causes irreversible harm: every day without it, a delete can destroy work that [Section 5.1](#51-the-26-currently-deleted-items) shows cannot be restored, and every new `abandoned` write grows the unmigratable legacy pool. It is also the smallest change in the set — freezing two write paths needs none of the new machinery.

SM-4 goes last because migration is a one-way semantic claim about 92 items and everything it depends on must already exist: the lawful destinations (`closed`, `resolved-elsewhere` with pointers — SM-3), a projector that can render the legacy remainder honestly while triage is in progress (SM-1, SM-5), and a frozen population so the 66 and the 26 are a fixed set rather than a moving target (SM-2). Dropping the enum values is safe only once no consumer can produce or expect them.

The middle order follows: SM-3 before SM-1 because the projector cannot emit Closed or Resolved elsewhere before those states and their records exist; SM-5 after SM-1 because the view work is rendering the projection, and its leak test is what proves the projector boundary holds before SM-4 makes the legacy values unreachable.

## 9. Open questions

- **OQ-1:** Is `completed` written only by a successful land? If any path writes it earlier, that row of [Section 2](#2-classification-of-every-status-the-board-can-show) moves to class (c) and Landed must be derived from the landing record. Needs one pass over the writers of that field before SM-1.
- **OQ-2:** Are the recorded bases on the 26 deleted tombstones complete enough to serve as closure reasons ([Section 5.1](#51-the-26-currently-deleted-items)), and how do the 66 split under the [Section 4.2](#42-migrating-the-66-existing-abandoned-items) pointer test? Both are answered by inspection during SM-4 triage, not assumed here.