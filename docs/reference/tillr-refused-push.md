---
title: "Refused autopushes: rebasing unpublished landings onto a moved remote"
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - tools/tillr-drain/drain/push_refused.go
  - tools/tillr-drain/drain/push_state.go
  - tools/tillr-drain/drain/trunk_rewrite.go
  - tools/tillr-drain/drain/trunk_history.go
  - tools/tillr-drain/remotesync/
  - tools/tillr-land/land/trunk.go
  - tools/tillr-view/push_waiting.go
  - tools/tillr-view/push_summary.go
tags: [tillr, drain, landing, push, reference]
summary: "What tillr-drain does when an automatic push does not complete: it separates a non-fast-forward refusal from a server decline, a held patch move and a transport failure; detects a remote force-push through a per-clone published pointer; replays unpublished local commits linearly onto a forward remote advance under land.lock; proves a conflict-repaired tip before publishing it; repairs a rebase conflict through a fenced item; and shows every hold with a mover that can act. Tillr stamps the configured git identity on the commits it makes and judges no other commit by identity (OQ-RP4)."
vantage:
  status-chip: true
---

# Refused autopushes: the drain rebases unpublished commits onto a moved remote and proves the new tip

**Status:** Verified 2026-11-24 against the tree. Each mechanism below names the function that carries it; `rg -n 'CheckIdentities|accept-identity|CarryIdentityAcceptances' tools -g '*.go'` returns nothing, so tillr holds no trunk identity check ([OQ-RP4](#OQ-RP4)). The nine `D-RP` decisions are the designer's; the owner may overturn any of them.

When an automatic push fails, the drain reads `git push --porcelain` and fetches the target ref to tell a moved remote from a server decline, a held patch move or a transport failure. For a moved remote it replays the unpublished local commits linearly onto the fetched tip under `land.lock`, resets the main checkout with `git reset --keep`, and remaps recorded landing commits. A textual conflict goes to a fenced repair item. Every hold is shown with its real mover and the count of unpublished commits. Without this, a commit pushed from another clone strands every later automatic push behind the same non-fast-forward rejection.

| Component | Lives in |
| :--- | :--- |
| Push classifier, published-pointer guard, linear replay | `tools/tillr-drain/drain` (`classifyPushFailure`, `guardPublished`, `healRefusedPush`) |
| Push state recording and the published pointer | `tools/tillr-drain/drain` (`recordPushState`, `advancePublished`) |
| Conflict repair item and its tree check | `tools/tillr-drain/remotesync` (`RemoteSyncItemID`, `ValidateRemoteSyncTree`) |
| Trunk rewrite map and green-pointer carry | `tools/tillr-drain/drain` (`observeTrunk`) |
| Landing identity | `tools/tillr-land/land` (`Trunk.LandingIdentity`, `Trunk.Squash`) |
| Waiting rows and the header summary | `tools/tillr-view` (`pushRow`, `readPushSummary`) |

**Cost.** One per-clone ref per remote branch, one fetch per due push, and one repair run per textual conflict. The trunk stays linear with no merge commits and the remote is never forced.

**Reads with:** [Landing principles](../design/landing-principles.md), [Restarting tillr in place](./tillr-restart.md), [the gate reference](./gate.md), [the item graph is the whole path](../design/tillr-graph-is-the-path.md), and [one vocabulary for tillr](../design/tillr-vocabulary.md).

---

## 1. What the landing principles require of a push

Automatic push is configured by `[push]` in [`.tillr.toml`](../../.tillr.toml): `what` is `"none"`, `"green"` (publish the green pointer when the heavy runner advances it; this repository's setting, [`D-RT10`](./tillr-restart.md#pushing-what-the-proof-has-passed)) or `"tip"` (publish the trunk tip after each landing). One background worker with a single replaceable pending slot runs every push.

| Principle | What it rules here |
| :--- | :--- |
| [§1](../design/landing-principles.md#1-a-refusal-must-be-a-verdict) and [§14](../design/landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) | The remote moves outside every local run. It never convicts a run or fails a landed item. |
| [§2](../design/landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [§3](../design/landing-principles.md#3-gate-what-will-be-the-tip-once) | Each unpublished commit is replayed by a three-way tree merge, the old-to-new commit map is recorded, and the rebased tip is proved once rather than commit by commit. |
| [§4](../design/landing-principles.md#4-every-outcome-is-state-changing) and [§11](../design/landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) | Retrying a push against a diverged remote repeats the same refusal on unchanged inputs. The drain classifies the failure and changes state. |
| [§7](../design/landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report) and [§13](../design/landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) | Unpublished proven commits are parked work. The waiting list and the Observatory header show their count, their start time and a mover that can act. |
| [§9](../design/landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work) and [§10](../design/landing-principles.md#10-supersession-is-a-verdict-not-a-stall) | A forward remote advance integrates with no hand work; a conflict goes to a repair item; a local commit whose change the remote already holds is superseded, not stalled. |

---

## 2. Words and state ownership

Existing terms follow [`tillr-vocabulary.md`](../design/tillr-vocabulary.md). Six are specific to this reference. $T$ is the local trunk tip, $R$ the fetched remote tip, $R_{\text{prev}}$ the published pointer, $B$ their merge base.

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **Refused push** *(coined here)* | A `git push` the remote rejected as non-fast-forward: `[rejected] (fetch first)`, `(non-fast-forward)` or `(stale info)`. | Not a hook failure, a timeout, a transport error or a `[remote rejected]` decline. |
| **Failed push** *(coined here)* | Any other push that exits non-zero or times out. | Not evidence that the remote has commits to rebase onto. |
| **Published pointer** *(coined here)* | The per-clone ref `refs/tillr/published/<remote>/<branch>`: the last commit this clone confirmed on the remote, by a successful push or an accepted forward fetch. | Not `refs/remotes/<remote>/<branch>`, which a fetch overwrites before anyone can check for a rewind. |
| **Remote rebase** *(coined here)* | Replaying unpublished local commits onto $R$ under `land.lock`, moving the trunk and the main checkout with `git reset --keep -q`, and remapping recorded landing commits. | Not a merge commit, not an unlocked pull, never a forced push. |
| **Remote rewrite** *(coined here)* | A fetched tip $R$ that does not descend from $R_{\text{prev}}$: someone force-pushed the remote branch. | Not an ordinary forward push from another clone. |
| **Conflict repair** *(coined here)* | One express `build` item per remote tip, `sync-remote-<short(R)>`, that resolves a textual replay conflict in an isolated worktree. | Not a write to the main checkout, not a step that asks the owner first. |

| State | Single writer | Rule |
| :--- | :--- | :--- |
| `refs/tillr/published/<remote>/<branch>` (`land.PublishedRefPrefix` in [`trunk.go`](../../tools/tillr-land/land/trunk.go)) | The drain's push worker (`advancePublished` in [`push_state.go`](../../tools/tillr-drain/drain/push_state.go)) | Moves forward only. Excused from a worker's boundary check ([`boundary.go`](../../tools/tillr-drain/drain/boundary.go)), so a background push never convicts a concurrent run. |
| `refs/heads/main`, the main checkout, `trunk_rewrites` | Whoever holds `<git-common-dir>/land.lock`: [`tillr-land`](../../tools/tillr-land/) for a landing, `tillr-drain` for a remote rebase | `git reset --keep -q` refuses, changing nothing, when the main checkout is off the trunk or has local changes on a path the rebase touches. |
| `refs/tillr/green` | The heavy runner ([`heavy.go`](../../tools/tillr-drain/heavyrun/heavy.go)); carried across a patch-identical replay by `observeTrunk` | Never carried onto or past a conflict-repaired commit, and never left off the trunk's ancestry. |

---

## 3. Telling a refused push apart from a failed one

The text on standard error cannot separate the cases: a local hook, a push broker and a server-side hook can each print "rejected" while the remote has not moved. Classification is mechanical (`D-RP1`).

### 3.1 Step 1: the porcelain ref status

`pushCommand` in [`push_refused.go`](../../tools/tillr-drain/drain/push_refused.go) runs `LC_ALL=C git push --porcelain -- <remote> <pushed>:<ref>`, never with `+` or a force flag, and `classifyPushFailure` reads the result:

| Porcelain line for the ref | Diagnosis | Next step |
| :--- | :--- | :--- |
| ` ` or `=` | Published (`push-ok`) | Advance the published pointer; clear any hold. |
| `!` with `[rejected] (fetch first \| non-fast-forward \| stale info)` | **Refused push** | Run Step 2 in the same turn. |
| `!` with `[remote rejected]` | `push-remote-declined` | Needs the operator. No rebase: fast-forward was not why the server declined. |
| None, and an unacknowledged patch-move hold is recorded | `push-patch-move-held` | Needs the operator: `tillr drain acknowledge-patch-move`. |
| None otherwise (hook, broker, authentication, network, deadline) | `push-failed` | Needs the operator, or the next push trigger retries. |

### 3.2 Step 2: fetch the target ref and compare ancestry

`healRefusedPush` fetches `<remote> <ref>`, resolves the tip as $R$, and compares it under `land.lock`. A failed fetch records `push-failed` and changes nothing.

| Ancestry | Meaning | Action |
| :--- | :--- | :--- |
| $R_{\text{prev}}$ exists and $R_{\text{prev}} \not\preceq R$ | **Remote rewrite** (`D-RP2`) | Stop, change nothing, record `remote-rewritten`. Rebasing and pushing would re-publish the commits the force-push removed. |
| $R \preceq T$ | The remote is already under the local trunk, so a moved remote does not explain the refusal. | Change nothing; the operator is the mover. |
| $T \prec R$ | The local trunk is behind the remote. | Pin the published pointer to $R$ and fast-forward the trunk and main checkout to $R$. |
| Neither contains the other | Forward divergence. | Replay $R..T$ onto $R$ ([Section 4](#4-rebasing-unpublished-commits-onto-the-moved-remote)), or on a conflict dispatch repair ([Section 6](#6-when-rebasing-onto-the-remote-conflicts)). |

When no published pointer exists yet, $R_{\text{prev}}$ falls back to `refs/remotes/<remote>/<branch>`.

### 3.3 The same fetch guards every due push

A remote force-pushed straight backward, to an ancestor of the published pointer, is an ancestor of the commit about to be pushed. An ordinary push would succeed as a fast-forward and put the removed commits back, with no refusal for Step 2 to act on. So every due push (`pushTrunk`, `pushGreen`, `tillr drain push`) first runs `guardPublished` (`D-RP7`):

1. Read $R_{\text{prev}}$. If neither the pointer nor the remote-tracking ref exists, this clone has confirmed nothing; push without fetching.
2. `git fetch --refmap= -- <remote> <ref>`. The empty refmap keeps the fetch from updating the remote-tracking ref, so a rejected observation never replaces the fallback. A failed fetch records `push-failed` and pushes nothing.
3. If $R_{\text{prev}} \not\preceq R$, do not push; record `remote-rewritten`. Otherwise advance the pointer to $R$ and run Step 1.

The guard is a check before the push, not an atomic lease, and not a timer poll: it runs once per push attempt.

---

## 4. Rebasing unpublished commits onto the moved remote

### 4.1 The trunk stays linear ([OQ-RP1](#OQ-RP1))

The unpublished commits $C_1, \dots, C_n$ in $R..T$ are replayed onto $R$ as single-parent commits ending at $T'$. Then $R \preceq T'$, so the next push is a plain fast-forward. An unexplained rewrite of the trunk still halts landings (`ReasonTrunkRewritten` in [`trunk_history.go`](../../tools/tillr-drain/drain/trunk_history.go)); a deliberate rebase is recorded in `trunk_rewrites(old_sha, new_sha, …)` so every reader of a landing commit resolves the replacement.

### 4.2 What `healRefusedPush` does under the landing lock

1. **Replay.** Walk $R..T$ oldest first. A merge or root commit in the range refuses without changing anything. Each commit runs `git merge-tree --write-tree` against the previous replayed parent:
   - a conflict leaves every ref and the main checkout untouched, pins the published pointer at $R$, and dispatches conflict repair;
   - a merged tree equal to the parent's tree means the remote already holds the change, so the commit maps to its parent and no empty commit is written;
   - otherwise `git commit-tree` writes the replacement, keeping the original message, author and author date, with the landing identity as committer.
2. **Pin** the published pointer at $R$.
3. **Reset.** Confirm the main checkout is on the trunk and the trunk still equals $T$, then `git reset --keep -q <T'>`. A refusal records `remote-sync-worktree-dirty`.
4. **Remap.** `observeTrunk` ([`trunk_rewrite.go`](../../tools/tillr-drain/drain/trunk_rewrite.go)) records $T \to T'$. A patch-identical replay is matched by `git patch-id --stable` and carries the green pointer. When the replay changed a commit's diff (conflict repair, shifted context, supersession), the drain passes the pairs it constructed instead, because patch identity does not match ([Principle 2](../design/landing-principles.md#2-re-application-preserves-the-change-not-the-patch)). If the fetched range touched a roll path, the roll command runs (`rollAfterRemoteSync`).

### 4.3 Tillr checks identity only on the commits it makes

Under [OQ-RP4](#OQ-RP4), tillr makes its own commits follow the identity git config states, and judges no other commit by identity.

- **What tillr does.** `Trunk.LandingIdentity` reads `user.name` and `user.email` from config scopes a lane cannot write and refuses to land without them or when a repository-local or worktree config overrides them. `Trunk.Squash` stamps the landed commit with that identity ([`trunk.go`](../../tools/tillr-land/land/trunk.go)), and the replay stamps it as committer.
- **What tillr does not do.** It holds no landing over the author or committer of a commit it did not make, wherever that commit came from. There is no acceptance command and no acceptance record.
- **No setting.** Git config is the one source of the identity tillr stamps.
- **A repository's own history rule is not tillr's.** This repository compares each commit to a [tracked list](../../tools/gate/allowed-commit-identities.json) in its own gate ([`git-identity.test.py`](../../tests/git-identity.test.py)). A repository that wants such a rule states it there.

---

## 5. Proving the rebased trunk before pushing

1. **Clean replay** (`D-RP4`). In `"tip"` mode the drain pushes the rebased tip once, immediately. In `"green"` mode it does not push on the refused turn: it records `remote-sync-proving`, the heavy runner proves `green..T'`, and the green advance publishes. A failure there is bisected as usual, resolving rebased landings through `trunk_rewrites`, and never auto-reverts a commit that came from the remote.
2. **Conflict repair** (`D-RP5`). The repaired commit is newly written, so it passes the repair item's gate before the trunk moves, and the green pointer is held strictly behind it. In `"green"` mode nothing is published until the heavy runner has proved the new tip.

---

## 6. When rebasing onto the remote conflicts

Neither side can be sent back to a lane: $R$ is published and $R..T$ is on the local trunk. Under [OQ-RP2](#OQ-RP2) the drain repairs the conflict itself and escalates obstacles as for any other item.

1. **One repair item per remote tip.** `RemoteSyncItemID` in [`remote_sync_filing.go`](../../tools/tillr-drain/remotesync/remote_sync_filing.go) names it `sync-remote-<short(R)>`. Its fence is the conflicting path set that `git merge-tree` reported, excluding gate code.
2. **Linear staging.** The lane is staged at the last cleanly replayed parent and receives the conflict output and the commit messages on both sides. The worker commits one single-parent commit that keeps the conflicting commit's intent.
3. **Tree check outside the fence** (`ValidateRemoteSyncTree`). Every path outside the fence must equal the blob the mechanical three-way merge produced, so the worker cannot drop a file the remote brought in or revert an earlier replayed commit.
4. **Gate, then finish the replay.** At landing (`landRemoteSync`), under `land.lock`, the drain replays the remaining commits, including any that landed while the worker ran, on top of the repaired commit. A second conflict advances the same item to the next conflicting commit without moving the trunk. When the chain is clean the drain resets the trunk, records every pair in `trunk_rewrites`, and holds the green pointer behind the repaired commit.
5. **Escalation.** A `fence` obstacle widens the fence, reaching the owner only when gate code is requested. An obstacle the drain cannot clear, or a repeated identical failure, turns `remote-sync-conflict` into `needs you` with the owner or operator as mover.

Items on disjoint paths keep running while the repair runs.

---

## 7. Showing the state and its mover while it lasts

Each push outcome is recorded on `drain.post-landing-action` with a structured `kind`, the remote and rebased tips, `since_ts` and the unpublished counts (`D-RP6`, `recordPushState`).

### 7.1 Waiting rows and named movers

`pushRow` in [`push_waiting.go`](../../tools/tillr-view/push_waiting.go) maps each kind to a class and a mover. `push-ok` and `remote-sync-cleared` show no row.

| Kind | Class | Mover |
| :--- | :--- | :--- |
| `remote-sync-proving` | waits on work | The heavy runner, proving the rebased tip. |
| `remote-sync-conflict`, repair running | waits on work | The repair item. |
| `remote-sync-conflict`, escalated | needs you | The owner (gate-code conflict or owner obstacle) or the operator (rebase by hand). |
| `remote-sync-worktree-dirty` | needs you | The operator: check out the trunk, or commit or restore the clashing paths. |
| `remote-rewritten` | needs you | The operator: reconcile the force-pushed branch. |
| `push-patch-move-held` | needs you | The operator: `tillr drain acknowledge-patch-move`. |
| `push-remote-declined` | needs you | The operator: inspect the rejection, then `tillr drain push`. |
| `push-failed` | needs you | The operator: restore remote access, or `tillr drain push`. |

### 7.2 Header pill and publishing from an idle queue

- `readPushSummary` in [`push_summary.go`](../../tools/tillr-view/push_summary.go) adds a `push` summary to the status payload (`published_sha`, `unpublished_green_count`, `unpublished_main_count`, the state kind, mover and since). The Observatory header shows an unpublished pill whenever a hold stands or proven commits remain ahead of the published pointer.
- `tillr drain push` (`PushOnce`) publishes on demand, and clearing a hold enqueues a push, so a finished queue does not wait for another landing to publish.

### 7.3 Holds are read from facts, not from the newest push event

Two holds outlive the attempt that recorded them, and a later attempt can end differently. Each is therefore read from the fact that makes it true (`D-RP8`):

| Hold | The fact | Effect of a later attempt |
| :--- | :--- | :--- |
| `remote-sync-conflict` | A `sync-remote-<short(R)>` item is open. Class and mover come from that item's current state. | Every outcome records the conflict with the item's mover; `tillr drain push` never returns `push-ok`. |
| `remote-sync-proving` | A recorded rebased tip is on the trunk and the green pointer does not contain it. | The attempt records its own outcome and the hold stands. |

When a repair item closes without landing, no push happened, so the drain writes one `remote-sync-cleared` event instead of `push-ok` (`D-RP9`, `settleClosedRemoteSyncConflict`). It shows no row, counts as no hold, and ends the hold's clock.

---

## 8. Invariants, non-goals and risks

**Must never happen:**

- A local commit is dropped by a rebase, or a merge commit is written to the trunk.
- A push forces the remote (`+`, `--force`, `--force-with-lease`).
- A remote force-push is undone, by a rebase or by an ordinary fast-forward over a backward rewind.
- A conflict-repaired commit is published unproved in `"green"` mode.
- Uncommitted edits in the main checkout are overwritten.
- A landing is held over the identity on a commit tillr did not make ([OQ-RP4](#OQ-RP4)).
- A standing hold is erased by a later push outcome, or a hold names a mover that cannot clear it.

**Does not:**

- Poll the remote when no push is due.
- Change how a repository's `pre-push` hook publishes or guards other branches.
- Rebase any branch but the configured trunk.

| Risk | Mitigation |
| :--- | :--- |
| The rebased range fails to build or test although it merged textually. | The heavy runner proves the rebased tip before a `"green"` publish. |
| A drain re-publishes a commit the operator force-pushed away. | The published-pointer check before every due push and after every refusal. |
| A repair worker drops or reverts content outside the conflict. | The tree check outside the fence, before the gate. |
| Remote pushes arrive faster than proofs finish. | The single replaceable pending slot coalesces triggers; the heavy runner proves the latest tip. |

---

## Why it's this way

### Settled by the owner

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-RP1"></a>[OQ-RP1](#OQ-RP1) | Rebase unpublished local commits onto the fetched remote tip; do not merge the remote into the trunk. | 2026-11-20 | [Section 4](#4-rebasing-unpublished-commits-onto-the-moved-remote) | Yes |
| <a id="OQ-RP2"></a>[OQ-RP2](#OQ-RP2) | A rebase conflict is repaired by a fenced worker behind the gate and proof, with obstacles escalated as for any other item and no owner step in the ordinary case. | 2026-11-20 | [Section 6](#6-when-rebasing-onto-the-remote-conflicts) | Yes |
| <a id="OQ-RP3"></a>[OQ-RP3](#OQ-RP3) | Superseded by [OQ-RP4](#OQ-RP4): it exempted fetched commits from an identity check that tillr does not have. | 2026-11-20 | [Section 4.3](#43-tillr-checks-identity-only-on-the-commits-it-makes) | — |
| <a id="OQ-RP4"></a>[OQ-RP4](#OQ-RP4) | Tillr makes the commits it produces follow the identity git config states, and enforces nothing else about identities. | 2026-11-20 | [Section 4.3](#43-tillr-checks-identity-only-on-the-commits-it-makes) | Yes |

### The designer's decisions

Marked under [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21); the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-RP1` | Separate a refused push from a failed one mechanically: porcelain ref status, then a fetch and an ancestry comparison. Distinct kinds for a server decline, a held patch move and any other failure. | 2026-11-20 | [Section 3](#3-telling-a-refused-push-apart-from-a-failed-one) | Yes |
| `D-RP2` | Record the last remote commit this clone published or accepted in a per-clone published pointer; a fetched tip that does not descend from it stops the push as `remote-rewritten`. | 2026-11-20 | [Section 3.2](#32-step-2-fetch-the-target-ref-and-compare-ancestry) | Yes |
| `D-RP3` | Do the remote rebase inside `tillr-drain` under `land.lock`, with `git reset --keep`, patch-identity matching for a clean replay and directly recorded pairs when a diff changed. Rejected: a separate `tillr-land` command. | 2026-11-20 | [Section 4.2](#42-what-healrefusedpush-does-under-the-landing-lock) | Yes |
| `D-RP4` | A clean replay carries the green pointer; `"green"` mode waits for proof of the rebased tip, `"tip"` mode pushes once at once. | 2026-11-20 | [Section 5](#5-proving-the-rebased-trunk-before-pushing) | Yes |
| `D-RP5` | A repaired commit must match the mechanical merge on every path outside its fence, lands by finishing the linear replay, and keeps the green pointer behind it. Rejected: stopping for the operator; a two-parent merge. | 2026-11-20 | [Section 6](#6-when-rebasing-onto-the-remote-conflicts) | Yes |
| `D-RP6` | Project structured push states with real start times, unpublished counts and truthful movers into the waiting list, the status payload and the header; add `tillr drain push`. Rejected: naming the next green advance as mover of a hold it cannot clear. | 2026-11-20 | [Section 7](#7-showing-the-state-and-its-mover-while-it-lasts) | Yes |
| `D-RP7` | Before every due push, fetch the target ref and stop as `remote-rewritten` when it does not contain the published pointer. Rejected: checking only after a refusal, which never sees a backward rewind; `--force-with-lease`. | 2026-11-21 | [Section 3.3](#33-the-same-fetch-guards-every-due-push) | Yes |
| `D-RP8` | Read a hold from the fact that makes it true, never from the newest push event. | 2026-11-21 | [Section 7.3](#73-holds-are-read-from-facts-not-from-the-newest-push-event) | Yes |
| `D-RP9` | Record `remote-sync-cleared` when a conflict hold ends because its repair item closed without landing. Rejected: recording `push-ok` when no push happened. | 2026-11-21 | [Section 7.3](#73-holds-are-read-from-facts-not-from-the-newest-push-event) | Yes |
