---
title: "Replaying the drain's recorded tasks against several models"
date: 2026-11-17
status: in-review
stage: DESIGN
next: "Freeze the eleven shortlisted task records, which is copying only and runs no model. It is work to file, not a question: since 2026-11-18 no ref holds a landed run's own commits, so Git may delete them. The first round itself waits on the owner saying he is ready to run one."
tags: [tillr, models, benchmarks, design]
summary: "A finished drain run keeps its starting commit, brief, allowed paths, report, change, spend and session log, read here from one real run. What it does not keep is the environment it ran in, so a replay is compared with other replays made together, never with history. The design proposes an isolated place to replay, a first shortlist of eleven tasks, and what the owner reads. It computes no score."
---

# Can a finished drain run be started again for another model?

**Status:** 2026-11-18. A proposal, written from records read on 2026-11-17 and re-read the next
day. Nothing is built: no record is frozen, no task has been replayed and no model has been run
for comparison. The owner said on 2026-11-17 that he is "not ready to run these just yet", so
every question here is blocked until he is. The [Decision Ledger](#decision-ledger) holds
designer's decisions he has not reviewed, each labeled as one.

> **In short.** Yes for the task, no for the surroundings. The drain keeps a run's starting
> commit, brief and allowed paths well enough to start it again. It does not keep the briefing,
> the harness build or the model version the run had. So a replay can only be compared fairly with
> other replays made at the same time in the same surroundings, and the recorded original is a
> reference beside them, not one of the results.

**Why it matters.** The owner wants to read several models' work on the same task before any
rubric exists. Today the only comparison available is one model's run against history, and
[Section 1.3](#13-what-is-not-kept-or-does-not-survive) shows the surroundings changed within a
single day.

**The shape.** Three parts, none of them inside the live loop:

- a **task record**, a frozen copy of one finished run's inputs, with its outcome sealed apart;
- a **replay root**, a separate clone that one replay runs in and that nothing lands from;
- a **reading packet**, one directory per task that puts the results beside each other.

**Cost.** A replay pays for the model again, and a round needs the owner to start a separate jail.
The shortlisted runs' own commits are held by no ref since 2026-11-18, so they can be lost to
ordinary Git housekeeping until they are copied
([Section 1.3](#13-what-is-not-kept-or-does-not-survive)).

**Start at** [Section 3](#3-how-a-replay-is-kept-apart), the isolation. The shortlist and the
packet follow from what it can and cannot hold fixed.

**Needs your ruling:** None today. Three questions are blocked until the owner is ready to run a
round: [OQ-MB1](#OQ-MB1) (what the first round is), [OQ-MB5](#OQ-MB5) (whether he reads the
results blind) and [OQ-MB10](#OQ-MB10) (what happens to a replay that overspends).

**Reads with:**

- [`tillr-planning-node-model-tiering.md`](./tillr-planning-node-model-tiering.md): which model
  does which kind of work, and the other way to compare them
  ([Section 6](#6-how-this-relates-to-planning-and-model-tiering)).
- [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md): the lane layout and the brief every adapter
  reads.
- [`tillr-intake-paths.md`](./tillr-intake-paths.md): how the owner's request reached the queue.

No implementation plan exists, and none is owed until the owner asks for a round.

---

## The request, as the owner said it

Dictated on 2026-11-17 and reproduced as received. Only transcription slips are corrected.

> I've been wanting to do some sort of model benchmarks for a long time. I don't really know
> where this is going, but we now have lots of very bounded tasks where there's completely known
> inputs: Git tree, machine state, task, whatever, parameterized completely. And we run them and
> we have historical records of them. So this seems like the perfect opportunity to capture some
> of this stuff and rerun it. We have both design type things as well as more mechanical coding
> tasks, and I would love to run the exact same tasks isolated, so they appear to be starting
> from the exact same point, repository state, everything, and then run the same thing against
> multiple models and see how it differs.
>
> I think it's going to start off with: I don't know the rubric to evaluate these things yet. I
> would like to see the different implementations and judge them subjectively before we make
> some sort of objective rubric here. And then for the designs, I would love to give the two
> different design models the same sort of information, and then see the design docs side by
> side, what was generated, and then try to figure out some sort of rubric there as well.
>
> Not ready to run these just yet, but I want to start mining ideas here.

## The words

Each of these is coined here.

| Word | What it is | What it is not |
| :--- | :--- | :--- |
| **Task record** | A frozen copy of what one finished run was given: its starting commit, brief, allowed paths and, where it had one, its conflict record. The run's outcome is kept with it but sealed apart | Not the queue item, which may have changed since |
| **Replay** | One new run of a task record's brief from its starting commit | Not a retry: the queue never hears of it, and it cannot land |
| **Arm** | One combination of adapter, model and effort that a replay is run with | Not a backend row in the policy; the policy is not consulted |
| **Control arm** | The arm that repeats the original run's own adapter, model and effort | Not the recorded original, which ran in other surroundings |
| **Replay root** | The directory one replay runs in: its own clone, its own lane directory and its own store | Not a worktree of the working repository |
| **Round** | A set of tasks and arms run together under one spending limit, in one jail | Not a pass of the loop |
| **Reading packet** | What the owner is handed for one task | Not a report card: it holds no score |

## 1. What a finished run keeps

### 1.1 The run that was read

Everything in [Section 1.2](#12-what-is-kept-and-where) was read on 2026-11-17 from one run,
`run-item-pt-copied-usage-a1`. It is a first attempt at a coding item on lane 3, run by pi on Sol
6.1 at medium effort, and its change landed. Design runs were read the same way, and
[Section 1.2](#12-what-is-kept-and-where) names where they differ.

The commands, with `S` the store at `.tillr/state.db` and `R` the run id:

```bash
tillr run status --store "$S" --run "$R"
tillr store events --store "$S" --output events.json     # 13,547 events on 2026-11-17
ls .tillr/loop/"$R"
git for-each-ref | rg pt-copied-usage
```

### 1.2 What is kept, and where

| What | Where it is kept | What was read |
| :--- | :--- | :--- |
| **Starting commit** | The `drain.started` event's `base_sha`; the file `.tillr/loop/<run>/base_sha`; the brief's header | `b86a367fc`, and `git cat-file -t` says it is a commit |
| **Brief** | `.tillr/loop/<run>/brief.md` | 8,951 bytes, written when the run was staged |
| **Allowed paths** | `.tillr/loop/<run>/fence.txt` | Ten lines. The `drain.inputs` event holds only a hash of them, and `tillr item show` gives the item's paths as they are now |
| **Report** | `.tillr/lanes/<lane>/durable/<run>/report.md`, which the run record's `artifact_refs` names | 4,655 bytes, ending in `RESULT: done` |
| **Change** | The worker's commit, listed in `.tillr/loop/<run>/commits.txt`; the ref `refs/tillr/runs/<run>`; after landing, `refs/tillr/landed/<run>` and the `land.landing` event | On 2026-11-17 the ref held the worker's commit and the loop's commit that retires the document block. Since 2026-11-18 both refs are deleted once the change lands. The landed commit is `28db296ab`, on the trunk |
| **Spend** | The `run.finish` event's `usage`; `.tillr/loop/<run>/run.record.json` | 4,506,679 tokens and $0.948536, `spend_status` `reported`. Later runs the same day also record each model and each turn |
| **Session log** | `.tillr/lanes/<lane>/<run>.log`, the raw stream | 16,005 lines. Its first message is the system prompt in named sections, so the briefing, the tool list and the skill list the run received are in it |
| **Model and effort** | The run request, in the `drain.inputs` event and in `.tillr/lanes/.requests/<run>.request.json` | `run-pi --backend pi --model amazon-bedrock/global.openai.gpt-6.1-sol --effort medium` |
| **Gate result** | `.tillr/loop/<run>/gate.verdict.json`, with the logs beside it | `pass`, with the tree it judged and the digest of the check registry it used |
| **What the run executed** | `.tillr/loop/<run>/proc-exec.jsonl` and `git-trace2.jsonl` | Every shell command and Git call the run made |
| **Conflict record** (design runs that answer a conflict) | `.tillr/loop/<run>/conflict.json` | Read from `run-item-def-tillr-loop-hooks-a2` |

Two things in that table are easy to get wrong:

- **The lane's own copies are not the record.** A lane's `brief.md`, `fence.txt`, `base_sha` and
  `report.md` belong to whichever run used the lane last. On 2026-11-17 lane 3's `brief.md` was
  9,165 bytes and four hours newer than this run's. The copy under `.tillr/loop/<run>/` is the one
  that stays.
- **A design run's report says `model: unknown` and `effort: unknown`.** The run request has both.

### 1.3 What is not kept, or does not survive

| Input | State on 2026-11-17 | What it means for a replay |
| :--- | :--- | :--- |
| **Briefing, skills and tool list** | Generated when a jail starts, from packs and synced prose. The log holds the text a run received, and nothing can restore it | A replay gets today's. One day's evidence: the run above has no memory section in its prompt, and a run twelve hours later on lane 19 has one (`rg -c 'Long-term memory'` on each log: no match, then 25) |
| **Harness build** | pi and its extensions are patched and built at launch. The event records a hash of the tillr build, `tillr_build`, and no binary | A replay runs today's build |
| **Model version** | The id is recorded and the model behind it is the provider's. The coding runs on the shortlist used Sol 6.1. The work policy has since moved on: on 2026-11-18 it builds on Terra 6.1 and reviews on Sol 6.2. Sol 6.1 is still selectable | The original's model may stop being served |
| **Memory and past sessions** | pi's memory notes and its session search are outside the repository | Left as they are, they hold the original run itself ([Section 3.5](#35-kept-from-seeing-the-original)) |
| **Fork checkouts** | The fork checkout directory is ignored by Git, shared by every checkout and absent from a lane's worktree | No commit pins it, so a task that touches a fork series cannot be replayed faithfully |
| **Store contents** | The events are append-only, so the queue at any moment could be rebuilt in principle; no tool does it | The eleven briefs read here take the item from the tree, so none needs the store |
| **Worktree leftovers and caches** | Untracked files a lane's earlier run left, and the shared Go build and module caches, are not recorded | They change wall time, and could change a result that depends on a stray file |
| **Network** | `uvx` fetches its tool at run time, and a run may search the web | The checker's version, and anything public, can differ |
| **Commits off the trunk** | The trunk was rewritten on 2026-11-17 (`refs/backup/main-before-reauthor-2026-11-17`). `git merge-base --is-ancestor` says one shortlisted starting commit is no longer in `HEAD`'s history; refs under `refs/tillr/` still hold it | It lasts as long as those refs do |
| **Unreferenced results** | On 2026-11-18, for each of the seven shortlisted runs that list a worker commit, `git cat-file -t` still says commit, `git merge-base --is-ancestor` says it is not in `HEAD`, and `git for-each-ref --contains` prints nothing. The four design runs that list none are named only by their `land.landing` events, and two of those candidates had no ref on 2026-11-17 | Git's housekeeping may delete them. The landed, squashed form is on the trunk |
| **Run pins** | [Prevent old run labels from cluttering history](../tillr/roadmap.md#prevent-old-run-labels-from-cluttering-history) landed on 2026-11-18: a landing deletes its run's pins, and a sweep removed the backlog. That day `git for-each-ref refs/tillr/runs` printed 5 refs and `refs/tillr/landed` none | Nothing keeps a landed run's own commits reachable. This is why the freeze cannot wait |
| **Reused run ids** | 39 run ids appear in more than one `run.finish` event, and `.tillr/loop/<run>/` is keyed by run id | A later run under the same id can overwrite the record |
| **Spend on older runs** | 400 of 809 `run.finish` events carry no `spend_status`, and the tiering design measured that a run's cost leaves out its child sessions | Cost is comparable only between runs recorded the same way |
| **The loop record itself** | `.tillr/loop` is untracked. It held 781 run directories and 74 GB on 2026-11-17 and 1,277 entries and 24 GB the next day (`du -sh`) | Something already shrinks it, and nothing read for this design says what or when |

The table sorts into one conclusion. **The task survives and the surroundings do not.** A replay
can hold the commit, the brief and the allowed paths fixed. It cannot run inside the briefing, the
build or the model version the original had. The design therefore holds the surroundings *equal
across arms* and makes no attempt to make them *equal to history*.

## 2. Principles

- **P1. Compare replays with replays.** Every arm of a task runs in the same round, in the same
  jail. The recorded original is shown as a reference and is never one of the arms.
- **P2. The brief is used byte for byte.** Nothing rewrites it, so no arm can be given a different
  task by accident.
- **P3. Nothing a replay can reach holds an answer.** That covers the original's change and
  report, every later commit, and the other arms.
- **P4. A replay cannot touch live work.** It has no route to the live store, the trunk or the
  working repository's refs. This is a property of where it runs, never a rule it is asked to
  follow.
- **P5. No score.** The packet shows facts a command produced and the work itself. It ranks
  nothing and adds nothing up across tasks.

## 3. How a replay is kept apart

### 3.1 Freezing a task

A task record is made once, by copying, before any replay of that task. It has two halves that
never share a directory.

- **The inputs:** the brief, the allowed paths, the conflict record if the brief names one, the
  original's run request, and the starting commit as a Git bundle that holds that commit's whole
  history and nothing after it.
- **The sealed outcome:** the original's change as a patch, its report, its gate verdict, its
  spend and its session log.

A bundle is used so that the record depends on no ref in the working repository. A task record is
written once and never edited. If the copy finds a file missing or a commit gone, that task is
not frozen, and the freeze says which piece was missing.

### 3.2 The replay root

Each replay gets its own replay root, made from the task record's inputs and from nothing else:

- a clone made from the bundle, with no remote configured;
- a lane directory at the path the brief names, holding the brief, the allowed paths, the starting
  commit, a new session id and a worktree checked out at the starting commit;
- an empty store of its own.

The eleven shortlisted briefs name absolute paths: the lane's worktree, the run's scratch
directory and, for one, the lane's conflict file. All begin with `/workspace/.tillr/lanes/`
(`rg -o '/workspace[^ )]*'` on each brief, 2026-11-17). When the replay root is what a jail mounts
as its workspace, every one of those paths exists inside it, and the brief needs no edit (P2).

That claim has not been tried in a jail whose workspace is a replay root. The dry run of
[Section 8](#8-what-would-happen-in-order) finds out before a model is paid for.

Replay roots are large and disposable. They live in the jail's durable scratch area, outside the
repository's tracked tree, and each is deleted once its results have been copied into the packet.
Where the packets are kept is [Section 5.5](#55-where-packets-and-notes-are-kept).

### 3.3 Varying the model, and nothing else

The adapters already take the model and the effort as flags and the brief as a file: the
original's request above is `run-pi --backend pi --model … --effort medium`. An arm is that
command with a different adapter, model or effort. The replay starts the adapter the way the loop
does, through the run step, against the replay root's own store, so spend is recorded in the same
shape as the original's.

- **What stays fixed across the arms of a task:** the starting commit, the brief, the allowed
  paths, the jail and so the briefing, the skills, the harness build and the tools.
- **What is checked, not assumed:** each replay's log opens with its system prompt. The packet
  compares them, and marks an arm whose prompt differs from the others.
- **What changing the adapter costs:** an arm on another harness changes the harness as well as
  the model. The packet names all three parts of every arm, so the reader can tell.
- **One at a time.** A round runs one replay at a time. It then draws on the provider's quota as
  one lane would, and no two replays share a cache or a build.

**Every task has a control arm.** The original's own adapter, model and effort run again in the
round, and the other arms are compared with it (P1). Without it a difference could come from the
model or from surroundings that changed within a day
([Section 1.3](#13-what-is-not-kept-or-does-not-survive)). A control costs about what the original
did. Which other arms run is [OQ-MB1](#OQ-MB1).

### 3.4 Kept from landing and from the live queue

| What must not happen | Why it cannot |
| :--- | :--- |
| A replay's commit reaches the trunk | The replay root is a separate clone with no remote. A commit there is in another object store, and no ref of the working repository can name it |
| The lander picks a replay up | The lander reads the live store. A replay is recorded only in its replay root's own store |
| A replay writes the live queue | The live store is not inside the replay root, and it is not mounted in the replay's jail at all |
| Replay spend is counted as drain spend | It is recorded in the replay root's store. The round has its own limit ([OQ-MB1](#OQ-MB1)) |
| A replay's result is applied by habit | The packet holds each change as a patch file. Applying one to the working repository is a deliberate act by the owner, outside this design |

### 3.5 Kept from seeing the original

Each row is a way a replay could find the answer, and what closes it.

| Where the answer is | Closed by |
| :--- | :--- |
| Later commits and `refs/tillr/runs/*` in the object store | The clone comes from a bundle that ends at the starting commit |
| The working checkout, which holds the landed change | It is not mounted in the replay's jail |
| `.tillr/loop/<run>/`, the lane logs and the reports | Not mounted; the sealed half of the task record is never inside a replay root |
| pi's session search, which indexes the original's session | The replay's jail starts with an empty session index |
| pi's memory notes, which may record lessons from the original | The replay's jail starts with empty memory, the same for every arm |
| The remote, which has the landed trunk | No remote in the clone, and no Git or GitHub grant for that jail |
| The brief itself, when it carries an earlier attempt's findings | The shortlist rule ([Section 4.1](#41-the-rule)) admits only briefs that stand alone |
| Another arm's work | Each arm has its own replay root and never sees another |
| The public web | Not closed. It matters only if the repository's history is public |

Prevention is checked afterwards as well. Each replay leaves the same trace of commands and Git
calls as a drain run. The packet scans it for the original's commit ids and for paths outside the
replay root, and prints the result as **leak check: clean** or the hits.

**A round runs in a jail of its own.** Only a separate jail closes the rows above. In the jail the
loop uses, the working checkout holds the landed answer to every shortlisted task one command
away, and a model that searches widely can read it without meaning to cheat. Starting a jail is
the owner's act, one command for each round, and it is the first thing a round asks of him.

### 3.6 The gate, as a check only

The gate that judges a replay is the one in the starting commit's tree, run against the replay's
commit inside the replay root. It records pass or fail for each check, with the logs.

- **No landing follows it.** There is no lander in a replay root.
- **A check that lacks an input says so.** A check that needs something the replay root does not
  have, such as a fork checkout, is recorded as **not run** with the reason, and never as a pass.
- **The registry is compared.** The original verdict records the digest of the check registry it
  used. The packet says whether the replay's gate used the same one.
- **The item's criteria are not judged again.** The owner asked to judge by eye first.

Whether the gate program can run against a store-less or freshly made root has not been tried.
The first step in [Section 8](#8-what-would-happen-in-order) finds out without running a model.

### 3.7 When something goes wrong

| What happens | What the round does | Who finds out, and how |
| :--- | :--- | :--- |
| An arm ends with a non-zero exit, or without the report's result line | Keeps what it produced; does not run it again | The packet row says so in the model's own terms |
| The provider refuses for quota or a fault | Marks the arm **not run: provider**, which is a different word from failed; it may be run again within the limit | The packet row |
| The round's spending limit would be passed by the next arm | Stops before starting it | The packet lists the arms that did not run |
| One replay spends far more than its original did | Undecided: stopped, or only marked ([OQ-MB10](#OQ-MB10)). Either way what it produced is kept | The packet row |
| The round is interrupted | On restart, an arm with a finished run record is never run again; a half-made replay root is deleted and made afresh | The round's own log |
| A frozen input turns out to be missing | The task is dropped from the round | The freeze and the packet both name the missing piece |

A replay has no wall-clock limit, as a drain run has none: the original briefs say `Timeout:
none`. What bounds a replay is money.

### 3.8 One writer for each thing

| State | Its one writer |
| :--- | :--- |
| A task record | The freeze, once; nothing afterwards |
| A replay root, its clone and its store | The one replay that runs in it |
| A reading packet | The step that builds it after the round; the owner writes only the notes file |
| The live store, the trunk, the working repository's refs | Nothing in this design |

### 3.9 Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Run the task again as a live queue item on another model**, with a policy override | Rejected. It starts from today's trunk, where the work is already done; it writes the live store; and a pass lands |
| **A linked worktree of the working repository at the starting commit**, as lanes are made | Rejected. A worktree shares the object store and the refs, so `git show refs/tillr/runs/<run>` prints the original's change, and a replay's commits join the shared store |
| **Keep the frozen commits as refs in the working repository** | Rejected. The owner reads history in `tig` and has asked for fewer run refs, not more; a bundle needs none |
| **Rewrite the brief's paths to wherever the replay is** | Rejected. It edits the one input that is meant to stay fixed, and it is needed only in the same-jail arrangement below |
| **Replay in the loop's own jail, in a separate clone** | Rejected. Nothing for the owner to start, but the answer stays reachable, the brief's paths must be rewritten, and a replay that read the answer is found only after it has been paid for (P3, P4) |
| **Rebuild the original's surroundings** from the log's recorded prompt | Rejected. The prompt text is kept; the harness build, the extensions and the model version are not, so the result would look faithful and not be |
| **Have a model score or rank the results** | Rejected for now. The owner asked to read first and find the rubric from the reading |
| **Use a public benchmark's tasks** | Rejected. The request is about this repository's own work, and its records already exist |

## 4. A first shortlist

### 4.1 The rule

A recorded run is on the shortlist only when all of these hold. Each was checked on 2026-11-17
with the commands in [Section 1.1](#11-the-run-that-was-read).

1. **It finished once.** Its run id appears in exactly one `run.finish` event, with a normal exit.
2. **Its work was good enough to keep.** The gate passed its change, and the item has landed. A
   task nobody finished gives no reference to read the replays against.
3. **Its inputs are all there.** `.tillr/loop/<run>/` holds the brief, the allowed paths and the
   starting commit, and Git still has that commit.
4. **Its brief stands alone.** It carries no facts from an earlier attempt, and it reads the item
   from the tree. A first attempt is preferred. A later attempt is admitted only when the earlier
   ones landed nothing and ran in other sessions. A brief that names a conflict record is admitted
   only when that record was kept.
5. **It needs nothing outside the tree.** Its allowed paths touch no fork series, patch directory
   or host configuration.
6. **Its spend was recorded**, so a replay's cost has something to stand beside.
7. **Together they vary** in size and in kind.

Under rule 2 and rule 6 alone, 36 first-attempt coding runs qualify. Eighteen of those are
one-file rewrites of an item's prose at about $0.25 each. They would make a cheap third kind of
task, and they are left off this list because the owner named two kinds.

### 4.2 Mechanical coding tasks

All six ran on pi with Sol 6.1 at medium effort, as a first attempt, and landed.

| Run | The task | Original change | Original cost and time | Why it is here |
| :--- | :--- | :--- | :--- | :--- |
| `run-item-store-trace-queue-test-not-flaky-a1` | Fix a flaky Go test in the store | 2 files, +140 −44 | $0.49, 3.6 min | The smallest: one cause to find, one test to prove it |
| `run-item-def-list-items-a1` | Add a command that lists a store's items | 8 files, +464 −2 | $0.59, 4.2 min | A small new feature with its tests and its pack copy |
| `run-item-view-running-shows-live-loop-a1` | Show the live loop's session and leases in a view | 18 files, +338 −62 | $0.53, 5.7 min | A view change with its tests; once named for a Terra trial that was never run |
| `run-item-just-format-recipe-a1` | Add a format recipe the gate also checks | 8 files, +220 −7 | $0.84, 6.7 min | Not Go: a recipe, a gate check and its test |
| `run-item-pt-copied-usage-a1` | Hold copied usage out of spend totals | 20 files, +1,678 −10 | $0.95, 9.8 min | Three modules and a data correction; the run [Section 1](#1-what-a-finished-run-keeps) was read from |
| `run-item-obs2-run-usage-a1` | Record and show what each run used | 80 files, +2,208 −156 | $2.88, 21 min | The largest |

### 4.3 Design tasks

No design item's first attempt landed, so each of these is a later attempt. Rule 4 was checked for
each: the earlier attempts landed nothing, and each attempt had its own session id.

| Run | The task | Original change | Original cost and time | Why it is here, and what to watch |
| :--- | :--- | :--- | :--- | :--- |
| `run-item-design-observatory-actions-a2` | Write a new design from two owner requests | A new 602-line document and a roadmap link | $7.93, 16 min, pi on Fable 5.2 | The plainest case of a design from a request. The document did not exist at the starting commit |
| `run-item-design-judge-lifecycle-slices-a2` | Settle a design's questions and cut it into build items | 2 files, +233 −15 | $4.89, 9.5 min, pi on Fable 5.2 | Judgment over an existing design. Its change passed the gate and landed through a later conflict fix |
| `run-item-design-queue-persistence-slices-a2` | The same kind of task, on another design | 2 files, +221 −56 | $7.28, 14 min, pi on Fable 5.2 | A second sample of one kind, to see whether a difference between models repeats. It landed the same way |
| `run-item-design-item-graphs-slices-a3` | The same kind of task, on the largest design | 2 files, +442 −129 | $10.94, 17 min, Claude Code on Fable | The one run on another harness, so its control arm is on Claude Code |
| `run-item-def-tillr-loop-hooks-a2` | Answer a conflict a build run raised, by amending one item | 1 file, +66 −1 | $2.21, 14 min, pi on Fable 5.2 | A short ruling and the cheapest design task. Its conflict record was kept. [unlanded] Its starting commit `6ddc4b643` is held only by refs |

Two more facts apply to the whole list:

- **Every record has a deadline.** No ref holds any shortlisted run's own commits
  ([Section 1.3](#13-what-is-not-kept-or-does-not-survive)). Until they are copied, Git's
  housekeeping can remove them. The landed form of each stays on the trunk, but it was squashed,
  and a later owner edit may sit on top of it.
- **Every record should pass a dry run before a model is paid for.** Applying the original's own
  change in a replay root and running the gate there must reproduce the original's pass. A task
  that fails this is not replayable, whatever the rule above says.

## 5. What the owner is handed

One reading packet per task. It is a directory, and it opens with one page.

### 5.1 The page every packet opens with

- **The task:** the brief's task and criteria, word for word, with the starting commit and the
  allowed paths.
- **One row per arm**, in label order and no other:

| Column | What it holds |
| :--- | :--- |
| Arm | Its label, and its adapter, model and effort unless [OQ-MB5](#OQ-MB5) hides them. While they are hidden the logs stay out of the packet, because they name the model |
| Outcome | The report's result line, or how the run ended |
| Gate | Pass, fail or not run, for each check, with a link to the log |
| Cost | Dollars, tokens, turns and wall time, from the run record |
| Size | Files changed, lines added and removed |
| Fence | Paths changed outside the allowed paths, or none |
| Surroundings | Whether its system prompt matched the other arms' |
| Leak check | Clean, or the hits |

- **The recorded original**, in a row of its own under the heading *History*, with a note that it
  ran in other surroundings.
- **No totals, no ranking, no colour for a winner, and nothing summed across tasks.** Rows are in
  label order.

### 5.2 For a coding task

- **Each arm's change as a patch**, and its report beside it.
- **A reading clone:** the starting commit with one branch per arm, so the owner can diff any two
  arms, or an arm against the start, in `tig` or a diff tool he already uses.
- **A file table:** one row per file any arm touched, one column per arm, showing lines added and
  removed. It shows at a glance where the arms agree on what needed changing and where one went
  somewhere the others did not.
- **The tests each arm added**, listed by name, since what a model chose to prove is part of what
  the owner is judging.

### 5.3 For a design task

- **Each arm's document, whole**, as it would be read, in a directory of its own so that links
  inside it still work. Two can be opened side by side.
- **An outline table:** each arm's headings in order, in adjacent columns, so the shapes of the
  documents can be compared before either is read.
- **Plain counts:** length in lines and words, the number of open questions it left, and what
  `vantage-check` said. These are facts about the documents, and they are not weighted.
- **The changes to files other than the design**, as a patch, since a design run usually edits a
  roadmap as well.

### 5.4 The notes file

Each packet holds one file the owner writes in, with a heading for each arm and nothing else. What
he notices there, in his own words, is the raw material for a rubric. Nothing reads it but people.

### 5.5 Where packets and notes are kept

- **Packets stay in the jail's durable scratch area**, outside the tracked tree. They hold several
  models' versions of a design, and a design carries item blocks: committed among the real
  designs, the next sync would read those blocks as work to file, and a reader could take one for
  the live design.
- **The owner's notes file and the packet's opening page are committed under `docs/`.** The notes
  are the one part that cannot be made again. The committed opening page states its figures and
  cites no scratch path, as this repository's rule for tracked documents requires.

## 6. How this relates to planning and model tiering

[Planning and model tiering](./tillr-planning-node-model-tiering.md) decides which model does
which kind of work. Since 2026-11-18 Terra builds and Sol 6.2 plans and verifies, by the owner's
instruction. Its [Section 1.5](./tillr-planning-node-model-tiering.md#15-what-the-history-cannot-say)
lists what the run history cannot say, and its first row is still open: Terra's failure rate and
turn count on this repository's work. Its price comparison ends: "All of this assumes a cheaper
model needs the same turns. Nothing in this repository measures that."

The two designs measure different things, and neither replaces the other:

| | The tiering design's cost report | A replay round |
| :--- | :--- | :--- |
| What is compared | What landed items cost in production, before and after a change of model or workflow | Several models on the identical task, from the identical start |
| What it can show | Whether the bill moved | How each model's work differs, and how each fails |
| What it cannot show | Whether two models would have done the same task differently: no two items are the same | A rate, or anything about the live path |

**What a replay could give the tiering design:**

- **Terra's turns, tokens and cost on a task Sol is known to have passed**, beside a Sol control
  run the same day. Three shortlisted coding tasks were once named for a Terra trial that never
  ran; all landed on Sol on a first attempt.
- **What a cheap model's failure looks like here:** a wrong change that passes the gate, a change
  that fails it, or no change at all.

**What a replay could not give it:**

- **A rate.** A handful of tasks, run once or twice, shows whether something works and how it
  fails. It does not measure how often.
- **Anything about planning.** No recorded run was planned or split into pieces, so there is no
  such task to replay.

The two stay separate documents that link to each other.

## 7. What this design does not do

- It replays nothing and runs no model. It is a proposal.
- It computes no score, and proposes no rubric.
- It does not change the drain, the lander, the gate or the store.
- It does not make old runs reproducible: what [Section 1.3](#13-what-is-not-kept-or-does-not-survive)
  lists as lost stays lost.
- It does not propose that the drain keep more per run. Several rows of that table suggest it, and
  that is work for another design once a round has shown which gaps matter.

## 8. What would happen, in order

Steps 1 and 2 run no model and wait on no question. Everything after them waits on the owner
saying he is ready to run a round.

1. **Freeze the shortlist.** Copying only. The frozen records go to the jail's durable scratch
   area. A record that carried its own bundle of the whole history would be about 270 MB, so
   eleven come to at most about 3 GB (estimated from the packed object store's size on
   2026-11-18); records that share history need far less, as long as each replay's clone still
   ends at its own starting commit.
2. **Dry-run each record**, as [Section 4.3](#43-design-tasks) describes: the original's change,
   in a replay root, through the gate. No model runs. This proves the replay root and the gate
   work together, and drops any task that cannot be replayed.
3. **The owner picks the first round** ([OQ-MB1](#OQ-MB1)) and starts its jail.
4. **The round runs**, one replay at a time.
5. **The owner reads the packets** ([OQ-MB5](#OQ-MB5)) and writes his notes.
6. **A larger round, or a rubric**, whichever the reading calls for. That is a new decision, and
   this document does not make it.

### 8.1 What a first round can be made of

These are the separate choices inside [OQ-MB1](#OQ-MB1), kept apart here so each can be changed
alone. Every dollar figure is an estimate that assumes a cheaper model needs the same number of
turns, which is the very thing not yet measured. The eleven originals cost $39.54 together
(measured): $6.28 for the six coding runs and $33.26 for the five design runs.

| Choice | Smallest | Middle | Largest |
| :--- | :--- | :--- | :--- |
| **Tasks** | Two: the cheapest coding task ($0.49) and the cheapest design task ($2.21) | Six: the three cheapest of each kind ($1.61 and $14.38) | All eleven |
| **Models beside the control** | One: Terra for coding, Sol for design | Add one comparison of harnesses: Fable on pi beside Fable on Claude Code, for one design task | All four models on every task; Fable's coding runs cost about four times Sol's |
| **Controls** | One for each task | One for each task | Two on the cheap coding tasks, to show how far one model differs from itself |
| **Cost with one other model** | About $3.50, of which $2.70 is the controls | About $20, of which $16 is the controls | About $50, of which $40 is the controls |
| **A spending limit that leaves room** | $10 | $30 | $75; $150 with all four models, about $80 |

## 9. Risks

| Risk | What limits it |
| :--- | :--- |
| A replay finds the original's answer, and the comparison is worthless | [Section 3.5](#35-kept-from-seeing-the-original), and the leak check printed in every packet |
| One run per arm is taken for a property of the model | The control arm, and a second control where one is paid for, show how far two runs of one model differ; the packet never sums across tasks |
| The gate passes a poor change, and a pass is read as quality | The packet puts the gate result beside the work, and the owner reads the work |
| A round draws on the quota the live loop needs | One replay at a time ([Section 3.3](#33-varying-the-model-and-nothing-else)) |
| A cheap model loops and spends without finishing | The round's spending limit ([OQ-MB1](#OQ-MB1)), and what [OQ-MB10](#OQ-MB10) decides |
| A replayed design holds item blocks, and a sync imports them as live work | The packets are outside the tree a sync reads ([Section 5.5](#55-where-packets-and-notes-are-kept)) |
| The records are cleaned up before they are frozen | The freeze is the next step and waits on nothing ([Section 8](#8-what-would-happen-in-order)) |
| The original's model is retired before a control can be run | The packet names the model each arm really used; a missing control is stated, never replaced silently |

## Decision Ledger

The owner has ruled on nothing in this document. Each row says what it is. A **designer's
decision** was made by a design seat from the evidence named; the owner has not reviewed it, and
the owner may overturn this. A **merged** row says where its question went.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-MB2 | **Merged into [OQ-MB1](#OQ-MB1).** How many tasks the first round replays is one of that question's choices, since the number of tasks, the models and the limit cannot be chosen apart | 2026-11-18 | [Section 8.1](#81-what-a-first-round-can-be-made-of) | — |
| OQ-MB3 | **Designer's decision; the owner may overturn this.** Every task has one control arm: the original's own model run again in the round. **Evidence:** the surroundings changed within one day, so a replay read against the recorded original cannot put a difference down to the model. **Rejected:** no control, which saves the control's cost (about what the original cost) and makes the comparison unreadable; two controls everywhere, kept as a paid extra on cheap coding tasks | 2026-11-18 | [Section 3.3](#33-varying-the-model-and-nothing-else) | — |
| OQ-MB4 | **Merged.** The round's spending limit is part of [OQ-MB1](#OQ-MB1). Its second half, stopping one run at three times its original's cost, is asked on its own as [OQ-MB10](#OQ-MB10) | 2026-11-18 | [Section 8.1](#81-what-a-first-round-can-be-made-of) | — |
| OQ-MB6 | **Designer's decision; the owner may overturn this.** A round runs in a separate jail, which the owner starts with one command. **Evidence:** in the loop's jail the landed answer to every task is one command away, and principles P3 and P4 require that a replay cannot reach it. **Rejected:** the same jail with a separate clone, which can only report after the fact that a replay read the answer | 2026-11-18 | [Section 3.5](#35-kept-from-seeing-the-original) | — |
| OQ-MB7 | **Designer's decision; the owner may overturn this.** The shortlisted records are copied before any round is approved. **Evidence:** run pins are deleted on landing since 2026-11-18 and no ref holds the shortlisted runs' own commits; the owner asked to "capture some of this stuff"; copying runs no model, changes nothing in the queue, and is undone by deleting the copy. **Rejected:** waiting, which loses the originals' own commits. Not done by this document: the freeze is work still to file | 2026-11-18 | [Section 8](#8-what-would-happen-in-order) | — |
| OQ-MB8 | **Designer's decision; the owner may overturn this.** This stays a separate document from planning and model tiering, each linking the other. **Evidence:** that design changes how the loop runs and this one only reads what it has done; side-by-side reading of design documents has nothing to do with routing. **Rejected:** folding this in as that design's measuring stage | 2026-11-18 | [Section 6](#6-how-this-relates-to-planning-and-model-tiering) | ✅ |
| OQ-MB9 | **Designer's decision; the owner may overturn this.** Packets stay in scratch storage; the owner's notes and each packet's opening page are committed under `docs/`. **Evidence:** committed packets hold item blocks a sync would file as work, and the notes are the one part that cannot be made again. **Rejected:** committing whole packets behind an exclusion that must hold; committing nothing, which leaves the notes in storage that is cleaned up | 2026-11-18 | [Section 5.5](#55-where-packets-and-notes-are-kept) | — |

## Open Questions

All three are blocked. The owner said on 2026-11-17: "Not ready to run these just yet." None is
asked until he is ready to run a round, and the freeze and the dry run do not wait on them.

1. 🔒 **OQ-MB1: How big should the first replay round be, and how much may it spend?**

   You decide to try a round. Each shortlisted task is run again from its starting commit, once on the model that originally did it, as a control, and once on each other model you want to see. You then read one packet per task. Nothing has been replayed yet, so nobody knows whether a replay reproduces a gate pass or whether a packet is readable.

   - **A — Two tasks, a $10 limit.** The cheapest coding task and the cheapest design task. About $3.50 with one other model each. Two packets.
   - **B — Six tasks, a $30 limit.** About $20. Six packets, three holding long design documents.
   - **C — All eleven, a $75 limit.** About $50. Eleven packets.

   The other model is yours to name. The estimates assume Terra for coding and Sol for design.

   <!-- vantage: question id=OQ-MB1 blocked-on="owner-ready-to-run-a-round" leaning="Two tasks, a $10 limit (A): the first round's real product is knowing whether the replay and the packet work, and the next limit is better set from its real costs." -->

   _Leaning:_ Two tasks, a $10 limit (A): the first round's real product is knowing whether the replay and the packet work, and the next limit is better set from its real costs.

   Blocked until you are ready to run a round. The choices inside each option are in [Section 8.1](#81-what-a-first-round-can-be-made-of).

   **Answer:**
   > _(empty — fill in when decided)_

2. 🔒 **OQ-MB5: Do you want to read the results without knowing which model made which?**

   You open a packet with three results side by side. You already hold views on these models. If each result is named, those views read along with you. If they are lettered, the cost must be hidden too, because a $0.25 result beside a $7 one gives the model away.

   - **A — Names shown.** Model and cost sit beside each result from the start.
   - **B — Hidden until your notes are written.** Results are lettered and cost is on a second page. You write your notes, then open the key. You may open it first.
   - **C — Hidden for design tasks only**, where taste weighs most; coding packets show names.

   <!-- vantage: question id=OQ-MB5 blocked-on="owner-ready-to-run-a-round" leaning="Hidden until your notes are written (B): you asked to judge by eye before a rubric exists, and a rubric found while knowing the names inherits the views you already hold." -->

   _Leaning:_ Hidden until your notes are written (B): you asked to judge by eye before a rubric exists, and a rubric found while knowing the names inherits the views you already hold.

   Blocked until a round is approved; no packet exists before then.

   **Answer:**
   > _(empty — fill in when decided)_

3. 🔒 **OQ-MB10: Should a replay that spends far more than its original be stopped?**

   You start a round and leave it. One replay, on a model that loops, passes three times what the original run cost and is still going. A replay has no time limit, so only money bounds it. Your standing rule for the loop's own work is that a budget is a target and never fails work. A replay lands nothing, and it is spending your money.

   - **A — Stop it.** The run ends at three times its original's cost. The packet shows what it had produced, marked as stopped.
   - **B — Mark it and let it run.** The packet shows it went over. Only the round's limit ends it, and other tasks may then not run.

   <!-- vantage: question id=OQ-MB10 blocked-on="owner-ready-to-run-a-round" leaning="Stop it (A): a replay is a measurement and not work to deliver, and one looping run should not use up the money for the tasks behind it." -->

   _Leaning:_ Stop it (A): a replay is a measurement and not work to deliver, and one looping run should not use up the money for the tasks behind it.

   Blocked until a round is approved.

   **Answer:**
   > _(empty — fill in when decided)_
