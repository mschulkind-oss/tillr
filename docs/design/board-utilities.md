---
title: "Board utilities: seven utilities over one shared substrate"
date: 2026-11-06
status: accepted
stage: DESIGN
next: "Write the implementation plan, then run gate-zero's registry inventory to start the strangler"
tags: [board, tillr, decomposition, utilities, gate, testability, landing-velocity, naming, boundary]
summary: "Seven utilities over one shared substrate — store, item, land, drain, run, gate, view — each its own binary, versioned together as one release set, and each required to run alone against a fixture store or a record, because the module and state boundaries buy per-module gate selection, contained blast radius, and a testable surface, and the separate binaries let a project take only the utilities it wants."
---

# Board utilities: seven utilities over one shared substrate

**Status:** Accepted, 2026-11-06. Companion to `docs/design/board-sanity-recovery.md` and `docs/design/board-names.md`. **Read the "today" below as the tree the contract was written against (2026-11-06):** the board CLI it decomposes was deleted on 2026-11-11 (`3f02e3b916`, `afdc9a9571`), so every present-tense sentence about `tools/board-cli` is history. The contract itself is live: the seven `tools/tillr-*` binaries implement it, and the document retirement records which of its companion documents were absorbed or superseded.

**In short**

> The board's 156,425 lines are not one job done badly; they are roughly seven jobs sharing one process, one command registry, one Go module, and one test suite, so every job pays every other job's cost. Draw the seams where state changes hands, give each piece of state exactly one writer, put a documented record on every boundary, require each utility to do useful work alone against a fixture store or a record, and make each utility its own module with its own gate check — and the same cut that makes a utility provable in isolation also makes a typical board change cost one small suite instead of 17 minutes, confines its blast radius to one program face, and lets new capability arrive as a new utility instead of a new flag on a 6,774-line `drain.go`.

**Why it matters:** At the time of writing, `go-tool` was `cd tools/board-cli && go test ./...` over one module, so *any* change under `tools/board-cli/` selected all 1,141 tests — that single fact produced the 17-minute gate, the un-testable-in-isolation suite, and the unbounded blast radius at once. Decomposition is the one intervention that addresses the shared cause, provided the seams are state seams, each utility can be used without invoking the others, and the gate selection follows the modules.

**The shape:** Seven utilities over one shared substrate, each a module the gate can select independently and each shipped as its own binary. `store` owns the schema and event log; `item` owns items, criteria, edges, and decision and mail records, and carries a reference to the design document an item's open questions are read from; `land` owns the candidate lifecycle and the single landing transaction; `drain` owns dispatch; `run` owns lanes and runs; `gate` owns gate plans and verdicts; `view` owns nothing and renders everything read-only. Config is deliberately not a utility ([§3.4](#34-the-four-question-test)). The system is `tillr`: the canonical invocation is `tillr store`, `tillr item`, and so on, dispatched by a thin wrapper to the individual binaries ([§6](#6-the-naming-model-one-prefix-independent-names-or-a-hybrid), [§11](#11-seven-binaries-versioned-together)).

