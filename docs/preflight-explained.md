---
title: Preflight, explained
date: 2026-10-30
---

# Preflight, explained

Short version: you had never heard of this because nobody chose to turn it on — a test did, on every run — and on 2026-10-30 it left 13 CPU-burning processes behind. But the leak is not the main problem. The main problem is that the thing it tries to test cannot be tested. Verdict, in section 5: delete it.

## 1. What is "preflight"? Why does it exist at all?

A "gate" is the set of checks — a check is one automated test that can pass or fail — which a candidate (a proposed change) must pass before it lands on the trunk (the main shared branch). "Preflight" is this tool's own name, not a standard term, for an optional extra mode of that gate: it re-runs the same checks under deliberately bad conditions, in three "cells", a cell being the tool's word for one run under one bad condition. It was built to catch a "false green": a check that passes (green) only because the machine was idle, and would fail in a real gate run where other checks are running alongside it. The usual suspects are checks that assert something about elapsed time — how long a thing took by the clock on the wall.

## 2. Why does it burn CPU? Is that really necessary?

One cell, `gate-parallel`, creates "contention": more processes wanting a CPU core than there are cores, so each sometimes has to wait to run. It does this with "burners" — this tool's own coinage — processes that do nothing but spin:

```
python3 -c "marker='fork-check-preflight-burner'; while True: pass"
```

The idea is that a check saying "this must finish within 30 thousandths of a second of real time" passes easily when a core is free and can miss when it must wait its turn. The burners run at normal priority on purpose; lowered priority would make them step aside, gentler than a real gate. They are started detached — in their own process group — on purpose, so Ctrl-C does not kill the load mid-run, and the run stops them at the end in a cleanup step that executes only if the run finishes or returns. So burning CPU is necessary *for this method*. The next section is why the method itself is unsound, however carefully it is run.

## 3. Surely we can test this another way — can we?

