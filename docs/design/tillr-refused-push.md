---
title: "Refused autopushes: rebasing unpublished landings onto a moved remote"
status: accepted
stage: DECIDED
updated: 2026-11-20
tags: [tillr, drain, landing, push, design]
summary: "Settles what tillr-drain and tillr-land do when an automatic push is refused because the remote branch moved: separate non-fast-forward refusals from failed pushes via git push --porcelain plus a targeted fetch, detect remote force-pushes via a per-clone published pointer, rebase unpublished local commits onto forward remote advances under the landing lock with main-checkout reset and landing-SHA remapping, trust commit identities already on the upstream branch, prove the rebased tip before publishing, resolve rebase conflicts via a fenced repair worker with ordinary obstacle escalation, and show every state with a real mover."
next: "Decided on OQ-RP1, OQ-RP2, and OQ-RP3 (with D-RP1 through D-RP6 marked as designer's decisions awaiting review), and nothing in this design is built yet (only post-hoc git pull --rebase absorption via trunk_rewrites landed in aed03075b); next step is writing tillr-refused-push-plan.md and filing fenced build items."
depends-on:
  - landing-principles.md
  - tillr-restart.md
vantage:
  status-chip: true
---

# When the remote has moved, the drain rebases unpublished commits onto it and proves the new tip instead of retrying a push that cannot fast-forward

