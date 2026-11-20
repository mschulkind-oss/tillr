---
title: "Owner Review Comments: Standing Rule, Fast Track, and Vantage Seam"
status: in-review
stage: DESIGN
updated: 2026-11-20
next: "Review of designer's decisions D-RC1 to D-RC8"
vantage:
  status-chip: true
related:
  - ../reference/tillr-designer-sessions.md
  - ./tillr-intake-paths.md
  - ./tillr-early-design-landing.md
  - ./tillr-live-drain-config.md
  - ./tillr-vocabulary.md
  - ../tillr/decision-provenance-review.md
---

# Owner Review Comments: Standing Rule, Fast Track, and Vantage Seam

**Status:** In review · 2026-11-20. Nothing in this design beyond the Vantage-coupled path of [designer sessions](../reference/tillr-designer-sessions.md) is built.

The owner answered all four questions on 2026-11-20. The [Decision Ledger](#7-decision-ledger) records his words and identifies the eight engineering decisions the designer made. The owner may overturn those decisions.

---

## 1. What happened on 2026-11-19, and what is true in the tree today

On 2026-11-19 at about 6:55 PM ET, the owner answered four open questions as Vantage review comments and then asked:

> "there should be a standing rule, and perhaps even a special track for, vantage comments. we 'work well with vantage' but we don't require it."

What happened in that round exposes four gaps in the tree at `9a3b977af`:

- **Automatic comment intake was off, and settings tie document desks to Vantage.** [`.tillr.toml`](../../.tillr.toml) sets `[lanes] express = 2` and has no `[comments]` table. In [`tools/tillr/settings/settings.go`](../../tools/tillr/settings/settings.go) (`Read`), adding `[comments]` is refused unless `comments.vantage_url` names an HTTP server. When `Comments` is `nil`, `deskDocument` in [`tools/tillr-drain/drain/comments.go`](../../tools/tillr-drain/drain/comments.go) returns empty for ordinary `design`-seat runs, so initial design runs never stage at `desk-<doc>` or leave a warm transcript there.
- **No Vantage-free comment command existed, so the operator filed four ordinary requests.** Respecting the rule that only a designer inside the drain edits a design document, the operator did not edit `/workspace/docs/` directly. Because `tillr item comments take` in [`tools/tillr-item/comments.go`](../../tools/tillr-item/comments.go) requires `--vantage` and reads only Vantage's HTTP review API, the operator called `tillr item request --seat design --ordinary` four times instead. Passing `--ordinary` (`Express = false` in [`tools/tillr-item/item/request.go`](../../tools/tillr-item/item/request.go)) barred all four items from the idle `express-1` and `express-2` slots, queued them behind ordinary build lanes, expanded each to the two-node cold `scouted-design@v1` graph (`RequestDefaultWorkflow["design"]`), and skipped comment-reply delivery altogether.
- **Even on `item-comments-*`, four latency and ordering traps remain in the code.**
  1. `selectWorkflow` in [`tools/tillr-drain/drain/workflow_defaults.go`](../../tools/tillr-drain/drain/workflow_defaults.go) forces `single-worker@v1` only for `IsGeneratedRepair`, so setting `[workflows.default] design` would expand `item-comments-*` into a multi-node cold workflow and break warm session resume.
  2. `readOrderedItems` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) puts `rewrite-item-*` in priority bucket `1` ahead of `item-comments-*` in bucket `2`.
  3. `TakeComments` in [`tools/tillr-item/item/comments.go`](../../tools/tillr-item/item/comments.go) prepends `"0m"` before the lowest existing rank, so a second comment batch gets `"00m"` ahead of an earlier waiting `"0m"` batch (last-in, first-out) and a later `tillr item request` can jump ahead of it.
  4. As measured in [the 2026-11-19 quick-request latency study](../tillr/latency/2026-11-19-quick-request-latency.md) ([Section 5](../tillr/latency/2026-11-19-quick-request-latency.md#5-ranked-avoidable-delays-and-one-piece-fixes)), two-pass post-run settlement (`deferJudge=true` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go)) and unscoped gate checks turn 11–40 seconds of active editing into 10–27 minutes of wall time.
- **Vantage details leak across six packages, while hand-edited answer blocks fell between two designs.** [`tools/tillr/vantage/vantage.go`](../../tools/tillr/vantage/vantage.go) shells out unconditionally to `uvx vantage-check index --format json` to read open questions; `TakeComments` hardcodes `uvx vantage-check <doc> exits 0` as criterion `c2`; `commentThreadTask` and `writeCommentDelivery` in [`tools/tillr-drain/drain/comments.go`](../../tools/tillr-drain/drain/comments.go) embed `"Vantage round"` and `.vantage/inbox/` directly. Meanwhile [designer sessions](../reference/tillr-designer-sessions.md) left hand-edited `**Answer:**` blocks to [the idea pipeline](./tillr-idea-pipeline.md), and [the idea pipeline](./tillr-idea-pipeline.md) ([Section 3](./tillr-idea-pipeline.md#3-the-questions-path)) marked its own answer-return path superseded by [designer sessions](../reference/tillr-designer-sessions.md), leaving neither built.

---

## 2. The standing rule

When the owner comments on a tracked design or planning document or answers one of its open questions — through a delivered Vantage comment or in his own words in a session — one rule governs who acts, in what order, and how fast.

### 2.1. Who acts, and what the operator never does

1. **Only a `design`-seat run inside the drain edits a design document.** Neither the human's interactive coordinator session nor a `build`, `docs`, or `scout` seat edits a design document, its companion `<topic>-plan.md`, or a linking roadmap on the trunk checkout. Every change reaches `main` through the `git-landing` charter ([OQ-VO4](./tillr-vocabulary.md#OQ-VO4), [OQ-VO5](./tillr-vocabulary.md#OQ-VO5)) ahead of any build work that depends on it ([OQ-EL1](./tillr-early-design-landing.md#OQ-EL1)).
2. **The operator session is a five-second relay, never a drafter or a `request` filer (`D-RC1`).** For a comment or answer spoken in a session, or a design comment already delivered, the operator records the owner's exact words and runs `tillr item comment` right away (`< 5 s`), with zero repository scouting beforehand. Filing an owner comment or open-question ruling via `tillr item request` (express or `--ordinary`) is forbidden by standing rule; `tillr item request` refuses `--question` and directs callers commenting on an existing document to `tillr item comment`.

### 2.2. Exact order of events from comment arrival to reply

Every owner comment or question ruling follows five steps, regardless of how it arrived:

1. **Capture and per-document batching (`t = 0`).** Intake records a transport-neutral `CommentBatch` in `.tillr/state.db` (`TakeComments`): target document path, arrival source (`"cli"` or `"vantage"` for a delivered Vantage comment), verbatim comment entries (each with comment ID, round, optional `OQ-…` ID or quoted anchor, body, author, timestamp, and opaque external reference), and fence `{<doc>, <doc>-plan.md (if present), linking roadmaps}`. Multiple comments for the same document in that intake fold into one `item-comments-<doc>-<N>`; a batch spanning *K* documents creates *K* independent items in the same transaction ([Section 3.4](#34-how-a-multi-document-batch-is-split-and-coalesced)).
2. **Immediate loop wake and `design-under-comment` hold (`t < 1 s`).** Writing `item-comments-<doc>-<N>` wakes the drain's wait loop immediately. On that pass, unstarted build or design items whose `design_doc` or fence names `<doc>` hold on `design-under-comment` (`plan.go`), preventing any worker from starting against prose the owner has just amended or overturned.
3. **Single-turn `design`-seat edit at `<doc>`'s desk (`15–45 s` active).** The planner dispatches `item-comments-<doc>-<N>` ahead of express and ordinary items onto `desk-<doc>`, resuming `<doc>`'s `last_designer` session when available ([Section 3.3](#33-seat-workflow-and-session-reuse)). The designer applies the comment to `<doc>`, compacts any answered open question into the Decision Ledger with the owner's dated verbatim quote ([OQ-PR22](../tillr/decision-provenance-review.md#OQ-PR22)), updates frontmatter (`status`, `stage`, `next`) and `<doc>-plan.md` / roadmaps inside its fence as needed, writes one reply entry per comment to `<scratch>/replies.jsonl`, runs the repository's document check on the touched files, and commits once (or leaves zero commits when the comment needed an explanation without a document change).
4. **Documents-only gate and squash landing (`3–10 s`).** When the run exits, settlement runs immediately without waiting for unrelated build runs in the pass (`D-RC5`), executes only gate checks that apply to Markdown documents, squashes the commit onto `main` (`Trunk.Squash`), and updates the served working-tree checkout.
5. **Post-save reply delivery (`< 1 s` after save).** Only after `landings` records `result = 'landed'` **and** `<root>/<doc>` on disk matches the landed Git blob (`git cat-file blob`, or immediately on a zero-diff `answered` run) does the drain write the verified reply receipt to the store and invoke the origin's reply sink ([Section 4](#4-comment-response-delivery-and-what-a-reply-says-about-unbuilt-work)).

### 2.3. Target turnaround budget

Grounded in the [2026-11-16 warm-resume measurement](../reference/tillr-designer-sessions.md#what-a-turn-costs-measured) (`$0.42` warm vs `$0.84` cold) and the [2026-11-19 quick-request latency study](../tillr/latency/2026-11-19-quick-request-latency.md) (`11–40 s` active tool work on single-file edits), the end-to-end target from batch intake to saved document and delivered reply is **under 60 seconds** on a warm desk and **under 90 seconds** cold:

| Step | Actor | Target wall time | What keeps it inside budget |
| :--- | :--- | :--- | :--- |
| Intake & wake | `tillr item comment` | `< 1–5 s` | Direct store write wakes `waitForTrigger`; no operator exploration |
| Dispatch & stage | `tillr-drain` planner | `< 2 s` | Priority bucket `1` ahead of express/rewrite (`D-RC2`); persistent `desk-<doc>` worktree |
| Designer turn | `design` seat (`single-worker@v1`) | `15–45 s` warm (`30–65 s` cold/compacted) | One node (`D-RC3`), prompt cache warm inside 1 hour, narrow Markdown fence |
| Gate & squash land | `tillr-gate` + `tillr-land` | `3–10 s` | Immediate settlement + documents-only gate checks (`D-RC5`), skipping code test suites |
| Save check & reply | `deliverCommentReplies` | `< 1 s` | Blob-match check on `<root>/<doc>`, atomic store receipt + origin sink (`D-RC6`) |

---

## 3. The comment track through the drain

Owner review comments get **their own track (`comment`)**, separate from `ordinary` and ahead of `express`. Sharing `express` as an undifferentiated queue would still put a 30-second question ruling behind two 10-minute `scouted-design@v1` idea drafts occupying `express-1` and `express-2`.

### 3.1. The four tracks compared

| Track | Item IDs | Planner bucket | Worktree & concurrency | Seat & workflow | Detours skipped | Gate scope |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Repair** | `fixer-main-*`, `defect-*` | `0` (first) | `desk-trunk-fixer` or free lane | `build` · `single-worker@v1` | `triage-no-block`, `prose.rewrite`, `question-open` | Touched check scope |
| **Comment** | `item-comments-<doc>-<N>` | `1` (ahead of express & rewrites; FIFO by batch ID, `D-RC2`) | `desk-<doc>` (`D-RC8`; borrows idle express capacity first, with overflow per [OQ-RC1](#OQ-RC1)) | `design` · `single-worker@v1` (`D-RC3`) | `triage-no-block`, `prose.rewrite`, `<doc>`'s own `question-open` | Documents-only checks (`D-RC5`) |
| **Express** | `item-idea-*`, `item-request-*` (default), `express="true"` blocks | `3` (by `items.rank`, eligible for `express-*` first) | `express-1..M`, falling back to `lane-1..N` | Item's seat & workflow (`scouted-design@v1`, `planned-build@v1`, etc.) | `triage-no-block` for ideas/requests | Changed-path scope |
| **Ordinary** | Document blocks, `tillr item request --ordinary` | `3` (by `items.rank`, `lane-1..N` only) | `lane-1..N` (`rewrite-item-*` runs in bucket `2` ahead of its target) | Seat default workflow | None | Full applicable gate |

### 3.2. Precedence, worktrees, and capacity

- **Ahead of other express work and prose rewrites (`D-RC2`).** `readOrderedItems` places `item-comments-*` in planner bucket `1`, immediately after trunk/tooling repairs (`bucket 0`) and ahead of `rewrite-item-*` (`bucket 2`) and all `express` and `ordinary` items (`bucket 3`). Within bucket `1`, items order first-in, first-out by `comment_batches` sequence rather than prepending `"0m"` / `"00m"` strings onto `items.rank`. Later `tillr item request` calls or queue reorders therefore never jump ahead of a waiting comment batch, and `order.record` (`docs/tillr/queue-order.txt`) does not churn on transient comment items.
- **Physical worktree is always `desk-<doc>`, even without Vantage (`D-RC8`).** On 2026-11-17, moving comment runs onto `express-1` broke warm resume because the `pi` adapter resolves `--session <id>` inside the lane's own working directory ([roadmap record](../tillr/roadmap.md#filed-2026-11-15-the-loop-says-why-every-item-is-where-it-is-and-what-it-has-spent)). Every comment run for `<doc>` — and every single-document `design`-seat run in a `git-landing` store, whether or not Vantage is configured — therefore executes in `.tillr/lanes/desk-<doc>/worktree`. Two runs never edit `<doc>` concurrently because `desk-<doc>` serializes them (`document-desk-busy`).
- **Capacity ([OQ-RC1](#OQ-RC1), option A).** A comment desk borrows an idle `[lanes] express` slot first; the Observatory shows the slot in use. If unrelated express work occupies the slots, comment desks can still start, up to `comments.max_running` (default `3`). The worktree remains `desk-<doc>`.

### 3.3. Seat, workflow, and session reuse

- **Seat and mandatory one-node workflow (`D-RC3`).** Every comment item runs on `seat = "design"` and is pinned in `selectWorkflow` to `single-worker@v1` before `[workflows.default]` is consulted. A repository default such as `[workflows.default] design = "scouted-design@v1"` still governs cold multi-file design drafts without splitting a comment turn across a read-only `open` node and a cold `write` node. Comment items also bypass `prose.rewrite` (their brief is the owner's verbatim words) and document-fed `triage-no-block`.
- **Session dispatch.** `chooseDesigner` (`tools/tillr-drain/drain/designer.go`) reuses `<doc>`'s `last_designer` at `desk-<doc>`: `resumed-warm` inside `cache_lifetime_seconds`, `resumed-compacted` above `compact_over_tokens` (40,000 tokens), `resumed-expired` at or below 40,000 tokens, or a fresh session on `no-designer`, `seat-changed`, `cannot-resume`, or `transcript-missing` (plus automatic `replaceFailedDesigner` fallback if a resumed harness session produces zero assistant output). `retireDesigners` drops `last_designers` only when `<doc>` is deleted from `main` or reaches a done stage with zero open questions.

### 3.4. How a multi-document batch is split and coalesced

When one batch of comments arrives — whether four Vantage comments across four files, three rulings spoken in one operator message across two files, or a multi-file `tillr item comment` invocation — `TakeComments` splits and coalesces strictly **by target document** (`D-RC4`):

1. **Multiple comments on the same document in one intake -> one item.** Four comments on `docs/design/doc-a.md` become a single `item-comments-doc-a-1` carrying all four entries in reading order. The designer reconciles interactions among the four comments in one coherent revision, pays prompt input tokens once, and runs the gate and squash land once.
2. **Comments spanning *K* documents -> *K* independent items in one transaction.** Two comments on `doc-a.md` and two on `doc-b.md` become `item-comments-doc-a-1` (on `desk-doc-a`) and `item-comments-doc-b-1` (on `desk-doc-b`). Each resumes its own document's designer in parallel (up to the comment concurrency limit), holds only items touching its own document (`design-under-comment`), and lands and delivers its reply as soon as its own turn finishes. If both update adjacent lines of a shared linking roadmap, `Trunk.Squash` three-way merges disjoint edits and `conflictFix` reconciles overlapping lines without rerunning the other document's designer ([landing principles §2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)).
3. **New comment arriving while `<doc>` already has a batch:**
   - If `item-comments-<doc>-<N>` is still `in-queue` (not yet dispatched), the new comment appends to `-<N>`'s batch in place so the desk runs once over the combined set.
   - Once `-<N>` is dispatched (`leased` / running or settling), its batch is frozen; the new comment opens `item-comments-<doc>-<N+1>`, waits on `document-desk-busy` until `-<N>` lands on `main`, and immediately resumes the now-warm `desk-<doc>` session on top of `-<N>`'s commit.

---

## 4. Comment response delivery, and what a reply says about unbuilt work

### 4.1. Delivery waits for the saved document on disk

A reviewer reading `<root>/<doc>` — whether through Vantage's browser view or in an editor — reads the working-tree file on disk at the moment the reply arrives. Delivering a reply while the commit still lives only in `desk-<doc>/worktree`, or before `Trunk.Squash` updates `<root>/<doc>`, points the reviewer at stale text where the open question is still unanswered.

`deliverCommentReplies` (`D-RC6`) therefore enforces one save barrier for every arrival source:

1. During its turn, the designer writes `<scratch>/replies.jsonl` — one JSON object per comment in the batch: `{"id": "<comment_id>", "round": <n>, "summary": "<plain-English reply>"}`.
2. When the run produced a commit, delivery stays `pending` until `landings` records `result = 'landed'` for that `run_id` **and** reading `<root>/<doc>` from the served checkout matches `git cat-file blob <landed_tree>:<doc>`. If `<root>/<doc>` has local uncommitted bytes (`served-file-mismatch`), delivery records `pending-checkout` and retries on subsequent passes (`deliverPendingCommentReplies`) without losing the reply.
3. When the run finished `RESULT: done` with zero diff (`outcome = 'answered'`, because the comment asked a question that needed no edit), `<root>/<doc>` already matches `HEAD`, so delivery runs immediately on settlement.
4. Once the barrier passes, the drain writes a canonical **reply receipt** in `.tillr/state.db` (`comment_runs` + `comment_replies`, storing `item_id`, `run_id`, `source`, `landed_sha`, `delivered_at`, and each `{id, round, summary}`) and invokes the origin sink:
   - **`source == "vantage"`:** the Vantage adapter atomically writes `.vantage/inbox/<item_id>.jsonl` (`temp` -> `fsync` -> `rename`) without contacting a server.
   - **`source == "cli"`:** `tillr item comment --wait` unblocks on the store receipt and prints the landed SHA, touched paths, and per-comment summaries so the operator relays them verbatim to the owner; `tillr item comment show <item-id>` and the Observatory's Item and Run pages render the same receipt at any later time.

### 4.2. What every reply must say about work recorded vs built

A comment turn is fenced to `{<doc>, <doc>-plan.md, linking roadmaps}`. It updates designs and plans; it **never edits code or builds features**. Saying *"Done — switched to running totals and froze saved entries"* without saying whether code changed misleads the reviewer into reading a saved ruling as shipped behavior.

At stage time, `stageComment` (`D-RC6`) queries `.tillr/state.db` and `<doc>`'s parsed questions and appends a short factual inventory to the brief: `<doc>`'s remaining open questions, any stored items currently held on `<doc>` (`question-open` or `design-under-comment`), and whether `<doc>-plan.md` is in the fence. Every reply `summary` in `replies.jsonl` must state **both what changed in the saved document and the exact build status of any implied implementation work**, matching one of four cases:

| Case | Required content of `summary` | Example reply |
| :--- | :--- | :--- |
| **1. Answer only (no edit)** | Answers the question from verified tree evidence and states that `<doc>` was not modified. | *"Answered from `tools/tillr-drain/drain/plan.go` (`freeLane`): express items already fall back to ordinary lanes when `express-*` are busy. No document or code changes needed."* |
| **2. Document-only or already live** | Names the updated section or ledger row and states that no code build is owed. | *"Clarified the wording in [§2.1](#21-who-acts-and-what-the-operator-never-does) to match live `Trunk.Squash` behavior. Document-only change; nothing to build."* |
| **3. Recorded and queued to build (not built yet)** | Names the compacted `OQ-…` ledger row / section and companion plan or unblocked item ID, and states explicitly **that the code is not built yet**. | *"Recorded your ruling on [OQ-UP1](../pi/live-ui-performance-design.md#OQ-UP1) in the Decision Ledger and updated [Section 3](../pi/live-ui-performance-design.md#3-settled-design-running-totals-and-freezing-saved-entries) and [live-ui-performance-design-plan.md](../pi/live-ui-performance-design-plan.md). Not built yet: `<doc>` now has 0 open questions, unblocking `item-pi-running-totals` in the build queue."* |
| **4. Recorded, still held on other open questions** | Names what was saved, states **not built yet**, and lists the remaining open `OQ-…` IDs holding the design under [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21). | *"Recorded your answer and updated the capacity rule. No code changed; the build still waits on the other question named in the receipt."* |

---

## 5. Working well with Vantage without requiring it

### 5.1. Supported inputs

The operator uses `tillr item comment` to relay the owner's words (`--doc`, `--question` or a quoted anchor, `--body`, and optionally `--wait`). `--batch <file-or-stdin>` accepts newline-delimited JSON entries (`{doc, question?, anchor?, body}`) and groups them by document in one transaction. For a delivered Vantage comment, the batch retains the original comment ID and round so the designer's reply can be matched to its thread. Tillr does not ask Vantage for comments.

A hand edit to an `**Answer:**` block is not a supported input ([OQ-RC3](#OQ-RC3)). An unexpected edit must not break tillr. No intake or other special handling is built for it.

### 5.2. One named seam: `ReviewAdapter` (`tools/tillr/vantage`)

The designer proposes two operations behind `ReviewAdapter` (`D-RC7`): `DeliverReplies` writes a reply file for delivered design comments, and `QuestionLink` builds an Observatory link from a configured base URL. That link is for the reader's browser. Tillr neither checks the URL nor assumes it is reachable from the jail.

| Subsystem | Tillr native | Vantage-specific |
| :--- | :--- | :--- |
| **Settings** | `[comments]` is valid without a URL. In document-fed `git-landing` stores `paths` defaults to `["docs/design"]`; `max_running = 3`, `compact_over_tokens = 40000`, and `refresh_hours = 0` remain the defaults. | An optional base URL for the Observatory link only. |
| **Question & stage reader** | `tools/tillr/docquestions` reads `stage`, `status`, and `## Open Questions` entries in Go, treating optional question attributes as plain HTML comments. | None at runtime. |
| **Comment intake** | `TakeComments` records the submitted `CommentBatch`. | None. |
| **Item criteria (`c1`–`c3`)** | `c1`: every batch comment addressed in `<doc>` (or answered with zero diff) and summarized in `replies.jsonl` with recorded-vs-built status; `c2`: configured document gate checks pass on `<doc>` (when any are declared); `c3`: `replies.jsonl` has one valid line per comment ID. | `vantage-check <doc>` runs as part of `c2` and the gate **only** in repositories whose gate check declarations include `vantage-check` (such as this one). |
| **Worker prompt & artifacts** | Neutral wording (`"Review batch <N> on <doc>"`), `<scratch>/replies.jsonl` (`{id, round, summary}`), and injected store inventory of open questions and held items. | `.vantage/` path fence exclusion when `.vantage/` exists; `.vantage/inbox/<item_id>.jsonl` format written by `vantage.Adapter.DeliverReplies`, never by the worker. |
| **Reply delivery & UI** | Post-landing `git cat-file blob` save check, canonical `comment_replies` receipt in `.tillr/state.db`, `--wait` CLI output, and native Observatory question/reply rendering. | Atomic write to `.vantage/inbox/<item_id>.jsonl` for delivered comments; `Open in Vantage` link when a base URL is configured. |

### 5.3. Build outline

The four questions are closed. The designer proposes four slices; recording the rulings does not implement them.

1. **Native `docquestions` reader and Vantage-free `[comments]` settings:** replace `uvx vantage-check index` subprocess calls in `tillr-drain` and `tillr-view` with `tools/tillr/docquestions`, decouple `[comments]` from `vantage_url`, and route single-document `design` runs to `desk-<doc>` unconditionally (`D-RC7`, `D-RC8`).
2. **`ReviewAdapter` seam (`tools/tillr/vantage`) and `tillr item comment` CLI:** isolate `.vantage/inbox/` reply delivery and Observatory link formatting in `tools/tillr/vantage`; add `tillr item comment` (`--doc`, `--question`, `--body`, `--batch`, `--wait`) and the `tillr item request --question` refusal guard; update operator skill instructions (`D-RC1`, `D-RC4`, `D-RC7`).
3. **Comment track priority, workflow pin, concurrency rule, and fast documents-only settlement:** bucket `1` FIFO ordering in `readOrderedItems`, `single-worker@v1` pin and `prose.rewrite` / `triage-no-block` exemptions, `in-queue` batch coalescing, [OQ-RC1](#OQ-RC1) capacity accounting, and immediate documents-only gate/settlement (`D-RC2`, `D-RC3`, `D-RC4`, `D-RC5`).
4. **Canonical reply receipts and recorded-vs-built reply check:** store `comment_replies` receipts after the `cat-file blob` save check, stage open-question / held-item inventory into `commentThreadTask`, and surface receipts on `tillr item comment --wait` and the Observatory (`D-RC6`).

---

## 6. Icebox: asking Vantage for pending comments

The operator reads [OQ-RC2](#OQ-RC2) and [OQ-RC4](#OQ-RC4) together: do not fetch comments from a running Vantage server. With no poller there is no quiet window to tune and no tracked `[comments.vantage]` intake to enable. All three belong in the [roadmap icebox](../tillr/roadmap.md#icebox), not the build outline. The existing conditional poller is documented in [designer sessions](../reference/tillr-designer-sessions.md#comment-intake-and-batch-items).

---

## 7. Decision Ledger

The owner's direction of 2026-11-19 (*"there should be a standing rule, and perhaps even a special track for, vantage comments. we 'work well with vantage' but we don't require it."*) and his earlier rulings on [OQ-VO4](./tillr-vocabulary.md#OQ-VO4), [OQ-VO5](./tillr-vocabulary.md#OQ-VO5), [OQ-EL1](./tillr-early-design-landing.md#OQ-EL1), [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21), and [OQ-PR22](../tillr/decision-provenance-review.md#OQ-PR22) frame this design. `D-RC1` to `D-RC8` are engineering decisions made by the designer in writing this document; the owner may overturn any of them. The following owner comments were delivered on 2026-11-20 at about 6:00 PM ET.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-RC1"></a>[`OQ-RC1`](#OQ-RC1) | **Owner: option A.** Owner (`0eaee1fa`): *"Option A — keeps lanes.express meaningful and visible on the board while guaranteeing comments never wait behind slow express ideas or requests."* | 2026-11-20 | [Capacity](#32-precedence-worktrees-and-capacity) | — |
| <a id="OQ-RC2"></a>[`OQ-RC2`](#OQ-RC2) | **Closed on the operator’s reading: no poller to debounce.** Owner (`885f42cd`): *"I don't understand at all what this is polling for. Vantage never updates documents. It doesn't write to the repo. Other than consuming comments and deleting that one delivered file it doesn't do literally anything so I'm not even sure what you're talking about here."* | 2026-11-20 | [Icebox](#6-icebox-asking-vantage-for-pending-comments) | — |
| <a id="OQ-RC3"></a>[`OQ-RC3`](#OQ-RC3) | **Designer’s reading: tolerate but do not process hand edits.** Owner (`cd52ee8f`): *"I don't anticipate this being something we have to do. I have never edited these answers myself. I always have agents do it. And at this point, we're agents going to be aware of Tiller. So let's protect ourselves so we don't just get broken from this. But otherwise, let's ignore this as much as possible."* | 2026-11-20 | [Supported inputs](#51-supported-inputs) | — |
| <a id="OQ-RC4"></a>[`OQ-RC4`](#OQ-RC4) | **Closed on the operator’s reading: no tracked server intake.** Owner (`6ba15a09`): *"Not sure what you're talking about here. You talking about we could go ping Vantage and reach out for comments. I don't want to do that yet. It's an interesting idea to put in the icebox, but I don't want to do that. The only Vantage integration here is purely we know about replying to Vantage design comments and putting them through their paces. And other than that, it's just that... We can provide a link from the observatory with the base URL filled in. That's the only thing we do with that base URL. It's not even assumed to be accessible from inside the jail where Tiller is operating."* | 2026-11-20 | [Vantage seam](#52-one-named-seam-reviewadapter-toolstillrvantage) | — |
| D-RC1 | **Designer's decision; the owner may overturn this.** Only a `design`-seat run inside the drain edits a design document; an operator relaying an owner comment or question ruling files `tillr item comment` within seconds with no pre-exploration and never `tillr item request` (`tillr item request` refuses `--question`). | 2026-11-19 | [Section 2.1](#21-who-acts-and-what-the-operator-never-does) | — |
| D-RC2 | **Designer's decision; the owner may overturn this.** `readOrderedItems` gives `item-comments-*` planner priority bucket `1` after trunk/tooling repairs (`0`) and ahead of `rewrite-item-*` (`2`) and all `express` / `ordinary` items (`3`), ordered FIFO by `comment_batches` ID without prepending `"0m"` strings onto `items.rank`. | 2026-11-19 | [Section 3.2](#32-precedence-worktrees-and-capacity) | — |
| D-RC3 | **Designer's decision; the owner may overturn this.** `selectWorkflow` pins `item.IsCommentItem` to `single-worker@v1` ahead of `[workflows.default]`, and comment items bypass `prose.rewrite` and document-fed `triage-no-block`. | 2026-11-19 | [Section 3.3](#33-seat-workflow-and-session-reuse) | — |
| D-RC4 | **Designer's decision; the owner may overturn this.** Multi-document intakes split into one `item-comments-<doc>-<N>` per target document in one transaction; new comments for `<doc>` append to an unstarted `in-queue` batch and open `-<N+1>` once `-<N>` has dispatched. | 2026-11-19 | [Section 3.4](#34-how-a-multi-document-batch-is-split-and-coalesced) | — |
| D-RC5 | **Designer's decision; the owner may overturn this.** Markdown-only comment runs settle immediately on exit without waiting for unrelated in-flight runs and execute only gate checks applicable to Markdown documents. | 2026-11-19 | [Section 2.2](#22-exact-order-of-events-from-comment-arrival-to-reply) | — |
| D-RC6 | **Designer's decision; the owner may overturn this.** Reply delivery waits until `landings.result = 'landed'` and `<root>/<doc>` matches `git cat-file blob` (or zero-diff `answered`), writes a canonical store receipt for `--wait` and the Observatory, and requires every reply `summary` to state both saved document changes and whether implied code work is `doc-only`, `queued (not built yet)`, or `held on open questions`. | 2026-11-19 | [Section 4](#4-comment-response-delivery-and-what-a-reply-says-about-unbuilt-work) | — |
| D-RC7 | **Designer's decision; the owner may overturn this.** Only `.vantage/inbox/` reply delivery and Observatory link formatting sit behind `ReviewAdapter` in `tools/tillr/vantage`, while open-question/stage parsing moves to pure-Go `tools/tillr/docquestions` and `[comments]` requires no Vantage URL. | 2026-11-19 | [Section 5.2](#52-one-named-seam-reviewadapter-toolstillrvantage) | — |
| D-RC8 | **Designer's decision; the owner may overturn this.** Single-document `design`-seat runs in `git-landing` stores always stage at `desk-<doc>` without requiring Vantage, so `last_designers` transcripts live at `desk-<doc>` before the first comment arrives. | 2026-11-19 | [Section 3.2](#32-precedence-worktrees-and-capacity) | — |
