---
title: "Designer Sessions and Review Comments"
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - tools/tillr/settings/settings.go
  - tools/tillr-item/comments.go
  - tools/tillr-item/item/comments.go
  - tools/tillr-item/item/comment_intake.go
  - tools/tillr-drain/drain/comments.go
  - tools/tillr-drain/drain/comment_intake.go
  - tools/tillr-drain/drain/comment_observatory.go
  - tools/tillr-drain/drain/designer.go
  - tools/tillr-drain/drain/designer_compaction.go
  - tools/tillr-run/backends/pi/compact
  - tools/tillr-view/designers.go
  - tools/tillr-view/comment_intake.go
  - tools/tillr-view/web/src/components/Designers.tsx
vantage:
  status-chip: true
related:
  - ./observatory-redesign.md
  - ./observatory-actions.md
  - ./tillr-item-states.md
  - ../design/tillr-review-comments.md
  - ../tillr/decision-provenance-review.md
---

# Designer Sessions and Review Comments

> **Delivered copy.** This is `docs/reference/tillr-designer-sessions.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-designer-sessions.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Verified 2026-11-24 by reading the code named in `covers`.
UNMEASURED: whether a resumed session answers a comment better than a new one
has not been measured beyond the cost comparison in
[What a turn costs, measured](#what-a-turn-costs-measured).

**Reads with:** [observatory-redesign.md](observatory-redesign.md) (the
observatory's pages and waiting classes),
[observatory-actions.md](observatory-actions.md) (idea intake and queue
placement), [tillr-item-states.md](tillr-item-states.md) (item states), and
[tillr-review-comments.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-review-comments.md) (a proposed
comment command that needs no Vantage server).

---

## Overview

Review comments left in Vantage (the Markdown viewer whose review API tillr
reads) on a tracked design document become a generated `design`-seat item. That item
runs in a lane directory reserved for the document — its **desk** — and resumes
the harness session that last worked on that document. No agent process stays
alive between turns: the session persists as its harness transcript file in the
desk's durable directory, and the desk's worktree exists only while a run needs
it.

```text
Vantage review API ──► automatic intake / `tillr item comments take`
                             │
                             ▼
                  item-comments-<doc>-<N>  (holds unstarted <doc> items)
                             │
                             ▼
                   session choice at desk-<doc>
          ┌──────────────────┼──────────────────┐
          ▼                  ▼                  ▼
     new session       resumed-warm     resumed-compacted /
                                         resumed-expired
          └──────────────────┼──────────────────┘
                             ▼
            gate + squash land (or zero-commit answer)
                             │
                             ▼
       served-blob check ──► .vantage/inbox/<item_id>.jsonl
