# session-liveness

A headless harness for pi session *liveness*: from a state a session can reach, does it return to
idle within a bound, and does a later send-now actually start work?

It is the sibling of [`tools/queued-message-states`](../queued-message-states/README.md), which
measures which queued-message states are reachable and what each one does. This directory asks the
next question — whether a state can hold the session open forever — and it is **expected to be
green**.
Every spec that measures a leak is green against the current fork, and each one fails on its own
assertion (a bounded race) instead of timing out the file. `open-run` pins the unbounded contract:
the turn-stall watchdog was removed outright (owner ruling 2026-10-20, fork patch `0154`), so a
silent open run stays open with no notice, and the spec asserts exactly that.

## Prerequisite

The same as the sibling harness: the fork checkout and its installed dependencies.

```console
$ bin/pi-forks setup --fork pi    # materializes .forks/pi from the patch series
$ (cd .forks/pi && npm ci)        # only when its node_modules is missing
```

`PI_FORK` names the checkout under test: the `run` script, the vitest binary, the
`@earendil-works/*` package aliases and the `@fork/coding-agent/*` aliases that reach the harness's
own sources all resolve through it, so `PI_FORK=$PWD/.forks/candidate/pi just session-liveness`
judges that checkout and nothing else. The gate hands a candidate checkout there rather than the
shared `.forks/pi`, and a spec that imports a fork path literally is refused by
[`tools/fork-check.py`](../fork-check.py): a relative import resolves against the spec's own
location, so it reads the shared working copy however `PI_FORK` is set.

## Usage

```console
$ just session-liveness
$ tools/session-liveness/run
$ tools/session-liveness/run stuck-operation      # filter by file name
```

The run prints vitest's per-test status and, for each failure, the assertion message that names the
leak. Exit status is 1 while the leaks exist; that is the measurement, not a broken harness.

## The specs, and the leak each one pins

| Spec | State | What it asserts |
| :--- | :--- | :--- |
| `compaction-only abort never settles` | a real compaction running (`ignoreAbortSignal`), no agent run, a queued user message | `sendAllQueuedNow()` settles: `_abortCurrentOperation` arms the `turnSettleBoundMs` bound for an abort with no run, so the compaction that ignored its abort cannot hold `waitForIdle()` forever. |
| `an abandoned compaction stops holding the session` | the same stuck compaction, after the session gave up on it | `isCompacting` is false, `isIdle` is true, and a prompt is accepted again: abandoning the operation clears the compaction registration rather than only releasing the wait. The manual controller is the one `prompt()` refuses on, so the prompt is the state's real proof. |
| `timed-out settle leaves the idle wait unresolved` | a cancelled run that retires before the 100ms bound, while auto compaction keeps `isCompacting` true | The bound fires and `_releaseTimedOutTurn()` takes its early return (no run token, no active run). The abandonment it performs resolves the idle wait it was created for, leaves no wait pending, and the next plain `waitForIdle()` — armed with no watchdog, because a new run cleared `_agentRunAbortRequested` — settles. |
| `a later send-now is a no-op after a stuck operation` | the first spec's stuck operation | The stored operation is cleared when it is abandoned or superseded, so a later send-now starts a new operation within 2000ms. |
| `a branch-summary-only abort is bounded and leaves nothing compacting` | a branch summary running (`ignoreAbortSignal`), no agent run, no compaction | `abort()` settles within the bound, and afterwards `isCompacting` is false and the session is idle: the bound covers a summarization-only abort, and the abandonment clears it. |
| `an open run that produces no event is never bounded` | a run whose provider emits `message_start` and then nothing | No leak: within 5000ms the session emits no `agent_end`/`agent_settled` and no diagnostic naming a stall, and the run is still streaming. The turn bound is armed only after an abort, so a run nobody cancelled has no bound at all — the unbounded contract after the `0154` removal. |
| `interactive send-now reports which state it is in` | (a) the flag stuck with nothing in flight, (b) the flag stuck while an abort is in flight | The two states print different statuses, and (a) does not claim a cancellation is running while nothing is in flight. |

Each spec asserts its own cause, not just the timeout: it reads the private state the claim names
(`sessionInternals()`: `_agentRunAbortRequested`, `_idleWaitPromise`, `_immediatePromptPromise`),
and a spec that would otherwise assert only on a timeout reads the cause in its red branch and
fails on that claim instead. A bare test timeout would not be evidence, so every await is a bounded
race (`settleWithin`, `bounded`, `waitFor`, or `nextTurn(ms)`).

## Files

| File | What it holds |
| :--- | :--- |
| [`harness.ts`](./harness.ts) | A copy of the sibling harness's fakes — the controllable `streamFn` `AgentSession` and the real-prototype `InteractiveMode` double — plus the seams these specs need: `Turn.startThenSilence()`, `setSessionAutoCompacting()`, `startStuckCompaction()`, and the bounded-observation helpers. |
| [`stuck-operation.test.ts`](./stuck-operation.test.ts) | The compaction-only hang, the abandoned compaction that must stop holding the session, the idle wait a timed-out settle must resolve, the branch-summary-only abort, and the stored operation that makes a later send-now a no-op. |
| [`open-run.test.ts`](./open-run.test.ts) | A run that opens and emits nothing. |
| [`send-now-status.test.ts`](./send-now-status.test.ts) | The two states `handleSendNow`'s refusal sentence has to tell apart. |
| [`vitest.config.ts`](./vitest.config.ts) | The fork's source aliases, with the repo root as vitest's root so specs can live outside the checkout. |
| [`tsconfig.check.json`](./tsconfig.check.json) | The fork's compiler options over these files, for a type check. |

## Seams this harness cannot express

- **A real auto compaction that outlives its abort.** `isCompacting` is set by the auto, manual,
  and branch-summary controllers. The unresolved-idle-wait spec needs compaction running with no
  agent run, and a real auto compaction needs a threshold or overflow that cannot be produced on
  demand, so it sets the auto controller directly (`setSessionAutoCompacting`), the same technique
  the sibling harness uses for the manual one. The other two compaction states are driven for real:
  `startStuckCompaction()` runs a manual compaction whose summarization request ignores its abort,
  and the branch-summary spec runs a real `navigateTree(..., { summarize: true })`.
- **A compaction that ignores its abort, without ignoring every abort.** `ignoreAbortSignal` applies
  to every turn the harness stream serves. The compaction-only spec needs the summarization request
  to ignore its signal; nothing else runs in that spec, so the coarseness does not reach another
  state.
- **A run-level stall diagnostic's shape.** No such diagnostic exists by design — the turn-stall
  watchdog was removed (fork patch `0154`) — so the open-run spec asserts `stallDiagnostic()` stays
  `undefined`. A reintroduced run notice would own the wording. A **tool-call** notice does exist
  (`tool_call_stall`, see [`docs/pi/tool-call-bounds.md`](../../docs/pi/tool-call-bounds.md)), and
  it reports a call rather than a run: no spec here holds a tool call open past its threshold, so
  none of them observes it.