**Status:** 2026-11-20 — decided; nothing in this design is built yet. Today's behavior and `git` measurements verified against `0991306b51` (`d0c69060bb` for `git push --porcelain` behavior; post-hoc `git pull --rebase` absorption via `trunk_rewrites` landed in `aed03075b`, while automatic refused-push handling is unbuilt). [OQ-RP1](#OQ-RP1), [OQ-RP2](#OQ-RP2), and [OQ-RP3](#OQ-RP3) were ruled by the owner on 2026-11-20; six engineering choices (`D-RP1` through `D-RP6`) remain marked as the designer's decisions in the [Decision Ledger](#decision-ledger), where [OQ-RP1](#OQ-RP1) overturned the merge-commit steps of `D-RP3` and `D-RP5`.

> **In short.** When an automatic push fails, the drain checks `git push --porcelain` and fetches the target ref to separate a moved remote from hook, broker, and transport failures; replays unpublished local commits onto forward remote commits under `tillr-land`'s lock, resets the main checkout to the rebased tip, and remaps recorded landing SHAs; proves the rebased trunk before publishing; repairs textual conflicts automatically with ordinary obstacle escalation; and reports every non-idle state with an honest mover and unpublished commit count.

**Why it matters.** Today a single commit pushed from another machine or merged on GitHub causes every later automatic push to fail with a non-fast-forward rejection while local landings pile up unpublished behind a waiting row that claims the next green advance will clear it.
**The shape.** A two-step push classifier ([Section 3](#3-telling-a-refused-push-apart-from-a-failed-one)), linear rebase onto the fetched remote through `tillr-land` ([Section 4](#4-rebasing-unpublished-commits-onto-the-moved-remote)), proof before publication ([Section 5](#5-proving-the-rebased-trunk-before-pushing)), automatic fenced conflict repair with standard escalations ([Section 6](#6-when-rebasing-onto-the-remote-conflicts)), and truthful operator surfaces ([Section 7](#7-showing-the-state-and-its-mover-while-it-lasts)).
**Cost.** One `refs/tillr/published/<remote>/<branch>` pointer per clone, a `tillr-land sync-remote` rebase command with guarded non-fast-forward main-checkout reset and direct landing-SHA remapping, an upstream-branch exemption in `CheckIdentities`, one fenced repair run per textual rebase conflict, and a push-state projection in `tillr-view` and the Observatory header — keeping `main` linear with zero merge commits.
**Start at** [Section 1](#1-what-happens-today-and-which-principles-it-breaks) for today's gap, [Section 3](#3-telling-a-refused-push-apart-from-a-failed-one) and [Section 4](#4-rebasing-unpublished-commits-onto-the-moved-remote) for the core mechanism, or the [Decision Ledger](#decision-ledger) for the owner's three rulings and the six designer decisions.
**Needs your ruling:** None — [OQ-RP1](#OQ-RP1), [OQ-RP2](#OQ-RP2), and [OQ-RP3](#OQ-RP3) were ruled by the owner on 2026-11-20; six engineering choices (`D-RP1` through `D-RP6`) in the [Decision Ledger](#decision-ledger) remain marked as the designer's decisions under [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21) and may be overturned by the owner.
**Reads with:** [Landing principles](./landing-principles.md), [Rolling tillr onto new code](./tillr-restart.md), [The gate rework](./gate-rework.md), [The item graph is the whole path](./tillr-graph-is-the-path.md), and [One vocabulary for tillr](./tillr-vocabulary.md).

---

## 1. What happens today, and which principles it breaks

On 2026-11-19 the owner asked: *"what happens if remote moves before an autopush. auto rebase? something fixes conflicts automatically?"* Asked whether to file the gap, he said yes.

### 1.1 Today's push path

Automatic push is configured by `[push]` in [`.tillr.toml`](../../.tillr.toml), where `what` is `"none"`, `"green"` (this repository's setting, settled in [the restart design](./tillr-restart.md#pushing-what-the-proof-has-passed) as `D-RT10`), or `"tip"`, and `remote` and `ref` default to `refs/heads/main`'s upstream (`pushDestination` in [`post_landing.go`](../../tools/tillr-drain/drain/post_landing.go)).

1. **Trigger and single-slot queue.** `pushAction` runs in one background goroutine with a replaceable pending slot and a five-minute `pushTimeout`. In `"tip"` mode (`afterLandingCandidate`), each trunk landing submits `pushTrunk`, running `git push -- <remote> refs/heads/main:<ref>`. In `"green"` mode (`afterGreen`), advancing `refs/tillr/green` after a passing heavy run — and once at loop startup — submits `pushGreen`.
2. **Stale local skip check, no fetch.** `pushGreen` checks whether `refs/remotes/<remote>/<branch>` already contains `<green>` via `git merge-base --is-ancestor <green> <tracking>`. If not, it runs `git push -- <remote> <green>:<ref>` with no `+` or `--force*` flag. Neither `pushGreen` nor `pushTrunk` ever runs `git fetch`, `git merge`, or `git rebase`. Every `git push` also runs [`hooks/pre-push`](../../hooks/pre-push), which calls [`bin/patch-branches-publish`](../../bin/patch-branches-publish) to publish required `patch/<name>` branches.
3. **False recovery promise when the remote has moved.** When `git push` exits non-zero, `recordPush` logs `loop: push of green <sha> failed (retried at the next green advance; the loop goes on): <detail>` and writes a `drain.post-landing-action` event with `action: "push"`. In [`waiting.go`](../../tools/tillr-view/waiting.go), `appendPostLandingWaiting` turns any non-empty `detail` into one `post-landing:push` row with `Class: "waits on work"`, `Since: "—"`, and `Mover: "the next green advance"` (or `"the next relevant landing"` for `"tip"`).
4. **Hidden accumulation on operator surfaces.** Because `Class` is `"waits on work"` and `Mover` names neither `owner` nor `operator`, `waitingActors` in [`waiting.ts`](../../tools/tillr-view/web/src/models/waiting.ts) counts the row under `next: drain`, and [`ObservatoryHeaderBar.tsx`](../../tools/tillr-view/web/src/components/ObservatoryHeaderBar.tsx) shows no unpublished-trunk indicator at all. Every later green commit `G2, G3, …` descends from `G1`, still lacks the remote's commit, and is rejected identically. Landings accumulate locally and unpublished with no bound until someone notices the log or inspects `waiting` and rebases onto the remote by hand.

### 1.2 Checked against the landing principles

Checking today's behavior and the settled design against [`landing-principles.md`](./landing-principles.md) pins down each mechanism:

| Principle | What it rules in or out here |
| :--- | :--- |
| [§1 A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict) & [§14 Convict a run on what it did](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) | Remote movement happens outside local runs and after local candidates have already gated or landed. It must never convict an unrelated run or fail an already-landed item; pushing from another machine is the remote twin of an ad-hoc local commit. |
| [§2 Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) & [§3 Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once) | Rebasing unpublished local commits onto a moved remote replays each commit via three-way tree merge (`git merge-tree --write-tree`), records the old-to-new SHA map directly rather than requiring `git patch-id` to stay unchanged when remote context shifts, and proves the rebased trunk tip once rather than re-gating every unpublished local commit one by one. |
| [§4 Every outcome is state-changing](./landing-principles.md#4-every-outcome-is-state-changing) & [§11 A failure is diagnosed, not waited out](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) | Retrying `git push` at the next green advance when the remote has diverged repeats an identical refusal on unchanged inputs. The drain must classify why the push did not complete and change state — rebasing onto a forward remote move, or stopping on a diagnosed hold when automatic action cannot clear it. |
| [§7 A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report) & [§13 A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) | Unpublished proven commits are parked deliverables. While publication is delayed or blocked, the Observatory header and `tillr view waiting` must show the unpublished count, start time, and the actor that can actually move it — never `"the next green advance"` when no green advance can fast-forward the remote. |
| [§9 Fix the machinery; never hand-carry the work](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work) & [§10 Supersession is a verdict, not a stall](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall) | Routine forward commits on the upstream branch integrate without manual SSH surgery, textual conflicts go to a fenced repair worker with ordinary obstacle escalations, and if a remote commit already contains what a local commit or lane carried, the empty three-way replay supersedes it cleanly rather than stalling. |

---

## 2. Words and state ownership

Existing terms (`item`, `run`, `candidate`, `fast gate`, `heavy runner`, `green pointer`, `mechanic` / `trunk fixer`) follow [`tillr-vocabulary.md`](./tillr-vocabulary.md) and [`tillr-graph-is-the-path.md`](./tillr-graph-is-the-path.md). Six push-specific terms carry exact meanings here:

| Term | Meaning | What it is *not* |
| :--- | :--- | :--- |
| **Refused push** *(coined here)* | A `git push` that reached the remote and returned a non-fast-forward rejection (`[rejected] (fetch first)`, `(non-fast-forward)`, or `(stale info)`) because the remote ref is not an ancestor of the pushed commit. | Not a local `pre-push` hook failure, git-broker denial, timeout, transport error, or server-side `[remote rejected]` policy decline. |
| **Failed push** *(coined here)* | Any non-zero or timed-out `git push` that is not a refused push — local hook exit, broker denial or timeout, SSH/HTTPS/network error, or `[remote rejected]` server hook failure. | Not evidence that the remote branch has new commits to rebase onto. |
| **Published pointer** *(coined here)* | Per-clone ref `refs/tillr/published/<remote>/<branch>` recording the last commit OID this clone confirmed on `<remote>` `<ref>` (by a successful push or an accepted forward fetch). | Not `refs/remotes/<remote>/<branch>`, which `git fetch` overwrites before checking whether the remote rewound. |
| **Remote rebase** *(coined here)* | Replaying unpublished local commits onto the fetched remote tip $R$ through `tillr-land sync-remote` under `land.lock` (or fast-forwarding straight to $R$ when local `main` has none), resetting the main checkout to the resulting tip $T'$, and remapping recorded landing SHAs ([OQ-RP1](#OQ-RP1)). | Not a merge commit on `main`, not a blind `git pull` in `tillr-drain` outside `land.lock`, and never a `--force` push to the remote. |
| **Remote rewrite** *(coined here)* | A fetched remote tip $R$ that does not descend from this clone's published pointer $R_{\text{prev}}$ ($R_{\text{prev}} \not\preceq R$), meaning someone force-pushed the remote branch. | Not an ordinary forward push by another machine ($R_{\text{prev}} \preceq R$). |
| **Conflict repair** *(coined here)* | A deduplicated express `build` item (`sync-remote-<short(R)>`) that resolves textual rebase conflicts when replaying unpublished local commits onto a fetched forward remote tip $R$ in an isolated worktree, escalating obstacles like any other item ([OQ-RP2](#OQ-RP2)). | Not an unreviewed direct write to the main checkout, not a step that asks the owner first in the ordinary case, and not a license to edit files outside the conflict fence. |

### One writer per ref and record

| State | Single writer | Concurrency and boundary rule |
| :--- | :--- | :--- |
| `refs/remotes/<remote>/<branch>` | `tillr-drain`'s single `pushAction` worker via targeted `git fetch` | Ignored by worker `boundary.check` ([`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md)); serialized by `landingAction`'s single worker goroutine. |
| `refs/tillr/published/<remote>/<branch>` | `tillr-drain`'s `pushAction` worker (and `tillr drain accept-remote-rewrite` if the operator accepts a force-pushed remote) | Added to `boundary.go`'s ignored `refs/tillr/` prefixes alongside `refs/tillr/green` and `refs/tillr/trunk-history/*` so background pushes never pollute a concurrent worker's boundary report. |
| `refs/heads/main`, the main checkout, and `trunk_rewrites` | [`tools/tillr-land`](../../tools/tillr-land/) under `land.lock` (`Trunk.Move` for pure fast-forwards; `sync-remote` guarded reset and SHA remap for rebases; plus `observeTrunk` for external `git pull --rebase`) | `tillr-drain` never updates `refs/heads/main` directly; `tillr-land` acquires `land.lock`, checks `trunk_history` and `Trunk.UncommittedPaths`, updates `main` and the main checkout, and writes `trunk_rewrites` and the store receipt outside Git I/O (`store.CheckIO`). |
| `refs/tillr/green` | `HeavyRunner` in [`heavy.go`](../../tools/tillr-drain/drain/heavy.go) for forward proof advances (rewound to $\min(G, B)$ under `land.lock` when `sync-remote` rebases $G > B$ onto unproven $R$) | Advances forward only after the heavy proof passes on a commit; `sync-remote` never leaves `refs/tillr/green` off `main` and never moves it forward onto an unproven rebased commit. |

---

## 3. Telling a refused push apart from a failed one

Stderr substring matching cannot separate a moved remote from other failures: a local [`hooks/pre-push`](../../hooks/pre-push) failure, a yolo git-broker rejection, and a GitHub branch-protection hook can all print the word `"rejected"` while the remote branch has not moved at all. Classification therefore uses two mechanical steps (`D-RP1`).

### 3.1 Step 1: Porcelain ref status separates transport and hook failures from non-fast-forward refusals

`pushGreen` and `pushTrunk` run `LC_ALL=C git push --porcelain -- <remote> <pushed>:<ref>`. Measured on `git` 2.56.0, stdout's `<flag>\t<from>:<to>\t<summary>` table separates the outcomes cleanly before reading prose:

| `--porcelain` stdout for `<ref>` | Exit | Diagnosis | Next step |
| :--- | :--- | :--- | :--- |
| `<space>` (`<old>..<new>`) or `=` (`[up to date]`) | `0` | Published | Advance `refs/tillr/published/<remote>/<branch>` to the remote's OID and clear any push warning. |
| `!` with `[rejected] (fetch first)`, `[rejected] (non-fast-forward)`, or `[rejected] (stale info)` | `1` | **Refused push** (remote ref is not a known ancestor of `<pushed>`) | Run Step 2 (`git fetch` + local DAG classification) immediately in the same `pushAction` turn. |
| `!` with `[remote rejected] (<reason>)` | `1` | **Failed push: `push-remote-declined`** (server hook, branch protection, secret scan) | Record `push-remote-declined` (`needs you`); do not fetch-and-rebase, because fast-forward was not why the server declined. |
| No porcelain line for `<ref>`, and an unacknowledged `patch_moves` hold is active in SQLite | non-`0` | **Failed push: `push-patch-move-held`** ([`bin/patch-branches-publish`](../../bin/patch-branches-publish) refused in [`hooks/pre-push`](../../hooks/pre-push)) | Record `push-patch-move-held` (`needs you`), naming `tillr drain acknowledge-patch-move`. |
| No porcelain line for `<ref>` (other local hook failure, git-broker denial/timeout, auth, or network `exit 128`) | non-`0` or deadline | **Failed push: `push-failed`** | Record `push-failed` with stderr detail (`needs you` on broker/hook/auth failure; retryable via `tillr drain push` or the next push trigger). |

### 3.2 Step 2: Fetching the target ref classifies the exact commit graph

On a Step 1 refused push, `pushAction` reads the prior `refs/tillr/published/<remote>/<branch>` OID $R_{\text{prev}}$ (falling back to the pre-fetch `refs/remotes/<remote>/<branch>` OID when `refs/tillr/published/` does not exist yet on first run) and runs:

```sh
git fetch --no-tags -- <remote> +<ref>:refs/remotes/<remote>/<branch>
```

In [`config/yolo-jail/config.toml`](../../config/yolo-jail/config.toml), `[allow.git]` sets `always = ["fetch"]` and `on_request = ["new_branches", "push"]`, so `git fetch` runs unattended inside the jail without popping an approval prompt. If `git fetch` itself fails, the turn records `push-failed` and guesses nothing.

Once `git fetch` writes $R$ (`refs/remotes/<remote>/<branch>`), `git merge-base --is-ancestor` compares four commits in the local object store — $P$ (`<pushed>`), $T$ (current local `refs/heads/main`), $R$ (fetched remote tip), and $R_{\text{prev}}$ (last published pointer):

| Local ancestry check after `fetch` | Meaning | Action |
| :--- | :--- | :--- |
| $R_{\text{prev}}$ exists and $R_{\text{prev}} \not\preceq R$ | **Remote rewrite (`remote-rewritten`):** someone force-pushed `<remote>` `<ref>`, dropping or replacing commits this clone already saw on the remote (`D-RP2`). | Stop autopushing and surface `remote-rewritten` (`needs you`). Never auto-rebase: replaying local commits (which include $R_{\text{prev}}$) onto $R$ and pushing would re-publish the commits the force-push just removed. |
| $P \preceq R$ and $T \preceq R$ | **Local behind remote:** the remote already holds $P$ and every local `main` commit (for instance, the owner pushed from the main checkout or another clone). | Advance `refs/tillr/published/<remote>/<branch>` to $R$, clear the push warning, and ask `tillr-land` to fast-forward local `main` from $T$ to $R$. |
| $P \preceq R$ and $T \not\preceq R$, $R \not\preceq T$ | **Green already on remote, unproved `main` diverged:** `<remote>` already contains `green` ($P$), while newer local landings in $P..T$ diverged from $R$. | Advance `refs/tillr/published/<remote>/<branch>` to $R$ and rebase $P..T$ onto $R$ now so the next green advance can fast-forward `<remote>`. |
| $R \preceq P$ | **Now fast-forwardable:** `<pushed>` already descends from $R$. | Re-run `git push --porcelain -- <remote> <pushed>:<ref>` once immediately. |
| $P \not\preceq R$, $R \not\preceq P$, and ($R_{\text{prev}}$ unset or $R_{\text{prev}} \preceq R$) | **Forward divergence (`remote-diverged`):** another writer fast-forwarded `<remote>` to $R$ while local `main` advanced to $T$ from merge-base $B = \text{merge-base}(T, R)$. | Advance `refs/tillr/published/<remote>/<branch>` to $R$ and rebase $B..T$ onto $R$ ([Section 4](#4-rebasing-unpublished-commits-onto-the-moved-remote)). |

---

## 4. Rebasing unpublished commits onto the moved remote

### 4.1 Linear trunk by rebasing unpublished commits onto the remote

On 2026-11-20 the owner ruled [OQ-RP1](#OQ-RP1) in favor of **Option B (rebase onto the remote)** rather than the proposal's merge-commit leaning: `refs/heads/main` stays linear, unpublished local commits are replayed onto the fetched remote tip, the main checkout is reset to the result, and recorded landing commit IDs are remapped.

When local `main` ($T$) and the fetched remote ($R$) have both advanced past their merge-base $B = \text{merge-base}(T, R)$ ($T \not\preceq R$ and $R \not\preceq T$), `tillr-land sync-remote` replays the unpublished local commits $C_1, \dots, C_n \in B..T$ onto $R$ as single-parent commits $B \to R_1 \to \dots \to R \to C_1' \to \dots \to T'$. That satisfies $R \preceq T'$ (so the next push to `<remote>` `<ref>` is a pure fast-forward with no `--force`) while dropping the pre-rebase tip $T$ from `main`'s ancestry ($T \not\preceq T'$).

Unexplained rewrites of `refs/heads/main` continue to halt landings under `ReasonTrunkRewritten` in [`trunk_history.go`](../../tools/tillr-drain/drain/trunk_history.go). Two deliberate rebase paths preserve every local commit across $T \to T'$:

1. **Manual `git pull --rebase` in the main checkout** (built in `aed03075b`): [`trunk_rewrite.go`](../../tools/tillr-drain/drain/trunk_rewrite.go) detects the rewrite on the next pass, matches each commit in $B..T$ to a commit in $B..T'$ via `git patch-id --stable`, and writes `trunk_rewrites(old_sha, new_sha, recorded_at, evidence)`.
2. **Automatic `tillr-land sync-remote --remote-tip <R>` on a refused push** (specified below, not yet built): because `tillr-land` itself executes the three-way replay under `land.lock`, it knows each exact $C_i \mapsto C_i'$ pair directly and writes `trunk_rewrites` and `trunk_history.observed_tip = T'` inside the locked transition — never relying on `git patch-id --stable`, which legitimately changes whenever $B..R$ modified adjacent context or conflict repair reconciled conflicting hunks ([Principle 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)).

### 4.2 What `tillr-land sync-remote` does under the landing lock

Under [OQ-RP1](#OQ-RP1) and `D-RP3`, `tillr-drain` invokes `tillr-land sync-remote --remote-tip <R>` so `tillr-land` remains the single writer of `refs/heads/main`:

1. **Pure fast-forward when local has no unpublished commits ($T \preceq R$).** Under `land.lock`, `tillr-land` verifies `trunk_history`, runs `Trunk.Move("refs/heads/main", R, T)` to fast-forward `refs/heads/main` and the main checkout straight to $R$, and updates `trunk_history.observed_tip` and `refs/tillr/trunk-history/observed` to $R$. No commits are replayed or remapped.
2. **Sequential three-way replay onto $R$ when both sides advanced ($T \not\preceq R$ and $R \not\preceq T$).** Before acquiring `land.lock`, `tillr-land` lists the unpublished local commits in `git rev-list --first-parent --reverse <B>..<T>` ($C_1, \dots, C_n$) and replays each $C_i$ onto the running tip $P_{i-1}$ (starting at $P_0 = R$) in memory without touching any worktree:
   - Each step runs `git merge-tree --write-tree --merge-base=<C_i^1> <P_{i-1}> <C_i>`.
   - If `merge-tree` exits `1` with `CONFLICT`, replay stops without moving any ref or file and returns the conflicting commit $C_i$, its clean predecessor $P_{i-1}$, and the conflicting paths for conflict repair ([Section 6](#6-when-rebasing-onto-the-remote-conflicts)).
   - If the merged tree equals $P_{i-1}$'s tree (an upstream commit in $B..R$ already applied the exact same change as $C_i$, [Principle 10](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall)), `tillr-land` skips writing an empty commit and maps $C_i \mapsto P_{i-1}$.
   - Otherwise `git commit-tree <tree_i> -p <P_{i-1}>` writes $C_i'$, preserving $C_i$'s commit message, author, and `GIT_AUTHOR_DATE` (so an ad-hoc owner commit keeps its authorship and timestamp) while stamping `GIT_COMMITTER_*` with `Trunk.LandingIdentity()`.
   - Under `land.lock`, `tillr-land` re-reads `refs/heads/main`. If a concurrent candidate landed while replay ran ($T \to T_1$), `tillr-land` replays the newly appended $T..T_1$ commits onto $T'$ before checking the main checkout or moving any ref.
3. **Guarded reset of the main checkout (`T -> T'`).** Because $T \not\preceq T'$, `Trunk.Move`'s `git merge --ff-only` cannot move a checked-out `main`. Under `land.lock`, `tillr-land` locates any worktree with `refs/heads/main` checked out (`Trunk.WorktreeFor`) and checks `Trunk.UncommittedPaths(worktree)` against every path that differs between $T$'s tree and $T'$'s tree (`git diff-tree -r --name-only <T> <T'>`):
   - **Dirty main-checkout protection:** if any uncommitted tracked or untracked path in the main checkout is among the paths differing between $T$ and $T'$, `tillr-land` refuses without touching any ref or file and returns `trunk-worktree-uncommitted` naming those clashing paths. `tillr-drain` records `remote-sync-worktree-dirty` (`needs you`, matching `trunk-path-dirty` in [`holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go)) and re-attempts `sync-remote` once the main checkout's uncommitted path set changes.
   - **Atomic checkout and branch reset:** when no uncommitted path clashes, `tillr-land` pins the pre-rebase tip at `refs/tillr/trunk-history/accepted/<T>`, updates the main checkout's index and working tree across $T \to T'$ via two-tree `git read-tree -u -m <T> <T'>` (leaving unrelated dirty files untouched) paired with `git update-ref refs/heads/main <T'> <T>`, and updates `refs/tillr/trunk-history/observed` to $T'$.
4. **Remapping recorded landing commit IDs, markers, and the green pointer.** In the same locked transition:
   - `tillr-land` inserts every replayed commit pair $C_i \mapsto C_i'$ into `trunk_rewrites(old_sha, new_sha, recorded_at, evidence)`, appends a `drain.trunk-rewrite-absorbed` event, and sets `trunk_history.observed_tip = T'` (`lost_tip = ''`).
   - Any live `refs/tillr/landed/<run_id>` marker ref pointing at $C_i \in B..T$ is updated to $C_i'$, and every reader of `landings.trunk_after` resolves through `trunk_rewrites` (`RewriteResolve` / `RewriteAliases`), keeping crash recovery, ref cleanup, lane-base blocker checks, comment reply delivery, `firstRed` bisection, and `auto_revert` intact across the rebase.
   - If `refs/tillr/green` ($G$) pointed at an unpublished commit in $B..T$ ($B < G \le T$), `tillr-land` rewinds `refs/tillr/green` to $B$ (when $G \le B$, `refs/tillr/green` stays at $G$). Unlike an external `git pull --rebase` where `trunk_history.go` carries `green` forward to `Pairs[green]`, `sync-remote` never marks $G'$ green before `HeavyRunner` proves it atop $R$ ([Section 5](#5-proving-the-rebased-trunk-before-pushing)), while keeping `refs/tillr/green` at $B \preceq R \preceq T'$ ensures `refs/tillr/green` remains an ancestor of `main` for lane staging.

### 4.3 Trusting commit identities already on the upstream branch

When `main` moves from $T$ to $T'$ (or fast-forwards from $T$ to $R$), `CheckIdentities` in [`identity_history.go`](../../tools/tillr-land/land/identity_history.go) walks `old..tip` and compares every commit against this machine's `LandingIdentity()`. Because this repository deploys distinct git emails across work and personal machines (`tag-work` vs `tag-personal`), [`git-identity.test.py`](../../tests/git-identity.test.py) bounds its check at `merge-base HEAD origin/main` rather than re-judging published remote commits.

Per the owner's 2026-11-20 ruling on [OQ-RP3](#OQ-RP3) (**Option A**), commits already reachable from the fetched upstream branch are trusted by `CheckIdentities` without requiring `tillr drain accept-identity`:

- `CheckIdentities` excludes commits reachable from the fetched upstream ref (`git rev-list --reverse <old>..<tip> --not <R>`), because any commit already on `<remote>` `<ref>` passed push credentials on the machine that published it.
- Local commits replayed in $R..T'$ remain subject to `CheckIdentities`; if an ad-hoc local commit $C_i \in B..T$ had already been accepted in `trunk_identity_acceptances`, `sync-remote` carries that acceptance forward to $C_i'$ so replaying it onto $R$ does not re-trigger `trunk-foreign-identity`.

### 4.4 What the rebase design needs that is not built yet

Audited against `0991306b51`, one piece of the rebase substrate exists and seven pieces remain unbuilt:

- **Already built (`aed03075b`, 2026-11-20):** `trunk_rewrites` and `trunk_rewrite_unmatched` in [`tools/tillr-drain/heavy/rewrites.go`](../../tools/tillr-drain/heavy/rewrites.go) plus post-hoc `proveRewrite` in [`tools/tillr-drain/drain/trunk_rewrite.go`](../../tools/tillr-drain/drain/trunk_rewrite.go), which absorbs a manual `git pull --rebase` when every commit's `git patch-id --stable` stays identical and resolves `trunk_rewrites` in `lane_base.go`, `heavy/status.go`, `heavy.go`, `heavy_revert.go`, and `why.go`.
- **Not built yet:**
  1. **Refused-push classifier, targeted fetch, and `refs/tillr/published/<remote>/<branch>` (`tillr-drain`):** [`post_landing.go`](../../tools/tillr-drain/drain/post_landing.go) still invokes `git push` without `--porcelain`, never runs `git fetch`, maintains no `refs/tillr/published/` pointer (nor `boundary.go` ignore rule for it), and labels every non-zero exit `"retried at the next green advance"`.
  2. **`tillr-land sync-remote` sequential three-way replay and direct `trunk_rewrites` write (`tillr-land`):** `tillr-land` only implements single-candidate fast-forward `Squash`; it has no command to replay $B..T$ onto $R$ and record $C_i \mapsto C_i'$ directly in `trunk_rewrites` when shifted remote context or conflict repair alters `git patch-id --stable` (where post-hoc `proveRewrite` would stop on `trunk-rewritten`).
  3. **Guarded non-fast-forward main-checkout reset (`tillr-land`):** `Trunk.Move` in [`trunk.go`](../../tools/tillr-land/land/trunk.go) hardcodes `git -c merge.autoStash=false merge --ff-only`, which refuses every rebase $T \to T'$ when `main` is checked out. Checking `Trunk.UncommittedPaths` against `git diff-tree <T> <T'>` and updating the main checkout via two-tree `git read-tree -u -m <T> <T'>` plus `update-ref` is unbuilt.
  4. **Remaining landing-SHA, marker, and roll-path readers (`tillr-land` and `tillr-drain`):** [`refs.go`](../../tools/tillr-drain/drain/refs.go) (`land.after != ref.SHA` marker retention), [`comments.go`](../../tools/tillr-drain/drain/comments.go) (comment-reply `BlobID` lookup), [`stale_base.go`](../../tools/tillr-drain/drain/stale_base.go), [`landing_call_lifecycle.go`](../../tools/tillr-drain/drain/landing_call_lifecycle.go), and `Trunk.RecoveredLanding` (`refs/tillr/landed/<run_id>` markers) still inspect raw `landings.trunk_after` or pre-rebase markers without `trunk_rewrites` resolution or marker updates; and `changedPaths` in [`post_landing.go`](../../tools/tillr-drain/drain/post_landing.go) (`tip^..tip`) must check paths across $B..R$ when `refs/tillr/green` advances across a rebase so remote updates to `tools/tillr*/` trigger `roll.command`.
  5. **Guarded `refs/tillr/green` rewind on unproven rebase (`tillr-land` and `tillr-drain`):** [`trunk_history.go`](../../tools/tillr-drain/drain/trunk_history.go) carries `refs/tillr/green` forward to `Pairs[green]` after a manual `git pull --rebase`, whereas `sync-remote` must hold or rewind `refs/tillr/green` to $\min(G, B)$ until `HeavyRunner` proves the rebased commits atop $R$.
  6. **Upstream-branch identity exemption (`tillr-land`):** `CheckIdentities` in [`identity_history.go`](../../tools/tillr-land/land/identity_history.go) has no `--not <R>` exclusion for fetched upstream commits ([OQ-RP3](#OQ-RP3)) and does not carry `trunk_identity_acceptances` across $C_i \mapsto C_i'$.
  7. **Conflict-repair item (`sync-remote-<short(R)>`), unfenced-tree lock, `tillr drain push`, and push-state views (`tillr-drain`, `tillr-land`, `tillr-view`):** none of [Section 6](#6-when-rebasing-onto-the-remote-conflicts) or [Section 7](#7-showing-the-state-and-its-mover-while-it-lasts) is built.

---

## 5. Proving the rebased trunk before pushing

Rebasing $B..T$ onto $R$ never publishes the rebased commits unverified (`D-RP4`). How proof runs before `git push` follows `push.what` while keeping [Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once) intact (one proof of the rebased tip, never $n$ separate gate runs for $n$ rebased local commits):

1. **When `push.what = "green"` (this repository's setting, `D-RT10`):**
   - When $T \preceq R$ (local `main` had no unpublished commits and simply fast-forwarded to $R$), nothing local needs pushing; `refs/tillr/green` stays at $G \le T \preceq R$, and the heavy runner proves $G..R$ on its normal schedule.
   - When `tillr-land sync-remote` replays $B..T$ onto $R$ to form $T'$, `refs/heads/main` resets to $T'$ while `refs/tillr/green` sits at $\min(G, B) \le B < R < T'$. **Nothing is pushed when $T'$ is written**, because none of $C_1'..\,T'$ has passed proof atop $R$ — even if $G == T$ before the rebase and $R$ passed checks on another machine, $B..R$ and $B..T$ can still interact semantically (for example, $R$ changed a function signature that $C_i$ called in another package).
   - `tillr-drain` records `remote-sync-proving` (`Class: "waits on work"`, naming the heavy runner as mover) and wakes `HeavyRunner`.
   - `HeavyRunner` runs both the fast whole-tree declarations and the heavy checks on `main` ($T'$, or a newer tip $T_k \succeq T'$ if path-disjoint landings arrived meanwhile):
     - **Pass:** `refs/tillr/green` advances to $T'$ (or $T_k$). If $B..R$ changed `tools/tillr*/`, advancing `green` across $B..R$ triggers `roll.command` and safe loop restart. `afterGreen` fires `pushGreen`, which now fast-forwards `<remote>` `<ref>` from $R$ to `green` and advances `refs/tillr/published/<remote>/<branch>`.
     - **Fail:** `refs/tillr/green` stays at $\min(G, B) \le B$, so **no broken tree is ever pushed to `<remote>`**. `HeavyRunner` bisects `green..T'` via `firstRed`: if the first red commit lies in $B..R$ (the remote branch itself arrived red), no `landings` row owns it (`auto_revert` never reverts an upstream commit) and `HeavyRunner` files a deduplicated trunk fixer (`fixer-main-<sha12>`); if the first red commit is a rebased local landing $C_i' \in R..T'$, `trunk_rewrites` resolves $C_i'$ back to $C_i$'s `landings` row so `auto_revert` or `fixer-main-<sha12>` acts with exact attribution while path-disjoint work continues under [`OQ-GP7`](./tillr-graph-is-the-path.md#OQ-GP7). Once `HeavyRunner` passes on the repaired or reverted tip $F \succeq R$, `refs/tillr/green` advances to $F$ and `pushGreen` fast-forwards `<remote>` from $R$ to $F$.
2. **When `push.what = "tip"`:**
   - Because `"tip"` mode publishes `main` without waiting for `refs/tillr/green`, `tillr-drain` runs the fast gate (`tillr-gate`) on the candidate rebased tip $T'$'s tree in a detached scratch checkout **outside `land.lock`** before calling `tillr-land` to reset `main` from $T$ to $T'$.
   - If the fast gate passes, `tillr-land` resets `main` to $T'$ under `land.lock` and `pushTrunk` immediately fast-forwards `<remote>` `<ref>` from $R$ to $T'$. If the fast gate fails, `main` stays at $T$ and the failure enters the repair path in [Section 6](#6-when-rebasing-onto-the-remote-conflicts) rather than moving `main` to or pushing a broken tip.

---

## 6. When rebasing onto the remote conflicts

When replaying $C_1, \dots, C_n \in B..T$ onto $R$ hits a `git merge-tree --write-tree` exit `1` on commit $C_k$ (or when `"tip"` mode's pre-reset fast gate fails on $T'$), neither `refs/heads/main` nor the main checkout is touched, and no conflict markers are written to the main checkout. Neither side can simply be sent back to an unlanded lane: $R$ is already published on `<remote>`, and $B..T$ is already committed on local `main`.

Per the owner's 2026-11-20 ruling on [OQ-RP2](#OQ-RP2), the drain repairs rebase conflicts automatically through a fenced repair worker and escalates obstacles the same way any other item's obstacles escalate, with no step that involves the owner in the ordinary case:

1. **Deduplicated express repair item.** With no human prompt beforehand, the drain files at most one open repair item per remote tip $R$ (`sync-remote-<short(R)>`, express priority, `build` seat, routed on a free lane or `desk-trunk-fixer` when all regular lanes hold retained candidates). Its initial fence is the conflicting path set reported by `git merge-tree` for $C_k$ onto $P_{k-1}$ (where $P_{k-1}$ is $C_{k-1}'$, or $R$ when $k = 1$), widened on a `fence` obstacle through `drain.fixer-fence-widened` (never widening into gate definitions).
2. **Brief and linear staging onto $P_{k-1}$.** The lane stages at $P_{k-1}$ ($R \preceq P_{k-1}$) and receives the conflicting file paths, conflict hunks, commit messages on $B..R$ and $C_k$, and any item summary recorded in `landings` for $C_k$. The worker resolves the conflict and commits a single linear commit $C_k'$ on top of $P_{k-1}$, keeping $C_k$'s subject/intent and zero merge parents.
3. **Locked non-conflicting tree check (`D-RP5`).** Before running the fast gate, the drain compares the worker's commit $C_k'$ against the mechanical three-way merge of $(P_{k-1}, C_k)$ over merge-base $C_k^1$ on every path **outside** the item's fence: every unfenced path in $C_k'$'s tree must match the exact blob OID produced by `merge-tree` on $(P_{k-1}, C_k)$. A worker therefore cannot silently drop an unrelated file brought in by $R$ or revert an earlier rebased commit in $R..P_{k-1}$.
4. **Gate, review, linear rebase completion, and heavy proof.** $C_k'$ runs through the fast gate and code review ([`tillr-diff-review.md`](./tillr-diff-review.md)). At landing time under `land.lock`, `tillr-land sync-remote` replays the remaining commits $C_{k+1}..\,T_{\text{now}}$ (including any concurrent local landings $T_0..\,T_{\text{now}}$ that arrived while the worker ran) linearly atop $C_k'$ via `git merge-tree --write-tree` — never writing a merge commit. If a later commit $C_m$ ($m > k$) also conflicts, `sync-remote` advances the repair item's staged base to $C_{m-1}'$ for the next conflict; once the full chain reaches $T_{\text{now}}'$, `tillr-land` resets `refs/heads/main` and the main checkout from $T_{\text{now}}$ to $T_{\text{now}}'$, records every $C_i \mapsto C_i'$ pair (including $C_k \mapsto C_k'$) in `trunk_rewrites`, and holds `refs/tillr/green` at $\min(G, B)$ until `HeavyRunner` proves $T_{\text{now}}'$ before `pushGreen` publishes it.
5. **Standard obstacle escalation.** Obstacles reported by the repair worker (`fence`, `definition`, `tooling`, `seat`, `owner`) follow the exact same obstacle routing and `repeatedEnding` loop guard as every other item in the drain: `fence` widens the conflict fence, `seat` escalates to a stronger model or design seat, and only an unresolvable obstacle (such as `owner` on genuinely contradictory intent) or a repeated identical failure transitions the state to `needs you` naming `owner` or `operator` as mover.

Path-disjoint local items continue running in their lanes while conflict repair runs; they are not killed or blamed ([Principle 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)).

---

## 7. Showing the state and its mover while it lasts

To satisfy [Principle 7](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report) and [Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act), `drain.post-landing-action` (or a dedicated `push_state` projection keyed by `action = "push"`) records a structured `kind` alongside `detail`, `remote_tip`, `since_ts` (preserved across updates until `push-ok` clears it), and unpublished counts (`D-RP6`).

### 7.1 Waiting rows and named movers

`appendPostLandingWaiting` in [`waiting.go`](../../tools/tillr-view/waiting.go) maps each non-`ok` push state to the class and mover prefix that `waitingActors` in [`waiting.ts`](../../tools/tillr-view/web/src/models/waiting.ts) routes accurately (`needs you` with `owner; …` or `operator; …` whenever human action is required, `waits on work` only while an active drain step is actually advancing publication):

| State `kind` | `Class` | `Mover` | What `Why` states (alongside real `Since`) |
| :--- | :--- | :--- | :--- |
| `remote-sync-proving` | `waits on work` | `the heavy runner; prove rebased tip <short(T')> before pushing` | Rebased `<n>` local commit(s) onto remote `<short(R)>` at `<short(T')>`; awaiting heavy proof before pushing. |
| `remote-sync-conflict` (repair active) | `waits on work` | `<sync-item-id>; resolve rebase conflict onto <remote>/<branch> (<short(R)>)` | Rebasing onto `<short(R)>` conflicted on `<paths>`; `<k>` proven / `<n>` total local commits unpublished while repair runs. |
| `remote-sync-conflict` (escalated) | `needs you` | `owner; …` (on owner obstacle) or `operator; rebase main onto <remote>/<branch> (<short(R)>)` | Conflict repair for rebase onto `<short(R)>` escalated (`<reason>`) on `<paths>`; `<k>` proven / `<n>` total local commits unpublished. |
| `remote-sync-worktree-dirty` | `needs you` | `operator; commit, stash, or restore clashing paths in the main checkout` | Cannot reset `main` to rebased `<short(T')>` because the main checkout has uncommitted edits on clashing paths `<paths>`. |
| `remote-rewritten` | `needs you` | `operator; reconcile force-pushed <remote>/<branch> (<short(R_prev)> -> <short(R)>)` | Remote rewound or rewrote `<short(R_prev)>` to `<short(R)>`; autopush stopped to avoid re-publishing dropped commits. |
| `push-patch-move-held` | `needs you` | `operator; run tillr drain acknowledge-patch-move after checking the fork rewrite` | [`hooks/pre-push`](../../hooks/pre-push) held publication on an unacknowledged patch-branch move; `<k>` proven commits unpublished. |
| `push-remote-declined` | `needs you` | `operator; inspect remote rejection on <remote> <ref> and run tillr drain push` | Remote server declined `<short(P)>`: `<detail>`; `<k>` proven commits unpublished. |
| `push-failed` | `needs you` | `operator; check git-broker approval or remote access, or run tillr drain push` | Push of `<short(P)>` did not reach a ref update (`<detail>`); `<k>` proven commits unpublished (also re-attempted on the next push trigger). |

### 7.2 Header badge and idle-queue recovery

1. **Observatory header and `tillr view status`.** A drained queue with `0 running` must not look finished while proven commits sit unpublished locally. Status payloads add a `push` summary (`published_sha`, `unpublished_green_count`, `unpublished_main_count`, `state_kind`, `mover`, `since`), and [`ObservatoryHeaderBar.tsx`](../../tools/tillr-view/web/src/components/ObservatoryHeaderBar.tsx) renders an unpublished pill whenever `push.what != "none"` and either `state_kind` is non-empty or proven commits remain ahead of `published_sha` after the push worker goes idle.
2. **Publishing after a hold clears on an idle queue (`tillr drain push`).** Relying solely on `"the next green advance"` strands publication when the queue has already finished its last item and the operator subsequently restores Wi-Fi, approves the git broker, resolves a dirty main checkout, or runs `tillr drain acknowledge-patch-move`. Adding an explicit `tillr drain push` command — and having `acknowledge-patch-move` and dirty-checkout clearance enqueue `pushAction` when `green` (or `main` in `"tip"` mode) is ahead of `refs/tillr/published/<remote>/<branch>` — publishes the parked commits immediately without needing a dummy landing or loop restart.

---

## 8. Invariants, non-goals, and risks

### 8.1 What must never happen

- **No local commit is silently dropped on rebase, and `main` stays linear:** `tillr-land sync-remote` writes zero merge commits on `refs/heads/main`; every unpublished local commit in $B..T$ is replayed onto $R$ and mapped in `trunk_rewrites` (with $T$ pinned at `refs/tillr/trunk-history/accepted/<T>`, or superseded only when its exact tree change is already in $R$), while any unmapped external rewrite still halts on `trunk-rewritten`.
- **A push never forces the remote:** `git push` never passes `+`, `--force`, or `--force-with-lease` on `<ref>`.
- **A remote force-push is never silently undone:** when $R_{\text{prev}} \not\preceq R$, the drain never rebases local commits (which include $R_{\text{prev}}$) onto $R$ and pushes them back onto the remote.
- **Nothing unproven reaches the remote in `"green"` mode:** a rebased tip $T'$ is published only after `HeavyRunner` advances `refs/tillr/green` to $T'$ or a descendant of $T'$, and `sync-remote` never carries `refs/tillr/green` forward onto unproven rebased commits.
- **Uncommitted edits in the main checkout are never overwritten:** `sync-remote` checks `Trunk.UncommittedPaths` against `git diff-tree <T> <T'>`, refuses clashing paths before moving any ref or file, and waits as `remote-sync-worktree-dirty`.
- **Cross-machine pushes on the upstream branch never freeze local landings on foreign identity**, while local commits remain checked ([OQ-RP3](#OQ-RP3)).
- **Non-diverged failures never claim `"the next green advance"` as their mover** when a green advance cannot clear them.

### 8.2 What this does not do

- Does not poll the remote on a timer when no push is due; remote state is read only when a push is refused or `tillr drain push` is invoked.
- Does not change how fork `patch/<name>` branches are published or guarded by [`bin/patch-branches-publish`](../../bin/patch-branches-publish); it surfaces `patch_moves` holds truthfully instead of masking them as `"waits on work"`.
- Does not rebase branches other than the configured trunk (`refs/heads/main` onto its configured `<remote>` `<ref>`).

### 8.3 Risks

| Risk | Mitigation |
| :--- | :--- |
| Clean textual rebase introduces a semantic build or test failure between $B..R$ and replayed $B..T$. | In `"green"` mode, `refs/tillr/green` stays at $\min(G, B)$ until `HeavyRunner` proves $T'$; a failure holds `refs/tillr/green`, bisects `green..T'` via `firstRed` (resolving rebased local landings through `trunk_rewrites`), and auto-reverts or files a trunk fixer (`fixer-main-<sha12>`). In `"tip"` mode, the fast gate runs on $T'$ before resetting `main`. |
| Operator force-pushes `<remote>` `<ref>` to drop a bad commit, and a running drain rebases local history containing that commit onto $R$ and re-pushes it. | `refs/tillr/published/<remote>/<branch>` checks $R_{\text{prev}} \preceq R$ on every refused-push fetch (`D-RP2`); $R_{\text{prev}} \not\preceq R$ stops autopushing as `remote-rewritten` (`needs you`). |
| Conflict-repair worker accidentally reverts or drops non-conflicting files from $R$ or earlier local commits. | `D-RP5` locks every path outside the item's fence in repaired commit $C_k'$ to the exact blob OID of the mechanical three-way merge of $(P_{k-1}, C_k)$ over $C_k^1$ before the fast gate runs. |
| Frequent remote pushes arrive faster than `HeavyRunner` finishes proving each rebase. | `landingAction`'s single replaceable pending slot coalesces triggers, and `HeavyRunner` proves the latest `main` tip in batch rather than proving intermediate rebases one by one. |

### 8.4 What done looks like

1. Pushing a non-conflicting commit to `<remote>` `<ref>` from another clone (including from another machine with a different git email) while local landings are unpublished causes the next autopush to detect `[rejected] (fetch first)`, fetch $R$, replay $B..T$ linearly onto $R$ under `land.lock`, reset the main checkout, remap landing SHAs in `trunk_rewrites`, prove the rebased tip, and fast-forward `<remote>` `<ref>` with zero manual intervention and zero merge commits.
2. Pushing a conflicting commit to `<remote>` `<ref>` dispatches one fenced `sync-remote-<short(R)>` repair item that resolves the conflict linearly onto $R$, passes gate, review, and heavy proof, and escalates through standard obstacle routing only when an obstacle or repeated failure holds it.
3. Force-pushing `<remote>` `<ref>` backwards stops autopush immediately as `remote-rewritten` (`Class: "needs you"`) without rebasing and re-publishing the dropped commit onto the remote.
4. A local `pre-push` failure, `[remote rejected]` decline, or broker denial records its distinct state and real `needs you` mover in `tillr view waiting` and the Observatory header, with accurate `Since` and unpublished commit counts, and `tillr drain push` publishes immediately once the operator clears the cause.

---

## Decision Ledger

### Settled by the owner

| ID | Ruling / Decision | Date | Where | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-RP1"></a>[`OQ-RP1`](#OQ-RP1) | **Owner ruling:** Rebase unpublished local commits onto the fetched remote tip (`Option B`) rather than merging the remote into `main` (`Option A`, the proposal's leaning): `git log` stays linear, unpublished local commits in $B..T$ are replayed onto $R$ under `land.lock`, the main checkout is reset across $T \to T'$, and recorded landing commit IDs are remapped via `trunk_rewrites` (and `refs/tillr/landed/<run_id>`). Asked whether a moved remote should be merged into `main` or unpublished local commits rebased onto it, the owner's answer on 2026-11-20 was: *"B"*. | 2026-11-20 | [Section 4](#4-rebasing-unpublished-commits-onto-the-moved-remote), [Section 5](#5-proving-the-rebased-trunk-before-pushing) | — |
| <a id="OQ-RP2"></a>[`OQ-RP2`](#OQ-RP2) | **Owner ruling:** When rebasing onto a moved remote conflicts textually with unpublished local commits, dispatch a fenced repair worker (`sync-remote-<short(R)>`) that resolves the conflict behind the gate, review, and heavy proof and escalates obstacles through the same path as any other item, with no step involving the owner in the ordinary case. Ruled on 2026-11-20: *"definitely repair as necessry with esaclations like naything else. ideally I shouldn't be involved"*. | 2026-11-20 | [Section 6](#6-when-rebasing-onto-the-remote-conflicts), [Section 7.1](#71-waiting-rows-and-named-movers) | — |
| <a id="OQ-RP3"></a>[`OQ-RP3`](#OQ-RP3) | **Owner ruling:** Trust commits fetched from the configured upstream branch in `CheckIdentities` (`Option A`) rather than holding local landings on `trunk-foreign-identity` until `tillr drain accept-identity`, while continuing to check commits authored or replayed locally. Ruled on 2026-11-20: *"A — a commit already on the configured upstream branch passed push credentials on the machine that published it, and requiring accept-identity would freeze cross-machine work between your work and personal machines."* | 2026-11-20 | [Section 4.3](#43-trusting-commit-identities-already-on-the-upstream-branch) | — |

### The designer's decisions, awaiting the owner's review

Every `D-RP` row below is an engineering choice made by the designer and marked under [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21); none is the owner's ruling, and the owner may overturn any of them. The owner's linear-rebase ruling on [OQ-RP1](#OQ-RP1) overturned the merge-commit mechanics in `D-RP3` (which originally used fast-forward `Trunk.Move` for diverged branches) and `D-RP5` (which originally landed conflict repairs as two-parent `-p <T_now> -p <R>` merge commits); both rows below state their updated linear-rebase form while keeping their remaining designer choices marked. `D-RP1`, `D-RP2`, `D-RP4`, and `D-RP6` were not overturned.

| ID | Decision | Date | Where | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-RP1 | **Designer's decision; the owner may overturn this.** Separate refused pushes from failed pushes in two mechanical steps: parse `LC_ALL=C git push --porcelain` stdout for `!` with `[rejected] (fetch first\|non-fast-forward\|stale info)`, then run `git fetch --no-tags -- <remote> +<ref>:refs/remotes/<remote>/<branch>` and classify by `git merge-base --is-ancestor` in the local object store rather than matching stderr substrings. | 2026-11-20 | [Section 3](#3-telling-a-refused-push-apart-from-a-failed-one) | — |
| D-RP2 | **Designer's decision; the owner may overturn this.** Record the last remote OID this clone published or accepted at `refs/tillr/published/<remote>/<branch>` (ignored by worker `boundary.check`); if a fetched remote tip $R$ does not descend from that OID ($R_{\text{prev}} \not\preceq R$), stop autopushing as `remote-rewritten` (`needs you`) rather than auto-rebasing onto a force-pushed remote. | 2026-11-20 | [Section 2](#2-words-and-state-ownership), [Section 3.2](#32-step-2-fetching-the-target-ref-classifies-the-exact-commit-graph) | — |
| D-RP3 | **Designer's decision (merge-move half overturned by [OQ-RP1](#OQ-RP1)); the owner may overturn the rest.** Keep `tillr-land` as the single writer of `refs/heads/main` under `land.lock`: `tillr-drain` fetches into `refs/remotes/<remote>/<branch>` and invokes `tillr-land sync-remote`, using `Trunk.Move` when $T \preceq R$ and a guarded two-tree reset (`git read-tree -u -m <T> <T'>` + `update-ref`, replacing the proposal's fast-forward merge move per [OQ-RP1](#OQ-RP1)) when $T \not\preceq R$, holding cleanly as `remote-sync-worktree-dirty` when the main checkout has uncommitted edits on paths differing between $T$ and $T'$. | 2026-11-20 | [Section 2](#2-words-and-state-ownership), [Section 4.2](#42-what-tillr-land-sync-remote-does-under-the-landing-lock) | — |
| D-RP4 | **Designer's decision; the owner may overturn this.** Prove rebased commits according to `push.what` before pushing: in `"green"` mode, hold/rewind `refs/tillr/green` to $\min(G, B)$ during `sync-remote` and require `HeavyRunner` to advance `refs/tillr/green` to or past $T'$ before `pushGreen` publishes (using `firstRed`, `trunk_rewrites`, `auto_revert`, and `fixer-main-<sha12>` if red); in `"tip"` mode, pass `tillr-gate`'s fast tier on $T'$'s tree in a detached checkout before resetting `main` and running `pushTrunk`. | 2026-11-20 | [Section 5](#5-proving-the-rebased-trunk-before-pushing) | — |
| D-RP5 | **Designer's decision (two-parent merge landing overturned by [OQ-RP1](#OQ-RP1)); the owner may overturn the rest.** On conflict repair ([OQ-RP2](#OQ-RP2)), enforce a pre-gate tree check requiring every path outside the item's fence in repaired commit $C_k'$ to match the mechanical three-way merge blob OID of $(P_{k-1}, C_k)$ over $C_k^1$, and complete landing onto a concurrently advanced `main` ($T_{\text{now}}$) by replaying $C_{k+1}..\,T_{\text{now}}$ linearly as single-parent commits atop $C_k'$ (replacing the proposal's `-p <T_now> -p <R>` merge commit per [OQ-RP1](#OQ-RP1)). | 2026-11-20 | [Section 6](#6-when-rebasing-onto-the-remote-conflicts) | — |
| D-RP6 | **Designer's decision; the owner may overturn this.** Project explicit push states with real `Since` timestamps, unpublished proven/total commit counts, and truthful `Class` and `Mover` values into `tillr view waiting`, `tillr view status`, and `ObservatoryHeaderBar`, and add `tillr drain push` (plus automatic re-enqueue when a blocking hold clears) so an idle drained queue can publish without waiting for a new landing. | 2026-11-20 | [Section 7](#7-showing-the-state-and-its-mover-while-it-lasts) | — |

---

## Open Questions

None. [OQ-RP1](#OQ-RP1), [OQ-RP2](#OQ-RP2), and [OQ-RP3](#OQ-RP3) were ruled by the owner on 2026-11-20 and are compacted into the [Decision Ledger](#decision-ledger).
