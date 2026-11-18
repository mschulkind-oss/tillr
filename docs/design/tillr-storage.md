---
title: "The item graph's record is a log per item, written by one process"
status: in-review
stage: DESIGN
updated: 2026-11-17
tags:
  - tillr
  - storage
  - graphs
  - drain
  - design
summary: "A proposal for the store the item graph needs, argued from trials of four structures run on 2026-11-17. One process applies every write; the record is an append-only log per item, in which a change and its event are the same line; SQL becomes a rebuildable index nothing waits on. The rest of tillr sees a narrow interface with no way to run its own code inside a write, so the structure behind it can change again."
next: "Run the six trials of Section 10 on a prototype of the recommended structure, which was not itself measured, and classify today's tables by the rule of Section 9.3. The owner rules OQ-TS1 to OQ-TS3 from the results. The three remaining lock faults are fixed now and wait on nothing. A link from the tillr roadmap is owed so the questions are routed."
depends-on:
  - tillr-graph-is-the-path.md
---

# The item graph's record is a log per item, written by one process

**Status:** 2026-11-17. A proposal; none of it is built. It answers [OQ-GP2 of the graph design](./tillr-graph-is-the-path.md#OQ-GP2). The numbers come from trials run on 2026-11-17 and reproduced in [Section 3](#3-what-was-measured) with their caveats. The recommended structure is a combination the trials measured only in parts, and [Section 10](#10-trials-owed-before-building) says which parts.

> **In short.** The model already says a graph is a history, so the record should be that history: one append-only log per item, where a change and its event are a single line. One process applies every write, which turns the fog-of-war checks and every cross-item check into ordinary code with nothing to lock. SQL survives as an index that can be deleted at any moment.

**Why it matters.** Eleven store incidents are on record for 2026-11. Five came from SQLite's one-writer file lock under many processes, four from how tillr used it, two from the file system. The file system part does not go away: under load 40 to 120, every trialed variant that waits on a flush showed 3 to 5 second maximum write latencies.

**The shape.** Four parts: a narrow interface with no caller code inside a write; one writer process, which is whoever holds a kernel lock; a log per item plus one for the queue; and derived indexes that nothing on the write path reads.

**Cost.** tillr owns a small storage layer instead of borrowing one. About 100 write call sites and the SQL in about 135 files are rewritten onto the interface, and about 1,700 lines of checkpoint machinery are deleted. Ad hoc SQL against the record is lost; it remains against the index.

**Start at [Section 5](#5-the-structure-i-recommend)**, the structure. [Section 4](#4-the-structures-against-the-needs) is why, and [Section 11](#11-what-would-change-the-recommendation) is what would change it.

**Needs your ruling:** [OQ-TS1](#OQ-TS1), [OQ-TS2](#OQ-TS2), [OQ-TS3](#OQ-TS3).

**Reads with:** [the graph design](./tillr-graph-is-the-path.md) (the model this store serves; its Section 10 lists the needs), [the SQLite contention recovery](../recovery/sqlite-contention/README.md) (the incidents and the residual limit in landing), [the store's own reference](../../tools/tillr-store/README.md) (today's checkpoint scheme, which this deletes).

---

## 1. The verdict

1. **The interface comes first and is the expensive part.** Every structure worth choosing needs callers to stop composing transactions. Once they have, the structure behind the interface is a few hundred lines and can change again.
2. **One process writes.** Not as the least change, but because the model's cross-item checks (a lane used once, a premise on another item's node) become reads of that process's memory instead of locks across files.
3. **The record is a log per item**, with one more log for the queue. PostgreSQL and SQLite both provide the needs of the graph design; the logs provide them with less machinery, cheaper tests, no flush on the write path and a fault that stays inside one item.
4. **The path there passes through the runner-up.** One writer over today's SQLite file is the second step of the migration and a complete stopping point. If the trials of [Section 10](#10-trials-owed-before-building) go against the logs, the work stops there with nothing wasted.
5. **Migration cost is stated, not used as a reason.** Under the graph design most of today's tables stop existing, so their SQL is rewritten once whatever is chosen. The choice is only what it is rewritten into.

## 2. What the store must do

### 2.1 The requirements that are rulings

| Requirement | Source |
| :--- | :--- |
| **Durability survives a process kill, not a power loss.** Every acknowledged write survives the death of any or all processes. A machine crash may lose the last 30 to 60 seconds of writes, and that is accepted | The owner's ruling of 2026-11-16, made when the store moved to `synchronous=NORMAL` in commit `64bee8457` |
| A lock failure gets a root cause and a fix, never a retry or a longer wait | The owner, standing |
| Minimal work under a lock; no time limit fails work; no test depends on load | The owner, standing |
| The loop never stops on one item's condition | R6 of [the graph design](./tillr-graph-is-the-path.md#1-the-direction-and-the-rules) |
| The jail kills every process at once at relaunch, and recovery needs no hand repair | How the jail works |
| Tests create thousands of throwaway stores | 116 test files open a store today |
| The owner inspects state through the command line and the view | The owner, standing |

The first row decides more than it seems to. A store that needs no flush before it acknowledges has no reason to wait on the file system's slowest operation at all, so the design rule is stronger than "do not hold a lock across a flush": **the write path never calls a flush.**

### 2.2 What the graph design needs

[Section 10 of the graph design](./tillr-graph-is-the-path.md#10-storage) lists eight needs. They are taken one by one in [Section 4.1](#41-need-by-need). Two more come from the rest of that document: a store-wide order with a cursor, for the view and for replay; and reads that never block a writer.

### 2.3 What today's store does, and where it failed

Today one SQLite file of about 36 MB holds about 55 tables. Every tillr program opens it, and callers compose their own write transactions through one primitive that takes the write lock, runs the caller's function, and commits. About 135 files outside tests carry SQL; about 100 call sites use the write primitive. The committed write rate is low (85 landings in 9.6 hours on the overnight run of 2026-11-15) while the rate of opens and probes is high.

Eleven incidents are recorded for 2026-11 in [the contention recovery](../recovery/sqlite-contention/README.md) and the commits it names. By primary cause:

| Cause | Count | Examples |
| :--- | ---: | :--- |
| SQLite's one-writer lock and its log restart, under many processes | 5 | A read-then-write transaction refused at once; a closing program checkpointing the log (`91425b408`); the next writer flushing a restarted log inside the lock (`27f04df2d`) |
| tillr's usage | 4 | Git work inside the landing's write (`31ba65672`); a migration transaction on every open; the trace index rebuilt whole each tick |
| The file system | 2 | Flushes of 1.8 to 2.6 seconds under gate load; one of 6.7 seconds |

Three of the five in the first row were reachable only because a multi-second flush sat inside the lock. About 1,700 lines of checkpoint code and about 1,100 lines of its prose exist only to move that flush.

**Usage faults on 2026-11-17.** Three were found and fixed in place: a migration write transaction on every open; a history scan held under the write lock for 4.8 seconds; one lane's failure faulting the loop. Three remain: no shared handle per process (about 85 open call sites in the drain); a planning step that holds the write lock for up to 0.85 seconds; and a write lock held across a git ref move in landing. Separately, the trace index, a derived index in a file of its own, grew to 32 GB and stalled the loop because the loop waited for it.

## 3. What was measured

Four structures were prototyped behind one Go interface (claim a node, append an event, read an item, list ready nodes, audit) and run on 2026-11-17 on a 32-core machine whose one-minute load average stayed between 40 and 120 throughout. The prototypes (about 2,400 lines with their harness) are kept outside the repository, so these numbers can be checked only by running them again or bringing them in.

| Id | Structure |
| :--- | :--- |
| A | Today's shape: every process opens one SQLite file, write-ahead log, no flush per commit |
| B | One daemon owns the same SQLite file; clients send it requests over a unix socket; a client that finds none starts one |
| C | One append-only log file per item; a writer holds a kernel lock on that log for one `write`; no SQLite. C-sync adds a flush before each acknowledgement |
| D | PostgreSQL 18.6, rootless, unix socket; one stored function per write. D-async turns off the wait for the flush |

**Workload** (60 seconds, 200 items of 50 events preloaded): one loop process, 20 lane processes writing in bursts, 5 short command processes a second.

| | Writes done | Lane write p50 / p95 / p99 / max (ms) | Errors |
| :--- | ---: | :--- | :--- |
| A | 47,740 | 0.12 / 13.20 / 114.81 / 5,020 | 1 "busy" after the 5 s timeout |
| B | 54,534 | 0.35 / 3.93 / 43.57 / 5,370 | none |
| C | 68,487 | 0.29 / 1.39 / 3.57 / 1,389 | none |
| C-sync | 47,745 | 2.55 / 9.99 / 23.91 / 3,863 | none |
| D | 43,402 | 3.85 / 15.82 / 53.92 / 3,367 | none |
| D-async | 74,138 | 0.12 / 0.85 / 1.91 / 10 | none |

**Exactly once, staleness and kills.** In the race, 20 processes each tried to claim the same 1,000 nodes. In the kill trial, 8 writers and any server were sent SIGKILL in random order, 50 times, and every acknowledged write was then looked for.

| | Race: double claims of 20,000 attempts | Race wall time (s) | Kill: acknowledged events, problems | First read after the kill, p50 / p95 / max (ms) |
| :--- | :--- | ---: | :--- | :--- |
| A | 0 (2 "busy" errors) | 6.49 | 21,156, none | 12.9 / 40.3 / 494 |
| B | 0 | 1.93 | 72,907, none | 31.3 / 1,273 / 6,639 |
| C | 0 | 0.59 | 26,312, none | 1.5 / 4.1 / 5.8 |
| C-sync | 0 | 2.89 | 12,070, none | 0.7 / 2.8 / 3.9 |
| D | 0 | 143.93 | 9,516, none | 761 / 1,485 / 3,375 |
| D-async | 0 | 0.32 | 1,544,382, **about 15,855 lost** | 766 / 2,097 / 4,809 |

A claim made against a revision that had since moved was refused in every variant, and of 20 processes claiming different nodes of one item from the same revision exactly one won, on all 49 items tried.

**Cost of a store for a test, and size.**

| | One store: create, write, read, remove (ms) | Lines in the storage layer |
| :--- | :--- | ---: |
| A | 15.07 | 282 |
| B | 23.54 in-process; 27.65 with a daemon started (100 stores) | 541 |
| C | 0.13 (1.93 with a flush) | 273 |
| D | 173.78 as a database cloned on one shared server; 2,947 as a server of its own (5 stores); 4,390 once to create the first | 313 |

**What the trials found that the earlier survey did not predict.**

- **A:** the write-ahead log reached 93 MB in 60 seconds (B's ended at 4 MB), and a short command's open took 699 ms at p99.
- **B:** 17 of 21 processes started a daemon in the first second and 16 lost; the daemon used 27.9 CPU-seconds in 60, more than all of A's processes together; the worst first read after a kill was 6.6 seconds.
- **C:** no write can span two items; listing ready nodes cost a directory read and a `stat` of every log.
- **D:** PostgreSQL refuses to run as root and the jail is uid 0 in a user namespace, so it ran as a mapped uid behind a preloaded library that supplies a password-file entry. After a kill, the next start failed with "pre-existing shared memory block is still in use" in 14 of 50 rounds, then 11, then on 34 attempts in the final run, and a wrapper had to start it again until it stayed up.

**What the numbers cannot carry.**

- **The maximums cannot rank the variants.** One run each, in sequence, on a machine whose load moved. The 3 to 5 second maximums appear in A, B, C-sync and D, and not in D-async, which is the one variant that waits on no flush. A probe at a quiet moment measured a 0.6 ms median flush. The stalls belong to the file system under load. C, which also waits on no flush, still showed one 1.4 second write, so a plain `write` can stall too; removing the flush removes the worst of it, not all of it.
- **Throughput is not capacity.** The offered load was fixed by sleeps.
- **B's daemon is crude:** JSON framing, one write connection, no batching. Its processor cost is the prototype's.
- **C's kill result rests on the page cache**, which survives a process kill. Its torn records were injected (46, all repaired by the next writer), not natural.
- **The check trusts each writer's own record of what was acknowledged**, so a present event with a wrong payload would pass.
- **Not run:** a daemon that is stuck and not dead; any Python client; any write across two items; power loss; a shared SQLite index over the logs; more than 20 lanes; and the structure recommended here.

## 4. The structures against the needs

### 4.1 Need by need

Three structures are compared: **B**, one writer over SQLite; **D**, PostgreSQL with its flush on, since without it acknowledged writes were lost; and **L**, the structure of [Section 5](#5-the-structure-i-recommend), one writer over a log per item. L was not trialed whole: its file layer is C's and its lifecycle is B's.

| Need | B: one writer, SQLite | D: PostgreSQL | L: one writer, a log per item |
| :--- | :--- | :--- | :--- |
| **A change and its event are one atomic write** | Two rows in one transaction | Two rows in one transaction | One line. There is no second thing to be atomic with |
| **An ordered history for each item** | A query by item | A sequence per item, taken under the item's row lock | The file, in order |
| **A claim happens exactly once** | 0 doubles | 0 doubles; the race took 144 s because every winner waited on a flush | 0 doubles in C. Under one writer the check is in memory and the race cannot arise |
| **Compare against the revision read** | Refused when stale | Refused when stale | Refused when stale |
| **Kill safety** | 0 problems in 50 rounds | 0 problems in 50 rounds, once the harness restarted the server until it stayed up | 0 problems in 50 rounds for the file layer; a torn tail is cut by the next writer |
| **No git, file or process work inside a write** | True only if callers cannot run code inside a transaction, which is the interface's doing and not SQLite's | The same | The same; a write is one `write` call of one line |
| **A store for a test costs almost nothing** | 15 to 28 ms | 174 ms on a shared server that every test then depends on | 0.13 ms |
| **Premises are checked in the same write** | In the transaction | In the stored function | In the writer, before the one line is appended; nothing else can write between |
| **A store-wide order and cursor** | Free with one writer | Not free: a sequence number can commit out of order, so a reader can skip a row unless appends are serialized | A sequence number the writer stamps on every line |
| **No flush on the write path** | Not quite: after a checkpoint the next write restarts the log and flushes its header (the incident fixed in `27f04df2d`), and with one writer everyone queues behind it | No: removing the flush lost about 15,855 acknowledged writes | None anywhere |
| **Inspection** | `sqlite3`, read-only | `psql` | The item's log is text; cross-item questions go to the index |
| **Go and Python** | A request protocol in both, or Python through a command | Mature clients in both | A Go library; Python through a command that prints JSON |
| **A fault stays in one item** | One file; a damaged page is everyone's | One server; its failure to start is everyone's | A damaged log is that item's |

**Where "in the same write" is weakest.** In B and D the check and the change share a transaction the engine enforces. In L they share a critical section the writer enforces: one process, one request at a time, check then append. That is as strong as the transaction only while exactly one process holds the writer lock, so the lock is the thing to test hardest ([Section 10](#10-trials-owed-before-building), trial 2).

### 4.2 Does the model need a write across two items?

C has none, and L keeps that limit on purpose. With one writer, the question splits into two that have different answers.

**A check across items needs no cross-item write.** The writer holds every open item's graph in memory, so "is this lane already running a node of another item" or "does another item's edge hold this node as a premise" is a read of its own memory, made in the same critical section as the append.

**An effect across items is two steps in the graph design already.** An ending is data, and the policy function adds what the graph lacks, with the property that applying a result and evaluating again returns nothing. Every cross-item act I can find in the model takes that form:

| Act | First write | Second write | A kill between them leaves |
| :--- | :--- | :--- | :--- |
| A landing | The land node is claimed in the item's log. The lander moves the git ref with no store write open | The node's ending `landed`, with the trunk commit | A claimed node with no ending: an exception. The node resumes, finds the ref already moved, and writes the ending |
| Close against land | Closing is a transformation of the same item's graph | — | Nothing: one write. It is refused while a land node is claimed |
| A lane is used once | The claim names its lane | — | Nothing: one write. Lane occupancy is derived from running nodes |
| An inter-item edge from the queue planner | A line in the queue's log naming both items and the facts read | — | Nothing: one write. The writer checks the pinned facts when either item is written |
| An ending that files another item (a mechanic, a triage result) | The ending, naming what is to be filed | The new item's first line, added by the rule that answers the ending | An ending with no successor, which the next evaluation answers |

So my answer is that the model does not need it. I hold that with less confidence than the rest of this document, because I checked it against the sections of the graph design that define the model, the invariant and the storage needs, and not against its queue-level sections line by line. That check is trial 3. If it finds an act that must change two items or neither, the on-disk form becomes one log for the whole store, where one line can carry both changes, behind the same writer and the same interface.

### 4.3 The verdicts

| Structure | Verdict |
| :--- | :--- |
| **A — many processes, one SQLite file** | Rejected. It is the cause of five recorded incidents, and the fixed faults do not remove the one-writer queue or the flush inside it |
| **B — one writer over SQLite** | The runner-up, and the second step of the migration. It removes cross-process lock failures by construction. It keeps the engine's log restart on the write path, a second representation of every change (the row and its event), and a store that costs a test 15 ms or more |
| **C as trialed — many processes, a log per item, no writer** | Rejected. It has no daemon to go wrong, which is a real merit in a jail. But every cross-item check then needs several file locks taken in order, the store-wide sequence needs a lock every write holds, and every short command rebuilds the cross-item picture for itself |
| **D — PostgreSQL** | Rejected. It buys concurrent writers for a workload that commits a few times a minute. The price is a flush on every commit (or lost writes), a server that failed to restart after a kill in up to 14 of 50 rounds until a wrapper insisted, a preloaded library to get past its refusal of uid 0, a test cost above a tenth of a second on a server all tests share, and a data migration at each major version |
| **Other servers** (Redis or Valkey, NATS JetStream, etcd, FoundationDB, a durable-execution engine) | Rejected from the survey, without trials. Each replaces the relational record with keys or streams, so each costs the same rewrite as L, and adds a server, a flush policy to get right (NATS acknowledges before a flush that runs every 2 minutes by default) and a lifecycle |
| **Other embedded engines** (LMDB, bbolt, DuckDB, the SQLite branches with two logs or concurrent writers) | Rejected from the survey. LMDB and bbolt have one writer too; DuckDB allows one writing process; the SQLite branches are not in any Go driver |
| **L — one writer over a log per item, SQL as a derived index** | Recommended, subject to the trials of [Section 10](#10-trials-owed-before-building) |

> [!WARNING]
> "One writer over SQLite keeps the callers' SQL" is true only if the daemon accepts SQL and lets a caller hold a transaction open across requests. That keeps the fault this design exists to remove: a caller doing slow work inside a write, now with a socket in the middle and a killed client to clean up after. A daemon with a narrow interface costs the callers the same rewrite as the logs do.

### 4.4 What each structure does to the files that write SQL

About 135 files, about 515 SQL statements and about 100 write call sites, outside tests.

| Structure | What happens to them |
| :--- | :--- |
| A, or B with SQL passed through | Unchanged. The write primitive that runs caller code under the lock stays |
| D | Every statement is ported: about 150 uses of SQLite's dialect (`PRAGMA`, `INSERT OR IGNORE`, `json_extract`, abort triggers), and every test helper that opens a path opens a server instead |
| B with a narrow interface, or L | Every write call site is rewritten as a call on the interface. Reads in the loop become interface reads. Reads in the view and the evidence tools stay SQL, against the index |

The graph design changes the baseline for all three rows. Runs, dispatches, retries, holds, landing attempts and node runs become nodes of item graphs, so the SQL over those tables is rewritten by that design's steps whatever the store is. What this design adds is the direction of the rewrite: onto the interface, once.

## 5. The structure I recommend

### 5.1 The words

- **The writer** *(coined here)*: the one process that applies writes to a store. It is whichever process holds the store's writer lock, a kernel `flock` on a file in the store directory, which the kernel releases when the holder dies. It is not the loop, and it is not "the owner", which in tillr's documents means the person.
- **An item log** *(coined here)*: the append-only file holding one item's history, one text line per record. It is the record of that item; nothing else is.
- **The queue log** *(coined here)*: one more log of the same form for what belongs to no single item: the list of items, the rank, the inter-item edges, and the facts the policy function is permitted to read.
- **The store sequence** *(coined here)*: a number the writer stamps on every line, increasing by one across all logs. It is the cursor readers resume from. It is not an item's revision, which counts accepted changes to that item's graph only.
- **A derived index** *(coined here)*: any file built from the logs to answer a question faster. It can be deleted at any time and rebuilt. It is never the record.

### 5.2 The record

- **One line is one record:** a JSON object ending in a newline, carrying its kind, its store sequence, the item's revision after it, the request id that caused it, and a check value over the rest. Text, so that `rg`, `tail` and `jq` read it; the trial's binary framing made plain tools awkward.
- **What a record holds:** a transformation (nodes and edges added, replaced or removed, with the rule and the facts read); a claim; an ending; a refusal with its reason; or bookkeeping for a running node (a process segment, an exception, a progress note, a hoisted child). Bookkeeping and refusals do not advance the item's revision.
- **Large content is not in the log.** Briefs, transcripts and outputs stay files beside the store, as they are today, and a record names them by content hash. A record over 1 MiB is refused.
- **Liveness is not a record.** A running node's holder is known by a kernel lock the runner holds for as long as it lives, so there is no heartbeat row to rewrite every few seconds and nothing to expire when every process dies at once. An interruption is a record, because it is read afterwards.
- **A format version** is the first line of every log. A log written by a newer program is refused by an older one, as today's schema version is.

### 5.3 What a write does

One request at a time, the writer:

1. **Looks up the request id.** If that request was already applied, it returns the answer it gave then. This is what lets a client send a request again after a lost connection.
2. **Checks.** The staleness comparison and the ten refusals of [Section 4.3 of the graph design](./tillr-graph-is-the-path.md#43-what-the-store-refuses) are pure functions of the graph in memory and the change. A refusal is appended as a refusal record and returned.
3. **Appends one line** to one log with a single `write` call, and updates its memory.
4. **Answers** with the new revision and store sequence.

It never calls a flush, never runs a command, never touches git, and never waits on anything but that `write`. A caller has no way to put code between steps 2 and 3, which makes "no git, file or process work inside a write" a property of the interface instead of a rule to remember. The fog checks are written once as pure functions and are the same code in every structure this design passes through.

### 5.4 Reads

- **The loop reads from the writer:** the graph of an item, the ready nodes, the queue. These are answers from memory at a stated store sequence.
- **Anything may read an item's log directly**, without the writer and without a lock: a reader takes complete lines and ignores an incomplete last one. So an item page and a command that shows an item work while the writer is busy, stuck or absent, and no reader can hold anything a writer needs.
- **Store-wide reads** (events since a cursor, the head sequence, wait until the head passes a number) come from the writer. The head is the cheap change signal the view polls for today.
- **Python reads through a command that prints JSON.** No Python program writes the store today, and none will.

### 5.5 Kills, and a machine crash

- **A process kill** leaves every acknowledged line in the page cache and at most one torn last line in one log. The writer cuts a torn tail when it next opens that log. Nothing else is needed, and nothing is left locked, because every lock is the kernel's.
- **A machine crash** is outside the durability requirement, but recovery must still need no hand repair. Each log may have lost a different amount, so the surviving lines can have a gap in the store sequence: a later line kept in one log, an earlier one lost from another. At its first start after a boot (it records the kernel's boot id), the writer finds the first missing sequence number, moves every line above it into a side file, and reports what it moved. The store is then a consistent earlier state. This is the last seconds the ruling already accepts losing.
- **A damaged line that is not at a tail** is a defect of that log only. The item is shown as damaged with the line's position, the writer refuses writes to it, and every other item drains.

### 5.6 The writer's life

- **Starting.** A client that cannot connect to the socket in the store directory starts the writer detached and connects when it is listening. Any number may start one at once; all but the holder of the lock exit at once. The trial saw 17 starts and 16 immediate exits in the first second, and a daemon started by the next client in all 50 kill rounds.
- **At start** the writer takes the lock, removes a socket left by a dead predecessor (safe, because it holds the lock), reads the logs, cuts torn tails, and listens. Today it reads every log; [trial 1](#10-trials-owed-before-building) measures that at 10 and 100 times today's volume, and a derived list of finished items may let it skip them.
- **Stopping.** It exits after 60 seconds with no client connected, a setting. It needs no clean shutdown and has none.
- **In a test** the writer runs inside the test process on a temporary directory, with no socket. It is the same code holding the same lock.
- **Waiting is never a failure.** A client waits for the writer with no time limit and says what it is waiting on. What may be done about a writer that has stopped answering is [OQ-TS3](#OQ-TS3).
- **Host and jail** share the store directory and one kernel, so the lock and the socket work across them. Process ids do not, so a client on one side cannot signal a writer on the other; it can only wait and say so. Not trialed.

### 5.7 Failures and edge cases

| Case | What happens |
| :--- | :--- |
| An empty store | Valid. The head is 0 and there is nothing ready |
| An item with no nodes | Valid |
| A request for an item that does not exist | Refused with that reason |
| The same request sent twice | The first answer, both times |
| Two requests for one item from one revision | The second is refused as stale and the caller recomputes |
| The disk is full | The writer cuts the log back to its length before the write and refuses with that reason. Reads continue. The loop shows one named wait for the whole store, whose mover is the person |
| The store directory's path is too long for a unix socket (107 bytes) | The writer refuses to listen and says why; the in-process form still works |
| A record kind this program does not know | That log is refused as newer than the program |
| A plain copy of the directory taken while the writer runs | Opened like a machine crash: cut to the first gap. A backup command asks the writer to copy at one store sequence instead |

## 6. The interface the rest of tillr sees

The interface is in the graph design's words and names no engine. Every write carries a request id chosen by the caller and returns the item's new revision and the store sequence, or a refusal with a reason.

| Call | What it does | Refused when |
| :--- | :--- | :--- |
| **Transform an item's graph** at the revision read, with the rule and the facts read | Adds, replaces or removes nodes and edges in fog | Stale revision; any of refusals 1 to 6, 9 and 10 of the graph design |
| **Claim a node** at the revision read, with its premises and what the scheduler chose | Fixes the node. Exactly one caller wins and the loser is told who did | The node is not in fog, a source is not done, the lane is in use, or the revision is stale |
| **End a node** with its ending | Makes the node done | It has an ending, or the cause is not in its kind's list |
| **Note against a running node** | Records a segment, an exception, a progress note or a hoisted child | The node is not running |
| **Create an item**, and **revise an item's definition** | Starts an item's log; records the definition in force | The id exists; the revision is stale |
| **Change the queue** at the queue revision read, with the item facts read | Sets rank; adds or removes an inter-item edge | Stale; the edge would constrain a claimed node |
| **Record a fact** | Appends a fact the policy function may read, such as a backend's reopening time | The fact's kind is not declared |
| **Read an item** at a revision, the latest by default | The graph, its revision and the store sequence | — |
| **List ready nodes**, up to a limit | Nodes in fog whose sources are done and which no inter-item edge holds, in rank order | — |
| **Read the queue** | Items, rank, inter-item edges, lanes in use | — |
| **Events since** a store sequence; **the head**; **wait past** a sequence | The cursor reads | — |

**What replaces direct SQL.** A caller that today opens a write transaction and runs statements inside it makes one of the calls above. There is no call that takes a function, a statement or a transaction. A caller that today reads tables to decide something asks for the graph and decides in its own memory. A caller that reads to display or to analyze (the view, the evidence tools, the trace indexer) reads a derived index with SQL, or the cursor.

**What this leaves out on purpose.** The interface has no write across two items ([Section 4.2](#42-does-the-model-need-a-write-across-two-items)), no way to change a fixed field, and no way to record liveness.

## 7. Derived data

Three rules, for the view's index, the trace index and any later one.

1. **Rebuildable.** A derived index is built from the logs through the cursor and holds the store sequence it has reached. Deleting it loses nothing. Its layout carries a version, and a mismatch means delete and rebuild, never repair in place.
2. **Never on the write path.** The writer does not read, write or wait for any index. An index has one writing process of its own, under its own kernel lock.
3. **Never able to block the loop.** The loop starts an indexer detached and does not wait for it, and decides nothing from an index. A reader that needs the index shows how far behind it is: "index at 4,100 of 4,144".

**Size is shown.** Each index states an expected ceiling, and the view shows its size against it. Passing the ceiling is a reported defect. The trace index reached 32 GB with nothing reporting it, and what turned that into a stall was the loop waiting.

**The view's index** is SQLite, written by one indexer and opened read-only by everything else. During the migration it is today's file with today's tables, which is why the view's queries move last.

**The trace index** keeps its own file and reads the store through the cursor instead of opening the store's file.

## 8. Alternatives inside the recommendation

| Alternative | Verdict |
| :--- | :--- |
| One log for the whole store, the same writer | Not chosen; the fallback. It allows a write across items and makes a machine crash trivially consistent, but a damaged line is everyone's and an item's history is scattered. It becomes the choice if trial 3 finds a cross-item write the model needs |
| The logs as the record and SQLite as an index the loop also reads | Rejected. It puts a derived file on the path that decides, which is how a 32 GB index stalled the loop |
| A command writes for itself when no writer runs | Rejected. It is a second write path that runs only when the system is quiet, so it is never exercised under the conditions that break things |
| The loop is the writer | Rejected. Commands must work with the loop stopped, and a loop fault must not take the store with it |
| Flush in the background on a timer | Not needed. The kernel writes dirty pages back by itself, and the ruling accepts the window. Whether a background flush would even stay off the write path on this file system is unmeasured |

## 9. Migration, while the drain keeps running

### 9.1 What is fixed first, whatever is ruled

The three remaining usage faults are defects under R6 and wait on nothing in this document:

1. **One store handle per process**, shared, in place of about 85 opens in the drain.
2. **The planning step computes outside the write** and holds the lock only to apply its result; it holds it for up to 0.85 seconds today.
3. **The landing moves the git ref with no write open.** The exclusion between closing and landing becomes a short write that marks the landing begun, the git move, then a short write that records it, with the existing marker ref making a repeat harmless.

Each is the same change the interface later makes structural, so none is thrown away.

### 9.2 The order, against the graph design's sixteen steps

[The graph design's migration order](./tillr-graph-is-the-path.md#13-migration-order) puts the storage structure last, at step 16, and puts "the policy function and the store's checks" at step 3. Left that way, the checks are written into SQL transactions at step 3, steps 4 to 15 each add SQL over graph tables, and step 16 rewrites all of it. So storage does not stay at step 16. It splits into four stages, each one shippable and each a place to stop:

| Stage | What moves | Where it sits |
| :--- | :--- | :--- |
| **S1** | The three usage faults | Now; beside step 1 |
| **S2** | The interface of [Section 6](#6-the-interface-the-rest-of-tillr-sees), with the fog checks as pure functions, implemented over today's SQLite file | It is step 3's "store's checks". Steps 4 to 15 write graph state only through it |
| **S3** | One writer: every graph write goes through one process. The record is still the SQLite file. This is structure B, complete | After step 3; independent of steps 4 to 15 |
| **S4** | The record becomes the logs, item by item; the SQLite file becomes the view's index | After S3, after the trials, and after [OQ-TS1](#OQ-TS1) |

Whether step 3 is built over today's SQLite first, as S2 says, or waits for the logs, is [OQ-TS2](#OQ-TS2). I recommend S2 as written for two reasons: no step of the graph design waits on a store that is still being trialed, and the SQLite implementation of the interface becomes the reference the logs are checked against, the way the four prototypes were checked against each other.

### 9.3 How existing state is carried over

- **Into S2:** as the graph design already states, runs recorded before step 3 are imported as done nodes.
- **Into S4, one item at a time.** An item's graph lives in exactly one structure, and a line in the queue's record says which. An item with no claimed node is moved by writing its history to a new log in order and then that marker; a new item starts in a log. An item with a running node moves when the node ends. The drain never stops, and a kill during a move leaves a log with no marker, which is deleted and written again.
- **The comparison.** While both exist, a check folds each moved item's log and compares it with what the SQLite implementation holds for it. A difference is a defect that stops moves, not items.
- **Tables outside the graph.** Each of today's roughly 55 tables falls in one of three classes, by this rule: what the graph design turns into nodes, endings or bookkeeping is **superseded**; what the policy function or the scheduler reads to decide is a **fact** in the queue log; everything else is **derived** when it can be rebuilt from the logs and, when it cannot, a record of another subsystem that keeps its own small log under the same writer. I have not classified the tables one by one, and the count in each class is unknown. That classification is owed before this design is accepted, because a large last class is one of the things that would change the recommendation.

### 9.4 Drafted items

These are proposals, written as item blocks without the item directive so that sync reads none of them and nothing can be dispatched, as in [the graph design's drafted items](./tillr-graph-is-the-path.md#131-drafted-items). A block gets its directive when this design is accepted and its question is ruled.

#### Share one store handle in each process

The drain opens the store at about 85 call sites, once per operation and from many goroutines, so one process queues against itself for the write lock and each open repeats checks the last one made. This item gives each process one handle for its life.

- **Paths:** `tools/tillr-drain/`, `tools/tillr/store/`, `docs/`
- **Waits on:** nothing.
- **c1:** A process opens the store once and every operation in it uses that handle. A test fails when a second writable open happens in one process.
- **c2:** A test runs a fixture pass and asserts the number of store opens is one.
- **c3:** The existing suites pass with no change to what is stored.

#### Compute the plan outside the write

A planning step holds the write lock while it computes, for up to 0.85 seconds as measured on 2026-11-17, and every other writer waits behind it. This item makes it read, compute with nothing held, and take the lock only to apply the result against the revision it read.

- **Paths:** `tools/tillr-drain/`, `docs/`
- **Waits on:** nothing.
- **c1:** The step's write contains only the statements that apply its result. A test asserts no computation runs between the write's start and its commit.
- **c2:** A result computed from state that has since changed is refused and computed again. A test changes the state between the read and the write.

#### Move the trunk in a landing with no write open

A landing holds the store's write lock while git moves the trunk ref, so a slow git stalls every other writer. This item replaces that with a short write that marks the landing begun, the git move with nothing held, and a short write that records the result.

- **Paths:** `tools/tillr-land/`, `tools/tillr-drain/`, `docs/`
- **Waits on:** nothing.
- **c1:** No git command runs while a store write is open in a landing. A test asserts it with a git that blocks.
- **c2:** An item cannot be closed between the two writes. A test attempts it and reads the refusal.
- **c3:** A landing killed after the ref moved and before the second write is finished by the next attempt, which moves nothing and records the landing once. A test kills at that point.
- **c4:** A heartbeat write completes while a landing's git command is blocked. A test asserts it.

#### Run the storage trials on the recommended structure

The storage design recommends a structure that was measured only in parts. This item builds a prototype of the whole of it behind the trial interface and runs the six trials the design names, so the ruling rests on measurements of the thing ruled on.

- **Paths:** `docs/`, `tools/tillr-store/`
- **Waits on:** nothing.
- **c1:** The prototype and its harness are in the repository, and one command reruns every trial.
- **c2:** Each of the six trials has a recorded result in a tracked document: what was run, the numbers and the date.
- **c3:** The variants run interleaved, more than once, so that a slow period of the machine does not fall on one of them.
- **c4:** No trial result is a pass or fail on elapsed time.

#### Put the graph's writes behind one interface

Callers write the store by composing their own transactions, so any of them can do slow work while holding the lock. This item adds the storage design's interface, moves every graph write onto it, and removes the way to run caller code inside a write. It is the "store's checks" half of the graph design's third step.

- **Paths:** `tools/tillr/store/`, `tools/tillr-drain/`, `tools/tillr-item/`, `tools/tillr-run/`, `tools/tillr-land/`, `docs/`
- **Waits on:** Add a stated ending to every node run.
- **c1:** Each call in the design's interface table exists and each refusal it lists is returned with its reason. A test asserts every refusal and an unchanged graph.
- **c2:** The checks are functions of a graph and a change that open no store. A test evaluates them with no store present.
- **c3:** No graph state is written except through the interface. A test fails when a package outside the store writes a graph table.
- **c4:** A request sent twice with one request id is applied once and answered the same way twice.

#### Apply every graph write in one process

Many processes write one file and contend for its lock. This item makes one process the writer, started by the first client that finds none, with the record still in SQLite.

- **Paths:** `tools/tillr/store/`, `tools/tillr-store/`, `docs/`
- **Waits on:** Put the graph's writes behind one interface.
- **c1:** While a writer runs, no other process opens the store for writing. A test asserts it.
- **c2:** Twenty clients starting at once end with one writer. Every process killed at once is followed by a successful write with no manual step. A test does both.
- **c3:** A client waiting for the writer says what it waits on and has no time limit.
- **c4:** A test store runs its writer inside the test process.

#### Keep each item's record in its own log

The record of an item is rows in several tables plus events that repeat them. This item makes it one append-only log per item, moves existing items across one at a time while the drain runs, and turns the SQLite file into an index.

- **Paths:** `tools/tillr/store/`, `tools/tillr-store/`, `tools/tillr-view/`, `tools/tillr-trace/`, `docs/`
- **Waits on:** Apply every graph write in one process; Run the storage trials on the recommended structure; the owner's ruling on the structure.
- **c1:** An item's log, read alone, reproduces its graph at every revision. A test compares it with the SQLite implementation for every fixture.
- **c2:** An item moves only when it has no claimed node, and a kill during a move leaves the item readable and movable again.
- **c3:** Deleting every index file loses nothing: a rebuild returns the same answers. A test deletes and compares.
- **c4:** The loop finishes a pass with the indexer stopped and with the index file missing.
- **c5:** A log damaged in its middle stops writes to that item only, and the view names the item and the position.

## 10. Trials owed before building

| # | Trial | What it settles |
| :--- | :--- | :--- |
| 1 | **The recommended structure, whole.** One writer over a log per item with text lines, behind the same interface, through the same four trials: the workload, the race, 50 kill rounds, and the cost of a test store. All variants interleaved and repeated. Plus the writer's start time at 1, 10 and 100 times today's volume | Whether the combination behaves like its parts. Today's support is C for the files and B for the lifecycle |
| 2 | **A writer that is stuck and not dead.** Stop the writer with SIGSTOP; block it inside a `write`; make it spin in a check. For each: what clients see, whether a second writer can ever run beside it, and what a restart costs | The writer lock, and the facts [OQ-TS3](#OQ-TS3) is ruled on |
| 3 | **Cross-item acts.** Walk every act in the graph design that touches two items or an item and the queue, and write each as single-log writes. Kill between every pair of writes and evaluate again | Whether the model needs a write across items ([Section 4.2](#42-does-the-model-need-a-write-across-two-items)), and so whether the logs are per item or one |
| 4 | **A Python reader.** The trace indexer and the evidence tool read through the JSON command and the cursor, during the workload | That no second implementation of the format is needed |
| 5 | **A simulated machine crash.** Cut random amounts off the tails of random logs, start the writer, and check that what remains is a state the store once held | The cut rule of [Section 5.5](#55-kills-and-a-machine-crash). A real power loss cannot be tested in the jail and is outside the durability requirement |
| 6 | **Host and jail together.** A writer in the jail and a client on the host, and the reverse | The lock and the socket across the bind mount |

The table classification of [Section 9.3](#93-how-existing-state-is-carried-over) is owed alongside them. It is a reading of the tree, not a trial.

## 11. What would change the recommendation

| Finding | Then |
| :--- | :--- |
| Trial 1 loses an acknowledged write, or the whole structure is materially slower to start or to write than B | Stop at S3: one writer over SQLite |
| Trial 3 finds an act that must change two items or neither | One log for the store in place of one per item; the rest unchanged |
| The table classification finds many records that are neither graph, fact nor derived, and that must change together with the graph | Stop at S3: transactions across tables are what SQLite is for |
| Durability must survive power loss | Every structure then flushes before it acknowledges, the writer batches flushes, and the ranking is made again from C-sync, B and D |
| The store must serve more than one machine | PostgreSQL |
| Trial 2 shows the writer lock cannot be made to exclude a second writer in some reachable case | No single-writer structure stands, B included; the question returns to C with ordered locks or to D |

## 12. What this does not cover

- How leases, lanes and the scheduler behave beyond what the store must hold; that is the graph design's.
- The view's pages, beyond the rule that they read an index or the cursor.
- Per-file changes and the schema of the index; those are for the implementation plan, which a planner writes once this is accepted. No companion seed is opened: nothing surfaced that the drafted items do not carry.
- Retention. Logs grow without bound at a few thousand lines a day, which is small; nothing here deletes history.

## 13. Risks

| Risk | What is done about it |
| :--- | :--- |
| **tillr now owns a storage layer**, and hand-built file stores fail at torn tails, ordering and recovery | The layer is one append per write with one writer. Trials 1, 2 and 5 exercise exactly those three. B stays one step back as a complete fallback |
| **The writer is a single point of liveness** | Reads of an item do not need it; a dead writer is replaced by the next client; a stuck one is [OQ-TS3](#OQ-TS3) |
| **The recommendation rests on a combination that was not measured** | Trial 1 is the first thing built, and S1 to S3 are worth doing whatever it shows |
| **The cross-item argument is wrong somewhere** | Trial 3, and a fallback that changes only the on-disk form |
| **Ad hoc SQL over the record is lost** | The index answers the same questions, and it says how far behind it is |
| **Two structures exist during S4** | An item is in exactly one; the comparison check runs throughout; moves stop on the first difference |
| **The interim SQLite implementation of the interface is thrown away** | It is kept as the reference the logs are tested against |

## 14. Amendments to the graph design

These are proposed texts. The graph design is not edited by this document.

### 14.1 Its Section 10, Storage

The table of needs stands. The paragraphs around it are replaced. Current text, before the table:

```text
What follows is what the graph model needs from storage. Which structure provides it is [OQ-GP2](#OQ-GP2). The owner directed on 2026-11-17 that the choice be the right structure and not the easy one, so it is made in a storage design that compares the structures against this table, with trials.
```

Replacement:

```text
What follows is what the graph model needs from storage. Which structure provides it is decided in [the storage design](./tillr-storage.md), which compares the structures against this table with trials run on 2026-11-17 and recommends one writer process over an append-only log per item. The rest of this design depends only on the interface that document defines, not on the structure behind it.
```

Two rows are added to the table:

```text
| **A store-wide order with a cursor** | The view and replay resume from a position; a reader must never skip a write that committed late |
| **No flush on the write path** | Durability need only survive a process kill (the owner, 2026-11-16), and a flush on this file system took 3 to 5 seconds under load in every trialed variant that waited on one |
```

Current text, after the table:

```text
Research on 2026-11-17, with no trials, found the following. The store is about 36 MB of SQLite. Its failures come from many processes contending for the one write lock and from how it is used, not from its size: a write lock held across a git move in landing; the loop opening the store for each operation from many goroutines; and a migration check that takes the write lock on every open. Changing the engine would not remove the single-writer limit.

Those three usages are defects and are fixed whatever the question's answer is (R6). They are not options.
```

Replacement:

```text
The store is about 36 MB of SQLite. Its failures come from many processes contending for the one write lock, from a flush that sits inside that lock, and from how it is used, not from its size. Of the usage faults, the migration check that took the write lock on every open was fixed on 2026-11-17; three remain: the loop opening the store for each operation from many goroutines, a planning step that computes while holding the write lock, and a write lock held across a git move in landing.

Those three are defects and are fixed whatever structure is ruled (R6). They are the first stage of [the storage design's migration](./tillr-storage.md#9-migration-while-the-drain-keeps-running).
```

### 14.2 Its migration table

Current rows:

```text
| 3 | The policy function and the store's checks, with a table that reproduces today's routing | step 2 |
```

```text
| 16 | The storage structure | [OQ-GP2](#OQ-GP2) |
```

Replacements:

```text
| 3 | The policy function and the store's checks, with a table that reproduces today's routing. The checks are pure functions behind [the storage design's interface](./tillr-storage.md#6-the-interface-the-rest-of-tillr-sees), and from here on graph state is written only through it | step 2 |
```

```text
| 16 | The storage structure behind the interface: one writer, then a log per item, as [the storage design](./tillr-storage.md#92-the-order-against-the-graph-designs-sixteen-steps) stages them | step 3; the storage design's questions |
```

### 14.3 Its open question on storage

Current text:

```text
1. 💬 **OQ-GP2: Should one process own every write to the store?**

   The loop, the lander, each run and each command you type all write one SQLite file. A landing holds the write lock while git moves a ref, and your status command answers "database is locked". The file is about 36 MB, so size is not the cause; this was researched on 2026-11-17 without trials. Three usage faults get fixed either way.

   - **A — One owner.** One process applies every write and the rest send it requests. You stop seeing lock errors. With no owner running, a command does its one write itself.
   - **B — A log per item.** Each item's history is a file you can read and compare, and SQLite becomes an index rebuilt from the logs. It is the larger rewrite.
   - **C — Fix the faults only**, and measure before changing structure.

   [the question directive, with id OQ-GP2 and a leaning of "None yet"]

   _Leaning:_ None yet. The owner directed on 2026-11-17 to do the right thing here, not the easy thing. The earlier leaning toward one owner rested on "the least change", which is not a reason he accepts. A storage design compares one owner against a log per item on the needs of [Section 10](#10-storage), with trials, and this question is asked again from it.

   **Answer:**
   > _(empty — fill in when decided)_
```

The directive line is shown in words above so that this document does not carry a second question with that id. Replacement, in which the directive keeps its id and loses its leaning:

```text
1. 🔒 **OQ-GP2: Should one process own every write to the store?**

   [the question directive, with id OQ-GP2 and no leaning]

   Waits on [the storage design's first question](./tillr-storage.md#OQ-TS1), which asks this from trials run on 2026-11-17 and recommends one writer process over a log per item. A ruling there is recorded here.

   **Answer:**
   > _(empty — fill in when decided)_
```

The graph design's header line and its `next` field name this question as waiting on a storage design; both then name [OQ-TS1](#OQ-TS1) instead.

---

## Decision Ledger

The `D-TS` rows are the designer's decisions of 2026-11-17, made so that the proposal is complete; each is open to the owner's overturn. None is built.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-TS1` | The rest of tillr writes the store through a narrow interface in the graph design's words, with no call that takes a statement, a function or a transaction. **Rejected:** passing SQL through a daemon, which keeps caller work inside a write. | 2026-11-17 | [Section 6](#6-the-interface-the-rest-of-tillr-sees) | — |
| `D-TS2` | The writer is whichever process holds a kernel lock; clients start one on demand; tests run it in-process; it exits when idle. **Rejected:** a command writing for itself when no writer runs, and the loop as the writer. | 2026-11-17 | [Section 5.6](#56-the-writers-life) | — |
| `D-TS3` | The write path never calls a flush, and the durability requirement is a process kill. **Rejected:** a flush per acknowledgement, which cost 2.55 ms at the median and 3.9 s at worst in the trial for a guarantee the owner ruled out needing. | 2026-11-17 | [Section 2.1](#21-the-requirements-that-are-rulings) | — |
| `D-TS4` | No write spans two logs. Cross-item checks are reads of the writer's memory; cross-item effects are an ending followed by a rule application. **Rejected for now:** one log for the whole store, which is the fallback if trial 3 finds a counterexample. | 2026-11-17 | [Section 4.2](#42-does-the-model-need-a-write-across-two-items) | — |
| `D-TS5` | Liveness is a kernel lock the runner holds, not a stored row. **Rejected:** a heartbeat record, which is the most frequent write in today's store and carries nothing anyone reads later. | 2026-11-17 | [Section 5.2](#52-the-record) | — |
| `D-TS6` | Derived data is rebuildable, off the write path, and never waited on by the loop; each index shows its size against a ceiling and how far behind it is. **Rejected:** the loop reading an index to decide. | 2026-11-17 | [Section 7](#7-derived-data) | — |
| `D-TS7` | After a machine crash the writer cuts the store back to the first gap in the store sequence by itself and reports what it set aside. **Rejected:** stopping for a person, which the no-hand-repair rule excludes. | 2026-11-17 | [Section 5.5](#55-kills-and-a-machine-crash) | — |
| `D-TS8` | Storage leaves step 16 and is staged: usage faults, the interface at step 3, one writer over SQLite, then logs item by item. **Rejected:** building the new store beside the drain and switching over. | 2026-11-17 | [Section 9.2](#92-the-order-against-the-graph-designs-sixteen-steps) | — |
| `D-TS9` | PostgreSQL, the other servers and the other embedded engines are rejected, with the reasons of the verdict table. | 2026-11-17 | [Section 4.3](#43-the-verdicts) | — |

---

## Open Questions

1. 💬 **OQ-TS1: Should tillr keep each item's record as a file of its own, written by one process?**

   You type a status command while a landing is in flight, and today it can answer "database is locked", because every program writes one SQLite file. Four structures were trialed on 2026-11-17 under load. All four kept every acknowledged write through 50 kills except PostgreSQL with its flush off, which lost about 15,855. The one recommended was measured only in its two halves.

   - **A — One writer process, a file per item.** You never see a lock error. An item's history is a text file you can read with `tail`; SQL questions go to an index that says how far behind it is. A test store costs 0.13 ms. tillr owns the storage code.
   - **B — One writer process, the SQLite file kept.** You never see a lock error and `sqlite3` still reads everything. A test store costs 15 ms or more, and the checkpoint machinery stays in part.
   - **C — PostgreSQL.** `psql` reads everything. Commits wait on the disk (3.85 ms typical, 3.4 s at worst in the trial), and after a kill it needed restarting until it stayed up.

   <!-- vantage: question id=OQ-TS1 leaning="A, confirmed by a trial of the whole structure first — the change and its event become one line, nothing waits on a flush, and B is a complete stopping point on the way there." -->

   _Leaning:_ A, confirmed by a trial of the whole structure first — the change and its event become one line, nothing waits on a flush, and B is a complete stopping point on the way there.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-TS2: Should the graph's store checks be built on today's SQLite first, or wait for the new store?**

   The graph work's third step adds the checks that stop anything already started from changing. The new store is not yet trialed whole. If the checks are written into today's store as it is, they are rewritten when the store changes, along with everything the twelve later steps add.

   - **A — Build them now, behind the new interface, on today's SQLite.** The graph steps go ahead without waiting. You see lock errors a while longer. One SQLite-backed piece is later replaced, and is kept as the reference the new store is checked against.
   - **B — Wait for the new store.** Nothing is built twice. The graph work's third step and all that follow wait on the trials and the build.

   <!-- vantage: question id=OQ-TS2 leaning="A — the checks are written once as plain functions either way, so only a thin layer is replaced, and nothing in the graph work waits on a store still being trialed." -->

   _Leaning:_ A — the checks are written once as plain functions either way, so only a thin layer is replaced, and nothing in the graph work waits on a store still being trialed.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-TS3: When the store's writer process stops answering, may a command kill it?**

   With one writer, a command sends its write and waits. Suppose the writer has stopped answering. If it is dead, the next command starts another, which the trial did 50 times of 50. If it is alive and stuck, every command waits, and under your rules no wait may time out into a failure. This case was not trialed.

   - **A — Wait, and say so.** You see "waiting on the store writer, pid 4711, 40 s". Nothing is killed. You kill it yourself when you judge it stuck.
   - **B — Kill it on evidence.** The writer reports what it is doing. If it is waiting on the disk, commands wait. If it has sat in a check, which takes microseconds, or cannot report at all, the waiting command kills it, records what it was doing, and a new writer starts. No work fails, and you read the record afterwards.

   <!-- vantage: question id=OQ-TS3 leaning="B — killing the writer is already safe and tested, a restart loses nothing, and the record of what it was doing is what lets the bug be fixed." -->

   _Leaning:_ B — killing the writer is already safe and tested, a restart loses nothing, and the record of what it was doing is what lets the bug be fixed.

   **Answer:**
   > _(empty — fill in when decided)_
