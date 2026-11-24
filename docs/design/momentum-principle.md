# The momentum principle — the pile is the only throttle

Status: owner ruling, 2026-11-12. Applies to the tillr loop and to anything that supervises it.

## The ruling

> "I don't want max ticks, or max spend, or anything like that. This is a service that spins up,
> and maintains whatever forward motion it can to preserve a momentum towards the entire pile
> being empty, regardless of what the next steps are for things, as fast as possible, through all
> sorts of parallelism and cleverness. […] If you don't want work done, don't add it to the pile,
> we don't artificially pause until things are done."

## The principle

**The pile is the only throttle.** The service runs continuously and keeps whatever forward
motion it can toward an empty pile, as fast as the work allows, in parallel where the work is
independent. It does not stop because a counter reached a number, because a budget was spent, or
because a night ended.

A limit on how much work the service may do is a limit on the pile, not on the service. If a
piece of work should not be done, it does not go in the pile. If it is in the pile, the service's
job is to finish it.

## What this forbids

- **A tick budget.** A loop that stops after twenty ticks stops with work in the pile, which is
  the one outcome the service exists to prevent.
- **A spend or token ceiling.** A budget that stops good work is a wall an operator has to debug
  at 3am, and the number it compares against is a guess about work nobody has done yet.
- **Any other artificial pause.** A "cool-down", a "quiet hours" window, a "max concurrent runs"
  chosen for comfort rather than for a measured resource limit. A limit that exists because a
  resource is genuinely finite (file descriptors, disk, a provider's rate limit) is not an
  artificial pause: it is a fact about the world, and it is stated as one, with the measurement
  that produced it.
- **Treating "nothing to do right now" as completion.** A drained queue is not the end of the
  service; it is the service waiting for the next item. A supervisor that reads "drained" as
  "finished" stops the service exactly when it should be idle but alive.

## What replaces the walls

Removing the walls does not remove the need to stop *sometimes*. The distinction is between a
**fault** and a **budget**:

- A **budget** says "you have done enough". Forbidden. There is no such thing here.
- A **fault** says "you cannot proceed". Allowed, and it must be loud: a store that cannot be
  opened, a boundary violation, a backend that does not exist, a gate program that cannot run.
  A fault stops the *attempt*, never silently, and never in a way that reads as success.

Between those two sits the case that actually happens: **the machinery is broken but the work is
fine.** A harness that fails to start, a provider that refuses, an adapter that cannot parse its
own report. The answer is not to stop and not to spin: it is to **report the failure, back off,
and try again**, because the pile still has work in it and the fault is not the work's fault.

So the rule for a repeated fault is a **backoff, not a count**: after consecutive infrastructure
failures the loop waits longer between attempts, reports each one, and keeps trying. A backoff is
not an artificial pause on work: it is the absence of work to do, made visible. It must never
apply while there is a dispatchable item the loop could be running.

## The one legitimate stop

An **operator stop**: a signal, a `tillr drain stop`, a human killing the process. That is the
owner of the pile deciding to stop, which is the only authority the principle recognizes.

## How the loop enforces it

1. `tillr drain loop` has no tick, spend or token budget. It refuses `--max-ticks`,
   `--max-spend-usd` and `--max-tokens`, and has no stop reason that names a budget
   (`TestTheLoopCommandRefusesTheDeletedBudgets` in
   [`tools/tillr-drain/spend_line_test.go`](../../tools/tillr-drain/spend_line_test.go);
   `TestTheLoopHasNoTickBudget` in
   [`tools/tillr-drain/drain/loop_test.go`](../../tools/tillr-drain/drain/loop_test.go)).
2. The loop stays alive and waits when nothing is dispatchable (`Run` in
   [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go));
   `--exit-when-drained` opts into exiting instead. Its stops are:
   - `queue-drained`, only under `--exit-when-drained`, when nothing is dispatchable and no lane
     is held;
   - `operator-stop`;
   - `lanes-leased`, when a lane it may dispatch onto is held with no expiry, so `tillr run
     release` is required (`StopLanesLeased`;
     `TestALaneHeldWithNoExpiryStopsTheLoopAsLanesLeased`);
   - `fault`, when a loop step cannot proceed.
3. A repeated infrastructure failure earns a backoff with reporting, not an exit (`BackoffDelay`
   in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go);
   `TestBackoffDelayDoublesUpToItsCap` in
   [`tools/tillr-drain/drain/spend_test.go`](../../tools/tillr-drain/drain/spend_test.go)).
4. **Parallelism is the point, not an optimization.** The loop dispatches onto several lanes at
   once (`--lanes` in [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go);
   `TestTwoLanesRunConcurrentlyInOneBatch` in
   [`tools/tillr-drain/drain/loop_test.go`](../../tools/tillr-drain/drain/loop_test.go)), and the
   boundary check is scoped to one run's lane so that lanes do not serialize each other. A service
   that ran one item at a time would not be "as fast as possible" no matter how long it ran.
5. Spend is **measured and reported**, per run and in total (`spendLine` in
   [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go);
   `TestTheSpendLineNamesTheMeasurement` in
   [`tools/tillr-drain/spend_line_test.go`](../../tools/tillr-drain/spend_line_test.go)).
   Measurement is introspection, not a wall: seeing what the work cost is a different thing from
   refusing to spend it.

## What this does not change

- The gate still judges every candidate. Speed comes from parallelism and from not re-running
  what has not changed, never from judging less.
- A candidate that fails is still a failure, and is never retried blindly forever. A gate
  refusal records no failing criterion verdict and returns with its evidence to the item's worker
  ([`tools/tillr-drain/drain/returned.go`](../../tools/tillr-drain/drain/returned.go)). What stops
  endless repetition is an identical repeat, which escalates to the `design` seat, or blocks on
  the owner when no design seat is configured
  (`TestAFailedCandidateGoesBackToItsWorkerUntilItRepeats` and
  `TestADrainThatRefusedEveryCandidateNamesItAsARepeatedRefusal` in
  [`tools/tillr-drain/drain/loop_test.go`](../../tools/tillr-drain/drain/loop_test.go)). The
  principle is about the service's appetite for work, not about pretending bad work is good.
- The boundary check still catches a write outside the lane, and holds the offending item as
  `host-boundary-violation` without gating its candidate (`StopHostBoundaryViolation` in
  [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go);
  `TestAWriteToALaneOutsideTheRunIsCaught` in
  [`tools/tillr-drain/drain/loop_test.go`](../../tools/tillr-drain/drain/loop_test.go)).
