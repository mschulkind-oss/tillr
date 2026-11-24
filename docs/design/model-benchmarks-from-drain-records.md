---
title: "Replaying the drain's recorded tasks against several models"
status: in-review
stage: DESIGN
next: "Nothing is owed until the owner is ready to run a round. The first work then is freezing a shortlist of task records, which is copying only and runs no model."
tags: [tillr, models, benchmarks, design]
summary: "A finished drain run keeps its starting commit, brief, allowed paths, report, change, spend and session log. It does not keep the environment it ran in, so a replay is compared with other replays made together, never with history. The design proposes an isolated place to replay, a rule for shortlisting tasks, and what the owner reads. It computes no score."
---

# Can a finished drain run be started again for another model?

**Status:** A proposal. Nothing is built: no record is frozen, no task has been replayed and no
model has been run for comparison (`rg -n -i 'replay root|task record|reading packet' tools -g '!*.md'`
prints nothing). Every open question is blocked until the owner is ready to run a round. The
[Decision Ledger](#decision-ledger) holds designer's decisions, each labeled as one.

> **In short.** Yes for the task, no for the surroundings. The drain keeps a run's starting
> commit, brief and allowed paths well enough to start it again. It does not keep the briefing,
> the harness build or the model version the run had. So a replay can only be compared fairly with
> other replays made at the same time in the same surroundings, and the recorded original is a
> reference beside them, not one of the results.

**Why it matters.** The drain's tasks are bounded and their inputs are recorded, which makes them
a ready source of comparisons between models on this repository's own work. The only comparison
available without a replay is one model's run against history, and the surroundings of a run are
not recorded ([Section 1.2](#12-what-is-not-kept-or-does-not-survive)).

**The shape.** Three parts, none of them inside the live loop:

- a **task record**, a frozen copy of one finished run's inputs, with its outcome sealed apart;
- a **replay root**, a separate clone that one replay runs in and that nothing lands from;
- a **reading packet**, one directory per task that puts the results beside each other.

**Cost.** A replay pays for the model again, and a round needs the owner to start a separate jail.

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

No implementation plan exists, and none is owed until the owner asks for a round.

---

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

### 1.1 What is kept, and where

`<run>` is the run id and `<lane>` the lane the run used. The events are read with
`tillr store events` and the run record with `tillr run status`.

| What | Where it is kept |
| :--- | :--- |
| **Starting commit** | The `drain.started` event's `base_sha`; the file `.tillr/loop/<run>/base_sha`; the brief's header |
| **Brief** | `.tillr/loop/<run>/brief.md`, written when the run is staged |
| **Allowed paths** | `.tillr/loop/<run>/fence.txt`. The `drain.inputs` event holds only a hash of them, and `tillr item show` gives the item's paths as they are now |
| **Report** | `.tillr/lanes/<lane>/durable/<run>/report.md`, which the run record's `artifact_refs` names |
| **Change** | The worker's commit, listed in `.tillr/loop/<run>/commits.txt`, and the `land.landing` event, which names the landed commit on the trunk. Refs under `refs/tillr/runs/` and `refs/tillr/landed/` name a run's commits while they exist |
| **Spend** | The `run.finish` event's `usage`; `.tillr/loop/<run>/run.record.json` |
| **Session log** | `.tillr/lanes/<lane>/<run>.log`, the raw stream. Its first message is the system prompt, so the briefing, the tool list and the skill list the run received are in it |
| **Model and effort** | The run request, in the `drain.inputs` event and in `.tillr/lanes/.requests/<run>.request.json` |
| **Gate result** | `.tillr/loop/<run>/gate.verdict.json`, with the logs beside it: the verdict, the tree it judged and the digest of the check registry it used |
| **What the run executed** | `.tillr/loop/<run>/proc-exec.jsonl` and `git-trace2.jsonl`: every shell command and Git call the run made |
| **Conflict record** (design runs that answer a conflict) | `.tillr/loop/<run>/conflict.json` |

**The lane's own copies are not the record.** A lane's `brief.md`, `fence.txt`, `base_sha` and
`report.md` belong to whichever run used the lane last. The copy under `.tillr/loop/<run>/` is the
one that stays.

### 1.2 What is not kept, or does not survive

| Input | What happens to it | What it means for a replay |
| :--- | :--- | :--- |
| **Briefing, skills and tool list** | Generated when a jail starts. The log holds the text a run received, and nothing can restore it | A replay gets today's |
| **Harness build** | The adapter's harness is built at launch. The event records a hash of the tillr build, `tillr_build`, and no binary | A replay runs today's build |
| **Model version** | The id is recorded and the model behind it is the provider's | The original's model may stop being served |
| **Memory and past sessions** | A harness's memory notes and its session search are outside the repository | Left as they are, they hold the original run itself ([Section 3.5](#35-kept-from-seeing-the-original)) |
| **Store contents** | The events are append-only, so the queue at any moment could be rebuilt in principle; no tool does it | A brief that takes its item from the tree does not need the store |
| **Worktree leftovers and caches** | Untracked files a lane's earlier run left, and the shared build caches, are not recorded | They change wall time, and could change a result that depends on a stray file |
| **Network** | `uvx` fetches its tool at run time, and a run may search the web | The checker's version, and anything public, can differ |
| **A run's own commits** | A landing squashes the run's commits. The originals stay reachable only while a ref under `refs/tillr/` names them | Git's housekeeping may delete them, so a task record never depends on a ref |
| **The loop record itself** | `.tillr/` is ignored by Git, and `.tillr/loop/<run>/` is keyed by run id | A record can be removed, or overwritten by a later run under the same id, before it is frozen |
| **Spend on older runs** | A `run.finish` event may carry no `spend_status` | Cost is comparable only between runs recorded the same way |

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

A brief names absolute paths under the workspace's `.tillr/lanes/`: the lane's worktree, the run's
scratch directory and, where it has one, the lane's conflict file. When the replay root is what a
jail mounts as its workspace, every one of those paths exists inside it, and the brief needs no
edit (P2). That is untested; the dry run of [Section 8](#8-what-would-happen-in-order) finds out
before a model is paid for.

Replay roots are large and disposable. They live in the jail's scratch area, outside the
repository's tracked tree, and each is deleted once its results have been copied into the packet.
Where the packets are kept is [Section 5.5](#55-where-packets-and-notes-are-kept).

### 3.3 Varying the model, and nothing else

The adapters under `tools/tillr-run/backends/` take the model and the effort as flags and the
brief as a file. An arm is the original's run request with a different adapter, model or effort.
The replay starts the adapter the way the loop does, through the run step, against the replay
root's own store, so spend is recorded in the same shape as the original's.

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
model or from surroundings that changed since the original ran. A control costs about what the
original did. Which other arms run is [OQ-MB1](#OQ-MB1).

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
| A harness's session search, which indexes the original's session | The replay's jail starts with an empty session index |
| A harness's memory notes, which may record lessons from the original | The replay's jail starts with empty memory, the same for every arm |
| The remote, which has the landed trunk | No remote in the clone, and no Git or GitHub grant for that jail |
| The brief itself, when it carries an earlier attempt's findings | The shortlist rule ([Section 4](#4-which-runs-are-shortlisted)) admits only briefs that stand alone |
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
  have is recorded as **not run** with the reason, and never as a pass.
- **The registry is compared.** The original verdict records the digest of the check registry it
  used. The packet says whether the replay's gate used the same one.
- **The item's criteria are not judged again.** The owner reads the work first; no rubric exists
  to judge against.

Whether the gate program can run against a freshly made root is untested. The dry run in
[Section 8](#8-what-would-happen-in-order) finds out without running a model.

### 3.7 When something goes wrong

| What happens | What the round does | Who finds out, and how |
| :--- | :--- | :--- |
| An arm ends with a non-zero exit, or without the report's result line | Keeps what it produced; does not run it again | The packet row says so in the model's own terms |
| The provider refuses for quota or a fault | Marks the arm **not run: provider**, which is a different word from failed; it may be run again within the limit | The packet row |
| The round's spending limit would be passed by the next arm | Stops before starting it | The packet lists the arms that did not run |
| One replay spends far more than its original did | Undecided: stopped, or only marked ([OQ-MB10](#OQ-MB10)). Either way what it produced is kept | The packet row |
| The round is interrupted | On restart, an arm with a finished run record is never run again; a half-made replay root is deleted and made afresh | The round's own log |
| A frozen input turns out to be missing | The task is dropped from the round | The freeze and the packet both name the missing piece |

A replay has no wall-clock limit, as a drain run has none. What bounds a replay is money.

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
| **Keep the frozen commits as refs in the working repository** | Rejected. Run refs clutter the history a person reads; a bundle needs none |
| **Rewrite the brief's paths to wherever the replay is** | Rejected. It edits the one input that is meant to stay fixed, and it is needed only in the same-jail arrangement below |
| **Replay in the loop's own jail, in a separate clone** | Rejected. Nothing for the owner to start, but the answer stays reachable, the brief's paths must be rewritten, and a replay that read the answer is found only after it has been paid for (P3, P4) |
| **Rebuild the original's surroundings** from the log's recorded prompt | Rejected. The prompt text is kept; the harness build and the model version are not, so the result would look faithful and not be |
| **Have a model score or rank the results** | Rejected for now. The owner reads first, and the rubric comes from the reading |
| **Use a public benchmark's tasks** | Rejected. The subject is this repository's own work, and its records already exist |

## 4. Which runs are shortlisted

A recorded run is on the shortlist only when all of these hold.

1. **It finished once.** Its run id appears in exactly one `run.finish` event, with a normal exit.
2. **Its work was good enough to keep.** The gate passed its change, and the item has landed. A
   task nobody finished gives no reference to read the replays against.
3. **Its inputs are all there.** `.tillr/loop/<run>/` holds the brief, the allowed paths and the
   starting commit, and Git still has that commit.
4. **Its brief stands alone.** It carries no facts from an earlier attempt, and it reads the item
   from the tree. A first attempt is preferred. A later attempt is admitted only when the earlier
   ones landed nothing and ran in other sessions. A brief that names a conflict record is admitted
   only when that record was kept.
5. **It needs nothing outside the tree.** Its allowed paths touch nothing the replay root cannot
   hold, such as host configuration.
6. **Its spend was recorded**, so a replay's cost has something to stand beside.
7. **Together they vary** in size and in kind: mechanical coding tasks from the smallest
   one-cause fix to a change across many files, and design tasks from a new document to a short
   ruling on a conflict.

No shortlist is frozen. Choosing it is the first step of [Section 8](#8-what-would-happen-in-order),
and two facts apply to whatever is chosen:

- **Every record has a deadline.** Until a run's inputs and commits are copied, Git's housekeeping
  and the loop record's own cleanup can remove them
  ([Section 1.2](#12-what-is-not-kept-or-does-not-survive)).
- **Every record passes a dry run before a model is paid for.** Applying the original's own
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
- **No totals, no ranking, no color for a winner, and nothing summed across tasks.**

### 5.2 For a coding task

- **Each arm's change as a patch**, and its report beside it.
- **A reading clone:** the starting commit with one branch per arm, so the owner can diff any two
  arms, or an arm against the start, in whatever diff tool he uses.
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

- **Packets stay in the jail's scratch area**, outside the tracked tree. They hold several
  models' versions of a design, and a design can carry work definitions: committed among the real
  designs, a sync would read them as work to file, and a reader could take one for the live
  design.
- **The owner's notes file and the packet's opening page are committed under `docs/`.** The notes
  are the one part that cannot be made again. The committed opening page states its figures and
  cites no scratch path, as this repository's rule for tracked documents requires.

## 6. How this relates to planning and model tiering

[Planning and model tiering](./tillr-planning-node-model-tiering.md) decides which model does
which kind of work. Its
[Section 1.5](./tillr-planning-node-model-tiering.md#15-what-the-history-cannot-say) lists what
the run history cannot say, among them a cheaper model's failure rate and turn count on this
repository's work.

The two designs measure different things, and neither replaces the other:

| | The tiering design's cost report | A replay round |
| :--- | :--- | :--- |
| What is compared | What landed items cost in production, before and after a change of model or workflow | Several models on the identical task, from the identical start |
| What it can show | Whether the bill moved | How each model's work differs, and how each fails |
| What it cannot show | Whether two models would have done the same task differently: no two items are the same | A rate, or anything about the live path |

**What a replay could give the tiering design:**

- **A cheaper model's turns, tokens and cost on a task a dearer one is known to have passed**,
  beside a control run of the dearer one in the same round.
- **What a cheap model's failure looks like here:** a wrong change that passes the gate, a change
  that fails it, or no change at all.

**What a replay could not give it:**

- **A rate.** A handful of tasks, run once or twice, shows whether something works and how it
  fails. It does not measure how often.
- **Anything about a task no recorded run performed.**

The two stay separate documents that link to each other.

## 7. What this design does not do

- It replays nothing and runs no model. It is a proposal.
- It computes no score, and proposes no rubric.
- It does not change the drain, the lander, the gate or the store.
- It does not make old runs reproducible: what [Section 1.2](#12-what-is-not-kept-or-does-not-survive)
  lists as lost stays lost.
- It does not propose that the drain keep more per run. Several rows of that table suggest it, and
  that is work for another design once a round has shown which gaps matter.

## 8. What would happen, in order

Steps 1 and 2 run no model and wait on no question. Everything after them waits on the owner
saying he is ready to run a round.

1. **Choose and freeze the shortlist.** Copying only. The frozen records go to the jail's scratch
   area. A record's size is dominated by its bundle; records that share history can share it, as
   long as each replay's clone still ends at its own starting commit.
2. **Dry-run each record**, as [Section 4](#4-which-runs-are-shortlisted) describes: the
   original's change, in a replay root, through the gate. No model runs. This proves the replay
   root and the gate work together, and drops any task that cannot be replayed.
3. **The owner picks the first round** ([OQ-MB1](#OQ-MB1)) and starts its jail.
4. **The round runs**, one replay at a time.
5. **The owner reads the packets** ([OQ-MB5](#OQ-MB5)) and writes his notes.
6. **A larger round, or a rubric**, whichever the reading calls for. That is a new decision, and
   this document does not make it.

### 8.1 What a first round can be made of

These are the separate choices inside [OQ-MB1](#OQ-MB1), kept apart here so each can be changed
alone. A round's cost is estimated from the originals' recorded spend, on the assumption that a
cheaper model needs the same number of turns, which is the very thing not yet measured. The
control arms are most of the cost, because each costs about what its original did.

| Choice | Smallest | Largest |
| :--- | :--- | :--- |
| **Tasks** | Two: the cheapest coding task and the cheapest design task | The whole shortlist |
| **Models beside the control** | One for each kind of task | Every model on every task, with one comparison of harnesses on the same model |
| **Controls** | One for each task | Two on the cheap coding tasks, to show how far one model differs from itself |
| **Spending limit** | A few times the two originals' spend | Set from the first round's real costs |

## 9. Risks

| Risk | What limits it |
| :--- | :--- |
| A replay finds the original's answer, and the comparison is worthless | [Section 3.5](#35-kept-from-seeing-the-original), and the leak check printed in every packet |
| One run per arm is taken for a property of the model | The control arm, and a second control where one is paid for, show how far two runs of one model differ; the packet never sums across tasks |
| The gate passes a poor change, and a pass is read as quality | The packet puts the gate result beside the work, and the owner reads the work |
| A round draws on the quota the live loop needs | One replay at a time ([Section 3.3](#33-varying-the-model-and-nothing-else)) |
| A cheap model loops and spends without finishing | The round's spending limit ([OQ-MB1](#OQ-MB1)), and what [OQ-MB10](#OQ-MB10) decides |
| A replayed design holds work definitions, and a sync imports them as live work | The packets are outside the tree a sync reads ([Section 5.5](#55-where-packets-and-notes-are-kept)) |
| The records are cleaned up before they are frozen | The freeze is the first step and waits on no question ([Section 8](#8-what-would-happen-in-order)) |
| The original's model is retired before a control can be run | The packet names the model each arm really used; a missing control is stated, never replaced silently |

## Decision Ledger

The owner has ruled on nothing in this document. A **designer's decision** was made by a design
seat from the evidence named, and the owner may overturn it. A **merged** row says where its
question went.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-MB2 | **Merged into [OQ-MB1](#OQ-MB1).** How many tasks the first round replays is one of that question's choices, since the number of tasks, the models and the limit cannot be chosen apart | 2026-11-18 | [Section 8.1](#81-what-a-first-round-can-be-made-of) | — |
| OQ-MB3 | **Designer's decision; the owner may overturn this.** Every task has one control arm: the original's own model run again in the round. **Evidence:** the surroundings are not recorded, so a replay read against the recorded original cannot put a difference down to the model. **Rejected:** no control, which saves the control's cost and makes the comparison unreadable; two controls everywhere, kept as a paid extra on cheap coding tasks | 2026-11-18 | [Section 3.3](#33-varying-the-model-and-nothing-else) | — |
| OQ-MB4 | **Merged.** The round's spending limit is part of [OQ-MB1](#OQ-MB1). Its second half, stopping one run at three times its original's cost, is asked on its own as [OQ-MB10](#OQ-MB10) | 2026-11-18 | [Section 8.1](#81-what-a-first-round-can-be-made-of) | — |
| OQ-MB6 | **Designer's decision; the owner may overturn this.** A round runs in a separate jail, which the owner starts with one command. **Evidence:** in the loop's jail the landed answer to every task is one command away, and principles P3 and P4 require that a replay cannot reach it. **Rejected:** the same jail with a separate clone, which can only report after the fact that a replay read the answer | 2026-11-18 | [Section 3.5](#35-kept-from-seeing-the-original) | — |
| OQ-MB7 | **Designer's decision; the owner may overturn this.** The shortlisted records are copied before any round is approved. **Evidence:** nothing guarantees that a landed run's own commits or its loop record are kept; copying runs no model, changes nothing in the queue, and is undone by deleting the copy. **Rejected:** waiting, which can lose the originals | 2026-11-18 | [Section 8](#8-what-would-happen-in-order) | — |
| OQ-MB8 | **Designer's decision; the owner may overturn this.** This stays a separate document from planning and model tiering, each linking the other. **Evidence:** that design changes how the loop runs and this one only reads what it has done; side-by-side reading of design documents has nothing to do with routing. **Rejected:** folding this in as that design's measuring stage | 2026-11-18 | [Section 6](#6-how-this-relates-to-planning-and-model-tiering) | ✅ |
| OQ-MB9 | **Designer's decision; the owner may overturn this.** Packets stay in scratch storage; the owner's notes and each packet's opening page are committed under `docs/`. **Evidence:** committed packets hold work definitions a sync would file as work, and the notes are the one part that cannot be made again. **Rejected:** committing whole packets behind an exclusion that must hold; committing nothing, which leaves the notes in storage that is cleaned up | 2026-11-18 | [Section 5.5](#55-where-packets-and-notes-are-kept) | — |

## Open Questions

All three are blocked until the owner is ready to run a round. The freeze and the dry run do not
wait on them.

1. 🔒 **OQ-MB1: How big should the first replay round be, and how much may it spend?**

   You decide to try a round. Each shortlisted task is run again from its starting commit, once on the model that originally did it, as a control, and once on each other model you want to see. You then read one packet per task. Nothing has been replayed yet, so nobody knows whether a replay reproduces a gate pass or whether a packet is readable.

   - **A — Two tasks, a small limit.** The cheapest coding task and the cheapest design task, with one other model each. Two packets.
   - **B — About half the shortlist.** Several packets, some holding long design documents.
   - **C — The whole shortlist.** One packet for every task, and the largest bill.

   The other model is yours to name.

   <!-- vantage: question id=OQ-MB1 blocked-on="owner-ready-to-run-a-round" leaning="Two tasks, a small limit (A): the first round's real product is knowing whether the replay and the packet work, and the next limit is better set from its real costs." -->

   _Leaning:_ Two tasks, a small limit (A): the first round's real product is knowing whether the replay and the packet work, and the next limit is better set from its real costs.

   Blocked until you are ready to run a round. The choices inside each option are in [Section 8.1](#81-what-a-first-round-can-be-made-of).

   **Answer:**
   > _(empty — fill in when decided)_

2. 🔒 **OQ-MB5: Do you want to read the results without knowing which model made which?**

   You open a packet with three results side by side. You already hold views on these models. If each result is named, those views read along with you. If they are lettered, the cost must be hidden too, because a cheap result beside a dear one gives the model away.

   - **A — Names shown.** Model and cost sit beside each result from the start.
   - **B — Hidden until your notes are written.** Results are lettered and cost is on a second page. You write your notes, then open the key. You may open it first.
   - **C — Hidden for design tasks only**, where taste weighs most; coding packets show names.

   <!-- vantage: question id=OQ-MB5 blocked-on="owner-ready-to-run-a-round" leaning="Hidden until your notes are written (B): a rubric found while knowing the names inherits the views you already hold." -->

   _Leaning:_ Hidden until your notes are written (B): a rubric found while knowing the names inherits the views you already hold.

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
