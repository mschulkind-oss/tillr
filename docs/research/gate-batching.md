---
title: "Gate batching vs. per-candidate gates: schedule comparison and the 2026-11-25 burst measurement"
status: accepted
stage: CURRENT
tags:
  - tillr
  - gate
  - batching
  - concurrency
  - research
summary: "Records the 2026-11-25 owner ruling closing OQ-GTC4 against batching or queueing candidate gates, the three-schedule comparison, the two reasons batching lost (expected bisection time at a 15% per-candidate failure rate, and single-candidate attribution under landing principles 3 and 14), the 2026-11-25 five-gate burst measurement on a 32-processor machine, and what would have to change to revisit batching with data."
vantage:
  status-chip: true
---

# Gate batching vs. per-candidate gates: schedule comparison and the 2026-11-25 burst measurement

On 2026-11-25, after reading the comparison of gate schedules in
[Section 5 of the gate capacity design](../design/gate-test-capacity.md#5-arrival-order-and-batching-against-running-ready-gates-at-once),
the owner closed [`OQ-GTC4`](../design/gate-test-capacity.md#OQ-GTC4):

> "ok, so let's not batch. close it with a resaerch report recorded for the future I guess?"

Each finished candidate ([the vocabulary](../design/tillr-vocabulary.md)) keeps starting its own
[landing gate](../reference/gate.md#the-landing-gate) as soon as it is ready. Candidates are neither
combined into one tree for a shared gate run nor held idle to collect a batch or wait in an
arrival-order queue.

This record preserves the three schedules compared, the two reasons batching was rejected, the
operating measurements from 2026-11-24 and 2026-11-25 on a 32-processor machine, and the conditions
under which reopening the question with new data would make sense.

---

## 1. The three schedules compared

When several finished candidates reach the gate close together, three schedules are possible:

- **A. Start every ready gate (chosen in [`OQ-GTC4`](../design/gate-test-capacity.md#OQ-GTC4)).**
  Each candidate starts its own gate immediately when its worker exits, and the kernel schedules all
  runnable test processes across the machine's processors.
- **B. One Go gate at a time in arrival order (rejected).** Gates that run Go package tests
  ([`go-affected`](../reference/gate.md#affected-go-tests-before-the-merge)) acquire a cross-gate
  lock and execute one at a time in arrival order, while fast documentation-only gates bypass that
  queue.
- **C. One combined gate for a batch (rejected).** Waiting candidates are merged into one combined
  tree and gated together once; if any check fails, the batch is bisected (split repeatedly and
  re-gated) until the failing candidate or candidates are isolated.

In [Section 5 of the gate capacity design](../design/gate-test-capacity.md#5-arrival-order-and-batching-against-running-ready-gates-at-once),
the three schedules were compared against the baseline measured on 2026-11-24 on a 32-processor
machine, where a Go gate running alone took 5 to 8 minutes and six Go gates running together beside
an after-merge [trunk proof](../reference/gate.md#after-merge-proof-and-the-green-pointer) took 18
to 21 minutes each at a load average of 130 to 180:

| Schedule | Staggered arrivals | Six candidates at once: first done | All six done | A fast documentation gate during the burst | Cost of one red candidate |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **A. Start every ready gate** | 5–8 min, no wait | 18–21 min | 20–26 min | Seconds | None for the others |
| **B. Arrival order, one Go gate at a time** | 5–8 min, plus a wait when two finish close together | 5–8 min | 30–48 min | Seconds, if it bypasses the queue | None for the others; needs a cross-gate lock |
| **C. One combined gate for the batch** | Solo time, or a deliberate hold to collect a batch | 6–10 min if all pass | 6–10 min if all pass; 20–32 min with bisection | Delayed with the batch | One red candidate stalls the rest |

Starting every ready gate beats an arrival-order queue on total burst clearance (20 to 26 minutes
versus 30 to 48 minutes) without adding a cross-gate lock. A single gate does not keep all 32
processors busy from start to finish: test selection, compilation warm-up, child-process and SQLite
I/O waits, and the last long shards all leave processor capacity idle. Running gates serially pays
those gaps once per candidate, whereas running ready gates together overlaps them. Arrival order
shortens wall time only for the first candidate in a burst, at the expense of the whole batch.

---

## 2. Why batching was rejected

Batching lost to per-candidate gates on two independent grounds:

1. **Expected wall time under failure and bisection.** At a 15% gate failure rate per candidate, a
   batch of six candidates passes together on the first combined run only 38% of the time
   (`0.85^6 ≈ 0.377`). The remaining 62% of batches hit at least one red check and need about three
   more rounds of bisection to isolate the failing candidate, stretching completion to 20 to 32
   minutes — slower in expectation than six independent gates running concurrently (20 to 26
   minutes). Under staggered arrivals, assembling a batch also requires holding a finished candidate
   idle while waiting for others to finish, which violates the owner ruling against artificial
   barriers ([`R-GTC3`](../design/gate-test-capacity.md#R-GTC3)).
2. **A red batch names no candidate, breaking single-candidate verdicts and clean attribution.**
   - [Landing principle 3 (Gate what will be the tip, once)](../design/landing-principles.md#3-gate-what-will-be-the-tip-once)
     and [landing principle 2](../design/landing-principles.md#2-re-application-preserves-the-change-not-the-patch)
     bind each `gate.verdict` to one candidate's `(base_sha, head_sha^{tree})` so `Trunk.Squash` in
     [`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go) can three-way merge
     that candidate onto a moving trunk without a second gate. A combined tree produces no
     per-candidate verdict until every failure in it has been separated out.
   - [Landing principle 14 (Convict a run on what it did, not on what happened while it ran)](../design/landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)
     requires that a check failure convict only the candidate whose change caused it. In a combined
     batch, one red candidate stalls every green sibling sharing the batch and gives no candidate
     attribution on its own.

---

## 3. Measurements on a 32-processor machine

### Baseline before the ruling (2026-11-24)

As recorded in [the gate capacity design](../design/gate-test-capacity.md), on a 32-processor
machine a Go gate running alone finished in 5 to 8 minutes. Six Go gates running concurrently
beside a trunk proof finished in 18 to 21 minutes each at a load average of 130 to 180, with
container memory pressure at 0.00 and 81 GB free; a separate host-wide reading under five or six
gates plus a trunk proof showed 11% to 15% memory-pressure stall time over ten seconds and no
process killed.

### Operating-session burst measurement (2026-11-25)

On 2026-11-25, during an operating session on a 32-processor machine, six nearly finished
candidates were refiled at the front of the queue at once after a store reset. Five of those six
candidates reached the gate within about three minutes of each other — between 00:37 and 00:41 ET —
while the full after-merge trunk proof was also running:

- **Machine load and process counts:** load average stood between 158 and 170 with 4 to 5 percent
  processor idle, about 1,700 new processes per second, 82 Go test runners, and 81 drain test
  binaries alive at the same time.
- **Gate outcomes and durations:** the first three gates to finish all passed; the two with recorded
  times took 1,503 seconds (25.1 minutes) and 1,573 seconds (26.2 minutes) against a 330-second
  target budget (`DefaultGateBudget` in
  [`tools/tillr-drain/drain/timing.go`](../../tools/tillr-drain/drain/timing.go); budgets are
  targets rather than kill deadlines under [`D-GR15`](../reference/gate.md#D-GR15) and
  [`R-GTC4`](../design/gate-test-capacity.md#R-GTC4), and only a lack of progress trips
  [the stall watchdog](../reference/gate.md#budgets-and-the-stall-watchdog)).
- **Reliability under saturation:** no test failed because of the load.
- **Watch alerts observed:** the [`tillr-watch`](../design/tillr-watch.md) alerts
  `trunk-proof-stalled`
  ([Section 5.9 of the watch design](../design/tillr-watch.md#59-trunk-proof-lag-and-stalled-heavy-proof-progress-d-tw54))
  and `fast-gate-run-slow`
  ([Section 5.5 of the watch design](../design/tillr-watch.md#55-fast-gate-time-two-rules-and-the-event-they-read-d-tw43))
  both fired during the burst; neither had a fault behind it other than the load stretching
  per-gate wall time and proof progress intervals.

This 2026-11-25 burst matches the `Start every ready gate` row of
[Section 5 of the gate capacity design](../design/gate-test-capacity.md#5-arrival-order-and-batching-against-running-ready-gates-at-once)
(18 to 21 minutes for the first done and 20 to 26 minutes across six gates together) and is
somewhat slower on the first finishes (25.1 and 26.2 minutes alongside a running full trunk proof).
Under ordinary operation without a store reset — and with the restart-wait hold already removed
([Section 2.1 of the gate capacity design](../design/gate-test-capacity.md#21-what-makes-several-gates-start-at-once))
— candidates finish staggered rather than arriving five at once.

---

## 4. What would have to change to revisit batching

From the comparison and measurements above, batching would be worth reopening with new data if the
inputs to the trade-off changed in these ways (an analytical note from this research record, not a
ruling binding the owner):

1. **A much lower per-candidate gate failure rate.** Batching wins on wall time only when almost
   every combined tree passes on its first run. At a 15% failure rate per candidate, 62% of
   six-candidate batches fail and pay roughly three extra bisection rounds. Pre-gate checks or
   narrower candidate changes would have to push the per-candidate gate failure rate well below 5%
   (for example to 2% or 3%, where a six-candidate batch passes on the first try `0.97^6 ≈ 83%` to
   `0.98^6 ≈ 89%` of the time) so that expected time including bisection beats concurrent
   single-candidate gates.
2. **Candidate-level attribution inside a combined tree.** If the gate could attribute a failing
   check or failing test inside a combined tree directly to the candidate whose diff caused it —
   for instance when batched candidates touch disjoint package graphs so each failing check's read
   set implicates one candidate alone — the gate could convict that candidate and issue passing
   verdicts for the unaffected siblings without multi-round bisection and without breaking
   [landing principle 3](../design/landing-principles.md#3-gate-what-will-be-the-tip-once) or
   [landing principle 14](../design/landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran).
3. **Frequent natural bursts without holding ready work.** Even with low failure rates or direct
   attribution, holding a ready candidate idle to wait for batchmates adds latency to staggered
   arrivals ([`R-GTC3`](../design/gate-test-capacity.md#R-GTC3)). Batching is only worth revisiting
   if steady-state lane throughput produces multiple Go-affecting candidates waiting at the gate at
   the same time without a store reset or restart hold, and concurrent per-candidate gates become a
   measured throughput or memory bottleneck even after the per-process hygiene and priority choices
   in [the gate capacity design](../design/gate-test-capacity.md) settle.