No — not the thing it claims to test. In your words: "you don't know how fast a machine is that you're going to run on." An assertion on elapsed time measures the machine, not the code: the scheduler (the part of the operating system that picks which process runs next), whatever else is running on the box, how hot the chip is and whether it is slowing itself down, the kernel (the operating system's core). It passes or fails according to the box. Widening the margin does not fix this: measuring one second against a budget of 30 seconds is the same error with more room. And the budget itself — 30 seconds, 45 minutes — is a policy choice, a number someone picked. No test can validate that choice; only watching the system in the real world does, late and noisily.

What *can* be tested, precisely because it does not depend on the machine:

- **The deadline's reaction, with a simulated clock.** The code reads a pretend clock that the test controls and can jump forward instantly. Set it to one minute past a 45-minute deadline and assert that the timed-out work is retired exactly once, with the right reason recorded, and handed to its next step. This runs in millionths of a second and validates the *mechanism*. It does **not** show the code is fast enough, and must never be described as showing that.
- **Counts, order, and state.** How many operations were performed, how many allocations, how many processes or threads exist, in what order events happened, what the state is after a given number of steps. These are properties of the code; the machine cannot change them.

Elapsed time has two legitimate homes, neither of them a pass/fail check: a benchmark (a timing run on named, specified hardware, labelled as measuring that machine) and production telemetry (measurements collected from the real system while it runs). The corresponding rule is simple: ban elapsed-time assertions inside checks entirely — not just the obvious abuse of waiting a fixed time and hoping something else has finished.

## 4. When does it run? How often does it run? Who opts into it?

A normal gate run does **not** run the preflight. It is opt-in via `just preflight` or `fork-check --preflight`. That was not true when this document was written: `tests/fork-check.test.py` called the real preflight six times, each `--only structural` with a `--load`, and because that self-suite file is itself a gate check, the preflight ran on **every** gate run — every landing pass and every `just done` — in batches of one or two processes for a few seconds each. Nobody opted into that; it was a test calling a mode. Those invocations are deleted, and [section 7](#7-what-was-removed-and-why-2026-10-31) records the removal.

On 2026-10-30 at ~01:37 UTC, 13 such burner processes were found orphaned (their parent process was gone, so PID 1, the system process that adopts leftovers, owned them), no suite running, 38 minutes old, holding the machine's load average (roughly, how many processes are running or waiting) at 69.44 on 12 cores. They were killed. They had started seconds apart, matching one suite run's sequential calls leaking rather than one large batch. The likely cause: a per-check timeout kills the parent's process group, the cleanup step therefore never runs, and the detached burners survive precisely because they were detached. While they spun, every wall-clock measurement the system reported was invalid, and that silently contaminated two hours of your time. This leak is a symptom of the mechanism being wrong, not the whole of the problem: a leak-free version would still be answering a question nobody can answer.

## 5. Keep it, shrink it, run it only on demand, or delete it?

**Delete the burner cell.** Its premise is unfalsifiable, meaning no outcome can ever settle what it claims to settle: "this check went red under load" cannot distinguish "this check is broken" from "this box was slow". The verdict's subject is unknown. This system already forbids exactly that move one level down — a non-answer about a candidate may not be recorded as a verdict on that candidate — and preflight does it one level up, about the machine. So it is not merely expensive: no cleanup fix, no schedule change, and no "on demand only" would make it sound, because those address when it runs and whether it leaks, not whether its answer means anything.

What deletion costs is therefore smaller than it looks: no sound signal is lost, because there never was one. What it appeared to catch were checks that should never have asserted elapsed time in the first place. There is one on the books now — `herdr-native-placement`, with its 30 ms real-time assertion — currently "quarantined", where quarantine means its named failing cases stop making their check red while the test file keeps the content the quarantine was declared against, and each gate report still names and counts them apart from passes. (This sentence first gave an end date, 2027-01-26; the gate no longer reads a date, as of 2026-11-21.) The fix for that check is not more stress; it is to rewrite it around a simulated clock or a count, as in section 3, or drop the assertion.

## 6. If it stays in any form, what is the smallest safe form, and what is the one test that proves a killed run cannot leave load behind?

It should not stay, and there is no form of it that is both kept and sound. The only form that cannot leave load behind is the one that spawns nothing. So this section is harm reduction for the interval before deletion lands, not a reason to keep it: remove the six automatic calls from the self-suite first (that stops the unconsented every-run burning), and if a manual run is still possible during that interval, each burner must carry its own expiry a few seconds past its cell's end and stop itself then. Checked: no such self-expiry, parent-death, or watchdog mechanism exists today; the end-of-run cleanup is the only stop.

The one proving test: start it, hard-kill the parent process in the way that runs no cleanup at all, wait past the expiry, and count processes carrying the marker. The count must be zero. Note what kind of test that is — a count of processes, the same machine-independent kind section 3 says is valid. It can prove the load is gone. Nothing can prove the original verdict meant something, which is why the cell goes.
## 7. What was removed, and why (2026-10-31)

The verdict in section 5 was carried out, and section 6's harm reduction was skipped because
the deletion landed in one change. This section is the record of what went and what replaced
it, so a reader does not have to reconstruct it from a diff.

**The burner cell is gone.** `gate-parallel` was deleted from `tools/fork-check.py`, and with
it every piece of machinery that existed only to feed it: the `start_load` and `stop_load`
functions, the `BURNER` program and its `BURNER_MARKER`, the `--load` option, the `--jobs`
override that only the parallel cell read, and the per-cell batch-size and worker-count lines
in the report. A preflight cell is now an environment and nothing else. Two cells remain:
`baseline`, the caller's own shell, and `gate-serial`, the gate's environment — HOME and
XDG_STATE_HOME scrubbed, `BOARD_STORE_STRICT` set — with one check at a time. The preflight
still answers the one question a developer's shell cannot: "does this check only pass in my
shell?" It no longer claims to answer "is this check broken or is the box slow?", because
section 3 is why that question has no answer.

**The self-suite no longer invokes the preflight.** `tests/fork-check.test.py` called the real
preflight six times, each with `--only structural` and a `--load`, which is what made every
gate run burn CPU nobody opted into. Those end-to-end invocations are deleted. The preflight
is now a manual diagnostic (`just preflight`, `fork-check --preflight`), and the self-suite
pins its matrix, its cell environments, its `--cells` and `--only` selection, and its
declarations file without running it. What that costs is stated plainly: the report's
classification and exit status are no longer exercised end to end by the gate. They are
exercised by hand, and the alternative — an automatic invocation that burns CPU on every
landing — is the defect this change exists to remove.

**The rule is enforced, not just written down.** Section 5's argument is a rule about the
whole gate, and a rule that lives only in prose is one an agent will rebuild past within an
hour: a load generator is the shortest way to satisfy "fix the flaky gate checks". So the
rule lives beside the gate as `GATE_TIMING_RULE` in `tools/fork-check.py`, with its reasoning
attached, and case 53 of `tests/fork-check.test.py` fails the gate when either half is
violated:

- **No check generates load.** The gate's own source must define no load generator and no
  preflight cell that carries a load or a batch size, and no tracked test file may contain the
  burner marker or start the preflight as a subprocess.
- **No check asserts elapsed time.** The scan looks for the shape that decides a verdict by
  the machine: a wall-clock delta — now minus a start captured before the work — compared to a
  bound. A deadline *wait* is not that shape; it compares an absolute time, and section 3's
  rule 3 permits it. Every hit must be declared in `tools/gate-timing-declarations.json` with
  its kind (`liveness` or `backlog`), its reason, and its repair. A hit with no declaration
  fails, and so does a declaration that no longer matches a hit, because a declaration that
  outlives its defect is a silencer.

The declarations file records eleven assertions on the day this landed: eight liveness
deadlines, each with a bound an order of magnitude past the operation's expectation, and three
real timing assertions that are the backlog — `tests/pi-profile-live.test.mjs`'s child-start
ceiling, `tools/bedrock-spend/internal/cli/cli_test.go`'s one-second watch bound, and
`yolo-packs/matt/tests/work-control-board-pile.test.mjs`'s "the wait was slower than the old
budget" assertion. Those three are the follow-through, not part of this change: each is
repaired by asserting what the product recorded instead of how long it took.

**What this does not do.** It does not rewrite the quarantined `herdr-native-placement` cases
(their 30 ms sleeps become awaits of the real event, or an injected clock), and it does not
rework pile items 191 and 196. Those are named in the item as follow-through. What it does is
make the rule the gate enforces, so the next agent handed "fix the flaky gate checks" cannot
reach for a load generator without a red gate telling it why not.
