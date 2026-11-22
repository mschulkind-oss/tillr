---
title: "Contract: tillr-item"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, contract, item]
summary: "What tillr-item provides and expects, read from the code at dab5a2da0e: nineteen faces, eleven record kinds, eleven event kinds, three packages, fourteen tables, and the names of states, verdicts, claim classes and rejection rules. The drain links 79 names of its `item` package and also writes four of its tables with its own SQL. It expects rows of the run, land and drain utilities."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-item

**Status:** 2026-11-21. Read at `dab5a2da0e`; nothing here is built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Go program at `tools/tillr-item` with three packages: `item`, `docshare` and `workflowmeta`. It holds items, their criteria and verdicts, conflicts and waits, comments, ideas, decisions and mail.

## 1. What it provides

### 1.1 Faces

`apply`, `rank`, `dep`, `sync`, `reconcile`, `comments`, `list`, `show`, `state`, `place`, `idea`, `doc-triage`, `request`, `close`, `reopen`, `decision`, `mail`, `verdict`, `judge`.

Those another program runs:

| Face | Run by | Flags they pass | What they read back |
| :--- | :--- | :--- | :--- |
| `state` | drain | `--store --item --state --now`, and for a block `--waiter --reason`; also `--require-verdicts` | `item.record`; on exit 1 an `item.rejection` and its `rule` |
| `sync` | drain | `--store --root --now --json`, and `--roadmap` | `item.sync`. Exit 0 clean, 1 ran with findings, 2 usage, 3 store, 4 Git |
| `show` | `tillr-loop` | `--store --item --root` | `item.record` with each criterion's `criterion_id text claim_class` |
| `verdict record` | `tillr-loop` | `--store --input --now` | `item.verdict` |
| `place` | view | `--store --item`, one of `--to --before --after`, `--by --via` | `item.place`, or an `item.rejection` |
| `idea` | view | `--store --root`, a placement flag, `--by --via`, and the words on standard input | `item.idea`, or an `item.rejection` |

Exit codes are the library's six: 1 means a rejection record was printed, 2 means nothing was written. `judge` adds 10 for defective, 11 for blocked and 12 for not-done. A face that made a record prints it whatever its exit code.

### 1.2 Go packages

| Package | What it is for | Linked by |
| :--- | :--- | :--- |
| `item` | The tables, kinds, and the one mapping from a record to rows | drain (79 names in production, about a dozen more in tests); `tillr-store` for no name |
| `docshare` | Compares the item declarations in documents with stored states, without registering a schema | drain: `Attributes Fence ReadQueue Start` |
| `workflowmeta` | Reads a workflow selection without registering a schema | view: `ReadSelection` |

The 79 names of `item` the drain uses, by what they do:

- **State changes:** `SetState SetStateWithReceipt MoveReceipt Close Apply FinishExistingLandingAction LandedBlocker CurrentLanding LandingReference InOrder`.
- **Kinds:** `RecordKind AddKind UpdateKind ConflictKind VerdictRecordKind VerdictsWithdrawnKind WaitReleasedKind`.
- **Verdicts and judging:** `VerdictDone VerdictNotDone VerdictBlocked VerdictDefective RecordVerdict RecordJudge CheckJudge JudgeOptions JudgeResult JudgeEntry JudgeOutput EvaluatorBinding ReadEvaluatorBinding ReadWorkflowSelection CriterionHash WithdrawVerdicts ActorMechanical ClaimVerification ClaimRedGateTriage ClaimDirection Rejectionf RejectionOf ExitCode`.
- **Conflicts and waits:** `Conflict ReadConflicts RecordConflict WaitingOn ReadWaitingOn OwnerDecision OwnerQuestion OwnerQuestionID DecidedByOwner`.
- **Comments:** `IsCommentItem CommentBatch CommentDocumentName ReadCommentBatch ReadReview ReviewComment ReviewChanged PendingReview QuietComments TakeComments`.
- **Sync and documents:** `Sync SyncInput SyncDocument SyncNote ReadSyncDocuments DefinitionBlockExists DefinitionBlockIDs DefinitionBody DefinitionBodyContent BlocksFrom RetireItemBlock ProseRewriteFindings ProseRewriteOriginal ValidateProseRewrite`.
- **Seats and workflows:** `WorkflowRefValid ValidSeatName SeatBuild ForkCheckoutPath`.
- **Stored reads:** `ReadStored RequestWords`.

### 1.3 Records and event kinds

Records printed and never appended: `item.record`, `item.rejection`, `item.list`, `item.sync`, `item.place`, `item.idea`, `item.request`, `item.doc-triage`, `mail.delivery`. `decision.record` and `item.verdict` are both a record and an event.

