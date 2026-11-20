---
title: "Designer Sessions and Review Comments"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: 47047cacf11667b20746f01591f5204866f995f3
covers:
  - tools/tillr/settings/settings.go
  - tools/tillr-item/comments.go
  - tools/tillr-item/item/comments.go
  - tools/tillr-item/item/comment_intake.go
  - tools/tillr-item/docshare/docshare.go
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

**Status:** Current reference · Verified 2026-11-20 against `47047cacf11667b20746f01591f5204866f995f3`. UNMEASURED: the owner's live two-comment run on a tracked design document and any comparison beyond one paired comment of whether a resumed session answers better than a cold one.

**Reads with:** [observatory-redesign.md](./observatory-redesign.md) (the linked-page observatory, waiting classes, and dollar-stripped usage display), [observatory-actions.md](./observatory-actions.md) (idea intake and queue placement), [tillr-item-states.md](./tillr-item-states.md) (item lifecycle states and `closed` terminal evidence), [tillr-review-comments.md](../design/tillr-review-comments.md) (the proposed Vantage-free comment command and fast-track extensions), and [decision-provenance-review.md](../tillr/decision-provenance-review.md) ([`OQ-PR12`](../tillr/decision-provenance-review.md#OQ-PR12), the 2026-11-20 owner ruling confirming `D-CD1`–`D-CD11`).

---

## Overview

Review comments left in Vantage on a tracked design document become generated `design`-seat items (`item-comments-<doc>-<N>`) that run at a dedicated lane directory for that document — its **desk** (`desk-<doc>`) — and resume the harness session that most recently worked on that document (`last_designers`). No agent process stays alive between turns: the session persists as its harness transcript file inside the desk's durable directory while the desk's git worktree is created per run and removed after settlement.

```text
Vantage review API ──► intakeComments / `tillr item comments take`
                             │
                             ▼
                  item-comments-<doc>-<N>  (holds unstarted <doc> items)
                             │
                             ▼
                   chooseDesigner at desk-<doc>
          ┌──────────────────┼──────────────────┐
          ▼                  ▼                  ▼
     new session       resumed-warm     resumed-compacted /
   (cold / fallback)   (inside TTL)      resumed-expired
          └──────────────────┼──────────────────┘
                             ▼
            gate + squash land (or zero-commit answer)
                             │
                             ▼
       served-blob check ──► .vantage/inbox/<item_id>.jsonl
```

<a id="the-words-this-design-uses"></a>

## Terms and ownership

- **Last designer (`last_designers` in `tools/tillr-drain/drain/comments.go`):** The single recorded harness session per document path that most recently completed a run at that document's desk (including a failed initial design run). Stored as JSON in `last_designers(document, record)` with `session_id`, `backend`, `model` (policy route), `desk`, `run_id`, `last_request`, `context_tokens`, `cache_lifetime_seconds`, `compacted`, `blob_id`, and `transcript`.
- **Desk (`desk-<doc>`):** A lane outside the numbered and express pools dedicated to one document (`commentDesk` in `tools/tillr-drain/drain/comments.go`). Its parent directory `<lane-root>/desk-<doc>/` and `<lane-root>/desk-<doc>/durable/` persist across runs so harness session lookup by ID finds the transcript at the same working-directory path (`D-CD3`), while `<lane-root>/desk-<doc>/worktree` exists only during an active or retained run.
- **Comment batch (`comment_batches` in `tools/tillr-item/item/comments.go`):** One sitting of pending Vantage review comments for a single document, captured atomically with its `item-comments-<doc>-<N>` item and de-duplicated by `comment_taken(document, comment_id, turns, changed)`.

| Package / binary | Responsibility |
| :--- | :--- |
| `tools/tillr/settings` | Parses `[comments]` in `.tillr.toml` (`vantage_url`, `repo`, `paths`, `quiet_seconds`, `poll_seconds`, `max_running`, `compact_over_tokens`, `refresh_hours`) |
| `tools/tillr-item` (`comments.go`, `item/comments.go`, `item/comment_intake.go`, `docshare`) | Reads Vantage's `/api/r/<repo>/review` endpoint, filters quiet/unanswered comments, computes the document + companion-plan + linking-roadmap fence, and writes `comment_batches`, `comment_taken`, and `item-comments-<doc>-<N>` |
| `tools/tillr-drain` (`comments.go`, `comment_intake.go`, `comment_observatory.go`, `designer.go`, `designer_compaction.go`, `plan.go`, `loop.go`) | Polls Vantage, holds dependent items (`design-under-comment`), routes desks, selects/compacts/records designer sessions, verifies `replies.jsonl`, delivers `.vantage/inbox/<item_id>.jsonl` after landing, and confirms delivery |
| `tools/tillr-run/backends/pi/compact` | Runs `pi --mode rpc --session <transcript>` to compact an expired Pi session in place and reports before/after token counts and compaction spend |
| `tools/tillr-view` (`designers.go`, `comment_intake.go`, `Designers.tsx`, `DocumentPane.tsx`) | Serves `/api/v1/designs`, attaches `last_designer` to document payloads and `comments` to item payloads, surfaces comment intake/delivery waits, and strips dollar fields before HTTP encoding |

---

## Comment intake and batch items

Comments enter the store either on demand through `tillr item comments take` (`tools/tillr-item/comments.go`) or automatically during `Loop.intakeComments` (`tools/tillr-drain/drain/comment_intake.go`) when `.tillr.toml` declares `[comments]`.

### Pending and quiet-window filtering

- **Public review read (`item.ReadReview` in `tools/tillr-item/item/comments.go`):** Calls `GET <vantage_url>/api/r/<repo>/review?path=<doc>` with a 15-second HTTP timeout; Vantage's private database files are never read directly.
- **Pending thread (`item.PendingReview`):** A comment is pending when `resolved` is false, `status` is not `"draft"` or `"orphaned"`, `comment` is non-empty, and the latest reaction (excluding `actor: "agent", kind: "noted"` acknowledgments) is not from `"agent"`.
- **First-intake baseline (`comment_intake`):** On the first automatic poll (or the first hand intake when `[comments]` is configured), `first_intake` is stamped once in `comment_intake(id=1)`. Automatic polling ignores comments whose latest reviewer change (`ReviewChanged`) predates `first_intake`, recording their count in `comment_intake_documents(document, older)` so the observatory surfaces them without flooding old threads. Passing `--older` to `tillr item comments take` includes pre-baseline comments.
- **Quiet sitting (`item.QuietComments` in `tools/tillr-item/item/comment_intake.go`):** When automatic polling runs (or when hand intake runs in a repository with `[comments]` configured), **all** comments on the document are inspected together (`D-CD9`):
  - If any unresolved comment has `status == "draft"` (an open comment box in Vantage), **nothing** is taken for that document.
  - Otherwise, if the most recent reviewer activity across pending eligible comments occurred fewer than `comments.quiet_seconds` (default `60`) ago, nothing is taken yet.
  - When `.tillr.toml` has no `[comments]` table, `tillr item comments take --doc <path> --vantage <url>` skips the quiet timer and takes pending non-draft comments immediately.
- **Outage atomicity:** `Loop.intakeComments` fetches `/api/r/<repo>/review` for every non-symlink `.md` file under `comments.paths` (verifying `EvalSymlinks` stays local to the repository) before opening its write transaction. If any fetch fails, no batch is written and `comment_intake.unavailable` is set to `1`.

### Generated item shape and holds

`item.TakeComments` (`tools/tillr-item/item/comments.go`) runs inside one SQLite write transaction:

- **One open batch per document:** If any item in `comment_batches` for `doc` is currently in `in-queue`, `in-progress`, or `blocked`, `TakeComments` returns `""` without creating a second batch; remaining comments wait for the next pass after the active batch settles. Already-taken `(document, comment_id, turns, changed)` tuples in `comment_taken` are skipped.
- **Item identity and rank:** Allocates `item-comments-<CommentDocumentName(doc)>-<N>` (`N` starting at `1`; `CommentDocumentName` strips `docs/design/` and `.md`, slugifying path punctuation and appending an 8-hex SHA-256 suffix when non-slug characters were normalized). Places the item ahead of the lowest active rank by prepending `"0m"` (or `"m"` when the queue is empty), sets `seat = "design"`, `design_doc = doc`, and `state = "in-queue"`.
- **Fence (`docshare.CommentFence` in `tools/tillr-item/docshare/docshare.go`):** Includes `doc`, its existing companion `<stem>-plan.md` when present on disk, and every tracked roadmap file under `sync.roadmaps` (default `docs/`) that contains a Markdown link resolving to `doc`.
- **Criteria:** Two mechanical criteria: `c1` requires revising `doc` (or leaving zero commits when already answered) and writing one JSON reply per comment to `<durable>/replies.jsonl`; `c2` requires `uvx vantage-check <doc>` to exit `0` when `doc` still exists.
- **Planner priority and `design-under-comment` hold (`tools/tillr-drain/drain/plan.go`):** `readOrderedItems` places `item-comments-*` in priority bucket `2` (behind `rewrite-item-*` in bucket `1`, ahead of express items in bucket `3` and ordinary items in bucket `4`). While any `item-comments-*` item for `doc` is open (`in-queue`, `in-progress`, or `blocked`), unstarted items whose `design_doc` equals `doc` or whose `allowed_paths` include `doc` hold with `reason: "design-under-comment"` (`D-CD11`); runs already in flight finish undisturbed.
- **Pre-dispatch check (`Loop.checkCommentDispatch` in `tools/tillr-drain/drain/comment_intake.go`):** Immediately before claiming a comment item (when a Vantage URL is configured), the loop re-reads `/api/r/<repo>/review`. If Vantage fails to answer, the item is not claimed (`ReasonVantageUnavailable = "Vantage not answering"`). If every comment in the batch has been deleted or marked resolved in Vantage since intake, the loop closes the item via `item.Close` (`"review comments all resolved or deleted before dispatch"`) and replans in the same pass.

---

## The desk

Every comment item, and every eligible single-document `design`-seat item, runs at its document's desk (`desk-<CommentDocumentName(doc)>`) rather than in a numbered `lane-N` or `express-N` slot (`D-CD3`).

- **Desk routing (`deskDocument` in `tools/tillr-drain/drain/comments.go`):**
  - Any `item-comments-*` item routes to its batch document's desk even when `[comments]` is absent from `.tillr.toml` (hand intake).
  - Any other `design`-seat item routes to a document desk only when `[comments]` is configured and the item's `allowed_paths` contain **exactly one** distinct literal `.md` file path (not a directory prefix, not zero, and not two designs) inside `comments.paths`. Multi-document or directory-fenced design runs use ordinary or express pool lanes and record no `last_designers` row.
- **Concurrency and holds (`countDocumentDesks` in `comments.go`, `plan.go`, `holdrules/holdrules.go`):**
  - Only one item can occupy a given `desk-<doc>` at a time (across live leases, unsettled candidates, and current-pass dispatches). A second item targeting the same desk holds with `waits on the document's desk` (`ReasonDocumentDeskBusy`), naming the occupying item as mover.
  - Active document desks (every `desk-*` except `desk-trunk-fixer`) are capped at `comments.max_running` (default `3` when `[comments]` is configured), independent of numbered and express lane counts. Additional desk items hold with `waits on a desk` (`ReasonDeskCapacity`).
- **Worktree lifecycle and boundary rules:**
  - `stageComment` (`comments.go`) ensures the `desk-<doc>` row exists in `lanes`, assigns `spec.SessionID`, and records `comment_runs` and `comment_arrivals`.
  - Staging checks out `<lane-root>/desk-<doc>/worktree` on branch `refs/heads/tillr/<scope>/desk-<doc>`.
  - On lane reset (`resetLaneTo` / `resetLaneToRegistryLocked` in `tools/tillr-drain/drain/loop.go`) and held-lane release (`held_lane.go`), `desk-*` lanes acquire `withWorktreeRegistryLock` **before** opening any SQLite writer, hard-reset and remove `<lane-root>/desk-<doc>/worktree`, and leave `<lane-root>/desk-<doc>/durable/` intact so the harness session transcript persists on disk.
  - Run-bracket boundary checks (`loopPlaces.deskBranch` and worktree checks in `tools/tillr-drain/drain/boundary.go`) excuse `<lane-root>/desk-<doc>/worktree` and `refs/heads/tillr/<scope>/desk-<doc>` within the loop's own lane scope so concurrent desk staging or teardown never convicts an unrelated lane run.

---

## Choosing at dispatch

When planning or claiming a run at a desk, `chooseDesigner` (`tools/tillr-drain/drain/designer.go`) evaluates the document's `last_designers` row in strict order (and `selectSeatDispatch` in `seat_shares.go` re-evaluates it inside the claim transaction if weighted or round-robin seat selection switches the backend):

| Condition | Session used | Recorded `DesignerReason` |
| :--- | :--- | :--- |
| No `last_designers` row exists for `doc` | New session ID | `"no-designer"` |
| Previous backend's adapter directory contains `pause-kind` equal to `cannot-resume` | New session ID | `"cannot-resume"` |
| `d.Backend != req.Backend` or `d.Model != req.Route()` | New session ID | `"seat-changed"` |
| `d.Transcript` is missing or not a regular file | New session ID | `"transcript-missing"` |
| `d.LastRequest` parses, `d.CacheLifetimeSeconds > 0`, and `at < last_request + cache_lifetime_seconds` | `d.SessionID` | `"resumed-warm"` |
| Cache is expired/unknown, `*d.ContextTokens > 40000`, and `<adapter-dir>/compact` is an executable regular file | `d.SessionID` (after compaction) | `"resumed-compacted"` |
| Otherwise (expired or unknown cache, `<= 40000` tokens or no `compact` step) | `d.SessionID` (as-is) | `"resumed-expired"` |

### Staged brief and failed-resume fallback

- **Comment brief (`LaneStager.commentTask`, `commentThreadTask` in `tools/tillr-drain/drain/comments.go`):** Embeds every batch comment (`id`, turn count `len(Reactions)` as Vantage `round`, `heading`, `captured_block` or `selected_text`, `question_id`, root comment, and reactions), followed by the four answering rules and a `What moved:` section:
  - Cold or replacement session: `"new session; read the document"`, plus `Previous session transcript (optional reading): <path>` when a prior `last_designers` row existed.
  - Resumed session with no recorded `blob_id`: `"no recorded document blob; read the document"`.
  - Resumed session whose `d.BlobID` matches `BaseSHA:<doc>`: `"unchanged"`.
  - Resumed session whose `git diff <blob_id> <BaseSHA>:<doc>` is under `400` lines: the unified diff inline; `400` lines or more: `"the document was rewritten; read it again"`.
  - When `d.Compacted` is true, appends `"Your earlier conversation was summarized."`
- **One automatic fallback on dead resume (`failedDesignerResume`, `replaceFailedDesigner` in `tools/tillr-drain/drain/designer.go`):** If a `"resumed-*"` run exits nonzero and its log stream contains **no** assistant text, thinking, data, or tool-call output (`D-CD10`), `replaceFailedDesigner` marks the failed `comment_runs` row `outcome = 'resume-failed'` and dispatches **once** in the same settlement pass with `Resume = false`, a fresh session ID, `DesignerReason = "resume-failed"`, and the prior transcript path in the brief (switching to an open quota replacement backend if the original backend's quota window closed). Any run that emitted partial assistant output or tool calls is **not** replayed automatically.

---

## The keep-warm rule

Sessions are kept warm solely by the provider's prompt-cache retention window reported on the run stream (`D-CD5`); no background keep-alive timer or synthetic cache-extension request runs.

- **Recording the session (`Loop.recordDesigner` in `tools/tillr-drain/drain/comments.go`):** After any run at a document desk settles (even if a writing run failed before creating `doc`), `recordDesigner` scans settled events in the run's JSONL log:
  - Captures `session.id` and `session.transcript` when emitted (falling back to the lane's `session-id` file and log artifact path), sets `Compacted = true` if `decision.DesignerReason == "resumed-compacted"`, `event.Type == "auto_compaction_end"`, or the same session was already marked `Compacted` in `last_designers`.
  - On each assistant `turn_end`, updates `LastRequest` from `message.timestamp`, computes `ContextTokens = input + cacheRead + cacheWrite + output`, and reads `CacheLifetimeSeconds` from `usage.cacheLifetimeSeconds` — or, when `cacheLifetimeSeconds` is absent and `cacheWrite > 0` with Anthropic's `cacheWrite1h` present, sets `3600` when `*cacheWrite1h > 0` and `300` otherwise. Absent provider measurements stay `nil` rather than defaulting to an assumed TTL.
  - Records `BlobID` from `git ls-tree --format=%(objectname) HEAD -- :(literal)<doc>` in the lane target and upserts `last_designers(document, record)`.
- **Retirement (`Loop.retireDesigners` in `tools/tillr-drain/drain/designer.go`):** At the start of each pass, `retireDesigners` deletes a document's `last_designers` row on two grounds (`D-CD8`):
  1. **Deleted document:** `<root>/<doc>` does not exist on disk **and** `git ls-tree --name-only <syncedSHA> -- <doc>` is empty on the trunk commit pinned for the pass (preventing a concurrent `git merge --squash` unlink-and-rewrite window or an uncommitted file from erasing a live designer row).
  2. **Completed stage:** Vantage's stage index (`Done(doc)`) maps the document's `stage` to the `done` role (for example `CURRENT`, `BUILT`, or `SUPERSEDED` per `.vantage.toml`, unless a new comment item re-enters at its desk).
  Retirement removes only the `last_designers` SQLite row; harness retention owns transcript files on disk.

> [!NOTE]
> **Parsed settings not wired into `tillr-drain` at `8e47153ab`:** `settings.Read` (`tools/tillr/settings/settings.go`) parses and validates `comments.compact_over_tokens` (default `40000`) and `comments.refresh_hours` (default `0`), and `Settings.StartupLine()` prints both. However, `chooseDesigner` in `tools/tillr-drain/drain/designer.go` hardcodes `*d.ContextTokens > 40000` without reading `Comments.CompactOverTokens`, and nothing in `tools/tillr-drain/` reads `Comments.RefreshHours` or sends cache-refresh requests (`D-CD5`).

### Compaction and crash recovery

Compaction runs lazily at dispatch (`Loop.compactDesigner` and `ensureDesignerCompaction` in `tools/tillr-drain/drain/designer_compaction.go`) only when `chooseDesigner` returned `"resumed-compacted"` (`D-CD6`, `D-CD7`):

1. **Exclusive journal:** Keyed by `<lane-root>/compactions/<session-id>/<checkpoint>.json`, where `<checkpoint>` is the previous `last_designers.RunID` (sanitized to filename-safe bytes). Before invoking `<adapter-dir>/compact`, `ensureDesignerCompaction` creates this file with `O_CREATE|O_EXCL` recording `session_id`, `checkpoint`, `transcript`, `offset` (current byte size of `transcript`), `run_id`, and `item_id`.
2. **Adapter execution (`tools/tillr-run/backends/pi/compact`):** Verifies the session header in `--transcript`, snapshots total usage across assistant entries, rewrites `--journal` atomically with `started: true` and the exact pre-compaction byte `offset`, spawns `pi --mode rpc --session <transcript>` (passing `--provider` and `--model` when `--route` is set) with `{"id":"tillr-compact","type":"compact"}` under `--timeout-ms` (default `300000` ms), reads the newly appended `compaction` entry (`tokensBefore`), measures post-compaction summary tokens and added assistant usage (`tokens`, `cost_usd`), and prints the JSON receipt on stdout.
3. **Receipt persistence:** `ensureDesignerCompaction` writes the receipt into both the journal and `<run-dir>/compaction.json`, and `compactDesigner` appends a `drain.designer-compacted` event (`object_id = journal.RunID`) carrying `item_id`, `session_id`, `tokens_before`, `tokens_after`, `tokens`, and `cost_usd`.
4. **Crash reconciliation (`reconcileCompactionJournal`):** If a prior loop crashed mid-compaction, the next dispatch or startup pass (`reconcilePendingDesignerCompactions`) inspects the existing journal:
   - If the journal already carries `receipt`, or `<run-dir>/compaction.json` exists, it reuses that receipt without calling the model again.
   - If the transcript grew past `journal.Offset` and contains a `type == "compaction"` entry after that offset, it reconstructs the receipt from the transcript tail without re-running `compact`.
   - If `journal.Started` is false and the transcript size still equals `journal.Offset`, the adapter never started `compact`, so the stale journal is removed and compaction runs cleanly.
   - Otherwise completion is ambiguous, and `reconcileCompactionJournal` returns an error rather than paying for a second compaction.

<a id="what-a-turn-costs-measured"></a>

### What a turn costs, measured

The 40,000-token compaction threshold and warm-versus-cold policy come from direct measurements on 2026-11-16 on this host (`claude-opus-4-6` via Pi `0.67.1`, where `.yolo` sets `PI_CACHE_RETENTION=long`, giving a 3,600-second cache TTL; without `long`, Pi's Anthropic cache TTL is 300 seconds):

| Turn shape (35,000-token prior design conversation) | Prompt / cache tokens billed | Measured turn cost |
| :--- | :--- | :--- |
| Cold new session reading the document and neighboring files | Fresh input + cache creation (`23,000`–`56,000` tokens across three runs) | `$0.84` |
| Resumed warm session inside the 1-hour cache window | `~35,000` `cacheRead` tokens + comment turn | `$0.42` |
| Resumed expired session without compaction (`35,000` tokens) | `~35,000` `cacheWrite` tokens + comment turn | `$1.11` (`+$0.27` vs cold) |
| Expired session compacted (`$0.57`–`$0.82`) then resumed (`$0.39`–`$0.64`) | Uncached compaction read + fresh summary cache write | `$1.21` (`+$0.37` vs cold; breaks even below un-compacted resume above `~40,000` tokens) |

---

## Settlement and reply delivery

When a comment run exits cleanly (`RESULT: done`), settlement verifies `<durable>/replies.jsonl` (`readCommentReplies` in `tools/tillr-drain/drain/comments.go`) before either closing or landing the item:

- **Strict 1-to-1 reply validation (`readCommentReplies`):** Every non-empty line in `replies.jsonl` must be valid JSON containing a non-null integer `round`, an `id` matching a comment in the batch, `round == len(comment.Reactions)` at intake, and a non-empty `summary`. Missing, duplicate, foreign, or wrong-round replies record a mechanical `not_done` verdict on `c1` (`actor: "drain/comment-replies"`) and refuse the candidate (`rejectCommentReplies`). Each valid reply receives `path = batch.Document`, `root_text_version = comment.RootTextVersion`, and deterministic `nonce = "tillr-" + sha256(item_id + "\x00" + comment_id)`.
- **Zero-commit answer (`answerCommentWithoutCommit`):** When the designer made no commit (`HeadSHA == BaseSHA`), the loop checks `git status --porcelain` is clean (refusing as `RefusalFence` if dirty), validates `replies.jsonl`, marks `comment_runs.outcome = 'answered'`, delivers replies against `BaseSHA:<doc>`, and closes the item via `item.Close` (`"answered review comments without a revision"`).
- **Committed revision (`deliverCommentReplies`, `writeCommentDelivery`):** When the designer committed a revision, the candidate passes through the standard fence check, repo gate (`c2` runs `uvx vantage-check <doc>`), and `tillr-land` squash merge. Only **after** `landings` records `result = 'landed'` for `(item_id, run_id)` (`D-CD4`) does `deliverCommentReplies` store the immutable JSONL payload and landed `blob_id` in `comment_deliveries(run_id, item_id, document, blob_id, body, written=0)`.
- **Served-checkout guard and atomic inbox drop (`writeCommentDelivery`, `flushCommentDeliveries`):** Before writing `.vantage/inbox/<item_id>.jsonl`, `writeCommentDelivery` compares the bytes of `<root>/<doc>` on disk against `git cat-file blob <blob_id>`. If `<root>/<doc>` is missing or holds unsaved local edits that do not match `blob_id`, delivery waits (`written` stays `0`) and `flushCommentDeliveries` retries from `comment_deliveries` on subsequent passes without needing the removed desk worktree. Once the served file matches `blob_id`, the loop writes `.vantage/inbox/.tillr-*.writing`, `fsync`s file and directory, renames it to `.vantage/inbox/<item_id>.jsonl`, and sets `comment_deliveries.written = 1` and `comment_runs.delivered = 1`.
- **Post-inbox confirmation (`Loop.confirmCommentReplies` in `tools/tillr-drain/drain/comment_observatory.go`):** Once `.vantage/inbox/<item_id>.jsonl` has been consumed (unlinked by Vantage) and `VantageURL` + `VantageRepo` are configured, the loop queries `/api/r/<repo>/review` and records per-comment state in `comment_reply_states(run_id, comment_id, state)` (emitting `drain.comment-reply-state` events): `"delivered"` when an agent reaction with `kind != "noted"` has `answers_round == reply.Round`, `"dropped"` when the comment ID is gone from Vantage, and `"not confirmed"` otherwise.

---

## Observatory surfaces

`tillr-view` exposes designer sessions and comment batches across four read-only surfaces (`tools/tillr-view/designers.go`, `comment_intake.go`, `serve.go`, `priceless.go`, `Designers.tsx`, `DocumentPane.tsx`):

- **`GET /api/v1/designs` (`DesignsPage` at `#/designs` and `#/design/<path>`):** Lists every document path present in `last_designers`, `comment_batches`, or `items.design_doc` (optionally filtered by `?path=`), alongside its `last_designer` view and aggregate `arrivals` counts from `comment_arrivals` (`within_lifetime`, `after_lifetime`, `no_session`, `unmeasured`).
- **Document pane (`GET /api/v1/items/{id}/doc`, `DesignerCard` in `Designers.tsx` and `DocumentPane.tsx`):** Attaches `last_designer` to the item's design document payload. `DesignerCard` displays `session_id`, derived state (`"warm"` with `warm until <timestamp>` while `now < last_request + cache_lifetime_seconds`, `"compacted"` when `compacted` is true, or `"expired"`), `last_request`, `context_tokens`, and links to each comment batch (`#/item/<item_id>`) and run (`#/run/<run_id>`) with its dispatch `reason`.
- **Comment item detail (`GET /api/v1/items/{id}`, `readCommentItem`):** Attaches `data.comments` (`document`, `runs`, `comments` with per-reply confirmation state `"waiting on the served checkout"`, `"written"`, `"delivered"`, `"dropped"`, or `"not confirmed"`, and `confirmation_note` when no Vantage server is configured).
- **Waiting rows (`waitingCommentIntake` in `tools/tillr-view/comment_intake.go`):** Appends store-backed waiting rows when `comment_deliveries` has `written = 0` (`"reply delivery waits on served document <doc>"`), when `comment_intake.unavailable` is `1` (`"Vantage not answering; last answer: <stamp>"`), and when `comment_intake_documents.older > 0` (`"<N> pending comments predate first intake"`, naming `tillr item comments take --doc "<doc>" --older` as the mover).
- **Dollar-free HTTP output (`withoutPrices` in `tools/tillr-view/priceless.go`):** Although `designerView` and `designerBatch` scan `usage_cost_usd` internally, `serve.go` runs `withoutPrices` over every `/api/v1/` payload before serialization, stripping `cost_usd` and `missing_costs` in accordance with [observatory-redesign.md](./observatory-redesign.md).

---

## Current values

Verified at `47047cacf11667b20746f01591f5204866f995f3`. The prose above explains how these settings, timeouts, and thresholds behave; this table is the single place their literal values are stated.

| Value | Setting / literal | Defined in |
| :--- | :--- | :--- |
| Automatic quiet window | `comments.quiet_seconds` = `60` s | `Read` in `tools/tillr/settings/settings.go` |
| Automatic Vantage poll interval | `comments.poll_seconds` = `15` s | `Read` in `tools/tillr/settings/settings.go`; `intakeComments` in `tools/tillr-drain/drain/comment_intake.go` |
| Concurrent active document desks | `comments.max_running` = `3` | `Read` in `tools/tillr/settings/settings.go`; `plan.go` |
| Compaction token threshold (enforced) | `*d.ContextTokens > 40000` | `chooseDesigner` in `tools/tillr-drain/drain/designer.go` (`comments.compact_over_tokens` default `40000` in `settings.go` is not read by `drain`) |
| Cache refresh setting (unwired) | `comments.refresh_hours` = `0` | `Read` in `tools/tillr/settings/settings.go` (parsed only; no caller in `tillr-drain`) |
| Vantage review HTTP timeout | `15s` | `ReadReview` in `tools/tillr-item/item/comments.go` |
| Inline document diff ceiling in brief | `< 400` newline characters | `commentTask` in `tools/tillr-drain/drain/comments.go` |
| Pi RPC compaction timeout | `300000` ms (`5` min default `--timeout-ms`) | `tools/tillr-run/backends/pi/compact` |
| Anthropic fallback cache TTLs | `3600` s (`cacheWrite1h > 0`), `300` s (`cacheWrite1h == 0`) | `recordDesigner` in `tools/tillr-drain/drain/comments.go` |
| Desk lane prefix and exclusion | `"desk-"` (excluding `"desk-trunk-fixer"`) | `commentDesk`, `documentDesk` in `tools/tillr-drain/drain/comments.go` |
| Comment item ID prefix | `"item-comments-<doc>-<N>"` | `TakeComments` in `tools/tillr-item/item/comments.go`; `IsCommentItem` in `docshare/docshare.go` |
| Planner priority bucket for comments | Bucket `2` (after `rewrite-item-*` `1`, before express `3`) | `readOrderedItems` in `tools/tillr-drain/drain/plan.go` |
| Hold reasons | `"design-under-comment"`, `"waits on the document's desk"`, `"waits on a desk"`, `"Vantage not answering"` | `tools/tillr-drain/drain/plan.go`, `tools/tillr-drain/holdrules/holdrules.go` |
| Event kinds | `drain.designer-compacted`, `drain.comment-reply-state` | `designerCompactedKind` (`designer.go`), `commentReplyStateKind` (`comment_observatory.go`) |

---

<a id="decision-ledger"></a>

## Why it's this way

Every row below (`D-CD1`–`D-CD11`) was originally drafted from the 2026-11-16 cache and compaction measurements and subsequently **confirmed by the owner on 2026-11-20** in [`OQ-PR12`](../tillr/decision-provenance-review.md#OQ-PR12) (*"A: comment work closes quickly, and building on a design you have just objected to wastes runs"*).

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| `D-CD1` | Each sitting of comments on a document becomes a new generated `design`-seat item (`item-comments-<doc>-<N>`) rather than reopening the landed design item or attaching to code-fenced build items | A comment batch has its own Markdown/plan/roadmap fence, mechanical reply and lint criteria, and squash landing (`1e0fe3259`). |
| `D-CD2` | `last_designers` tracks the session that most recently ran at the document's desk rather than pinning the session that originally authored the file | Later sessions have read the document as revised; pinning the first author leaves its memory stale after any intervening turn. |
| `D-CD3` | Every design or comment session for a document runs in one fixed desk directory (`<lane-root>/desk-<doc>/`) instead of resuming in whichever pool lane is free | Harness session lookup keys transcripts by working directory, and transcripts hold paths into that desk's durable directory (`4b16f3af8`). |
| `D-CD4` | The drain delivers `.vantage/inbox/<item_id>.jsonl` only after landing (and only once the served checkout matches the landed blob), never from inside the agent turn | Vantage renders diffs from the served checkout, and delivering a reply before the revision lands shows stale prose or leaks replies from gate-refused runs. |
| `D-CD5` | A session is warm solely for the cache TTL reported on its stream (`3600` s with `PI_CACHE_RETENTION=long`), with no background keep-alive requests | Extending cache lifetime via synthetic pings only pays above roughly one comment per 50 extra hours, which is unmeasured. |
| `D-CD6` | Compaction runs lazily at the next cold dispatch when context exceeds `40,000` tokens, never on a timer when the cache expires | Compaction after expiry reads uncached tokens either way; running it on expiry wastes model spend on sessions that never receive another comment (`081eb89f3`). |
| `D-CD7` | An expired session is resumed (compacted above `40,000` tokens, as-is at or below `40,000`) rather than replaced with a cold session | Starting cold saves `$0.27`–`$0.37` on the first expired comment (`$0.84` cold vs `$1.11` un-compacted or `$1.21` compacted-then-resumed), but loses unrecorded trade-offs and owner remarks carried in the session summary. |
| `D-CD8` | One `last_designers` row is kept per active design document until the document is deleted from trunk and working tree or reaches a `done` stage | A transcript is a few hundred kilobytes on disk, so time- or count-based eviction discards context for negligible space. |
| `D-CD9` | Automatic intake waits `60` quiet seconds per document and takes nothing while any comment box on that document is `"draft"` | One review pass over a document batches into one turn and one squash commit instead of racing the reviewer mid-sentence (re-examined in [`OQ-RC2`](../design/tillr-review-comments.md#OQ-RC2)). |
| `D-CD10` | Non-resumable adapters, changed backend/model routes, missing transcripts, and nonzero resumes that produced zero model output all fall back once to a fresh session | Matches the drain's general session-recovery rule while preventing duplicate edits when a run failed after emitting tool calls or partial prose. |
| `D-CD11` | An open comment item holds unstarted items targeting its document (`design-under-comment`) without killing runs already in flight | A review comment signals the design may change; holding unstarted items avoids wasted build runs without throwing away nearly finished work ([`OQ-PR12`](../tillr/decision-provenance-review.md#OQ-PR12)). |