**The boundary test:** A boundary is real only if a person or agent can run that one utility's own binary against a fixture store, a record on stdin, or both — without any other utility's binary present — and get a useful answer with a documented exit code. [§5](#5-independent-use-the-boundary-made-concrete) specifies that use for every utility. A utility that cannot pass it has a code seam, not a state seam, and must be merged or redrawn under the four-question test.

**Cost:** A versioned record format maintained forever, drift between modules, narrowed cross-domain atomicity, fixture and dry-run faces maintained as real product surface, seven artifacts to build and publish with the version skew that separation makes possible, and an honest limit — a split that only moves code leaves the tests exactly as heavy, and the worst case still pays the heavy module's own suite. What shrinks the suite is the ownership the split forces: duplicated coverage collapses into the owning module's one test, and the survivors stop standing up the whole program. [§3](#3-why-not-more-the-split-curve) and [§13](#13-what-this-costs-and-what-it-forecloses) price each.

**Start at:** [§3](#3-why-not-more-the-split-curve) for the split curve and the four-question test that produces seven; [§5](#5-independent-use-the-boundary-made-concrete) for the proof that each boundary is independently usable; [§10](#10-landing-velocity-the-per-module-gate) for the velocity arithmetic; [§12](#12-the-migration-path) for the order.

**Reads with:** `docs/design/board-sanity-recovery.md` (the diagnosis this decomposes), `docs/design/board-names.md` (the 50-candidate naming companion; the name is now ruled as `tillr`, [§6.4](#64-the-name-is-tillr)), `docs/design/board-unified-architecture.md` (principles P2 and P6), `docs/design/landing-velocity.md` and `docs/design/landing-velocity-plan.md` (the tier table and S2–S9 gate split this design must plug into, not displace).

This document designs the decomposition of `tools/board-cli` — 156,425 lines of
Go in one module — into seven single-purpose utilities over one shared SQLite
substrate. It is a design, not an implementation plan: it names the boundaries,
the contracts that hold them, and the decisions that have been ruled. It does not
list per-file edits.

Revisions from the previous draft, all from owner feedback:

1. **Seven utilities, not nine.** The landing queue merges into the landing
   pass, and decisions and mail fold into items; open questions are not board
   state at all. [§3](#3-why-not-more-the-split-curve) gives the test that
   produces seven and explains why more splits would not help.
2. **Long names, no `b` prefix.** The utilities are invoked by agents, not
   typed by humans, so clarity beats brevity. The naming model is the simple
   hybrid of [§6](#6-the-naming-model-one-prefix-independent-names-or-a-hybrid):
   `tillr <noun>` is canonical, and the bare noun is correct in context.
3. **Seven separate binaries, versioned together.** Each utility is relied on
   individually and can be taken into another project without the other six;
   a thin `tillr` wrapper is available as a single control surface
   ([§11](#11-seven-binaries-versioned-together)).
4. **Independent use is specified, not asserted.** [§5](#5-independent-use-the-boundary-made-concrete)
   gives, for each utility, a standalone invocation, the record it consumes
   and emits, the tables it touches, its exit behavior, and a worked scenario
   that needs no other utility face.
5. **The system name is `tillr`.** The old public project is wiped and
   re-founded fresh: the name is taken, the baggage is not
   ([§6.4](#64-the-name-is-tillr)).
6. **The test win is two wins, and final gating stays serial.** Owner ruling:
   untangling the code lets the suite be pared down and the survivors made
   faster, not merely redistributed ([§3.2](#32-what-more-splits-do-not-buy),
   [§9](#9-testability-and-testing-speed)). And gating non-overlapping candidates in
   parallel is answered rather than left open: a verdict binds to the tree it
   examined, so parallel final gates produce at most one landable verdict, and
   parallelism is spent on advisory preflight
   ([§10.1](#101-parallel-gating-of-non-overlapping-candidates)).
7. **A second consumer is assessed, not accommodated.** The owner is building an
   auto-QA loop in another workspace and asked whether these chunks could be
   reused for it. [§13.1](#131-what-a-second-consumer-would-reuse-and-what-it-would-not)
   states what transfers — the substrate mechanisms, the record contract, and
   `gate`'s no-store face — and what does not, and why a work producer is a client
   of `item` rather than an eighth utility. No seam moved; `D-UT15` records it.
8. **The nine open questions are ruled and integrated (2026-11-06).** One
   shared database; one landing implementation reached by whatever route is
   fastest; gate selection interleaved with the landing-velocity work at
   migration step 3; seven separate binaries versioned together, reversing
   the earlier one-binary position; the name `tillr`; questions removed from
   board state in favor of an item's reference to its design document; the
   per-module budgets held; the naming hybrid simplified; and the record and
   table shapes designed for the boundary rather than inherited from the
   current store. Each ruling is folded into the section it governs and
   compacted into the Decision Ledger under its original question ID
   ([§14](#14-decision-ledger)).

Terms of art, defined once:

- **Utility** — a single-purpose program: one writer of one slice of state,
  shipped as its own binary, invoked as a command, exchanging versioned records.
- **Module** — a Go module with its own test suite and its own gate step. The
  module boundary is what the gate selects on.
- **Substrate** — everything the utilities share: the SQLite store, the schema
  and record package, the event log, and the config loader.
- **Record** — the versioned, serialized form in which one utility's output
  becomes another's input. Records, not function calls, cross boundaries.
- **Fixture store** — a disposable, explicitly named SQLite store created from
  seed records for a test or a standalone run. It is never the live store by
  default.
- **Independent use** — running one utility's own binary to a useful result
  with no other utility's binary present. The shared substrate libraries and a
  fixture store may be present; hidden calls into another utility may not.
- **Conformance suite** — the always-selected test suite that checks every
  utility against the boundary contract (record format, schema, event kinds,
  exit codes).
- **Version set** — one release of all seven binaries together, sharing one
  version. The binaries are separate artifacts; the set is what is released.
- **Wrapper** — the thin `tillr` command that dispatches `tillr <noun> ...`
  to the `tillr-<noun>` binary, in the manner of `git` dispatching to
  `git-rebase`. It is a convenience control surface, not the distribution.
- **Strangler** — a migration that moves one responsibility at a time behind
  the old interface, so the system works at every intermediate step.

## 1. The goals, as a set

The decomposition serves five goals. Each has a recognition test — an
observation that would tell us the goal was met.

| Goal | Statement | Recognition test |
|---|---|---|
| G1 Testability | Each responsibility can be tested with its neighbors, the clock, and the process table replaced by inputs. | A landing test runs without spawning a real drain or waiting real seconds. |
| G2 Landing velocity | A typical change gates in tens of seconds, not tens of minutes. The owner's "big one." | A one-module change selects one module's suite plus the conformance suite ([§10](#10-landing-velocity-the-per-module-gate)). |
| G3 Testing speed | The fast suite for any one module fits in a developer's edit loop. | Per-module budgets in [§9](#9-testability-and-testing-speed) hold on a loaded machine. |
| G4 Blast radius | A change breaks, at most, the state its module owns. This is the price G2 charges: velocity comes from *not* running the other suites, which is only safe if the boundaries are real. | A red suite names the module whose state is wrong. |
| G5 Composability | Agents combine utilities through records, the way shell tools combine through pipes. | A new report is a new reader of existing records, not a new command in an existing writer; and each utility also does useful work alone ([§5](#5-independent-use-the-boundary-made-concrete)). |

G2 and G4 are one decision seen from two sides. Per-module gate selection is
exactly the claim "a change here cannot break state over there." If that claim
is false, selection hides breakage ([§13](#13-what-this-costs-and-what-it-forecloses)). The whole design is the work
of making the claim true. The independent-use requirement is the operational
form of the same claim: if a utility cannot run alone, the claim that its
suite can be selected alone is already suspect.

## 2. The seam analysis

Before proposing new machinery, the survey: what responsibilities exist, what
state each writes, and what already lives outside the monolith.

| # | Responsibility | Writes | Read by |
|---|---|---|---|
| R1 | Store (schema, migrations, event log) | The database itself | Everything |
| R2 | Items (the work records) | Item state | Drain, view, land |
| R3 | Landing queue | Candidate state and eligibility | Landing pass, view |
| R4 | Landing pass | Landing attempts, the trunk ref | Queue, view |
| R5 | Drain (dispatch and supervision loop) | Dispatch decisions, run launches | View |
| R6 | Gate (plan and run checks) | Gate plans and verdicts | Landing pass |
| R7 | Lanes and runs | Lane and run state | Drain, view |
| R8 | Reporting | Nothing | Humans and agents |
| R9 | Decisions and mail | Decision records, deliveries | Items, agents, the owner |
| R10 | Config | Nothing at runtime (loaded, not written) | Everything |
| R11 | Serve (long-running read/serving faces) | Nothing of its own | Clients of R8 |

Open questions do not appear in this table, deliberately. They are not a
board responsibility: they live in the design documents, in Vantage's world,
and an item points at its design document and reads that document's open
questions from it ([§4](#4-the-proposed-suite-seven-utilities),
[§5.3](#53-item-alone)). The board stores the reference and nothing else, so
there is one copy of every question and the two cannot drift.

Three seams announce themselves:

- **R8 writes nothing.** A reader with no state is the cheapest possible first
  extraction: it cannot corrupt anything, and it proves the record contract
  from the safe side.
- **R6 is already half-external.** `tools/fork-check.py` does real gate work
  outside the monolith today. The gate boundary exists in practice; the design
  formalizes it rather than inventing it.
- **R4 exists twice.** `tools/mq` (14,521 lines) and the monolith's
  `landing_queue.go` (5,745 lines) both implement parts of the landing
  lifecycle. The decomposition must end with one landing implementation and
  one writer for candidate state, not enshrine both. This is the
  existing-machinery check the design is most obliged to respect: the new
  `land` utility *absorbs* `tools/mq`; it is not a third lander beside two
  existing ones. Which route reaches that end state — port `tools/mq`, wrap
  then replace it, or reimplement against its tests — is an implementation
  decision, not a design one: the end state is fixed, and the one constraint
  on the route is to get there quickly and not let two implementations
  coexist longer than the migration step that removes the second one.

Equally, candidate seams fail on inspection, and [§3](#3-why-not-more-the-split-curve) makes the
failure precise: R3 and R4 are one lifecycle with two writers (the pass must
write "landed / isolated" back into candidate state the queue owns), R9's
decision and mail records are item-adjacent rather than a lifecycle of their
own, and questions are not board state at all, so there is no question seam
to draw.

## 3. Why not more: the split curve

The owner's question: *what's the downside of more? The more splits we have,
the faster our tests, and better isolated our changes are.* That intuition is
right up to a point, and then it inverts. This section states the curve
honestly, because the count in [§4](#4-the-proposed-suite-seven-utilities) is an output of it, not a target
anyone picked.

### 3.1 What more splits buy

A smaller suite selected for a *typical* change. That is real, and it is the
velocity win of [§10](#10-landing-velocity-the-per-module-gate): today every change to the monolith selects
`go test ./...` over the whole module; after decomposition, a change to one
module selects that module's fast suite (budgeted 20–40s) plus the
conformance suite (budgeted in seconds). Isolation improves in the same step:
a module that owns its state can be broken only through its records.

### 3.2 What more splits do not buy

- **A split by itself only moves tests; the untangling that makes a real
  split possible is what shrinks them.** Dragging a suite across a boundary
  intact leaves it exactly as heavy: `landing_queue_test.go` is 3,038 lines
  and `drain_test.go` is 1,947 lines today, and they arrive that heavy inside
  `land` and `drain`. What does pare the suite down is the ownership the
  boundary forces. Today one behavior is covered several times because
  several entry points can reach it and no module can say which one owns it;
  once one writer owns the state, the duplicate cases collapse into the one
  test in the owning module, and the survivors get faster because they no
  longer have to stand up the whole program to reach their subject. So the
  count falls and the per-test cost falls — but the *worst case* still pays
  the heavy module's own suite, however many other modules exist, and that is
  the limit this bullet is about.
- **The substrate is shared, and it is the one wide blast radius.** A change
  to the schema or record package selects every module's suite, correctly.
  No amount of splitting shrinks that, because the shared part is shared by
  definition. Splitting harder mostly moves code *into* the substrate's
  shadow, not out of it.

### 3.3 What more splits cost

- **Every boundary is a versioned record, a conformance test, and a skew
  risk.** Each split converts a function call the compiler checked into a
  contract only the conformance suite checks. That trade is worth making at a
  state seam; at a mere code seam it is pure overhead.
- **Integration surface grows with composition paths.** With N utilities the
  pipeline tier must exercise the ways they actually compose. The integration
  tier is deliberately the least-selected tier ([§9](#9-testability-and-testing-speed)), so every
  additional boundary adds coverage pressure to the tier that runs least
  often.
- **"One writer per state" gets harder to enforce.** Fine splits tempt two
  utilities to write two halves of one lifecycle — exactly the R3/R4 defect
  the merge in [§4](#4-the-proposed-suite-seven-utilities) removes. Each such pair reintroduces cross-module
  atomicity questions the substrate was meant to settle ([§13](#13-what-this-costs-and-what-it-forecloses)).
- **Migration cost is superlinear and competes with the goal order.** Every
  boundary must be strangler-migrated, conformance-tested, and documented.
  The owner's first goal is drain autonomy; a fifteen-way split spends the
  migration budget on boundary plumbing instead of on the drain.
- **A utility too small becomes a pass-through** — a database table with a
  process boundary, whose records carry no decision the caller could not have
  made from the store directly. It pays all the costs above and buys no
  isolation.
- **The reporting surface is the trap.** Reporting reads everything and owns
  nothing, so it can always be split again — and splitting one report into
  three binaries makes three reports, not a smaller surface. Readers
  consolidate; writers separate. That asymmetry is why R8 and R11 share one
  utility.

### 3.4 The four-question test

A proposed boundary earns its keep only if all four answers are yes:

1. **Own state?** Does it own state no one else writes?
2. **Carry information?** Does its record carry a decision the caller needs,
   or is it a pass-through of store contents?
3. **Test alone?** Can it be tested with a fixture store and input records,
   without the other utilities?
4. **Select alone?** Does a change to it select only its own suite (plus
   conformance)?

Question 3 is sharpened by this revision: "test alone" means the utility also
has a *useful standalone invocation*, specified in [§5](#5-independent-use-the-boundary-made-concrete),
not merely a unit-test harness that links its packages. A boundary only a
test can exercise is not yet a boundary a user or agent can use.

Applied to the contested cases:

| Candidate split | Own state? | Carry? | Test alone? | Select alone? | Verdict |
|---|---|---|---|---|---|
| Landing queue apart from landing pass | No — the pass writes landed/isolated back into candidate state | Yes | Barely — each needs the other's fixtures | No — lifecycle changes land on both | **One utility** (`land`) |
| Decisions/questions apart from items | Decisions and mail are item-adjacent records; questions own no board state at all — they live in design documents | Decisions yes; questions are read from the document an item points at, not carried as board records | Decisions yes; questions n/a | Decisions unclear; questions n/a | **Decisions and mail fold into `item`; questions are not board state**, so there is no eighth utility to consider for them |
| Gate planner inside the lander | Yes, but it would grade its own homework: the selector must select itself (item 621) | Yes | Yes | No | **Keep `gate` separate** |
| Config as a utility | No — loaded, never written | No — pass-through | n/a | n/a | **Substrate library**, administered through `store` |
| Reporting split by report | No state at all | No — same records, narrower windows | Yes | Yes, vacuously | **One `view`**; readers consolidate |

### 3.5 The conclusion

More splits help until each boundary is a *state* seam rather than a *code*
seam, and then they cost more than they save. Seven is where the state seams
in [§2](#2-the-seam-analysis) run out. If a future responsibility arrives owning new state and
passing all four questions, the count becomes eight for a reason — the
number follows the seams, never the other way round.

## 4. The proposed suite: seven utilities

Names in this table are utility identities (nouns). Each identity is a
separate binary, `tillr-<noun>`, and the canonical command form is
`tillr <noun>` through the wrapper ([§6](#6-the-naming-model-one-prefix-independent-names-or-a-hybrid),
[§11](#11-seven-binaries-versioned-together)).

| Utility | Owns (sole writer) | Absorbs |
|---|---|---|
| `store` | Schema, migrations, the event log, store administration | R1, the administrative face of R10 |
| `item` | Item state; decision and mail records; each item's `design_doc` reference | R2, R9 |
| `land` | Candidate state and eligibility, landing attempts, the trunk ref | R3 + R4, including `tools/mq` |
| `drain` | Dispatch decisions and the supervision loop | R5 |
| `run` | Lane and run state | R7 |
| `gate` | Gate plans and verdicts; the check registry | R6, formalizing `tools/fork-check.py`'s role |
| `view` | Nothing — all reporting and serving faces | R8, R11 |

Notes on the three judgment calls:

- **`land` is one loop with one writer.** The queue owns candidate state and
  eligibility; the pass owns attempts and the trunk ref; and the pass must
  record "landed / isolated" against the candidate. Split, that is two
  writers on one lifecycle and a cross-boundary transaction on the system's
  most safety-critical path. Merged, it is a state machine inside one
  module. The merge also settles the duplication from [§2](#2-the-seam-analysis):
  `tools/mq` and `landing_queue.go` become one implementation with one
  writer for candidate state, or the decomposition has failed at its most
  important seam. The route to that one implementation is the implementer's
  choice, constrained only by speed: get there quickly, and do not let two
  implementations coexist longer than the migration step that removes the
  second one.
- **`item` carries decisions and mail; it does not carry questions.**
  Decisions attach to items and mail is how their records are delivered, not
  a state of its own, so both fold into `item`. Questions are different in
  kind: open questions live in the design documents, in Vantage's world,
  and the board does not store, mirror, or answer them. An item carries a
  `design_doc` reference, and its open questions are read from that document
  when they are surfaced ([§5.3](#53-item-alone)). There is therefore no
  eighth utility to consider for questions — the question dissolves rather
  than being answered "no," because there is no board state for such a
  utility to own.
- **`gate` stays separate on principle, not size.** The gate planner decides
  which checks a landing must pass, including checks over the planner itself
  (the selector must select itself, item 621). A planner inside the lander
  grades its own homework: a lander bug could choose a plan that never
  examines the lander. Whatever the size arithmetic says, this boundary
  stays.

The 103 existing commands map onto the seven by the state they write, not by
their current grouping in the monolith: a command belongs to the utility that
owns the state it mutates; a command that mutates nothing belongs to `view`.
(The per-command inventory itself is gate-zero of the migration,
[§12](#12-the-migration-path), because it must be generated from the binary's own
registry rather than transcribed by hand.) Some commands will be **deleted
rather than moved**: anything whose only job was to compensate for the
monolith's shape — cross-domain fix-ups, dual bookkeeping between the two
landing implementations — has no home in the suite and should not be given
one.

## 5. Independent use: the boundary made concrete

This section is the proof obligation for the whole decomposition. For each
utility it gives a standalone invocation, the records consumed and emitted,
the store tables touched, and a scenario in which the utility is useful with
no other utility face invoked. If an implementer cannot build the fixture and
the input record from this section, the section has failed; if the extracted
utility cannot run the invocation, the boundary has failed.

Invocations use the canonical spelling (`tillr gate plan ...`), which the
wrapper dispatches to the utility's own binary (`tillr-gate plan ...`); the
direct binary form runs the identical face, and it is the direct form that
the acceptance condition in [§5.11](#511-where-the-burden-of-proof-sits)
exercises, with no other utility's binary present. Two levels of
independence are required, and they are not the same:

- **Operational independence:** one invocation runs one utility's binary and
  no other. The wrapper, when used, only dispatches; it runs no face of its
  own.
- **Module independence:** the utility's fast suite builds and runs against
  the substrate packages, fixture stores, and input records only. This is the
  level that makes per-module gate selection honest ([§10](#10-landing-velocity-the-per-module-gate)).

A third, stronger property — *independent adoptability*, a utility being
useful to a project that does not run this system at all — is the reason the
binaries are separate ([§11](#11-seven-binaries-versioned-together)):
someone must be able to take three of these into their own project and
leave the other four behind. That requires every binary to install and run
alone ([§5.11](#511-where-the-burden-of-proof-sits)); it does not require each
utility's records to become generic, and on current evidence `gate` has the
strongest claim to usefulness wholly outside this system, because it is
already half-external. All seven must be independently *usable* and
independently *installable*; each earns a life beyond the substrate
contract it needs only on evidence ([§6.3](#63-the-model-hybrid-without-ceremony)).

### 5.1 The common contract

Every record carries a common envelope. Field and table names below are the
logical contract, **designed for the boundary, not inherited from the
current store**: compatibility with today's column names is not a design
constraint. The gate-zero registry inventory ([§12](#12-the-migration-path))
is how the migration finds what exists — an inventory of what to move, not
a specification of what to keep — and the migration maps the old shapes
onto this contract.

| Envelope field | Meaning |
|---|---|
| `record_version` | Version of this record kind's format. Consumers refuse unsupported versions; they do not guess. |
| `kind` | Record kind, e.g. `gate.verdict`. Kinds live in the event/record registry held by the conformance suite. |
| `id` | This record's opaque identifier. |
| `producer` | `{system, utility, version}` — who emitted the record, with `system` always `tillr`. This is where family identity survives even where a bare noun is used in context ([§6](#6-the-naming-model-one-prefix-independent-names-or-a-hybrid)). |
| `created_at` | RFC 3339 UTC timestamp. Fixture and test modes take the clock as an input so records are reproducible. |
| `subject` | `{kind, id}` of the thing the record is about, where applicable. |
| `correlation_id` | Optional identifier carried across one composed pipeline run so its records can be reassembled by `view`. |
| `idempotency_key` | Optional, on write requests: replaying a request with the same key returns the original result instead of writing twice. |
| `payload` | The kind-specific fields. The per-utility field lists below describe payloads. |

Exit codes are part of the same contract. The distinction that matters most
to an agent caller is between a *negative decision* (the utility ran and
the answer is no) and *no decision* (the utility could not answer):

| Exit | Meaning | Record on stdout? | State changed? |
|---|---|---|---|
| 0 | Completed; where the face makes a decision, the decision is positive (pass, eligible, landed, found). | Yes | Only as specified for the face |
| 1 | Completed with a negative decision: check failed, candidate not eligible, subject not found, dispatch held. The record says why. | Yes | Only the decision record itself, where the face records decisions |
| 2 | Usage error or malformed/unsupported input record. | No (diagnostics on stderr) | No |
| 3 | Store, schema, or record-version incompatible, or the store stayed locked past the caller's bound. | No | No |
| 4 | An external dependency prevented a decision: git, a check command that could not start, a runner that could not launch. Distinct from a failed check. | Where a partial verdict record exists, yes | No decision recorded as final |
| 5 | Internal error. A bug, not an outcome. | No | The write transaction, if any, is rolled back |

Store access is always explicit in fixture and standalone use: a caller
names `--store <path>`; no standalone face silently opens the live store.
Read-only faces (`view`, and every `check`/`plan` face below) open the store
read-only. A fixture store is marked as a fixture at creation, and seed
import refuses any store not so marked — seeding a live store must be
impossible, not merely unlikely.

The logical table ownership the subsections below refer to:

| Utility | Writes (logical tables) | Reads |
|---|---|---|
| `store` | `meta`, `schema_migrations`; administers `events` | All, for verification only |
| `item` | `items` (including each item's `design_doc` reference), `item_criteria`, `item_edges`, `decisions`, `mail_deliveries` | Its own tables, `meta`; the design document an item points at, when surfacing its open questions |
| `land` | `candidates`, `landing_attempts`; external write: the trunk git ref | `items` (as records), its own tables, `events` |
| `drain` | `dispatches` | `items`, `lanes`, `runs`, its own table, `events` |
| `run` | `lanes`, `runs`; external writes: processes and worktrees under a caller-named lane root | Its own tables, `meta` |
| `gate` | Nothing in no-store mode; `gate_plans`, `gate_verdicts` only when a store is named | The check registry (config), the candidate/change record |
| `view` | Nothing | Any table or record stream, read-only |

There is no `questions` table. Open questions live in design documents, and
no utility stores or mirrors them; what the store holds is the `design_doc`
reference on an item, and the questions themselves are read from that
document at the moment they are surfaced.

The event log needs one clarification against a naive reading of "one
writer": `store` owns the `events` table's schema and administration, but
each utility appends events for *its own* state transitions, inside the same
transaction as the transition, through the substrate's append primitive.
Appending is part of writing one's own state; no utility appends an event
asserting a transition in another utility's tables.

### 5.2 `store` alone

**Does alone:** creates, migrates, verifies, and interrogates a store.

```
tillr store init   --store $FIXTURE/store.sqlite3 --fixture
tillr store seed   --store $FIXTURE/store.sqlite3 --input seed-records.json
tillr store check  --store $FIXTURE/store.sqlite3
tillr store events --store $FIXTURE/store.sqlite3 --since 0 --output events.json
```

`check` emits a `store.report` record: `schema_version`,
`supported_record_versions`, `event_head_seq`, `table_counts` (by logical
table), `fixture` (boolean), and `integrity` (`ok` or the first failure).
Exit 0 if the store is compatible and intact, 3 if its schema is newer or
older than this binary supports without migration.

**Requires:** nothing but a filesystem path. `seed` consumes a record bundle
(`kind: fixture.seed`, payload `records: [...]`) and refuses (exit 2) any
store not marked as a fixture.

**Scenario:** every other utility's test suite starts here — a throwaway
store built from seed records in milliseconds, with no live store at risk.
It is equally the forensic entry point: `check` and `events` against a
*copied* production store answer "what schema is this and what happened in
it" while every writer is stopped.

### 5.3 `item` alone

**Does alone:** creates and mutates work items and their attached decision
records in a named store, and surfaces the open questions of the design
document an item points at, read from that document.

```
tillr item apply --store $FIXTURE/store.sqlite3 --input item-record.json
tillr item show  --store $FIXTURE/store.sqlite3 --item item_042
```

It consumes and emits `item.record` payloads with fields: `item_id`,
`title`, `body_ref` (a reference, not necessarily inline text), `state`,
`criteria` (list of `{criterion_id, text, state, claim_class}`), `blocked_by` (list of
item ids), `design_doc` (an optional reference to the item's design
document), and `updated_at`. A successful `apply` echoes the record as
stored, including the event sequence it produced; an `apply` that violates
an edge or state rule exits 1 with an `item.rejection` record naming the
rule, and writes nothing.

For the Git-landing definition, entering `landed` requires at least one criterion,
all `done` (`landed-criteria-unmet`), and a current receipt: a `landings` row
with result `landed` or a `drain.graph-completed` event naming the item
(`landed-receipt-missing`). A receipt is current if it follows the latest
return from `landed` to `in-queue`, or no return has occurred. Entering
`resolved-elsewhere` requires `--resolves-via` with `item:<id>` naming another
stored item, `commit:<sha>`, or `doc:<path>` (`resolved-pointer-missing`); a
current receipt refuses the move as `already-landed`. `show` prints the pointer.
These rules cover changes through `state` and `apply`, not imports of new
finished items or reads of existing ones. Finished states are terminal except
that `state` may return a reverted `landed` item to `in-queue` with `--reason`
and `--by`; `apply` cannot make that return. Same-state writes are idempotent.

Criterion text and criterion state are not ordinary editable fields, and
`apply` is not their writer. A criterion's `text` is frozen when the
criterion is first stored: an `apply` that presents different text for an
existing `criterion_id` is refused by name, and new criteria may only be
appended. A criterion's `state` moves only on an `item.verdict` record —
`item_id`, `criterion_id`, the hash of the frozen criterion text, the
verdict (`done` / `not-done` / `blocked` / `defective`), its reason, the
landed refs and evidence pointers it read, the evaluated head, and its
actor kind (`mechanical` / `agent` / `human`). An `apply` carrying a changed
criterion state with no verdict is refused, and a verdict naming any hash
but the frozen text's is refused; only a verdict retires an item. Who may
produce each actor kind's verdict — the composed pipeline for a mechanical
one, an agent only as the selected workflow's independent evaluator — is ruled in
[the judge and lifecycle design](tillr-judge-and-lifecycle.md).
Repairing a frozen criterion that is itself wrong is a supersession record,
never an edit; its shape is owned by [`criterion-repair.md`](criterion-repair.md).

Each criterion stores `item_criteria.claim_class`, a non-null text column
that defaults to `verification-only`; `show` includes it for every criterion.
The class states which authority can judge the claim, not whether it is true.
An item block declares classes with `claims="c1:owner-only,c3:direction"`;
unnamed criteria default to `verification-only`, and `sync` stores the classes.
An unknown class or an id absent from the block is a finding naming that
block, and its file writes nothing.

Classes can only become stricter through `apply` or `sync`:
`verification-only` can become any class, and `red-gate-triage` or `direction`
can become `owner-only`. Keeping the same class is allowed; every other move
is refused as `claim-class-weakened`. Omitting an authority-bearing criterion
is refused too, even before it has a verdict, so removing and recreating it
cannot weaken its class. Class-only hardening does not rewrite frozen
criterion text or its verdict state. The actor rules, settled in
[the judge and lifecycle design](./tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow), are:

| Stored class and meaning | `mechanical` | `agent` | `human` |
| :--- | :--- | :--- | :--- |
| `verification-only`: truth settled by a named check on evidence | Allowed with evidence | Allowed with independence when listed in `judge_classes` | Allowed |
| `red-gate-triage`: judgment about a failing gate | Refused | Allowed with independence when listed in `judge_classes`; not listed by the shipped evaluators | Allowed |
| `direction`: choice between defensible directions | Refused | Allowed with independence when listed in `judge_classes`; not listed by the shipped evaluators | Allowed |
| `owner-only`: a decision reserved to the owner | Refused | Refused | Allowed; sole writer |

`verdict record` refuses a class differing from the stored one as
`claim-class-mismatch`, and an incompatible actor as `claim-class-actor`.
Agent intake checks the persisted evaluator and its class list, and binds the
verdict to the selected graph, node and exact candidate. No renewal is required.
A `verification-only` criterion matching `(?i)^owner decision recorded\b`
is refused by both definition writers as `owner-criterion-untyped`.
On a migrating store opening, the item utility's registered migration types
legacy rows with that prose as `owner-only`; repeated openings leave their
classes unchanged. The loop reads the stored class, records mechanical
verdicts only for `verification-only`, and prints each criterion left for
another authority without treating it as a failure.

Questions are not among these faces, because they are not board state.
`show` surfaces them instead: where the item carries a `design_doc`
reference, the output includes that document's open questions — their
identifiers and text as the document itself declares them, with the
document named as their source — read from the document at show time. The
board never stores, mirrors, or answers them, so the document and the item
cannot drift apart: there is only one copy. A question is answered by
editing the document, in Vantage's world, and the next `show` simply reads
the new state of the document.

**Requires:** a store (fixture or live) at a compatible schema, and — only
for the questions `show` surfaces — read access to the design document the
item points at. It does not require the drain to be running, a lane to
exist, or mail to be deliverable: in fixture mode, mail is recorded in
`mail_deliveries` as intent and not sent.

**Scenario:** backlog import and triage with the execution side of the
system entirely stopped — items are created, criteria appended, edges
validated, and each item's design-document questions read in place. Agents
can also rehearse a brief: apply it to a fixture store and read back the
stored record to confirm the criteria and edges say what the author meant,
before anything is dispatched.

One follow-up is noted here rather than left open: if reading questions
from documents proves awkward in practice, the remedy is a Vantage feature
request to make that integration easier — `vantage-check`'s linter is the
existing tool in that world — not a board-side copy of the questions.

### 5.4 `land` alone

**Does alone:** evaluates candidate eligibility and performs (or refuses)
the landing transaction, driven by records rather than by live counter-parties.

```
tillr land enqueue --store $FIXTURE/store.sqlite3 --repo $FIXTURE/repo --input candidate-record.json
tillr land check   --store $FIXTURE/store.sqlite3 --repo $FIXTURE/repo --candidate cand_007 --input verdict-record.json
tillr land pass    --store $FIXTURE/store.sqlite3 --repo $FIXTURE/repo --candidate cand_007 --input verdict-record.json
```

The input `land.candidate` payload: `candidate_id`, `item_id`, `base_ref`,
`base_sha`, `head_sha`, `tree_sha`, `state`, `enqueued_at`. The crucial
input to `check` and `pass` is a `gate.verdict` **record** ([§5.7](#57-gate-alone)) —
a file, not a running gate. `land` verifies the verdict binds the same
`tree_sha` and `base_sha` as the candidate; a verdict that binds any other
tree is a refusal, never a re-plan. A trunk that has moved since `base_sha`
is carried, not refused, per
[`landing-principles.md`](landing-principles.md) [Section 3](landing-principles.md#3-gate-what-will-be-the-tip-once):
`pass` re-applies the gated candidate onto the trunk's current tip and
lands a clean re-application by one compare-and-swap naming that tip, with
no second gate, while a re-application that conflicts isolates the
candidate — its repair is a new candidate, gated fresh, because the tree a
repair produces is a tree no verdict has seen. Both faces emit a
`land.attempt` record: `attempt_id`, `candidate_id`, `verdict_id`, `result`
(`landed` / `isolated` / `refused`), `reason` (registered string, e.g.
`carry-conflict`, `verdict-mismatch`, `verdict-failed`), `trunk_before`,
`trunk_after`, and, where the candidate was carried, the carried
`tree_sha` beside the gated one the verdict names. `check` is the same
evaluation with the ref move suppressed (`result` reports what `pass`
would do, carry included); `pass` on a refusal exits 1 and moves nothing.

**Requires:** a store, a git repository, and a verdict record. It does not
require the drain, a live gate process, or a live system of any kind — that
is what makes hand-landing ([§5.10](#510-landing-a-candidate-by-hand-without-the-drain)) possible.

**Scenario:** incident recovery. A candidate was isolated at 03:00 and
nobody knows why: replay `land check` against a restored fixture store and
the recorded verdict, and read the refusal reason, without starting the
drain or touching the live trunk.

### 5.5 `drain` alone

**Does alone:** computes dispatch decisions for one tick, deterministically.

```
tillr drain plan --store $FIXTURE/store.sqlite3 --input tick-request.json --output decisions.json
tillr drain once --store $FIXTURE/store.sqlite3 --input tick-request.json --runner record-sink
```

`plan` is a pure decision face: it consumes a `drain.tick` payload — `now`
(the injected clock), `available_lanes`, `max_dispatches`, `policy_ref` —
reads item, lane, and run state, and emits one `drain.decision` record per
considered item: `decision_id`, `item_id`, `action`
(`dispatch` / `hold` / `retry`), `reason` (registered string, e.g.
`blocked`, `no-lane`, `capacity`, `question-open`), `lane_id` where
assigned, and, for `dispatch`, a complete `run.request` payload
([§5.6](#56-run-alone)). Where `question-open` is the reason, it is derived
at plan time by reading the design document the item points at, through the
same surface as `item show` ([§5.3](#53-item-alone)) — never from
board-stored question state, because there is none. `plan` writes nothing.
A lane whose `leased_until` is at or before the tick's `now`, with no
terminal record for the run that leased it, is expired: `plan` treats it as
available, and `once` reaps it — recording the release — before it plans,
so no decision ever dispatches into a lane the store still calls leased.
An item with a terminal run behind it and a free lane is planned as
`retry`, never held for having run before; a correction retry keeps that
item's lane and its worktree, under a new run and a new lease, while a
different item dispatched onto a released lane is staged into a fresh
worktree at its own base. `once` additionally records the decisions in
`dispatches` and hands run requests to a caller-named runner; in fixture
mode the runner is a record sink that captures requests to a file instead
of launching anything.

**Requires:** a store whose `items`, `lanes`, and `runs` tables it reads
(and which, in a fixture, were seeded as records — [§5.2](#52-store-alone)). It does not
require `run` to launch anything, a real clock, or a supervision loop.

**Scenario:** "why won't this item dispatch?" — the single most common
drain question — becomes one command against a seeded fixture: `drain plan`
and read the `reason`. The same face is a capacity simulator: vary
`available_lanes` and `max_dispatches` and diff the decision records. A
drain that can only answer by being left running against the live store
fails question 3 of [§3.4](#34-the-four-question-test); the `plan`/`once` faces are therefore part
of the extraction's definition, not optional extras.

### 5.6 `run` alone

**Does alone:** allocates a lane and executes one run to completion,
recording its lifecycle.

```
tillr run exec    --store $FIXTURE/store.sqlite3 --lane-root $FIXTURE/lanes --input run-request.json
tillr run status  --store $FIXTURE/store.sqlite3 --run run_019
tillr run lanes   --store $FIXTURE/store.sqlite3
tillr run release --store $FIXTURE/store.sqlite3 --lane lane_003
```

`exec` consumes a `run.request`: `run_id`, `item_id`, `lane_id` (or a
request to allocate), `command` (an argv array, never a shell string),
`working_dir`, `timeout_ms`, and `env_refs` (names of environment entries,
resolved by the substrate config loader). It emits a terminal `run.record`:
`run_id`, `item_id`, `lane_id`, `state` (`exited` / `failed-to-start` / `timed-out`),
`exit_code` where the process ran, `started_at`, `finished_at`, and
`artifact_refs` (including the resulting `tree_sha` where the run produced
a candidate tree). `item_id` is copied from the request into the record and materialized on
the `runs` row: the request already carries it, so the record states it honestly, and a run
nobody can trace to the work it was dispatched for cannot be judged later. A command that exits nonzero is a completed run with
that `exit_code` in the record (the face itself exits 0 — the run's failure
is data, not the utility's); a command that cannot be started exits 4.

A lane is leased per run, and the lease is set by the runner at claim:
`exec`, in the transaction that allocates the lane and records the run's
start, writes the lane `leased` with `leased_until` set to the run's start
plus its `timeout_ms` plus a fixed grace of 300,000 ms for the kill and the
terminal record write. The clock is the store host's — the machine running
`run` and `drain` against the store — stored as Unix milliseconds in UTC,
the same clock and spelling as `started_at`; every comparison against a
lease (the planner's, the reaper's) reads that one clock, through the
tick's injected `now` where a tick supplies one ([§5.5](#55-drain-alone)).
`timeout_ms` is optional, and `0` means the run has no wall: `exec` takes a
cancellable context instead of a deadline, and the claim writes
`leased_until = NULL` — a lease no clock reaches. `NULL` is a real lease and
not a missing one: the reaper's `leased_until IS NOT NULL` skips it, so no
clock takes a run that is still working, `run lanes` and the planner hold
it, and `run release` frees it immediately. That is the operator's one
command for a run with no wall, and it is why an unbounded run is not a
lane held forever by construction. A negative `timeout_ms` is refused at
claim, because a wall cannot run backwards. A fixed TTL is refused — run
timeouts vary per request, so one TTL must either reap live long runs or
hold dead short ones — and a heartbeat is not built: `run` is a one-shot
process with no resident component to renew anything. The terminal record
releases the lane in the transaction that writes it — lane free,
`leased_until` cleared — clearing only a lane still leased to the finishing
run, so a run that finishes after its lease was reaped cannot release a
lane a newer run now holds. `run release` is the standalone form of that
release for a lane whose run is terminal or whose lease has expired with no
terminal record; releasing an unexpired *bounded* lease is refused by name,
because the longest an operator ever waits out a wedged lane is one timeout
plus the grace. A manual release closes a run that left no record of its
own with a `timed-out` record whose `finished_at` is the release's own
clock; an unbounded claim carries no expiry, so the epoch would be the only
other answer and that would be a lie. The lane itself persists across its
runs, worktree included ([§5.5](#55-drain-alone)); the lease is only ever
one run's claim on it.

The wall is a budget and not a fault detector
([`momentum-principle.md`](./momentum-principle.md)), so no default wall
exists anywhere in the suite: no `timeout_seconds` in the backend policy,
and `run-pi`, `run-muse` and `run-agy` wrap the harness in `timeout(1)`
only when staging passes an explicit `--timeout N`. What a wall does not
excuse is the work a killed run produced: `drain`'s `settle` judges any
run whose target `HEAD` moved off its base, whatever the exit status, and
only a run that committed nothing is unjudged
([`tillr-drain-successor.md`](../reference/tillr-drain-successor.md) [Transition Table](../reference/tillr-drain-successor.md#transition-table)).

**Requires:** a store and a caller-named lane root. In fixture use the
command is a stub (`/bin/true`, a script that writes a known artifact), so
the full lifecycle — allocate, start, observe, record — is exercised with
no agent and no drain.

**Scenario:** reproducing a failed agent run by hand: take the recorded
`run.request` from the store, replay it against a fixture lane root, and
compare `run.record`s. Lane hygiene is the other standalone job: `run lanes`
lists lane state (`lane_id`, `state`, `run_id`, `leased_until`) so a stale
lane can be identified and released without starting the drain.

### 5.7 `gate` alone

**Does alone:** turns a change description into a plan, and a plan plus a
worktree into a verdict. This is the most independent utility in the suite:
in its primary mode it needs no store at all.

```
tillr gate plan --input change-record.json --output plan-record.json
tillr gate run  --input plan-record.json --worktree $FIXTURE/worktree --output verdict-record.json
```

`plan` consumes a `gate.change`: `base_sha`, `tree_sha`, `changed_paths`,
and the `subject` (typically a candidate). It emits a `gate.plan`:
`plan_id`, `base_sha`, `tree_sha`, `registry_digest` (the digest of the
check registry that produced the plan), and `checks` — each
`{check_id, command (argv array), timeout_ms, reads, writes, depends_on}`.
The `reads`/`writes` declarations are what let a runner serialize checks
that would collide over one directory. `run` executes a plan in the named
worktree and emits a `gate.verdict`: `verdict_id`, `plan_id`,
`registry_digest`, `base_sha`, `tree_sha`, `result`
(`pass` / `fail` / `error`), and per-check `checks` entries
`{check_id, result, duration_ms, log_ref}`. Exit 0 on `pass`, 1 on `fail`
(a completed negative verdict), 4 on `error` (a check could not be run —
never to be read as a failure of the candidate). Where a store is named,
plans and verdicts are also recorded in `gate_plans` / `gate_verdicts`;
where none is named, the records on stdout/files are the whole output.

**Requires:** a check registry (substrate config) and, for `run`, a
worktree. No store, no candidate queue, no lander.

**Scenario:** preflight. An agent or developer runs `gate plan` and `gate
run` against a worktree before a candidate exists, and learns in under a
minute whether the change will gate — in any repository whose checks are
registered, whether or not the rest of this system is installed. This is
the one utility with a present claim to independent adoptability beyond
the system, because its work is already half-external today
([§2](#2-the-seam-analysis)).

### 5.8 `view` alone

**Does alone:** renders status and reports from a store opened read-only,
or from a stream of records with no store involved.

```
tillr view status --store $FIXTURE/store.sqlite3 --item item_042
tillr view status --records pipeline-records.json --item item_042
tillr view events --store $FIXTURE/store.sqlite3 --since 1200
```

`status` emits a `view.status` record: `generated_at`, `snapshot`
(`schema_version` and `event_head_seq`, so two reports can be compared for
whether they describe the same moment), and the assembled subject — the
item record, the open questions read from the design document the item
points at ([§5.3](#53-item-alone)), its candidates with their latest
`land.attempt`, the latest `gate.verdict` per candidate, and its runs.
Every component is copied from records other utilities emitted or from the
design document itself; `view` computes presentation, never new state.
Human-readable rendering is the same record through a format flag, not a
second data path. Exit 1 when the subject does not exist in the snapshot,
3 when the store's schema is unsupported.

**Requires:** read access to a store copy or a records file. Nothing else,
and it writes nothing anywhere — it is safe to point at the live store
while writers run, against a consistent snapshot.

**Scenario:** postmortem and owner reporting with the system stopped: copy
the store, run `view status` and `view events` over the copy, and the full
history of an item — dispatched, run, gated, isolated, and why — is
reconstructable without starting any writer. The `--records` mode makes the
same report available to a recipient who was sent records but has no store.

### 5.9 The composed pipeline

In normal operation an agent (or the drain as orchestrator) composes the
seven by carrying records between faces and letting each writer append its
events. No utility calls another in-process; the store and the record
stream are the only couplings.

| Step | Face | Consumes | Emits |
|---|---|---|---|
| 1 | `item apply` | `item.record` (state `ready`) | Stored item; `item.*` events |
| 2 | `drain plan` / `once` | `drain.tick`; item/lane/run state | `drain.decision` carrying a `run.request` |
| 3 | `run exec` | `run.request` | `run.record` with `artifact_refs` including `tree_sha` |
| 4 | `land enqueue` | `land.candidate` built from the run's artifacts | Candidate in `candidates`; `land.*` events |
| 5 | `gate plan` | `gate.change` for the candidate tree | `gate.plan` |
| 6 | `gate run` | `gate.plan`; candidate worktree | `gate.verdict` |
| 7 | `land pass` | Candidate; `gate.verdict` record | `land.attempt`; trunk ref moved iff the attempt lands |
| 8 | `view status` | Store snapshot or the records from steps 1–7 | `view.status` |

Every record in the table carries the pipeline's `correlation_id`, so step
8 can reassemble the run from the record stream alone. Note what the table
proves structurally: each step's input is a record a fixture can supply and
each step's output is a record a fixture can assert on. That is why the
fast suites in [§9](#9-testability-and-testing-speed) can replace neighbors with files.

### 5.10 Landing a candidate by hand, without the drain

The sharpest form of the owner's test: an agent has a finished candidate
and wants it landed now, with the drain stopped and no supervision loop
involved. Four faces, four separate invocations, each runnable alone:

```
# 1. Plan the gate for this candidate tree (no store needed).
tillr gate plan --input candidate-change.json --output plan.json

# 2. Run the plan in the candidate worktree. Exit 0 = pass, 1 = fail, 4 = could not run.
tillr gate run  --input plan.json --worktree <candidate-worktree> --output verdict.json

# 3. Land, presenting the verdict as a record. Refusals exit 1 with a reason and move nothing.
tillr land pass --store <store> --repo <repo> --candidate cand_007 --input verdict.json

# 4. Confirm from a read-only snapshot.
tillr view status --store <store> --item item_042
```

The safety properties live in the records, not in the operator's care: the
verdict binds `plan_id`, `registry_digest`, `base_sha`, and `tree_sha`, and
`land pass` refuses a verdict that does not match the candidate, and
carries the candidate onto a trunk that has moved since `base_sha`
([§5.4](#54-land-alone)). Substituting `land check` for step 3 gives
the same evaluation as a dry run. If any step required a running drain, a
live gate process, or an in-process call into another utility, this
walkthrough would be impossible — and the boundary it crosses would be a
code seam to redraw under [§3.4](#34-the-four-question-test), not a seam to defend.

### 5.11 Where the burden of proof sits

To be precise about what this section claims: the invocations above are the
**required standalone faces of the extracted utilities**, stated concretely
enough to build fixtures against. They are not claims that today's monolith
already exposes each face in this form — `gate` and `view` are close by
construction (half-external; writes nothing), while `land check` against a
verdict file and `drain plan` as a pure decision face are work the
extraction must actually do, and their feasibility against the current code
is unverified until gate-zero's inventory maps the current shapes onto the
logical contract and the first extraction is attempted. Each migration step
in [§12](#12-the-migration-path) therefore carries an acceptance condition,
in its strongest form: **the utility's own binary, installed alone, runs
its standalone faces green against a fixture store and/or records, with no
other utility's binary present**, before the monolith's path for that
responsibility is retired. A utility that cannot meet that condition does
not get extracted as specced; its seam goes back through
[§3.4](#34-the-four-question-test).

## 6. The naming model: one prefix, independent names, or a hybrid

The naming *shape* — long names, no letter prefix — was settled first. How
the system name combines with the utility nouns is settled now too, and the
model is deliberately simple: the hybrid of [§6.3](#63-the-model-hybrid-without-ceremony),
with no ladder of earned names and no ceremony. This section separates the
two settled parts, compares the three models that were on the table, and
states the model.

### 6.1 What is settled: long names, no letter prefix

The previous draft named the utilities `bstore`, `bland`, `blandq`, and so
on. The owner hated them, and they are in fact bad names:

- The `b` carries no meaning. Everything in the suite is part of the system;
  a prefix that says "part of the system" says nothing.
- They collide with names already in the tree (`bin/land`, `tools/land`,
  `tools/mq`), so the new names would be ambiguous against the old during
  exactly the migration that must keep them distinct.
- `bland` reads as the English word "bland"; `blandq` is unpronounceable.
  Names are read aloud in design discussion and dictated in prompts; a name
  that cannot be said cannot be briefed.
- A bare letter is not the Unix convention anyway. The convention is a *word*
  prefix where a prefix is used at all: `git-commit`, `git-merge`,
  `git-remote-https`.

The decisive point is the owner's second one: **most or all of these
utilities will be invoked by agents, not by humans at a prompt.** A
utility's name is read in a tool call, a log line, an error message, a gate
plan, and a doc — never typed under time pressure by someone saving
keystrokes. That flips the usual Unix brevity argument: the cost of a long
name (typing) barely exists, and the benefit (a log line that explains
itself to the agent reading it) is paid on every invocation. Under the
model in [§6.3](#63-the-model-hybrid-without-ceremony), every utility's name
says what state it owns or what loop it runs — `store`, `item`, `land`,
`drain`, `run`, `gate`, `view` — because a name that survives the
four-question test should say what its utility owns. Whimsical per-tool
brands would have to argue against this paragraph, not merely be available.

### 6.2 The three models

| | One prefix | Independent names | Hybrid |
|---|---|---|---|
| **Shape** | `tillr-store`, `tillr-item`, … / `tillr store`, … | Each utility has its own unrelated name | `tillr <noun>` canonical over `tillr-<noun>` binaries; the bare noun correct in context |
| **What a name tells an agent reading a log** | The job *and* the family, every time | The job only — if the names stay descriptive; nothing, if they turn brand-like | The job always; the family wherever the canonical form is used, and in every record regardless |
| **Independent use ([§5](#5-independent-use-the-boundary-made-concrete))** | Possible but disguised: a prefixed name advertises a dependence the design just removed | Advertised honestly: the name claims no more coupling than exists | Advertised by the separate binaries themselves; the wrapper form adds family without hiding the binary |
| **Learning and discovery** | One name to learn; the family lists itself under one prefix | Seven names, seven doc sets; the family relationship is invisible without a map | One system name, seven descriptive nouns, one dispatch list (`tillr` with no noun) |
| **PATH collisions** | None of consequence | Real: bare `gate`, `run`, `view` are generic words other tools also want | Avoided: on-PATH names are `tillr` and `tillr-<noun>`; bare nouns are speech and in-context reference, not PATH entries |
| **Packaging and versioning** | One distribution, one version | Tends toward seven packages and seven version lines — and therefore toward skew that nothing polices | Seven binaries released together as one version set ([§11](#11-seven-binaries-versioned-together)): separately installable, versioned as one |
| **Cost of renaming the system later** | Every name changes at once | Only the umbrella changes; utility names stand | The umbrella and the binary prefixes change; the utility nouns — the identities in records, modules, and docs — stand |
| **Failure mode** | The prefix becomes empty ceremony repeated in every log line | Seven brands to remember, and an agent holding a `gate` verdict cannot tell whose `land` should consume it | Two ways to refer to a utility (canonical and bare); contained by using the canonical form in docs, records, and commands, and the bare noun only where context already supplies the family |

Two facts discipline the choice. First, the agent-invocation fact cuts
*both* ways: it makes long names cheap, but it also means a name's audience
is a reader mid-task with limited context — family visibility in the name
is one way to give that reader provenance, and the record envelope's
`producer` field ([§5.1](#51-the-common-contract)) is another, which works under any model.
Provenance therefore does not *require* a prefix; it requires that one of
the two mechanisms be reliable, and the record mechanism is already
mandatory. Second, independent *runnability* is required of all seven and
delivered by the separate binaries ([§5](#5-independent-use-the-boundary-made-concrete),
[§11](#11-seven-binaries-versioned-together)); independent *brand
names* would add nothing to that. A name is a publishing commitment, and
publishing seven brands on spec repeats, in naming form, the over-splitting
error [§3](#3-why-not-more-the-split-curve) warns against in code.

### 6.3 The model: hybrid, without ceremony

**The canonical form is `tillr <noun>`; the bare noun is correct in
context.** That is the whole model. As with Git — it is called
`git rebase`, and everyone understands "rebase" in a Git context — the
standard form names the system, and context does the rest.

- Each utility's **identity** is its descriptive noun — in module names,
  record kinds, gate step names, docs, and the `producer.utility` field.
  That identity is independent: it does not change if the system is renamed,
  and it is what [§5](#5-independent-use-the-boundary-made-concrete)'s standalone faces answer to conceptually.
- The **distribution** is a release train of seven binaries,
  `tillr-store` through `tillr-view`, versioned together as one set
  ([§11](#11-seven-binaries-versioned-together)). The thin `tillr`
  wrapper dispatches `tillr <noun> ...` to `tillr-<noun>`; each binary also
  runs its faces directly.
- The family relationship is carried by the `tillr` name, the docs, and
  `producer.system` in every record — documented and recorded, rather than
  re-encoded in every reference.

A utility that later earns a fully standalone name of its own is a
publishing decision to make then, on whatever evidence exists then; this
design specifies no ladder for earning one. The honest cost of the hybrid
is its failure mode from the table — two forms of reference — contained by
one rule: docs, records, and commands use the canonical form, and the bare
noun is for speech and in-context reference only.

### 6.4 The name is `tillr`

The system name is **`tillr`**. The old public project of that name is
wiped and re-founded fresh: the name is taken, the baggage is not. The
one-time cost of that reset — whatever stale search results, docs, and
inbound references the old project carries until the new system overwrites
them in practice — is accepted, not priced further.

The name is genuinely apt, not decorative: the coordinator is "the whale,"
and a tiller is what steers the boat, which is loosely what this system is
for the whale's work — the steering gear.

`tillr` is the system name everywhere it appears: the wrapper, the seven
`tillr-<noun>` binaries, the store and config paths, and the
`producer.system` value in every record. The utility nouns underneath it
are independent identities ([§6.3](#63-the-model-hybrid-without-ceremony))
and would survive a future system rename, but no name in this design is
schematic any longer.

## 7. The rename: now is the cheapest moment

The system is renamed from `board` to `tillr` in this pass. The
decomposition is the cheapest moment for it: the decomposition already
touches every command, every doc, and every test, so the marginal cost of
new names inside that pass is small against the cost of a second,
dedicated rename pass later — which would touch the same surfaces again,
this time including seven new utilities' worth of freshly-written docs,
records, and muscle memory. Under the hybrid model the rename surface is
smaller than under one-prefix naming — the utility nouns stand either
way — but the store path, config, binary names, and record `producer`
values all carry the system name.

The cost, stated honestly. The rename touches:

- the store path (`~/.local/state/board/`) — which means a **migration for
  existing stores to their `tillr` location, executed by the system**, not
  by the owner moving directories;
- the config (`~/.config/board/config.toml`), likewise migrated by the
  system;
- the extension (`board/client.ts`, `board/pile.ts`) and everything that
  imports it;
- the commands, the gate step names, and every doc, skill, and prompt that
  names them;
- every in-flight brief and board item that refers to the old names — the
  old names must keep resolving (aliases, redirects) until in-flight work
  drains, or the rename breaks the very dispatch system performing it.

That is a real cost, and it grows every month the old name accretes new
references, which is why it is paid now. The decomposition is written
against the `tillr` names throughout: no placeholder survives in docs or
records, and no `board-`-prefixed utilities are minted for a rename to
immediately orphan.

## 8. The shared substrate

One SQLite database, partitioned by writer. Each utility writes only the
tables it owns ([§4](#4-the-proposed-suite-seven-utilities), [§5.1](#51-the-common-contract)); any utility may read any table, but reads
cross boundaries as records, so a reader depends on the record contract, not
on another module's schema choices. Nothing yet pays for several databases
— one file to back up, one event log with one order — and the record
contract does not foreclose them later if a demonstrated contention or
backup-granularity need ever argues for paying.

The record and table shapes on this substrate are the design's own,
designed for the boundary rather than inherited from the current store
([§5.1](#51-the-common-contract)). The migration maps the old shapes onto
the logical contract; compatibility with today's column names is not a
design constraint, and the opening registry inventory is an inventory of
what to move, not a specification of what to keep.

- **Notification is the event log.** Utilities learn that state changed by
  following the event log (`tillr store events` / `tillr view events`),
  not by being called. A writer's obligation ends at appending the event;
  who reacts is the reactors' business. This is what makes `view` and the
  drain composable with writers they were not written against (G5).
- **The boundary contract** is four things: the record format (envelope and
  payloads, [§5.1](#51-the-common-contract)), the schema package, the event-kind registry, and
  the exit-code convention. The conformance suite holds all four and is
  **always selected** — it is the one suite no change may skip, because it
  is the empirical content of the claim in [§1](#1-the-goals-as-a-set) that selection is safe.
  The standalone invocations of [§5](#5-independent-use-the-boundary-made-concrete) are conformance cases, not
  merely documentation: each is run against a fixture store with the other
  utilities' binaries absent.
- **Substrate changes are the one legitimately wide blast radius.** A schema
  or record change selects every module's suite, takes migration through
  `store`, and is scheduled as such — rarely, deliberately, and never
  smuggled inside a one-module change.

## 9. Testability and testing speed

The current suite is 1,141 test functions in 209 files — 69,581 lines of
test against 86,844 lines of non-test Go — and its cost is dominated by
per-test process spawn and real waits: tests that start the binary to
observe it, and sleep to let loops tick. P6's 5-second target for the core
loop is unmet by roughly two orders of magnitude.

A split by itself fixes none of this: it redistributes the same 1,141 tests
among seven modules, and a suite dragged across a boundary intact arrives
exactly as heavy ([§3.2](#32-what-more-splits-do-not-buy)). What the untangling that makes a real split
possible buys, on top of redistribution, is two reductions. **First, fewer
tests.** In the monolith no module can say which entry point owns a
behavior, so the same behavior is covered from several entry points, each
test standing up enough of the program to reach it. Once one writer owns
the state, the duplicate cases become redundant: they collapse into the
owning module's one test and the rest are deleted, not moved. **Second,
cheaper survivors.** A test inside a boundary takes its neighbors, the
clock, and process-spawning as inputs — fixture stores, input records, an
injected clock — instead of reaching for the real ones, so it stops paying
to stand up the whole program. A test that needs a landed candidate
consumes a "landed" record; it does not run a landing. The standalone faces
in [§5](#5-independent-use-the-boundary-made-concrete) are exactly the surfaces those tests drive: `drain plan`
with an injected `now` replaces a test that sleeps; `land check` against a
verdict file replaces a test that runs a gate; `run exec` with a stub
command replaces a test that launches an agent.

The limits stay honest. **The size of the reduction is unmeasured.** No
measurement says how much of the 69,581 lines is duplicated coverage or
whole-program setup; the shrink is unknown until the first module migrates,
and it is a target to be re-derived from that module's actuals — the same
discipline [§10](#10-landing-velocity-the-per-module-gate) applies to its 35–50s figure. The heavy suites
arrive heavy (`landing_queue_test.go`, 3,038 lines; `drain_test.go`, 1,947
lines) until their own untangling is done; the worst case still pays a heavy
module's own suite; and a substrate change still selects everything.

Budgets, per utility — and these budgets stand as the acceptance bar, not
as opening bids:

| Tier | Budget | Selected |
|---|---|---|
| Core loop (the tests a developer runs per edit) | 5s | By hand, constantly |
| Fast suite (one module, all of it) | 20–40s | Per change to that module |
| Conformance suite (contract + [§5](#5-independent-use-the-boundary-made-concrete) standalone cases) | Seconds | Always |
| Integration / pipeline tier (utilities composed, [§5.9](#59-the-composed-pipeline)) | Unbounded, but honest about it | Least often: substrate changes, scheduled runs, pre-release |

A module that cannot meet its fast-suite budget has a testability defect,
not a budget negotiation. The heavy inherited suites named in [§3.2](#32-what-more-splits-do-not-buy)
are the known cases; their budgets are met by deleting duplicated coverage
as ownership becomes clear and converting the surviving spawn-and-sleep
tests to fixture-and-record tests as they move. Both halves are work the
migration must budget for explicitly rather than discover, and neither is
delivered by the move alone. What that conversion costs is genuinely
unknown until the first heavy module migrates and is measured; until then
the gate target in [§10](#10-landing-velocity-the-per-module-gate) is
arithmetic, not fact.

## 10. Landing velocity: the per-module gate

Today the gate runs the monolith as one step: `go-tool` is
`cd tools/board-cli && go test ./...`, with a 3,600s timeout, priced at
175.0s, measured at a 303.5s median and 545.6s max — about 16m44s of a
17m9s gate wall under load. Every change, however small, pays for every
test.

The decomposed gate replaces that one step with:

- **`go-<utility>`** — one step per module, selected when that module (or
  the substrate) changes;
- **`go-substrate`** — the schema/record/event packages' own suites;
- **`tillr-conformance`** — the boundary contract suite, always selected;
- **`tillr-pipeline`** — the integration tier, selected on substrate
  changes and on schedule, not per landing.

Two different wins are stacked in that replacement, and the arithmetic
below claims both. **Selection**: the typical change runs *less of the
suite* — one module's fast suite instead of all 1,141 tests. **Shrinkage**:
the suite itself is smaller and each surviving test cheaper, because the
untangling deletes duplicated coverage and takes whole-program setup out of
the survivors ([§9](#9-testability-and-testing-speed)). The arithmetic for the typical one-module
change: one fast suite (20–40s) plus conformance (seconds) targets
**35–50s of selected test time against today's ~17 minutes of gate wall**.
That figure is a target derived from the [§9](#9-testability-and-testing-speed) budgets, not a
measurement, and the two wins stand on different evidence: selection is
structural — it follows from the module boundary — while the size of the
shrinkage is unknown until the first module migrates. The target becomes a
measurement when that module lands ([§12](#12-the-migration-path)) and should be re-derived
from its actuals, shrinkage included, before the rest of the migration is
priced. And per [§3.2](#32-what-more-splits-do-not-buy), the worst case — a substrate change — still
selects everything, by design. Velocity is bought for the median change;
the wide change stays wide, and now knows why.

### 10.1 Parallel gating of non-overlapping candidates

Could two candidates that touch disjoint files be gated at the same time
and then landed one after another, instead of gating serially or batching
them into one combined candidate? For the *final* verdict, no — and after
the per-module gate, it would buy little. The reasoning, in two halves:

**Can they? Not as landable verdicts.** This design binds every verdict
to the exact tree it examined: `gate.verdict` carries `base_sha` and
`tree_sha`, and `land pass` refuses a verdict whose tree is not the
candidate's ([§5.4](#54-land-alone),
[§5.10](#510-landing-a-candidate-by-hand-without-the-drain)). Gate two
candidates in parallel against the same trunk and the first one lands: the
trunk moves, so the second verdict binds a stale base and a tree that is
not the tree landing would produce. Carrying ([§5.4](#54-land-alone)) lands
that second candidate by re-applying it — which is exactly why parallel
final gating stays refused: carrying prices one ungated re-application
under a serial gate, and parallel gating would multiply ungated
combinations, each landing on a verdict about a tree that never existed.
Only the first of a parallel batch ever holds a verdict about the tree
that lands. Disjoint files do not repair this, because textual
disjointness is decidable from `changed_paths` but *semantic* independence
is not: one candidate changes a function's signature in one file, the other
adds a caller in a file the first never touches, each passes alone against
the old trunk, and the combination does not compile. When the
non-overlap decision is wrong in that way, the failure mode is a broken
trunk admitted by two green gates — the exact outcome a final gate exists
to prevent. The only sound parallel form is *speculative* gating: gate
the second candidate against a predicted trunk made of the first candidate
applied, the third against the first two applied, and so on. That is
real machinery — predicted trees, and an invalidation cascade that
discards every downstream speculative verdict when an earlier candidate
fails or is isolated — built on top of the landing utility, which is
already the riskiest migration step ([§12](#12-the-migration-path)). This design does not
adopt it. Today's pass gates and lands candidates one after another under
one land lock, and the decomposed design keeps that shape; the serial
landing transaction itself — a ref move — is cheap, and is not the
bottleneck. The gate's wall time is.

**Would it matter? The per-module gate removes the pressure first.** The
queue's wait is queue depth times the serial gate wall. At the last
observed depth of 15 queued candidates, today's ~17-minute gate prices the
tail of the queue at roughly four hours of gating; the 35–50s target above
prices the same queue at roughly 9–13 minutes. Parallel final gating is a
remedy for a queue that per-module selection already drains at an
acceptable rate. The CPU facts point the same way, though the conclusion
does not need them: the gate is `go test`, CPU-bound rather than I/O-bound,
and its wall already stretches under contention (priced 175.0s, measured
303.5s median and 545.6s max under load). Whether one gate saturates every
core on this machine is unmeasured; on a machine it does saturate, two
concurrent full gates make both slower, buy no throughput, and spend
timeout budget. What parallelism *is* safe, this design already has:
preflight. `gate plan` and `gate run` in a lane, before a candidate exists
([§5.7](#57-gate-alone)), produce advisory verdicts that claim nothing landable, so any
number of lanes may run them concurrently and failures are found before
enqueue — the overlap the queue cannot safely use, spent where it cannot
admit a broken trunk.

This is settled for this design: **final gating stays serial, one
candidate, one verdict bound; parallelism lives in preflight.** It reopens
only on measurements taken after the per-module gate exists — queue wait
(rather than gate wall) shown to be the routine bottleneck at real queue
depths, *and* a single selected gate shown to leave cores idle. Both are
measurable from gate timings, queue depth at enqueue and landing, and CPU
utilization during one gate. If both come back positive, speculative
gating is a new design to price on its own, not a tweak to this one.

## 11. Seven binaries, versioned together

**Seven separate binaries, versioned together.** Each utility is its own
binary — `tillr-store`, `tillr-item`, `tillr-land`, `tillr-drain`,
`tillr-run`, `tillr-gate`, `tillr-view` — with its own entry point, its own
docs, and its own package, released together as one version set sharing a
single version. A thin `tillr` wrapper provides a single control surface
in the Git style, dispatching `tillr gate ...` to `tillr-gate ...` as
`git` dispatches to `git-rebase`. The wrapper is a convenience; the
binaries are the product.

The reason is **adoption**, not composability. The utilities must be able
to be relied on individually: if there are seven, somebody must be able to
take three of them into their own project and leave the other four behind.
A multicall binary — one executable behaving as several utilities
depending on how it is invoked — satisfies "each face does useful work
alone" operationally, but it still ships all seven faces to anyone who
wants one, and it enforces the boundaries only by package structure and
import rules. Separate binaries make the reliance real: what a project
installs is exactly what it uses, and a boundary a neighboring utility is
not present to reach across cannot be reached across.

The module boundary still does its own work underneath: per-module gate
selection (G2), blast radius (G4), and the per-module test surface (G3)
come from strict modules, and the binaries are built one per module so the
two boundaries coincide. The naming model is independent of this choice
and already fits it ([§6](#6-the-naming-model-one-prefix-independent-names-or-a-hybrid)):
`tillr <noun>` is canonical whether typed through the wrapper or read in a
log, the bare noun is correct in context, and `tillr-<noun>` is the binary
that answers.

The honest costs, and they are the price of the adoption property:

- **Version skew is now possible.** Seven installed artifacts can drift to
  seven versions in a way one binary never could. The release train and
  the conformance suite therefore carry more weight: the seven are
  versioned and released as one set, records carry
  `producer.version` ([§5.1](#51-the-common-contract)), and consumers
  refuse unsupported record and schema versions rather than guessing
  (exit 3). Skew becomes a detected, refused condition instead of silent
  corruption — but detecting and refusing is work one binary did not owe.
- **Seven artifacts to build and publish** — seven build targets, seven
  packages, seven sets of docs, and a release that is not done until the
  whole set is. The set discipline is what keeps "seven binaries" from
  decaying into "seven products on seven schedules."
- **The wrapper must be kept honest.** A wrapper that only dispatches is a
  convenience; a wrapper that grows behavior of its own — composing faces,
  holding state, acquiring flags no binary has — becomes the monolith
  again with the utilities as its subroutines. Its entire job is to find
  `tillr-<noun>` and hand over argv, stdin, stdout, and the exit code,
  untouched.

## 12. The migration path

A strangler, sequenced by the owner's goal order (drain autonomy first,
velocity second), interleaved with the landing-velocity gate work:
per-module gate selection arrives at step 3, serving both the
decomposition and the velocity effort without either building gate
plumbing the other replaces. The first migrated module's measured gate
timings are the evidence that sequencing is revisited against. Every step
is executable by the system itself — the owner is not a copy-paste monkey,
and no step may require them to move files, rename directories, or
transcribe inventories by hand.

0. **Gate-zero: the registry inventory.** Generate the authoritative list
   of the 103 commands, the tables each writes, and the event kinds in use,
   from the binary's own registry and schema. Every later step is checked
   against this inventory; a command not in it does not exist, and a
   command in it must end up owned, deleted ([§4](#4-the-proposed-suite-seven-utilities)), or explicitly
   deferred. The inventory is an inventory of what to move, not a
   specification of what to keep: it establishes the physical shapes that
   exist so the migration can map them onto [§5](#5-independent-use-the-boundary-made-concrete)'s logical
   contract, which is designed for the boundary. Where an old shape and
   the logical contract differ, the contract wins and the migration does
   the mapping; compatibility with today's column names is not owed.
1. **`store` substrate first, silently.** Extract the schema, record, and
   event packages behind the existing binary. No behavior change; the
   conformance suite is born here, and fixture-store creation
   ([§5.2](#52-store-alone)) becomes available to every later step's tests.
2. **`view`** — the writer-of-nothing goes first, proving the record
   contract from the safe side and retiring the reporting surface out of
   the monolith.
3. **`gate`** — formalize the already-half-external boundary, so per-module
   selection exists before the remaining modules need it. Its no-store
   standalone mode ([§5.7](#57-gate-alone)) is the earliest full demonstration that a
   utility here can live on records alone. This is where the interleaved
   landing-velocity work meets the decomposition: one selection mechanism,
   built once, per module.
4. **`land`, absorbing `tools/mq`** — one landing implementation, one
   writer for candidate state. The riskiest step; it goes when the gate can
   already select per-module. `land check` against a verdict record is
   part of this step's acceptance, not a follow-up. The route — port,
   wrap-then-replace, or reimplement against `tools/mq`'s tests — is the
   implementer's to choose for speed; the step is done when exactly one
   implementation remains, and two never coexist beyond it.
5. **`item` and `run`** — the state-keeping domains, including R9's
   decision and mail records under `item` and each item's `design_doc`
   reference. No question records move, because questions are not board
   state: what `item` gains is the reference and the read-through surface
   of [§5.3](#53-item-alone).
6. **`drain` last** — it depends on all the others' records, and the
   autonomy work (the owner's first goal) lands on the decomposed drain
   rather than being moved twice. `drain plan` as a pure decision face is
   this step's boundary proof.

At every step, the acceptance condition from [§5.11](#511-where-the-burden-of-proof-sits) applies in its
strong form: the extracted utility's own binary, installed alone, runs its
standalone invocations green against a fixture store, with no other
utility's binary present, before the monolith's path retires.

Throughout, the shim is the wrapper: `tillr` dispatches each noun to its
extracted binary — or, for a responsibility not yet extracted, to the
monolith's surviving face — so in-flight work and external callers see one
interface, and the old `board` names keep resolving as aliases until
in-flight work drains ([§7](#7-the-rename-now-is-the-cheapest-moment)). **Writes never dual-run**: at every instant exactly one code
path writes each slice of state; a responsibility moves atomically or not
yet. Reads may dual-run freely — that is what makes `view` a safe first
step.

## 13. What this costs and what it forecloses

- **A record format, forever.** Boundaries that were function calls become
  versioned contracts with compatibility obligations. Changing a record is
  a substrate change ([§8](#8-the-shared-substrate)), priced accordingly.
- **Drift.** Seven modules can re-implement the same helper seven ways. The
  substrate must be the obvious home for shared code, or the suite
  re-monoliths by copy-paste.
- **Narrowed cross-domain atomicity.** Transactions that span two writers'
  state stop being available by construction. R3+R4 were merged precisely
  where that would have hurt most; anywhere else it surfaces later, the
  answer is an event and a compensating action, or a re-examination of the
  seam under [§3.4](#34-the-four-question-test) — not a reach across the boundary.
- **Selection can hide breakage if the contract leaks.** Every unrecorded
  coupling — a shared table written by two modules "just here," an event
  kind used but not registered — is a place where per-module selection
  claims safety it does not have. The conformance suite, the import rules,
  and the physical separation of the binaries
  ([§11](#11-seven-binaries-versioned-together)) are the entire
  defense; if they are allowed to rot, G2 becomes a way to land breakage
  faster.
- **The smaller suite is earned during migration, not delivered by it.**
  The shrinkage [§9](#9-testability-and-testing-speed) claims — duplicated coverage deleted once
  ownership is clear, survivors freed of whole-program setup — happens only
  if each migration step actually does the deleting and converting. A step
  that drags its suite across intact to save time that month makes the
  cost permanent and distributes it across seven modules, where the
  duplication is harder to see than it was in one. And until the first
  migrated module provides actuals, no number may be claimed for the
  reduction; the honest price of the win is budgeted pruning work per
  module plus that measurement, taken before the remaining migration is
  priced.
- **Standalone faces are product surface.** `drain plan`, `land check`,
  fixture seeding, and no-store gate mode ([§5](#5-independent-use-the-boundary-made-concrete)) must be maintained,
  documented, and kept honest against the live paths they mirror. A dry-run
  face that drifts from the real decision logic is worse than none: it
  produces confident wrong answers. The conformance suite must drive both
  from the same code path wherever the face claims equivalence.
- **Under the hybrid naming model, two reference forms.** `tillr gate` is
  canonical; bare `gate` is correct in context; `tillr-gate` is the binary
  ([§6.3](#63-the-model-hybrid-without-ceremony)). Docs, records, and
  commands must hold to the canonical form so a reader out of context is
  never left guessing whose `gate` a log line means — a small permanent
  discipline, and the price of the model's simplicity.
- **Seven binaries can skew, and must be released as one set
  ([§11](#11-seven-binaries-versioned-together)).** Separate artifacts
  make version skew possible where one binary made it impossible, so the
  release train and the conformance suite carry that weight permanently:
  seven artifacts built, versioned, published, and refused loudly
  (exit 3) when their record or schema versions do not match. The return
  is the other side of the same coin — a project installs only the
  utilities it relies on, and a fixed `tillr-drain` can ship in the next
  version set without its adopters taking a new `tillr-land` they never
  installed. And the `tillr` wrapper must stay a dispatcher; a wrapper
  that grows behavior becomes the monolith again.
- **Migration competes with feature work for its whole duration.** The
  sequence in [§12](#12-the-migration-path) is measured in landings, each paying the current
  gate until step 3 makes the gate cheaper. The velocity win arrives part
  way through, not at the end — by design — but the early steps are paid
  at today's prices.

### 13.1 What a second consumer would reuse, and what it would not

Would a second system with a similar-looking loop — generate work,
prioritize it, fan it out to agents, and generate more work from the
results — need any seam here to move so that it could reuse these chunks?
No. The two candidate changes both fail the four-question test
([§3.4](#34-the-four-question-test)) inside this system, and the reusable part is
already the part this design keeps separate on its own merits: the
substrate, the record contract, and the pattern of drawing utilities at
state seams.

- **A producer is a client, not a utility.** A *producer* is whatever
  originates work. This system has no utility that invents work because,
  inside this system, a producer would own no state its caller does not
  already own: its whole output is an `item.record` presented to
  `item apply` ([§5.3](#53-item-alone)), and a producer informed by results
  is a reader of the event log as well ([§8](#8-the-shared-substrate)) —
  composable with writers it was not written against, which is G5 already
  working. Minting it as an eighth utility would create exactly the
  pass-through [§3.3](#33-what-more-splits-cost) warns against: a boundary
  whose record carries no decision `item` does not make. If a second
  system has a generator that *does* own state — what has already been
  tried, what has been covered, what the results ruled out — that state
  is that system's seam to draw under [§3.4](#34-the-four-question-test),
  and its producer can still only deliver work here through `item`, the
  one writer. That is the boundary holding, not a missing face.
- **`drain plan` is pure, but it is not generic, and it does not need to
  be.** The decision face in [§5.5](#55-drain-alone) is deterministic and
  writes nothing, but what it decides is board policy: `blocked_by`
  edges, open questions read from an item's design document, lanes,
  capacity, and whatever `policy_ref` names. Owning that decision is why
  `drain` exists. Execution is already separated from it: `drain` emits a
  `run.request`, and `run` knows the command, the lane, and the timeout
  but not why this work was chosen next ([§5.6](#56-run-alone)). Carving a
  policy-free scheduling kernel out of `drain` would move board decisions
  into the substrate — the one wide blast radius
  ([§8](#8-the-shared-substrate)) — to serve a second system whose
  priority rules are not written down anywhere yet. A second system either
  casts its work as items and supplies its own `policy_ref`, or it writes
  its own dispatch utility over the same pattern. Both are available
  without touching this seam.

What transfers, and what is board-specific by design:

| Layer | What a second consumer reuses | What it does not reuse |
|---|---|---|
| Substrate | The mechanisms as libraries: the record envelope and versioning ([§5.1](#51-the-common-contract)), the exit-code convention, event append, fixture stores, the config loader, and the conformance pattern. | The board schema, event kinds, and payload fields. Those are this system's state, not the mechanism's. |
| `gate` | The no-store `plan`/`run` faces, where the job really is checking a tree against a registered set of checks — the one present claim to independent adoptability ([§5](#5-independent-use-the-boundary-made-concrete)). The `tillr-gate` binary itself, installed alone. | A `gate.verdict` as a general-purpose evaluation of results. A verdict binds a plan, a registry digest, and a tree so that `land` can trust it ([§5.4](#54-land-alone)); an exploratory result that binds no tree is a `run.record`, not a verdict, and treating it as one would strip the verdict of the property that makes landing safe. |
| `run` | The lane and process lifecycle shape: an argv array, a lane, a timeout, artifacts back, and a run's failure as data rather than the utility's. | The record as named here — its subject is an item and its headline artifact is a candidate tree. |
| `item`, `drain`, `land`, `view` | The pattern, and — where a second system's work genuinely is items, dispatch, landing, or reporting over the same records — the binaries themselves, taken individually ([§11](#11-seven-binaries-versioned-together)). | The board-specific content: frozen criteria, `blocked_by` edges, and `design_doc` references; the dispatch reasons and policy; the candidate lifecycle and the trunk transaction, which have no analogue outside landing code; and `view`'s assembled subject, which is a board subject. |

So the expectation to hold is this: a second system is a *second set of
utilities* over the substrate mechanisms and this pattern — and because
the seven ship as separate binaries
([§11](#11-seven-binaries-versioned-together)), it can also simply
take the three it wants and leave the four it does not, installing
`tillr-gate` where its work is checking trees without taking the rest of
the suite. Separate binaries strengthen the reuse answer; they do not
change its structure. Needing new tables, new payloads, new event kinds,
or new utilities in that system — including a producer that owns its
generation state there — is the design working as intended, not evidence
against it. What would reopen this subsection is narrower and harder:
building that system and finding that the substrate libraries themselves
cannot be used without forking them — that the envelope, the append
primitive, or fixture seeding cannot express its records without board
concepts leaking into the shared code. A seam forced in the substrate is
a reason to redraw one here; a seam drawn in a second system, on its own
state, is not. Until that evidence exists, generality for a consumer
described in one paragraph is a cost this design declines to pay
([§3.5](#35-the-conclusion)).

## 14. Decision ledger

`D-UT16` records the original claim authority rule. Its grant terms are
superseded by [the workflow rulebook](./tillr-judge-and-lifecycle.md#rulebook-and-its-enforcement).

| ID | Decision | Grounds |
|---|---|---|
| D-UT1 | Decompose `tools/board-cli` into utilities over one shared SQLite substrate, partitioned by writer. | G1–G5; [§2](#2-the-seam-analysis) |
| D-UT2 | Seven utilities: store, item, land, drain, run, gate, view. The count is an output of the state seams via the four-question test, not a target. | [§3.4](#34-the-four-question-test) |
| D-UT3 | Landing queue and landing pass are one utility with one writer for candidate state; it absorbs `tools/mq` and `landing_queue.go` into a single implementation. | [§2](#2-the-seam-analysis) (R4 exists twice), [§3.4](#34-the-four-question-test) |
| D-UT4 | Decisions and mail fold into `item`. Questions are not board state: they live in design documents, in Vantage's world; an item carries a `design_doc` reference and its open questions are read from that document when surfaced, never stored or mirrored. There is therefore no eighth utility to consider for questions. | [§3.4](#34-the-four-question-test), [§4](#4-the-proposed-suite-seven-utilities), [§5.3](#53-item-alone) |
| D-UT5 | The gate planner remains a separate utility; the selector must select itself and must not live inside the lander it examines. | Item 621; [§4](#4-the-proposed-suite-seven-utilities) |
| D-UT6 | Utility identities are long descriptive nouns naming the state owned or loop run; the `b`-prefix scheme is withdrawn. Agents, not humans, are the callers. How identities become command names is the naming model, D-UT12. | [§6.1](#61-what-is-settled-long-names-no-letter-prefix) |
| D-UT7 | Seven separate binaries — `tillr-store` through `tillr-view` — each its own entry point, docs, and package, released together as one version set. A thin `tillr` wrapper dispatches to them, Git-style, as a convenience control surface; the binaries are the product, so a project can take three and leave four. Version skew is policed by the release train and the conformance suite; the wrapper holds no behavior of its own. | [§11](#11-seven-binaries-versioned-together) |
| D-UT8 | The boundary contract — record format, schema package, event-kind registry, exit codes — is held by an always-selected conformance suite; substrate changes select every suite. | [§5.1](#51-the-common-contract), [§8](#8-the-shared-substrate), [§10](#10-landing-velocity-the-per-module-gate) |
| D-UT9 | Migration is a strangler in the owner's goal order; writes never dual-run; the `tillr` wrapper is the shim until in-flight work drains. | [§12](#12-the-migration-path) |
| D-UT10 | The system is renamed to `tillr` in this pass, before decomposition work mints user-visible names; the migration of existing stores and config is executed by the system, and old names resolve as aliases until in-flight work drains. | [§7](#7-the-rename-now-is-the-cheapest-moment) |
| D-UT11 | Independent use is an acceptance condition for every extraction, in its strongest form: each utility's own binary, installed alone, runs its standalone faces ([§5](#5-independent-use-the-boundary-made-concrete)) green against a fixture store and/or records, with no other utility's binary present, before the monolith path retires. A utility that fails is redrawn under [§3.4](#34-the-four-question-test). | [§5](#5-independent-use-the-boundary-made-concrete), [§5.11](#511-where-the-burden-of-proof-sits) |
| D-UT12 | Naming model, hybrid without ceremony: the canonical form is `tillr <noun>`, the bare noun is correct in context, and the utility nouns are independent identities that do not change if the system is renamed. The distribution is a release train of seven `tillr-<noun>` binaries, not one artifact. A fully standalone name for a utility is a publishing decision made on evidence when it arises; no ladder is specified. | [§6.2](#62-the-three-models), [§6.3](#63-the-model-hybrid-without-ceremony) |
| D-UT13 | The test win is stated as two wins: per-module *selection* (a typical change runs less of the suite) and *shrinkage* (ownership deletes duplicated coverage and removes whole-program setup from survivors). A split that only moves tests buys neither; the worst case still pays the heavy module's own suite; and the size of the shrinkage is unmeasured until the first module migrates, where its actuals re-derive the targets. | [§3.2](#32-what-more-splits-do-not-buy), [§9](#9-testability-and-testing-speed), [§10](#10-landing-velocity-the-per-module-gate) |
| D-UT14 | Final gating remains serial — one candidate, one verdict bound to its tree. Parallel gates against one trunk produce at most one landable verdict, and textual non-overlap does not prove semantic independence; parallelism is spent on advisory preflight instead. Speculative gating reopens only on post-decomposition measurements named in [§10.1](#101-parallel-gating-of-non-overlapping-candidates). | [§5.4](#54-land-alone), [§5.10](#510-landing-a-candidate-by-hand-without-the-drain), [§10.1](#101-parallel-gating-of-non-overlapping-candidates) |
| D-UT15 | No seam moves for a hypothetical second consumer. Reuse is the substrate mechanisms, the record contract, and whichever individual binaries — `tillr-gate` first — a second system takes on their own; a work producer is a client of `item` and a reader of the event log, not an eighth utility, and `drain`'s policy stays board policy with execution already separated in `run`. A second system otherwise builds its own utilities; this reopens only on evidence that the substrate libraries themselves must fork, not on that system needing its own tables, records, or utilities. | [§3.4](#34-the-four-question-test), [§5.3](#53-item-alone), [§5.5](#55-drain-alone), [§5.7](#57-gate-alone), [§8](#8-the-shared-substrate), [§13.1](#131-what-a-second-consumer-would-reuse-and-what-it-would-not) |
| D-UT16 | Criterion state moves only on an `item.verdict` naming the frozen criterion text's hash, and frozen criterion text cannot be rewritten through `item apply` (append only). Stored `item_criteria.claim_class` defaults to `verification-only` and can only harden. A mechanical verdict from a landed attempt retires only `verification-only`; an agent-issued verdict counts only under an independent actor and a store-enforced grant covering the stored class, never `owner-only`; a human alone judges `owner-only`. Verdict classes must match the stored class, and grants cannot delegate `owner-only`. The actor table and named refusals are in the governing section. (Designer's decision, 2026-11-12, written as a "design ruling"; the owner may overturn it. Claim authority settled 2026-11-16.) | [§5.3](#53-item-alone) |
| D-UT17 | A lane lease is set by `run exec` at claim for the run's `timeout_ms` plus a 300,000 ms grace, on the store host's clock; `timeout_ms: 0` is an unbounded run whose lease carries `leased_until = NULL` and which no clock reaps; the terminal record releases it, `drain once` reaps an expired lease before planning, and `run release` is the standalone release face. A correction retry keeps the item's lane and worktree under a new lease. (Designer's decision, 2026-11-12, written as a "design ruling"; the owner may overturn it. Unbounded leases amended 2026-11-15 by [`run-wall-ruling.md`](./run-wall-ruling.md) `D-RW1`, superseding this decision's mandatory positive wall.) | [§5.5](#55-drain-alone), [§5.6](#56-run-alone); [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#steering-continuation-and-session-modes) |
| D-UT19 | A run names its item: `run.record` carries `item_id`, copied from the request and materialized on the `runs` row, so every run is traceable to the work it was dispatched for. (Owner ruling, 2026-11-12.) | [§5.6](#56-run-alone), [§5.5](#55-drain-alone) |
| D-UT18 | A gated candidate whose trunk moved is carried: `land pass` re-applies it onto the current tip and lands a clean re-application with no second gate; a conflicting re-application isolates the candidate. (Owner ruling stands, recorded 2026-11-12.) | [§5.4](#54-land-alone), [§5.10](#510-landing-a-candidate-by-hand-without-the-drain); [`landing-principles.md`](landing-principles.md) [Section 3](landing-principles.md#3-gate-what-will-be-the-tip-once) |
| OQ-UT1 | One shared SQLite database, partitioned by writer. Nothing yet pays for several, and the record contract does not foreclose them later. (Owner ruling, 2026-11-06.) | [§8](#8-the-shared-substrate) |
| OQ-UT2 | Not a design question. The design fixes the end state — one landing implementation, one writer for candidate state — and the route (port `tools/mq`, wrap then replace, or reimplement against its tests) is the implementer's, constrained only to get there quickly and not let two implementations coexist longer than the migration step that removes the second. (Owner ruling, 2026-11-06.) | [§2](#2-the-seam-analysis), [§12](#12-the-migration-path) |
| OQ-UT3 | Interleaved: per-module gate selection arrives at migration step 3, serving both the decomposition and the landing-velocity work without building it twice, revisited against the first migrated module's measured gate timings. (Owner ruling, 2026-11-06.) | [§12](#12-the-migration-path) |
| OQ-UT4 | Seven separate binaries, versioned together as one release set, with a thin `tillr` wrapper as convenience control surface. The grounds are adoption: each utility can be relied on and taken individually. (Owner ruling, 2026-11-06.) | [§11](#11-seven-binaries-versioned-together), [§5.11](#511-where-the-burden-of-proof-sits) |
| OQ-UT5 | The system name is `tillr`. The old public project is wiped and re-founded fresh — the name is taken, the baggage is not — and the one-time cost is accepted. The rename happens in this pass, with existing stores migrated by the system. (Owner ruling, 2026-11-06.) | [§6.4](#64-the-name-is-tillr), [§7](#7-the-rename-now-is-the-cheapest-moment) |
| OQ-UT6 | Questions are not board state. Open questions live in design documents, in Vantage's world; an item points at its design document and reads its open questions from there — one copy, so nothing drifts. Decisions and mail fold into `item`; there is no eighth utility to consider for questions. If reading questions from documents proves awkward, the follow-up is a Vantage feature request, with `vantage-check`'s linter the existing tool. (Owner ruling, 2026-11-06.) | [§4](#4-the-proposed-suite-seven-utilities), [§5.3](#53-item-alone) |
| OQ-UT7 | The per-module budgets stand as the acceptance bar. The conversion cost is genuinely unknown until the first heavy module migrates and is measured; until then the gate target is arithmetic, not fact. (Owner ruling, 2026-11-06.) | [§9](#9-testability-and-testing-speed), [§10](#10-landing-velocity-the-per-module-gate) |
| OQ-UT8 | Hybrid, without ceremony: the canonical form is `tillr <noun>` and the bare noun is correct in context. Utility nouns are independent identities; a later standalone name is a publishing decision made then, not a ladder specified now. (Owner ruling, 2026-11-06.) | [§6.3](#63-the-model-hybrid-without-ceremony) |
| OQ-UT9 | The record and table shapes are designed for the boundary, not inherited from the current store, and compatibility with today's column names is not a design constraint. The opening registry inventory finds what exists so the migration can map the old shapes onto the logical contract; it is an inventory of what to move, not a specification of what to keep. (Owner ruling, 2026-11-06.) | [§5.1](#51-the-common-contract), [§8](#8-the-shared-substrate), [§12](#12-the-migration-path) |

## 15. Open questions

None. All nine open questions are settled and compacted into the Decision Ledger ([§14](#14-decision-ledger)) under their original IDs.
