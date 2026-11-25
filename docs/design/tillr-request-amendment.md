---
title: "A request that is hard to deliver as written is amended once, by the design seat, and both texts are kept"
status: in-review
stage: DESIGN
tags: [tillr, drain, request, design-seat, verify, design]
summary: "How a request is worded should not decide how hard the work is. The design seat may write an amendment: an append-only record beside a filed request's saved words, which are never changed. A worker that meets a flaw does the rest of its work and lists what it left, and is stopped only when nothing can be built. One question is open: whether every new request is read for known flaws before its first worker starts. Nothing is built."
next: "Rule OQ-RA1 (is every new request read for known flaws before its first worker starts). Slices 1 to 8 of Section 11 do not wait on it."
depends-on:
  - tillr-item-split.md
  - criterion-repair.md
  - tillr-judge-and-lifecycle.md
vantage:
  status-chip: true
---

# A request that is hard to deliver as written is amended once

**Status:** Nothing is built. `rg -n 'request_words' tools/tillr-item --glob '!*_test.go'` finds one insert, in the filing, and no update, and `rg -n 'request_words|RequestWords' tools/tillr-view` finds nothing. `R-RA1` is the only owner ruling; every other decision is the designer's and is marked so in the [Decision Ledger](#decision-ledger).

> **In short.** A filed request keeps its saved words byte for byte, and the design seat may write an **amendment** beside them that says how the request is read, which named cases follow as their own requests, and which of the request's own criteria are replaced. A worker that meets a flaw in the request finishes what it can and lists what it left, so one design turn settles every flaw the request has.

