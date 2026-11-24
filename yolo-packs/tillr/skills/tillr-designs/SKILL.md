---
name: tillr-designs
description: Read tillr's design documentation from another repository — what tillr does today, what is built, queued or only proposed, what changed recently, and which surfaces are stable enough to depend on. Use when your project consumes tillr (the Product QA pipeline, or anything that calls its binaries, store, hooks or reports), when you need to check whether a behavior is live or still proposed, or when you want to send tillr comments or requests.
---

# Reading tillr's designs from another repository

tillr is the owner's work-queue toolset: eight command-line binaries over one SQLite store
(`tillr` and the seven `tillr-<noun>` programs). Under the owner's direction, tillr separates
general machinery of queues, leased runs, criteria, and finite workflow graphs (**the core**)
from a per-use **charter** (`definition.json` in code and flags today, named *charter* by owner
ruling [`OQ-VO4`](../../../../docs/design/tillr-vocabulary.md#OQ-VO4)):

- **The `git-landing` charter** ([`OQ-VO5`](../../../../docs/design/tillr-vocabulary.md#OQ-VO5), default on `tillr store init`) stages each ready item
  into an isolated Git worktree, runs its workflow graph of agent and command nodes inside a path
  fence, checks the candidate with `tillr-gate`, squashes one commit onto the trunk with
  `tillr-land`, and proves heavy suites asynchronously after merge.
- **Custom non-landing charters** declare their own states, transitions, `completes: "hook"`, and
  a `complete-item` hook program so findings or investigations run through reproduction commands
  and agent stages and finish by recording hash-bound criterion verdicts with no Git repository,
  gate, or commit.

"Product QA" is a product bug-finding pipeline in its own repository (tillr's first
external consumer). `docs/design/tillr-autoqa.md` is a different thing: tillr's own exploratory
self-testing design, currently in the roadmap's Icebox.

## Where the files are

- `~/.local/share/tillr/docs/tillr/design-status.md` is the **status index**. Start here.
- `~/.local/share/tillr/docs/tillr/roadmap.md` orders remaining rulings, loop fixes, and builds,
  and lists what is in the Icebox.
- `~/.local/share/tillr/docs/design/` holds every design the status index marks `shipped`.
- `~/.local/share/tillr/<module>/README.md` is each Go module's reference, mirrored from
  `tools/tillr*` on every gate run alongside the source the `tillr*` launchers build from.
  `~/.local/share/tillr/tillr/definition/README.md` documents `definition.json` and hooks, and
  `~/.local/share/tillr/tillr/examples/recorded-verdict/README.md` walks through a runnable
  non-Git charter from an empty directory.
- `~/.local/share/tillr/docs/snapshot.json` names the commit and date of the design snapshot.

Note the two cadences: `~/.local/share/tillr/<module>/` and the binaries on `PATH` move with
`just pack-sync` on every tillr code landing, whereas `~/.local/share/tillr/docs/` is a dated
snapshot refreshed by `just pack-docs`. When a design copy and a module `README.md` or binary's
`--help` disagree, the live binary and module `README.md` reflect newer code. Every delivered
design copy opens with a banner naming its source file and commit, links undelivered files to
GitHub, and neutralizes `<!-- tillr: ... -->` directives so copies are never read as queue items.

## What tillr does today

- **Store initialization, events, views, and tracing are built.** `tillr store init` creates all
  module tables up front. The binaries emit structured events (`item.*`, `run.*`, `drain.*`,
  `call.*`, `land.*`) readable through `tillr store events` and `tillr view events`. Items enter
  via `tillr item sync` or `tillr item apply`, and `tillr item place` reorders them. `tillr view` serves CLI inspection (`status`, `waiting`, `passes`, `lanes`,
  `running`, `events`, `trace`, `perf-log`) and the embedded web observatory (`tillr view serve`),
  and `tillr-trace` records the commands a run executes.
- **Filing work with `tillr item request`.** Work that needs no design-document block is filed
  straight into the store from standard input with a title and path fence. A request carries four
  fixed criteria (plus optional additions), defaults to express routing unless marked ordinary,
  and finishes with no Markdown item block — any diff touching item blocks is refused.
  `tillr item idea` and `tillr item doc-triage` file store-side ideas and one-document triages.
- **Plan, build, and judge steps — and the quick path through them.** Build items default to
  `planned-build@v1`: a read-only `plan` node reads the item and either keeps one piece or fans
  out up to twelve seat-routed pieces (`build-pieces@v1`), a `work` node implements or joins
  them, and a read-only `verify` evaluator node checks the candidate diff against the criteria
  before `tillr-gate`. The persisted evaluator's `judge_classes` allows only `verification-only`.
  Design items default to `scouted-design@v1` (`open`, optional parallel scouts, `write`).
  Naming `single-worker@v1` skips planning and ordinarily goes straight to the mechanical gate.
  A legacy `judge="agent"` opt-in instead retains a read-only compatibility review.
- **Trunk proof, automatic revert, and work under a red check.** Under `after-merge` landing,
  `tillr-gate` runs fast change and whole-tree checks before `tillr-land` squashes onto `main`,
  while a background heavy runner proves the full suites asynchronously on trunk, advances
  `refs/tillr/green`, and pushes proved commits when `[push] what = "green"`. With `auto_revert`
  on, a red heavy check bisects the culprit landing, gates and lands its inverse
  (`revert-main-<sha12>`), withdraws the item's verdicts, moves the item back to `in-queue`, and
  returns the failure output to the author's work (filing a deduplicated trunk fixer if revert is
  not possible). Under owner ruling [`OQ-GP7`](../../../../docs/design/tillr-graph-is-the-path.md#OQ-GP7), a red trunk check does not cap or freeze
  unrelated work: non-fixer runs still start with a trunk-state notice, a candidate whose changed
  paths do not intersect any red check's declared `reads` promotes to `pass-inherited` and lands,
  and one whose paths intersect a red check's `reads` is acquitted without charge as
  `trunk-already-red` (`trunk-broken` in waiting views) behind the open fixer.
- **Reserved lanes for interactive requests.** Repository settings (`[lanes] express` in
  `.tillr.toml`, set to two in the owner's repository) reserve `express-1`,
  `express-2` outside the ordinary worker pool for saved ideas, CLI requests (unless marked
  ordinary), and express blocks so interactive requests do not queue behind long background work.
- **Loop detection and recovery.** When `pi` ends a runaway worker for repeating the same tool
  call (`repeating-call`), the drain preserves uncommitted work, never resumes the
  looping session, and may start a fresh session on the same backend naming the repeated call.
  If the fresh session repeats or pi already recovered in-run, the item holds as `repeating-call`. The runner also terminates settled harnesses that linger idle (`settled-idle`),
  reclaims orphan runs left by dead loops or container restarts, returns gate, fence, and land
  refusals to the worker session uncounted (`returned-with-evidence`) until an identical repeat
  escalates to design, migrates pause-capable live workers onto a rolled build at their next turn
  gap, and stops passes, gates, and proofs promptly on `tillr drain stop`.
- **Copilot, Bedrock, and Claude backends.** Seat policies map each seat (`plan`, `scout`, `edit`,
  `build`, `test-write`, `test-run`, `verify`, `docs`, `design`, `review`) across preferred
  backends, fallback chains, escalation lists, and quota shares, reloading live without a restart.
  `pi` runs on GitHub Copilot or AWS Bedrock model tiers, while `run-claude` runs headless Claude
  Code so design and review seats can spend a Claude subscription (`muse` remains built in the
  tree but dormant). An infrastructure failure holds on the backend that failed. A recorded
  recovery permits work to continue on that backend; an identical repeat holds for the operator.
- **Landing and obstacle tools a worker ends with.** When `pi --features` reports
  `parked-tool-calls`, the drain switches `pi` workers from `RESULT:` text footers to
  `call-suspends` mode and loads two extension tools: `land` (which checks for a clean worktree
  ahead of `base_sha`, records a landing offer, and suspends the tool call) and `obstacle` (which
  validates structured `definition`, `fence`, `trunk`, `tooling`, `seat`, or `owner` blocker
  fields and suspends). If the gate, lander, or revert rejects the candidate, the drain reopens
  the session without a new prompt and delivers the refusal as that tool call's result; when
  `parked-tool-calls` is absent, workers fall back to `RESULT:` footers.

## Reading order

The paths below are under `~/.local/share/tillr/`.

1. `docs/tillr/design-status.md` ("Read this first") and `docs/tillr/roadmap.md`: what tillr
   does today, what is built, queued, proposed, and iceboxed, and how statuses are verified.
2. `docs/design/tillr-definitions-plan.md` and `tillr/examples/recorded-verdict/README.md`: all
   eighteen built items of the first charter tranche, plus the worked non-Git example you can run
   from an empty directory.
3. `docs/design/tillr-separable-parts.md`: first "For the Product QA project's agent", then
   Section 7 (each second-round request's fate and the item that built it), Section 2 (the stable
   command-and-record contract vs internal surfaces), and Section 1.3 (what stays charter-scoped).
   `docs/design/tillr-product-qa-requests.md` holds Product QA's second-round reply as received.
4. `docs/design/tillr-core-and-git-landing.md`, Sections 1, 4, 5, 6 and 8, alongside
   `tillr/definition/README.md`: the core-and-charter split and the exact `definition.json` and
   `complete-item` hook contract.
5. Optional background on how work enters `git-landing`: `docs/design/tillr-intake-paths.md`.

## Built, queued, and only proposed

Trust `docs/tillr/design-status.md` and `docs/tillr/roadmap.md` over an individual design's
header line, and check `~/.local/share/tillr/<module>/README.md` or `<binary> --help` for
landings newer than `docs/snapshot.json`:

- **Built:** All eight CLI binaries, the 18-item `tillr-definitions-plan.md` tranche (custom
  `definition.json` lifecycles, `complete-item` hooks, and `examples/recorded-verdict`), item
  graphs, `planned-build@v1` and seat tiering, queue persistence and ordering, designer desks and
  comment intake, rolling restarts, the converge planner, fast gate + `after-merge` heavy proof +
  `auto_revert` + [`OQ-GP7`](../../../../docs/design/tillr-graph-is-the-path.md#OQ-GP7) path-disjoint landing, Observatory pages and reorder/idea actions,
  Copilot/Bedrock/Claude adapters, `tillr-trace` slice 1, and `land` + `obstacle` tool calls.
- **Partly built or queued:** Remaining judge-and-lifecycle blocks (owner-criterion holds, raw
  `item state` terminal-state evidence guards, design-refusal session resume), `uvx vantage-check`
  default index invocation, heavy-proof recovery and check ordering, direct backend quota
  reserves, the self-hosted source launcher ([`OQ-SH2`](../../../../docs/design/tillr-selfhosted-build.md#OQ-SH2) open), and unfiled separable-parts items
  (explicit-store recipe P2, stock plain-command adapter P3, published change feed P4, standalone
  dead-run probe P6, and charter hooks beyond `complete-item`).
- **Proposed only (awaiting owner rulings or ledger review — do not build on these):** Companion
  `<topic>-plan.md` plans retiring hidden item comments (`tillr-queue-mirrors-designs.md`),
  mandatory pre-landing code diff review ([`OQ-DR1`](../../../../docs/design/tillr-diff-review.md#OQ-DR1), [`OQ-DR2`](../../../../docs/design/tillr-diff-review.md#OQ-DR2)), the policy compliance officer
  ([`OQ-DP5`](../../../../docs/design/tillr-design-document-check.md#OQ-DP5)), the whole-life item-graph runtime (beyond [`OQ-GP7`](../../../../docs/design/tillr-graph-is-the-path.md#OQ-GP7)), log-per-item storage ([`OQ-TS1`](../../../../docs/design/tillr-storage.md#OQ-TS1),
  [`OQ-TS3`](../../../../docs/design/tillr-storage.md#OQ-TS3)), early design landing ([`OQ-EL3`](../../../../docs/design/tillr-early-design-landing.md#OQ-EL3)), criterion supersession ([`OQ-CR7`](../../../../docs/design/criterion-repair.md#OQ-CR7)), code-level
  vocabulary renames, recorded-task benchmark replay, scheduled tasks, and the 20 remaining
  provenance-review questions.
- **Iceboxed:** AutoQA exploratory self-testing, Goal Monitor, Muse backend, and Observatory
  dollar pricing.

## What is stable — and what is still moving

Depend only on Section 2.1 ("The stable surface") of `docs/design/tillr-separable-parts.md`:

- **Stable:** Calling `tillr <noun> <face>` or `tillr-<noun> <face>` with explicit paths
  (`--store`, `--root`, `--lane-root`, `--backends-dir`, `--input`, `--now`, `--item`, `--since`,
  and `--definition` on `store init`); the consumer faces on `store` (`init`, `check`, `events`),
  `item` (`apply`, `list`, `show`, `state`, `close`, `reopen`, `verdict`, `judge`, `decision`,
  `mail`), `run` (`exec`, `status`, `lanes`, `release`), `view` (`status`, `events`, `waiting`,
  `passes`, `lanes`, `running`), `drain` (`why`, `plan`, plus `once` and `loop` on your own store
  and root), and no-store `gate` (`change`, `plan`, `shadow`); exit codes `0..5` (plus `6`
  `stalled` on `gate`); `record_version: 1` JSON envelopes (`producer.system: "tillr"`),
  `item.record`, `run.request`, `run.record`, `item show` / `verdict` outputs, event envelopes,
  `TILLR_*` worker environment variables, and `definition.json` + `complete-item` hooks.
- **Moving and internal (Section 2.2):** Go packages under `tools/tillr*`, SQLite table and
  column schemas (never read or write `state.db` with SQL), planner hold-reason strings,
  human-formatted view text, `.tillr/loop` internal files, repository-specific glue
  (`tools/tillr-loop`, `just tillr-keep`), and anything marked in a module README's Known Gaps
  banner.

## Sending tillr a request

Do not edit the delivered copies under `~/.local/share/tillr/`. They are overwritten on the next
sync or snapshot, and nothing reads them back.

1. Write your comments or requests as a tracked document in your own repository and tell the
   owner where it is; the owner relays it. tillr records each round as received in
   `docs/design/tillr-product-qa-requests.md`.
2. Number each entry and record the behavior you ran or read, the snapshot commit from
   `docs/snapshot.json` (plus binary build if newer), and the date. Try the `recorded-verdict`
   example and cover what your pipeline needs from store events and views, cross-repository
   filing and outcome reads, first-adopted surfaces, breaking-change sensitivities, and
   diagnosing or recovering a drain in a repository tillr's maintainers cannot access directly.
3. tillr's answers return in `docs/design/tillr-separable-parts.md` and queued build plans in the
   next snapshot this pack delivers.
