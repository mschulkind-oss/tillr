# tillr-drain — the pass, and the loop that advances items

> [!WARNING]
> **Known gaps.** Two things about `tillr` are not what a first reading assumes:
> 1. **The observatory front end is a separate build** — a `tillr-view` built by a plain
>    `go build` answers 404 for the UI. The front end under `tools/tillr-view/web` is a
>    Node build, embedded only under the `observatory` build tag (`just observatory` does
>    both) or served from a directory named with `--web-dist`.
> 2. **A `tillr` installed from the pack is not this working tree** — the pack under
>    `yolo-packs/tillr` builds from its own mirrored copy of the source, which
>    `just pack-sync` regenerates. To run an edit under `tools/tillr*`, build the working
>    tree with `just tillr-build`.

## What this module owns

`tillr-drain` turns store state into per-item successor decisions and, as `loop`, carries
them out unattended. It reads items, lanes and runs from the tillr store (the SQLite file
at `<root>/.tillr/state.db`) and never judges work itself: the gate, the lander and the
verdict are programs the caller names.

The words this document uses:

- An **item** is one piece of queued work with criteria. A **run** is one execution of a
  harness (the agent program an adapter starts) for an item.
- A **pass** visits items in queue order and applies at most one transition per item.
- A **lane** is a worktree the loop stages work in, under `<root>/.tillr/lanes/<lane>/`.
- A **seat** is a workflow role such as `build` or `design`; a **backend** is the execution
  service the backend policy maps a seat to.
- A **candidate** is the committed change a run leaves in its lane. The **trunk** is the
  branch `main`.
- A **hold** is a recorded reason an item cannot advance; its **mover** is the question,
  item, run, lane or operator action that can advance it.
- An **examination** is the loop settling or judging one finished run.

The faces, each of which prints its own flags with `--help`:

- `plan` computes one pass and writes its decisions to the file the caller names. It writes
  nothing else. `once` plans the same way, records the decisions in the store's `dispatches`
  table, and hands each run request to the `record-sink` runner.
- `loop` is the unattended supervisor. `loop --check` validates flags, settings, policies,
  step programs and any required store migration without starting a pass.
- `why` explains every item's position and mover; `policy` prints the backend policy in
  force; `workflow check` validates workflow files.
- `retry`, `clear-fault`, `acquit`, `examine` and `set-aside` are the operator's acts on a
  held item. `split` files a design seat's division of a request into parts.
- `stop`, `restart` and `scale` act on a running loop. `resume` continues one run in its own
  session.
- `push`, `accept-rewrite`, `acknowledge-patch-move` and `sweep-refs` act on the trunk's
  publication, its history and tillr's refs. `probe-backend` records a smoke check of a
  backend.
- `boundary snapshot` and `boundary check` record the write boundary before and after a run
  and compare the pair.

