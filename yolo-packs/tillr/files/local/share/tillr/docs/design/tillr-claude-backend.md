---
title: "A claude backend for tillr: a seat can run on the Claude subscription and falls back when its window is spent"
status: accepted
stage: DESIGN
updated: 2026-11-16
tags:
  - tillr
  - backend
  - design
summary: "Adds a fourth adapter, run-claude, so the backend policy can put any seat on the Claude Code command line run headless (claude -p), which is the only way to spend the Claude subscription from a drain. Measures what a headless run loads by default, fixes what the adapter always passes, gives a backend row a list of harness arguments for tools, servers and prompt text, and lets a seat name an ordered list of backends so a spent subscription window falls back to the next one."
next: "The owner starts the real loop with backend-policy.claude.json and records its first run here. The quota-window slice is filed as item-claude-backend-quota-window in docs/tillr/roadmap.md."
depends-on:
  - tillr-muse-backend.md
  - tillr-convergence.md
vantage:
  status-chip: true
---

# A claude backend for tillr: a seat can run on the Claude subscription and falls back when its window is spent

> **Delivered copy.** This is `docs/design/tillr-claude-backend.md` from this repository at commit `b8eac4989e` (2026-11-17), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-claude-backend.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-16. The adapter and Claude-first policy dispatched and landed
`item-scratch-claude-build` in a lane-owned scratch clone, not on the real trunk or store:
run `run-item-scratch-claude-build-a1`, model alias `opus` (report model `claude-opus-5-5`),
202,563 tokens, estimated $0.293586. The subscription allowed the run. The gate command
needed correction; restarting the loop landed the existing candidate without another model
run. A second pass refused `item-scratch-claude-fallback` with a simulated-spent window,
then the same item completed and landed on real pi in that pass. [The scratch measurements](#scratch-drain-measurements)
record both runs; [a fresh-store preview defect](#a-fresh-store-cannot-preview-document-intake)
is filed outside this item's repair paths.
UNMEASURED: no loop on the real trunk and store has dispatched to this backend; that remains
the owner's step. A genuinely spent window has not refused a run in these measurements.
The owner kept the running drain's Bedrock-only policy on pi; only a loop explicitly naming
the Claude-first policy reaches this backend.

> **In short.** A backend is already an adapter program plus a row in the backend policy
> ([the muse backend design](tillr-muse-backend.md#1-what-a-backend-is)). This adds the adapter
> for Claude Code run headless, and two small things the policy lacked: a row can carry extra
> arguments for its harness, and a seat can name more than one backend in order, so a seat on the
> subscription moves to the next backend by itself when the subscription refuses.

**Why it matters.** The owner's Claude subscription cannot be reached through pi, so today every
drained run is billed to Amazon Bedrock. The subscription is spent in windows, and on the day of
writing the five-hour window was 81% used, so a backend that only works while the window is open
is useless unless the fall-back is automatic.

**Needs your ruling:** nothing.

**Reads with:** [the muse backend design](tillr-muse-backend.md) (what a backend is, the report
shape, failover), [the convergence design](tillr-convergence.md) (where models are named), and
the adapters' own READMEs under `tools/tillr-run/backends/`.

---

## 1. What was measured

Each row is one headless run on 2026-11-15, model alias `haiku`, effort `low`, brief on standard
input, output as a stream of JSON lines (`--output-format stream-json --verbose`).

| Run | What it loaded | Prompt tokens on the first turn |
| :--- | :--- | :--- |
| Built-in tools removed (`--tools ""`), nothing else | 20 connector servers from the account and the jail, all their tools, 0 skills | 92,263 |
| Connector servers off (`--strict-mcp-config`) | 26 built-in tools, 0 servers, 53 skills | 29,084 |

Five facts the design rests on:

1. **Removing built-in tools does not remove connector tools.** Only `--strict-mcp-config` does,
   and it cuts the prompt by about two thirds.
2. **The stream ends with one `result` line** carrying the final text, the session id,
   `total_cost_usd`, and one `usage` object for the whole run. Each `assistant` line also carries
   usage, and a message that arrives in two lines repeats it, so summing `assistant` lines counts
   twice.
3. **The stream reports the subscription's windows.** A `rate_limit_event` line carries the
   five-hour and seven-day use as fractions and the time each resets.
4. **The jail's launcher adds `--dangerously-skip-permissions`** unless `YOLO_NO_LAUNCH_FLAGS=1`.
   Without that flag a headless run cannot ask, so every write is refused.
5. **The account administrator's instructions reach a headless run.** The model, asked, said it had
   been given organization-level instructions. No flag removes them, so a worker on this backend
   works under them and a worker on pi does not.

## Scratch drain measurements

On 2026-11-16, the lane cloned its worktree into its own scratch directory, renamed the
clone's branch to `main`, disabled the clone's existing item declarations, and committed one
small build item block. A new store inside that clone held only the experimental queue.
The six binaries used by the loop were built from that clone with the standard shared Go
build cache. The Claude harness was the real installed Claude Code 2.1.325, signed in to
the subscription; neither its policy model nor its harness arguments were changed.

The build item asked for a Python program with one exact output line. Its committed test
failed before implementation and passed afterward. The scratch gate checked that test
through the real gate adapter, and the real lander recorded the landing. This was not the
repository's full landing gate: rolling checks, base replay, and trace indexing were
disabled, and no `just done` was run.

With `ROOT` naming the scratch clone, `STORE` its new store, and `SCRATCH` the experiment's
directory, the loop command was:

```sh
tillr-drain loop --exit-when-drained --root "$ROOT" --store "$STORE" \
  --policy config/tillr/backend-policy.claude.json --lanes 1 \
  --gate "tools/tillr-loop/tillr-loop gate --root $ROOT --store $STORE --gate-program $SCRATCH/probe-gate" \
  --land "tools/tillr-loop/tillr-loop land --root $ROOT --store $STORE" \
  --verdict "tools/tillr-loop/tillr-loop verdict --root $ROOT --store $STORE" \
  --questions-cmd "uvx vantage-check index --format json" \
  --rolling '' --replay '' --trace-bin ''
```

The executable `probe-gate` ran `python3 config/tillr/scratch-probe.test.py` in the
candidate's working directory. An initial command instead put that multiword program in
quotes inside the `--gate` string. The drain splits that string on whitespace, not shell
quoting, so the gate adapter rejected its extra argument and the loop exited 4 with
`gate-harness-error`. Correcting the command and restarting examined and landed the
existing candidate: there was one Claude model run, not a second build attempt.
Earlier setup attempts inherited the lane branch name instead of `main`, missed multiline
item declarations, and used an invalid scratch item id; none dispatched a worker.

The first run's output and archived report agreed:

```text
loop: tick 0 dispatch item-scratch-claude-build -> claude on lane-1
loop: run-item-scratch-claude-build-a1 on claude spent 202563 tokens, $0.293586

backend: claude
model: claude-opus-5-5
tokens: 202563
cost usd: 0.293586
rate limit kind: seven_day
fraction: 0.86
status: allowed_warning
reset: 1795093200
complete: yes
result: done
```

The alias `opus` is the route in the run record and the policy's build model; the report
names the resolved model. Cost is the harness's list-price estimate, not a subscription
charge. The scratch store's check reported `integrity: ok`, one run and one landing.

The second pass used the same clone, store, unchanged Claude-first policy, gate and lander.
It committed a new build item, `item-scratch-claude-fallback`, whose regression test required
a changed output line and failed before the worker changed the program. The simulation
was a program named `claude`, first on `PATH`, that exits non-zero and writes no report.
It printed a simulated-spent-window message to standard error and exited 1; it did not
contact Claude or claim that the real subscription window was spent. Its complete program
was:

```sh
#!/bin/sh
printf '%s\n' 'SIMULATED-SPENT-WINDOW: Claude subscription window spent; refusing without a report.' >&2
exit 1
```

The loop saw `run-item-scratch-claude-fallback-a1` as an infrastructure failure, then
ran the same item as `run-item-scratch-claude-fallback-a2` on real pi, using the policy's
Bedrock route. The copied log lines show the refusal, failover, and completed pass:

```text
loop: tick 0 dispatch item-scratch-claude-fallback -> claude on lane-1
loop: run-item-scratch-claude-fallback-a1 on claude spent unknown tokens, unknown cost
loop: item-scratch-claude-fallback failed in the infrastructure class (1 consecutive)
loop: failover item-scratch-claude-fallback -> pi on lane-1
loop: tick 2 dispatch item-scratch-claude-fallback -> pi on lane-1
loop: run-item-scratch-claude-fallback-a2 on pi spent 324610 tokens, $0.120795
loop: dispatched 2, landed 1, rejected 0, infra failures 1
```

The refusal's archived extractor report said exit code 1, zero stream lines,
`complete: no`, `result: missing`, and unknown tokens and cost. The successful pi report
said `complete: yes`, `result: done`, 324,610 tokens and $0.120795. Both the restarted
first pass and the second pass exited 0 with `queue-drained`. After both passes,
`python3 config/tillr/scratch-probe.test.py` printed `PASS: scratch probe exact output`,
and `tillr-store check --store "$STORE"` reported `integrity: ok`, three runs and two
landings. The store recorded both items as `landed`; both landings' gated and landed
trees matched. No real trunk ref or real store was moved by these experiments.

No adapter behavior needed repair. The adapter suite's four cases and the documentation
suite's 37 documented invocations passed. The command-quoting mistake was corrected in
the scratch invocation, not patched as new shell-parsing behavior. The fresh-store preview
failure found during setup is filed [below](#a-fresh-store-cannot-preview-document-intake),
not silently repaired outside the fence.

The earlier hand-driven adapter run on 2026-11-15 used alias `haiku`, loaded 26 built-in
tools and no connector servers, ended `RESULT: done`, and reported the five-hour window
at 97% used. That run did not exercise dispatch or landing. The measurements in
[section 1](#1-what-was-measured) also used Claude Code 2.1.325 on that date.

## 2. The adapter

`tools/tillr-run/backends/claude/` holds `run-claude`, `extract-report`, `README.md` and
`testdata/`, the same four things every adapter has. The directory is named for the harness, as
`pi`, `muse` and `agy` are; no model id appears in it.

**What it runs,** from the lane's worktree:

```
claude -p --output-format stream-json --verbose \
    --model <policy model> --effort <policy effort> \
    --session-id <the lane's session id> \
    --strict-mcp-config --dangerously-skip-permissions \
    <the row's harness arguments> < brief.md
```

- **The brief travels on standard input**, so its size is not bounded by the argument list.
- **`--strict-mcp-config` is always passed.** A run sees only the servers its row names with
  `--mcp-config`. A worker with the account's mail, calendar and chat connectors is a worker that
  can act outside the repository, and nothing in a brief needs them.
- **`YOLO_NO_LAUNCH_FLAGS=1` is exported and the permission flag is passed by the adapter**, so
  the argument list is the same inside and outside a jail.
- **The lane markers are exported** exactly as `run-pi` exports them (`BOARD_DRAIN_RUN`,
  `BOARD_DRAIN_SESSION`, `BOARD_DRAIN_BRIEF`) along with the recording variables, so an extension
  can tell a lane's worker from a person's session and the run's shell and git calls are recorded.
- **Refusals** (exit 2, nothing started) are the shared ones: wrong backend name, no model, no
  effort, a brief that fails preflight, a target that is not the working directory, no `claude` on
  `PATH`. `claude` and `claude-<role>` are both accepted as the backend name, as with pi.
- **Write confinement: nothing beyond the boundary snapshot**, the same statement the pi adapter
  makes. The harness has a restricted mode, but it removes the shell tool a builder needs.

**What the extractor reads.** The text is the `result` line's `result` when the stream has one,
otherwise the text blocks of the `assistant` lines in order, so a stream cut off before its end
still yields what arrived. Tokens are the `result` line's usage (input, output, cache read and
cache write added together); without a `result` line they are the `assistant` lines' usage counted
once per message id. Cost is `total_cost_usd`, and it is an estimate at list prices: a subscription
is not billed per run. The session id is the `result` line's, or the `init` line's.

## 3. Controlling what a seat may use

The owner asked to control a Claude seat the way a pi seat is controlled: which tools exist, which
are refused, what custom tools it gets, which skills, what standing text. Pi takes all of that from
the machine's pi profile. Claude Code takes it from arguments, so the policy row carries them.

**Decision: a backend row gains `harness_args`, a list of strings staging appends to the adapter's
argument list after a `--` separator, and the adapter hands them to the harness unchanged.** The
policy's rule that it holds data and the adapter holds spelling is kept: tillr does not invent one
permission vocabulary for four harnesses, which
[the runtime design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md#1-purpose-and-scope) names as a hole it does not fill.

| Wanted | Harness argument |
| :--- | :--- |
| Remove built-in tools | `--tools "Read,Edit,Bash"` (only these exist) |
| Refuse one tool or one command shape | `--disallowed-tools "WebSearch" "Bash(git push *)"` |
| Add custom tools | `--mcp-config <file in the repository>` (the only servers the run sees) |
| No skills or slash commands | `--disable-slash-commands` |
| Extra skills | `--plugin-dir <directory in the repository>` |
| Standing text for the seat | `--append-system-prompt <text>` |
| No user or project settings | `--setting-sources ""` |

Paths in these arguments are relative to the lane's worktree, which is a checkout of the
repository, so a file the row names is versioned with the row. An adapter that takes no harness
arguments refuses them, as every adapter refuses an argument it does not know.

**Content is prepared the same way.** The brief is the same template every backend gets. The
worktree carries the repository's own instruction files, and Claude Code reads them as pi reads its
own; nothing is copied into a second place.

## 4. Falling back when the window is spent

**Decision: a seat may name an ordered list of backends, and a failover walks the item's own seat
list.** Today a seat names one backend and a failover walks the policy's single `preference`, so a
design item whose backend fails walks to the *build* backend. With a list, the design seat reads
`["claude-designer", "pi-designer"]` and the build seat `["claude", "pi"]`: each falls back to its
own counterpart. A seat that names one backend behaves as it does today, and an item with no seat
walks `preference` as it does today.

A run the subscription refuses exits non-zero and writes no `RESULT:` footer. The drain already
classes that as a failure of the machinery, the one class a failover fires on, so the same item
runs again on the next backend in its seat's list in the same pass. Each later item tries the
subscription first, which is also how work returns to it when the window reopens.

**What this costs, and the slice that removes the cost.** While the window is shut, every item
spends one refused start before it falls back. That is seconds and no tokens. A run cut off
mid-work is different: it is re-run from the start on the next backend, in the same worktree. The
follow-up slice reads the reset time the stream reports, skips the backend until then, and shows
the wait in `tillr-view waiting`; it is filed as a queue item once this slice has landed.

## 5. What done looks like

- `tillr drain loop --policy config/tillr/backend-policy.claude.json` dispatches a build item to
  `claude`, and the drain log's spend line names that backend.
- A design item dispatches to `claude-designer`.
- With the subscription refusing, the log shows `failover <item> -> pi` and the item lands or is
  judged on pi.
- A row's `harness_args` appear after `--` in the recorded `run.request`.

## 6. Build order

1. **By hand, now:** the adapter, its extractor, fixtures reduced by hand to the line shapes in
   [section 1](#1-what-was-measured) with no account data in them, the README, `harness_args`,
   seat lists, and the Bedrock machine's policy rows. Tests: the adapter's refusals, the extractor
   on a complete and a cut-off stream, the staged argument list, and the seat walk.
2. **Queue item, after 1:** the quota window, as described in
   [section 4](#4-falling-back-when-the-window-is-spent).
3. **Queue item, later:** `harness_args` for the pi adapter, when a pi seat needs it.

## 7. Finding from scratch setup

### A fresh store cannot preview document intake


A store initialized with `tillr-store init` has no item tables until a writing item
operation creates them. On 2026-11-16, the source-built `tillr-item sync --dry-run` against
such a store exited 5 rather than previewing its first document item. A second fresh store
reproduced the same error, ruling out the scratch item's earlier invalid id as the cause:

```text
tillr-item: reading the stored items: SQL logic error: no such table: items (1)
```

Reproduction, with `ROOT` naming a repository containing one valid item block and `STORE`
naming a new store file:

```sh
tillr-store init --store "$STORE"
tillr-item sync --root "$ROOT" --store "$STORE" --dry-run
```

A writing `sync` initialized the tables and allowed the preview afterward; that was an
operational setup step, not a fix. Repair the empty-store read behavior with a regression
test that fails first. This finding is outside the Claude adapter's repair paths and does
not invalidate either dispatch-and-landing measurement.


## Decision Ledger

| ID | Decision | Why |
| :--- | :--- | :--- |
| `D-CB1` | The adapter always passes `--strict-mcp-config` | Measured: connector servers triple the prompt and let a worker act outside the repository |
| `D-CB2` | A backend row carries `harness_args`, passed unchanged after `--` | The policy holds data and the adapter holds spelling; one permission vocabulary across harnesses is not designed |
| `D-CB3` | A seat may name an ordered list of backends and a failover walks that list | A design item must fall back to a design backend, not to the build backend |
| `D-CB4` | A refused subscription is a machinery failure, so it fails over in the same pass | It is what the drain's existing classes already say about a run that wrote no report |
| `D-CB5` | Cost on this backend is recorded as the harness's estimate | The harness reports list-price dollars; a subscription is not billed per run |
