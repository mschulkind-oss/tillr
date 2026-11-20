---
title: "Refused autopushes: integrating a moved remote without rewinding the local trunk"
status: in-review
stage: DESIGN
updated: 2026-11-20
tags: [tillr, drain, landing, push, design]
summary: "Settles what tillr-drain and tillr-land do when an automatic push is refused because the remote branch moved: separate non-fast-forward refusals from failed pushes via git push --porcelain plus a targeted fetch, detect remote force-pushes via a per-clone published pointer, integrate forward remote advances under the landing lock without rewinding local main, prove the integrated commit before publishing, handle merge conflicts, and show every state with a real mover."
next: "Owner answers OQ-RP1, OQ-RP2, and OQ-RP3 (and reviews D-RP1 through D-RP6); route this document on docs/tillr/roadmap.md and file the implementation items once ruled."
depends-on:
  - landing-principles.md
  - tillr-restart.md
vantage:
  status-chip: true
---

# When the remote has moved, the drain integrates and proves the new commits instead of retrying a push that cannot fast-forward

**Status:** 2026-11-20. Proposal; nothing here is built. Today's behavior and `git` measurements verified at `d0c69060bb`.

> **In short.** When an automatic push fails, the drain checks `git push --porcelain` and fetches the target ref to separate a moved remote from hook, broker, and transport failures; integrates forward remote commits under `tillr-land`'s lock without ever moving local `main` backwards; proves the combined tree before publishing; and reports every non-idle state with an honest mover and unpublished commit count.

