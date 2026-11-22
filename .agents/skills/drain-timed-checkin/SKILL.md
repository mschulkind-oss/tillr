---
name: drain-timed-checkin
description: Use when a timer fires a drain check-in in this repository, when resuming the tillr drain after a session relaunch or a cleared session, or when setting up the recurring check-in timer — how the operating agent starts, checks, triages, files, alerts, steps lanes and reports.
---

# The timed drain check-in

A *check-in* is one pass, fired by a 15-minute timer, in which the operating
agent confirms the tillr drain is healthy, triages every open item, and acts on
what it finds before it reports. The *drain* is the `tillr-drain loop` process
that dispatches queue items to lanes; a *lane* is one worktree that one worker
uses at a time. **Where this skill and a binary's `--help` disagree, the binary
is right.**

This skill holds the operating **method** only: how to check, how to act, and
the standing rules the owner has given. It holds no priorities. What to work on
next comes from the owner and from the queue, never from here.

## Keep this skill current

A session with no history must be able to load this skill and operate the drain
the same way. So, in the same check-in that it happens:

- When the way of checking or acting changes (a new command, a new file, a new
  cause to look for, a tool that turned out not to exist), edit this skill.
- When the owner states a standing rule about how to operate, quote it here
  with its date, and record it verbatim in the rulings file as well.
- Leave out anything that is true only today: item IDs, what is queued, what
  to do first. Those belong in the rulings file.
- Commit the edit on its own as `docs(skills): ...` and check the file with
  `uvx vantage-check` on its path.

## Where the session's files are

These are locations, not evidence. They live outside the tracked tree.

```bash
export PATH=/workspace/.yolo/durable/bin:$PATH    # built tillr binaries first
S=.tillr/state.db                                 # the store; tillr CLI only, never opened directly
N=/workspace/.yolo/durable/drain-recovery/night
#   $N/owner-rulings.md                  owner words verbatim, operator decisions, each check-in's record
#   $N/run-keeper.sh LANES POLICY        starts the keeper, which starts and keeps the loop
#   $N/backend-policy.copilot.abs.json   the policy file the loop runs
#   $N/loop-watch.py                     the loop watcher
#   $N/keeper-HHMM.log                   the loop's own messages (skip lines starting tillr-sqlite)
R=/workspace/.yolo/durable/drain-recovery/ratchet
#   $R/sample.py      lane sampler, one sample every 10 s into $R/samples.jsonl
#   $R/report.py MIN  busy and idle share per lane over the last MIN minutes
/workspace/.yolo/durable/drain-recovery/why.json  # the last saved `tillr drain why --json`
```

Read the tail of the rulings file first in a new session: its newest entries say
what lane count the loop last ran, what was decided, and what was owed.

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
- **This is not only about models (owner, same day): "That is everything. ...
  If something fails, you fix it. If it fails loudly, you fix it. You don't hide
  it with a fallback."** Do not write, keep or approve a fallback path of any
  kind. A fallback needs a strong written justification and the owner's own
  agreement before it exists.
- Changing a model is a deliberate policy-file edit, never a per-item retry on
  "another model". The running loop accepts a valid policy file when it changes.
- The operating agent's own helpers: a cheap model for well-specified analysis,
  a mid-tier model for research, and its own token use kept low.

## The bar (owner, 2026-11-20)

"You should be able to operate this drain in a way such that changes land in
nearly the same speed as if you were running yourself as sub-agents. There
shouldn't be random pauses, and if there is, that's a bug that you fix." Every
idle minute on a lane that has schedulable work is a defect with a cause to find.
Owner, 2026-11-21: "I do'nt wnt 'haven't chased yet', just go do it. that's why
you're here."

## Start or resume

Six processes belong to a running session: the keeper, the loop, the loop
watcher, the lane sampler, the Observatory (the web view of the queue, on port
4318), and one `tillr watch wait` watcher. A session relaunch kills them. Check
for all of them first and start what is missing.

