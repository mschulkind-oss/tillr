---
title: "Landing velocity — a landing tier, a rolling tier, and a watermark between them"
status: accepted
stage: DESIGN
verified: 2026-11-05
next: "Build the landing tier in shadow, then flip (§10); all three questions are ruled in the Decision ledger"
tags: [landing, gate, velocity]
summary: "Split the landing gate in two. A landing tier runs on every batch before the trunk moves: the integrity set, a full build and test-compile of every touched module, every cheap diff-scoped check, and the gate's own audit suite whenever gate code is in the batch. A rolling tier runs the behavior suites on the trunk itself, every 10 landings or 20 minutes, whichever first, and advances a verified watermark; landing refuses when the watermark lags more than 20 landings or 60 minutes. At batch 5 the landing tier is priced at 35–50 seconds against today's 8–12 minutes, which is over 100 landings per hour on a serial merge with no parallel gates. Parallel gate runs and larger batches are rejected as first builds, on measurements named in the body."
---

# Landing velocity: the trunk moves on the cheap proof, the suites follow on a clock

**Status:** All three questions are ruled in the [Decision ledger](#decision-ledger), which
records what is built. [The gate design](gate-rework.md) amends this two-tier design. Gate timings are measurements of 2026-11-02 to 2026-11-05 on
a 12-core machine, stated where they are used.

> **In short.** The gate is one proof doing two jobs: "this tree is safe to stand on" and
> "this tree's behavior suites pass." Only the first job has to finish before the trunk
> moves. Split them, bound how far the second may lag, and the serial merge stops being
> the bottleneck at the machine we already have.

**Why it matters.** Ten lanes at half an hour each produce about 20 candidates an hour.
Today's serial gate at the committed batch cap of 2 lands about 12 an hour in the
dominant change class, so the queue grows without bound while implementation waits.

**The shape.** A **landing tier** on every batch, a **rolling tier** on the trunk every
10 landings or 20 minutes, and a **verified watermark** naming how much of the trunk the
rolling tier has proved — with a hard lag cap that refuses landing when it is exceeded.

**Cost.** The trunk is suite-unverified inside a bounded, visible window, and some reds
are found after landing and repaired by a queue-landed revert instead of before landing.
That trade is [D-LV-1](#decision-ledger), ruled by the owner on 2026-11-05.

**Ruled 2026-11-05.** The owner took A on all three: the trunk may be suite-unverified
inside the bounded window ([D-LV-1](#decision-ledger)), the lander reverts its own
escapes mechanically and requeues the candidate as rework ([D-LV-2](#decision-ledger)),
and the throughput target is blended over the drain hour with gate-code batches exempt
([D-LV-3](#decision-ledger)). The 100-per-hour figure is directional — an operating
goal, not a per-class contract.

**Start at [§4](#4-the-two-tiers)** — the split itself. The arithmetic that forces it is
[§3](#3-the-arithmetic-at-12-cores); the protections are [§5](#5-invariants-that-may-not-bend)
and [§6](#6-cadence-watermark-and-the-halt-rule).

**Ruled:** [D-LV-1](#decision-ledger), [D-LV-2](#decision-ledger), [D-LV-3](#decision-ledger) — A, A, A; the questions and their options are compacted there.

**Reads with:** [`gate-rework.md`](gate-rework.md) (the gate design that amends this one) and
[`landing-principles.md`](landing-principles.md) (the rulings this design is checked
against, principle by principle, in [§5](#5-invariants-that-may-not-bend)).

---

## 1. The verdict

Build the tiered gate first. Specifically, in this order, each step measured before the
next is believed ([§10](#10-sequencing-shadow-first-then-flip) has the full sequence):

1. **Declare a tier for every check kind** in the gate, and split the Go step into a
   build step and a suite step. Run the landing tier in shadow beside today's gate and
   measure what it would have cost and what it would have missed.
2. **Stand up the rolling runner and the watermark** while the full gate still decides
   every landing, so the runner's cadence and cost are measured before anything
   depends on it.
3. **Flip the landing pass to the landing tier**, restore the batch bound from 2 to the
   code's declared 5, and let the lag cap be the only thing that can slow landing down
   again.

What this buys, at the prices in [§3](#3-the-arithmetic-at-12-cores): a batch in the
dominant class gates in an estimated 35–50 seconds instead of 8–12 minutes, so a serial merge
at batch 5 has a wall-clock capacity above 350 landings per hour, and above 100 per
hour even if the estimate is wrong by a factor of three. No parallel gate runs, no
larger batches, and no new hardware are needed to reach the owner's 100 per hour.
Latency to first landing falls from one full gate (minutes, after the batch fills its
claim) to one landing-tier run (under a minute).

Two honest limits, stated now rather than discovered later:

- **100 per hour is a capacity, not a promise about every class.** A batch that changes
  the gate's own code still pays the gate's audit suite before it lands
  ([§5](#5-invariants-that-may-not-bend)), at batch-of-one — roughly 6–15 such
  landings per hour at that suite's measured range. That class is exempt from the
  target and counted separately ([D-LV-3](#decision-ledger)).
- **The rolling tier consumes real CPU.** At the stated cadence it is up to half of one
  serial gate slot, off the landing path. On a 12-core box already at load 15.5 with
  ten lanes running, that CPU comes from the lanes' headroom, not from nowhere.
  [§11](#11-what-would-falsify-this) names the lag measurement that says the box
  cannot afford both.

## 2. What exists today, measured

The gate is one command, `just done`, stated in [`../../.justfile`](../../.justfile).
It is already
selective: a step runs only when a changed path matches what it watches, plus a fixed
integrity set (`structural`, `file-size`, `commit-identity`) on every run. Over
2026-11-02 to 2026-11-04, 371 of 371 planned gates were scoped and none was full, so
selection is spent as a lever. The
landing pass in
[`../../tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/)
batches candidates (declared bound 5 in code, capped to 2 by the drain recipe in
[`../../.justfile`](../../.justfile)), gates the integrated tree once, bisects a red
batch, and publishes by one compare-and-swap. The plan contract in
[`../../tools/tillr-gate/plan.go`](../../tools/tillr-gate/plan.go)
already proves a selective plan covered the batch's diff before anything lands on it.

Where the time goes, all measured:

| Fact | Number | Source |
| :--- | :--- | :--- |
| One selective batch gate, dominant class | 8–12 min (575s selected, 958s unskipped, one hand gate over two commits) | Owner's 2026-11-05 pass |
| `go-tool` share of observed check time | 57.3%; top three checks 76.5% | 2026-11-02 → 11-04 histogram |
| Largest Go module, `go-tool` median | 303.5s (Nov 2–4), then 219.7s over 45 steps on 2026-11-05, max 545.6s | Histogram; the gate's timing timeline, 2026-11-05 |
| Docs-only gate | 1.4s | Measured 2026-11-02 |
| Gate efficiency ratio (wall ÷ floor) | 1.00–1.15 at peak concurrency 11 of 12 | Histogram and efficiency records |
| Carried (reused) verdicts | 1 in the histogram window | Histogram |
| Isolations by cause | `conflict` 50.0%, `unverified` 30.3%, `content` 19.7% | Histogram |

Three readings of that table drive everything below:

- **Gate cost is per change class, not per candidate.** A docs batch already lands at
  thousands per hour of capacity. The bottleneck class is the largest Go module:
  across large gates the single largest check is a median 67% of the gate's time,
  and in this class that check is the combined build-and-suite step. Any honest
  design is class-aware or it is optimizing the classes that are already free.
- **Inside one gate there is nothing left to parallelize.** A wall-to-floor ratio of
  1.00 means the run already costs its longest necessary chain.
  Parallelizing the suite's serial cases bought a real improvement (median
  303.5s → 219.7s) and still missed its own pre-stated 150s acceptance at today's
  load. Suite-speed work remains worth doing, but one more increment of it does not
  close a tenfold gap.
- **The two locks forbid parallel gates today.** The pass holds the land lock across
  the gate, and the gate holds its record lock for the whole run, so a second gate
  run refuses rather than runs. Parallel gates are therefore a build, not a knob —
  and [§9](#9-alternatives-each-with-a-verdict) prices what that build would buy on a
  box whose load already exceeds its cores.

One rule follows from how deferral fails. A slow subset that runs only on a full audit, which
the landing path never requests, lets a red case land unseen. Deferral without a named runner,
a cadence, and a bound is not a tier; it is a blind spot. This design's rolling tier is
deferral with all three named — or it should not be built.

## 3. The arithmetic at 12 cores

The owner's numbers: about 20 candidates per hour arrive; landing capacity should be
about 100 per hour so the queue stays near-empty and absorbs bursts. On a serial
merge, capacity is batch size ÷ gate wall time, so the per-batch wall budget for
100 per hour is:

| Batch | Wall budget per batch for 100/hour | Today, dominant class (≈600s) | Landing tier (est. 35–50s) |
| :--- | ---: | ---: | ---: |
| 1 | 36s | 6/hour | 72–103/hour |
| 2 (committed cap) | 72s | 12/hour | 144–206/hour |
| 5 (code default) | 180s | 30/hour | 360–514/hour |
| 10 | 360s | 60/hour before bisect costs | 720+/hour |

The landing-tier column is an estimate built from measured parts, and it is marked as
one wherever it is used: the cheap phase's longest member measured 15.9s,
the integrity set is under a second each, and a warm-cache
build plus test-compile of one Go module is the only unmeasured term, priced here at
15–30 seconds under load. [§10](#10-sequencing-shadow-first-then-flip) measures it in
shadow before anything lands on it, and
[§11](#11-what-would-falsify-this) states the measured value that would kill the
design. Even at three times the estimate (150s), batch 5 clears 100 per hour.

Why the other shapes do not get there first, in arithmetic rather than taste:

- **Parallel gates create no CPU.** The box is 12 cores at load 15.5 with ten lanes
  running, and one gate already runs at efficiency 1.00–1.15. Four concurrent gates
  mostly timeshare the same cores: the realistic gain is overlap of one run's cheap
  phase with another's suite, perhaps 1.3–1.6×, against the 8× the target needs in
  the dominant class — after paying to split two locks and inventing a global CPU
  admission budget the gate does not have (its job caps are per-process).
- **Larger batches buy throughput with red batches.** Gate cost is roughly fixed per
  class union, so batch 10 looks cheap until it is red. Content reds were 52 of 736
  batched candidates in the histogram window, about 7% per candidate, so a batch of
  10 contains a content red with probability near one half — and bisecting it costs
  roughly five more full gates. Batch size is an amortization knob, not a
  tenfold lever.
- **A tiered gate is the only shape that removes work from the serial path instead
  of rearranging it.** The suite still runs — minutes later, on the trunk, over a
  range — but it no longer stands between a candidate and the compare-and-swap.

The rolling tier's own cost, stated plainly: one range run is roughly the largest
suite's short subset (median 219.7s today) plus its integration subset (about 160s,
measured 2026-10-24) plus whatever
other heavy kinds the range's watches select — call it 6–10 minutes of wall per run,
at most three runs an hour at the cadence in
[§6](#6-cadence-watermark-and-the-halt-rule). That is up to half an hour of suite
wall per hour, admitted under a smaller heavy-job cap than a landing gate, off the
path any candidate waits on.

## 4. The two tiers

### The substrate Go suites gate at the landing tier

`tools/tillr/` gates at the landing tier under its own check kind and `tools/tillr-drain/`
stays rolling, with per-module measured costs.

Four terms, defined here and used nowhere else in the tree yet:

- **Landing tier** *(coined here)* — the checks a batch must pass on the exact tree
  the compare-and-swap will publish, before the trunk moves. It is not "the fast
  checks": membership is by what a failure would cost after landing, per the rule in
  [§4.3](#43-the-membership-rule), and some members are slow.
- **Rolling tier** *(coined here)* — the checks that run on the trunk tip after
  landing, over the range since the watermark, on the cadence in
  [§6](#6-cadence-watermark-and-the-halt-rule). It is not a nightly job and not
  optional: an overdue rolling tier refuses landing.
- **Verified watermark** *(coined here)* — the newest trunk commit the rolling tier
  has judged green. The gate already keeps a per-branch verified baseline for its own
  planning; the watermark is that fact made a landing surface, with one writer (the
  rolling runner) and one reader that matters (the lander, enforcing the lag cap).
  It is not a claim that commits past it are broken — only that they are unproved.
- **Escape** *(coined here)* — a rolling-tier red attributed to a specific landed
  candidate: a defect the landing tier was never going to catch, found within its
  window. An escape is the price of the design made countable, and
  [§11](#11-what-would-falsify-this) is written in escapes per 100 landings.

### 4.1 What the landing tier runs, on every batch

| Member | Why it cannot wait |
| :--- | :--- |
| The integrity set: `structural`, `file-size`, `commit-identity` | Already unconditional today; properties of the tree, sub-second to seconds |
| Build and test-compile of every touched Go module (`go build`, plus a vet or no-test run that compiles the test files) | The trunk must always compile. A non-compiling trunk breaks every lane's next rebase and the next gate itself |
| Series and fork invariant steps when a fork series is in the diff | A series that does not apply breaks the next launch the moment it lands |
| Every diff-scoped check whose declared cost estimate is at most 30s: `documents`, `citations`, `design-citations`, `scratch`, and their peers | Deferring them saves under half a minute and loses per-candidate attribution. Cheap proof stays at the scene of the change |
| The plan-coverage contract (plan base at or behind batch base; every required kind either run or recorded as owed to the rolling tier) | The meta-invariant that makes selection honest today; it now also names the debt |
| `self-suite`, the gate's own audit suite — **only when gate code is in the batch, and then the batch is of one** | Gate code defines what every later landing is judged by. A broken selector landed on deferred proof would be judged by itself. [§5](#5-invariants-that-may-not-bend) states this as an invariant |

The store-migration step the lander already runs after the gate and before the trunk
moves is untouched: a landing that outruns the live store's schema is exactly the
failure that step exists to refuse.

### 4.2 What the rolling tier runs, on the trunk

| Member | Why it can wait one window |
| :--- | :--- |
| Go behavior suites: the `-short` subsets split out of today's combined `go-tool` step, per touched module | A behavior red is repaired by revert with no data loss. This is the 220–320s term leaving the serial path |
| The Go integration subset | A cadence with a named runner is strictly more proof than a landing gate that runs it once per batch |
| Heavy diff-scoped suites above the 30s line | Same revert argument, at 20–100s each |
| Fork-quality steps (the forks' own upstream suites) | The fork build itself stays in the landing tier with the other series invariants, so a launch can always construct what landed; an upstream suite red is behavior risk inside the window, repaired by revert like any other |

One selection rule spans both tiers: the rolling tier is planned from the watermark,
exactly as the gate plans from its verified baseline today. A range that touches no
watched path of a heavy kind does not run it. Nothing in this design runs every test
on every rolling run; it runs the affected tests on every range, and the union over
ranges is what replaces the per-landing full proof.

### 4.3 The membership rule

A check belongs to the landing tier when any of these holds, and to the rolling tier
only when none does:

1. Its failure, landed, would break the machinery other work stands on — compilation,
   the live store's schema, a series application, or the gate's own selection.
2. Its failure cannot be attributed after the fact (it judges the diff's shape, not
   the resulting tree's behavior).
3. It is cheap at the declared estimate (30s), so deferral buys nothing worth the
   attribution it costs.

Membership is a declared table in the gate, in the same shape and with the same
fail-safe direction as the existing check-kind tables: every kind the planner can
produce is named with its tier, the gate's audit suite refuses a kind the table does
not name, and an unnamed kind defaults to the **landing** tier — the worst a missing
entry can do is make a landing slower, never quieter. Moving a kind between tiers is
a deliberate edit to that table, reviewable in one line, and
[§11](#11-what-would-falsify-this) is what orders such a move when the escape count
says a kind was placed wrong.

## 5. Invariants that may not bend

Each invariant names the landing principle that already owns it where one does; this
design bends none of them, and the one it puts under strain is named at the end.

- **I1. The gate judges the tree that will be the tip, once.** The landing tier runs
  on the integrated batch tree, the compare-and-swap publishes that tree, and a lost
  race replays without a second landing-tier run — exactly
  [principle 3](landing-principles.md#3-gate-what-will-be-the-tip-once). Tiering
  changes which checks that one run carries, never which tree it judges.
- **I2. The trunk always compiles, migrates, and applies.** Build, test-compile,
  store migration, and series application are pre-publish, always. What the gap
  risks is behavior, never the ground other work stands on.
- **I3. Gate code never lands on deferred proof.** A batch touching gate code is a
  batch of one and carries the gate's audit suite in its landing tier. The selector
  is the one component whose failure hides every other failure, so it is the one
  component this design refuses to speed up.
- **I4. A refusal is never a verdict, in either tier.** Exit 2 leaves candidates
  unjudged and uncharged
  ([principle 1](landing-principles.md#1-a-refusal-must-be-a-verdict),
  [principle 5](landing-principles.md#5-only-verdicts-charge),
  [principle 8](landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)).
  A rolling-tier refusal is a machinery fact: the watermark does not advance, the
  lag clock keeps running, and attribution does not start.
- **I5. Deferred is recorded, never indistinguishable from skipped.** The plan
  contract gains a third state beside ran and not-planned: owed to the rolling tier,
  naming the kinds and the range that owes them. A landing whose required kinds are
  neither run nor recorded as owed is refused, as an uncovered plan is today.
- **I6. The lag is bounded and the bound refuses.** Past the cap in
  [§6](#6-cadence-watermark-and-the-halt-rule), landing refuses — records no verdict,
  charges no candidate — until the watermark advances. The refusal names a mover
  that can still act: the rolling runner, owned by the drain
  ([principle 13](landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)).
- **I7. Nothing is hand-carried, including the remedy.** A revert is a candidate the
  lander itself composes and lands through the landing tier; a revert that cannot
  land mechanically becomes a mechanic lane's work, never a hand push
  ([principle 9](landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work)).
- **I8. The window is visible on the surface that judges the queue.** The queue
  surface shows the watermark, the lag in landings and minutes, and any unattributed
  red range, beside the queue it already shows
  ([principle 7](landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)).
  A lane whose targeted check fails against an unproved range records that surface
  fact, not a verdict against its candidate.

The principle under strain is the reading of
[principle 3](landing-principles.md#3-gate-what-will-be-the-tip-once) as "the tip is
fully proved before it moves." This design keeps the principle's mechanism — gate
the published tree, once, never re-gate on trunk movement — and narrows what "proved"
means at publish time to the landing tier. That narrowing is a policy change about
what the trunk guarantees, it is the whole of [D-LV-1](#decision-ledger), and had the
owner ruled against it this design would be finished, not adjustable: there is no version of
100 per hour on this box that also keeps the full suite on the serial path, and
[§3](#3-the-arithmetic-at-12-cores) is the proof of that sentence.

## 6. Cadence, watermark, and the halt rule

**Cadence: every 10 landings since the watermark, or every 20 minutes since the
watermark advanced, whichever comes first.** The trigger is evaluated by the drain
on each landing and on its existing supervisory beat; there is no separate timer
process to die quietly. The two numbers are chosen against the two costs:

- **N = 10 bounds attribution.** Bisecting a red range of 10 landed commits, running
  only the failing kinds, takes at most 4 scoped runs. At N = 20 it takes 5 and the
  unproved window doubles; at N = 5 the rolling tier runs twice as often for one
  fewer bisect step. 10 is where the bisect is still a handful of runs — the same
  reasoning the code already states for the batch bound of 5.
- **M = 20 minutes bounds staleness when arrivals are slow.** At 20 arrivals per hour
  the count trigger alone would leave a red undiscovered for half an hour; the time
  trigger fires first at steady state (about 7 landings), and the count trigger takes
  over exactly when a burst makes the range grow faster than the clock.

**Lag cap: landing refuses when the watermark is more than 20 landings or more than
60 minutes behind the tip.** Two cadence windows, not one, so a single slow or red
rolling run does not stop the queue, and a dead runner stops it within the hour
instead of never. This cap is the design's answer to unbounded deferral: deferral here is a
declared, expiring state, and expiry refuses landing rather than renewing silently.

**When the rolling tier is red and the next batch wants to land**, the rule is:

1. Attribution starts immediately ([§7](#7-a-rolling-red-attribution-and-remedy));
   landing continues under the lag cap while it runs.
2. Landing halts — refuses, per I6 — when any of these first occurs: the lag cap is
   reached; a second consecutive rolling run over a fresh range is red (the trunk is
   degrading faster than attribution is clearing it); or the remedy for an attributed
   escape cannot land within one cadence window (the trunk is known-red and
   mechanically unfixable, so adding to it stops).
3. A red whose failing kind is a landing-tier member (build, integrity) is not a
   cadence event at all: the landing tier just proved that kind on the published
   tree, so its rolling failure means the tiering machinery is lying. Landing halts
   immediately, the kill switch in [§11](#11-what-would-falsify-this) is thrown, and
   the design is treated as falsified until a person says otherwise.

A rolling run that is merely still running changes nothing: runs overlap landings by
design, a run judges the tip it started from, and its green advances the watermark to
that tip when it lands. Two rolling runs never run at once — the runner is serial,
because its whole value is an ordered watermark.

## 7. A rolling red: attribution and remedy

A landed commit cannot be un-landed. This repository's queue does not publish to a
remote, so "landed" means the local trunk moved; the remedy is a revert commit, and
the history keeps both. The flow is mechanical until it cannot be, and each step
names its actor:

1. **Confirm.** The runner re-runs the failing kinds once on the same tree, as a
   named measurement. Green on re-run is a flake: it is filed as one, the watermark
   advances, nothing is charged. Red on re-run is a verdict. One re-run is the
   retry-as-test the failure-diagnosis ruling already sanctions; a second re-run
   would be waiting a failure out
   ([principle 11](landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)).
2. **Attribute.** The runner bisects the landed commits in the range, running only
   the failing kinds, until one candidate's commit is the first red. That candidate
   now has a judged verdict against it, recorded exactly as a per-batch red is today,
   and charged per distinct cause
   ([principle 5](landing-principles.md#5-only-verdicts-charge)).
3. **Revert.** The lander composes a revert of that candidate's landed commits and
   lands it at the head of the queue as a batch of one, through the landing tier —
   the system's own candidate, not an operator's hand act. The watermark then
   advances over the range, the revert included, once a rolling run over the new tip
   is green. The lander does this without asking ([D-LV-2](#decision-ledger)),
   recording the attribution it acted on.
4. **Return the work.** The reverted candidate re-enters the queue as rework carrying
   the rolling failure as its brief, through the same repair path a per-batch red
   takes today. Nothing about its second attempt is special, except that its first
   verdict arrived after its first landing.
5. **Escalate what the mechanics cannot.** If the revert conflicts, fails the landing
   tier, or the attributed range contains a gate-code or store-schema change, the
   mechanical path stops: one deduped ticket is filed, a mechanic lane is the mover,
   landing halts per [§6](#6-cadence-watermark-and-the-halt-rule), and the owner is
   the rung above the mechanic — reached by the ticket, not by silence.

Fix-forward is deliberately not the default remedy. A fix takes an unbounded time
with the trunk known-red; a revert takes one landing-tier run. A remedy candidate
that already exists when attribution completes may land instead of the revert if it
passes the landing tier as the immediate next batch — same path, same proof, no
waiting for a lane to produce one.

## 8. What protects the trunk in the gap

Between a landing and its rolling proof, the trunk is protected by five things, and
by nothing else. They are listed so the list can be argued with:

1. **The ground cannot break** (I2): compile, schema, and series application are
   pre-publish, so lanes always rebase onto a trunk that builds.
2. **The judge cannot break quietly** (I3): gate code lands alone, fully audited.
3. **The gap is short and counted** ([§6](#6-cadence-watermark-and-the-halt-rule)):
   at most 10 landings or 20 minutes to discovery, 20 or 60 to a refused landing.
4. **The gap is visible** (I8): anyone reading the queue surface can see exactly
   which commits are unproved, so no one has to guess whether a red lane check is
   theirs or the trunk's.
5. **The repair is faster than the damage**: the remedy path costs one landing-tier
   run, so a discovered escape is typically reverted within a minute of attribution
   plus the bisect — far inside the window in which a behavior red misleads a lane.

What is **not** claimed: that a lane can trust the tip's behavior. It cannot, inside
the window, and [D-LV-1](#decision-ledger) puts a name and a bound on exactly that exposure
rather than leaving it as fine print.

## 9. Alternatives, each with a verdict

| Alternative | What it buys | Verdict |
| :--- | :--- | :--- |
| Parallel gate runs, serial merge | An estimated 1.3–1.6× on a saturated box, after splitting the land lock and the gate record lock and building a global CPU admission budget | **Rejected as the first build.** Build it only if falsifier F3 fires — if the measured landing tier is still above budget, parallelism is how the remaining constant is attacked |
| Larger batches (10–20) with bisect | Up to 60/hour before bisect costs at today's gate | **Rejected.** Half of all batches of 10 would be red on content alone at the measured 7% per-candidate rate, and latency to first landing worsens. The bound returns to 5, no higher |
| Suite-speed work alone (shard, parallelize, cache harder) | Measured: about 1.4× on the dominant step | **Rejected as the landing answer; kept as rolling-tier cost work.** Every second it shaves still shortens a rolling run and an attribution bisect |
| Test-level selection inside the monolith package (run only tests naming the changed files, per batch) | Could in principle keep behavior proof pre-landing | **Rejected for v1.** No sound file-to-test map exists for a single-package suite whose cases cross files freely; an unsound map is a quieter version of the same deferral, without the watermark that makes deferral honest. If a sound selection ever exists, the selected subset joins the landing tier and the escape rate should fall — the tier table is where it would land |
| A second machine or remote CI for gates | Real extra CPU | **Out of scope here.** It changes the local-first trust model every other landing ruling assumes, and the owner's arithmetic is stated for this machine. Nothing in the tier design forbids the rolling runner from living elsewhere later |
| Keep today's gate, accept the queue | No build | **Rejected by arithmetic.** At the committed batch cap the dominant class lands about 12 per hour against 20 arriving; the queue is the product, in the wrong direction |

## 10. Sequencing: shadow first, then flip

No step below is believed until the step before it is measured. This is the design's
sequence; the build order inside each step belongs to the implementation plan.

1. **Tier table and the Go split, in shadow.** Declare every kind's tier; split the
   combined Go step into build and suite kinds. The landing pass records what the
   landing tier would have planned and cost beside the full gate that still decides.
   Exit measurement: landing-tier wall per class over at least 100 real batches, and
   the would-be escape list — every full-gate red the landing tier would have passed.
2. **The rolling runner and watermark, observing.** The runner starts proving ranges
   on the trunk at the stated cadence while the full gate still gates every landing,
   so its green is redundant and its cost is pure measurement. The queue surface
   gains the watermark and lag. Exit measurement: rolling runs keep the lag at zero
   (they have no excuse not to) at a heavy-job cap that does not measurably slow the
   lanes.
3. **The flip.** The landing pass requests the landing tier; the batch bound returns
   from the recipe's 2 to the declared 5; the lag cap starts refusing. Tier selection
   is the caller's request, never the gate command's new default
   ([§12](#12-what-this-does-not-license)).
4. **Attribution and auto-revert.** Bisect-on-red and the lander-composed revert,
   switched on only after the flip has produced its first real escapes to practice
   on by ticket. Until this step, an escape files a ticket and a person is the mover
   — named, per I6, rather than automated on a guess.

Rollback at any step after the flip is one configuration change: the landing pass stops
requesting the landing tier and every batch is full-gated again, exactly as today.
The watermark and runner can stay running; they are harmless when nothing depends on
them, and their records are the evidence for whatever is decided next.

## 11. What would falsify this

Each falsifier is a measurement the system's own records already know how to produce,
a threshold fixed now, and a named consequence. "Revert to serial-and-slow" has a
single number, stated first.

| ID | Measurement | Threshold | Consequence |
| :--- | :--- | :--- | :--- |
| F1 | Escapes per 100 landings, rolling 7 days | **Above 5** — or any single escape in a landing-tier kind | The kill switch: the landing pass returns to the full gate the same day, by configuration, and this design needs a fresh ruling before any retry. 5 is the number that reverts to serial-and-slow |
| F2 | Escapes per 100 landings in one rolling kind | Above 2 | That kind moves to the landing tier (a one-line table edit) and the count restarts. The design survives; the placement did not |
| F3 | Landing-tier wall, dominant class, p95 in shadow or after flip | Above 180s at batch 5 (below 100/hour) | The estimate was wrong where it mattered. Parallel gates become the next build; if they cannot recover the budget either, the 100/hour target is not reachable on this box and the owner re-rules the target, not the proofs |
| F4 | Watermark lag during drain hours, p95 | Above 10 landings or 20 minutes sustained over a week | The box cannot afford lanes plus the rolling tier at this cadence. Halve arrivals' pressure (fewer lanes) or move the runner; do not stretch the cadence, which only renames the blind spot |
| F5 | Combined judged-red rate (landing reds + escapes) against the 19.7% content-isolation baseline | Falls materially while landings rise | Speed was bought by judging less, somewhere undeclared. Audit the tier table against the gate's selection records before anything else ships |
| F6 | Attribution cost per escape | Median above 4 bisect runs, or a second revert-per-month needing a person | N is too large. Halve it once; a repeat is F1 evidence, not a second halving |

The positive claim, held to the same standard: within a week of the flip, a drain
hour at 20 arrivals should show a near-empty landing queue, a watermark lag at or
below one cadence window, and latency-to-first-landing under a minute — read from
the pass and gate records, not from this document's say-so.

## 12. What this does not license

- **The tier is not the gate command's default.** The tier is a caller request with a
  full-gate default, so a caller with no watermark machinery keeps the full gate.
- **No check is deleted.** Every kind that runs today runs in one tier or the other;
  the integration subset gains a regular runner on the trunk. A kind leaving both tiers
  is a different proposal with its own ruling.
- **The refusal pathologies are not fixed here.** A landing pass that refuses in a loop
  is the refusal and re-eligibility machinery's own defect
  class, owned by [the landing principles](landing-principles.md). Tiering makes
  each cycle cheaper; it does not make a looping cycle terminate.
- **The batch recipe cap is not raised before the flip.** At today's gate, cap 5
  buys 30/hour at worse latency; the interim state is not worth its own churn.
- **Nothing here is a retry, a cooldown, or a bench.** Every re-run in this design is
  either the one named confirmation measurement or work against a changed input
  (a revert, a remedy, a moved watermark), per
  [principle 11](landing-principles.md#11-a-failure-is-diagnosed-not-waited-out).

## Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-LV-1 | [OQ-LV1](#decision-ledger): The trunk may move before its behavior suites have passed, inside a bounded window — the landing tier proves the tree safe to stand on, the rolling tier proves behavior on the trunk every 10 landings or 20 minutes, a verified watermark is published, and landing refuses when the watermark lags more than 20 landings or 60 minutes. The unproved range is visible on the queue surface and the bound is the kill switch | 2026-11-05 | [§4](#4-the-two-tiers), [§6](#6-cadence-watermark-and-the-halt-rule) | not built |
| D-LV-2 | [OQ-LV2](#decision-ledger): The lander may revert a landed candidate by itself, recording the attribution it acted on and returning the candidate to the queue as rework; a revert is itself a candidate through the same gate. Gate-code and schema ranges still escalate to the owner | 2026-11-05 | [§7](#7-a-rolling-red-attribution-and-remedy) | not built |
| D-LV-3 | [OQ-LV3](#decision-ledger): The 100-per-hour target is blended over the drain hour, not required of every class alone; a batch that changes the gate's own code is exempt and counted separately in the open. The figure is a directional, estimated goal — an operating direction, not a letter-by-letter contract | 2026-11-05 | [§3](#3-the-arithmetic-at-12-cores) | not built |
