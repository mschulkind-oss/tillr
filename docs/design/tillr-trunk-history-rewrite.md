---
title: "Carrying the store, unlanded candidates, and the proved pointer across a trunk history rewrite"
status: in-review
stage: DESIGN
updated: 2026-11-26
tags: [tillr, drain, item, gate, store, git, design]
summary: "Weighs three responses to pre-publish trunk history rewrites after four content-removal rewrites on 2026-11-24 and 2026-11-25 forced 4m 42s store resets, destroyed run/spend/landing/alert history, and cost five finished candidates their gate standing despite an identical trunk tree. States exact proof transfer rules when old_tip^{tree} == new_tip^{tree}, specifies both in-place commit-map carry-over (tillr drain rewrite-history) and supported queue export/import (tillr item export / import), settles nothing only the owner can decide (OQ-HR1 to OQ-HR3), and records D-HR1 to D-HR6 as the designer's decisions."
next: "Rule OQ-HR1, OQ-HR2, and OQ-HR3, confirm roadmap placement under Section 4 of docs/tillr/roadmap.md, and then file and build the slices of Section 8."
depends-on:
  - ../reference/gate.md
  - ../reference/tillr-queue-persistence.md
  - ../reference/authority-not-approval.md
  - landing-principles.md
  - operator-decisions-inside-tillr.md
  - ../tillr/decision-provenance-review.md
vantage:
  status-chip: true
---

# Carrying the store, unlanded candidates, and the proved pointer across a trunk history rewrite