| Event kind | Payload fields |
| :--- | :--- |
| `item.add`, `item.update` | `item_id title body_ref state waiting_on underlying_state close_reason closed_at closed_by closed_from lifecycle_reason lifecycle_by resolves_via criteria blocked_by design_doc allowed_paths rank place seat prose workflow selected_workflow workflow_source workflow_args judge_mode express updated_at event_seq`; on some updates `placed_by placed_from lifecycle_action`. A criterion is `criterion_id text state claim_class`. A wait is `waiter reason source` |
| `item.verdict` | `workflow evaluator_node graph_sha256 evaluated_tree item_id criterion_id criterion_hash verdict claim_class reason landed evidence evaluated_head actor_kind actor judged_actor grant_id recorded_at` |
| `item.conflict` | `conflict_id item_id run_id body_ref text blocked_criteria waiting_on status design_run_id question_removed_head answer_head question_ref question_head question_run_id triage_version` |
| `item.verdicts-withdrawn` | `item_id criteria reason by` |
| `item.wait-released` | `blocker run_id reason` |
| `decision.record`, `decision.revoke`, `mail.send`, `mail.ack`, `item.grant` | their own |

Other record payloads: `item.rejection` is `item_id rule reason`. `item.place` and `item.idea` are `item_id place count event_seq event_head_seq`. `item.sync` has 25 fields, of which the drain reads `skipped_files created_count updated_count unchanged_count frozen findings warnings unshared_open stale_doc_blocks` and the view reads `unshared_open unshared_open_ids unshared_open_oldest stale_doc_blocks stale_doc_block_ids`.

### 1.4 Tables

| Table | Read from outside by | Written from outside by |
| :--- | :--- | :--- |
| `items` | drain (more than thirty files), view, land, the library | drain: `allowed_paths`, `title` |
| `item_criteria` | drain, view | drain: `text` |
| `item_edges` | drain, view | — |
| `decisions` | drain, view | — |
| `comment_intake`, `comment_intake_documents` | view | drain |
| `comment_batches`, `design_without_item`, `item_sync_residue` | view | — |
| `comment_taken`, `request_words` | drain | — |
| `idea_intake` | drain, view | — |
| `item_requests`, `mail_deliveries` | — | — |

### 1.5 Files

| Place | Read by |
| :--- | :--- |
| `<store's directory>/intake/<id>.txt`, an idea's words before it has an item | view, drain |
| The tracked words file under the intake directory `.tillr.toml` names | view, drain |
| `docs/triage/<date>-<slug>.md`, a document-triage report | the triage workflow |

### 1.6 Names others match on

- **States.** `not-started`, `blocked`, `in-queue`, `landed`, `resolved-elsewhere`, `closed`. The definition says which are ready, blocked and finished.
- **Verdicts.** `done`, `not-done`, `blocked`, `defective`. A criterion's state is its last verdict, and empty means none.
- **Claim classes.** `verification-only`, `red-gate-triage`, `direction`, `owner-only`.
- **Waiters.** `owner`, `design`, `plan`, and `item:<id>`. A wait's source is `state` or `conflict:<id>`.
- **Conflict statuses.** `raised`, `awaiting`, `owner`, `question`, `answered`, `closed`.
- **Rejection rules read by the drain.** `criteria-unjudged`, `already-landed`, `design-doc-missing`, `blocked-use-retry`, and the judge's ten: `actor-in-output claim-class-mismatch criterion-duplicate criterion-hash criterion-missing evaluated-head evaluated-tree evaluator-binding judge-output verdict-evidence`.
- **Rejection rules read by the view.** `unknown-item`, `not-in-order`, `store-busy`, `words-empty`, `words-not-text`, `words-too-large`, `intake-not-configured`, `intake-store-failed`.
- **Rejection rule read by land's tests.** `landing-in-progress`.
- **The actor kind** `mechanical`, and that a criterion's hash is the SHA-256 of its text in lowercase hex.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, exit codes, store opener, registries, the definition, settings, the Vantage index | [tillr](./tillr.md) | Go import | throughout |
| Rows of `runs`: `run_id backend state exit_code started_at event_seq`, and which states are final | [tillr-run](./tillr-run.md) | SQL | `item/blocked_retry.go`, `item/wait_release.go` |
| Rows of `landings`: `landing_id item_id run_id result trunk_after event_seq` | [tillr-land](./tillr-land.md) | SQL | `item/current_landing.go`, `item/terminal_evidence.go` |
| Rows of `landing_claims`: `item_id run_id` | [tillr-land](./tillr-land.md) | SQL | `item/lifecycle.go` |
| `trunk_rewrites` (`old_sha new_sha`); `retries` (`item_id supersedes_run_id`); `workflow_selections` (`workflow source graph judge_graph`) | [tillr-drain](./tillr-drain.md) | SQL | `item/current_landing.go`, `item/blocked_retry.go`, `workflowmeta/read.go`, `item/evaluator.go` |
| The events `drain.revert` (`result landing item_id run_id`), `drain.graph-completed` (`item_id`), `drain.refusal` (`step`) | [tillr-drain](./tillr-drain.md) | SQL on `events` | `item/current_landing.go`, `item/terminal_evidence.go`, `item/blocked_retry.go` |
| The event `gate.verdict` on a run, as the drain appends it | [tillr-drain](./tillr-drain.md), wrapping [tillr-gate](./tillr-gate.md) | SQL on `events` | `item/blocked_retry.go` |
| The drain's retry command, its two acknowledgment words, and the adapter's time-limit exit 124, each typed here to print a remedy | [tillr-drain](./tillr-drain.md), [tillr-run](./tillr-run.md) | copies | `item/blocked_retry.go` |
| The verdict input file `tillr-loop` writes | [tillr-loop](./tillr-loop.md) | files | `verdict.go` |
| Workflow definitions under `config/tillr/workflows` | the repository's configuration | files | `request.go`, `doc_triage.go`, `sync.go` |
| The item declarations in tracked documents | people and agents | files | `docshare`, `item/sync.go` |

