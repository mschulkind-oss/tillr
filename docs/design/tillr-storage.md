---
title: "The item graph's record is a log per item, written by one process"
status: in-review
stage: DESIGN
tags:
  - tillr
  - storage
  - graphs
  - drain
  - design
summary: "A proposal for the store the item graph needs. One process applies every write; the record is an append-only log per item, in which a change and its event are the same line; SQL becomes a rebuildable index nothing waits on. The rest of tillr sees a narrow interface with no way to run its own code inside a write, so the structure behind it can change again. The case for changing structure is not made: the comparison is owed on a healthy file system."
next: "Fix the remaining usage faults, then run today's store and the proposed structures through one workload from a harness in the repository, counting lock failures and stalls; the owner is then asked OQ-TS1, whether any change of structure is still worth making. The designer's decisions D-TS1 to D-TS9, and the one recorded as OQ-TS2, await the owner's review."
depends-on:
  - tillr-graph-is-the-path.md
---

# The item graph's record is a log per item, written by one process

**Status:** Not built, except the landing's ref move outside a store write
([Section 9.1](#91-what-is-fixed-first-whatever-is-ruled)). The case for a new structure is not
made: the recommended structure has been measured only in parts, and no trial harness is in the
repository ([Section 3](#3-what-was-measured)). [Section 10](#10-trials-owed-before-building)
says what is owed. The `D-TS` rows of the ledger are the designer's decisions and await the
owner's review.

> **In short.** The model already says a graph is a history, so the record should be that history: one append-only log per item, where a change and its event are a single line. One process applies every write, which turns the fog-of-war checks and every cross-item check into ordinary code with nothing to lock. SQL survives as an index that can be deleted at any moment.

**Why it matters.** The store's failures fall in three classes: SQLite's one-writer file lock under many processes, how tillr uses the file, and the file system. Usage faults are fixed at their cause, whatever the structure. What a structure can add is to make the next one impossible: an interface through which no caller can do slow work inside a write.

**The shape.** Four parts: a narrow interface with no caller code inside a write; one writer process, which is whoever holds a kernel lock; a log per item plus one for the queue; and derived indexes that nothing on the write path reads.

**Cost.** tillr owns a small storage layer instead of borrowing one. Every write call site is rewritten onto the interface, and the checkpoint machinery (`tools/tillr/store/checkpoint.go` and its tests) is deleted. Ad hoc SQL against the record is lost; it remains against the index.

**Start at [Section 5](#5-the-recommended-structure)**, the structure. [Section 4](#4-the-structures-against-the-needs) is why, and [Section 11](#11-what-would-change-the-recommendation) is what would change it.

**Needs your ruling:** None open. **Blocked:** [OQ-TS1](#OQ-TS1) and [OQ-TS3](#OQ-TS3), until the trials are run. **Decided by the designer, for the owner's review:** [OQ-TS2](#OQ-TS2).

**Reads with:** [the graph design](./tillr-graph-is-the-path.md) (the model this store serves; its Section 10 lists the needs), [the store contention reference](../reference/tillr-store-contention.md) (the residual limit in landing), [the store's own reference](../../tools/tillr-store/README.md) (today's checkpoint scheme, which this deletes).

---

## 1. The verdict

**What no structure fixes.** A write lock held while a process does other work (moves a git ref, scans history, computes a plan) makes every other writer wait. That is a usage fault. One writer process does not remove it: the writer would block in the same place, and every other write would queue behind it. The repair is to do nothing else under the lock, which is the owner's standing rule (minimal work under locks; every lock failure gets a root cause and a fix). None of the usage faults of [Section 9.1](#91-what-is-fixed-first-whatever-is-ruled) is a reason for any structure here.

**A status command is a read.** It needs no write lock, and a writer does not block it.

**What is left to choose once the usage faults are gone is not known.** The case for changing the structure cannot rest on lock errors. It rests on four claims, none of them measured on a healthy file system:

- **An interface that makes the fault impossible.** If no call takes a function, a statement or a transaction, no caller can do other work under the lock. This is the structural form of the owner's rule. It needs no new structure, and it is worth building whatever the trials show.
- **Cross-item checks without locks.** With one writer, "is this lane in use" is a read of that process's memory.
- **One representation.** In a log, a change and its event are the same line.
- **Cheap test stores.**

**The proposal, if the trials support a change:**

1. **The interface comes first and is the expensive part.** Every structure worth choosing needs callers to stop composing transactions. Once they have, the structure behind the interface is a few hundred lines and can change again.
2. **One process writes.** Not as the least change, and not to cure a lock held too long, but because the model's cross-item checks (a lane used once, a premise on another item's node) become reads of that process's memory instead of locks across files.
3. **The record is a log per item**, with one more log for the queue. PostgreSQL and SQLite both provide the needs of the graph design; the logs provide them with less machinery, cheaper tests, no flush on the write path and a fault that stays inside one item.
4. **The path there passes through the runner-up.** One writer over today's SQLite file is the second step of the migration and a complete stopping point. If the trials of [Section 10](#10-trials-owed-before-building) go against the logs, the work stops there with nothing wasted.
5. **Migration cost is stated, not used as a reason.** Under the graph design most of today's tables stop existing, so their SQL is rewritten once whatever is chosen. The choice is only what it is rewritten into.

**If the trials show no failure left** in today's shape once the faults are fixed, the work stops after the interface, and neither a single writer nor the logs is built on the strength of lock errors.

## 2. What the store must do

### 2.1 The requirements that are rulings

| Requirement | Source |
| :--- | :--- |
| **Durability survives a process kill, not a power loss.** Every acknowledged write survives the death of any or all processes. A machine crash may lose the last 30 to 60 seconds of writes, and that is accepted | Owner ruling, 2026-11-16; the store runs with `synchronous=NORMAL` |
| A lock failure gets a root cause and a fix, never a retry or a longer wait | The owner, standing |
| Minimal work under a lock; no time limit fails work; no test depends on load | The owner, standing |
| The loop never stops on one item's condition | R6 of [the graph design](./tillr-graph-is-the-path.md#1-the-direction-and-the-rules) |
| The drain's container may kill every process at once, and recovery needs no hand repair | How the drain is run |
| Tests create thousands of throwaway stores | The test suites |
| The owner inspects state through the command line and the view | The owner, standing |

The first row decides more than it seems to. A store that needs no flush before it acknowledges has no reason to wait on the file system's slowest operation at all, so the design rule is stronger than "do not hold a lock across a flush": **the write path never calls a flush.**

### 2.2 What the graph design needs

[Section 10 of the graph design](./tillr-graph-is-the-path.md#10-storage) lists eight needs. They are taken one by one in [Section 4.1](#41-need-by-need). Two more come from the rest of that document: a store-wide order with a cursor, for the view and for replay; and reads that never block a writer.

### 2.3 What today's store does, and where it fails

One SQLite file holds every table. Every tillr program opens the file, and callers compose their own write transactions through one primitive that takes the write lock, runs the caller's function, and commits. The committed write rate is low (a few landings an hour) while the rate of opens and probes is high.

Its failures, by cause:

| Cause | Examples |
| :--- | :--- |
| SQLite's one-writer lock and its log restart, under many processes | A read-then-write transaction refused at once; a closing program checkpointing the log; the next writer flushing a restarted log inside the lock |
| tillr's usage | Git work inside a landing's write; a migration transaction on every open; a fold of the whole event log inside a planning write; an unindexed poll that keeps a reader slot busy so no checkpoint can finish |
| The file system | A flush that takes seconds under load |

Most of the first row is reachable only when a slow flush sits inside the lock. The checkpoint machinery exists only to move that flush.

## 3. What was measured

Four structures were prototyped behind one Go interface (claim a node, append an event, read an item, list ready nodes, audit) and run through one workload: one loop process, 20 lane processes writing in bursts, and 5 short command processes a second, for 60 seconds. The prototypes are not in the repository.

| Id | Structure |
| :--- | :--- |
| A | Today's shape: every process opens one SQLite file, write-ahead log, no flush per commit |
| B | One daemon owns the same SQLite file; clients send it requests over a unix socket; a client that finds none starts one |
| C | One append-only log file per item; a writer holds a kernel lock on that log for one `write`; no SQLite. C-sync adds a flush before each acknowledgement |
| D | PostgreSQL, rootless, unix socket; one stored function per write. D-async turns off the wait for the flush |

> [!WARNING]
> **No timing from those runs may rank the structures.** The file system under them was later found faulty, each variant ran once, in sequence, on a machine under moving load. Trial 1 of [Section 10](#10-trials-owed-before-building) replaces them, along with the counts below.

What the runs showed that does not rest on timing:

- **Exactly once.** With 20 processes each trying to claim the same 1,000 nodes, no variant recorded a double claim. A claim made against a revision that had since moved was refused in every variant.
- **Kills.** With 8 writers and any server sent SIGKILL in random order, 50 times, A, B, C, C-sync and D lost no acknowledged write. D-async lost about 15,855 of 1,544,382.
- **B:** 17 of 21 processes started a daemon in the first second and 16 lost the lock and exited.
- **C:** no write can span two items; listing ready nodes costs a directory read and a `stat` of every log. Its kill result rests on the page cache, which survives a process kill; its torn records were injected (46, all repaired by the next writer), not natural.
- **D:** PostgreSQL refuses to run as root, and the drain's container runs as uid 0 in a user namespace, so it needed a mapped uid and a preloaded library. After a kill its next start failed with "pre-existing shared memory block is still in use" in 14 of 50 rounds, and a wrapper had to start it again until it stayed up.

**Not run:** a daemon that is stuck and not dead; any Python client; any write across two items; power loss; a shared SQLite index over the logs; more than 20 lanes; and the structure recommended here.

## 4. The structures against the needs

### 4.1 Need by need

Three structures are compared: **B**, one writer over SQLite; **D**, PostgreSQL with its flush on, since without it acknowledged writes were lost; and **L**, the structure of [Section 5](#5-the-recommended-structure), one writer over a log per item. L was not trialed whole: its file layer is C's and its lifecycle is B's.

| Need | B: one writer, SQLite | D: PostgreSQL | L: one writer, a log per item |
| :--- | :--- | :--- | :--- |
| **A change and its event are one atomic write** | Two rows in one transaction | Two rows in one transaction | One line. There is no second thing to be atomic with |
| **An ordered history for each item** | A query by item | A sequence per item, taken under the item's row lock | The file, in order |
| **A claim happens exactly once** | 0 doubles | 0 doubles; every winner waits on a flush | 0 doubles in C. Under one writer the check is in memory and the race cannot arise |
| **Compare against the revision read** | Refused when stale | Refused when stale | Refused when stale |
| **Kill safety** | No acknowledged write lost | None lost, once a wrapper restarted the server until it stayed up | None lost for the file layer; a torn tail is cut by the next writer |
| **No git, file or process work inside a write** | True only if callers cannot run code inside a transaction, which is the interface's doing and not SQLite's | The same | The same; a write is one `write` call of one line |
| **A store for a test costs almost nothing** | A file and a schema per store | A database on a server every test then depends on | A directory |
| **Premises are checked in the same write** | In the transaction | In the stored function | In the writer, before the one line is appended; nothing else can write between |
| **A store-wide order and cursor** | Free with one writer | Not free: a sequence number can commit out of order, so a reader can skip a row unless appends are serialized | A sequence number the writer stamps on every line |
| **No flush on the write path** | Not quite: after a checkpoint the next write restarts the log and flushes its header, and with one writer everyone queues behind it | No: removing the flush lost acknowledged writes | None anywhere |
| **Inspection** | `sqlite3`, read-only | `psql` | The item's log is text; cross-item questions go to the index |
| **Go and Python** | A request protocol in both, or Python through a command | Mature clients in both | A Go library; Python through a command that prints JSON |
| **A fault stays in one item** | One file; a damaged page is everyone's | One server; its failure to start is everyone's | A damaged log is that item's |

**Where "in the same write" is weakest.** In B and D the check and the change share a transaction the engine enforces. In L they share a critical section the writer enforces: one process, one request at a time, check then append. That is as strong as the transaction only while exactly one process holds the writer lock, so the lock is the thing to test hardest ([Section 10](#10-trials-owed-before-building), trial 2).

### 4.2 Does the model need a write across two items?

C has none, and L keeps that limit on purpose. With one writer, the question splits into two that have different answers.

**A check across items needs no cross-item write.** The writer holds every open item's graph in memory, so "is this lane already running a node of another item" or "does another item's edge hold this node as a premise" is a read of its own memory, made in the same critical section as the append.

**An effect across items is two steps in the graph design already.** An ending is data, and the policy function adds what the graph lacks, with the property that applying a result and evaluating again returns nothing. Every cross-item act found in the model takes that form:

| Act | First write | Second write | A kill between them leaves |
| :--- | :--- | :--- | :--- |
| A landing | The land node is claimed in the item's log. The lander moves the git ref with no store write open | The node's ending `landed`, with the trunk commit | A claimed node with no ending: an exception. The node resumes, finds the ref already moved, and writes the ending |
| Close against land | Closing is a transformation of the same item's graph | — | Nothing: one write. It is refused while a land node is claimed |
| A lane is used once | The claim names its lane | — | Nothing: one write. Lane occupancy is derived from running nodes |
| An inter-item edge from the queue planner | A line in the queue's log naming both items and the facts read | — | Nothing: one write. The writer checks the pinned facts when either item is written |
| An ending that files another item (a mechanic, a triage result) | The ending, naming what is to be filed | The new item's first line, added by the rule that answers the ending | An ending with no successor, which the next evaluation answers |

So the answer is that the model does not need it. That answer is held with less confidence than the rest of this document: it was checked against the sections of the graph design that define the model, the invariant and the storage needs, and not against its queue-level sections line by line. That check is trial 3. If it finds an act that must change two items or neither, the on-disk form becomes one log for the whole store, where one line can carry both changes, behind the same writer and the same interface.

### 4.3 The verdicts

| Structure | Verdict |
| :--- | :--- |
| **A — many processes, one SQLite file** | Not shown to be enough, and not shown to fail. Whether it still fails once the usage faults are fixed is the first thing the trials must show |
| **B — one writer over SQLite** | The runner-up, and the second step of the migration. It removes cross-process lock failures by construction. It keeps the engine's log restart on the write path, a second representation of every change (the row and its event), and a costlier test store |
| **C as trialed — many processes, a log per item, no writer** | Rejected. It has no daemon to go wrong, which is a real merit in a container. But every cross-item check then needs several file locks taken in order, the store-wide sequence needs a lock every write holds, and every short command rebuilds the cross-item picture for itself |
| **D — PostgreSQL** | Rejected. It buys concurrent writers for a workload that commits a few times a minute. The price is a flush on every commit (or lost writes), a server that failed to restart after a kill until a wrapper insisted, a preloaded library to get past its refusal of uid 0, a server all tests share, and a data migration at each major version |
| **Other servers** (Redis or Valkey, NATS JetStream, etcd, FoundationDB, a durable-execution engine) | Rejected from a survey, without trials. Each replaces the relational record with keys or streams, so each costs the same rewrite as L, and adds a server, a flush policy to get right and a lifecycle |
| **Other embedded engines** (LMDB, bbolt, DuckDB, the SQLite branches with two logs or concurrent writers) | Rejected from a survey. LMDB and bbolt have one writer too; DuckDB allows one writing process; the SQLite branches are not in any Go driver |
| **L — one writer over a log per item, SQL as a derived index** | Recommended, subject to the trials of [Section 10](#10-trials-owed-before-building) |

> [!WARNING]
> "One writer over SQLite keeps the callers' SQL" is true only if the daemon accepts SQL and lets a caller hold a transaction open across requests. That keeps the fault this design exists to remove: a caller doing slow work inside a write, now with a socket in the middle and a killed client to clean up after. A daemon with a narrow interface costs the callers the same rewrite as the logs do.

### 4.4 What each structure does to the files that write SQL

| Structure | What happens to them |
| :--- | :--- |
| A, or B with SQL passed through | Unchanged. The write primitive that runs caller code under the lock stays |
| D | Every statement is ported off SQLite's dialect (`PRAGMA`, `INSERT OR IGNORE`, `json_extract`, abort triggers), and every test helper that opens a path opens a server instead |
| B with a narrow interface, or L | Every write call site is rewritten as a call on the interface. Reads in the loop become interface reads. Reads in the view and the evidence tools stay SQL, against the index |

The graph design changes the baseline for all three rows. Runs, dispatches, retries, holds, landing attempts and node runs become nodes of item graphs, so the SQL over those tables is rewritten by that design's steps whatever the store is. What this design adds is the direction of the rewrite: onto the interface, once.

## 5. The recommended structure

### 5.1 The words

- **The writer** *(coined here)*: the one process that applies writes to a store. It is whichever process holds the store's writer lock, a kernel `flock` on a file in the store directory, which the kernel releases when the holder dies. It is not the loop, and it is not "the owner", which in tillr's documents means the person.
- **An item log** *(coined here)*: the append-only file holding one item's history, one text line per record. It is the record of that item; nothing else is.
- **The queue log** *(coined here)*: one more log of the same form for what belongs to no single item: the list of items, the rank, the inter-item edges, and the facts the policy function is permitted to read.
- **The store sequence** *(coined here)*: a number the writer stamps on every line, increasing by one across all logs. It is the cursor readers resume from. It is not an item's revision, which counts accepted changes to that item's graph only.
- **A derived index** *(coined here)*: any file built from the logs to answer a question faster. It can be deleted at any time and rebuilt. It is never the record.

### 5.2 The record

- **One line is one record:** a JSON object ending in a newline, carrying its kind, its store sequence, the item's revision after it, the request id that caused it, and a check value over the rest. Text, so that `rg`, `tail` and `jq` read it.
- **What a record holds:** a transformation (nodes and edges added, replaced or removed, with the rule and the facts read); a claim; an ending; a refusal with its reason; or bookkeeping for a running node (a process segment, an exception, a progress note, a hoisted child). Bookkeeping and refusals do not advance the item's revision.
- **Large content is not in the log.** Briefs, transcripts and outputs stay files beside the store, and a record names them by content hash. A record over 1 MiB is refused.
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
- **Store-wide reads** (events since a cursor, the head sequence, wait until the head passes a number) come from the writer. The head is the cheap change signal the view polls for.
- **Python reads through a command that prints JSON.** No Python program writes the store.

### 5.5 Kills, and a machine crash

- **A process kill** leaves every acknowledged line in the page cache and at most one torn last line in one log. The writer cuts a torn tail when it next opens that log. Nothing else is needed, and nothing is left locked, because every lock is the kernel's.
- **A machine crash** is outside the durability requirement, but recovery must still need no hand repair. Each log may have lost a different amount, so the surviving lines can have a gap in the store sequence: a later line kept in one log, an earlier one lost from another. At its first start after a boot (it records the kernel's boot id), the writer finds the first missing sequence number, moves every line above it into a side file, and reports what it moved. The store is then a consistent earlier state. This is the last seconds the ruling already accepts losing.
- **A damaged line that is not at a tail** is a defect of that log only. The item is shown as damaged with the line's position, the writer refuses writes to it, and every other item drains.

### 5.6 The writer's life

- **Starting.** A client that cannot connect to the socket in the store directory starts the writer detached and connects when it is listening. Any number may start one at once; all but the holder of the lock exit at once.
- **At start** the writer takes the lock, removes a socket left by a dead predecessor (safe, because it holds the lock), reads the logs, cuts torn tails, and listens. It reads every log; [trial 1](#10-trials-owed-before-building) measures that at 10 and 100 times today's volume, and a derived list of finished items may let it skip them.
- **Stopping.** It exits after 60 seconds with no client connected, a setting. It needs no clean shutdown and has none.
- **In a test** the writer runs inside the test process on a temporary directory, with no socket. It is the same code holding the same lock.
- **Waiting is never a failure.** A client waits for the writer with no time limit and says what it is waiting on. What may be done about a writer that has stopped answering is [OQ-TS3](#OQ-TS3).
- **Host and container** share the store directory and one kernel, so the lock and the socket work across them. Process ids do not, so a client on one side cannot signal a writer on the other; it can only wait and say so. Not trialed.

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

**What replaces direct SQL.** A caller that opens a write transaction and runs statements inside it makes one of the calls above instead. There is no call that takes a function, a statement or a transaction. A caller that reads tables to decide something asks for the graph and decides in its own memory. A caller that reads to display or to analyze (the view, the evidence tools, `tillr-trace`) reads a derived index with SQL, or the cursor.

**What this leaves out on purpose.** The interface has no write across two items ([Section 4.2](#42-does-the-model-need-a-write-across-two-items)), no way to change a fixed field, and no way to record liveness.

## 7. Derived data

Three rules, for the view's index and any later one:

1. **Rebuildable.** A derived index is built from the logs through the cursor and holds the store sequence it has reached. Deleting it loses nothing. Its layout carries a version, and a mismatch means delete and rebuild, never repair in place.
2. **Never on the write path.** The writer does not read, write or wait for any index. An index has one writing process of its own, under its own kernel lock.
3. **Never able to block the loop.** The loop starts an indexer detached and does not wait for it, and decides nothing from an index. A reader that needs the index shows how far behind it is: "index at 4,100 of 4,144".

**Size is shown.** Each index states an expected ceiling, and the view shows its size against it. Passing the ceiling is a reported defect. An index that grows unreported becomes a stall the moment the loop waits for it.

**The view's index** is SQLite, written by one indexer and opened read-only by everything else. During the migration it is today's file with today's tables, which is why the view's queries move last.

**There is no trace index.** Owner ruling, 2026-11-18: cached derived data is removed until a measured performance problem asks for a cache. The loop runs no indexer, and `tillr-trace` and the view read a run's artifacts on demand ([tillr-trace README](../../tools/tillr-trace/README.md#no-index-every-answer-is-read-from-the-artifacts)). A cache that returns is held to the three rules above, and reads the store through the cursor instead of opening the store's file.

## 8. Alternatives inside the recommendation

| Alternative | Verdict |
| :--- | :--- |
| One log for the whole store, the same writer | Not chosen; the fallback. It allows a write across items and makes a machine crash trivially consistent, but a damaged line is everyone's and an item's history is scattered. It becomes the choice if trial 3 finds a cross-item write the model needs |
| The logs as the record and SQLite as an index the loop also reads | Rejected. It puts a derived file on the path that decides |
| A command writes for itself when no writer runs | Rejected. It is a second write path that runs only when the system is quiet, so it is never exercised under the conditions that break things |
| The loop is the writer | Rejected. Commands must work with the loop stopped, and a loop fault must not take the store with it |
| Flush in the background on a timer | Not needed. The kernel writes dirty pages back by itself, and the ruling accepts the window |

## 9. Migration, while the drain keeps running

### 9.1 What is fixed first, whatever is ruled

Each of these is a usage fault under the owner's standing rule that a lock failure gets a root cause and a fix, and none waits on anything in this document.

| Fault | Repair | Built |
| :--- | :--- | :--- |
| A landing holds the store's write lock while git moves the trunk ref | A short claim write, the git move with nothing held, and a short receipt write; a marker ref makes a repeat harmless | Yes: `publishLanding`, `claimLanding` and `recordPublished` in `tools/tillr-land/land/landing.go`, pinned by `TestFinalMoveHoldsNoStoreWriteAndTheClaimRefusesClose` and `TestGitPreparationAllowsHeartbeatAndClose` |
| A process opens the store once per operation, so it queues against itself | One store handle per process, shared | Not confirmed |
| A planning step computes inside its write | Read, compute with nothing held, then take the lock only to apply the result against the revision read | Not confirmed. An event cache (`tools/tillr/store/eventcache.go`) removes the fold of the whole event log from that write |
| One event kind written far more often than anything reads it | Fixed where the event is written; no structure should absorb it | Not confirmed |

These are the same change the interface later makes structural, so none is thrown away.

### 9.2 The order, against the graph design's sixteen steps

[The graph design's migration order](./tillr-graph-is-the-path.md#13-migration-order) puts "the policy function and the store's checks" at step 3 and the storage structure at step 16. If storage were one step at the end, the checks would be written into SQL transactions at step 3, steps 4 to 15 would each add SQL over graph tables, and step 16 would rewrite all of it. So storage is four stages, each one shippable and each a place to stop:

| Stage | What moves | Where it sits |
| :--- | :--- | :--- |
| **S1** | The faults of [Section 9.1](#91-what-is-fixed-first-whatever-is-ruled) | Now; beside step 1 |
| **S2** | The interface of [Section 6](#6-the-interface-the-rest-of-tillr-sees), with the fog checks as pure functions, implemented over today's SQLite file | It is step 3's "store's checks". Steps 4 to 15 write graph state only through it |
| **S3** | One writer: every graph write goes through one process. The record is still the SQLite file. This is structure B, complete | After step 3; independent of steps 4 to 15 |
| **S4** | The record becomes the logs, item by item; the SQLite file becomes the view's index | After S3, after the trials, and after [OQ-TS1](#OQ-TS1) |

**Step 3 is built over today's SQLite file, behind the interface.** This is the designer's decision, recorded in the ledger as [OQ-TS2](#OQ-TS2), and the owner may overturn it. Two reasons carry it. No step of the graph design then waits on a store that is still being trialed. And the SQLite implementation of the interface becomes the reference the logs are checked against. The checks are pure functions written once, so only a thin layer is replaced later. The alternative, holding step 3 until the new store exists, is rejected: it stops the whole graph migration behind trials that have not been run, to save replacing that one layer.

### 9.3 How existing state is carried over

- **Into S2:** as the graph design states, runs recorded before step 3 are imported as done nodes.
- **Into S4, one item at a time.** An item's graph lives in exactly one structure, and a line in the queue's record says which. An item with no claimed node is moved by writing its history to a new log in order and then that marker; a new item starts in a log. An item with a running node moves when the node ends. The drain never stops, and a kill during a move leaves a log with no marker, which is deleted and written again.
- **The comparison.** While both exist, a check folds each moved item's log and compares it with what the SQLite implementation holds for it. A difference is a defect that stops moves, not items.
- **Tables outside the graph.** Each of today's tables falls in one of three classes, by this rule: what the graph design turns into nodes, endings or bookkeeping is **superseded**; what the policy function or the scheduler reads to decide is a **fact** in the queue log; everything else is **derived** when it can be rebuilt from the logs and, when it cannot, a record of another subsystem that keeps its own small log under the same writer. The tables are not classified one by one, and the count in each class is unknown. That classification is owed before this design is accepted, because a large last class is one of the things that would change the recommendation.

### 9.4 Drafted items

These are proposals in prose. None is a queue item, and nothing can be dispatched from them.

#### Share one store handle in each process

The drain opens the store once per operation and from many goroutines, so one process queues against itself for the write lock and each open repeats checks the last one made. This item gives each process one handle for its life.

- **Paths:** `tools/tillr-drain/`, `tools/tillr/store/`, `docs/`
- **Waits on:** nothing.
- **c1:** A process opens the store once and every operation in it uses that handle. A test fails when a second writable open happens in one process.
- **c2:** A test runs a fixture pass and asserts the number of store opens is one.
- **c3:** The existing suites pass with no change to what is stored.

#### Compute the plan outside the write

A planning step holds the write lock while it computes, and every other writer waits behind it. This item makes it read, compute with nothing held, and take the lock only to apply the result against the revision it read.

- **Paths:** `tools/tillr-drain/`, `docs/`
- **Waits on:** nothing.
- **c1:** The step's write contains only the statements that apply its result. A test asserts no computation runs between the write's start and its commit.
- **c2:** A result computed from state that has since changed is refused and computed again. A test changes the state between the read and the write.

#### Move the trunk in a landing with no write open

Built ([Section 9.1](#91-what-is-fixed-first-whatever-is-ruled)). A landing takes a short write that marks the landing begun, moves the trunk ref with nothing held, and takes a short write that records the result.

- **c1:** No git command runs while a store write is open in a landing. A test asserts it with a git that blocks.
- **c2:** An item cannot be closed between the two writes. A test attempts it and reads the refusal.
- **c3:** A landing killed after the ref moved and before the second write is finished by the next attempt, which moves nothing and records the landing once. A test kills at that point.
- **c4:** A heartbeat write completes while a landing's git command is blocked. A test asserts it.

#### Run the storage trials on the recommended structure

The recommended structure has been measured only in parts, and no timing exists that can be relied on. This item builds a prototype of the whole structure behind the trial interface and runs every trial the design names, all structures together, on a file system checked healthy first, so that the ruling rests on measurements of the thing ruled on.

- **Paths:** `docs/`, `tools/tillr-store/`
- **Waits on:** nothing.
- **c1:** The prototype and its harness are in the repository, and one command reruns every trial.
- **c2:** Each of the six trials has a recorded result in a tracked document: what was run, the numbers and the date.
- **c3:** The variants run interleaved, more than once, so that a slow period of the machine does not fall on one of them.
- **c4:** No trial result is a pass or fail on elapsed time.
- **c5:** Each recorded result states how the file system's health was checked before the run, and a run on a file system that reports errors is not recorded as a result.

#### Put the graph's writes behind one interface

Callers write the store by composing their own transactions, so any of them can do slow work while holding the lock. This item adds the interface of [Section 6](#6-the-interface-the-rest-of-tillr-sees), moves every graph write onto it, and removes the way to run caller code inside a write. It is the "store's checks" half of the graph design's third step.

- **Paths:** `tools/tillr/store/`, `tools/tillr-drain/`, `tools/tillr-item/`, `tools/tillr-run/`, `tools/tillr-land/`, `docs/`
- **Waits on:** a stated ending on every node run (the graph design's).
- **c1:** Each call in the interface table exists and each refusal it lists is returned with its reason. A test asserts every refusal and an unchanged graph.
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
| 1 | **Today's shape with its faults fixed, and the recommended structure, whole.** First, structure A after the usage faults are fixed: does any lock failure or stall remain? Then one writer over a log per item with text lines, behind the same interface, through the same four trials: the workload, the race, 50 kill rounds, and the cost of a test store. All variants interleaved and repeated, on a file system checked healthy first. Plus the writer's start time at 1, 10 and 100 times today's volume | Whether any change of structure is still warranted, and whether the combination behaves like its parts. Today's support is C for the files and B for the lifecycle |
| 2 | **A writer that is stuck and not dead.** Stop the writer with SIGSTOP; block it inside a `write`; make it spin in a check. For each: what clients see, whether a second writer can ever run beside it, and what a restart costs | The writer lock, and the facts [OQ-TS3](#OQ-TS3) is ruled on |
| 3 | **Cross-item acts.** Walk every act in the graph design that touches two items or an item and the queue, and write each as single-log writes. Kill between every pair of writes and evaluate again | Whether the model needs a write across items ([Section 4.2](#42-does-the-model-need-a-write-across-two-items)), and so whether the logs are per item or one |
| 4 | **A Python reader.** `tillr-trace` and the evidence tool read through the JSON command and the cursor, during the workload | That no second implementation of the format is needed |
| 5 | **A simulated machine crash.** Cut random amounts off the tails of random logs, start the writer, and check that what remains is a state the store once held | The cut rule of [Section 5.5](#55-kills-and-a-machine-crash). A real power loss is outside the durability requirement |
| 6 | **Host and container together.** A writer in the container and a client on the host, and the reverse | The lock and the socket across the bind mount |

The table classification of [Section 9.3](#93-how-existing-state-is-carried-over) is owed alongside them. It is a reading of the tree, not a trial.

## 11. What would change the recommendation

| Finding | Then |
| :--- | :--- |
| Trial 1 shows no lock failure and no stall in today's shape once the usage faults are fixed | Stop at S2: the interface, over today's file, with many processes |
| Trial 1 loses an acknowledged write, or the whole structure is materially slower to start or to write than B | Stop at S3: one writer over SQLite |
| Trial 3 finds an act that must change two items or neither | One log for the store in place of one per item; the rest unchanged |
| The table classification finds many records that are neither graph, fact nor derived, and that must change together with the graph | Stop at S3: transactions across tables are what SQLite is for |
| Durability must survive power loss | Every structure then flushes before it acknowledges, the writer batches flushes, and the ranking is made again from C-sync, B and D |
| The store must serve more than one machine | PostgreSQL |
| Trial 2 shows the writer lock cannot be made to exclude a second writer in some reachable case | No single-writer structure stands, B included; the question returns to C with ordered locks or to D |

## 12. What this does not cover

- How leases, lanes and the scheduler behave beyond what the store must hold; that is the graph design's.
- The view's pages, beyond the rule that they read an index or the cursor.
- Per-file changes and the schema of the index; those are for the implementation plan, which a planner writes once this is accepted.
- Retention. Nothing here deletes history.

## 13. Risks

| Risk | What is done about it |
| :--- | :--- |
| **tillr owns a storage layer**, and hand-built file stores fail at torn tails, ordering and recovery | The layer is one append per write with one writer. Trials 1, 2 and 5 exercise exactly those three. B stays one step back as a complete fallback |
| **The writer is a single point of liveness** | Reads of an item do not need it; a dead writer is replaced by the next client; a stuck one is [OQ-TS3](#OQ-TS3) |
| **The recommendation rests on a combination that was not measured** | Trial 1 is the first thing built, and S1 to S3 are worth doing whatever it shows |
| **The case for a new structure collapses** once the usage faults are fixed | Trial 1 tests exactly that first, and S2 is a complete stopping point |
| **The cross-item argument is wrong somewhere** | Trial 3, and a fallback that changes only the on-disk form |
| **Ad hoc SQL over the record is lost** | The index answers the same questions, and it says how far behind it is |
| **Two structures exist during S4** | An item is in exactly one; the comparison check runs throughout; moves stop on the first difference |
| **The interim SQLite implementation of the interface is thrown away** | It is kept as the reference the logs are tested against |

---

## Decision Ledger

The `D-TS` rows are the designer's decisions, made so that the proposal is complete. None of them is the owner's ruling; the owner may overturn any of them, and all await review. None is built. [OQ-TS2](#OQ-TS2) was asked as a question and is recorded here as a designer's decision, because evidence settles it.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-TS1` | The rest of tillr writes the store through a narrow interface in the graph design's words, with no call that takes a statement, a function or a transaction. **Rejected:** passing SQL through a daemon, which keeps caller work inside a write. | 2026-11-17 | [Section 6](#6-the-interface-the-rest-of-tillr-sees) | — |
| `D-TS2` | The writer is whichever process holds a kernel lock; clients start one on demand; tests run it in-process; it exits when idle. **Rejected:** a command writing for itself when no writer runs, and the loop as the writer. | 2026-11-17 | [Section 5.6](#56-the-writers-life) | — |
| `D-TS3` | The write path never calls a flush, and the durability requirement is a process kill. **Rejected:** a flush per acknowledgement, which makes every write wait on the file system for a guarantee the owner ruled out needing. | 2026-11-17 | [Section 2.1](#21-the-requirements-that-are-rulings) | — |
| `D-TS4` | No write spans two logs. Cross-item checks are reads of the writer's memory; cross-item effects are an ending followed by a rule application. **Rejected for now:** one log for the whole store, which is the fallback if trial 3 finds a counterexample. | 2026-11-17 | [Section 4.2](#42-does-the-model-need-a-write-across-two-items) | — |
| `D-TS5` | Liveness is a kernel lock the runner holds, not a stored row. **Rejected:** a heartbeat record, which is the most frequent write in today's store and carries nothing anyone reads later. | 2026-11-17 | [Section 5.2](#52-the-record) | — |
| `D-TS6` | Derived data is rebuildable, off the write path, and never waited on by the loop; each index shows its size against a ceiling and how far behind it is. **Rejected:** the loop reading an index to decide. | 2026-11-17 | [Section 7](#7-derived-data) | — |
| `D-TS7` | After a machine crash the writer cuts the store back to the first gap in the store sequence by itself and reports what it set aside. **Rejected:** stopping for a person, which the no-hand-repair rule excludes. | 2026-11-17 | [Section 5.5](#55-kills-and-a-machine-crash) | — |
| `D-TS8` | Storage is staged across the graph design's steps: usage faults, the interface at step 3, one writer over SQLite, then logs item by item. **Rejected:** building the new store beside the drain and switching over. | 2026-11-17 | [Section 9.2](#92-the-order-against-the-graph-designs-sixteen-steps) | — |
| `D-TS9` | PostgreSQL, the other servers and the other embedded engines are rejected, with the reasons of the verdict table. | 2026-11-17 | [Section 4.3](#43-the-verdicts) | — |
| `OQ-TS2` | **Designer's decision, not the owner's ruling. The owner may overturn this.** The graph design's third step builds its store checks on today's SQLite file, behind the new interface. **Evidence:** the interface is what stops a caller from doing other work under the lock, and it is needed whatever the structure; the checks are pure functions written once, so only a thin layer is replaced if the structure changes; and no graph step then waits on trials. **Rejected:** waiting for the new store, whose only effect is to delay every later step of the graph design. | 2026-11-18 | [Section 9.2](#92-the-order-against-the-graph-designs-sixteen-steps) | — |

---

## Open Questions

1. 🔒 **OQ-TS1: Is any change to how tillr stores its record still worth making once the lock faults are fixed, and if so which?**

   <!-- vantage: question id=OQ-TS1 blocked-on="the usage faults fixed, then the storage trials run on a healthy file system from the repository" -->

   Waits on two things. First the remaining usage faults are fixed; no structure fixes those. Then today's store and the proposed structures are run through one workload on a healthy file system, from a harness kept in the repository, counting lock failures and stalls. If today's store shows no failure left, you are asked nothing and the work stops at the interface. If it does, you are asked one thing: whether tillr keeps one writer process over today's SQLite file, or owns a small storage layer of its own, a text log per item. Nothing built before then depends on the answer.

   **Answer:**
   > _(empty — fill in when decided)_

2. 🔒 **OQ-TS3: When the store's writer process stops answering, may a command kill it?**

   <!-- vantage: question id=OQ-TS3 blocked-on="a decision to build a single writer process at all, that writer existing, and the trial of a writer that is stuck and not dead" -->

   Waits on a single writer process being chosen and built, and on the trial of a writer that is stuck and not dead. If the trials show that no single writer is needed, this question is withdrawn. Until then nobody knows what a stuck writer looks like from outside, and your standing rule holds: a command waits with no time limit and says what it is waiting on, and a stuck writer is a defect to diagnose.

   **Answer:**
   > _(empty — fill in when decided)_
