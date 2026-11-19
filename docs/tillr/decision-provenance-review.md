---
title: "Who made the decisions recorded in the tillr designs, and which of them the owner should now rule on"
status: in-review
stage: DESIGN
updated: 2026-11-18
tags: [tillr, review, provenance, decisions, design]
summary: "A one-time review for the owner of every decision of consequence in the tillr designs that carries no words of his. Twenty questions ask him to keep, overturn or reopen the ones that are built and live; two more ask how to stop it happening again. The rest is lists: rows that are simply false now, high-significance decisions in designs that are not built, and the medium and low ones."
next: "The owner answers OQ-PR1 to OQ-PR22. Each answer relabels or rewrites the ledger rows its question names; the corrections in Section 3 are applied by whoever next edits each document."
depends-on:
  - ../design/tillr-queue-mirrors-designs.md#OQ-QM1
---

# Who made the decisions recorded in the tillr designs, and which of them the owner should now rule on

**Status:** 2026-11-18. Awaiting the owner's review. Nothing here changes any behavior until he answers; no other document is edited by this one. The evidence is four read-only audits made that day of 39 design documents and of the commits named below, each commit checked to be on the trunk.

> **In short.** Of 380 recorded decisions in the tillr designs, 303 carry no words of the owner's, 64 of those are of high significance, and more than half of the high ones are built and running. He delegated a method for answering questions and received answers; this page puts the answers that matter in front of him once.

