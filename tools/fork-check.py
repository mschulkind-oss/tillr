#!/usr/bin/env python3
"""fork-check -- what `just done` verifies about the patch series this repo deploys.

WHY THIS EXISTS. What deploys from this repo, for pi and its extensions, is a PATCH
SERIES: `git format-patch --base` files under yolo-packs/ that yolo applies to an
upstream, builds, and runs. Two things break that between commits, and a diff shows
neither of them:

  * the upstream moves. Most of these packs follow a branch, and with no `follow` a
    launch builds the upstream's newest release (yolo's patched-forks design, the
    default follow rule), so a series that applied yesterday can stop applying
    today with nothing changed here; and
  * our own series changes and no longer replays, matches its working copy, or
    builds.

A launch stops on the first: it refuses, and the surprise lands on whoever restarts
the jail next. The same diff can land a document in the wrong shape -- a link that
resolves nowhere, a directive the renderer drops -- and nothing notices, because
the question was only ever asked of the series. So completion asks the launch's own
question, and this repository's document check, before the launch.

WHAT IT RUNS, and when. Only what the change can reach:

  1. RETIRED here: structural proof is a fast tree declaration in tools/gate/checks/.
  2. WHEN a series or its pack declaration changed, OR the upstream a launch would
     build from has moved: `yolo pack series check <pack>` -- the verdict a launch
     reaches, in a scratch mirror, naming the first member that conflicts. This is the
     check that would have caught the pi-subagents/v0.77.0 surprise. A series whose
     `patch_branch` this machine cannot resolve (the stamp is the host's) is declared
     elsewhere rather than skipped or failed: the pack's other series still decide the
     check, and the report names the series that were not answered.
  3. WHEN a series changed: `bin/pi-forks patch-branch verify
     --fork <name>` -- the patch branch's INV-3 (docs/pi/patch-branches/design.md): the pack's
     series is exactly the export of that working copy's patch branch, so a
     hand-edited, half-merged or stale series fails here instead of at a launch.
     Offline; a missing working copy refuses the run (see the FORK CHECKOUTS section).
  4. WHEN the declaration or series changed, or the upstream moved (2's own condition):
     `bin/pi-forks patch-branch merge-check --fetch --fork <name>` -- INV-2: the patch
     branch still merges the upstream a launch would build, naming the paths when it does
     not. It fetches, because a launch does: the ref it asks is the one the launch
     resolves, never the one this checkout happened to fetch last. A missing working copy
     refuses the run (see the FORK CHECKOUTS section).
  5. WHEN a series changed: `bin/pi-forks candidate --fork
     <name>` -- that the series replays at its base into a CLEAN checkout of its own,
     `.forks/candidate/<name>`, and the report names that path. Every step below reads
     that tree, never the shared `.forks/<name>` working tree, so a lane's or a hand
     fix's half-finished edits cannot become a verdict; the shared checkout still
     supplies the objects and upstream refs, so a missing one refuses the run (see the
     FORK CHECKOUTS section).
  6. WHEN a series changed: `bin/pi-forks candidate --build --fork
     <name>` -- that it builds, in that same clean checkout. Without the shared
     checkout it would need a clone of the upstream, an `npm ci` and a pinned-catalog
     fetch (all network), so the run is REFUSED (exit 2), naming the missing checkout
     and the setup command, rather than a silent omission. `--no-build` skips it and
     names that too. The source checks below run through the same `candidate` command,
     so they read the candidate and not a working tree; a fork with no `build` script
     gets `--deps` from them, because `--build` installs nothing for a source-only
     package.

  6a. WHEN a fork whose source this gate verifies moved (its series, its pack's
     declaration, or its upstream): `fork-quality` -- pi-session-search's `npm run
     check` and `npm run test`, and pi-subagents' `npm run typecheck` and `npm run
     test:unit`, in the clean candidate checkout. A build only transpiles; these are
     the typecheck and the unit suite it cannot make. A failing one is a verdict; a
     missing runner (exit 127) or a timeout (124) is an inability to answer. So are
     suite cases whose assertion is a real-time budget this machine meets on an idle run
     and misses beside the gate's other checks: they are QUARANTINED, named with their
     reason, their pile item and their expiry (`UNANSWERABLE_FORK_QUALITY`), counted apart
     from the passing checks, and only when EVERY case the run failed is one of them, so
     any other red in the same suite stays a verdict. An expired or ticketless declaration
     blocks its case again.
  7. RETIRED here: rendering changed Markdown is a change declaration under
     tools/gate/checks/. It takes the caller's base and head, not the remembered baseline.

  8. RETIRED here: scratch and design citations are fast tree declarations in tools/gate/checks/.
  8b. WHEN `config/tillr/seed-items.json` changes: the LEGACY-SEED INTAKE FREEZE --
     `D-QP1` retires the file by attrition, so it may only shrink: a net-new `item_id`
     or an edit to a surviving entry is refused, while a removal is the one permitted
     edit. The refusal names the ruling and the remedy -- file the item through the store
     and the owning document, not the legacy seed. The freeze is diff-scoped, so a run
     with no base says it could not judge the change rather than reading silence as a
     pass.
  9. WHEN the change can change what a storybook renders (the tool under
     tools/roster-storybook/ or tools/footer-storybook/, its own repository check, or
     the series it imports its widget from -- pi-subagents for the roster, pi-archimedes
     for the footer): regenerate it. These are the steps that write, and they write only
     under the durable artifact directory (or `$TMPDIR` when there is none), never the
     repository -- see the record note below. Each storybook's run.sh asserts the badge
     it renders, so a storybook that renders wrong is a failure.
 10. WHEN the change can change what the meta-muse surfaces render (the pack, the
     personal layer that selects it, or the two `tests/meta-muse-*.test.mjs` files): run
     them together with `node --test`. They render the pack at the HOST notch and hand the
     bytes to Pi's own ModelRuntime, so they are the only place a meta-muse surface
     regression is caught. A machine without `node` or `yolo` names the skip. The
     installed yolo build and node version are ADVISORY: a moved one is reported and does
     not plan this step.
 11. WHEN a yolo config layer changes (`config/yolo-jail/`, `tag-personal/config/yolo-jail/`,
     `tag-work/config/yolo-jail/`), the matt pack's manifest, or `tests/yolo-config.test.sh`
     itself: `sh tests/yolo-config.test.sh` -- the repository's only assertion on those
     layers, and the one place the work class's `profile` selection is pinned. A machine
     without `python3` or `yolo` names the skip.
 12. WHEN the change can change what a WORK-CLASS yolo render produces (the matt pack, its
     `files/` overlays and extensions, the four `yolo-packs/matt/tests/` files this step runs --
     the render tests `effort-thinking.test.mjs` and `bedrock-context.test.mjs`, and the overlay
     tests `research-tools.test.mjs` and `agent-inherit-skills.test.mjs` -- or the work layer
     that selects `bedrock`): run all four through `node --test`. The render pair drive the
     HOST render -- the only notch that composes yolo's derives -- with the work layer
     included, so they are the one place a
     work-class regression is caught: a lost `bedrock` pack selection empties the derived
     settings table, which is what these pin. The overlay pair pin the `pi/settings`
     `subagents.agentOverrides` grants that same render composes: a tool name allowlisted
     without the provider that registers it is a silent NO-OP, and a role whose `tools` entry
     drops a builtin is disarmed at the next launch, with nothing else in the gate noticing.
     A machine without `node` or `yolo` names the skip; a test whose `.forks/pi-subagents` or
     installed pi-subagents checkout is absent skips with that fixture named.
     must not depend on what the shell that ran the gate exported. A machine without
     `node`, or with no Pi package root to name, reports the skip with its reason.
 14c. WHEN the durable steering timer `yolo-packs/matt/files/extensions/steering-timer.ts` or
     its suite changes: run `yolo-packs/matt/tests/steering-timer.test.mjs` with `node --test`.
     It is the only assertion on the mechanism under the active-steering protocol -- the record
     outside any session, the check-in injected at `next_due_at`, the ack as the only
     deadline-advancer, the grace re-injection and its alarm, the coalesced catch-up, and the
     re-arm a replaced session gets -- and it drives a fake clock through the real registration,
     so it needs `node` and nothing else. A machine without `node` names the skip.
 15. WHEN anything under a Go tool directory changes -- any entry of `GO_TOOL_DIRS`
     below, which is the list, so a directory added there is covered without editing this
     paragraph -- run the full `go test ./...` behavior suite in THAT
     directory: one step per directory, so a change under one tool is compiled by its own
     build and never answered by a sibling's. The step runs the WHOLE suite, with no
     `-short`: the cases that drive real child processes and wait on real expiry are the
     cases a landing must judge. They used to be a second step planned only by a full
     audit (`--all`), which the landing path never passes, so no gate ran them and a red
     case landed (item 554); item 570 wired them into the routine step and deleted the
     second step and the quarantine that declared the skip. The Go tools under the
     rcm-excluded `tools/` are run from this checkout and no launch builds them, so this is
     the only place a tool that stopped compiling -- or a failing unit test -- is caught.
     The build runs with
     `CGO_ENABLED=0`, which is the whole point of the binary's portability; the standard
     shared Go build cache is left alone (no per-worktree `GOCACHE`), per
     `yolo-packs/matt/briefing/shared-go-cache.md`. A machine without `go` names the
     skip, per directory.
 16. WHEN `tools/fork-check.py` or `tests/fork-check.test.py` changes: run the suite
     with the interpreter running this gate. Nothing else executes it, so an edit to the
     checker was otherwise verified by the gate it had just changed. A failing case exits
     non-zero and is reported as NOT VERIFIED -- a step with no verdict, not a skip and
     not a series verdict, even when its transcript quotes one.
 17. WHEN the rank-order suite, the rank alphabet it reads, or the series that builds its
     working copy changes: run `node --test yolo-packs/matt/tests/
     pi-subagents-rank-order.test.mjs`. It is the one place a capacity-queued dispatch's
     ORDER is pinned -- it imports the production pump and the ordering helper from the
     `.forks/pi-subagents` checkout and reads the queue's own rank alphabet -- and
     it was hand-run only. Without that checkout every case skips and `node` still exits 0,
     so a missing working copy REFUSES the run (exit 2) rather than a green over eight skipped
     cases; a machine without `node` names the skip too.
 19. WHEN `tests/session-forensics.test.py` or anything under `tools/session-forensics/`
     changes: run `python3 tests/session-forensics.test.py`. It is the only assertion on the
     hang-analysis report -- judge passes, slow tool calls, telemetry gaps, a turn that never
     closed -- and it landed with the tool while no step here named it, so a gate that never
     ran it would have let it rot. A failing case is a verdict about the change, not "not
     verified".
 19b. WHEN `tests/tillr-muse-backend.test.py`, anything under
     `tools/tillr-run/backends/`, or `config/tillr/` changes: run
     `python3 tests/tillr-muse-backend.test.py`. It is the only assertion that the
     four adapters' dialects produce one report shape and that a footerless stream is
     refused; the gate discovers nothing under `tests/` by glob, so a new suite that no
     step names never runs.

 19a. WHEN `tests/pi-inspector.test.py`, the `pi-inspector` extension, or anything under
     `tools/pi-profiler/` changes: run `python3 tests/pi-inspector.test.py`. It starts the
     real pi entry point and proves an attachable loopback inspector exists with no signal
     sent, and that the profiler finds it by pid -- the half `tests/yolo-config.test.sh`
     cannot see, because that one only pins the jail-wide spelling's absence. A machine
     without `pi` or `go` names the skip.
 19b. WHEN `tests/session-budget.test.py` or anything under `tools/session-budget/`
     changes: run `python3 tests/session-budget.test.py`. It is the only assertion on the
     session-budget report -- prompt, lag and telemetry against their stated bounds -- and
     it landed with the tool, so a gate that never ran it would have let it rot. A failing
     case is a verdict about the change, not "not verified".
 20. WHEN anything under `tools/session-liveness/` changes, or the `pi-fork` series under
     `yolo-packs/pi-fork/patches/` changes: build the clean checkout of the pi series that a
     verdict can be about (`bin/pi-forks candidate --fork pi --build`), then run
     `sh tools/session-liveness/run` against it through `PI_FORK` -- the headless liveness
     harness, which asks whether a session returns to idle within a bound and whether a later
     send-now starts work. The harness judges the TREE's series: its fork imports resolve
     through `PI_FORK`, so a green is evidence about the series the change edited and never
     about the shared, gitignored working copy, whose content can differ while the tree does
     not. A build that cannot be made is the inability to answer, refused before anything
     runs, and the run script's exit 127 is the same rather than a pass. A failing
     spec is the verdict the harness exists to make (it is red until the fork fix lands), so
     it fails the gate rather than reading as "not verified". `.forks/pi` is gitignored and
     so can never appear in a diff; the harness and the series are the inputs the plan can
     see.

     THE LIMIT THIS CHECK HAS: it runs against that untracked shared `.forks/pi` checkout, not
     against the series the branch is editing. A green therefore says the checkout's fork
     sources pass the harness; it does not say the branch's series does. A series edit that
     has not been re-exported into `.forks/pi`, or a checkout left on another branch, can be
     green here and still be wrong -- read the green as evidence about `.forks/pi`, never as
     verification of the series itself.
 22. RETIRED here: commit citations are a change declaration under tools/gate/checks/.
 23. WHEN `bin/land`, anything under `tools/mq/`, or `tests/land-queue.test.sh`
     changed: `bash tests/land-queue.test.sh`. THIS STEP IS RETIRED. The merge queue and its
     shim are deleted (docs/design/mq-retirement.md), so there is no step here and nothing to
     plan; the number is kept so a reader who remembers it learns where it went. What the
     suite pinned that outlived it moved: the fast-forward author normalization and the
     identity refusals to `tools/tillr-land/main_test.go`, and the gate's per-check report
     line to tests/fork-check.test.py (section 60). Everything else is named with the
     guarantee it no longer holds in the retirement document's test disposition.
 23a. RETIRED here: pack synchronization is a fast tree declaration in tools/gate/checks/.
 23b. WHEN `tests/tillr-docs.test.py`, anything under `tools/tillr*/` or the
     `tillr-operating` skill changes: `python3 tests/tillr-docs.test.py` -- the layer-2
     contract of docs/design/tillr-distribution.md section 3, where `--help` is the one home
     of a flag. It builds the eight binaries and runs `--help` on every
     `tillr <noun> <face>` and `tillr-<noun> <face>` the module READMEs and the skill name,
     so a face that does not exist fails the gate instead of surviving in a document, and it
     refuses a Markdown flag table (a header naming a flag, or a line that is an option
     list) in any of those documents. It also pins the stamped Known Gaps callout all eight
     module READMEs open with. The gate discovers nothing under `tests/` by glob, so this
     step is what runs it.
 24. WHEN `hooks/pre-push`, `bin/patch-branches-publish` or
     `tests/pre-push-guard.test.sh` changes: `sh tests/pre-push-guard.test.sh` -- the
     guard that publishes the patch branches a manifest names, reports on every push
     which hook file git ran and whether it is this repository's own, pushes a manifest's
     patch branch itself, then lets the manifest through; only when that push fails does
     it refuse the manifest push, naming the manifest, the locator and the exact remedy.
     The same suite pins the publish step's rewrite rule: a rewrite is published on its
     own while the remote still holds the value this clone published (a re-export), and
     refused, naming the remote commit, when the remote moved away from it. The suite
     builds its own bare origin, work repository, second clone and diverged branch, so it
     reaches no network.
 25. ALWAYS: warn about tracked .ts files above 2,000 lines. Added-file refusals are
     change declarations under tools/gate/checks/ and require the caller's base and head.
 28. WHEN `bin/pi-forks`, `bin/pi-forks.list`, `tests/pi-forks.test.sh`, anything under
     `tests/pi-forks/`, a series directory, or a pack's declaration changes:
     `bash tests/pi-forks.test.sh` -- the driver's own 332 cases in 26 scenarios, which
     build fixture upstreams, series, packs and a manifest in a scratch directory and
     drive `bin/pi-forks` as a command: setup materializes a checkout and refuses a
     divergent one, export writes the series back whole and refuses what it cannot
     export, check replays it, the patch-branch verbs answer INV-2 and INV-3, and the
     linked-worktree refusal names the checkout that owns `.forks` while a sourced
     driver stays callable there. Every patch-series step above execs that driver, and
     nothing ran the cases that pin it, so a regression reached a launch or a human's
     hand first -- and the suite was itself broken on a clean tree, aborting under
     `set -e` after 80 passing cases because that refusal fired on a lane worktree. The
     runner shards the scenarios across the CPU count, each in its own process and temp
     root, so a case in one cannot see another's state. It needs no `.forks/` working
     copy and reaches no network; a failing case is a verdict about the change. About
     10s as a gate step on a shared 12-core box.
 29. WHEN `bin/hooks-install`, anything under `hooks/`, the `.justfile` recipe that calls
     it, or `tests/hooks-install.test.sh` changes: `sh tests/hooks-install.test.sh` -- the
     installer `just hooks-install` runs, which keeps the shared hooks directory in step
     with `hooks/`. A copy that carries this repository's `tillr-hook:` marker is
     replaced when its bytes differ, and the run reports that as drift; a hook without the
     marker is left alone and chained as `<name>.local`; every run prints one line per
     hook. The 2026-10-23 failure is the reason: `.git/hooks/pre-push` was one lane's
     revision while the landed hook was another, and the installer asked only whether a
     file existed, so the guard on a push was never the guard in the tree. The suite
     builds its own repository under the system temp directory and passes it as `--root`,
     so it never writes the shared hooks directory this checkout's git reads.
 30. WHEN the shared pipeline-evidence format (`tools/pipeline-evidence/`), its
     conformance suite (`tests/pipeline-evidence.test.py`), or the gate's own record builder
     (this file) changes: run that suite on the interpreter running this gate. The format
     (`docs/pi/pipeline-evidence.md`) exists so a board, a lander and a gate can record the
     same kind of fact without sharing a process or a store. The step validates the gate's
     own record against the one schema and feeds the velocity report synthetic records
     written to the format, so a shape drifting either way is a red here rather than in a
     reader that has already lost the data. It needs no lander and no board binary: the
     merge queue's records are history the format still reads, and the fixtures are what
     pin the shape now.
 31. WHEN a shipped extension source, the pack manifest that stages it, the reload-residue
     registry (`docs/reference/reload-residue.md`), the audit or the inventory it reads
     changes: `python3 tests/extension-residue.test.py` -- every provider registration, armed
     timer and listener on an object pi does not track is cleaned on `session_shutdown`,
     bounded by its own owner, or an accepted exception with its reason. A new timer with no
     teardown is invisible in a diff: it works, and it keeps working after the session that
     armed it has gone. The step audits the real tree and then breaks one thing at a time in a
     fixture, so a guard that stopped catching a site fails here instead of staying green.
 32. WHEN the active-steering skill (`yolo-packs/matt-agentwork/skills/active-steering/`)
     or `tests/active-steering-skill.test.py` changes: run that test on the interpreter
     running this gate. The skill is the protocol layer of a steered lane, and its cadence
     is the steering record's rather than a number in the prose -- a number written back
     in is stale the moment the operator changes the cadence, and a reader cannot tell a
     stale one from a current one. The test also pins that the ack tool (`steer_checkin`)
     is named, because it is the only thing that advances the deadline, and that a record
     with `missed_count` above zero is a drift signal. Nothing else in this gate reads a
     pack skill's text, so this is the only place the regression is caught.

A PLANNED FORK STEP WITHOUT ITS CHECKOUT REFUSES. The six fork-dependent steps --
the patch branch's merge-check (INV-2) and verify (INV-3), the replay (`bin/pi-forks
check`), the build, the rank-order suite, and the session-liveness harness -- read
working copies under the forks directory: the checkout's own `.forks/`, or the one
under the repository root `PI_FORKS_ROOT` names. Each planned step carries the paths it
reads in `needs`; after planning, a need absent from the forks directory REFUSES the run
(exit 2) instead of planning from it. The report names each missing path with the
step that needs it and the command that provides it (`bin/pi-forks setup --fork
<name>`, or the liveness symlink for `.forks/pi`), and the recorded baseline is not
advanced past a change nothing examined. That is how a lane worktree -- which shares
the record through the common Git directory but never ran a setup -- is told to
provide the checkouts rather than handed a VERIFIED over steps that never ran, and
how a worktree points at the shared ones: `PI_FORKS_ROOT=/workspace just done`. A
checkout that is present behaves exactly as before; `PI_FORKS_ROOT` changes only where
the probes look, and it names a repository root for `bin/pi-forks` too -- the variable
has one reading, and `.forks/` is derived from it rather than being it.

THE SERIES COME FROM THE TREE BEING VERIFIED, THE CHECKOUTS FROM PI_FORKS_ROOT. The
series, `bin/pi-forks.list` and the pack declarations are read from the tree this run
is in -- the worktree -- while `PI_FORKS_ROOT` says only where the untracked `.forks/`
checkouts live. Every fork step carries that tree in `PI_FORKS_SERIES_ROOT` so an
inherited value cannot move it, and the report names both roots when they differ. The
split is what makes a lane that changes a series verifiable at all: with one value for
both, a landing run in a worktree read the main checkout's 11-patch series and reported
a correct 12-patch series as not its export (2026-10-24).

THE UPSTREAM IS RESOLVED BY THE BRIDGE THAT CARRIES THE LAUNCH'S RULE, AND THEN BY A
PROBE. `bin/pi-forks patch-branch target --fetch` answers, per fork, the ref a launch
would build -- the release tags merged into the followed branch and containing the
series base, the branch head under `follow: "head"`, or the pin -- after refreshing the
upstream the way a launch does. That is the ref a verdict rests on and the ref the
record keeps, so the gate cannot name a release the launch never builds. It costs one
fetch per followed upstream per run, and it is worth it: the same probe read off a tag
list alone, or off a checkout that had not fetched since the last release, blessed a
pi-subagents series against v0.83.0 while every launch had moved to v0.83.1 and would
refuse the merge (2026-10-28).

When no checkout can answer -- a machine with no `.forks/`, or an upstream it cannot
reach -- the FINGERPRINT falls back to the tag list read by one `git ls-remote` per
distinct upstream, which is a few hundred milliseconds and needs no checkout. That
probe applies no ancestry: it can call an unmoved upstream moved, which costs one
check, and never the other way round, and the plan SAYS when it answered instead of the
bridge, so a green headline never reads as "asked the ref a launch builds". The
fingerprint of every upstream travels in the record, so an unchanged one costs a
comparison rather than a fetch.

THE INSTALLED YOLO BUILD IS ADVISORY: it is reported, and it plans nothing. Four steps
are decided by the yolo binary answering on PATH rather than by a file in this
repository -- the series check (it applies the series), `tests/yolo-config.test.sh` (it
shells `yolo check`), the meta-muse tests and the matt pack's render tests (they drive
yolo's derives) -- so a yolo upgrade can change any of their answers with no path here
moving. That is the blindness this gate carried from 2026-10-16 to 2026-10-18: a new
build made the matt pack's `bedrock-context.ts` inert, nothing planned `matt-render`,
and a green headline said nothing about it. Planning those steps from the build's
identity was the answer, and it was wrong twice over: the record is shared through the
common Git directory, so a run on another machine -- a jail, whose `yolo` is its own
build -- planned them on every run for a build nothing in this repository had changed;
and a rebuild at the same commit forced the same replan. So the identity is still
recorded with the baseline and a different one is still REPORTED, but those steps are
planned only by the paths they watch (see A GATE VERDICT IS LOCAL TO THE MACHINE THAT
RAN IT below for why an unreadable input never vetoes a verdict). The identity is
`yolo --version --full`'s build COMMIT, falling back to its binary sha256 for a build
that names no commit: a rebuild at the same commit is not a different build, so it never
reads as a change, while two builds that differ in their rules name different commits
(docs/pi/yolo-source-provenance.md: a sha256 is identity, a version string is not). A
yolo that cannot name its sha256 is declared UNREADABLE in the report, with the command
a reader must run instead -- an unpinnable input is named, never guessed at.

THE INSTALLED NODE VERSION IS AN INPUT, and the plan reads it the same way. The
steps decided by the node binary answering on PATH rather than by a file in this
repository -- every step this gate runs through `node --test` directly (the meta-muse
tests, the matt pack's render tests, the rank-order suite, the steering-timer suite,
the seat-profile tests) or through a bash runner that needs node (the
two storybooks, the liveness harness) -- so a node
upgrade can change any of their answers with no path here moving. So the installed
version (`node --version`) is recorded with the baseline, and a run whose version
differs from the recorded one plans those steps whatever the diff says. The
`fork-quality` npm steps stay out: they answer through `npm`, so they are covered by
npm's command in the report rather than planned here. A node that names no parseable
version is declared UNREADABLE in the report, with the command a reader must run
instead.

WHAT IT DOES NOT COVER, and always names in the report: the launch's own build of
the newest upstream (only a pack whose series changed is built, and at its base); a
change to an external tool this gate cannot identify -- a new `uvx`, `go` or `npm`
(each is declared rather than pinned, and the report names the command that covers
it); the HOST's own yolo build and the patch-branch stamp, which are DECLARED
ELSEWHERE in one line each with the command that answers them; and anything a machine
without `yolo` or `uvx`, or without network, cannot answer.

WHAT A VERDICT COVERS, in the headline's own words. A step is planned when the change
touches a path it watches -- never because the installed yolo build or node version
moved, which are ADVISORY (reported, and planning nothing) -- so a verified run may have
examined a subset of the tree, and a step that did not run is not a step that passed. The
headline counts four things apart -- checks passed, checks that did not run because
nothing they watch changed,
checks this machine could not answer, and inputs DECLARED elsewhere -- and the report
names every step it did not run with its reason. The gap that remains is structural: a
step gated on repository paths cannot see drift from outside the repository, and a yolo
upgrade that changed what the matt pack renders is exactly that -- no path here moved, so
nothing was planned, and a green verdict said nothing about it from 2026-10-16 to
2026-10-18. The installed yolo build and the installed node version are now reported
rather than planned (see above); the rest are named in every report with the command
that covers each. The record keeps the count, so a plan that shrank between two commits
is visible there too, not only in a headline that read the same both times.

A GATE VERDICT IS LOCAL TO THE MACHINE THAT RAN IT, and a machine-local input is
DECLARED rather than blocking. Two inputs a verdict would like to read are answers that
live on another machine: the patch-branch stamp (`yolo pack series check` resolves a
pack's `patch_branch` from a record written on the host, and a jail never sees that
record) and the host's own yolo build (a jail reads the build on its own PATH, not the
host's; a host run reads that build itself, so it declares nothing there). Neither says
anything about whether the change holds, so neither may veto the run. Such an input is
recorded as `declared elsewhere` -- with the reason it cannot be read here and the
command that answers it there -- and it never turns the run into 'not a verdict'. The
report prints one line per declared input and the run entry keeps them. A declaration
never covers a failing check and never stands in for one: the series that
COULD be applied in the same pack must all have been (an unrecognised transcript line
beside a non-zero exit still fails closed), and every check that ran must have passed,
so a VERIFIED run with declarations is one where nothing that could be checked failed.

AN EXIT STATUS SAYS WHICH OF TWO THINGS HAPPENED, and the two are never the same
number. 0 is VERIFIED. 1 is a VERDICT: at least one check ran and failed, or a document
conflicts with the tree. 2 is NOT A VERDICT: the run refused to judge (a dirty path a
check reads, a missing fork checkout, a record that could not be read, a write that
landed while the plan was built) or a check reached no verdict at all, so nothing was
verified and nothing was disproved. A caller that reads the status as a verdict is why
the two
numbers have to be disjoint: on 2026-10-27 a landing pass recorded a REFUSED run as a
candidate's red, named it `exit:1` because the gate had blamed no check, and the same
candidate landed when the same gate re-ran -- so the red described the tree the gate
was handed, never the candidate (item 262). the board tool reads 2 as a refusal now;
the two dirty refusals below were the paths that still answered 1.

WHICH NUMBER A FAILED CHECK EARNS IS A PROPERTY OF ITS KIND, and every kind is declared.
A check that ran and failed is a VERDICT -- 1, printed `FAIL` -- unless its kind has a
declared way to say "this machine could not answer" (a runner that timed out or is
absent, a driver that names no invariant, the gate's own audit suite, the citation
guard's refusal). `CHECK_KIND_GROUP` holds that table and a kind it does not name is a
verdict, the failing side. It was an allowlist with a fallback until 2026-10-28, and a
kind nobody added to it answered 2 by default, so the red was reported as `ERR`/exit 2 --
"not a verdict" -- while the gate had judged that tree. The landing queue had no check
name to record and keyed the flake on the exit status, `exit:2`, which no re-run can
settle (item 284). The report's own line names the kind either way, which is why the
record reads `fork-check: FAIL <kind> -- <label>` and never an exit status.

A FAILED CHECK IS DIAGNOSED FROM ITS OWN OUTPUT, NEVER RE-RUN BLIND. A check that fails
inside the gate may be failing because of what ran beside it: a suite whose index worker
is starved, a build that reaches its timeout under load, two suites colliding in one temp
root. On 2026-10-24 the pi-session-search suite exited with its index worker's code 3
inside the gate and passed standalone in the same tree -- 158 tests, 0 failures, 62s --
and that one unexplained red starved the landing queue for a night. The answer is not a
second run: a re-run that passes says only that something differed between two runs, and
it cannot say what, so one unexplained red becomes an equally unexplained yellow. So a
failure costs exactly ONE run of the failing check, and what the gate produces instead is
a TRIAGE PACKET read out of that run's own output: the failing case's name and file, the
assertion text, the paths the assertion names, the commits that touched those paths, and
the one-file command that reproduces it. The packet is written machine-readably beside the
record and printed by the report, so the repair path dispatches a debugger with the packet
rather than with a request to run the gate again. A debugger may run that single file, or
`--test-name-pattern` on it, as many times as it needs to settle determinism -- and may
not run the gate or the whole suite, which is the cost the retry existed to avoid.

A CHECK WHOSE VERDICT IS DECIDED BY REAL TIME IS DECLARED EXCLUSIVE IN THE PLAN.
The one failure a packet cannot settle by itself is a step whose assertions are about real
time, because what differs between the failing run and a passing one is the machine it
shared. Such a step is declared exclusive where it is planned (`EXCLUSIVE_CHECKS`), and
the runner starts it only when nothing else is running, so the contention is removed from
the run instead of being re-tested at the verdict; the report prints it beside the check.
The test of whether a step belongs there is its assertion, not its cost: a suite that
spawns real children is slow but decided by what the product recorded, and load only makes
it slower. A suite whose heartbeat is a count of recorded yields and whose
freezes are asserted across a directly driven pulse is decided by what the product
recorded, not by a clock. Real-process files that spawn real children
still run one at a time inside their own batch, which is a scheduling choice about that batch, not a claim
that a clock decides them.

THE RECORD lives in the repository's common Git directory
(`yolo-fork-check/record.jsonl`): the verified commit and tree per branch, the newest
one of those in a shared entry, the upstream fingerprints verified with it, the installed
yolo build's identity (its binary
sha256, build stamp and commit), the installed node version, and one bounded entry per
run. A run entry keeps per check only its kind, label, exit code and seconds -- what
a verdict needs, and not what a diagnosis needs -- plus the inputs it DECLARED
elsewhere (label, reason, command), so a reader can tell later which part of a green
run's verdict rested on another machine. So a run that FAILS also writes the
failing checks' own output, with each one's command, exit code and last
EXCERPT_LINES lines, to `yolo-fork-check/report.txt` beside the record, and points the
run entry's failed checks at that file. Without it the only copy of a host run's
failure is the terminal scrollback of whoever ran it, which is a request to paste
rather than evidence a reader can return to; a run with nothing failing writes no
excerpt and leaves the last failure's file in place, so a later green
run cannot erase the thing a reader is diagnosing. The record also keeps the TRIAGE
RECORD, one entry per check whose command has failed, holding the newest packet's commit,
time and cases -- it replaces the flake ledger a re-run used to fill, because a failed
check is no longer re-run and its diagnosis is what a later reader needs. Sharing the common
directory is deliberate -- every worktree of this repository reads the same record,
so a fresh checkout does not pay for a check the repository already passed. Apart
from that file and the regenerated storybook artifacts (which live outside the
checkout), this command is READ-ONLY with respect to the repository: it never
formats, stages, commits or deploys.

TWO GATES NEVER SHARE THE RECORD AT ONCE -- BECAUSE THE RECORD IS APPEND-ONLY.
The record above was one JSON document, so two `just done` runs in two worktrees
read-modify-wrote the same bytes: each loaded the state, appended its own run
entry, and saved, and the second save silently dropped the first run's entry. A
lock and a merge under it papered over that shape; the shape itself is what was
wrong. The record is now a JSONL log: every save APPENDS one line describing what
that run changed -- its run entry, the baseline it advanced, the fingerprints it
verified, the triage entries it set or cleared -- and a read folds the lines in
order, the newest entry per branch (and per fingerprint, and per triage command)
winning. Appending needs no lock and no merge, because two runs append two lines
and both survive; a reader never writes. The folded bounds are `MAX_RUNS`,
`MAX_BASELINES` and `MAX_TRIAGE`, so the log is compacted into one snapshot line
when it grows past `MAX_RECORD_LINES`, and the live file stays within a few
megabytes rather than growing without end. A line that cannot be parsed (a write
torn by a killed process) is skipped and counted, never guessed at. `state.json`
from before the log is migrated once, in place, and left behind as a backup.
`--lock-timeout` and `--no-wait` are still accepted, because callers outside this
file pass them, and no longer mean anything: there is no record lock to wait for.
The measured cost of the change -- `Record` load and `save` before and after, on
the same live record -- is in the commit that made it and in
docs/pi/gate-record-log.md.

THE BASELINE IS KEPT PER BRANCH, AND PLANNED FROM THE NEWEST VERIFIED COMMIT THIS
HISTORY CONTAINS -- OR, FAILING THAT, ITS FORK POINT. The record is shared through the
common Git directory, so a single shared baseline was advanced by whichever worktree ran
last: a verified run in one worktree moved it to THAT worktree's commit, and the next run
in another worktree held a baseline its branch did not contain. `git diff baseline..HEAD`
over such a commit names the paths that differ between two unrelated trees -- none at all
when their content matches -- so the plan was empty, the steps gated on changed paths
(`documents`, `scratch`) were never planned, and the headline still read VERIFIED. A
landing lane recorded a candidate sha that never reached main and got exactly that.
Refusing the diff (the fix this replaces) made the vacuity impossible to plan from, but it
made one worktree's verified run STOP another worktree's run, which is the same ownership
bug in a louder form. So the baseline is recorded per branch, keyed by the full ref the
run's checkout has checked out, and a run advances only its own branch's entry: two
worktrees are two branches, and neither can move the other's base. Every entry -- this
branch's, another branch's, the shared newest one -- is a CANDIDATE, and the run plans
against the newest candidate that is an ancestor of HEAD. That rule is what makes the
base safe: an entry is written only by a verified run, so a candidate this history
contains is a commit the repository verified, and the diff from it names exactly the
paths that changed since that verification.

With no such candidate the base is the FORK POINT with an entry, the oldest one offered:
the integration branch advanced past the commit this branch was cut from, so its verified
tree is not this branch's, but everything after their common ancestor on this branch is
this branch's own unverified work, and the diff from that fork point names exactly it.
Without that fallback the run would be a first run, which plans the series steps from
nothing and never plans the path-gated ones (`documents` above all), so a lane's own
document would go unchecked behind a VERIFIED headline. An entry that offers neither --
another branch's work with no common history, or a verified tree that is AHEAD of this
branch -- is skipped rather than refused, and a run with nothing at all is a first run
that checks everything. Nothing is ever diffed against a commit this history lacks, so
the vacuous plan cannot be built at all, and no run refuses another's. When this branch's
own entry is one of the skipped ones the report says so, because a verified point that
vanished from its own branch is news. A baseline whose tree equals HEAD's plans nothing
because nothing changed.

THE PREFLIGHT (`--preflight`, `just preflight`). A gate check is judged in conditions a
developer's shell does not have: its steps run beside each other and beside every other
lane, some suites scrub HOME and XDG_STATE_HOME and demand an explicit board store, and in
a jail the whole run is uid 0. A check that only holds in a developer's shell is green
until a landing asks the gate's question, and then it is found one landing at a time.

So the preflight runs the SAME checks this change plans -- `plan_gate`, the gate's own
planning -- in a small matrix of those conditions, and reports every failure in one pass:

  baseline       the caller's own shell, one check at a time: where a check passes today
  gate-serial    HOME and XDG_STATE_HOME scrubbed, BOARD_STORE_STRICT, one check at a time

There is no cell that adds CPU load. One used to exist (`gate-parallel`, which also ran
the checks concurrently beside one burner process per core) and it was deleted on
2026-10-31: a check that goes red under induced contention cannot be told apart from a
slow box, so the cell's verdict had no subject. `GATE_TIMING_RULE` below is the rule that
replaced it, and `tests/fork-check.test.py` fails the gate when a check asserts elapsed
time or spawns load.

`--cells` selects columns and `--only KIND[:LABEL]` narrows the checks; the report names
whatever either one omitted, so a narrowed run cannot read as full coverage. The matrix
is `PREFLIGHT_CELLS`, and `tools/preflight-declarations.json` is where a check declares a
cell it cannot pass, with the reason and the repair it waits on -- a declaration is
counted in every report and never silences a different cell.

It is a DIAGNOSTIC and never a verdict: it writes no record, advances no verified
baseline and appends no line to the log, so it runs beside a real gate. The report says
whether the tree was unmodified, because a finding about a dirty tree is a finding about
the working tree. Its findings are read BEFORE a fix is attempted -- that ordering is the
whole point, since a fix applied first destroys the evidence of what the environment did.

Usage: fork-check [--since BASE | --all] [--build|--no-build] [--explain] [--jobs N]
       [--heavy-jobs N] [--tier {landing,rolling}]
       fork-check --triage [SELECTOR]
       fork-check --preflight [--cells NAMES] [--only KIND[:LABEL]]
                  [--report PATH] [--json] [--explain] [--since BASE | --all]
       fork-check --replay --only KIND[:LABEL] [--only ...]
  --since BASE diagnose only the committed BASE..HEAD range, without unrelated drift probes;
               NOT completion approval; never advances the last verified baseline.
  --all       check accumulated changes and force every series, including upstream drift
  (default)   check all changes since the newest verified commit this history contains,
              including upstream drift
  --build     build each fork whose series changed (the default)
  --no-build  do not build; the report says build verification did not run
  --explain   print the plan and run none of it. The plan still refreshes each fork
              checkout's upstream refs -- one fetch per followed upstream -- because
              the upstream it names must be the one a launch would build; it runs no
              check and records no verdict.
  --tier T    plan one tier of the gate. `landing` keeps the pre-publish kinds and
              records each rolling kind the diff selects as owed to the rolling tier;
              `rolling` plans the deferred kinds. Absent is the full gate, which is what
              the merge queue runs, and a landing-tier verified run does not advance the
              recorded baseline.
  --jobs N    run at most N independent checks at a time; the default is this machine's
              core count, and 1 runs them one after another. Two checks that declare the
              same written path never overlap whatever the bound.
  --heavy-jobs N
              admit at most N HEAVY checks (declared above CHEAP_CHECK_SECONDS) at a time,
              separately from the whole pool, so cheap checks stay wide while suites,
              fork-quality and pi-forks are bounded. The default is selected by the
              measured sweep in docs/pi/gate-heavy-admission-2026-11-02.md; a value at or
              above --jobs is the old flat admission. The cap changes admission only,
              never the planned or executed set.
  --lock-timeout SECS
              ACCEPTED AND IGNORED. The record is append-only, so there is no record
              lock to wait for; the flag stays because callers outside this file still
              pass it, and it will be refused once they stop.
  --no-wait   ACCEPTED AND IGNORED, for the same reason as --lock-timeout
  --triage    print the last recorded failure's triage packet as JSON, run nothing
  --preflight run the planned checks in every cell of the hostile matrix and report every
              failure at once; writes no record and advances no baseline
  --replay    run the checks `--only` names once, on the current tree, and write no record:
              the base-replay a driver uses to tell a red trunk from a red candidate
              (exit 0 all green, 1 any red, 2 could not answer)
  --cells NAMES
              comma-separated preflight cells (default: baseline, gate-serial)
  --only KIND[:LABEL]
              preflight only the checks this names; repeatable or comma-separated
  --report PATH
              also write the preflight's JSON findings to PATH

Exit: 0 verified, 1 a check failed (a series that does not apply, or a document finding),
2 nothing could be verified (missing tool, no network, a check that produced no verdict, a
document check that could not run, or an added document line that cites a commit HEAD
does not contain).
"""

from __future__ import annotations

import argparse
import concurrent.futures
import contextlib
import copy
import fcntl
import hashlib
import json
import os
import re
import shlex
import shutil
import signal
import subprocess
import sys
import threading
import time
import uuid
from datetime import date, datetime, timezone
from pathlib import Path
from typing import Callable

SCHEMA = 1
PRODUCER = "tools/fork-check.py"
# The landing path's batch base. A landing has already assembled the commit its gate
# judges, so the diff that decides which checks run is that batch's -- not whatever
# verified baseline the scratch worktree's shared record happens to offer. The board sets
# this to the batch's base; a plain gate run leaves it unset and plans from its own
# baseline. It names the base only, so the run is still a verdict: see plan_gate.
PLAN_BASE_ENV = "FORK_CHECK_PLAN_BASE"
# The landing path's per-check reuse list. A batch replayed onto a moved tip is re-gated,
# and a check whose watches the landed paths cannot reach is a result the caller already
# holds: this names those checks, comma-separated, in the `kind` or `kind:label`
# vocabulary `EXCLUSIVE_CHECKS` and the preflight's `--only` use. The gate does not run
# them and reports them in the plan line's `reused` field, so the caller can merge the
# result it holds with the ones this run bought. The integrity set is never reused: it is
# a property of the tree, and the tree is what moved. A gate that does not read this
# variable runs every check, which is the safe direction -- the caller sees no `reused`
# field and knows the run bought everything.
REUSE_ENV = "FORK_CHECK_REUSE"
# The record is an append-only JSONL log, one line per save. These four numbers are what
# keep it from growing without end.
#
# `MAX_RUNS` and `MAX_BASELINES` bound the folded view: the newest per-run entries, and
# the newest per-branch baselines. `MAX_TRIAGE` is the bound the triage ledger did not
# have: `clear_triage` removes only a check that ran here and passed, so a failure for a
# check nothing re-plans stays in the ledger forever -- the live record carried 160 of
# them on 2026-11-15 while the run and baseline maps stayed at their caps. `MAX_RECORD_LINES`
# bounds the FILE: when a save would push the log past this many lines, it is compacted
# into one snapshot line holding the folded view, so the bytes on disk stay within the
# same order as the view rather than growing a line per run forever.
MAX_RUNS = 20
# How many branches' verified commits the record keeps. A lane worktree gets its own
# entry, so the map would otherwise grow with every lane this repository ever gated;
# the newest entries are the ones a lane plans from, and an entry for a branch that has
# been deleted and rebased is only ever a candidate that this history does not contain.
MAX_BASELINES = 32
# How many recorded failures the triage ledger keeps, newest by `at`. One entry per
# failing command, so the cap is generous against the handful of steps that can fail at
# once; what it removes is a command nothing re-plans, whose packet a reader would
# otherwise read as a failure that still stands.
MAX_TRIAGE = 64
# How many lines the log may hold before a save compacts it. Each line is one run's
# delta and a run entry is the bulk of it, so this is the file-size bound: compaction
# rewrites the log as one snapshot line plus one line per kept run -- so its floor is
# `MAX_RUNS + 1`, and a bound at or below that would compact on every save -- and this
# number is how many more deltas may follow before it happens again. The file therefore
# stays between one and one-and-a-half folded views, never a line per run forever.
MAX_RECORD_LINES = MAX_RUNS + 12
# The log's name, and the JSON document it replaced. The old name is read once as the
# migration source and left in place as a backup.
RECORD_NAME = "record.jsonl"
LEGACY_RECORD_NAME = "state.json"
# Where a failing check's own output is kept, beside the record: `report.txt` in the
# same directory as `record.jsonl`. The file is rewritten by each failing run and names
# the run it belongs to in its header, because the record's older entries carry the
# same path and only that header says which run those bytes describe. A run with no
# failing check writes nothing, so a later green run does not erase the excerpt a
# reader is diagnosing.
EXCERPT_NAME = "report.txt"
# Append-only machine-local timeline: one JSON object per run, every check's own
# seconds, command and output size. Beside the record (in .git) and never committed,
# so the gate's cost is attributable after the fact without growing the tracked tree.
TIMINGS_NAME = "timings.jsonl"
# The shared pipeline-evidence record, one JSON line per check and one for the run,
# beside the record and the timings timeline. Same rule as the timings: instrumentation
# that could fail a landing is worse than none, so a write that fails says so once and
# leaves the verdict alone. docs/pi/pipeline-evidence.md is the format.
EVIDENCE_NAME = "evidence.jsonl"
# The content-fingerprint verdict cache for fork-quality steps (board item 398, the
# gate-latency design's D2/F-3). It lives beside the record, in the common Git
# directory: outside the scratch worktree and outside the candidate checkout, so a
# candidate's own git identity cannot name it. The key is the materialized tree, the
# exact quality command with its install mode, and the runtime; a green verdict is
# reused only for that key, and a hit is reported as cached rather than as a fresh pass.
VERDICTS_NAME = "verdicts.json"
VERDICT_SCHEMA = 1
# How many green verdicts the store keeps. Eviction is a space policy only: a verdict
# that is evicted is a cache miss, never a wrong answer.
MAX_VERDICTS = 200
# The record format's version. A consumer that meets a version it does not know refuses
# instead of guessing, so this number is the contract's own identity: raise it only in
# the change that also adds a schema file for the new shape.
EVIDENCE_VERSION = 1
# How many of a failing command's last lines the excerpt keeps. The terminal report
# prints all of them for the human watching the run; the file is for the reader who
# was not there, and a check's finding is at the end of its output -- a compiler's
# last error, a test runner's failure summary -- so the tail is the part worth
# keeping. The header says how many earlier lines were dropped, so a truncated
# excerpt never reads as the whole transcript.
EXCERPT_LINES = 60
# Where the triage packets live, beside the record: one JSON document, rewritten by each
# failing run, naming the run its packets belong to. `state.json` keeps a bounded copy of
# the newest packet per failing command -- a record a later reader can consult -- while
# this file keeps the whole packet of the run it describes, including every case.
TRIAGE_NAME = "triage.json"
# How many cases one packet keeps. A suite that fails everywhere can name hundreds; the
# first few are what a debugger starts from, and the packet says how many it dropped so a
# truncated list never reads as the whole failure.
TRIAGE_CASES = 10
# How many commits per path the packet lists. A path with a long history would bury the
# assertion it is meant to explain; the newest commits are the ones a regression is in.
TRIAGE_COMMITS = 3
# How many times the repair path may run one failing case to settle determinism. This is
# the debugger's budget, not the gate's: the gate runs a failed check exactly once, and the
# repetition exists so a determinism question is answered on ONE file rather than by
# re-running a suite.
TRIAGE_RUNS = 3
LS_REMOTE_TIMEOUT = 60
SERIES_TIMEOUT = 1800
FORKS_TIMEOUT = 600
BUILD_TIMEOUT = 3600
# The candidate preflight (`bin/pi-forks candidate --check`) answers in seconds: it reads
# git state and a lockfile stamp and runs nothing. The bound is generous so a slow disk
# cannot turn a refusal into a timeout, and a timeout is still a refusal rather than a red
# about the change. Board item 194: a 216-second gate run reported two failing pi-subagents
# cases the series did not have, because the candidate tree the gate read was not the
# series' tree.
CANDIDATE_CHECK_TIMEOUT = 120
# The word the driver prints when its candidate is not the series' tree, and the exit
# status it uses for that refusal (the gate's own refusal code).
STALE_CANDIDATE = "STALE:"
STALE_CANDIDATE_RC = 2


def candidate_stale(check: "Check") -> bool:
    """Whether this check refused because its candidate is not the series' tree.

    A stale candidate is not a test failure: the step never ran its command, so there is
    no case to triage and no red to blame on the change. The phrase is the driver's own
    (`check_candidate`), and the exit status is the gate's refusal code, so a test
    failure that happened to print the word cannot be mistaken for one.
    """
    return (check.kind == "fork-quality" and check.rc == STALE_CANDIDATE_RC
            and STALE_CANDIDATE in check.output)


# The forks whose own source this gate verifies, and the package scripts it runs for each
# in the clean candidate checkout (`--deps` installs the dependencies first). A build alone
# proves nothing about behavior, and for a source-only fork -- pi-subagents' entry point is
# TypeScript loaded through jiti, so it declares no `build` script -- nothing else compiles
# it at all. `typecheck` is that fork's build: `tsc --noEmit` is the check a transpiler
# cannot make, and `test:unit` is the suite it ships. pi-session-search needs its own build
# before its tests, which is why its first script is `check`. Item 190: pi-subagents had no
# step here, so a broken build or a failing unit case in it reached the owner and never the
# gate.
FORK_QUALITY = {
    "pi-session-search": ("check", "test"),
    "pi-subagents": ("typecheck", "test:unit"),
}
# The forks whose steps must install the checkout's dependencies themselves, because their
# build step does not: `candidate --build` skips a fork with no `build` script, so a
# source-only fork arrives at its typecheck with no node_modules at all. The others declare
# a build, whose install uses that pack's own npm flags (`--legacy-peer-deps` for
# pi-session-search), so a second install here would only risk disagreeing with it.
FORK_QUALITY_INSTALLS = {"pi-subagents"}
# What each quality script writes in the PREPARED checkout, as paths relative to it. The
# prepare step is the only writer of the checkout itself; a script that writes nothing is a
# reader and runs beside its sibling. These are measured, not assumed: the run that
# produced them is recorded in docs/pi/fork-quality-write-sets.md, and a script whose
# writes are not disjoint from its sibling's must not be added here (board item 405).
FORK_QUALITY_WRITES = {
    # `npm run test` runs `pretest` -> `npm run build`, which rewrites the esbuild bundle.
    # `npm run check` is `tsc --noEmit` and writes nothing, so the two are disjoint.
    ("pi-session-search", "test"): ("dist",),
    # pi-subagents' `typecheck` is `tsc --noEmit` and its `test:unit` writes only under the
    # isolated temp root its loader creates outside the checkout, so both write nothing.
}
# A quarantine is one named case -- or one whole subject, a file or a directory -- that
# stops blocking a gate while staying in place, named and counted. docs/design/agent-runtime.md section 27 rules on it:
# a quarantine is never a verdict that the case passes (D6), it carries the pile item that
# repairs it in the same act (D5), it carries an expiry and the case blocks again when the
# expiry passes (D7), and the repository holds a hard budget of them (D8). This is the
# in-tree form of the declaration, where the gate itself is what enforces it: the store's
# form is the board's `flake_overrides` row, which carries the same policy in a foreign key.
#
# The classifications a quarantine can carry. `unanswerable` is the environmental row of
# agent-runtime section 27.2: the check cannot produce a meaningful verdict in this
# environment, so its named cases are counted as "could not answer here" rather than as
# passed or failed. The report counts it as a sub-count of quarantined. They are declared
# before the class so a declaration that suppresses a SKIP rather than a failing case can
# leave the classification at its default: a skip has no failing case to classify.
QUARANTINE_UNANSWERABLE = "unanswerable"
QUARANTINE_FLAKY = "flaky"
QUARANTINE_UPSTREAM = "upstream"
QUARANTINE_UNKNOWN = "unknown"


class Quarantine:
    """One in-tree quarantine declaration, with everything D5, D7 and D8 require of it.

    `check` is the label prefix of the gate step it applies to, and `path` the test file
    whose cases it names -- the narrowest granularity this mechanism can express, because
    a whole check is quarantined only when the harness cannot run at all. `cases` is a set
    of case names, or None for the whole subject: a name list is right when the failing
    cases are stable, and the whole-subject form is right when the case that loses a race
    rotates from run to run, because a name list describes the runs that wrote it and
    refuses the next one. A declaration that suppresses a SKIP rather than a failing case
    names a directory as its subject and leaves `classification` at its default.

    `item` is the pile item's identity. It is an external reference rather than a numeric
    store id on purpose: the board store is per workspace and disposable, so a committed
    number would name a different item after a store rebuild. `expires` is an ISO date, and
    the gate refuses the declaration -- leaving the case red -- once it has passed.
    """

    def __init__(self, check: str, path: str, cases: set[str] | None,
                 item: str, expires: str, reason: str,
                 classification: str = QUARANTINE_UNKNOWN):
        self.check = check
        self.path = path
        self.cases = cases
        self.classification = classification
        self.item = item
        self.expires = datetime.strptime(expires, "%Y-%m-%d").date()
        self.reason = reason

    def lapse(self, today: date | None = None) -> str | None:
        """Why this declaration no longer suppresses its case, or None while it holds."""
        today = today or datetime.now(timezone.utc).date()
        if not self.item.strip():
            return (f"the quarantine declaration for {self.path} carries no pile item, and no ticket "
                    f"means no quarantine (agent-runtime section 27.5 D5): the case blocks again")
        if self.expires < today:
            return (f"the quarantine declaration for {self.path} expired on {self.expires.isoformat()} "
                    f"(item {self.item}); the case blocks again until it is restored or renewed with a "
                    f"fresh triage run (section 27.6 D7)")
        return None

    def where(self) -> str:
        """The quarantined subject, in the report's own words.

        `cases` None is the whole-subject form, and the subject is a file for a failing
        case and a directory for a declared skip, so the words name neither: "every case"
        is true of both, and "the whole file" was wrong for the directory.
        """
        if self.cases is None:
            return f"{self.path} (every case)"
        return f"{self.path} -- {', '.join(sorted(self.cases))}"

# D8's hard budget, in the gate's own mechanism: 10 active quarantined cases
# repository-wide, 3 per check. Past it the declaration does not suppress anything and the
# case blocks, which is the same refusal the board's write path gives a new quarantine.
QUARANTINE_BUDGET_REPOSITORY = 10
QUARANTINE_BUDGET_PER_CHECK = 3

# A fork-quality step whose suite carries cases this machine cannot answer, as quarantine
# declarations. These are a QUARANTINE, not a verdict that the cases pass: pi-subagents'
# herdr bridge cases write a configure frame and assert the acknowledgement within 30 ms,
# while the acknowledgement runs `discoverAgents` synchronously over the checkout. The same
# two cases pass alone and fail under the suite's own concurrency, and which one loses the
# 30 ms race changes from run to run (2026-10-27: the gate at 2439a5cd7 failed the
# APPEND_SYSTEM case, an idle `npm run test:unit` in the same candidate failed neither, and a
# run beside one other suite failed the contact_supervisor case instead). The file is
# byte-identical at v0.83.0, v0.83.1 and the series tip, and both cases were confirmed on a
# zero-patch v0.83.0 checkout (2026-10-29): 46/46 alone, the contact_supervisor case inside
# the full suite, and both cases under 24 concurrent copies of the file.
# `docs/pi/pi-subagents-quarantine-evidence.md` holds the commands and transcripts, and the
# declaration's reason cites it.
#
# The skip is taken only when EVERY failing case in the run is quarantined for that step:
# any other failure -- and any run whose `ℹ fail` count disagrees with its list -- keeps the
# step a red verdict, so one quarantined case cannot hide a regression beside it.
UNANSWERABLE_FORK_QUALITY = (
    Quarantine(
        check="fork pi-subagents: npm run test:unit",
        path="test/unit/herdr-native-placement.test.ts",
        # The two cases that carry the 30 ms acknowledgement wait -- and no others. These
        # are the only two fixed waits around the configure reply in this file (2026-10-29,
        # `git grep -n 'setTimeout(resolve, 30)'`), so a third case failing here is a
        # verdict rather than another turn of a rotation. A case in this file that is not
        # named below blocks the step, as does any failure in another file.
        cases=frozenset({
            "leaves the remote operator's APPEND_SYSTEM.md out of the child's prompt",
            "preserves canonical contact_supervisor progress and structured-interview semantics",
        }),
        classification=QUARANTINE_UNANSWERABLE,
        # The pile item's external ref. It is not a numeric store id because the store is
        # per workspace and disposable: a committed number would name a different item
        # after a rebuild. `board item show quarantine/herdr-native-placement` is the read
        # that answers whether it is still open; the gate enforces presence and expiry,
        # which are the two halves it can answer from a checkout with no store.
        item="quarantine/herdr-native-placement",
        # 90 days from the declaration: blocked on the named upstream change (the 30 ms
        # budget in the upstream suite), which is the one term D7 allows that long.
        expires="2027-01-26",
        reason=(
            "QUARANTINED, not answered: its 30 ms budget for the bridge acknowledgement is a "
            "real-time assertion this machine meets on an idle run and misses under the suite's "
            "own concurrency, and which of the two cases loses the race changes from run to run "
            "(the gate at 2439a5cd7 failed the APPEND_SYSTEM case, an idle run of the same "
            "candidate failed neither, and a run beside one other suite failed the "
            "contact_supervisor case instead). Confirmed as upstream's on 2026-10-29 against a "
            "zero-patch v0.83.0 checkout: the file passes 46/46 alone, the full suite fails the "
            "contact_supervisor case, and 24 concurrent copies of the file fail both cases (42 "
            "case failures across 72 runs), while the test file's blob is identical at v0.83.0, "
            "v0.83.1 and the series tip, so no series patch touches it. The runs, commands and failure "
            "transcript are recorded in docs/pi/pi-subagents-quarantine-evidence.md, and this "
            "reason cites that record as the confirmation the pile item asks for. Until the "
            "upstream wait polls instead of sleeping, this step cannot answer for the series "
            "here -- it is not a claim that the cases pass. Restored when five consecutive "
            "full-suite runs at the series tip are green. Tracked on the pile item above, and "
            "expiring per the quarantine ruling in docs/design/agent-runtime.md: an expired "
            "quarantine blocks the step again."
        ),
    ),
)
DOCUMENTS_TIMEOUT = 300
STORYBOOK_TIMEOUT = 900
# The tree-wide warning threshold; change-only refusals live in tools/gate/checks.
TS_SIZE_WARN_LINES = 2000
META_MUSE_TIMEOUT = 600
YOLO_CONFIG_TIMEOUT = 600

# tests/waybar-ai-modules.test.sh exits 77 when every case it could answer passed
# and the case that reads the *installed* `ai-usagebar`'s vendor list could not run
# (77 is the automake convention for "cannot answer here"). The gate names that as a
# skip, with the reason from the script's own `skip` line; 124 (timeout) and 127
# (missing command) are the other sentinels this file reads.
WAYBAR_SKIP_RC = 77

# The sentence `yolo pack series check` prints when the pack's patch-branch record is
# not on this machine. The record is written on the host and never reaches a jail, so
# this means "cannot answer here", not "the series is broken": the series is DECLARED
# elsewhere, in one report line, and the pack's other series still decide the check. A
# record that IS here and disagrees prints a conflict instead, and that stays a hard
# failure.
SERIES_UNRESOLVED = "the patch branch is not yet resolved on this machine"

# A series check runs per PACK, but one pack may carry several series, and the pack's
# transcript reports each of them. So the unresolved sentence for one series says
# nothing about the others: a broken series prints its own verdict line instead. Only
# when EVERY remaining line is one of the benign shapes below does the pack's verdict
# stand beside the declarations -- fail closed, because output nobody recognises is not
# evidence that nothing broke.
SERIES_BENIGN_HEADER = re.compile(r"^extension \S+: \d+ patch(?:es)? in ")

WAYBAR_WATCHES = (
    "config/waybar/", "config/ai-usagebar/config.toml",
    "host-laptop/config/ai-usagebar/config.toml",
    "tests/waybar-ai-modules.test.sh", "tests/test_waybar_cpu.py",
)

# Directories that never hold a document this gate checks: a fork checkout, a
# dependency tree, and this repo's durable scratch.
DOCUMENT_EXCLUDED = frozenset({".forks", "node_modules", ".yolo"})

HEX = re.compile(r"^[0-9a-fA-F]{40}$|^[0-9a-fA-F]{64}$")
SHA256 = re.compile(r"^[0-9a-f]{64}$")

# The gate steps the INSTALLED node version decides: every step this gate runs through
# `node --test` directly, or through a bash runner that needs `node` on PATH. A node
# upgrade can change any of their verdicts with no path in this repository moving -- the
# same blindness class as the yolo upgrade that left `bedrock-context.ts` inert. The
# build is ADVISORY (see `node_moved`): a different one is reported and plans nothing, so
# these steps are planned by the paths they watch only (item 114's rule, applied to node
# by item 126). The `fork-quality` npm steps are the one exception: they answer through
# `npm`, so they stay under npm's covering command in the report rather than being planned
# here. The report names the decided steps from this tuple.
NODE_DEPENDENT = ("storybook", "steering-timer", "rank-order",
                  "session-liveness", "meta-muse", "matt-render",
                  "seat-profiles", "observatory-web")

# The gate steps the INSTALLED yolo build decides: each runs yolo itself (the series
# check applies the series; `tests/yolo-config.test.sh` shells `yolo check`) or drives
# the render yolo composes (the meta-muse and matt-pack tests). A yolo upgrade can
# change any of their verdicts with no path in this repository moving -- which is how
# `bedrock-context.ts` stayed inert from 2026-10-16 to 2026-10-18 while a green
# headline said nothing about it. The build is ADVISORY (see `yolo_moved`): a different
# one is reported and plans nothing, so these steps are planned by the paths they watch
# only. The report names them from this tuple, in the advisory's own line.
YOLO_DEPENDENT = ("series", "meta-muse", "yolo-config", "matt-render", "seat-profiles")
# The sentence a host-written verdict is named by in the report, and the command that
# answers an input this machine cannot read. One line per input, and the line carries
# both, because "not covered" without "here is how to cover it" is a dead end.
DECLARED_ELSEWHERE = "declared elsewhere"
HOST_YOLO_LABEL = "the host's own yolo build"
HOST_YOLO_COMMAND = "`yolo --version --full` on the host"
# The command a host-written patch-branch verdict is refreshed by: the one the series
# check's own message names, so the report and the tool cannot disagree about the remedy.
SERIES_STAMP_COMMAND = "`yolo pack update` on the host"
# A series check's per-series declaration line, as `yolo pack series check` prints it:
# `yolo pack series check: extension matt/pi-manage-todo-list: the patch branch is not
# yet resolved on this machine -- ...`. The series name is what the report names; a
# line of the same meaning without one is still a declaration, just unnamed.
SERIES_STAMP_LINE = re.compile(r"^yolo pack series check: extension (?P<series>\S+): ")

# `--version --full` is the only line that names a build: the plain version line is
# `YOLO_VERSION`-overridable, so two builds with different rules have printed one
# string (docs/pi/yolo-source-provenance.md, "a sha256 is identity, a version string is
# not"). The binary sha256 is the fingerprint; the stamp and commit name the build in
# the report.
YOLO_IDENTITY_COMMAND = ("yolo", "--version", "--full")
YOLO_IDENTITY_TIMEOUT = 60
# `node --version` prints one line (`v24.12.0`): unlike yolo's plain version line no
# environment override rewrites it, so the line is the binary answering. Anything else
# -- nothing on PATH, a failure, an unparseable line -- is an input the plan cannot pin.
NODE_IDENTITY_COMMAND = ("node", "--version")
NODE_IDENTITY_TIMEOUT = 60
NODE_VERSION = re.compile(r"^v?\d+\.\d+\.\d+")
# `npm --version` is the other half of the runtime a fork-quality verdict rests on: the
# install a fixed tree produces depends on the npm that resolves it, so a different npm
# is a different key. Like node, an unreadable version is not a version with an empty
# name -- it disables the cache for the run rather than inventing a key.
NPM_IDENTITY_COMMAND = ("npm", "--version")
NPM_IDENTITY_TIMEOUT = 60
NPM_VERSION = re.compile(r"^\d+\.\d+\.\d+")
VERSION_TAG = re.compile(r"^v?(\d+)\.(\d+)\.(\d+)$")
SOURCE = re.compile(r"^git\+(?P<url>[^?]+)(?:\?ref=(?P<ref>[^&]+))?")

# The paths a check reads from the working tree. A dirty path under one of these is a
# refusal: the check would report a verdict on bytes that are not in the commit. A
# dirty path anywhere else -- a config file, a note, anything the checks do not read
# -- is named and ignored, so a tree with unrelated edits still gets a verdict.

# A change to one of these means the fork question may have a different answer: the
# series itself, or the pack that declares where it applies. `bin/pi-forks` is the
# driver and `bin/pi-forks.list` is the manifest, so either can change how every series
# is exported or materialized; both set `manifest_touched` below. The driver's own cases
# are NOT here: they live in `tests/pi-forks.test.sh`, a separable path, and a change
# confined to them plans that suite (PI_FORKS_INPUT) without moving a series. That is the
# structural boundary D5 narrows to; a driver change outside it still moves every series.
FORK_INPUT = (
    re.compile(r"^yolo-packs/[^/]+/patches/"),
    re.compile(r"^yolo-packs/[^/]+/pack\.json$"),
    re.compile(r"^bin/pi-forks$"),
    re.compile(r"^bin/pi-forks\.list$"),
)
# A change that can change what the roster storybook renders: the tool itself, or the
# pi-subagents series whose Fleet widget it imports. The tool's own regeneration is
# step 6 of the plan; the artifacts land outside the checkout.
STORYBOOK_INPUT = (
    re.compile(r"^tools/roster-storybook/"),
    re.compile(r"^yolo-packs/matt/patches/pi-subagents/"),
)
# A change that can change what the footer storybook renders: the tool, its own
# repository check, or the pi-archimedes series whose footer source it imports.
FOOTER_STORYBOOK_INPUT = (
    re.compile(r"^tools/footer-storybook/"),
    re.compile(r"^tests/footer-storybook\.test\.sh$"),
    re.compile(r"^yolo-packs/matt/patches/pi-archimedes/"),
)
# Each storybook step: input patterns that plan a check, the artifact directory it
# regenerates into, and the reason the report names. A step writes only under the
# durable artifact directory (or $TMPDIR), never the checkout, and its run.sh asserts
# the rendered badge, so a storybook that renders wrong is a failure.
STORYBOOKS = (
    ("the pi-subagents roster storybook", STORYBOOK_INPUT, "roster-storybook",
     "the change can change what the roster renders",
     "no roster or storybook tooling changed"),
    ("the pi-archimedes footer storybook", FOOTER_STORYBOOK_INPUT, "footer-storybook",
     "the change can change what the footer renders",
     "no footer or storybook tooling changed"),
)

# The drain observatory's front end (tools/tillr-view/web): Vite 7 + React 19 + TypeScript +
# TanStack Query, the other half of `tillr-view serve`. No other step in this gate compiles
# TypeScript, so a type error, or a component that invents a value the API never sent, lands
# green without this one. Its cost is why it is PATH-SCOPED: `npm ci` plus a Vite build is
# minutes, and landing-principles.md refuses a step whose cost rises on every unrelated
# commit. A commit that touches nothing under the web tree plans nothing here and pays 0 ms;
# a commit that touches it pays the typecheck and the honesty suite (`tsc --noEmit` and
# `vitest run`, design §7.3). The install is conditional for the same reason: a warm
# checkout pays neither, and the recipe `just observatory` runs installs by the same test.
OBSERVATORY_WEB_DIR = "tools/tillr-view/web/"
OBSERVATORY_WEB_WATCHES = (OBSERVATORY_WEB_DIR,)
OBSERVATORY_WEB_INPUT = (re.compile(r"^" + re.escape(OBSERVATORY_WEB_DIR)),)
OBSERVATORY_WEB_TIMEOUT = 900
OBSERVATORY_WEB_COMMAND = (
    "cd tools/tillr-view/web && "
    "{ [ -d node_modules ] || npm ci --no-audit --no-fund; } && "
    "npm run typecheck && npm run test"
)

# Both observatory recipes run tools/observatory/run.sh; the dev one supervises the API and
# UI together. The suite uses real just/hivemind with stub go/npm/node children, hermetic
# checkouts at fresh absolute paths, and loopback ports. Measured 2026-11-15: the original
# 15 cases in 0.9s wall; 2026-11-16, with both recipes' hermetic cases, 1.3-1.4s. A cheap
# landing check, not a rolling behavior suite. Exact inputs keep unrelated changes free and
# watch every source the suite reads.
OBSERVATORY_DEV_TEST = "tests/observatory-dev.test.py"
OBSERVATORY_DEV_WATCHES = (".justfile", "tools/observatory/run.sh", "tools/tillr-view/Procfile.dev",
                           "mise.toml", OBSERVATORY_DEV_TEST)
OBSERVATORY_DEV_INPUT = tuple(re.compile(r"^" + re.escape(path) + r"$")
                              for path in OBSERVATORY_DEV_WATCHES)
# A broken recipe exercises the suite's bounded shutdown/startup waits (up to about
# 410s when neither process starts). Leave room to report its assertions, not a timeout.
OBSERVATORY_DEV_TIMEOUT = 600

# The durable timer under the active-steering protocol: a steering record outside any session, a
# timer that injects the check-in at `next_due_at`, and `steer_checkin`, the only thing that
# advances the deadline. Its suite drives a fake clock through the real registration, so it needs
# `node` and nothing else -- no Go CLI, no Pi package root, no store.
STEERING_TIMER_WATCHES = (
    "yolo-packs/matt/files/extensions/steering-timer.ts",
    "yolo-packs/matt/tests/steering-timer.test.mjs",
)
STEERING_TIMER_INPUT = tuple(re.compile(r"^" + re.escape(path) + r"$")
                             for path in STEERING_TIMER_WATCHES)
STEERING_TIMER_TEST = "yolo-packs/matt/tests/steering-timer.test.mjs"
STEERING_TIMER_TIMEOUT = 120

# The shared pipeline-evidence format: one versioned record shape a board, a lander and the
# gate each write, and one consumer that reads whatever of them a repository has. The
# producers live in different languages and places, so the only thing that can catch a shape
# drifting is a suite that drives one and validates its output against the one schema file --
# which is what this step runs. It watches this file because the gate is the producer that
# still ships here; the retired merge queue's records remain history the format reads, and
# the suite's fixtures are what pin the shape now.
PIPELINE_EVIDENCE_WATCHES = (
    "tools/pipeline-evidence/",
    "tests/pipeline-evidence.test.py",
    "tools/fork-check.py",
)
PIPELINE_EVIDENCE_TEST = "tests/pipeline-evidence.test.py"
PIPELINE_EVIDENCE_TIMEOUT = 900

# The reload-residue audit: every provider registration, timer and listener a shipped extension
# creates is either cleaned on `session_shutdown`, bounded by its own owner, or an accepted
# exception with a reason (docs/reference/reload-residue.md). Nothing else executes that audit,
# and a new timer added without a teardown is invisible in a diff -- it works, and it keeps
# working after the session that armed it has gone. The registry is a fenced block in the
# document, so the document, the tool, the inventory it reads and the extension sources are all
# inputs, and so are the two manifests that decide which sources are staged at all.
RESIDUE_WATCHES = (
    "tools/extension-residue.py",
    "tests/extension-residue.test.py",
    "tools/extension-inventory.py",
    "docs/reference/reload-residue.md",
    "yolo-packs/matt/files/extensions/",
    "yolo-packs/matt-mods/mods/tok-stats/hooks/",
    "yolo-packs/matt/pack.json",
    "yolo-packs/matt-mods/pack.json",
)
RESIDUE_TEST = "tests/extension-residue.test.py"
RESIDUE_TIMEOUT = 300


def residue_scratch() -> str:
    """The fixture base the residue test owns for this run, outside any checkout.

    The test wipes and rebuilds that base, so it must be one no other run is using: two lanes
    sharing the durable directory would delete each other's fixtures mid-case. The process id
    makes the base this run's alone, and the step declares it so no other check writes it.
    """
    durable = os.environ.get("YOLO_DURABLE_DIR") or os.environ.get("TMPDIR") or "/tmp"
    return str(Path(durable) / "extension-residue" / f"run-{os.getpid()}")

# Where this run reads fork working copies: the checkout's own `.forks/`, or the one
# under the repository root `PI_FORKS_ROOT` names. `bin/pi-forks` reads that variable as
# the same thing -- a repository root, never a forks directory -- so one value serves
# both tools, and a managed worktree that never ran a setup points at the shared
# checkouts with `PI_FORKS_ROOT=/workspace just done`. A relative value is read from the
# current directory, which is the checkout when `just done` runs. It moves the checkouts
# only: the series a fork step reads come from the tree holding the driver, which is
# this one (`series_env()`).
# The marker files a container runtime leaves behind, which is how this process knows it
# is not on the host. Podman and Docker create one each; a bare host has neither.
JAIL_MARKERS = ("/run/.containerenv", "/.dockerenv")


def in_jail() -> bool:
    """Whether this run is inside a container jail, by the runtime's own marker.

    It decides one DECLARED input: the host's own yolo build, which a jail cannot read
    because the `yolo` on its PATH is the jail's own build. A host run reads that build
    itself -- it is the one `read_yolo_build` just recorded -- so it declares nothing
    there, and a declaration never claims more than the run can know.
    """
    return any(Path(marker).exists() for marker in JAIL_MARKERS)


def shared_checkout_root(root: Path) -> Path:
    """The checkout that owns the repository's untracked `.forks/` working copies.

    `.forks/` is gitignored and exists ONCE per repository, in the main checkout: a linked
    worktree -- a lane's, or the scratch worktree the landing gate judges a candidate in -- has
    none of its own. `git rev-parse --git-common-dir` answers `/repo/.git` in a linked worktree
    and `.git` in the main checkout, so its parent is that checkout either way. `PI_FORKS_ROOT`
    still wins, and a tree that is not a git checkout, or whose git directory is not a `.git`
    directory, keeps looking under itself.
    """
    try:
        common = git(root, "rev-parse", "--path-format=absolute", "--git-common-dir",
                     check=False).strip()
    except OSError:
        return root
    if Path(common).name != ".git":
        return root
    return Path(common).parent


def forks_root(root: Path) -> Path:
    override = os.environ.get("PI_FORKS_ROOT", "").strip()
    return Path(override) / ".forks" if override else shared_checkout_root(root) / ".forks"


def series_env(root: Path) -> dict[str, str]:
    """The environment a fork step needs to read THIS tree's series.

    `PI_FORKS_ROOT` says only where the untracked `.forks/` checkouts are. The series,
    `bin/pi-forks.list` and the pack declarations a fork step reads come from the tree
    holding the driver it runs -- this one -- so a lane worktree checks its own series
    against the shared checkouts. One value for both used to read the main checkout's
    series in a worktree, and a landing run then reported a correct 12-patch series as
    the export of the main checkout's 11-patch one (2026-10-24). Pinning the reading
    here keeps an inherited value from moving a gate run to another tree's series, and
    `bin/pi-forks` names the directory it read in every verdict about a series. Every
    fork step is handed it -- the build too, which reads no series -- so no two steps
    in one run can disagree about which tree they read.
    """
    return {"PI_FORKS_SERIES_ROOT": str(root)}


def candidate_root(root: Path) -> Path:
    """Where `bin/pi-forks candidate` builds the clean checkout the fork steps read.

    One per fork, reset from the series on every run: the path a reader needs in the
    report to tell a verdict about the candidate from one about a working tree somebody
    is editing. The driver derives it the same way (`$FORKS/candidate/<name>`), so it is
    shared by every run of this repository and two runs over the same `.forks/` at once
    would collide; the drain admitting one gate at a time is what keeps that from
    happening.
    """
    return forks_root(root) / "candidate"


def fork_setup(name: str) -> str:
    """The command that provides a missing `<name>` working copy, named in a refusal."""
    return f"bin/pi-forks setup --fork {name}"


# The rank-order suite pins what nothing else in the gate does: that a capacity-queued dispatch
# is ordered by the pile's rank. It imports the production pump and the ordering helper from the
# `.forks/pi-subagents` working copy and reads the queue's own rank alphabet it hands the
# queue, so three inputs can change its answer -- the suite, that alphabet, and the series
# `bin/pi-forks setup` builds the working copy from.
RANK_ORDER_TEST = "yolo-packs/matt/tests/pi-subagents-rank-order.test.mjs"
RANK_ORDER_INPUT = (
    re.compile(r"^yolo-packs/matt/tests/pi-subagents-rank-order\.test\.mjs$"),
    re.compile(r"^yolo-packs/matt/patches/pi-subagents/"),
)
# The repo-relative paths the suite reads from this checkout. The working copy it imports is
# untracked (`.forks/` is gitignored), so those bytes are not this tree's to judge; the series
# that builds it is under `yolo-packs/`, which the structural step already watches.
RANK_ORDER_WATCHES = (
    "yolo-packs/matt/tests/pi-subagents-rank-order.test.mjs",
)
RANK_ORDER_TIMEOUT = 600
# Without this checkout every case skips and `node --test` still exits 0, so the suite is only a
# verdict when the file it imports the ordering from is there. The path is relative to
# `forks_root()` above, and the step carries it in `needs`, so a checkout that is not
# there refuses the run instead of reading as a pass.
RANK_ORDER_CHECKOUT = Path("pi-subagents") / "src" / "runs" / "background" / "queue-rank.ts"
RANK_ORDER_SETUP = "bin/pi-forks setup --fork pi-subagents"


# The recorded approver replay is its own step: three cases that drive a live session's recorded
# toolCall, toolResult and notify shapes through the real receipt path to `review_items` with a
# canned evaluator. It reads the extension, the board module and its own recorded shapes -- not
# the manifest, the neighbours or the golden receipts -- so a change to those must not plan it.
# tests/session-forensics.test.py pins the hang-analysis CLI's own report -- judge passes,
# slow tool calls, telemetry gaps, a turn that never closed -- against a synthetic session
# log, and it landed with the tool while no step here named it, so nothing would notice it
# rot. It reads the CLI it exercises, so a change to the test or to anything under the tool
# plans it. The interpreter is `python3`, the one the justfile starts this gate with.
SESSION_FORENSICS_TEST = "tests/session-forensics.test.py"
SESSION_FORENSICS_WATCHES = (
    SESSION_FORENSICS_TEST,
    "tools/session-forensics/",
)
SESSION_FORENSICS_INPUT = (
    re.compile(r"^" + re.escape(SESSION_FORENSICS_TEST) + r"$"),
    re.compile(r"^tools/session-forensics/"),
)
SESSION_FORENSICS_TIMEOUT = 300

# tests/pi-inspector.test.py pins the other half of board item 192: the jail's environment
# carries no `--inspect` (tests/yolo-config.test.sh), and a real pi process still opens an
# attachable loopback inspector that the profiler finds by pid with no signal. It builds the
# profiler and starts the real `pi` entry point, so a change to the test, the extension, or
# the profiler plans it.
PI_INSPECTOR_TEST = "tests/pi-inspector.test.py"
PI_INSPECTOR_WATCHES = (
    PI_INSPECTOR_TEST,
    "yolo-packs/matt/files/extensions/pi-inspector.ts",
    "tools/pi-profiler/",
)
PI_INSPECTOR_INPUT = (
    re.compile(r"^" + re.escape(PI_INSPECTOR_TEST) + r"$"),
    re.compile(r"^yolo-packs/matt/files/extensions/pi-inspector\.ts$"),
    re.compile(r"^tools/pi-profiler/"),
)
PI_INSPECTOR_TIMEOUT = 600
# tests/session-budget.test.py pins the session-budget CLI's own report -- prompt, lag and
# telemetry against their stated bounds -- against a synthetic session log. It landed with
# the tool, so a change to the test or to anything under the tool plans it; nothing else
# does. The interpreter is `python3`, the one the justfile starts this gate with.
SESSION_BUDGET_TEST = "tests/session-budget.test.py"
SESSION_BUDGET_WATCHES = (
    SESSION_BUDGET_TEST,
    "tools/session-budget/",
)
SESSION_BUDGET_INPUT = (
    re.compile(r"^" + re.escape(SESSION_BUDGET_TEST) + r"$"),
    re.compile(r"^tools/session-budget/"),
)
SESSION_BUDGET_TIMEOUT = 300

# tests/tillr-muse-backend.test.py is the only assertion on the muse backend
# adapters: that the three stream dialects produce one report shape, that a footerless
# stream is refused, and that no backend's model id has leaked into the tillr tree. It
# landed with the adapters while no step here named it, so nothing would notice it rot.
# It reads the adapters and the policy they are configured by, so a change to any of the
# three plans it. The interpreter is `python3`, the one the justfile starts this gate with.
TILLR_BACKEND_TEST = "tests/tillr-muse-backend.test.py"
TILLR_BACKEND_WATCHES = (
    TILLR_BACKEND_TEST,
    "tools/tillr-run/backends/",
    "config/tillr/",
)
TILLR_BACKEND_INPUT = (
    re.compile(r"^" + re.escape(TILLR_BACKEND_TEST) + r"$"),
    re.compile(r"^tools/tillr-run/backends/"),
    re.compile(r"^config/tillr/"),
)
TILLR_BACKEND_TIMEOUT = 300

# tools/tillr-loop/test.py is the loop adapter's own suite: the three faces
# `tillr drain loop` requires, run against shell-stub stand-ins for the real programs.
# It asserts the adapter that exists, and it landed with the adapter while no step here
# named it -- so it rotted as the gate's argv, the lander's queue and the registry's
# path changed under it, and twelve cases described a front end nobody ran. It reads the
# adapter and the README that documents it, so a change to either plans it.
TILLR_LOOP_TEST = "tools/tillr-loop/test.py"
TILLR_LOOP_WATCHES = ("tools/tillr-loop/",)
TILLR_LOOP_INPUT = (re.compile(r"^tools/tillr-loop/"),)
TILLR_LOOP_TIMEOUT = 300

# tests/tillr-docs.test.py is the only assertion that a face a document names is a face the
# binary answers, and that no document grew a flag table. `--help` is the one home of a flag
# (docs/design/tillr-distribution.md Section 3), so a document that restates one is the page
# that disagrees with the binary a month later. It reads the eight module READMEs, the
# `tillr-operating` skill and the binaries it builds, so a change to any of them plans it.
# Its rolling-check guard also reads every tracked file, judging the tillr pack's delivered
# copies as their sources by the rule in tools/pack-docs.py, so a change to that delivered
# tree or to the rule plans it too.
TILLR_DOCS_TEST = "tests/tillr-docs.test.py"
TILLR_DOCS_SKILL = "yolo-packs/tillr/skills/tillr-operating/"
TILLR_DOCS_DELIVERED = "yolo-packs/tillr/files/local/share/tillr/"
TILLR_DOCS_RULE = "tools/pack-docs.py"
TILLR_DOCS_WATCHES = (
    TILLR_DOCS_TEST,
    TILLR_DOCS_RULE,
    TILLR_DOCS_DELIVERED,
    "tools/tillr/",
    "tools/tillr-cli/",
    "tools/tillr-drain/",
    "tools/tillr-gate/",
    "tools/tillr-item/",
    "tools/tillr-land/",
    "tools/tillr-run/",
    "tools/tillr-store/",
    "tools/tillr-view/",
    TILLR_DOCS_SKILL,
)
TILLR_DOCS_INPUT = (
    re.compile(r"^" + re.escape(TILLR_DOCS_TEST) + r"$"),
    re.compile(r"^tools/tillr(-[a-z]+)?/"),
    re.compile(r"^" + re.escape(TILLR_DOCS_SKILL)),
    re.compile(r"^" + re.escape(TILLR_DOCS_RULE) + r"$"),
    re.compile(r"^" + re.escape(TILLR_DOCS_DELIVERED)),
)
TILLR_DOCS_TIMEOUT = 600

# tools/session-liveness is the headless liveness harness: from a state a session can reach,
# does it return to idle within a bound, and does a later send-now start work? It judges the
# tree's pi series, and a verdict is a function of the tree rather than of ambient state: the
# plan builds a candidate checkout from that series and hands it as PI_FORK, which is where the
# run script's vitest binary and every fork import of the harness resolve (D38). No spec written
# as a literal `.forks/pi/...` path may creep back in -- a relative import resolves against the
# spec's own location, so it reads the shared working copy whatever PI_FORK says, and the
# verdict silently stops being about the tree.
#
# `.forks/` is gitignored, so it can never appear in a diff; only the harness and the pi-fork
# series are inputs the plan can see, and they are what decide that the steps run. The shared
# checkout is a construction SUBSTRATE: the candidate is cloned from it by ref, and its working
# tree is never read. A missing substrate refuses the run, naming the setup command, because a
# construction that cannot be made is an inability to answer and never a pass.
SESSION_LIVENESS_FORK = "pi"
SESSION_LIVENESS_RUN = "tools/session-liveness/run"
SESSION_LIVENESS_WATCHES = ("tools/session-liveness/", "yolo-packs/pi-fork/patches/")
SESSION_LIVENESS_INPUT = (
    re.compile(r"^tools/session-liveness/"),
    re.compile(r"^yolo-packs/pi-fork/patches/"),
)
SESSION_LIVENESS_TIMEOUT = 600

# A change that can change what the meta-muse surfaces render: the pack itself, the
# personal layer that selects it (the tests render the HOST notch, which is the only one
# that runs the derive), or the tests. The render tests live in this repository's tests/
# dir; the MSP bridge test lives in the pack's own tests/ dir, beside the extension it
# drives, because the pack now stages a deployable files tree.
META_MUSE_INPUT = (
    re.compile(r"^yolo-packs/meta-muse/"),
    re.compile(r"^tag-personal/config/yolo-jail/config\.d/personal\.toml$"),
    re.compile(r"^tests/meta-muse-settings\.test\.mjs$"),
    re.compile(r"^tests/meta-muse-wonder-pi-models\.test\.mjs$"),
)
# The tests, run together through the repository's own Node test runner. The MSP bridge
# test drives a real `muse serve` through a scripted model endpoint; it skips its live half
# when `muse` or `openssl` is absent, and its deterministic half always runs.
META_MUSE_TESTS = (
    "tests/meta-muse-settings.test.mjs",
    "tests/meta-muse-wonder-pi-models.test.mjs",
    "yolo-packs/meta-muse/tests/msp-bridge.test.mjs",
)

# The seat-profile encoding: the design document's approved set fillings and mechanical
# seat bounds, shipped as data by yolo-packs/seat-profiles. The test pins every set row and
# every bound, resolves each model id against the catalog row that ships it, reads the
# tabled sections of the design document itself (so a document row and its encoding cannot
# drift apart), and reads the two yolo layers that name a set -- so a change to the data,
# the pack, the test, the document, or either layer can change its verdict. It shells
# `yolo pack lint` too, which is why the step is listed under YOLO_DEPENDENT; a machine
# without yolo names that case a skip, not a pass.
SEAT_PROFILES_INPUT = (
    re.compile(r"^yolo-packs/seat-profiles/"),
    re.compile(r"^tests/seat-profiles\.test\.mjs$"),
    re.compile(r"^docs/design/seat-profiles\.md$"),
    re.compile(r"^config/yolo-jail/config\.toml$"),
    re.compile(r"^tag-personal/config/yolo-jail/config\.d/personal\.toml$"),
    re.compile(r"^tag-work/config/yolo-jail/config\.d/work\.toml$"),
)
SEAT_PROFILES_TESTS = ("tests/seat-profiles.test.mjs",)
SEAT_PROFILES_TIMEOUT = 600

# tests/yolo-config.test.sh is the repository's only assertion on the yolo config layers --
# the shared, personal and work files, and the Pi package list the matt pack contributes. The
# work class's `profile` selection lives in tag-work/ with nothing else pinning it, so this is
# the check that catches its loss. It reads real configs and shells out to `yolo check`, so a
# machine without `yolo` (or `python3`) cannot answer it.
YOLO_CONFIG_INPUT = (
    re.compile(r"^config/yolo-jail/"),
    re.compile(r"^tag-personal/config/yolo-jail/"),
    re.compile(r"^tag-work/config/yolo-jail/"),
    re.compile(r"^yolo-packs/matt/pack\.json$"),
    re.compile(r"^tests/yolo-config\.test\.sh$"),
)
YOLO_CONFIG_TEST = "tests/yolo-config.test.sh"
# The repo-relative paths that test reads. A dirty one of them means the check would judge
# the working tree rather than the commit, so it refuses.
YOLO_CONFIG_WATCHES = (
    "config/yolo-jail/",
    "tag-personal/config/yolo-jail/",
    "tag-work/config/yolo-jail/",
    "yolo-packs/matt/pack.json",
    "tests/yolo-config.test.sh",
)

# The matt pack's four render/overlay tests. The render pair (effort-thinking,
# bedrock-context) drive the HOST notch -- the only one that composes yolo's derives -- for
# the WORK class: a scratch HOME points ~/code/tillr at this checkout, includes
# `tag-work/config/yolo-jail/config.d/work.toml`, and selects the `bedrock` profile. Nothing else
# asserts on a work-class render, so this is the check that catches a lost `bedrock` pack
# selection -- the derive then writes no `defaultProvider`, no `defaultModel` and an empty
# derived table.
#
# The overlay pair pin the `subagents.agentOverrides` grants the render composes:
# research-tools the session-history and memory-read tool lists per role plus their child
# provider specs, agent-inherit-skills the `inheritSkills` flags. Each was written and then
# run by nothing -- a test no gate runs is the appearance of coverage, which is how a stale
# assertion survives for weeks. They share this check's fixture family (a host render for
# research-tools, and `node` for both) and its triggers (the manifest and `files/`), so they
# run in the same step rather than a second one over the same paths. Every one of them reads
# the pack manifest and the pack's `files/` (the overlays and the extensions/ sources); the
# render pair also read the work layer and the shared JSONC helper.
MATT_RENDER_INPUT = (
    re.compile(r"^yolo-packs/matt/tests/effort-thinking\.test\.mjs$"),
    re.compile(r"^yolo-packs/matt/tests/bedrock-context\.test\.mjs$"),
    re.compile(r"^yolo-packs/matt/tests/research-tools\.test\.mjs$"),
    re.compile(r"^yolo-packs/matt/tests/agent-inherit-skills\.test\.mjs$"),
    re.compile(r"^yolo-packs/matt/pack\.json$"),
    re.compile(r"^yolo-packs/matt/files/"),
    re.compile(r"^tag-work/config/yolo-jail/"),
)
MATT_RENDER_TESTS = ("yolo-packs/matt/tests/effort-thinking.test.mjs",
                     "yolo-packs/matt/tests/bedrock-context.test.mjs",
                     "yolo-packs/matt/tests/research-tools.test.mjs",
                     "yolo-packs/matt/tests/agent-inherit-skills.test.mjs")
# The repo-relative paths those tests read. A dirty one of them means the check would judge
# the working tree rather than the commit, so it refuses.
MATT_RENDER_WATCHES = (
    "yolo-packs/matt/pack.json",
    "yolo-packs/matt/files/",
    *MATT_RENDER_TESTS,
    "tag-work/config/yolo-jail/",
    "tests/helpers/json-comments.cjs",
)
MATT_RENDER_TIMEOUT = 600

# The active-steering skill is the protocol layer of a steered lane, and the one thing a
# reader must be able to trust in it is that its cadence comes from the steering record
# rather than from a number written into the prose. Nothing else in this gate reads a pack
# skill's text, so a re-hard-coded cadence, or a lost reference to the ack tool the timer
# advances on, would otherwise reach a launch unnoticed. The test reads the skill and
# nothing else, so it needs no toolchain beyond the interpreter running this gate.
ACTIVE_STEERING_INPUT = (
    re.compile(r"^yolo-packs/matt-agentwork/skills/active-steering/"),
    re.compile(r"^tests/active-steering-skill\.test\.py$"),
)
ACTIVE_STEERING_TEST = "tests/active-steering-skill.test.py"
# The repo-relative paths that test reads. A dirty one of them means the check would judge
# the working tree rather than the commit, so it refuses.
ACTIVE_STEERING_WATCHES = (
    "yolo-packs/matt-agentwork/skills/active-steering/",
    ACTIVE_STEERING_TEST,
)
ACTIVE_STEERING_TIMEOUT = 120

# The code that MATERIALIZES OR JUDGES a series. A change here can change what a fork
# check tests, so it forces every fork check. The materializer is the driver
# (`bin/pi-forks` and its list, which the plan handles as `manifest_touched` so the
# cause stays `driver`); the structural judge is `tests/pi-patch-series.test.sh`.
#
# Board item 412: this set is deliberately NOT "the gate's own code". The gate shells
# out to materialize and to judge -- `bin/pi-forks candidate`, `yolo pack series check`,
# `bin/pi-forks patch-branch verify/merge-check`, and the fork's own npm scripts -- so
# its own file selects and reports rather than judging. A change to that file is
# GATE_CODE, and it plans the self-suite and no fork check.
MECHANISM = (
    "bin/pi-forks",
    "bin/pi-forks.list",
    "tests/pi-patch-series.test.sh",
)

# The gate's own SELECTION AND REPORTING code: which checks are planned, how a verdict
# is worded, what a pass records. A change here cannot change how a series is
# materialized or judged, so it plans the self-suite and carries no fork (board item
# 412). The self-suite is the guard for this class: it is the gate's own audit, and it
# is `exclusive` because it spawns real gates.
GATE_CODE = (
    "tools/fork-check.py",
    "tests/fork-check.test.py",
    ".justfile",
)


# The gate's own audit suite. `tests/fork-check.test.py` is the only thing that
# exercises the routing above, and nothing else in this repository runs it -- so
# without this step a change to `tools/fork-check.py` is verified by the gate it just
# changed. Its inputs are GATE_CODE -- the checker, the suite that pins it, and the
# justfile recipe that starts it -- plus the preflight declarations. The suite is a
# script, not a pytest module, and `just done` runs python steps with the interpreter
# running the gate. A failing case exits non-zero, which is a check that reached no
# verdict -- NOT VERIFIED -- and never a skip.
# Where a check declares a condition it cannot pass (`--preflight`, PREFLIGHT_CELLS). The
# file is the escape hatch the matrix needs to stay honest: a check whose assertions are
# about real time, or that needs the developer's real home to resolve a package, cannot
# pass a hostile cell, and a red that is a known and accepted limitation is one people
# learn to ignore. A declaration names the check, the cell, WHY it cannot pass there, and
# the REPAIR it waits on -- so a declaration is a recorded debt with a next step, never a
# silencer -- and every preflight report counts the declarations beside the failures.
PREFLIGHT_DECLARATIONS = "tools/preflight-declarations.json"

SELF_SUITE = (
    *GATE_CODE,
    # The identity guard is an integrity step planned every run, and this suite is where
    # its scope rule is pinned: a lane's `git config` write must not change the identity
    # the trunk gate reads (board item 414). A change to the guard therefore plans the
    # suite that proves the rule, not only the guard itself.
    # The preflight's declarations are read by this checker and their shape is pinned by
    # this suite, so an edit to them is judged here rather than by the matrix they steer.
    PREFLIGHT_DECLARATIONS,
)
SELF_SUITE_TEST = "tests/fork-check.test.py"
SELF_SUITE_TIMEOUT = 600

# tests/pre-push-guard.test.sh pins hooks/pre-push and bin/patch-branches-publish: the
# guard that refuses a manifest push naming a patch branch the remote lacks, after pushing
# that branch itself, and the publish step's rewrite rule -- a rewrite publishes itself
# while the remote still holds the value this clone published, and is refused, naming the
# remote commit, when the remote moved away from it. The suite builds its own bare origin,
# work repository, second clone and diverged branch, so it touches no real remote and no
# checkout state; a change to the hook, the publisher or the suite is when it matters, and
# every one of them is watched so a dirty one refuses the run.
PRE_PUSH_TEST = "tests/pre-push-guard.test.sh"
PRE_PUSH_WATCHES = ("hooks/pre-push", "bin/patch-branches-publish", PRE_PUSH_TEST)
PRE_PUSH_INPUT = tuple(re.compile(r"^" + re.escape(path) + r"$") for path in PRE_PUSH_WATCHES)
PRE_PUSH_TIMEOUT = 300

# tests/pi-forks.test.sh pins bin/pi-forks -- the driver every patch-series step above
# execs (the merge-check, the verify, the replay and the build). No step ran the cases
# that pin it, so a driver regression reached a launch, or a human running it by hand,
# before the gate saw it; the suite itself then sat broken on a clean tree, aborting
# under `set -e` after 80 passing cases because the linked-worktree refusal fired on a
# lane worktree (2026-10-24). It builds its own upstreams, series, packs and manifest in
# a scratch directory and drives the driver as a command, so it reaches no network and
# needs no `.forks/` working copy: it is the one step here that judges the driver itself
# rather than a series.
#
# A SERIES CHANGE PLANS IT TOO. The driver parses what a series directory holds, so a
# series edit is the input that can expose a parsing regression; the step is planned by
# the same FORK_INPUT patterns the fork steps use -- a series directory, a pack's
# declaration, `bin/pi-forks` and `bin/pi-forks.list` -- plus the suite. The suite is
# deliberately NOT in FORK_INPUT: it is the driver's own test path, so a change confined
# to it plans these cases and moves no series (D5, OQ-5). The WATCHES are narrower on
# purpose: the suite reads this tree's driver and its real manifest, and no `yolo-packs/`
# file (its series are fixtures), so a dirty pack path must not refuse a run this check
# would judge anyway. The series steps watch those paths themselves.
#
# The suite is a RUNNER plus one scenario file per case group under `tests/pi-forks/`.
# The runner discovers the scenarios and runs them across parallel workers, each in its
# own process and its own temp root, so a case in one scenario cannot see another's
# state. Both the runner and the directory are inputs and watches: a scenario edit is
# as much a change to what this check judges as an edit to the runner.
PI_FORKS_TEST = "tests/pi-forks.test.sh"
PI_FORKS_CASES = "tests/pi-forks/"
PI_FORKS_INPUT = FORK_INPUT + (
    re.compile(r"^" + re.escape(PI_FORKS_TEST) + r"$"),
    re.compile(r"^" + re.escape(PI_FORKS_CASES)),
)
PI_FORKS_WATCHES = ("bin/pi-forks", "bin/pi-forks.list", PI_FORKS_TEST, PI_FORKS_CASES)
# 332 cases in 26 scenarios, each building a git fixture in a scratch directory. Sharded
# across the CPU count it measured 6.8-22.8s (median 9.9s) on a shared 12-core box whose
# load moved between 7.7 and 39.3; the pre-sharding single-process suite on the same tree
# measured 27.4-34.0s (median 30.5s), and the same runner at one worker 36.2-101.8s
# (median 40.1s). Generous room for a slow filesystem, without hiding a real hang.
PI_FORKS_TIMEOUT = 600

# tests/hooks-install.test.sh pins bin/hooks-install, the program `just hooks-install` runs.
# It is the only assertion on the installer: a marked copy replaced with drift reported on
# every run, an unmarked hook left alone and chained, and one report line per hook. Every
# input is watched (the installer, the hooks it installs, the recipe that calls it, the
# suite), so a dirty one refuses the run rather than judging the working tree. The suite
# builds a throwaway repository and passes it as --root, so it writes no shared hooks
# directory; measured at 0.5s alone.
HOOKS_INSTALL_TEST = "tests/hooks-install.test.sh"
HOOKS_INSTALL_WATCHES = (".justfile", "bin/hooks-install", "hooks/", HOOKS_INSTALL_TEST)
HOOKS_INSTALL_TIMEOUT = 120


# The Go tools live under the rcm-excluded `tools/` and are run from this checkout, so
# nothing else in this repository compiles them: a launch never builds one, and a broken
# or failing build would otherwise rot unnoticed until someone ran it by hand. Each
# directory is its own module, so each gets its own build and unit test -- a change
# under one tool is judged by that tool's build, not a sibling's. The build is
# `CGO_ENABLED=0` because a static binary is the tool's portability claim. The Go build
# cache is left at its shared default (`~/.cache/go-build`); this check must not
# allocate a per-worktree cache -- see `yolo-packs/matt/briefing/shared-go-cache.md`.
#
# Builds and test compilation belong to the parallel go-build tree declaration.
# This program retains one behavior suite per changed Go tool directory.
GO_TOOL_DIRS = (
    "tools/pi-profiler",
    "tools/tillr",
    "tools/tillr-store",
    "tools/tillr-item",
    "tools/tillr-run",
    "tools/tillr-gate",
    "tools/tillr-view",
    "tools/tillr-land",
    "tools/tillr-drain",
    "tools/tillr-cli",
)
GO_LANDING_SUITE_DIRS = ("tools/tillr",)
# Whole-suite wall seconds measured 2026-11-15, one module at a time with the shared
# build cache warm and test-result caching disabled. Reproduce each entry with:
#   cd <directory> && CGO_ENABLED=0 go test -count=1 -parallel 48 -timeout 3600s ./...
# These are module costs, not the deleted board monolith's build/integration price.
# Drain also measured 30.87/30.94/30.98s with three concurrent independent copies of
# the same command (three testing lanes, no source writers). Both the single-suite
# and concurrent readings exceed section 4.3 rule 3's 30s line, so drain stays rolling.
GO_TOOL_COST_ESTIMATE = {
    "tools/pi-profiler": 0.71,
    "tools/tillr": 0.94,
    "tools/tillr-store": 0.87,
    "tools/tillr-item": 1.76,
    "tools/tillr-run": 1.64,
    "tools/tillr-gate": 2.18,
    "tools/tillr-view": 5.36,
    "tools/tillr-land": 4.42,
    "tools/tillr-drain": 32.30,
    "tools/tillr-cli": 0.44,
}
# The suite's tests are I/O-bound subprocess cases, so the default GOMAXPROCS cap
# leaves the suite waiting: measured 2026-10-24 at 12 parallel 24s, 32 parallel 17s, and
# 48 parallel 13s for the same cases. 48 is the oversubscription that fits the budget.
GO_TOOL_PARALLEL = 48
# The check's own budget, and the `go test` timeout it passes down. Both are stated
# because Go's default (10m) is an accidental elapsed-time assertion: on a loaded box
# this package's suite takes 601s of wall clock and Go kills it with a stack dump, so
# the gate reports a timing failure as if the tests had failed. A timeout here is a
# liveness guard, not a verdict -- the verdict is the test results.
GO_TOOL_TIMEOUT = 3600
GO_TEST_TIMEOUT = "3600s"
# The build step's own liveness guard. A compile is minutes at worst, and this is a
# liveness guard rather than a verdict, like the suite's timeout above.




def go_tool_command(directory: str) -> str:
    """The whole behavior suite for one Go tool directory, run from this checkout.

    The build is the `go-build` step's job, so this command does not repeat it: a module
    that does not compile is already a red before this step is read.
    """
    return (f"cd {directory} && CGO_ENABLED=0 go test -parallel {GO_TOOL_PARALLEL} "
            f"-timeout {GO_TEST_TIMEOUT} ./...")


def now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def clock(seconds: float) -> str:
    return f"{seconds:.1f}s" if seconds < 60 else f"{int(seconds // 60)}m{int(seconds % 60)}s"


# How long a run may be silent before a watcher may read it as a hung tool. The report
# is printed once, at the end, so without a bound a healthy run of many minutes is
# indistinguishable from a wedged one: a harness that flags a silent tool call fires
# on healthy runs, and a signal that fires on healthy runs is one people learn to
# ignore. Every check's own timeout is minutes (a build's is an hour), so the bound
# cannot come from the checks finishing.
PROGRESS_INTERVAL = 30


def progress(text: str) -> None:
    """Say where the run is, while it is still running.

    Progress is stderr and the report is stdout, so a caller that pipes or captures
    the report (`tee`, `tail`) still sees the run move -- and buffering was never the
    reason a run looked silent, because nothing was written to stream in the first
    place. Flushed: a line left in a buffer is not observable.
    """
    print(text, file=sys.stderr, flush=True)


@contextlib.contextmanager
def heartbeat(what: str | Callable[[], str]):
    """Keep a phase observable however long it takes.

    A phase's result exists only when it returns, so waiting for it is the only thing
    a watcher can be shown meanwhile. The beat is the bounded interval: no phase is
    silent for its whole length.

    `what` is the phase's name, or a callable that returns it: the check phase runs
    several checks at once and which ones are running changes while it lasts, so a beat
    that repeated the name it started with would describe a phase that is no longer the
    one running.
    """
    started = time.monotonic()
    stopping = threading.Event()

    def beat() -> None:
        while not stopping.wait(PROGRESS_INTERVAL):
            progress(f"fork-check: ... {what() if callable(what) else what} still running "
                     f"({clock(time.monotonic() - started)} elapsed)")

    thread = threading.Thread(target=beat, name="fork-check-heartbeat", daemon=True)
    thread.start()
    try:
        yield
    finally:
        stopping.set()
        thread.join()


def git(root: Path, *args: str, check: bool = True) -> str:
    result = subprocess.run(
        ["git", "-C", str(root), *args], capture_output=True, text=True, errors="replace"
    )
    if check and result.returncode != 0:
        raise SystemExit(f"fork-check: git {' '.join(args)} failed:\n{result.stderr.strip()}")
    return result.stdout


# The caller's board settings a check must never inherit. `LAND_BATCH_LIMIT` is refused
# by the board by name (item 587): the drain hands its environment to every gate it runs,
# and the gate's tests derive K from the batch limit, so a check that inherited it would
# refuse rather than judge. The Go suites strip it themselves; this is the boundary that
# covers every other check, including the Python delivery tests that drive the board
# binary directly.
GATE_ENV_DROP = ("LAND_BATCH_LIMIT",)


def environ(env: dict[str, str] | None = None,
            drop: tuple[str, ...] = ()) -> dict[str, str]:
    """This process's environment with `env` added and `drop` removed.

    One function for both rules, so a caller that both adds and drops cannot spell the
    two differently: additions win over the inherited value, and a dropped name is gone
    whether or not an addition also named it (an addition for a dropped name is a
    contradiction, and the drop wins). `GATE_ENV_DROP` is always removed, so no check
    can inherit a setting the board refuses, and the reuse list is always removed, so a
    check that spawns another gate cannot hand its own reuse down.
    """
    merged = {**os.environ, **(env or {})}
    # A reuse list belongs to the gate invocation that was handed it, never to a check:
    # a check that spawns another gate (the self-suite does) must not hand its own reuse
    # down, or the inner gate would skip checks the outer one was told to skip.
    merged.pop(REUSE_ENV, None)
    for name in (*GATE_ENV_DROP, *drop):
        merged.pop(name, None)
    return merged


class Cancellation:
    """A run's stop signal, and the child processes it must terminate.

    The gate stops when a check it blames finishes red (board item 337): no check that
    has not started may start, and every check already running is terminated. A worker
    registers the process it spawned here, so `cancel` can kill that process GROUP -- a
    suite that forked its own children cannot leave them behind holding the pipe -- and a
    worker that has already finished unregisters it. Registering after `cancel` is race
    free: the new process is killed immediately.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._procs: set[subprocess.Popen] = set()
        self.cancelled = False

    def register(self, proc: subprocess.Popen) -> None:
        with self._lock:
            self._procs.add(proc)
            cancelled = self.cancelled
        if cancelled:
            _kill_process_group(proc)

    def unregister(self, proc: subprocess.Popen) -> None:
        with self._lock:
            self._procs.discard(proc)

    def cancel(self) -> None:
        with self._lock:
            self.cancelled = True
            procs = list(self._procs)
        for proc in procs:
            _kill_process_group(proc)


def _kill_process_group(proc: subprocess.Popen) -> None:
    """Kill `proc` and every child in the session it was started in, if it is alive."""
    if proc.poll() is not None:
        return
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError, AttributeError):
        try:
            proc.kill()
        except ProcessLookupError:
            pass


def _run_cancellable(cwd: Path, argv: list[str], timeout: int,
                     env: dict[str, str] | None, drop: tuple[str, ...],
                     cancel: Cancellation, split: bool) -> tuple[int, str, str, float]:
    """The Popen half of run_command/run_split: a child this run can terminate.

    The command starts in its own session so cancellation reaches its whole process
    group rather than only the shell the gate happened to exec. A cancel returns 130,
    which the caller records as a check that was stopped, never as a verdict.
    """
    started = time.monotonic()
    try:
        proc = subprocess.Popen(
            argv, cwd=str(cwd), stdout=subprocess.PIPE,
            stderr=subprocess.PIPE if split else subprocess.STDOUT,
            text=True, errors="replace", env=environ(env, drop),
            start_new_session=True,
        )
    except FileNotFoundError:
        return 127, "", f"{argv[0]}: not found on PATH", time.monotonic() - started
    cancel.register(proc)
    try:
        try:
            out, err = proc.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            _kill_process_group(proc)
            out, err = proc.communicate()
            return 124, out or "", err or f"{' '.join(argv)}: timed out after {timeout}s", \
                time.monotonic() - started
        if cancel.cancelled:
            return 130, out or "", err or "", time.monotonic() - started
        return proc.returncode, out or "", err or "", time.monotonic() - started
    finally:
        cancel.unregister(proc)


def run_command(cwd: Path, argv: list[str], timeout: int,
                env: dict[str, str] | None = None,
                drop: tuple[str, ...] = (),
                cancel: Cancellation | None = None) -> tuple[int, str, float]:
    """Run one command, optionally with `env` added to this process's environment.

    `env` is additions, never a replacement: a check is handed the one variable its
    suite cannot resolve for itself, and keeps the rest of the environment the gate
    was started in. `drop` names variables that must be ABSENT rather than overridden:
    the preflight's scrubbed cells need an unset `BOARD_DB` to mean unset, because a
    variable set to the empty string is a different question to a store resolver than
    one that is not there at all.
    """
    started = time.monotonic()
    if cancel is not None:
        rc, out, err, seconds = _run_cancellable(cwd, argv, timeout, env, drop, cancel, False)
        # One stream, so a child's stderr keeps its place in the transcript rather
        # than being appended after everything it wrote to stdout.
        return rc, out + err, seconds
    try:
        # One stream, so a child's stderr keeps its place in the transcript rather
        # than being appended after everything it wrote to stdout.
        result = subprocess.run(
            argv, cwd=str(cwd), stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            text=True, errors="replace", timeout=timeout,
            env=environ(env, drop),
        )
        return result.returncode, result.stdout or "", time.monotonic() - started
    except FileNotFoundError:
        return 127, f"{argv[0]}: not found on PATH", time.monotonic() - started
    except subprocess.TimeoutExpired:
        return 124, f"{' '.join(argv)}: timed out after {timeout}s", time.monotonic() - started


def run_split(cwd: Path, argv: list[str], timeout: int,
              env: dict[str, str] | None = None,
              drop: tuple[str, ...] = (),
              cancel: Cancellation | None = None) -> tuple[int, str, str, float]:
    """Like run_command, but keeps stdout and stderr apart.

    A document finding is the tool's own report on stdout. `uvx`'s own failure -- a
    first fetch with no network, say -- writes to stderr, exits non-zero too, and must
    not be read as a finding. The caller joins the two again for the transcript.
    """
    started = time.monotonic()
    if cancel is not None:
        return _run_cancellable(cwd, argv, timeout, env, drop, cancel, True)
    try:
        result = subprocess.run(
            argv, cwd=str(cwd), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, errors="replace", timeout=timeout,
            env=environ(env, drop),
        )
        return result.returncode, result.stdout or "", result.stderr or "", time.monotonic() - started
    except FileNotFoundError:
        return 127, "", f"{argv[0]}: not found on PATH", time.monotonic() - started
    except subprocess.TimeoutExpired:
        return 124, "", f"{' '.join(argv)}: timed out after {timeout}s", time.monotonic() - started


# ── reading a pack declaration, the way a launch does ───────────────────────────


def strip_json_comments(text: str) -> str:
    """Remove // and /* */ comments that are outside strings.

    A pack.json is read by a parser that tolerates comments (`tests/helpers/
    json-comments.cjs` is the same rule on the JavaScript side), so this reads one
    too rather than assume the strict JSON that all of them happen to be today.
    """
    out: list[str] = []
    i, n, in_string, escaped = 0, len(text), False, False
    while i < n:
        char = text[i]
        if in_string:
            out.append(char)
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == '"':
                in_string = False
            i += 1
        elif char == '"':
            in_string = True
            out.append(char)
            i += 1
        elif text.startswith("//", i):
            while i < n and text[i] != "\n":
                i += 1
        elif text.startswith("/*", i):
            end = text.find("*/", i + 2)
            i = n if end < 0 else end + 2
        else:
            out.append(char)
            i += 1
    return "".join(out)


class Series:
    """One patch series this repo deploys, as the pack that carries it declares it."""

    def __init__(self, pack: str, rel: str, source: str, follow: str | None, branch: str = ""):
        self.pack = pack
        # rel is the committed series directory, empty for a converted fork: its patches
        # are the commits on `branch`, and nothing is staged under yolo-packs/.
        self.rel = rel
        self.branch = branch
        self.source = source
        self.follow = follow
        match = SOURCE.match(source or "")
        self.url: str | None = match.group("url") if match else None
        self.ref: str = (match.group("ref") if match else None) or "HEAD"

    @property
    def owner_key(self) -> str:
        return f"{self.pack}/{self.fork}"

    @property
    def fork(self) -> str:
        """The fork's name: the upstream repository's, which bin/pi-forks.list uses."""
        tail = (self.url or "").rstrip("/").rsplit("/", 1)[-1]
        return tail[:-4] if tail.endswith(".git") else tail

    @property
    def follows_branch(self) -> bool:
        return bool(self.url) and not HEX.match(self.ref)


def read_series(root: Path) -> list[Series]:
    found: list[Series] = []
    for manifest in sorted((root / "yolo-packs").glob("*/pack.json")):
        try:
            data = json.loads(strip_json_comments(manifest.read_text()))
        except (OSError, json.JSONDecodeError) as problem:
            raise SystemExit(f"fork-check: {manifest.relative_to(root)} is unreadable: {problem}")
        for contribution in data.get("contributes") or []:
            rel = contribution.get("patches")
            patch_branch = contribution.get("patch_branch")
            branch = ""
            if isinstance(patch_branch, str) and patch_branch:
                # `patch_branch` is yolo's own field name, and it names the ref in the
                # locator's `?ref=`. A converted fork commits NO series directory: its
                # patches are the branch's commits, so there is nothing to stage and
                # nothing to digest. A contribution that still carries a directory --
                # a fork part-way through converting -- keeps it, and its path is the
                # one docs/pi/patch-branches/design.md section 2 fixes:
                # <pack>/patches/<last segment of `into`>. A program contribution has no
                # `into` at all, which is why the path is optional rather than required.
                for pair in patch_branch.partition("?")[2].split("&"):
                    key, _, value = pair.partition("=")
                    if key == "ref":
                        branch = value
                if not rel:
                    into = contribution.get("into")
                    leaf = into.rstrip("/").rsplit("/", 1)[-1] if isinstance(into, str) else ""
                    candidate = f"patches/{leaf}" if leaf else ""
                    if candidate and (manifest.parent / candidate).is_dir():
                        rel = candidate
            if not rel and not branch:
                continue
            found.append(
                Series(
                    pack=manifest.parent.name,
                    rel=rel,
                    source=contribution.get("source", ""),
                    follow=contribution.get("follow"),
                    branch=branch,
                )
            )
    return found


# ── the identity of the installed yolo build ────────────────────────────────────


class YoloBuild:
    """The yolo binary answering on PATH: the input a render verdict rests on.

    `readable` is False when no identity could be read -- `yolo` is not on PATH, the
    command failed, or it printed no binary sha256 (a build older than `--full`). An
    unreadable identity is not a build with an empty name: it is an input the plan
    cannot pin, and the report says so with the command that would pin it.
    """

    def __init__(self, sha256: str = "", stamp: str = "", commit: str = "", why: str = ""):
        self.sha256 = sha256
        self.stamp = stamp
        self.commit = commit
        self.why = why

    @property
    def readable(self) -> bool:
        return bool(self.sha256)

    @property
    def fingerprint(self) -> str:
        return f"sha256:{self.sha256}"

    def describe(self) -> str:
        """The build in the report's words: its stamp (or commit) and a short sha256."""
        return f"{self.stamp or self.commit or 'an unnamed build'}, binary sha256 {self.sha256[:12]}"


def read_yolo_build(root: Path) -> YoloBuild:
    """Which yolo is answering, read from its own `--version --full`."""
    if shutil.which("yolo") is None:
        return YoloBuild(why="`yolo` is not on PATH")
    rc, out, _ = run_command(root, list(YOLO_IDENTITY_COMMAND), YOLO_IDENTITY_TIMEOUT)
    if rc != 0:
        first = out.strip().splitlines()
        detail = f": {first[0]}" if first else ""
        return YoloBuild(why=f"`yolo --version --full` exited {rc}{detail}")
    fields: dict[str, str] = {}
    for line in out.splitlines():
        key, _, value = line.partition(":")
        if value.strip():
            fields[key.strip().lower()] = value.strip()
    sha = fields.get("binary sha256", "")
    stamp = fields.get("build stamp", "")
    commit = fields.get("commit", "")
    if not SHA256.match(sha):
        return YoloBuild(stamp=stamp, commit=commit,
                         why="`yolo --version --full` printed no binary sha256")
    return YoloBuild(sha, stamp, commit)


def describe_recorded_yolo(entry: dict) -> str:
    """A recorded yolo identity, in the same words as `YoloBuild.describe`."""
    sha = (entry.get("fingerprint") or "").partition(":")[2]
    return f"{entry.get('build') or 'an unnamed build'}, binary sha256 {sha[:12]}"


def build_identity(entry: dict | None) -> str:
    """A recorded build's identity for comparison: its commit, else its sha256.

    The commit is what a reader must look at -- two builds of one commit differ only in
    when they were built -- so a rebuild at the same commit is not a change. A record
    from before this comparison names only a sha256, and a build that names no commit
    falls back to the sha256, so an older record still compares rather than reading as
    "nothing to compare".
    """
    if not entry:
        return ""
    return (entry.get("commit") or "").strip() or (entry.get("fingerprint") or "")


def yolo_moved(record: Record, yolo: YoloBuild | None) -> str | None:
    """Why the installed yolo build is news, or None. ADVISORY: it plans nothing.

    A step gated on a path in this repository cannot see a new yolo: no file here
    moves, so nothing is planned, and the 2026-10-16 to 2026-10-18 blindness is
    exactly that. Planning those steps from the build's identity was the answer,
    and it was wrong twice over: the record is shared through the common Git directory,
    so a run on another machine (a jail, whose yolo is its own build) planned them on
    every run for a build nothing here had changed; and a rebuild at the same commit
    forced the same replan. So a different build is REPORTED and plans nothing -- the
    steps run when a path they watch changes -- and the comparison is the build's
    COMMIT, so a same-commit rebuild never reads as a change at all (`build_identity`).

    A baseline that names no build is news once (this run records one), and with no
    baseline there is no earlier verification to differ from.
    """
    if yolo is None or not yolo.readable:
        return None
    previous = record.recorded_yolo
    if previous is None:
        if record.baseline is None:
            return None
        return ("no yolo build identity was recorded with the last verified run, so this "
                f"run records one ({yolo.describe()}); nothing was planned from it")
    if build_identity(previous) == build_identity({"commit": yolo.commit, "fingerprint": yolo.fingerprint}):
        return None
    return (f"the installed yolo build changed: {yolo.describe()} "
            f"(last verified run: {describe_recorded_yolo(previous)})")


class NodeVersion:
    """The node binary answering on PATH: the input a `node --test` verdict rests on.

    `readable` is False when no version could be read -- `node` is not on PATH, the
    command failed, or it printed no parseable version. An unreadable version is not a
    version with an empty name: it is an input the plan cannot pin, and the report says
    so with the command that would pin it.
    """

    def __init__(self, version: str = "", why: str = ""):
        self.version = version
        self.why = why

    @property
    def readable(self) -> bool:
        return bool(self.version)

    @property
    def fingerprint(self) -> str:
        return self.version

    def describe(self) -> str:
        """The version in the report's words, in the same spelling `node` prints."""
        return f"node {self.version}"


def read_node_version(root: Path) -> NodeVersion:
    """Which node is answering, read from its own `--version`."""
    if shutil.which("node") is None:
        return NodeVersion(why="`node` is not on PATH")
    rc, out, _ = run_command(root, list(NODE_IDENTITY_COMMAND), NODE_IDENTITY_TIMEOUT)
    if rc != 0:
        first = out.strip().splitlines()
        detail = f": {first[0]}" if first else ""
        return NodeVersion(why=f"`node --version` exited {rc}{detail}")
    version = out.strip().splitlines()[0].strip() if out.strip() else ""
    if not NODE_VERSION.match(version):
        return NodeVersion(why="`node --version` printed no parseable version")
    return NodeVersion(version)


class RuntimeFingerprint:
    """The node and npm answering on PATH: the runtime a fork-quality verdict rests on.

    A fixed tree with its lockfile produces the same test environment only under a fixed
    runtime, so the cache key carries both versions. `readable` is False when either
    cannot be read: an unreadable runtime is not a runtime with an empty name, and a key
    built from one would be a key that cannot be compared. The cache is disabled for the
    run rather than guessing.
    """

    def __init__(self, node: str = "", npm: str = "", why: str = ""):
        self.node = node
        self.npm = npm
        self.why = why

    @property
    def readable(self) -> bool:
        return bool(self.node and self.npm)

    @property
    def fingerprint(self) -> str:
        return f"node:{self.node} npm:{self.npm}"

    def entry(self) -> dict:
        """The runtime as the cache key and the provenance record it."""
        return {"node": self.node, "npm": self.npm}

    def describe(self) -> str:
        return f"node {self.node}, npm {self.npm}"


def read_runtime(root: Path, node: str = "") -> RuntimeFingerprint:
    """Which node and npm are answering, read from their own `--version`.

    `node` is the version the plan already read, when it has one: the runtime is the same
    machine a moment later, and asking twice would make one run's transcript name two
    reads of one input. npm is read here because nothing else needs it.
    """
    versions: dict[str, str] = {"node": node} if node else {}
    for name, command, pattern in (
        ("node", NODE_IDENTITY_COMMAND, NODE_VERSION),
        ("npm", NPM_IDENTITY_COMMAND, NPM_VERSION),
    ):
        if name in versions:
            continue
        if shutil.which(command[0]) is None:
            return RuntimeFingerprint(why=f"`{command[0]}` is not on PATH")
        rc, out, _ = run_command(root, list(command), NPM_IDENTITY_TIMEOUT)
        if rc != 0:
            first = out.strip().splitlines()
            detail = f": {first[0]}" if first else ""
            return RuntimeFingerprint(why=f"`{' '.join(command)}` exited {rc}{detail}")
        version = out.strip().splitlines()[0].strip() if out.strip() else ""
        if not pattern.match(version):
            return RuntimeFingerprint(why=f"`{' '.join(command)}` printed no parseable version")
        versions[name] = version
    return RuntimeFingerprint(node=versions["node"], npm=versions["npm"])


def describe_recorded_node(entry: dict) -> str:
    """A recorded node version, in the same words as `NodeVersion.describe`."""
    return f"node {entry.get('version') or entry.get('fingerprint') or 'an unnamed version'}"


def node_moved(record: Record, node: NodeVersion | None) -> str | None:
    """Why the installed node version is news, or None. ADVISORY: it plans nothing.

    The model of `yolo_moved`: a step gated on a path in this repository cannot see a new
    node, so the version read now is compared with the one recorded with the last verified
    run and REPORTED. It does not plan the steps it decides: those run when a path they
    watch changes, and a version that moved with no path here moving adds work this change
    cannot affect (item 114's rule, applied to node as item 126 requires). A baseline that
    names no version is news once -- this run records one, and plans nothing from it; with
    no baseline there is no earlier verification to differ from.
    """
    if node is None or not node.readable:
        return None
    previous = record.recorded_node
    if previous is None:
        if record.baseline is None:
            return None
        return ("no node version was recorded with the last verified run, so this run "
                f"records one ({node.describe()}); nothing was planned from it")
    if previous.get("fingerprint") == node.fingerprint:
        return None
    return (f"the installed node version changed: {node.describe()} "
            f"(last verified run: {describe_recorded_node(previous)})")


# ── the installed Pi a node suite drives ────────────────────────────────────────

# The variable pi-subagents sets in every session -- parent or child -- naming the
# package root the running Pi is loaded from. A suite that imports pi-tui reads it
# directly; other helpers read the same pair with their own fallbacks. The gate
# resolves it once and hands it to every node check that drives the installed Pi,
# because otherwise the verdict depends on whatever the shell that invoked the gate
# happened to export: a lander runs the gate as `just done` in its own environment,
# and a landing whose environment lacked the variable failed a node suite with
# `Cannot find package 'undefined'` (2026-10-21) on the first run that planned the
# step -- a missing environment reported as a verdict.
PI_PACKAGE_ENV = ("PI_SUBAGENTS_PI_CODING_AGENT_PACKAGE_ROOT", "PI_CODING_AGENT_PACKAGE_ROOT")
PI_PACKAGE_MISSING = (
    "no installed Pi package root resolves here, so the suite cannot import pi-tui: "
    f"{' nor '.join(PI_PACKAGE_ENV)} names one, and `npm root -g` holds no "
    "@earendil-works/pi-coding-agent -- a usable root has dist/index.js and a sibling "
    "../pi-tui/dist/index.js, which is what the suites import"
)
# Suites that import only the coding agent resolve it with a fallback chain that ends
# at `npm root -g`, so a global install that nests pi-tui under the agent is usable for
# them, and the skip this message names is only the genuinely absent package.
PI_PACKAGE_MISSING_AGENT_ONLY = (
    "no installed Pi package root resolves here, so the suite cannot import the "
    f"coding agent: {' nor '.join(PI_PACKAGE_ENV)} names one, and `npm root -g` holds no "
    "@earendil-works/pi-coding-agent -- a usable root has dist/index.js"
)


def pi_package_usable(candidate: Path, *, need_tui: bool = True) -> bool:
    """Can a node suite import through this package root?

    Every suite that drives the installed Pi imports the coding agent's `dist/index.js`.
    Only some also import pi-tui, and those import it as a SIBLING
    (`<root>/../pi-tui/dist/index.js`), and have no fallback. Suites that import only
    the coding agent resolve pi-tui themselves, with a fallback chain that ends at
    `npm root -g`. So `need_tui` is the caller's statement about its own suites, not a
    property of the install.

    `npm root -g` on a machine where only the coding agent was installed globally nests
    pi-tui under the agent (`<root>/node_modules/@earendil-works/pi-tui`), which is a
    normal npm layout and not a broken install. A suite that needs the sibling cannot use
    it; a suite that does not, can.
    """
    if not (candidate / "dist" / "index.js").is_file():
        return False
    if not need_tui:
        return True
    return (candidate.parent / "pi-tui" / "dist" / "index.js").is_file()


def pi_package_root(root: Path, *, need_tui: bool = True) -> Path | None:
    """The installed Pi's package root, or None when this machine has none to name.

    The session's own variables come first: they are what the running build says about
    itself, and the same value its suites would have read had the gate not intervened.
    `npm root -g` is the fallback because it is the installer answering where it put the
    package -- a path this file would otherwise have to hard-code, and a hard-coded home
    path is the defect this seam exists to remove, not the fix.

    A candidate counts only when `pi_package_usable` accepts it. A stale variable naming
    a removed build, or a global install with no sibling pi-tui where the caller's suites
    need one, is the same missing environment as an unset one.
    """
    for name in PI_PACKAGE_ENV:
        candidate = os.environ.get(name, "").strip()
        if candidate and pi_package_usable(Path(candidate), need_tui=need_tui):
            return Path(candidate)
    rc, out, _ = run_command(root, ["npm", "root", "-g"], NODE_IDENTITY_TIMEOUT)
    if rc == 0 and out.strip():
        installed = Path(out.strip().splitlines()[0].strip()) / "@earendil-works/pi-coding-agent"
        if pi_package_usable(installed, need_tui=need_tui):
            return installed
    return None


def pi_sdk_env(root: Path, *, need_tui: bool = True) -> dict[str, str]:
    """What a node check driving the installed Pi is handed, or {} when there is none.

    Empty means the plan cannot supply it: a check whose suite requires the variable names
    its own skip, and one whose suite has fallbacks of its own runs as it would have.
    """
    found = pi_package_root(root, need_tui=need_tui)
    return {PI_PACKAGE_ENV[0]: str(found)} if found else {}


# ── the fingerprint of what a launch would build ────────────────────────────────

# The bridge's own resolution of the upstream a launch would build, with the fetch that
# makes it the launch's answer rather than this checkout's last one. `bin/pi-forks`
# resolves it from the pack's `follow` rule and the checkout's git objects -- the release
# tags merged into the followed branch and containing the series base -- which is the rule
# the launcher applies and the one `merge-check` and `lag` ask, so the gate reads the same
# answer instead of resolving the upstream a second way.
TARGET_TIMEOUT = 300


def resolve_targets(root: Path, series: list[Series]) -> tuple[dict[str, dict[str, str]], dict[str, str]]:
    """The upstream a launch would build, per fork, and why any fork could not be resolved.

    One call, one fetch per followed upstream. The answer is keyed by the fork's name --
    the upstream repository's, which is what `bin/pi-forks.list` uses -- and holds the
    kind, the ref and the commit the launch resolves.

    A fork with no checkout, or one whose upstream cannot be fetched, answers nothing and
    the fingerprint falls back to the tag-list probe below: that probe can name a release
    a launch never builds (a tag cut off the followed branch, or one this checkout has not
    fetched), which is why it is the fallback and not the rule.
    """
    wanted = {one.fork for one in series if one.follows_branch and one.url}
    if not wanted:
        return {}, {}
    argv = [str(root / "bin" / "pi-forks"), "patch-branch", "target", "--fetch"]
    rc, out, _ = run_command(root, argv, TARGET_TIMEOUT, series_env(root))
    found: dict[str, dict[str, str]] = {}
    for line in out.splitlines():
        fields = line.split("\t")
        if len(fields) != 4:
            continue
        url, kind, ref, commit = fields
        tail = url.rstrip("/").rsplit("/", 1)[-1]
        name = tail[:-4] if tail.endswith(".git") else tail
        if name in wanted:
            found[name] = {"kind": kind, "ref": ref, "commit": commit}
    problems: dict[str, str] = {}
    if rc != 0:
        for name in sorted(wanted - set(found)):
            problems[name] = (
                f"bin/pi-forks patch-branch target --fetch could not resolve it"
                + (" (no checkout, or its upstream is unreachable)" if rc == 127 else "")
            )
    return found, problems


def newest_release(tags: dict[str, str]) -> tuple[str, str] | None:
    """The newest release tag and its commit, by semver precedence.

    yolo's rule (patched-forks.md, PF-D4): the newest tag on the followed branch
    whose name is a semantic version, optionally v-led, by semver precedence;
    pre-releases and unparseable tags are never chosen. Only the tag list is read
    here -- not the ancestry a launch also applies, so this is the PROBE used when no
    checkout answered `resolve_targets`, never the resolution a verdict rests on.
    """
    best: tuple[tuple[int, int, int], str, str] | None = None
    for name, sha in tags.items():
        match = VERSION_TAG.match(name)
        if not match:
            continue
        version = tuple(int(part) for part in match.groups())
        if best is None or version > best[0] or (version == best[0] and name > best[1]):
            best = (version, name, sha)
    return (best[1], best[2]) if best else None


def ls_remote(url: str) -> tuple[dict[str, dict[str, str]] | None, str]:
    """One upstream's heads and tags, or (None, why not)."""
    try:
        result = subprocess.run(
            ["git", "ls-remote", url], capture_output=True, text=True, errors="replace",
            timeout=LS_REMOTE_TIMEOUT,
        )
    except (subprocess.TimeoutExpired, OSError) as problem:
        return None, str(problem)
    if result.returncode != 0:
        text = (result.stderr or result.stdout or "git ls-remote failed").strip()
        return None, text.splitlines()[0] if text else "git ls-remote failed"
    heads: dict[str, str] = {}
    tags: dict[str, str] = {}
    peeled: dict[str, str] = {}
    for line in result.stdout.splitlines():
        fields = line.split("\t")
        if len(fields) != 2:
            continue
        sha, ref = fields
        if ref.startswith("refs/heads/"):
            heads[ref[len("refs/heads/") :]] = sha
        elif ref.startswith("refs/tags/"):
            name = ref[len("refs/tags/") :]
            if name.endswith("^{}"):
                peeled[name[:-3]] = sha
            else:
                tags[name] = sha
    tags.update(peeled)
    return {"heads": heads, "tags": tags}, ""


def fingerprint(series: Series, refs: dict[str, dict[str, str]],
                target: dict[str, str] | None = None) -> str:
    """The upstream a launch would build, in one line the record can compare.

    `target` is `resolve_targets`' answer for this fork: the launch's own ref, which is
    what the line names when a checkout could answer. Without it the tag-list probe below
    answers instead, in the same spelling, so a machine with no checkouts still records
    something comparable -- but a probe answer is a question to ask again, not a verdict.
    """
    if not series.follows_branch:
        return f"pinned:{series.ref.lower()}"
    if target:
        kind, ref, commit = target["kind"], target["ref"], target["commit"]
        if kind == "release":
            return f"release:{ref.rsplit('/', 1)[-1]}@{commit}"
        if kind == "head":
            return f"head:{ref.rsplit('/', 1)[-1]}:{commit}"
        if kind == "base":
            # No release contains the series base, so the launch builds the base itself.
            return f"release:base@{commit}"
        return f"pinned:{commit}"
    if (series.follow or "").startswith("head"):
        return f"head:{series.ref}:{refs['heads'].get(series.ref, 'absent')}"
    release = newest_release(refs["tags"])
    return f"release:{release[0]}@{release[1]}" if release else "release:no-version-tag"


def describe(fingerprint: str) -> str:
    kind, _, rest = fingerprint.partition(":")
    if kind == "pinned":
        return f"pinned at {rest[:12]}"
    if kind == "head":
        _, ref, tip = fingerprint.split(":", 2)
        return f"branch {ref} at {tip[:12]}"
    if kind == "release":
        if rest == "no-version-tag":
            return "no version tag"
        name = rest.split("@")[0]
        if name == "base":
            return "the series base, as no release contains it"
        return f"newest release {name}"
    return fingerprint


# ── the record ─────────────────────────────────────────────────────────────────
#
# The record is an append-only JSONL log in the common Git directory. One line per
# save, describing what that run CHANGED; a read folds the lines in order. Nothing
# reads and rewrites the whole document, so two gates in two worktrees append two
# lines and both survive -- there is no record lock to take and no merge to run.
#
# A line is one of:
#
#   {"kind": "run", "at": ..., "branch": ..., "runs": [...], "baselines": {...},
#    "baseline": {...}, "targets": {...}, "inputs": {...}, "triage": {...},
#    "triage_clear": [...], "last_audit": {...}}
#       one run's delta. Every key is optional: a run that only recorded a refusal
#       carries its run entry and nothing else, and an absent key changes nothing.
#
#   {"kind": "snapshot", "at": ..., "state": {...}}
#       the whole folded record, written by compaction and by the one-time migration
#       from `state.json`. A snapshot replaces the accumulator, so a line before it is
#       never read again.
#
# The fold is order-based: the newest `at` per branch, per fingerprint and per triage
# command wins, and a later line wins a tie. A run entry carries an `id`, so a line
# that compaction recovered twice folds once.

# The lock wait the record used to take. There is no record lock any more; the number
# survives only as the default of two flags callers outside this file still pass.
DEFAULT_LOCK_TIMEOUT = 1800


def record_log_path(common_dir: Path) -> Path:
    """The record's log for a repository, from its common Git directory."""
    return common_dir / "yolo-fork-check" / RECORD_NAME


def empty_record() -> dict:
    """A record with nothing recorded: the shape every reader indexes."""
    return {"schema": SCHEMA, "producer": PRODUCER, "targets": {}, "inputs": {},
            "triage": {}, "runs": [], "baselines": {}}


def run_id(entry: dict) -> str:
    """A run entry's identity, so a line folded twice is one run and not two.

    Every run this file writes carries an `id` from `add_run`, and the migration stamps
    one on every run it carries forward. The fallback is for a fixture that seeds a
    record by hand, and it is a cheap tuple rather than a re-serialization: the fold
    runs on every gate run, and a 20-run history is most of a megabyte of JSON.
    """
    identity = entry.get("id")
    if identity:
        return str(identity)
    return f"{entry.get('at')}|{entry.get('commit')}|{entry.get('outcome')}"


def _newer(entry: object, current: object) -> bool:
    """Whether `entry` wins in the fold: newest `at`, and a later line on a tie."""
    if not isinstance(entry, dict):
        return False
    if not isinstance(current, dict):
        return True
    return str(entry.get("at") or "") >= str(current.get("at") or "")


def _newest_map(entries: dict, limit: int) -> dict:
    """The newest `limit` entries by `at`, keeping each entry's key."""
    if len(entries) <= limit:
        return entries

    def recorded_at(pair: tuple[str, object]) -> str:
        entry = pair[1]
        return str(entry.get("at") or "") if isinstance(entry, dict) else ""

    return dict(sorted(entries.items(), key=recorded_at, reverse=True)[:limit])


def bound_record(data: dict) -> dict:
    """Apply the record's three bounds: run history, per-branch baselines, triage."""
    data["runs"] = data["runs"][-MAX_RUNS:]
    data["baselines"] = _newest_map(data["baselines"], MAX_BASELINES)
    data["triage"] = _newest_map(data["triage"], MAX_TRIAGE)
    return data


def fold_record_lines(lines: list[dict]) -> dict:
    """Fold the log's lines into the record a reader sees."""
    data = empty_record()
    seen: set[str] = set()
    for line in lines:
        if not isinstance(line, dict):
            continue
        if line.get("kind") == "snapshot":
            state = line.get("state")
            if not isinstance(state, dict):
                continue
            data = empty_record()
            for key in ("targets", "inputs", "triage", "baselines"):
                if isinstance(state.get(key), dict):
                    data[key] = dict(state[key])
            if isinstance(state.get("runs"), list):
                data["runs"] = [run for run in state["runs"] if isinstance(run, dict)]
            for key in ("baseline", "last_audit"):
                if isinstance(state.get(key), dict):
                    data[key] = state[key]
            seen = {run_id(run) for run in data["runs"]}
            continue
        for run in line.get("runs") or []:
            if not isinstance(run, dict):
                continue
            key = run_id(run)
            if key in seen:
                continue
            seen.add(key)
            data["runs"].append(run)
        for branch, entry in (line.get("baselines") or {}).items():
            if _newer(entry, data["baselines"].get(branch)):
                data["baselines"][branch] = entry
        shared = line.get("baseline")
        if _newer(shared, data.get("baseline")):
            data["baseline"] = shared
        for key in ("targets", "inputs"):
            for name, entry in (line.get(key) or {}).items():
                if _newer(entry, data[key].get(name)):
                    data[key][name] = entry
        for command, entry in (line.get("triage") or {}).items():
            if _newer(entry, data["triage"].get(command)):
                data["triage"][command] = entry
        for command in line.get("triage_clear") or []:
            data["triage"].pop(command, None)
        audit = line.get("last_audit")
        if _newer(audit, data.get("last_audit")):
            data["last_audit"] = audit
    return bound_record(data)


def parse_record_bytes(raw: bytes) -> list[dict]:
    """The JSON objects a run of log bytes holds, skipping a line that is not one.

    A line that does not parse is a write torn by a killed process, not a record, so
    it is skipped rather than guessed at. The caller that measures the compaction
    bound counts it separately; the reader does not see it at all.
    """
    lines: list[dict] = []
    for text in raw.decode("utf-8", "replace").splitlines():
        if not text.strip():
            continue
        try:
            parsed = json.loads(text)
        except ValueError:
            continue
        if isinstance(parsed, dict):
            lines.append(parsed)
    return lines


def read_record_lines(path: Path) -> tuple[list[dict], int]:
    """Every object the log holds, and how many lines were in it.

    One decode and one split for the whole file: this runs on every gate run, so a
    second pass over the bytes to count lines would be half the load cost again.
    """
    try:
        raw = path.read_bytes()
    except OSError:
        return [], 0
    lines: list[dict] = []
    count = 0
    for text in raw.decode("utf-8", "replace").splitlines():
        if not text.strip():
            continue
        count += 1
        try:
            parsed = json.loads(text)
        except ValueError:
            continue
        if isinstance(parsed, dict):
            lines.append(parsed)
    return lines, count


def render_record_line(line: dict) -> bytes:
    return (json.dumps(line, sort_keys=True) + "\n").encode("utf-8")


def _write_all(fd: int, payload: bytes) -> None:
    view = memoryview(payload)
    while view:
        view = view[os.write(fd, view):]


def append_record_line(path: Path, payload: bytes) -> None:
    """Append one line to the log, and prove it landed in the file the path names.

    `O_APPEND` puts the write at the end of the file whatever else is open, and one
    write of the whole line is what keeps two concurrent appends from interleaving.
    The inode check is what makes compaction safe: a compactor that replaced the file
    between this open and this write leaves the line in the old inode, which nobody
    reads, so the write is retried against the new one.
    """
    path.parent.mkdir(parents=True, exist_ok=True)
    for _ in range(8):
        fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o644)
        try:
            _write_all(fd, payload)
            current = os.stat(path)
            mine = os.fstat(fd)
            if (current.st_dev, current.st_ino) == (mine.st_dev, mine.st_ino):
                return
        finally:
            os.close(fd)
    # The file was replaced on every attempt. This last write is the useful one: it is
    # in the file the path names now, or in one replaced after it -- and a line that
    # survives in neither is what the next run's own line re-states.
    fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o644)
    try:
        _write_all(fd, payload)
    finally:
        os.close(fd)


def read_legacy_record(path: Path) -> dict | None:
    """The `state.json` document the log replaced, or None when there is not one."""
    try:
        loaded = json.loads(path.with_name(LEGACY_RECORD_NAME).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    if not isinstance(loaded, dict) or loaded.get("schema") != SCHEMA:
        return None
    return loaded


def migrate_legacy_record(path: Path) -> None:
    """Write the old `state.json` into the log as one snapshot, once, and leave it.

    The old document is the backup a reader returns to if the migration is ever wrong,
    so nothing deletes it. The hard link is what makes the migration idempotent and
    race-safe: `os.link` fails when the target exists, so two first runs cannot both
    write a snapshot, and a snapshot can never land after a line a concurrent run
    already appended.
    """
    if path.exists():
        return
    loaded = read_legacy_record(path)
    if loaded is None:
        return
    # The migrated runs get the same identity `add_run` gives a new one, so a fold of
    # the snapshot and a fold of a line dedupe a run by the same key.
    for run in loaded.get("runs") or []:
        if isinstance(run, dict):
            run.setdefault("id", uuid.uuid4().hex)
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_name(path.name + ".migrate")
    temp.write_bytes(render_record_line({"schema": SCHEMA, "kind": "snapshot",
                                         "at": now_iso(), "state": loaded}))
    try:
        os.link(temp, path)
    except FileExistsError:
        pass
    except OSError:
        # A filesystem without hard links: an exclusive create is the same guarantee
        # about who wins, and the reader tolerates the content landing in place.
        try:
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
        except FileExistsError:
            pass
        else:
            try:
                _write_all(fd, temp.read_bytes())
            finally:
                os.close(fd)
    finally:
        temp.unlink(missing_ok=True)


def _compact_log(path: Path) -> int | None:
    """One compaction, with the compaction marker already held. The new line count.

    The snapshot carries the folded state EXCEPT the run entries: those are re-emitted
    as their own lines, oldest first, exactly as an ordinary save would have written
    them. That is what keeps the log one line per run -- the run history is stored
    once, not once in the snapshot and again in the lines that follow it, so the file
    stays the size the folded view is rather than twice it.
    """
    try:
        handle = path.open("rb")
    except OSError:
        return None
    try:
        before = handle.read()
        # A file whose last line is incomplete is a write torn by a killed process; it
        # is not a record, and the tail below must start at that line's beginning so it
        # is never spliced onto a line this rewrite writes.
        cut = before.rfind(b"\n") + 1
        complete, partial = before[:cut], before[cut:]
        state = fold_record_lines(parse_record_bytes(complete))
        runs = [run for run in state.pop("runs", []) if isinstance(run, dict)]
        payload = render_record_line({"schema": SCHEMA, "kind": "snapshot",
                                      "at": now_iso(), "state": state})
        for run in runs:
            payload += render_record_line({"schema": SCHEMA, "kind": "run",
                                           "at": run.get("at"), "runs": [run]})
        temp = path.with_name(f"{path.name}.compact.{os.getpid()}.{time.time_ns()}")
        try:
            temp.write_bytes(payload)
            os.replace(temp, path)
        except OSError:
            # The rename is the only step that can fail against a live log, and a log
            # that cannot be replaced is a log the next save tries again.
            temp.unlink(missing_ok=True)
            return None
        # Lines appended to the inode we read after this compaction took over are in
        # neither the snapshot nor the new file: recover them from the old descriptor.
        handle.seek(cut)
        tail = handle.read()
        rest = partial + tail
        recovered = 0
        if rest:
            append_record_line(path, rest if rest.endswith(b"\n") else rest + b"\n")
            recovered = sum(1 for line in rest.split(b"\n") if line.strip())
        return 1 + len(runs) + recovered
    finally:
        handle.close()


def compact_record(path: Path) -> int | None:
    """Rewrite the log as one snapshot line plus its run lines, and say its new length.

    The snapshot is written to a sibling and renamed over the log, so a reader sees the
    old file or the new one and never a half-written third. A line appended to the old
    inode after the fold was read is recovered by reading the old descriptor from where
    the fold stopped; an appender that finds its write in the old inode retries against
    the new one. A line recovered twice carries the same `id` and folds once.

    Two compactors must not interleave -- the second one's snapshot was built from the
    file the first already replaced, so it would drop the first one's recovered tail.
    The marker file is a NON-BLOCKING `flock` held for this compaction only: it never
    serializes a run and never waits, so a run that cannot take it skips this
    compaction and tries again on its next save. It is not the record lock the gate
    used to take -- the record itself is still append-only, and a save that does not
    compact takes no lock at all. `flock` releases on process death, so a compacting
    process that was killed leaves no stale marker behind.
    """
    path.parent.mkdir(parents=True, exist_ok=True)
    marker = path.with_name(path.name + ".compacting")
    try:
        marker_fd = os.open(str(marker), os.O_RDWR | os.O_CREAT, 0o644)
    except OSError:
        return None
    try:
        try:
            fcntl.flock(marker_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return None
        return _compact_log(path)
    finally:
        os.close(marker_fd)


class Record:
    def __init__(self, path: Path, branch: str | None = None):
        self.path = path
        # The full ref this run's checkout has checked out (`refs/heads/<name>`), or
        # None on a detached HEAD. A verified run advances the entry for this ref only,
        # so one worktree's run cannot move the base another worktree plans from.
        self.branch = branch
        # The entry THIS run plans against, set by `run_gate` before the plan is built.
        # `baseline` reads it rather than the record so a run's own plan and the report
        # it prints describe the same commit -- the property used to name HEAD after the
        # advance had already been written.
        self.planning_baseline: dict | None = None
        # What THIS run changed, and therefore everything its one line carries. A read
        # folds the log; a save appends this delta, so nothing here re-states the record.
        self.delta: dict = {"runs": [], "baselines": {}, "baseline": None,
                            "targets": {}, "inputs": {}, "triage": {},
                            "triage_clear": [], "last_audit": None}
        # How many lines the log held when it was read, for the compaction bound.
        self._lines = 0
        self.data: dict = empty_record()
        if path.exists():
            lines, count = read_record_lines(path)
            self._lines = count
            self.data = fold_record_lines(lines)
        else:
            # The record from before the log is folded in memory, so a read-only run
            # (`--explain`, `--preflight`) plans from it and writes nothing. The first
            # save writes the migration, and the old file stays as the backup.
            legacy = read_legacy_record(path)
            if legacy is not None:
                self.data = fold_record_lines([{"kind": "snapshot", "state": legacy}])

    @property
    def baseline(self) -> dict | None:
        """The verified commit this run plans against, or None for a first run.

        `build_plan` reads it to decide whether the installed yolo build and node
        version have an earlier verification to differ from, so it must be the entry
        this run's own diff came from, never "the last run, whichever branch".
        """
        return self.planning_baseline

    @property
    def baselines(self) -> dict:
        """The verified commit recorded per branch, keyed by full ref."""
        entries = self.data.get("baselines")
        if not isinstance(entries, dict):
            entries = {}
            self.data["baselines"] = entries
        return entries

    def baseline_for(self, branch: str | None) -> dict | None:
        """The verified commit recorded for one branch, if the record has one."""
        if branch is None:
            return None
        entry = self.baselines.get(branch)
        return entry if isinstance(entry, dict) and entry.get("commit") else None

    @property
    def shared_baseline(self) -> dict | None:
        """The newest verified commit in the record, whichever branch produced it.

        Kept for a reader that predates per-branch entries, and as one more candidate
        a run may plan from -- it is only ever used when this history contains it.
        """
        entry = self.data.get("baseline")
        return entry if isinstance(entry, dict) and entry.get("commit") else None

    def advance_baseline(self, commit: str, tree: str) -> None:
        """Record a verified commit: this branch's entry, and the shared newest one."""
        entry = {"commit": commit, "tree": tree, "at": now_iso()}
        if self.branch is not None:
            self.baselines[self.branch] = dict(entry)
            self.delta["baselines"][self.branch] = dict(entry)
        self.data["baseline"] = entry
        self.delta["baseline"] = entry
        prune_baselines(self)

    def set_target(self, owner_key: str, entry: dict) -> None:
        self.data["targets"][owner_key] = entry
        self.delta["targets"][owner_key] = entry

    def set_input(self, name: str, entry: dict) -> None:
        self.data["inputs"][name] = entry
        self.delta["inputs"][name] = entry

    def set_triage(self, command: str, entry: dict) -> None:
        self.data["triage"][command] = entry
        self.delta["triage"][command] = entry

    def clear_triage(self, command: str) -> None:
        """Drop a recorded failure this run re-ran and passed."""
        self.data["triage"].pop(command, None)
        self.delta["triage"].pop(command, None)
        if command not in self.delta["triage_clear"]:
            self.delta["triage_clear"].append(command)

    def set_last_audit(self, entry: dict) -> None:
        self.data["last_audit"] = entry
        self.delta["last_audit"] = entry

    def target(self, owner_key: str) -> dict | None:
        entry = self.data["targets"].get(owner_key)
        return entry if isinstance(entry, dict) else None

    @property
    def recorded_yolo(self) -> dict | None:
        """The yolo build identity recorded with the last verified run, if any."""
        inputs = self.data.get("inputs")
        entry = inputs.get("yolo") if isinstance(inputs, dict) else None
        return entry if isinstance(entry, dict) and entry.get("fingerprint") else None

    @property
    def recorded_node(self) -> dict | None:
        """The node version recorded with the last verified run, if any."""
        inputs = self.data.get("inputs")
        entry = inputs.get("node") if isinstance(inputs, dict) else None
        return entry if isinstance(entry, dict) and entry.get("fingerprint") else None

    def add_run(self, entry: dict) -> None:
        entry.setdefault("id", uuid.uuid4().hex)
        self.data["runs"].append(entry)
        self.delta["runs"].append(entry)

    @property
    def excerpt_path(self) -> Path:
        """Where a failing run's own output goes: beside the record, not inside it."""
        return self.path.with_name(EXCERPT_NAME)

    @property
    def timings_path(self) -> Path:
        """Where the append-only timing timeline lives: beside the record."""
        return self.path.with_name(TIMINGS_NAME)

    @property
    def evidence_path(self) -> Path:
        """Where the shared pipeline-evidence records live: beside the record."""
        return self.path.with_name(EVIDENCE_NAME)

    @property
    def triage_path(self) -> Path:
        """Where the newest run's triage packets go: beside the record, not inside it."""
        return self.path.with_name(TRIAGE_NAME)

    def save(self) -> None:
        """Append this run's line, and compact the log once it passes its bound.

        One line, one write, and no read of the file first: a concurrent gate appends
        its own line and neither save can drop the other's entry, so there is nothing
        to lock and nothing to merge. Compaction is the one rewrite, and it is bounded
        by `MAX_RECORD_LINES` and safe against a concurrent append (`compact_record`).
        """
        migrate_legacy_record(self.path)
        line = {"schema": SCHEMA, "kind": "run", "at": now_iso(),
                "branch": self.branch, **self.delta}
        append_record_line(self.path, render_record_line(line))
        self._lines += 1
        if self._lines > MAX_RECORD_LINES:
            compacted = compact_record(self.path)
            if compacted is not None:
                self._lines = compacted


# ── triage: what a failed check's own output says ───────────────────────────────


# The shapes a failing case is named in. Each runner says it differently, and the gate
# runs all of them: node's test runner (TAP when its output is captured, the spec
# reporter when a suite asks for it), Python's unittest and pytest, Go's test binary, and
# the shell suites that print TAP of their own.
NODE_NOT_OK = re.compile(r"^\s*not ok \d+ - (?P<name>.+?)\s*$")
# The spec reporter prints a duration in parentheses on every case line, and its
# section headers ("failing tests:") carry none: the duration is what tells a case
# from a header, so it is required rather than optional.
NODE_SPEC_FAIL = re.compile(r"^\s*[\u2716\u00d7]\s+(?P<name>.+?)\s+\(\d+(?:\.\d+)?m?s\)\s*$")
NODE_LOCATION = re.compile(r"^\s*location:\s*'(?P<file>[^']+?)'\s*$")
NODE_ERROR = re.compile(r"^(?P<indent>\s*)error:\s*(?P<inline>.*)$")
SPEC_AT = re.compile(r"^\s*test at (?P<file>[^\s]+):(?P<line>\d+):\d+\s*$")
UNITTEST_FAIL = re.compile(r"^(?:FAIL|ERROR):\s+(?P<name>\S+)\s+\((?P<where>[^)]+)\)\s*$")
PYTEST_FAIL = re.compile(r"^FAILED\s+(?P<file>[^\s:]+)(?:::(?P<name>\S+))?\s*(?:-\s*(?P<text>.*))?$")
GO_FAIL = re.compile(r"^\s*--- FAIL: (?P<name>\S+)")
GO_POSITION = re.compile(r"^\s*(?P<file>[^\s:]+\.go):(?P<line>\d+):\s*(?P<text>.*)$")
TRACEBACK_FILE = re.compile(r'^\s*File "(?P<file>[^"]+)", line (?P<line>\d+), in (?P<func>\S+)')
EXCEPTION_LINE = re.compile(r"^(?P<kind>[A-Za-z_][\w.]*(?:Error|Exception|Failure|Exit))\b:?\s*(?P<text>.*)$")
# A path an assertion names. Repo-relative or absolute, with a file extension: a token
# like `1.5s` or `v9` is not a path, and a path is what `git log` can be asked about.
PATH_TOKEN = re.compile(r"(?<![\w./-])(?P<path>(?:[\w.@+-]+/)+[\w.@+-]+\.[A-Za-z][\w.+-]*)")
# A finding with a location and no test case: the gate's own citation guards and the size
# budget print `path:line: what is wrong`, and a compiler does the same. There is no case
# name to report, so the finding's own words are the name and its location is the file.
GUARD_FINDING = re.compile(
    r"^(?P<file>[^\s:]+\.[A-Za-z][\w.+-]*):(?P<line>\d+):\s*(?P<text>\S.*)$")
# A flat Python script -- the gate's own audit suite is one -- names a failing assertion
# with the interpreter's traceback: the last `File "...", line N, in <frame>` frame before
# the exception, and the exception's own words. Only an `AssertionError` is a failing
# assertion; any other exception is the script not completing (an import error, a typo, a
# crash), which names no case and stays "not verified".
TRACEBACK_START = re.compile(r"^Traceback \(most recent call last\):\s*$")
SCRIPT_FRAME = re.compile(r'^\s*File "(?P<file>[^"]+)", line (?P<line>\d+), in (?P<func>\S+)')
SCRIPT_ASSERTION = re.compile(r"^AssertionError\b:?\s*(?P<text>.*)$")


def _relative(root: Path, path: str) -> str:
    """A path as the repository spells it: absolute paths under `root` lose the prefix."""
    if not path:
        return ""
    candidate = Path(path)
    if candidate.is_absolute():
        try:
            return str(candidate.relative_to(root))
        except ValueError:
            return str(candidate)
    return path


def _case(name: str, file: str = "", line: int | None = None,
          assertion: str = "") -> dict:
    return {"name": name.strip(), "file": file, "line": line, "assertion": assertion.strip()}


def _node_cases(lines: list[str]) -> list[tuple[int, dict]]:
    """Cases node's test runner names, from TAP or from the spec reporter.

    TAP puts the location and the error in a YAML block under the `not ok` line. The spec
    reporter names the case with a cross, prints its file and line on a `test at ...` line
    just above it, and the assertion on the indented lines below. Both are read here,
    because which one node prints depends on the reporter its own defaults pick, and the
    gate's job is to read the transcript it was handed rather than assume one.
    """
    found: list[tuple[int, dict]] = []
    for index, line in enumerate(lines):
        tap = bool(NODE_NOT_OK.match(line))
        named = NODE_NOT_OK.match(line) or NODE_SPEC_FAIL.match(line)
        if not named:
            continue
        case = _case(named.group("name"))
        if not tap:
            for back in range(max(0, index - 6), index):
                where = SPEC_AT.match(lines[back])
                if where:
                    case["file"] = where.group("file")
                    case["line"] = int(where.group("line"))
                    break
        assertion: list[str] = []
        error_indent: int | None = None
        blanks = 0
        for follow in lines[index + 1:index + 60]:
            if NODE_NOT_OK.match(follow) or NODE_SPEC_FAIL.match(follow):
                break
            location = NODE_LOCATION.match(follow)
            if location:
                where = location.group("file")
                parts = where.rsplit(":", 2)
                if len(parts) >= 2 and parts[-1].isdigit():
                    case["file"] = parts[0]
                    case["line"] = int(parts[1]) if parts[1].isdigit() else None
                else:
                    case["file"] = where
                continue
            error = NODE_ERROR.match(follow)
            if error:
                inline = error.group("inline").strip()
                if inline and inline not in ("|", "|-", ">", ">-"):
                    assertion = [inline.strip("'\"")]
                    break
                error_indent = len(error.group("indent"))
                continue
            if error_indent is not None:
                indent = len(follow) - len(follow.lstrip())
                if not follow.strip():
                    # The block keeps a blank line between node's own message and the
                    # operands; the block ends where its indentation does.
                    continue
                if indent > error_indent:
                    assertion.append(follow.strip())
                    continue
                break
            if tap:
                continue
            # The spec reporter's shape: prose under the cross, up to the stack frames. The
            # assertion is often two paragraphs -- node's own message, then the operands --
            # so a blank line between them does not end it; a dedent or a stack frame does.
            stripped = follow.strip()
            if not stripped:
                blanks += 1
                if blanks > 2:
                    break
                continue
            blanks = 0
            if stripped.startswith("at ") or stripped.startswith("file://"):
                break
            if not follow.startswith((" ", "\t")):
                break
            if re.match(r"^(duration_ms|type|location|code|failureType)\b", stripped):
                continue
            assertion.append(stripped)
            if len(assertion) >= 4:
                break
        if assertion:
            case["assertion"] = "\n".join(assertion).strip()
        found.append((index, case))
    return found


def _unittest_cases(lines: list[str]) -> list[tuple[int, dict]]:
    """Cases Python's unittest names, with the traceback's last file and the exception."""
    found: list[tuple[int, dict]] = []
    for index, line in enumerate(lines):
        named = UNITTEST_FAIL.match(line)
        if not named:
            continue
        case = _case(named.group("name"))
        for follow in lines[index + 1:index + 200]:
            if UNITTEST_FAIL.match(follow) or re.match(r"^Ran \d+ test", follow):
                break
            where = TRACEBACK_FILE.match(follow)
            if where:
                case["file"] = where.group("file")
                case["line"] = int(where.group("line"))
                continue
            raised = EXCEPTION_LINE.match(follow.strip())
            if raised and raised.group("kind") not in ("Traceback",):
                text = raised.group("text").strip()
                case["assertion"] = f"{raised.group('kind')}: {text}".strip(": ")
                break
        found.append((index, case))
    return found


def _pytest_cases(lines: list[str]) -> list[tuple[int, dict]]:
    found: list[tuple[int, dict]] = []
    for index, line in enumerate(lines):
        named = PYTEST_FAIL.match(line)
        if not named:
            continue
        case = _case(named.group("name") or named.group("file"), file=named.group("file"))
        if named.group("text"):
            case["assertion"] = named.group("text")
        found.append((index, case))
    return found


def _go_cases(lines: list[str]) -> list[tuple[int, dict]]:
    found: list[tuple[int, dict]] = []
    for index, line in enumerate(lines):
        named = GO_FAIL.match(line)
        if not named:
            continue
        case = _case(named.group("name"))
        for follow in lines[index + 1:index + 40]:
            if GO_FAIL.match(follow) or follow.startswith(("FAIL", "ok ", "PASS")):
                break
            where = GO_POSITION.match(follow)
            if where:
                case["file"] = where.group("file")
                case["line"] = int(where.group("line"))
                case["assertion"] = where.group("text").strip()
                break
        found.append((index, case))
    return found


def _script_cases(lines: list[str], script: str = "") -> list[tuple[int, dict]]:
    """A flat script's own failing assertion, from the interpreter's traceback.

    Only the LAST traceback block is read: the script's own failure is the last thing it
    prints, and an earlier block is a transcript the script captured from a gate it
    spawned. `script`, when given, is the file the check runs, and a frame outside it is
    not this check's case. An exception that is not an `AssertionError` is the script not
    completing, which names no case.
    """
    starts = [index for index, line in enumerate(lines) if TRACEBACK_START.match(line)]
    if not starts:
        return []
    start = starts[-1]
    frame = None
    source = ""
    for follow in lines[start + 1:start + 200]:
        if TRACEBACK_START.match(follow):
            break
        where = SCRIPT_FRAME.match(follow)
        if where:
            file = where.group("file")
            if not script or file == script or file.endswith("/" + script):
                frame = where
                source = ""
            continue
        if frame is not None and not source and follow.startswith((" ", "\t")) \
                and not follow.strip().startswith("^"):
            source = follow.strip()
            continue
        raised = SCRIPT_ASSERTION.match(follow)
        if raised:
            if frame is None:
                return []
            file = frame.group("file")
            text = raised.group("text").strip()
            name = text or source or f"assertion at {file}:{frame.group('line')}"
            return [(start, _case(name, file=file, line=int(frame.group("line")),
                                  assertion=text or source))]
        if EXCEPTION_LINE.match(follow.strip()) and not follow.startswith((" ", "\t")):
            return []
    return []


def _guard_cases(lines: list[str]) -> list[tuple[int, dict]]:
    """Findings that name a location instead of a test case.

    The `.ts` size budget and the two citation guards run in process and report
    `path:line: text`; a compiler reports the same shape. A packet for one of those still
    names the file, the line and the words, so the repair path opens one file rather than
    the whole tree -- there is just no case name, and the finding's own text stands in.
    """
    found: list[tuple[int, dict]] = []
    for index, line in enumerate(lines):
        named = GUARD_FINDING.match(line)
        if not named:
            continue
        text = named.group("text").strip()
        case = _case(text[:120] or "finding", file=named.group("file"),
                     line=int(named.group("line")), assertion=text)
        found.append((index, case))
    return found


def failure_cases(output: str) -> list[dict]:
    """Every failing case a check's output names, in the order it named them.

    Four runners, four shapes, one list. A case is a name, the file and line it lives at,
    and the assertion's own words -- which is the whole of what a debugger needs to start
    on one file instead of on the suite. Output in no recognised shape yields no cases,
    and the packet then says so rather than inventing one.
    """
    lines = (output or "").splitlines()
    merged: list[tuple[int, dict]] = []
    for scanner in (_node_cases, _unittest_cases, _pytest_cases, _go_cases, _guard_cases):
        merged.extend(scanner(lines))
    merged.sort(key=lambda pair: pair[0])
    cases: list[dict] = []
    by_name: dict[str, dict] = {}
    for _, case in merged:
        if not case["name"]:
            continue
        existing = by_name.get(case["name"])
        if existing is not None and (not existing["file"] or not case["file"]
                                     or existing["file"] == case["file"]):
            # One case is often named twice: the run's own summary and the failing-tests
            # block. The mentions are one case, and the richer mention fills the holes.
            for field in ("file", "line", "assertion"):
                if not existing.get(field) and case.get(field):
                    existing[field] = case[field]
            continue
        by_name[case["name"]] = case
        cases.append(case)
    return cases


def named_paths(root: Path, case: dict) -> list[str]:
    """The repository paths a failing case names, its own file first.

    A path is kept only when the repository has it or the case's own file names it, so a
    token that merely looks like a path (`a.b`, `1.5s`) does not become a `git log`
    question. The case's file is always first: it is the one file a debugger opens.
    """
    found: list[str] = []
    if case["file"]:
        found.append(_relative(root, case["file"]))
    for token in PATH_TOKEN.findall(case["assertion"] or ""):
        relative = _relative(root, token)
        if relative in found:
            continue
        if (root / relative).exists():
            found.append(relative)
    return found


def touching_commits(root: Path, path: str) -> list[dict]:
    """The newest commits that touched one path: the history a regression is in."""
    if not path or not (root / path).exists():
        return []
    shown = subprocess.run(
        ["git", "-C", str(root), "log", "-n", str(TRIAGE_COMMITS),
         "--format=%h%x09%s", "--", path],
        capture_output=True, text=True, errors="replace",
    )
    commits: list[dict] = []
    for line in shown.stdout.splitlines():
        sha, _, subject = line.partition("\t")
        if sha.strip():
            commits.append({"sha": sha.strip(), "subject": subject.strip()})
    return commits


def _reproduce(check: Check, root: Path, case: dict) -> tuple[str, str | None]:
    """The one-file command that reproduces a case, and the single-case form of it.

    A `node --test` step names its files on the command line, so the case's own file is
    handed back to the same runner, and `--test-name-pattern` narrows it to the case. A
    step that runs one script over many files -- a suite entry point, a driver -- has no
    one-file form to derive, and says so: the packet's `scope` is `check` and the command
    it names is the check's own, which the repair path may run but not repeat as a suite.
    """
    argv = check.argv or []
    file = case["file"]
    if file:
        file = _relative(root, file)
    if argv[:2] == ["node", "--test"] or (argv[:1] == ["node"] and "--test" in argv):
        target = file if file else next(
            (word for word in argv[2:] if word.endswith((".mjs", ".js", ".ts"))), "")
        if target:
            one_file = f"node --test {shlex.quote(target)}"
            if case["name"]:
                one_case = (f"node --test --test-name-pattern "
                            f"{shlex.quote(case['name'])} {shlex.quote(target)}")
                return one_file, one_case
            return one_file, None
    if argv[:1] in (["python3"], ["python"]) and len(argv) >= 2 and argv[1].endswith(".py"):
        target = file or argv[1]
        return f"{argv[0]} {shlex.quote(target)}", None
    # A step that runs a script or a driver over many files has no one-file form to derive.
    # The packet says so by naming the check's own command, which the repair path may run
    # once; the suite it starts is not a thing to repeat.
    return check.command_line(), None


def candidate_identity(root: Path, check: Check) -> dict | None:
    """The commit and tree of the candidate a fork-quality step judged, or None.

    A triage packet is evidence about one tree. Keying it to the run alone let a packet
    from an earlier candidate stand as if it were about the current one, so the packet
    carries the candidate's own identity and the report names it when the packet is
    historical (board item 194).
    """
    if not check.fork:
        return None
    candidate = candidate_root(root) / check.fork
    if not (candidate / ".git").exists():
        return None
    commit = git(candidate, "rev-parse", "--verify", "--quiet", "HEAD^{commit}",
                 check=False).strip()
    tree = git(candidate, "rev-parse", "--verify", "--quiet", "HEAD^{tree}",
               check=False).strip()
    if not commit and not tree:
        return None
    return {"commit": commit or None, "tree": tree or None}


def triage_packet(check: Check, root: Path) -> dict:
    """One failed check's own output, as a packet a debugger can be dispatched with.

    The packet is the answer to "what failed, and where do I look", read from the run that
    failed rather than from a second one: the case's name and file, the assertion text,
    the paths the assertion names, the commits that touched those paths, and the one-file
    command that reproduces it. `runs_allowed` is the debugger's budget for settling
    determinism on that one file, and `may_not_run` names the two things the repair path
    must not do -- re-run the gate, or run the whole suite -- because a blind second run is
    exactly what this replaces.
    """
    cases = check.own_cases
    kept = cases[:TRIAGE_CASES]
    for case in kept:
        case["file"] = _relative(root, case["file"])
        paths = named_paths(root, case)
        case["paths"] = paths
        case["commits"] = [{"path": path, "touched": touching_commits(root, path)}
                           for path in paths]
        one_file, one_case = _reproduce(check, root, case)
        case["reproduce"] = one_file
        case["single_case"] = one_case
    first = kept[0] if kept else {}
    return {
        "check": check.kind,
        "label": check.label,
        "command": check.command_line(),
        # The candidate this packet is about, when the step judged one. A packet is
        # evidence about a tree, so a later run must be able to tell whether it is
        # looking at this run's candidate or an earlier one's (board item 194).
        "candidate": candidate_identity(root, check),
        "exit": check.rc,
        "seconds": round(check.seconds, 2),
        "cases": kept,
        "dropped": max(0, len(cases) - len(kept)),
        "scope": "file" if first.get("file") else "check",
        "reproduce": first.get("reproduce") or check.command_line(),
        "single_case": first.get("single_case"),
        "runs_allowed": TRIAGE_RUNS,
        "may_not_run": ["the gate (`just done`)", "the whole suite the check runs"],
    }


def write_triage(record: Record, entry: dict, checks: list[Check],
                 root: Path) -> tuple[str | None, list[dict]]:
    """Write the failed checks' triage packets beside the record; return the path.

    The record's per-check entries are a verdict, not a diagnosis, so a reader on another
    machine -- or on this one after the scrollback is gone -- could only ask whoever ran it
    to paste the output. The packet answers the question the output raises instead: which
    case, which file, which assertion, which commits touched what it names, and the one
    command that reproduces it on one file. A run with nothing failing writes nothing and
    leaves an earlier failure's packets in place, so a later green run cannot erase what a
    reader is diagnosing.
    """
    # A stale-candidate refusal is not a test failure: the step never ran its command,
    # so there is no case to name and no packet to dispatch a debugger with. Its own
    # output already names the expected and actual identity and the rebuild command.
    failed = [check for check in checks
              if check.failed and not (check.preflight_refused or candidate_stale(check))]
    if not failed:
        return None, []
    packets = [triage_packet(check, root) for check in failed]
    path = record.triage_path
    document = {
        "at": entry["at"],
        "commit": entry["commit"],
        "scope": entry["scope"],
        "outcome": entry["outcome"],
        "checks": packets,
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(".json.new")
    temp.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n")
    os.replace(temp, path)
    return str(path), packets


def record_triage(record: Record, entry: dict, packets: list[dict]) -> None:
    """Keep the newest packet per failing command in the record itself.

    The record is what a later reader -- or the repair path, with `--triage` -- consults
    without knowing which file the last failing run wrote. One entry per command, replaced
    by the newest failure, so the record names the failure that still stands instead of a
    count of flakes nobody is fixing.
    """
    for packet in packets:
        record.set_triage(packet["command"], {
            "kind": packet["check"],
            "label": packet["label"],
            "at": entry["at"],
            "commit": entry["commit"],
            # The candidate the packet is about, so a later run can tell an earlier
            # candidate's failure from this one's (board item 194).
            "candidate": packet.get("candidate"),
            "cases": [{"name": case["name"], "file": case["file"]} for case in packet["cases"]],
            "dropped": packet["dropped"],
            "reproduce": packet["reproduce"],
            "single_case": packet["single_case"],
            "runs_allowed": packet["runs_allowed"],
        })


def clear_triage(record: Record, checks: list[Check]) -> None:
    """Drop a recorded failure once the check that produced it runs and passes here.

    The record names the failure that still stands, so a check that ran and passed clears
    its own entry: without this a fixed failure would keep printing on every later run that
    does not plan the check, and a reader could not tell it from an open one.
    """
    ledger = record.data.get("triage")
    if not isinstance(ledger, dict):
        return
    for check in checks:
        if check.failed:
            continue
        command = check.command_line()
        # A clear for a command the record does not hold would grow the log's line for
        # nothing; the check passing is not news about a failure nobody recorded.
        if command in ledger:
            record.clear_triage(command)


def recorded_triage(record: Record, plan: Plan | None) -> list[str]:
    """Recorded failures this run did not plan, so a stale packet does not go quiet.

    A check whose watched paths did not change is not planned, so its own line never
    prints. Without this the record's newest packet would be visible only on the runs that
    happen to plan the check that produced it.
    """
    ledger = record.data.get("triage")
    if not isinstance(ledger, dict):
        return []
    planned = {check.command_line() for check in plan.checks} if plan is not None else set()
    lines: list[str] = []
    for command, entry in sorted(ledger.items()):
        if command in planned or not isinstance(entry, dict):
            continue
        cases = entry.get("cases") or []
        named = cases[0].get("name") if cases and isinstance(cases[0], dict) else ""
        candidate = entry.get("candidate") or {}
        where = ""
        if isinstance(candidate, dict) and candidate.get("commit"):
            where = f", candidate {str(candidate['commit'])[:12]}"
        lines.append(f"{entry.get('kind', 'check')}: {entry.get('label', command)} -- "
                     f"failed {entry.get('at', 'unknown')} at "
                     f"{str(entry.get('commit', 'unknown'))[:12]}{where}"
                     + (f" ({named})" if named else ""))
    return lines


def triage_lines(packet: dict) -> list[str]:
    """A packet as the report prints it: what failed, where, and what may be run.

    Printed rather than left in the file, because the person who ran the gate is the one
    who decides whether to dispatch a debugger, and a path alone asks them to open a JSON
    document to learn whether the failure is a test, a build or a series.
    """
    lines: list[str] = []
    for case in packet["cases"]:
        where = case["file"] or "(no file named)"
        if case.get("line"):
            where += f":{case['line']}"
        lines.append(f"case: {case['name']} -- {where}")
        if case["assertion"]:
            first = case["assertion"].splitlines()[0]
            lines.append(f"assertion: {first[:200]}")
        if case.get("paths"):
            lines.append(f"paths: {', '.join(case['paths'])}")
        for named in case.get("commits", []):
            for commit in named["touched"]:
                lines.append(f"touched {named['path']}: {commit['sha']} {commit['subject'][:80]}")
        lines.append(f"reproduce (one file): {case['reproduce']}")
        if case.get("single_case"):
            lines.append(f"one case: {case['single_case']}")
    if not packet["cases"]:
        lines.append(f"no case named in the output; reproduce with: {packet['reproduce']}")
    if packet["dropped"]:
        lines.append(f"({packet['dropped']} more failing case(s) not listed; the packet keeps "
                     f"{len(packet['cases'])})")
    lines.append(f"repair: dispatch a debugger with this packet -- it may run the one-file "
                 f"command up to {packet['runs_allowed']} time(s) to settle determinism, and "
                 f"may not run {', or '.join(packet['may_not_run'])}")
    lines.append(flake_override_line(packet))
    return lines


def flake_override_line(packet: dict) -> str:
    """The one path a green re-run has to take, printed beside the packet that licenses it.

    A one-file re-run that passes is a red overruled by a re-run, and the flake has to be
    filed at the moment the merge goes ahead rather than remembered afterwards. That is a
    single board command that records the override and files the item in one transaction,
    so the packet names it with the check and the case already filled in: the operator has
    the evidence in hand and nothing left to look up.
    """
    check = shlex.quote(packet["check"])
    cases = packet.get("cases") or []
    named = cases[0].get("name") if cases and isinstance(cases[0], dict) else ""
    case = f" --case {shlex.quote(named)}" if named else ""
    return (f"if the one-file re-run passes, the red is overruled by a re-run: file the flake in "
            f"the same act -- board flake override --check {check}{case} "
            f"--failing <this run's evidence> --passing <the re-run's evidence>")


def print_triage(plan: Plan, record: Record, packets: list[dict], path: str | None) -> None:
    """Name every failing check's packet, and the record's older failures that did not run."""
    if packets:
        print()
        print(f"fork-check: triage -- {len(packets)} failed check(s), diagnosed from this run's "
              f"own output and never re-run:")
        for packet in packets:
            print(f"  triage {packet['check']}: {packet['label']}")
            print(f"       command: {packet['command']}")
            for line in triage_lines(packet):
                print(f"       {line}")
        if path is not None:
            print(f"fork-check: the machine-readable packet is {path}")
    stale = recorded_triage(record, plan)
    if stale:
        # HISTORICAL, not this run's failures: the check did not run here, so its packet
        # is evidence about an earlier run and possibly an earlier candidate. It is named
        # with its originating run and candidate so a reader can tell it from a live red,
        # and it is never counted in this run's failure tally or its summary (item 194).
        print(f"fork-check: {len(stale)} recorded failure(s) did not run here -- historical, "
              "not this run's failures; their packets stand until the check that produced "
              "them runs again:")
        for line in stale:
            print(f"           {line}")


def materialized_fingerprint(path: str) -> str | None:
    """The patched tree a fork-quality step ran against, as one line a reader can compare.

    The candidate checkout is materialized from the series at its base, so its HEAD tree is
    the patched source tree. A missing checkout, a directory that is not a git repository,
    or a git that cannot answer all return None: a fingerprint that cannot be read is not a
    fingerprint, and instrumentation must never fail the run that produced it.
    """
    if not path:
        return None
    try:
        done = subprocess.run(["git", "-C", path, "rev-parse", "HEAD^{tree}"],
                              capture_output=True, text=True, timeout=30)
    except (OSError, subprocess.SubprocessError):
        return None
    tree = done.stdout.strip()
    return f"tree:{tree}" if done.returncode == 0 and tree else None


def verdict_key(tree: str, command: list[str], install: str, runtime: dict) -> str:
    """The cache key for one fork-quality verdict: tree, command, install mode, runtime.

    Upstream ref and series identity are deliberately absent: if two different upstreams
    plus patches produce the identical tree, the verdict on that tree is the verdict. The
    key is a hash of exactly the four things a verdict depends on, so any change to one
    of them is a different key and a miss -- invalidation needs no mechanism beyond the
    key, and eviction is a space policy only.
    """
    payload = json.dumps({
        "tree": tree,
        "command": list(command),
        "install": install,
        "runtime": runtime,
    }, sort_keys=True, separators=(",", ":"))
    return "sha256:" + hashlib.sha256(payload.encode("utf-8")).hexdigest()


class VerdictCache:
    """Green fork-quality verdicts, keyed by tree + command + install mode + runtime.

    The store is a JSON file beside the record, in the common Git directory. That is
    outside the scratch worktree and outside the candidate checkout, so a candidate's
    own git identity (`git rev-parse --git-common-dir` in the candidate) names the
    candidate's own `.git`, never this file. A candidate's build or test writes cannot
    reach it by any path it derives from its own checkout, which is the trust boundary
    the design requires: a candidate cannot certify itself.

    Only green verdicts are kept. A lookup that misses, or a store that cannot be read,
    is a miss -- never a wrong answer. A write that fails is reported once and changes
    nothing about the verdict, exactly like the timings and evidence timelines.
    """

    def __init__(self, path: Path):
        self.path = path
        self.data: dict = {"schema": VERDICT_SCHEMA, "verdicts": {}}
        self.dirty = False
        self.lock = threading.Lock()
        if path.exists():
            try:
                loaded = json.loads(path.read_text())
            except (OSError, json.JSONDecodeError):
                loaded = None
            if isinstance(loaded, dict) and loaded.get("schema") == VERDICT_SCHEMA:
                self.data = loaded
                self.data.setdefault("verdicts", {})

    def lookup(self, key: str) -> dict | None:
        """The green verdict recorded for this key, or None."""
        with self.lock:
            entry = self.data["verdicts"].get(key)
        return entry if isinstance(entry, dict) and entry.get("rc") == 0 else None

    def record(self, key: str, provenance: dict) -> None:
        """Record one green verdict, with the provenance a reader needs beside it."""
        with self.lock:
            self.data["verdicts"][key] = {**provenance, "rc": 0, "at": now_iso()}
            self.dirty = True
            self._evict()

    def _evict(self) -> None:
        """Keep the newest MAX_VERDICTS. A space policy, never a correctness one."""
        verdicts = self.data["verdicts"]
        if len(verdicts) <= MAX_VERDICTS:
            return
        ordered = sorted(verdicts.items(), key=lambda item: item[1].get("at", ""))
        for key, _ in ordered[:len(verdicts) - MAX_VERDICTS]:
            del verdicts[key]

    def save(self) -> Path | None:
        """Write the store atomically, and never fail the run for it."""
        with self.lock:
            if not self.dirty:
                return None
            try:
                self.path.parent.mkdir(parents=True, exist_ok=True)
                temp = self.path.with_suffix(".json.new")
                temp.write_text(json.dumps(self.data, indent=2, sort_keys=True) + "\n")
                os.replace(temp, self.path)
            except OSError as error:
                print(f"fork-check: the verdict cache was not written to {self.path}: {error}",
                      file=sys.stderr)
                return None
            self.dirty = False
            return self.path


def append_timings(record: Record, entry: dict, checks: list[Check], check_wall: float,
                   jobs: int, peak: int, heavy_jobs: int | None = None) -> Path | None:
    """Append this run's per-check cost to the machine-local timeline.

    The record keeps a bounded history and only what a verdict needs. Improving the
    gate's runtime needs the opposite -- every run, every check, its command, how long
    it took and how much it printed -- so it goes to a JSONL file beside the record.
    A run that cannot write it still gets its verdict: this is instrumentation, and
    instrumentation that can fail a landing is worse than none.

    The check phase's own wall clock, the bound it ran under and the peak it reached are
    here because the sum alone cannot say whether a run was serial or concurrent: the same
    sum at two different wall clocks is the whole question this file answers.
    """
    recorded: list[dict] = []
    for check in checks:
        one = {
            "kind": check.kind,
            "label": check.label,
            "rc": check.rc,
            "seconds": round(check.seconds, 2),
            "started": check.started,
            "output_bytes": len(check.output or ""),
            "command": check.command_line(),
            # Why this check started when it did, split by the rule that held it. The
            # three causes the latency work names are `pool`, `writes` and `cheap`, and
            # they are disjoint. `exclusive` is a SUB-COUNT of `pool` (an exclusive check
            # waits for the pool to be empty), so a consumer summing the fields must not
            # add it again. A check that started immediately has zeros here, which is a
            # measurement rather than a missing field.
            "waited": {cause: round(seconds, 2) for cause, seconds in check.waited.items()},
        }
        if check.fingerprint_dir:
            # The materialized tree this step tested, so a later reader can count the
            # executions whose fingerprint had already passed (board item 396). The key is
            # this tree plus the command above plus the run's node version, which the
            # record keeps under `inputs.node`.
            one["fingerprint"] = materialized_fingerprint(check.fingerprint_dir)
        if check.cached:
            # A cached verdict is a reused full run, not an execution: the timeline keeps
            # the key so a reader counting executions can exclude it.
            one["cached"] = True
            one["cache_key"] = check.cache_key
        if check.cause:
            # Why this fork-quality step executed, so the cause breakdown is answerable
            # from the timeline alone (board item 407).
            one["cause"] = check.cause
        # One entry per check, because a check runs once: a failed check is diagnosed from
        # its own output, never re-run, so there is no second duration to keep apart.
        recorded.append(one)
    line = {
        "at": entry["at"],
        "commit": entry.get("commit"),
        "scope": entry.get("scope"),
        "outcome": entry.get("outcome"),
        "total_seconds": entry.get("seconds"),
        "check_seconds": round(sum(check.seconds for check in checks), 1),
        "check_wall_seconds": round(check_wall, 1),
        "jobs": jobs,
        "heavy_jobs": heavy_jobs,
        "peak_concurrency": peak,
        # The pass's change class, target, S1 floor and efficiency ratio, so the timeline
        # answers the design's D10 question without re-deriving it from the checks.
        "change_class": entry.get("change_class"),
        "change_class_target": entry.get("change_class_target"),
        "floor_seconds": entry.get("floor_seconds"),
        "efficiency": entry.get("efficiency"),
        "checks": recorded,
    }
    path = record.timings_path
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(line, sort_keys=True) + "\n")
    except OSError:
        return None
    return path


# A check's history is keyed by its kind and its label with the machine-local paths
# removed. The same step in another worktree names a different candidate checkout, and a
# key that moved with the path would never find its own past duration. The pattern is an
# absolute path token: a leading slash, then path components, and it stops at whitespace
# or punctuation that cannot be part of a path.
_ABSOLUTE_PATH = re.compile(r"(?<![\w./-])/(?:[\w.@+-]+/)*[\w.@+-]+")


def check_identity(kind: str, label: str) -> tuple[str, str]:
    """The stable name a check's historical duration is keyed by.

    The kind alone is not enough: `fork-quality` is one kind shared by every fork and
    every script, and ordering those by one duration would be a guess. The label alone is
    not enough either, because it names the candidate checkout, which is a different
    absolute path in every worktree. So the key is the kind plus the label with each
    absolute path replaced by `<path>`.
    """
    return (kind, _ABSOLUTE_PATH.sub("<path>", label))


def historical_durations(record: Record) -> dict[tuple[str, str], float]:
    """The most recent measured duration of each check, keyed by `check_identity`.

    The gate already writes every check's duration to the timings timeline, so the
    scheduler can order the heavy phase longest-first without a second measurement. The
    last entry for an identity wins, because the timeline is append-only and the newest
    run is the best estimate of what this machine will cost now. A missing or unreadable
    timeline is no history: the plan then falls back to the declared cost, which is what
    it used before this existed, and a run that cannot read its own instrumentation still
    gets a verdict.
    """
    durations: dict[tuple[str, str], float] = {}
    try:
        lines = record.timings_path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return durations
    for line in lines:
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        if not isinstance(entry, dict):
            continue
        for one in entry.get("checks") or []:
            if not isinstance(one, dict):
                continue
            # A cached verdict is a reused full run, not an execution: its `seconds` is the
            # time to report the hit, not what the step costs when it runs. Ordering by it
            # would put a cached suite at the back of the heavy phase, so it is skipped and
            # the last EXECUTED duration is the estimate.
            if one.get("cached"):
                continue
            kind, label, seconds = one.get("kind"), one.get("label"), one.get("seconds")
            if not isinstance(kind, str) or not isinstance(label, str):
                continue
            if not isinstance(seconds, (int, float)) or isinstance(seconds, bool) or seconds < 0:
                continue
            durations[check_identity(kind, label)] = float(seconds)
    return durations


def attach_history(plan: "Plan", record: Record) -> None:
    """Give each planned check its most recent measured duration, when the timeline has one.

    This is the only place the scheduler's ordering input is filled, so a plan built by a
    test or a preflight without a record simply has no history and keeps plan order.
    """
    durations = historical_durations(record)
    if not durations:
        return
    for check in plan.checks:
        check.history = durations.get(check_identity(check.kind, check.label))


def repo_ident(root: Path) -> str:
    """How this repository is named in an evidence record: its remote, else its root.

    A path is machine-local and a URL is not, so a URL is the better identity when the
    clone has one. A clone without a remote still records where the work happened
    rather than leaving the subject unnamed.
    """
    remote = subprocess.run(["git", "-C", str(root), "config", "--get", "remote.origin.url"],
                            capture_output=True, text=True)
    url = remote.stdout.strip()
    return url if remote.returncode == 0 and url else str(root)


def evidence_records(root: Path, entry: dict, checks: list[Check],
                     started_at: str | None = None) -> list[dict]:
    """This run's evidence: one record per check, then one for the run itself.

    The shared format (`docs/pi/pipeline-evidence.md`) carries the pipeline stage, the
    producer's own step, the subject, the step's start and end, its duration and its
    outcome. The gate's stage is `gate`; its step is the check's kind, so the velocity
    report can say which check ate a run without reading this file's prose.

    A run's outcome in the shared vocabulary is coarser than the gate's own word --
    verified, failed, refused, unproven, dirty -- which stays in `detail.outcome`, so
    nothing is lost and a consumer comparing components still has one axis.
    """
    run = f"gate-{entry.get('commit', 'unknown')[:12]}-{entry.get('at', 'unknown')}"
    subject_base = {"repository": repo_ident(root), "commit": entry.get("commit"),
                    "item": None, "run": run}
    records: list[dict] = []
    for check in checks:
        skip = sentinel_skip(check)
        quarantine = applied_quarantine(check)
        if skip is not None:
            outcome = "skipped"
        elif check.rc == 0:
            outcome = "ok"
        elif check.unanswered:
            # The shared axis has no "could not answer": nothing was judged, so it is
            # aborted, with the gate's own word in detail.outcome.
            outcome = "aborted"
        else:
            outcome = "failed"
        status = check_status(check)
        detail = {"label": check.label, "rc": check.rc,
                  "status": status, "outcome": status,
                  "command": check.command_line(),
                  "outputBytes": len(check.output or ""),
                  "warnings": list(check.warnings),
                  # The wait split travels with the shared record too, so a consumer that
                  # reads only the evidence file can attribute a start delay without the
                  # machine-local timings timeline. `exclusive` is a sub-count of `pool`,
                  # not a fourth addend.
                  "waited": {cause: round(seconds, 3)
                             for cause, seconds in check.waited.items()}}
        if check.cached:
            # The shared vocabulary has no separate outcome for a cached verdict (it is
            # `ok`), so the key travels in the detail: a consumer counting executions can
            # tell a reused verdict from a fresh pass.
            detail["cached"] = True
            detail["cacheKey"] = check.cache_key
        if check.cause:
            # Why this fork-quality step executed, in the design's cause vocabulary.
            detail["cause"] = check.cause
        # The quarantine travels with the record: the shared vocabulary has no separate
        # outcome for one (a quarantined case is `skipped`, never `ok`), so the declaration
        # it stands for is what a later reader needs to find it again.
        if quarantine is not None:
            detail["quarantine"] = {
                "item": quarantine.item,
                "classification": quarantine.classification,
                "expires": quarantine.expires.isoformat(),
                "path": quarantine.path,
            }
        records.append({
            "version": EVIDENCE_VERSION,
            "component": "gate",
            "stage": "gate",
            "step": check.kind,
            "subject": dict(subject_base),
            "startedAt": check.started or entry.get("at"),
            "endedAt": check.ended or entry.get("at"),
            "durationMs": int(round(check.seconds * 1000)),
            "outcome": outcome,
            "detail": detail,
        })
    verdict = entry.get("outcome", "unproven")
    # `unproven` is the same class as a refusal: the run reached no verdict about the
    # tree, so the shared axis says aborted rather than reporting a judgement it never
    # made. The gate's own word travels in detail.outcome.
    outcome = {"verified": "ok", "failed": "failed", "refused": "aborted",
               "unproven": "aborted", "dirty": "skipped"}.get(verdict, "aborted")
    check_seconds = round(sum(check.seconds for check in checks), 3)
    records.append({
        "version": EVIDENCE_VERSION,
        "component": "gate",
        "stage": "gate",
        "step": "run",
        "subject": dict(subject_base),
        "startedAt": started_at or entry.get("at"),
        "endedAt": entry.get("at"),
        "durationMs": int(round(float(entry.get("seconds") or 0) * 1000)),
        "outcome": outcome,
        "detail": {"outcome": verdict, "scope": entry.get("scope"),
                   "since": entry.get("since"), "tree": entry.get("tree"),
                   "changed": entry.get("changed"), "checks": len(checks),
                   "checkSeconds": check_seconds,
                   "planSeconds": round(float(entry.get("seconds") or 0) - check_seconds, 3),
                   "notPlanned": entry.get("not_planned"),
                   "couldNotAnswer": entry.get("could_not_answer"),
                   "changeClass": entry.get("change_class"),
                   "changeClassTarget": entry.get("change_class_target"),
                   "floorSeconds": entry.get("floor_seconds"),
                   "efficiency": entry.get("efficiency")},
    })
    return records


def append_evidence(record: Record, root: Path, entry: dict, checks: list[Check],
                    started_at: str | None = None) -> Path | None:
    """Append this run's shared evidence records, and never fail the run for it.

    The merge queue and the board write the same shape (`docs/pi/pipeline-evidence.md`),
    so one consumer reads all three. Writing it is instrumentation exactly like the
    timings timeline: a path that cannot be written reports once and changes nothing
    about the verdict, because evidence nobody could record must not cost a landing.
    """
    path = record.evidence_path
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("a", encoding="utf-8") as handle:
            for one in evidence_records(root, entry, checks, started_at):
                handle.write(json.dumps(one, sort_keys=True) + "\n")
    except OSError as error:
        print(f"fork-check: the run's evidence was not recorded in {path}: {error}", file=sys.stderr)
        return None
    return path


# ── change class, S1 floor and efficiency ───────────────────────────────────────
#
# The gate-latency design (D10, part 2) asks every pass to report its change class, its
# wall time, the S1 floor computed from that same run's per-check durations, the
# efficiency ratio, and the cause of every fork-quality execution. The class says which
# target applies; the floor says what the run would have cost with only the necessary
# serializations; the ratio says whether the gap is scheduling or suite growth.

# The gate-code target is the self-suite plus the cheap phase, NOT the design's original
# all-fork target plus the self-suite: board item 412 stopped a gate-only change from
# carrying every series, so the class no longer pays the fork checks. The self-suite
# measured 147-227s over the recorded timeline (`.git/yolo-fork-check/timings.jsonl`,
# 2026-11-01 to 2026-11-02), so 240s is the bound with the cheap phase's ~10s of room.
GATE_CODE_MEASURED_SECONDS = 240.0
CHANGE_CLASS_TARGETS = {
    "docs-only": 15.0,
    "driver-test-only": 60.0,
    "driver-behavior": 225.0,
    "one-fork-series": 225.0,
    "gate-code": GATE_CODE_MEASURED_SECONDS,
    "other": None,
}


class ChangeClass:
    """What kind of change a pass judged, and the wall-time target the design sets.

    `name` is one of the design's classes; `target` is its target in seconds, or None
    for a change the design does not target (a mixed change, or one outside the table).
    `detail` is the one-line reason the class was chosen, printed beside it so a reader
    can check the classification against the diff rather than trust a label.
    """

    def __init__(self, name: str, target: float | None, detail: str):
        self.name = name
        self.target = target
        self.detail = detail


def classify_change(root: Path, changed: list[str], series: list[Series]) -> ChangeClass:
    """Name the design's change class for this diff, and the target it is measured against.

    The classes are the design's D10 rows, in the order that decides a mixed change: a
    change to the code that materializes or judges a series moves every series, so it
    dominates; a gate-code change plans the self-suite and carries no series (board item
    412); a series or declaration change moves the forks it names; a driver-test-only
    change plans the driver's cases and no series; a docs-only change plans the cheap phase. A change that spans classes the
    table does not combine is `other`, with no target -- the report still prints its
    wall, floor and ratio, but claims no target it cannot defend.
    """
    paths = [path for path in changed if path]
    gate = [path for path in paths if path in GATE_CODE]
    mechanism = [path for path in paths if path in MECHANISM]
    driver = [path for path in paths if path in ("bin/pi-forks", "bin/pi-forks.list")]
    driver_tests = [path for path in paths
                    if path == PI_FORKS_TEST or path.startswith("tests/pi-forks/")]
    series_paths = [path for path in paths
                    if re.match(r"^yolo-packs/[^/]+/patches/", path)]
    declarations = [path for path in paths
                    if re.match(r"^yolo-packs/[^/]+/pack\.json$", path)]
    docs = [path for path in paths if is_document(root, path)]
    classified = set(gate) | set(mechanism) | set(driver) | set(driver_tests) \
        | set(series_paths) | set(declarations) | set(docs)
    other = [path for path in paths if path not in classified]

    if driver:
        return ChangeClass(
            "driver-behavior", CHANGE_CLASS_TARGETS["driver-behavior"],
            "the driver or its manifest changed, so every series moves")
    if mechanism:
        return ChangeClass(
            "driver-behavior", CHANGE_CLASS_TARGETS["driver-behavior"],
            "the code that judges a series changed, so every series moves")
    if gate:
        return ChangeClass(
            "gate-code", CHANGE_CLASS_TARGETS["gate-code"],
            "the gate's own selection or reporting code changed, so the self-suite is planned and no series is carried")
    if series_paths or declarations:
        forks: set[str] = set()
        for path in series_paths:
            match = re.match(r"^yolo-packs/[^/]+/patches/([^/]+)/", path)
            if match:
                forks.add(match.group(1))
        for path in declarations:
            pack = path.split("/")[1]
            for one in series:
                if one.pack == pack:
                    forks.add(one.fork)
        if len(forks) == 1:
            fork = next(iter(forks))
            target = 180.0 if fork == "pi-session-search" else CHANGE_CLASS_TARGETS["one-fork-series"]
            return ChangeClass(
                "one-fork-series", target,
                f"one fork's series or declaration changed ({fork})")
        return ChangeClass(
            "driver-behavior", CHANGE_CLASS_TARGETS["driver-behavior"],
            "more than one fork's series or declaration changed, so every series moves")
    if driver_tests:
        return ChangeClass(
            "driver-test-only", CHANGE_CLASS_TARGETS["driver-test-only"],
            "only the driver's own cases changed, so no series moves")
    if docs and not other:
        return ChangeClass(
            "docs-only", CHANGE_CLASS_TARGETS["docs-only"],
            "only Markdown changed, so only the cheap phase runs")
    return ChangeClass(
        "other", None,
        "the change spans classes the design does not combine, so it has no target")


def s1_floor(checks: list[Check]) -> float:
    """The wall time this run would take with only its necessary serializations.

    The design's S1 floor: every check keeps the duration it just measured, and the only
    serializations left are the ones the gate genuinely requires. Those are:

      * the `cheap_first` barrier -- every cheap check finishes before any heavy one
        starts, so the cheap phase costs the longest cheap check (they run together);
      * a declared write conflict -- two checks that write one path never overlap, so
        each connected group of writers costs the sum of its members' durations. This is
        what makes a fork's `patch-branch merge-check` and its two npm scripts one chain;
      * an `exclusive` check -- it runs alone, so its duration is additive to everything
        else rather than competing for a slot.

    Everything else is parallel, so the floor is the cheap phase plus the exclusive
    checks plus the longest writer chain. A check that did not run contributes nothing;
    a cached check contributes its probe time, because that is the duration this run
    actually paid. The floor is a floor under MEASURED durations, not a law: the
    durations themselves were measured under contention and may shrink in isolation.
    """
    live = [check for check in checks if check.not_run_reason is None]
    cheap = [check for check in live if check.cost < CHEAP_CHECK_SECONDS]
    heavy = [check for check in live if check.cost >= CHEAP_CHECK_SECONDS]
    cheap_phase = max((check.seconds for check in cheap), default=0.0)
    exclusive = [check for check in heavy if check.exclusive]
    exclusive_seconds = sum(check.seconds for check in exclusive)
    rest = [check for check in heavy if not check.exclusive]

    parent = list(range(len(rest)))

    def find(index: int) -> int:
        while parent[index] != index:
            parent[index] = parent[parent[index]]
            index = parent[index]
        return index

    def union(left: int, right: int) -> None:
        left_root, right_root = find(left), find(right)
        if left_root != right_root:
            parent[right_root] = left_root

    for left in range(len(rest)):
        for right in range(left + 1, len(rest)):
            if write_conflict(rest[left].writes, rest[right].writes) is not None:
                union(left, right)
    chains: dict[int, float] = {}
    for index, check in enumerate(rest):
        root = find(index)
        chains[root] = chains.get(root, 0.0) + check.seconds
    return cheap_phase + exclusive_seconds + max(chains.values(), default=0.0)


def efficiency_figures(wall: float, floor: float) -> tuple[float, float | None]:
    """The floor as the record keeps it (to 10 ms) and the ratio of wall to that floor.

    The ratio exists only when the KEPT floor is positive. A floor under 5 ms -- a run of
    in-process checks over a small tree -- is kept as 0.0, and a ratio against it would be
    noise against a wall clock kept to 0.1 s. The printed line and the record both use
    this, so the two never disagree about whether a pass had a ratio.
    """
    kept = round(floor, 2)
    return kept, (round(wall / kept, 2) if kept > 0 else None)


def efficiency_line(change: ChangeClass, wall: float, floor: float) -> str:
    """The one line every pass prints: class, wall, floor, ratio and target.

    The ratio is wall divided by the floor. A ratio persistently above about 1.25 on
    comparable passes says serialization has crept back; a ratio near 1.0 with a missed
    target says the suites themselves grew. A run with no checks has no floor and no
    ratio, and prints `-` rather than a division by zero.
    """
    _, ratio_value = efficiency_figures(wall, floor)
    ratio = f"{ratio_value:.2f}" if ratio_value is not None else "-"
    target = clock(change.target) if change.target is not None else "-"
    return (f"fork-check: efficiency -- class={change.name} wall={clock(wall)} "
            f"floor={clock(floor)} ratio={ratio} target={target} ({change.detail})")


def print_timings(checks: list[Check], total: float, check_wall: float, jobs: int, peak: int,
                  verbose: bool, heavy_jobs: int | None = None) -> None:
    """Name where a run's wall clock went, every time, without being asked.

    A gate that prints one number for eight minutes of work cannot be improved: the few
    steps that eat most of it are invisible in a total. So this prints the run's wall
    clock, the sum of the checks' own seconds, the concurrency actually achieved (that sum
    over the check phase's wall clock, with the peak and the bound), the part of the run
    that was not checks at all (plan, report, save), and the slowest checks.

    A share here is of the checks' own time, not of the wall clock: with checks running at
    the same time a share of the wall clock can exceed 100%, which reads as a mistake
    rather than as parallelism.
    """
    if not checks:
        return
    spent = sum(check.seconds for check in checks)
    overhead = max(0.0, total - check_wall)
    achieved = (spent / check_wall) if check_wall > 0 else 0.0
    ranked = sorted(checks, key=lambda check: -check.seconds)
    shown = ranked if verbose else ranked[:8]
    bound = f"{jobs} at a time"
    # The heavy cap is named only when it BINDS: a cap at or above the pool is the old flat
    # admission, so printing it there would report a bound that decides nothing.
    if heavy_jobs is not None and heavy_jobs < jobs:
        bound += f", {heavy_jobs} heavy"
    print(f"fork-check: timings -- {clock(total)} wall clock, {clock(spent)} in {len(checks)} check(s), "
          f"{achieved:.1f}x concurrent (peak {peak}, {bound}), "
          f"{clock(overhead)} planning and reporting")
    # The other half of the same question: not only how long each check ran, but why it
    # started when it did. A run whose checks all started at once has no wait to report.
    # `exclusive` and `heavy` are sub-counts of `pool`, so they are printed in
    # parentheses rather than added to the four disjoint causes.
    waits = {cause: sum(check.waited.get(cause, 0.0) for check in checks)
             for cause in ("pool", "writes", "cheap", "after", "exclusive", "heavy")}
    if any(waits.values()):
        pool = f"{clock(waits['pool'])} pool"
        named = []
        if waits["exclusive"]:
            named.append(f"{clock(waits['exclusive'])} exclusive")
        if waits["heavy"]:
            named.append(f"{clock(waits['heavy'])} heavy")
        if named:
            pool += f" ({', '.join(named)})"
        print(f"fork-check: waits -- {pool}, {clock(waits['writes'])} writes, "
              f"{clock(waits['cheap'])} cheap_first, {clock(waits['after'])} after")
    for check in shown:
        share = (check.seconds / spent * 100) if spent else 0.0
        # The summary's word is the report's own (`check_status`), and a non-zero exit
        # is always named beside it. A check that did not run, or that refused, must not
        # read as "ok" here while its rc says otherwise elsewhere in the same output:
        # a human reading the tail could not tell a stale red from a live one (item 194).
        status = check_status(check)
        if check.rc != 0:
            status = f"{status} rc={check.rc}"
        print(f"  {clock(check.seconds):>7}  {share:4.1f}%  {check.kind:<17} {status:<6} {check.label[:64]}")
    if not verbose and len(ranked) > len(shown):
        rest = sum(check.seconds for check in ranked[len(shown):])
        print(f"  {clock(rest):>7}  {(rest / spent * 100) if spent else 0:4.1f}%  "
              f"{len(ranked) - len(shown)} more check(s) -- pass --timings for every one")


def full_gate_cost(record: Record) -> tuple[float, int]:
    """What the whole gate last cost: every step's most recent duration, added up.

    A selective run cannot price the steps it did not run, so the figure it is measured
    against is assembled from the record instead: each step the record holds a duration
    for, at the last cost that step was measured at. The result is what running every
    step would have cost at the prices this machine has actually paid.

    It is an ESTIMATE, and a conservative one in both directions: a step no run has
    measured yet is absent from it (so it is low), and a step retired from the gate keeps
    its price until it ages out of the bounded history (so it is high). The count of steps
    priced is returned beside it, so a reader can see how much of the gate the figure
    covers rather than reading one number as if it were complete.
    """
    priced: dict[str, float] = {}
    for entry in record.data.get("runs") or []:
        for one in entry.get("checks") or []:
            label, seconds = one.get("label"), one.get("seconds")
            if label and isinstance(seconds, (int, float)):
                # Runs are appended oldest first, so a later entry's duration replaces an
                # earlier one: the estimate names what each step cost the last time it ran.
                priced[label] = float(seconds)
    return round(sum(priced.values()), 1), len(priced)


def print_plan(record: Record, plan: Plan, base: str | None, head: str) -> None:
    """One machine-readable line saying what this run planned, and what that saved.

    The gate is a selector: a step runs only when a path it watches changed, plus the
    fixed integrity set. That selection is invisible in a VERIFIED headline, and a
    landing path that reuses a verdict, or an operator asking what a gate run cost,
    needs it as data rather than as prose. So every run ends with one line of keys:

        fork-check: plan base=<sha|-> head=<sha> ran=<n> notrun=<n> seconds=<f>
                       full=<f> saved=<f> priced=<n> integrity=<n>
                       checks=<kind,...> required=<kind,...> reused=<kind,...>

    `base` is the commit the diff was taken from, so a caller that knows which commit its
    own change started at can check that this plan covered it (`-` means the run had no
    diff base and planned everything). `seconds` is this run's own check time; `full` and
    `saved` come from `full_gate_cost`, floored at this run's own cost, and `priced` is how
    many steps that figure prices. `integrity` counts the fixed set this run planned, which
    is `INTEGRITY_CHECKS`.

    `checks` names the kinds this run planned, and `required` names the kinds its diff
    SELECTED: the planned ones plus the ones the diff reached that this machine could not
    answer (`unavailable`, or a quarantined failure). A consumer that has to know whether
    a change's own paths were judged reads the pair rather than guessing from a count: a
    kind in `required` and not in `checks` is a step the diff needed and did not get, and
    a landing over that would put work on the trunk nothing examined (board item 299).
    `-` means the list is empty. Kinds are named, not labels, because a kind is what a
    check's watches are declared by and several steps can share one (`go-tool`, one per
    directory).

    `reused` names the kinds this run was told not to buy because the caller already
    holds their result (`REUSE_ENV`). They are neither planned nor required: the caller
    is responsible for them, and the selection line carries their watches so a reader can
    see what licensed the reuse. `-` means nothing was reused, which is every ordinary
    run and every gate that does not read the reuse list.

    `tier` is the caller's request: `full` when no `--tier` was given, else `landing` or
    `rolling`. `owed` names the kinds the diff selected that this tier DEFERS to the
    other one -- the third state beside ran and not-planned (invariant I5), and the debt
    a landing's coverage contract reads. `landing` is the landing-wall model: `s1_floor`
    over the landing-tier checks this run planned, so a full run prices what the landing
    tier would have cost from the durations it just measured. On a landing run it is the
    run's own floor; on a rolling run it is 0.0, because no landing-tier check ran.

    It is printed LAST on purpose: the board keeps the tail of a gate's output, and a
    line a consumer needs must survive that cut. The selection line (`print_selection`)
    is printed immediately above it for the same reason, so the two lines a consumer
    needs are the tail it keeps and this one stays last by contract.
    """
    spent = round(sum(check.seconds for check in plan.checks), 1)
    full, priced = full_gate_cost(record)
    # The figure is a floor under this run, not only a sum of past prices: a run's own
    # steps are prices the machine just paid, and a subset cannot cost more than the
    # whole gate it is part of. Without the floor a record holding two fast steps (two
    # 0.02s checks round the gate to 0.0) prices the gate below the documents step beside
    # them, and the line claims a saving against a gate cheaper than its own part.
    full = max(full, spent)
    saved = round(full - spent, 1) if full > 0 else 0.0
    if saved < 0:
        saved = 0.0
    ran = len(plan.checks)
    # Owed steps are counted in `owed=`, not here: a step the diff selected and this tier
    # deferred is not a step nothing selected, and folding the two would make a landing's
    # plan read as if its diff reached nothing.
    not_run = sum(1 for skip in plan.skips if not skip.planned and not skip.owed)
    integrity = sum(1 for check in plan.checks if check.kind in INTEGRITY_CHECKS)
    planned, required = plan_kinds(plan)
    reused = sorted({check.kind for check in plan.reused})
    owed = sorted({skip.kind for skip in plan.skips if skip.owed and skip.kind})
    landing_wall = round(s1_floor([check for check in plan.checks
                                   if check_tier(check.kind) == TIER_LANDING]), 1)
    print(f"fork-check: plan base={base or '-'} head={head} ran={ran} notrun={not_run} "
          f"seconds={spent:.1f} full={full:.1f} saved={saved:.1f} priced={priced} "
          f"integrity={integrity} checks={','.join(planned) or '-'} "
          f"required={','.join(required) or '-'} reused={','.join(reused) or '-'} "
          f"tier={plan.tier or 'full'} owed={','.join(owed) or '-'} "
          f"landing={landing_wall:.1f}")


def plan_kinds(plan: Plan) -> tuple[list[str], list[str]]:
    """The kinds a plan ran, and the kinds its diff selected -- `checks` and `required`.

    Everything planned is required, and a skip the plan marked `planned` is required too:
    that flag is what distinguishes a step the change gave no reason to run from one it
    did, which this machine or invocation could not answer. A quarantined failure is the
    same news -- the step ran and failed -- so it stays in `required` and never in
    `checks`, which is what lets a landing refuse a green over it. An OWED skip is
    deliberately not required: it is debt the other tier pays, named in the plan line's
    `owed=` rather than read as an unanswered requirement (invariant I5).

    Sorted and de-duplicated, because a kind is what a consumer compares and several
    steps of one kind can run (`go-tool` per directory, `storybook` per artifact).
    """
    planned = sorted({check.kind for check in plan.checks})
    required = set(planned)
    for skip in plan.skips:
        if skip.planned and skip.kind:
            required.add(skip.kind)
    return planned, sorted(required)


def write_failure_excerpts(record: Record, entry: dict, checks: list[Check]) -> str | None:
    """Write the failing checks' own output beside the record; return that path.

    The record's per-check entries are a verdict, not a transcript: kind, label, exit
    code and seconds say that a step failed and cannot say why. So a reader on another
    machine -- or on this one after the scrollback is gone -- could only ask whoever
    ran it to paste the output. This writes that output where the record already
    lives, in the common Git directory every worktree of this repository shares, and
    the caller points each failed check's entry at it.

    A run with nothing failing writes nothing and leaves an earlier failure's file alone:
    `just done` passing afterwards must not erase the excerpt a reader is diagnosing. The
    file's header names the run its bytes belong to, because older entries in the record
    carry the same path. Each failed section carries the check's command, its exit code and
    its last EXCERPT_LINES lines, and says how many earlier lines it dropped. The triage
    packet beside it is the diagnosis; this is the transcript it was read from.
    """
    failed = [check for check in checks
              if check.failed and not (check.preflight_refused or candidate_stale(check))]
    if not failed:
        return None
    path = record.excerpt_path
    lines = [
        "fork-check failure excerpt",
        f"run: {entry['at']}  commit {entry['commit']}  scope {entry['scope']} "
        f"outcome {entry['outcome']}",
        f"failing: {len(failed)} of {len(checks)} check(s)",
        "",
    ]
    for check in failed:
        output = check.output.strip().splitlines()
        kept = output[-EXCERPT_LINES:]
        dropped = len(output) - len(kept)
        lines.append(f"=== {check.kind} -- {check.label}")
        lines.append(f"status: {check_status(check)}")
        lines.append(f"command: {check.command_line()}")
        lines.append(f"exit code: {check.rc}")
        lines.append(f"duration: {clock(check.seconds)}")
        if dropped:
            lines.append(f"--- output: last {len(kept)} of {len(output)} line(s), "
                         f"{dropped} earlier line(s) dropped ---")
        else:
            lines.append(f"--- output: all {len(kept)} line(s) ---")
        lines.extend(kept or ["(the command printed nothing)"])
        lines.append("--- end output ---")
        lines.append("")
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(".txt.new")
    temp.write_text("\n".join(lines) + "\n")
    os.replace(temp, path)
    return str(path)


# ── the plan ────────────────────────────────────────────────────────────────────


# Checks that must run with nothing else of the gate beside them, keyed by kind, with the
# reason the report prints. A step is declared here for one of two recorded reasons, and the
# distinction is kept because they fail differently:
#
#   * ITS VERDICT IS DECIDED BY REAL TIME -- a freeze window measured in milliseconds, a
#     heartbeat's gaps. The difference between a failing run and a passing one is then the
#     machine it shared, and no amount of re-running the verdict can tell those two apart.
#   * ITS RECORDED VERDICT HAS DIFFERED BY CONTENTION ALONE -- a red inside the gate and a
#     green standalone on the same tip. That is the scheduling-declaration condition in
#     docs/design/agent-runtime.md §27.3 (D9), and serializing without that evidence would
#     be a disguised disable. The contention is removed from the run rather than re-tested
#     at the verdict.
#
# COST IS NOT AN ENTRY CRITERION. A suite that spawns real child processes is expensive but
# decided by counts, orderings and equalities over what the product recorded, and load only
# makes it slower -- so it belongs in the pool. A suite whose heartbeat is a count of
# recorded yields and whose freezes are asserted across a directly driven pulse is decided
# by what the product recorded, not by elapsed time. A step whose assertions are about behaviour alone
# and whose runs agree belongs in the pool: serializing it would spend the run's concurrency
# for nothing.
EXCLUSIVE_CHECKS = {
    # A key may be a kind, or `kind:label-prefix` to name one check of a shared kind.
    # The prefix names the UNIT SUITE, not the whole fork: the reason below is about that
    # suite's real-time case, and a fork's prepare step and typecheck have no such
    # assertion, so they belong in the pool (board item 405).
    "fork-quality:fork pi-subagents: npm run test:unit": (
        "its unit suite holds a real ~30 s timing case -- 'queued same-worktree creates "
        "start their timeout when the predecessor becomes active' passes alone in 28.3 s "
        "and times out beside the gate's other checks -- so it runs alone rather than "
        "being re-tested at the verdict"),
    # The gate's own audit suite spawns real gates in fixtures, so on 2026-10-29 it errored
    # with exit 2 (NOT VERIFIED, not a verdict) 6m52s into a landing pass while other gate
    # work was in flight, and passed standalone on the same tip -- leaving item 258's
    # candidate stranded with `gate-unverified` and no clock (board item 315). Its assertions
    # are about the gate's routing rather than elapsed time, so this is the second ground
    # above, not the first: the runs disagreed by contention alone.
    "self-suite": (
        "it spawns real gates in fixtures and on 2026-10-29 errored with exit 2 inside a "
        "landing pass while other gate work was in flight while passing standalone on the "
        "same tip, so it runs alone rather than stranding a candidate with gate-unverified "
        "(board item 315)"),
}


# The INTEGRITY SET: the checks that are a property of the tree rather than of the diff,
# so every run plans them whatever changed. A run is otherwise selective -- a step runs
# only when a path it watches changed -- and that selection is what makes a routine
# change cost seconds instead of minutes. The set is the floor under that selection: the
# repository's own invariants (every series still applies to the pack that declares it,
# no tracked .ts file has outgrown its budget) hold or fail independently of which files
# a diff touches, so a run that planned only the diff would stop noticing them entirely.
#
# This is a fixed set, not a rule: it is the same two checks on every run, it is named
# here so a reader (and a consumer of the report's `plan` line) can count it, and adding
# a member is a deliberate edit rather than a side effect of some check's watches.
# `plan_integrity` is the one place they are planned.
INTEGRITY_CHECKS = ("file-size",)

# The line a run ends with, immediately before the plan line, naming why every check ran
# and every step did not. The payload is one JSON object on one line; `print_selection`
# is the only writer and the board's `readGateSelection` is the reader.
SELECTION_PREFIX = "fork-check: selection "


def exclusive_declaration(kind: str, label: str = "") -> tuple[bool, str]:
    """Whether a check is declared exclusive, and the reason to print.

    A declaration names either the whole kind or one check of it, `kind:label-prefix`,
    because a shared kind can hold one real-timing suite and several fast ones.
    """
    for key, reason in EXCLUSIVE_CHECKS.items():
        if key == kind:
            return (True, reason)
        head, _, prefix = key.partition(":")
        if head == kind and prefix and label.startswith(prefix):
            return (True, reason)
    return (False, "")


# ── what a check's non-zero exit means ──────────────────────────────────────────
#
# A check that ran and failed is a VERDICT about the change unless its kind has a
# declared way to say "this machine could not answer". That is the whole rule, and
# CHECK_KIND_GROUP below is the whole of it: every kind the planner can produce is
# named there with the group it belongs to.
#
# The direction matters more than the entries. Until 2026-10-28 this was an allowlist
# with a string fallback at the end, so a kind nobody had added answered "not a
# verdict" by default, and the red was printed `ERR` and exited 2 -- "not a verdict" --
# for a tree the gate had in fact judged. The landing queue then had no check name to record, and
# keyed the flake on the exit status (`exit:2`), which no re-run can settle: board item
# 284. A kind this table does not name is now a VERDICT, the failing side, so the worst
# a missing entry can do is refuse a change loudly; `tests/fork-check.test.py` reads the
# planner's own kind literals and fails until every one of them is declared here.
KIND_VERDICT = "verdict"      # a non-zero exit judges the change
# A non-zero exit may be an inability to answer. The `COULD_NOT_ANSWER_CODES` statuses are
# read for every kind before the group is consulted, so what this group declares is that a
# non-zero exit outside that table is the tree's answer -- which is what KIND_VERDICT says
# too, and the group is kept so the declaration still reads as a decision about the kind.
KIND_UNABLE = "unable"
KIND_INVARIANT = "invariant"  # the driver names the invariant it refused
KIND_NAMED = "named"          # only a failing case the output names is a verdict

CHECK_KIND_GROUP = {
    # A config, render, build or suite assertion that fails is a verdict about the change:
    # the layer, the render it produces, the dispatch order,
    # the hang-analysis report, a landing's contract, a tool that no
    # longer builds, or a guard whose finding is real no longer holds.
    "file-size": KIND_VERDICT,
    "waybar": KIND_VERDICT,
    "storybook": KIND_VERDICT,
    "rank-order": KIND_VERDICT,
    "session-forensics": KIND_VERDICT,
    "pi-inspector": KIND_VERDICT,
    "session-budget": KIND_VERDICT,
    "tillr-backends": KIND_VERDICT,
    # A documented face that the binary does not answer, or a document that grew a flag
    # table, is this change's own red: the document is the claim and the binary is the
    # authority, so a disagreement is exactly what the step exists to refuse.
    "tillr-docs": KIND_VERDICT,
    # The loop adapter's own suite: a case that no longer describes the front end is this
    # change's own red, exactly like every other assertion above.
    "tillr-loop": KIND_VERDICT,
    # The pack's committed copy of the modules, and the front end's own build and suite: a
    # copy that diverges from its source, or a screen that does not compile, is this
    # change's own red rather than an inability to answer.
    "observatory-web": KIND_VERDICT,
    "meta-muse": KIND_VERDICT,
    "yolo-config": KIND_VERDICT,
    "matt-render": KIND_VERDICT,
    # The build and test-compile of a Go module, and its behavior suite, are two steps of
    # one module: a module that does not compile is this change's own red, and so is a
    # failing case in its suite. The build always lands; the substrate's suite also
    # lands, while the other behavior suites roll (docs/design/landing-velocity.md).
    "go-tool": KIND_VERDICT,
    "go-substrate": KIND_VERDICT,
    # The seat-profile encoding's own tests: a suite that fails is a verdict about the
    # change, like every other assertion above. It arrived from a lane that could not
    # see this table -- it planned the step and left the kind unclassified, and the audit
    # at the bottom of tests/fork-check.test.py is what refused it rather than letting a
    # red fall back to the table's default silently.
    # The durable steering timer's suite: a failing case is a verdict about the change, like every
    # other assertion above.
    "steering-timer": KIND_VERDICT,
    "seat-profiles": KIND_VERDICT,
    "pipeline-evidence": KIND_VERDICT,
    "extension-residue": KIND_VERDICT,
    # The active-steering skill's own text: a cadence written back into the prose, or a
    # lost ack-tool reference, is this change's red, not an inability to answer.
    "active-steering-skill": KIND_VERDICT,
    "pre-push-guard": KIND_VERDICT,
    "pi-forks": KIND_VERDICT,
    "hooks-install": KIND_VERDICT,
    "observatory-dev": KIND_VERDICT,
    # Compiler and test findings are failures; a missing runner or a timeout is an explicit
    # inability to verify, and must not approve the baseline either.
    "fork-quality": KIND_UNABLE,
    # The harness is expected to be red while the fork leaks exist, so a failing spec is the
    # verdict it exists to make. Exit 127 is the run script saying it could not resolve the
    # checkout or its vitest -- an inability to answer, not a pass.
    "session-liveness": KIND_UNABLE,
    # Building the checkout the liveness harness judges is a verdict about the tree: the
    # candidate is replayed from this tree's own series, so a series that does not build is
    # this change's answer, and the driver's exit 2 says so. A missing substrate is refused
    # at plan time instead, and a timeout or a missing runner is the COULD_NOT_ANSWER_CODES
    # table above rather than a red.
    "fork-construct": KIND_UNABLE,
    # These drivers refuse by naming the invariant they refused: an upstream that does not
    # take the patch series, a merge-tree conflict (INV-2), or a series that is not the
    # export (INV-3). A failure that names none of them is the driver saying it could not
    # answer -- a follow rule that does not resolve, a checkout missing what it reads -- and
    # is reported as "not verified" rather than as a verdict about the change.
    "series": KIND_INVARIANT,
    "forks-check": KIND_INVARIANT,
    "build": KIND_INVARIANT,
    "patch-branch-inv2": KIND_INVARIANT,
    "patch-branch-inv3": KIND_INVARIANT,
    # The gate's own audit suite is a flat script, so a failing assertion is named by the
    # interpreter's traceback rather than by a test framework. A suite that ran to
    # completion and named the assertion that failed is a verdict about the tree it ran
    # against; a crash, a timeout, an import error or a failure that names nothing is the
    # gate's own integrity being unknown, which stays NOT VERIFIED (board item 420). The
    # kind is not KIND_INVARIANT: the suite's transcript quotes the synthetic gates it
    # spawns, and a series-conflict phrase in one of those is not the bridge's refusal.
    "self-suite": KIND_NAMED,
}

# ── which tier a check belongs to ───────────────────────────────────────────────
#
# The landing tier is the proof a batch must carry on the exact tree the trunk will
# publish; the rolling tier is the proof that follows on a clock, over the range since
# the watermark. The design is docs/design/landing-velocity.md, and its membership rule
# (section 4.3) is the whole of the placement below: a kind lands when its failure would
# break the machinery other work stands on, when it cannot be attributed after the fact,
# or when it is cheap at the declared estimate (30s).
#
# This table is a DECLARATION, in the same shape and with the same fail-safe direction as
# CHECK_KIND_GROUP: every kind the planner can produce is named here, the audit in
# tests/fork-check.test.py refuses a kind it does not name, and an unnamed kind defaults
# to the LANDING tier -- the worst a missing entry can do is make a landing slower, never
# quieter. Moving a kind between tiers is a deliberate edit to one line of this table,
# ordered by the escape count in the design's section 11 (F2), never by a guess.
#
# Tier selection reads this declaration through `apply_tier`; an unsliced gate still
# plans both Go steps per module.
TIER_LANDING = "landing"
TIER_ROLLING = "rolling"
CHECK_TIER = {
    # The integrity set: properties of the tree, sub-second to seconds, and unconditional
    # today. Rule 1 and rule 3.
    "file-size": TIER_LANDING,
    # The trunk must always compile. A non-compiling trunk breaks every lane's next rebase
    # and the next gate itself, so the build and the test-compile are pre-publish, always
    # (invariant I2). The substrate suite also lands: its store, migration, and event-kind
    # conformance failures break the machinery other work stands on (rule 1).
    "go-substrate": TIER_LANDING,
    # The drain suite exceeds 30s alone and with three concurrent suites (measurements
    # beside GO_TOOL_DIRS). It rolls under section 4.3 rule 3, not a retired tool's cost.
    "go-tool": TIER_ROLLING,
    # Series and fork invariants: a patch series that does not apply breaks the next host
    # launch the moment it lands, on every host that selects the pack. Rule 1.
    "series": TIER_LANDING,
    "forks-check": TIER_LANDING,
    "build": TIER_LANDING,
    "fork-construct": TIER_LANDING,
    "patch-branch-inv2": TIER_LANDING,
    "patch-branch-inv3": TIER_LANDING,
    "pi-forks": TIER_LANDING,
    # Gate code defines what every later landing is judged by, so a batch carrying it is a
    # batch of one and carries the gate's own audit suite (invariant I3). The step is
    # conditional on the diff, as it already was; the tier is not.
    "self-suite": TIER_LANDING,
    # The heavy diff-scoped suites the design names as rolling: a behavior red is repaired
    # by revert with no data loss, and each is 20-100s or more. Rule 2 and rule 3 both say
    # no. `go-tool` is above, beside its build half.
    "pi-inspector": TIER_ROLLING,
    "session-liveness": TIER_ROLLING,
    "storybook": TIER_ROLLING,
    "fork-quality": TIER_ROLLING,
    # Every other diff-scoped kind is at or under the 30s line, so deferral buys nothing
    # worth the attribution it costs (rule 3). Two are borderline and the plan records the
    # call: `rank-order` (20s) lands under rule 3, and F2 is
    # the mechanism that moves one, with evidence.
    "waybar": TIER_LANDING,
    "rank-order": TIER_LANDING,
    "session-forensics": TIER_LANDING,
    "session-budget": TIER_LANDING,
    "tillr-backends": TIER_LANDING,
    "tillr-docs": TIER_LANDING,
    # Twelve shell-stub cases that finish in a couple of seconds, so deferral buys nothing.
    "tillr-loop": TIER_LANDING,
    # The pack's copy of the modules is a property of the tree, and the front end's build is
    # what the served binary embeds: both land.
    "observatory-web": TIER_LANDING,
    "meta-muse": TIER_LANDING,
    "yolo-config": TIER_LANDING,
    "matt-render": TIER_LANDING,
    "steering-timer": TIER_LANDING,
    "seat-profiles": TIER_LANDING,
    "pipeline-evidence": TIER_LANDING,
    "extension-residue": TIER_LANDING,
    "active-steering-skill": TIER_LANDING,
    "pre-push-guard": TIER_LANDING,
    "hooks-install": TIER_LANDING,
    "observatory-dev": TIER_LANDING,
}


def check_tier(kind: str) -> str:
    """The tier a kind belongs to. An unnamed kind is the landing tier, the safe side."""
    return CHECK_TIER.get(kind, TIER_LANDING)


# The phrase each invariant kind's driver prints when it is refusing the change rather
# than failing to answer. A kind in this table is a KIND_INVARIANT in CHECK_KIND_GROUP.
INVARIANT_PHRASES = {
    "patch-branch-inv2": "conflicts with",
    "patch-branch-inv3": "is not the export of",
    "series": "does not take the patch series",
    "forks-check": "does not take the patch series",
    "build": "does not take the patch series",
}

# The exit statuses that mean a runner did not answer at all: 124 its timeout, 125 and 126
# the shell's "cannot execute", 127 the command was not found, 137 it was killed. A step
# that returned one of these has not judged the change, so `Check.conflict` reads this
# table for EVERY kind rather than only the ones declared KIND_UNABLE. The direction is the
# one that matters: the number says the same thing everywhere, so a verdict kind that timed
# out (or whose runner is not installed) cannot silently become a red about the tree --
# it is a check that could not answer, which is exit 2 and never a pass.
COULD_NOT_ANSWER_CODES = (124, 125, 126, 127, 137)

# The ten-second line that decides the gate's first phase. A check estimated under this
# many seconds runs to completion before any suite or fork check starts, so a cheap red
# kills the run before the expensive work begins (board item 337). These are ESTIMATES
# declared per kind. Every planned kind must be declared here, so a new suite cannot slip
# into the cheap phase by being forgotten -- the audit in tests/fork-check.test.py refuses
# it. A kind the table does not name is treated as expensive.
CHEAP_CHECK_SECONDS = 10.0
# The separate concurrency budget for HEAVY checks (board item 404, the gate-latency
# design's D7 and F-9). `jobs` bounds the whole pool; this bounds only the checks above
# CHEAP_CHECK_SECONDS, so cheap checks stay wide while suites, fork-quality and
# pi-forks are admitted under a smaller cap. The value is selected by the measured sweep
# in docs/pi/gate-heavy-admission-2026-11-02.md, not guessed: the sweep runs a fixed
# representative batch at several caps and the deployed cap is the one that curve selects.
# `--heavy-jobs` overrides it for a measurement or a one-off run. A cap at or above `jobs`
# is the old flat admission, so the default is the only place the tuning lives.
HEAVY_JOBS_DEFAULT = 6
CHECK_COST_ESTIMATE = {
    "file-size": 0.0,
    "waybar": 4.0,
    "storybook": 20.0,
    "rank-order": 20.0,
    "session-forensics": 15.0,
    "pi-inspector": 60.0,
    "session-budget": 15.0,
    "tillr-backends": 8.0,
    # Eight `go build` invocations against the shared build cache (measured 2026-11-14: about
    # 5s warm, and the gate's `go-build` steps keep that cache warm), plus one `--help` per
    # documented face.
    "tillr-docs": 60.0,
    # Twelve shell-stub cases: measured under 4s cold on 2026-11-15, and each is a shim on
    # PATH answering a chosen exit code, so the price is Python startup plus a few dozen
    # subprocesses rather than any real work.
    "tillr-loop": 5.0,
    "observatory-web": 20.0,
    "meta-muse": 15.0,
    "seat-profiles": 8.0,
    "yolo-config": 5.0,
    "matt-render": 15.0,
    # The build and the test-compile of one module, split out of the old combined step.
    # The design prices it at 15-30s provisionally; 30.0 is the upper bound until the
    # timings timeline has a reading of its own (the landing gate's first run writes one,
    # and S1's report is where the distribution replaces this number).
    # Per-step costs come from the module table; a kind-only check uses the largest
    # declared member rather than inheriting a deleted monolith's price.
    "go-tool": max(cost for directory, cost in GO_TOOL_COST_ESTIMATE.items()
                   if directory not in GO_LANDING_SUITE_DIRS),
    "go-substrate": max(GO_TOOL_COST_ESTIMATE[directory]
                        for directory in GO_LANDING_SUITE_DIRS),
    "steering-timer": 2.0,
    "pipeline-evidence": 3.0,
    "extension-residue": 3.0,
    "active-steering-skill": 1.0,
    "pre-push-guard": 10.0,
    "pi-forks": 20.0,
    "hooks-install": 5.0,
    # 1.3-1.4s measured alone on 2026-11-16 with the hermetic cases for both recipes added.
    "observatory-dev": 1.4,
    "fork-quality": 60.0,
    "session-liveness": 600.0,
    "fork-construct": 120.0,
    "series": 10.0,
    "forks-check": 30.0,
    "build": 60.0,
    "patch-branch-inv2": 10.0,
    "patch-branch-inv3": 10.0,
    "self-suite": 600.0,
}
# A kind this table has not declared is treated as expensive: it waits behind the cheap
# phase rather than starting beside a suite. The audit makes that a temporary state.
DEFAULT_CHECK_COST = 60.0


def check_blamed(check: "Check") -> bool:
    """Whether this finished check is one the gate blames for a red verdict.

    A check that could not answer (`ERR`, exit 2) is not a verdict against the change, so
    it does not stop the run; a quarantined or skipped check never does. What does stop it
    is a verdict (`FAIL`), including the citation guard's `REFUSED` certification. The
    status is the same word the report prints, so a step the report calls a skip cannot
    stop the run as a failure.
    """
    return check.verdict


class Check:
    def __init__(self, kind: str, label: str, argv: list[str], why: str, timeout: int,
                 watches: tuple[str, ...] = (), needs: tuple[str, ...] = (),
                 remedy: str = "", env: dict[str, str] | None = None,
                 drop: tuple[str, ...] = (),
                 writes: tuple[str, ...] = (), after: tuple[str, ...] = (),
                 cost: float | None = None,
                 fingerprint_dir: str = "", cache_probe: list[str] | None = None,
                 cache_quality: list[str] | None = None, cache_install: str = "",
                 cache_provenance: dict | None = None,
                 selected_by: tuple[str, ...] | None = None, forced: str = "",
                 history: float | None = None, cause: str = "", fork: str = "",
                 reusable: bool = True):
        self.kind = kind
        self.label = label
        self.argv = argv
        # The fork a `fork-quality` step judges. Its candidate is verified against the
        # series before the step's own command runs, so a stale tree is refused in
        # seconds rather than reported as a test failure (board item 194).
        self.fork = fork
        # Set when the candidate preflight refused: the step never ran its command, so
        # its exit is a refusal whatever the driver's own status was.
        self.preflight_refused = False
        self.why = why
        self.timeout = timeout
        # Why this step executed, for the one kind whose executions the latency work
        # counts: a fork-quality step runs because the fork was carried, and the carry
        # has one primary cause -- `gate-code`, `driver`, `series`, `declaration` or
        # `drift`. Empty for every other kind. The report prints it beside the step and
        # the records keep it, so a pass can be attributed without re-reading its diff
        # (board item 407, the gate-latency design's F-12).
        self.cause = cause
        # The checkout whose HEAD tree is this check's materialized fingerprint, when the
        # check judges a tree a driver built rather than the repository's own working tree.
        # A fork-quality step runs in `.forks/candidate/<fork>`, materialized from the
        # series at its base, so that checkout's HEAD tree is the patched source tree the
        # step tested -- before the quality command writes build artifacts, which are
        # untracked and do not move HEAD. Recorded per run so a later reader can count how
        # often a step re-ran a tree that had already passed (board item 396).
        self.fingerprint_dir = fingerprint_dir
        # The content-fingerprint verdict cache's inputs, for a fork-quality step (board
        # item 398). `cache_probe` materializes the candidate without running the quality
        # command, so the tree can be fingerprinted before any npm script runs; a READER
        # whose prepare step already materialized the tree declares none, and the cache
        # fingerprints the prepared checkout directly (board item 405). `cache_quality` is
        # the exact command after `--`; `cache_install` is the dependency-install mode
        # (`deps` when the step installs, `build` when the build step did);
        # `cache_provenance` is the upstream and series identity recorded beside a verdict,
        # never part of its key. All None/empty for every other kind.
        self.cache_probe = cache_probe
        self.cache_quality = cache_quality
        self.cache_install = cache_install
        self.cache_provenance = cache_provenance
        # Whether a re-gate on a moved tip may reuse this check's result. True for a
        # check whose verdict is a function of the diff and the tree; False for one whose
        # verdict also depends on an input outside its watches -- the upstream a launch
        # would build, a content-fingerprint cache keyed by more than the tree. A check
        # that declares False is always re-run, which is the safe direction: a false
        # green is not a cost.
        self.reusable = reusable
        # Set when this check's verdict came from the cache rather than from a run: the
        # report says CACHED with the key, never a fresh pass, and the run entry keeps
        # both so a reader can tell a reused verdict from an executed one.
        self.cached = False
        self.cache_key = ""
        self.cache_hit: dict | None = None
        # The changed paths that selected this check, when the plan already knows them and
        # they are not simply the paths it watches. A fork step watches `.forks/<name>/`,
        # which is gitignored and never appears in a diff, so its selection is the series
        # path that moved; a step forced by `--all` or `--build` has no changed path at
        # all. `None` means "derive it from `watches` and the diff", which is right for
        # every step whose watches are the paths that select it.
        self.selected_by = None if selected_by is None else tuple(selected_by)
        # The flag that forced this check when no changed path did (`--all`, `--build`).
        # Empty means the diff selected it, or the fixed integrity set planned it.
        self.forced = forced
        # The paths and artifacts this check writes, absolute so two declarations cannot
        # disagree about what "the same path" means. Independent checks run at the same
        # time (run_checks), and this is the whole of the independence claim: two checks
        # whose write sets intersect never overlap, while a check that writes nothing runs
        # beside anything. A path here is a directory or a file; either way a declared
        # path and anything under it are the same resource.
        #
        # A shared tool cache is deliberately NOT declared: Go's build cache and uv's are
        # written by those tools' own concurrent-safe locking, so declaring them would
        # serialize every go-tool and document step for no safety. A checkout or an
        # artifact directory is declared because nothing inside it serializes a second
        # writer.
        self.writes = tuple(writes)
        # The labels of checks that must FINISH before this one starts. A write set says
        # two checks must not overlap; this says one must not start until another is done,
        # which a write set cannot express when the later check writes nothing. A fork's
        # quality scripts are the case it exists for: the prepare step is the only writer
        # of the candidate checkout, and a typecheck that writes nothing still must not
        # read the tree while that step is materializing it (board item 405). A label the
        # plan does not hold is satisfied, so a preflight that drops a check whose working
        # copy is absent does not leave its dependents waiting forever.
        self.after = tuple(after)
        # Environment additions this check's command needs beyond the gate's own. A
        # suite that imports the installed Pi cannot resolve it from this repository,
        # so the plan resolves it once and hands it over here.
        self.env = env or {}
        # Environment names this check's command must not inherit. The board's own
        # tests derive K from the landing batch limit, and the drain hands its
        # environment to every gate it runs, so a batch limit in the gate's
        # environment changes what the suite judges (item 587): K collapses to twice
        # the drain's batch and WIP cases fail for a reason no candidate caused. The Go
        # steps drop it and run under the suite's own defaults.
        self.drop = tuple(drop)
        # The repo-relative paths this check reads. A dirty path under one of them means
        # the check would judge the working tree rather than the commit, so it refuses.
        # They are also the paths that SELECT it: a changed path matching one of them is
        # the reason the plan ran it, and the run's record names that path.
        self.watches = watches
        # The checkout paths this check reads from the forks directory (`forks_root()`:
        # the checkout's own `.forks/`, or `PI_FORKS_ROOT`). A planned check whose need
        # is absent refuses the run before anything executes: a step that cannot read
        # its working copy reaches no verdict, and a green over steps that never ran
        # would advance the shared baseline past an unverified change. `remedy` is the
        # command that provides the missing checkout, named in the refusal.
        self.needs = needs
        self.remedy = remedy
        self.rc = 0
        self.output = ""
        # How long this check is expected to cost. The plan starts every check under
        # CHEAP_CHECK_SECONDS before any suite or fork check (board item 337). The price is
        # the kind's declaration, so the phase is deterministic across machines and across
        # runs rather than moving with one noisy measurement.
        self.cost = (CHECK_COST_ESTIMATE.get(kind, DEFAULT_CHECK_COST)
                     if cost is None else float(cost))
        # How long this check actually took the last time the gate ran it, read from the
        # timings timeline (`historical_durations`). The heavy phase is ordered
        # longest-first by this, because a long check started late is the run's critical
        # path: the measured pass started a 2m57s check after a 2m54s one had finished,
        # and that 2m54s wait was the whole difference between 6m29s and the 3m36s floor
        # (board item 401). None means the timeline has no measurement for this check, and
        # the scheduler falls back to `cost`, which is what it used before this existed.
        self.history = history
        # Set when the run stopped before this check could finish (or start): it ran
        # nothing, so it is named as not-run rather than read as a pass or a failure.
        self.not_run_reason: str | None = None
        # Whether the cancellation token stopped this check's process.
        self.was_cancelled = False
        # Lines the report prints beside the check whether it passed or failed: a
        # finding that is news without being a verdict (a file over the size budget
        # that this change did not add).
        self.warnings: list[str] = []
        self.seconds = 0.0
        # When this check started, ISO-8601 UTC. Recorded per run so the gate's
        # cost is attributable to a check after the fact: a landing's gate is the
        # long pole (480s measured), and one total per run cannot say which step
        # ate it.
        self.started = ""
        # When it finished, ISO-8601 UTC. A check's duration alone cannot place it in
        # time, and an evidence record needs both ends.
        self.ended = ""
        # The check's clock in nanoseconds, stamped where the check starts and ends. The
        # evidence record's `seconds` is rounded to a tenth, so an in-process guard (the
        # `.ts` budget) records `0.0s` and
        # the waterfall cannot draw it. These are the same two ends at full precision, and
        # they are what `--spans-out` writes; a minute on one axis needs both ends, and a
        # duration alone cannot tell two checks that ran beside each other from two that
        # serialized on a lock.
        self.start_ns = 0
        self.end_ns = 0
        # Why this check did not start when the run did, and for how long: the seconds it
        # spent waiting on the thread pool (`pool`), on the cheap_first barrier (`cheap`),
        # on a write conflict (`writes`), and on an unfinished prerequisite (`after`).
        # `run_checks` fills it as the scheduler decides; a check that never went through
        # the scheduler (a preflight cell) keeps the zeros. The four are disjoint and sum
        # to the total wait. `exclusive` and `heavy` are SUB-COUNTS of `pool`, not fifth
        # and sixth addends: an exclusive check waits for the pool to be empty, and a heavy
        # check waits for the heavy admission budget, and both are thread-pool waits. The
        # sub-counts name the mechanism so a reader can tell a declared barrier or the
        # tuned cap from saturation.
        self.waited = {"pool": 0.0, "cheap": 0.0, "exclusive": 0.0, "heavy": 0.0,
                       "writes": 0.0, "after": 0.0}
        # The inputs this check's own transcript declared elsewhere (a series whose
        # patch-branch stamp is the host's). A check may both pass and declare: the
        # series it could answer for were applied, and the ones it could not are named
        # in the report instead of being silently counted as coverage.
        self.declared: list[Declared] = []
        # Whether this check must run with nothing else of the gate beside it, and why. A
        # step whose assertions are about real time fails on what it shared the machine
        # with, so the plan removes the contention instead of re-testing the verdict. The
        # reason is printed beside the check, so a reader sees why a slow step held the
        # whole run rather than reading it as a scheduling accident.
        self.exclusive, self.exclusive_reason = exclusive_declaration(kind, label)

    def command_line(self) -> str:
        """The command this check ran, in a spelling a reader can re-run.

        The remaining in-process `.ts` size warning runs no command, so it names
        the gate's own guard rather than an empty line that reads as a broken record.
        """
        if self.argv:
            return shlex.join(self.argv)
        return f"fork-check {self.kind} guard (in process, no command line)"

    @property
    def failed(self) -> bool:
        """Whether this check did not pass. NOT whether it judged the change.

        A non-zero exit has two meanings and this property cannot tell them apart: the
        check ran and said no (`.verdict`), or it could not answer (`.unanswered`).
        Anything that reports a *verdict* asks those, never this; this answers the
        bookkeeping question "did this step need a diagnosis?" and is the one thing the
        triage packet and the excerpt are written for. There is no retry either: a check
        runs once, and what a failure means is read out of its own output afterwards
        (`triage_packet`).
        """
        return self.not_run_reason is None and self.rc != 0

    @property
    def unanswered(self) -> bool:
        """Whether this check reached no verdict: this machine could not answer.

        Two of `check_status`'s words are this class: `ERR` (it ran and could not
        answer) and `skip` (its own result named a case this machine cannot answer).
        Both leave the change unverified, and neither is a red. A refusal to certify
        (the citation guard's `REFUSED`) is a verdict, not this.
        """
        return check_status(self) in ("ERR", "skip")

    @property
    def verdict(self) -> bool:
        """Whether this check judged the change: it ran and said no."""
        return check_status(self) in ("FAIL", "REFUSED")

    @property
    def script(self) -> str:
        """The one Python script this check runs, or "" when it runs a framework or a command.

        A check that runs a test framework names its cases in the framework's own shape; a
        check that runs one script -- the gate's own audit suite -- names a failing assertion
        with the interpreter's traceback, and only that traceback is its own case.
        """
        if len(self.argv) >= 2 and self.argv[0] == sys.executable \
                and self.argv[1].endswith(".py"):
            return self.argv[1]
        return ""

    @property
    def own_cases(self) -> list[dict]:
        """The failing cases this check's own output names, as its own.

        A flat script -- the gate's own audit suite is one -- prints the transcripts of the
        gates it spawns, and a `not ok` line in one of those is the fixture's failure, not
        the suite's. So a script check's own case is the interpreter's traceback for the
        script it runs, and nothing else. Every other kind reads the whole transcript.
        """
        if self.kind == "self-suite":
            return [case for _, case in
                    _script_cases((self.output or "").splitlines(), self.script)]
        return failure_cases(self.output)

    @property
    def named_case(self) -> bool:
        """Whether this check's own output names a failing case.

        A kind that is a verdict only when it names one -- the gate's own audit suite --
        reads this: a suite that ran to completion and named the assertion that failed is a
        verdict about the tree, while a crash, a timeout or a failure that names nothing is
        the gate's own integrity being unknown, which stays "not verified".
        """
        return bool(self.own_cases)

    @property
    def conflict(self) -> bool:
        """A verdict of "this change does not hold up", as distinct from a failed run.

        Two things decide it, and the first is not per kind. A step that returned one of
        `COULD_NOT_ANSWER_CODES` -- it timed out, its runner is not installed, the shell
        could not execute it, it was killed -- did not judge the change, so it is never a
        verdict, whatever its kind: that is the machine's answer, and reading it as a red
        would blame a candidate's work for something no candidate did.

        Every other non-zero exit is decided by the kind's group in CHECK_KIND_GROUP, and
        that table is the rest of the rule: a verdict kind's exit judges the change, an
        invariant kind is a verdict only when its driver named the invariant it refused, a
        finding kind only when the checker named a finding, a named kind only when the
        output names a failing case, and a never kind is reported as "not verified"
        however it failed. A kind the table does not name is a verdict, because a red that
        quietly becomes "not a verdict" is a red nobody fixes.
        """
        if self.rc in COULD_NOT_ANSWER_CODES:
            return False
        group = CHECK_KIND_GROUP.get(self.kind, KIND_VERDICT)
        if group == KIND_NAMED:
            return self.named_case
        if group == KIND_UNABLE:
            # A fork-quality step whose candidate is not the series' tree refused to
            # judge: it never ran its command, so its exit is not a verdict about the
            # change (board item 194).
            if self.preflight_refused or candidate_stale(self):
                return False
            # pi-forks refuses linked-worktree callers before running candidate
            # preparation. This machine-local refusal exits 1, not the driver's
            # usual unable-to-answer code, but still judged no repository bytes.
            # Match both the driver argv and its own diagnostic: suite output
            # containing the same sentence must not suppress a real failure.
            if (len(self.argv) >= 2
                    and Path(self.argv[0]).name == "pi-forks"
                    and self.argv[1] == "candidate"
                    and "--deps" in self.argv and "--" not in self.argv
                    and any(line.startswith("pi-forks: refusing to run from a linked worktree:")
                            for line in self.output.splitlines())):
                return False
            return self.rc != 0
        if group == KIND_INVARIANT:
            return INVARIANT_PHRASES[self.kind] in self.output
        return self.rc != 0


class Skip:
    """A gate step the run did not put a verdict on, in the terms the verdict turns on.

    Three kinds are not the same news, and the report counts them apart. `planned` is
    False when the selected change gave the step no reason to run -- no path it watches
    changed -- so its silence says nothing about whether it still holds: drift from
    outside this repository (a yolo upgrade that changes a render) plans nothing here at
    all. `planned` is True when the change did give it a reason and this machine or
    invocation could not answer: no `node`, no `yolo`, no working copy under `.forks/`,
    `--no-build`. The third carries a `quarantine`: the step ran, it failed, and a
    recorded decision with a pile item and an expiry suppressed the failure, so it is
    neither a pass nor this machine's inability to answer.

    The report names the step in each entry -- one line for a step that did not run, and
    a quarantined entry's own block of lines, because its case, item and deadline are
    what a reader acts on. The headline counts entries, so its number is what a reader
    can count in the report itself.

    `owed` is the third state the landing-velocity design adds (invariant I5): the diff
    selected the step, and a caller-requested tier DEFERS it to the other tier rather
    than this run being unable to answer. It is deliberately not `planned`: `planned`
    means "required and unanswered", which a landing refuses, while an owed kind is
    covered-by-debt and must not read as unverified. A skip is owed or planned or
    neither, never two of them.
    """

    def __init__(self, label: str, why: str, planned: bool = False,
                 quarantine: "Quarantine | None" = None, kind: str = "",
                 owed: bool = False):
        self.label = label
        self.why = why
        self.planned = planned
        # The step is deferred to the other tier, not unanswerable here. See the class
        # docstring: this is the state a landing's plan-coverage contract reads as debt.
        self.owed = owed
        # The step's kind, for a skip that stands for a step the diff DID select: the
        # plan line names those beside the checks that ran, so a consumer can tell a
        # check that was skipped because nothing it watches changed from one the diff
        # reached and this run could not answer (`unavailable`, `quarantine`).
        self.kind = kind
        # The quarantine declaration this skip stands for, when it stands for one. A
        # quarantined case is counted and named apart from a skip: it did run, and its
        # failure is suppressed by a recorded decision rather than by this machine's
        # inability to answer (agent-runtime section 27.6 D6).
        self.quarantine = quarantine


class Declared:
    """An input this run cannot check here, with the command that checks it elsewhere.

    A gate verdict is local to the machine that ran it. The patch-branch stamp (the
    host writes the record `yolo pack series check` resolves a `patch_branch` from) and
    the host's own yolo build are answers that live on another machine, and neither says
    anything about whether the change holds. So each is DECLARED -- named with the reason
    it cannot be read here and the command that reads it there -- rather than left to
    veto the run, because a machine-local input that blocked a verdict would make every
    jail landing wait for a host run. What CAN be checked must still pass: a declaration
    never covers a failing check and never stands in for a step that did not run.
    """

    def __init__(self, label: str, reason: str, command: str):
        self.label = label
        self.reason = reason
        self.command = command

    def line(self) -> str:
        """The report's one line for this input, in the report's own words."""
        return f"{DECLARED_ELSEWHERE} -- {self.label}: {self.reason}; checks with {self.command}"

    def entry(self) -> dict:
        """The record's copy: what a later reader needs, and nothing to re-derive."""
        return {"label": self.label, "reason": self.reason, "command": self.command}


class Plan:
    def __init__(self):
        self.checks: list[Check] = []
        self.skips: list[Skip] = []
        # Checks whose results the caller already holds, removed from `checks` by
        # `apply_reuse`. They are not skips: a skip is a step this run could not answer,
        # and a reused check is one it was told not to ask. The plan line names them in
        # `reused`, and the selection line carries their watches, so a reader can see
        # what the run did not buy and why.
        self.reused: list[Check] = []
        # Inputs this run cannot check here, each with the command that checks it
        # elsewhere. They are news, never a verdict, so nothing about the outcome reads
        # them; the report prints one line each and the run entry keeps them.
        self.declared: list[Declared] = []
        self.refs: dict[str, dict[str, dict[str, str]]] = {}
        self.unreachable: dict[str, str] = {}
        # The upstream a launch would build, per fork, as `bin/pi-forks patch-branch
        # target --fetch` resolves it -- the ref every verdict here names -- and why any
        # fork could not be resolved. Empty when the run audits no upstream.
        self.targets: dict[str, dict[str, str]] = {}
        self.target_problems: dict[str, str] = {}
        # The installed yolo build the plan was made against, and why it is news: both are
        # the report's, so a reader can see which build a verdict rests on. ADVISORY: the
        # reason is printed and plans nothing.
        self.yolo: YoloBuild | None = None
        self.yolo_moved: str | None = None
        self.yolo_audited = False
        # The installed node version the plan was made against, and why it is news: both
        # are the report's, so a reader can see which version a verdict rests on. ADVISORY:
        # the reason is printed and plans nothing.
        self.node: NodeVersion | None = None
        self.node_moved: str | None = None
        self.node_audited = False
        # The caller-requested tier this plan was built for: None is the full gate, and
        # `landing` / `rolling` are the two halves. It travels on the plan so the plan
        # line, the selection line and the report all read one value rather than each
        # being handed it separately.
        self.tier: str | None = None

    def not_planned(self, label: str, why: str, kind: str = "") -> None:
        """A step the selected change gave no reason to run."""
        self.skips.append(Skip(label, why, kind=kind))

    def unavailable(self, label: str, why: str, kind: str = "") -> None:
        """A step the change gave a reason to run that this machine could not answer."""
        self.skips.append(Skip(label, why, planned=True, kind=kind))

    def quarantine(self, label: str, why: str, declared: "Quarantine", kind: str = "") -> None:
        """A step that ran and failed on a case a recorded quarantine suppresses."""
        self.skips.append(Skip(label, why, planned=True, quarantine=declared, kind=kind))

    def owed(self, label: str, why: str, kind: str = "") -> None:
        """A step the diff selected that a caller-requested tier defers to the other tier.

        Not `unavailable`: nothing about this machine or invocation could not answer. The
        step is debt the other tier pays, and the plan line names it in `owed=` so a
        landing's coverage contract can tell deferred proof from a step nothing selected.
        """
        self.skips.append(Skip(label, why, planned=False, kind=kind, owed=True))

    def declare(self, label: str, reason: str, command: str) -> None:
        """Record an input this run cannot check here, with the command that can."""
        self.declared.append(Declared(label, reason, command))


# ── running the plan: independent checks at the same time ───────────────────────


def write_conflict(left: tuple[str, ...], right: tuple[str, ...]) -> str | None:
    """The one path two checks both write, or None when their write sets are disjoint.

    Compared by path COMPONENT, not by string prefix: `.forks/pi` and `.forks/pi-subagents`
    are two different checkouts that merely share a prefix, and serializing every fork's
    build behind every other fork's would cost the concurrency this exists to gain. A
    declared path is a file or a directory, and a directory covers what is under it, so
    `durable/roster-storybook` and `durable/roster-storybook/index.html` are the same
    resource.
    """
    for one in left:
        for other in right:
            if one == other or _within(one, other) or _within(other, one):
                return one if len(one) <= len(other) else other
    return None


def _within(path: str, parent: str) -> bool:
    """Is `path` under the directory `parent`? Component-wise, and never the reverse."""
    child, holder = Path(path).parts, Path(parent).parts
    return len(child) > len(holder) and child[:len(holder)] == holder


def run_checks(checks: list[Check], jobs: int,
               run_one: Callable[[int], None],
               on_start: Callable[[int], None] | None = None,
               on_complete: Callable[[int], None] | None = None,
               honor_exclusive: bool = True,
               cheap_first: bool = False,
               blame: Callable[[int], bool] | None = None,
               terminate: Callable[[], None] | None = None,
               on_not_run: Callable[[int, int], None] | None = None,
               heavy_jobs: int | None = None,
               ) -> tuple[float, int, int | None]:
    """Run every check, at most `jobs` at a time, never two writers of one path at once.

    Returns the wall clock the checks took, the peak number that ran at the same time, and
    the plan index of the check that stopped the run (or None when it ran to the end).

    `heavy_jobs` is the separate admission budget for HEAVY checks -- the ones declared
    above `CHEAP_CHECK_SECONDS` -- and defaults to `jobs`, which is the old flat
    admission. Cheap checks are bounded only by `jobs`, so they stay wide; heavy checks
    are bounded by `heavy_jobs` as well, so a suite, a fork-quality step or `pi-forks`
    cannot fill the pool beside eleven siblings. This is the gate-latency design's D7/F-9: `jobs` counts
    CHECKS, not load, and a check spawns a subprocess tree that parallelizes internally,
    so the cap is the only bound on nested parallelism. The cap changes ADMISSION only:
    the same checks are planned and the same checks run, whatever the cap.

    The rules, in the order they decide anything:

      * with `cheap_first`, every check estimated under `CHEAP_CHECK_SECONDS` runs to
        completion (or is stopped by a red below) before any suite or fork check starts:
        no check with `cost >= CHEAP_CHECK_SECONDS` may start while a cheap check is still
        pending or running. A cheap red therefore kills the run before the expensive work
        begins (board item 337);
      * when `blame(check)` is true for a completed check, the run stops at once: no
        pending check starts, every running check is handed to `terminate`, and each
        pending check is named through `on_not_run(index, blamed_index)`. A check already
        running is not waited out: the caller's `terminate` kills its process group (see
        `Cancellation`), so its worker returns and the pass ends within seconds;
      * a check may start only when the bound has room, only when no RUNNING check
        writes a path it writes too (`write_conflict`), and only when every check it
        names in `after` has FINISHED. The write set keeps two writers apart; `after`
        orders a reader behind a writer it shares no path with, which is how a fork's
        quality scripts wait for the one prepare step that materializes their checkout;
      * a HEAVY check may start only when fewer than `heavy_jobs` heavy checks are
        running, even when the pool has room. The heavy budget is a pool bound, so its
        wait is charged to `pool` and named by the `heavy` sub-count;
      * a check the plan DECLARED EXCLUSIVE (`check.exclusive`) starts only with nothing
        else running, and nothing else starts until it finishes: its assertions are about
        real time, so sharing the machine is the failure mode, and the plan removes it
        instead of leaving the verdict to be re-tested afterwards. `honor_exclusive` is
        the preflight's override: it runs a check beside everything on purpose, because
        whether the check survives that is the question a preflight asks -- a check that
        cannot must DECLARE it (PREFLIGHT_DECLARATIONS) rather than have the contention
        removed for it; the write-set rule below is not overridable, because two writers
        of one path is a corrupted fixture rather than a condition;
      * the cheap phase is considered in plan order, and the heavy phase is considered
        longest-first by each check's most recent measured duration (`check.history`),
        falling back to its declared `cost` when the timeline has no measurement. A long
        check started late is the run's critical path, so the scheduler starts the longest
        one it can start now; plan order breaks ties, so a run with no history reads
        exactly as it did before (board item 401). This is order, not selection: the same
        checks run, and the write-set and cheap_first rules below still decide what may
        start beside what. A serial run (`jobs == 1`) keeps plan order, because one slot
        makes the order irrelevant to the wall clock and the routing cases read that order;
        a writer whose path is busy does not block later readers, which is what keeps one
        slow storybook from idling the pool;
      * every completion is reported through `on_complete` in this thread, so the caller
        can print in plan order rather than in completion order. A concurrent run then
        reads exactly like a serial one: same lines, same order, same verdict.

    `run_one` is expected to record its own result on the check; it runs in a worker thread
    and must not print, because output from two workers would interleave.

    Every check's wait is recorded on the check itself (`check.waited`), split by the rule
    that held it: the thread pool, the cheap_first barrier, a write conflict, and an
    unfinished prerequisite (`after`). The four are disjoint and sum to the total wait. An
    exclusive check waits for the pool to be EMPTY, and a heavy check waits for the heavy
    admission budget; both are thread-pool waits, so both are charged to `pool`, and
    `exclusive` and `heavy` are sub-counts of `pool` that name the mechanism, never extra
    addends. The split is the
    scheduler's own decision, not a guess: between two completions the rule in force is
    the one that was in force when the interval began, and a check that starts is charged
    up to the moment it was submitted. A check that never starts (the run stopped on a
    red) keeps the wait it had accumulated, and its `not_run_reason` says why it never
    ran. This is what makes a start delay attributable: a check that began after a long
    sibling finished is distinguishable from one that was simply slow.
    """
    if jobs < 1:
        raise ValueError(f"jobs must be at least 1, not {jobs}")
    if heavy_jobs is None:
        heavy_jobs = jobs
    if heavy_jobs < 1:
        raise ValueError(f"heavy_jobs must be at least 1, not {heavy_jobs}")
    pending = list(range(len(checks)))
    running: dict[concurrent.futures.Future, int] = {}
    cheap = {index for index, check in enumerate(checks)
             if cheap_first and check.cost < CHEAP_CHECK_SECONDS}
    # Each check's declared prerequisites, as plan positions. A label the plan does not
    # hold is satisfied: the preflight drops a check whose working copy is absent, and a
    # dependent step must not wait forever on a step that will never run.
    position = {check.label: index for index, check in enumerate(checks)}
    waits = {index: {position[label] for label in check.after if label in position}
             for index, check in enumerate(checks)}
    # The heavy set is independent of `cheap_first`: the admission budget applies to a
    # heavy check whether or not the cheap barrier is in force (a preflight cell runs
    # without the barrier and still gets the cap). It is STRICTLY above the cheap line,
    # not at it: a step declared at exactly CHEAP_CHECK_SECONDS (a patch-branch question,
    # a series check) is a short step that waits for the cheap phase but does not spawn
    # the subprocess tree the cap exists to bound, so it stays wide. The floor and the
    # barrier keep their own `>=`/`<` reading of the same line; the three questions are
    # different, and the budget's is "does this check fan out?".
    heavy = {index for index, check in enumerate(checks)
             if check.cost > CHEAP_CHECK_SECONDS}
    halted: int | None = None
    peak = 0
    started = time.monotonic()
    # The wait each pending check has accumulated, by the rule that held it, and the rule
    # in force at the last decision. `last_seen` is when that decision was made, so the
    # interval to the next one is charged to the rule that was in force across it.
    wait = {index: {"pool": 0.0, "cheap": 0.0, "exclusive": 0.0, "heavy": 0.0,
                    "writes": 0.0, "after": 0.0}
            for index in pending}
    blocked_by: dict[int, str | None] = {index: None for index in pending}
    last_seen = {index: started for index in pending}

    def cheap_left() -> bool:
        return any(index in cheap for index in pending) or any(
            index in cheap for index in running.values())

    def heavy_running() -> int:
        """How many HEAVY checks are in flight, for the separate admission budget."""
        return sum(1 for other in running.values() if other in heavy)

    def start_priority(index: int) -> tuple[int, float, int]:
        """Which pending check the scan considers first.

        The cheap phase keeps plan order: every cheap check runs before any suite or fork
        check, and their order among themselves changes nothing. The heavy phase is
        longest-first by the check's most recent measured duration (its declared cost when
        the timeline has none), because a long check started late is the run's critical
        path: the measured pass started a 2m57s check after a 2m54s one had finished, and
        that 2m54s wait was the whole difference between 6m29s and the 3m36s floor (board
        item 401). Plan order breaks ties, so a run with no history reads exactly as it
        did before. This is ORDER, not selection: the same checks run, and every declared
        write set, the cheap_first barrier and the heavy admission budget still decide
        what may start beside what.
        """
        check = checks[index]
        # A serial run has one slot, so the order cannot change its wall clock: it keeps
        # plan order, which is what the routing cases read. Longest-first is a concurrent
        # rule, and it is applied only where a slot is a choice.
        if jobs == 1 or (cheap_first and index in cheap):
            return (0, 0.0, index)
        estimate = check.history if check.history is not None else check.cost
        return (1, -estimate, index)

    with concurrent.futures.ThreadPoolExecutor(max_workers=jobs,
                                               thread_name_prefix="fork-check") as pool:
        while pending or running:
            # Charge the interval since the last decision to the rule that held each
            # pending check across it. A check that was not yet decided (the first pass)
            # has no rule to charge, and the interval is the microseconds before the scan.
            now = time.monotonic()
            for index in pending:
                reason = blocked_by[index]
                if reason is not None:
                    elapsed = now - last_seen[index]
                    # An exclusive check waits for the pool to be EMPTY, and a heavy
                    # check waits for the heavy admission budget; both are thread-pool
                    # waits like saturation is, so both are charged to `pool`, and
                    # `exclusive` and `heavy` are sub-counts that name the mechanism. The
                    # four causes the latency work names -- pool, writes, cheap, after --
                    # stay disjoint and sum to the total wait; the sub-counts are subsets
                    # of `pool`, never extra addends.
                    wait[index]["pool" if reason in ("exclusive", "heavy") else reason] += elapsed
                    if reason == "exclusive":
                        wait[index]["exclusive"] += elapsed
                    elif reason == "heavy":
                        wait[index]["heavy"] += elapsed
                last_seen[index] = now
            # Start what may start, in `start_priority` order: the cheap phase in plan
            # order, then the heavy phase longest-first. The scan does not stop at the
            # first blocked check: a reader behind a busy writer is independent of it.
            if halted is None:
                decided: set[int] = set()
                for index in sorted(pending, key=start_priority):
                    if index not in pending:
                        continue
                    check = checks[index]
                    # The binding constraint, not the code's evaluation order: an
                    # exclusive sibling or the cheap_first barrier stops a check even when
                    # the pool has room, so it is the reason the check is not running.
                    if honor_exclusive and (check.exclusive and running or any(
                            checks[other].exclusive for other in running.values())):
                        # Nothing else may be in flight, and nothing else may start beside
                        # it: the run's whole point here is to remove the contention.
                        blocked_by[index] = "exclusive"
                        decided.add(index)
                        continue
                    if cheap_first and index not in cheap and cheap_left():
                        # The cheap phase is not finished: no suite or fork check starts
                        # until it is, so a cheap failure can kill it before it begins.
                        blocked_by[index] = "cheap"
                        decided.add(index)
                        continue
                    if any(other in pending or other in running.values()
                           for other in waits[index]):
                        # A prerequisite has not finished. Checked before the write sets,
                        # because the reason is an ordering, not a shared path: a reader
                        # that writes nothing still must not read a tree a prepare step is
                        # materializing (board item 405).
                        blocked_by[index] = "after"
                        decided.add(index)
                        continue
                    if index in heavy and heavy_running() >= heavy_jobs:
                        # The heavy budget is full: a heavy check waits for a heavy slot
                        # even when the pool has room. This is the weighted admission the
                        # design's D7 asks for -- cheap checks stay wide, heavy checks are
                        # bounded by a separately tuned cap -- and it is a pool bound, so
                        # the wait is charged to `pool` and named by `heavy`.
                        blocked_by[index] = "heavy"
                        decided.add(index)
                        continue
                    if len(running) >= jobs:
                        # The pool is full. Every check this scan has not already decided
                        # waits on it, unless a stronger rule holds it: an exclusive check
                        # (or a check beside a running exclusive one) waits for the pool to
                        # be empty, and a non-cheap check waits for the cheap barrier. The
                        # binding constraint is the reason, not the full pool.
                        barrier = cheap_first and cheap_left()
                        exclusive_running = honor_exclusive and any(
                            checks[other].exclusive for other in running.values())
                        heavy_full = heavy_running() >= heavy_jobs
                        for rest in pending:
                            if rest in decided:
                                continue
                            rest_check = checks[rest]
                            if honor_exclusive and (
                                    exclusive_running
                                    or (rest_check.exclusive and running)):
                                blocked_by[rest] = "exclusive"
                            elif barrier and rest not in cheap:
                                blocked_by[rest] = "cheap"
                            elif rest in heavy and heavy_full:
                                blocked_by[rest] = "heavy"
                            else:
                                blocked_by[rest] = "pool"
                        break
                    busy = None
                    for other in running.values():
                        busy = write_conflict(check.writes, checks[other].writes)
                        if busy is not None:
                            break
                    if busy is not None:
                        blocked_by[index] = "writes"
                        decided.add(index)
                        continue
                    pending.remove(index)
                    blocked_by.pop(index, None)
                    last_seen.pop(index, None)
                    # The wait is complete at submission: the worker may start a moment
                    # later, but that is thread scheduling, not a rule of this scheduler.
                    check.waited = dict(wait[index])
                    running[pool.submit(run_one, index)] = index
                    if on_start is not None:
                        on_start(index)
            peak = max(peak, len(running))
            if not running:
                if halted is None and pending:
                    # Unreachable by construction -- with nothing running, the first
                    # pending check conflicts with nothing -- and a hard stop is better
                    # than a spin.
                    raise RuntimeError(f"{len(pending)} check(s) cannot start: nothing is running")
                break
            done, _ = concurrent.futures.wait(
                running, return_when=concurrent.futures.FIRST_COMPLETED)
            for future in done:
                index = running.pop(future)
                # Re-raise a worker's own exception in this thread: a check that crashed
                # must stop the run rather than read as a check that passed.
                future.result()
                if on_complete is not None:
                    on_complete(index)
                if halted is None and blame is not None and blame(checks[index]):
                    halted = index
                    # Stop the work already in flight first, so nothing it writes can
                    # land after the report says the run is over.
                    if terminate is not None and running:
                        terminate()
                    for waiting in list(pending):
                        pending.remove(waiting)
                        # The wait it had accumulated is still real: it was pending until
                        # the run stopped, and the reason it never started is the red.
                        checks[waiting].waited = dict(wait[waiting])
                        if on_not_run is not None:
                            on_not_run(waiting, halted)
    return time.monotonic() - started, peak, halted


def execute_check(check: Check, root: Path, diff_base: str | None,
                  cancel: "Cancellation | None" = None,
                  cache: VerdictCache | None = None,
                  runtime: RuntimeFingerprint | None = None) -> None:
    """Run one planned check in place: its one run, its transcript and its end.

    The run goes through `execute_attempt`, and it happens exactly once: a failure is
    triaged from this output afterwards, never re-run, so a concurrent worker that recorded
    only its own result is recording all there is. `read_series_transcript` is applied
    here, in the check's own worker, because it reads and rewrites that check's result and
    nothing else.
    """
    # The span's two ends, at nanosecond precision, read where the check itself starts and
    # finishes rather than from the report's rounded `seconds`. A worker stamps its own
    # check, so two checks that overlapped have two real spans on one axis.
    check.start_ns = time.time_ns()
    execute_attempt(root, check, diff_base, cancel=cancel, cache=cache, runtime=runtime)
    # A series whose patch-branch stamp is the host's is declared elsewhere here, not failed
    # and not skipped: the pack's other series still decide the check, and the unanswered
    # one is named in the report.
    read_series_transcript(check)
    # A stopped process is not a check that ran: the report names it not-run with the
    # reason the run stopped, and the record keeps that status rather than a kill signal.
    # An in-process guard cannot be killed, so it finishes; the same mark keeps its result
    # out of the verdict -- it was decided after the run had already stopped.
    if cancel is not None and cancel.cancelled:
        check.was_cancelled = True
        check.rc = 0
    if check.was_cancelled:
        check.not_run_reason = "terminated when the run stopped on a red check"
    # The check's own end, which the evidence record's `endedAt` reads, and the span's end
    # at the same millisecond boundary the record names.
    check.ended = now_iso()
    check.end_ns = time.time_ns()


def run_plan_checks(plan: Plan, root: Path, diff_base: str | None,
                    jobs: int, cache: VerdictCache | None = None,
                    runtime: RuntimeFingerprint | None = None,
                    heavy_jobs: int | None = None) -> tuple[float, int, int | None]:
    """Run the plan's checks concurrently where they are independent; report in plan order.

    One line per check, in plan order, on stderr as each one finishes: a run of eight
    concurrent checks and a run of the same eight one after another print the same lines in
    the same order, so a reader comparing two runs is comparing verdicts rather than
    interleaving. The heartbeat names what is running meanwhile, because a phase that
    prints nothing until its last check finishes is indistinguishable from a hung one.

    A red check stops the pass (board item 337): the checks that finished keep their
    lines, the ones already running are terminated and named not-run, and every check that
    never started is named not-run with the verdict that stopped it. The streaming report
    is flushed in plan order at the end so the stopped pass reads as one ordered account
    rather than as a prefix with a silent tail.
    """
    checks = plan.checks
    finished = [False] * len(checks)
    active: dict[int, None] = {}
    # The heartbeat thread reads `active` while this thread changes it, and iterating a
    # dict another thread is mutating raises rather than reading stale. The beat is a
    # diagnostic, so it takes a copy under the lock and never holds it while printing.
    active_lock = threading.Lock()
    reported = 0
    cancellation = Cancellation()

    def on_start(index: int) -> None:
        with active_lock:
            active[index] = None

    def note(index: int) -> None:
        one = checks[index]
        progress(f"fork-check: {check_status(one)} {one.kind} -- {one.label} "
                 f"({clock(one.seconds)})")
        if one.not_run_reason:
            progress(f"fork-check:   because: {one.not_run_reason}")

    def on_complete(index: int) -> None:
        nonlocal reported
        with active_lock:
            active.pop(index, None)
        finished[index] = True
        # Everything finished and everything before it already printed, so this is the
        # earliest unfinished check: print the whole finished prefix now.
        while reported < len(checks) and finished[reported]:
            note(reported)
            reported += 1

    def on_not_run(index: int, blamed: int) -> None:
        red = checks[blamed]
        checks[index].not_run_reason = (
            f"not started: {red.kind} -- {red.label} made the verdict red")

    def describe() -> str:
        with active_lock:
            running = sorted(active)
        if not running:
            return "the checks"
        named = ", ".join(checks[index].kind for index in running[:3])
        more = "" if len(running) <= 3 else f" and {len(running) - 3} more"
        return f"{len(running)} check(s) at once ({named}{more})"

    with heartbeat(describe):
        check_wall, peak, halted = run_checks(
            checks, jobs,
            lambda index: execute_check(checks[index], root, diff_base, cancellation,
                                        cache=cache, runtime=runtime),
            on_start=on_start, on_complete=on_complete,
            cheap_first=True, blame=check_blamed,
            terminate=cancellation.cancel, on_not_run=on_not_run,
            heavy_jobs=heavy_jobs)
    # A stopped pass leaves its tail unprinted (the prefix waits on a check that will never
    # finish), so flush the rest in plan order, naming each not-run with its reason.
    while reported < len(checks):
        note(reported)
        reported += 1
    return check_wall, peak, halted


def is_document(root: Path, path: str) -> bool:
    """A tracked Markdown file this gate checks.

    It is a `.md` inside the repository -- never under a fork checkout, a dependency
    tree or this repo's durable scratch -- and it still exists at HEAD, so a document
    the change deleted is not handed to the checker as a missing path.
    """
    if not path.endswith(".md"):
        return False
    parts = Path(path).parts
    if path.startswith("/") or ".." in parts or any(part in DOCUMENT_EXCLUDED for part in parts):
        return False
    return (root / path).is_file()


















def tracked_typescript_lines(root: Path) -> list[tuple[str, int]]:
    """Every tracked `.ts` file with its line count at HEAD, largest first.

    Read from the commit, never the working tree: the budget judges what the change
    lands, and a count taken from the tree would judge bytes the commit does not
    contain -- which is why this check watches nothing and refuses no dirty path. One
    `git grep -c` counts every file's lines in a single process (`-c` counts the lines
    an empty pattern matches, which is every line; a file with no newline at its end
    counts its last line too, and an empty file is absent rather than counted as 0).
    """
    counted = git(root, "grep", "-c", "", "HEAD", "--", "*.ts", check=False)
    sizes: list[tuple[str, int]] = []
    for line in counted.splitlines():
        path, _, count = line.rpartition(":")
        try:
            lines = int(count)
        except ValueError:
            continue
        if path.startswith("HEAD:"):
            path = path[len("HEAD:"):]
        sizes.append((path, lines))
    return sorted(sizes, key=lambda pair: (-pair[1], pair[0]))



def typescript_size_findings(root: Path) -> list[str]:
    """Tree-wide warnings only; added-file refusals belong to the change runner."""
    return [f"{path}: {lines:,} lines, over the {TS_SIZE_WARN_LINES:,}-line budget for a .ts file"
            for path, lines in tracked_typescript_lines(root) if lines > TS_SIZE_WARN_LINES]


def waybar_skip_reason(check: Check) -> str:
    """Why a Waybar check ran without one of its cases, in the terms the report prints.

    One case compares the repository's vendor config against the vendor list of the
    `ai-usagebar` installed on this machine, so a machine without that binary cannot
    answer it. The script prints one `skip <case>: <why>` line for each case it left
    undecided, and the reason is read from that line rather than restated here, so the
    report cannot name a dependency the script did not actually miss.
    """
    lines = [line.strip()[len("skip "):] for line in check.output.splitlines()
             if line.strip().startswith("skip ")]
    return "; ".join(lines) if lines else f"a case reached no verdict here (exit {check.rc})"


def watched_path(path: str, watched: set[str]) -> bool:
    """Is `path` one a check reads? A watch ending in `/` is a directory prefix."""
    return any(path == watch or (watch.endswith("/") and path.startswith(watch))
               for watch in watched)


def watched_spelling(watches: tuple) -> list[str]:
    """A check's watches as the report prints them: the path itself, never `repr`.

    A watch is a repo-relative path -- a file, or a directory ending in `/` -- so it is
    printed as the declaration spells it. A compiled pattern is printed as its source,
    which names what the check reads even though it is not a path a reader can prefix
    match; the declaration's contract is that watches are paths.
    """
    return [watch.pattern if isinstance(watch, re.Pattern) else str(watch) for watch in watches]


def watch_matches(path: str, watch) -> bool:
    """Does one changed path match one watch, whether the watch is a path or a pattern?"""
    if isinstance(watch, re.Pattern):
        return watch.match(path) is not None
    return watched_path(path, {str(watch)})


def selection_paths(check: "Check", changed: list[str]) -> list[str]:
    """The changed paths that selected this check, in the order the diff named them.

    A check's watches are the paths it reads, and a changed path matching one of them is
    the reason the plan ran it. That derivation is right for every step whose watches are
    the paths that select it, which is most of them; a step that declares `selected_by`
    (a fork step watching a gitignored checkout) or is forced by a flag has no such path,
    and answers what it declared. The fixed integrity set is planned whatever the diff
    touches, so it has no selecting path at all -- `selection_entry` says so instead.
    """
    if check.selected_by is not None:
        return list(check.selected_by)
    if check.kind in INTEGRITY_CHECKS:
        return []
    return [path for path in changed
            if any(watch_matches(path, watch) for watch in check.watches)]


def selection_entry(check: "Check", changed: list[str]) -> dict:
    """One check as the run's record names it: why it ran, and what selected it.

    The gate already computes a reason for every check it plans (`Check.why`) and prints
    it in the report; this is the same fact as data, so a reader of the record -- the
    board's stored pass, or an operator asking why a six-minute step ran -- does not have
    to read the gate's source and guess. `selected_by` is the changed path that matched a
    watch, `unconditional` says the fixed integrity set planned it whatever the diff
    touched, and `forced` names the flag that planned it when no path did.
    """
    unconditional = check.kind in INTEGRITY_CHECKS
    return {
        "kind": check.kind,
        "label": check.label,
        "why": check.why,
        "watches": watched_spelling(check.watches),
        "selected_by": selection_paths(check, changed),
        "unconditional": unconditional,
        "forced": check.forced,
    }


def skip_entry(skip: "Skip") -> dict:
    """One step the run did not put a verdict on, in the terms the record keeps.

    `planned` is the distinction the report already draws: False means the change gave
    the step no reason to run, True means it did and this machine or invocation could not
    answer. A quarantined step ran and failed, and its declaration travels with it.
    """
    entry = {"label": skip.label, "why": skip.why, "planned": skip.planned,
             "kind": skip.kind}
    # Only when true, so a full-gate run's selection line is byte-identical to the one it
    # printed before the tier existed. A reader treats an absent `owed` as false.
    if skip.owed:
        entry["owed"] = True
    if skip.quarantine is not None:
        entry["quarantine"] = {"item": skip.quarantine.item,
                              "expires": skip.quarantine.expires.isoformat(),
                              "classification": skip.quarantine.classification}
    return entry


def print_selection(plan: "Plan", changed: list[str]) -> None:
    """One machine-readable line naming why every check ran and every step did not.

    The report prints the same facts as prose, and `--explain` prints them before a run
    exists; this is the run's own account as data, so a consumer that keeps only the tail
    of a gate's output can still read it. It is printed immediately BEFORE the plan line,
    not after it: the plan line is the last line by contract (a landing's plan reader and
    the tail both depend on that), and the two lines together are the tail a consumer
    keeps. The payload is one JSON object on one line, so a label or a reason containing
    a newline cannot split it.

    A gate that prints no such line is recorded as having declared none, never as having
    declared an empty plan: "could not ask" and "nothing was selected" are different
    claims, the same distinction the plan line's `checks`/`required` pair makes.
    """
    payload = {
        "checks": [selection_entry(check, changed) for check in plan.checks],
        "skips": [skip_entry(skip) for skip in plan.skips],
        # The checks this run was told not to buy, with the watches that licensed the
        # reuse. They are not skips: nothing about them was unanswerable, and the caller
        # holds their result. A reader of the record can see what the run did not buy.
        "reused": [selection_entry(check, changed) for check in plan.reused],
    }
    print(f"{SELECTION_PREFIX}{json.dumps(payload, sort_keys=True)}")


def dirty_paths(root: Path) -> list[str]:
    """Working-tree paths, one per changed file, rename detection off.

    `--no-renames` keeps every entry to `XY PATH`, so the path is the token past the
    two status columns; a rename would otherwise emit its original path as a second
    NUL-separated field and be read as a path of its own.
    """
    raw = git(root, "status", "--porcelain=v1", "-z", "--no-renames")
    return [entry[3:] for entry in raw.split("\0") if entry]


def dirty_state(root: Path) -> dict[str, tuple[int, int] | None]:
    """The dirty set with a per-path fingerprint, so two reads can be compared.

    `build_plan` issues one `git ls-remote` per distinct upstream, which can take
    seconds, so a write can land between the snapshot and the refusal decision that
    reads it. `size` and `mtime_ns` are the fingerprint: a file whose bytes change
    without changing either is not one a person or an editor writes. A dirty path
    that no longer exists in the working tree -- a staged deletion -- has no stat,
    and reads as `None`.
    """
    state: dict[str, tuple[int, int] | None] = {}
    for path in dirty_paths(root):
        try:
            info = os.stat(root / path)
            state[path] = (info.st_size, info.st_mtime_ns)
        except OSError:
            state[path] = None
    return state




def file_size_integrity() -> Check:
    """The size budget: a property of the tree rather than of the diff.

    Planned every run for that reason -- gated on changed paths it would have said nothing
    while a shipped extension grew to 19,839 lines. It reads the commit's blobs and watches no
    path, so it neither reads a working-tree byte nor refuses a dirty one.
    """
    return Check("file-size",
                 f"warn when a tracked .ts file exceeds {TS_SIZE_WARN_LINES:,} lines",
                 [], "always", 0)


# The integrity set's members, by kind. `INTEGRITY_CHECKS` is the declaration and this is
# how each member is built, so a kind added to one without the other fails here rather than
# silently planning nothing or being planned twice.
INTEGRITY_BUILDERS = {
    "file-size": file_size_integrity,
}


def plan_integrity(plan: Plan) -> None:
    """Plan the fixed integrity set: the checks a run makes whatever the diff touches.

    They are planned here, once, so `INTEGRITY_CHECKS` is the whole declaration and a
    reader counting them in the report's `plan` line is counting this tuple. Each one
    carries its own reason, because the reason is why it is in the set.
    """
    for kind in INTEGRITY_CHECKS:
        plan.checks.append(INTEGRITY_BUILDERS[kind]())


def reuse_selectors() -> list[str]:
    """The checks a caller says it already holds a result for, from `REUSE_ENV`.

    The vocabulary is the one `EXCLUSIVE_CHECKS` and the preflight's `--only` use: a
    kind, or `kind:label` to name one check of a shared kind. An empty answer means the
    caller asked for no reuse, which is every ordinary run.
    """
    raw = os.environ.get(REUSE_ENV, "").strip()
    return [name.strip() for name in raw.split(",") if name.strip()]


def reuse_names(name: str, kind: str, label: str) -> bool:
    """Whether `name` selects this check: its kind, or `kind:label` exactly.

    The label is matched EXACTLY rather than as a prefix, because the caller reads the
    label from the selection line this gate printed and hands it back: a prefix match
    would let one check's reuse silently cover a sibling of the same kind.
    """
    head, sep, label_name = name.partition(":")
    if head != kind:
        return False
    return label_name == label if sep else True


def apply_reuse(plan: Plan, selectors: list[str]) -> None:
    """Remove the checks whose results the caller already holds, and record them.

    The integrity set is never removed. It is a property of the tree rather than of the
    diff, and a re-gate exists because the tree moved, so a reused integrity result would
    be a claim about a tree this run did not judge. A selector that names one is ignored
    rather than refused: the caller's reuse list is a request, and the plan line's
    `reused` field is the answer, so a caller that asked for too much still gets a run
    that judged everything it needed to.
    """
    if not selectors:
        return
    kept: list[Check] = []
    for check in plan.checks:
        if check.kind in INTEGRITY_CHECKS:
            kept.append(check)
            continue
        if not check.reusable:
            # A check that declares itself not reusable has told the gate its verdict
            # depends on an input outside its watches: the upstream a launch would build,
            # or a content-fingerprint cache keyed by more than the tree. Reuse would
            # override that declaration, so a moved upstream between the two gates would
            # be invisible. It is re-run.
            kept.append(check)
            continue
        if any(reuse_names(name, check.kind, check.label) for name in selectors):
            plan.reused.append(check)
            continue
        kept.append(check)
    plan.checks = kept


def apply_tier(plan: Plan, tier: str | None) -> None:
    """Keep the checks a caller-requested tier plans, and name the rest.

    `None` is the full gate: every step the diff selected, exactly as before, and this
    function returns without touching the plan. That is the merge queue's command and the
    default, so a bare `just done` plans byte for byte what it planned before the flag
    existed.

    `landing` keeps the landing-tier kinds and records each rolling kind the diff selected
    as OWED to the rolling tier -- the third state beside ran and not-planned (invariant
    I5), so a landing's coverage contract can tell deferred proof from a step nothing
    selected. A rolling kind the diff did NOT select stays not-planned: nothing owes it.

    `rolling` keeps the rolling-tier kinds and names the landing-tier steps as not this
    run's business. The landing tier already proved them on the tree that published, and a
    rolling run that re-ran them would be a full gate on a clock. Go behavior suites
    outside GO_LANDING_SUITE_DIRS are rolling kinds, planned for every `GO_TOOL_DIRS`
    entry the range touches -- the same watch-based selection the full gate uses,
    which is the design's
    "per touched module" rule (section 4.2), not an unconditional run of every module.

    Gate code is landing-tier (`self-suite`), so a gate-code diff under `landing` still
    plans the audit suite and under `rolling` plans nothing extra: gate code never proves
    itself late (invariant I3).
    """
    plan.tier = tier
    if tier is None:
        return
    kept: list[Check] = []
    for check in plan.checks:
        if check_tier(check.kind) == tier:
            kept.append(check)
        elif tier == TIER_LANDING:
            plan.owed(check.label,
                      f"the landing tier defers it to the rolling tier "
                      f"({check.kind} is a rolling kind)", kind=check.kind)
        else:
            plan.not_planned(check.label,
                             f"the landing tier's business; a rolling run plans only the "
                             f"rolling tier ({check.kind} is a landing kind)",
                             kind=check.kind)
    plan.checks = kept
    # A skip the diff selected for a kind this tier does not plan is the same news as a
    # check it would have run: under `landing` it is owed, and under `rolling` it is the
    # landing tier's business. A skip nothing selected stays not-planned.
    for skip in plan.skips:
        if not skip.planned or not skip.kind or check_tier(skip.kind) == tier:
            continue
        skip.planned = False
        if tier == TIER_LANDING:
            skip.owed = True
            skip.why = (f"the landing tier defers it to the rolling tier "
                        f"({skip.kind} is a rolling kind)")
        else:
            skip.why = (f"the landing tier's business; a rolling run plans only the "
                        f"rolling tier ({skip.kind} is a landing kind)")


def build_plan(
    root: Path, series: list[Series], record: Record, changed: list[str], force_all: str | None,
    build: bool, audit_upstreams: bool = True, yolo: YoloBuild | None = None,
    audit_yolo: bool = True, node: NodeVersion | None = None, audit_node: bool = True,
    base: str | None = None, tier: str | None = None,
) -> Plan:
    plan = Plan()
    plan.tier = tier
    plan.yolo = yolo
    plan.yolo_audited = audit_yolo
    # Why the installed build is news, whatever the diff says. ADVISORY: one string for
    # the whole run, printed by the report, and it plans nothing -- the steps it
    # decides run when a path they watch changes (`yolo_moved`).
    plan.yolo_moved = yolo_moved(record, yolo) if audit_yolo else None
    # The build the host runs is not readable from a jail, which reads the build on its
    # own PATH: a machine-local input, so it is declared rather than left to veto the
    # verdict, in one line with the command that answers it. A host run reads that build
    # itself, so it has nothing to declare here.
    if in_jail():
        plan.declare(HOST_YOLO_LABEL,
                     "a jail reads the yolo on its own PATH, so the build the host runs "
                     "is not visible from here",
                     HOST_YOLO_COMMAND)
    plan.node = node
    plan.node_audited = audit_node
    # Why the installed node is news, whatever the diff says. ADVISORY: one string for the
    # whole run, printed by the report, and it plans nothing -- the steps it decides run
    # when a path they watch changes (`node_moved`).
    plan.node_moved = node_moved(record, node) if audit_node else None
    plan_integrity(plan)

    if any(watched_path(path, set(WAYBAR_WATCHES)) for path in changed):
        for argv, label in (
            (["sh", "tests/waybar-ai-modules.test.sh"], "Waybar module configuration"),
            ([sys.executable, "-m", "unittest", "discover", "-s", "tests", "-p",
              "test_waybar_cpu.py"], "Waybar CPU appearance and reserved width"),
        ):
            plan.checks.append(Check("waybar", label, argv, "a Waybar input changed",
                                     60, watches=WAYBAR_WATCHES))
    else:
        plan.not_planned("waybar (module configuration, CPU appearance)",
                         "no Waybar input changed")

    for name, inputs, artifact, why, skip in STORYBOOKS:
        matched = [path for path in changed if any(pattern.match(path) for pattern in inputs)]
        storybook_runner = root / "tools" / artifact / "run.sh"
        if matched and shutil.which("node") and storybook_runner.is_file():
            # The writing steps: frames, screenshots and the index land outside the
            # checkout, so a verified commit still leaves a clean tree. A node upgrade is
            # reported and plans nothing (NODE_DEPENDENT); the step watches its own tool.
            # The artifact directory is declared as this check's write set: each storybook
            # rewrites its own, and two steps writing one directory must never run together.
            durable = os.environ.get("YOLO_DURABLE_DIR") or os.environ.get("TMPDIR") or "/tmp"
            artifact_dir = str(Path(durable) / artifact)
            plan.checks.append(
                Check("storybook", f"regenerate {name}",
                      ["bash", str(storybook_runner), artifact_dir],
                      why, STORYBOOK_TIMEOUT,
                      watches=tuple(matched) or (f"tools/{artifact}/",),
                      writes=(artifact_dir,))
            )
        elif matched:
            plan.unavailable(
                f"storybook (tools/{artifact})", "node or the storybook runner is unavailable here",
                kind="storybook",
            )
        else:
            plan.not_planned(f"storybook (tools/{artifact})", skip)

    # The observatory's front end. Path-scoped on purpose: see OBSERVATORY_WEB_INPUT above.
    observatory_web = [path for path in changed
                       if any(pattern.match(path) for pattern in OBSERVATORY_WEB_INPUT)]
    if observatory_web:
        if not shutil.which("npm"):
            plan.unavailable("observatory-web (tsc --noEmit, vitest run)",
                             "npm is unavailable here, so no TypeScript in this tree can be "
                             "compiled or its suite run",
                             kind="observatory-web")
        else:
            plan.checks.append(
                Check("observatory-web",
                      "the observatory's types compile and every gap renders as a gap",
                      ["sh", "-c", OBSERVATORY_WEB_COMMAND],
                      "a path under tools/tillr-view/web/ changed",
                      OBSERVATORY_WEB_TIMEOUT, watches=OBSERVATORY_WEB_WATCHES)
            )
    else:
        plan.not_planned("observatory-web (tsc --noEmit, vitest run)",
                         "no path under tools/tillr-view/web/ changed")

    # The dev recipe's supervision: no unrelated commit pays for its process checks.
    observatory_dev = [path for path in changed
                       if any(pattern.match(path) for pattern in OBSERVATORY_DEV_INPUT)]
    if observatory_dev:
        missing = [tool for tool in ("python3", "just", "hivemind")
                   if not shutil.which(tool)]
        if missing:
            plan.unavailable("observatory-dev (python3 tests/observatory-dev.test.py)",
                             ", ".join(missing) + " unavailable here; the suite requires "
                             "python3 and real just/hivemind supervision",
                             kind="observatory-dev")
        else:
            plan.checks.append(
                Check("observatory-dev",
                      "the observatory dev recipe refuses held ports and leaves neither "
                      "API nor UI alive after interruption or a dead sibling",
                      ["python3", OBSERVATORY_DEV_TEST],
                      "the dev recipe, Procfile, tool declaration or supervision suite changed",
                      OBSERVATORY_DEV_TIMEOUT, watches=OBSERVATORY_DEV_WATCHES)
            )
    else:
        plan.not_planned("observatory-dev (python3 tests/observatory-dev.test.py)",
                         "the dev recipe, Procfile, tool declaration and supervision suite "
                         "are unchanged", kind="observatory-dev")

    # The durable steering timer: the mechanism under the active-steering protocol. Its suite is
    # the only assertion on the record, the injected check-in, the ack rule, the grace
    # re-injection and the coalesced catch-up, and it drives a fake clock through the real
    # registration, so `node` is the whole fixture.
    steering_timer = [path for path in changed
                      if any(pattern.match(path) for pattern in STEERING_TIMER_INPUT)]
    if steering_timer:
        if not shutil.which("node"):
            plan.unavailable("steering-timer (node --test)", "node is unavailable here",
                             kind="steering-timer")
        else:
            plan.checks.append(
                Check("steering-timer",
                      "the steering record, its injected check-in and its ack rule hold",
                      ["node", "--test", STEERING_TIMER_TEST],
                      "the steering timer or its suite changed",
                      STEERING_TIMER_TIMEOUT, watches=STEERING_TIMER_WATCHES)
            )
    else:
        plan.not_planned("steering-timer (node --test)",
                         "the steering timer and its suite are unchanged")

    # The rank-order suite was hand-run only, so the one assertion that a capacity-queued
    # dispatch follows the pile's rank could rot without the gate noticing. A change to the
    # suite, the alphabet it reads, or the series that builds its working copy plans it. The
    # suite skips every case when the checkout is absent -- and exits 0 doing so -- so the
    # step carries the file it imports in `needs`: a missing checkout refuses the run
    # (naming `bin/pi-forks setup --fork pi-subagents`) instead of reading as a pass.
    rank_order = [path for path in changed
                  if any(pattern.match(path) for pattern in RANK_ORDER_INPUT)]
    if rank_order and not shutil.which("node"):
        plan.unavailable("rank-order (node --test)", "node is unavailable here", kind="rank-order")
    elif rank_order:
        plan.checks.append(
            Check("rank-order", "a capacity-queued dispatch follows the pile's rank",
                  ["node", "--test", RANK_ORDER_TEST],
                  "the rank alphabet, its suite, or the series it reads changed",
                  RANK_ORDER_TIMEOUT, watches=RANK_ORDER_WATCHES,
                  needs=(str(forks_root(root) / RANK_ORDER_CHECKOUT),),
                  remedy=RANK_ORDER_SETUP)
        )
    else:
        plan.not_planned("rank-order (node --test)",
                         "the rank alphabet and its suite are unchanged")

    # tests/session-forensics.test.py landed with the tool and the gate never named it, so the
    # only assertion on the hang-analysis report would have been hand-run. A change to the test
    # or to anything under the tool it exercises plans it; nothing else does.
    forensics = [path for path in changed
                 if any(pattern.match(path) for pattern in SESSION_FORENSICS_INPUT)]
    if forensics:
        plan.checks.append(
            Check("session-forensics", "the hang-analysis report still names what a session log leaves behind",
                  ["python3", SESSION_FORENSICS_TEST],
                  "the session-forensics test or the tool it reads changed",
                  SESSION_FORENSICS_TIMEOUT, watches=SESSION_FORENSICS_WATCHES)
        )
    else:
        plan.not_planned("session-forensics (python3 tests/session-forensics.test.py)",
                         "the session-forensics test and the tool it reads are unchanged")

    # tests/pi-inspector.test.py is the other half of board item 192: the jail's environment
    # carries no `--inspect`, and a real pi process still opens an attachable loopback
    # inspector the profiler finds by pid. It builds the profiler and starts the real `pi`
    # entry point, so a change to the test, the extension, or the profiler plans it.
    inspector = [path for path in changed
                 if any(pattern.match(path) for pattern in PI_INSPECTOR_INPUT)]
    if inspector and shutil.which("pi") and shutil.which("go"):
        plan.checks.append(
            Check("pi-inspector", "a real pi process opens an attachable inspector with no signal",
                  ["python3", PI_INSPECTOR_TEST],
                  "the pi-inspector test, the extension, or the profiler it drives changed",
                  PI_INSPECTOR_TIMEOUT, watches=PI_INSPECTOR_WATCHES)
        )
    elif inspector:
        plan.unavailable("pi-inspector (python3 tests/pi-inspector.test.py)",
                         "pi or go is unavailable here", kind="pi-inspector")
    else:
        plan.not_planned("pi-inspector (python3 tests/pi-inspector.test.py)",
                         "the pi-inspector test, the extension, and the profiler are unchanged")
    # tests/session-budget.test.py is the only assertion on the session-budget report. A
    # change to the test or to anything under the tool it exercises plans it; nothing else
    # does.
    budget = [path for path in changed
              if any(pattern.match(path) for pattern in SESSION_BUDGET_INPUT)]
    if budget:
        plan.checks.append(
            Check("session-budget", "the session-budget report still measures prompt, lag and telemetry",
                  ["python3", SESSION_BUDGET_TEST],
                  "the session-budget test or the tool it reads changed",
                  SESSION_BUDGET_TIMEOUT, watches=SESSION_BUDGET_WATCHES)
        )
    else:
        plan.not_planned("session-budget (python3 tests/session-budget.test.py)",
                         "the session-budget test and the tool it reads are unchanged")

    # tests/tillr-muse-backend.test.py is the only assertion on the muse backend
    # adapters. A change to the test, to an adapter, or to the policy that configures them
    # plans it; nothing else does.
    tillr_backend = [path for path in changed
                     if any(pattern.match(path) for pattern in TILLR_BACKEND_INPUT)]
    if tillr_backend:
        plan.checks.append(
            Check("tillr-backends", "the four backend adapters produce one report shape and refuse a truncated stream",
                  ["python3", TILLR_BACKEND_TEST],
                  "the backend test, an adapter, or the backend policy changed",
                  TILLR_BACKEND_TIMEOUT, watches=TILLR_BACKEND_WATCHES)
        )
    else:
        plan.not_planned("tillr-backends (python3 tests/tillr-muse-backend.test.py)",
                         "the backend test, the adapters and the policy are unchanged")

    # tools/tillr-loop/test.py is the loop adapter's own suite. It asserted the adapter a
    # refactor had replaced -- the gate's `--no-wait`, the lander's deleted `enqueue`, the
    # registry's absolute path -- and nothing ran it, so it rotted while the loop kept
    # landing. A change to the adapter, its README or its suite plans it; nothing else does.
    tillr_loop = [path for path in changed
                  if any(pattern.match(path) for pattern in TILLR_LOOP_INPUT)]
    if tillr_loop:
        plan.checks.append(
            Check("tillr-loop", "the loop adapter's three faces translate, refuse and self-check",
                  ["python3", TILLR_LOOP_TEST],
                  "the loop adapter, its suite or its README changed",
                  TILLR_LOOP_TIMEOUT, watches=TILLR_LOOP_WATCHES)
        )
    else:
        plan.not_planned("tillr-loop (python3 tools/tillr-loop/test.py)",
                         "the loop adapter, its suite and its README are unchanged")

    # tests/tillr-docs.test.py is the only assertion that a documented face exists and that no
    # document carries a flag table. A change to the test, to a module a README belongs to, or
    # to the skill that composes them plans it; nothing else does. The same fixture guard as
    # pack-sync's: a repository without the test has no documents of ours to check.
    tillr_docs = [path for path in changed
                  if any(pattern.match(path) for pattern in TILLR_DOCS_INPUT)]
    if tillr_docs and not (root / TILLR_DOCS_TEST).exists():
        plan.not_planned("tillr-docs (python3 tests/tillr-docs.test.py)",
                         "this repository has no tillr documentation suite")
    elif tillr_docs and not shutil.which("go"):
        plan.unavailable("tillr-docs (python3 tests/tillr-docs.test.py)",
                         "go is unavailable here, and the suite builds the eight binaries it "
                         "asks for `--help`", kind="tillr-docs")
    elif tillr_docs:
        plan.checks.append(
            Check("tillr-docs", "every face the tillr documents name answers --help, and no document carries a flag table",
                  ["python3", TILLR_DOCS_TEST],
                  "the documentation suite, a tillr module or its README, or the composition skill changed",
                  TILLR_DOCS_TIMEOUT, watches=TILLR_DOCS_WATCHES)
        )
    else:
        plan.not_planned("tillr-docs (python3 tests/tillr-docs.test.py)",
                         "the documentation suite, the modules it reads and the skill are unchanged")

    # The liveness harness landed with the fork fix it measures, and nothing else executes it.
    # A change to the harness, or to the pi-fork series whose fix it measures, plans TWO steps:
    # the construction of the checkout the verdict is about, and the harness run against it.
    # The construction is planned with the harness rather than borrowed from a series step,
    # because the harness is also planned when only its own files changed and no series step has
    # built a candidate. It reads the shared checkout as a substrate -- cloned by ref, its
    # working tree never read -- so a managed worktree without one refuses the run with the
    # setup command instead of letting the run's exit 127 read as a pass.
    liveness = [path for path in changed
                if any(pattern.match(path) for pattern in SESSION_LIVENESS_INPUT)]
    if liveness and not shutil.which("node"):
        plan.unavailable("session-liveness (tools/session-liveness/run)",
                         "node is unavailable here", kind="session-liveness")
    elif liveness:
        substrate = forks_root(root) / SESSION_LIVENESS_FORK
        judged = candidate_root(root) / SESSION_LIVENESS_FORK
        plan.checks.append(
            Check("fork-construct",
                  f"fork {SESSION_LIVENESS_FORK}: build the checkout the liveness harness judges at {judged}",
                  [str(root / "bin" / "pi-forks"), "candidate", "--fork", SESSION_LIVENESS_FORK, "--build"],
                  "the liveness harness judges the pi series this tree carries, so the checkout it "
                  "reads is built from the series here and not taken from a shared working copy",
                  BUILD_TIMEOUT, watches=SESSION_LIVENESS_WATCHES, needs=(str(substrate),),
                  remedy=fork_setup(SESSION_LIVENESS_FORK), env=series_env(root),
                  # The build writes node_modules, dist and the regenerated pinned catalog into
                  # the checkout, so it is the same resource the harness's vitest cache is.
                  writes=(str(judged),))
        )
        plan.checks.append(
            Check("session-liveness", "a session returns to idle within its bound, and a later send-now starts work",
                  ["sh", SESSION_LIVENESS_RUN],
                  "the liveness harness or the pi-fork series changed",
                  SESSION_LIVENESS_TIMEOUT, watches=SESSION_LIVENESS_WATCHES,
                  # PI_FORK is the whole handover: the run script, the vitest binary, the package
                  # aliases and the harness's own fork imports all resolve through it, so this is
                  # the checkout the verdict is about. It is not a `needs` path: the plan builds it
                  # in the step above, and a need on a path no step has written yet would refuse
                  # the run before the construction could happen.
                  env={"PI_FORK": str(judged)},
                  writes=(str(judged),))
        )
    else:
        plan.not_planned("session-liveness (tools/session-liveness/run)",
                         "the liveness harness is unchanged")

    # The meta-muse surfaces are pinned by Node tests: two in this repository's tests/ dir
    # render the pack at the host notch and hand the result to Pi itself, and one in the pack's
    # own tests/ dir drives the MSP bridge against a real `muse serve`. They run in the gate
    # when a path they read changes -- the pack, the personal layer that selects it, or the
    # tests. `yolo` is named as a skip rather than a failure, exactly as the series checks treat
    # a missing one: a machine without it cannot answer this question, and neither can the check.
    meta_muse = [path for path in changed if any(pattern.match(path) for pattern in META_MUSE_INPUT)]
    # The installed node version and the installed yolo build both feed these, and both are
    # ADVISORY: a moved one is reported and plans nothing (YOLO_DEPENDENT, NODE_DEPENDENT).
    # The step runs when a path it reads changes.
    if meta_muse and shutil.which("node") and shutil.which("yolo"):
        plan.checks.append(
            Check("meta-muse", "the meta-muse surfaces render and Pi composes them",
                  ["node", "--test", *META_MUSE_TESTS],
                  "the change can change what the meta-muse surfaces render",
                  META_MUSE_TIMEOUT,
                  watches=("yolo-packs/meta-muse/", "tag-personal/config/yolo-jail/config.d/personal.toml",
                           *META_MUSE_TESTS))
        )
    elif meta_muse:
        plan.unavailable("meta-muse (node --test)", "node or yolo is unavailable here", kind="meta-muse")
    else:
        plan.not_planned("meta-muse (node --test)", "no meta-muse surface changed")

    # The seat-profile encoding is DATA plus the one test that pins it, and the test is the
    # only thing that can tell a moved row or a loosened bound from a deliberate one. It runs
    # when the encoding, the pack that ships it, or a yolo layer that names a set changes.
    seat_profiles = [path for path in changed
                     if any(pattern.match(path) for pattern in SEAT_PROFILES_INPUT)]
    if seat_profiles and shutil.which("node"):
        plan.checks.append(
            Check("seat-profiles", "the encoded sets and seat bounds still pin",
                  ["node", "--test", *SEAT_PROFILES_TESTS],
                  "the change touches the encoding, the pack that ships it, the design document it encodes, or a yolo layer that names a set",
                  SEAT_PROFILES_TIMEOUT,
                  watches=("yolo-packs/seat-profiles/", *SEAT_PROFILES_TESTS,
                           "docs/design/seat-profiles.md",
                           "config/yolo-jail/config.toml",
                           "tag-personal/config/yolo-jail/config.d/personal.toml",
                           "tag-work/config/yolo-jail/config.d/work.toml"))
        )
    elif seat_profiles:
        plan.unavailable("seat-profiles (node --test)", "node is unavailable here", kind="seat-profiles")
    else:
        plan.not_planned("seat-profiles (node --test)", "no seat-profile encoding path changed")

    # tests/yolo-config.test.sh is the only place the yolo layers -- the work class's profile
    # selection among them -- are asserted, and nothing else pins those files. It runs when a
    # file it reads changes, and names a skip when the `python3` and `yolo` it needs are absent.
    config_touched = [path for path in changed
                      if any(pattern.match(path) for pattern in YOLO_CONFIG_INPUT)]
    if config_touched and shutil.which("python3") and shutil.which("yolo"):
        plan.checks.append(
            Check("yolo-config", "the yolo config layers still assert",
                  ["sh", YOLO_CONFIG_TEST],
                  "a yolo config layer, or the test that pins it, changed",
                  YOLO_CONFIG_TIMEOUT, watches=YOLO_CONFIG_WATCHES)
        )
    elif config_touched:
        plan.unavailable("yolo-config (sh tests/yolo-config.test.sh)",
                         "python3 or yolo is unavailable here", kind="yolo-config")
    else:
        plan.not_planned("yolo-config (sh tests/yolo-config.test.sh)",
                         "no yolo config file changed")

    # The matt pack's render and overlay tests read a HOST render of the WORK class and the
    # `pi/settings` overlay it composes, so a change to a path they read can change what a
    # work-class host composes. They run together, and name a skip when the `node` and `yolo`
    # they need are absent.
    matt_render = [path for path in changed
                   if any(pattern.match(path) for pattern in MATT_RENDER_INPUT)]
    # A moved yolo build and a moved node version are both reported, not planned
    # (YOLO_DEPENDENT, NODE_DEPENDENT): the step runs when a path it reads changes.
    # Name the paths that planned the step, so `--explain` says which of the four tests, the
    # manifest or `files/` decided it rather than naming the class of change.
    matt_render_named = ", ".join(matt_render[:3])
    if len(matt_render) > 3:
        matt_render_named += f" and {len(matt_render) - 3} more"
    if matt_render and shutil.which("node") and shutil.which("yolo"):
        plan.checks.append(
            Check("matt-render", "the matt pack's render reaches the work class",
                  ["node", "--test", *MATT_RENDER_TESTS],
                  f"{matt_render_named} changed, and the render reads it",
                  MATT_RENDER_TIMEOUT, watches=MATT_RENDER_WATCHES)
        )
    elif matt_render:
        plan.unavailable("matt-render (node --test)", "node or yolo is unavailable here", kind="matt-render")
    else:
        plan.not_planned("matt-render (node --test)",
                         "no work-class render test or layer changed")

    # Build and test compilation moved to the whole-tree go-build declaration.
    # This planner still selects the full behavior suite of each changed module.
    for directory in GO_TOOL_DIRS:
        suite_kind = "go-substrate" if directory in GO_LANDING_SUITE_DIRS else "go-tool"
        watches = (directory + "/",)
        go_tool = [path for path in changed if watched_path(path, set(watches))]
        if go_tool and shutil.which("go"):
            plan.checks.append(
                Check(suite_kind, f"{directory}'s whole suite passes",
                      ["sh", "-c", go_tool_command(directory)],
                      f"a path under {directory}/ changed", GO_TOOL_TIMEOUT,
                      watches=watches, selected_by=tuple(go_tool),
                      cost=GO_TOOL_COST_ESTIMATE[directory],
                      drop=("LAND_BATCH_LIMIT",))
            )
        elif go_tool:
            plan.unavailable(f"{suite_kind} ({directory})", "go is unavailable here", kind=suite_kind)
        else:
            plan.not_planned(f"{suite_kind} ({directory})", f"no path under {directory}/ changed")

    # The shared pipeline-evidence format. The gate is the producer that still ships here,
    # and the suite feeds the velocity report synthetic records written to the format, so no
    # lander binary is needed to notice the shape drifting: this step validates the gate's
    # own record against the one schema and the report against its fixtures. It runs on the
    # interpreter running this gate.
    pipeline_evidence = [path for path in changed
                         if watched_path(path, set(PIPELINE_EVIDENCE_WATCHES))]
    if pipeline_evidence:
        plan.checks.append(
            Check("pipeline-evidence",
                  "one record from each producer validates against the shared schema",
                  [sys.executable, PIPELINE_EVIDENCE_TEST],
                  "a producer of pipeline evidence, the format, or its conformance suite changed",
                  PIPELINE_EVIDENCE_TIMEOUT, watches=PIPELINE_EVIDENCE_WATCHES)
        )
    else:
        plan.not_planned("pipeline-evidence (tests/pipeline-evidence.test.py)",
                         "no producer of pipeline evidence and no format file changed")

    # The reload-residue audit: the record of what each shipped extension registers on the
    # long-lived runtime, and the guard that keeps the record complete. It audits the real tree
    # and breaks one thing at a time in a fixture, so a guard that stopped catching a site fails
    # here rather than staying green.
    residue = [path for path in changed if watched_path(path, set(RESIDUE_WATCHES))]
    if residue:
        plan.checks.append(
            Check("extension-residue",
                  "every reload-residue site in a shipped extension is cleaned or recorded, "
                  "and the guard still catches one that is not",
                  [sys.executable, RESIDUE_TEST],
                  "an extension source, the registry document, the audit or its inventory changed",
                  RESIDUE_TIMEOUT, watches=RESIDUE_WATCHES,
                  # The test wipes and rebuilds its fixture base under the durable directory;
                  # declaring it costs no serialization and makes the claim checkable.
                  writes=(residue_scratch(),))
        )
    else:
        plan.not_planned("extension-residue (tests/extension-residue.test.py)",
                         "no extension source, registry document or audit changed")

    # The active-steering skill's text. Its cadence is the steering record's, and the ack
    # tool is what advances the deadline; a number written back into the prose, or a lost
    # `steer_checkin` reference, is exactly the regression the skill's own test names. It
    # runs on the interpreter running this gate, so no machine lacks it.
    steering_skill = [path for path in changed
                      if any(pattern.match(path) for pattern in ACTIVE_STEERING_INPUT)]
    if steering_skill:
        plan.checks.append(
            Check("active-steering-skill",
                  "the active-steering skill takes its cadence from the steering record",
                  [sys.executable, ACTIVE_STEERING_TEST],
                  "the active-steering skill or the test that pins it changed",
                  ACTIVE_STEERING_TIMEOUT, watches=ACTIVE_STEERING_WATCHES)
        )
    else:
        plan.not_planned("active-steering-skill (tests/active-steering-skill.test.py)",
                         "the active-steering skill and its test are unchanged")

    # The gate's own audit suite: nothing else executes it, so an edit to the checker or
    # to the suite that pins it is judged by that suite rather than only by the gate the
    # edit just changed.
    self_suite = [path for path in changed if path in SELF_SUITE]
    if self_suite:
        plan.checks.append(
            Check("self-suite", "the gate's own audit suite still passes",
                  [sys.executable, SELF_SUITE_TEST],
                  "the checker or the audit suite that pins it changed",
                  SELF_SUITE_TIMEOUT, watches=SELF_SUITE)
        )
    else:
        plan.not_planned("self-suite (tests/fork-check.test.py)",
                         "neither the checker nor its audit suite changed")

    # The patch-branch pre-push guard: the hook that keeps origin from holding a manifest
    # whose patch branch it cannot deliver. Its suite drives real pushes against a bare
    # origin it builds, so nothing here reaches a network.
    pre_push = [path for path in changed
                if any(pattern.match(path) for pattern in PRE_PUSH_INPUT)]
    if pre_push and shutil.which("sh"):
        plan.checks.append(
            Check("pre-push-guard",
                  "hooks/pre-push: the hook report, the branch publish, the rewrite rule, "
                  "the refusal and the remedy it prints",
                  ["sh", PRE_PUSH_TEST],
                  "the guard, the publish step or the suite that pins them changed",
                  PRE_PUSH_TIMEOUT, watches=PRE_PUSH_WATCHES)
        )
    elif pre_push:
        plan.unavailable("pre-push-guard (sh tests/pre-push-guard.test.sh)",
                         "sh is unavailable here", kind="pre-push-guard")
    else:
        plan.not_planned("pre-push-guard (sh tests/pre-push-guard.test.sh)",
                         "the guard, the publish step and the suite that pins them are "
                         "unchanged")

    # The driver's own suite. See PI_FORKS_INPUT for why a series change plans it, and
    # PI_FORKS_WATCHES for why a dirty pack path does not refuse it.
    pi_forks = [path for path in changed
                if any(pattern.match(path) for pattern in PI_FORKS_INPUT)]
    if pi_forks and shutil.which("bash"):
        plan.checks.append(
            Check("pi-forks",
                  "bin/pi-forks: the driver's own cases -- setup, export, check, the "
                  "patch-branch verbs and the manifest",
                  ["bash", PI_FORKS_TEST],
                  "the driver, the suite that pins it, or a series or manifest it reads changed",
                  PI_FORKS_TIMEOUT, watches=PI_FORKS_WATCHES)
        )
    elif pi_forks:
        plan.unavailable("pi-forks (bash tests/pi-forks.test.sh)",
                         "bash is unavailable here, and the suite sources and drives the driver through it",
                         kind="pi-forks")
    else:
        plan.not_planned("pi-forks (bash tests/pi-forks.test.sh)",
                         "the driver, its suite and the series it reads are unchanged")

    # `just hooks-install`: the installer that keeps the shared hooks directory in step
    # with hooks/. Its suite builds a throwaway repository under the system temp
    # directory, so nothing here writes the hooks directory this checkout's git reads.
    hooks_install = [path for path in changed
                     if watched_path(path, set(HOOKS_INSTALL_WATCHES))]
    if hooks_install and shutil.which("sh"):
        plan.checks.append(
            Check("hooks-install",
                  "bin/hooks-install: a marked copy is replaced and drift reported on every "
                  "run, an unmarked hook is left alone and chained, and every hook is named",
                  ["sh", HOOKS_INSTALL_TEST],
                  "the installer, a hook, the recipe or the suite that pins it changed",
                  HOOKS_INSTALL_TIMEOUT, watches=HOOKS_INSTALL_WATCHES)
        )
    elif hooks_install:
        plan.unavailable("hooks-install (sh tests/hooks-install.test.sh)",
                         "sh is unavailable here", kind="hooks-install")
    else:
        plan.not_planned("hooks-install (sh tests/hooks-install.test.sh)",
                         "the installer, the hooks, the recipe and the suite are unchanged")

    # Board item 412: a change confined to the gate's own selection or reporting code
    # cannot change how a series is materialized or judged, so a fork's upstream drift is
    # not this change's business -- the full audit (`--all`) asks it. Without this, a
    # broken fork blocks every gate fix, which is the deadlock the item names: the
    # landing pass plans from the batch base with the upstream audit on, so a moved
    # upstream carried every fork even for a gate-only diff. Documents ride along, as
    # they do in `classify_change`; a change that reaches any fork input is not
    # gate-only and keeps the drift reason.
    gate_only = bool(changed) and any(path in GATE_CODE for path in changed) and all(
        path in GATE_CODE or is_document(root, path) for path in changed)

    touched_series: set[str] = set()
    declaration_packs: set[str] = set()
    manifest_touched = False
    for path in changed:
        # The driver is the materializer, and it keeps its own branch so the cause stays
        # `driver` rather than reading as a change to the gate's code (board item 412).
        if path in ("bin/pi-forks", "bin/pi-forks.list"):
            manifest_touched = True
            continue
        if path in MECHANISM:
            force_all = "the change touches the code that materializes or judges a series"
            continue
        if not any(pattern.match(path) for pattern in FORK_INPUT):
            continue
        match = re.match(r"^yolo-packs/([^/]+)/pack\.json$", path)
        if match:
            declaration_packs.add(match.group(1))
            continue
        for one in series:
            if path.startswith(f"yolo-packs/{one.pack}/{one.rel}/"):
                touched_series.add(one.owner_key)

    # The upstream a launch would build, resolved by the bridge that carries the launch's
    # own follow rule. Asked FIRST and in one call: its fetch is what makes the ref the
    # launch's rather than this checkout's last one, and the tag probe below is only a
    # fallback for a fork the bridge could not answer.
    if audit_upstreams:
        plan.targets, plan.target_problems = resolve_targets(root, series)

    # Every distinct upstream, once, all at once.
    wanted = sorted({one.url for one in series if one.url}) if audit_upstreams else []
    if wanted:
        with concurrent.futures.ThreadPoolExecutor(max_workers=min(8, len(wanted))) as pool:
            futures = {pool.submit(ls_remote, url): url for url in wanted}
            for future in concurrent.futures.as_completed(futures):
                url = futures[future]
                try:
                    got, why = future.result()
                except Exception as problem:  # a shape we did not foresee; never fatal
                    got, why = None, str(problem)
                if got is None:
                    plan.unreachable[url] = why
                else:
                    plan.refs[url] = got

    affected_packs: dict[str, list[str]] = {}
    # The series a change in this repository moved -- its content, its pack's declaration,
    # `bin/pi-forks.list`, `--all`, a first run, or a moved upstream. A yolo upgrade puts
    # nothing here: it is reported (YOLO_DEPENDENT) and changes neither the patch branch
    # nor the fork's source, so the merge, export, replay, build and npm checks -- which
    # read git content and compilation, not which yolo answers -- are not planned by it.
    moved_series: list[Series] = []
    # Why each carried fork's quality steps execute, in the design's priority order:
    # gate-code, driver, series, declaration, drift. A fork can be carried for more than
    # one reason; the primary cause is the first that applies, and the check's own `why`
    # line keeps every reason. This is the cause the report prints and the records keep
    # (board item 407). `gate-code` is the fork tooling's own change -- the gate's code or
    # the series judge -- and it is the one cause a gate-only change no longer produces
    # (board item 412).
    cause_of: dict[str, str] = {}
    for one in series:
        reasons: list[str] = []
        cause = ""
        if force_all:
            reasons.append(force_all)
            # `--all` is a full audit, not a change to the fork tooling: both forced every
            # series, but they are different causes and a reader counting causes must not
            # read a nightly audit as gate work.
            cause = "audit" if force_all == "--all was given" else "gate-code"
        elif manifest_touched:
            reasons.append("bin/pi-forks.list changed, which maps every series")
            cause = "driver"
        elif one.owner_key in touched_series:
            reasons.append("the series changed")
            cause = "series"
        elif one.pack in declaration_packs:
            reasons.append("its pack's declaration changed")
            cause = "declaration"

        if one.follows_branch and audit_upstreams and not gate_only:
            target = plan.targets.get(one.fork)
            if one.url in plan.unreachable and target is None:
                reasons.append(f"its upstream is unreachable ({plan.unreachable[one.url]})")
                cause = cause or "drift"
            else:
                current = fingerprint(one, plan.refs.get(one.url) or {"heads": {}, "tags": {}}, target)
                previous = record.target(one.owner_key)
                if previous is None:
                    reasons.append(f"its upstream was never checked ({describe(current)})")
                    cause = cause or "drift"
                elif previous.get("fingerprint") != current:
                    reasons.append(
                        f"its upstream moved: {describe(current)} (last check: {describe(previous['fingerprint'])})"
                    )
                    cause = cause or "drift"
                if target is None and one.fork in plan.target_problems:
                    # The probe answered, not the launch's own resolution: say so, so a
                    # green headline never reads as "asked the ref a launch builds".
                    reasons.append(f"the launch's resolution was not available ({plan.target_problems[one.fork]})")

        if reasons:
            affected_packs.setdefault(one.pack, []).append(f"{one.owner_key} -- {'; '.join(reasons)}")
            moved_series.append(one)
            cause_of[one.owner_key] = cause or "drift"
        else:
            if gate_only:
                why = ("the change is confined to the gate's own selection or reporting "
                       "code, so no fork is carried; upstream drift belongs to the full audit")
            elif audit_upstreams:
                previous = record.target(one.owner_key)
                where = describe(previous["fingerprint"]) if previous else "unknown"
                why = f"no change to its series, and its upstream is where the last check left it ({where})"
            else:
                why = "outside the selected change; upstream drift belongs to the full audit"
            plan.not_planned(one.owner_key, why)

    def fork_selection(name: str) -> tuple[str, ...]:
        """The changed paths that selected a fork's steps, in the diff's own order.

        A fork step watches `.forks/<name>/`, which is gitignored and never appears in a
        diff, so its selection is the series path that moved -- or the manifest that maps
        every series, or the gate's own mechanism when the change is the tooling itself.
        """
        paths = [path for path in changed
                 if any(path.startswith(f"yolo-packs/{one.pack}/{one.rel}/")
                        for one in series if one.fork == name)]
        if manifest_touched:
            paths += [path for path in changed if path in ("bin/pi-forks", "bin/pi-forks.list")]
        if force_all:
            paths += [path for path in changed if path in MECHANISM]
        return tuple(paths)

    def pack_selection(pack: str) -> tuple[str, ...]:
        """The changed paths that selected a pack's series check."""
        paths = [path for path in changed if path.startswith(f"yolo-packs/{pack}/")]
        if manifest_touched:
            paths += [path for path in changed if path in ("bin/pi-forks", "bin/pi-forks.list")]
        if force_all:
            paths += [path for path in changed if path in MECHANISM]
        return tuple(paths)

    for pack, reasons in sorted(affected_packs.items()):
        plan.checks.append(
            Check("series", f"pack {pack}: {len(reasons)} series", ["yolo", "pack", "series", "check", f"yolo-packs/{pack}"],
                  "\n       ".join(reasons), SERIES_TIMEOUT, watches=(f"yolo-packs/{pack}/",),
                  selected_by=pack_selection(pack), reusable=False)
        )

    # INV-2, the launch's own merge question (`bin/pi-forks patch-branch merge-check`), for
    # every series the plan already has a reason to look at -- the series check's own
    # condition, so a launch's verdict is asked here rather than at a restart. The check
    # is offline, and it asks in the candidate checkout (`--candidate`): the branch that
    # matters is the one holding this series' commits, not whatever a shared working tree
    # carries, so a writer's unexported work in the cache cannot answer for the candidate.
    # The step carries the shared checkout in `needs`, because that is where the
    # candidate's objects and upstream refs come from: without one the run is refused,
    # naming the setup command, rather than recording a verdict over a step that never ran.
    carried = {}
    for one in moved_series:
        carried.setdefault(one.fork, one)
    for name in sorted(carried):
        plan.checks.append(
            Check("patch-branch-inv2",
                  f"fork {name}: the series merges the upstream a launch would build, asked in "
                  f"the clean checkout at {candidate_root(root) / name}",
                  [str(root / "bin" / "pi-forks"), "patch-branch", "merge-check", "--candidate", "--fetch",
                   "--fork", name],
                  "the declaration or series changed, or the upstream moved", FORKS_TIMEOUT,
                  watches=(f"yolo-packs/{carried[name].pack}/", "bin/pi-forks"),
                  selected_by=fork_selection(name),
                  needs=(str(forks_root(root) / name),), remedy=fork_setup(name),
                  env=series_env(root),
                  # --candidate MATERIALIZES .forks/candidate/<fork>, so this step is a
                  # writer of that directory and must serialize with every other step that
                  # works in it -- the fork-quality scripts, which run there too. Declaring
                  # nothing let the two collide, and the loser failed in under a second with
                  # "cannot check patch/<fork> out in .forks/candidate/<fork>" (2026-10-26,
                  # pi-subagents' merge-check against its typecheck).
                  writes=(str(candidate_root(root) / name),), reusable=False)
        )

    # INV-3, offline (`bin/pi-forks patch-branch verify`): the series in the pack is exactly
    # the export of its working copy, so a hand-edited, half-merged or stale series
    # fails here rather than at a launch. Planned for a series that changed, which is
    # the same working copy the replay check below reads, and carrying it in `needs`
    # for the same refusal.
    exported = {}
    for one in series:
        if one.owner_key in touched_series:
            exported.setdefault(one.fork, one)
    for name in sorted(exported):
        plan.checks.append(
            Check("patch-branch-inv3", f"fork {name}: the series is exactly its working copy's export",
                  [str(root / "bin" / "pi-forks"), "patch-branch", "verify", "--fork", name],
                  "the series changed, so it must still be the export", FORKS_TIMEOUT,
                  watches=(f"yolo-packs/{exported[name].pack}/", "bin/pi-forks"),
                  selected_by=fork_selection(name),
                  needs=(str(forks_root(root) / name),), remedy=fork_setup(name),
                  env=series_env(root), reusable=False)
        )

    # Every fork step that judges the series runs in the CANDIDATE checkout, a clean tree
    # `bin/pi-forks candidate` builds by replaying the series at its base -- never in the
    # shared `.forks/<name>` working tree. That tree is a cache and, on a machine where a
    # lane or a hand fix is working, it holds somebody's half-finished edits: on 2026-10-24
    # `.forks/pi-session-search` carried six edited test files from a writer that had gone
    # stale four hours earlier, and a gate reading it would have tested those instead of
    # the candidate. The shared checkout is still what these steps need (it supplies the
    # objects and upstream refs the replay and the build read), so a missing one still
    # refuses the run rather than leaving the series unverified.
    forks = sorted({one.fork for one in series if one.owner_key in touched_series})
    # The build step's label per fork, so a quality run can name it as its prepare step.
    build_labels: dict[str, str] = {}
    for name in forks:
        checkout = forks_root(root) / name
        candidate = candidate_root(root) / name
        plan.checks.append(
            Check("forks-check", f"fork {name}: the series replays into a clean checkout at {candidate}",
                  [str(root / "bin" / "pi-forks"), "candidate", "--fork", name],
                  "the series changed, so it must still apply at its base", FORKS_TIMEOUT,
                  watches=(f".forks/{name}/",), selected_by=fork_selection(name),
                  needs=(str(checkout),), remedy=fork_setup(name),
                  env=series_env(root), reusable=False,
                  # `candidate` replays into a scratch clone and may fetch the series base
                  # into the checkout, so it writes the same working copy a build does --
                  # and it MATERIALIZES the candidate checkout, which is the resource every
                  # other `candidate` step shares. Declaring only the shared checkout let a
                  # replay and a quality script materialize one candidate at once, which is
                  # the "cannot check patch/<fork> out" collision of 2026-10-26.
                  #
                  # It does NOT install: the quality run's prepare step is the one producer
                  # of the candidate's dependency state, and the preflight that reads that
                  # state runs on the readers, behind it (board items 194 and 405).
                  writes=(str(checkout), str(candidate)))
        )
        # A build has one home: the candidate checkout. Without the shared checkout it
        # would need a clone of the upstream (`bin/pi-forks setup`), an `npm ci`, and a
        # fetch of the pinned model catalog -- all network, and this gate decides upstream
        # drift without fetching. A missing working copy refuses the run (naming the setup)
        # rather than leaving the series' compilation unmentioned
        # (docs/design/patch-series-reliability.md, Inputs: a missing working copy is a
        # refusal, not a silent pass).
        if not build:
            plan.unavailable(f"fork {name} (bin/pi-forks candidate --build)",
                             "--no-build: build verification did not run", kind="build")
        else:
            build_labels[name] = f"fork {name}: the series builds in the clean checkout at {candidate}"
            plan.checks.append(
                Check("build", build_labels[name],
                      [str(root / "bin" / "pi-forks"), "candidate", "--fork", name, "--build"],
                      "the series changed, so it must compile", BUILD_TIMEOUT,
                      watches=(f".forks/{name}/",), selected_by=fork_selection(name),
                      forced="--build" if not fork_selection(name) else "",
                      needs=(str(checkout),), remedy=fork_setup(name),
                      env=series_env(root), reusable=False,
                      # The build materializes the candidate as well as building it, so it
                      # is a writer of both the shared checkout and the candidate.
                      writes=(str(checkout), str(candidate)))
            )

    # Every fork the gate has steps for, and the ones this run had no reason to look at.
    # A fork whose series did not change gets no patch-branch verify, no replay and no build, so
    # those steps are named in one entry rather than dropped: a step that is not planned
    # is not a pass, and a reader counting what a green run covered must see them.
    for name in sorted({one.fork for one in series} - set(forks)):
        # The merge question is planned for a fork whose upstream moved as well, so it is
        # named here only when that is not why this run looked at the fork.
        steps = [] if name in carried else ["patch-branch merge-check"]
        steps += ["patch-branch verify", "candidate replay"]
        if build:
            steps.append("candidate build")
        if name in FORK_QUALITY and name not in carried:
            steps.append("quality prepare and scripts")
        plan.not_planned(f"fork {name} ({', '.join(steps)})",
                         "no series of it changed")

    # A build cannot detect failing behavioral tests or TypeScript diagnostics
    # (esbuild only transpiles). Check the candidate checkout as well as its
    # replay and build: the driver materializes it from the series and runs the command
    # inside it, so a missing checkout or npm fails explicitly and a writer's edits in the
    # shared tree are not what gets tested. --no-build skips the build, not these checks.
    #
    # ONE PREPARE STEP, THEN READERS. Materializing the series and installing its
    # dependencies is the only shared write a quality run makes, so it is planned once, as
    # the sole writer of the candidate checkout. Each script then runs with `--prepared`,
    # which touches nothing, and declares only what it actually writes -- nothing for a
    # typecheck, `dist/` for a suite whose `pretest` rebuilds the bundle. The write sets
    # are measured rather than assumed: docs/pi/fork-quality-write-sets.md records the run
    # that produced them, and the split is taken only because they are disjoint (board
    # item 405). `after` is what orders a reader that writes nothing behind the prepare
    # step; the write set alone cannot express it.
    for name in sorted(set(FORK_QUALITY) & set(carried)):
        one = carried[name]
        checkout = forks_root(root) / one.fork
        candidate = candidate_root(root) / one.fork
        # The build step already materializes and installs this checkout, so it IS the
        # prepare step when it is planned. A source-only fork has no build step that
        # installs (pi-subagents declares `build:pkg`, never `build`), and --no-build plans
        # no build at all, so those get a prepare step of their own.
        #
        # `prepare_installs` is the cache key's install mode: a reader's verdict depends on
        # how its tree was prepared, and `deps` names a prepare step that installed while
        # `build` names a build step that did.
        #
        # THE PREPARE STEP IS THE PRODUCER, THE READERS' PREFLIGHT IS THE GUARD. The
        # prepare step materializes the series, so it REPAIRS a candidate that is merely
        # out of date; a preflight there would refuse the very staleness it exists to
        # repair. What it cannot repair is a candidate that claims to be this series and
        # is not: `materialize_candidate` reuses a checkout whose recorded series digest
        # matches, whose HEAD is `main` and whose tree is clean, without comparing the
        # tree -- which is exactly the tree board item 194's 216-second run judged. So the
        # preflight runs on the READERS, after the prepare step and before the cache and
        # the script, and refuses a tree that is not the series' (that reuse hole, or a
        # writer that touched the checkout after it was prepared).
        prepare_installs = name in FORK_QUALITY_INSTALLS or name not in build_labels
        prepare = None if name in FORK_QUALITY_INSTALLS else build_labels.get(name)
        if prepare is None:
            prepare = f"fork {one.fork}: prepare the clean checkout at {candidate}"
            plan.checks.append(
                Check("fork-quality", prepare,
                      [str(root / "bin" / "pi-forks"), "candidate", "--fork", one.fork, "--deps"],
                      "its series, declaration, or upstream changed; the quality scripts read the "
                      f"tree this prepares (prepare a missing checkout with bin/pi-forks setup --fork {one.fork})",
                      BUILD_TIMEOUT, watches=(f".forks/{one.fork}/", f"yolo-packs/{one.pack}/"),
                      selected_by=fork_selection(one.fork),
                      needs=(str(checkout),), remedy=fork_setup(one.fork),
                      env=series_env(root),
                      # The one step of a quality run that writes the checkout: it
                      # materializes the series and installs the dependencies. It also
                      # declares the shared checkout, because materializing may fetch the
                      # series base into it (`ensure_base`), the same write the replay step
                      # declares.
                      writes=(str(checkout), str(candidate)),
                      # It materializes the same candidate the readers test, so it records
                      # that tree too: every fork-quality step carries the fingerprint of
                      # the checkout it ran in (board item 396).
                      fingerprint_dir=str(candidate),
                      # Why this execution happened, for the pass's cause breakdown.
                      cause=cause_of.get(one.owner_key, ""))
            )
        for script in FORK_QUALITY[name]:
            quality = ["npm", "run", script]
            plan.checks.append(
                Check("fork-quality", f"fork {one.fork}: npm run {script} in the clean checkout at {candidate}",
                      [str(root / "bin" / "pi-forks"), "candidate", "--fork", one.fork, "--prepared",
                       "--", *quality],
                      "its series, declaration, or upstream changed; build alone does not verify source quality "
                      f"(prepare a missing checkout with bin/pi-forks setup --fork {one.fork})",
                      BUILD_TIMEOUT, watches=(f".forks/{one.fork}/", f"yolo-packs/{one.pack}/"),
                      selected_by=fork_selection(one.fork),
                      needs=(str(checkout),), remedy=fork_setup(one.fork),
                      # The series this reads is THIS tree's, pinned like every other fork
                      # step: an inherited PI_FORKS_SERIES_ROOT must not move a gate run to
                      # another tree's series (2026-10-24).
                      env=series_env(root),
                      after=(prepare,),
                      # Only what the script itself writes in the prepared tree. A
                      # typecheck writes nothing; a suite whose `pretest` rebuilds the
                      # bundle writes that output, and nothing else.
                      writes=tuple(str(candidate / sub)
                                   for sub in FORK_QUALITY_WRITES.get((name, script), ())),
                      # The candidate this step judges, so the preflight can prove it is
                      # the series' tree before the test command runs (board item 194).
                      # The prepare step is the producer and carries no `fork`: a preflight
                      # there would refuse the very staleness it exists to repair (see the
                      # comment above the prepare step).
                      fork=one.fork,
                      # The tree this step tested, recorded so a repeat is countable rather
                      # than inferred (board item 396).
                      fingerprint_dir=str(candidate),
                      # No cache probe: the prepare step is the only writer of this checkout
                      # and it has already materialized the tree, so the cache fingerprints
                      # the prepared checkout directly. A probe that materialized would write
                      # the tree a sibling reader is reading (board item 405). The install
                      # mode is part of the key: `deps` when the prepare step installed,
                      # `build` when a build step did.
                      cache_quality=quality,
                      cache_install="deps" if prepare_installs else "build",
                      reusable=False,
                      # Provenance, never key: two upstreams plus patches that produce the
                      # identical tree share one verdict.
                      cache_provenance={"fork": one.fork, "series": one.owner_key,
                                        "upstream": one.url, "ref": one.ref},
                      # Why this execution happened, for the pass's cause breakdown.
                      cause=cause_of.get(one.owner_key, ""))
            )
    # The caller's tier is applied last, to the plan the full gate would have made: the
    # selection above is the one declaration of what a diff reaches, and a tier only
    # decides which half of it this run carries.
    apply_tier(plan, tier)
    return plan


# ── report ──────────────────────────────────────────────────────────────────────


# Node's spec reporter names a failing case as a `test at <file>:<line>:<col>` line followed
# by the case's `✖ <name> (<duration>)` line, all of it after one `✖ failing tests:` marker.
FAILING_TEST_MARKER = "✖ failing tests:"
FAILING_TEST_AT = re.compile(r"^test at (?P<file>\S+?):\d+:\d+$")
FAILING_TEST_NAME = re.compile(r"^✖ (?P<name>.+?)(?: \([\d.]+ms\))?$")
NODE_FAIL_COUNT = re.compile(r"^ℹ fail (?P<count>\d+)$", re.MULTILINE)


def failing_tests(output: str) -> list[tuple[str, str]] | None:
    """The (file, case name) pairs a suite's output names as its failures, or None.

    None when the transcript cannot be read as a complete list: no `ℹ fail` count, no
    `✖ failing tests:` section, or a count that disagrees with the cases the section names.
    A step that quarantines some cases must not read a truncated or unparsable list as
    "only quarantined cases failed".
    """
    counted = NODE_FAIL_COUNT.search(output)
    if counted is None:
        return None
    _, marker, listed = output.rpartition(FAILING_TEST_MARKER)
    if not marker:
        return None
    named: list[tuple[str, str]] = []
    at = ""
    for raw in listed.splitlines():
        line = raw.strip()
        where = FAILING_TEST_AT.match(line)
        if where is not None:
            at = where.group("file")
            continue
        case = FAILING_TEST_NAME.match(line)
        if case is not None and at:
            named.append((at, case.group("name")))
    if not named or len(named) != int(counted.group("count")):
        return None
    return named


def declared_quarantine(check: Check) -> Quarantine | None:
    """The quarantine declared for this step, whatever its state, or None."""
    return next((one for one in UNANSWERABLE_FORK_QUALITY
                 if check.label.startswith(one.check)), None)


def quarantine_budget_lapse(declared: Quarantine, today: date | None = None) -> str | None:
    """Why D8's budget refuses this declaration, or None while it is within it.

    The budget is counted over the declarations that still hold: a lapsed one has stopped
    suppressing its case, so it is not a slot. Past the budget the declaration is refused
    whole rather than applied to some of its cases, because a partly-applied quarantine is
    exactly the ambiguity the budget exists to avoid.
    """
    live = [one for one in UNANSWERABLE_FORK_QUALITY if one.lapse(today) is None]
    on_this_check = [one for one in live if one.check == declared.check]
    if len(on_this_check) > QUARANTINE_BUDGET_PER_CHECK:
        return (f"the quarantine on {declared.path} is not applied: check {declared.check!r} holds "
                f"{len(on_this_check)} active declarations, past the budget of "
                f"{QUARANTINE_BUDGET_PER_CHECK} per check (agent-runtime section 27.6 D8), so the case "
                f"blocks until one is restored")
    if len(live) > QUARANTINE_BUDGET_REPOSITORY:
        return (f"the quarantine on {declared.path} is not applied: the repository holds {len(live)} "
                f"active declarations, past the budget of {QUARANTINE_BUDGET_REPOSITORY} "
                f"(agent-runtime section 27.6 D8), so the case blocks until one is restored")
    return None


def applied_quarantine(check: Check) -> Quarantine | None:
    """The declaration that suppresses this step's failing cases right now, or None.

    Three conditions, and a step that fails any of them is the red it already is: every
    failing case is one the declaration names (D2's narrowest granularity), the declaration
    has not lapsed (D5's item, D7's expiry), and it is within the budget (D8). This is the
    one read the report, the check status and the evidence record share, so they cannot
    disagree about whether a case is quarantined.
    """
    if check.kind != "fork-quality" or check.rc == 0:
        return None
    declared = declared_quarantine(check)
    if declared is None:
        return None
    failed = failing_tests(check.output)
    if failed is None:
        return None
    # `cases` is a name set, or None for the whole file. The whole-file form exists for a
    # file whose failing case rotates and cannot be named ahead of the run; a declaration
    # that can name its cases does, because that is the narrowest granularity the mechanism
    # supports (docs/design/agent-runtime.md section 27.3).
    if any(file != declared.path or (declared.cases is not None and case not in declared.cases)
           for file, case in failed):
        return None
    if declared.lapse() is not None or quarantine_budget_lapse(declared) is not None:
        return None
    return declared


def fork_quality_skip(check: Check) -> tuple[str, str] | None:
    """The (label, reason) for a fork-quality step whose every failure is quarantined.

    A step is unanswerable when EVERY case it failed is a name declared for it in
    UNANSWERABLE_FORK_QUALITY -- a real-time budget the candidate this gate builds cannot
    meet reliably. A step that passed answers, so it is never a skip; a step that failed
    any case outside the declaration, or whose transcript does not read as a complete list
    of its failures, is a verdict and stays red.

    A declaration that exists and matches but has lapsed -- no item, past its expiry, or
    past the budget -- is NOT a skip: the case blocks again, and the reason is recorded on
    the check's warnings so the red is explained rather than merely reported.
    """
    declared = applied_quarantine(check)
    if declared is not None:
        return (check.label, declared.reason)
    candidate = declared_quarantine(check) if check.kind == "fork-quality" else None
    if candidate is not None and check.rc != 0:
        failed = failing_tests(check.output)
        if failed is not None and all(
                file == candidate.path and (candidate.cases is None or case in candidate.cases)
                for file, case in failed):
            problem = candidate.lapse() or quarantine_budget_lapse(candidate)
            if problem is not None and problem not in check.warnings:
                check.warnings.append(problem)
    return None


def sentinel_skip(check: Check) -> tuple[str, str] | None:
    """The (label, reason) a finished check's own result names as a skip, or None.

    A non-zero exit is not always a verdict about the change: the document checker
    can reach no verdict at all, and the Waybar script exits 77 for a case this machine
    cannot answer (the installed `ai-usagebar` is absent). Both are named skips with the
    reason. This is the one classification the progress stream and the report's skip
    pass read, so a check the report calls a skip cannot stream as a failure.

    A series check is NOT here: a series whose patch-branch stamp is the host's is
    declared elsewhere (`read_series_transcript`) rather than skipped, because the
    pack's other series did answer and their verdict stands.
    """
    if check.kind == "fork-quality":
        return fork_quality_skip(check)
    if check.kind == "waybar" and check.rc == WAYBAR_SKIP_RC:
        return (f"waybar ({' '.join(check.argv)})", waybar_skip_reason(check))
    return None


def read_series_transcript(check: Check) -> None:
    """Split a series check's transcript into declarations and a verdict.

    `yolo pack series check <pack>` reports every series in the pack in one transcript,
    and a series whose `patch_branch` this machine cannot resolve prints
    SERIES_UNRESOLVED among the results for the others -- the record it needs is
    written on the host and never reaches a jail. That is an input DECLARED elsewhere,
    not a verdict about the series, so:

      * each such line becomes a declaration naming the series, with the reason and the
        command the tool itself names;
      * the pack's remaining lines still decide the check -- every series that could be
        applied must have been, and an unrecognised line beside a non-zero exit fails
        closed, so a broken series in the same pack cannot hide behind the declaration;
        a command that exited 0 has already given its own verdict, and a line nobody
        recognises cannot turn that into a pass or a failure by itself; and
      * a check with nothing left but declarations and passes is a PASS, so the run is
        not turned into 'not a verdict' by an input that lives on another machine.
    """
    if check.kind != "series":
        return
    declared: list[str] = []
    unrecognised: list[str] = []
    for raw in check.output.splitlines():
        line = raw.strip()
        if not line:
            continue
        if SERIES_UNRESOLVED in line:
            named = SERIES_STAMP_LINE.match(line)
            declared.append(named.group("series") if named else "")
            continue
        if "takes the series" in line or "merges the patch branch" in line:
            continue
        if line.startswith("checking ") or line.startswith("yolo-jail "):
            continue
        if SERIES_BENIGN_HEADER.match(line):
            continue
        unrecognised.append(line)
    for series in declared:
        check.declared.append(Declared(
            label=f"the patch-branch stamp ({series})" if series else "the patch-branch stamp",
            reason=("the machine-local patch-branch record is absent here (it is written on "
                    "the host and never reaches a jail), so this machine cannot answer for "
                    "this series"),
            command=SERIES_STAMP_COMMAND,
        ))
    if declared and not unrecognised:
        # Everything the machine could answer passed, so the pack's verdict stands and
        # the unanswered series are named beside it rather than voiding it.
        check.rc = 0


def check_status(check: Check) -> str:
    """A check's result, in the one spelling the progress line and the report share."""
    # A check the run stopped before it finished (or before it started) is named
    # not-run, never ok or failed: the verdict was already red when it would have
    # decided anything, and its own result says nothing about the change.
    if check.not_run_reason is not None:
        return "not-run"
    # A cached verdict is never folded into "ok": it is a reused full run on a
    # hash-identical tree, and a reader must be able to tell it from a fresh pass.
    if check.cached:
        return "CACHED"
    # A quarantined case is never folded into "ok": it ran, it failed, and a recorded
    # decision suppressed the failure. The progress line says so in its own word, because
    # a reader watching a run must be able to tell the three apart.
    if applied_quarantine(check) is not None:
        return "QUARANTINED"
    if sentinel_skip(check) is not None:
        return "skip"
    if candidate_stale(check):
        return "STALE"
    return "ok" if check.rc == 0 else ("FAIL" if check.conflict else "ERR")


def external_inputs(plan: Plan) -> None:
    """Name the inputs outside this repository: what is declared, and what is read.

    One line per DECLARED input -- an answer that lives on another machine, with the
    reason this run cannot read it and the command that does -- because a machine-local
    input must not veto a verdict and must not be silently missing from the report
    either. Then the two inputs the plan reads: the installed yolo build and the installed
    node version, both ADVISORY (a different one is reported and plans nothing; the steps
    they decide run when a path they watch changes). Last, the external tools a step may
    reach for, each with the command that covers it: a reader told "not covered" and
    nothing else has no way to cover it.
    """
    for declared in plan.declared:
        print(f"fork-check: {declared.line()}")
    steps = ", ".join(YOLO_DEPENDENT)
    yolo = plan.yolo
    if yolo is None or not yolo.readable:
        why = (yolo.why if yolo is not None and yolo.why else "it was not read")
        print(f"fork-check: the installed yolo build could not be identified ({why}), so a yolo "
              f"upgrade is not reported here: run `yolo --version --full` and compare it with "
              f"the record by hand ({steps}).")
    elif plan.yolo_moved:
        print(f"fork-check: {plan.yolo_moved}")
        print(f"fork-check: reported, not replanned: {steps} run when a path they watch changes, "
              f"so check them by hand after an upgrade.")
    elif not plan.yolo_audited:
        print(f"fork-check: the installed yolo build is {yolo.describe()}; this targeted diagnostic "
              f"does not compare it with the record, and `just done` does ({steps}).")
    else:
        print(f"fork-check: the installed yolo build is {yolo.describe()}; a different one is "
              f"reported here and plans nothing ({steps} run when a path they watch changes).")
    node_steps = ", ".join(NODE_DEPENDENT)
    node = plan.node
    if node is None or not node.readable:
        why = (node.why if node is not None and node.why else "it was not read")
        print(f"fork-check: the installed node version could not be identified ({why}), so a node "
              f"upgrade is not reported here: run `node --version` and compare it "
              f"with the record by hand ({node_steps}).")
    elif plan.node_moved:
        print(f"fork-check: {plan.node_moved}")
        print(f"fork-check: reported, not replanned: {node_steps} run when a path they watch changes, "
              f"so check them by hand after an upgrade.")
    elif not plan.node_audited:
        print(f"fork-check: the installed node version is {node.describe()}; this targeted diagnostic "
              f"does not compare it with the record, and `just done` does ({node_steps}).")
    else:
        print(f"fork-check: the installed node version is {node.describe()}; a different one is "
              f"reported here and plans nothing ({node_steps} run when a path they watch changes).")
    print("fork-check: not visible to this plan, declared rather than pinned, and the "
          "command that covers each: `uvx`/vantage-check (`uvx vantage-check <file>`),")
    print("fork-check:   `go` (`cd tools/<go-tool dir> && go test ./...`, in the directory "
          "whose step did not run), and `npm` "
          "(`bin/pi-forks candidate --deps --fork <fork> -- npm run check`, which also answers "
          "the fork-quality steps through the node it runs under).")


def report(plan: Plan, record: Record, baseline: dict | None, head: str, changed: list[str],
           outcome: str, seconds: float, yolo_missing: bool, series_count: int,
           root: Path, since: str | None = None, excerpt: str | None = None,
           baseline_origin: str | None = None, series: list[Series] | None = None) -> None:
    """Print the plan's result, naming the baseline it was diffed against.

    `baseline` is passed in rather than read from the record: a verified run has
    already advanced the record by the time this prints, so reading it here named HEAD
    as the baseline and printed "0 path(s) changed since it" beside a run that planned
    the changed paths -- and named a baseline a first run never had.
    """
    if since is not None:
        print(f"fork-check: TARGETED DIAGNOSTIC {since}..{head[:12]}, {len(changed)} changed path(s)")
        print("fork-check: NOT completion approval; run `just done` to check all pending changes.")
        print("fork-check: full-audit baseline unchanged; unrelated upstream drift was not checked,"
              " and neither was the installed yolo build nor the installed node version.")
        print("fork-check: run `just done` for accumulated changes and upstream drift.")
        audits = [run for run in record.data["runs"] if run.get("scope", "audit") == "audit"]
        last_audit = record.data.get("last_audit") or (audits[-1] if audits else None)
        # A verified audit is the only outcome this diagnostic leaves standing; a failed or
        # unproven one still stands against the tree.
        if last_audit and last_audit["outcome"] != "verified":
            print(f"fork-check: last full audit remains {last_audit['outcome']}; this run does not clear it.")
    elif baseline is None:
        print("fork-check: no baseline -- no verified commit this history contains, so every "
              "series is checked")
        print(f"fork-check: HEAD {head[:12]}, {series_count} series across the packs")
    else:
        origin = f", {baseline_origin}" if baseline_origin else ""
        print(f"fork-check: baseline {baseline['commit'][:12]} ({baseline.get('at', 'unknown')}{origin}), "
              f"HEAD {head[:12]}, {len(changed)} path(s) changed since it")
    # Which tree's series this run read, named whenever it is not the one holding the
    # checkouts: two checkouts hold the same relative series path, so a transcript that
    # names neither leaves a reader unable to tell which series answered.
    checkouts = forks_root(root)
    if checkouts != root / ".forks":
        print(f"fork-check: series are read from {root} (the tree this run is in); "
              f"fork checkouts from {checkouts}")
    print()
    for check in plan.checks:
        if check.not_run_reason is not None:
            print(f"  not run  {check.kind:<11} {check.label}")
        else:
            print(f"  run  {check.kind:<11} {check.label}")
        print(f"       because: {check.why}")
        print(f"       [{check_status(check)} in {clock(check.seconds)}]")
        if check.cached:
            # A hit is a reused full run on a hash-identical tree, never a fresh pass:
            # the key and the run it came from are printed so a reader can check both.
            hit = check.cache_hit or {}
            print(f"       cached: {check.cache_key} -- recorded {hit.get('at', 'unknown')} "
                  f"at {str(hit.get('commit', 'unknown'))[:12]}")
        # Why it started when it did, when it waited at all. The four causes the latency
        # work names are printed first; an exclusive wait and the heavy admission cap are
        # sub-counts of the pool wait, named in parentheses so a declared barrier or the
        # tuned cap is distinguishable from saturation without being counted twice.
        waited = check.waited
        if any(waited.values()):
            pool = f"{clock(waited['pool'])} pool"
            named = []
            if waited["exclusive"]:
                named.append(f"{clock(waited['exclusive'])} exclusive")
            if waited["heavy"]:
                named.append(f"{clock(waited['heavy'])} heavy")
            if named:
                pool += f" ({', '.join(named)})"
            print(f"       waited: {pool}, {clock(waited['writes'])} writes, "
                  f"{clock(waited['cheap'])} cheap_first, {clock(waited['after'])} after")
        # Why a fork-quality step executed, in the design's cause vocabulary. A cached
        # step still names its cause: the cache removed the execution, not the reason the
        # fork was carried, and a reader counting causes must see both.
        if check.cause:
            print(f"       cause: {check.cause}")
        if check.not_run_reason is not None:
            print(f"       why not: {check.not_run_reason}")
        # The repo-relative paths this check reads, as it declared them. Two readers
        # take this line: the board's breakage record, which is keyed by the failing
        # check and the paths it watches, and the board drain's scoped dispatch barrier,
        # which holds only the items whose declared paths a failing check watches -- so a
        # report that stopped printing them would leave every breakage coarse and every
        # red trunk drain-wide.
        if check.watches:
            print(f"       watches: {', '.join(watched_spelling(check.watches))}")
        # What this check declared it writes, when it declared anything: the run's own
        # account of which steps cannot overlap, so a reader sees why a slow writer held a
        # slot instead of taking that on trust.
        if check.writes:
            print(f"       writes: {', '.join(check.writes)}")
        # A step the plan declared exclusive says so, and why: it held the whole run while
        # it ran, and that is a decision about its assertions rather than a scheduling
        # accident a reader has to guess at.
        if check.exclusive:
            print(f"       exclusive: runs alone -- {check.exclusive_reason}")
        # A warning is news without being a verdict, so it prints beside a passing check
        # too: a file the tree already carries over the budget must be visible on the
        # run that did not add it.
        for line in check.warnings:
            print(f"       warn: {line}")
        if check.failed:
            for line in check.output.strip().splitlines():
                print(f"       | {line}")
    # Owed is counted apart from not-planned: a step the diff selected and this tier
    # deferred is debt, not a step nothing selected (invariant I5).
    owed = [skip for skip in plan.skips if skip.owed]
    not_planned = [skip for skip in plan.skips if not skip.planned and not skip.owed]
    unavailable = [skip for skip in plan.skips if skip.planned and skip.quarantine is None]
    # A quarantine suppresses a FAILING case: the step ran, and a recorded decision
    # suppressed the failure. It is counted apart from the steps that did not run.
    quarantined = [skip for skip in plan.skips if skip.quarantine is not None]
    for skip in plan.skips:
        if skip.quarantine is not None:
            continue
        word = "owed" if skip.owed else ("skip" if skip.planned else "not run")
        print(f"  {word:<16} {skip.label}")
        print(f"       because: {skip.why}")
    # A quarantined case gets its own lines, never a skip line: it ran and it failed, and
    # what suppressed the failure is a recorded decision with a ticket and a deadline. The
    # item and the expiry are printed with it because a quarantine nobody can trace to its
    # repair and its deadline is a deleted test with extra steps (section 27.6 D6).
    for skip in quarantined:
        one = skip.quarantine
        print(f"  {'quarantined':<16} {skip.label}")
        print(f"       case: {one.where()}")
        print(f"       item {one.item}, expires {one.expires.isoformat()} ({one.classification})")
        print(f"       because: {skip.why}")
    print()

    # A verdict and a machine answer are counted apart: a step the gate could not
    # answer is not a red against the change, so it is never listed as one.
    verdicts = [check for check in plan.checks if check.verdict]
    unanswered = [check for check in plan.checks if check.unanswered]
    not_run = [check for check in plan.checks if check.not_run_reason is not None]
    if outcome == "verified":
        verdict = "DIAGNOSTIC PASSED (not completion approval)" if since is not None else "VERIFIED"
        cached = sum(1 for check in plan.checks if check.cached)
        cached_note = f" ({cached} cached)" if cached else ""
        owed_note = f"{len(owed)} owed to the rolling tier, " if owed else ""
        print(f"fork-check: {verdict} -- {len(plan.checks)} check(s) passed{cached_note}, "
              f"{len(not_planned)} gate step(s) did not run (nothing they watch changed), "
              f"{owed_note}"
              f"{len(unavailable)} could not answer here, "
              f"{len(plan.declared)} input(s) {DECLARED_ELSEWHERE} ({clock(seconds)})")
    elif outcome == "dirty":
        print(f"fork-check: REFUSED -- the tree is dirty, so nothing was verified ({clock(seconds)})")
    elif outcome == "failed":
        print(f"fork-check: FAILED -- {len(verdicts)} of {len(plan.checks)} check(s) "
              f"({clock(seconds)}); each failure is diagnosed from its own output, and none "
              f"was re-run")
        for check in verdicts:
            print(f"           {check.kind}: {check.label}")
        if unanswered:
            print(f"fork-check: {len(unanswered)} further check(s) could not answer here "
                  f"and are not counted as red; each is named above as ERR or skip.")
        if not_run:
            print(f"fork-check: {len(not_run)} check(s) did not run because the verdict was "
                  f"already red; each is named above with the check that stopped the run.")
    else:
        print(f"fork-check: NOT VERIFIED -- at least one check reached no verdict ({clock(seconds)})")
    # Where the failure's own words are, for the reader who was not at this terminal: the
    # run entry points here too, so a diagnosis needs no paste.
    if excerpt is not None:
        print(f"fork-check: the failing checks' own output is in {excerpt}")

    if quarantined:
        unanswerable = sum(1 for skip in quarantined
                           if skip.quarantine.classification == QUARANTINE_UNANSWERABLE)
        print(f"fork-check: {len(quarantined)} case(s) quarantined -- {unanswerable} unanswerable, "
              f"{len(quarantined) - unanswerable} flaky or upstream -- each named above with its "
              f"item and expiry; a quarantined case is not a passing one, and an expired "
              f"declaration blocks its case again.")
    if not_planned:
        print(f"fork-check: {len(not_planned)} gate step(s) were not run at all; each is named "
              "above with the reason, and a step that did not run is not a pass.")
    if owed:
        print(f"fork-check: {len(owed)} gate step(s) are owed to the rolling tier; each is "
              "named above, and deferred proof is not a pass.")
    # The pass's own efficiency: which class it judged, what it cost, what it would have
    # cost with only the necessary serializations, and the ratio between them. Printed on
    # every pass, green or red, because the instrument is what says whether a saving
    # survived -- a red pass's scheduling is the same scheduling.
    change = classify_change(root, changed, series or [])
    floor = s1_floor(plan.checks)
    print(efficiency_line(change, seconds, floor))
    external_inputs(plan)
    print("fork-check: never covered here: the launch's own build of the newest upstream")
    if not any(check.kind == "build" for check in plan.checks):
        print("fork-check: build verification did not run (it builds at the series' base; --build forces it)")
    if yolo_missing:
        print("fork-check: `yolo` is not on PATH, so the online series check could not run")


# ── the recorded baseline ───────────────────────────────────────────────────────


class BaselineReach:
    """Where the recorded baseline sits relative to HEAD, in the shapes a reader must
    be told apart.

    A stored sha means nothing until it is compared with the history it is diffed
    against. The record is shared through the common Git directory, so a verified run
    in one worktree advances the baseline to THAT worktree's commit; a second worktree
    then holds a baseline its branch does not contain, and `git diff baseline..HEAD`
    names the paths that differ between two unrelated trees -- none at all when their
    content matches. That is how a landing lane planned 0 paths from a candidate sha
    that never reached main, skipped `documents` and `scratch`, and still read
    VERIFIED. The kinds are kept apart because the next command a reader needs differs:
    no baseline at all is a first run, `elsewhere` is another worktree's commit, and
    `gone` is a candidate that never landed or a history that was rewritten.
    """

    def __init__(self, kind: str, refs: list[str] | None = None):
        self.kind = kind  # "ancestor" | "elsewhere" | "gone" | "absent"
        self.refs = refs or []

    @property
    def usable(self) -> bool:
        return self.kind == "ancestor"

    def cause(self) -> str:
        if self.kind == "elsewhere":
            return f"it is on {', '.join(self.refs[:3])}"
        if self.kind == "gone":
            return ("no branch or tag here contains it, so it is a candidate that never landed "
                    "or a history that was rewritten")
        return "this repository does not have that commit"


def baseline_reach(root: Path, commit: str, head: str) -> BaselineReach:
    """Compare the recorded baseline with HEAD's history, and classify the miss."""
    resolved = subprocess.run(
        ["git", "-C", str(root), "rev-parse", "--verify", f"{commit}^{{commit}}"],
        capture_output=True, text=True,
    )
    if resolved.returncode:
        return BaselineReach("absent")
    ancestor = subprocess.run(
        ["git", "-C", str(root), "merge-base", "--is-ancestor", commit, head],
        capture_output=True, text=True,
    )
    if ancestor.returncode == 0:
        return BaselineReach("ancestor")
    containing = [line.strip() for line in
                  git(root, "for-each-ref", "--contains", commit, "--format=%(refname:short)").splitlines()
                  if line.strip()]
    return BaselineReach("elsewhere", containing) if containing else BaselineReach("gone")


def current_branch(root: Path) -> str | None:
    """The full ref this checkout has checked out, or None on a detached HEAD.

    The baseline is kept per branch, keyed by this ref, so two worktrees of one
    repository -- which git always puts on different branches -- advance two different
    entries. A detached HEAD has no branch to key, so it advances only the shared
    newest-verified entry, which every run then reads as one more candidate.
    """
    result = subprocess.run(
        ["git", "-C", str(root), "symbolic-ref", "--quiet", "HEAD"],
        capture_output=True, text=True, errors="replace",
    )
    ref = result.stdout.strip()
    return ref if result.returncode == 0 and ref.startswith("refs/heads/") else None


def commit_stamp(root: Path, commit: str) -> tuple[int, str] | None:
    """A commit's committer time as (epoch seconds, ISO 8601), or None if unreadable."""
    out = git(root, "show", "-s", "--format=%ct %cI", commit, check=False).strip()
    fields = out.split()
    try:
        return int(fields[0]), (fields[1] if len(fields) > 1 else fields[0])
    except (IndexError, ValueError):
        return None


def merge_base(root: Path, left: str, right: str) -> str | None:
    """The newest commit both contain, or None when they share no history."""
    out = git(root, "merge-base", left, right, check=False).strip()
    return out.splitlines()[0].strip() if out else None


def select_baseline(root: Path, record: Record, branch: str | None,
                    head: str) -> tuple[dict | None, str | None, list[str]]:
    """The commit this run plans from, a phrase naming where it came from, and notes.

    Every entry in the record was written by a verified run, so an entry this history
    CONTAINS is a commit the repository verified, and the diff from it names exactly the
    paths that changed since that verification. Those entries are the first choice, the
    newest one first: a lane branched from the integration branch plans its own change
    against that branch's verified point rather than re-checking the repository, and the
    entry its own branch recorded is preferred by being newer still.

    With no verified commit in this history there is still a safe base, and it is the
    fork point with an entry: the integration branch advanced past the commit
    this branch was cut from, so its verified tree is not this branch's, but everything
    after their common ancestor on this branch is this branch's own unverified work, and
    the diff from that fork point names exactly it. That is what keeps a lane honest
    while it works: without it the run would be a first run, which plans the series steps
    from nothing and never plans the path-gated ones (`documents` above all), so a lane's
    own document would go unchecked behind a VERIFIED headline. Among fork points the
    OLDEST is taken, because a newer one leaves this branch's own earlier commits
    unexamined.

    A branch whose OWN entry is not in its history was rewritten -- a rebase or a reset --
    and then neither choice above is right. Its own entry is the one its runs advanced, so
    the fork point with it is where its rewritten history begins: everything after that
    point is this branch's own new work, and everything before it is shared history that
    was verified under the old commits. Planning from another branch's verified entry
    instead is what a rebased integration branch got wrong: the newest verified commit in
    its history was a lane baseline from three weeks earlier, so the run re-checked
    history the branch never touched and refused on citations the rebase had orphaned.
    The fork point is preferred in that case, and the two choices above are the fallback
    for a rewrite whose fork point is unavailable.

    An entry is skipped when it offers neither: it is another branch's work with no
    common history, or this branch is BEHIND it -- the verified tree is a later one and
    this branch's tree was never judged by it. Skipping is not a refusal, so one
    worktree's verified run can never stop another worktree's run, and nothing is ever
    diffed against a commit this history lacks, so the vacuous plan that read VERIFIED
    over zero changed paths cannot be built at all. A run that finds nothing at all is a
    first run and checks everything.

    A note is returned for the one case a reader must be told: this branch's own entry is
    gone from its own history (a rebase or reset), so the run plans from something else.
    """
    candidates: list[tuple[dict, str]] = []
    seen: set[str] = set()
    for ref, entry in sorted(record.baselines.items()):
        if not isinstance(entry, dict) or not entry.get("commit"):
            continue
        if entry["commit"] in seen:
            continue
        seen.add(entry["commit"])
        candidates.append((entry, ref))
    shared = record.shared_baseline
    if shared is not None and shared["commit"] not in seen:
        seen.add(shared["commit"])
        candidates.append((shared, "the record's shared entry"))

    verified: list[tuple[int, dict, str]] = []
    forks: list[tuple[int, str, str, str]] = []
    for entry, label in candidates:
        commit = entry["commit"]
        if baseline_reach(root, commit, head).usable:
            stamp = commit_stamp(root, commit)
            if stamp is not None:
                verified.append((stamp[0], entry, label))
            continue
        merged = merge_base(root, commit, head)
        if merged is None or merged == head:
            continue
        stamp = commit_stamp(root, merged)
        if stamp is not None:
            forks.append((stamp[0], merged, stamp[1], label))

    chosen: tuple[str, str, dict] | None = None
    own = record.baseline_for(branch)
    own_reach = baseline_reach(root, own["commit"], head) if own is not None else None
    if own_reach is not None and not own_reach.usable:
        # This branch's history was rewritten under its own entry, so the fork point with
        # that entry is where its new history begins (see the docstring).
        merged = merge_base(root, own["commit"], head)
        if merged is not None and merged != head:
            stamp = commit_stamp(root, merged)
            if stamp is not None:
                chosen = (merged, f"the fork point with {branch}",
                          {"commit": merged, "at": stamp[1]})
    if chosen is None and verified:
        _, entry, label = max(verified, key=lambda item: item[0])
        chosen = (entry["commit"], f"recorded on {label}", entry)
    elif chosen is None and forks:
        _, base, at, label = min(forks, key=lambda item: item[0])
        chosen = (base, f"the fork point with {label}", {"commit": base, "at": at})

    notes: list[str] = []
    if own is not None and own_reach is not None and not own_reach.usable:
        replaced = (f"so this run plans from {chosen[0][:12]} instead" if chosen is not None
                    else "no other verified commit or fork point is available either, so this "
                         "run checks everything as a first run would")
        notes.append(
            f"the baseline recorded on {branch} ({own['commit'][:12]}) is not in this "
            f"history ({own_reach.cause()}) -- {replaced}; that entry is the one this branch's "
            f"own runs advanced, so its history was rewritten under it"
        )
    if chosen is None:
        return None, None, notes
    return chosen[2], chosen[1], notes


def prune_baselines(record: Record) -> None:
    """Keep the per-branch map bounded: the newest MAX_BASELINES entries by `at`.

    One entry per branch would otherwise grow with every lane this repository ever ran a
    gate on, and a lane branch is usually deleted after it lands. The cap is generous
    against the number of branches in flight at once, and pruning by the recorded time
    keeps the entries a lane is most likely to plan from.
    """
    entries = record.baselines
    if len(entries) <= MAX_BASELINES:
        return

    def recorded_at(pair: tuple[str, object]) -> str:
        entry = pair[1]
        return str(entry.get("at") or "") if isinstance(entry, dict) else ""

    ordered = sorted(entries.items(), key=recorded_at, reverse=True)
    record.data["baselines"] = dict(ordered[:MAX_BASELINES])


def missing_fork_needs(plan: Plan) -> list[tuple[Check, list[str]]]:
    """Planned fork-dependent checks whose working copy is absent, and what is missing.

    One entry per check with at least one absent need; each need is the path the
    refusal names. Needs resolve through `forks_root()`, so a `PI_FORKS_ROOT` naming a
    repository root whose `.forks/` holds populated checkouts satisfies them from outside
    the checkout.
    """
    found: list[tuple[Check, list[str]]] = []
    for check in plan.checks:
        missing = [need for need in check.needs if not Path(need).exists()]
        if missing:
            found.append((check, missing))
    return found


def needs_refusal(missing: list[tuple[Check, list[str]]], record: Record,
                  head: str, tree: str, changed: list[str], since: str | None) -> int:
    """Say which working copies the planned fork steps need, and what provides them.

    A fork-dependent check without its working copy reaches no verdict: the rank-order
    suite would skip every case and still exit 0, the liveness harness would exit 127,
    and a replay, export, merge or build check has no tree to read. So nothing runs,
    and the report names each missing path with the step that needs it and the command
    that provides it, reusing the REFUSED channel -- the recorded baseline is not
    advanced past a change nothing examined. A managed worktree either runs the setup
    commands or names, in `PI_FORKS_ROOT`, a repository root whose `.forks/` has the
    checkouts.
    """
    ordered: list[str] = []
    steps: dict[str, list[str]] = {}
    remedies: dict[str, str] = {}
    for check, paths in missing:
        for path in paths:
            if path not in steps:
                ordered.append(path)
                steps[path] = []
            steps[path].append(check.kind)
            if check.remedy and path not in remedies:
                remedies[path] = check.remedy
    lines = []
    for path in ordered:
        kinds = ", ".join(steps[path])
        remedy = f"; run {remedies[path]}" if path in remedies else ""
        lines.append(f"{path} (step {kinds}{remedy})")
    print(f"fork-check: REFUSED -- {len(ordered)} working copie(s) planned fork steps need "
          f"are absent, so nothing was verified.")
    for line in lines:
        print(f"           {line}")
    print("fork-check: provide them with the commands above, or point PI_FORKS_ROOT at "
          "a repository root whose .forks/ has them (a worktree reads /workspace/.forks "
          "with PI_FORKS_ROOT=/workspace).")
    record.add_run({"at": now_iso(), "commit": head, "tree": tree, "outcome": "refused",
                    "scope": "targeted" if since is not None else "audit", "since": since,
                    "changed": len(changed), "missing_needs": ordered})
    record.save()
    print("fork-check: NOT VERIFIED -- nothing ran, and the recorded baseline was not advanced.")
    return 2


# ── running a check, and re-running one that failed ─────────────────────────────


def cached_verdict(root: Path, check: Check, cache: VerdictCache,
                   runtime: RuntimeFingerprint | None,
                   env: dict[str, str] | None = None,
                   drop: tuple[str, ...] = (),
                   cancel: "Cancellation | None" = None) -> bool:
    """Report a cached verdict for this fork-quality step, or False to run it.

    The tree is fingerprinted before any npm script runs. A step that declares a
    `cache_probe` materializes the candidate first, WITHOUT the quality command; a step
    that declares none is a READER whose prepare step already materialized the tree, so
    the fingerprint is taken from the prepared checkout directly (board item 405). A probe
    that cannot materialize returns False and the full command runs, so its own output is
    what a reader diagnoses. A missing runtime, a missing checkout, or an unreadable tree
    is a miss, never a hit: a key that cannot be built is not a key.
    """
    if runtime is None or not runtime.readable:
        return False
    if not check.cache_quality:
        return False
    if not Path(check.fingerprint_dir).is_dir():
        return False
    started = time.monotonic()
    if check.cache_probe:
        rc, _, _ = run_command(root, check.cache_probe, check.timeout,
                               {**check.env, **(env or {})}, drop, cancel)
        if rc != 0:
            return False
    tree = materialized_fingerprint(check.fingerprint_dir)
    if tree is None:
        return False
    key = verdict_key(tree, check.cache_quality, check.cache_install, runtime.entry())
    entry = cache.lookup(key)
    if entry is None:
        return False
    check.cached = True
    check.cache_key = key
    check.cache_hit = entry
    check.rc = 0
    check.seconds = round(time.monotonic() - started, 2)
    check.output = (
        f"cached verdict {key}: this tree, command and runtime already passed at "
        f"{entry.get('at')} (commit {entry.get('commit')}); no npm script ran"
    )
    return True


def record_verdict(root: Path, check: Check, cache: VerdictCache,
                   runtime: RuntimeFingerprint | None) -> None:
    """Record a green fork-quality verdict, with the provenance a reader needs."""
    if runtime is None or not runtime.readable:
        return
    if not check.cache_quality:
        return
    tree = materialized_fingerprint(check.fingerprint_dir)
    if tree is None:
        return
    key = verdict_key(tree, check.cache_quality, check.cache_install, runtime.entry())
    provenance = dict(check.cache_provenance or {})
    provenance.update({
        "tree": tree,
        "command": list(check.cache_quality),
        "install": check.cache_install,
        "runtime": runtime.entry(),
    })
    cache.record(key, provenance)


def execute_attempt(root: Path, check: Check, diff_base: str,
                    env: dict[str, str] | None = None,
                    drop: tuple[str, ...] = (),
                    cancel: "Cancellation | None" = None,
                    cache: VerdictCache | None = None,
                    runtime: RuntimeFingerprint | None = None) -> None:
    """Run a check once and record what that one run said.

    One execution of the check's command, or one pass of an in-process guard: `check.rc`,
    `check.output` and `check.seconds` describe it, and `check.started` says where in the
    run it began. There is no second pass to add to them -- a failed check is diagnosed
    from this output (`triage_packet`), not re-run.

    `env` and `drop` are the preflight's: a matrix cell imposes an environment on the
    same check the gate runs, so the runner takes it as an argument rather than the
    preflight re-implementing what each kind of check does. A gate run passes neither.

    `cache` and `runtime` are the gate's content-fingerprint verdict cache (board item
    398). A fork-quality step whose materialized tree, command and runtime match a
    previous green verdict reports that verdict and runs no npm script; a miss runs the
    full command and records a green result. The preflight passes neither, so a
    diagnostic never writes the cache.
    """
    if not check.started:
        check.started = now_iso()
    # A check's own drop list is part of what it judges, so it applies to the gate run
    # and to a preflight cell alike.
    drop = tuple(drop) + check.drop
    check.warnings = []
    # Prove the candidate is the series' tree BEFORE anything else touches it: before the
    # verdict cache, whose key is the HEAD tree (so a dirty candidate would otherwise
    # inherit a clean tree's verdict), and before the step's own command. The driver
    # answers in seconds and runs nothing; a stale candidate is a refusal (2), never a
    # test failure, and the test command is not invoked at all (board item 194). The
    # preflight is a separate process so the driver's own `candidate --check` is the one
    # implementation the gate and a person share.
    preflight_seconds = 0.0
    if check.kind == "fork-quality" and check.fork:
        rc, out, preflight_seconds = run_command(
            root, [str(root / "bin" / "pi-forks"), "candidate", "--check", "--fork", check.fork],
            CANDIDATE_CHECK_TIMEOUT, {**check.env, **(env or {})}, drop, cancel)
        if rc != 0:
            # 2 is the gate's refusal code whatever the driver's own status was: a
            # preflight that could not answer (a missing checkout, an unreadable series)
            # also judged nothing, and must not become a red about the change.
            check.preflight_refused = True
            check.rc = STALE_CANDIDATE_RC
            check.output = out
            check.seconds = round(preflight_seconds, 2)
            return
    if check.kind == "fork-quality" and cache is not None:
        if cached_verdict(root, check, cache, runtime, env, drop, cancel):
            check.seconds = round(check.seconds + preflight_seconds, 2)
            return
    if check.kind == "file-size":
        pass_started = time.monotonic()
        warnings = typescript_size_findings(root)
        refusals = []
        check.warnings = warnings
        check.rc = 1 if refusals else 0
        check.output = "\n".join(refusals)
        seconds = time.monotonic() - pass_started
    else:
        check.rc, check.output, seconds = run_command(
            root, check.argv, check.timeout, {**check.env, **(env or {})}, drop, cancel)
    # A stopped process is not a verdict. The caller marks the check not-run; setting rc
    # to 0 here keeps a kill signal out of the triage packet and out of the exit status.
    if cancel is not None and cancel.cancelled:
        check.was_cancelled = True
        check.rc = 0
    check.seconds = round(seconds + preflight_seconds, 2)
    # A green fork-quality run is a verdict about the tree it tested, so it is recorded
    # for the next pass at the same tree, command and runtime. A red one is never cached.
    if check.kind == "fork-quality" and cache is not None and check.rc == 0 \
            and not check.was_cancelled:
        record_verdict(root, check, cache, runtime)


# ── main ────────────────────────────────────────────────────────────────────────


def verdict_of(checks: list[Check]) -> tuple[str, int]:
    """Classify tree checks: content conflict, unavailable judgment, or verified."""
    failed = [check for check in checks if check.failed]
    if any(check.conflict for check in failed):
        return "failed", 1
    if failed:
        return "unproven", 2
    return "verified", 0


class PlanningRefusal(Exception):
    """A run that cannot build a plan at all, with the reason a reader can act on.

    Raised from `plan_gate`, which both a gate run and a preflight call: the two share
    every input the plan is made from, so an argument or a commit that cannot be read
    refuses both identically, with exit 2 (nothing ran, so there is no verdict).
    """


class GatePlan:
    """Everything planning decided, in one object a run or a preflight reads.

    The gate and the preflight must ask the SAME question -- which checks this change
    affects -- or a preflight would drift from the thing it exists to pre-empt. So the
    planning half of a gate run lives in `plan_gate`, and this is what it returns: the
    record and branch it read, the commit and tree it judged, the paths the diff named,
    the plan, and the three decisions the dirty and missing-checkout states produce.

    Nothing here writes anything. A gate run records its outcome afterwards, and a
    preflight records none at all.
    """

    def __init__(self) -> None:
        self.root: Path
        self.common_dir: Path
        self.branch: str | None = None
        self.record: Record
        self.head = ""
        self.tree = ""
        self.dirty: dict[str, tuple[int, int] | None] = {}
        self.series: list[Series] = []
        self.changed: list[str] = []
        self.diff_base: str | None = None
        self.since: str | None = None
        self.baseline: dict | None = None
        self.baseline_origin = ""
        self.plan: Plan
        self.watched: set[str] = set()
        self.changed_while_planning: list[str] = []
        self.blocking: list[str] = []
        self.ignored: list[str] = []
        self.missing: list[tuple[Check, list[str]]] = []
        self.yolo_missing = False
        self.yolo: YoloBuild | None = None
        self.node: NodeVersion | None = None
        self.started = 0.0
        self.started_at = ""


def plan_gate(root: Path, options) -> GatePlan:
    """Build the plan one gate run would make, and read nothing else.

    `options` is the parsed gate command line: `all`, `since` and `build` decide what
    the plan asks. This function reads the record and never writes it, so a preflight
    can call it beside a running gate.
    """
    gate = GatePlan()
    gate.root = root
    common = git(root, "rev-parse", "--git-common-dir").strip()
    gate.common_dir = Path(common) if os.path.isabs(common) else (root / common).resolve()
    gate.branch = branch = current_branch(root)
    record = Record(record_log_path(gate.common_dir), branch)
    gate.record = record

    head = git(root, "rev-parse", "HEAD").strip()
    gate.head = head
    tree = git(root, "rev-parse", "HEAD^{tree}").strip()
    gate.tree = tree
    dirty = dirty_state(root)
    gate.dirty = dirty

    series = read_series(root)
    gate.series = series
    force_all: str | None = None
    if options.all:
        force_all = "--all was given"
    changed: list[str] = []
    # The commit `changed` is diffed against, when there is one: the unreachable-citation
    # guard reads the lines the range ADDS, so it needs the same base the diff came from.
    diff_base: str | None = None
    # The commit this run plans from: the newest verified entry this history contains,
    # whatever branch recorded it. Set on the record as well, because the plan's own
    # yolo/node comparisons read it there.
    baseline, baseline_origin, baseline_notes = select_baseline(root, record, branch, head)
    record.planning_baseline = baseline
    gate.baseline = baseline
    gate.baseline_origin = baseline_origin
    since = options.since if not options.all else None
    gate.since = since
    # The landing path's batch base (PLAN_BASE_ENV). A landing has already assembled the
    # commit its gate judges, so the diff that decides which checks run is the batch's,
    # not whatever verified baseline this scratch worktree's history happens to offer. A
    # plan taken from a different base can omit a check whose path the batch's diff
    # reaches -- a red test then lands unseen -- so the landing names the base and this
    # run plans from exactly it. It replaces only the DIFF: unlike `--since`, the record
    # baseline, the upstream and input audits and the run's scope are unchanged, because
    # this is still the landing's verdict rather than a range diagnostic.
    landed_base = "" if options.all or since is not None else os.environ.get(PLAN_BASE_ENV, "").strip()
    if options.all and getattr(options, "tier", None) == TIER_ROLLING:
        # The drain has no watermark yet: audit the tracked tree, not the gate's
        # remembered diff. force_all alone only forces fork series; path-selected
        # rolling suites otherwise disappear when the baseline already equals HEAD.
        changed = [path for path in git(root, "ls-files", "-z").split("\0") if path]
    elif since is not None:
        resolved = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "--verify", f"{since}^{{commit}}"],
            capture_output=True, text=True,
        )
        if resolved.returncode:
            raise PlanningRefusal(f"cannot resolve --since {since!r} to a commit")
        base = resolved.stdout.strip()
        ancestor = subprocess.run(
            ["git", "-C", str(root), "merge-base", "--is-ancestor", base, head],
            capture_output=True, text=True,
        )
        if ancestor.returncode:
            raise PlanningRefusal(f"--since {since!r} is not an ancestor of HEAD")
        diff_base = base
        changed = [path for path in
                   git(root, "diff", "--name-only", "--no-renames", "-z", base, head).split("\0")
                   if path]
    elif landed_base:
        resolved = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "--verify", f"{landed_base}^{{commit}}"],
            capture_output=True, text=True,
        )
        if resolved.returncode:
            raise PlanningRefusal(f"cannot resolve {PLAN_BASE_ENV}={landed_base!r} to a commit")
        base = resolved.stdout.strip()
        ancestor = subprocess.run(
            ["git", "-C", str(root), "merge-base", "--is-ancestor", base, head],
            capture_output=True, text=True,
        )
        if ancestor.returncode:
            raise PlanningRefusal(f"{PLAN_BASE_ENV}={landed_base!r} is not an ancestor of HEAD")
        diff_base = base
        changed = [path for path in
                   git(root, "diff", "--name-only", "--no-renames", "-z", base, head).split("\0")
                   if path]
    elif baseline is None:
        force_all = force_all or ("no verified baseline this history contains, and the first run "
                                  "on it checks everything")
    elif baseline.get("tree") == tree:
        # Nothing changed in content: the tree this run judges is the one the verified run
        # judged. The base is still NAMED, because a plan that printed `-` here would read
        # as a full plan: `-` means "no diff base, everything planned", and this run plans
        # only the integrity set. A consumer checking coverage then compares this commit
        # against its batch, which is the answer it needs.
        diff_base = baseline["commit"]
        changed = []
    else:
        # `select_baseline` only returns a commit this history contains, so this diff always
        # names the paths that changed since a verified commit or this branch's fork point.
        diff_base = baseline["commit"]
        changed = [path for path in
                   git(root, "diff", "--name-only", "--no-renames", "-z", baseline["commit"], head).split("\0")
                   if path]
    gate.changed = changed
    gate.diff_base = diff_base
    if since is None:
        # A --since diagnostic plans from the range it was given, so what the record's
        # baseline is doing is not news for it.
        for note in baseline_notes:
            print(f"fork-check: {note}")

    yolo_missing = shutil.which("yolo") is None
    gate.yolo_missing = yolo_missing
    # Which build is answering, read once: the plan compares it with the identity the
    # last verified run recorded, and the report names it.
    yolo_build = read_yolo_build(root)
    # Which node is answering, read once: the plan compares it with the version the last
    # verified run recorded, and the report names it.
    node_version = read_node_version(root)
    gate.yolo = yolo_build
    gate.node = node_version
    started = time.monotonic()
    started_at = now_iso()
    gate.started = started
    gate.started_at = started_at
    progress(f"fork-check: planning {len(changed)} changed path(s) against {len(series)} series")
    with heartbeat("the plan"):
        plan = build_plan(root, series, record, changed, force_all, options.build,
                          audit_upstreams=since is None, yolo=yolo_build,
                          audit_yolo=since is None, node=node_version,
                          audit_node=since is None, base=diff_base,
                          # The preflight's parser has no `--tier`, and a preflight is a
                          # diagnostic of the full gate, so an absent attribute is the
                          # full gate rather than an error.
                          tier=getattr(options, "tier", None))
    # A re-gate on a moved tip is told which checks the caller already holds a result
    # for. They are removed before anything runs, so the run buys only what the landed
    # paths can reach, and the plan line names them in `reused`.
    apply_reuse(plan, reuse_selectors())
    # The heavy phase is ordered longest-first by what each check actually cost last time.
    # The timeline is machine-local instrumentation, so a missing one is no history rather
    # than a refusal: the plan then keeps plan order, which is what it did before.
    attach_history(plan, record)
    progress(f"fork-check: plan ready -- {len(plan.checks)} check(s), "
             f"{sum(1 for skip in plan.skips if not skip.planned)} not planned, "
             f"{sum(1 for skip in plan.skips if skip.planned)} skip(s)")
    gate.plan = plan

    watched = {path for check in [*plan.checks, *plan.reused] for path in check.watches}
    gate.watched = watched
    # The plan issues the eight upstream `ls-remote` calls, so it can take seconds, and
    # a write landing in that window is absent from the snapshot above: the refusal
    # decision would then judge a tree no snapshot described. Re-read the tree now --
    # the moment before that decision, and before any check reads a file -- and refuse
    # if a watched path's state differs from the pre-plan snapshot, because the plan
    # read those bytes to choose the checks and so no longer describes this tree.
    dirty_now = dirty_state(root)
    changed_while_planning = sorted(
        path for path in set(dirty) | set(dirty_now)
        if watched_path(path, watched) and dirty.get(path) != dirty_now.get(path)
    )
    blocking = [path for path in sorted(dirty_now) if watched_path(path, watched)]
    blocking_set = set(blocking)
    ignored = [path for path in sorted(dirty_now) if path not in blocking_set]
    missing = missing_fork_needs(plan)
    gate.changed_while_planning = changed_while_planning
    gate.blocking = blocking
    gate.ignored = ignored
    gate.missing = missing
    return gate


# The checks whose shape depends on the change, and the declaration each one is. A check
# not named here is static: its declaration's `selects` are its watches and its command is
# its argv, so a planner computes the same instance for any change. Keyed by
# (kind, index among the plan's checks of that kind).
#
# This table is the one place a declaration is not derived from the instance, so the export
# verifies it: it recomputes the instance from the declarations and refuses if the result
# differs from the plan the gate itself built. A pattern that stopped selecting its check,
# or a command template that stopped reproducing its argv, is a refusal rather than a
# registry a planner would silently diverge from.
DOCUMENT_SELECTS = [{"regex": r".*\.md$", "exclude_dirs": sorted(DOCUMENT_EXCLUDED),
                     "must_exist": True}]
CHANGE_DEPENDENT_DECLARATIONS = {
    ("storybook", 0): {
        "id": "storybook/roster",
        "label": "regenerate the pi-subagents roster storybook",
        "selects": [{"regex": pattern.pattern} for pattern in STORYBOOK_INPUT],
        "watches": [{"paths": True}],
    },
    ("storybook", 1): {
        "id": "storybook/footer",
        "label": "regenerate the pi-archimedes footer storybook",
        "selects": [{"regex": pattern.pattern} for pattern in FOOTER_STORYBOOK_INPUT],
        "watches": [{"paths": True}],
    },
}


def declaration_id(kind: str, label: str, root: Path) -> str:
    """A check's stable identifier: its kind and a slug of its label, never a count or a path.

    The label is a human sentence and may carry a count or this machine's absolute root, so
    the two volatile parts the export can identify exactly are templated before the slug is
    taken. A verdict names a check by this id, so an id that moved with a document count
    would make two verdicts over the same check incomparable.
    """
    text = label.replace(str(root), "{root}")
    text = re.sub(rf"run-{os.getpid()}(?!\d)", "run-<pid>", text)
    return f"{kind}/{re.sub(r'[^a-z0-9]+', '-', text.lower()).strip('-')}"


def watch_patterns(watches) -> list[dict]:
    """A check's watches as declaration patterns: a path, a directory prefix, or a regex."""
    return [{"regex": watch.pattern} if isinstance(watch, re.Pattern) else {"path": str(watch)}
            for watch in watches]


def pattern_selects(pattern: dict, path: str, root: Path) -> bool:
    """Does one changed path match one declaration pattern?

    A `path` is the gate's own watch rule: the path itself, or anything under it when it
    ends in `/`. A `regex` is a compiled input pattern, with the two refinements the
    document predicate adds: a path part it excludes, and a file that must still exist.
    """
    if "regex" in pattern:
        if re.match(pattern["regex"], path) is None:
            return False
        if any(part in pattern.get("exclude_dirs", ()) for part in Path(path).parts):
            return False
        return not pattern.get("must_exist") or (root / path).is_file()
    watch = pattern["path"]
    return path == watch or (watch.endswith("/") and path.startswith(watch))


def common_directory(paths: list[str]) -> str:
    """The deepest directory every one of these paths is under, with its trailing slash."""
    parts = [path.split("/") for path in paths]
    shared: list[str] = []
    for column in zip(*parts):
        if len(set(column)) != 1:
            break
        shared.append(column[0])
    # The last shared part is a file name only when the shortest path ends there; when the
    # paths diverge below it, it is a directory they all sit in.
    if len(shared) == min(len(part) for part in parts):
        shared = shared[:-1]
    return "/".join(shared) + "/"


def selection_patterns(check: "Check") -> list[dict]:
    """A check's selection as patterns: its watches narrowed to where its selection is.

    A check that declares `selected_by` selects by something its watches do not spell. A
    fork step watches its whole pack, because a pack's manifest can move every series in
    it, while the gate selects it by the series that changed -- so the watch is narrowed to
    the deepest directory the selected paths share, and a path the check selects that no
    watch names is carried as itself. A watch that names a file is carried as that file: it
    has no directory to narrow to.

    Narrowing rather than listing the selected paths is what keeps a file added after the
    export selected: a plan is computed for a change the export never saw.
    """
    if check.selected_by is None:
        return watch_patterns(check.watches)
    selected = list(dict.fromkeys(check.selected_by))
    patterns: list[dict] = []
    claimed: set[str] = set()
    for watch in check.watches:
        if isinstance(watch, re.Pattern):
            continue
        group = [path for path in selected if watched_path(path, {str(watch)})]
        if not group:
            continue
        claimed.update(group)
        patterns.append({"path": str(watch) if not str(watch).endswith("/")
                         else common_directory(group)})
    for path in selected:
        if path not in claimed:
            patterns.append({"path": path})
    return patterns


def declaration_of(check: "Check", index: int, root: Path, changed: list[str]) -> dict:
    """One check's declaration: what selects it, and the command and watches it computes.

    `selects` is what plans the check, and it is the gate's own selection rather than the
    check's watches: a fork step watches `.forks/<name>/`, which is gitignored and never
    appears in a diff, and it watches its whole pack, which is wider than the series that
    selects it. `selected_by` names that selection where the check declares one its watches
    do not express, so a reader can see what the patterns stand for; it is empty for a
    check whose selection is its watches, which is most of them. `watches` is what the
    check reads, which is the same set for a static check and the selected paths for a
    diff-scoped one. `{paths}` in a command is replaced by the selected paths, so a planner
    computes the argv for its own change rather than replaying the one this export happened
    to build.
    """
    pid = re.compile(rf"run-{os.getpid()}(?!\d)")
    decl = {
        "id": declaration_id(check.kind, check.label, root),
        "kind": check.kind,
        "label": check.label,
        "selects": selection_patterns(check),
        "selected_by": list(check.selected_by) if check.selected_by is not None else [],
        "command": list(check.argv),
        "watches": watch_patterns(check.watches),
        "writes": [pid.sub("run-<pid>", path) for path in check.writes],
        "after": list(check.after),
        "timeout_ms": int(check.timeout * 1000),
        "forced": check.forced or ("always" if check.why == "always" else ""),
        "env": {key: pid.sub("run-<pid>", value) for key, value in check.env.items()},
        "drop": list(check.drop),
        "needs": list(check.needs),
        "tier": CHECK_TIER.get(check.kind, ""),
        "exclusive": check.exclusive,
        "exclusive_reason": check.exclusive_reason,
    }
    decl.update(CHANGE_DEPENDENT_DECLARATIONS.get((check.kind, index), {}))
    return decl


def declaration_instance(decl: dict, changed: list[str], root: Path) -> tuple[list[str], list[str]]:
    """The argv and watches a declaration computes for a changed-path set."""
    selected = [path for path in changed
                if any(pattern_selects(pattern, path, root) for pattern in decl["selects"])]
    argv: list[str] = []
    for part in decl["command"]:
        argv.extend(selected if part == "{paths}" else [part])
    watches: list[str] = []
    for watch in decl["watches"]:
        watches.extend(selected if watch.get("paths") else [watch.get("path", watch.get("regex"))])
    return argv, watches


def declaration_mismatch(decl: dict, check: "Check", changed: list[str], root: Path,
                         index: int) -> str:
    """The first field in which a declaration fails to reproduce the check the gate plans.

    Every field is compared, not only the argv and the watches: `env`, `drop`, `needs`,
    `tier`, exclusivity, `writes`, `after`, `timeout_ms`, `forced`, `id` and `label` are
    what a runner needs to reproduce the gate, so a difference in any of them is a refusal
    rather than a divergence a planner would discover by running the wrong checks. The two
    per-run values are compared in the normalised form the export writes.

    The fields `CHANGE_DEPENDENT_DECLARATIONS` overrides are the exception, because they
    are hand-written rather than derived: `id` and `label` there are templates of the
    gate's own sentence, and `selects`/`command`/`watches` are the patterns and the argv
    template. Those are verified by the instance comparison below and by the probe change
    set, which is what a template can be checked against; comparing them to the gate's
    expanded form would only restate the override.
    """
    pid = re.compile(rf"run-{os.getpid()}(?!\d)")
    argv, watches = declaration_instance(decl, changed, root)
    if argv != list(check.argv):
        return (f"argv {len(argv)} vs {len(check.argv)} element(s); first difference "
                f"{first_difference(argv, list(check.argv))}")
    if watches != list(check.watches):
        return (f"watches {len(watches)} vs {len(check.watches)}; first difference "
                f"{first_difference(watches, list(check.watches))}")
    want = {
        "id": declaration_id(check.kind, check.label, root),
        "kind": check.kind,
        "label": check.label,
        "timeout_ms": int(check.timeout * 1000),
        "forced": check.forced or ("always" if check.why == "always" else ""),
        "drop": list(check.drop),
        "needs": list(check.needs),
        "tier": CHECK_TIER.get(check.kind, ""),
        "exclusive": check.exclusive,
        "exclusive_reason": check.exclusive_reason,
        "after": list(check.after),
        "writes": [pid.sub("run-<pid>", path) for path in check.writes],
        "env": {key: pid.sub("run-<pid>", value) for key, value in check.env.items()},
    }
    overridden = CHANGE_DEPENDENT_DECLARATIONS.get((check.kind, index), {})
    for field, value in want.items():
        if field in overridden:
            continue
        if decl[field] != value:
            return f"{field} {decl[field]!r} vs {value!r}"
    return ""


def first_difference(got: list, want: list) -> str:
    """The first place two lists disagree, for a refusal that names the defect and not a dump."""
    for index, (left, right) in enumerate(zip(got, want)):
        if left != right:
            return f"at {index}: {left!r} vs {right!r}"
    return "" if len(got) == len(want) else f"at {min(len(got), len(want))}: one list ends"


def registry_json(root: Path) -> int:
    """Print the check registry as data: every check the gate can plan, and nothing run.

    The registry is what a verdict binds (`registry_digest`) and what the Go planner reads
    instead of this file, so it must name every check this gate plans. It is a set of check
    *declarations*, not one plan instance: each names the patterns that select it, the
    command and watches it computes from the selected paths, and the fields a plan instance
    carries but a command list cannot (`env`, `drop`, `needs`, `tier`, exclusivity). A
    planner given a change computes its own instance from these; replaying the instance this
    export built would run the `documents` check over every tracked document whatever the
    change, and would select nothing for a file added after the export.

    The declarations are derived from a plan built over a synthetic all-changed path set --
    every tracked path -- because a plan built from one diff names only the checks that diff
    selected. Nothing is refreshed and nothing runs: the upstream audit is off, so no fork
    is fetched, and the record is not read, so the answer is a property of the tree rather
    than of this machine's history.

    A check with no argv is this file's own code rather than a command, so it is not
    something a registry of commands can name. It is not dropped: `in_process` names each
    one by its stable `id`, and carries the same `selects` and `watches` a command check
    does, so a planner can say which of them a change owes. The integrity set is planned
    every run rather than selected by a path, and `forced` is where a check declares what
    selects it when no changed path does, so its `always` is carried there.

    A declared write path that names this process is a per-run scratch base rather than a
    property of the tree, so its pid is written as `<pid>`: the registry is committed and
    diffed, and a digest that moved with the process id would never be current.
    """
    series = read_series(root)
    changed = [path for path in git(root, "ls-files", "-z").split("\0") if path]
    plan = build_plan(root, series, Record(Path(os.devnull), None), changed, None, True,
                      audit_upstreams=False, audit_yolo=False, audit_node=False, base=None)
    seen: dict[str, int] = {}
    declarations = []
    keys: list[tuple[str, int]] = []
    decl_by_key: dict[tuple[str, int], dict] = {}
    for check in plan.checks:
        index = seen.get(check.kind, 0)
        seen[check.kind] = index + 1
        decl = declaration_of(check, index, root, changed)
        declarations.append(decl)
        keys.append((check.kind, index))
        decl_by_key[(check.kind, index)] = decl
    # The declarations are only worth a planner's trust if they reproduce the plan the gate
    # itself built, so recompute the instance from them and refuse on any difference. This
    # is what makes the one hand-written table above safe: a pattern that stopped selecting
    # its check, or a command template that stopped reproducing its argv, is a refusal here
    # rather than a divergence a planner would discover by running the wrong checks.
    for check, decl, key in zip(plan.checks, declarations, keys):
        mismatch = declaration_mismatch(decl, check, changed, root, key[1])
        if mismatch:
            print(f"fork-check: the declaration for {decl['id']} does not reproduce the "
                  f"check the gate plans: {mismatch}", file=sys.stderr)
            return 2
    # `selects` is what plans the check, so it must select exactly the paths the gate's own
    # rule selects -- not the check's watches, which are wider for every step that declares
    # its own selection. A fork step watches its whole pack while the gate selects it by the
    # series that changed, so a `selects` copied from the watches plans the step for a file
    # the gate would not run it for. The comparison is on the matched set rather than on the
    # patterns, because a pattern set has no canonical spelling.
    # The comparison is worth nothing for a declaration whose `selects` are its watches --
    # that is the same predicate twice -- and nothing for the integrity set, which is
    # planned by its `forced` flag and answers no selecting path at all. The two that need
    # it are the checks that declare their own selection, and the hand-written table.
    subset = changed[::7]
    for key, check, decl in zip(keys, plan.checks, declarations):
        if check.kind in INTEGRITY_CHECKS or check.forced:
            continue
        if check.selected_by is None and key not in CHANGE_DEPENDENT_DECLARATIONS:
            continue
        # A check that declares its own selection is compared on the whole change set: its
        # patterns are paths, and the gate's answer is the paths this export produced. The
        # hand-written table is compared on a subset instead, because the all-changed
        # instance cannot see a pattern that matches every tracked path and diverges on a
        # subset -- and because its patterns carry `must_exist`, which costs a stat per
        # candidate path.
        where, instance = (("", changed) if check.selected_by is not None
                           else (" of a subset", subset))
        # A set, not a list: the gate's own selection can name one path twice (a manifest
        # path that is also a mechanism path), and a pattern set cannot.
        got = sorted({path for path in instance
                      if any(pattern_selects(pattern, path, root) for pattern in decl["selects"])})
        want = sorted(set(selection_paths(check, instance)))
        if got != want:
            print(f"fork-check: the declaration for {decl['id']} selects {len(got)} "
                  f"path(s){where} where the gate selects {len(want)}; first difference "
                  f"{first_difference(got, want)}", file=sys.stderr)
            return 2
    command_checks = [decl for decl in declarations if decl["command"]]
    in_process = [{"id": decl["id"], "kind": decl["kind"], "label": decl["label"],
                   "selects": decl["selects"], "watches": decl["watches"]}
                  for decl in declarations if not decl["command"]]
    # The digest is a function of the declarations and the in-process checks, and nothing
    # else, so a reader can recompute it from the file: both are written in one canonical
    # form and hashed as those exact bytes. The in-process checks are hashed too because
    # they are what a verdict owes for a change no command can spell, and a digest over the
    # command declarations alone would let an edit to one of their selections move what a
    # verdict owes without moving the digest.
    body = json.dumps(command_checks, separators=(",", ":"), sort_keys=True)
    in_proc_body = json.dumps(in_process, separators=(",", ":"), sort_keys=True)
    digest = hashlib.sha256((body + in_proc_body).encode()).hexdigest()
    print('{\n  "digest": ' + json.dumps(digest) + ',\n  "declarations": ' + body +
          ',\n  "in_process": ' + in_proc_body + '\n}')
    return 0


def write_gate_spans(path: str, plan: "Plan", root: Path, started_ns: int,
                     outcome: str, head: str, tree: str, changed: list[str],
                     peak: int, jobs: int, wall: float) -> None:
    """Write one run's check spans for the drain's `gate_spans` table.

    The shape is the table's: a plan span, one span per EXECUTED check, the steps the run
    did not put a verdict on, and a summary. `check_spans` is keyed by the check's
    `declaration_id`, which is the id a verdict names, so the drain can compute
    `hash(verdict_id || '/' || check_id)` without a second naming scheme. `skipped_checks`
    is deliberately NOT rows: a landed gate skips ~22 steps, and writing one not-run row
    per skip would make the table mostly silence. It is the file's account of what the run
    did not ask, and the drain's table keeps only the spans that ran.

    `waited_ms_json` and `attributes_json` are JSON strings, not objects: the table has one
    column each and a consumer parses them at the edge, which keeps a span's row legible in
    `sqlite3` without a schema per kind.

    The write is atomic (temp file plus `os.replace`) so a reader never sees half a span
    set. The file is optional: a run that writes none still records its verdict, and the
    drain degrades to zero rows and says so.
    """
    if not path:
        return

    def waited_ms(check: "Check") -> str:
        return json.dumps({key: round(value * 1000.0, 3)
                           for key, value in check.waited.items()}, sort_keys=True)

    check_spans: list[dict] = []
    for check in plan.checks:
        if not check.start_ns:
            continue
        attributes = {
            "rc": check.rc,
            "seconds": round(check.seconds, 3),
            "cached": check.cached,
            "exclusive": check.exclusive,
            "command": check.command_line(),
            "selected_by": list(check.selected_by) if check.selected_by is not None else [],
            "writes": list(check.writes),
            "after": list(check.after),
        }
        if check.cause:
            attributes["cause"] = check.cause
        if check.fingerprint_dir:
            attributes["fingerprint_dir"] = check.fingerprint_dir
        check_spans.append({
            "check_id": declaration_id(check.kind, check.label, root),
            "kind": check.kind,
            "label": check.label,
            "status": check_status(check),
            "start_ns": check.start_ns,
            "end_ns": check.end_ns or check.start_ns,
            "waited_ms_json": waited_ms(check),
            "attributes_json": json.dumps(attributes, sort_keys=True),
        })
    skipped = [{
        "check_id": declaration_id(skip.kind, skip.label, root) if skip.kind else "",
        "kind": skip.kind,
        "label": skip.label,
        "reason": skip.why,
        "planned": skip.planned,
        "owed": skip.owed,
    } for skip in plan.skips]
    ended_ns = time.time_ns()
    payload = {
        "plan_span": {
            "check_id": "plan",
            "kind": "plan",
            "label": "gate plan",
            "status": outcome,
            "start_ns": started_ns,
            "end_ns": ended_ns,
            "waited_ms_json": "{}",
            "attributes_json": json.dumps({
                "head": head, "tree": tree, "changed": len(changed),
                "tier": plan.tier, "jobs": jobs, "peak_concurrency": peak,
                "wall_seconds": round(wall, 3),
            }, sort_keys=True),
        },
        "check_spans": check_spans,
        "skipped_checks": skipped,
        "summary": {
            "checks": len(plan.checks),
            "executed": len(check_spans),
            "skipped": len(skipped),
            "outcome": outcome,
            "wall_seconds": round(wall, 3),
        },
    }
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    # One temp name per writer. A fixed `<name>.tmp` lets two runs aimed at one path -- a
    # nested gate inheriting TILLR_GATE_SPANS_OUT from the drain, or two shells given the
    # same -- interleave so that the first `os.replace` consumes the file the second was
    # about to rename, and the loser dies with FileNotFoundError on a span file it did not
    # need to share. The rename stays atomic; only the name is made unique.
    temporary = target.with_name(
        f"{target.name}.{os.getpid()}.{uuid.uuid4().hex}.tmp")
    temporary.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.replace(temporary, target)


def run_gate(argv: list[str]) -> int:
    started_ns = time.time_ns()
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--all", action="store_true", help="full audit of accumulated changes and upstream drift")
    parser.add_argument("--since", metavar="BASE", help="range diagnostic only, not completion approval; --all overrides this")
    parser.add_argument("--build", dest="build", action="store_true", default=True,
                        help="build each fork whose series changed (the default)")
    parser.add_argument("--no-build", dest="build", action="store_false", help="skip build verification")
    parser.add_argument("--explain", action="store_true",
                        help="print the plan and run none of it (it still refreshes each fork's "
                             "upstream refs, so the upstream named is the one a launch would build)")
    parser.add_argument("--tier", choices=(TIER_LANDING, TIER_ROLLING), default=None,
                        help="plan one tier of the gate: `landing` keeps the pre-publish "
                             "kinds and records each rolling kind the diff selects as owed "
                             "to the rolling tier; `rolling` plans the deferred kinds. "
                             "Absent is the full gate, which is what the merge queue runs")
    parser.add_argument("--timings", action="store_true",
                        help="print every check's duration, not only the slowest few")
    # Both record-lock flags are ACCEPTED AND IGNORED: the record is append-only, so
    # there is no lock to wait for. They stay because callers outside this file still
    # pass them -- the rolling program and the fixer gate pass `--lock-timeout 30`, and
    # the dual-run diagnostic passes `--no-wait` -- and a flag a caller passes that this
    # parser does not declare would refuse the run before it checked anything.
    parser.add_argument("--lock-timeout", metavar="SECS", type=float, default=DEFAULT_LOCK_TIMEOUT,
                        help="accepted and ignored: the record is append-only and takes no lock")
    parser.add_argument("--no-wait", action="store_true",
                        help="accepted and ignored: the record is append-only and takes no lock")
    parser.add_argument("--jobs", metavar="N", type=int, default=0,
                        help="run at most N independent checks at a time (default: this "
                             "machine's core count; 1 is one after another)")
    parser.add_argument("--heavy-jobs", metavar="N", type=int, default=0,
                        help="admit at most N HEAVY checks at a time, separately from the "
                             f"whole pool (default: {HEAVY_JOBS_DEFAULT}, selected by the "
                             "measured sweep in docs/pi/gate-heavy-admission-2026-11-02.md; "
                             "a value at or above --jobs is the old flat admission)")
    parser.add_argument("--spans-out", metavar="PATH", default="",
                        help="write this run's per-check spans to PATH as JSON (the drain's "
                             "gate_spans rows); a run that writes none still records its verdict")
    options = parser.parse_args(argv)
    # The drain hands the path through the environment rather than on the command line:
    # the program it names here is a wrapper (`tillr-loop gate`) that composes the real
    # gate call, and a flag it did not declare would be refused before fork-check saw it.
    # The explicit flag wins, and the environment is only the fallback the drain uses.
    spans_out = options.spans_out or os.environ.get("TILLR_GATE_SPANS_OUT", "")
    if options.jobs < 0:
        print(f"fork-check: --jobs must be at least 1, not {options.jobs}", file=sys.stderr)
        return 2
    if options.heavy_jobs < 0:
        print(f"fork-check: --heavy-jobs must be at least 1, not {options.heavy_jobs}",
              file=sys.stderr)
        return 2
    jobs = options.jobs or (os.cpu_count() or 1)
    heavy_jobs = options.heavy_jobs or HEAVY_JOBS_DEFAULT

    root = Path(__file__).resolve().parents[1]
    try:
        gate = plan_gate(root, options)
    except PlanningRefusal as refusal:
        print(f"fork-check: {refusal}", file=sys.stderr)
        return 2
    common_dir = gate.common_dir
    branch = gate.branch
    record = gate.record
    head, tree, dirty = gate.head, gate.tree, gate.dirty
    series, changed, diff_base, since = gate.series, gate.changed, gate.diff_base, gate.since
    plan = gate.plan
    watched = gate.watched
    changed_while_planning = gate.changed_while_planning
    blocking, ignored, missing = gate.blocking, gate.ignored, gate.missing
    yolo_missing = gate.yolo_missing
    yolo_build, node_version = gate.yolo, gate.node
    started, started_at = gate.started, gate.started_at
    baseline, baseline_origin = gate.baseline, gate.baseline_origin

    if options.explain:
        scope = f"TARGETED DIAGNOSTIC {since}..HEAD (not completion approval)" if since is not None else "approved-baseline completion"
        # The tier is named only when one was requested, so a bare `--explain` prints the
        # same line it printed before the flag existed.
        if plan.tier:
            scope += f" [{plan.tier} tier]"
        print(f"fork-check: {scope} plan for {head[:12]} ({len(changed)} path(s) changed)")
        for check in plan.checks:
            print(f"  would run  {check.kind:<11} {check.label}\n       because: {check.why}")
            if check.watches:
                print(f"       watches: {', '.join(watched_spelling(check.watches))}")
            if check.writes:
                print(f"       writes: {', '.join(check.writes)}")
            if check.exclusive:
                print(f"       exclusive: runs alone -- {check.exclusive_reason}")
        for skip in plan.skips:
            # `would owe` is the third state: the diff selected the step and this tier
            # defers it to the other one, which is neither a skip this machine could not
            # answer nor a step nothing selected.
            word = "would owe" if skip.owed else ("would skip" if skip.planned else "would not run")
            print(f"  {word:<22} {skip.label}\n       because: {skip.why}")
        external_inputs(plan)
        if changed_while_planning:
            print(f"  would refuse           {len(changed_while_planning)} watched path(s) that changed while the plan was built")
        elif blocking:
            print(f"  would refuse           {len(blocking)} dirty path(s) the checks read")
        elif missing:
            print(f"  would refuse           {sum(len(paths) for _, paths in missing)} working copie(s) planned fork steps need are absent")
            for _, paths in missing:
                for path in paths:
                    print(f"                           {path}")
        elif ignored:
            print(f"  would ignore           {len(ignored)} dirty path(s) the checks do not read")
        # The same machine-readable selection a real run ends with, so a planning pass and
        # a run answer "why did this check run" in one shape.
        print_selection(plan, changed)
        return 0

    if changed_while_planning:
        record.add_run({"at": now_iso(), "commit": head, "outcome": "dirty",
                        "scope": "targeted" if since is not None else "audit", "since": since,
                        "dirty": len(blocking), "dirty_ignored": len(ignored),
                        "changed_while_planning": len(changed_while_planning)})
        record.save()
        print(f"fork-check: REFUSED -- {len(changed_while_planning)} path(s) the checks read "
              f"changed while the plan was being built; nothing was verified.")
        for path in changed_while_planning[:10]:
            print(f"           {path}")
        if len(changed_while_planning) > 10:
            print(f"           ... and {len(changed_while_planning) - 10} more")
        print("fork-check: the plan read those bytes to choose the checks, so it does not describe this tree.")
        write_gate_spans(spans_out, plan, root, started_ns, "refused", head, tree,
                         changed, 0, jobs, time.monotonic() - started)
        # 2, not 1: this run reached no verdict, and 1 is reserved for a check that
        # failed. See "AN EXIT STATUS SAYS WHICH OF TWO THINGS HAPPENED" above.
        return 2
    if blocking:
        record.add_run({"at": now_iso(), "commit": head, "outcome": "dirty",
                        "scope": "targeted" if since is not None else "audit", "since": since,
                        "dirty": len(blocking), "dirty_ignored": len(ignored)})
        record.save()
        print(f"fork-check: REFUSED -- {len(blocking)} path(s) the checks read are staged, "
              f"unstaged or untracked; commit first.")
        for path in blocking[:10]:
            print(f"           {path}")
        if len(blocking) > 10:
            print(f"           ... and {len(blocking) - 10} more")
        print("fork-check: a verification of changed bytes proves nothing about the commit, so nothing ran.")
        write_gate_spans(spans_out, plan, root, started_ns, "refused", head, tree,
                         changed, 0, jobs, time.monotonic() - started)
        # 2, not 1: nothing ran, so there is no verdict to report. A caller that reads
        # 1 as "a check failed" recorded this refusal as the change's own red (item 262).
        return 2
    if missing:
        write_gate_spans(spans_out, plan, root, started_ns, "refused", head, tree,
                         changed, 0, jobs, time.monotonic() - started)
        return needs_refusal(missing, record, head, tree, changed, since)
    if ignored:
        print(f"fork-check: ignoring {len(ignored)} working-tree change(s) the checks do not read:")
        for path in ignored[:10]:
            print(f"           {path}")
        if len(ignored) > 10:
            print(f"           ... and {len(ignored) - 10} more")
        print()

    # Independent checks run at the same time (run_plan_checks): the gate's steps are a
    # collection of separate suites, and running them one after another made the landing's
    # gate the sum of its parts (640s measured on a 12-core host, four of the checks
    # accounting for 527s of it). The bound is the machine's core count unless --jobs says
    # otherwise, and a check that writes a path another writes waits for it.
    #
    # A check the plan declared exclusive runs alone (see EXCLUSIVE_CHECKS), so its
    # real-timing assertions are not measured against a machine its siblings are using.
    #
    # Each check reads its own output in its own worker (execute_check), so a series whose
    # patch-branch stamp is the host's is declared elsewhere rather than failed or skipped:
    # the pack's other series still decide the check, and the unanswered one is named in the
    # report. A concurrent run prints its lines in plan order, so two runs of one tree are
    # compared by verdict, not interleaving.
    #
    # The content-fingerprint verdict cache (board item 398) is loaded here, under the
    # record lock, and saved after the checks. Its store is beside the record in the
    # common Git directory -- outside the scratch worktree and outside the candidate
    # checkout -- so a candidate's own git identity cannot name it. A runtime that cannot
    # be read disables the cache for this run rather than inventing a key.
    cache: VerdictCache | None = None
    runtime: RuntimeFingerprint | None = None
    if any(check.cache_quality for check in plan.checks):
        cache = VerdictCache(common_dir / "yolo-fork-check" / VERDICTS_NAME)
        runtime = read_runtime(root, node_version.version if node_version is not None
                               and node_version.readable else "")
        if not runtime.readable:
            print(f"fork-check: the verdict cache is off for this run: {runtime.why}")
        for check in plan.checks:
            if check.cache_provenance is not None:
                check.cache_provenance["commit"] = head
    check_wall, peak, halted = run_plan_checks(plan, root, diff_base, jobs,
                                               cache=cache, runtime=runtime,
                                               heavy_jobs=heavy_jobs)
    if cache is not None:
        # The verdict cache is a separate file with no read-modify-write to serialise:
        # a concurrent save is a cache miss, not a lost run entry, so it takes no lock.
        cache.save()

    # A Waybar case that needs the installed `ai-usagebar` to answer cannot run on a
    # machine without that binary. The script says so (exit 77) and the check's other
    # cases ran and passed, so the case is named as a skip rather than failing a case no
    # repository change can clear -- the model of a missing `yolo`, and of a document
    # check that could not run.
    for check in [one for one in plan.checks if one.kind == "waybar"]:
        skip = sentinel_skip(check)
        if skip is not None:
            plan.unavailable(*skip, kind=check.kind)
            plan.checks.remove(check)

    # A fork-quality step whose only failing case is a real-time budget this machine's
    # candidate cannot meet cannot answer for the series, so it is named a skip with that
    # reason rather than failing a step no repository change can clear. Any other failure
    # is a verdict and stays in the run.
    for check in [one for one in plan.checks if one.kind == "fork-quality"]:
        skip = sentinel_skip(check)
        if skip is not None:
            # A fork-quality skip is always a quarantine: the step ran, and only a
            # declaration suppresses it (fork_quality_skip).
            plan.quarantine(*skip, applied_quarantine(check), check.kind)
            plan.checks.remove(check)

    # Every input a check's own transcript declared elsewhere joins the plan's list, so
    # the report prints each in one line and the run entry keeps it. Nothing about the
    # outcome reads them: a declaration is news about this machine, never a verdict.
    for check in plan.checks:
        plan.declared.extend(check.declared)

    # Nothing is re-run. A failed check already ran once, and a second run cannot say what
    # differed; what the run does instead is read the failure's own output into a triage
    # packet below, which the repair path hands to a debugger. A step that could not answer
    # at all was named a skip above, not triaged: nothing about it reached a verdict.
    outcome, status = verdict_of(plan.checks)

    if outcome == "verified" and since is None:
        # A landing-tier run proves only the landing tier, so it must NOT advance the
        # verified baseline: the next full run would then plan from a commit whose rolling
        # kinds were never proved, and the debt would be invisible. A rolling run may
        # advance it -- it is the run that pays the debt -- and the full gate always does.
        if plan.tier != TIER_LANDING:
            # This branch's entry only, so a verified run in one worktree leaves every
            # other worktree's baseline where it was.
            record.advance_baseline(head, tree)
        for one in series:
            if one.follows_branch and (one.url in plan.refs or one.fork in plan.targets):
                target = plan.targets.get(one.fork)
                entry = {
                    "url": one.url,
                    "ref": one.ref,
                    "fingerprint": fingerprint(one, plan.refs.get(one.url) or {"heads": {}, "tags": {}}, target),
                    "at": now_iso(),
                }
                # The ref a launch builds, when the bridge could name it: the fingerprint
                # above is comparable across machines, this is the answer a verdict was
                # taken against, and a reader must be able to tell which is which.
                if target:
                    entry["resolved"] = dict(target)
                record.set_target(one.owner_key, entry)
        # The build this verdict rests on. A run that could not identify it records
        # nothing, so the next run asks again rather than reading silence as a match.
        if yolo_build.readable:
            record.set_input("yolo", {
                "fingerprint": yolo_build.fingerprint,
                "build": yolo_build.stamp,
                "commit": yolo_build.commit,
                "at": now_iso(),
            })
        # The node this verdict rests on. A run that could not read it records nothing,
        # so the next run asks again rather than reading silence as a match.
        if node_version.readable:
            record.set_input("node", {
                "fingerprint": node_version.fingerprint,
                "version": node_version.version,
                "at": now_iso(),
            })
    at = now_iso()

    # A run's coverage is part of its record: a plan that shrank from one commit to the
    # next is then visible in the history rather than only in a headline that read the
    # same both times. One entry per check, because a check runs once. Each entry carries
    # the reason the gate gave and the changed path that selected it, so "why did this
    # check run" is answered by the record rather than by reading the gate's source; the
    # steps that did not run are named with their reasons beside them.
    recorded_checks: list[dict] = []
    for check in plan.checks:
        selection = selection_entry(check, changed)
        recorded = {"kind": check.kind, "label": check.label, "rc": check.rc,
                    "status": check_status(check), "started": check.started,
                    "seconds": round(check.seconds, 1),
                    "why": selection["why"],
                    "watches": selection["watches"],
                    "selected_by": selection["selected_by"],
                    "unconditional": selection["unconditional"],
                    "forced": selection["forced"]}
        if check.fingerprint_dir:
            recorded["fingerprint"] = materialized_fingerprint(check.fingerprint_dir)
        if check.cached:
            # A cached verdict is recorded as cached, with its key: a reader counting
            # executions must not read a reused verdict as a fresh pass.
            recorded["cached"] = True
            recorded["cache_key"] = check.cache_key
        if check.cause:
            # Why this fork-quality step executed, so the cause breakdown is answerable
            # from the record without re-reading the diff (board item 407).
            recorded["cause"] = check.cause
        recorded_checks.append(recorded)
    change = classify_change(root, changed, series)
    wall = round(time.monotonic() - started, 1)
    floor, efficiency = efficiency_figures(wall, s1_floor(plan.checks))
    entry = {
        "at": at, "commit": head, "tree": tree, "changed": len(changed), "outcome": outcome,
        "scope": "targeted" if since is not None else "audit", "since": since,
        "seconds": wall,
        # The check phase's own wall clock and how much of it overlapped: a run entry whose
        # seconds are the sum of its checks and one whose seconds are one slow check read
        # the same in `seconds` alone.
        "check_wall_seconds": round(check_wall, 1),
        "jobs": jobs,
        "heavy_jobs": heavy_jobs,
        "peak_concurrency": peak,
        "yolo": yolo_build.fingerprint if yolo_build.readable else None,
        "node": node_version.fingerprint if node_version.readable else None,
        # Owed is its own count: a step the diff selected and this tier deferred is not a
        # step nothing selected, and a reader counting coverage must not fold the two.
        "not_planned": sum(1 for skip in plan.skips if not skip.planned and not skip.owed),
        "owed": sum(1 for skip in plan.skips if skip.owed),
        "could_not_answer": sum(1 for skip in plan.skips if skip.planned),
        "declared": [declared.entry() for declared in plan.declared],
        "exclusive": [check.kind for check in plan.checks if check.exclusive],
        # The pass's change class, the target the design sets for it, the S1 floor under
        # this run's own per-check durations, and the efficiency ratio. Recorded on every
        # run, so the instrument survives in the timeline rather than only in a terminal.
        "change_class": change.name,
        "change_class_target": change.target,
        "floor_seconds": floor,
        "efficiency": efficiency,
        "checks": recorded_checks,
        # Every step the run did not put a verdict on, with the reason it did not run and
        # whether the change gave it a reason this machine could not answer. The counts
        # above are the same news as numbers; this is the news a reader acts on.
        "skips": [skip_entry(skip) for skip in plan.skips],
    }
    # What a failed check printed, and the packet read out of it, written before the entry
    # is saved so the entry's pointers name files that already hold the bytes. A passing
    # check gets no pointer: there is nothing to read, and a path on every check would make
    # the file's absence read as a lost record rather than as "nothing failed".
    excerpt = write_failure_excerpts(record, entry, plan.checks)
    triage_path, packets = write_triage(record, entry, plan.checks, root)
    entry["triage"] = [{"kind": packet["check"], "label": packet["label"],
                        "cases": len(packet["cases"]), "reproduce": packet["reproduce"]}
                       for packet in packets]
    if packets:
        record_triage(record, entry, packets)
    clear_triage(record, plan.checks)
    for one in entry["checks"]:
        if one["rc"] != 0:
            if excerpt is not None:
                one["excerpt"] = excerpt
            if triage_path is not None:
                one["triage"] = triage_path
    if since is None:
        record.set_last_audit(entry)
    elif "last_audit" not in record.data:
        # Preserve an older audit before targeted runs age it out of bounded history.
        audits = [run for run in record.data["runs"] if run.get("scope", "audit") == "audit"
                  and run["outcome"] != "dirty"]
        if audits:
            record.set_last_audit(audits[-1])
    record.add_run(entry)
    record.save()

    report(plan, record, baseline, head, changed, outcome, wall, yolo_missing,
           len(series), root, since, excerpt, baseline_origin, series)
    print_triage(plan, record, packets, triage_path)
    append_timings(record, entry, plan.checks, check_wall, jobs, peak, heavy_jobs)
    append_evidence(record, root, entry, plan.checks, started_at)
    print_timings(plan.checks, time.monotonic() - started, check_wall, jobs, peak,
                  options.timings, heavy_jobs)
    # The selection line sits immediately above the plan line, so the two lines a consumer
    # needs are the tail it keeps, and the plan line stays last by contract.
    print_selection(plan, changed)
    print_plan(record, plan, diff_base, head)
    write_gate_spans(spans_out, plan, root, started_ns, outcome, head, tree,
                     changed, peak, jobs, wall)
    if outcome == "unproven":

        print("fork-check: at least one check reached no verdict, so this is not a verdict -- see its output above")
    return status


# ── the hostile-matrix preflight ────────────────────────────────────────────────
#
# The gate judges a change in conditions a developer's shell does not have: its steps
# run beside each other and beside every other lane on the machine, some suites scrub
# HOME and XDG_STATE_HOME and demand an explicit board store, and in a jail the whole
# run is uid 0. A check that only holds in the developer's own shell is therefore green
# until a landing asks it the gate's question -- and then it is found one defect at a
# time, one landing each, which is how a suite's store resolution and its
# widget order each cost a landing.
#
# The preflight asks all of it at once, BEFORE a fix is attempted: the same checks the
# gate would plan for this change, each run in each cell of a small matrix of the gate's
# conditions, reported in one pass. It is a diagnostic and never a verdict: it writes no
# record, advances no baseline, and takes no lock, so it runs beside a real gate.

# THE GATE'S TIMING RULE, and the reason it is a rule rather than a preference.
#
# No check asserts elapsed time, and no check generates load to test another check.
#
# An assertion on elapsed time measures the machine, not the code: the scheduler, the
# other tenants, the thermal state. It passes or fails according to the box, so its
# verdict has no subject -- and a budget's value (30 ms, 30 s, 45 min) is a policy choice
# no test can validate. Induced load does not fix that; it only makes the same
# unattributable verdict arrive more often, and it leaves the load behind when the run is
# killed. The preflight's `gate-parallel` cell did exactly that and was deleted on
# 2026-10-31 after it orphaned 13 burner processes on a 12-core box.
#
# What a check may assert instead: counts, orderings and equalities over what the product
# recorded; a deadline's REACTION under a simulated clock; and a wait that fails only on
# "never happened", with a budget an order of magnitude past expectation. Elapsed time
# has two legitimate homes, neither a pass/fail check: a benchmark on named hardware, and
# production telemetry. The full statement is docs/design/agent-runtime.md section 23.3.
#
# This constant is not decoration: `tests/fork-check.test.py` asserts it is present and
# scans the gate and its check corpus for both banned shapes, so deleting the rule or
# reintroducing either shape fails the gate. `tools/gate-timing-declarations.json` is
# where an existing elapsed-time assertion is recorded with its reason and its repair.
GATE_TIMING_RULE = (
    "no check asserts elapsed time; no check generates load to test another check"
)

# The matrix. Two cells, because the one question a red gate check leaves open that a
# developer's shell cannot answer is "is it the environment?" -- and the caller's own
# shell is the reference the gate cell is read against. There is deliberately no cell
# that adds CPU load: see GATE_TIMING_RULE.


class PreflightCell:
    """One column of the matrix: an environment.

    `environment` is `caller` (inherit the shell that ran the preflight, which is where a
    check passes today) or `gate` (scrub HOME and XDG_STATE_HOME, demand an explicit
    board store, and hand a suite the real home only where it must resolve a package).
    """

    def __init__(self, name: str, why: str, environment: str):
        self.name = name
        self.why = why
        self.environment = environment


PREFLIGHT_CELLS = (
    PreflightCell(
        "baseline",
        "the caller's own shell, one check at a time, nothing else of the run beside it: "
        "the conditions a check passes in today",
        "caller"),
    PreflightCell(
        "gate-serial",
        "the gate's own environment -- HOME and XDG_STATE_HOME scrubbed, "
        "BOARD_STORE_STRICT set -- with one check at a time and no added load",
        "gate"),
)

# What a failing cell's transcript keeps. A preflight's value is the diagnosis, and a
# check that failed in two cells printed two different things; the report prints this
# much of each, and the JSON report keeps this much per failure.
PREFLIGHT_EXCERPT_LINES = 40


class PreflightError(Exception):
    """The matrix itself is misconfigured, so no cell can be read as a finding."""


class Declaration:
    """A check's declared inability to pass one cell, with the repair it waits on."""

    def __init__(self, check: str, cell: str, reason: str, repair: str):
        self.check = check
        self.cell = cell
        self.reason = reason
        self.repair = repair

    def covers(self, kind: str, label: str, cell: str) -> bool:
        return self.cell in ("*", cell) and preflight_names(self.check, kind, label)

    def entry(self) -> dict:
        return {"check": self.check, "cell": self.cell, "reason": self.reason,
                "repair": self.repair}


def preflight_names(name: str, kind: str, label: str) -> bool:
    """Whether `name` selects this check: a kind, or `kind:label-prefix`.

    The same vocabulary EXCLUSIVE_CHECKS uses, because both answer "which check is this
    about" and a second spelling would be a second thing to get wrong.
    """
    head, _, prefix = name.partition(":")
    if head != kind:
        return False
    return label.startswith(prefix) if prefix else True


def load_declarations(path: Path) -> list[Declaration]:
    """Read the declarations file, refusing a shape that could not mean anything.

    A declaration file that cannot be read is not an empty one: the matrix would then
    report a known limitation as a failure, or a real failure as declared. So every
    problem -- unreadable JSON, an unknown key, an empty reason, a cell no cell has --
    is an error that names the entry, and the preflight runs nothing.
    """
    if not path.exists():
        return []
    try:
        document = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        raise PreflightError(f"cannot read the declarations at {path}: {error}")
    if not isinstance(document, dict) or set(document) != {"declarations"}:
        raise PreflightError(
            f"{path} must be an object with one key, 'declarations'")
    entries = document["declarations"]
    if not isinstance(entries, list):
        raise PreflightError(f"{path}: 'declarations' must be a list")
    cells = {cell.name for cell in PREFLIGHT_CELLS} | {"*"}
    seen: set[tuple[str, str]] = set()
    declarations: list[Declaration] = []
    for index, entry in enumerate(entries):
        where = f"{path}: declarations[{index}]"
        if not isinstance(entry, dict):
            raise PreflightError(f"{where} must be an object")
        unknown = set(entry) - {"check", "cell", "reason", "repair"}
        if unknown:
            raise PreflightError(f"{where} has unknown key(s): {', '.join(sorted(unknown))}")
        for key in ("check", "cell", "reason", "repair"):
            if not isinstance(entry.get(key), str) or not entry[key].strip():
                raise PreflightError(f"{where} needs a non-empty '{key}'")
        if entry["cell"] not in cells:
            raise PreflightError(
                f"{where} names cell {entry['cell']!r}, which is not one of "
                f"{', '.join(sorted(cells))}")
        key = (entry["check"], entry["cell"])
        if key in seen:
            raise PreflightError(f"{where} repeats the declaration for {key[0]!r} in {key[1]!r}")
        seen.add(key)
        declarations.append(Declaration(entry["check"], entry["cell"], entry["reason"],
                                       entry["repair"]))
    return declarations


def preflight_scratch() -> Path:
    """The scratch root this preflight owns: a scrubbed home, a state home, and nothing else.

    Under the durable directory, like every other writing step here, and keyed by the
    process id so two lanes preflighting at once cannot scrub each other's home mid-run.
    It is removed when the run ends: what a reader keeps is the report.
    """
    durable = os.environ.get("YOLO_DURABLE_DIR") or os.environ.get("TMPDIR") or "/tmp"
    return Path(durable) / "hostile-preflight" / f"run-{os.getpid()}"


def shared_go_caches() -> dict[str, str]:
    """The caller's Go build and module caches, carried across a scrubbed HOME.

    Scrubbing HOME would otherwise put GOCACHE and GOMODCACHE under the scrubbed home,
    which is a per-run private copy of the build cache -- exactly what this repository's
    rule against worktree-local Go caches forbids -- and would make every Go step rebuild
    the world in every cell. The build cache is not part of what the condition tests, so
    it is preserved explicitly; `go env` is not consulted, because a preflight may run
    before anything has been built.
    """
    home = Path(os.environ.get("HOME") or str(Path.home()))
    cache = Path(os.environ.get("XDG_CACHE_HOME") or str(home / ".cache"))
    gopath = Path(os.environ.get("GOPATH") or str(home / "go"))
    return {
        "GOCACHE": os.environ.get("GOCACHE") or str(cache / "go-build"),
        "GOMODCACHE": os.environ.get("GOMODCACHE") or str(gopath / "pkg" / "mod"),
    }


def cell_environment(cell: PreflightCell, scratch: Path) -> tuple[dict[str, str], tuple[str, ...]]:
    """The additions and removals one cell imposes on every check it runs.

    The `gate` environment is the conditions named in the item this preflight exists for:
    HOME and XDG_STATE_HOME scrubbed to this run's scratch, BOARD_STORE_STRICT demanding
    an explicit store, and BOARD_DB/BOARD_CLI ABSENT -- a store resolver handed an empty
    variable is being asked a different question from one handed nothing.

    The scrub is the condition under test; resolving the
    installed Pi package is not.

    `FORK_CHECK_PREFLIGHT` and `FORK_CHECK_PREFLIGHT_CELL` name the run and the cell, so a
    failing check's own output says which condition produced it.
    """
    marker = {"FORK_CHECK_PREFLIGHT": "1", "FORK_CHECK_PREFLIGHT_CELL": cell.name}
    if cell.environment != "gate":
        return marker, ()
    additions = {
        "HOME": str(scratch / "home"),
        "XDG_STATE_HOME": str(scratch / "state"),
        "BOARD_STORE_STRICT": "1",
        **shared_go_caches(),
        **marker,
    }
    return additions, ("BOARD_DB", "BOARD_CLI")


class CellResult:
    """One check in one cell: what it said there, and how the report reads it.

    `status` is one of `pass`, `fail`, `declared` (it failed in a cell it has declared it
    cannot pass) and `unanswerable` (the check's own inputs are absent, so it never ran).

    `check` is the COPY that ran in this cell -- each cell runs its own copy, because a
    Check holds one run's result -- and `source` is the planned check it was copied
    from, which is what groups a check's cells together.
    """

    def __init__(self, cell: PreflightCell, check: Check, status: str,
                 declaration: Declaration | None = None, output: str = "",
                 source: Check | None = None):
        self.cell = cell
        self.check = check
        self.source = source if source is not None else check
        self.status = status
        self.declaration = declaration
        self.output = output

    def entry(self) -> dict:
        entry = {"cell": self.cell.name, "status": self.status, "rc": self.check.rc,
                 "seconds": self.check.seconds}
        if self.declaration is not None:
            entry["declaration"] = self.declaration.entry()
        if self.status in ("fail", "declared"):
            lines = self.output.strip().splitlines()
            entry["output"] = "\n".join(lines[-PREFLIGHT_EXCERPT_LINES:])
            entry["output_lines"] = len(lines)
        return entry


def preflight_report(gate: GatePlan, cells: list[PreflightCell], results: dict,
                     declarations: list[Declaration], uid: int,
                     omitted: dict) -> dict:
    """The run's machine-readable findings, and the classification the exit status reads.

    A check is `environment-conditional` when it passed in at least one cell and failed
    (undeclared) in another: that is the defect class this preflight exists to surface,
    and it is the one a developer's shell can never show. `unconditional` failed every
    cell it ran, so the environment is not the story. `declared` failed only where it
    said it would. `unanswerable` never ran.
    """
    checks = []
    failures = []
    for check in results["order"]:
        per_cell = [result for result in results["cells"] if result.source is check]
        statuses = {result.status for result in per_cell}
        if statuses == {"unanswerable"}:
            classification = "unanswerable"
        elif "fail" in statuses:
            ran = {result.status for result in per_cell if result.status != "unanswerable"}
            classification = ("environment-conditional" if "pass" in ran else "unconditional")
        elif "declared" in statuses:
            classification = "declared"
        else:
            classification = "clean"
        entries = []
        for result in per_cell:
            entry = result.entry()
            if result.status == "pass":
                covering = next((one for one in declarations
                                 if one.covers(check.kind, check.label, result.cell.name)), None)
                if covering is not None:
                    entry["declaration_covers"] = covering.entry()
            if result.status == "fail":
                failures.append(result)
            entries.append(entry)
        checks.append({
            "kind": check.kind,
            "label": check.label,
            "command": check.command_line(),
            "classification": classification,
            "cells": entries,
        })
    return {
        "preflight": 1,
        "at": now_iso(),
        "commit": gate.head,
        "tree": gate.tree,
        # The criterion the preflight is asked to meet is "against an unmodified tree",
        # so the record states which tree was judged rather than leaving it to a reader's
        # assumption: a finding about a dirty tree is a finding about the working tree.
        "tree_state": "dirty" if gate.blocking else "clean",
        "dirty_watched": gate.blocking,
        "changed_while_planning": gate.changed_while_planning,
        "uid": uid,
        "uid0": report_uid0_line(),
        "cells": [{"name": cell.name, "why": cell.why, "environment": cell.environment}
                  for cell in cells],
        "declarations": [declaration.entry() for declaration in declarations],
        "omitted": omitted,
        "checks": checks,
        "failures": [result.entry() for result in failures],
        "exit": 1 if failures else 0,
    }


def print_preflight(report: dict, results: dict) -> None:
    """Print the findings in one pass: every cell's line for every check, then each failure."""
    print(f"fork-check preflight: {len(report['checks'])} check(s) x {len(report['cells'])} cell(s) "
          f"at {report['commit'][:12]}")
    if report["tree_state"] == "dirty":
        print(f"fork-check preflight: THE TREE IS MODIFIED -- {len(report['dirty_watched'])} "
              f"path(s) the checks read are uncommitted, so a finding here is about the "
              f"working tree rather than about a commit:")
        for path in report["dirty_watched"][:10]:
            print(f"           {path}")
        if len(report["dirty_watched"]) > 10:
            print(f"           ... and {len(report['dirty_watched']) - 10} more")
    else:
        print("fork-check preflight: the tree is unmodified (nothing the checks read is dirty)")
    if report["changed_while_planning"]:
        print(f"fork-check preflight: WARNING -- {len(report['changed_while_planning'])} path(s) "
              f"the checks read changed while the plan was built, so the plan may not describe "
              f"this tree: {', '.join(report['changed_while_planning'][:5])}")
    print(f"fork-check preflight: {report['uid0']}")
    for cell in report["cells"]:
        print(f"  cell {cell['name']}: {cell['why']}")
    if report["omitted"]["checks"]:
        print(f"  not selected by --only: {len(report['omitted']['checks'])} check(s) -- "
              f"{', '.join(report['omitted']['checks'][:8])}"
              f"{' ...' if len(report['omitted']['checks']) > 8 else ''}")
    if report["omitted"]["cells"]:
        print(f"  cells not selected by --cells: {', '.join(report['omitted']['cells'])}")
    print()
    for check in report["checks"]:
        word = {"clean": "ok      ", "environment-conditional": "CONDITIONAL",
                "unconditional": "FAILED  ", "declared": "declared",
                "unanswerable": "cannot  "}[check["classification"]]
        print(f"  {word} {check['kind']:<11} {check['label']}")
        for result in check["cells"]:
            if result["status"] == "pass":
                note = ("  (a declaration covers this cell -- it may be stale)"
                        if result.get("declaration_covers") else "")
                print(f"           {result['cell']:<14} pass  {result['seconds']:.1f}s{note}")
            elif result["status"] == "declared":
                declaration = result["declaration"]
                print(f"           {result['cell']:<14} declared: {declaration['reason']}")
                print(f"           {'':<14} repair:   {declaration['repair']}")
            elif result["status"] == "fail":
                print(f"           {result['cell']:<14} FAIL  exit {result['rc']}  {result['seconds']:.1f}s")
            else:
                print(f"           {result['cell']:<14} not run: the check's own inputs are absent")
    declared = [entry for check in report["checks"] for entry in check["cells"]
                if entry["status"] == "declared"]
    unanswerable = [check for check in report["checks"] if check["classification"] == "unanswerable"]
    print()
    print(f"fork-check preflight: {len(report['failures'])} failure(s), {len(declared)} declared, "
          f"{len(unanswerable)} unanswerable, {len(report['checks'])} check(s) read")
    conditional = [check for check in report["checks"]
                   if check["classification"] == "environment-conditional"]
    if conditional:
        print(f"fork-check preflight: {len(conditional)} ENVIRONMENT-CONDITIONAL -- green in one "
              f"cell and red in another, which is the class a developer's shell cannot show:")
        for check in conditional:
            green = [result["cell"] for result in check["cells"] if result["status"] == "pass"]
            red = [result["cell"] for result in check["cells"] if result["status"] == "fail"]
            print(f"           {check['kind']}: {check['label']} -- passed in {', '.join(green)}; "
                  f"failed in {', '.join(red)}")
    # The transcripts are printed from the results, which keep the check they belong to.
    for result in results["failures"]:
        print()
        print(f"=== {result.cell.name} -- {result.check.kind}: {result.check.label}")
        print(f"    command: {result.check.command_line()}")
        print(f"    exit code: {result.check.rc}  duration: {result.check.seconds:.1f}s")
        lines = result.output.strip().splitlines()
        kept = lines[-PREFLIGHT_EXCERPT_LINES:]
        if len(lines) > len(kept):
            print(f"    --- output: last {len(kept)} of {len(lines)} line(s) ---")
        for line in kept:
            print(f"    {line}")


def run_preflight(argv: list[str]) -> int:
    """Run the plan's checks under the gate's conditions, and report every failure at once.

    Exit status: 0 when every check passed or declared every cell it failed, 1 when at
    least one check failed a cell it has not declared, and 2 when the preflight itself
    cannot answer -- a plan it cannot build, or a declarations file it cannot read.
    """
    parser = argparse.ArgumentParser(
        prog="fork-check --preflight",
        description="Run the checks this change plans under the gate's own conditions, and "
                    "report every failure in one pass. A diagnostic: it writes no record and "
                    "advances no verified baseline.")
    parser.add_argument("--all", action="store_true", help="plan the full audit, as `just done --all` would")
    parser.add_argument("--since", metavar="BASE", help="plan the range BASE..HEAD, as `just done --since` would")
    parser.add_argument("--build", dest="build", action="store_true", default=True,
                        help="build each fork whose series changed (the default)")
    parser.add_argument("--no-build", dest="build", action="store_false", help="skip build verification")
    parser.add_argument("--cells", default=None,
                        help="comma-separated cells to run (default: every cell); "
                             + ", ".join(cell.name for cell in PREFLIGHT_CELLS))
    parser.add_argument("--only", action="append", default=[], metavar="KIND[:LABEL]",
                        help="run only the checks this names; repeatable, and a comma-separated "
                             "list is accepted. The report names what it omitted.")
    parser.add_argument("--declarations", metavar="PATH", default="",
                        help=f"the declarations file (default: {PREFLIGHT_DECLARATIONS})")
    parser.add_argument("--report", metavar="PATH", default="",
                        help="write the JSON findings here as well as printing them")
    parser.add_argument("--json", action="store_true", help="print the JSON findings instead of the report")
    parser.add_argument("--explain", action="store_true", help="print the matrix and the plan, and run none of it")
    parser.add_argument("--preflight", action="store_true", help=argparse.SUPPRESS)
    options = parser.parse_args(argv)

    root = Path(__file__).resolve().parents[1]
    try:
        cells = select_preflight_cells(options.cells)
        declarations = load_declarations(
            Path(options.declarations) if options.declarations else root / PREFLIGHT_DECLARATIONS)
        gate = plan_gate(root, options)
    except (PreflightError, PlanningRefusal) as error:
        print(f"fork-check: {error}", file=sys.stderr)
        return 2

    checks = [check for check in gate.plan.checks if preflight_selected(check, options.only)]
    omitted_checks = [f"{check.kind}: {check.label}" for check in gate.plan.checks
                      if check not in checks]
    omitted_cells = [cell.name for cell in PREFLIGHT_CELLS if cell not in cells]
    if not checks:
        print("fork-check preflight: no check matches --only; nothing to run", file=sys.stderr)
        return 2

    # A check whose own working copy is absent never ran, so it has no finding: it is named
    # unanswerable rather than failed, the same rule the gate applies when it refuses.
    missing = {id(check): paths for check, paths in gate.missing}
    if options.explain:
        print(f"fork-check preflight: {len(checks)} check(s) x {len(cells)} cell(s), plan at "
              f"{gate.head[:12]}")
        print(f"fork-check preflight: {report_uid0_line()}")
        for cell in cells:
            print(f"  cell {cell.name}: {cell.why}")
        for check in checks:
            where = (f"UNANSWERABLE -- {', '.join(missing[id(check)])} is absent"
                     if id(check) in missing else "would run")
            print(f"  {where:<22} {check.kind:<11} {check.label}")
        for cell in PREFLIGHT_CELLS:
            if cell not in cells:
                print(f"  cell not selected       {cell.name}")
        if omitted_checks:
            print(f"  not selected by --only  {len(omitted_checks)} check(s): "
                  f"{', '.join(omitted_checks[:8])}")
        if declarations:
            print(f"  declarations            {len(declarations)}")
        return 0

    scratch = preflight_scratch()
    results = {"order": checks, "cells": [], "failures": []}
    started = time.monotonic()
    try:
        (scratch / "home").mkdir(parents=True, exist_ok=True)
        (scratch / "state").mkdir(parents=True, exist_ok=True)
        for cell in cells:
            runnable = [check for check in checks if id(check) not in missing]
            copies = [copy.copy(check) for check in runnable]
            additions, drop = cell_environment(cell, scratch)
            progress(f"fork-check preflight: cell {cell.name} -- {len(copies)} check(s), "
                     f"one at a time")
            run_checks(
                copies, 1,
                lambda index: execute_attempt(root, copies[index], gate.diff_base,
                                              additions, drop),
                honor_exclusive=False)
            for check, run in zip(runnable, copies):
                declaration = next((one for one in declarations
                                    if one.covers(check.kind, check.label, cell.name)), None)
                if run.rc == 0:
                    status = "pass"
                elif declaration is not None:
                    status = "declared"
                else:
                    status = "fail"
                result = CellResult(cell, run, status, declaration, run.output, source=check)
                results["cells"].append(result)
                if status == "fail":
                    results["failures"].append(result)
            for check in checks:
                if id(check) in missing:
                    results["cells"].append(CellResult(cell, check, "unanswerable"))
    finally:
        shutil.rmtree(scratch, ignore_errors=True)

    report = preflight_report(gate, cells, results, declarations, os.geteuid(),
                              {"checks": omitted_checks, "cells": omitted_cells})
    # The declarations the run never asked about are news, not findings: a check that is
    # not planned this run, or a cell that was not selected, leaves its declaration
    # unexercised -- and a declaration whose check PASSED everywhere is one to re-check,
    # because a declaration that outlives its defect is a silencer.
    planned = {check.kind for check in checks}
    for declaration in declarations:
        if declaration.check.partition(":")[0] not in planned:
            report.setdefault("unexercised_declarations", []).append(declaration.entry())
    report["exit"] = 1 if results["failures"] else 0
    if options.report:
        path = Path(options.report)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    if options.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print_preflight(report, results)
        if options.report:
            print(f"\nfork-check preflight: findings written to {options.report}")
        elapsed = time.monotonic() - started
        print(f"fork-check preflight: {elapsed:.1f}s total")
    return report["exit"]


def report_uid0_line() -> str:
    uid = os.geteuid()
    if uid == 0:
        return "uid 0: exercised -- this process is uid 0, as a jail's gate is"
    return (f"uid 0: unexercised -- this process is uid {uid}; no non-interactive path to "
            "uid 0 is used, because running a suite as root would leave root-owned files "
            "in the tree")


def select_preflight_cells(selector: str | None) -> list[PreflightCell]:
    """The cells `--cells` names, in matrix order, refusing a name no cell has.

    `None` is the option not given, which is every cell. An explicitly empty selector is
    refused rather than read as "every cell": a run that named no cell and a run that
    named all of them are different questions, and the report counts the cells it ran.
    """
    if selector is None:
        return list(PREFLIGHT_CELLS)
    wanted = [name.strip() for name in selector.split(",") if name.strip()]
    unknown = [name for name in wanted if name not in {cell.name for cell in PREFLIGHT_CELLS}]
    if unknown:
        raise PreflightError(
            f"--cells names {', '.join(unknown)}, which is not a cell; the cells are "
            + ", ".join(cell.name for cell in PREFLIGHT_CELLS))
    if not wanted:
        raise PreflightError("--cells was given nothing to run")
    return [cell for cell in PREFLIGHT_CELLS if cell.name in wanted]


def preflight_selected(check: Check, selectors: list[str]) -> bool:
    """Whether `--only` selects this check. No selector selects every check."""
    names = [name.strip() for selector in selectors for name in selector.split(",") if name.strip()]
    if not names:
        return True
    return any(preflight_names(name, check.kind, check.label) for name in names)


def show_triage(common_dir: Path, selector: str) -> int:
    """Print the last recorded triage packet as JSON, and run nothing.

    This is the repair path's entry point: a failure is diagnosed from the run that made
    it, so the packet is already written, and handing it to a debugger is a read rather
    than another gate run. `selector` narrows it to one check -- a kind, a label fragment
    or a command fragment -- so a lane repairing one step does not read every packet.
    """
    path = common_dir / "yolo-fork-check" / TRIAGE_NAME
    try:
        document = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        print(f"fork-check: no triage packet is recorded at {path}; the last run that failed "
              f"writes one, and nothing has failed since.")
        return 0
    checks = document.get("checks") or []
    if selector:
        checks = [packet for packet in checks
                  if selector in (packet.get("check") or "")
                  or selector in (packet.get("label") or "")
                  or selector in (packet.get("command") or "")]
        document = {**document, "checks": checks}
        if not checks:
            print(f"fork-check: no recorded packet matches {selector!r} in {path}")
            return 2
    print(json.dumps(document, indent=2, sort_keys=True))
    return 0


# ── replay: re-run a red gate's failing checks on the base ──────────────────────
#
# A red gate is a suspect until the same checks are run on the base the candidate was
# staged from. This face is that replay and nothing else: it plans the full gate (so a
# check the candidate's diff happens not to select is still present), keeps only the
# checks the caller names with `--only`, runs them once, and exits 0 when every one is
# green and 1 when any is red. It writes no record and advances no baseline -- it judges a
# base revision, not a candidate, and a record here would be a verdict about work nobody
# staged. Exit 2 is "could not answer" (no planning, no matching check, a check whose
# working copy is absent), which the loop reads as unknown and never as green.

def run_replay(argv: list[str]) -> int:
    """Re-run a named set of checks on a revision, and answer their verdict.

    Exit status: 0 every named check passed, 1 at least one failed, 2 the replay itself
    could not answer.
    """
    parser = argparse.ArgumentParser(
        prog="fork-check --replay",
        description="Re-run a red gate's failing checks on one revision, and write no record.")
    parser.add_argument("--only", action="append", default=[], metavar="KIND[:LABEL]",
                        help="the checks to replay; repeatable, and a comma-separated list is accepted")
    parser.add_argument("--since", metavar="BASE", default=None, help=argparse.SUPPRESS)
    parser.add_argument("--all", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--build", dest="build", action="store_true", default=True, help=argparse.SUPPRESS)
    parser.add_argument("--no-build", dest="build", action="store_false", help=argparse.SUPPRESS)
    parser.add_argument("--replay", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--jobs", default=None, help=argparse.SUPPRESS)
    options = parser.parse_args(argv)
    # Force the full plan: a replay names checks that the current revision's diff may not
    # select, and a narrowing plan would silently replay nothing.
    options.all = True
    options.since = None
    root = Path(__file__).resolve().parents[1]
    try:
        gate = plan_gate(root, options)
    except (PreflightError, PlanningRefusal) as error:
        print(f"fork-check replay: {error}", file=sys.stderr)
        return 2
    checks = [check for check in gate.plan.checks if preflight_selected(check, options.only)]
    if not checks:
        print("fork-check replay: no planned check matches --only", file=sys.stderr)
        return 2
    missing = {id(check) for check, _ in gate.missing}
    if any(id(check) in missing for check in checks):
        print("fork-check replay: a named check's working copy is absent, so it cannot run",
              file=sys.stderr)
        return 2
    copies = [copy.copy(check) for check in checks]
    for check in copies:
        execute_attempt(root, check, gate.diff_base)
    failed = []
    for check in copies:
        status = "PASS" if check.rc == 0 else "FAIL"
        print(f"fork-check replay: {status} {check.kind}: {check.label} (exit {check.rc})")
        if check.rc != 0:
            failed.append(f"{check.kind}: {check.label}")
    if failed:
        print(f"fork-check replay: {len(failed)} of {len(copies)} check(s) red on "
              f"{gate.head[:12]}: {', '.join(failed)}", file=sys.stderr)
        return 1
    print(f"fork-check replay: all {len(copies)} named check(s) green on {gate.head[:12]}")
    return 0


# ── pin drift: upstream ahead of a pinned fork ──────────────────────────────────
#
# A hex `?ref=` holds a fork at the commit its series was tested against, so an upstream
# push cannot move the fork's fingerprint and carry its quality suite into an unrelated
# landing pass. The cost is that the pin does not move by itself: a neglected pin stops
# absorbing upstream fixes. This detector is the other half of that trade -- it compares
# each pinned fork's pin with the upstream's default branch and, with `--file`, files a
# BUMP ITEM when upstream is ahead, so adoption is deliberate work with its own item
# rather than a tax on arbitrary landings.
#
# It is a read of the upstream and the checkout, plus (with `--file`) one board write. It
# takes no record lock and runs no check, so it is cheap enough to run continuously.
PIN_DRIFT_TIMEOUT = 120
PIN_DRIFT_EXTERNAL = "fork-pin-drift"


def pinned_series(series: list[Series]) -> list[Series]:
    """The series a hex `?ref=` holds, which is what this detector is about."""
    return [one for one in series if one.url and not one.follows_branch]


def upstream_default_branch(url: str) -> str:
    """The upstream's default branch, from `git ls-remote --symref`.

    A pin records the commit, not the branch it was taken from, so the branch to compare
    against is the upstream's own default. That is the branch a pin is taken from in
    practice, and it needs no extra field in the pack declaration.
    """
    try:
        result = subprocess.run(
            ["git", "ls-remote", "--symref", url, "HEAD"], capture_output=True,
            text=True, errors="replace", timeout=LS_REMOTE_TIMEOUT,
        )
    except (subprocess.TimeoutExpired, OSError):
        return "main"
    for line in result.stdout.splitlines():
        if line.startswith("ref:") and "\tHEAD" in line:
            ref = line[len("ref:"):].split("\t", 1)[0].strip()
            if ref.startswith("refs/heads/"):
                return ref[len("refs/heads/"):]
    return "main"


def pin_drift_one(root: Path, one: Series) -> dict[str, str]:
    """How far the upstream's default branch is past this fork's pin.

    `state` is one of `at-pin`, `ahead`, `diverged`, `unknown` (the upstream could not be
    read) or `unchecked` (the tip differs but no checkout can count the commits).
    """
    pin = one.ref.lower()
    branch = upstream_default_branch(one.url or "")
    refs, why = ls_remote(one.url or "")
    answer = {"fork": one.fork, "owner": one.owner_key, "pin": pin, "branch": branch,
              "tip": "", "ahead": "", "state": "unknown", "why": why}
    if refs is None:
        return answer
    tip = refs["heads"].get(branch, "")
    answer["tip"] = tip
    if not tip:
        answer["why"] = f"upstream has no branch {branch}"
        return answer
    if tip == pin:
        answer["state"] = "at-pin"
        return answer
    checkout = forks_root(root) / one.fork
    if not checkout.is_dir():
        answer["state"] = "unchecked"
        answer["why"] = f"no checkout at {checkout} to count the commits"
        return answer
    # The tip `ls-remote` named may not be in the checkout yet. Fetch it there rather than
    # trust a stale remote-tracking ref: the question is about upstream NOW.
    rc, out, _ = run_command(checkout, ["git", "cat-file", "-e", f"{tip}^{{commit}}"],
                             PIN_DRIFT_TIMEOUT)
    if rc != 0:
        rc, out, _ = run_command(checkout, ["git", "fetch", "--quiet", one.url or "", branch],
                                 PIN_DRIFT_TIMEOUT)
        if rc != 0:
            answer["state"] = "unchecked"
            answer["why"] = f"could not fetch {branch} into {checkout}: {out.strip()}"
            return answer
        rc, out, _ = run_command(checkout, ["git", "rev-parse", "FETCH_HEAD"], PIN_DRIFT_TIMEOUT)
        if rc == 0 and out.strip():
            tip = out.strip().splitlines()[0]
            answer["tip"] = tip
    rc, _, _ = run_command(checkout, ["git", "merge-base", "--is-ancestor", pin, tip],
                           PIN_DRIFT_TIMEOUT)
    if rc != 0:
        answer["state"] = "diverged"
        answer["why"] = "the pin is not an ancestor of the upstream tip"
        return answer
    rc, out, _ = run_command(checkout, ["git", "rev-list", "--count", f"{pin}..{tip}"],
                             PIN_DRIFT_TIMEOUT)
    answer["state"] = "ahead"
    answer["ahead"] = out.strip() if rc == 0 else "?"
    return answer


def bump_item(root: Path, answer: dict[str, str], board: str, seat: str) -> dict[str, str]:
    """File one bump item for a drifted pin, or report the one already filed.

    The external ref is stable for a (fork, pin) pair, so a detector run on every drain
    poll files one item per pin rather than one per poll. A later pin gets its own item.
    """
    external = f"{PIN_DRIFT_EXTERNAL}:{answer['fork']}:{answer['pin']}"
    rc, out, _ = run_command(root, [board, "item", "show", external, "--json"], PIN_DRIFT_TIMEOUT)
    if rc == 0:
        try:
            body = json.loads(out)
            item = body.get("item") or body
            if isinstance(item, dict) and item.get("id") is not None:
                return {"state": "exists", "id": str(item["id"]), "external": external}
        except ValueError:
            pass
        return {"state": "exists", "id": "?", "external": external}
    ahead = answer["ahead"] or "?"
    title = (f"Bump {answer['fork']} pin: upstream {answer['branch']} is {ahead} commit(s) ahead")
    criterion = (
        f"The pack's source ref for {answer['fork']} is a hex commit at upstream "
        f"{answer['branch']}'s tip (or a reviewed later commit), the series merges it, and "
        f"the fork-quality suite passes at the new pin.")
    description = "\n".join([
        "Filed by tools/fork-check.py --pin-drift.",
        "",
        f"fork:      {answer['owner']}",
        f"pin:       {answer['pin']}",
        f"upstream:  {answer['branch']} {answer['tip']}",
        f"ahead:     {ahead} commit(s)",
        "",
        "The pin holds the launch at the commit the series was tested against, so an",
        "upstream push no longer carries this fork's quality suite into an unrelated",
        "landing pass. Adoption is this item's work: move the pin in",
        "yolo-packs/matt/pack.json, merge the patch branch onto the new commit, run the",
        "full fork-quality suite, and land the result. A bump that changes the patched",
        "tree runs the full quality suite, full stop.",
        "",
        "Reproduce: tools/fork-check.py --pin-drift",
    ])
    rc, out, _ = run_command(
        root,
        [board, "item", "add", "--title", title, "--criterion", criterion,
         "--description", description, "--external-ref", external, "--seat", seat, "--json"],
        PIN_DRIFT_TIMEOUT,
    )
    if rc != 0:
        return {"state": "failed", "id": "", "external": external,
                "why": (out.strip().splitlines() or ["board item add failed"])[-1]}
    try:
        body = json.loads(out)
        item = body.get("item") or body
        if isinstance(item, dict) and item.get("id") is not None:
            return {"state": "filed", "id": str(item["id"]), "external": external}
    except ValueError:
        pass
    match = re.search(r'"id":\s*(\d+)', out)
    return {"state": "filed", "id": match.group(1) if match else "?", "external": external}


def run_pin_drift(root: Path, file_items: bool, board: str, seat: str) -> int:
    """Report every pinned fork's distance from its upstream, and file the bumps."""
    series = pinned_series(read_series(root))
    if not series:
        print("fork-pin-drift: no pinned series; nothing to compare")
        return 0
    print(f"fork-pin-drift: {len(series)} pinned series")
    owed = 0
    for one in series:
        answer = pin_drift_one(root, one)
        state = answer["state"]
        if state == "at-pin":
            detail = "at the pin"
        elif state == "ahead":
            detail = f"AHEAD by {answer['ahead']} commit(s)"
            owed += 1
        elif state == "diverged":
            detail = "DIVERGED (the pin is not an ancestor of the upstream tip)"
            owed += 1
        elif state == "unchecked":
            detail = f"differs from the pin; {answer['why']}"
            owed += 1
        else:
            detail = f"cannot answer: {answer['why']}"
        print(f"  {answer['owner']:<24} pin {answer['pin'][:12]}  "
              f"upstream {answer['branch']} {answer['tip'][:12] or '?':<12}  {detail}")
        if file_items and state in ("ahead", "diverged", "unchecked"):
            filed = bump_item(root, answer, board, seat)
            if filed["state"] == "filed":
                print(f"    filed item {filed['id']}: bump {answer['fork']} pin")
            elif filed["state"] == "exists":
                print(f"    item {filed['id']} already tracks this pin ({filed['external']})")
            else:
                print(f"    FAILED to file: {filed.get('why')}")
    if owed:
        print(f"fork-pin-drift: {owed} bump(s) owed"
              + ("" if file_items else "; run with --file to file them"))
    else:
        print("fork-pin-drift: every pin is at its upstream")
    return 0


def main(argv: list[str]) -> int:
    """One gate run, serialized against every other gate run on this repository.

    The lock is taken before the record is read and released after it is saved,
    so the whole run -- plan, checks, baseline advance, run entry -- is one atomic
    update of the shared state. A run that cannot take the lock in time refuses
    (exit 2) with whose run holds it, and records nothing: an unmade update beats
    a clobbering one. `--triage` is a read of the last failure's packet, so it takes
    no lock and runs no check.
    """
    preparser = argparse.ArgumentParser(add_help=False)
    preparser.add_argument("--lock-timeout", type=float, default=DEFAULT_LOCK_TIMEOUT)
    preparser.add_argument("--no-wait", action="store_true")
    preparser.add_argument("--triage", nargs="?", const="", default=None)
    preparser.add_argument("--preflight", action="store_true")
    preparser.add_argument("--replay", action="store_true",
                           help="re-run a red gate's failing checks on one revision; writes no record")
    preparser.add_argument("--registry-json", action="store_true",
                           help="print the check registry as data and run nothing")
    preparser.add_argument("--pin-drift", action="store_true",
                           help="report how far each pinned fork's upstream is past its pin, "
                                "and with --file file a bump item for each drifted pin; runs no check")
    preparser.add_argument("--file", action="store_true",
                           help="with --pin-drift, file a board bump item for each drifted pin")
    preparser.add_argument("--board", default="board",
                           help="with --pin-drift --file, the board CLI to file through "
                                "(default: board)")
    preparser.add_argument("--seat", default="whale",
                           help="with --pin-drift --file, the seat the bump item is filed under")
    known, _ = preparser.parse_known_args(argv)

    # The preflight is a diagnostic, so it takes no record lock: it writes nothing, and a
    # preflight that blocked a landing gate (or waited behind one for half an hour) would
    # be a diagnostic nobody runs before a fix. It is dispatched before the common Git
    # directory is even read, because it needs none of what the lock protects.
    if known.preflight:
        return run_preflight(argv)
    # A replay is a diagnostic of a BASE REVISION, not a verdict about a candidate: it
    # writes no record and advances no baseline, so it takes no record lock and is
    # dispatched before the common Git directory is read, the same shape as --preflight.
    if known.replay:
        return run_replay(argv)

    root = Path(__file__).resolve().parents[1]
    # The pin-drift detector is a read of the upstream and the checkout, plus one board
    # write with --file. It takes no record lock and runs no check, so it is dispatched
    # before the common Git directory is read -- the same shape as --preflight.
    if known.pin_drift:
        board = known.board or "board"
        return run_pin_drift(root, known.file, board, known.seat)
    # The registry export is a property of the tree: it runs no check, fetches no
    # fork, and reads no record (it plans against /dev/null). It takes no record
    # lock for the same reason -- a pure export that waited half an hour behind a
    # landing gate, or refused because one was running, would be a diagnostic
    # nobody runs. Dispatched before the common Git directory is read, the same
    # shape as --preflight and --pin-drift.
    if known.registry_json:
        return registry_json(root)
    try:
        common = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "--git-common-dir"],
            capture_output=True, text=True, errors="replace",
        )
        common_dir = (Path(common.stdout.strip()) if os.path.isabs(common.stdout.strip())
                      else (root / common.stdout.strip()).resolve()) if common.returncode == 0 else None
    except OSError:
        common_dir = None
    if common_dir is None:
        print("fork-check: REFUSED -- cannot locate the repository's common Git directory, "
              "so the record cannot be read and nothing was verified.", file=sys.stderr)
        return 2
    if known.triage is not None:
        return show_triage(common_dir, known.triage)
    # The record is an append-only log in that common directory: a save appends one line
    # and takes no lock, so check execution never queues behind another gate's record
    # write at all. `--tier landing` and the concurrent lanes both depend on that.
    return run_gate(argv)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