```bash
ps -eo pid,etimes,args | rg 'tillr-drain loop --root /workspace --gate|tillr-keep|loop-watch.py|ratchet/sample.py|tillr-view serve|tillr-watch wait' | rg -v 'rg '
printf 'say ok' | claude -p --model fable          # the designer backend answers
TILLR_BUILD_UNPROVED=HEAD just tillr-build         # only when the loop is down
```

- **Start each detached with Python**, because `setsid` is not installed here:
  `subprocess.Popen([...], stdin=DEVNULL, stdout=DEVNULL, stderr=DEVNULL,
  start_new_session=True, cwd='/workspace')`. The keeper command is
  `bash $N/run-keeper.sh <lanes> $N/backend-policy.copilot.abs.json`; the others
  are `python3 $N/loop-watch.py`, `python3 $R/sample.py` and
  `just observatory 4318`.
- **Lane count on start** is the count the loop was last running, read from the
  newest entries of the rulings file.
- **The loop restarts itself in place** onto each newly built binary; its
  process ID stays the same and its log says "restarted in place onto build".
  `tillr drain why` raises a `stale-loop-build` alarm while the running loop is
  older than the installed binary. Do not restart the loop by hand for a new
  build.
- **When the loop must be stopped by hand:** ask the keeper to stop
  (`python3 tools/tillr-roll/tillr-keep --stop`), then `tillr drain stop`, then
  wait for the loop process to exit. Never signal a `tillr-run` process and
  never end a worker. A loop that has closed its store, has no child process
  and still does not exit may be killed by its own process ID only; record that
  in the rulings file with the evidence.
- **The watcher** is started as a background task of the operating session, so
  that its exit wakes the session:
  `tillr watch wait --store .tillr/state.db --rules config/tillr/watch --owner operator-session`.
  Do not pass `--follow`: that form never exits and so never wakes anyone.
  When it exits with an alert, act on the alert and start it again. Restart it
  whenever a new rule file lands in `config/tillr/watch`, so it loads the file.

## Each check-in

Gather first, in one command, then act on everything it shows.

```bash
date '+%H:%M:%S %Z'                                # report times in Eastern, read from here
ps -eo etimes,args | rg '^ *[0-9]+ tillr-run exec' # live workers; the run name is in the --input path
tillr drain why --store $S --root . --lanes <count> --json > /workspace/.yolo/durable/drain-recovery/why.json
python3 $R/report.py 30
git log --since=<last check-in> --format='%h %ad %s' --date=format:%H:%M
```

In the saved `why` output: `trunk_line` is absent when the trunk tip is proved
green; `alarms` lists standing alarms; each row has `item_id`, `state`,
`position` (queued, running, candidate, held), `reason`, `why`, `mover` and
`refusals`. Read only rows whose `state` is `in-queue` or `blocked`.

1. **Health.** All six processes are up. The loop did not exit. The trunk line
   is absent or shows a short lag with nothing failing.
2. **Count workers from the process list**, never from `tillr drain why`, which
   has reported dead runs as running. For each ordinary lane say whether a
   worker is live. A test binary under a run's directory can also be named
   `tillr-run` or `tillr-drain`; count only the processes whose `--input` names
   a request under the lanes directory.
