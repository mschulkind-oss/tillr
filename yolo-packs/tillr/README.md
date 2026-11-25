# The tillr pack

This pack ([`pack.json`](./pack.json)) delivers tillr, the owner's work-queue toolset, to a jail in any workspace that selects it. It delivers three things to the jail and keeps one reviewer task brief in the repository beside them.

| What | Where it lands in the jail | Produced by |
| :--- | :--- | :--- |
| The Go modules and the eleven `tillr*` launchers on `PATH` that build lazily per source digest | `~/.local/share/tillr/<module>/`, `~/.local/bin/` | `just pack-sync`, checked by [`tests/pack-sync.test.py`](../../tests/pack-sync.test.py) |
| The [Git-landing loop adapter](../../tools/tillr-loop/README.md), the program the loop calls to check, land, and record verdicts for candidates | `~/.local/share/tillr/tillr-loop/`, `~/.local/bin/tillr-loop` (built lazily per source digest like the other `tillr*` launchers) | `just pack-sync`, checked by the same suite |
| A dated snapshot of tillr's design documentation: the [status index](../../docs/tillr/design-status.md) and every document it marks `shipped` | `~/.local/share/tillr/docs/`, plus `~/.local/share/tillr/tillr-loop/README.md` | `just pack-docs` ([`tools/pack-docs.py`](../../tools/pack-docs.py)), checked by the same suite |
| Two skills: [`tillr-operating`](./skills/tillr-operating/SKILL.md) (running and recovering the loop) and [`tillr-designs`](./skills/tillr-designs/SKILL.md) (reading the designs and sending tillr requests from another repository) | each agent's skill directory | [`skills/`](./skills/) |

[`tasks/autoqa-handoff.md`](./tasks/autoqa-handoff.md) stays outside `briefing/` on purpose: it is a review task brief handed only to an agent asked to review tillr as a consumer, never broadcast as a house rule.

A delivered path mirrors the repository path, with a leading `tools/` dropped. So `docs/reference/tillr-item-graphs.md` lands at `~/.local/share/tillr/docs/reference/tillr-item-graphs.md`, and `tools/tillr/examples/recorded-verdict/README.md` lands at `~/.local/share/tillr/tillr/examples/recorded-verdict/README.md`.

## What tillr does today

The pack delivers ten command-line programs over one SQLite database (`state.db` plus its write-ahead log): the umbrella `tillr` dispatcher and `tillr-store`, `tillr-item`, `tillr-run`, `tillr-drain`, `tillr-gate`, `tillr-land`, `tillr-trace`, `tillr-view`, and `tillr-watch`. Callers compose them as OS processes that exchange exit codes and versioned JSON records (`record_version: 1`). Each face's flags live on the binary's own `--help`.

