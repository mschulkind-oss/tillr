---
name: drain-timed-checkin
description: Use when a timer fires a drain check-in in this repository, when resuming the tillr drain after a session relaunch, or when setting up the recurring check-in timer — the model policy, the lane count, the triage steps, and how to report to the owner.
---

# The timed drain check-in

A *check-in* is one pass, fired by a 15-minute timer, in which the operating
agent confirms the tillr drain is healthy, triages every open item, and acts on
what it finds before it reports. The *drain* is the `tillr-drain loop` process
that dispatches queue items to lanes; a *lane* is one worktree that one worker
uses at a time. **Where this skill and a binary's `--help` disagree, the binary
is right.**

## Model policy (owner, 2026-11-20)

| Seats | Backend | Model |
|---|---|---|
| design, plan, review | `claude-designer` | Fable 5.2, run through `claude -p` |
| build, tricky, verify | `pi` | Sol 6.2 on Bedrock |
| edit, test-write, docs, scout, test-run | `pi-build`, `pi-scout` | Terra 6.1 on Bedrock |

- Every designer is Fable 5.2 through `claude -p`. A designer never implements.
- **No fallbacks (owner, 2026-11-20): "We need deliberate choices, not fallbacks.
  We need to fix what's wrong."** Every seat list in the policy file names
  exactly one backend. When a backend fails or closes, the work holds with its
  cause; find and fix the cause. Never add a second backend to a seat, and never
  move an item to another model to get past an error.
- Changing a model is a deliberate policy-file edit, never a per-item retry on
  "another model". The running loop accepts a valid policy file when it changes.

## Lanes (owner, 2026-11-20)

Run **2 ordinary lanes** plus the 2 express lanes. The goal is that 2 means 2:
each ordinary lane has a live worker. Do not raise the count until both stay
full across several check-ins.

## The bar (owner, 2026-11-20)

"You should be able to operate this drain in a way such that changes land in
nearly the same speed as if you were running yourself as sub-agents. There
shouldn't be random pauses, and if there is, that's a bug that you fix." Every
idle minute on a lane that has schedulable work is a defect with a cause to find.

## Start or resume

A session relaunch kills the loop, its keeper, the watcher and the Observatory
(the web view of the queue). Check for all four first and start what is missing.
The commands, run from the repository root with the built binaries first on
`PATH`:

```bash
export PATH=/workspace/.yolo/durable/bin:$PATH
N=/workspace/.yolo/durable/drain-recovery/night
ps -eo args | rg 'tillr-keep --|tillr-drain loop --root|loop-watch.py|tillr-view serve'
printf 'say ok' | claude -p --model fable          # the designer backend answers
TILLR_BUILD_UNPROVED=HEAD just tillr-build         # only when the loop is down
# start each with python subprocess.Popen(..., start_new_session=True), stdio to DEVNULL:
#   /bin/bash $N/run-keeper.sh 2 $N/backend-policy.copilot.abs.json
#   python3 $N/loop-watch.py
#   just observatory 4318
```

Stop cleanly before a rebuild: ask the keeper and the loop to stop, wait for the
live `tillr-run` processes to end, and only then start again. Never signal a
`tillr-run` process.

## Each check-in

1. **Health.** The newest keeper output has no repeated "loop exited". The
   `green=` value in the newest keeper log advances; when it does not, find the
   failing after-merge check and fix or file it.
2. **Count workers from the process list**, never from `tillr drain why`, which
   has reported dead runs as running. For each ordinary lane say whether a
   worker is live; when one is idle, find the cause and fix or file it now.

   ```bash
   ps -eo etime,args | rg '^ *\S+ tillr-run exec'
   ```

   **An idle lane, a gap, or a step that took longer than it should gets a full
   root cause analysis in the same check-in (owner, 2026-11-20: "you don't
   guess, you read everything before making a decision").** Before naming a
   cause or acting:

   - Read the run's own log to its last line, its report, and every recorded
     event for that run in order, with times.
   - Read the loop's messages for the same minutes: restarts, declined
     dispatches, holds, failed restart checks.
   - Account for every minute of the gap: who had the lane, what they were
     waiting on, and what ended the wait.
   - Do not blame a model, a backend, or "a restart" from a summary line. A run
     that ended with an error is not evidence that its work was bad or lost.
   - State what is measured and what is still unknown, and file the defect with
     the timeline in it.

3. **Triage every running or open item.** Export the events since the last
   cursor and read each item's timeline. Any item with a verify refusal, more
   than one cycle, more than three worker-hours unlanded, an instant run, a
   failover-suffixed run, a fence or gate refusal, or an operator hold gets a
   stated cause and an action in the same check-in.
4. **File every defect at once**, express (omit `--ordinary`), placed first when
   it blocks other work. Drain-core changes use
   `--workflow single-worker@v1 --seat tricky`.
5. **List what landed** since the last check, and deliver any owed Vantage
   replies for landed design documents that carry an owner ruling.
6. **Keep unlanded commits.** Before a lane is reset or the loop restarts, put a
   `keep/` branch on any lane head that is ahead of the trunk.

## Reporting to the owner

- Report only what changed or is wrong, in a few lines, with times in Eastern.
- Describe each item and commit by what it does. A hex ID may follow in
  parentheses; it never stands alone.
- Leave nothing as "not yet looked at".
- Never ask the owner to run a command or to bless a decision already made. Act
  as the operator and record the evidence; the rule is
  [Authority, not approval](../../../docs/design/agent-runtime.md#28-authority-not-approval).
- Record every owner statement verbatim in the rulings file, and label operator
  decisions as operator decisions.

## Setting the timer

Create one recurring session timer at a 15-minute period whose prompt says:
"Load the `drain-timed-checkin` skill and run one check-in." Replace an older
timer rather than adding a second.