**Why it matters.** A request whose words are wrong for the seat or the tree stops its worker, and the answer lands as a design record that never reaches the request ([§2](#2-the-measured-basis)).

**The shape.** One record, one writer and five callers. The design seat writes the amendment, the drain checks and records it, and a filing check, a planner, a worker, a verifier or the operator may call for one.

**Cost.** A design turn still stands between a flawed request and its landing. An amendment lets the seat that wants work finished restate what finished means, and the guard is that both texts are in front of the verifier and on the item page.

**Start at** [§4](#4-the-rules-an-amendment-keeps), the rules, then [§7](#7-when-an-amendment-is-called-for-and-what-the-worker-does-meanwhile).

**Needs your ruling:** [OQ-RA1](#OQ-RA1).

**Reads with:** [an oversized item lands in parts](./tillr-item-split.md) (the split, which is one of the answers a call may get), [criterion repair](./criterion-repair.md) (the rule for a criterion a verdict has frozen), [the judge and lifecycle design](./tillr-judge-and-lifecycle.md#419-a-worker-returned-by-a-design-answer-starts-from-a-trunk-that-holds-the-answer-d-jl32) (how a definition obstacle on a filed request is answered), and [the landing principles](./landing-principles.md) (the rulings this must not break).

**Words.** [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, criterion, candidate, lane and verdict. A **filed request** is an item filed from the command line whose statement is its saved words and not a block in a document. These are coined in this document:

- **Flaw**: a property of a request, from the fixed list in [§5](#5-the-flaws), that makes it hard to deliver as written. It is not a defect in the work.
- **Amendment**: an append-only record on a filed request, written by the design seat, that states how the request is read. It is not an edit of the saved words, and it is not a [criterion supersession](./criterion-repair.md#5-the-shape-any-repair-has-to-have), which replaces a criterion a verdict has frozen.
- **Left-undone list**: a worker's list, delivered with its commit, of the sentences and criteria of the request it did not carry out, each with a reason. It is not an obstacle: the worker's commit exists.
- **Rewrite call**: a recorded statement that a request needs an amendment, naming who made it, the flaw and the evidence. It decides nothing.
- **Deferred case**: a named case of the request that an amendment moves out of the item into a request of its own.
- **Follow-up**: the ordinary request the drain files for a deferred case.

---

## 1. The ruling

The owner ruled on 2026-11-21 (`R-RA1`):

1. How a request is worded should not decide how hard the work is.
2. The split of responsibilities between seats stays enforced.
3. A back and forth for each flaw is waste, and is not the way that split is kept.
4. Some part of the design gets permission to rewrite a request whose criteria or words make it hard to deliver.

The ruling leaves open which seat that is. This design chooses the design seat (`D-RA1`), and the owner may overturn that.

Four standing rulings bound the design:

- **No count of attempts or elapsed time fails work** ([the judge design](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)).
- **Part of a change may land while a named case is unmet** ([the split design](./tillr-item-split.md#1-the-ruling)).
- **Only an item's designer changes a design document**, and a failed check on a design document goes to the design seat ([the design document check](./tillr-design-document-check.md#decision-ledger)).
- **The design seat replaces a criterion's text after a verdict has frozen it**, because it is neither the worker nor the judge, and visibility is preferred to a narrower permission ([criterion repair](./criterion-repair.md#decision-ledger)).

## 2. The measured basis

Measured over one day of the queue, from the store's events filtered by kind and item and from `git log --first-parent` on the trunk:

- 169 requests were filed. Workers ended with a `definition` obstacle 80 times, on 36 items; 13 items had more than one and five had five or more.
- Verifiers refused 22 items 128 times; six items were refused nine or more times.
- In one two-hour window 14 of 30 trunk commits were design records, 11 of them answers to a worker's report or a verifier's refusals.
- Over five measured rounds, 17% of the time from obstacle to the next worker start was the landing of the answer's commit, 37% the wait for the design run and 28% the design run itself.

Reading each obstacle's first evidence line gives this division, which is an interpretation and not a stored field:

| What the evidence says | Obstacles |
| :--- | ---: |
| The words state a fact, a prerequisite or a tree state that is not so | 28 |
| The words ask the seat, the node or its write scope for what it may not do | 20 |
| The words ask for what a recorded ruling or decision forbids | 14 |
| The design the request points at does not say, or contradicts itself | 13 |
| The tree already does what is asked | 5 |

What follows from the measurements:

1. **The answer never reaches the request.** A design record sits in a document. The worker and the verifier are still handed the original words and criteria and must find the record and reconcile the two.
2. **One flaw is answered per round.** The design seat is asked about the one sentence the worker reported, so a request with three flaws can cost three rounds.
3. **The worker stops for flaws that block nothing.**
4. **Many flaws can be read in the words on the day a request is filed**: four of six requests examined closely carried one. The repository's filing guidance names each kind (the drain check-in skill (an operating skill kept outside this repository)).
5. **Some stops are not flaws in the request.** A node of the plan cannot write a file its change requires; that is a flaw in the plan.
6. **Narrowing is done by closing the item** and filing new requests by hand, which drops the lane, the worker's session and the item's history.
7. **Narrower words end refusals about what the request stops naming.** They do not end refusals inside the named scope.

## 3. What exists today

| Mechanism | What it does | What it cannot do |
| :--- | :--- | :--- |
| Saved words (`FileRequest` in [`request.go`](../../tools/tillr-item/item/request.go)) | Stores the words once, apart from the item, and appends them to every worker's and verifier's brief | Be amended: no command changes or adds to them, and filing the same words with other options is refused |
| Request criteria | The four standard criteria, then up to four of the filer's, eight in all (`MaxRequestCriteria`) | Follow the words: no criterion is derived from the prose |
| `tillr item apply` | Rewrites a criterion that has no verdict; refuses one that has (`criterion-frozen`) | Say who changed it or why, or keep the text it replaced. No seat is routed to it for a filed request |
| Definition obstacle on a filed request | Sends the item to the design seat, which answers in a design document (`requestObstacleDesignInstruction` in [`obstacle.go`](../../tools/tillr-drain/drain/obstacle.go)); the answer lands and the worker returns | Change what the worker and verifier are held to |
| `plan-wrong` | A verifier's reason, or a builder's report during a verification repair, sends work to the plan seat (`builderPlanWrong` in [`verify_return.go`](../../tools/tillr-drain/drain/verify_return.go)) | Reach the plan seat from an ordinary build worker's obstacle |
| The design-document rule (`DesignDocumentRule` in [`stage.go`](../../tools/tillr-drain/drain/stage.go)) | Tells build, edit, docs and test-write seats not to edit a design document and to end with an obstacle | Let the worker finish the rest first |
| The repeated-obstacle hold (`repeatedAnsweredObstacle`) | Holds an item when a worker repeats an answered obstacle with the same evidence text | See the same obstacle reworded |
| The split ([an oversized item lands in parts](./tillr-item-split.md)) | Replaces one item by part items under a parent | Narrow an item that is one behavior with a few cases too many |
| The item page | Shows current criteria and their state | Show a filed request's saved words: it shows saved words only for an idea |

## 4. The rules an amendment keeps

- **P1. The saved words never change.** They stay byte for byte as filed, and every reader is shown them first. An amendment is read beside them and never in their place.
- **P2. One seat writes the amendment.** The design seat writes it and the drain records it. The worker held to the request, the verifier that applies it and the planner that plans within it do not.
- **P3. Nothing is dropped and nothing is added.** After an amendment, every sentence of the saved words and every criterion is in one of four places: delivered by this item, delivered by a named seat as its own share, deferred to a named follow-up, or stated with evidence as something the tree cannot establish or contradicts. Only the owner releases a case outright. An amendment adds no work the words do not ask for.
- **P4. A ruling is not amendable.** An amendment never contradicts the owner's quoted words or a recorded ruling. Where the two disagree, [§6.3](#63-what-an-amendment-may-not-do) says which gives way.
- **P5. Both texts are shown.** The verifier and the item page show the saved words, the current amendment, every earlier one, and for each replaced criterion the old text, the new text and the reason, with the word *replaced*. The prior text is the only thing that makes a weakening visible.
- **P6. One turn settles the request.** The design seat reads the whole request against the list of flaws when any call reaches it, and not only the sentence that was reported.
- **P7. A worker is stopped only when nothing can be built.** A flaw that leaves work to do is reported with the work, in a left-undone list.
- **P8. Nothing here counts.** No call is made by a number of attempts, refusals, obstacles or minutes, and no amendment holds an item on the owner.

## 5. The flaws

A flaw is a fact about the request that a reader can check against the words, the tree or the documents. The list is tracked configuration, so adding a flaw is a reviewed commit.

| # | Flaw | How it is recognized | Example | What the amendment does |
| :--- | :--- | :--- | :--- | :--- |
| F1 | **Another seat's work.** The words tell a worker to do what its seat may not: edit a design document, mark a design slice built, change a status line | The sentence, read against the seat's rules | "Update the slice table to mark this slice built." | Names the seat whose share the sentence is. When that is the design seat, it does the share in the same turn |
| F2 | **A fact the worker cannot establish.** A criterion requires a stated cause or measurement the tree may not hold | The criterion asks the worker to state why something happened | "The commit body states what interrupted the first run" | Replaces the criterion: the worker states what the code establishes, and says so where the cause is not established |
| F3 | **A statement the tree contradicts.** The words name a file, a symbol, a count or a behavior that is not so | The worker's or the reader's evidence from the tree | "Four files have the fault", where one has | States the fact as it is, and reads the sentence against it |
| F4 | **Unbounded reach.** The words bind the work to every case of something without naming the cases, or to the whole of a document | "for every", "any", "all of", "exactly as the design says" | "For every gate run"; "Follow the design's decisions exactly" | Names the cases this item delivers and defers the rest ([§6.2](#62-deferring-a-case)) |
| F5 | **Several behaviors.** The words name behaviors that could each land if the others never shipped | The split design's [signals](./tillr-item-split.md#6-the-signals) for filing | A request for an evaluator, three projections and six commands | Not an amendment: the design seat answers with a split statement |
| F6 | **A criterion that joins clauses.** One criterion holds several testable clauses | The criterion as written | — | Replaces it by one criterion per clause, within the limit of eight |
| F7 | **Against a recorded ruling or decision.** The words ask for what a ruling or a design decision forbids | The reader's citation of the ruling | — | [§6.3](#63-what-an-amendment-may-not-do); when a worker's `definition` obstacle cites a settled decision that the request did not name, [settled-decision conflicts](./tillr-settled-decision-conflicts.md) proposes routing straight to a person on the first conflict ([`OQ-SDC1`](./tillr-settled-decision-conflicts.md#OQ-SDC1)) |

Three things are not flaws in a request, and are not answered by an amendment:

- **The plan is wrong.** A node's task or write list does not fit the change. The plan seat amends the plan ([§7.4](#74-a-flaw-in-the-plan-goes-to-the-planner)).
- **The design does not say.** The request points at a design that is silent or contradicts itself. That is design work, answered in the design document, and the amendment then names the section that answers it ([§6.4](#64-when-a-design-document-also-changes)).
- **The tree already does it.** The item is resolved elsewhere, by the existing route.

## 6. The amendment

### 6.1 What it holds

1. **The reason**: each flaw settled, by its number, with the evidence. An amendment with no reason is refused.
2. **The reading**: for each sentence it qualifies, the sentence quoted from the saved words, and how it is read.
3. **The scope**: what this item delivers, in sentences a verifier can test, and each deferred case with the words of its follow-up or the existing item that owns it.
4. **The criteria**: for each of the request's own criteria, whether it is kept, replaced by new text, or moved to a named follow-up.
5. **The sections of design documents that answer the request**, when there are any.

An amendment is the whole current reading. A later amendment restates everything that still holds and replaces the earlier one; the earlier ones stay in the record. So every reader derives one current statement: the saved words, then the latest amendment.

### 6.2 Deferring a case

Deferring is how an amendment narrows a request without lowering it. The amendment names the case in one sentence, and the drain files a follow-up for it in the same transaction as the amendment.

- **A follow-up is an ordinary request.** Its words are the original's saved words, verbatim, followed by the case. It takes the four standard criteria and any criterion the amendment moved to it.
- **The remaining cases have an owner.** Every amendment that defers also names one follow-up for the cases nobody has named yet.
- **The item keeps what it has.** Its lane, its candidate, its worker's session and its history stay. Nothing is saved at a Git ref and nothing is closed.
- **On the amended item, a gap is filed and a regression is refused.** The verifier is shown the deferred cases. A finding that the item does not handle a deferred case, or a case of the request the amendment's scope does not name, is recorded on the follow-up that owns it when the trunk with the candidate behaves there as the trunk does without it. The item is refused for making the trunk worse, for an unasked change, for a missing test or for not delivering the scope it states. This is the split design's [rule for a part](./tillr-item-split.md#4-the-rules-a-split-keeps), on an item that is not split.
- **Only the owner drops a follow-up.** One closed any other way shows as needing him, on its own page and on the original's.

**Deferring or splitting.** The design seat defers when what stays is one behavior that can land alone and the candidate needs nothing taken out of it. It writes a [split statement](./tillr-item-split.md#71-the-split-statement) when the request is several behaviors, or when part of the candidate must be carved away. A split parent is never amended; its parts are.

### 6.3 What an amendment may not do

| It may not | Because | What happens instead |
| :--- | :--- | :--- |
| Change, shorten or re-order the saved words | P1 | The drain compares every quoted sentence with the saved bytes and refuses a mismatch |
| Drop a sentence or a criterion | P3 | The drain refuses an amendment that leaves one of the request's own criteria unaccounted for. A case nobody will build is released by the owner, in his dated words |
| Add work | P3 | The verifier's first standard criterion covers the amended statement as it covers the words |
| Change one of the four standard criteria | They are the same for every request | Refused by the drain |
| Change a criterion a verdict has frozen | [Criterion repair](./criterion-repair.md#1-the-invariant-the-freeze-protects) owns that | The design seat writes a criterion supersession, when that is built |
| Contradict a recorded owner ruling | P4 | Where the words are the operator's, the amendment reads the request so that it keeps the ruling. Where the words quote the owner and are later than the ruling, only he can say which stands, and the design seat ends on the owner |
| Contradict a designer's decision in a design document | The decision is the design seat's to change, in its document | The design seat changes the decision there first, as a design answer, and the amendment cites it |
| Add, edit or remove an item comment in a document | A filed request has none | Refused |
| Carry a verdict or code | The design seat neither judges nor builds | An amendment is prose in fixed fields |

### 6.4 When a design document also changes

A design document is changed when a decision of that design is missing or changes. It is not changed to record how one request is read.

When both are needed, the design seat commits the document change and writes the amendment in the same turn. The amendment names the section, so the worker's brief points at the answer wherever the worker's base is.

### 6.5 How it is recorded

The design seat ends its run with an amendment statement as its output. The drain checks it and records it in one store transaction: the amendment, the criteria it replaces or moves, each follow-up as an item, and each edge. Either all of it is stored or none. Recording is keyed by the item and the amendment's content, so a replay after a crash records nothing twice. The saved words are not written.

The drain checks, and refuses the whole statement with the reason when one fails:

- the reason is present and names at least one flaw from the list;
- every sentence quoted from the saved words is found there byte for byte;
- every one of the request's own criteria is kept, replaced or moved, and none of the standard four is touched;
- no replaced criterion has a verdict;
- every deferred case has follow-up words or names an existing open item, and a follow-up for the remaining cases is named;
- the criteria of the item and of each follow-up fit the limit of eight.

A refused statement goes back to the design seat. Nothing is recorded and the item stays where it was.

## 7. When an amendment is called for, and what the worker does meanwhile

### 7.1 The five callers

| Caller | The point | What the call states | What the worker does meanwhile |
| :--- | :--- | :--- | :--- |
| **A reader at filing** ([OQ-RA1](#OQ-RA1)) | After the request is filed, before its first plan or build run | Each flaw found, by number, with the sentence | No worker has started. A request with no flaw found starts at once |
| **The planner** | At the plan step | Each flaw, with the plan it wrote for the rest; or a split call | Nothing is built until the design seat answers, because the plan may change |
| **A worker** | At the end of its run, with its commit | A left-undone list: each sentence or criterion it did not carry out, and one of four reasons ([§7.2](#72-the-left-undone-list)) | It has finished. Its candidate waits for the design seat and then goes to the verifier |
| **The verifier** | At a not-done verdict | Nothing new: the split design's [signals](./tillr-item-split.md#6-the-signals) make the call, and the design seat may answer it with an amendment that defers | The candidate is kept in its lane |
| **The operator** | At any time | The flaw and the evidence | A running worker finishes its run first |

A call made during a run waits for that run to settle. A second call on an item that has one pending is added to the first, so one design turn answers both.

### 7.2 The left-undone list

A worker on a build, edit, docs, test-write or tricky seat that meets a sentence or criterion it cannot carry out does everything else, commits, and lists what it left. Each entry quotes the sentence or names the criterion, and gives one reason:

| Reason | The worker's evidence |
| :--- | :--- |
| It is another seat's | The seat rule it would break |
| The tree does not establish it | What it read, and what that does and does not show |
| The tree contradicts it | The command and its output |
| A recorded ruling or decision forbids it | The ruling, cited |

The worker ends with a `definition` obstacle only when the flaw leaves nothing to build: the request's premise is false, or every part of the work depends on the answer.

A candidate that arrives with a left-undone list goes to the design seat before the verifier. The design seat answers each entry:

- **It stands.** The entry becomes part of an amendment, and the candidate goes to the verifier, which judges it against the amended statement. The worker is not run again.
- **It does not stand.** The request is right as written. The entry goes back to the worker's own session as a finding, as a verifier's finding does.
- **Only the owner can say.** The item waits on him, with the question in a document, by the existing route.

A worker cannot lower its own bar this way. An entry changes nothing until the design seat accepts it, and the verifier is shown the entry, the amendment and the original text.

### 7.3 The design seat's turn

The design seat is given the saved words, every criterion, each pending call with its evidence, the candidate when there is one, the earlier amendments and the list of flaws. It reads the whole request against the list (P6) and answers with one of four things:

| Answer | When | What follows |
| :--- | :--- | :--- |
| **An amendment** | A flaw F1 to F4, F6 or F7 holds | The drain records it. The item goes on from where it was: to its first run, back to its worker's session, or to the verifier |
| **A split statement** | F5 holds, or the candidate must be carved | [The split design](./tillr-item-split.md#7-what-a-split-produces) |
| **No change**, with the reason | The request is right as written | The call's findings return to the caller's seat. The answer is shown, and the same flaw is not called again without evidence the answer did not see |
| **The owner** | Only he can settle it | The item waits on him, with the question written where he reads it |

No landing stands between an amendment and the worker's return: the amendment is in the store and in the next brief, so the worker's base does not have to move. A landing is needed only when a design document changed ([§6.4](#64-when-a-design-document-also-changes)).

### 7.4 A flaw in the plan goes to the planner

A worker whose only obstacle is that its node's task or write list does not fit the change says so, and the item goes to the plan seat, which amends the plan within the unchanged request. No design turn is spent and no design document is written. The route exists for a verifier's `plan-wrong` reason and for a builder during a verification repair; this extends it to an ordinary build worker's report. The worker is still stopped: a node's write list is enforced, so nothing here lets it write outside it.

### 7.5 Failure and edge cases

| Case | What happens |
| :--- | :--- |
| The design run fails or ends with no answer | The item is held on the design seat with the call shown; the existing route for a design run that repeats applies |
| The statement is malformed | Refused whole, with the reason, back to the design seat |
| An amendment is recorded while a verifier's run is in flight | A verdict names the amendment it was judged under. One written under an earlier amendment is discarded and the item is judged again |
| The amendment changes nothing the candidate depends on | The candidate goes on to the verifier without a worker's turn |
| The amendment asks for a change to the candidate | The item returns to its worker's own session with the amendment |
| A call names no flaw from the list | It is refused at once and the caller is told. A flaw that keeps being met and is not on the list is added to the list by a reviewed commit |
| Two calls disagree | Both are in front of the design seat in one turn |
| A follow-up cannot be filed | The transaction fails, and nothing of the amendment is recorded |
| The item is a split parent | It is not amended; the call goes to the part it concerns |
| The item is not a filed request | Not covered: an item with a block in a document keeps its existing path |
| A request in flight on the day a slice ships | Nothing changes for it until its next call |
| A left-undone list arrives with no commit | It is an obstacle, and is treated as one |

## 8. How the split of responsibilities stays enforced

| Seat | May | May not | What holds it |
| :--- | :--- | :--- | :--- |
| Worker | Build; decline a sentence and say why | Edit a design document; change the words or a criterion; decide that its own entry stands | Its brief's design-document rule; the store is on the deny-list; an entry takes effect only through the design seat; the verifier sees both texts |
| Design seat | Write an amendment or a split statement; change a design document | Build; judge; change the saved words; drop a case | Its task; the drain's checks in [§6.5](#65-how-it-is-recorded); both texts in front of the verifier and on the item page |
| Verifier | Judge against the current statement; state a finding's kind | Write or propose an amendment; ignore one | Its output is a verdict; a verdict names the amendment it judged under |
| Planner | Plan within the statement; amend its own plan; call | Change a criterion | Its output is a plan or a call |
| Reader at filing | Report flaws | Write anything | It is read-only |
| Operator | File; call | Write an amendment | The record is written only from a design run's output |
| Drain | Check and record | Author any text | It copies the statement or refuses it |
| Owner | Release a case; overturn any decision here | — | — |

Two limits on that table. The design-document rule is an instruction in a worker's brief and not a path check: the deny-list (`config/tillr/deny-list.json`) does not name `docs/design/`, and this design adds no fence. And the operator can still close an item and file another by hand; nothing forbids it, and [§11](#11-the-build-in-slices-that-each-land-alone) removes the reasons to.

The split is enforced by who may write which record, not by stopping the worker. The worker never needs to cross the line to keep working, so there is nothing to stop it for.

## 9. What the queue and the status surfaces show

| Surface | What it shows |
| :--- | :--- |
| The item page and `tillr item show` | The saved words. Then each amendment, newest first: when, by which design run, on whose call, the reason, the reading, the scope, each deferred case with its follow-up's state, and each replaced criterion with both texts. Each left-undone list and the answer to each entry. Each "no change" answer |
| A worker's brief | The current amendment, then the saved words, byte-counted at the end |
| The verifier's brief | The same, with every replaced criterion's old text marked *replaced*, the deferred cases, and any left-undone list |
| The queue's reasons | "waiting on the design seat: amendment called by the worker for F2", with the design seat as mover |
| A follow-up's row and page | The request it was deferred from and the case |
| Reports of landed work | An item with deferred cases reads "landed; 2 of 5 deferred cases landed" until the last one lands or the owner releases it |

## 10. How this fits what exists

- **The split.** A split replaces an item by parts under a parent, and the parent's criteria never change. An amendment keeps the item and restates how it is read. The split design's [answer to a call](./tillr-item-split.md#71-the-split-statement) is a split statement or "no split"; this adds a third answer, an amendment that defers. The callers of a split and the five here overlap on purpose: a call reaches the design seat once, and the design seat chooses the answer.
- **Criterion repair.** Before a verdict freezes a criterion, the design seat may rewrite it with no record. An amendment is that act with a record, a reason and both texts shown. After the freeze, [criterion supersession](./criterion-repair.md#5-the-shape-any-repair-has-to-have) applies and an amendment does not. A verifier's refusal of an unlanded candidate is not a freeze, so an amendment written after such a refusal can still make a criterion easier; the guard is visibility.
- **The rule that a build worker does not edit a design document.** Kept whole. What changes is what the worker does when it meets the rule: it finishes and reports instead of stopping.
- **The split design's rule for marking a slice built** ([its section 14](./tillr-item-split.md#14-the-build-in-slices-that-each-land-alone)). Unchanged: the mark is the design seat's. Under this design the sentence asking for the mark is flaw F1, and the amendment says whose share it is.
- **The returned worker's base.** [The judge design](./tillr-judge-and-lifecycle.md#419-a-worker-returned-by-a-design-answer-starts-from-a-trunk-that-holds-the-answer-d-jl32) fixes the base of a worker returned by a landed design answer. An amendment is in the brief whatever the base; the base rule is still needed for an answer that is a document.
- **The design document check.** [That design](./tillr-design-document-check.md) is a read-only check of what an item is about to commit, against written policies, on the cheapest model. It writes nothing, so it is not the rewriter. A reader at filing is the same kind of check at a different point: a request read against a written list.
- **The landing principles.** [A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act): a pending call names the design seat. [A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report): deferred cases are shown on the original. [Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change): deferring is how a request reaches it without being closed.
- **No count.** No call reads a tally or a clock ([§4](#4-the-rules-an-amendment-keeps), P8).

Per round, an amendment removes the landing of the answer and can shorten the restart; the wait for the design seat and the design run remain. The larger effect is in rounds that do not occur: the flaw is read at filing, or the worker is not stopped, or one turn settles what three would. An amendment narrows what is asked; it does not make the candidate right.

## 11. The build, in slices that each land alone

Each slice is filed as its own request and lands as its own commit. Each is useful on the trunk if no later slice lands, and none makes the trunk worse while the later ones are queued (`D-RA11`). The order puts what only shows facts first, then the record with a person calling for it, then each caller.

| Slice | What lands | Why it is useful alone | Follows |
| ---: | :--- | :--- | :--- |
| 1 | The item page and `tillr item show` show a filed request's saved words | A reader sees what was asked without opening a brief | — |
| 2 | The amendment record, without deferral: a statement written by a design run at the operator's call is checked and recorded in one transaction; the saved words are untouched; worker and verifier briefs carry the current amendment; the item page shows every amendment and each replaced criterion's two texts | An operator's correction of a request keeps the item, its candidate and its history | 1 |
| 3 | A definition obstacle on a filed request is answered by an amendment. The design seat is told to read the whole request against the flaws, and to change a design document only where a design decision is missing or changes. A worker returned by an amendment alone waits for no landing | The answer is in the next brief, so the same obstacle is not raised again, and a round costs no gate run and no trunk commit | 2 |
| 4 | The left-undone list: a worker's brief tells it to finish the rest and list what it left; a candidate with a list goes to the design seat before the verifier; an accepted entry is amended and a rejected one returns to the worker | A worker is not stopped for F1, F2, F3 or F7 when there is other work to do | 2 |
| 5 | Deferral: an amendment may defer named cases; the drain files each follow-up in the amendment's transaction; the verifier of an amended item is shown the deferred cases and files a gap against the follow-up that owns it; the original's page shows each deferred case's state | Narrowing a request does not mean closing it and filing by hand | 2 |
| 6 | A worker's report that only its node's task or write list is wrong goes to the plan seat | A flaw in a plan costs a plan turn and no design record | — |
| 7 | The verifier's call is answered by an amendment: when the split design's call reaches the design seat, it may answer with an amendment that defers | An item leaves the repair loop with its candidate in its lane | 5, and [split slice 8](./tillr-item-split.md#14-the-build-in-slices-that-each-land-alone) |
| 8 | The planner reports flaws with its plan | A flaw is settled before anything is built on it | 2 |
| 9 | The reader at filing, as [OQ-RA1](#OQ-RA1) is ruled | A flaw readable in the words is settled before any worker starts | 2 |

Slices 1 and 6 follow nothing. Slices 3, 4, 5 and 8 each follow only slice 2 and may be built at once in separate lanes.

Each slice's request is written for a build seat alone: it does not ask the worker to mark this table. The design seat marks a slice here when its request reaches it.

## 12. What this does not do

- **It does not rewrite the owner's words**, in a request or anywhere else.
- **It does not let a worker or a verifier change what is asked.**
- **It does not cover items declared by a block in a document.** Their path is unchanged.
- **It does not replace criterion supersession.** A frozen criterion is still that design's.
- **It does not stop the operator closing and refiling by hand.**
- **It does not add a path fence** on design documents or anything else.
- **It does not name commands, fields, events or store tables.** Those belong to the implementation plan.
- **It does not change the filing guidance.** The guidance tells an operator how to write a request that needs no amendment, and stays the first defense.

## 13. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Edit the saved words in place | Rejected: the original is lost, and the owner's words in a request would be changed by an agent |
| The design seat keeps answering in design documents | Rejected on the measurements of [§2](#2-the-measured-basis): the answer does not reach the brief, each round lands a commit, and one request's reading is recorded in a document about something else |
| A separate janitor seat writes the amendment | Rejected: an amendment restates what finished means, which is the bar. A second seat that writes the bar would need the same context at the same cost |
| The design document check writes it | Rejected: that check is read-only, on the cheapest model. A reader at filing is its kind of work; the rewrite is not |
| The planner writes it | Rejected: the planner is held to the criteria it would be changing |
| The operator writes it | Rejected: the operator wants the queue to drain, and wrote the words in the first place. It may call |
| Let a build worker make small design-document edits, such as marking a slice built | Rejected: it sets aside the ruling that only an item's designer changes a design document |
| Every narrowing is a split with a parent | Rejected: it replaces the item, its lane and its worker's session to take one case out |
| Narrow by closing and refiling | Rejected as the standing mechanism: the history is on a closed item, and each refile is hand work the operator has to notice is needed |
| Stop the worker at every flaw | Rejected by `R-RA1`: a back and forth per flaw is waste |
| Call for an amendment after a number of refusals or obstacles | Rejected: no count routes work |
| Refuse a flawed request at filing | Rejected: the filer is usually an agent that then rewords until the check passes, and a filing check lets some through |

## 14. Risks

| Risk | Mitigation |
| :--- | :--- |
| The design seat lowers a bar to get an item landed | Both texts and the reason are in front of the verifier and on the item page; nothing can be dropped, only deferred to a filed follow-up; only the owner releases a case |
| Follow-ups pile up and are never built | Each is an ordinary queued request, shown on the original with its state; only the owner drops one |
| Workers list what they should have done | An entry takes effect only if the design seat accepts it, and a rejected entry returns as a finding |
| The design seat becomes the queue's narrow point | It already answers every definition obstacle. An amendment needs no landing, one turn settles the request, and how long calls wait is in the queue's reasons |
| A flaw on the list is wrong, and good requests are sent to the design seat | The design seat answers "no change", which is shown; the list is a tracked file |
| The amendment and a design document disagree | The amendment cites the section and does not restate it; a decision is changed in its document first |
| A deferred case hides a regression | A gap is filed only where the verifier shows the trunk with the candidate behaves as the trunk does without it; a finding it cannot classify is a regression, as in the split design |

## Open Questions

1. 💬 **OQ-RA1: Should every new request be read for known flaws before its first worker starts?**

   The operator files a request with `tillr item request`, and one sentence of it says "Update the slice table to mark this slice built." Today the request goes straight to a build worker, which stops when it reaches that sentence, and the next worker starts about twelve minutes later. Of six requests examined closely, four had a flaw of this kind that could be read in their words when they were filed. A reader would see those before any worker starts, but it is one more run on every request, including the ones you are waiting on.

   - **A — A cheap read-only seat reads every request.** Each new request shows "being read" for one short run on the cheapest model before its first worker starts. One with a flaw then shows "waiting on the design seat" and names the flaw; one without starts. You pay one cheap run per request, and every request starts a little later.

   - **B — Nothing reads it first.** Every request starts at once. A flaw is reported by the first planner or worker that meets it, together with its work, and the design seat amends then. A clean request costs nothing extra; a flawed one costs part of a worker's run before it is amended.

   - **C — The design seat reads every request.** The reader can also rewrite, so a flawed request is amended in the same run. Every request waits for a run on the dearest model before it starts; a measured design run takes between one and a half and five and a half minutes.

   <!-- vantage: question id=OQ-RA1 leaning="A (A cheap read-only seat reads every request) — four of the six examined requests had a flaw readable at filing, and the reading is a check against a written list, which is work for the cheapest model." -->

   _Leaning:_ A (A cheap read-only seat reads every request) — four of the six examined requests had a flaw readable at filing, and the reading is a check against a written list, which is work for the cheapest model.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

`R-RA1` is the owner's ruling. Every other row is the designer's decision, and the owner may overturn it. Nothing is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-RA1 | **Owner ruling.** How a request is worded should not decide how hard the work is; the split of responsibilities stays enforced; a back and forth per flaw is waste; the right part of the design gets permission to rewrite a request that is going to be hard to deliver | 2026-11-21, owner | [§1](#1-the-ruling) | — |
| D-RA1 | The design seat writes the rewrite: it already rewrites a criterion before a verdict, and it is neither the worker nor the judge. **Rejected:** a janitor seat, the design document check, the planner and the operator as the writer | 2026-11-21, designer | [§4](#4-the-rules-an-amendment-keeps) (P2), [§13](#13-alternatives-considered) | — |
| D-RA2 | The rewrite is an amendment: an append-only record beside the saved words, which never change. The latest amendment is the whole current reading | 2026-11-21, designer | [§4](#4-the-rules-an-amendment-keeps) (P1), [§6.1](#61-what-it-holds) | — |
| D-RA3 | An amendment may state how a sentence is read, name the seat whose share a sentence is, defer a named case, and replace or move one of the request's own criteria that has no verdict. It may not change the saved words, drop a case, add work, change a standard or frozen criterion, or contradict a ruling | 2026-11-21, designer | [§6.3](#63-what-an-amendment-may-not-do) | — |
| D-RA4 | Nothing is dropped: a narrowed case is deferred to a follow-up filed in the same transaction, and only the owner releases one | 2026-11-21, designer | [§4](#4-the-rules-an-amendment-keeps) (P3), [§6.2](#62-deferring-a-case) | — |
| D-RA5 | An item with deferred cases keeps its lane, candidate and session and lands when its stated scope is met. On it a gap in a deferred or unnamed case is filed against the follow-up and a regression is refused. **Rejected:** every narrowing is a split with a parent | 2026-11-21, designer | [§6.2](#62-deferring-a-case) | — |
| D-RA6 | The seven flaws of [§5](#5-the-flaws), kept as a tracked list, and the three things that are not flaws in a request | 2026-11-21, designer | [§5](#5-the-flaws) | — |
| D-RA7 | A worker that meets a flaw finishes the rest, commits and lists what it left; it ends with an obstacle only when nothing can be built. An entry takes effect only if the design seat accepts it | 2026-11-21, designer | [§7.2](#72-the-left-undone-list) | — |
| D-RA8 | The design seat reads the whole request against the flaws on any call and answers with an amendment, a split statement, "no change" or the owner. A design document changes only where a design decision is missing or changes | 2026-11-21, designer | [§7.3](#73-the-design-seats-turn), [§6.4](#64-when-a-design-document-also-changes) | — |
| D-RA9 | A flaw in a plan node's task or write list goes to the plan seat and not to the design seat | 2026-11-21, designer | [§7.4](#74-a-flaw-in-the-plan-goes-to-the-planner) | — |
| D-RA10 | The saved words, every amendment, every replaced criterion's two texts and every left-undone list are shown on the item page and to the verifier. The owner is not asked to approve an amendment; he sees it | 2026-11-21, designer | [§9](#9-what-the-queue-and-the-status-surfaces-show) | — |
| D-RA11 | The build is nine slices that each land alone, in the order of [§11](#11-the-build-in-slices-that-each-land-alone) | 2026-11-21, designer | [§11](#11-the-build-in-slices-that-each-land-alone) | — |
| D-RA12 | No call is made by a count of attempts, refusals or obstacles, or by elapsed time | 2026-11-21, designer | [§4](#4-the-rules-an-amendment-keeps) (P8) | — |