The design that divides this surface into `--help`, this README and the composition
skill is [`docs/reference/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-distribution.md).
Positions and transitions are defined in the
[drain successor reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-drain-successor.md).

## Moving a running loop onto new code

Start the drain and observatory from the `just tillr-build` bin directory.
After a tillr landing the loop runs `just tillr-roll --no-request`, then requests restarts; under after-merge proof, the proof runner owns the suites.
The drain starts work on free lanes while an in-place restart waits for a gate or landing. Replacement leaves live workers on their original leases; a refusal leaves the old loop running.
After a manual commit, run `just tillr-roll` (in-place) or `just tillr-restart` (rebuild HEAD, restart keeper and loop).
Pack launchers use a separate install: run `yolo host apply --assert` before requesting a restart.

## Standalone invocation

```sh
tillr-drain plan
tillr-drain once
tillr-drain loop
tillr-drain resume --run <run-id>
tillr-drain push --root <repository>
```

`plan` is the read-only half an external caller composes against its own reporter. `loop`
is the whole service and needs the gate, land and verdict programs named, or a store
definition whose hook program supplies them
([step-program argument contract](#step-program-argument-contract)).

Interrupting `resume` or `once` stops that command, not its worker. The run face (the
`tillr-run exec` process the drain started) runs in its own process group, finishes, and
writes its terminal record; the next loop settles it.

### Exit codes

Every face answers with the suite's record contract (`tools/tillr/record/exit.go`): 0 for
completion, 1 for a completed negative decision, 2 for a usage error or malformed input, 3
for an incompatible store, 4 when an external dependency prevented a decision, and 5 for an
internal fault.

`loop` maps its stop reason through `StopExitCode`. `queue-drained` (only with
`--exit-when-drained`: nothing is dispatchable and no run is in flight) and `operator-stop`
(SIGINT, SIGTERM or `tillr drain stop`) exit 0. `lanes-leased` (nothing is dispatchable
because a lane is leased with no expiry by another owner) exits 1. A `fault`
([which faults](#faults-that-stop-the-whole-loop)) exits 5. `restart --wait` exits 1 when
the loop refuses the request, and `resume` exits with the run's own nonzero exit code.

### Files and environment

Read:

- `<root>/config/tillr/backend-policy.json` (or the `--policy` file), `gate-policy.json`,
  `deny-list.json`, `commands.json` and `workflows/` in the same directory, and
  `plan-guidance.md` when present.
- `<root>/.tillr.toml`: the tables `lanes`, `push`, `roll`, `comments`, `workflows`,
  `prose`, `sync` and `candidate_size`.
- `tools/tillr-run/backends/brief-template.md` (or the `--brief-template` file), from which
  staging renders each brief.
- `bin/pi-forks.list` under the root, for [fork handling](#fork-handling).

Written:

- The store, and `<store>.loops/*.present`, a presence file each loop holds open for writing
  while it runs.
- `<root>/.tillr/loop/`: one record per loop (`drain-<first>-<last>.json`), one directory
  per run, `backend-policy.json` and `backend-policy-changes.jsonl`.
- `<lane root>/<lane>/`: the worktree and, beside it, `brief.md`, `session-id`, `fence.txt`,
  `base_sha`, `target` and `brace-literals`. The loop also keeps its own directories under
  the lane root: `.requests/`, `.fixers/`, `.rolling/`, `.replay/` and `.revert/`.
- In the Git common directory: `tillr-loop.lock`, the heavy runner's files beside
  `tillr-gate/`, and refs under `refs/tillr/`
  ([ref lifetimes](#ref-lifetimes-and-backlog-cleanup)).

Environment the drain reads: `TILLR_SQLITE_TRACE` (`1` traces store statements),
`TILLR_RESTART_REQUEST` and `TILLR_RESTART_FROM` (set by an in-place restart),
`TILLR_PI_BUILDS_DIR`, and `CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME` ([landing calls](#landing-calls)). It marks every
process of a run with `TILLR_RUN_ID` and `TILLR_LOOP_STORE` (the canonical store path), and
hands a rolling program `TILLR_GATE_SPANS_OUT`.

## The loop's steps

At startup the loop takes the repository's loop lock, records its session, checks every step
program's arguments, stages its lanes, and recovers runs whose owning loop is gone. Then it
repeats, waking on a store notification, a lane release, a trunk move, or the
`--fallback-poll` interval:

1. **Read the policy.** A changed `--policy` file is checked and, when valid, becomes the
   policy in force ([backend policy changes](#backend-policy-changes)).
2. **Sync the documents.** At a new trunk commit the loop runs `tillr-item sync` (the
   `--item-bin` binary) and refreshes the open-question index. Passes at the same commit
   reuse both. A block with a finding holds its item; a sync that cannot run at all is a
   fault.
3. **Observe the trunk.** The tip is compared with `trunk_history`
   ([Git movement](#git-movement-and-run-convictions)).
4. **Probe quota** for backends whose reading is stale
   ([backend quota admission](#backend-quota-admission)).
5. **Reap and reconcile.** Lanes whose examination is recorded done are reclaimed, held
   candidates release or reclaim lanes, and refs are cleaned.
6. **Plan.** The planner computes each item's position and transition: `dispatch`, `retry`,
   `settle` or `judge` when work can advance, and `none` with a reason otherwise.
7. **Claim and stage.** A dispatch claims its lane, backend and attempt in one store
   transaction, then stages the lane: worktree, base commit, brief, fence and session id.
8. **Run.** The loop starts `tillr-run exec` for the request inside a boundary bracket (a
   snapshot of refs and worktrees before and after).
9. **Settle.** The finished run is classified, its boundary checked, and its ending routed:
   a candidate, an obstacle, a refusal or a continuation.
10. **Judge.** A candidate passes the deny-list check, any opted-in judgment, and the gate
    program.
11. **Land.** A passing candidate goes to the land program; the verdict program then
    records the item's criteria.
12. **After the landing** the loop triggers the after-merge heavy run, any roll, and any
    push the repository's settings ask for.

Dispatched runs and examinations proceed independently; the loop does not wait for a batch
to finish. When nothing is dispatchable and no run is in flight it prints the held count
and the first five items with their reasons once, then waits. After consecutive
infrastructure failures it waits `--backoff-base`, doubling to a five-minute cap; that is a
wait, never a stop.

## State invariants

- **`plan` writes nothing.** A preview that moved state would make "what would happen" and
  "what happened" the same question.
- **The loop owns the order and the stopping conditions, never a step's judgment.**
- **There is no pass, spend or token budget.** The pile is the only throttle. The loop is a
  daemon unless `--exit-when-drained` is given.
- **A held lane is never reported as a drained queue.** `lanes-leased` and `queue-drained`
  are different answers.
- **A seat is a role, not a model.** An item names a seat and never a model ID. If the
  active policy does not map the seat, the item waits as `seat-unmapped` while the loop
  continues with other items. The hold names the seat and policy file. It clears when the
  operator puts a policy that maps the seat in force; no default backend fills a named
  seat. If the seat's backend is closed, the item also waits.
- **An adapter's refusal of a brief is the brief's fault, not the backend's.** An adapter
  that prints `run-<adapter>: refused: <reason>` and exits 2 classifies as `refused`: no
  backend is closed, and the item is held as `adapter-refused` until
  `tillr drain clear-fault`. An exit 2 without those words is an infrastructure failure.
- **A run id carries one counter and a bounded stem.** The second and later restart, fresh
  session or interruption of one node is `<id>-<count>-<kind>`, and a digest bounds the stem
  so every run id stays under 120 characters.
- **Shares choose the backend for new work.** Neither shares nor item overrides send an
  infrastructure-failed run to another backend.

## Pass observations and report identity

The input kind is `drain.pass`. Its payload needs no `now` or `available_lanes`: the
envelope's creation time supplies the observation time, and registered lanes are offered in
lane-id order. A caller may supply those fields, and `max_dispatches`, for a simulation;
reaching the cap simulates `no-lane`. The live loop supplies no cap. Each plan record prints
`position` and `transition`. A missing usable policy is a fault, not an item hold.

An applied pass receives a store-counted **generation**, the ordinal defined by the
[trigger and generation model](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-drain-successor.md#generations-timing-and-the-trigger-loop).
An empty pass allocates none. Timing lives in `drain_passes`, whose `tick_id` column carries
the generation: `claim_ms` measures from the later of item readiness and lane availability
to the completed claim, and `stage_ms` records staging separately. `gate_ms`, `land_ms` and
`gate_count` measure ordinary candidates only; a change to the gate runner proves its own
suite before merging
([the runner-change rule](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/gate.md#affected-go-tests-before-the-merge))
and is recorded apart as `runner_gate_ms`, `runner_land_ms` and `runner_gate_count`.

Loop reports are named by their inclusive first and last generations: `drain-1-3.json`
holds record id `loop-1-3`, and `drain-0-0.json` is a loop that applied no generation. An
existing `drain-0-0.json` is archived to `drain-0-0-<unixNano>.json` before the new one is
written. On exit the loop reports the spend it accumulated, separating work it started from
earlier loops' runs it settled; a sum that omits a measurement is labeled a lower bound.

## The commit a new lane starts from

The gate policy's `lane_base` defaults to `green`. A new dispatch records and stages the
commit at `refs/tillr/green`, the
[green pointer](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/gate.md#after-merge-proof-and-the-green-pointer) that
names the last proved trunk commit. With `tip` it stages the trunk tip. A trunk fixer (the
generated item that repairs a red trunk check) always starts from the tip.

- **A finished blocker must be proved.** Under `green`, a dependent item waits with
  `trunk-unproved` until the green pointer contains its blocker's landing.
- **The lane carries the item's own block.** When the green pointer holds another version
  of the item's document block, or none, the claim cuts the lane from the oldest trunk
  commit after green that carries the tip's version.
- **A stale base is the drain's fault, not the item's.** When a worker reports `blocked`
  and a trunk commit after its base carries the dispatched block, or a blocker landing its
  base lacks was recorded before the dispatch, the loop records `drain.stale-base` and
  treats the run as interrupted.
- **With no pointer** the dispatch uses the tip and the loop reports that choice once. An
  invalid pointer, or one outside trunk history, is an error.

The decision is recorded in the dispatch's `base_sha`.

## Express lanes

The [saved-idea rule](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-actions.md#the-saved-idea-item)
names **express work**: a saved idea, a [filed request](#filed-requests) not filed with
`--ordinary`, and an item whose block sets `express="true"`. Express lanes are reserved for
that work and do not consume the ordinary `--lanes` count. The `[lanes]` table of
`.tillr.toml` sets their count with `express = <n>` (default zero), and the loop names them
`express-1`, `express-2`, and so on.

Express work prefers an express lane, then a free ordinary lane, and otherwise waits.
Ordinary work never uses an express lane, and no running work is stopped to make room.
Review-comment work stays at its document's
[desk](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-designer-sessions.md#the-desk), a lane assigned to that
document, and counts in neither pool.

## Backend policy changes

Each seat names one backend unless it gives explicit shares to every backend it lists. An
unshared multi-backend list is refused with the seat's name. The keys `preference`,
`max_failovers` and `red_cap` are accepted and ignored
(`TestLegacyPreferenceAndFailoverBudgetAreIgnored`). `read_only_seats` and `seat_purposes`
reference seats already mapped in `seats`; workflow loading refuses an `access: write` node
on a read-only seat with `workflow-read-only-seat-writes`.

At the start of each pass `loop` reads its `--policy` file and compares its SHA-256 with the
previous reading. The startup checks must pass before the new content becomes the policy in
force. Every dispatch planned from that pass uses it; a running request keeps its backend
and command. Invalid or unreadable content prints the startup refusal and leaves the
accepted policy in force, and `tillr view waiting` shows one policy warning row until a
valid reading. Each changed reading is appended to
`.tillr/loop/backend-policy-changes.jsonl` with the previous, new and accepted hashes, and
the accepted policy is kept in `.tillr/loop/backend-policy.json`.

`tillr drain policy` prints the accepted snapshot without activating disk edits. Before a
loop has recorded a policy it refuses.

## Seat backend shares and selection

To spread fresh dispatches across backends, give every entry of a seat a positive numeric
`share`:

```json
{
  "seats": {
    "build": [{"backend": "claude", "share": 3}, {"backend": "pi", "share": 1}],
    "review": {
      "backends": [{"backend": "claude", "share": 1}, {"backend": "pi", "share": 1}],
      "pick": "round-robin"
    }
  }
}
```

Each named backend needs a row in the policy's `backends` map. A seat object's `pick` is
`in-order` (the default: the backend whose next dispatch is earliest in the proportional
count order, ties in list order), `round-robin` (forward through the list from the seat's
last recorded dispatch) or `random` (one draw per fresh dispatch with odds proportional to
shares).

The choice is made inside the store transaction that claims the dispatch
(`selectSeatDispatch`; `selectReviewDispatch` for an agent review outside a workflow) and
recorded once in `seat_dispatches`. Failed transactions and cancelled staging claims count
nothing. The claim chooses before checking availability: if the choice is closed, the item
waits for that backend. An item override wins for a worker's fresh dispatch. Interrupted
runs keep their backend.

## Backend quota admission

A backend row can name `quota`, an argument list for a command that reads remaining quota
without starting work, and `reserve`, the fraction of every window kept for the owner
(default 0, range 0 through 1):

```json
"quota": ["tools/tillr-run/backends/claude/quota"],
"reserve": 0.2
```

The command runs from the repository root and answers one JSON document:

```json
{"windows":[{"kind":"five_hour","fraction":0.8,"reset_at":"2026-11-19T13:00:00Z"}]}
```

Before planning, the loop probes readings older than `QuotaProbeInterval`, changed commands
and reached resets. A missing, malformed, failed or stale reading holds that backend.
Commands are limited to 15 seconds and 64 KiB of output. The latest reading is in
`backend_quota`. With reserve 0.2 a window at 0.8 used closes admission. Checks happen while
choosing and claiming a backend, never after staging, and do not stop running work.

When a run is refused for quota, the loop records the paused run in `quota_pauses`, keeps
its lane, and waits. A refusing run that left work continues under the same run id when the
backend reopens; one that left none starts a new run there. The quota command for the
`claude` backend is described in the
[backend reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-claude-backend.md#direct-readings-and-the-owners-reserve).

## Stages that name workflows

A **workflow** is a tracked JSON file in
[`config/tillr/workflows/`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows) whose stages become the nodes one
item runs. The schema, selection, expansion, proposals and evaluator routing are in
[the item-graph reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-item-graphs.md#workflows-selection-and-expansion).
What the drain does with one:

- A `workflow` stage is replaced before dispatch by the executable nodes of the file it
  names, with ids `<stage>/<inner id>`. A whole value spelled `${args.name}` substitutes a
  declared argument; no expression is evaluated.
- An item that names a workflow may carry `workflow_args`
  ([item record](https://github.com/mschulkind-oss/tillr/blob/main/tillr-item/README.md#workflow-arguments-and-store-declared-items)). A
  workflow that declares `args` and is named without them holds the item as
  `workflow-argument-missing`.
- The stored graph holds the expanded nodes, parents, scopes and SHA-256 hashes of every
  contributing file. Continuation and restart use that snapshot, not edited files.
- A planner's refused `graph.proposal` leaves the graph intact and records its rule, such
  as `proposal-seat-unmapped` or `proposal-scope-outside-fence`.

`tillr drain workflow check <file-or-directory>` applies the same loader and prints each
file's node count, dependency depth and fan-out. Its `--help` lists every rule id.

### Document triage

[`doc-triage@v1`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/doc-triage@v1.json) checks a list of
documents for relevance and accuracy. `tillr-item doc-triage` files the item
([how](https://github.com/mschulkind-oss/tillr/blob/main/tillr-item/README.md#filing-a-document-triage)). Its `read` stage repeats over
`args.docs` (at most 48), one read-only `scout` node per document, each writing a `finding`.
Its `report` stage is the one writing node, on the `design` seat, and its write scope is
exactly the report path. The triage item is store-declared, so the
[document-fed prerequisite](#document-fed-workflow-prerequisite) does not apply.

### Filed requests

`tillr-item request` files work straight into the queue
([how](https://github.com/mschulkind-oss/tillr/blob/main/tillr-item/README.md#filing-a-request)). The item names a workflow, and its words,
held in the store's `request_words` table, are its whole statement. It never has a document
block. The drain runs it like any other item, with these additions:

- **Brief.** Every node's brief ends with the words in a byte-counted suffix,
  `## Saved request words (verbatim; N bytes)`. An agent judge or a workflow's
  `evaluator_slot` node reads the same suffix.
- **Candidate check.** A candidate that adds, removes or edits an item block is refused
  with a detail starting `request-item-block:` and returns to its worker.
- **Owner questions and repeats.** An `owner` obstacle or an identical-repeat hold names
  `tillr drain retry` on the held run. The retry's text reaches later briefs under
  `## Answered owner questions` or `## Retry reasons`.

`build` requests default to
[`planned-build@v1`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/planned-build@v1.json) and `design`
requests to [`scouted-design@v1`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/scouted-design@v1.json).

### Filing a design-seat split

`tillr drain split --item <parent-id>` reads the design seat's statement on standard input.
The parent is a queued or blocked request with no attempt in flight.

```json
{
  "parts": [
    {"label":"core", "title":"Build the core", "share":"Implement the core behavior", "leaves":{"cases":"Add the edge cases"}, "criteria":["The core cases pass"], "required":true},
    {"label":"cases", "title":"Cover the edge cases", "share":"Add the edge cases", "follows":["core"], "criteria":["The edge cases pass"], "required":true}
  ],
  "coverage": {"c5":[{"part":"cases"}]}
}
```

`leaves` maps sibling labels to work left to them; `follows` lists siblings that finish
first. A coverage entry without `clause` assigns the whole criterion; `{"closing_check":true}`
leaves an end-to-end criterion to the final check. Replaying a statement changes nothing,
and a refused statement files nothing. Once every required part has landed the parent stays
blocked and `tillr drain why` names the operator to check its criteria.

## Node write scopes

A writing stage may set `write_scope` to `"inherit"` (the default) or a nonempty list of
repository-relative path prefixes; the loader's rules are in
[the item-graph reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-item-graphs.md#lane-lifecycle-write-scopes-and-chained-commits).
Writing nodes run one at a time in the item's single Git target, and at each exit the drain
checks only that node's changed paths. An escape, or a dirty target left by a node that
ended with a readable answer, is refused as `boundary-scope-escape`, naming the node and
path. Passing every node does not waive the
[deny-list check](#the-deny-list-and-the-session) of the whole committed change.

## Parallel read-only nodes

Ready read-only [nodes](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-item-graphs.md#the-words) of one item
start together on its lane and worktree, bounded by the workflow's `max_parallel` (4 when
omitted, at most 8). The item's lane stays leased until every reader has a terminal record;
a changed HEAD or dirty tree then holds the item as `boundary-scope-escape`
([the claim and its tables](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-item-graphs.md#parallel-read-only-nodes)).

A node's answer is the word after its report's last `RESULT:`, read through Markdown
emphasis. An agent node whose footer is missing, or names none of `done`, `failed` and
`blocked`, gave no answer and runs again. A node that ended `blocked` with no cause holds
as `node-answer-unread: <node>`; its mover is a retry of the item's latest finished run.

## Registered command nodes

A stage with `kind: command` runs a registered program, not a model. Its `command_ref` names
an entry in [`config/tillr/commands.json`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/commands.json), which supplies
`argv` and a `flags` map from accepted long flag names to `string`, `number` or `path`
([the registry](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-item-graphs.md#shipped-workflows-and-command-registry)).
Flags become separate `--name=value` arguments in sorted order; no value is interpreted by a
shell. The command runs through `tillr run exec` with backend `command`: no model route,
spend or quota window. Exit 0 passes and exit 1 fails; any other exit, a program that cannot
start, and an expired `timeout_ms` hold the item as `command-fault`. A failed command
follows a ready `when_outcome: fail` edge; otherwise the item holds as
`node-failed: <node>`.

## Document-fed workflow prerequisite

**An item needs a document block before it is built.** A store is document-fed when at
least one stored item has a block in the tracked documents that sync reads. Terminal items,
generated items (`IsGeneratedItem` in [`drain/defect.go`](./drain/defect.go): `fixer-main-*`,
`defect-*`, `rewrite-*`, `revert-main-*`, `gate-budget-*`) and store-declared items (a
[document triage](#document-triage) or a [filed request](#filed-requests)) are exempt.

An open item without a block goes to **triage**, the design-seat run that shapes a stored
request into a document declaration, with reason `triage-no-block`. Its fence (the paths a
run may change) admits only the Markdown document its body reference names. Its brief
carries the stored title, reference, criteria and the
[prose guide](https://github.com/mschulkind-oss/tillr/blob/main/.agents/skills/tillr-prose-style/SKILL.md), and offers three results:

1. A ready block with the same item id, committed for the gate and landing.
2. That block plus a numbered owner question; after landing the `question-open` rule holds
   the item.
3. A report naming a duplicate item or explaining why the work is already done.

Before the gate, the loop checks the candidate with sync's parser; any finding or warning
on the triage block refuses it. A report with no block, a blocked run, or a reference that
names no Markdown document under `docs/` becomes an owner conflict, shown under
`conflict-owner`. The owner closes the item or answers the conflict. Each version of the
stored title, body reference, and criteria gets at most one run; a run a loop stop
interrupted, or a claim staging cancelled, does not count as that run.

## Prose rewrites before build

[Opt-in sync rewrites](https://github.com/mschulkind-oss/tillr/blob/main/tillr-item/README.md#automatic-prose-rewrites) run on the build
seat. Queue planning places trunk fixers first, then rewrites, then ordinary items, keeping
rank order within each group. When every offered lane is occupied, a trunk fixer or a
remote-sync item is dispatched to `desk-trunk-fixer`, a lane only those items use, one at a
time (`lane == "desk-trunk-fixer"` in [`drain/start_proposal.go`](./drain/start_proposal.go)). The original waits with `awaits-rewrite` until the rewrite
lands or its block passes the prose check. When the original has more than eight criteria,
its rewrite waits on the owner with `prose-criteria-count`.

Before the gate, the loop compares the original block in the base and candidate. A changed
`id`, `paths`, `blocked_by` or `seat`, an added, removed or renumbered criterion, a missing
block, or a remaining prose finding refuses the rewrite. A valid rewrite lands without
retiring the block.

## Opted-in judgment before the gate

An item directive with `judge="agent"` asks for an independent, read-only review of its
candidate. The drain persists a fixed `review` evaluator before the worker starts; the
policy must map its seat. A workflow may instead set `exit.evaluator_slot` to a read-only
node that follows every writer and declares `judge_classes`
([workflow evaluator](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-item-graphs.md#workflow-evaluator)). An
item with both gets only the workflow's evaluator.

The review runs in the worker's lane after the deny-list check, with its own run and
session. Its brief carries the criteria, their hashes, the candidate's tree identity, the
path to the retained `candidate.diff`, and the path for its verdict. A review that commits
or dirties the worktree is refused and the candidate restored. The
[item judge intake](https://github.com/mschulkind-oss/tillr/blob/main/tillr-item/README.md#written-judge-verdicts) checks the written
file:

| Finding | Effect |
| :--- | :--- |
| Missing or refused file | `judge-unreadable`, attributed to the review run. |
| `not-done`, ordinary judge, document item | `eval-failed`; no gate, landing or verdict. |
| `not-done`, ordinary judge, filed request | Returned to the builder's session. |
| `not-done`, workflow evaluator | `verify-not-done`; [returns to the build step](#a-refused-run-goes-back-to-its-worker). |
| `blocked` | The item is blocked with the entry's reason and `waiting_on.waiter`. |
| `defective` | The item closes with `defective: <criterion>: <reason>`. |

For mixed findings `defective` outranks `blocked`, which outranks `not-done`. A pass
permits the gate and landing; only a successful landing records the checked verdicts, bound
to the landed commit, in the same transaction that marks the item landed.

## Settled harnesses

A worker can answer and commit without its harness exiting. The
[run face's completed-session rule](https://github.com/mschulkind-oss/tillr/blob/main/tillr-run/README.md#ending-a-settled-harness-that-lingers)
ends such a harness and records `end_cause: settled-idle`. Because the report answered, the
drain classes the attempt as `answered`: candidate examination and the gate follow, not an
infrastructure retry. A pi run whose adapter ended its settled session
([pi's ending](https://github.com/mschulkind-oss/tillr/blob/main/tillr-run/backends/pi/README.md#ending-a-settled-session-pi-does-not-exit))
is judged on its answer the same way.

## A session pi ended for a repeating call

The runner records a session that pi ended for one repeated tool call with
[`end_cause: repeating-call`](https://github.com/mschulkind-oss/tillr/blob/main/tillr-run/README.md#a-session-pi-ended-for-a-repeating-call).
The drain does not judge it or charge it to the backend. It records `drain.repeating-call`,
pins the run's commit at `refs/tillr/runs/<run>`, preserves uncommitted work, and either
starts a new session on the same backend, whose brief names the repeated call, or holds the
item for the operator as `repeating-call`. The hold is taken when pi already recovered in
the run (a recorded `loop_recovery` or `action: "ended"`) or the replacement session also
repeats. The dispatch decision carries the ending it answers as `repeated_call`; there is no
counter. A workflow node takes the same routes. Tests:
[`drain/repeating_call_test.go`](./drain/repeating_call_test.go).

## Landing calls

A **landing call** is a worker's request, made as a tool call, that the drain judge and land
its commit ([the design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-landing-call.md)). The drain owns one store
record per call id: `call.offered` binds the offer to its run, session, worktree, base and
head, and `call.answered` stores the one answer. The harness publishes `landing-offer.json`
in the run's directory, and the drain writes `landing-answer.json` in the directory of the
run that delivers the answer. At loop start the drain records each adapter's
`ending-capability`, `footer` or `call-suspends`; an absent binary records `footer`.

## Nothing re-runs because an input changed

A pass does not compare a held item's block, workflow file, allowed paths, backend policy
or running build with the inputs recorded at its dispatch. An item moves on an event that
names it: `tillr drain retry`, a design answer, or the recovery of a killed run
(`TestAChangedInputReDispatchesNothing` in
[`drain/readmit_test.go`](./drain/readmit_test.go)). A retry authorization naming a run
other than the item's latest run does not speak.

A loop stop or fault leaves in-flight runs alone:

```sh
tillr drain stop --root <repository>
tillr drain stop --root <repository> --end-runs
```

The first stops supervision only. `--end-runs` is the drain's only run-ending path: it
records the interruption, then sends SIGTERM to each run face. An interruption is no attempt
outcome; the next pass dispatches the item again on the same backend.

A stop takes effect without waiting for an in-flight gate or trunk proof: the loop cancels
its run context and exits `operator-stop` without recording a verdict on interrupted work.
Gate steps run under a subreaper supervisor (a process that adopts orphaned descendants),
so descendants in other sessions are ended. A rolling or replay program gets SIGTERM, then
five seconds, then SIGKILL. A stop acts on what its process domain (boot, PID namespace and
user) can see and reports the rest as `loop: stop left alone:` lines; a live loop in
another domain makes the stop exit nonzero.

At startup a replacement loop reclaims a claim with no terminal record only on evidence
that its owner is dead and no worker survives: an earlier boot, an ended drain session, a
dead recorded process, or, for another process domain, a presence file with no writer and
no process holding `run.record.json` or `run.stderr` open. Reclaimed runs are closed as
interrupted, their work kept under `refs/tillr/preserved/<run-id>`, and their items offered
again. A claim without that evidence stays protected.

### A worker ends with its commit or a named obstacle

A single-worker build run has two endings: its commit, or `RESULT: blocked` with a line
`OBSTACLE: <kind>` and that kind's fields
([the design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)).
An **obstacle** is one of five named reasons:

| Kind and fields | Route |
| :--- | :--- |
| `definition` (`CRITERIA`, `EVIDENCE`) | A design-seat conflict. |
| `owner` (`QUESTION`) | A design-seat conflict carrying the question; the design seat answers or objects `NEXT-ACTOR: owner`. |
| `trunk` (`CHECKS`, `BASE`) | The named checks are replayed on the base and tip. All red: a trunk fixer is filed and the item waits on it. Red only on the base: the lane is rebased onto the tip and the session continues. |
| `tooling` (`COMMAND`, `EVIDENCE`, `PATHS`) | A defect item `defect-<digest>`; the item holds as `tooling-defect` behind it. |
| `seat` (`TRIED`, `MISSING`, and a change in the worktree) | `drain.seat-escalated` moves the item to the next backend on the policy's `escalate` list; past its end, a design-seat conflict. |

`RESULT: failed`, a blocked report without a complete obstacle, and a report with no footer
are a missing output: with no commit the run is refused as `no-op` and returns to its
worker. A filed request records `drain.obstacle-hold` instead of a conflict: `owner` holds
on the owner with the question, `trunk` holds until a green proof covers the checks, and
`tooling` and `seat` hold for the operator. Code:
[`drain/obstacle.go`](./drain/obstacle.go),
[`drain/obstacle_route.go`](./drain/obstacle_route.go) and
[`drain/obstacle_hold.go`](./drain/obstacle_hold.go).

### A refused run goes back to its worker

A worker run's failure is not counted and does not park its item
([the design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)).
When the gate refuses a candidate, a run ends with no commit, a candidate escapes its
fence, a landing is refused, a squash conflicts, or a landing is reverted, the item returns
to its worker with evidence, dispatched as `returned-with-evidence`. The evidence is the
refusal's words, each failing gate check with its log, each failing case's single-case
reproduce command, and the refs that keep the earlier work.

The worker keeps the same lane and session when the adapter can resume it. The next turn
has a distinct run record and must make a new commit: the refused head alone is not a new
candidate. A workflow returns to the producing node; predecessors are referenced, not
rerun. The decision's `worker_session_reason` names the path:

| Reason | Meaning |
| :--- | :--- |
| `resumed-worker` | The session continues on its lane. |
| `cannot-resume` | The adapter declares no resume. |
| `session-missing` | The lane's session handle is missing or does not match. |
| `seat-changed` | The backend or model does not match. |
| `lane-unavailable` | The retained lane is occupied or not offered. |
| `workflow-restarted` | The producing node cannot resume, so the workflow restarts at its entry. |
| `resume-failed` | The harness refused to open the session; a fresh run replaces it. |

A workflow evaluator's `verify-not-done` carries each not-done criterion and its reason
word for word, and the build step is dispatched on the candidate the verifier read. The
`plan` step runs again only when a reason begins `plan-wrong:` or the builder reports
`PLAN-WRONG:`.

**An identical repeat stops repetition, not a count.** A refusal records an evidence key:
the failing checks with digests of their normalized logs, or the uncommitted paths and a
digest of their content, or the refusal's normalized words. When an ending's seat, cause and
key equal an earlier ending of the same item, the item goes to the design seat with both
runs' evidence, or to the owner when the item has no design document or the policy no
design seat (`repeatedEnding` in [`drain/returned.go`](./drain/returned.go)).

**Squash conflicts.** On a moved trunk the loop stages a conflict-fix run with the
conflicting paths and markers. `conflictFix` resumes the session only when replaying the
worker's commits onto the tip is clean; otherwise it restages the lane on the tip and starts
a fresh fix run. A second conflict with the same paths and hunks digest within one retry
cycle restarts the item's workflow from its first stage (`restartConflicted`); a conflict of
that fresh attempt holds as `conflict-owner`.

A normal build exit with uncommitted work and no commit is continued in its own session for
as long as each continuation changes that work; see the
[build-commit continuation reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-build-commit.md).

### Recorded backend availability

An **infrastructure failure** means the harness could not produce a judgable answer: it
failed to start, refused before running, or died without a report. The latest terminal run
on a backend closes that backend only in this class; a later run ending in any other
non-interrupted class reopens it. The loop records the class once, in `backend_outcomes`
and a `drain.backend-outcome` event. An external SIGTERM or SIGINT, a run the loop
interrupted itself, a refusal with a stated reset time, and a resumed session that is gone
do not close a backend.

An infrastructure failure holds the item as `backend-closed`, naming the backend in
`deferred_by`. Recovery admits the work only on its original backend. A repeat of the same
failure (backend, end cause and normalized error words) holds the item as
`infrastructure-repeat` until `tillr drain clear-fault`. No automatic probe or timer spends
work on a closed backend.

```sh
tillr-drain probe-backend --store .tillr/state.db --backend <name> -- <smoke-check-command> <args>
```

Use a command that exercises that backend's harness and route. Exit zero records a
successful `drain.backend-probe` and clears the closure and any active refusal window
(`drain.backend-quota-reopened`); every other exit records a failed probe.

### Owner decisions in documents

An owner decision is a report's question together with at least two alternatives.
Settlement writes it as a numbered Vantage question below the item's block, with an `OC`
id, through the same fence, gate and landing as a design answer. Answer sync indexes the
trunk commit, not the checkout, so an uncommitted edit records no answer. A question counts
as answered when the index marks it answered, or when it is absent from the question list
and its id is in the document's decision ledger. If the id is in neither place, the item
holds as `question-removed` and the operator restores the entry.

`tillr drain loop --migrate-owner-questions` converts owner decision reports held as
conflicts into document questions, prints each conversion or skipped report, and exits.

### A design run that ends without answering

A raised conflict gets one design-seat dispatch, and the claim marks the conflict
`awaiting` that run. Every ending moves the conflict and none is counted
([the design, Section 4.3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9)):

| Ending | Route |
| :--- | :--- |
| A commit that passes the gate and lands | The conflict is `answered` and the item returns to its worker. |
| A commit the gate refuses | The same designer session, with the failing checks, as a new run record. |
| An interrupted or killed run | Its recorded session continues on the same lane when available. |
| An objection, `NEXT-ACTOR: build` | The conflict is answered. |
| An objection, `NEXT-ACTOR: owner`, with a question and alternatives | The [document question](#owner-decisions-in-documents) is published; the conflict is `question`. |
| No commit and no named actor | A missing output: the design seat is dispatched again, never the owner by default. |
| The same cause and evidence key as an earlier design ending (gate refusals excepted) | An owner conflict naming both runs and the command that moves it. |

Each re-raise and repeat writes one `drain.design-unanswered` event. The owner's command for
a conflict held on an objection is
`tillr drain retry --item <id> --run <design run> --by <who> --why <reason>`, which records
`drain.retry.design` and writes no `retries` row. Tests:
[`drain/design_unanswered_test.go`](./drain/design_unanswered_test.go).

## Owner edits and waiting landings

Uncommitted content in the main checkout belongs to the owner. Boundary snapshots attribute
it to no run. When the lander predicts that the owner's content conflicts with the landed
result, or finds the checkout in a merge, rebase or cherry-pick, the drain retains the gated
candidate and verdict and records `trunk-path-dirty: ` followed by the sorted paths. The
next pass retries only the landing
([the owner's working tree](https://github.com/mschulkind-oss/tillr/blob/main/tillr-land/README.md#the-owners-working-tree)). When the
owner commits the candidate's whole change, the lander answers `already-on-trunk` and the
item moves to `resolved-elsewhere`.

An item can reach a terminal state (`landed`, `closed` or `resolved-elsewhere`) while one of
its runs is pending. The next pass settles that run with reason `item-already-terminal`,
judges nothing, and records one `drain.item-already-terminal` event.

## Git movement and run convictions

Fetching, pulling, tagging and committing on the trunk are ordinary activity, not evidence
against a run. A trunk that advances keeps accepting gated candidates: the lander reapplies
each change onto the new tip without another gate
([landing principles](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/landing-principles.md)).

Each pass compares the trunk with `trunk_history`, the last observed tip, and keeps the
observed and lost tips under `refs/tillr/trunk-history/`. When the observed tip is not an
ancestor of the trunk:

- **A rebase that kept every commit is absorbed.** The loop matches each replaced commit to
  one on the new trunk by `git patch-id --stable`, records the pairs in `trunk_rewrites`,
  moves `refs/tillr/green`, and appends `drain.trunk-rewrite-absorbed`.
- **Any other rewrite holds landings** as `trunk-rewritten: <lost-tip> -> <current-tip>`.
  Runs continue and candidates are gated. Restoring the lost tip as an ancestor clears the
  hold.
- **`tillr drain accept-rewrite`** accepts a rewrite it can prove kept the content: the same
  tree and commit count, or a patch match (`git cherry`) for every lost commit. Run it with
  the loop stopped.

A landed commit left unmapped holds its dependents as `blocker-landing-not-on-trunk`.

The run bracket convicts on attribution, never on correlation in time:

| Movement | Outcome |
| :--- | :--- |
| Remote-tracking refs, tags, `refs/stash`, notes, and branches outside tillr's namespaces | Ignored |
| Trunk commits, rewritten trunk history, and main-checkout content | Ignored |
| The run's own branch, sibling lane branches, and the loop's own refs under `refs/tillr/` | Ignored |
| The loop's scratch worktrees under the lane root | Ignored |
| Any other ref in tillr's namespaces, any other worktree, or the main checkout's branch changing | Observed: named on the `boundary.check` event; the check stays clean |
| Changed status of a path in a watched, idle sibling lane worktree | Convicted |
| The run's own lane worktree going away | Convicted |
| A named store file changing in an adapter-only bracket | Convicted, in the standalone boundary check |

A conviction holds the item as `host-boundary-violation`; `tillr drain acquit` records an
operator's acquittal and keeps the conviction record. An observed move of a
`refs/heads/patch/<fork>` branch is stored in the event's `patch_moves` and holds
publication, not the run, until the owner acknowledges it:

```sh
tillr drain acknowledge-patch-move --store .tillr/state.db --observation 123 --fork pi --by owner
```

## After-merge heavy runs

With `landing_strategy` set to `after-merge`, each landing and each loop start trigger one
**heavy runner**, which proves the trunk tip in a detached checkout
([the gate reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/gate.md#after-merge-proof-and-the-green-pointer)).
This section states what the drain owns.

**The proof.** Only the proof of the loop that holds the repository's loop lock
(`tillr-loop.lock` in the Git common directory) moves the pointer or writes under
`tillr-gate/`. `--heavy-gate` names the declaration judge (default `tillr-gate`), and
`--rolling` names an optional rolling program judged as well; it is empty by default
(`DefaultRollingProgram` in [`drain/trunk.go`](./drain/trunk.go)). A proof that judged no
checks is never green. Each passing check is saved with the exact tip before the next starts
(`heavy-progress.json`), so a replacement loop's proof reuses them. A run with no failing or
unjudged check moves `refs/tillr/green` with a guarded update: the value read before judging
must be current and an ancestor of the tip.

**A red check.** The runner replays the check alone over the recorded landings since the
pointer ([a red proof](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/gate.md#a-red-proof)). A landing is named only
when the check passes on its first parent and fails on the landing. Then:

- With `auto_revert` on, the runner builds the inverse of the landing in
  `<lane-root>/.revert/worktree`, gates it as `revert-main-<sha12>`, replays the check, and
  lands it when all pass. The reverted item returns to `in-queue`, its criterion states are
  withdrawn (`item.verdicts-withdrawn`), and its worker gets the check's output. A revert, a
  fixer's landing and a landed item's dependency are never reverted.
- Otherwise the first red landing receives a trunk fixer naming every check attributed to
  it; a red that names no landing files one fixer against the tip. A later red tip for the
  same check attaches to the open fixer. Each failing check's output is snapshotted under
  `<lane-root>/.fixers/` (`drain.fixer-logs`) and copied into the run's `fixer.logs/`.

Before each plan, an open fixer is resolved (`resolved-elsewhere`, with
`drain.fixer-proved`) when a proof recorded after its last red observation, at or past its
red commit, passed every check it names. The fixer's own gate is in
[the gate reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/gate.md#the-trunk-fixers-gate).

**Pushing.** With `[push] what = "green"` in `.tillr.toml`, the loop pushes the pointer's
commit when it advances and at loop start; `"tip"` pushes the trunk tip after each landing;
`"none"` pushes nothing. The drain never forces a push. Each attempt is a
`drain.post-landing-action` event whose `kind` is `push-ok`, `push-remote-declined` (the
server answered `[remote rejected]`), `push-patch-move-held` (no ref status while a patch
move awaits acknowledgment), `push-failed`, `remote-sync-proving`,
`remote-sync-worktree-dirty`, `remote-rewritten` or `remote-sync-conflict`
([the repair](#remote-sync-conflict-repair)). When the remote has moved, the drain replays
the unpublished commits onto the fetched tip under the landing lock; each kind's meaning and
mover are in the
[refused-push reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-refused-push.md#71-waiting-rows-and-named-movers).
`tillr drain push` runs one publication attempt with the same settings and lock; its exit
status reflects the recorded event.

## Rolling trunk audits on the before-merge path

A rolling run happens only with `landing_strategy` `before-merge` and a rolling program
named by `--rolling` or the definition's `rolling` hook (`RollingConfigured` in
[`drain/steps.go`](./drain/steps.go)). A named program is due every eight landings, every
twenty minutes, and once before the queue drains. It judges a detached worktree of the tip;
a green run advances the watermark, and `rolling_runs` binds the outcome to its base and
tip. A red tree check files one deduplicated trunk fixer.

While a trunk check is red and a fixer is open, every other item may still start. Only
landing waits, by path intersection:

- A candidate refused only by checks that also fail on its base and on the tip, whose
  changed paths intersect none of those checks' declared `reads`, is promoted to
  `pass-inherited` and lands.
- When its paths do intersect, the loop records `trunk-already-red` and blocks the item on
  `item:<fixer id>`; the planner reports `trunk-broken`.
- When any failing check passes on the tip, no fixer is filed: the loop rebases the worker's
  commits onto the tip and returns the ordinary refusal.

The replay program (`--replay`) judges the failing checks on the base and the tip; an empty
value disables the acquittal.

## Faults that hold one item

The loop never stops on one item's condition. Each of these holds only its item and
survives a restart; none records a failing criterion verdict:

| Hold | Cause | Mover |
| :--- | :--- | :--- |
| `gate-stall-rerun` | The gate's first `stalled` answer (exit 6). | The loop: it gates the same head again with fewer gates in flight. |
| `gate-stalled` | A second stall on the same head tree. | `clear-fault`, or a retry when the fix is on the trunk. |
| `gate-no-verdict` | The gate ran and judged nothing. | The loop: it gates the same commit again. |
| `gate-harness-error` | The gate could not start, or refused its own arguments. | The operator fixes the gate, then `clear-fault` or a retry. |
| `host-boundary-violation` | A recorded boundary conviction. | `tillr drain acquit`. |
| `candidate-record-mismatch` | The lane's HEAD differs from the recorded candidate. | `clear-fault` or a retry. |
| `definition-sync-failed` | The item's document cannot be synced. | Correcting the block on the trunk; the hold then ends by itself. |
| `stage-failed` | The run could not be staged. | Fix the staging fault, then `clear-fault`. |
| `decision-collision` | The decision id is recorded with other columns. | `clear-fault`. |
| `settle-failed` | An item-local error while settling or judging. | `clear-fault`. |
| `pack-sync-failed` | The mirror could not be regenerated. | `clear-fault`. |
| `preserve-failed` | Uncommitted work could not be saved. | `clear-fault` ([preservation](#earlier-uncommitted-work)). |

The candidate is always judged by the installed `tillr-gate`, never by the candidate's own
build. `tillr view waiting` shows each fault with one complete command, such as
`tillr drain clear-fault --store <store> --item <id> --fault <n>`, where the fault number is
the event sequence. A stale command is refused when newer evidence holds the item. Clearing
a gate fault rejudges the retained candidate; it dispatches no run. When the candidate
itself is what fails, the release is a retry naming the held run,
`tillr drain retry --item <id> --run <run> --by <who> --why <reason>`, with
`--acknowledge gate-failed` when a `gate.verdict` was recorded. The loop then pins the
candidate, resets the lane, records `drain.candidate-superseded`, and dispatches the item
fresh ([`drain/retry_supersede.go`](./drain/retry_supersede.go)).

`tillr drain examine` settles and judges one named run with the loop's own step programs.
`tillr drain set-aside` pins an item's committed candidate, releases its lane, and holds the
item for rework.

## Faults that stop the whole loop

A fault stop is for a failure that cannot be attributed to one item: the store cannot be
opened, read or written; the trunk cannot be read; settings are invalid or shared machinery
cannot start (no usable backend policy or a document sync program that cannot run); or a
step program exits 2 at run time or refuses its argument check. When the loop ends on an
error it appends `drain.loop-ended` to the store before writing its result file.

## Held candidate capacity

A held candidate is pinned independently of its lane. A gate stall, an item fault, a
dirty-trunk wait, a trunk-broken wait and a rewritten-trunk wait release the lane once the
drain reads back the exact candidate commit at `refs/tillr/runs/<run>`. The drain saves the
fence, preserves uncommitted work, and records `drain.candidate-release-started` before the
reset and `drain.candidate-lane` after it. Clearing the hold claims any free lane
(`drain.candidate-assigned`, then `drain.candidate-staged`); it starts no worker, and a
retained passing verdict retries landing, not gating.

These classes keep their lanes: a conflict repair that owns the pending transition, a hold
before a candidate is recorded, a live or unsettled workflow member, a host-boundary
violation, a candidate-record mismatch, and an unverified pin or a failed preservation or
reset. `tillr drain why` and both waiting views say `lane released` or `lane kept`, with the
reason.

### Changing the ordinary lane count with work in flight

`tillr drain scale --root . --lanes 3` saves the ordinary lane target; the running loop
applies it on its next pass without signaling workers. An explicit `--lanes` on a fresh
start replaces the saved target. Lowering it stops fresh dispatch to lanes outside the
rotation; those lanes keep serving the work they hold. Express lanes are unchanged.

## Lanes that staging refuses

Some staging refusals are about the lane, not the item:

| Reason | What the lane holds | Remedy |
| :--- | :--- | :--- |
| `lane-holds-unlanded-change` | A commit that the base, the trunk, a run pin and every recorded landing lack. | Land the change, or [reset the lane](#resetting-a-lane). |
| `lane-branch-held` | Its branch, checked out by another worktree. | Remove that worktree. |
| `lane-holds-uncommitted-work` | Uncommitted or untracked files when a pinned candidate is staged onto it. | Commit or remove the files, or reset the lane. |
| `lane-run-children-live` | Processes of an ended run whose working directory is inside the lane. | End the processes the waiting row names. |

The loop takes such a lane out of rotation, writes one `drain.lane-refused` event, and
offers the item the next free lane in the same pass. It re-checks after the lane's HEAD,
status or branch changes, and otherwise every ten minutes; a live-process hold is checked
every pass. A cleared lane writes `drain.lane-restored`. The item whose run left processes
holds as `run-processes-live`. A refused staging cancels the claim; a claim left without a
started run is recognized at the next pass, which dispatches the item again.

### What the attempt number counts

The attempt number names an item's next dispatch: `run-<item>-a<N>`. It is one more than
the highest attempt among the item's recorded node runs. A dispatch that ends before a
worker starts does not raise it, because cancelling a claim deletes its node rows. It is
therefore not a count of failed builds. For work done, read `tillr view lanes` and the
item's `work` entry.

### Resetting a lane

Reset a lane only after deciding its change does not land from that lane, and only when the
change is kept on the trunk or at a pin under `refs/tillr/runs/<run>`:

1. Confirm the lane is idle: `tillr view lanes` shows no run on it.
2. Confirm the commit is kept: `git merge-base --is-ancestor <sha> main`, or
   `git for-each-ref refs/tillr/runs` lists a ref at it.
3. Point the worktree and branch at the trunk:
   `git -C <lane root>/<lane>/worktree checkout --force -B <lane branch> main`. The lane
   branch has the form `tillr/<scope>/<lane>`.

The next pass re-checks the lane because its HEAD changed.

## Conflict repair that cannot be staged

A conflict-repair staging failure holds only its item as `conflict-repair-staging-failed`.
The candidate is pinned before the lane is reset, and a `drain.repair-held` event keeps the
candidate, the conflict reports, the staging error and the next action. Correcting the
reported cause lets the next pass stage the repair against the current trunk. Tests:
[`drain/conflict_hold_test.go`](./drain/conflict_hold_test.go).

## Earlier uncommitted work

Before resetting a settled lane, the loop saves its uncommitted edits at
`refs/tillr/preserved/<run-id>`: tracked edits, nonignored untracked files, and, for an
interrupted run, commits made past its base. It builds that commit from temporary indexes,
never the lane's own. When staged bytes differ from both HEAD and the working copy, a second
parent keeps the staged version. The `drain.work-preserved` event names the item, run and
ref. A clean lane creates neither ref nor event.

The saved commit is recovery evidence only: it is never gated or landed, and the next
attempt's brief names it. If saving fails, the loop keeps the lane and holds the item as
`preserve-failed`. Preservation and every lane reset first remove a stale `index.lock`, one
that no process holds open in a lane no process has as its working directory, and record
`drain.stale-index-lock-removed`. When the item is already terminal, the loop records the
lane's status in `drain.lane-discarded`, pins the head, and resets the lane. Saved-edit refs
are removed when the item is landed, resolved elsewhere, closed or removed from the store
(`drain.work-pruned`).

## Ref lifetimes and backlog cleanup

A **run pin** is the commit pointer for a worker attempt; a **landing marker** is the
lander's crash-recovery pointer
([the landing contract](https://github.com/mschulkind-oss/tillr/blob/main/tillr-land/README.md#known-cases-the-marker-does-not-settle)).

| Namespace under `refs/tillr/` | Writer and lifetime |
| :--- | :--- |
| `runs/<run>` | The loop or lander. Removed once the trunk contains the commit and the pin names that run's landed head, or the item is finished or removed. For retryable items the newest eight examined pins are kept (`RetryPinLimit`). |
| `runs/<run>-set-aside-base`, `runs/<run>-superseded-head` | The loop, for `set-aside` and a superseding retry. |
| `landed/<run>` | The lander. Removed after that run's `landed` row and a matching published commit. |
| `preserved/<run>` | The loop. Removed when the item finishes or is removed. |
| `held-forks/<digest>` | The loop, for a held fork candidate. |
| `sync-remote/<item>` | The repair filer; kept until the item is finished or removed. |
| `held/<name>`, `recovery/<name>` | An operator, by hand. No tillr program writes or deletes them. |
| `green` | The proof. Cleanup never deletes it. |
| `trunk-history/observed`, `trunk-history/lost`, `trunk-history/accepted/<lost-tip>` | The loop. Cleanup deletes none of them. |

The loop cleans refs after each settlement and on later passes. For accumulated refs:

```sh
tillr-drain sweep-refs --root . --store .tillr/state.db --dry-run
tillr-drain sweep-refs --root . --store .tillr/state.db
```

The sweep reports each ref as removed or kept, with its reason. Unknown ownership, symbolic
refs, unrecorded landings and mismatched markers are kept. It takes the common-directory
`land.lock` before sampling, and deletion is guarded by each ref's observed commit. Run it
against the repository's own store.

### Remote-sync conflict repair

When a replay onto a moved remote stops at a conflict, the drain records
`remote-sync-conflict` and files or reuses one express repair item `sync-remote-<short>`
per fetched remote tip. Its lane starts from the pinned replay prefix, and a gated repair
lands by linear replay under `land.lock`. If the operator reconciles `main` by hand, the
next sweep closes the repair and records `remote-sync-cleared`. The checks on the repair
and its recovery are in the
[refused-push reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-refused-push.md#6-when-rebasing-onto-the-remote-conflicts).

## Staging without Git landing

A store's [definition](https://github.com/mschulkind-oss/tillr/blob/main/tillr/definition/README.md#keys-types-defaults-and-readers) sets
`completes`. When it is not `landing`, staging creates an ordinary directory named `work`
and writes the brief and session id beside it, with no worktree, base, fence or Git
metadata. The definition's `brief_template` is required. Tests:
[`drain/plain_stage_test.go`](./drain/plain_stage_test.go).

## Completion without landing

For a definition with `completes: "hook"`, an ended run goes directly to the
`complete-item` hook: no candidate, boundary bracket, gate, lander or Git command. The hook
records criterion verdicts and answers one JSON object naming a finished `state`, a `reason`
and an optional `resolves_via`. The drain moves the item with `--require-verdicts`. Missing
verdicts hold the item as `criteria-unjudged`; other invalid answers hold it as
`complete-item-fault`.

## Where step programs come from

For gate, fixer gate, land and verdict, a configured command naming the standard adapter at
`tools/tillr-loop/tillr-loop` under `--root` runs the copy embedded in the drain binary
([`drain/tillr-loop.py`](./drain/tillr-loop.py)); the working-tree file is not read. Other
programs run as configured, and a definition's hook program always runs the declared
executable. Changing the standard step programs means rebuilding the tillr programs and
restarting the loop
([the adapter README](https://github.com/mschulkind-oss/tillr/blob/main/tillr-loop/README.md#step-programs-belong-to-the-running-build)).

## Step-program argument contract

A **step program** is the configured gate, fixer gate, land or verdict command. A store
definition may instead name one hook program whose `describe` record lists its steps
([hook contract](https://github.com/mschulkind-oss/tillr/blob/main/tillr/definition/README.md#hook-program-contract)).

A program that supports startup validation advertises the literal
`tillr-step-argv-check-v1` in its `--help` and accepts `--check-argv`: it parses every
argument with the step's own parser and returns 0, or 2 with a diagnostic naming the refused
flag. It reads no candidate artifact, opens no store and moves no ref. Both
`tillr drain loop --check` and startup pass every candidate flag through this mode before
any dispatch. A command that does not advertise support is named once as `unchecked` and the
loop starts. A supported command that refuses the check stops startup with exit 2.

## Fork handling

The drain carries handling for patch-series forks of an outside tool, slated for removal by
[roadmap entry 4](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/roadmap.md#2-a-core-that-carries-only-its-own-code). The
fork it serves in this repository is `mschulkind-oss/tillr-pi`, a fork of upstream pi that
carries only what the adapter requires. What the code does:

- **Target.** A request names one fork from `bin/pi-forks.list`. The `plan` step of
  `planned-build@v1`, or the read-only `classify` seat, records the delivery as repository,
  fork or both. A mixed item delivers the fork first.
- **Staging.** A fork piece's checkout is `.forks/lanes/<lane>/<fork>`, started at the
  delivered patch tip. The worker commits there and publishes nothing; the lander alone
  moves `refs/heads/patch/<fork>`.
- **Gate.** [`fork-gate.py`](fork-gate.py) runs the `fork-build` check on a clean copy of
  the candidate head, then `fork-quality` from its `FORK_QUALITY` table.
- **Holds.** A held fork candidate is pinned under `refs/tillr/held-forks/`
  ([`remotesync/held_fork.go`](./remotesync/held_fork.go)).

## The deny-list and the session

Per-item path fences do not decide which files a candidate may change. Before the gate, the
drain reads [`config/tillr/deny-list.json`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/deny-list.json) from the
committed trunk and checks the worker's base-to-head change against it. A refusal returns
the matched entry, the path and its reason to the worker. For a trunk candidate, the drain
then regenerates the pack mirror for the shipped modules the candidate changed and commits
the result; a failed regeneration holds only that item (`pack-sync-failed`).

**A pool lane is not a session.** Staging mints a fresh session id for an ordinary dispatch
and writes it beside the brief as `session-id`. `resume` names the id it continues.

## Hand-driven comment delivery

An item created by `tillr item comments take` dispatches on the design seat to its
document's desk. New build dispatches for that document wait with `design-under-comment`
until the batch settles. `comments.max_running` bounds occupied desks; an item beyond it
waits with `waits on a desk`.

The session is chosen by the reference's
[dispatch table](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-designer-sessions.md#choosing-at-dispatch). Only
an expired session over 40,000 tokens is compacted, and only when its adapter ships an
executable compact step; otherwise it resumes with `resumed-expired`. A reply-only
`RESULT: done` records `answered`, skips the gate and landing, and closes the item. The
[keep-warm rule](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-designer-sessions.md#the-keep-warm-rule) ends a
designer's session when its document is deleted or its stage is done, and
[settlement](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-designer-sessions.md#settlement-and-reply-delivery)
describes how replies reach the inbox.

## Ask the planner why

```sh
tillr drain why --store /path/to/.tillr/state.db --policy /path/to/policy.json --lanes lane-1,lane-2
tillr drain why <item-id> --store /path/to/.tillr/state.db --policy /path/to/policy.json
```

`why` runs the planner `plan` runs, with no pass input file, and writes nothing. It prints
place, item id, position, reason and mover for every item not stored as `landed`, then a
summary that partitions the rows into running items, items the next pass could advance,
and holds counted by reason. Naming an item prints its full explanation, and `--json` gives
the rows and summary as one object. `--lanes` takes a comma-separated list of lane ids or a
count. With no policy, readiness is held as `no-policy`.

### Adding a hold reason

A hold reason is an exported `Reason...` string constant in the `drain` package. How it is
shown is one table, `Rules` in [`holdrules/holdrules.go`](holdrules/holdrules.go), a leaf
package the read-only `tillr view` face imports. To add one, declare the constant with a
string literal value, add its row (class, where, mover) to `holdrules.Rules`, handle its
prefix in `holdrules.For` when it carries detail after a colon, and run
`go test -run TestEveryReasonConstantHasAHoldRule ./drain` here.

## What it refuses

- **A loop with no gate, land or verdict program** — it judges nothing itself.
- **A root it cannot write a record under** — the record is the answer.
- **A re-dispatch of a held run nobody authorized** — a retry is an operator's act.
- **A resume with no session to continue, or of a judged run** — continuing judged work is
  a retry.
- **A seat it cannot route** — the refusal names the requested seat, the mapped seats and
  the policy file.

## Known gaps

- **Fork handling is repository-specific code in the core.** It reads `bin/pi-forks.list`
  and calls `bin/pi-forks`, and this tree ships neither (`fd pi-forks bin` returns
  nothing). [Roadmap entry 4](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/roadmap.md#2-a-core-that-carries-only-its-own-code)
  removes it.
- **An escape outside the lanes is only observed.** A rogue ref or stray worktree does not
  hold its item; the `boundary.check` event names it for an operator.
- **No drain face resets a lane.** [Resetting a lane](#resetting-a-lane) is three Git
  commands by hand.
- **An in-flight heavy check is not carried across a loop stop.** Passed checks are reused;
  the check in flight starts over.
- **A design run's gate refusal that the trunk caused is not recognized.** The replay has
  no verdict for a change check, so such a refusal returns to the design seat each time.
- **`reserve` is a threshold on the last reading.** A run can overshoot it between
  probes.
- **A report changed before the drain first reads an ended run** can change that run's
  first classification; a probe or a later run corrects availability.

## Test

```sh
python3 tools/go-test-shards.py tools/tillr-drain -k 6
```

That runs the module's Go suites in six balanced processes with Go's own test timeout off.
The suites build the binary and drive each face against a scratch store and scratch lane
roots.

The test binary declares the sibling sources as inputs that Go's test-result cache can see:
the tests open the sibling Go sources through an ignored, module-local symlink to the
parent directory. The one sibling list is
[siblings.txt](internal/testinputs/siblings.txt). The
[cache regression check](cache_test.py) warms the result in an isolated clone, confirms an
unchanged tree reports `(cached)`, then changes each sibling and requires a noncached
failure. The gate check `go-shard-helpers-test` runs
`python3 tools/tillr-drain/cache_test.py --run TestTheComposedPipelineRunsEndToEndOnAFixture`,
the case that builds every sibling; without `--run` the script repeats the whole suite for
each sibling.
