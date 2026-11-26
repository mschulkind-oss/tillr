# {{ITEM_ID}} — {{TITLE}}

You are running unattended: nobody reads or answers your messages while you work. Your
first message without a tool call ends the run, so keep working with tools until the goal
in **Report shape** is met or one of the obstacles it names holds.

<!--
The brief a lane receives for a definition that completes without landing (for example,
via a completion hook in a plain directory). Instances are UNTRACKED: plain-directory
staging renders this file into the lane directory as `brief.md` and fills every
double-brace placeholder. This template carries all ten `##` sections required by
`backends/preflight.sh` so built-in adapters such as `run-pi` accept it without
requiring each non-landing store to author its own template.
-->

## Header

- **Item:** {{ITEM_ID}}
- **Run:** {{RUN_ID}}
- **Backend:** {{BACKEND}}
- **Target:** {{TARGET}} ({{TARGET_KIND}})
- **Date:** {{DATE}}
- **Timeout:** {{TIMEOUT_SECONDS}}

## Task

{{TASK}}

The item's criteria, quoted verbatim. Every criterion maps to a verification line in
**Verification commands** below, or is marked here as not command-verifiable.

1. {{CRITERION_1}}

## Scope and boundaries

This store completes items without landing on a Git branch. Work inside the target
directory named in the header (`{{TARGET}}`) and your run's scratch directory
(`{{DURABLE_DIR}}`). Do not create Git commits or modify the store, other lanes, or
paths outside the target and scratch directories.

Running commands is permitted inside the target directory as needed to complete the task
and verify the result.

## Seats

These are seats tillr runs as nodes; a worker cannot start one.

{{SEAT_LINES}}

Give mechanical searching, editing and checking to the cheaper sub-agents your own harness offers.
Your harness's configuration chooses those sub-agents' models; tillr's policy does not.

## Settled decisions — do not redesign

{{SETTLED}}

If this brief is wrong about the workspace or inputs, name the `definition` obstacle with
the evidence; do not invent a mechanism.

## Facts carried

{{FACTS}}

## Method

{{METHOD}}

When you believe a criterion is met, append one line naming its criterion ID
(for example, `c1`) to the progress file named by `TILLR_PROGRESS_FILE` in this
run's scratch directory. Append; do not replace earlier lines. This is a worker
claim, not a verdict: the runner records every line as a `run.progress` event while you
work.

```bash
printf '%s\n' c1 >> "$TILLR_PROGRESS_FILE"
```

Never end processes by name or pattern: no `pkill`, no `killall`, no `kill` fed from
`pgrep`. End only a process you started, by the process ID you recorded when you started it.

## Verification commands

Run each of these.

{{VERIFICATION}}

## Report shape

The goal of this run is to finish the work in the target directory so the store's
completion rule can judge it, without making a Git commit. Do not end your turn until
that work and its verification have run.

The only other ending is an **obstacle**: a fact this run cannot change, of one of the
kinds below, together with what it needs. Each kind leads to a different action, so name
the one that holds:

| `OBSTACLE:` | When | Required lines | What happens next |
|---|---|---|---|
| `definition` | The item's own words are wrong or contradict the inputs | `CRITERIA:` the criteria affected; `EVIDENCE:` the contradiction, quoted | The design seat amends the item |
| `trunk` | A shared upstream dependency is broken, and this run did not cause it | `CHECKS:` the failing check ids; `BASE:` the base identifier | The loop handles the upstream failure |
| `tooling` | A tool or the harness is defective: a hung command, a broken adapter, a missing binary | `COMMAND:` the command; `EVIDENCE:` its output; `PATHS:` the tool's paths | The loop records the tooling defect |
| `seat` | You made the attempt, and it shows the task exceeds what this model can do | `TRIED:` the attempt you made and what its checks showed; `MISSING:` what you could not do | The item moves to a stronger model or design seat |
| `owner` | A decision only the owner can make | `QUESTION:` the question | The design seat answers it from the documents, or asks the owner |

Your final message on success is just `RESULT: done`. For an obstacle, give its
`OBSTACLE:` line and its required fields, each on a line of its own.
In either case the last line is exactly one of:

    RESULT: done
    RESULT: blocked

Nothing after it. `RESULT: done` means the work in the target directory is complete.
`RESULT: blocked` means an obstacle is named above it with every required line.

## Stop conditions

There is no stop other than completing the work and the obstacles in **Report shape**:

- a required input or tool is missing or broken: `tooling`;
- the item's own words contradict the inputs or workspace: `definition`;
- a decision only the owner can make blocks progress: `owner`.