```

<a id="the-words-this-design-uses"></a>

## Terms and ownership

- **Last designer** — the one recorded harness session per document that most
  recently completed a run at that document's desk, including a failed first
  design run. Stored in the `last_designers` table with the session id,
  backend, model route, desk, last request time, context size, cache lifetime
  and transcript path. Not the session that first wrote the document.
- **Desk** (`desk-<doc>`) — a lane outside the numbered and express pools,
  reserved for one document. Its directory and its `durable/` subdirectory
  persist across runs, so the harness finds the transcript at the same
  working-directory path; its `worktree` exists only during a run or while a
  candidate is retained.
- **Comment batch** — one sitting of pending review comments on one document,
  captured atomically with its `item-comments-<doc>-<N>` item. A comment
  already taken at the same turn count is never taken twice.

| Package | Responsibility |
| :--- | :--- |
| `tools/tillr/settings` | Parses the `[comments]` table of `.tillr.toml` |
| `tools/tillr-item` | Reads Vantage's review endpoint, selects quiet unanswered comments, computes the batch's fence, and writes the batch and its item |
| `tools/tillr-drain` | Polls Vantage, holds dependent items, routes desks, chooses, compacts and records designer sessions, checks replies, and delivers them after landing |
| `tools/tillr-run/backends/pi/compact` | Compacts an expired Pi session in place and reports token counts and spend |
| `tools/tillr-view` | Serves designer sessions and comment batches read-only |

---

## Comment intake and batch items

Comments enter the store on demand through `tillr item comments take`, or
automatically during a drain pass when `.tillr.toml` declares `[comments]`.
Without that table the loop never polls; this repository's
[tracked settings](https://github.com/mschulkind-oss/tillr/blob/main/.tillr.toml) do not declare it.

### Pending comments and the quiet window

- **Read.** Intake calls Vantage's public review endpoint,
  `GET <vantage_url>/api/r/<repo>/review?path=<doc>`. Vantage's own database
  files are never read.
- **Pending.** An unresolved, non-empty comment is pending when its latest
  reviewer change is newer than the agent's answer for that round, or no such
  answer exists. A `noted` acknowledgment is not an answer.
- **First-intake baseline.** The first automatic poll stamps a baseline time
  once. Automatic polling ignores comments whose latest reviewer change
  predates it and records their count per document, so the observatory can
  show them without flooding the queue with old threads.
  `tillr item comments take --older` includes them.
- **Quiet sitting** (`D-CD9`). Pending comments on one document are judged
  together. If any has an open comment box, nothing is taken for that document.
  Otherwise nothing is taken until the most recent reviewer activity is older
  than the quiet window. Hand intake in a repository with no `[comments]` table
  skips the timer and takes pending comments with closed boxes at once.
- **Outage.** Automatic intake fetches every eligible document under
  `comments.paths` before it opens its write transaction. If any fetch fails,
  no batch is written and the observatory shows Vantage as not answering.

### The generated item

`TakeComments` (package `item` in `tools/tillr-item`) writes one transaction:

- **One open batch per document.** While a comment item for the document is in
  any state that is not finished, no second batch is created; remaining
  comments wait for the next pass after it settles.
- **Identity and rank.** The item is `item-comments-<doc>-<N>`, where `<doc>`
  is the document's name without `docs/design/` and `.md`, and `N` counts the
  document's batches. It is ranked ahead of every ordinary ranked item, with
  `seat = "design"` and state `in-queue`.
- **Fence.** The document, its companion `<stem>-plan.md` when that file
  exists, and every `roadmap.md` under `docs/` that links to the document.
- **Criteria.** Two mechanical criteria: revise the document (or leave zero
  commits when it already answers the comments) and write one JSON reply per
  comment to `replies.jsonl` in the desk's durable directory; and
  `uvx vantage-check <doc>` exits `0`.
- **Hold on dependent work** (`D-CD11`). While a comment item for a document is
  open, unstarted items whose design document is that document hold with
  `design-under-comment`. Runs already in flight finish undisturbed.
- **Check before dispatch.** Just before claiming a comment item, the loop
  re-reads the review. If Vantage does not answer, the item is not claimed
  (`Vantage not answering`). If every comment in the batch has since been
  deleted or resolved, the loop closes the item and replans in the same pass.

---

## The desk

Every comment item, and every eligible single-document `design`-seat item, runs
at its document's desk and not in a numbered or express lane (`D-CD3`).

- **Routing.** A comment item always routes to its batch document's desk, with
  or without `[comments]`. Any other `design`-seat item routes to a desk only
  when `[comments]` is configured and the item's fence names exactly one
  literal `.md` path inside `comments.paths`. A design run fenced to several
  documents or to a directory uses an ordinary or express lane and records no
  last designer.
- **One occupant.** One item occupies a desk at a time, counting live leases,
  unsettled candidates and dispatches in the current pass. A second item holds
  with `waits on the document's desk`.
- **Desk cap.** Active document desks are capped at `comments.max_running`,
  independent of the numbered and express lane counts. Further desk items hold
  with `waits on a desk`. The trunk fixer's desk is not a document desk and is
  not counted.
- **Worktree lifetime.** Staging checks out `<lane-root>/desk-<doc>/worktree`
  on the desk's own branch. Resetting or releasing the desk removes the
  worktree and leaves `durable/` intact, so the transcript survives.
- **Boundary checks.** A run's write-boundary check excuses desk worktrees and
  desk branches inside the loop's own lane scope, so staging or tearing down a
  desk never convicts an unrelated run.

---

## Choosing at dispatch

When a run is planned or claimed at a desk, `chooseDesigner` (package `drain`)
evaluates the document's last designer in this order. If seat selection
switches the backend inside the claim, the choice is made again.

| Condition | Session used | Recorded reason |
| :--- | :--- | :--- |
| No last designer for the document | New session | `no-designer` |
| The previous backend's adapter declares `cannot-resume` | New session | `cannot-resume` |
| The backend or model route differs from the last designer's | New session | `seat-changed` |
| The transcript is missing or not a regular file | New session | `transcript-missing` |
| The cache lifetime is known and has not run out | The recorded session | `resumed-warm` |
| The cache is expired or unknown, the context is over the compaction threshold, and the adapter has a `compact` step | The recorded session, after compaction | `resumed-compacted` |
| Otherwise | The recorded session, as it is | `resumed-expired` |