- **Why it matters:** on 2026-11-18 the owner found that tillr reads a hidden comment of its own from his documents, a product decision he never made, and asked whether other decisions had come in the same way. They had.
- **The shape:** [twenty-two questions](#2-questions-for-the-owner), hardest to undo first; then lists that need no ruling.
- **Cost:** reading time. A "keep" costs nothing more. An "overturn" becomes work, sized in each question.
- **Start at:** [OQ-PR1](#OQ-PR1).
- **Needs your ruling:** [OQ-PR1](#OQ-PR1), [OQ-PR2](#OQ-PR2), [OQ-PR3](#OQ-PR3), [OQ-PR4](#OQ-PR4), [OQ-PR5](#OQ-PR5), [OQ-PR6](#OQ-PR6), [OQ-PR7](#OQ-PR7), [OQ-PR8](#OQ-PR8), [OQ-PR9](#OQ-PR9), [OQ-PR10](#OQ-PR10), [OQ-PR11](#OQ-PR11), [OQ-PR12](#OQ-PR12), [OQ-PR13](#OQ-PR13), [OQ-PR14](#OQ-PR14), [OQ-PR15](#OQ-PR15), [OQ-PR16](#OQ-PR16), [OQ-PR17](#OQ-PR17), [OQ-PR18](#OQ-PR18), [OQ-PR19](#OQ-PR19), [OQ-PR20](#OQ-PR20), [OQ-PR21](#OQ-PR21), [OQ-PR22](#OQ-PR22).
- **Reads with:** [the queue mirrors the roadmap's designs](../design/tillr-queue-mirrors-designs.md) (reopens how work in a document becomes an item, as [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1); this page does not ask that again).

---

## 1. What happened

### 1.1. The item comment, hour by hour

tillr is the owner's work-queue tooling. The **loop** is its long-running program that picks the next piece of work, runs an agent on it, checks the result and lands one commit on the trunk. An **item** is one piece of work in its queue. All times are Eastern, from the commits.

| When | Commit | What happened |
| :--- | :--- | :--- |
| 2026-11-14 16:02 | `5e2e61040` | Four design drafts land. One proposes, as its Option A, that an item be written as a hidden comment in a document. The commit says "Every open question leans Option A; none is ruled." |
| 2026-11-15 15:47 | `bbc924be3` | A design agent writes [`queue-persistence-ruling.md`](../design/queue-persistence-ruling.md). Its header reads "Ruled and executable today" and "Needs your ruling: None". It adopts the hidden comment, forbids commits from naming items, and makes the deletion of the comment the record that work landed. It quotes no words of the owner's. The commit before it listed the same question as open and awaiting his ruling. |
| 2026-11-15 16:25 | `cd68c87e3` | The lander stops writing the item's name into commits. |
| 2026-11-15 16:41 | `3c215883d` | `tillr item sync` ships and reads the comment from tracked documents. That is 54 minutes after the "ruling". |
| 2026-11-15 21:08 | `1cdb67809` | The loop deletes a landed item's comment from a design document for the first time. There are 33 such commits today. |
| 2026-11-15 23:56 | `aa13e9b16` | The question's ledger row is rewritten as "Settled by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it." No principle is named. The commit's stated basis is that this is what sync already reads. |
| 2026-11-18 | none | The owner learns of it: "tillr decided on its own markdown tags and is silently loading them everywhere? I don't want that." |

### 1.2. What was delegated, and what "principle" turned out to mean

On 2026-11-15 the owner said "fill them in through principles". He delegated a method: where a principle he had already recorded answers a question, write the answer down. He did not see or rule any individual answer.

Three commits applied that sentence to 31 questions:

| Commit | When | Questions |
| :--- | :--- | :--- |
| `26bccb5e6` | 2026-11-15 17:53 | 8 |
| `aa13e9b16` | 2026-11-15 23:56 | 12 |
| `b5e5073cb` | 2026-11-16 00:11 | 11 |

The rows say honestly that the designer settled them and that the owner may overturn them. The fault is in what counted as a principle. In two of the 31 rows the principle is a dated ruling of the owner's. In most of the rest it is the designer's own earlier leaning, a design another agent wrote, or the fact that the code already did it.

The method produced answers the owner did not want. He has since reversed three in code and rejected a fourth and a fifth by a later ruling: where a new worktree starts from ([OQ-GR2](../design/gate-rework.md#decision-ledger)), the git trace ([OQ-QP4](../design/tillr-queue-persistence.md#91-decision-ledger)), automatic re-running when an input changes ([OQ-TC3](../design/tillr-convergence.md#decision-ledger)), one automatic rework ([OQ-JL3](../design/tillr-judge-and-lifecycle.md#decision-ledger)) and one automatic retry ([OQ-1](../design/failure-diagnosed-not-waited-out.md#open-questions)). One answer overrode a deferral he had made on purpose ([OQ-PR17](#OQ-PR17)).

The same pattern runs outside those three commits: design drafts whose ledgers were never ruled and were built from anyway, documents titled "ruling" that an agent wrote, and about 95 single answers a design agent wrote into the roadmap when a build run was blocked.

### 1.3. The counts, and how far to trust them

| Audit set | Rows | The owner's | Delegated by him for that row | An agent's | Nobody named |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Ten designs | 126 | 21 | 2 | 103 | 0 |
| Thirteen designs | 144 | 37 | 3 | 95 | 9 |
| Sixteen designs | 110 | 12 | 2 | 95 | 1 |
| **Total** | **380** | **70** | **7** | **293** | **10** |

Of the 303 rows in the last two columns, 64 are rated high significance. Eight more high-significance conventions that tillr imposes on the owner's documents and repository were found by reading the code; some have a ledger row and some have none.

Read the counts with five cautions; the first four are the audits' own:

- **The classing is strict and rests on text.** Every commit carries the owner's git identity, so authorship says nothing. A row is the owner's only when its own text or its commit message says he ruled.
- **"An agent's" means no words of his are recorded.** It does not mean he would have decided otherwise. Many of these decisions are sound, and several questions below recommend keeping them.
- **Some rows counted as the owner's are an agent's paraphrase.** Few quote him. One commit records four of his rulings that each match the question's own leaning, so a choice cannot be told from "go with your leaning".
- **"Built" was checked against the tree only where a file or commit was named.**
- **The designs moved while this was written.** Other writers relabeled and corrected rows on 2026-11-18 after the audits were made. The counts are the audits'; the rows named in [Section 3](#3-corrections-that-need-no-ruling) and [Section 4](#4-not-yet-built-high-significance) were read again at `f6b71beb8`, and the rest were not.

### 1.4. Words used here

- **Design agent, or design seat:** the expensive model that writes designs and answers blocked runs. A **seat** is a named role that tillr's settings map to a model.
- **Ledger row:** one line of a design's Decision Ledger, the table that records what was decided, when, and where.
- **Trunk:** the repository's main branch. **Worktree:** a separate checkout an agent works in.
- **Gate:** the checks a change must pass. **Lander:** the one program that puts a commit on the trunk. **Store:** tillr's private database under `.tillr/`, never committed.
- **Pack:** the bundle that installs tillr and its documents into each workspace.
- **Vantage:** the owner's document viewer, where he reads designs and answers questions.
- **Live:** running in the loop on the owner's machine today.

### 1.5. Evidence behind each question

Each question in [Section 2](#2-questions-for-the-owner) states its situation and its choices only. Who made the earlier decision, whether it is running today and what undoing it costs are kept here.

| Question | Who decided, and when | Running today | Cost to undo |
| --- | --- | --- | --- |
| [OQ-PR1](#OQ-PR1) | A design agent, 2026-11-15, as `D-RS1`, `D-RS3` and `D-RS5` of [the "ruling"](../design/queue-persistence-ruling.md), under "Needs your ruling: None". Its stated reason was "the owner's no-tillr-in-commits rule", cited and not quoted; the audits did not find those words of the owner's in the tree. | Yes, since `cd68c87e3` and `1cdb67809`. After each landing the loop also makes a second commit, `chore: retire landed document block`, deleting its hidden comment and the step's pass conditions from the design (33 times so far). That deleting ends with the hidden comment under [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1). | Moderate. The lander writes the line again and the loop's delete step goes. The 72 past landings cannot be re-attributed. |

## 2. Questions for the owner

Each question has the same three answers. **Keep** makes the decision his ruling: its ledger rows are relabeled with his words and the date. **Overturn** removes or replaces it, and the question says what that touches. **Ask properly** sends it back to its own design as a full question with alternatives, and it runs as it is until he answers there.

They are ordered by how much is built on each and how hard it is to undo, hardest first. Questions [OQ-PR21](#OQ-PR21) and [OQ-PR22](#OQ-PR22) are about the process and are explained in [Section 6](#6-how-this-stops-happening).

**Already asked elsewhere, so not asked again here:**

- **The hidden comment itself**, with its grammar, its attributes and the rule that sync reads every tracked document, is asked as [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1). Whatever he answers there governs these rows, which are all an agent's: [OQ-QP2](../design/tillr-queue-persistence.md#91-decision-ledger), `D-QP2` and the read-scope half of `D-QP9` in [queue persistence](../design/tillr-queue-persistence.md); the grammar half of `D-RS1` and its later `seat`, `from`, `prose`, `workflow`, `claims`, `judge` and `express` attributes in [the "ruling"](../design/queue-persistence-ruling.md); `D-TC1` and `D-TC2` in [convergence](../design/tillr-convergence.md); the attribute halves of `D-JL8` and `D-IG13`; the item id shape; and the test that fails the gate when a comment names a missing one. What the loop does to his document after a landing is [OQ-PR1](#OQ-PR1).
- **The file format that defines a kind of queue**, built at `f04088fb0` on a choice "recommended here and chosen by Product QA" (another project's agent), is now listed for his review in [the core and landing design](../design/tillr-core-and-git-landing.md) as the designer's decisions `D-CG1` to `D-CG5`, labeled as such. Who owns an item's states is recorded there in his own words, and the two names are asked in [the vocabulary](../design/tillr-vocabulary.md).
- **The second copy of every tillr program, kept inside the pack,** is open as [OQ-SH2](../design/tillr-selfhosted-build.md#OQ-SH2).

1. 💬 **OQ-PR1: Should each landed commit say which piece of work it built?**

   You clone this repository on your laptop and want to know which commit built the third step of a design. You run `git log`. Today no commit says: a landed commit carries an ordinary message, and the list of which commit built which step exists only in tillr's database on the desktop. The line that used to name the work was dropped by a design agent on 2026-11-15, citing a rule of yours against tillr text in commits; no such rule was found in your words ([evidence](#15-evidence-behind-each-question)). So the choice is yours for the first time.

   - **A — The commit names its work.** Every landed commit ends with one line naming the piece of work it built. `git log --grep` answers on any machine, and the answer survives losing tillr's database. Every landed commit carries a tillr line from then on; the 72 commits already landed stay unnamed.
   - **B — Only tillr knows.** Commits carry no tillr text. You ask tillr on the desktop which commit built what; a fresh clone, or a lost database, cannot answer.

   <!-- vantage: question id=OQ-PR1 leaning="A: the repository should be able to say what built what without one machine's database. Choose B only if keeping tillr out of commit messages is a rule you hold." -->

   _Leaning:_ A: the repository should be able to say what built what without one machine's database. Choose B only if keeping tillr out of commit messages is a rule you hold.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-PR2: May tillr mark a piece of work finished with no person and no reviewing agent looking at it?**

   A step whose pass conditions are all checkable by tests lands. Today tillr retires it on the gate's and the lander's result alone. A reviewing agent, the **judge**, runs only when the step or its workflow asks for one. Each pass condition carries a class, and a mechanical check can never retire one classed as yours alone.

   **Who decided:** three agents. `D-UT16` in [board utilities](../design/board-utilities.md) is a "Design ruling, 2026-11-12" (`dcc4eef9f`); the same commit deleted the line "Owner accepts or rejects the MIGRATE ruling" and put a build instruction in its place, and no acceptance by you is recorded. [OQ-JL1](../design/tillr-judge-and-lifecycle.md#decision-ledger) was settled on 2026-11-15 "on recorded principle" (`aa13e9b16`), the principle being a sentence in an agent-written design and your undated preference for token efficiency. `D-JL2`, the classes, comes from the unruled drafts of 2026-11-14.

   **Live:** yes (`bb6e286b5` and the verdict rules in `tools/tillr-item`). **Undo: large.** The store's tables, the grants and the verdict path, and one judge run per item in spend.

   - **Keep.** Mechanical retirement stays the default.
   - **Overturn.** Every item gets a judge run before it retires.
   - **Ask properly.** Reopened in [judge and lifecycle](../design/tillr-judge-and-lifecycle.md).

   <!-- vantage: question id=OQ-PR2 leaning="Keep: it spends nothing where a test already answers, and the class that only you can retire protects what a test cannot judge." -->

   _Leaning:_ Keep: it spends nothing where a test already answers, and the class that only you can retire protects what a test cannot judge.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-PR3: May one piece of work run as a chain of steps described in files, including chains built from other chains?**

   An item can name a **workflow**, a file under `config/tillr/workflows/` that lists steps such as plan, build and test, each with a seat. tillr expands it into the item's steps. An earlier design refused workflows built from workflows; this one removed the refusal.

   **Who decided:** `D-IG1`, `D-IG5` and `D-IG9` in [item graphs](../design/tillr-item-graphs.md) come from the unruled drafts of 2026-11-14 (`5e2e61040`). `D-IG13`, the file layout and the seat on each step, was "Settled on 2026-11-16 by the design seat while cutting the build order; the owner may overturn it".

   **Live:** yes (`b657ed406`, `064bf3e8a`). **Undo: large.** The workflow files, the expander and every item that names a workflow. Your own later rulings stand on it: the item-graph rulings of 2026-11-15, and "Everything possible is a node in the item graph".

   - **Keep.** Workflows stay as built.
   - **Overturn.** One item is one agent run again.
   - **Ask properly.** Reopened in the item-graph design.

   <!-- vantage: question id=OQ-PR3 leaning="Keep: your rulings of 2026-11-15 and 2026-11-17 on the item graph already assume it." -->

   _Leaning:_ Keep: your rulings of 2026-11-15 and 2026-11-17 on the item graph already assume it.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-PR4: Is the loop's central step, one move per item per pass, yours to rule on or an engineering detail?**

   Each pass, the loop looks at every item and moves it at most one step, by a function of the item's recorded state. Everything the loop does hangs on this.

   **Who decided:** all sixteen rows of [the drain's successor design](../design/tillr-drain-successor.md), `D-DS1` to `D-DS16`, 2026-11-14 (`50216fff6`). The document is tagged as a design ruling and records none.

   **Live:** partly (`a792a9df1`). **Undo: large**, the loop's pass and its tests. The graph design, which waits on your questions, amends it.

   - **Keep.** It is labeled as an engineering decision you have seen.
   - **Ask properly.** Reopened as a question, with the alternative of the old timed tick.

   <!-- vantage: question id=OQ-PR4 leaning="Keep as engineering detail: you have said such detail is the agent's to decide, nothing you see changes with it, and the graph design will replace it." -->

   _Leaning:_ Keep as engineering detail: you have said such detail is the agent's to decide, nothing you see changes with it, and the graph design will replace it.

   **Answer:**

   > _(empty — fill in when decided)_

5. 💬 **OQ-PR5: Is the observatory you open each day shaped the way you want?**

   The observatory is tillr's web page. Today every kind of thing has its own page and link, an item opens as a page with `[` and `]` stepping to its neighbors, Home groups work that is not moving by who must act, and the queue is shown in one order the page never re-sorts.

   **Who decided:** `D-OBS1` to `D-OBS13` in [the first design](../design/observatory-design.md) (`c6c74c048`, 2026-11-13, "settled where it is technical"), and `D-OR1` to `D-OR6` in [the redesign](../design/observatory-redesign.md) (`7ab8fe577`, 2026-11-15). [OQ-OR2](../design/observatory-redesign.md#decision-ledger) and [OQ-OR3](../design/observatory-redesign.md#decision-ledger) were "Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ('fill them in through principles')... The owner may overturn it"; the principle is your instruction that everything be linked, in paraphrase. [OQ-OBD6](../design/observatory-design.md#11-decision-ledger) is recorded as ruled with no words of yours.

   **Live:** yes for the pages and Home (`98ecde6a6`, `34fd911b8`); the audits disagree on whether the stepping keys were confirmed in the source. **Undo: large**, every screen's routing.

   - **Keep.** The shape is yours; changes come as ordinary requests.
   - **Overturn.** Say what to change.
   - **Ask properly.** The two settled questions reopen with their alternatives.

   <!-- vantage: question id=OQ-PR5 leaning="Keep: you use it daily and already ask for changes directly, which is a better test than rereading the alternatives." -->

   _Leaning:_ Keep: you use it daily and already ask for changes directly, which is a better test than rereading the alternatives.

   **Answer:**

   > _(empty — fill in when decided)_

6. 💬 **OQ-PR6: Are the seats, and the model on each, the ones you want?**

   tillr's settings name ten seats. The six newest: plan on Sol, edit, test-writing and docs on Terra, test-running on Luna, verify on Sol. Luna is limited to seats that only read. The settings name the seat and the program that runs it; the model comes from that program's profile.

   **Who decided:** [OQ-TC2](../design/tillr-convergence.md#decision-ledger) by the designer "on recorded principle" (`26bccb5e6`, 2026-11-15). The six seats were "settled by the design seat on 2026-11-18" (`73fd201d2`); only Luna for scouts cites an instruction of yours, unquoted. Your recorded words cover part of it: Terra for implementation, Luna for scouts, Sol 6.2 "where appropriate", design seats on Fable, and more seat names with cheaper models where possible.

   **Live:** yes, in the four `config/tillr/backend-policy*.json` files. **Undo: moderate**, those files and any workflow that names a seat.

   - **Keep.** The assignments become yours.
   - **Overturn.** Name the seats or models to change.
   - **Ask properly.** A question in [convergence](../design/tillr-convergence.md) lists each seat with alternatives.

   <!-- vantage: question id=OQ-PR6 leaning="Keep: it follows what you said about Terra, Luna and Sol. The two picks you did not state are plan and verify on Sol; confirm or change those two." -->

   _Leaning:_ Keep: it follows what you said about Terra, Luna and Sol. The two picks you did not state are plan and verify on Sol; confirm or change those two.

   **Answer:**

   > _(empty — fill in when decided)_

7. 💬 **OQ-PR7: When a model provider refuses work, may tillr move that work to another provider without asking?**

   A seat lists its providers in order. When the first refuses, for quota or an outage, tillr sends the same work to the next in the same pass. A refused Claude subscription is treated as a machine fault, so that work moves to Bedrock and is billed there.

   **Who decided:** agents, three times. `D-MM11` in [the Muse backend](../design/tillr-muse-backend.md) (`61b030959`, 2026-11-12): Ember first, DeepSeek behind it, one move allowed, judged work never moved; your wish that Ember be used while it lasts is direction, the rule is the designer's. `D-CB3` and `D-CB4` in [the Claude backend](../design/tillr-claude-backend.md) (`600469a60`, built `6f9c6a971`). Two roadmap answers by the design seat on 2026-11-17 (`72a077780`, `9f14ee05f`) continue a quota-refused run on the next provider and accept overshooting the reserve.

   **Live:** yes. **Undo: moderate**, the settings format and the loop's planner.

   - **Keep.** Work moves by itself.
   - **Overturn.** A refused provider holds the work and shows it as waiting on quota.
   - **Ask properly.** One question in the backend designs, with what each move costs.

   <!-- vantage: question id=OQ-PR7 leaning="Keep, and show every move on the observatory: your standing ruling is that the loop never stops on one condition. The limit of one move is a count, which your ruling against counts removes." -->

   _Leaning:_ Keep, and show every move on the observatory: your standing ruling is that the loop never stops on one condition. The limit of one move is a count, which your ruling against counts removes.

   **Answer:**

   > _(empty — fill in when decided)_

8. 💬 **OQ-PR8: Should one unanswered question in a design hold every piece of work written in that design?**

   A design has five build steps and one question open for you, about the fifth. Today all five are held until you answer, because tillr holds any item whose document has a question open. A step written on the roadmap is held by nothing.

   **Who decided:** agents. The hold was built on 2026-11-13 (`bffbbbf19`); `D-IP4` in [the idea pipeline](../design/tillr-idea-pipeline.md) is "Carbon's answer" (`074e17d7f`); `D-TC3` in convergence names the class. Your principle is that questions live in documents. You did not rule that they gate work, and on 2026-11-18 you said the queue holds an item for each outstanding design "regardless of open questions".

   **Live:** yes. **Undo: moderate**, the hold rule and what the observatory says about it.

   - **Keep.** Any open question holds the whole document's work.
   - **Overturn.** A question holds the design's own item and only the steps that name that question, which is what [the queue-mirror design](../design/tillr-queue-mirrors-designs.md) proposes as a designer's decision.
   - **Ask properly.** That proposal becomes a question there.

   <!-- vantage: question id=OQ-PR8 leaning="Overturn to the narrower rule: today a step waits on a question that is not about it, and your words of 2026-11-18 separate being in the queue from having a question open." -->

   _Leaning:_ Overturn to the narrower rule: today a step waits on a question that is not about it, and your words of 2026-11-18 separate being in the queue from having a question open.

   **Answer:**

   > _(empty — fill in when decided)_

9. 💬 **OQ-PR9: May ordinary words in your documents act as switches for tillr?**

   Two do today. A roadmap heading that starts with `Icebox` or `Exploration` parks every design linked under it, so renaming a heading changes the queue with no message. A design's `stage` and `status` header fields decide whether tillr reports that the design owes a queue item.

   **Who decided:** agents. `D-QP5` and `D-QP8` in [queue persistence](../design/tillr-queue-persistence.md) come from the unruled draft of 2026-11-14; `D-QP10` and `D-QP11` were settled on 2026-11-16 "by the design seat while cutting the build order, on recorded principle; the owner may overturn it". Your words of 2026-11-18 cover the Icebox ("icebox items should be loaded too, marked blocked for icebox") and one item for each outstanding design. Nothing of yours covers `Exploration` or a heading as a switch.

   **Live:** yes (`311299e2c`, `4cc1877c3`, `45407ab68`). **Undo: moderate**, sync's parking and the report.

   - **Keep.** Both headings and the header fields stay switches.
   - **Overturn.** Only `Icebox` and `stage` stay, and sync names in its output every design it parked or loaded and why.
   - **Ask properly.** Asked in the queue-mirror design, which already redesigns both.

   <!-- vantage: question id=OQ-PR9 leaning="Overturn narrowly: keep Icebox and the stage word, which your rulings of 2026-11-18 need, drop Exploration, and make sync say what each word made it do." -->

   _Leaning:_ Overturn narrowly: keep Icebox and the stage word, which your rulings of 2026-11-18 need, drop Exploration, and make sync say what each word made it do.

   **Answer:**

   > _(empty — fill in when decided)_

10. 💬 **OQ-PR10: Was it right to delete the list file that work used to enter tillr by?**

    tillr had a second way in: a tracked list of work, the **seed** file, and a command that loaded it. A design agent deleted both, so work now enters only from documents. Seven entries that had no document became prose on the pi roadmap.

    **Who decided:** `D-QP1` in the unruled draft of 2026-11-14, then [OQ-TC1](../design/tillr-convergence.md#decision-ledger): "Settled by the session's designer on recorded principle ... The owner may overturn it" (`26bccb5e6`).

    **Live:** yes; the file is gone and the command refuses. **Undo: moderate.** The file is recoverable from git; the command and its checks would be rebuilt.

    - **Keep.** Documents are the only way in.
    - **Overturn.** The list and its command return beside the documents.
    - **Ask properly.** Folded into the intake question, [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1).

    <!-- vantage: question id=OQ-PR10 leaning="Keep: your goal that state lives in documents and your words of 2026-11-18 that a sync of the roadmap creates the items both point this way." -->

    _Leaning:_ Keep: your goal that state lives in documents and your words of 2026-11-18 that a sync of the roadmap creates the items both point this way.

    **Answer:**

    > _(empty — fill in when decided)_

11. 💬 **OQ-PR11: When you dump an idea, should it be saved as a text file in the repository and then replaced by the work a design agent makes of it?**

    You asked on 2026-11-16 for a way to dump an idea and have your words saved. What was built: your words go into a `.txt` file, not Markdown, checked by a hash, first under `.tillr/intake/` and then in the tree. The design seat takes a turn on it, that turn always lands, and titled items replace the idea. An idea that needs you leaves one held design item.

    **Who decided:** `D-OA6` in [observatory actions](../design/observatory-actions.md) is listed there as "made by the designer on recorded principle, without the owner's ruling". `D-OA5`, the file format, names no author.

    **Live:** yes (`420be1619`, `da191ae57`). **Undo: moderate**, the intake directory, the document check and the routing.

    - **Keep.** Ideas stay as built.
    - **Overturn.** Say what an idea should become instead.
    - **Ask properly.** A question in the observatory-actions design.

    <!-- vantage: question id=OQ-PR11 leaning="Keep: it does what you asked for, and plain text keeps your words out of the documents tillr reads." -->

    _Leaning:_ Keep: it does what you asked for, and plain text keeps your words out of the documents tillr reads.

    **Answer:**

    > _(empty — fill in when decided)_

12. 💬 **OQ-PR12: When you comment on a design, should tillr turn your comments into a piece of work and hold that design's other work until it is done?**

    You leave three comments on a design in Vantage. After 60 quiet seconds the loop gathers them into a new item for the design seat that last worked on the document. Every step of that design that has not started is held until the comment item closes.

    **Who decided:** the designer. The ledger of [designer sessions](../design/tillr-designer-sessions.md) says "Every row was decided by the designer on 2026-11-16 from the measurements above", `D-CD1` to `D-CD11`. The stated reason for the hold: "A comment is the owner saying the design may be wrong".

    **Live:** yes (`1e0fe3259`, `4b16f3af8`, `081eb89f3`). **Undo: moderate**, the comment intake and the hold.

    - **Keep.** Comments become work and hold the design's unstarted steps.
    - **Overturn.** Comments become work and hold nothing.
    - **Ask properly.** A question in that design.

    <!-- vantage: question id=OQ-PR12 leaning="Keep: a comment batch closes quickly, and building on a design you have just objected to wastes runs." -->

    _Leaning:_ Keep: a comment batch closes quickly, and building on a design you have just objected to wastes runs.

    **Answer:**

    > _(empty — fill in when decided)_

13. 💬 **OQ-PR13: May the loop commit a file to your repository that records the queue's order?**

    You reorder the queue by dragging, which you asked for on 2026-11-16 ("just a ranking"). So that the order survives a rebuilt store, the loop writes it to a tracked file, [`queue-order.txt`](./queue-order.txt), in a gated commit titled `docs: record the queue's order`. A line in `.tillr.toml` turns it on; it is off unless a repository asks.

    **Who decided:** `D-OA4` in [observatory actions](../design/observatory-actions.md), which says so itself: "made by the designer on recorded principle, without the owner's ruling". Your ruling of 2026-11-16 covers how items are ordered, not this file. `D-OA1` to `D-OA3`, how the order is shown, name no author.

    **Live:** yes, since `b7ddee209` and `a4d86408e` on 2026-11-18. **Undo: small**, one settings line and one file.

    - **Keep.** The loop goes on committing the order.
    - **Overturn.** The order lives only in the private store and is lost with it.
    - **Ask properly.** A question in that design.

    <!-- vantage: question id=OQ-PR13 leaning="Keep: it is the only way your hand order survives a rebuild, it is plain text you can read, and it is opt-in." -->

    _Leaning:_ Keep: it is the only way your hand order survives a rebuild, it is plain text you can read, and it is opt-in.

    **Answer:**

    > _(empty — fill in when decided)_

14. 💬 **OQ-PR14: May a cheap model rewrite the text of a piece of work in your document before it is built?**

    The idea is yours: text written without the prose guide should be marked and rewritten, and on 2026-11-18 you said the rewrite stays on, as a step on a fast cheap model. What agents chose: the mark is the attribute `prose="2"` in the hidden comment; a title must start with a verb and run at most 12 words, with at most 8 pass conditions; and the rewrite edits the item's text where it sits, in your document.

    **Who decided:** no ledger row exists for the mark or the limits. They arrived in code (`803d2a5e4`, `3122f481c`) and in the `tillr-prose-style` skill (`8343814ae`).

    **Live:** yes; `.tillr.toml` has it on. **Undo: small**, the limits and one settings line.

    - **Keep.** The limits and the in-place rewrite stay.
    - **Overturn.** The rewrite is kept in the store and shown on the item's page, and your document is left as written.
    - **Ask properly.** A question in the intake-paths design.

    <!-- vantage: question id=OQ-PR14 leaning="Keep the rewrite and the limits as engineering detail. Where the rewritten text lives follows your answer to OQ-QM1, so nothing more is needed here." -->

    _Leaning:_ Keep the rewrite and the limits as engineering detail. Where the rewritten text lives follows your answer to [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1), so nothing more is needed here.

    **Answer:**

    > _(empty — fill in when decided)_

15. 💬 **OQ-PR15: May copies of your tillr designs be delivered to every other workspace?**

    A tool copies each design that [the status index](./design-status.md) marks `shipped` into the tillr pack, which installs them in every jail (an agent's sandboxed workspace), with the hidden comments disabled in the copies. Agents in other projects read them to learn what tillr does.

    **Who decided:** nobody on record. It arrived as code on 2026-11-16 (`c24e60d36`) and has no ledger row.

    **Live:** yes. **Undo: small**, the tool, its test and the copies.

    - **Keep.** The designs go on being delivered as a dated snapshot.
    - **Overturn.** Only the programs are delivered.
    - **Ask properly.** A question beside [OQ-SH2](../design/tillr-selfhosted-build.md#OQ-SH2), which already asks about the pack's copy of the programs.

    <!-- vantage: question id=OQ-PR15 leaning="Keep: agents in other projects read them to learn what tillr does, the Product QA pipeline's among them, and the cost is a snapshot that lags." -->

    _Leaning:_ Keep: agents in other projects read them to learn what tillr does, the Product QA pipeline's among them, and the cost is a snapshot that lags.

    **Answer:**

    > _(empty — fill in when decided)_

16. 💬 **OQ-PR16: May the observatory change the queue, with no login?**

    The first design made the page read-only. Since the drag ordering and the idea box, it writes: the server accepts two kinds of change when it is started with `--write`, checks that the request came from its own page, and asks for no login.

    **Who decided:** `D-OA8` in [observatory actions](../design/observatory-actions.md), listed there as the designer's, "without the owner's ruling". It reverses `D-OBS2`, which was also an agent's. `D-OA7`, how the write is carried out, names no author.

    **Live:** yes. **Undo: small**, one flag.

    - **Keep.** Writes stay behind the flag.
    - **Overturn.** The page is read-only again, and ordering and ideas go through the command line.
    - **Ask properly.** A question in that design, including whether a login is wanted.

    <!-- vantage: question id=OQ-PR16 leaning="Keep: you asked for dragging, which needs a write, and the server listens on your own machine." -->

    _Leaning:_ Keep: you asked for dragging, which needs a write, and the server listens on your own machine.

    **Answer:**

    > _(empty — fill in when decided)_

17. 💬 **OQ-PR17: Which claims may an agent sign off for you, and for how long?**

    The design said you had deferred this on purpose: "a scope and an expiry written before any delegated verdict exists cannot be corrected from evidence". A bulk commit answered it anyway: an agent may sign off verification claims only, under a grant that lasts 24 hours.

    **Who decided:** the designer, "on recorded principle" (`b5e5073cb`, 2026-11-16), in [the agent runtime design](../design/agent-runtime.md) as [OQ-SO1](../design/agent-runtime.md#the-default-grant-until-the-owner-rules). Of the 31 bulk answers this is the one that overrode a decision of yours.

    **Live:** yes, in the grant code of `tools/tillr-item`. **Undo: small**, one scope and one duration.

    - **Keep.** Verification claims, 24 hours.
    - **Overturn.** No agent signs off anything until you set a scope.
    - **Ask properly.** It returns to its design as the open question it was, with the evidence gathered since.

    <!-- vantage: question id=OQ-PR17 leaning="Ask properly: you deferred it on purpose, and the answer in force is the narrowest one, so it is safe to leave running until you rule." -->

    _Leaning:_ Ask properly: you deferred it on purpose, and the answer in force is the narrowest one, so it is safe to leave running until you rule.

    **Answer:**

    > _(empty — fill in when decided)_

18. 💬 **OQ-PR18: Should an agent's run have no time limit by default?**

    A run used to be killed at a fixed wall time. Now it has none unless one is set, a run that committed is judged before any wall is looked at, and a walled run that had not committed is resumed once.

    **Who decided:** [`run-wall-ruling.md`](../design/run-wall-ruling.md), `D-RW1` to `D-RW3`, titled a "Design ruling" (`6ec85e450`, 2026-11-15, built `adc9b1269` nineteen minutes later). It cites six complaints of yours gathered in an evidence file and carries no note that you may overturn it.

    **Live:** yes. **Undo: small**, one default.

    - **Keep.** No default limit.
    - **Overturn.** A default limit returns; say how long.
    - **Ask properly.** A question in that document.

    <!-- vantage: question id=OQ-PR18 leaning="Keep: your later words say the same, that there are no timeouts and a killed run resumes uncounted. The word once in the resume rule is a count and goes under that ruling." -->

    _Leaning:_ Keep: your later words say the same, that there are no timeouts and a killed run resumes uncounted. The word once in the resume rule is a count and goes under that ruling.

    **Answer:**

    > _(empty — fill in when decided)_

19. 💬 **OQ-PR19: When is a check declared stuck, and what happens the second time?**

    You ruled that a time budget is a target and only a stall ends a check: "target budgets, watch for stalls, but don't let 'load is high' cause failures". The designer then chose what a stall is: 60 one-second samples in a row with no new output and no processor time used. A stalled gate is run once more; a second stall holds the item for you.

    **Who decided:** `D-GR16` in [the gate rework](../design/gate-rework.md): "Settled by the session's designer ... the owner may overturn it" (`5e367ffde`, 2026-11-17). The numbers and the count are not in your words.

    **Live:** yes (`724cb5853`, `6a75bf6dc`). **Undo: small**, the loop's stall handling.

    - **Keep.** Sixty seconds, one re-run, then you.
    - **Overturn the count only.** Sixty seconds stays; a stalled check is restarted uncounted and its cause is diagnosed, and it reaches you only through an objection that names you.
    - **Ask properly.** A question in the gate design.

    <!-- vantage: question id=OQ-PR19 leaning="Overturn the count only: your ruling that there are no attempt counts already decides it, and the sixty-second definition is engineering detail." -->

    _Leaning:_ Overturn the count only: your ruling that there are no attempt counts already decides it, and the sixty-second definition is engineering detail.

    **Answer:**

    > _(empty — fill in when decided)_

20. 💬 **OQ-PR20: Should landings stop when the trunk has been failing its checks for too long?**

    When the full checks fail on the trunk, landings continue until a cap, then stop while repairs still land. The cap was set at 20 landings or 60 minutes; the settings file today has 20 landings and the minutes off.

    **Who decided:** [OQ-GR1](../design/gate-rework.md#decision-ledger), settled "at the owner's request of 2026-11-15 ('fill them in through principles')" (`26bccb5e6`). The graph design records, as a rule it is held to, that you do not accept this cap, without quoting you. On 2026-11-18 you asked "why does a red check hold a queue?", and a proposal that keeps the cap as the one thing that holds the queue is not ruled.

    **Live:** yes (`c3a3a35a9`). **Undo: small**, one settings key.

    - **Keep.** Twenty landings.
    - **Overturn.** No cap; a failing trunk never stops landings, and the repair runs alongside.
    - **Ask properly.** Decided in the gate conversation you said you would continue.

    <!-- vantage: question id=OQ-PR20 leaning="Overturn to no cap, if the graph design's record of your view is right; it is an agent's paraphrase, so this one needs your own words either way." -->

    _Leaning:_ Overturn to no cap, if the graph design's record of your view is right; it is an agent's paraphrase, so this one needs your own words either way.

    **Answer:**

    > _(empty — fill in when decided)_

21. 💬 **OQ-PR21: May a design be built from while it holds decisions an agent made that you have not seen?**

    Every decision above was built that way. A design agent writes ten decisions into a design's ledger, marks them as its own, and a build starts the same hour. You meet them later, running.

    - **A — No, for anything you would care about.** A decision that touches your documents, your repository, your money, your models or what you see is put to you as a question, and the design cannot be built from until you answer. Engineering detail is labeled as the designer's and may be built.
    - **B — No, for everything.** No design is built from until you have ticked every designer's decision in it.
    - **C — Yes, as today.** Decisions are labeled and you may overturn them later.

    <!-- vantage: question id=OQ-PR21 leaning="A: it matches what you said on 2026-11-18, that you decide product direction and trade-offs and the agent decides engineering detail. B would stop the queue on detail you do not want to read." -->

    _Leaning:_ A: it matches what you said on 2026-11-18, that you decide product direction and trade-offs and the agent decides engineering detail. B would stop the queue on detail you do not want to read.

    **Answer:**

    > _(empty — fill in when decided)_

22. 💬 **OQ-PR22: Does "fill them in through principles" still stand?**

    That sentence of 2026-11-15 is what the three bulk commits cite. It was read as leave for an agent to close a question addressed to you whenever it could name something it called a principle.

    - **A — Narrowed.** An agent may close a question of yours only by quoting a dated ruling of yours that answers it in so many words. The row carries the quote. Anything else stays open.
    - **B — Withdrawn.** No agent closes a question addressed to you. It may attach the ruling it thinks applies, and you still answer.
    - **C — As it was.**

    <!-- vantage: question id=OQ-PR22 leaning="A: it keeps what you wanted, not being asked the same thing twice, and removes the designer's own leaning as a principle." -->

    _Leaning:_ A: it keeps what you wanted, not being asked the same thing twice, and removes the designer's own leaning as a principle.

    **Answer:**

    > _(empty — fill in when decided)_

## 3. Corrections that need no ruling

These rows are false or mislabeled now. None is applied by this document, because other writers have those documents open; each is for whoever next edits the file.

**Rows that still state a default the owner later reversed:**

- [ ] [Gate rework](../design/gate-rework.md), [OQ-GR2](../design/gate-rework.md#decision-ledger): says the default is to start a new worktree from the last passing commit, and notes only that this repository sets `tip`. Should say the owner overturned the default on 2026-11-17 (`1fa2c069c`) because it handed workers a tree older than their own work's definition.
- [ ] [Convergence](../design/tillr-convergence.md), [OQ-TC3](../design/tillr-convergence.md#decision-ledger) and the section it settled: still say a held item is started again when an input changes. Should say superseded by the owner's ruling of 2026-11-17 (`D-JL10`); the code was removed at `f8774bd63`.
- [ ] [Queue persistence](../design/tillr-queue-persistence.md), [OQ-QP4](../design/tillr-queue-persistence.md#91-decision-ledger): ends "the trace is on". Should say the owner removed the git trace on 2026-11-17 (`d09016bba`).
- [ ] [Scheduled tasks](../design/scheduled-tasks.md), [OQ-ST2](../design/scheduled-tasks.md#12-open-questions): rests on the re-run rule of [OQ-TC3](../design/tillr-convergence.md#decision-ledger). Should drop "re-dispatched once on a changed input".
- [ ] [Failure diagnosed, not waited out](../design/failure-diagnosed-not-waited-out.md), [OQ-1](../design/failure-diagnosed-not-waited-out.md#open-questions): one automatic retry. Should say superseded by `D-JL10`.
- [ ] [Run wall ruling](../design/run-wall-ruling.md), `D-RW3`: "resumed once". Should say resumed, uncounted.
- [ ] [Item graphs](../design/tillr-item-graphs.md), `D-IG4`: depth and node caps. Should say the caps were dropped.
- [ ] [Queue persistence](../design/tillr-queue-persistence.md), `D-QP5`: says parking a design with no item creates nothing. Should cite the owner's words of 2026-11-18 that Icebox items are loaded and marked blocked.

**Rows and headers that claim a ruling nobody recorded:**

- [ ] [The queue-persistence "ruling"](../design/queue-persistence-ruling.md): header says "Ruled" and "Needs your ruling: None". `D-RS1` to `D-RS5` should each read as a designer's decision the owner may overturn, and the header should name [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) and [OQ-PR1](#OQ-PR1).
- [ ] The same document, `D-RS3`: cites "the owner's no-tillr-in-commits rule". Should say the rule is unquoted and unconfirmed until he answers [OQ-PR1](#OQ-PR1).
- [ ] [Run wall ruling](../design/run-wall-ruling.md): titled "Design ruling" with no note. `D-RW1` to `D-RW3` should read as designer's decisions.
- [ ] [Board utilities](../design/board-utilities.md), `D-UT16` and `D-UT17`: "Design ruling, 2026-11-12". Should read as designer's decisions. [The judge-in-tillr document](../design/work-control-judge-in-tillr.md) should say again that the owner has not accepted the assessment its build rests on.
- [ ] [Build commit](../design/tillr-build-commit.md): "Needs your ruling: None" over a decision an agent chose. Should name the decision as the designer's.
- [ ] [Observatory design](../design/observatory-design.md), [OQ-OBD6](../design/observatory-design.md#11-decision-ledger): written as ruled at `7c9abc080`, equal to its leaning, with no words of the owner's, after `17f6fa0ca` had withdrawn an earlier early compaction for that reason. Should read as a designer's decision.
- [ ] [Agent runtime](../design/agent-runtime.md), [OQ-SO1](../design/agent-runtime.md#the-default-grant-until-the-owner-rules): marked answered beside text that says the owner deferred it. Should read as a designer's default in force, open for the owner.
- [ ] [Judge and lifecycle](../design/tillr-judge-and-lifecycle.md), `D-JL11`: the quoted principle is the owner's; the six kinds of obstacle and their routes are the coordinator's. Should say which is which.
- [ ] [The tillr roadmap](./roadmap.md), the paragraph under "Rulings that are waiting on the owner": says 23 settled questions each say so in their design. Several do not, and the count of questions left is stale.

**Recorded rulings that contradict each other:**

- [ ] [Gate rework](../design/gate-rework.md), [OQ-GR1](../design/gate-rework.md#decision-ledger) sets a cap on landings while the trunk fails; [the graph design](../design/tillr-graph-is-the-path.md) lists as a rule that the owner does not accept one. One of them is wrong; [OQ-PR20](#OQ-PR20) decides which.
- [ ] The graph design's rule that the git trace is ruled out carries no quote and reverses [OQ-QP4](../design/tillr-queue-persistence.md#91-decision-ledger). Should cite `d09016bba`.
- [ ] [Item graphs](../design/tillr-item-graphs.md), `D-IG11`: the owner's words of 2026-11-15 that a sub-agent is never a node. His words of 2026-11-17 hoist sub-agents into nodes. The row should say it is overturned in part.
- [ ] [Observatory design](../design/observatory-design.md), `D-OBS2`: "the board writes nothing". Should point at `D-OA8`, which reversed it.

**Ids and superseded rows:**

- [ ] [Muse backend](../design/tillr-muse-backend.md): the id `D-MM11` is used for two different rows. The second, the failover order, should be `D-MM12`.
- [ ] `D-RS` is a prefix in both [the queue-persistence "ruling"](../design/queue-persistence-ruling.md) and [the restart design](../design/tillr-restart.md), for unrelated decisions. One should be renamed.
- [ ] [Queue persistence](../design/tillr-queue-persistence.md), `D-QP6`: migrate by attrition. Should say superseded by [OQ-TC1](../design/tillr-convergence.md#decision-ledger).

## 4. Not yet built, high significance

Nothing in this table is built. Each of these designs is still waiting on questions of the owner's, so he can weigh these rows when he reads the design. All are an agent's. Other writers revised five of these designs on 2026-11-18 while this review was written; the rows are as the designs read at `f6b71beb8`. One row the audits listed is gone: the vocabulary's rule that an item ends only once it is proven is now asked of the owner as [OQ-VO6](../design/tillr-vocabulary.md#OQ-VO6).

| Id | Decision | Design |
| :--- | :--- | :--- |
| `D-GP5` | All routing is one function that returns what to add to an item's graph and what it waits on | [Graph is the path](../design/tillr-graph-is-the-path.md) |
| `D-GP6` | Seats are declared in settings; no ladder of models; no model named on a step (the seat names are live; the rest is not) | same |
| `D-GP7` | The gate, the judge and the landing become steps in each item's graph | same |
| `D-GP9` | Design answers and triage may change an item's pass conditions and allowed paths; rewrites and work may not | same |
| `D-GP11` | A repeated failure is judged against the whole chain, with no count; a chain whose every ending is new has no limit | same |
| `D-GP13` | A planning agent owns the queue's order and the links between items | same |
| `D-GP14` | Only a finished proof marks the trunk as failing; this replaces the cap in [OQ-PR20](#OQ-PR20) | same |
| `D-EL1` | A design change lands on the trunk ahead of its item, through the ordinary gate | [Early design landing](../design/tillr-early-design-landing.md) |
| `D-EL4` | An item's marker may land early inside such a document | same |
| `D-EL6` | The owner's answer reaches the waiting work as a fact once it is committed on the trunk; tillr writes no answer there itself | same |
| `D-VO1` | An item exists without any marker in a document | [Vocabulary](../design/tillr-vocabulary.md) |
| `D-VO3` | "Run" means one step's execution; the whole try is the "path" | same |
| `D-VO8` | "Stage" and "step" are fixed words; five others are never used bare | same |
| `D-LC1` | A worker ends by calling one tool, `land`, with a summary and nothing else | [Landing call](../design/tillr-landing-call.md) |
| `D-LC5` | That call stays open until the full checks have passed on the change; a revert is its answer | same |
| `D-TS2` | One writer process holds the store behind a lock; every program writes through it | [Storage](../design/tillr-storage.md) |
| `D-TS8` | Storage is built in stages, ahead of its place in the graph design's order | same |
| `D1` | Four rungs decide a conflict: implementer, plan owner, designer, the owner | [Escalation ladder](../design/escalation-ladder.md) |
| `D2` | The plan owner is a cheap seat distinct from the designer | same |
| `D8` | A list of things a worker may never edit to make its change fit | same |
| [OQ-6](../design/escalation-ladder.md#open-questions) | The designer's decisions are reviewed in batches and the owner's silence ratifies them | same |
| `D-GM6` | Two keys guard the monitor's workflow, one of them the owner's approval | [Goal monitor](../design/tillr-goal-monitor.md) (iceboxed by the owner, 2026-11-17) |
| `D-GM8` | The monitor cannot repair itself; the owner reading the board is its outside authority | same |

The escalation ladder's last row is the pattern this review is about, written down as policy. [OQ-PR21](#OQ-PR21) and [OQ-PR22](#OQ-PR22) decide it before that design is built.

## 5. Medium and low significance

### 5.1. Medium, live or partly live

All are an agent's unless the row says nobody is named. Rows already covered by a question in [Section 2](#2-questions-for-the-owner) are left out.

| Id | Design | Decision | Live? |
| :--- | :--- | :--- | :--- |
| `D-GP1`, `D-GP10`, `D-GP12` | [Graph is the path](../design/tillr-graph-is-the-path.md) | One graph per item for life; an objection is the one ending that changes who acts; a visible sub-agent is a step inside its parent | Partly |
| `D-JL1` | [Judge and lifecycle](../design/tillr-judge-and-lifecycle.md) | The judge runs before the gate for items that opt in | Partly |
| `D-JL3`, `D-JL4`, `D-JL5`, `D-JL7` | same | The stored "in progress" state is retired; moves between states are checked; a failed gate writes no verdict; the AutoQA actor names are removed | Yes |
| `D-JL11` | same | A worker ends with a commit or one of six named obstacles, each with a route (the principle is the owner's, quoted; the six and their routes are the coordinator's) | Yes |
| `D-GR9`, `D-GR10`, `D-GR11`, `D-GR12` | [Gate rework](../design/gate-rework.md) | A check that cannot run on this machine is "unavailable"; a check is one declaration file; a budget test; upstream drift files an item and holds nothing | Yes |
| `D-PT1`, `D-PT2`, `D-PT4` | [Planning and model tiering](../design/tillr-planning-node-model-tiering.md) | Each kind of work is a seat and the list is open; a plan lists its pieces; the planner is the first step of a planned build | Yes |
| `D-IG2`, `D-IG3`, `D-IG7` | [Item graphs](../design/tillr-item-graphs.md) | The outside sees one item; a step is fixed once recorded; a step writes only inside the item's allowed paths | Yes |
| `D-MM1`, `D-MM2`, `D-MM11` (first use) | [Muse backend](../design/tillr-muse-backend.md) | A backend's contract is where it may write, held by a sandbox and a snapshot; a worktree run commits its own work (the first two name nobody) | Yes |
| `D-CB1`, `D-CB5` | [Claude backend](../design/tillr-claude-backend.md) | The Claude program is started with only tillr's own tool servers; its cost is recorded as its own estimate | Yes |
| [OQ-IP1](../design/tillr-idea-pipeline.md#61-settled-decisions), `D-IP5` | [Idea pipeline](../design/tillr-idea-pipeline.md) | Scouting and designing one idea are steps of one item; pending answers travel by `tillr item answer` | Yes, partly |
| `D-TC4` | [Convergence](../design/tillr-convergence.md) | One command says why each item is not moving | Partly |
| Two working choices | [Core and git landing](../design/tillr-core-and-git-landing.md) | The words "definition" and "git-landing" are in file names and flags; a store records which definition it runs | Yes |
| Five changes "now"; fourteen answers to Product QA | [Separable parts](../design/tillr-separable-parts.md) | Small changes built for the Product QA project, each mapped to a build item | Partly |
| Reworded pass conditions | [Definitions plan](../design/tillr-definitions-plan.md) | Three pass conditions of one item were reworded by a design agent after stopped runs | Done |
| [OQ-CR1](../design/criterion-repair.md#decision-ledger) | [Criterion repair](../design/criterion-repair.md) | A repair replaces the whole pass condition with a new record and never edits it (nobody named) | Partly |
| `D3`, `D5`, `D6`, `D10` | [Escalation ladder](../design/escalation-ladder.md) | A conflict is a record; answers are typed; a worker's unfinished change is kept; the implementer records and routes | Partly, in a different shape |
| `D-GM11` | [Goal monitor](../design/tillr-goal-monitor.md) | Work that is not yet due is a wait of up to five minutes, not a stop | Partly |
| `D-AC1` | [Agent context delivery](../design/agent-context-delivery.md) | Where each kind of rule for an agent lives | Partly |
| Principles 11 and 13 | [Landing principles](../design/landing-principles.md) | A failure is diagnosed, not waited out; a held state names someone who can still act (both later matched by the owner's rulings) | Partly |
| `D-UT17` | [Board utilities](../design/board-utilities.md) | A worktree's lease has 300 seconds of grace; a correction reuses the worktree | Yes |
| Roadmap answer, `440c31bbe` | [tillr roadmap](./roadmap.md) | A landing never deletes files it does not track and pauses when it predicts a conflict | Partly |
| Roadmap answer, `9397f0471` | same | A workflow step with no write scope inherits the item's allowed paths | Yes |
| Roadmap answer, `db8cdef66` | same | A deleted question of the owner's is a repair for the operator, not something waiting on the owner | Partly |
| Request, `5b2a3e050` | [Usage recording request](../pi/usage-recording-request.md) | A provider's usage record that holds an identifier is withheld whole, losing that billing detail | Not verified |
| No row | Loop behavior | The loop writes `OQ-OC` questions into the owner's documents, with a marker comment and ids made from the run's id (`cf84f0c6a`); the concept is the owner's | Yes |
| No row | Loop behavior | The loop creates git refs under `refs/tillr/` for runs, landings, the passing commit, trunk history and recovery | Yes |
| No row | Loop behavior | The wording of the commits the loop writes | Yes |

About 95 further answers by a design agent to blocked build runs sit in roadmap entries, each committed with a subject beginning "rule that". The audit graded them and found almost all to be engineering detail; they are counted here as one row and were not re-read one by one.

### 5.2. Medium, not built

| Id | Design | Decision |
| :--- | :--- | :--- |
| `D-GP8`, `D-GP15` | [Graph is the path](../design/tillr-graph-is-the-path.md) | Every step starts on the latest trunk; migration records each act before routing on it |
| `D-QP4` | [Queue persistence](../design/tillr-queue-persistence.md) | A badge and a lock bound work that is in the store and not yet in a document |
| [OQ-GR3](../design/gate-rework.md#decision-ledger) | [Gate rework](../design/gate-rework.md) | A change to the gate's own runner lands only after the runner's tests pass |
| `D-GM1`, `D-GM3`, `D-GM4`, `D-GM7` | [Goal monitor](../design/tillr-goal-monitor.md) | The monitor is a recurring item with a fixed workflow, a schedule, and a fence against changing itself |
| `D-EL3`, `D-EL5`, `D-EL7` | [Early design landing](../design/tillr-early-design-landing.md) | An early landing holds whole documents only, passes the same gate without a judge, and does not make the worker wait |
| `D-IP1`, `D-IP2`, `D-IP6` | [Idea pipeline](../design/tillr-idea-pipeline.md) | Items gain a kind; links gain a kind; child items are applied only by the loop after a landing |
| `D-VO2`, `D-VO6` | [Vocabulary](../design/tillr-vocabulary.md) | What acts on another item's work is a step, not an item; the limit on running agents is its own setting |
| `D-LC3`, `D-LC4`, `D-LC7`, `D-LC8`, `D-LC9` | [Landing call](../design/tillr-landing-call.md) | Four possible answers to a landing call; a second tool for obstacles; three patches to the pi fork |
| `D-AQ1`, `D-AQ2`, `D-AQ5`, `D-AQ6`, `D-AQ7`, [OQ-AQ1](../design/tillr-autoqa.md#decision-ledger), [OQ-AQ3](../design/tillr-autoqa.md#decision-ledger) | [AutoQA](../design/tillr-autoqa.md) (iceboxed by the owner, 2026-11-17) | A separate program on its own store; four checks before a bug is filed; campaigns bounded by default; starts in this repository |
| `D-TS1`, `D-TS3`, `D-TS7`, `D-TS9` | [Storage](../design/tillr-storage.md) | A narrow write interface; no flush on the write path; after a crash the store is cut back to its first gap; database servers are rejected |
| `D-TC5` | [Convergence](../design/tillr-convergence.md) | Derived copies are regenerated between commit and gate |
| `D-SH5` | [Self-hosted build](../design/tillr-selfhosted-build.md) | Which ten observatory items are built first (not checked) |
| `D4`, `D9`, `D11`, [OQ-1](../design/escalation-ladder.md#open-questions), [OQ-4](../design/escalation-ladder.md#open-questions), [OQ-5](../design/escalation-ladder.md#open-questions) | [Escalation ladder](../design/escalation-ladder.md) | Routing as an overlay on states; a policy ledger under `docs/policy/`; the designer recommends and the owner ratifies |
| `D-1`, `D-5`, `D-7`, `D-10`, `D-12`, [OQ-ST1](../design/scheduled-tasks.md#12-open-questions) | [Scheduled tasks](../design/scheduled-tasks.md) | Due times and repeat intervals with no scheduler; only the owner and the lead agent may set them; the first user is a weekly update of the forks |
| [OQ-2](../design/failure-diagnosed-not-waited-out.md#open-questions) | [Failure diagnosed, not waited out](../design/failure-diagnosed-not-waited-out.md) | The loop starts a diagnosis on the design seat, the expensive one, and reports its cost |
| `D-AC3`, `D-AC4` | [Agent context delivery](../design/agent-context-delivery.md) | The briefing has no per-mode scope; the fleet stays on the patched pi |
| [OQ-SB1](../design/seat-profiles.md#8-open-questions), [OQ-SB2](../design/seat-profiles.md#8-open-questions), [OQ-SB6](../design/seat-profiles.md#8-open-questions) | [Seat profiles](../design/seat-profiles.md) | Limits fixed for each seat; shell access removed from two seats; output without its evidence is refused. Reclassified on 2026-10-20 as decided by the agent (`d432428b8`); the first was never ruled |
| `D-AL1`, `D-AL9` | [Autonomous landing](../design/autonomous-landing.md) (superseded) | The unattended guarantee; a failing trunk stops the whole loop |

### 5.3. Low

About 100 further rows are rated low: engineering detail with nothing the owner sees. The count for each document, an agent's in every case: agent context delivery 10, drain successor 10 (covered by [OQ-PR4](#OQ-PR4)), scheduled tasks 9, autonomous landing 8, escalation ladder 7, goal monitor 6, self-hosted build 5, gate rework 4, observatory design 4, definitions plan 4, AutoQA 3, designer sessions 3, graph is the path 3, item graphs 3, landing call 3, storage 3, queue persistence 2, observatory actions 2, idea pipeline 2, Muse backend 2, planning and model tiering 2, restart 2, seat profiles 2, and one each in the Claude backend, early design landing, observatory redesign, the queue-persistence "ruling", separable parts and vocabulary. The bulk commits add a handful in the merge-queue retirement and observatory documents.

## 6. How this stops happening

Three of these are the designer's decisions for this document, labeled as such; the owner may overturn any of them. Two are real choices and are questions.

**D-PR1, designer's decision: only the owner's quoted, dated words are a ruling.** A ledger row may say "ruled" or name the owner only when it carries his words in quotation marks and the date. A paraphrase is recorded as a paraphrase, with who wrote it.

**D-PR2, designer's decision: a designer's decision always says so, in the row.** The row begins "Designer's decision; the owner may overturn this", as [the queue-mirror design](../design/tillr-queue-mirrors-designs.md)'s ledger already does. A document that holds one never says "Needs your ruling: None" without also naming how many designer's decisions it holds, and its title never calls itself a ruling.

**D-PR3, designer's decision: a check enforces the first two.** It joins the gate's document checks and refuses, on lines a change adds:

- a ledger row that says "ruled" or "owner" with no quotation and no date;
- a header that says "Needs your ruling: None" in a document whose ledger holds a designer's decision;
- a question closed in a change whose row cites no words of the owner's.

What would have to be built: that check and its test; a pass over the 39 audited documents to relabel rows by the answers above; and, if [OQ-PR21](#OQ-PR21) is answered A or B, a rule in the loop that a design holding an unseen designer's decision of the named kinds is not yet ready to build from. The existing rows are a backlog, so the check judges added lines only, as the gate's other document checks do.

**The two questions.** [OQ-PR21](#OQ-PR21) asks whether a design may be built from while it holds designer's decisions the owner has not seen. [OQ-PR22](#OQ-PR22) asks whether "fill them in through principles" still stands.

**What the audits did not settle.** They disagree on three ratings: `D-QP9` and `D-RS5` are high in one and low or medium in another, and `D-QP10` is high in one and medium in another; this page treats each at the higher rating. They did not verify that rows recorded as the owner's are his: most are paraphrase. They did not open the sources of landing principles 1 to 10, the earlier ruling behind `D-QP5`, or the roughly 95 roadmap answers one by one.

## Decision Ledger

Every row is a designer's decision made in writing this review, and the owner may overturn it. None is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-PR1 | **Designer's decision; the owner may overturn this.** Only the owner's quoted, dated words are recorded as a ruling. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | — |
| D-PR2 | **Designer's decision; the owner may overturn this.** A designer's decision says so in its row, and its document never claims that nothing needs a ruling. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | — |
| D-PR3 | **Designer's decision; the owner may overturn this.** A gate check refuses an unquoted "ruled" row and a false "Needs your ruling: None", on added lines only. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | — |
| D-PR4 | **Designer's decision; the owner may overturn this.** Where two audits rate a decision differently, this review uses the higher rating. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | — |
