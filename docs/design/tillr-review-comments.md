---
title: "Owner Review Comments: Standing Rule, Fast Track, and Vantage Seam"
status: accepted
stage: DECIDED
stage-since: 2026-11-27
updated: 2026-11-27
tags: [tillr, comments, design, drain, item, vantage]
summary: "An owner review comment or question ruling on a design is relayed in seconds through tillr item comment, edited in one turn by the document's own designer at desk-<doc> on a FIFO comment track ahead of rewrites and express work, and answered only after the saved document is on disk, stating both what changed in the document and whether implied code work is built, queued, or held. All four owner questions are ruled; only inbox reply delivery and an optional Observatory link sit behind the Vantage seam."
next: "Plan and build the four slices of Section 5.3; no question is open."
depends-on:
  - ./tillr-vocabulary.md
  - ./tillr-early-design-landing.md
  - ./tillr-design-to-planning-transition.md
  - ./tillr-per-step-question-holds.md
  - ./tillr-queue-mirrors-designs.md
  - ./tillr-scheduling-and-notifications.md
  - ../tillr/decision-provenance-review.md
vantage:
  status-chip: true
related:
  - ../reference/tillr-designer-sessions.md
  - ./tillr-intake-paths.md
  - ./tillr-live-drain-config.md
---

# Owner Review Comments: Standing Rule, Fast Track, and Vantage Seam