It imports no other utility's package and runs no other tillr program.

## 3. Contract and private

**Contract:**

- The six faces of [§1.1](#11-faces) with the flags and exit meanings shown, and `item.record`, `item.rejection`, `item.sync`, `item.place`, `item.idea` and `item.verdict` for the fields listed.
- The name sets of [§1.6](#16-names-others-match-on).
- The 79 names of `item`, the four of `docshare` and `ReadSelection`.
- Functions to add, so that the drain and the view stop typing SQL on item tables: a fence change, a title and criterion-text change (each appending an `item.update`), comment-intake writes, and readers for "who is waiting on what", for an item's edges and criteria, and for the in-order rule.
- `items`, `item_criteria` and `item_edges` for the columns read today, until those readers exist.
- `item.update`, `item.verdict`, `item.conflict` and `item.wait-released` with the fields listed.
- The idea words path, as a function.

**Private:**

- The other thirteen faces as far as programs go. People run them; their flags are documented by `--help` and are not in a contract until a program depends on them.
- The text output of `sync`, `reconcile` and `judge`'s outcome word. A program that needs `judge`'s answer reads the exit code.
- Every reason sentence. A consumer branches on `rule`.
- `item_requests`, `mail_deliveries`, `comment_taken`, `request_words` and the other comment tables, once the drain reads them through functions.
- The JSON stored in `items.waiting_on`. Readers use `ReadWaitingOn`.

## 4. How the contract is pinned

In `tools/tillr-item`, in its fast suite, in process.

- **Exists today.** `TestTheItemPackageRegistersItsOwnContract`, `TestTheItemPackageRegistersItsMaterializer`, `TestTheItemUtilityRegistersItsOwnContract`, `TestApplyEchoesTheStoredItem`, `TestShowOfAMissingItemExitsOneWithARejection`, `TestHelpIsTheFaceSurface`, and about three hundred rule-by-rule tests.
- **Name sets.** One file per set and one test comparing it with the constants. The rejection rules are found by walking every `Rejectionf` call, so a new rule without an entry fails here.
- **Examples.** One per situation another utility reads: an item in each state; a blocked item with each waiter; a conflict in each status with the wait it owns; an independent wait beside a conflict; `show` with each claim class; `sync` clean and with findings; each rejection the view and the drain branch on. Each is made by `Apply`, `SetState` or `RecordConflict` and holds the rows, the events and the record.
- **Packages.** One file uses every listed name with the drain's types.
- **Builders.** `item` publishes builders for those same situations. The view's 52 typed `items` inserts and its copied schema are replaced by them, and so is the `ALTER TABLE` in its tests.
- **Faces.** `state`, `sync`, `show`, `place` and `idea` are called in process for each exit code a caller branches on. The flag list is a file compared with `--help`, which the drain reads in place of searching help text.

The blocked-wait failure would have gone differently with these: the change to what a stored wait means would have changed the "blocked with a legacy wait" example, in the item utility's suite.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As provider: RI-09 to RI-12 (the view), RI-22 (the drain writes its tables), RI-23 (the drain reads its tables in more than thirty files), RI-25 (the drain matches kinds it never registers), RI-26 (the drain searches `--help` text), RI-33 (land reads `items`), RI-41 (the library reads `items`), RI-48 (the adapter parses `show` and builds a verdict record by hand).
- As consumer: RI-20 (its list of run states omits `paused`), RI-24 (it reads drain tables and events and copies the retry command), RI-32 and RI-33 (it reads `landings` and `landing_claims`).