Under [the core-and-charter direction](../../docs/design/tillr-core-and-git-landing.md), tillr separates general machinery of queues, leased runs, criteria, and finite workflow graphs (**the core**) from the per-use configuration (**charter**, ruled in [`OQ-VO4`](../../docs/design/tillr-vocabulary.md#OQ-VO4) and [`OQ-VO5`](../../docs/design/tillr-vocabulary.md#OQ-VO5); still spelled `definition` in code and flags today):

- **The `git-landing` charter** (default on `tillr store init`) syncs items from tracked Markdown or store intake, stages each ready item into an isolated Git worktree, runs its workflow graph of agent and command nodes inside a path fence, checks the candidate with `tillr-gate`, squashes one commit onto the trunk with `tillr-land`, and proves heavy checks asynchronously after merge.
- **Custom non-landing charters** ([`tools/tillr/definition/README.md`](../../tools/tillr/definition/README.md)) declare their own states, transitions, `completes: "hook"`, and a `complete-item` hook program so findings or investigations run through reproduction commands and agent stages and finish by recording hash-bound criterion verdicts with no Git repository, gate, or commit. [`tools/tillr/examples/recorded-verdict/README.md`](../../tools/tillr/examples/recorded-verdict/README.md) runs end to end from an empty directory.

Two QA names appear in the documents and are distinct: **Product QA** is the example consumer the designs use, a bug-finding pipeline that runs a store of its own and lands nothing, while **AutoQA** ([`docs/design/tillr-autoqa.md`](../../docs/design/tillr-autoqa.md)) is tillr's own exploratory self-testing design, parked on the roadmap's Icebox.

## What the loop does

- **Store initialization, events, views, and tracing are live.** `tillr store init` creates all module tables at once. The binaries emit structured store events (`item.*`, `run.*`, `drain.*`, `call.*`, `land.*`) readable via `tillr store events` and `tillr view events`. Work enters through `tillr item sync`, `tillr item apply`, and `tillr item place`. `tillr view` serves CLI inspection (`status`, `waiting`, `passes`, `lanes`, `running`, `events`, `trace`, `perf-log`) and the embedded React observatory (`tillr view serve`), while `tillr-trace` records the commands a worker runs.
- **Filing work with `tillr item request`.** Work that needs no design-document block can be filed straight into the store from standard input with a title and path fence. A request gets four fixed criteria (plus optional caller criteria), enters the express pool by default unless marked ordinary, and finishes with no Markdown item block — any candidate diff that adds, edits, or removes an item block is refused. `tillr item idea` and `tillr item doc-triage` similarly file store-side ideas and one-document triages.
- **Plan, build, and judge steps — and the quick path through them.** Build items default to `planned-build@v1`: a read-only `plan` node reads the item and either leaves it as one piece or fans out up to twelve seat-routed pieces (`build-pieces@v1`), a `work` node implements or joins them, and a read-only `verify` evaluator node checks the candidate diff against the criteria before `tillr-gate`. The judge is a step of the selected workflow: the shipped `verify` and compatibility `review` steps list only `verification-only` in `judge_classes`, and there is no grant to issue or renew. Design work defaults to `scouted-design@v1` (`open`, optional parallel scouts, `write`). Naming `single-worker@v1` runs one worker straight to the mechanical gate with no planning node and no evaluator grant.
- **Trunk proof, automatic revert, and work under a red check.** Under `after-merge` landing, `tillr-gate` runs fast change and whole-tree checks before `tillr-land` squashes onto `main`, while a background heavy runner proves the full test suites on a detached trunk checkout, advances `refs/tillr/green`, and pushes only proved commits when `[push] what = "green"`. With `auto_revert` enabled, a red heavy check bisects the culprit landing, gates and lands its inverse (`revert-main-<sha12>`), withdraws the item's verdicts, moves the item back to `in-queue`, and returns the failure output to the author's work (filing a deduplicated trunk fixer when automatic revert is not possible). Per [`OQ-GP7`](../../docs/design/tillr-graph-is-the-path.md#OQ-GP7), a red trunk check does not cap or halt unrelated work: non-fixer runs still start with a trunk-state notice, a candidate whose changed paths do not touch any red check's declared `reads` is promoted to `pass-inherited` and lands, and one whose paths intersect a red check's `reads` is acquitted without charge as `trunk-already-red` (`trunk-broken` in waiting views) behind the open fixer.
- **Reserved lanes for interactive requests.** Setting `[lanes] express` in `.tillr.toml` (two reserved lanes in this repository) sets aside `express-1`, `express-2` outside the ordinary worker cap for saved ideas, CLI requests (unless filed ordinary), and express-marked blocks so interactive tasks do not wait behind a full batch of background builds.
- **Loop detection and recovery.** When `pi` ends a runaway worker for repeating the same tool call (`repeating-call`), the drain pins uncommitted work, never resumes the looping session, retries fresh on the same backend naming the repeated call, fails over to the seat's next backend on a second repeat, and holds the item as `repeating-call` once the fallback chain wraps. Alongside that, the runner ends settled harnesses that linger idle (`settled-idle`), reclaims orphan runs left by dead loops or container restarts, returns gate, fence, and land refusals to the worker session uncounted (`returned-with-evidence`) until an identical repeat escalates to design, migrates pause-capable live workers onto a newly rolled build at their next turn gap, and stops passes, gates, and proofs promptly on `tillr drain stop`.
- **Copilot, Bedrock, and Claude backends.** Seat policies (`config/tillr/backend-policy*.json`) route each seat (`plan`, `scout`, `edit`, `build`, `test-write`, `test-run`, `verify`, `docs`, `design`, `review`) across preferred backends, fallback chains, escalation lists, and quota shares, reloading on the fly without a restart. `pi` runs against GitHub Copilot or AWS Bedrock model tiers, while `run-claude` runs headless Claude Code so design and review seats can spend a Claude subscription first and fail over automatically to `pi` when a quota or availability window closes (`muse` stays built in the tree but dormant).
- **Landing and obstacle tools a worker ends with.** When `pi --features` advertises `parked-tool-calls`, the drain switches `pi` workers from `RESULT:` text footers to `call-suspends` mode and loads two extension tools: `land` (which checks for a clean worktree ahead of `base_sha`, writes a landing offer, and suspends the tool call) and `obstacle` (which validates structured `definition`, `fence`, `trunk`, `tooling`, `seat`, or `owner` blocker fields and suspends). If the gate, lander, or revert rejects the candidate, the drain reopens that session with no new prompt and delivers the refusal as the suspended call's tool result; on `pi` builds without `parked-tool-calls`, workers use the text footer contract unchanged.

## Built, partly built, designed, open and parked

[`docs/tillr/design-status.md`](../../docs/tillr/design-status.md) has one row per design and reference document, each with one of five statuses, and [`docs/tillr/roadmap.md`](../../docs/tillr/roadmap.md) orders the unfinished work:

- **Built:** every reference under [`docs/reference/`](../../docs/reference/), the eight loop programs, and the first build of [the core and charter split](../../docs/design/tillr-core-and-git-landing.md#82-what-is-built-and-what-is-not) (custom `definition.json` lifecycles, `complete-item` hooks, and `examples/recorded-verdict`).
- **Partly built:** the document's status paragraph says which part. [The unbuilt units of the split](../../docs/design/tillr-core-and-git-landing.md#85-the-unbuilt-units) and [four pieces of consumer-facing work](../../docs/design/tillr-separable-parts.md#3-proposed-work) are not built.
- **Designed, not built, and Open questions:** do not build on these. A design with open questions promises nothing until the owner rules them.
- **Parked:** AutoQA, the goal monitor, the Muse adapter and prices on the observatory, under [the roadmap's Icebox](../../docs/tillr/roadmap.md#icebox).

## Which surfaces are stable and which are moving

External consumers should build strictly on [the stable surface in `tillr-separable-parts.md` Section 2.1](../../docs/design/tillr-separable-parts.md#21-the-stable-surface) and avoid [the internal surfaces in Section 2.2](../../docs/design/tillr-separable-parts.md#22-what-is-internal-and-may-change-daily):

- **Stable to depend on:** Invoking `tillr <noun> <face>` or `tillr-<noun> <face>` with explicit paths (`--store`, `--root`, `--lane-root`, `--backends-dir`, and `--definition` on `store init`); the consumer faces on `store` (`init`, `check`, `events`), `item` (`apply`, `list`, `show`, `state`, `close`, `reopen`, `verdict`, `judge`, `decision`, `mail`), `run` (`exec`, `status`, `lanes`, `release`), `view` (`status`, `events`, `waiting`, `passes`, `lanes`, `running`), `drain` (`why`, `plan`, plus `once` and `loop` on your own store and root), and no-store `gate` (`change`, `plan`, `shadow`); exit codes `0..5` (plus `6` `stalled` on `gate`); `record_version: 1` JSON envelopes (`producer.system: "tillr"`), `item.record`, `run.request`, `run.record`, `item show` / `verdict` records, event envelopes, `TILLR_*` worker environment variables, and the `definition.json` + `complete-item` hook contract.
- **Moving and internal (may change on any landing):** Go packages under `tools/tillr*`, SQLite table and column names (never read or write `state.db` with SQL), planner hold-reason strings, human-oriented text formatting, internal `.tillr/loop` file layout, repository-specific glue (`tools/tillr-loop`, `just tillr-keep`), and any unbuilt design above.

## The design documentation is a snapshot

The module source is held to `tools/` on every gate run. The documentation is not. The drain edits design documents on every landing, and a lane's fence does not include this pack's `docs/` tree, so a freshness requirement in the gate would fail every lane after any such landing. Instead:

- [`files/local/share/tillr/docs/snapshot.json`](./files/local/share/tillr/docs/snapshot.json) records the commit the copies were made from, and the blob of each source.
- The gated suite proves every copy is exactly the transform of the blob its snapshot names. A hand edit, a missing or stray file, or a live item directive fails it.
- `python3 tests/pack-sync.test.py --fresh` says whether HEAD has moved past the snapshot. `just pack-docs` runs it after a refresh.

The transform adds a banner naming the source and commit, and re-resolves links. A delivered target gets a relative link, and anything else becomes a link to the repository on GitHub. It also makes `<!-- tillr: ... -->` directives inert. The planning index skips the copies through the exclusion in [`.vantage.toml`](../../.vantage.toml).

## Refreshing the snapshot

```sh
just pack-docs
git add yolo-packs/tillr/files/local/share/tillr
git commit -m "chore(tillr-pack): refresh the design documentation snapshot" -- yolo-packs/tillr/files/local/share/tillr
```

`just pack-docs` copies from HEAD, so commit the source edits first.

## Adding a document

The index is the only list of what ships. To add a document, add its row to a table in [`docs/tillr/design-status.md`](../../docs/tillr/design-status.md), with a first cell that links the document and a last cell that reads exactly `shipped`. Commit, run `just pack-docs`, and commit the result. A row whose last cell reads `not shipped` is listed for the reader and left out. The sync refuses a row that says neither.

## Deploying a change

The pack deploys through yolo. On the host, run `yolo host apply --assert`, then relaunch each running jail. Run `yolo pack lint yolo-packs/tillr` after every edit to the pack.