**Status:** Decided; none of the four comment-track slices in [Section 5.3](#53-build-outline) are built. The tree has the Vantage-coupled comment path that [designer sessions](../reference/tillr-designer-sessions.md) documents (`tillr item comments take`); it has no `tillr item comment` command, no FIFO `comment` track, and no `ReviewAdapter` or `docquestions` package (`rg 'ReviewAdapter|docquestions' tools -g '*.go'` finds nothing). The general out-of-pass settlement and change-scoped Markdown gate selection that `D-RC5` relies on have landed in [`examine_coordinator.go`](../../tools/tillr-drain/drain/examine_coordinator.go) ([scheduling and notifications](./tillr-scheduling-and-notifications.md)) and [`change_gate.go`](../../tools/gate/change_gate.go).

> **In short.** An owner comment on a design is relayed in seconds, edited by the document's own designer on a track ahead of all other work, and answered only once the saved document is on disk. tillr works well with Vantage and does not require it.

**Why it matters.** Without a Vantage-free comment command and a dedicated fast track, relaying a spoken ruling falls back to `tillr item request` (a cold multi-node run with no reply), unconfigured repositories leave `desk-<doc>` cold on first design runs, and multiple comment batches invert into last-in, first-out order.

**The shape.** One transport-neutral `CommentBatch` intake (`tillr item comment`), one FIFO `comment` track at `desk-<doc>` pinned to `single-worker@v1` (borrowing idle `lanes.express` capacity first and overflowing up to `comments.max_running`), one shared Go question/stage reader (`tools/tillr/docquestions`), one narrow `ReviewAdapter` seam for `.vantage/inbox/` replies and optional Observatory links, and one canonical post-save reply receipt stating recorded-versus-built status.

**Cost.** Four small slices across `tools/tillr`, `tools/tillr-item`, `tools/tillr-drain`, and `tools/tillr-view`, retiring runtime `uvx vantage-check index` subprocesses and Vantage HTTP polling from the drain.

**Start at** [Section 2](#2-the-standing-rule) for the standing workflow, [Section 3](#3-the-comment-track-through-the-drain) for the fast track, and [Section 5](#5-working-well-with-vantage-without-requiring-it) for the Vantage seam and build slices.

**Needs your ruling:** None in this document ([OQ-RC1](#OQ-RC1) to [OQ-RC4](#OQ-RC4) are ruled in the [Decision Ledger](#7-decision-ledger)); eight designer's decisions (`D-RC1` to `D-RC8`) in the [Decision Ledger](#7-decision-ledger) may be overturned by the owner.

**Reads with:** [designer sessions](../reference/tillr-designer-sessions.md) (what is in the tree today), [design to planning](./tillr-design-to-planning-transition.md) (the two-way exit from a comment turn into open questions or `DECIDED` planning), [per-step question holds](./tillr-per-step-question-holds.md) ([OQ-PR8](../tillr/decision-provenance-review.md#OQ-PR8)), [the queue mirrors the designs](./tillr-queue-mirrors-designs.md) (`item-design-<slug>`), and [the decision provenance review](../tillr/decision-provenance-review.md) ([OQ-PR12](../tillr/decision-provenance-review.md#OQ-PR12), [OQ-PR14](../tillr/decision-provenance-review.md#OQ-PR14), [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21), [OQ-PR22](../tillr/decision-provenance-review.md#OQ-PR22)).

---

## 1. What the tree does today

The owner's direction of 2026-11-19 frames this design: *"there should be a standing rule, and perhaps even a special track for, vantage comments. we 'work well with vantage' but we don't require it."* Four properties of the tree fall short of that:

- **Comment settings and document desks miss Vantage-free and fenceless design runs.** In [`tools/tillr/settings/settings.go`](../../tools/tillr/settings/settings.go) a `[comments]` table is refused unless `comments.vantage_url` names an HTTP server. In [`tools/tillr-drain/drain/comments.go`](../../tools/tillr-drain/drain/comments.go), `deskDocument` returns empty for non-comment `design`-seat runs whenever `comments == nil` **and** inspects only `row.Allowed` rather than `row.DesignDoc` — so both without `[comments]` and on fenceless or mirrored `item-design-<slug>` items (`R-VO2` in [the vocabulary](./tillr-vocabulary.md#51-the-work-and-its-statement), [`sync_design.go`](../../tools/tillr-item/item/sync_design.go)), a first design run neither stages at `desk-<doc>` nor leaves a warm `last_designers` transcript there.
- **No comment command works without Vantage.** `tillr item comments take` in [`tools/tillr-item/comments.go`](../../tools/tillr-item/comments.go) requires `--vantage` and reads only Vantage's HTTP review API. An operator relaying a spoken comment has only `tillr item request` ([`tools/tillr-item/request.go`](../../tools/tillr-item/request.go)), which neither refuses `--question` nor delivers a reply and queues an ordinary design run.
- **A comment item is not pinned to one node, not ahead of rewrites, and not first-in, first-out.** `selectWorkflow` in [`tools/tillr-drain/drain/workflow_defaults.go`](../../tools/tillr-drain/drain/workflow_defaults.go) pins repairs and fork rebases to `single-worker@v1` without pinning `item.IsCommentItem`, so `[workflows.default]` can expand a comment turn into a multi-node graph. `queueOrderSQL` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) orders `rewrite-item-*` ahead of `item-comments-*`, and `TakeComments` in [`tools/tillr-item/item/comments.go`](../../tools/tillr-item/item/comments.go) prepends `"0m"` below the lowest non-fixer rank, so a second comment batch receives a lower lexical rank (`"00m"` < `"0m"`) and jumps ahead of an earlier waiting batch. Out-of-pass `examine`, `gate`, and `land` coordinators ([scheduling and notifications](./tillr-scheduling-and-notifications.md)) and `--scope-to-change` in [`tools/gate/change_gate.go`](../../tools/gate/change_gate.go) have since removed unrelated-run pass waits and non-Markdown gate checks (where an earlier measurement of quick single-file requests found 11 to 40 seconds of active editing inside 10 to 27 minutes of wall time), while `checkCommentDispatch` in [`tools/tillr-drain/drain/comment_intake.go`](../../tools/tillr-drain/drain/comment_intake.go) still gates claim on a Vantage HTTP call when `vantage_url` is set.
- **Vantage details are spread across packages.** [`tools/tillr/vantage/vantage.go`](../../tools/tillr/vantage/vantage.go) shells out to `uvx vantage-check index --format json` across `tillr-item`, `tillr-drain`, and `tillr-view`; `TakeComments` hardcodes a `uvx vantage-check` criterion into every comment item; `commentThreadTask` and `writeCommentDelivery` embed Vantage wording and unconditionally write `.vantage/inbox/<item_id>.jsonl` with no source-tagged store receipt for CLI callers.

---

## 2. The standing rule

When the owner comments on a tracked design or planning document or answers one of its open questions, through a delivered Vantage comment or in words spoken in a session, one rule governs who acts, in what order, and how fast.

### 2.1. Who acts, and what the operator never does

1. **Only a `design`-seat run inside the drain edits a design document.** Neither the human's interactive coordinator session nor a `build`, `docs`, or `scout` seat edits a design document, its companion `<topic>-plan.md`, or a linking roadmap on the trunk checkout. Every change reaches the trunk through the `git-landing` charter ([OQ-VO4](./tillr-vocabulary.md#OQ-VO4), [OQ-VO5](./tillr-vocabulary.md#OQ-VO5)) ahead of any build work that depends on it ([OQ-EL1](./tillr-early-design-landing.md#OQ-EL1)).
2. **The operator session is a relay, never a drafter or a `request` filer (`D-RC1`).** For a comment or answer spoken in a session, or a design comment already delivered, the operator records the owner's exact words and runs `tillr item comment` within seconds, with no repository scouting beforehand. Filing an owner comment or question ruling through `tillr item request` is forbidden; `tillr item request` refuses `--question` and directs the caller to `tillr item comment`.

### 2.2. Exact order of events from comment arrival to reply

Every owner comment or question ruling follows five steps, however it arrived:

1. **Capture and per-document batching.** Intake records a transport-neutral `CommentBatch` in the store: the target document path, the arrival source (`"cli"`, or `"vantage"` for a delivered Vantage comment), the verbatim comment entries (each with comment ID, round, optional question ID or quoted anchor, body, author, timestamp, and an opaque external reference), and the fence `{<doc>, <doc>-plan.md if present, linking roadmaps}`. Comments on one document in one intake fold into one `item-comments-<doc>-<N>`; a batch spanning *K* documents creates *K* independent items in the same transaction ([Section 3.4](#34-how-a-multi-document-batch-is-split-and-coalesced)).
2. **Immediate loop wake and the `design-under-comment` hold.** Writing the item wakes the drain's wait loop. On that pass, unstarted build or design items whose design document or fence names `<doc>` hold on `design-under-comment`, so no worker starts against prose the owner has just amended.
3. **A single-turn `design`-seat edit at the document's desk.** The planner dispatches the comment item ahead of express and ordinary items onto `desk-<doc>`, resuming the document's last designer session when one is available ([Section 3.3](#33-seat-workflow-and-session-reuse)). The designer applies the comment, compacts any answered open question into the Decision Ledger with the owner's dated words ([OQ-PR22](../tillr/decision-provenance-review.md#OQ-PR22)), updates frontmatter (`status: accepted` and `stage: DECIDED` when zero unanswered questions remain, or `status: in-review` and `stage: DESIGN` when questions remain open per [`R-DT1`](./tillr-design-to-planning-transition.md#52-the-binary-exit-outcome-1-more-help-needed-vs-outcome-2-straight-to-planning)) and the companion plan and roadmaps inside its allowed document paths as needed, writes one reply entry per comment to `<lane-dir>/scratch/<run-id>/replies.jsonl`, runs the repository's document check on the touched files, and commits once, or leaves no commit when the comment needed an explanation and no change.
4. **Documents-only gate and squash landing.** When the run exits, the out-of-pass coordinators settle it at once without waiting for unrelated build runs (`D-RC5`), execute only the gate checks whose `reads` cover the touched Markdown documents (`--scope-to-change`), squash the commit onto the trunk (`Trunk.Squash`), and update the served working-tree checkout.
5. **Reply delivery after the save.** Only after the landing is recorded **and** `<root>/<doc>` on disk matches the landed blob, or at once on a zero-diff `answered` run, does the drain write the reply receipt to the store and invoke the origin's reply sink ([Section 4](#4-comment-response-delivery-and-what-a-reply-says-about-unbuilt-work)).

### 2.3. Target turnaround budget

The end-to-end target from batch intake to saved document and delivered reply is **under 60 seconds** on a warm desk and **under 90 seconds** cold. It rests on the measured 11 to 40 seconds of active tool work on a single-file edit ([Section 1](#1-what-the-tree-does-today)) and on warm resume costing about half a cold turn ([designer sessions](../reference/tillr-designer-sessions.md#what-a-turn-costs-measured)).

| Step | Actor | Target wall time | What keeps it inside budget |
| :--- | :--- | :--- | :--- |
| Intake and wake | `tillr item comment` | under 5 s | A direct store write wakes the drain; no operator exploration |
| Dispatch and stage | the drain's planner | under 2 s | Priority ahead of express work and rewrites (`D-RC2`); a persistent `desk-<doc>` worktree |
| Designer turn | `design` seat, `single-worker@v1` | 15 to 45 s warm, 30 to 65 s cold or compacted | One node (`D-RC3`), a warm prompt cache, a narrow Markdown fence |
| Gate and squash landing | `tillr-gate` and `tillr-land` | 3 to 10 s | Immediate settlement and documents-only gate checks (`D-RC5`) |
| Save check and reply | the drain | under 1 s | A blob-match check on `<root>/<doc>`, then an atomic store receipt and the origin sink (`D-RC6`) |

---

## 3. The comment track through the drain

Owner review comments get **their own track**, `comment`, separate from `ordinary` and ahead of `express`. Sharing `express` would put a 30-second question ruling behind long design drafts occupying the express slots.

### 3.1. The four tracks compared

| Track | Items | Planner order | Worktree and concurrency | Seat and workflow | Detours skipped | Gate scope |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Repair** | Trunk and tooling repairs | First | The trunk fixer's desk or a free lane | `build`, `single-worker@v1` | Triage, prose rewrite, open-question hold | Touched check scope |
| **Comment** | `item-comments-<doc>-<N>` | Second, first-in, first-out by batch (`D-RC2`) | `desk-<doc>` (`D-RC8`); borrows idle express capacity first, with overflow per [OQ-RC1](#OQ-RC1) | `design`, `single-worker@v1` (`D-RC3`) | Triage, prose rewrite, the document's own open-question hold | Documents-only checks (`D-RC5`) |
| **Express** | Ideas, requests by default, blocks marked express | After rewrites, by rank, eligible for express slots first | Express slots, falling back to ordinary lanes | The item's seat and workflow | Triage for ideas and requests | Changed-path scope |
| **Ordinary** | Document blocks, `tillr item request --ordinary` | After rewrites, by rank, ordinary lanes only | Ordinary lanes | The seat's default workflow | None | Full applicable gate |

### 3.2. Precedence, worktrees, and capacity

- **Ahead of other express work and prose rewrites (`D-RC2`).** Comment items order immediately after trunk and tooling repairs and ahead of rewrite items and all express and ordinary items. Among themselves they order first-in, first-out by batch sequence, not by a rank string. A later `tillr item request` or queue reorder therefore never jumps ahead of a waiting comment batch, and the recorded queue order does not churn on transient comment items.
- **The worktree is always `desk-<doc>`, with or without Vantage (`D-RC8`).** The `pi` adapter resolves `--session <id>` inside the lane's own working directory, so a comment run staged on any other directory cannot resume the document's warm session. Every comment run for `<doc>`, and every single-document `design`-seat run in a `git-landing` store — resolved from `row.DesignDoc` (including mirrored `item-design-<slug>` rows and fenceless single-document `design` requests under `R-VO2`) or a single `.md` entry in `row.Allowed`, whether or not `[comments]` is configured — executes in `<lane-root>/desk-<doc>/worktree`. Two runs never edit `<doc>` at once, because the desk serializes them.
- **Capacity ([OQ-RC1](#OQ-RC1), option A).** A running comment desk borrows an idle `[lanes] express` concurrency slot first (with or without pool `express-*` directories on disk under item-owned worktrees), and the Observatory shows the express capacity in use. If unrelated express work occupies the slots, comment desks still start immediately, up to `comments.max_running` (default `3`).

### 3.3. Seat, workflow, and session reuse

- **Seat and one-node workflow (`D-RC3`).** Every comment item runs on the `design` seat and is pinned to `single-worker@v1` before `[workflows.default]` is consulted. A repository default such as `design = "scouted-design@v1"` still governs cold multi-file design drafts without splitting a comment turn across a read-only node and a cold writing node. Comment items also bypass the prose rewrite (their brief is the owner's verbatim words) and triage.
- **Session dispatch.** `chooseDesigner` (`tools/tillr-drain/drain/designer.go`) reuses the document's last designer at its desk: warm inside the cache lifetime, compacted above `comments.compact_over_tokens`, or a fresh session when there is no designer, the seat changed, or the session cannot be resumed. [Designer sessions](../reference/tillr-designer-sessions.md#choosing-at-dispatch) owns the rule.

### 3.4. How a multi-document batch is split and coalesced

Intake splits and coalesces strictly **by target document** (`D-RC4`):

1. **Several comments on one document in one intake make one item.** The designer reconciles them in one coherent revision, pays prompt input once, and runs the gate and landing once.
2. **Comments spanning *K* documents make *K* independent items in one transaction.** Each resumes its own document's designer in parallel, up to the comment concurrency limit, holds only items touching its own document, and lands and replies as soon as its own turn finishes. If two update adjacent lines of a shared roadmap, the lander's three-way merge places disjoint edits and the conflict repair reconciles overlapping lines without rerunning the other document's designer ([landing principles §2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)).
3. **A new comment arrives while `<doc>` already has a batch.**
   - If item `<N>` is still `in-queue`, the new comment appends to its batch, so the desk runs once over the combined set.
   - Once item `<N>` is dispatched its batch is frozen; the new comment opens item `<N+1>`, which waits for the desk until `<N>` lands and then resumes the warm session on top of its commit.

---

## 4. Comment response delivery, and what a reply says about unbuilt work

### 4.1. Delivery waits for the saved document on disk

A reviewer reading `<root>/<doc>`, in Vantage or in an editor, reads the working-tree file on disk at the moment the reply arrives. A reply delivered while the commit lives only in the desk worktree points the reviewer at stale text where the question is still unanswered. One save barrier therefore holds for every arrival source (`D-RC6`):

1. During its turn, the designer writes `<lane-dir>/scratch/<run-id>/replies.jsonl` (`run.ScratchDir` in [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go)): one JSON object per comment in the batch, `{"id": "<comment_id>", "round": <n>, "summary": "<plain-English reply>"}`.
2. When the run produced a commit, delivery stays pending until the landing is recorded for that run **and** `<root>/<doc>` in the served checkout matches the landed blob. If `<root>/<doc>` has local uncommitted bytes, delivery is recorded as pending on the checkout and retried on later passes without losing the reply.
3. When the run finished with zero diff, because the comment asked a question that needed no edit, `<root>/<doc>` already matches the trunk, so delivery runs on settlement.
4. Once the barrier passes, the drain writes a canonical **reply receipt** in the store (item, run, source, landed commit, delivery time, and each reply) and invokes the origin sink:
   - **Source `vantage`:** the Vantage adapter atomically writes `.vantage/inbox/<item_id>.jsonl` without contacting a server.
   - **Source `cli`:** `tillr item comment --wait` unblocks on the store receipt and prints the landed commit, the touched paths, and the per-comment summaries for the operator to relay verbatim; `tillr item comment show <item-id>` and the Observatory render the same receipt at any later time.

### 4.2. What every reply must say about work recorded vs built

A comment turn's `AllowedPaths` list is confined to `{<doc>, <doc>-plan.md, linking roadmaps}`. It updates designs and plans; it **never edits code or builds features**. A reply that says "done" without saying whether code changed misleads the reviewer into reading a saved ruling as shipped behavior.

At stage time the drain appends a short factual inventory to the brief: the document's remaining open questions, any stored items held on the document, and whether its companion plan is in the allowed paths. Every reply `summary` states **both what changed in the saved document and the build status of any implied implementation work**, matching one of four cases:

| Case | Required content of `summary` |
| :--- | :--- |
| **1. Answer only (no edit)** | Answers the question from verified tree evidence and states that `<doc>` was not modified. |
| **2. Document-only or already live** | Names the updated section or ledger row and states that no code build is owed. |
| **3. Recorded and queued to build** | Names the compacted ledger row or section (and `stage: DECIDED` transition when the last question closed per [`R-DT1`](./tillr-design-to-planning-transition.md#52-the-binary-exit-outcome-1-more-help-needed-vs-outcome-2-straight-to-planning)) plus the companion plan or unblocked item, and states explicitly **that the code is not built yet**. |
| **4. Recorded, still held on other open questions** | Names what was saved, states **not built yet**, and lists the open questions still holding the design or its dependent steps ([OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21), [OQ-PR8](../tillr/decision-provenance-review.md#OQ-PR8)). |

---

## 5. Working well with Vantage without requiring it

### 5.1. Supported inputs

The operator uses `tillr item comment` to relay the owner's words (`--doc`, `--question` or a quoted anchor, `--body`, and optionally `--wait`). `--batch <file-or-stdin>` accepts newline-delimited JSON entries (`{doc, question?, anchor?, body}`) and groups them by document in one transaction. For a delivered Vantage comment, the batch keeps the original comment ID and round so the designer's reply can be matched to its thread. tillr does not ask Vantage for comments.

A hand edit to an `**Answer:**` block is not a supported input ([OQ-RC3](#OQ-RC3)). An unexpected edit must not break tillr, and no intake is built for it.

### 5.2. One named seam: `ReviewAdapter` (`tools/tillr/vantage`)

Two operations sit behind `ReviewAdapter` (`D-RC7`): `DeliverReplies` writes a reply file for delivered design comments, and `QuestionLink` builds an Observatory link from a configured base URL. That link is for the reader's browser. tillr neither checks the URL nor assumes it is reachable from where tillr runs.

| Subsystem | tillr native | Vantage-specific |
| :--- | :--- | :--- |
| **Settings** | `[comments]` is valid without a URL. In a document-fed `git-landing` store `paths` defaults to `["docs/design"]`. | An optional base URL, for the Observatory link only. |
| **Question and stage reader** | One shared Go package, `tools/tillr/docquestions`, used by `tillr-item` (`sync`, `show`), `tillr-drain`, and `tillr-view`: reads `.vantage.toml` `[planning.stages]` when present (with built-in stage-role defaults when absent), frontmatter (`stage`, `status`, `depends-on`), `## Decision Ledger` IDs, and `## Open Questions` entries (numbered or bulleted, treating `<!-- vantage: question … -->` directives as plain HTML comments, and `**Answer:**` blocks). | None at runtime. |
| **Comment intake** | Intake records the submitted `CommentBatch` (`source = "cli"` or `"vantage"`). | None (no HTTP polling, pre-claim HTTP check, or post-delivery HTTP confirmation). |
| **Item criteria** | `c1`: every batch comment is addressed in `<doc>`, or answered with zero diff, and summarized in `replies.jsonl` with its recorded-versus-built status. `c2`: the configured document gate checks pass on `<doc>`. `c3`: `replies.jsonl` has one valid line per comment ID. | `vantage-check <doc>` runs as part of `c2` **only** in a repository whose gate declares it, as this one does. |
| **Worker prompt and artifacts** | Neutral wording, `<lane-dir>/scratch/<run-id>/replies.jsonl`, and the injected store inventory of open questions and held items. | The `.vantage/` path exclusion when that directory exists; the `.vantage/inbox/<item_id>.jsonl` format, written by the adapter and never by the worker. |
| **Reply delivery and UI** | The save check after landing, the canonical receipt in the store, `--wait` and `show` output, and Observatory rendering. | The atomic inbox write for delivered `source = "vantage"` comments; an "Open in Vantage" link when a base URL is configured. |

### 5.3. Build outline

Four slices (the designer's decomposition; the owner may overturn it):

1. **The native question reader and Vantage-free `[comments]` settings:** replace the `uvx vantage-check index` subprocess across `tillr-item`, `tillr-drain`, and `tillr-view` with the shared `tools/tillr/docquestions` package (keeping parity tests against `vantage-check index`), decouple `[comments]` from `vantage_url`, and route single-document `design` runs (`row.DesignDoc` or single-file `row.Allowed`) to `desk-<doc>` unconditionally (`D-RC7`, `D-RC8`).
2. **The `ReviewAdapter` seam and the `tillr item comment` CLI:** isolate `.vantage/inbox/` reply delivery and Observatory link formatting behind `ReviewAdapter` in `tools/tillr/vantage`; retire runtime Vantage HTTP calls (`intakeComments`, `checkCommentDispatch`, `confirmCommentReplies`) per [OQ-RC2](#OQ-RC2) and [OQ-RC4](#OQ-RC4); add `tillr item comment` (`--doc`, `--question`, `--body`, `--batch`, `--wait`, `show`) and the `tillr item request --question` refusal; update the operator skill (`D-RC1`, `D-RC4`, `D-RC7`).
3. **Comment track priority, workflow pin, coalescing, and capacity:** FIFO comment-batch ordering ahead of `rewrite-item-*` and express/ordinary items without writing rank strings, pinning `item.IsCommentItem` to `single-worker@v1` ahead of `[workflows.default]`, prose-rewrite and triage bypass, `in-queue` batch coalescing, and [OQ-RC1](#OQ-RC1) express-borrowing capacity (`D-RC2`, `D-RC3`, `D-RC4`; out-of-pass settlement and `--scope-to-change` Markdown gate selection from `D-RC5` are already in the tree).
4. **Canonical reply receipts and the recorded-versus-built reply check:** store source-tagged reply receipts after the save check, invoke `.vantage/inbox/` only for `source = "vantage"`, stage the open-question and held-item inventory into the comment brief, and surface receipts on `tillr item comment --wait`, `tillr item comment show`, and the Observatory (`D-RC6`).

---

## 6. Icebox: asking Vantage for pending comments

tillr does not fetch comments from a running Vantage server ([OQ-RC2](#OQ-RC2), [OQ-RC4](#OQ-RC4)). With no poller there is no quiet window to tune and no tracked server intake to enable. All three belong in the [roadmap icebox](../tillr/roadmap.md#icebox), not the build outline. The conditional poller the tree has today is documented in [designer sessions](../reference/tillr-designer-sessions.md#comment-intake-and-batch-items).

---

## 7. Decision Ledger

The owner's direction of 2026-11-19 (*"there should be a standing rule, and perhaps even a special track for, vantage comments. we 'work well with vantage' but we don't require it."*) and the [`OQ-RC1`](#OQ-RC1) to [`OQ-RC4`](#OQ-RC4) rows below record the owner's dated words; where the operator or designer interpreted a remark ([`OQ-RC2`](#OQ-RC2) to [`OQ-RC4`](#OQ-RC4)), the row names that reading explicitly. `D-RC1` to `D-RC8` are engineering decisions made by the designer, and the owner may overturn any of them. The design also rests on the owner's rulings [OQ-VO4](./tillr-vocabulary.md#OQ-VO4), [OQ-VO5](./tillr-vocabulary.md#OQ-VO5), `R-VO2` ([the vocabulary](./tillr-vocabulary.md#51-the-work-and-its-statement)), [OQ-EL1](./tillr-early-design-landing.md#OQ-EL1), [`R-DT1`](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling), [OQ-PR8](../tillr/decision-provenance-review.md#OQ-PR8), [OQ-PR12](../tillr/decision-provenance-review.md#OQ-PR12), [OQ-PR14](../tillr/decision-provenance-review.md#OQ-PR14), [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21), and [OQ-PR22](../tillr/decision-provenance-review.md#OQ-PR22).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-RC1"></a>[`OQ-RC1`](#OQ-RC1) | **Owner ruling: option A.** A comment desk borrows idle express capacity first and overflows past it: *"Option A — keeps lanes.express meaningful and visible on the board while guaranteeing comments never wait behind slow express ideas or requests."* | 2026-11-20 | [Capacity](#32-precedence-worktrees-and-capacity) | — |
| <a id="OQ-RC2"></a>[`OQ-RC2`](#OQ-RC2) | **Owner ruling (with the operator's reading that there is no poller to debounce):** *"I don't understand at all what this is polling for. Vantage never updates documents. It doesn't write to the repo. Other than consuming comments and deleting that one delivered file it doesn't do literally anything so I'm not even sure what you're talking about here."* | 2026-11-20 | [Icebox](#6-icebox-asking-vantage-for-pending-comments) | — |
| <a id="OQ-RC3"></a>[`OQ-RC3`](#OQ-RC3) | **Owner ruling (with the designer's reading to tolerate hand edits to an answer block and build no intake for them):** *"I don't anticipate this being something we have to do. I have never edited these answers myself. I always have agents do it. And at this point, we're agents going to be aware of Tiller. So let's protect ourselves so we don't just get broken from this. But otherwise, let's ignore this as much as possible."* | 2026-11-20 | [Supported inputs](#51-supported-inputs) | — |
| <a id="OQ-RC4"></a>[`OQ-RC4`](#OQ-RC4) | **Owner ruling (with the operator's reading that server comment polling goes to the icebox and tracked settings need no server intake):** *"Not sure what you're talking about here. You talking about we could go ping Vantage and reach out for comments. I don't want to do that yet. It's an interesting idea to put in the icebox, but I don't want to do that. The only Vantage integration here is purely we know about replying to Vantage design comments and putting them through their paces. And other than that, it's just that... We can provide a link from the observatory with the base URL filled in. That's the only thing we do with that base URL. It's not even assumed to be accessible from inside the jail where Tiller is operating."* | 2026-11-20 | [Vantage seam](#52-one-named-seam-reviewadapter-toolstillrvantage), [Icebox](#6-icebox-asking-vantage-for-pending-comments) | — |
| D-RC1 | **Designer's decision; the owner may overturn this.** Only a `design`-seat run inside the drain edits a design document; an operator relaying an owner comment or question ruling files `tillr item comment` within seconds with no exploration first, and never `tillr item request`, which refuses `--question`. | 2026-11-19 | [Section 2.1](#21-who-acts-and-what-the-operator-never-does) | — |
| D-RC2 | **Designer's decision; the owner may overturn this.** Comment items order after trunk and tooling repairs and ahead of rewrite items and all express and ordinary items, first-in, first-out by batch, without writing rank strings. | 2026-11-19 | [Section 3.2](#32-precedence-worktrees-and-capacity) | — |
| D-RC3 | **Designer's decision; the owner may overturn this.** A comment item is pinned to `single-worker@v1` ahead of `[workflows.default]`, and bypasses the prose rewrite and triage. | 2026-11-19 | [Section 3.3](#33-seat-workflow-and-session-reuse) | — |
| D-RC4 | **Designer's decision; the owner may overturn this.** A multi-document intake splits into one item per target document in one transaction; a new comment for a document appends to an undispatched batch and opens the next item once the earlier one has dispatched. | 2026-11-19 | [Section 3.4](#34-how-a-multi-document-batch-is-split-and-coalesced) | — |
| D-RC5 | **Designer's decision; the owner may overturn this.** A Markdown-only comment run settles on exit without waiting for unrelated runs and executes only gate checks that apply to Markdown documents. | 2026-11-19 | [Section 2.2](#22-exact-order-of-events-from-comment-arrival-to-reply) | Built (`examine_coordinator.go`, `change_gate.go`); retiring `checkCommentDispatch` HTTP wait is Slice 2 |
| D-RC6 | **Designer's decision; the owner may overturn this.** Reply delivery waits until the landing is recorded and `<root>/<doc>` matches the landed blob, or the run was a zero-diff answer; it writes a canonical store receipt, and every reply summary states both the saved document changes and whether implied code work is document-only, queued and not built, or held on open questions. | 2026-11-19 | [Section 4](#4-comment-response-delivery-and-what-a-reply-says-about-unbuilt-work) | — |
| D-RC7 | **Designer's decision; the owner may overturn this.** Only inbox reply delivery and Observatory link formatting sit behind `ReviewAdapter` in `tools/tillr/vantage`; question and stage parsing moves to the shared Go package `tools/tillr/docquestions` across `tillr-item`, `tillr-drain`, and `tillr-view`, and `[comments]` requires no Vantage URL. | 2026-11-19, amended 2026-11-27 | [Section 5.2](#52-one-named-seam-reviewadapter-toolstillrvantage) | — |
| D-RC8 | **Designer's decision; the owner may overturn this.** A single-document `design`-seat run in a `git-landing` store (`row.DesignDoc` or a single `.md` entry in `row.Allowed`, including mirrored `item-design-<slug>` rows and fenceless single-document `design` requests) always stages at `desk-<doc>`, with or without `[comments]`, so a warm transcript exists there before the first comment arrives. | 2026-11-19, amended 2026-11-27 | [Section 3.2](#32-precedence-worktrees-and-capacity) | — |

---

## 8. Open Questions

None. [OQ-RC1](#OQ-RC1) to [OQ-RC4](#OQ-RC4) are compacted into the [Decision Ledger](#7-decision-ledger).