3. **The trunk proof.** When the trunk is unproved for more than a few minutes,
   look at what the heavy runner is doing, not at the summary:

   ```bash
   ps -eo pid,etimes,pcpu,args | rg 'heavy-supervisor|go-test-shards.py tools' | rg -v 'rg |lanes/lane|express'
   rg -v '^tillr-sqlite' <newest keeper log> | rg 'heavy runner fault|restarted in place' | tail
   ```

   - A single Go test running for minutes at 0% CPU is a deadlocked test. Go
     ends it at ten minutes and prints every goroutine stack; read the stack
     from the heavy verdict under the repository's `.git/tillr-gate` directory
     and file the repair with the stack's location in the words.
   - "missing but locked worktree" means an interrupted `git worktree add` left
     a registration for the proof worktree with no directory. Confirm no
     `git worktree add` is running, then `git worktree unlock` that path and
     `git worktree prune`.
   - New work starts from the last proved green commit, and an item whose
     blocker just landed waits until that landing is proved (owner,
     2026-11-21: "you can only start a new item if the previous item's trunk
     landing went green ... and then you always just base an item on the most
     recent green on the trunk"; a trunk fixer starts from the red tip). So a
     stuck proof stops new work; treat it as blocking.
4. **Idle lanes.** An idle lane, a gap, or a step that took longer than it
   should gets a full root cause analysis in the same check-in (owner,
   2026-11-20: "you don't guess, you read everything before making a
   decision"). Before naming a cause or acting:

   - Read the run's own log to its last line, its report, and every recorded
     event for that run in order, with times
     (`tillr view events --store $S --since <seq>`; the events are under
     `payload.events`, and a `--since` above the head returns nothing).
   - Read the loop's messages for the same minutes: restarts, declined
     dispatches, holds, faults.
   - Account for every minute of the gap: who had the lane, what they were
     waiting on, and what ended the wait.
   - Check the measuring tool before believing it. The lane report prints an
     `UNTRUSTED` line when samples lack a waiting count or carry a worker on
     lane "?"; a window with that line proves nothing. Both have happened: a
     run mapped to no lane, and a status call that failed for an hour because
     the sampler found an older `tillr-drain` on its `PATH`. A helper that
     calls tillr must put the built binaries first on `PATH` itself.
   - Do not blame a model, a backend, or "a restart" from a summary line.
   - State what is measured and what is still unknown, and file the defect with
     the timeline in it.
5. **Triage every running or open item**, and act in the same check-in:

   - **Held for the operator** (`obstacle-seat`, `obstacle-tooling`, an owner
     hold): read the whole `why` and `mover` text. Decide what a person
     operating would decide, then send it back with
     `tillr drain retry --store $S --now <RFC 3339 time> --item <item> --run <the run named in the hold> --by operator --why "<the answer>"`.
     When the answer is "that part belongs elsewhere", say so in the answer and
     file the part as its own item blocked by this one.
   - **Faulted** (`drain.item-fault`, for example a decision collision): file
     the drain defect first, and run the `tillr drain clear-fault` command the
     hold names only after that fix has landed. A fault that returns after
     being cleared means the item's recorded state is the problem; close the
     item and refile it.
   - **Refused more than a few times:** read the verifier's reasons from the
     `drain.refusal` events. When each refusal names a new finding unrelated to
     the last, the item is too big; split it as described below. Do not send it
     round again.
   - **`conflict` in an item's refusals** counts `item.conflict` events. Each
     is a worker's *definition obstacle*: the request's words conflict with the
     code or with a design, so the design seat answers, usually with a design
     record that lands. It is not a merge conflict. Read the obstacle text in
     the event; when the request's own words caused it, that is a filing
     mistake to stop repeating.
   - **One step running a long time:** check that its processes are using CPU
     before doing anything. A working step is left alone.
   - **Finished elsewhere:** when an item's fix has already landed from another
     item or a trunk fixer, close it with the landed commit named.
6. **Keep the lanes fed.** When no item is waiting only for a lane, the queue is
   the limit. File ready, independent work that already has a design: slices a
   design says follow nothing, and single rules. Do not invent work.
7. **List what landed** since the last check, and deliver any owed Vantage
   replies for landed design documents that carry an owner ruling.
8. **Record the check-in** by appending to the rulings file (`cat >>`): the
   time, process and worker state, each finding with its evidence, each
   operator decision labelled as one, what was filed, and the lane step
   evidence.

## Filing work

Use `tillr item request` with the words on standard input and `--title`.
`--path` is ignored; name files in the words.

- **Express or ordinary.** A defect is express: omit `--ordinary`. Planned
  slices and single rules are `--ordinary`. Add `--to first` when the defect
  blocks other work. Drain-core changes use
  `--workflow single-worker@v1 --seat tricky`; design work uses `--seat design`.
  At most four `--criterion` flags; `--blocked-by <item>` chains slices.
- **File when found** (owner): never batch defects or wait for approval.
- **The smallest functional change (owner, 2026-11-21): "part of desgins can
  absoutely land. this is generally MORE healthy. Remember, we're trying to
  mirror humans here. Humans desire to land the smallest functional change
  possible to make reviewing, testing, velocity, all this stuff easier."** One
  behavior per item. A phrase such as "for every" or "all of" in a request
  makes a verifier find a new gap each time; name the one path instead.
- **Write the words so the item can land alone:** what happened, with the time,
  the command and its output; the one thing to change; "change nothing else";
  and an out-of-scope list that ends "a gap on one of those is not a reason to
  refuse this item; list it in the candidate note". Each criterion is one
  testable clause.
- **Splitting an item that keeps being refused:** save its last committed
  candidate with `git update-ref refs/keep/<name> <commit>` (for a fork, fetch
  the commit into the operator's own fork checkout under a `refs/keep/` ref),
  close the item with the reason, and refile slices that each say "take from
  that commit only what this part needs". Move items that waited on the closed
  one with `tillr item dep --add` and `--remove`. Closing an item ends its
  current run, so do it knowingly.
- **Do not write a request that a build worker cannot satisfy alone.** Do not
  tell it to edit a design document or mark a design slice built; that is the
  design seat's. Do not make a criterion of stating a cause the worker cannot
  establish from the code; give the evidence and ask for the fix. Each of
  these sends the item to the design seat as a definition obstacle.
- **No retry counts, attempt limits or time limits** in any fix (owner). A
  failure gets its root cause. A test that fails under load is broken.
- No agent adds or edits a `<!-- tillr: item` tag.

## Alerts

`tillr-watch` reports conditions from store events; its rules are TOML files in
`config/tillr/watch`. It applies no remedy (owner: "We don't cover issues. We
use this to find the issues and fix them").

- **Owner, 2026-11-21: "Apart from just creating a list of brainstormed alerts
  or however you want that you can think of ahead of time, every time you see a
  fault that you would have otherwise like to know about you should be filing an
  item to add an alert for that."**
- So each fault found at a check-in gets two filings: its fix, and one rule
  that would have reported it. One rule per item, one new file per rule.
- A rule can only read a registered event or a projection. When the fault is
  only a line in the loop's log, file the item that makes the drain write an
  event for it, and block the rule item on that.
- An alert that has never fired is unproven. Say so when reporting on it.

## Lane steps

The owner sets the target lane count. The operating agent steps toward it one
lane at a time, and only when all four hold over the last 30 minutes:

1. No ordinary lane was idle more than 10% of samples while work waited only
   for a lane.
2. Every idle stretch over 2 minutes with work waiting has a root cause that is
   filed.
3. The trunk proof was green and the loop did not exit.
4. At least one item was waiting only for a lane.

A window that contains a red trunk, a stuck heavy runner, an operator restart or
a sampler fault starts again from when that ended. Step with
`tillr drain scale --lanes <count>` on the running loop; no restart is needed.
Record each step and its evidence in the rulings file.

## Reporting to the owner

- Report only what changed or is wrong, in a few lines, with times in Eastern.
- Describe each item and commit by what it does. A hex ID may follow in
  parentheses; it never stands alone. When an item's subject is itself a tool,
  write "the item that does X", so the tool does not read as the actor.
- Say plainly what was not checked, and what is believed but not confirmed.
- Never ask the owner to run a command or to bless a decision already made. Act
  as the operator and record the evidence; the rule is
  [Authority, not approval](../../../docs/reference/authority-not-approval.md).
- Record every owner statement verbatim in the rulings file, and label operator
  decisions as operator decisions.
- A wrong statement made to the owner is corrected in the next message, as a
  correction.

## Standing limits

- The store is read and written through the tillr CLI only.
- Never signal a `tillr-run` or `stop-at-gap` process. Never move `main`
  backwards. Only the drain's lander moves a patch branch. Do not edit a fork's
  cache checkout.
- Stale lane worktrees are the agents' to remove; the owner has none.
- Scratch goes under the durable directory, never `/tmp`. Tracked Markdown
  never cites the durable directory as evidence.
- Do not run the heavy suites by hand; a few named tests are fine.
- Web searches carry nothing from the repository or the owner.

## Setting the timer

Create one recurring session timer at a 15-minute period whose prompt says:
"Load the `drain-timed-checkin` skill and run one check-in." Replace an older
timer rather than adding a second.