**Why it matters.** Today a single commit pushed from another machine or merged on GitHub causes every later automatic push to fail with a non-fast-forward rejection while local landings pile up unpublished behind a waiting row that claims the next green advance will clear it.
**The shape.** A two-step push classifier ([Section 3](#3-telling-a-refused-push-apart-from-a-failed-one)), forward-only remote integration through `tillr-land` ([Section 4](#4-integrating-the-remote-without-moving-the-local-trunk-backwards)), proof before publication ([Section 5](#5-proving-the-integrated-trunk-before-pushing)), bounded conflict handling ([Section 6](#6-when-integrating-the-remote-conflicts)), and truthful operator surfaces ([Section 7](#7-showing-the-state-and-its-mover-while-it-lasts)).
**Cost.** One `refs/tillr/published/<remote>/<branch>` pointer per clone, a remote-integration command on `tillr-land`, a push-state projection in `tillr-view` and the Observatory header, and — if [OQ-RP1](#OQ-RP1) takes Option A — one two-parent merge commit on `main` whenever the remote advances concurrently with unpublished local commits.
**Start at** [Section 1](#1-what-happens-today-and-which-principles-it-breaks) for today's gap, [Section 3](#3-telling-a-refused-push-apart-from-a-failed-one) and [Section 4](#4-integrating-the-remote-without-moving-the-local-trunk-backwards) for the core mechanism, or [Open Questions](#open-questions) for the three choices reserved for the owner.
**Needs your ruling:** [OQ-RP1](#OQ-RP1), [OQ-RP2](#OQ-RP2), and [OQ-RP3](#OQ-RP3); six engineering choices (`D-RP1` through `D-RP6`) in the [Decision Ledger](#decision-ledger) are marked as the designer's decisions under [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21).
**Reads with:** [Landing principles](./landing-principles.md), [Rolling tillr onto new code](./tillr-restart.md), [The gate rework](./gate-rework.md), [The item graph is the whole path](./tillr-graph-is-the-path.md), and [One vocabulary for tillr](./tillr-vocabulary.md).

---

## 1. What happens today, and which principles it breaks

On 2026-11-19 the owner asked: *"what happens if remote moves before an autopush. auto rebase? something fixes conflicts automatically?"* Asked whether to file the gap, he said yes.

### 1.1 Today's push path

Automatic push is configured by `[push]` in [`.tillr.toml`](../../.tillr.toml), where `what` is `"none"`, `"green"` (this repository's setting, settled in [the restart design](./tillr-restart.md#pushing-what-the-proof-has-passed) as `D-RT10`), or `"tip"`, and `remote` and `ref` default to `refs/heads/main`'s upstream (`pushDestination` in [`post_landing.go`](../../tools/tillr-drain/drain/post_landing.go)).

1. **Trigger and single-slot queue.** `pushAction` runs in one background goroutine with a replaceable pending slot and a five-minute `pushTimeout`. In `"tip"` mode (`afterLandingCandidate`), each trunk landing submits `pushTrunk`, running `git push -- <remote> refs/heads/main:<ref>`. In `"green"` mode (`afterGreen`), advancing `refs/tillr/green` after a passing heavy run — and once at loop startup — submits `pushGreen`.
2. **Stale local skip check, no fetch.** `pushGreen` checks whether `refs/remotes/<remote>/<branch>` already contains `<green>` via `git merge-base --is-ancestor <green> <tracking>`. If not, it runs `git push -- <remote> <green>:<ref>` with no `+` or `--force*` flag. Neither `pushGreen` nor `pushTrunk` ever runs `git fetch`, `git merge`, or `git rebase`. Every `git push` also runs [`hooks/pre-push`](../../hooks/pre-push), which calls [`bin/patch-branches-publish`](../../bin/patch-branches-publish) to publish required `patch/<name>` branches.
3. **False recovery promise when the remote has moved.** When `git push` exits non-zero, `recordPush` logs `loop: push of green <sha> failed (retried at the next green advance; the loop goes on): <detail>` and writes a `drain.post-landing-action` event with `action: "push"`. In [`waiting.go`](../../tools/tillr-view/waiting.go), `appendPostLandingWaiting` turns any non-empty `detail` into one `post-landing:push` row with `Class: "waits on work"`, `Since: "—"`, and `Mover: "the next green advance"` (or `"the next relevant landing"` for `"tip"`).
4. **Hidden accumulation on operator surfaces.** Because `Class` is `"waits on work"` and `Mover` names neither `owner` nor `operator`, `waitingActors` in [`waiting.ts`](../../tools/tillr-view/web/src/models/waiting.ts) counts the row under `next: drain`, and [`ObservatoryHeaderBar.tsx`](../../tools/tillr-view/web/src/components/ObservatoryHeaderBar.tsx) shows no unpublished-trunk indicator at all. Every later green commit `G2, G3, …` descends from `G1`, still lacks the remote's commit, and is rejected identically. Landings accumulate locally and unpublished with no bound until someone notices the log or inspects `waiting` and integrates the remote by hand.

### 1.2 Checked against the landing principles

Checking today's behavior and candidate remedies against [`landing-principles.md`](./landing-principles.md) narrows the design before any mechanism is chosen:

| Principle | What it rules in or out here |
| :--- | :--- |
| [§1 A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict) & [§14 Convict a run on what it did](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran) | Remote movement happens outside local runs and after local candidates have already gated or landed. It must never convict an unrelated run or fail an already-landed item; pushing from another machine is the remote twin of an ad-hoc local commit. |
| [§2 Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) & [§3 Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once) | Integrating remote commits uses three-way tree merge (`git merge-tree --write-tree`), never `git patch-id`, and proves the integrated trunk commit once rather than re-gating every unpublished local landing one by one. |
| [§4 Every outcome is state-changing](./landing-principles.md#4-every-outcome-is-state-changing) & [§11 A failure is diagnosed, not waited out](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) | Retrying `git push` at the next green advance when the remote has diverged repeats an identical refusal on unchanged inputs. The drain must classify why the push did not complete and change state — integrating a forward remote move, or stopping on a diagnosed hold when automatic action cannot clear it. |
| [§7 A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report) & [§13 A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) | Unpublished proven commits are parked deliverables. While publication is delayed or blocked, the Observatory header and `tillr view waiting` must show the unpublished count, start time, and the actor that can actually move it — never `"the next green advance"` when no green advance can fast-forward the remote. |
| [§9 Fix the machinery; never hand-carry the work](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work) & [§10 Supersession is a verdict, not a stall](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall) | Routine forward commits on the upstream branch should integrate without manual SSH surgery, and if a remote commit already contains what a local lane was preparing, `Trunk.Squash`'s existing `already-on-trunk` check resolves the lane as `resolved-elsewhere`. |

---

## 2. Words and state ownership

Existing terms (`item`, `run`, `candidate`, `fast gate`, `heavy runner`, `green pointer`, `mechanic` / `trunk fixer`) follow [`tillr-vocabulary.md`](./tillr-vocabulary.md) and [`tillr-graph-is-the-path.md`](./tillr-graph-is-the-path.md). Six push-specific terms carry exact meanings here:

| Term | Meaning | What it is *not* |
| :--- | :--- | :--- |
| **Refused push** *(coined here)* | A `git push` that reached the remote and returned a non-fast-forward rejection (`[rejected] (fetch first)`, `(non-fast-forward)`, or `(stale info)`) because the remote ref is not an ancestor of the pushed commit. | Not a local `pre-push` hook failure, git-broker denial, timeout, transport error, or server-side `[remote rejected]` policy decline. |
| **Failed push** *(coined here)* | Any non-zero or timed-out `git push` that is not a refused push — local hook exit, broker denial or timeout, SSH/HTTPS/network error, or `[remote rejected]` server hook failure. | Not evidence that the remote branch has new commits to integrate. |
| **Published pointer** *(coined here)* | Per-clone ref `refs/tillr/published/<remote>/<branch>` recording the last commit OID this clone confirmed on `<remote>` `<ref>` (by a successful push or an accepted forward fetch). | Not `refs/remotes/<remote>/<branch>`, which `git fetch` overwrites before checking whether the remote rewound. |
| **Remote integration** *(coined here)* | Advancing local `refs/heads/main` through `tillr-land` under `land.lock` so both the prior local `main` tip and the fetched remote tip are ancestors of the new `main` tip (unless rebased under [OQ-RP1](#OQ-RP1) Option B). | Not a blind `git pull` in `tillr-drain` outside `land.lock`, and never a `--force` push to the remote. |
| **Remote rewrite** *(coined here)* | A fetched remote tip $R$ that does not descend from this clone's published pointer $R_{\text{prev}}$ ($R_{\text{prev}} \not\preceq R$), meaning someone force-pushed the remote branch. | Not an ordinary forward push by another machine ($R_{\text{prev}} \preceq R$). |
| **Conflict repair** *(coined here)* | A deduplicated express `build` item (`sync-remote-<short(R)>`) that resolves textual merge conflicts between local `main` and a fetched forward remote tip $R$ in an isolated worktree. | Not an unreviewed direct write to the main checkout, and not a license to edit files outside the conflict fence. |

### One writer per ref and record

| State | Single writer | Concurrency and boundary rule |
| :--- | :--- | :--- |
| `refs/remotes/<remote>/<branch>` | `tillr-drain`'s single `pushAction` worker via targeted `git fetch` | Ignored by worker `boundary.check` ([`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md)); serialized by `landingAction`'s single worker goroutine. |
| `refs/tillr/published/<remote>/<branch>` | `tillr-drain`'s `pushAction` worker (and `tillr drain accept-remote-rewrite` if the operator accepts a force-pushed remote) | Added to `boundary.go`'s ignored `refs/tillr/` prefixes alongside `refs/tillr/green` and `refs/tillr/trunk-history/*` so background pushes never pollute a concurrent worker's boundary report. |
| `refs/heads/main` and the main checkout | [`tools/tillr-land`](../../tools/tillr-land/) under `land.lock` (`Trunk.Move`) | `tillr-drain` never updates `refs/heads/main` directly; `tillr-land` acquires `land.lock`, checks `trunk_history` and `Trunk.UncommittedPaths`, moves `main`, and writes the store receipt outside Git I/O (`store.CheckIO`). |
| `refs/tillr/green` | `HeavyRunner` in [`heavy.go`](../../tools/tillr-drain/drain/heavy.go) | Advances only after the heavy proof passes on a commit; remote integration never moves `refs/tillr/green` forward on an unproven merge. |

---

## 3. Telling a refused push apart from a failed one

Stderr substring matching cannot separate a moved remote from other failures: a local [`hooks/pre-push`](../../hooks/pre-push) failure, a yolo git-broker rejection, and a GitHub branch-protection hook can all print the word `"rejected"` while the remote branch has not moved at all. Classification therefore uses two mechanical steps (`D-RP1`).

### 3.1 Step 1: Porcelain ref status separates transport and hook failures from non-fast-forward refusals

`pushGreen` and `pushTrunk` run `LC_ALL=C git push --porcelain -- <remote> <pushed>:<ref>`. Measured on `git` 2.56.0, stdout's `<flag>\t<from>:<to>\t<summary>` table separates the outcomes cleanly before reading prose:

| `--porcelain` stdout for `<ref>` | Exit | Diagnosis | Next step |
| :--- | :--- | :--- | :--- |
| `<space>` (`<old>..<new>`) or `=` (`[up to date]`) | `0` | Published | Advance `refs/tillr/published/<remote>/<branch>` to the remote's OID and clear any push warning. |
| `!` with `[rejected] (fetch first)`, `[rejected] (non-fast-forward)`, or `[rejected] (stale info)` | `1` | **Refused push** (remote ref is not a known ancestor of `<pushed>`) | Run Step 2 (`git fetch` + local DAG classification) immediately in the same `pushAction` turn. |
| `!` with `[remote rejected] (<reason>)` | `1` | **Failed push: `push-remote-declined`** (server hook, branch protection, secret scan) | Record `push-remote-declined` (`needs you`); do not fetch-and-merge, because fast-forward was not why the server declined. |
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
| $R_{\text{prev}}$ exists and $R_{\text{prev}} \not\preceq R$ | **Remote rewrite (`remote-rewritten`):** someone force-pushed `<remote>` `<ref>`, dropping or replacing commits this clone already saw on the remote (`D-RP2`). | Stop autopushing and surface `remote-rewritten` (`needs you`). Never auto-merge: merging $T$ with $R$ and pushing would re-publish the commits the force-push just removed. |
| $P \preceq R$ and $T \preceq R$ | **Local behind remote:** the remote already holds $P$ and every local `main` commit (for instance, the owner pushed from the main checkout or another clone). | Advance `refs/tillr/published/<remote>/<branch>` to $R$, clear the push warning, and ask `tillr-land` to fast-forward local `main` from $T$ to $R$. |
| $P \preceq R$ and $T \not\preceq R$, $R \not\preceq T$ | **Green already on remote, unproved `main` diverged:** `<remote>` already contains `green` ($P$), while newer local landings in $P..T$ diverged from $R$. | Advance `refs/tillr/published/<remote>/<branch>` to $R$ and integrate $R$ into local `main` now so the next green advance can fast-forward `<remote>`. |
| $R \preceq P$ | **Now fast-forwardable:** `<pushed>` already descends from $R$. | Re-run `git push --porcelain -- <remote> <pushed>:<ref>` once immediately. |
| $P \not\preceq R$, $R \not\preceq P$, and ($R_{\text{prev}}$ unset or $R_{\text{prev}} \preceq R$) | **Forward divergence (`remote-diverged`):** another writer fast-forwarded `<remote>` to $R$ while local `main` advanced to $T$ from merge-base $B = \text{merge-base}(T, R)$. | Advance `refs/tillr/published/<remote>/<branch>` to $R$ and integrate $R$ with local `main` ([Section 4](#4-integrating-the-remote-without-moving-the-local-trunk-backwards)). |

---

## 4. Integrating the remote without moving the local trunk backwards

### 4.1 Why linear rebase collides with the forward-only trunk rule

Two invariants meet when local `main` ($T$) and fetched remote ($R$) have both moved past their merge-base $B$ ($T \not\preceq R$ and $R \not\preceq T$):

1. **The remote push never forces** ([`tillr-restart.md`](./tillr-restart.md#what-must-never-happen)): any commit $T_{\text{new}}$ pushed to `<remote>` `<ref>` must have $R$ as an ancestor ($R \preceq T_{\text{new}}$).
2. **The standing rule that local `main` never moves backwards** ([`trunk_history.go`](../../tools/tillr-drain/drain/trunk_history.go) and [`landing.go`](../../tools/tillr-land/land/landing.go)): every previously observed local `main` tip must remain an ancestor of `refs/heads/main` ($T \preceq T_{\text{new}}$), enforced on every pass by `ReasonTrunkRewritten`.

In a commit graph where $T \not\preceq R$ and $R \not\preceq T$, a commit $T_{\text{new}}$ satisfies **both** $T \preceq T_{\text{new}}$ and $R \preceq T_{\text{new}}$ **if and only if** $T_{\text{new}}$ (or an ancestor of it) is a merge commit connecting $T$ and $R$. Replaying $B..T$ as single-parent commits $B \to R_1 \to \dots \to R \to L_1' \to \dots \to T'$ gives $R \preceq T'$ while dropping $T$ from `main`'s ancestry ($T \not\preceq T'$):

- `trunk_history` immediately flags `trunk-rewritten: <T> -> <T'>` and freezes landings (`AcceptRewrite` in [`trunk_rewrite_accept.go`](../../tools/tillr-drain/drain/trunk_rewrite_accept.go) only accepts one-for-one identical-tree rewrites, not rebases onto new upstream commits).
- `Trunk.Move` in [`trunk.go`](../../tools/tillr-land/land/trunk.go) uses `git -c merge.autoStash=false merge --ff-only`, which refuses $T \to T'$ because the main checkout has `main` checked out.
- Every `landings.trunk_after` SHA in $B..T$, every `refs/tillr/landed/<run>` ref, and `refs/tillr/green` (when $B < G \le T$) are orphaned off `main`, breaking `heavy.Landings`, `firstRed` bisection, and `auto_revert` unless rewritten in lockstep.
- Any ad-hoc hand commit the owner made locally in $B..T$ must also be rewritten, changing its commit SHA and committer timestamp.

Because choosing between a merge commit on `main` and a rebase exception touches the repository's commit graph, [OQ-RP1](#OQ-RP1) puts that choice to the owner, leaning toward **Option A (fast-forward merge commit)**. [Section 4.2](#42-fast-forward-integration-under-the-landing-lock) specifies the Option A path; [Section 4.4](#44-what-changes-if-linear-rebase-is-chosen-instead) records what changes under Option B.

### 4.2 Fast-forward integration under the landing lock

Under [OQ-RP1](#OQ-RP1) Option A and `D-RP3`, `tillr-drain` invokes `tillr-land sync-remote --remote-tip <R>` so `tillr-land` remains the sole writer of `refs/heads/main`:

1. **Pure fast-forward when local has no unpublished commits ($T \preceq R$).** Under `land.lock`, `tillr-land` verifies `trunk_history` and runs `Trunk.Move(R)`. No merge commit is created; local `main` and the main checkout fast-forward straight to $R$, and `trunk_history.observed_tip` updates to $R$.
2. **Clean three-way merge when both sides advanced ($T \not\preceq R$ and $R \not\preceq T$).** Before acquiring `land.lock`, `tillr-land` runs `git merge-tree --write-tree --merge-base=<B> <T> <R>` in memory without touching any worktree. When `merge-tree` exits `0` with tree OID $\tau$:
   - Under `land.lock`, `tillr-land` re-reads `refs/heads/main`. If a concurrent candidate landed in the meantime ($T \to T_1$), `tillr-land` recomputes `merge-tree` against $T_1$.
   - If $\tau$ already equals $T$'s tree (the remote commits were already applied locally), `tillr-land` still writes merge commit $M$ with parents `-p <T> -p <R>` and tree $\tau$ so $R$ becomes an ancestor of `main` and the next push can fast-forward `<remote>`.
   - `git commit-tree <tau> -p <T> -p <R>` stamps $M$ with `Trunk.LandingIdentity()` and message `chore(trunk): integrate <remote>/<branch> (<short(R)>)`.
   - `Trunk.Move(M)` fast-forwards `refs/heads/main` and the main checkout from $T$ to $M$ and advances `trunk_history.observed_tip` to $M$.
3. **Dirty main-checkout protection.** If the owner has uncommitted edits in the main checkout on paths modified between $T$ and $M$ (i.e., paths touched by $B..R$), `Trunk.Move` refuses without touching the checkout and returns `trunk-worktree-uncommitted` with the clashing paths. `tillr-drain` records `remote-sync-worktree-dirty` (`needs you`, naming those paths, matching `trunk-path-dirty` in [`holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go)) and re-attempts `sync-remote` once the main checkout's uncommitted path set changes.
4. **First-parent compatibility with roll and bisection.** Because $T$ is $M$'s first parent ($M^1 = T$):
   - Every local `landings.trunk_after` commit stays on `main`'s `--first-parent` spine.
   - `changedPaths` in [`post_landing.go`](../../tools/tillr-drain/drain/post_landing.go) (`git diff-tree … tip^ tip`) diffs $M^1$ ($T$) against $M$, yielding the exact paths brought in by $B..R$ — so if $B..R$ updated `tools/tillr*/`, `roll.command` and safe loop restart trigger automatically once $M$ turns green.
   - `heavyFirstParentCommits` and `heavyCommitChangedPaths` in [`heavy.go`](../../tools/tillr-drain/drain/heavy.go) (`--first-parent`) treat $M$ as one step on the spine carrying $B..R$'s paths, and because $M$ has no item row in `landings`, `auto_revert` never mistakes $M$ for a single item's landing.

### 4.3 Foreign commit identities on the upstream branch

When `main` advances from $T$ to $M$ (or fast-forwards from $T$ to $R$), `CheckIdentities` in [`identity_history.go`](../../tools/tillr-land/land/identity_history.go) walks `git rev-list --reverse T..M` across **all** parents, checking every commit in $B..R$ against this machine's `LandingIdentity()`. Yet this repository deploys distinct git emails across work and personal machines (`tag-work` vs `tag-personal`), which is why [`git-identity.test.py`](../../tests/git-identity.test.py) already bounds its check at `merge-base HEAD origin/main` rather than re-judging published remote commits. Without an exemption for commits reachable from the fetched upstream ref, pushing a commit from the owner's laptop while the desktop runs the drain would freeze all desktop landings on `trunk-foreign-identity`. Because that touches the repository's identity guard, [OQ-RP3](#OQ-RP3) puts the choice to the owner, leaning toward trusting commits already reachable from the configured upstream branch.

### 4.4 What changes if linear rebase is chosen instead

If the owner selects Option B on [OQ-RP1](#OQ-RP1) (linear rebase onto $R$), `tillr-land sync-remote` replays each first-parent commit $C_i \in B..T$ sequentially onto $R$ via `git merge-tree --write-tree --merge-base=<C_i^1> <prev> <C_i>`, preserving each commit's author and `GIT_AUTHOR_DATE` and stamping committer with `LandingIdentity()`. Under `land.lock`, `tillr-land` must then verify the main checkout has no uncommitted changes on any path in $B..R \cup B..T$, pin $T$ at `refs/tillr/trunk-history/rebased/<T>`, update `refs/heads/main` and the main checkout across the non-fast-forward jump $T \to T'$, update `trunk_history.observed_tip` to $T'$, record a SHA-remap table so `landings.trunk_after` and `refs/tillr/landed/<run>` resolve $L_i \mapsto L_i'$, and move `refs/tillr/green` back to $B$ (or $R$ only if $R$ was already proven), because no rebased commit $L_i'$ has been tested atop $R$ yet.

---

## 5. Proving the integrated trunk before pushing

Integrating $R$ onto `main` never publishes the combined tree unverified (`D-RP4`). How proof runs before `git push` follows `push.what` while keeping [Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once) intact (one proof of the integrated tip, never $N$ re-gates of $N$ local landings):

1. **When `push.what = "green"` (this repository's setting, `D-RT10`):**
   - When $T \preceq R$ (local `main` had no unpublished commits and simply fast-forwarded to $R$), nothing local needs pushing; `refs/tillr/green` stays at $G \le T \preceq R$, and the heavy runner proves $G..R$ on its normal schedule.
   - When `tillr-land` writes merge commit $M$ (parents $T$ and $R$), `refs/heads/main` moves to $M$ while `refs/tillr/green` stays at $G < M$. **Nothing is pushed when $M$ is created**, because the combined tree $M$ has not passed proof — even if $G == T$ and $R$ passed checks on another machine, $B..T$ and $B..R$ can still interact semantically (for example, $R$ changed a function signature that $B..T$ called in another package).
   - `tillr-drain` records `remote-sync-proving` (`Class: "waits on work"`, naming the heavy runner as mover) and wakes `HeavyRunner`.
   - `HeavyRunner` runs both the fast whole-tree declarations and the heavy checks on `main` ($M$, or a newer tip $T_k \succeq M$ if path-disjoint landings arrived meanwhile):
     - **Pass:** `refs/tillr/green` advances to $M$ (or $T_k$). `afterGreen` fires `pushGreen`, which now fast-forwards `<remote>` `<ref>` from $R$ to `green` and advances `refs/tillr/published/<remote>/<branch>`.
     - **Fail:** `refs/tillr/green` stays at $G$, so **no broken tree is ever pushed to `<remote>`**. `HeavyRunner` bisects `G..M` via `firstRed`: if $M$ is first red (either $R$ arrived red or $B..R$ broke against $B..T$), `HeavyRunner` files a deduplicated trunk fixer (`fixer-main-<sha12>`) on `main` while path-disjoint work continues under [`OQ-GP7`](./tillr-graph-is-the-path.md#OQ-GP7). Once the fixer lands ($M \to F$) and `HeavyRunner` passes on $F$, `refs/tillr/green` advances to $F$ and `pushGreen` fast-forwards `<remote>` from $R$ to $F$.
2. **When `push.what = "tip"`:**
   - Because `"tip"` mode publishes `main` without waiting for `refs/tillr/green`, `tillr-drain` runs the fast gate (`tillr-gate`) on $M$'s tree in a detached scratch checkout **outside `land.lock`** before calling `tillr-land` to move `main` to $M$.
   - If the fast gate passes, `tillr-land` moves `main` to $M$ under `land.lock` and `pushTrunk` immediately fast-forwards `<remote>` `<ref>` to $M$. If the fast gate fails, `main` stays at $T$ and the failure enters the repair path in [Section 6](#6-when-integrating-the-remote-conflicts) rather than advancing or pushing a broken tip.

---

## 6. When integrating the remote conflicts

When `git merge-tree --write-tree --merge-base=<B> <T> <R>` exits `1` (textual conflict), neither `refs/heads/main` nor the main checkout is touched, and no conflict markers are written to disk. Unlike an unlanded lane hitting `squash-conflict`, neither side can simply be sent back to its lane: $R$ is already published on `<remote>`, and $B..T$ is already committed on local `main` (potentially spanning several landed items or a local hand commit).

Whether an agent resolves that conflict or the drain stops pushing and asks the operator touches model spend and reconciliation of external commits, so [OQ-RP2](#OQ-RP2) puts that choice to the owner:

- **Under [OQ-RP2](#OQ-RP2) Option A (leaning — one fenced, proven conflict-repair item):**
  1. **Deduplicated express item.** The drain files at most one open repair item per remote tip $R$ (`sync-remote-<short(R)>`, express priority, `build` seat, routed on a free lane or `desk-trunk-fixer` when all regular lanes hold retained candidates). Its initial fence is the exact list of conflicting paths reported by `git merge-tree`, widened on a `fence` obstacle through `drain.fixer-fence-widened` (never widening into gate definitions).
  2. **Brief and staging.** The lane stages at $T_0$ (local `main` when staged) with $R$ fetched locally and receives the conflicting file paths, conflict hunks, commit messages on both $B..T_0$ and $B..R$, and any item summaries recorded in `landings` for $B..T_0$. The worker resolves the conflict and commits a tree reconciling $T_0$ and $R$.
  3. **Locked non-conflicting tree check (`D-RP5`).** Before running the fast gate, the drain compares the worker's candidate tree $H$ against the mechanical three-way merge of $(T_0, R)$ on every path **outside** the item's fence: every unfenced path in $H$ must match the blob OID produced by `merge-tree` on $(T_0, R)$. A worker therefore cannot silently drop an unrelated file brought in by $R$ or revert an unrelated local landing in $B..T_0$.
  4. **Gate, review, concurrent-safe landing, and heavy proof.** $H$ runs through the fast gate and code review ([`tillr-diff-review.md`](./tillr-diff-review.md)). At landing time under `land.lock`, `tillr-land` merges $T_0..H$ onto current `main` ($T_{\text{now}}$, in case unrelated items landed while the worker ran) via `git merge-tree --write-tree --merge-base=<T_0> <T_now> <H>` and writes merge commit $M$ with parents `-p <T_now> -p <R>`. Under `push.what = "green"`, $M$ still waits for `HeavyRunner` to advance `refs/tillr/green` before `pushGreen` publishes it.
  5. **Escalation without loops.** If the conflict reflects incompatible product intent or touches owner-authored prose where guessing would violate [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21), the worker reports `OBSTACLE: owner` (or `design`). An obstacle or an identical repeated failure (`repeatedEnding`) transitions the state to `needs you`, naming `owner` or `operator` as mover rather than spinning.
- **Under [OQ-RP2](#OQ-RP2) Option B (hold immediately for the operator):**
  - No worker is dispatched and zero model budget is spent. The drain records `remote-sync-conflict` with $T$, $R$, and the conflicting paths, surfaces `Class: "needs you"` with `Mover: "operator; merge <remote>/<branch> (<short(R)>) into main"`, and resumes normal publication once `R` becomes an ancestor of `refs/heads/main`.

In both options, path-disjoint local items may continue running in their lanes; they are not killed or blamed ([Principle 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)).

---

## 7. Showing the state and its mover while it lasts

To satisfy [Principle 7](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report) and [Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act), `drain.post-landing-action` (or a dedicated `push_state` projection keyed by `action = "push"`) records a structured `kind` alongside `detail`, `remote_tip`, `since_ts` (preserved across updates until `push-ok` clears it), and unpublished counts (`D-RP6`).

### 7.1 Waiting rows and named movers

`appendPostLandingWaiting` in [`waiting.go`](../../tools/tillr-view/waiting.go) maps each non-`ok` push state to the class and mover prefix that `waitingActors` in [`waiting.ts`](../../tools/tillr-view/web/src/models/waiting.ts) routes accurately (`needs you` with `owner; …` or `operator; …` whenever human action is required, `waits on work` only while an active drain step is actually advancing publication):

| State `kind` | `Class` | `Mover` | What `Why` states (alongside real `Since`) |
| :--- | :--- | :--- | :--- |
| `remote-sync-proving` | `waits on work` | `the heavy runner; prove integrated commit <short(M)> before pushing` | Remote `<short(R)>` merged at `<short(M)>`; `<k>` proven / `<n>` total local commits unpublished while heavy proof runs. |
| `remote-sync-conflict` (agent active, [OQ-RP2](#OQ-RP2) A) | `waits on work` | `<sync-item-id>; resolve merge conflict with <remote>/<branch> (<short(R)>)` | Merge with `<short(R)>` conflicted on `<paths>`; `<k>` proven / `<n>` total commits unpublished. |
| `remote-sync-conflict` (escalated or [OQ-RP2](#OQ-RP2) B) | `needs you` | `owner; …` (on owner obstacle) or `operator; merge <remote>/<branch> (<short(R)>) into main` | Merge with `<short(R)>` needs human resolution on `<paths>`; `<k>` proven / `<n>` total commits unpublished. |
| `remote-sync-worktree-dirty` | `needs you` | `operator; commit, stash, or restore clashing paths in the main checkout` | Cannot fast-forward `main` onto integrated `<short(M)>` because the main checkout has uncommitted edits on `<paths>`. |
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

- **Local `main` never moves backwards** (unless the owner explicitly chooses the linear-rebase exception in [OQ-RP1](#OQ-RP1) Option B): every previously observed `refs/heads/main` tip remains an ancestor of `refs/heads/main`.
- **A push never forces the remote:** `git push` never passes `+`, `--force`, or `--force-with-lease` on `<ref>`.
- **A remote force-push is never silently undone:** when $R_{\text{prev}} \not\preceq R$, the drain never merges $T$ with $R$ and pushes $R_{\text{prev}}$ back onto the remote.
- **Nothing unproven reaches the remote in `"green"` mode:** an integrated commit $M$ is published only after `HeavyRunner` advances `refs/tillr/green` to $M$ or a descendant of $M$.
- **Uncommitted edits in the main checkout are never overwritten:** `Trunk.Move` refuses clashing paths cleanly and waits as `remote-sync-worktree-dirty`.
- **Non-diverged failures never claim `"the next green advance"` as their mover** when a green advance cannot clear them.

### 8.2 What this does not do

- Does not poll the remote on a timer when no push is due; remote state is read only when a push is refused or `tillr drain push` is invoked.
- Does not change how fork `patch/<name>` branches are published or guarded by [`bin/patch-branches-publish`](../../bin/patch-branches-publish); it surfaces `patch_moves` holds truthfully instead of masking them as `"waits on work"`.
- Does not merge or rebase branches other than the configured trunk (`refs/heads/main` and its configured `<remote>` `<ref>`).

### 8.3 Risks

| Risk | Mitigation |
| :--- | :--- |
| Clean textual merge introduces a semantic build or test failure between $B..T$ and $B..R$. | In `"green"` mode, $M$ is not pushed until `HeavyRunner` proves it; a failure holds `refs/tillr/green` and files a trunk fixer (`fixer-main-<sha12>`). In `"tip"` mode, the fast gate runs on $M$ before `Trunk.Move`. |
| Operator force-pushes `<remote>` `<ref>` to drop a bad commit, and a running drain re-merges and re-pushes that commit. | `refs/tillr/published/<remote>/<branch>` checks $R_{\text{prev}} \preceq R$ on every refused-push fetch (`D-RP2`); $R_{\text{prev}} \not\preceq R$ stops autopushing as `remote-rewritten` (`needs you`). |
| Conflict-repair worker accidentally reverts or drops non-conflicting files from $R$ or $B..T$. | `D-RP5` locks every path outside the item's fence to the exact blob OID of the mechanical three-way merge of $(T_0, R)$ before the fast gate runs. |
| Frequent remote pushes arrive faster than `HeavyRunner` finishes proving each merge. | `landingAction`'s single replaceable pending slot coalesces triggers, and `HeavyRunner` proves the latest `main` tip in batch rather than proving intermediate merges one by one. |

### 8.4 What done looks like

1. Pushing a non-conflicting commit to `<remote>` `<ref>` from another clone while local landings are unpublished causes the next autopush to detect `[rejected] (fetch first)`, fetch $R$, integrate $R$ onto `main` under `land.lock`, prove the integrated tip, and fast-forward `<remote>` `<ref>` with zero manual intervention.
2. Force-pushing `<remote>` `<ref>` backwards stops autopush immediately as `remote-rewritten` (`Class: "needs you"`) without merging the dropped commit back onto the remote.
3. A local `pre-push` failure, `[remote rejected]` decline, or broker denial records its distinct state and real `needs you` mover in `tillr view waiting` and the Observatory header, with accurate `Since` and unpublished commit counts, and `tillr drain push` publishes immediately once the operator clears the cause.

---

## Decision Ledger

Every row below is an engineering choice made to keep the proposal complete and is marked under [OQ-PR21](../tillr/decision-provenance-review.md#OQ-PR21); the three choices touching repository history shape, conflict-agent spend, and cross-machine commit identities remain open in [Open Questions](#open-questions).

| ID | Decision | Date | Where |
| :--- | :--- | :--- | :--- |
| D-RP1 | **Designer's decision; the owner may overturn this.** Separate refused pushes from failed pushes in two mechanical steps: parse `LC_ALL=C git push --porcelain` stdout for `!` with `[rejected] (fetch first\|non-fast-forward\|stale info)`, then run `git fetch --no-tags -- <remote> +<ref>:refs/remotes/<remote>/<branch>` and classify by `git merge-base --is-ancestor` in the local object store rather than matching stderr substrings. | 2026-11-20 | [Section 3](#3-telling-a-refused-push-apart-from-a-failed-one) |
| D-RP2 | **Designer's decision; the owner may overturn this.** Record the last remote OID this clone published or accepted at `refs/tillr/published/<remote>/<branch>` (ignored by worker `boundary.check`); if a fetched remote tip $R$ does not descend from that OID ($R_{\text{prev}} \not\preceq R$), stop autopushing as `remote-rewritten` (`needs you`) rather than auto-merging a force-pushed remote. | 2026-11-20 | [Section 2](#2-words-and-state-ownership), [Section 3.2](#32-step-2-fetching-the-target-ref-classifies-the-exact-commit-graph) |
| D-RP3 | **Designer's decision; the owner may overturn this.** Keep `tillr-land` as the single writer of `refs/heads/main`: `tillr-drain` fetches into `refs/remotes/<remote>/<branch>` and invokes `tillr-land` under `land.lock` (`Trunk.Move`), holding cleanly as `remote-sync-worktree-dirty` when the main checkout has uncommitted edits on paths touched by $B..R$. | 2026-11-20 | [Section 2](#2-words-and-state-ownership), [Section 4.2](#42-fast-forward-integration-under-the-landing-lock) |
| D-RP4 | **Designer's decision; the owner may overturn this.** Prove integrated commits according to `push.what` before pushing: in `"green"` mode, `HeavyRunner` must advance `refs/tillr/green` to or past the integrated commit before `pushGreen` publishes (using the existing heavy bisection and trunk-fixer flow if red); in `"tip"` mode, `tillr-gate` must pass the fast tier on the integrated tree in a detached checkout before `Trunk.Move` and `pushTrunk`. | 2026-11-20 | [Section 5](#5-proving-the-integrated-trunk-before-pushing) |
| D-RP5 | **Designer's decision; the owner may overturn this.** If automatic conflict repair runs ([OQ-RP2](#OQ-RP2) Option A), enforce a pre-gate tree check requiring every path outside the item's fence to match the mechanical three-way merge blob OID of $(T_0, R)$, and land onto a concurrently advanced `main` ($T_{\text{now}}$) via three-way merge over $T_0$ with parents `-p <T_now> -p <R>`. | 2026-11-20 | [Section 6](#6-when-integrating-the-remote-conflicts) |
| D-RP6 | **Designer's decision; the owner may overturn this.** Project explicit push states with real `Since` timestamps, unpublished proven/total commit counts, and truthful `Class` and `Mover` values into `tillr view waiting`, `tillr view status`, and `ObservatoryHeaderBar`, and add `tillr drain push` (plus automatic re-enqueue when a blocking hold clears) so an idle drained queue can publish without waiting for a new landing. | 2026-11-20 | [Section 7](#7-showing-the-state-and-its-mover-while-it-lasts) |

---

## Open Questions

- 💬 **OQ-RP1: Should a moved remote be merged into `main` or should unpublished local commits rebase onto it?**

  You push a commit from another machine while local landings sit unpublished on `main` ([Section 4](#4-integrating-the-remote-without-moving-the-local-trunk-backwards)). Today autopush fails repeatedly with `[rejected] (fetch first)`.

  - **A — Merge into `main`.** Both local `main` and the remote only fast-forward; landing SHAs, lanes, and the main checkout stay intact, adding one merge commit per concurrent remote push.
  - **B — Rebase onto the remote.** `git log` stays linear, but local `main` rewinds across unpublished commits, resetting the main checkout and remapping landing SHAs.
  - **C — Hold as `needs you`.** Fetch to diagnose, stop autopushing, and wait for a manual merge or rebase.

  <!-- vantage: question id=OQ-RP1 leaning="A — it integrates remote pushes unattended while keeping both local main and the remote strictly fast-forward, without resetting the main checkout or rewriting recorded landing SHAs." -->

  _Leaning:_ A — it integrates remote pushes unattended while keeping both local `main` and the remote strictly fast-forward, without resetting the main checkout or rewriting recorded landing SHAs.

  **Answer:**

  > _(empty — fill in when decided)_

- 💬 **OQ-RP2: When a remote commit conflicts textually with unpublished local commits, should an agent resolve it or ask you?**

  You push an edit on another machine touching the same lines a local unpublished landing just changed, so mechanical merge fails ([Section 6](#6-when-integrating-the-remote-conflicts)).

  - **A — Run one fenced repair worker.** An express `build` run resolves only the conflicting files, passes the gate, code review, and heavy proof before pushing, and escalates to `needs you` on an obstacle or repeat. Costs one run, gate, and review per conflict.
  - **B — Hold immediately as `needs you`.** Spend zero tokens, leave `main` untouched, and name the remote commit and conflicting files for manual merge.

  <!-- vantage: question id=OQ-RP2 leaning="A — routine textual collisions resolve unattended behind the full fence, gate, review, and heavy proof pipeline, while genuine intent contradictions still surface to you via obstacle escalation." -->

  _Leaning:_ A — routine textual collisions resolve unattended behind the full fence, gate, review, and heavy proof pipeline, while genuine intent contradictions still surface to you via obstacle escalation.

  **Answer:**

  > _(empty — fill in when decided)_

- 💬 **OQ-RP3: Should commits fetched from the configured upstream branch pass `CheckIdentities` when pushed from another machine?**

  Your work and personal machines use different git emails, and you push from your laptop while your desktop runs the drain ([Section 4.3](#43-foreign-commit-identities-on-the-upstream-branch)). Today integrating that commit freezes local landings on `trunk-foreign-identity`.

  - **A — Trust commits on the fetched upstream branch.** Cross-machine pushes integrate without freezing landings, matching [`git-identity.test.py`](../../tests/git-identity.test.py), while commits created locally are still checked.
  - **B — Hold on `trunk-foreign-identity`.** Any merged remote commit with another email pauses local landings until you run `tillr drain accept-identity`.

  <!-- vantage: question id=OQ-RP3 leaning="A — a commit already on the configured upstream branch passed push credentials on the machine that published it, and requiring accept-identity would freeze cross-machine work between your work and personal machines." -->

  _Leaning:_ A — a commit already on the configured upstream branch passed push credentials on the machine that published it, and requiring `accept-identity` would freeze cross-machine work between your work and personal machines.

  **Answer:**

  > _(empty — fill in when decided)_