**Status:** Not built. Nothing in Go code, configuration, or CLI flags changes in this commit. Only the dated statements quoted in [§2](#2-governing-rulings) are owner rulings; the 2026-11-25 rewrite measurement in [§1](#1-the-case-and-why-existing-rewrite-machinery-failed) comes from a filed command-line request; [OQ-HR1](#OQ-HR1), [OQ-HR2](#OQ-HR2), and [OQ-HR3](#OQ-HR3) await the owner's ruling; and `D-HR1` to `D-HR6` in the [Decision Ledger](#decision-ledger) are the designer's decisions, which the owner may overturn ([decision provenance review §6](../tillr/decision-provenance-review.md#6-how-this-stops-happening)).

> **In short.** When a pre-publish history rewrite scrubs content across commits on the **trunk** (the shared main branch that landings squash onto), `proveRewrite` and `tillr drain accept-rewrite` both fail because interior patches change, empty commits drop, and pinning pre-rewrite refs keeps scrubbed Git blobs reachable. A proof of the old tip when `old_tip^{tree} == new_tip^{tree}` proves **the tree, not the history**: every tree-scoped check carries full proof strength (and already hits `tillr-gate`'s content-addressed result cache), whereas commit-identity checks, commit-SHA citations in tracked Markdown, altered interior commits, and commit-OID-keyed `green-<oid>.json` sidecars do not transfer from tree equality alone. By the designer's leaning in [OQ-HR1](#OQ-HR1), tillr should offer **both** an in-place commit-map command (`tillr drain rewrite-history --commit-map`) that preserves `.tillr/state.db`, drops pre-rewrite ref pins, carries waiting candidates straight to the fast gate without worker runs, and carries tree-identical `refs/tillr/green` receipts, **and** a supported queue export/import (`tillr item export` / `tillr item import`) so deliberate store resets never scrape SQLite tables.

**Why it matters.** Four trunk rewrites in two days on 2026-11-24 and 2026-11-25 forced the operator to stop the loop, scrape internal SQLite tables, wipe `.tillr/state.db` and every **lane** (a leased worker slot and its Git worktree), and refile the queue by hand (`4m 42s` even when scripted) — losing two live runs, the entire append-only record of events, spend, landings, and alerts, and the **gate** (pre- and post-merge check runner) standing of five finished **candidates** (committed worker branches awaiting gate verification and squash landing).

**The shape.** Audit why today's `proveRewrite` and `accept-rewrite` paths break on content-scrubbing rewrites ([§1](#1-the-case-and-why-existing-rewrite-machinery-failed)), establish exact proof value when `old_tip^{tree} == new_tip^{tree}` ([§3](#3-what-a-proof-of-the-old-tip-is-worth-when-the-new-tip-has-the-same-tree)), weigh the three product options ([§4](#4-weighing-the-three-options)), specify in-place store, lane, candidate, ref, and `refs/tillr/green` carry-over ([§5](#5-in-place-store-and-candidate-carry-over)), and specify supported queue export, import, and new-store candidate carry-over ([§6](#6-supported-queue-export-import-and-new-store-carry-over)).

**Start at:** [§3](#3-what-a-proof-of-the-old-tip-is-worth-when-the-new-tip-has-the-same-tree) and [§4](#4-weighing-the-three-options), then [§5](#5-in-place-store-and-candidate-carry-over) and [§6](#6-supported-queue-export-import-and-new-store-carry-over).

**Needs your ruling:** [OQ-HR1](#OQ-HR1) (in-place commit-map rewrite plus queue export/import, in-place rewrite alone, or new-store-only with export and candidate carry-over), [OQ-HR2](#OQ-HR2) (whether `refs/tillr/green` carries immediately on tree equality plus fast history checks or waits for a heavy pass), and [OQ-HR3](#OQ-HR3) (whether pre-rewrite `refs/tillr/*` refs are dropped by default so `git gc` purges scrubbed blobs).

**Awaits your review:** the designer's decisions `D-HR1` to `D-HR6` in the [Decision Ledger](#decision-ledger), and proposed placement under Section 4 ("A drain that runs unattended") of [`docs/tillr/roadmap.md`](../tillr/roadmap.md).

**Reads with:** [the gate reference](../reference/gate.md#remembered-passes), [queue persistence](../reference/tillr-queue-persistence.md#what-lives-in-git-vs-the-store), [landing principles](./landing-principles.md), [operator decisions inside tillr](./operator-decisions-inside-tillr.md#1-the-ruling), and [authority, not approval](../reference/authority-not-approval.md#what-owner-only-is-for).

---

## 1. The case and why existing rewrite machinery failed

On 2026-11-25, a filed command-line request recorded what happened when this repository's trunk history was rewritten four times across two days to remove unwanted content from historical commits before publishing:

- Every rewrite changed the commit SHA of the earliest edited commit and every descendant commit up to `refs/heads/main`, while leaving the trunk tip's root Git tree byte-identical (`old_tip^{tree} == new_tip^{tree}`) because the unwanted content had already been removed before the tip.
- On the fourth rewrite, even with the recovery steps scripted, resetting took **`4 minutes 42 seconds`** from stopping the **drain** (tillr's scheduling, gating, and landing loop) to seven busy lanes.
- Despite scripting and carrying the five waiting candidate commits across the rewrite under `refs/keep/*`, three classes of state were still lost:
  1. **Two live worker runs** interrupted and destroyed when `.tillr/lanes/` and `.tillr/state.db` were deleted;
  2. **The entire store execution record** — `events`, per-run token and USD spend (`runs`, `drain_pass_spend`), `landings`, and `alert_episodes` / `watch_silences` / `watch_rule_baselines`; and
  3. **The gate standing of five finished candidates** waiting at the gate, each of which required a fresh LLM worker run to re-adopt its `refs/keep/*` commit before `tillr-drain` would gate and land it.

### 1.1 What exists on `HEAD` (`bc431c2541fd`)

Tillr already has partial trunk-rewrite machinery built for `git pull --rebase` and `git commit --amend`:

- **Automatic rewrite absorption (`Loop.observeTrunkLocked` in [`tools/tillr-drain/drain/trunk_history.go`](../../tools/tillr-drain/drain/trunk_history.go) and `Loop.proveRewrite` in [`tools/tillr-drain/drain/trunk_rewrite.go`](../../tools/tillr-drain/drain/trunk_rewrite.go)):** When `trunk_history.observed_tip` is no longer an ancestor of `main`, the loop tries `Trunk.IdentityRewrite` ([`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go), matching identical trees and commit messages where only author/committer metadata changed) or `proveRewrite` (1-to-1 `git patch-id --stable` matching across `merge-base(lost, tip)..lost`), writes `old_sha -> new_sha` rows into `trunk_rewrites` ([`tools/tillr-drain/heavy/rewrites.go`](../../tools/tillr-drain/heavy/rewrites.go)), moves `refs/tillr/green` and `refs/tillr/landed/<run_id>`, and pins `refs/tillr/trunk-history/accepted/<lost>`.
- **Manual rewrite acceptance (`AcceptRewrite` in [`tools/tillr-drain/fixer/trunk_rewrite_accept.go`](../../tools/tillr-drain/fixer/trunk_rewrite_accept.go), CLI `tillr drain accept-rewrite`):** Clears a `trunk-rewritten` hold only when both `--lost` and `--current` resolve as commit objects in `.git/objects/` and either `lost^{tree} == current^{tree}` **with equal commit counts** on both sides of `git rev-list --left-right --count lost...current` (`sides[0] == sides[1]`), or `git cherry` matches 100% of lost commits by stable patch ID.

### 1.2 Five exact failure modes on a pre-publish content scrub

1. **Pinning pre-rewrite commits defeats a content scrub — or fails once old objects are pruned.** Both `observeTrunkLocked` and `AcceptRewrite` create `refs/tillr/trunk-history/accepted/<lost>` ("so the history the operator replaced stays readable after the lost ref is cleared and reflogs expire"), while `refs/tillr/runs/*`, `refs/tillr/lanes/*`, `refs/tillr/preserved/*`, `refs/tillr/ended/*`, and unmapped `refs/tillr/landed/*` continue pointing at pre-rewrite commits. Keeping those refs reachable prevents `git gc --prune=now` from purging the scrubbed blobs before publishing. Conversely, if a rewrite tool (`git filter-repo`) has already rewritten refs and pruned the old objects, `AcceptRewrite` immediately fails on `git rev-parse --verify --quiet <lost>^{commit}`.
2. **Altered interior patches and pruned empty commits fail both `proveRewrite` and `AcceptRewrite`.** Scrubbing a file or blob across history changes the diff (and `git patch-id --stable`) of every commit that touched that content, drops commits that *only* touched that content (`git filter-repo --prune-empty=always`), and removes any common ancestor (`git merge-base`) when the root commit is rewritten. As a result, `proveRewrite` leaves `Unmatched` non-empty, and `AcceptRewrite` fails `sides[0] != sides[1]` on `git rev-list --left-right --count lost...current` even when `lost^{tree} == current^{tree}`.
3. **Interior `refs/tillr/green` commits fail `carryGreen`.** In `trunk_rewrite_accept.go`, `carryGreen` resolves `current~depth` at the exact first-parent distance `depth` below `lost` and requires both `green^{tree} == carried^{tree}` and equal history lengths (`git rev-list --left-right --count green...carried`). Whenever `refs/tillr/green` lagged behind the tip at an interior commit that still held the scrubbed file — or whenever an empty commit between `green` and `tip` was dropped — `carryGreen` refuses.
4. **`trunk_rewrites` aliases past landings only, never waiting candidates, lane worktrees, rolling watermarks, or gate verdicts.** Today `trunk_rewrites` is consulted by `containsCommit` and `readLandedBlockerCommits` ([`tools/tillr-drain/drain/lane_base.go`](../../tools/tillr-drain/drain/lane_base.go)), `CurrentLanding` ([`tools/tillr-item/item/current_landing.go`](../../tools/tillr-item/item/current_landing.go)), `landingRecorded` and `storedLanding` ([`tools/tillr-land/land/landing.go`](../../tools/tillr-land/land/landing.go)), and `SweepRefs` ([`tools/tillr-drain/drain/refs.go`](../../tools/tillr-drain/drain/refs.go)). It is **never** consulted for:
   - `pendingWork.BaseSHA` ([`tools/tillr-drain/drain/pass.go`](../../tools/tillr-drain/drain/pass.go)) or `Candidate{BaseSHA, HeadSHA, ObservedTrunk, WorkerHeadSHA}` ([`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go)), which are folded from append-only `events`;
   - `<runDir>/gate.verdict.json`, where `decide` in [`tools/tillr-land/land/landing.go`](../../tools/tillr-land/land/landing.go) refuses with `ReasonVerdictMismatch` whenever `Verdict.BaseSHA != req.BaseSHA` or `Verdict.TreeSHA != head^{tree}`;
   - `rolling_state.watermark_sha` and `last_tip_sha` ([`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go)); or
   - `.tillr/lanes/lane-*/worktree` checkouts and `refs/tillr/lanes/*` / `refs/tillr/runs/*` refs.
   Even when the operator rewrote the five candidate commits under `refs/keep/*`, no command could re-bind `(ItemID, RunID)` in `pendingWork` to `(B_new, H_new)` and queue them directly at `launchExamination`, forcing five redundant worker runs.
5. **Store-owned queue state has no supported export or import.** Running `rg -n 'item export|item import|ExportQueue' tools/` returns nothing. While `tillr item sync` rebuilds open Markdown-backed items from `docs/**/*.md` ([queue persistence](../reference/tillr-queue-persistence.md#what-lives-in-git-vs-the-store)), every **store-declared item** (an item filed directly in `.tillr/state.db` with no Markdown block) and every runtime queue relationship lives solely in SQLite tables under [`tools/tillr-item/item/`](../../tools/tillr-item/item/): `request_words` (`request.go`), `idea_intake` (`idea.go`, plus `<store-dir>/intake/<id>.txt`), `item_splits` (`split.go`), `comment_batches` (`comments.go`), `decisions`, `item_edges`, `workflow_selections`, and hand-dragged `items.rank` (`schema.go`). Deleting `.tillr/state.db` forced the operator to read private SQLite tables directly, against the consumer surface contract ([separable parts §2.2](./tillr-separable-parts.md#22-what-is-internal-and-may-change-daily)).

---

## 2. Governing rulings

Only dated owner rulings govern this design as settled policy:

1. **Fix the machinery; what the operator can settle moves inside tillr ([Landing Principle 9](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work), [Landing Principle 16](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr), [`R-OD1`](./operator-decisions-inside-tillr.md#1-the-ruling), 2026-11-21).** Scraping SQLite tables, hand-carrying `refs/keep/*` branches, and burning five fresh LLM worker runs on already-finished candidates are machinery gaps to close inside tillr.
2. **Tree-check passes are remembered by content fingerprint, never by commit SHA ([`D-GR8`](../reference/gate.md#D-GR8), 2026-11-15), and tillr deploys from `refs/tillr/green` ([`D-GR5`](../reference/gate.md#D-GR5), 2026-11-15).** Recorded in [the gate reference](../reference/gate.md#remembered-passes) and [after-merge proof](../reference/gate.md#after-merge-proof-and-the-green-pointer): `<git-common-dir>/tillr-gate/results/` keys `scope: "tree"` passes on declaration bytes, read file digests, tool versions, and needed refs, while `refs/tillr/green` marks the newest trunk commit whose after-merge proof passed.
3. **Commit identities on this repository ([`D-GR18`](../reference/gate.md#D-GR18), 2026-11-20) and lasting commit messages ([`OQ-PR1`](../tillr/decision-provenance-review.md#decision-ledger), 2026-11-20).** Rewritten commits in this repository must still satisfy [`tools/gate/allowed-commit-identities.json`](../../tools/gate/allowed-commit-identities.json), and carried commits never gain item IDs or ephemeral tillr trailers.
4. **Queue order lives in the database ([`OQ-PR13`](../tillr/decision-provenance-review.md#decision-ledger), 2026-11-20) and decision provenance ([`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger), [`OQ-PR22`](../tillr/decision-provenance-review.md#decision-ledger), `D-PR1`, `D-PR2`, 2026-11-18 to 2026-11-20).** Under [`OQ-PR13`](../tillr/decision-provenance-review.md#decision-ledger), hand-dragged queue order is `"database only"` (never a tracked file in Git). Under [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger) and [authority, not approval](../reference/authority-not-approval.md#what-owner-only-is-for), custody and store-retention trade-offs go to the owner ([OQ-HR1](#OQ-HR1) to [OQ-HR3](#OQ-HR3)), while engineering mechanisms (`D-HR1` to `D-HR6`) are marked as the designer's decisions for the owner to keep or overturn.

---

## 3. What a proof of the old tip is worth when the new tip has the same tree

Let $G_{\text{old}}$ be a proved trunk commit (`refs/tillr/green == G_old`, including the common case $G_{\text{old}} = T_{\text{old}}$) and $G_{\text{new}}$ be its replacement commit on the rewritten trunk with **identical root Git tree OID** (`G_old^{tree} == G_new^{tree}`). Because a Git SHA-1/SHA-256 tree OID recursively binds every tracked path, file mode, symlink target, and blob OID in the repository, tree equality establishes an exact boundary between what $G_{\text{old}}$'s proof still proves and what it does **not** prove:

| Surface | Gate / reader scope | Worth of $G_{\text{old}}$'s proof when $G_{\text{old}}^{\{\text{tree}\}} = G_{\text{new}}^{\{\text{tree}\}}$ | Why, and what still has to run or be written |
| :--- | :--- | :--- | :--- |
| **Deterministic tree checks** (`go-build`, `format`, `pack-sync`, `scratch`, `design-citations`, `tillr-docs`, `observatory-web`, and all `go-test-*` module suites) | `"scope": "tree"` ([gate reference](../reference/gate.md#change-checks-and-tree-checks)) | **Full proof value** (when declared `tools` versions and `needs` refs are unchanged) | Every tree check runs against a detached checkout of the tree alone; none of this repository's tree checks inspects commit history (`design-citations` in `tools/gate/tree_common.go` checks frontmatter paths and headings in the tree). Moreover, `tillr-gate`'s `treeMemory` (`D-GR8`) already caches every passed check by content `treeFingerprint`, independent of commit SHA. |
| **Commit authorship and committer metadata** (`commit-identity` in `tools/tillr/gitidentity/`) | `"scope": "change"` | **Zero proof value** | `git filter-repo`, `git rebase`, or `git commit-tree` can rewrite author or committer names/emails on rewritten commits while keeping `G_new^{tree} == G_old^{tree}` byte-identical. Fast `commit-identity` must re-check the rewritten commit range. |
| **Commit-SHA citations in tracked Markdown** (`citations`, `unreachableCitations` in `tools/gate/change_common.go`) | `"scope": "change"` (added lines) | **Zero proof value — silent rot hazard** | Any commit SHA inside the rewritten range that a tracked Markdown document cited as landed proof satisfied `git merge-base --is-ancestor <sha> HEAD` on $T_{\text{old}}$ and **no longer satisfies it on $T_{\text{new}}$**. Because `citations` checks added lines on diffs, orphaned SHAs sit silently in unchanged files on $T_{\text{new}}$ despite `T_old^{tree} == T_new^{tree}` and fail the next candidate that moves or edits those lines. |
| **Interior commits ($C'_i \prec T_{\text{new}}$) and lagged `refs/tillr/green`** | History bisection (`SearchFirstRed`) and interior `green` | **Zero proof value where trees differ** | In the 2026-11-25 case, unwanted content was added in an interior commit and deleted before $T_{\text{old}}$. Thus $T_{\text{new}}^{\{\text{tree}\}} = T_{\text{old}}^{\{\text{tree}\}}$ holds at the tip **while interior commits have altered trees ($C'_i{}^{\{\text{tree}\}} \neq C_i{}^{\{\text{tree}\}}$)**. A proof of $T_{\text{old}}$ proves nothing about whether $C'_i$ compiles or passes tests during culprit bisection; and if `refs/tillr/green` lagged at an interior $G_{\text{old}}$ with $G_{\text{old}}^{\{\text{tree}\}} \neq G_{\text{new}}^{\{\text{tree}\}}$, $G_{\text{old}}$'s proof does **not** carry to $G_{\text{new}}$. |
| **OID-keyed proof receipts and build stamps** (`<git-common-dir>/tillr-gate/green-<oid>.json` and `greenRevision`) | `ReadGreenProofFile` (`fixer_resolve.go`), `readGreenProof` (`tools/tillr/watch/git.go`), `just tillr-build` | **Unusable until re-bound to $G_{\text{new}}$** | `readGreenProof` and `judgeTrunkChecks` (`tools/tillr-drain/drain/trunk.go`) require `green-<G_new>.json` with `commit == G_new`; moving `refs/tillr/green` alone leaves `green_proof:refs/tillr/green` unrecorded in `tillr-watch`, and binaries built from $G_{\text{old}}$ embed stale `greenRevision = G_old`. |

**Summary rule.** When $G_{\text{old}}^{\{\text{tree}\}} = G_{\text{new}}^{\{\text{tree}\}}$ and `G_new` is an ancestor of `T_new`, $G_{\text{old}}$'s proof proves every tree-scoped check on $G_{\text{new}}$ and nothing about the rewritten commit chain that produced that tree. Carrying `refs/tillr/green` directly to $G_{\text{new}}$ is sound if and only if fast history verification (`commit-identity` over the rewritten range) passes, `green-<G_new>.json` is written (`carried_from: G_old`), and any tracked Markdown SHA citations orphaned by the rewrite are surfaced ([OQ-HR2](#OQ-HR2)). Even if `refs/tillr/green` is instead cleared to wait for the heavy runner ([OQ-HR2](#OQ-HR2) Option B), `treeMemory` already remembers every unchanged tree check by `treeFingerprint`, so the heavy pass completes from cache in seconds rather than re-running multi-minute test suites.

---

## 4. Weighing the three options

The request asks tillr to weigh three options:

1. **Option A — In-place commit-map rewrite (`tillr drain rewrite-history --commit-map`) *plus* supported queue export and import (`tillr item export` / `tillr item import`)** *(designer's leaning in [OQ-HR1](#OQ-HR1))*.
2. **Option B — In-place commit-map rewrite alone** (no queue export/import).
3. **Option C — A ruling that a trunk rewrite always starts a new store**, shipping only supported queue export/import and candidate carry-over into the fresh store.

| Dimension (measured against 2026-11-25) | Status quo (hand script) | Option A: In-place `--commit-map` + queue `export`/`import` | Option B: In-place `--commit-map` only | Option C: New store always (`export`/`import` + candidate carry-over) |
| :--- | :--- | :--- | :--- | :--- |
| **Recovery wall time (stop to busy lanes)** | `4m 42s` scripted | Seconds (single atomic CLI pass + fast-gate re-entry) | Seconds | Tens of seconds (`store init` + `item sync` + `item import` + lane worktree creation) |
| **`events`, token/USD spend, `landings`, and `alert_episodes` history** | **Lost** on every rewrite | **Kept intact** (`events` stays append-only; `trunk_rewrites` maps old SHAs) | **Kept intact** | **Lost on every rewrite** (four full history wipes in 48 hours on 2026-11-24..25) |
| **`CurrentLanding`, auto-revert lineage, and `heavy-misses` across the rewrite** | Broken (`landings` wiped) | **Preserved** via `trunk_rewrites` | **Preserved** via `trunk_rewrites` | Broken (`landings` wiped) |
| **Five waiting candidates at the gate** | Lost standing; required `5` new LLM worker runs | **Carried directly to fast gate** (`0` worker runs unless `merge-tree` or a check fails) | **Carried directly to fast gate** (`0` worker runs) | **Carried to fast gate** via `export`/`import` candidate bundle (`0` worker runs) |
| **Two live runs at loop stop** | Destroyed with lanes | Process stopped; dirty worktree snapshotted and replayed onto `B_new` in `refs/tillr/preserved/<run>` | Process stopped; dirty worktree snapshotted and replayed onto `B_new` | Process stopped; requires exporting `preserved/` refs into the new store |
| **Deliberate store reset (schema reset, corruption, moving unshared items)** | Scrapes private SQLite tables by hand | **Supported** via `tillr item export` / `import` (untracked JSON file, honoring [`OQ-PR13`](../tillr/decision-provenance-review.md#decision-ledger)) | **Still scrapes SQLite tables by hand** | **Supported** via `tillr item export` / `import` |
| **Scrubbed Git blobs eligible for `git gc`** | Yes (after deleting `.tillr/lanes` and unreferenced refs) | **Yes** (drops `accepted/*` and pre-rewrite `refs/tillr/*`; see [OQ-HR3](#OQ-HR3)) | **Yes** | **Yes** (once old `refs/tillr/*` refs are swept) |
| **Engineering surface** | None | `rewrite-history` + `drain.candidate-carried` + `item export`/`import` | `rewrite-history` + `drain.candidate-carried` | `item export`/`import` + cross-store candidate re-creation |

### Why Option A beats both Option B and Option C

- **Why in-place carry-over beats wiping the store on rewrites (Option A/B over Option C):** Tillr *already* pays the architectural cost of `trunk_rewrites` in `tools/tillr-drain/heavy/rewrites.go`, `tools/tillr-land/land/landing.go`, and `tools/tillr-item/item/current_landing.go`. Starting a fresh store on every rewrite saves zero ongoing query complexity unless `trunk_rewrites` were ripped out of `pull --rebase` as well, while permanently destroying `events`, cumulative model token/USD telemetry, `landings` (needed by `CurrentLanding`, blocker verification, and `tillr view heavy-misses`), and `tillr-watch` episode/silence/baseline history — four times in two days during pre-publish cleanup.
- **Why queue export/import is still needed alongside in-place carry-over (Option A over Option B):** Operators reset `.tillr/state.db` for reasons other than Git history rewrites (retiring a multi-week epoch, recovering from disk corruption, or testing schema changes). Without `tillr item export` and `tillr item import`, every store reset forces an agent or operator to query internal SQLite tables (`request_words`, `idea_intake`, `item_splits`, `decisions`, `items.rank`), violating [separable parts §2.2](./tillr-separable-parts.md#22-what-is-internal-and-may-change-daily).

Because choosing between carrying `.tillr/state.db` across history rewrites and treating a rewrite as a clean store epoch touches repository history and operator-visible records, [OQ-HR1](#OQ-HR1) leaves the choice of Option A, B, or C to the owner. [§5](#5-in-place-store-and-candidate-carry-over) and [§6](#6-supported-queue-export-import-and-new-store-carry-over) specify **both** mechanisms in full so neither path has an unspecified gap whichever option the owner rules.

---

## 5. In-place store and candidate carry-over

All mechanism choices in this section (`D-HR1` to `D-HR4`) are the designer's decisions and may be overturned by the owner.

### 5.1 Preconditions and commit-map contract (`tillr drain rewrite-history`) (`D-HR1`)

By the designer's decision (`D-HR1`), a dedicated drain face (`tillr drain rewrite-history`, whose exact flags `--help` will own) carries `.tillr/state.db`, lanes, candidates, and `refs/tillr/*` across a trunk rewrite in one atomic operation:

1. **Quiescent loop and exclusive locks:** `rewrite-history` verifies no live `tillr-drain` loop or active worker lease holds `.tillr/state.db` (`drain_sessions` and `lanes`), acquires `land.lock` (`lockTrunkHistory`), and performs all Git object creation and ref inspection before opening the store write transaction (`s.WriteTx`), matching `observeTrunkLocked` so `land.lock` and SQLite never deadlock.
2. **Commit-map format (`--commit-map <path>`):** Reads whitespace-separated `<old-sha> <new-sha>` pairs line by line — the exact file `git filter-repo` writes to `.git/filter-repo/commit-map` (skipping the optional `old new` header line):
   - **Root rewrites:** Requires no `git merge-base` between `T_old` and `T_new`, so rewriting the repository's initial commit works identically to rewriting the last five commits.
   - **Pruned empty commits (`many-to-one` and `0000...`):** When a commit touched *only* scrubbed content, `git filter-repo --prune-empty=always` maps `old_sha` either to its surviving parent `new_parent_sha` or (when pruned before the first kept root) to the all-zero OID. `rewrite-history` maps pruned commits onto their nearest surviving first-parent replacement on `T_new` (recording `evidence = "rewrite-history:pruned-to-ancestor"` in `trunk_rewrites` when non-zero) so lane bases and historical `landings` rows resolve to the exact tree state that survived.
   - **Pre-pruned repositories:** Does **not** require `old_sha` to still exist in `.git/objects/` when `new_sha` is already given by `--commit-map` (or `--keep-refs`).

### 5.2 Store commit state without mutating `events` (`D-HR1`)

Inside `s.WriteTx`:

- **`events` remains strictly append-only:** No historical row in `events` is ever updated or deleted.
- **Transitive `trunk_rewrites` upsert:** Across four rewrites in two days ($S_0 \to S_1 \to S_2 \to S_3 \to S_4$), `rewrite-history` first updates existing targets (`UPDATE trunk_rewrites SET new_sha = ?, recorded_at = ?, evidence = ? WHERE new_sha = ?`) and then upserts each new `(old_sha, new_sha)` pair, clearing `trunk_rewrite_unmatched`. Both `RewriteResolve` (`old -> latest`) and recursive `RewriteAliases` (`latest -> all prior SHAs`) in [`tools/tillr-drain/heavy/rewrites.go`](../../tools/tillr-drain/heavy/rewrites.go) and [`tools/tillr-land/land/landing.go`](../../tools/tillr-land/land/landing.go) continue to resolve every historical landing in one lookup.
- **Trunk history and rolling watermarks:** Sets `trunk_history(id = 1, observed_tip = T_new, lost_tip = '')`, updates `refs/tillr/trunk-history/observed` to `T_new`, deletes `refs/tillr/trunk-history/lost`, and translates `rolling_state.watermark_sha` and `last_tip_sha` ([`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go)) through the commit map when their replacement is an ancestor of `T_new` with an identical tree (otherwise rewinding `watermark_sha` to the newest unrewritten ancestor, or `""` on a root rewrite).
- **Audit receipt:** Appends one `drain.trunk-history-rewritten` event (`object_type = "trunk"`, `object_id = &T_new`) recording `old_tip`, `new_tip`, `old_tree`, `new_tree`, `same_tip_tree`, `mapped_commits`, `carried_candidates`, `conflicted_candidates`, `green_from`, `green_to`, `orphaned_doc_citations`, `by`, and `why`.

### 5.3 Carrying waiting candidates and interrupted lanes without worker runs (`D-HR2`)

By the designer's decision (`D-HR2`), `rewrite-history` carries every unfinished `pendingWork` entry folded by `readPendingHistory` ([`tools/tillr-drain/drain/pass.go`](../../tools/tillr-drain/drain/pass.go)):

1. **Waiting candidates (`work.Candidate != nil`, the `5` candidates in the 2026-11-25 case):**
   - Resolve `B_new = resolveOnTrunk(work.Candidate.BaseSHA, commitMap, T_new)`.
   - Compute `H_new` by the first rule that applies:
     1. **Already rewritten in map or `--keep-refs`:** If `work.Candidate.HeadSHA` appears in `--commit-map` (for example, when `git filter-repo` ran over all refs including `refs/tillr/runs/<run_id>` or `refs/keep/*`) or `--keep-refs <prefix>` holds a commit for `<run_id>` whose parent is an ancestor of `T_new`, adopt that commit as `H_new` (updating its parent to `B_new` via `git commit-tree` if `B_old` mapped to `B_new` and the kept commit was rebased onto `T_new` instead).
     2. **Three-way plumbing replay before prune:** Otherwise, when `B_old` and `H_old` are still in `.git/objects/`, replay `B_old..H_old` onto `B_new` in memory using `git merge-tree --write-tree --merge-base=<B_old> <B_new> <H_old>` and `git commit-tree` preserving `H_old`'s message, author, author date, and committer. (When `work.Candidate.RetirementTreeSHA != ""`, replay `WorkerHeadSHA` first and re-apply `RetireItemBlock` on top so `WorkerHeadSHA` and `HeadSHA` stay paired.)
   - **Folded carry event (`drain.candidate-carried`):** Add `candidateCarriedKind = "drain.candidate-carried"` to `pendingHistoryKinds` in `tools/tillr-drain/drain/pass.go`. Folding `drain.candidate-carried` sets:
     - `work.BaseSHA = B_new`, `work.Candidate.BaseSHA = B_new`, `work.Candidate.HeadSHA = H_new`, `work.Candidate.WorkerHeadSHA = W_new`, and `work.Candidate.ObservedTrunk = T_new`;
     - `work.Gated = false`, `work.Examining = false`, and `work.Examiner = nil`, while renaming any pre-rewrite `<runDir>/gate.verdict.json` to `gate.verdict.pre-rewrite.json` because `decide` in `tools/tillr-land/land/landing.go` refuses any verdict not bound to `(B_new, H_new^{tree})` (`ReasonVerdictMismatch`).
   - **Ref and lane update:** Point `refs/tillr/runs/<run_id>` at `H_new`, and if the candidate still holds `.tillr/lanes/<lane_id>/worktree`, reset that worktree and `refs/tillr/lanes/<lane_id>` to `H_new`.
   - **Zero wasted worker runs:** When `tillr-drain loop` restarts, every carried candidate immediately enters `launchExamination` (the fast landing gate) — **never dispatching an LLM worker**. When `H_new^{tree} == H_old^{tree}`, all `scope: "tree"` checks hit `treeMemory` in milliseconds and only fast `scope: "change"` checks run on `B_new..H_new`, stamping a valid `gate.verdict` bound to `(B_new, H_new^{tree})` ready for `Trunk.Squash`.
   - **Conflict handling:** If `git merge-tree --write-tree` reports a merge conflict (because the candidate itself edited lines that the content scrub altered or deleted), `rewrite-history` records `drain.candidate-rewrite-conflict` (`reason = "rewrite-merge-conflict"`) and resets the candidate's base to `T_new` so only that conflicted item returns to a worker.
2. **Interrupted live runs (the `2` live runs in the 2026-11-25 case):**
   - Stopping `tillr-drain` (`tillr-drain stop`) before rewriting Git history necessarily stops active worker processes (`drain.stop-requested`), because a live worker cannot keep committing onto objects being rewritten underneath its worktree.
   - Instead of deleting `.tillr/lanes/` and discarding their in-flight edits, `rewrite-history` snapshots any uncommitted or committed worktree delta above `B_old`, replays that tree onto `B_new` via `git merge-tree --write-tree`, writes `refs/tillr/preserved/<run_id>` parented on `B_new` (`D-HR2`, respecting pre-add untracked bounds once [ended-item saves](./tillr-ended-item-saves.md#5-bounding-untracked-files-before-git-hashes-them) lands), and resets the lane worktree to `T_new` so the next worker run receives its own replayed progress in `preservedFacts`.
3. **Idle lanes:** Every idle `.tillr/lanes/lane-*/worktree` checkout and `refs/tillr/lanes/<lane_id>` ref is reset cleanly to `T_new`.

### 5.4 Carrying `refs/tillr/green` and `green-<oid>.json` (`D-HR3`, [`OQ-HR2`](#OQ-HR2))

By the designer's decision (`D-HR3`), `heavy.GreenProof` in [`tools/tillr-drain/heavy/status.go`](../../tools/tillr-drain/heavy/status.go) gains two additive fields:

```go
type GreenProof struct {
	Commit      string    `json:"commit"`
	TreeSHA     string    `json:"tree_sha,omitempty"`
	ProvedAt    time.Time `json:"proved_at"`
	Passed      []string  `json:"passed,omitempty"`
	CarriedFrom string    `json:"carried_from,omitempty"`
}
```

Recording `TreeSHA` when `PublishHeavyJSON` writes `<git-common-dir>/tillr-gate/green-<tip>.json` lets `rewrite-history` compare `green_old.TreeSHA` against `G_new^{tree}` even when `git filter-repo` has already pruned `G_old` from `.git/objects/`.

When `G_old` maps to `G_new` on `T_new`:

- **If `old_tree != G_new^{tree}`** (for example, `refs/tillr/green` lagged at an interior commit whose tree held the scrubbed file): `G_old`'s proof does **not** transfer ([§3](#3-what-a-proof-of-the-old-tip-is-worth-when-the-new-tip-has-the-same-tree)). `rewrite-history` rewinds `refs/tillr/green` to the newest unrewritten ancestor of `T_new` that still has a valid `green-<oid>.json` (or deletes `refs/tillr/green` if the root was rewritten), leaving the next heavy pass to prove `T_new` (hitting warm `treeMemory` for every unchanged check when `T_new^{tree} == T_old^{tree}`).
- **If `old_tree == G_new^{tree}`:** Under [OQ-HR2](#OQ-HR2) Option A (designer's leaning), `rewrite-history` runs fast commit-history verification (`commit-identity` over the rewritten commits reachable from `G_new`), scans tracked `docs/**/*.md` and `README.md` for commit SHAs in the rewritten `old_sha` set that are no longer ancestors of `T_new` (reporting them in `orphaned_doc_citations`), writes `green-<G_new>.json` (`Commit: G_new`, `TreeSHA: G_new^{tree}`, `ProvedAt: green_old.ProvedAt`, `Passed: green_old.Passed`, `CarriedFrom: G_old`), and moves `refs/tillr/green` to `G_new`. Under [OQ-HR2](#OQ-HR2) Option B, `refs/tillr/green` is rewound/cleared even on tree equality and re-proved by the heavy runner against warm `treeMemory`.

### 5.5 Scrub-safe ref hygiene (`D-HR4`, [`OQ-HR3`](#OQ-HR3))

A pre-publish content scrub fails its entire purpose if `refs/tillr/*` pins keep pre-rewrite commits reachable from `git for-each-ref` and block `git gc --prune=now` from collecting the scrubbed blobs.

By the designer's decision (`D-HR4`), governed by the default chosen in [OQ-HR3](#OQ-HR3):

1. `refs/tillr/trunk-history/accepted/*` and `refs/tillr/trunk-history/lost` are **deleted** (never pinned) in scrub mode.
2. `refs/tillr/landed/<run_id>` markers move to `map(old_sha)` on `T_new` (or are deleted if a historical landing consisted solely of scrubbed content and pruned completely).
3. Settled pre-rewrite `refs/tillr/runs/<run_id>` pins (for runs that are neither live nor pending candidates) and finished `refs/tillr/preserved/<run_id>` refs are deleted; active `runs/<run_id>` and `preserved/<run_id>` refs move to their replayed commits on `B_new`.
4. For any surviving `refs/tillr/ended/<run_id>` or operator ref (`refs/tillr/held/*`, `refs/tillr/recovery/*`) that still reaches pre-rewrite commits: `rewrite-history` attempts to replay `S^1..S` onto `map(S^1)` when `S^1` is in the commit map, and if any ref still reaches an unmapped pre-rewrite commit (for example, because its diff touched the scrubbed content), **refuses before deleting or leaving a hidden leak**, listing the exact ref names so the operator disposes of them explicitly or passes `--allow-old-refs`.
5. **Local non-Git files:** `.tillr/loop/<run_id>/diff.patch` and worker transcripts are gitignored local files never sent by `git push`. `rewrite-history` prints a one-line notice naming `.tillr/loop/` when old diffs exist on disk, without deleting run logs silently.

---

## 6. Supported queue export, import, and new-store carry-over

By the designer's decision (`D-HR5`, which the owner may overturn), `tillr-item` gains supported `export` and `import` faces so neither deliberate store resets (under Option A) nor rewrite-triggered store resets (if the owner selects Option C in [OQ-HR1](#OQ-HR1)) ever read SQLite tables directly.

### 6.1 Queue export (`tillr item export`) (`D-HR5`)

`tillr item export` opens `.tillr/state.db` read-only and writes one versioned JSON envelope (`kind = "item.queue-export"`, `record_version = 1`) to `--out <path>` or stdout (an untracked operator file, honoring [`OQ-PR13`](../tillr/decision-provenance-review.md#decision-ledger)):

- **Included item set:** Every non-finished item (`in-queue`, `leased`, `blocked`, `not-started`), plus finished store-declared or closed items when `--include-finished` is requested so local closures are not re-imported as open work.
- **Complete per-item state across `tillr-item`'s schema:**
  - Core `items` attributes (`item_id`, `title`, `body_ref`, `design_doc`, `allowed_paths`, `rank`, `seat`, `prose`, `workflow`, `workflow_args`, `judge_mode`, `express`, `waiting_on`, `underlying_state`, `close_reason`, `closed_at`, `closed_by`, `closed_from`, `lifecycle_reason`, `lifecycle_by`, `resolves_via`, `finding`, and whether `leased` items reset to `in-queue` on import);
  - `item_criteria` (`criterion_id`, `text`, `claim_class`, and non-terminal criterion `state`);
  - `request_words` (`words` base64/UTF-8 bytes, `words_hash`, `input_hash`);
  - `idea_intake` (`words_hash`, `words_size`, `words_path`, `intake_path`, plus the contents of `<store-dir>/intake/<id>.txt` inline so deleting `.tillr/` loses zero uncommitted idea words);
  - `item_splits` (`parent_id`, `statement`);
  - `comment_batches` (`document`, `batch`) and `comment_taken`;
  - `item_edges` (`item_id`, `blocked_by`), `workflow_selections` (`workflow`, `source`, `graph`), and `decisions` (`decision_id`, `text`, `scope_kind`, `scope_ref`, `decided_by`, `decided_at`, `status`, `supersedes`, `session_id`).

### 6.2 Queue import (`tillr item import`) (`D-HR5`)

On a target store (after `tillr store init` and `tillr item sync`):

1. Validates the `item.queue-export` envelope version, foreign-key references (`item_edges`, `item_splits`, `decisions.supersedes`), and hash integrity (`words_hash`).
2. Inside one atomic `s.WriteTx`, restores store-declared items (`request_words`, `idea_intake` + writing `<store-dir>/intake/<id>.txt`, `item_splits`, `comment_batches`), applies `decisions`, `workflow_selections`, `item_edges`, and runtime holds/closures onto synced Markdown-backed items, and restores `items.rank` in exact exported order — emitting standard `item.add`, `item.update`, and `decision.record` events rather than bypassing materializers with raw SQL inserts.
3. **Idempotency:** Re-importing the same bundle into the same store is a no-op by `item_id`, `words_hash`, and `decision_id`.

### 6.3 Candidate carry-over into a new store (if Option C is ruled in [`OQ-HR1`](#OQ-HR1)) (`D-HR6`)

If the owner rules in [OQ-HR1](#OQ-HR1) that a trunk rewrite always starts a fresh `.tillr/state.db` (Option C), queue export/import pairs with candidate carry-over (`tillr drain export-candidates` before deleting `.tillr/` and `tillr drain import-candidates --commit-map <path>` after `tillr item import`):

- `export-candidates` reads `readPendingHistory`, writes a `drain.candidates-export` manifest of every waiting candidate `(item_id, run_id, decision, base_sha, head_sha, worker_head_sha)`, and pins `refs/tillr/export/<run_id>` at `head_sha`.
- `import-candidates --commit-map <path>` replays or maps each candidate onto `B_new` (using the exact merge-tree / `--keep-refs` logic of [§5.3](#53-carrying-waiting-candidates-and-interrupted-lanes-without-worker-runs-d-hr2)), deletes `refs/tillr/export/<run_id>`, creates the synthetic `run.Record` and `drain.candidate-carried` pending entry in the fresh store, and stages the candidate directly for `launchExamination` with zero LLM worker runs.
- **What Option C still discards by design:** Even with queue `export`/`import` and `import-candidates`, starting a new store permanently discards the prior store's `events` timeline, `runs` and `drain_pass_spend` token/cost history, `landings` (breaking `CurrentLanding`, `ReturnLanded` revert resolution, and `tillr view heavy-misses` across the boundary), and `alert_episodes` / `watch_silences` / `watch_rule_baselines`.

---

## 7. Failure and edge cases

| Case | Behavior |
| :--- | :--- |
| `git filter-repo` already ran and pruned pre-rewrite objects before `rewrite-history` was called | Succeeds for the store, `refs/tillr/green` (comparing `GreenProof.TreeSHA` in `green-<G_old>.json` against `G_new^{tree}`), and every candidate included in `--commit-map` or `--keep-refs`. If an unmapped candidate's `H_old` object is already missing from `.git/objects/`, `rewrite-history` reports that candidate explicitly as unrecoverable and resets its item to `in-queue` on `T_new` only with `--drop-missing-candidates` (otherwise refusing before mutating the store). |
| Content-only commit on `main` became empty and was pruned (`--prune-empty=always`) | `rewrite-history` maps the pruned `old_sha` to its nearest surviving first-parent commit on `T_new` in `trunk_rewrites` (`evidence = "rewrite-history:pruned-to-ancestor"`) so lane bases and historical `landings` lookups still resolve to an ancestor of `T_new`. |
| Root commit of the repository was rewritten (no `git merge-base` between `T_old` and `T_new`) | `--commit-map` supplies explicit `old_sha -> new_sha` pairs without calling `git merge-base`, avoiding `proveRewrite`'s common-ancestor failure. |
| Waiting candidate edited the exact lines removed by the history scrub | `git merge-tree --write-tree` exits non-zero; `rewrite-history` records `drain.candidate-rewrite-conflict`, leaves `work` returned to its worker on `T_new`, and carries all non-conflicting sibling candidates to the fast gate undisturbed. |
| Four consecutive history rewrites in 48 hours | Each `rewrite-history` run updates existing `trunk_rewrites.new_sha` values transitively before inserting the new pairs, keeping `RewriteResolve` $O(1)$ while `RewriteAliases` retains every intermediate SHA. |
| `tillr-drain loop` or a worker lease is still active when `rewrite-history` starts | Refuses with exit `1` before touching Git refs or SQLite, naming the live session/run ID and instructing the caller to run `tillr-drain stop` first. |
| Rewritten commits violate `allowed-commit-identities.json` | Fast `commit-identity` fails on the rewritten range; `refs/tillr/green` is **not** carried to `G_new`, and `rewrite-history` reports the offending rewritten commit SHAs immediately. |
| Tracked Markdown document cites a commit SHA inside the rewritten range | `rewrite-history` scans tracked Markdown for SHAs in the rewritten `old_sha` set that are no longer ancestors of `T_new` and lists `file: sha` in stdout and `orphaned_doc_citations` so the operator or a follow-up item updates or marks them before a future diff hits `citations`. |

---

## 8. The build, in slices that each land alone

Nothing in this document is built in this commit. Under [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger), implementation waits on the owner's rulings on [OQ-HR1](#OQ-HR1), [OQ-HR2](#OQ-HR2), and [OQ-HR3](#OQ-HR3), and then lands in independent slices (`D-HR6`):

| Slice | What lands | Depends on |
| ---: | :--- | :--- |
| 1 | Add `TreeSHA` and `CarriedFrom` to `heavy.GreenProof` (`tools/tillr-drain/heavy/status.go` and `heavyrun/heavy.go`) with unit tests so every newly written `green-<tip>.json` records its root tree OID (`D-HR3`). | [`OQ-HR1`](#OQ-HR1), [`OQ-HR2`](#OQ-HR2) |
| 2 | `tillr item export` and `tillr item import` (`tools/tillr-item/`) covering `items`, `item_criteria`, `request_words`, `idea_intake` (with inline intake file bytes), `item_splits`, `comment_batches`, `item_edges`, `workflow_selections`, `decisions`, and `items.rank`, with deterministic round-trip tests (`D-HR5`). | [`OQ-HR1`](#OQ-HR1) (Options A or C) |
| 3 | `tillr drain rewrite-history --commit-map` core store and ref migration: transitive `trunk_rewrites` upsert, `trunk_history` and `rolling_state` translation, `refs/tillr/landed/*` updates, scrub-safe ref cleanup per [OQ-HR3](#OQ-HR3), `refs/tillr/green` + `green-<G_new>.json` carry-over per [OQ-HR2](#OQ-HR2), and orphaned Markdown citation reporting (`D-HR1`, `D-HR3`, `D-HR4`). | Slice 1, [`OQ-HR1`](#OQ-HR1) (Options A or B), [`OQ-HR2`](#OQ-HR2), [`OQ-HR3`](#OQ-HR3) |
| 4 | Waiting-candidate and interrupted-lane carry-over: `drain.candidate-carried` in `pendingHistoryKinds`, `git merge-tree` / `--keep-refs` replay, `<runDir>/gate.verdict.json` invalidation and direct fast-gate re-entry without worker dispatch (or `export-candidates` / `import-candidates` if [OQ-HR1](#OQ-HR1) rules Option C), with integration tests reproducing five waiting candidates across a content-scrubbing rewrite (`D-HR2`, `D-HR6`). | Slice 3 (or Slice 2 under Option C) |
| 5 | Update [`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md), [`tools/tillr-item/README.md`](../../tools/tillr-item/README.md), [`docs/reference/tillr-queue-persistence.md`](../reference/tillr-queue-persistence.md), and `yolo-packs/tillr/skills/tillr-operating/SKILL.md`, and run `just pack-sync` (`D-HR6`). | Slices 2–4 |

---

## 9. What this does not do

- **It does not run `git filter-repo` or rewrite Git commits itself.** The operator or external rewrite tool performs the Git history rewrite and produces the commit map; tillr carries its store, refs, lanes, candidates, and proof onto the resulting history.
- **It does not mutate or delete existing rows in `events`.** Append-only audit integrity is preserved via `trunk_rewrites` and `drain.candidate-carried`.
- **It does not let `Land` bypass `ReasonVerdictMismatch`.** Every carried candidate gets a fresh fast-gate `gate.verdict` binding `(B_new, H_new^{tree})` (in seconds via warm `treeMemory`) before `Trunk.Squash` runs.
- **It does not run `git gc --prune=now` automatically.** Dropping pre-rewrite `refs/tillr/*` refs makes scrubbed objects unreachable so the operator's own `git gc` purges them cleanly.
- **It does not build any code or edit `docs/tillr/roadmap.md` before the owner confirms.**

---

## 10. Alternatives considered and risks

| Alternative | Verdict |
| :--- | :--- |
| Relax `tillr drain accept-rewrite` to accept any `lost^{tree} == current^{tree}` regardless of commit counts | Rejected (`D-HR1`): ignores pruned commits, leaves no `old_sha -> new_sha` map for interior commits, pins `refs/tillr/trunk-history/accepted/<lost>` (defeating content scrubs), and still leaves waiting candidates and lane worktrees stranded on pre-rewrite SHAs. |
| Rewrite historical JSON payloads inside SQLite's `events` table (`UPDATE events SET payload = ...`) | Rejected (`D-HR1`): breaks append-only event immutability, risks corrupting historical audit records, and is unnecessary once `trunk_rewrites` and a single appended `drain.candidate-carried` event re-bind active state. |
| Trust a pre-rewrite `gate.verdict.json` at `Land` time by translating `Verdict.BaseSHA` through `trunk_rewrites` | Rejected (`D-HR2`): violates `decide`'s `(BaseSHA, TreeSHA)` invariant in `tools/tillr-land/land/landing.go` and skips `scope: "change"` checks (`commit-identity`, `citations`, `documents`) against `B_new..H_new`, whereas re-running the fast gate with warm `treeMemory` takes seconds and preserves the lander's exact contract. |
| Commit the exported queue snapshot into Git | Rejected (`D-HR5`): violates [`OQ-PR13`](../tillr/decision-provenance-review.md#decision-ledger) (`"database only"` for hand queue order) and [`D-RS5`](../reference/tillr-queue-persistence.md#what-stays-private-d-rs5). |

| Risk | Mitigation |
| :--- | :--- |
| Operator runs `git filter-repo` (which prunes unreferenced objects) *before* running `rewrite-history`, losing candidate commits that lived only in worktrees | Document running `rewrite-history` immediately before pruning, or including `refs/tillr/runs/*` / `refs/keep/*` in `filter-repo` so candidate commits appear in `.git/filter-repo/commit-map`; record `GreenProof.TreeSHA` in `green-<oid>.json` so `refs/tillr/green` still verifies tree equality after `G_old` is pruned (`D-HR3`). |
| Tracked Markdown files retain commit-SHA citations to rewritten commits while `T_old^{tree} == T_new^{tree}` | `rewrite-history` scans tracked Markdown against the rewritten `old_sha` set and reports every orphaned citation immediately (`D-HR3`). |
| Pre-rewrite `refs/tillr/ended/*` or `refs/tillr/held/*` refs keep scrubbed blobs reachable | Under [OQ-HR3](#OQ-HR3) Option A, `rewrite-history` replays or refuses on any surviving `refs/tillr/*` ref that still reaches unmapped pre-rewrite commits, so zero hidden refs block `git gc` (`D-HR4`). |

---

## Open Questions

1. 💬 **OQ-HR1: What should tillr offer when the trunk's history is rewritten?**

   On 2026-11-24 and 2026-11-25, four pre-publish content-removal rewrites forced `4m 42s` store resets, wiped `events`, spend, `landings`, and `alert_episodes`, and cost five finished candidates their gate standing.

   - **A — Offer both in-place commit-map carry-over (`tillr drain rewrite-history --commit-map`) and supported queue export/import (`tillr item export` / `tillr item import`).** A history rewrite carries `.tillr/state.db` in place in seconds — keeping `events`, token/USD spend, `landings`, and `alert_episodes` intact while sending waiting candidates straight to the fast gate with zero worker runs — and `tillr item export` / `import` gives deliberate store resets a supported command surface so nobody ever scrapes SQLite tables.
   - **B — Offer in-place commit-map carry-over (`tillr drain rewrite-history --commit-map`) only.** History rewrites carry `.tillr/state.db`, lanes, waiting candidates, and `refs/tillr/green` in place; deliberate store resets continue to rely on `tillr item sync` alone without a queue export/import command.
   - **C — Rule that a trunk history rewrite always starts a new store, and ship only supported queue export/import plus candidate carry-over (`import-candidates`).** Keeps every `.tillr/state.db` tied to a single unrewritten trunk history (aside from `pull --rebase`) and eliminates the `5` wasted worker runs and SQLite table scraping, while accepting that every history rewrite permanently discards prior `events`, token/USD spend, `landings`, and `alert_episodes`.

   <!-- vantage: question id=OQ-HR1 leaning="A: four rewrites in 48 hours proved that wiping .tillr/state.db destroys spend, landing, and alert history repeatedly even though trunk_rewrites already exists, while deliberate store resets still need tillr item export/import so nobody scrapes SQLite tables." -->

   _Leaning:_ A: four rewrites in 48 hours proved that wiping `.tillr/state.db` destroys spend, landing, and alert history repeatedly even though `trunk_rewrites` already exists, while deliberate store resets still need `tillr item export`/`import` so nobody scrapes SQLite tables.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-HR2: When `refs/tillr/green`'s replacement commit on the rewritten trunk has the identical root tree (`old_tree == new^{tree}`), should `refs/tillr/green` carry immediately or wait for a heavy proof pass?**

   Tree equality proves every `scope: "tree"` check and leaves `treeMemory` warm, while proving nothing about rewritten commit identities or orphaned Markdown SHA citations ([§3](#3-what-a-proof-of-the-old-tip-is-worth-when-the-new-tip-has-the-same-tree)).

   - **A — Carry `refs/tillr/green` immediately after fast history verification.** When `G_new` is on `T_new`, `old_tree == G_new^{tree}`, and fast `commit-identity` passes over the rewritten range, `rewrite-history` moves `refs/tillr/green` to `G_new`, writes `green-<G_new>.json` (`carried_from: G_old`), and reports any orphaned Markdown commit-SHA citations. Builds (`just tillr-build`) and `lane_base: green` work immediately with zero unproved window.
   - **B — Rewind or clear `refs/tillr/green` on every non-patch-identical rewrite and let the next heavy pass re-prove `T_new`.** Even when `old_tree == G_new^{tree}`, `refs/tillr/green` waits for `HeavyRunner` to run on `T_new` (which reuses warm `treeMemory` passes for unchanged tree checks and writes a fresh `green-<T_new>.json`), trading a brief unproved window (`TILLR_BUILD_UNPROVED` until the heavy pass finishes) for one uniform writer of `refs/tillr/green`.

   <!-- vantage: question id=OQ-HR2 leaning="A: when G_old^{tree} == G_new^{tree} and commit-identity passes on the rewritten commits, every tree check already has the exact same inputs, and carrying green-<G_new>.json keeps just tillr-build and green_proof watches working immediately." -->

   _Leaning:_ A: when `G_old^{tree} == G_new^{tree}` and `commit-identity` passes on the rewritten commits, every tree check already has the exact same inputs, and carrying `green-<G_new>.json` keeps `just tillr-build` and `green_proof` watches working immediately.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-HR3: How should `rewrite-history` handle `refs/tillr/*` refs that still reach pre-rewrite Git objects?**

   Today `accept-rewrite` pins `refs/tillr/trunk-history/accepted/<lost>` so replaced commits stay readable. On a pre-publish content scrub, any surviving ref into pre-rewrite history keeps scrubbed blobs reachable against `git gc`.

   - **A — Drop pre-rewrite pins by default on `rewrite-history`, and refuse if any uncarried `ended/` or operator ref still reaches old commits.** By default, `rewrite-history` deletes `refs/tillr/trunk-history/accepted/*`, `lost`, and settled pre-rewrite `runs/*` / `preserved/*` refs, re-points active `runs/*`, `lanes/*`, `landed/*`, and `preserved/*` onto rewritten commits, and refuses (listing the exact refs unless `--allow-old-refs` is passed) if any `ended/*`, `held/*`, or `recovery/*` ref still reaches pre-rewrite history.
   - **B — Keep `refs/tillr/trunk-history/accepted/<lost>` by default, and purge pre-rewrite refs only when `--scrub` (`--drop-old-refs`) is passed.** Matches `accept-rewrite`'s default retention for benign rebases, at the risk that forgetting `--scrub` during a pre-publish content removal leaves scrubbed blobs reachable in `.git/objects/` under `refs/tillr/`.

   <!-- vantage: question id=OQ-HR3 leaning="A: git pull --rebase is already handled automatically by observeTrunk, whereas an explicit commit-map rewrite is overwhelmingly a pre-publish history scrub where silently retaining refs/tillr/trunk-history/accepted/<lost> defeats the scrub." -->

   _Leaning:_ A: `git pull --rebase` is already handled automatically by `observeTrunk`, whereas an explicit commit-map rewrite is overwhelmingly a pre-publish history scrub where silently retaining `refs/tillr/trunk-history/accepted/<lost>` defeats the scrub.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Every row in this ledger is the designer's decision, made in response to the 2026-11-25 command-line request without new words from the owner, and the owner may overturn any of them ([`D-PR2`](../tillr/decision-provenance-review.md#decision-ledger)). Governing owner rulings made in earlier documents (`R-OD1`, `D-GR5`, `D-GR8`, `D-GR18`, [`OQ-PR1`](../tillr/decision-provenance-review.md#decision-ledger), [`OQ-PR13`](../tillr/decision-provenance-review.md#decision-ledger), [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger), [`OQ-PR22`](../tillr/decision-provenance-review.md#decision-ledger)) remain owned by those documents' ledgers and are cited in [§2](#2-governing-rulings).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-HR1 | **Designer's decision; the owner may overturn this.** `tillr drain rewrite-history --commit-map <path>` runs only while no live loop or worker lease is active, holds `land.lock` across Git work before `s.WriteTx`, accepts `git filter-repo` `<old-sha> <new-sha>` maps (including root rewrites with no `merge-base` and pruned empty commits mapped to their surviving first-parent ancestor), updates `trunk_rewrites` transitively across chained rewrites, updates `trunk_history` and `rolling_state`, and keeps `events` strictly append-only (`drain.trunk-history-rewritten`). **Rejected:** relaxing `accept-rewrite` to ignore commit-count mismatches, or mutating historical `events` rows in SQLite. | 2026-11-26, designer | [§5.1](#51-preconditions-and-commit-map-contract-tillr-drain-rewrite-history-d-hr1), [§5.2](#52-store-commit-state-without-mutating-events-d-hr1) | No |
| D-HR2 | **Designer's decision; the owner may overturn this.** Waiting candidates (`work.Candidate != nil`) are carried onto `B_new` via `--commit-map` / `--keep-refs` adoption or `git merge-tree --write-tree` + `git commit-tree`, recorded by appending `drain.candidate-carried` (folded in `pendingHistoryKinds` with `work.Gated = false` and pre-rewrite `gate.verdict.json` moved aside), so carried candidates re-enter the fast landing gate directly with zero LLM worker runs unless `merge-tree` conflicts or a check fails; stopped dirty lanes snapshot and replay their edits onto `B_new` under `refs/tillr/preserved/<run_id>`. **Rejected:** bypassing `ReasonVerdictMismatch` in `Land` without re-running fast `change` checks on `B_new..H_new`. | 2026-11-26, designer | [§5.3](#53-carrying-waiting-candidates-and-interrupted-lanes-without-worker-runs-d-hr2) | No |
| D-HR3 | **Designer's decision; the owner may overturn this.** `heavy.GreenProof` (`green-<oid>.json`) records `tree_sha` and optional `carried_from` so tree equality (`old_tree == G_new^{tree}`) remains verifiable even if `G_old` was already pruned from `.git/objects/`; `rewrite-history` never carries `refs/tillr/green` when `old_tree != G_new^{tree}` or `commit-identity` fails on the rewritten range, and scans tracked Markdown to report commit-SHA citations orphaned by the rewrite. | 2026-11-26, designer | [§3](#3-what-a-proof-of-the-old-tip-is-worth-when-the-new-tip-has-the-same-tree), [§5.4](#54-carrying-refstillrgreen-and-green-oidjson-d-hr3-oq-hr2) | No |
| D-HR4 | **Designer's decision; the owner may overturn this.** Under scrub-safe ref cleanup (governed by [OQ-HR3](#OQ-HR3)), `rewrite-history` deletes `refs/tillr/trunk-history/accepted/*`, `lost`, and settled pre-rewrite `runs/*` / `preserved/*` pins, moves `refs/tillr/landed/*` and active `runs/*` / `lanes/*` / `preserved/*` onto rewritten commits, refuses (unless `--allow-old-refs`) if surviving `ended/*`, `held/*`, or `recovery/*` refs still reach unmapped pre-rewrite commits, and prints a local-disk notice when `.tillr/loop/*/diff.patch` files exist. | 2026-11-26, designer | [§5.5](#55-scrub-safe-ref-hygiene-d-hr4-oq-hr3) | No |
| D-HR5 | **Designer's decision; the owner may overturn this.** `tillr item export` and `tillr item import` serialize and atomically restore store-owned queue state (`items`, `item_criteria`, `request_words`, `idea_intake` with inline intake file bytes, `item_splits`, `comment_batches`, `item_edges`, `workflow_selections`, `decisions`, and `items.rank`) through `tillr-item`'s materializers in a versioned untracked `item.queue-export` JSON envelope, honoring [`OQ-PR13`](../tillr/decision-provenance-review.md#decision-ledger) and `D-RS5`. **Rejected:** committing queue exports to Git or scraping SQLite tables. | 2026-11-26, designer | [§6.1](#61-queue-export-tillr-item-export-d-hr5), [§6.2](#62-queue-import-tillr-item-import-d-hr5) | No |
| D-HR6 | **Designer's decision; the owner may overturn this.** If [OQ-HR1](#OQ-HR1) selects Option C, pair `tillr item export`/`import` with `tillr drain export-candidates` / `import-candidates --commit-map` so waiting candidates still enter the fast gate in the new store with zero worker runs; build lands in the five independent slices of [§8](#8-the-build-in-slices-that-each-land-alone) after [OQ-HR1](#OQ-HR1) to [OQ-HR3](#OQ-HR3) are ruled. | 2026-11-26, designer | [§6.3](#63-candidate-carry-over-into-a-new-store-if-option-c-is-ruled-in-oq-hr1-d-hr6), [§8](#8-the-build-in-slices-that-each-land-alone) | No |