### The staged brief and the one fallback

- **Brief.** The brief embeds every comment in the batch with its thread, the
  answering rules, and a `What moved:` section. A new session is told to read
  the document, and is given the previous transcript's path as optional
  reading when there was one. A resumed session is told the document is
  unchanged, or is shown the diff since the blob it last saw, or — when that
  diff is long ([Current values](#current-values)) — is told to read the
  document again. A compacted session is told its earlier conversation was
  summarized.
- **One fallback on a dead resume** (`D-CD10`). If a resumed run exits nonzero
  having produced no assistant text, thinking or tool call, the loop marks that
  run `resume-failed` and dispatches once more in the same settlement with a
  new session and the prior transcript path in the brief. A run that produced
  any model output is not replayed automatically, so a half-applied edit is
  never duplicated.

---

## The keep-warm rule

A session is warm only for the provider's prompt-cache retention reported on
its run stream (`D-CD5`). No background keep-alive request runs.

- **Recording.** After any run at a document desk settles — including a writing
  run that failed before creating the document — the loop records the last
  designer from the run's log: session id and transcript, the time and context
  size of the last assistant turn, the cache lifetime the provider reported,
  and the document's blob id on the lane's head. A cache lifetime the provider
  did not report stays unknown; no lifetime is assumed.
- **Retirement** (`D-CD8`). At the start of each pass the loop deletes a
  document's last-designer row when the document is gone from both the working
  tree and the trunk commit pinned for the pass, or when the document's stage
  has a `done` role. Only the row is removed; the harness owns transcript
  retention.

> [!NOTE]
> **Two settings are parsed and not used.** `comments.compact_over_tokens` and
> `comments.refresh_hours` are validated and printed at startup, but
> `rg -n 'CompactOverTokens|RefreshHours' tools/tillr-drain tools/tillr-view -g '*.go'`
> returns nothing: the compaction threshold is a literal in `chooseDesigner`,
> and nothing sends a cache-refresh request.

### Compaction and crash recovery

Compaction runs at dispatch, and only when the choice was `resumed-compacted`
(`D-CD6`, `D-CD7`):

1. **Journal first.** Before the adapter's `compact` step runs, the loop
   creates a journal file exclusively, keyed by the session and the last
   designer's run, recording the transcript's current size.
2. **Compact.** The adapter compacts the session in place and prints a receipt:
   tokens before and after, and what the compaction spent.
3. **Record.** The receipt is written into the journal and into the run's
   directory, and a `drain.designer-compacted` event is appended.
4. **Recover.** If a journal already exists with no receipt, the loop takes the
   receipt from the run's directory, or reconstructs it from the transcript
   when exactly one compaction entry follows the recorded size. Anything else
   is ambiguous, and the loop returns an error instead of paying for a second
   compaction.

<a id="what-a-turn-costs-measured"></a>

### What a turn costs, measured

The compaction threshold and the resume-over-replace policy rest on one
measurement: a single comment turn against a 35,000-token prior design
conversation, with one-hour prompt-cache retention, priced four ways.

| Turn shape | Measured turn cost |
| :--- | :--- |
| New session that reads the document and its neighbors | `$0.84` |
| Resumed inside the cache window | `$0.42` |
| Resumed after expiry, not compacted | `$1.11` |
| Compacted after expiry, then resumed | `$1.21` |

Compacting first costs more than resuming as-is at this size and breaks even
at roughly 40,000 tokens of context.

---

## Settlement and reply delivery

When a comment run ends `RESULT: done`, settlement checks `replies.jsonl`
before it closes or lands the item:

- **One reply per comment.** Each line must be valid JSON naming a comment in
  the batch, the round that comment was at when taken, and a non-empty summary.
  A missing, duplicate, foreign or wrong-round reply records a mechanical
  `not_done` verdict on the first criterion and refuses the candidate.
- **Zero-commit answer.** When the designer made no commit and the worktree is
  clean, the loop delivers the replies and closes the item as answered without
  a revision.
- **Committed revision** (`D-CD4`). The candidate passes the fence check, the
  gate and the squash landing like any other. Only after the landing is
  recorded does the loop store the replies and the landed blob id for delivery.
- **Served-checkout guard.** Before writing `.vantage/inbox/<item_id>.jsonl`,
  the loop compares the document on disk in the served checkout with the
  landed blob. If they differ, delivery waits and is retried on later passes.
  Once they match, the file is written atomically.
- **Confirmation.** After Vantage consumes the inbox file, and when a Vantage
  server is configured, the loop reads the review once more and records each
  reply as `delivered`, `dropped` (the comment is gone) or `not confirmed`.

---

## Observatory surfaces

`tillr-view` shows designer sessions and comment batches read-only:

- **`GET /api/v1/designs`** lists every document with a last designer, a
  comment batch or an item, with the designer's state and counts of how
  comments arrived relative to the cache lifetime.
- **The document pane** shows the last designer: session id, derived state
  (`warm`, `compacted` or `expired`), last request, context size, and links to
  each batch and run with its dispatch reason.
- **A comment item's detail** lists its comments with each reply's delivery
  state.
- **Waiting rows** appear when a reply waits on the served checkout, when
  Vantage is not answering, and when pending comments predate the first
  intake; the last names `tillr item comments take --doc <doc> --older` as the
  move.

Dollar amounts are stripped from every API payload before it is served; see
[observatory-redesign.md](observatory-redesign.md).

---

## Current values

| Value | Literal | Defined in |
| :--- | :--- | :--- |
| Quiet window | `comments.quiet_seconds`, default `60` | `tools/tillr/settings` |
| Vantage poll interval | `comments.poll_seconds`, default `15` | `tools/tillr/settings` |
| Active document desks | `comments.max_running`, default `3` | `tools/tillr/settings` |
| Compaction threshold | context over `40000` tokens | `chooseDesigner`, package `drain` |
| Inline diff ceiling in the brief | fewer than `400` lines | `tools/tillr-drain/drain/comments.go` |
| Hold reasons | `design-under-comment`, `waits on the document's desk`, `waits on a desk`, `Vantage not answering` | `Rules`, package `holdrules` |
| Event kinds | `drain.designer-compacted`, `drain.comment-reply-state` | package `drain` |

---

<a id="decision-ledger"></a>

## Why it's this way

The owner confirmed `D-CD1`–`D-CD11` in
[`OQ-PR12`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR12), 2026-11-20.

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| `D-CD1` | Each sitting of comments on a document becomes a new generated `design`-seat item; it neither reopens the landed design item nor attaches to a build item | A comment batch has its own fence, its own mechanical criteria and its own landing. |
| `D-CD2` | The last designer is the session that most recently ran at the desk, not the session that first wrote the document | Later sessions have read the document as revised; the first author's memory is stale after any other turn. |
| `D-CD3` | Every design or comment session for a document runs in one fixed desk directory | Harness session lookup keys a transcript by working directory, and transcripts hold paths into that directory. |
| `D-CD4` | The drain delivers replies only after landing, and only once the served checkout matches the landed blob; the agent turn never delivers | Vantage renders from the served checkout; an earlier reply shows stale prose or leaks a reply from a refused run. |
| `D-CD5` | A session is warm only for the cache lifetime its stream reported; nothing extends it | Whether paying to extend the cache is ever worth it is unmeasured. |
| `D-CD6` | Compaction runs at the next dispatch after expiry, never on a timer | Compaction after expiry reads uncached tokens either way; a timer pays for sessions that never get another comment. |
| `D-CD7` | An expired session is resumed — compacted above the threshold, as-is below it — and not replaced | A new session is cheaper on the first expired comment ([measured](#what-a-turn-costs-measured)) but loses the trade-offs and owner remarks only the session holds. |
| `D-CD8` | One last-designer row per document is kept until the document is deleted or its stage is done | A transcript is small; evicting by time or count discards context to save negligible space. |
| `D-CD9` | Automatic intake waits for a quiet window per document and takes nothing while a comment box is open | One review pass becomes one turn and one commit, and the reviewer is not raced mid-sentence. |
| `D-CD10` | A non-resumable adapter, a changed route, a missing transcript, and a resume that produced no model output each fall back once to a new session | It matches the drain's general session-recovery rule and never duplicates edits from a run that got partway. |
| `D-CD11` | An open comment item holds unstarted items on its document and leaves running ones alone | A comment signals the design may change; holding unstarted work avoids wasted runs without discarding nearly finished ones. |
