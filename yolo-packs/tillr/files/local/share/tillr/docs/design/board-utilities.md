---
title: "tillr utilities: seven utilities over one shared substrate"
status: accepted
stage: BUILT
next: "None."
tags: [tillr, utilities, gate, testability, landing-velocity, naming, boundary]
summary: "Seven utilities over one shared substrate — store, item, land, drain, run, gate, view — each its own binary, versioned together as one release set, and each required to run alone against a fixture store or a record, because the module and state boundaries buy per-module gate selection, contained blast radius, and a testable surface, and the separate binaries let a project take only the utilities it wants."
---

# tillr utilities: seven utilities over one shared substrate

> **Delivered copy.** This is `docs/design/board-utilities.md` from this repository at commit `5fabdf36fe` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/board-utilities.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Accepted. The seven binaries exist under `tools/tillr-store`, `tools/tillr-item`, `tools/tillr-land`, `tools/tillr-drain`, `tools/tillr-run`, `tools/tillr-gate` and `tools/tillr-view`, with the shared substrate in `tools/tillr` and the wrapper in `tools/tillr-cli`. UNMEASURED: the per-module time budgets of [§9](#9-testability-and-testing-speed) are the acceptance bar and no measurement of them is recorded here. Utilities outside the seven (`tillr-watch`, `tillr-loop`, `tillr-roll`, `tillr-trace`) are described in [tillr-utility-contracts.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts.md), not here.

**In short**

> Queueing work, running it, gating it and landing it are roughly seven jobs. If they share one process, one command registry, one Go module, and one test suite, every job pays every other job's cost. Draw the seams where state changes hands, give each piece of state exactly one writer, put a documented record on every boundary, require each utility to do useful work alone against a fixture store or a record, and make each utility its own module with its own gate selection — and the same cut that makes a utility provable in isolation also makes a typical change cost one small suite, confines its blast radius to one program face, and lets new capability arrive as a new utility instead of a new flag on a large one.

**Why it matters:** When the whole system is one Go module, *any* change selects every test — that single fact produces a long gate, a suite that cannot be tested in isolation, and an unbounded blast radius at once. Separate modules address the shared cause, provided the seams are state seams, each utility can be used without invoking the others, and the gate selection follows the modules.

**The shape:** Seven utilities over one shared substrate, each a module the gate can select independently and each shipped as its own binary. `store` owns the schema and event log; `item` owns items, criteria, edges, and decision and mail records, and carries a reference to the design document an item's open questions are read from; `land` owns the candidate lifecycle and the single landing transaction; `drain` owns dispatch; `run` owns lanes and runs; `gate` owns gate plans and verdicts; `view` owns nothing and renders everything read-only. Config is deliberately not a utility ([§3.4](#34-the-four-question-test)). The system is `tillr`: the canonical invocation is `tillr store`, `tillr item`, and so on, dispatched by a thin wrapper to the individual binaries ([§6](#6-the-naming-model-one-prefix-independent-names-or-a-hybrid), [§11](#11-seven-binaries-versioned-together)).

**The boundary test:** A boundary is real only if a person or agent can run that one utility's own binary against a fixture store, a record on stdin, or both — without any other utility's binary present — and get a useful answer with a documented exit code. [§5](#5-independent-use-the-boundary-made-concrete) specifies that use for every utility. A utility that cannot pass it has a code seam, not a state seam, and must be merged or redrawn under the four-question test.

**Cost:** A versioned record format maintained forever, drift between modules, narrowed cross-domain atomicity, fixture and dry-run faces maintained as real product surface, seven artifacts to build and publish with the version skew that separation makes possible, and an honest limit — a split that only moves code leaves the tests exactly as heavy, and the worst case still pays the heavy module's own suite. What shrinks the suite is the ownership the split forces: duplicated coverage collapses into the owning module's one test, and the survivors stop standing up the whole program. [§3](#3-why-not-more-the-split-curve) and [§13](#13-what-this-costs-and-what-it-forecloses) price each.

**Start at:** [§3](#3-why-not-more-the-split-curve) for the split curve and the four-question test that produces seven; [§5](#5-independent-use-the-boundary-made-concrete) for the proof that each boundary is independently usable; [§10](#10-landing-velocity-the-per-module-gate) for the velocity argument.

**Reads with:** [tillr-utility-contracts.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-utility-contracts.md) (the utilities' contracts), [landing-principles.md](landing-principles.md) (the landing rulings `land` implements), and [the judge and lifecycle design](tillr-judge-and-lifecycle.md) (who may record a verdict).

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

## 1. The goals, as a set

The design serves five goals. Each has a recognition test: an
observation that shows the goal is met.

| Goal | Statement | Recognition test |
|---|---|---|
| G1 Testability | Each responsibility can be tested with its neighbors, the clock, and the process table replaced by inputs. | A landing test runs without spawning a real drain or waiting real seconds. |
| G2 Landing velocity | A typical change gates in tens of seconds, not tens of minutes. | A one-module change selects one module's suite plus the conformance suite ([§10](#10-landing-velocity-the-per-module-gate)). |
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

The responsibilities, and the state each writes.

| # | Responsibility | Writes | Read by |
|---|---|---|---|
| R1 | Store (schema, migrations, event log) | The database itself | Everything |
| R2 | Items (the work records) | Item state | Drain, view, land |
| R3 | Landing eligibility | Whether a judged head may land | Landing pass, view |
| R4 | Landing pass | Landing records, the trunk ref | View, drain |
| R5 | Drain (dispatch and supervision loop) | Dispatch decisions, run launches | View |
| R6 | Gate (plan and run checks) | Gate plans and verdicts | Landing pass |
| R7 | Lanes and runs | Lane and run state | Drain, view |
| R8 | Reporting | Nothing | Humans and agents |
| R9 | Decisions and mail | Decision records, deliveries | Items, agents, the owner |
| R10 | Config | Nothing at runtime (loaded, not written) | Everything |
| R11 | Serve (long-running read/serving faces) | Nothing of its own | Clients of R8 |

Open questions do not appear in this table, deliberately. They are not a
responsibility of the store: they live in the design documents, in Vantage's world,
and an item points at its design document and reads that document's open
questions from it ([§4](#4-the-proposed-suite-seven-utilities),
[§5.3](#53-item-alone)). The store holds the reference and nothing else, so
there is one copy of every question and the two cannot drift.

Three seams announce themselves:

- **R8 writes nothing.** A reader with no state cannot corrupt anything, and
  it proves the record contract from the safe side.
- **R6 needs no store.** Checking a tree against a registered set of checks
  takes a change description and a worktree, so the gate boundary exists by
  the nature of the job.
- **R3 and R4 are one lifecycle.** There is one landing implementation and
  one writer for landing state.

Equally, candidate seams fail on inspection, and [§3](#3-why-not-more-the-split-curve) makes the
failure precise: R3 and R4 are one lifecycle with two writers if split (the pass
would write its result back into state the eligibility check owns), R9's
decision and mail records are item-adjacent rather than a lifecycle of their
own, and questions are not stored state at all, so there is no question seam
to draw.

## 3. Why not more: the split curve

More splits make tests faster and changes better isolated up to a point, and
then the effect inverts. The count in [§4](#4-the-proposed-suite-seven-utilities)
is an output of this curve, not a target.

### 3.1 What more splits buy

A smaller suite selected for a *typical* change. That is real, and it is the
velocity win of [§10](#10-landing-velocity-the-per-module-gate): in a single module every change selects
`go test ./...` over the whole module; with one module per utility, a change to one
module selects that module's fast suite (budgeted 20–40s) plus the
conformance suite (budgeted in seconds). Isolation improves in the same step:
a module that owns its state can be broken only through its records.

### 3.2 What more splits do not buy

- **A split by itself only moves tests; the ownership that makes a real
  split possible is what shrinks them.** A suite dragged across a boundary
  intact is exactly as heavy. What pares the suite down is the ownership the
  boundary forces. Where several entry points can reach one behavior and no
  module can say which one owns it, the behavior is covered several times;
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
  utilities to write two halves of one lifecycle — the R3/R4 defect
  the merge in [§4](#4-the-proposed-suite-seven-utilities) avoids. Each such pair reintroduces cross-module
  atomicity questions the substrate was meant to settle ([§13](#13-what-this-costs-and-what-it-forecloses)).
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

Question 3 is sharp: "test alone" means the utility also
has a *useful standalone invocation*, specified in [§5](#5-independent-use-the-boundary-made-concrete),
not merely a unit-test harness that links its packages. A boundary only a
test can exercise is not yet a boundary a user or agent can use.

Applied to the contested cases:

| Candidate split | Own state? | Carry? | Test alone? | Select alone? | Verdict |
|---|---|---|---|---|---|
| Landing eligibility apart from landing pass | No — the pass writes the landing result the eligibility check reads | Yes | Barely — each needs the other's fixtures | No — lifecycle changes land on both | **One utility** (`land`) |
| Decisions/questions apart from items | Decisions and mail are item-adjacent records; questions own no stored state at all — they live in design documents | Decisions yes; questions are read from the document an item points at, not carried as stored records | Decisions yes; questions n/a | Decisions unclear; questions n/a | **Decisions and mail fold into `item`; questions are not stored state**, so there is no eighth utility to consider for them |
| Gate planner inside the lander | Yes, but it would grade its own homework: the selector must select itself | Yes | Yes | No | **Keep `gate` separate** |
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

| Utility | Owns (sole writer) | Responsibilities |
|---|---|---|
| `store` | Schema, migrations, the event log, store administration | R1, the administrative face of R10 |
| `item` | Item state; decision and mail records; each item's `design_doc` reference | R2, R9 |
| `land` | Landing records and the trunk ref | R3 + R4 |
| `drain` | Dispatch decisions and the supervision loop | R5 |
| `run` | Lane and run state | R7 |
| `gate` | Gate plans and verdicts; the check registry | R6 |
| `view` | Nothing — all reporting and serving faces | R8, R11 |

Notes on the three judgment calls:

- **`land` is one transaction with one writer.** Deciding that a judged head
  may land and moving the trunk are one lifecycle. Split, that is two
  writers on one lifecycle and a cross-boundary transaction on the system's
  most safety-critical path. Merged, it is one landing transaction inside one
  module: a landing names the item and the run it is for, and there is no
  queue between the gate and the trunk.
- **`item` carries decisions and mail; it does not carry questions.**
  Decisions attach to items and mail is how their records are delivered, not
  a state of its own, so both fold into `item`. Questions are different in
  kind: open questions live in the design documents, in Vantage's world,
  and the store does not hold, mirror, or answer them. An item carries a
  `design_doc` reference, and its open questions are read from that document
  when they are surfaced ([§5.3](#53-item-alone)). There is therefore no
  eighth utility to consider for questions — the question dissolves rather
  than being answered "no," because there is no stored state for such a
  utility to own.
- **`gate` stays separate on principle, not size.** The gate planner decides
  which checks a landing must pass, including checks over the planner itself
  (the selector must select itself). A planner inside the lander
  grades its own homework: a lander bug could choose a plan that never
  examines the lander. Whatever the size arithmetic says, this boundary
  stays.

A command belongs to the utility that owns the state it mutates; a command
that mutates nothing belongs to `view`.

## 5. Independent use: the boundary made concrete

This section is the proof obligation for every boundary. For each
utility it gives a standalone invocation, the records consumed and emitted,
the store tables touched, and a scenario in which the utility is useful with
no other utility face invoked. If an implementer cannot build the fixture and
the input record from this section, the section has failed; if the
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
a project can take three of these and
leave the other four behind. That requires every binary to install and run
alone ([§5.11](#511-where-the-burden-of-proof-sits)); it does not require each
utility's records to become generic, and `gate` has the
strongest claim to usefulness wholly outside this system, because its
primary mode needs no store. All seven must be independently *usable* and
independently *installable*; each earns a life beyond the substrate
contract it needs only on evidence ([§6.3](#63-the-model-hybrid-without-ceremony)).

### 5.1 The common contract

Every record carries a common envelope (`Envelope` in `tools/tillr/record`).

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
| `land` | `landings`; external write: the trunk git ref. It also declares `candidates` and `landing_attempts`, which nothing writes and only readers open | `items` (as records), its own tables, `events` |
| `drain` | `dispatches` | `items`, `lanes`, `runs`, its own table, `events` |
| `run` | `lanes`, `runs`; external writes: processes and worktrees under a caller-named lane root | Its own tables, `meta` |
| `gate` | Nothing: its plans and verdicts are records on stdout or in files | The check registry, the change record |
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

**Scenario:** every other utility's test suite starts here, with a throwaway
store built from seed records and no live store at risk. `check` and `events`
against a copied store answer "what schema is this and what happened in
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
[the judge and lifecycle design](tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow), are:

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
When a store is opened for migration, the item utility's registered migration types
rows with that prose as `owner-only`; repeated openings leave their
classes unchanged. The loop reads the stored class, records mechanical
verdicts only for `verification-only`, and prints each criterion left for
another authority without treating it as a failure.

Questions are not among these faces, because they are not stored state.
`show` surfaces them instead: where the item carries a `design_doc`
reference, the output includes that document's open questions — their
identifiers and text as the document itself declares them, with the
document named as their source — read from the document at show time. The
store never holds, mirrors, or answers them, so the document and the item
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

If reading questions from documents proves awkward, the remedy is a change
to Vantage, not a stored copy of the questions.

### 5.4 `land` alone

**Does alone:** lands one judged head onto the trunk, or refuses, driven by
records rather than by live counter-parties.

```
tillr land pass --store $FIXTURE/store.sqlite3 --repo $FIXTURE/repo --item item_042 --run run_019 --base <base-sha> --head <head-sha> --verdict verdict-record.json
tillr land one  --store $FIXTURE/store.sqlite3 --repo $FIXTURE/repo --branch <branch> --gate <gate-program>
```

`pass` is the landing transaction with nothing in front of it. Its crucial
input is a `gate.verdict` **record** ([§5.7](#57-gate-alone)): a file, not a
running gate. `land` verifies that the verdict binds the candidate's
`base_sha` and the tree of its `head_sha`; a verdict that binds any other
tree is a refusal (`verdict-mismatch`), never a re-plan. A trunk that has
moved since `base_sha` is carried, not refused, per
[`landing-principles.md`](landing-principles.md) [Section 3](landing-principles.md#3-gate-what-will-be-the-tip-once):
`pass` three-way merges `base_sha..head_sha` onto the trunk's current tip under
the land lock and lands a clean result as one commit, with no second gate. A
merge that conflicts lands nothing; its repair is a new candidate, gated
fresh, because the tree a repair produces is a tree no verdict has seen.

Both faces emit a `land.landing` record, which is also the event appended in
the same write as the `landings` row: `landing_id`, `item_id`, `run_id`,
`verdict_id`, `result` (`landed` / `squash-conflict` / `refused`), `reason`
(a registered string such as `verdict-mismatch`, `verdict-failed` or
`trunk-rewritten`), `base_sha`, `head_sha`, `trunk_before`, `trunk_after`,
`gated_tree`, `landed_tree` and `conflict_paths`. `gated_tree` differing from
`landed_tree` is the normal outcome of a carried landing. A refusal exits 1
and moves nothing.

`one` is the face for a person: it gates the named branch with the gate
program it is handed and lands it through the same transaction.

There is no candidate queue and no dry-run face: `rg -n 'case "' tools/tillr-land/main.go`
shows `pass` and `one` only.

**Requires:** a store, a git repository, and a verdict record (or, for `one`,
a gate program). It does not require the drain or a live gate process, which
is what makes hand-landing ([§5.10](#510-landing-a-candidate-by-hand-without-the-drain)) possible.

**Scenario:** landing by hand with the drain stopped, and reading why a
landing was refused from the `land.landing` record alone.

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
stored question state, because there is none. `plan` writes nothing.
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

**Scenario:** "why won't this item dispatch?" becomes one command against a
seeded fixture: `drain plan` and read the `reason`. The same face is a capacity simulator: vary
`available_lanes` and `max_dispatches` and diff the decision records. A
drain that can only answer by being left running against the live store
fails question 3 of [§3.4](#34-the-four-question-test); the `plan`/`once` faces are therefore part
of the utility's definition, not optional extras.

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
([`momentum-principle.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/momentum-principle.md)), so no default wall
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
worktree into a verdict. It needs no store.

```
tillr gate plan --registry <checks> --input change-record.json --output plan-record.json
tillr gate run  --input plan-record.json --worktree $FIXTURE/worktree --output verdict-record.json
```

`plan` consumes a `gate.change`: `base_sha`, `tree_sha`, `changed_paths`,
and the `subject`. It emits a `gate.plan`:
`plan_id`, `base_sha`, `tree_sha`, `registry_digest` (the digest of the
check registry that produced the plan), and `checks` — each
`{check_id, command (argv array), timeout_ms, reads, writes, depends_on}`.
The `reads`/`writes` declarations let a runner serialize checks
that would collide over one directory. `run` executes a plan in the named
worktree and emits a `gate.verdict`: `verdict_id`, `plan_id`,
`registry_digest`, `base_sha`, `tree_sha`, `result`
(`pass` / `fail` / `error`), and per-check `checks` entries
`{check_id, result, duration_ms, log_ref}`. Exit 0 on `pass`, 1 on `fail`
(a completed negative verdict), 4 on `error` (a check could not be run,
never to be read as a failure of the candidate). The records on stdout or in
files are the whole output.

**Requires:** a check registry and, for `run`, a worktree. No store and no
lander.

**Scenario:** preflight. An agent or developer runs `gate plan` and `gate
run` against a worktree before a candidate exists and learns whether the
change will gate, in any repository whose checks are registered, whether or
not the rest of this system is installed.

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
points at ([§5.3](#53-item-alone)), its landings, and its runs.
Every component is copied from records other utilities emitted or from the
design document itself; `view` computes presentation, never new state.
Human-readable rendering is the same record through a format flag, not a
second data path. Exit 1 when the subject does not exist in the snapshot,
3 when the store's schema is unsupported.

**Requires:** read access to a store copy or a records file. Nothing else,
and it writes nothing anywhere — it is safe to point at the live store
while writers run, against a consistent snapshot.

**Scenario:** reporting with the system stopped: copy
the store, run `view status` and `view events` over the copy, and the
history of an item is readable without starting any writer. The `--records` mode makes the
same report available to a recipient who was sent records but has no store.

### 5.9 The composed pipeline

In normal operation the drain composes the
seven by carrying records between faces and letting each writer append its
events. No utility calls another in-process; the store and the record
stream are the only couplings.

| Step | Face | Consumes | Emits |
|---|---|---|---|
| 1 | `item apply` | `item.record` (state `ready`) | Stored item; `item.*` events |
| 2 | `drain plan` / `once` | `drain.tick`; item/lane/run state | `drain.decision` carrying a `run.request` |
| 3 | `run exec` | `run.request` | `run.record` with `artifact_refs` including `tree_sha` |
| 4 | `gate plan` | `gate.change` for the run's tree | `gate.plan` |
| 5 | `gate run` | `gate.plan`; the run's worktree | `gate.verdict` |
| 6 | `land pass` | The item, run, base and head; the `gate.verdict` record | `land.landing`; trunk ref moved iff the landing lands |
| 7 | `view status` | Store snapshot or the records from steps 1–6 | `view.status` |

The envelope's `correlation_id` lets step
7 reassemble one pipeline run from the record stream alone. Each step's input is a record a fixture can supply and
each step's output is a record a fixture can assert on, which is why the
fast suites in [§9](#9-testability-and-testing-speed) can replace neighbors with files.

### 5.10 Landing a candidate by hand, without the drain

An agent has a finished candidate
and wants it landed with the drain stopped. Four faces, four separate
invocations, each runnable alone:

```
# 1. Plan the gate for this candidate tree (no store needed).
tillr gate plan --registry <checks> --input candidate-change.json --output plan.json

# 2. Run the plan in the candidate worktree. Exit 0 = pass, 1 = fail, 4 = could not run.
tillr gate run  --input plan.json --worktree <candidate-worktree> --output verdict.json

# 3. Land, presenting the verdict as a record. Refusals exit 1 with a reason and move nothing.
tillr land pass --store <store> --repo <repo> --item item_042 --run run_019 --base <base-sha> --head <head-sha> --verdict verdict.json

# 4. Confirm from a read-only snapshot.
tillr view status --store <store> --item item_042
```

The safety properties live in the records, not in the operator's care: the
verdict binds `plan_id`, `registry_digest`, `base_sha`, and `tree_sha`,
`land pass` refuses a verdict that does not match the candidate, and it
carries the candidate onto a trunk that has moved since `base_sha`
([§5.4](#54-land-alone)). If any step required a running drain, a
live gate process, or an in-process call into another utility, the boundary
it crosses would be a code seam to redraw under
[§3.4](#34-the-four-question-test).

### 5.11 Where the burden of proof sits

The invocations above are the **required standalone faces of the
utilities**, stated concretely enough to build fixtures against. The
acceptance condition, in its strongest form: **the utility's own binary,
installed alone, runs its standalone faces green against a fixture store
and/or records, with no other utility's binary present.** A utility that
cannot meet that condition has a seam that goes back through
[§3.4](#34-the-four-question-test).

## 6. The naming model: one prefix, independent names, or a hybrid

### 6.1 What is settled: long names, no letter prefix

A one-letter prefix scheme is rejected. A bare letter carries no meaning,
cannot be said aloud in a brief, and is not the Unix convention, which is a
word prefix (`git-commit`). The deciding fact is that these utilities are
invoked by agents: a name is read in a tool call, a log line, an error
message, a gate plan and a document, and is rarely typed. The cost of a long
name barely exists and the benefit, a log line that explains itself, is paid
on every invocation. Every utility's name says what state it owns or what
loop it runs: `store`, `item`, `land`, `drain`, `run`, `gate`, `view`.

### 6.2 The three models

| | One prefix | Independent names | Hybrid |
|---|---|---|---|
| **Shape** | `tillr-store`, `tillr-item`, … | Each utility has its own unrelated name | `tillr <noun>` canonical over `tillr-<noun>` binaries; the bare noun correct in context |
| **What a name tells an agent reading a log** | The job and the family, every time | The job only | The job always; the family wherever the canonical form is used, and in every record regardless |
| **PATH collisions** | None of consequence | Real: bare `gate`, `run`, `view` are generic words other tools also want | Avoided: on-PATH names are `tillr` and `tillr-<noun>` |
| **Packaging and versioning** | One distribution, one version | Tends toward seven version lines and skew nothing polices | Seven binaries released together as one version set ([§11](#11-seven-binaries-versioned-together)) |
| **Cost of renaming the system** | Every name changes at once | Only the umbrella changes | The umbrella and the binary prefixes change; the utility nouns stand |
| **Failure mode** | The prefix becomes empty ceremony in every log line | An agent holding a `gate` verdict cannot tell whose `land` should consume it | Two ways to refer to a utility, contained by the rule in [§6.3](#63-the-model-hybrid-without-ceremony) |

Provenance does not require a prefix: the record envelope's `producer` field
([§5.1](#51-the-common-contract)) carries it under any model. Independent
runnability is delivered by the separate binaries, so independent brand names
would add nothing to it.

### 6.3 The model: hybrid, without ceremony

**The canonical form is `tillr <noun>`; the bare noun is correct in
context**, as `git rebase` is called "rebase" in a Git context.

- Each utility's **identity** is its descriptive noun: in module names,
  record kinds, gate step names, documents, and the `producer.utility` field.
  It does not change if the system is renamed.
- The **distribution** is a release train of seven binaries,
  `tillr-store` through `tillr-view`, versioned together as one set
  ([§11](#11-seven-binaries-versioned-together)). The thin `tillr`
  wrapper dispatches `tillr <noun> ...` to `tillr-<noun>`; each binary also
  runs its faces directly.
- The family relationship is carried by the `tillr` name, the documents, and
  `producer.system` in every record.

Documents, records and commands use the canonical form; the bare noun is for
speech and in-context reference only. A fully standalone name for one utility
is a publishing decision made on evidence when it arises; no ladder for
earning one is specified.

### 6.4 The name is `tillr`

The system name is **`tillr`**: a tiller is what steers the boat.

## 7. Where the name appears

`tillr` is the system name everywhere it appears: the wrapper, the seven
`tillr-<noun>` binaries, the store and config paths, and the
`producer.system` value in every record. The utility nouns underneath it
are independent identities ([§6.3](#63-the-model-hybrid-without-ceremony))
and would survive a future system rename.

## 8. The shared substrate

One SQLite database, partitioned by writer. Each utility writes only the
tables it owns ([§4](#4-the-proposed-suite-seven-utilities), [§5.1](#51-the-common-contract)); any utility may read any table, but reads
cross boundaries as records, so a reader depends on the record contract, not
on another module's schema choices. Nothing yet pays for several databases
— one file to back up, one event log with one order — and the record
contract does not foreclose them later if a demonstrated contention or
backup-granularity need ever argues for paying.

The record and table shapes on this substrate are designed for the boundary
([§5.1](#51-the-common-contract)).

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
  The suite lives in `tools/tillr/conformance`. The standalone invocations of
  [§5](#5-independent-use-the-boundary-made-concrete) are driven by each
  utility's own suite against a fixture store.
- **Substrate changes are the one legitimately wide blast radius.** A schema
  or record change selects every module's suite, takes migration through
  `store`, and is scheduled as such — rarely, deliberately, and never
  smuggled inside a one-module change.

## 9. Testability and testing speed

Ownership buys two reductions ([§3.2](#32-what-more-splits-do-not-buy)).
**Fewer tests:** once one writer owns a piece of state, a behavior is covered
by one test in the owning module instead of once per entry point. **Cheaper
tests:** a test inside a boundary takes its neighbors, the clock and
process-spawning as inputs (fixture stores, input records, an injected
clock) instead of standing up the whole program. A test that needs a landed
candidate consumes a `land.landing` record; it does not run a landing. The
standalone faces in [§5](#5-independent-use-the-boundary-made-concrete) are
the surfaces those tests drive: `drain plan` with an injected `now` replaces
a test that sleeps, `land pass` against a verdict file replaces a test that
runs a gate, and `run exec` with a stub command replaces a test that
launches an agent.

The worst case still pays a heavy module's own suite, and a substrate change
still selects everything.

Budgets, per utility. They are the acceptance bar:

| Tier | Budget | Selected |
|---|---|---|
| Core loop (the tests a developer runs per edit) | 5s | By hand, constantly |
| Fast suite (one module, all of it) | 20–40s | Per change to that module |
| Conformance suite (contract + [§5](#5-independent-use-the-boundary-made-concrete) standalone cases) | Seconds | Always |
| Integration / pipeline tier (utilities composed, [§5.9](#59-the-composed-pipeline)) | Unbounded, but honest about it | Least often: substrate changes, scheduled runs, pre-release |

A module that cannot meet its fast-suite budget has a testability defect,
not a budget negotiation. The remedy is deleting duplicated coverage
as ownership becomes clear and converting spawn-and-sleep
tests to fixture-and-record tests.

## 10. Landing velocity: the per-module gate

The gate selects Go tests by module: its affected-tests step
(`tools/gate/go_affected.py`) runs the test packages a change affects, so a
change to one module does not pay for every test.

Two different wins are stacked in that selection. **Selection**: the
typical change runs *less of the suite* — one module's fast suite instead
of every test. **Shrinkage**: the suite itself is smaller and each test
cheaper, because ownership removes duplicated coverage and takes
whole-program setup out of the tests
([§9](#9-testability-and-testing-speed)). The budget for the typical
one-module change is one fast suite (20–40s) plus conformance (seconds).
That figure is a budget derived from [§9](#9-testability-and-testing-speed),
not a measurement. And per [§3.2](#32-what-more-splits-do-not-buy), the
worst case — a substrate change — still selects everything, by design.
Velocity is bought for the median change; the wide change stays wide, and
knows why.

### 10.1 Parallel gating of non-overlapping candidates

Two candidates that touch disjoint files are not gated in parallel for
their final verdicts.

**Parallel final verdicts are not landable.** Every verdict binds the exact
tree it examined, and `land pass` refuses a verdict whose tree is not the
candidate's ([§5.4](#54-land-alone)). Gate two candidates in parallel against
one trunk and the first one lands: the trunk moves, and the second verdict is
about a tree that is not the tree landing would produce. Carrying prices one
ungated re-application under a serial gate; parallel gating would multiply
ungated combinations. Disjoint files do not repair this, because textual
disjointness is decidable from `changed_paths` and semantic independence is
not: one candidate changes a function's signature, the other adds a caller in
a file the first never touches, each passes alone, and the combination does
not compile. The only sound parallel form is speculative gating, where each
candidate is gated against a predicted trunk made of the candidates ahead of
it and every downstream verdict is discarded when an earlier candidate fails.
This design does not adopt it.

**The per-module gate removes the pressure first.** The queue's wait is queue
depth times the serial gate wall, and per-module selection shortens that wall
for the typical change. The gate is `go test`, which is CPU-bound, so on a
machine one gate saturates, two concurrent full gates buy no throughput.
Whether one gate saturates every core is unmeasured. The parallelism that is
safe is preflight: `gate plan` and `gate run` in a lane before a candidate
exists ([§5.7](#57-gate-alone)) produce advisory verdicts that claim nothing
landable, so any number of lanes may run them concurrently.

**Final gating stays serial, one candidate, one verdict bound; parallelism
lives in preflight.** This reopens only on two measurements together: queue
wait, not gate wall, shown to be the routine bottleneck at real queue depths,
and a single selected gate shown to leave cores idle.

## 11. Seven binaries, versioned together

Each utility is its own binary (`tillr-store`, `tillr-item`, `tillr-land`,
`tillr-drain`, `tillr-run`, `tillr-gate`, `tillr-view`) with its own entry
point, documents and module, released together as one version set sharing a
single version. A thin `tillr` wrapper (`tools/tillr-cli`) dispatches
`tillr gate ...` to `tillr-gate ...` as `git` dispatches to `git-rebase`. The
wrapper is a convenience; the binaries are the product.

The reason is **adoption**. A project can take three of the seven and leave
the other four behind. A multicall binary would ship all seven faces to
anyone who wants one and enforce the boundaries only by package structure.
With separate binaries, what a project installs is what it uses, and a
boundary a neighboring utility is not present to reach across cannot be
reached across. The binaries are built one per module, so the module boundary
(per-module gate selection, blast radius, test surface) and the binary
boundary coincide.

The costs:

- **Version skew is possible.** The seven are versioned and released as one
  set, records carry `producer.version` ([§5.1](#51-the-common-contract)),
  and consumers refuse unsupported record and schema versions (exit 3)
  instead of guessing.
- **Seven artifacts to build and publish**, and a release that is not done
  until the whole set is.
- **The wrapper must stay a dispatcher.** Its job is to find `tillr-<noun>`
  and hand over argv, stdin, stdout and the exit code untouched. The one
  built-in it hosts is `tillr notify serve`, a local wake-hint relay.

## 12. One writer at every instant

**Writes never dual-run**: at every instant exactly one code
path writes each slice of state. Reads may dual-run freely — that is what
makes `view` safe to point at a store while writers run. The `tillr`
wrapper dispatches each noun to its binary, so callers see one interface.
Every utility meets the acceptance condition of
[§5.11](#511-where-the-burden-of-proof-sits): its own binary, installed
alone, runs its standalone invocations green against a fixture store, with
no other utility's binary present.

## 13. What this costs and what it forecloses

- **A record format, forever.** Boundaries that were function calls become
  versioned contracts with compatibility obligations. Changing a record is
  a substrate change ([§8](#8-the-shared-substrate)), priced accordingly.
- **Drift.** Seven modules can re-implement the same helper seven ways. The
  substrate must be the obvious home for shared code, or the suite
  re-monoliths by copy-paste.
- **Narrowed cross-domain atomicity.** Transactions that span two writers'
  state stop being available by construction. R3+R4 are merged precisely
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
- **Standalone faces are product surface.** `drain plan`,
  fixture seeding, and the store-free gate ([§5](#5-independent-use-the-boundary-made-concrete)) must be maintained,
  documented, and kept honest against the live paths they mirror. A dry-run
  face that drifts from the real decision logic is worse than none: it
  produces confident wrong answers. The conformance suite must drive both
  from the same code path wherever the face claims equivalence.
- **Two reference forms** under the hybrid naming model
  ([§6.3](#63-the-model-hybrid-without-ceremony)), and **seven binaries that
  can skew** ([§11](#11-seven-binaries-versioned-together)).

### 13.1 What a second consumer would reuse, and what it would not

A second system with a similar loop (generate work, prioritize it, fan it
out to agents, generate more work from the results) needs no seam here to
move. The two candidate changes both fail the four-question test
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
  writes nothing, but what it decides is this system's policy: `blocked_by`
  edges, open questions read from an item's design document, lanes,
  capacity, and whatever `policy_ref` names. Owning that decision is why
  `drain` exists. Execution is already separated from it: `drain` emits a
  `run.request`, and `run` knows the command, the lane, and the timeout
  but not why this work was chosen next ([§5.6](#56-run-alone)). Carving a
  policy-free scheduling kernel out of `drain` would move this system's decisions
  into the substrate — the one wide blast radius
  ([§8](#8-the-shared-substrate)) — to serve a second system whose
  priority rules are not written down anywhere yet. A second system either
  casts its work as items and supplies its own `policy_ref`, or it writes
  its own dispatch utility over the same pattern. Both are available
  without touching this seam.

What transfers, and what is specific to this system by design:

| Layer | What a second consumer reuses | What it does not reuse |
|---|---|---|
| Substrate | The mechanisms as libraries: the record envelope and versioning ([§5.1](#51-the-common-contract)), the exit-code convention, event append, fixture stores, the config loader, and the conformance pattern. | This system's schema, event kinds, and payload fields. Those are this system's state, not the mechanism's. |
| `gate` | The no-store `plan`/`run` faces, where the job really is checking a tree against a registered set of checks — the one present claim to independent adoptability ([§5](#5-independent-use-the-boundary-made-concrete)). The `tillr-gate` binary itself, installed alone. | A `gate.verdict` as a general-purpose evaluation of results. A verdict binds a plan, a registry digest, and a tree so that `land` can trust it ([§5.4](#54-land-alone)); an exploratory result that binds no tree is a `run.record`, not a verdict, and treating it as one would strip the verdict of the property that makes landing safe. |
| `run` | The lane and process lifecycle shape: an argv array, a lane, a timeout, artifacts back, and a run's failure as data rather than the utility's. | The record as named here — its subject is an item and its headline artifact is a candidate tree. |
| `item`, `drain`, `land`, `view` | The pattern, and — where a second system's work genuinely is items, dispatch, landing, or reporting over the same records — the binaries themselves, taken individually ([§11](#11-seven-binaries-versioned-together)). | The content specific to this system: frozen criteria, `blocked_by` edges, and `design_doc` references; the dispatch reasons and policy; the candidate lifecycle and the trunk transaction, which have no analogue outside landing code; and `view`'s assembled subject, which is an item of this system. |

A second system is a second set of utilities over the substrate mechanisms
and this pattern, and it can take individual binaries and leave the rest.
Needing new tables, payloads, event kinds or utilities in that system is the
design working as intended. What would reopen this subsection is finding that
the substrate libraries themselves (the envelope, the append primitive,
fixture seeding) cannot express a second system's records without forking
them ([§3.5](#35-the-conclusion)).

## 14. Decision ledger

`D-UT16` states the claim authority rule; which agent may judge is governed by
[the workflow rulebook](tillr-judge-and-lifecycle.md#rulebook-and-its-enforcement).

| ID | Decision | Grounds |
|---|---|---|
| D-UT1 | The system is a set of utilities over one shared SQLite substrate, partitioned by writer. | G1–G5; [§2](#2-the-seam-analysis) |
| D-UT2 | Seven utilities: store, item, land, drain, run, gate, view. The count is an output of the state seams via the four-question test, not a target. | [§3.4](#34-the-four-question-test) |
| D-UT3 | Landing eligibility and the landing pass are one utility with one writer for landing state, in a single implementation. | [§2](#2-the-seam-analysis), [§3.4](#34-the-four-question-test) |
| D-UT4 | Decisions and mail fold into `item`. Questions are not stored state: they live in design documents, in Vantage's world; an item carries a `design_doc` reference and its open questions are read from that document when surfaced, never stored or mirrored. There is therefore no eighth utility to consider for questions. | [§3.4](#34-the-four-question-test), [§4](#4-the-proposed-suite-seven-utilities), [§5.3](#53-item-alone) |
| D-UT5 | The gate planner remains a separate utility; the selector must select itself and must not live inside the lander it examines. | [§4](#4-the-proposed-suite-seven-utilities) |
| D-UT6 | Utility identities are long descriptive nouns naming the state owned or loop run, with no letter prefix. Agents, not humans, are the callers. How identities become command names is the naming model, D-UT12. | [§6.1](#61-what-is-settled-long-names-no-letter-prefix) |
| D-UT7 | Seven separate binaries — `tillr-store` through `tillr-view` — each its own entry point, docs, and package, released together as one version set. A thin `tillr` wrapper dispatches to them, Git-style, as a convenience control surface; the binaries are the product, so a project can take three and leave four. Version skew is policed by the release train and the conformance suite; the wrapper holds no behavior of its own. | [§11](#11-seven-binaries-versioned-together) |
| D-UT8 | The boundary contract — record format, schema package, event-kind registry, exit codes — is held by an always-selected conformance suite (`tools/tillr/conformance`); substrate changes select every suite. | [§5.1](#51-the-common-contract), [§8](#8-the-shared-substrate), [§10](#10-landing-velocity-the-per-module-gate) |
| D-UT9 | Writes never dual-run: at every instant exactly one code path writes each slice of state. The `tillr` wrapper only dispatches. | [§12](#12-one-writer-at-every-instant) |
| D-UT10 | The system name is `tillr` wherever a name is user-visible: the wrapper, the binaries, the store and config paths, and `producer.system` in every record. | [§7](#7-where-the-name-appears) |
| D-UT11 | Independent use is an acceptance condition for every utility, in its strongest form: each utility's own binary, installed alone, runs its standalone faces ([§5](#5-independent-use-the-boundary-made-concrete)) green against a fixture store and/or records, with no other utility's binary present. A utility that fails is redrawn under [§3.4](#34-the-four-question-test). | [§5](#5-independent-use-the-boundary-made-concrete), [§5.11](#511-where-the-burden-of-proof-sits) |
| D-UT12 | Naming model, hybrid without ceremony: the canonical form is `tillr <noun>`, the bare noun is correct in context, and the utility nouns are independent identities that do not change if the system is renamed. The distribution is a release train of seven `tillr-<noun>` binaries, not one artifact. A fully standalone name for a utility is a publishing decision made on evidence when it arises; no ladder is specified. | [§6.2](#62-the-three-models), [§6.3](#63-the-model-hybrid-without-ceremony) |
| D-UT13 | The test win is stated as two wins: per-module *selection* (a typical change runs less of the suite) and *shrinkage* (ownership removes duplicated coverage and whole-program setup from tests). A split that only moves tests buys neither, and the worst case still pays the heavy module's own suite. | [§3.2](#32-what-more-splits-do-not-buy), [§9](#9-testability-and-testing-speed), [§10](#10-landing-velocity-the-per-module-gate) |
| D-UT14 | Final gating remains serial — one candidate, one verdict bound to its tree. Parallel gates against one trunk produce at most one landable verdict, and textual non-overlap does not prove semantic independence; parallelism is spent on advisory preflight instead. Speculative gating reopens only on the measurements named in [§10.1](#101-parallel-gating-of-non-overlapping-candidates). | [§5.4](#54-land-alone), [§5.10](#510-landing-a-candidate-by-hand-without-the-drain), [§10.1](#101-parallel-gating-of-non-overlapping-candidates) |
| D-UT15 | No seam moves for a hypothetical second consumer. Reuse is the substrate mechanisms, the record contract, and whichever individual binaries — `tillr-gate` first — a second system takes on their own; a work producer is a client of `item` and a reader of the event log, not an eighth utility, and `drain`'s policy stays this system's policy with execution already separated in `run`. A second system otherwise builds its own utilities; this reopens only on evidence that the substrate libraries themselves must fork, not on that system needing its own tables, records, or utilities. | [§3.4](#34-the-four-question-test), [§5.3](#53-item-alone), [§5.5](#55-drain-alone), [§5.7](#57-gate-alone), [§8](#8-the-shared-substrate), [§13.1](#131-what-a-second-consumer-would-reuse-and-what-it-would-not) |
| D-UT16 | Criterion state moves only on an `item.verdict` naming the frozen criterion text's hash, and frozen criterion text cannot be rewritten through `item apply` (append only). Stored `item_criteria.claim_class` defaults to `verification-only` and can only harden. A mechanical verdict from a landed attempt retires only `verification-only`; an agent-issued verdict counts only from an independent actor that is the selected workflow's evaluator, for a stored class its `judge_classes` lists, never `owner-only`; a human alone judges `owner-only`. Verdict classes must match the stored class. The actor table and named refusals are in the governing section. (Designer's decision, 2026-11-12; the owner may overturn it.) | [§5.3](#53-item-alone) |
| D-UT17 | A lane lease is set by `run exec` at claim for the run's `timeout_ms` plus a 300,000 ms grace, on the store host's clock; `timeout_ms: 0` is an unbounded run whose lease carries `leased_until = NULL` and which no clock reaps (`D-RW1` of [`run-wall-ruling.md`](../reference/run-wall-ruling.md)); the terminal record releases it, `drain once` reaps an expired lease before planning, and `run release` is the standalone release face. A correction retry keeps the item's lane and worktree under a new lease. (Designer's decision, 2026-11-12; the owner may overturn it.) | [§5.5](#55-drain-alone), [§5.6](#56-run-alone); [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md#steering-continuation-and-session-modes) |
| D-UT19 | A run names its item: `run.record` carries `item_id`, copied from the request and materialized on the `runs` row, so every run is traceable to the work it was dispatched for. (Owner ruling, 2026-11-12.) | [§5.6](#56-run-alone), [§5.5](#55-drain-alone) |
| D-UT18 | A gated candidate whose trunk moved is carried: `land pass` re-applies it onto the current tip and lands a clean re-application with no second gate; a conflicting re-application lands nothing and records `squash-conflict`. (Owner ruling, 2026-11-12.) | [§5.4](#54-land-alone), [§5.10](#510-landing-a-candidate-by-hand-without-the-drain); [`landing-principles.md`](landing-principles.md) [Section 3](landing-principles.md#3-gate-what-will-be-the-tip-once) |
| OQ-UT1 | One shared SQLite database, partitioned by writer. Nothing yet pays for several, and the record contract does not foreclose them later. (Owner ruling, 2026-11-06.) | [§8](#8-the-shared-substrate) |
| OQ-UT2 | The design fixes one landing implementation and one writer for landing state. (Owner ruling, 2026-11-06.) | [§2](#2-the-seam-analysis) |
| OQ-UT3 | Gate selection is per module: one selection mechanism, built once. (Owner ruling, 2026-11-06.) | [§10](#10-landing-velocity-the-per-module-gate) |
| OQ-UT4 | Seven separate binaries, versioned together as one release set, with a thin `tillr` wrapper as convenience control surface. The grounds are adoption: each utility can be relied on and taken individually. (Owner ruling, 2026-11-06.) | [§11](#11-seven-binaries-versioned-together), [§5.11](#511-where-the-burden-of-proof-sits) |
| OQ-UT5 | The system name is `tillr`. (Owner ruling, 2026-11-06.) | [§6.4](#64-the-name-is-tillr), [§7](#7-where-the-name-appears) |
| OQ-UT6 | Questions are not stored state. Open questions live in design documents, in Vantage's world; an item points at its design document and reads its open questions from there — one copy, so nothing drifts. Decisions and mail fold into `item`; there is no eighth utility to consider for questions. If reading questions from documents proves awkward, the follow-up is a change to Vantage. (Owner ruling, 2026-11-06.) | [§4](#4-the-proposed-suite-seven-utilities), [§5.3](#53-item-alone) |
| OQ-UT7 | The per-module budgets stand as the acceptance bar. (Owner ruling, 2026-11-06.) | [§9](#9-testability-and-testing-speed), [§10](#10-landing-velocity-the-per-module-gate) |
| OQ-UT8 | Hybrid, without ceremony: the canonical form is `tillr <noun>` and the bare noun is correct in context. Utility nouns are independent identities; a later standalone name is a publishing decision made then, not a ladder specified now. (Owner ruling, 2026-11-06.) | [§6.3](#63-the-model-hybrid-without-ceremony) |
| OQ-UT9 | The record and table shapes are designed for the boundary. (Owner ruling, 2026-11-06.) | [§5.1](#51-the-common-contract), [§8](#8-the-shared-substrate) |

## 15. Open questions

None. The nine questions are ruled in the Decision Ledger ([§14](#14-decision-ledger)) under their IDs.
