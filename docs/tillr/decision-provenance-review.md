---
title: "Who made the decisions recorded in the tillr designs, and which of them the owner should now rule on"
status: in-review
stage: DESIGN
updated: 2026-11-20
tags: [tillr, review, provenance, decisions, design]
summary: "A one-time review for the owner of every decision of consequence in the tillr designs that carries no words of his. Two rulings recorded on 2026-11-19 (OQ-PR21 and OQ-PR22) settle how this stops happening: a decision that touches his documents, his repository, his money, his models or what he sees is put to him as a question before anything is built on that design, while engineering detail is marked as the agent's and may be built on; and an agent closes a question addressed to him only by quoting a dated ruling of his that answers it in so many words. Ten of the twenty built-decision questions are now settled in the ledger (OQ-PR1 to OQ-PR5 and OQ-PR12 to OQ-PR14 ruled on 2026-11-20, OQ-PR17 ruled that night, and OQ-PR20 closed on his 2026-11-18 ruling of OQ-GP7); ten remain open (OQ-PR6 to OQ-PR11, OQ-PR15, OQ-PR16, OQ-PR18 and OQ-PR19). The rest is lists: rows that were false or mislabeled, high-significance decisions in designs that are not built, and the medium and low ones."
next: "The owner answers the ten remaining open questions (OQ-PR6 to OQ-PR11, OQ-PR15, OQ-PR16, OQ-PR18 and OQ-PR19). Ten built-decision questions are settled in the ledger (OQ-PR1 to OQ-PR5 and OQ-PR12 to OQ-PR14 ruled on 2026-11-20; OQ-PR17 ruled that night; OQ-PR20 closed on the 2026-11-18 ruling of OQ-GP7), OQ-PR21 and OQ-PR22 were ruled on 2026-11-19 (nothing built yet), and the corrections in Section 3 were applied on 2026-11-18."
depends-on:
  - ../design/tillr-queue-mirrors-designs.md#OQ-QM1
---

# Who made the decisions recorded in the tillr designs, and which of them the owner should now rule on

**Status:** 2026-11-20. Awaiting the owner's review of ten remaining open questions ([OQ-PR6](#OQ-PR6) to [OQ-PR11](#OQ-PR11), [OQ-PR15](#OQ-PR15), [OQ-PR16](#OQ-PR16), [OQ-PR18](#OQ-PR18) and [OQ-PR19](#OQ-PR19)). He ruled [OQ-PR21](#OQ-PR21) and [OQ-PR22](#OQ-PR22) on 2026-11-19, ruled [OQ-PR1](#OQ-PR1) to [OQ-PR5](#OQ-PR5) and [OQ-PR12](#OQ-PR12) to [OQ-PR14](#OQ-PR14) on 2026-11-20, ruled [OQ-PR17](#OQ-PR17) that night, and [OQ-PR20](#OQ-PR20) is closed on his 2026-11-18 ruling of [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7); code, configuration and corpus changes owed by those rulings are not built yet. Nothing here changes any behavior until it is built; no other document is edited by this one. The evidence is four read-only audits made on 2026-11-18 of 39 design documents and of the commits named below, each commit checked to be on the trunk.

> **In short.** Of 380 recorded decisions in the tillr designs, 303 carry no words of the owner's, 64 of those are of high significance, and more than half of the high ones are built and running. He delegated a method for answering questions and received answers; this page puts the answers that matter in front of him once.

**Why it matters.** On 2026-11-18 the owner found that tillr reads a hidden comment of its own from his documents, a product decision he never made, and asked whether other decisions had come in the same way. They had.

**The shape.** [Ten open questions](#2-questions-for-the-owner) on built decisions, hardest to undo first, alongside ten settled in [the ledger](#decision-ledger); [the rule from here on](#6-how-this-stops-happening), settled by the owner in [OQ-PR21](#OQ-PR21) and [OQ-PR22](#OQ-PR22); then lists that need no ruling.

**Cost.** Reading time. An answer that leaves things as they run today costs nothing more. An answer that changes them becomes work, sized for each question in [Section 1.5](#15-evidence-behind-each-question).

**Start at:** [OQ-PR6](#OQ-PR6).

**Needs your ruling:** [OQ-PR6](#OQ-PR6), [OQ-PR7](#OQ-PR7), [OQ-PR8](#OQ-PR8), [OQ-PR9](#OQ-PR9), [OQ-PR10](#OQ-PR10), [OQ-PR11](#OQ-PR11), [OQ-PR15](#OQ-PR15), [OQ-PR16](#OQ-PR16), [OQ-PR18](#OQ-PR18), [OQ-PR19](#OQ-PR19) (plus the designer's decisions `D-PR1` to `D-PR4` in [the ledger](#decision-ledger), which the owner may overturn).

**Reads with:** [the queue mirrors the roadmap's designs](../design/tillr-queue-mirrors-designs.md) (whose [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) the owner ruled on 2026-11-18 around the companion implementation plan; this page does not ask that again).

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

The method produced answers the owner did not want. He has since reversed three in code and rejected a fourth and a fifth by a later ruling: where a new worktree starts from ([OQ-GR2](../design/gate-rework.md#decision-ledger)), the git trace ([OQ-QP4](../reference/tillr-queue-persistence.md#why-its-this-way)), automatic re-running when an input changes ([OQ-TC3](../design/tillr-convergence.md#decision-ledger)), one automatic rework ([OQ-JL3](../design/tillr-judge-and-lifecycle.md#decision-ledger)) and one automatic retry ([OQ-1](../design/failure-diagnosed-not-waited-out.md#open-questions)). One answer overrode a deferral he had made on purpose ([OQ-PR17](#OQ-PR17)).

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

Each open question in [Section 2](#2-questions-for-the-owner) states its situation and its choices only. Who made the earlier decision, whether it is running today and what undoing it costs are kept here for both open and settled questions.

| Question | Who decided, and when | Running today | Cost to undo |
| --- | --- | --- | --- |
| [OQ-PR1](#OQ-PR1) | A design agent, 2026-11-15, as `D-RS1`, `D-RS3` and `D-RS5` of [the "ruling"](../design/queue-persistence-ruling.md), under "Needs your ruling: None". Its stated reason was "the owner's no-tillr-in-commits rule", cited and not quoted; the audits did not find those words of the owner's in the tree. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): commits say what was built in lasting words and carry no reference to tillr, tillr items or other ephemeral tillr state. | Yes, since `cd68c87e3` and `1cdb67809`. Commits omit item IDs and trailers; after each landing the loop also makes a second commit, `chore: retire landed document block`, deleting its hidden comment and the step's pass conditions from the design (33 times so far), which ends with the hidden comment under [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1). | None on commit trailers (the ban stands); the post-landing block-deletion commit ends under [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1). |
| [OQ-PR2](#OQ-PR2) | Three agents. `D-UT16` in [board utilities](../design/board-utilities.md) is a "Design ruling, 2026-11-12" (`dcc4eef9f`); the same commit deleted the line "Owner accepts or rejects the MIGRATE ruling" and put a build instruction in its place, and no acceptance by the owner is recorded. [OQ-JL1](../design/tillr-judge-and-lifecycle.md#decision-ledger) was settled on 2026-11-15 "on recorded principle" (`aa13e9b16`), the principle being a sentence in an agent-written design and the owner's undated preference for token efficiency. `D-JL2`, the classes that pass conditions carry (a mechanical check can never retire one classed as the owner's alone), comes from the unruled drafts of 2026-11-14. The reviewing agent is called the judge there. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): option A, every code change reviewed by a fresh agent before landing ([OQ-JL1](../design/tillr-judge-and-lifecycle.md#decision-ledger) overturned for code landings). | Mechanical-by-default is still running (`bb6e286b5` and the verdict rules in `tools/tillr-item`); mandatory code review is designed in [diff review](../design/tillr-diff-review.md) and not built yet. | Large: building the post-gate review step ([diff review](../design/tillr-diff-review.md)) and one review run per code landing in spend. |
| [OQ-PR3](#OQ-PR3) | `D-IG1`, `D-IG5` and `D-IG9` in [item graphs](../reference/tillr-item-graphs.md#why-its-this-way) come from the unruled drafts of 2026-11-14 (`5e2e61040`). `D-IG13`, the file layout and the seat on each step, was "Settled on 2026-11-16 by the design seat while cutting the build order; the owner may overturn it". The step files are called workflows; an earlier design refused workflows built from workflows, and this one removed the refusal. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): option A, kept. | Yes (`b657ed406`, `064bf3e8a`). | Nothing to undo; the workflow files and expander stay. |
| [OQ-PR4](#OQ-PR4) | An agent: all sixteen rows of [the drain successor reference](../reference/tillr-drain-successor.md#why-its-this-way), `D-DS1` to `D-DS16`, 2026-11-14 (`50216fff6`). The graduated design was tagged as a design ruling and recorded none. Everything the loop does hangs on this step. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): option A, left to the engineers as engineering detail. | Partly (`a792a9df1`). | Nothing to undo; the graph design amends it when built. |
| [OQ-PR5](#OQ-PR5) | Agents. `D-OBS1` to `D-OBS13` in [the first observatory reference](../reference/observatory-design.md#why-its-this-way) (`c6c74c048`, 2026-11-13, "settled where it is technical"), and `D-OR1` to `D-OR6` in [the observatory reference](../reference/observatory-redesign.md#why-its-this-way) (`7ab8fe577`, 2026-11-15). [OQ-OR2](../reference/observatory-redesign.md#why-its-this-way) and [OQ-OR3](../reference/observatory-redesign.md#why-its-this-way) were recorded in the graduated design as "Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ('fill them in through principles')... The owner may overturn it"; the principle is the owner's instruction that everything be linked, in paraphrase. [OQ-OBD6](../reference/observatory-design.md#why-its-this-way) is recorded as ruled with no words of the owner's. The web page is called the observatory. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): option A, kept as the base. | Yes for the pages and Home (`98ecde6a6`, `34fd911b8`); the audits disagree on whether the stepping keys were confirmed in the source. | Nothing to undo; changes continue to be requested directly. |
| [OQ-PR6](#OQ-PR6) | [OQ-TC2](../design/tillr-convergence.md#decision-ledger) by the designer "on recorded principle" (`26bccb5e6`, 2026-11-15). The settings name ten seats; the six newest (plan on Sol; edit, test-writing and docs on Terra; test-running on Luna; verify on Sol) were "settled by the design seat on 2026-11-18" (`73fd201d2`), and only Luna for scouts cites an instruction of the owner's, unquoted. The owner's recorded words cover part of it: Terra for implementation, Luna for scouts, Sol 6.2 "where appropriate", design seats on Fable, and more seat names with cheaper models where possible. | Yes, in the four `config/tillr/backend-policy*.json` files. The settings name the seat and the program that runs it; the model comes from that program's profile. | Moderate: those files and any workflow that names a seat. A seat-by-seat question would belong in [convergence](../design/tillr-convergence.md). |
| [OQ-PR7](#OQ-PR7) | Agents, three times. `D-MM11` in [the Muse backend reference](../reference/tillr-muse-backend.md#why-its-this-way) (`61b030959`, 2026-11-12): Ember first, DeepSeek behind it, one move allowed, judged work never moved; the owner's wish that Ember be used while it lasts is direction, the rule is the designer's. `D-CB3` and `D-CB4` in [the Claude backend](../design/tillr-claude-backend.md) (`600469a60`, built `6f9c6a971`): a refused Claude subscription is treated as a machine fault, so that work moves to Bedrock. Two roadmap answers by the design seat on 2026-11-17 (`72a077780`, `9f14ee05f`) continue a quota-refused run on the next provider and accept overshooting the reserve. | Yes. A seat lists its providers in order, and a refusal for quota or an outage moves the work in the same pass. | Moderate: the settings format and the loop's planner. |
| [OQ-PR8](#OQ-PR8) | Agents. The hold was built on 2026-11-13 (`bffbbbf19`); `D-IP4` in [the idea pipeline](../design/tillr-idea-pipeline.md) is "Carbon's answer" (`074e17d7f`); `D-TC3` in [convergence](../design/tillr-convergence.md) names the class. The owner's principle is that questions live in documents; he did not rule that they gate work. | Yes. | Moderate: the hold rule and what the observatory says about it. The narrower rule, a question holding the design's own item and only the steps that name it, is what [the queue-mirror design](../design/tillr-queue-mirrors-designs.md) proposes as a designer's decision. |
| [OQ-PR9](#OQ-PR9) | Agents. `D-QP5` and `D-QP8` in [queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way) come from the unruled draft of 2026-11-14; `D-QP10` and `D-QP11` were settled on 2026-11-16 "by the design seat while cutting the build order, on recorded principle; the owner may overturn it". The owner's words of 2026-11-18 cover the Icebox and one item for each outstanding design; nothing of his covers `Exploration` or a heading as a switch. | Yes (`311299e2c`, `4cc1877c3`, `45407ab68`). | Moderate: sync's parking and the report. [The queue-mirror design](../design/tillr-queue-mirrors-designs.md) already redesigns both. |
| [OQ-PR10](#OQ-PR10) | `D-QP1` in the unruled draft of 2026-11-14, then [OQ-TC1](../design/tillr-convergence.md#decision-ledger): "Settled by the session's designer on recorded principle ... The owner may overturn it" (`26bccb5e6`). The list was called the seed file. | Yes; the file is gone and the command refuses. | Moderate. The file is recoverable from git; the command and its checks would be rebuilt. It could also be folded into the intake question, [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1). |
| [OQ-PR11](#OQ-PR11) | `D-OA6` in [observatory actions](../reference/observatory-actions.md#why-its-this-way) was listed in the graduated design as "made by the designer on recorded principle, without the owner's ruling". `D-OA5`, the file format, names no author: a `.txt` file, not Markdown, checked by a hash, first under `.tillr/intake/` and then in the tree. | Yes (`420be1619`, `da191ae57`). | Moderate: the intake directory, the document check and the routing. |
| [OQ-PR12](#OQ-PR12) | The designer. The ledger of [designer sessions](../reference/tillr-designer-sessions.md#why-its-this-way) originally said "Every row was decided by the designer on 2026-11-16 from the measurements above", `D-CD1` to `D-CD11`. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): option A, kept. | Yes (`1e0fe3259`, `4b16f3af8`, `081eb89f3`). | Nothing to undo; the comment intake and hold stay. |
| [OQ-PR13](#OQ-PR13) | `D-OA4` in [observatory actions](../reference/observatory-actions.md#why-its-this-way), which the graduated design listed as "made by the designer on recorded principle, without the owner's ruling". The owner's ruling of 2026-11-16 covers how items are ordered, not this file. `D-OA1` to `D-OA3`, how the order is shown, name no author. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): `"database only."`, overturning `D-OA4`. | Yes, since `b7ddee209` and `a4d86408e` on 2026-11-18; the commit is gated, and removal is unbuilt. | Small: remove [`queue-order.txt`](./queue-order.txt), the loop's commit path and the `.tillr.toml` setting. |
| [OQ-PR14](#OQ-PR14) | No ledger row exists for the mark or the limits. They arrived in code (`803d2a5e4`, `3122f481c`) and in the `tillr-prose-style` skill (`8343814ae`). The mark is the attribute `prose="2"` in the hidden comment. The idea is the owner's: text written without the prose guide should be marked and rewritten. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): option B, never inside human-read design documents. | `.tillr.toml` still has in-document rewriting on until [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) moves build steps out of design documents. | Small: retire in-document `prose="2"` rewriting with [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) and one settings line. |
| [OQ-PR15](#OQ-PR15) | Nobody on record. It arrived as code on 2026-11-16 (`c24e60d36`) and has no ledger row. The bundle is the tillr pack, and a workspace is a jail, an agent's sandboxed workspace. | Yes. | Small: the tool, its test and the copies. [OQ-SH2](../design/tillr-selfhosted-build.md#OQ-SH2) already asks about the pack's copy of the programs. |
| [OQ-PR16](#OQ-PR16) | `D-OA8` in [observatory actions](../reference/observatory-actions.md#why-its-this-way), listed in the graduated design as the designer's, "without the owner's ruling". It reverses `D-OBS2`, which was also an agent's. `D-OA7`, how the write is carried out, names no author. | Yes. | Small: one flag. |
| [OQ-PR17](#OQ-PR17) | The designer, "on recorded principle" (`b5e5073cb`, 2026-11-16), in [the retired agent runtime design](../design/agent-runtime-retirement.md) as [OQ-SO1](../design/agent-runtime-retirement.md#question-ids-that-outlive-the-document). The design had said the owner deferred it on purpose: "a scope and an expiry written before any delegated verdict exists cannot be corrected from evidence". Of the 31 bulk answers this is the one that overrode a decision of his. The former default covered verification claims only. **Ruled by the owner on 2026-11-20** ([Decision Ledger](#decision-ledger)): the judge is a step in the flow that judges by the rulebook, and there is no permission to issue or renew. | Yes, in the grant code of `tools/tillr-item`; its removal is designed in [the judge design](../design/tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow) and not built. | Removal spans the store, workflow, drain, views and migration. |
| [OQ-PR18](#OQ-PR18) | An agent: [`run-wall-ruling.md`](../design/run-wall-ruling.md), `D-RW1` to `D-RW3`, titled a "Design ruling" (`6ec85e450`, 2026-11-15, built `adc9b1269` nineteen minutes later). It cites six complaints of the owner's gathered in an evidence file and carries no note that he may overturn it. The limit is called a wall. | Yes. | Small: one default. |
| [OQ-PR19](#OQ-PR19) | `D-GR16` in [the gate rework](../design/gate-rework.md): "Settled by the session's designer ... the owner may overturn it" (`5e367ffde`, 2026-11-17). The stall is 60 one-second samples in a row with no new output and no processor time used; the numbers and the count are not in the owner's words. | Yes (`724cb5853`, `6a75bf6dc`). | Small: the loop's stall handling. |
| [OQ-PR20](#OQ-PR20) | [OQ-GR1](../design/gate-rework.md#decision-ledger), settled "at the owner's request of 2026-11-15 ('fill them in through principles')" (`26bccb5e6`). The cap was set at 20 landings or 60 minutes. **Closed on the owner's 2026-11-18 ruling of [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7)** ([Decision Ledger](#decision-ledger)): no 20-landing cap; work lands while a check is red only when it does not touch what that check reads. | Yes (`c3a3a35a9`); the settings file today has 20 landings and the minutes off, and repairs still land past the cap until [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7)'s waiting rule is built. | Small once [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7)'s waiting rule lands: remove the cap setting. |
| [OQ-PR21](#OQ-PR21) | Not an earlier decision: settled by the owner on 2026-11-19 as the rule from here on ([Decision Ledger](#decision-ledger)). The earlier practice is in [Section 1.2](#12-what-was-delegated-and-what-principle-turned-out-to-mean) and the rule in [Section 6](#6-how-this-stops-happening). | The earlier practice continues today until the loop check in [Section 6](#6-how-this-stops-happening) is built. | Nothing to undo; the loop rule in [Section 6](#6-how-this-stops-happening) is unbuilt. |
| [OQ-PR22](#OQ-PR22) | The owner's sentence of 2026-11-15, as three bulk commits applied it to 31 questions ([Section 1.2](#12-what-was-delegated-and-what-principle-turned-out-to-mean)); narrowed by the owner on 2026-11-19 to dated, quoted rulings of his ([Decision Ledger](#decision-ledger), [Section 6](#6-how-this-stops-happening)). | Yes: the 31 answers stand except the ones since reversed or rejected, until each is reviewed here or in the lists below. | Nothing to undo in the sentence itself; the gate check in [Section 6](#6-how-this-stops-happening) is unbuilt, and each answer the sentence produced is reviewed on this page or in the lists below. |

## 2. Questions for the owner

Each open question below tells one situation: what you do, what tillr does today, and the choice that leaves. Its options say what you would see and what each costs you. Who made the earlier decision, whether it runs today and what undoing it costs are in [Section 1.5](#15-evidence-behind-each-question). They are ordered by how much is built on each and how hard it is to undo, hardest first.

**The rule for all the rest ([OQ-PR21](#OQ-PR21) and [OQ-PR22](#OQ-PR22), ruled 2026-11-19):** a decision that touches your documents, your repository, your money, your models or what you see is put to you as a question before anything is built on that design, while engineering detail is marked as the agent's and may be built on; and an agent closes a question addressed to you only when a dated ruling of yours answers it in so many words and the record carries the quote ([Section 6](#6-how-this-stops-happening)). Under [OQ-PR22](#OQ-PR22), a question answered by a dated quotation of yours is closed on that quote instead of asking you again ([OQ-PR20](#OQ-PR20)), and any question lacking a dated quotation of yours stays open as an unruled choice ([OQ-PR18](#OQ-PR18) and [OQ-PR19](#OQ-PR19)).

**Settled in this review ([Decision Ledger](#decision-ledger)):**

- **[OQ-PR1](#OQ-PR1) (commit messages and tillr state, ruled 2026-11-20):** Neither option as written. Every landed commit says what was built in lasting words that make sense without anyone knowing what tillr is, and carries no reference to tillr, tillr items, runs or other ephemeral tillr state (*"Yes, we should obviously say what was built, but I don't want any references to tiller or tiller items. Those are temporarily ephemeral state. I only want references that will persist and make sense without anybody even knowing what tiller is."*). That keeps `D-RS3`'s ban on `Tillr-Item:` trailers and item IDs in commit messages while requiring durable repository wording; post-landing `chore: retire landed document block` commits end with the hidden comment under [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1).
- **[OQ-PR2](#OQ-PR2), [OQ-PR3](#OQ-PR3), [OQ-PR4](#OQ-PR4), [OQ-PR5](#OQ-PR5) and [OQ-PR12](#OQ-PR12) (ruled 2026-11-20):** Option A on each — every code change is reviewed before landing by a fresh agent with no memory of making it while documents-only landings skip review ([OQ-PR2](#OQ-PR2), overturning [OQ-JL1](../design/tillr-judge-and-lifecycle.md#decision-ledger) for code landings; designed in [diff review](../design/tillr-diff-review.md) and not built yet); multi-step workflows in `config/tillr/workflows/` stay ([OQ-PR3](#OQ-PR3)); how the loop steps through the queue stays with the engineers as engineering detail ([OQ-PR4](#OQ-PR4)); the observatory layout stays as the base ([OQ-PR5](#OQ-PR5)); and while comment work on a design is open, unstarted build steps of that design wait ([OQ-PR12](#OQ-PR12)).
- **[OQ-PR13](#OQ-PR13) (where the hand-dragged queue order is kept, ruled 2026-11-20):** *"database only."* The hand-dragged queue order lives solely in tillr's private database (`.tillr/state.db`). That overturns `D-OA4` in [observatory actions](../reference/observatory-actions.md#why-its-this-way) and rules out saving the dragged queue order in the repository: it rules out the tracked file [`queue-order.txt`](./queue-order.txt), the loop's `docs: record the queue's order` commits that write it, and the `.tillr.toml` setting that turns repository persistence of queue order on (removing the file, the commit path and the setting is not yet built).
- **[OQ-PR14](#OQ-PR14) (prose rewriting and human-read documents, ruled 2026-11-20):** Option B — under [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1), human-read design documents hold no build steps, so no cheap model rewrites wording inside them; any prose shaping happens only on tillr's side or inside the agent-only companion `<topic>-plan.md` file (retiring in-document `prose="2"` rewriting waits on the [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) build).
- **[OQ-PR17](#OQ-PR17) (agents approving checkable results, ruled 2026-11-20 about 11:03 PM ET):** Neither option as written. Agents keep judging what a recorded check can settle, and there is no permission that lasts 24 hours or any other time: the judge is a step in the flow, judging by the rulebook (*"It's not a temporary grant. It's not a thing that will change on a whim. It's encoded for a purpose. ... Like, you're literally saying, here is the flow. There is a judge at this point, and they judge based on this."*). What the judge may approve is written in the workflow's definition and changes only when that file is edited; what waits for you is a pass condition marked as yours alone, as before. The design is in [the judge design](../design/tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow); removing the 24-hour permission from the code is not built.
- **[OQ-PR20](#OQ-PR20) (the 20-landing stop under a failing trunk check, closed on the 2026-11-18 ruling of [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7)):** Closed under [OQ-PR22](#OQ-PR22) on the owner's dated 2026-11-18 ruling in [the graph design](../design/tillr-graph-is-the-path.md#OQ-GP7), where after asking *"why does a red check hold a queue?"* he took the stated leaning in review (`c5ce34bfa`): *"C, weakly: it lets through exactly the items that cannot have made the red worse, so nothing is hidden and no 20-landing stop is needed; it is weak because a red on one of the eight whole-tree checks still holds the queue, and only B moves it then."* That overturns [OQ-GR1](../design/gate-rework.md#decision-ledger)'s 20-landing cap; the cap stays live in the drain only until that path-disjointness waiting rule is built.

**Already asked elsewhere, so not asked again here:**

- **The hidden comment itself**, with its grammar, its attributes and the rule that sync reads every tracked document, was asked as [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) and ruled by the owner on 2026-11-18 around the companion implementation plan (`<topic>-plan.md`). That ruling governs these rows, which are all an agent's: [OQ-QP2](../reference/tillr-queue-persistence.md#why-its-this-way), `D-QP2` and the read-scope half of `D-QP9` in [queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way); the grammar half of `D-RS1` and its later `seat`, `from`, `prose`, `workflow`, `claims`, `judge` and `express` attributes in [the "ruling"](../design/queue-persistence-ruling.md); `D-TC1` and `D-TC2` in [convergence](../design/tillr-convergence.md); the attribute halves of `D-JL8` and `D-IG13`; the item id shape; and the test that fails the gate when a comment names a missing one. What commits say about landed work is settled in [OQ-PR1](#OQ-PR1).
- **The file format that defines a kind of queue**, built at `f04088fb0` on a choice "recommended here and chosen by Product QA" (another project's agent), is now listed for his review in [the core and landing design](../design/tillr-core-and-git-landing.md) as the designer's decisions `D-CG1` to `D-CG5`, labeled as such. Who owns an item's states is recorded there in his own words, and the two names are asked in [the vocabulary](../design/tillr-vocabulary.md).
- **The second copy of every tillr program, kept inside the pack,** is open as [OQ-SH2](../design/tillr-selfhosted-build.md#OQ-SH2).

1. 💬 **OQ-PR6: Are the right models planning and verifying your work?**

   A piece of work is planned, built, tested and verified, each by a named role with a model behind it. You have said which models you want: Terra for implementation, Luna for scouts, Sol "where appropriate", Fable for design. The settings today follow that. Editing, test-writing and documentation run on Terra, and test-running on Luna, which is kept to roles that only read. Two picks are not in your words: an agent put planning and verifying on Sol.

   On 2026-11-20 you commented, *"I thought I ruled on basically this somkewhere elase recently."* A search across `docs/design/` and `docs/tillr/` — [planning and model tiering](../design/tillr-planning-node-model-tiering.md#decision-ledger), [convergence](../design/tillr-convergence.md#decision-ledger), [drain seat profiles](../design/tillr-seat-profiles.md#decision-ledger), [diff review](../design/tillr-diff-review.md#decision-ledger), [plan-step and seat selection](../design/tillr-plan-step-and-seat-selection.md#decision-ledger), [interactive seat profiles](../design/seat-profiles.md#decision-ledger), [coordinator pyramid](../design/coordinator-pyramid.md#decision-ledger) and [the tillr roadmap](./roadmap.md) — found related rulings of yours, none putting Bedrock's `plan` and `verify` drain seats on Sol: on 2026-11-18 [planning and model tiering](../design/tillr-planning-node-model-tiering.md#OQ-PT1) recorded Terra for implementation, Luna for scouts, Sol 6.2 *"where appropriate"* and splitting work into seats *"as we see fit"*, while [the roadmap](./roadmap.md) (`73fd201d2`) recorded `plan` on Sol and `verify` on Sol as settled by the design seat; on 2026-11-19 you put the post-gate `review` seat on the `build` route (Hydrogen on Copilot) in [OQ-DR1](../design/tillr-diff-review.md#OQ-DR1) and allowed same-model review and verification in [plan-step and seat selection](../design/tillr-plan-step-and-seat-selection.md#decision-ledger) while [OQ-PSS1](../design/tillr-plan-step-and-seat-selection.md#OQ-PSS1) and [OQ-PSS2](../design/tillr-plan-step-and-seat-selection.md#OQ-PSS2) remain open on retiring the `plan` step; [drain seat profiles](../design/tillr-seat-profiles.md#decision-ledger) covers the Copilot Gemini and held Claude subscription profiles rather than Bedrock; and [interactive seat profiles](../design/seat-profiles.md#decision-ledger) and [coordinator pyramid](../design/coordinator-pyramid.md#decision-ledger) (2026-10-20 and 2026-10-21) cover interactive pi sub-agent seats rather than tillr's drain policy.

   - **A — Planning and verifying stay on Sol.** Nothing changes, and the full set of roles becomes yours.
   - **B — Change one or both.** You name the model. A stronger one costs more on every piece of work; a cheaper one risks weaker plans or missed defects. The settings files and any step file that names a role change with it.

   <!-- vantage: question id=OQ-PR6 leaning="A: everything else follows what you said about Terra, Luna and Sol, and these two are the only picks you did not state." -->

   _Leaning:_ A: everything else follows what you said about Terra, Luna and Sol, and these two are the only picks you did not state.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-PR7: When a model provider refuses work, should tillr send it to the next provider without asking you?**

   The loop is running overnight and your Claude subscription runs out of quota. Today tillr sends the same work to the next provider on that role's list in the same pass, so it carries on at Bedrock and is billed there, and you are not asked. It moves a piece of work once only, and never moves work that is under review. Agents wrote this rule in three places; your one recorded wish was that Ember be used while it lasts.

   - **A — Work moves by itself.** The queue keeps draining, and you meet the cost on the next bill. Each move is shown on the web page. The once-only limit is dropped, because you have ruled out attempt counts.
   - **B — Work waits.** Refused work shows as waiting on quota until the provider recovers or you move it. There is no surprise bill, and that role's work stops overnight.

   <!-- vantage: question id=OQ-PR7 leaning="A: your standing ruling is that the loop never stops on one item's condition." -->

   _Leaning:_ A: your standing ruling is that the loop never stops on one item's condition.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-PR8: Should one unanswered question in a design hold all of that design's work?**

   A design lists five build steps and has one question open for you, about the fifth. You expect the first four to be built while you think. Today none is: tillr holds every piece of work written in a document that has a question open. A step written on the roadmap instead is held by nothing. Agents built that hold. You ruled that questions live in documents, not that they stop work, and on 2026-11-18 you said the queue tracks each outstanding design "regardless of open questions".

   - **A — The whole design waits.** Nothing is built on a design you may still change. Four steps that have nothing to do with the question sit idle until you answer.
   - **B — Only what the question touches waits.** The steps that name the question wait, and the other four are built. If your answer changes something they assumed, that work is redone.

   <!-- vantage: question id=OQ-PR8 leaning="B: today a step waits on a question that is not about it." -->

   _Leaning:_ B: today a step waits on a question that is not about it.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-PR9: Should renaming a heading in your roadmap be able to change the queue?**

   You tidy the roadmap and rename a heading from "Exploration" to "Ideas". The next `tillr item sync` stops treating the designs linked under it as parked, and prints nothing about it, because a heading that starts with `Icebox` or `Exploration` is what parks them. In the same way, a design's `stage` and `status` header fields decide whether tillr reports that the design owes work. You ruled on the Icebox on 2026-11-18 ("icebox items should be loaded too, marked blocked for icebox") and on one queue entry for each outstanding design. The `Exploration` heading, and a heading acting as a switch at all, are agents' choices.

   - **A — All of them stay switches.** Nothing changes. A rename or a header edit goes on changing the queue silently.
   - **B — Fewer switches, and sync says what it did.** Only the `Icebox` heading and the `stage` field act. Every sync lists each design it parked or loaded and the word that made it. A design under "Exploration" is treated like any other.

   <!-- vantage: question id=OQ-PR9 leaning="B: your rulings of 2026-11-18 need only Icebox and the stage word, and sync should say what each word made it do." -->

   _Leaning:_ B: your rulings of 2026-11-18 need only Icebox and the stage word, and sync should say what each word made it do.

   **Answer:**

   > _(empty — fill in when decided)_

5. 💬 **OQ-PR10: Should there be a way to queue work that has no document?**

   You think of a small chore and want it in the queue without writing a design. tillr used to have a way: you added a line to a tracked list file and ran a command that loaded it. An agent deleted both, so the command now refuses and work enters only by being written in a document. The seven entries that had no document became prose on the pi roadmap. Your words of 2026-11-18, that syncing a roadmap is what creates the queue's entries, point the same way, but you never ruled on the list itself.

   - **A — Documents only.** There is one place to look for where work came from. A chore needs at least a line in a document.
   - **B — The list returns.** A chore is one line and one command. Work then has two sources to keep in step, and the command and its checks are rebuilt; the file itself can be recovered from git.

   <!-- vantage: question id=OQ-PR10 leaning="A: your goal that state lives in documents and your words of 2026-11-18 on syncing the roadmap both point this way." -->

   _Leaning:_ A: your goal that state lives in documents and your words of 2026-11-18 on syncing the roadmap both point this way.

   **Answer:**

   > _(empty — fill in when decided)_

6. 💬 **OQ-PR11: When you jot down an idea, should a design agent turn it into work without checking back with you?**

   You type a half-formed idea into the box on tillr's web page. You asked for that on 2026-11-16, with your own words saved. Today they are saved as a plain `.txt` file in the repository. A design agent then takes one turn on the idea, the result of that turn always lands, and titled pieces of work replace the idea in the queue. Only an idea the agent thinks needs you stops, as one held design entry. Saving your words was your request; what happens next was an agent's choice.

   - **A — Straight into the queue, as today.** The idea becomes queued work after one agent turn. You see the pieces once they exist, and your original words stay in the text file.
   - **B — You approve first.** The agent's proposed pieces wait for your OK before they replace the idea. Nothing is queued that you have not seen, and every idea costs you a second visit.

   <!-- vantage: question id=OQ-PR11 leaning="A: it does what you asked for, and an idea that needs you already stops for you." -->

   _Leaning:_ A: it does what you asked for, and an idea that needs you already stops for you.

   **Answer:**

   > _(empty — fill in when decided)_

7. 💬 **OQ-PR15: Should copies of your tillr designs be installed in every other workspace?**

   An agent working on another project of yours, the Product QA pipeline for one, wants to know what tillr does. Today it reads a copy. A tool copies every design that [the status index](./design-status.md) marks `shipped` into the bundle that installs tillr, so every agent workspace gets them, with the hidden comments disabled in the copies. Nobody decided this on record; it arrived as code on 2026-11-16. The stakes are low: the copies are a dated snapshot, and removing them is a small job.

   - **A — The designs travel with tillr.** Agents elsewhere can read what tillr does. Their copy lags your originals until the next copy is made.
   - **B — Programs only.** Nothing stale is out there to mislead. An agent elsewhere has to be told what tillr does, or work it out from the programs.

   <!-- vantage: question id=OQ-PR15 leaning="A: agents in other projects read them to learn what tillr does, and the cost is a snapshot that lags." -->

   _Leaning:_ A: agents in other projects read them to learn what tillr does, and the cost is a snapshot that lags.

   **Answer:**

   > _(empty — fill in when decided)_

8. 💬 **OQ-PR16: May tillr's web page change the queue without a login?**

   You drag a piece of work up the queue on tillr's web page, or type an idea into its box. Both change the queue, and the page was first designed to be read-only. Today the server accepts those two kinds of change when it is started with `--write`. It checks that the request came from its own page and asks for no login. An agent made that reversal when the dragging and the idea box were built.

   - **A — Changes allowed, no login.** You drag and type with nothing in the way. So can anyone else who can reach the page while it runs with `--write`.
   - **B — Changes allowed behind a login.** You sign in first. A login has to be built and kept working.
   - **C — Read-only again.** The page only shows. You reorder the queue and add ideas from the command line.

   <!-- vantage: question id=OQ-PR16 leaning="A: you asked for dragging, which needs the page to change things, and the server listens on your own machine." -->

   _Leaning:_ A: you asked for dragging, which needs the page to change things, and the server listens on your own machine.

   **Answer:**

   > _(empty — fill in when decided)_

9. 💬 **OQ-PR18: Should an agent's run have a time limit?**

    An agent is three hours into a hard piece of work. It used to be killed after 30 minutes. Today a run has no limit unless one is set, a run that has committed is judged on its commit before any set limit is looked at, and a run stopped at a set limit before committing is resumed once. An agent wrote those three rules on 2026-11-15, citing six October 2026 timeout complaints of yours. No dated quotation of yours rules on a default run limit or on how many times a stopped run resumes ("we said there are no timeouts" is undated in the documents, and the uncounted-resume rule in [judge and lifecycle](../design/tillr-judge-and-lifecycle.md#decision-ledger) is recorded without quoting your words), so this has not been ruled.

    - **A — No limit, and a stopped run always resumes.** A long run finishes, committed work is judged before any set limit, and the "once" cap on resuming a stopped run goes. A run that is going nowhere keeps spending until something else notices.
    - **B — A default limit returns.** You say how long. Runs past it are stopped.

    <!-- vantage: question id=OQ-PR18 leaning="A: removing the 30-minute limit stopped false kills on finished work, and dropping the one-resume cap removes the remaining arbitrary count." -->

    _Leaning:_ A: removing the 30-minute limit stopped false kills on finished work, and dropping the one-resume cap removes the remaining arbitrary count.

    **Answer:**

    > _(empty — fill in when decided)_

10. 💬 **OQ-PR19: When a check hangs a second time, should the work be handed to you?**

    The full test run on a piece of work goes silent. On 2026-11-17 you ruled that a time budget is a target and only a stall ends a check: "target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine." That quote did not say what happens after a stall. An agent defined a stall as 60 seconds with no new output and no processor time used, and chose what follows: the check runs once more when fewer checks are in flight, and a second stall on the same work hands the work to you. Neither a dated quotation on hung checks nor a quoted rule on attempt counts settles that second-stall stop, so the choice is open.

    - **A — Restarted, never counted.** A stalled check is restarted when load drops while its cause is diagnosed and fixed, and reaches you only when an agent raises an objection that names you. A check that keeps stalling keeps spending until the cause is fixed.
    - **B — Twice, then you.** As today. The second stall puts the work in front of you, whatever caused it.

    <!-- vantage: question id=OQ-PR19 leaning="A: a second hang is a tool or check fault for an agent to diagnose rather than a stop count for you, while the sixty-second stall threshold stays as engineering detail." -->

    _Leaning:_ A: a second hang is a tool or check fault for an agent to diagnose rather than a stop count for you, while the sixty-second stall threshold stays as engineering detail.

    **Answer:**

    > _(empty — fill in when decided)_

## 3. Corrections that need no ruling

These rows were false or mislabeled when this review was written. They were applied on 2026-11-18 in one pass, and each row says what changed or that it was already true.

**Rows that still state a default the owner later reversed:**

- [x] [Gate rework](../design/gate-rework.md), [OQ-GR2](../design/gate-rework.md#decision-ledger): says the default is to start a new worktree from the last passing commit, and notes only that this repository sets `tip`. Should say the owner overturned the default on 2026-11-17 (`1fa2c069c`) because it handed workers a tree older than their own work's definition. **Applied 2026-11-18:** the row says the owner overturned it on 2026-11-17 (`1fa2c069c`) and that the code's default when the key is absent is still `green`; the Section 6.6 settings table and its lane-base paragraph say the same.
- [x] [Convergence](../design/tillr-convergence.md), [OQ-TC3](../design/tillr-convergence.md#decision-ledger) and the section it settled: still say a held item is started again when an input changes. Should say superseded by the owner's ruling of 2026-11-17 (`D-JL10`); the code was removed at `f8774bd63`. **Applied 2026-11-18:** the status line, the Section 3.2 bullet, the re-admission build section and the ledger row say superseded by `D-JL10`, removed at `f8774bd63`.
- [x] [Queue persistence](../reference/tillr-queue-persistence.md), [OQ-QP4](../reference/tillr-queue-persistence.md#why-its-this-way): ends "the trace is on". Should say the owner removed the git trace on 2026-11-17 (`d09016bba`). **Applied 2026-11-18:** the row reads overturned, removed at `d09016bba`, and the tier-3 text and the table row that described the trace say it is no longer recorded.
- [x] [Scheduled tasks](../design/scheduled-tasks.md), [OQ-ST2](../design/scheduled-tasks.md#12-open-questions): rests on the re-run rule of [OQ-TC3](../design/tillr-convergence.md#decision-ledger). Should drop "re-dispatched once on a changed input". **Applied 2026-11-18:** its _Settled_ text states `D-JL10` and no longer re-dispatches on a changed input.
- [x] [Failure diagnosed, not waited out](../design/failure-diagnosed-not-waited-out.md), [OQ-1](../design/failure-diagnosed-not-waited-out.md#open-questions): one automatic retry. Should say superseded by `D-JL10`. **Applied 2026-11-18:** a "Superseded" paragraph follows its _Settled_ text.
- [x] [Run wall ruling](../design/run-wall-ruling.md), `D-RW3`: "resumed once". Should say resumed, uncounted. **Applied 2026-11-18:** the row says resumed, uncounted, and built in part: the loop still keeps one continuation for a run cut by the wall with no uncommitted work.
- [x] [Item graphs](../reference/tillr-item-graphs.md#why-its-this-way), `D-IG4`: depth and node caps. Should say the caps were dropped. **Applied 2026-11-18:** the row says the caps were dropped on 2026-11-17 on the owner's reported words, pending his confirmation.
- [x] [Queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way), `D-QP5`: says parking a design with no item creates nothing. Should cite the owner's words of 2026-11-18 that Icebox items are loaded and marked blocked. **Applied 2026-11-18:** the row quotes the owner's words, as recorded in `D-QM2` of the queue-mirrors design, and says they are not built.

**Rows and headers that claim a ruling nobody recorded:**

- [x] [The queue-persistence "ruling"](../design/queue-persistence-ruling.md): header says "Ruled" and "Needs your ruling: None". `D-RS1` to `D-RS5` should each read as a designer's decision the owner may overturn, and the header should name [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) and [OQ-PR1](#OQ-PR1). **Applied 2026-11-18:** the status line names a design agent's ruling, the header names both questions, and the ledger says every row is a designer's decision.
- [x] The same document, `D-RS3`: cites "the owner's no-tillr-in-commits rule". Should say the rule is unquoted and unconfirmed until he answers [OQ-PR1](#OQ-PR1). **Applied 2026-11-18.**
- [x] [Run wall ruling](../design/run-wall-ruling.md): titled "Design ruling" with no note. `D-RW1` to `D-RW3` should read as designer's decisions. **Applied 2026-11-18:** a note under the title says whose ruling it is, and the ledger says every row is a designer's decision; the title is unchanged.
- [x] [Board utilities](../design/board-utilities.md), `D-UT16` and `D-UT17`: "Design ruling, 2026-11-12". Should read as designer's decisions. [The judge-in-tillr document](../design/work-control-judge-in-tillr.md) should say again that the owner has not accepted the assessment its build rests on. **Applied 2026-11-18:** both rows read as designer's decisions, and the judge-in-tillr status line says a design seat ruled it and the owner has not accepted the assessment.
- [x] [Build commit](../reference/tillr-build-commit.md#why-its-this-way): "Needs your ruling: None" over a decision an agent chose in the graduated design. Should name the decision as the designer's. **Applied 2026-11-18:** the graduated design's header named immediate continuation as the designer's choice and the cap removal as the owner's ruling of 2026-11-17 (now preserved in the reference's rationale table).
- [x] [Observatory design](../reference/observatory-design.md), [OQ-OBD6](../reference/observatory-design.md#why-its-this-way): written as ruled at `7c9abc080`, equal to its leaning, with no words of the owner's, after `17f6fa0ca` had withdrawn an earlier early compaction for that reason. Should read as a designer's decision. **Applied 2026-11-18.**
- [x] [Agent runtime (retired)](../design/agent-runtime-retirement.md), [OQ-SO1](../design/agent-runtime-retirement.md#question-ids-that-outlive-the-document): marked answered beside text that says the owner deferred it. Should read as a designer's default in force, open for the owner. **Applied 2026-11-18:** a note after its _Settled_ text said so and pointed at [OQ-PR17](#OQ-PR17). **Closed 2026-11-20** by the owner's ruling on [OQ-PR17](#OQ-PR17), which that section now records.
- [x] [Judge and lifecycle](../design/tillr-judge-and-lifecycle.md), `D-JL11`: the quoted principle is the owner's; the six kinds of obstacle and their routes are the coordinator's. Should say which is which. **Applied 2026-11-18:** the row and a new paragraph in Section 4.5 say which is which.
- [x] [The tillr roadmap](./roadmap.md), the paragraph under "Rulings that are waiting on the owner": says 23 settled questions each say so in their design. Several do not, and the count of questions left is stale. **Applied 2026-11-18:** the paragraph counts the 27 questions the index lists, says not every settled question says so in its design, and points here.

**Recorded rulings that contradict each other:**

- [x] [Gate rework](../design/gate-rework.md), [OQ-GR1](../design/gate-rework.md#decision-ledger) sets a cap on landings while the trunk fails; [the graph design](../design/tillr-graph-is-the-path.md) lists as a rule that the owner does not accept one. One of them is wrong; [OQ-PR20](#OQ-PR20) decides which. **Resolved 2026-11-18 by the owner's ruling of [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7), no landing cap:** the gate-rework row says he ruled the cap out and that it stays live until the rule that replaces it is built. [OQ-PR20](#OQ-PR20) is closed in [the ledger](#decision-ledger) on that ruling.
- [x] The graph design's rule that the git trace is ruled out carries no quote and reverses [OQ-QP4](../reference/tillr-queue-persistence.md#why-its-this-way). Should cite `d09016bba`. **Applied 2026-11-18:** rule R12 cites `d09016bba` and the reversal.
- [x] [Item graphs](../reference/tillr-item-graphs.md#why-its-this-way), `D-IG11`: the owner's words of 2026-11-15 that a sub-agent is never a node. His words of 2026-11-17 hoist sub-agents into nodes. The row should say it is overturned in part. **Applied 2026-11-18:** the row reads "Overturned in part" and quotes the words of 2026-11-17.
- [x] [Observatory design](../reference/observatory-design.md), `D-OBS2`: "the board writes nothing". Should point at `D-OA8`, which reversed it. **Applied 2026-11-18:** the row reads reversed in part by `D-OA8`, with a link.

**Ids and superseded rows:**

- [x] [Muse backend](../reference/tillr-muse-backend.md#why-its-this-way): the id `D-MM11` is used for two different rows. The second, the failover order, should be `D-MM12`. **Applied 2026-11-18, the other way round:** the failover row was written first (`61b030959`, before `a4cc6e457`) and the Go comments cite `D-MM11` for failover, so the later row, the commit in the lane's worktree, became `D-MM12`; the two documents that cited it were updated.
- [x] `D-RS` is a prefix in both [the queue-persistence "ruling"](../design/queue-persistence-ruling.md) and [the restart reference](../reference/tillr-restart.md#why-its-this-way), for unrelated decisions. One should be renamed. **Applied 2026-11-18:** the restart ledger's rows became `D-RT1` to `D-RT10`, with a note in its ledger, and the self-hosted build design's citation was updated. A third use of `D-RS`, in [the design-seat replacement ruling](../design/design-seat-replacement-ruling.md), is not renamed.
- [x] [Queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way), `D-QP6`: migrate by attrition. Should say superseded by [OQ-TC1](../design/tillr-convergence.md#decision-ledger). **Already true on 2026-11-18:** the row already said so.

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

The escalation ladder's last row is the pattern this review is about, written down as policy. The owner's rulings of 2026-11-19 on [OQ-PR21](#OQ-PR21) and [OQ-PR22](#OQ-PR22) reject building on unseen non-engineering decisions and closing owner questions without a dated quote before that design is built; its own open questions stay open in [escalation ladder](../design/escalation-ladder.md#open-questions) until he rules them there.

## 5. Medium and low significance

### 5.1. Medium, live or partly live

All are an agent's unless the row says nobody is named. Rows already covered by a question in [Section 2](#2-questions-for-the-owner) or [the ledger](#decision-ledger) are left out.

| Id | Design | Decision | Live? |
| :--- | :--- | :--- | :--- |
| `D-GP1`, `D-GP10`, `D-GP12` | [Graph is the path](../design/tillr-graph-is-the-path.md) | One graph per item for life; an objection is the one ending that changes who acts; a visible sub-agent is a step inside its parent | Partly |
| `D-JL1` | [Judge and lifecycle](../design/tillr-judge-and-lifecycle.md) | The judge runs before the gate for items that opt in | Partly |
| `D-JL3`, `D-JL4`, `D-JL5`, `D-JL7` | same | The stored "in progress" state is retired; moves between states are checked; a failed gate writes no verdict; the AutoQA actor names are removed | Yes |
| `D-JL11` | same | A worker ends with a commit or one of six named obstacles, each with a route (the principle is the owner's, quoted; the six and their routes are the coordinator's) | Yes |
| `D-GR9`, `D-GR10`, `D-GR11`, `D-GR12` | [Gate rework](../design/gate-rework.md) | A check that cannot run on this machine is "unavailable"; a check is one declaration file; a budget test; upstream drift files an item and holds nothing | Yes |
| `D-PT1`, `D-PT2`, `D-PT4` | [Planning and model tiering](../design/tillr-planning-node-model-tiering.md) | Each kind of work is a seat and the list is open; a plan lists its pieces; the planner is the first step of a planned build | Yes |
| `D-IG2`, `D-IG3`, `D-IG7` | [Item graphs](../reference/tillr-item-graphs.md#why-its-this-way) | The outside sees one item; a step is fixed once recorded; a step writes only inside the item's allowed paths | Yes |
| `D-MM1`, `D-MM2`, `D-MM11` (first use) | [Muse backend](../reference/tillr-muse-backend.md#why-its-this-way) | A backend's contract is where it may write, held by a sandbox and a snapshot; a worktree run commits its own work (the first two name nobody) | Yes |
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
| `D-QP4` | [Queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way) | A badge and a lock bound work that is in the store and not yet in a document |
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
| `D-AL1`, `D-AL9` | Retired autonomous-landing design (superseded by [landing principles](../design/landing-principles.md)) | The unattended guarantee; a failing trunk stops the whole loop |

### 5.3. Low

About 100 further rows are rated low: engineering detail with nothing the owner sees. The count for each document, an agent's in every case: agent context delivery 10, drain successor 10 (covered by [OQ-PR4](#OQ-PR4)), scheduled tasks 9, autonomous landing 8, escalation ladder 7, goal monitor 6, self-hosted build 5, gate rework 4, observatory design 4, definitions plan 4, AutoQA 3, designer sessions 3, graph is the path 3, item graphs 3, landing call 3, storage 3, queue persistence 2, observatory actions 2, idea pipeline 2, Muse backend 2, planning and model tiering 2, restart 2, seat profiles 2, and one each in the Claude backend, early design landing, observatory redesign, the queue-persistence "ruling", separable parts and vocabulary. The bulk commits add a handful in the merge-queue retirement and observatory documents.

## 6. How this stops happening

Two rules are the owner's rulings of 2026-11-19 ([OQ-PR21](#OQ-PR21) and [OQ-PR22](#OQ-PR22)). Three more (`D-PR1` to `D-PR3`) are the designer's decisions for this document, labeled as such; the owner may overturn any of them.

**Owner's ruling ([OQ-PR21](#OQ-PR21)): nothing is built on unseen product or user-facing decisions.** Any decision that touches the owner's documents, his repository, his money, his models or what he sees is put to him as an open question, and nothing is built on that design until he answers. Engineering detail is marked as the agent's decision and may be built on without stopping the queue.

**Owner's ruling ([OQ-PR22](#OQ-PR22)): an agent closes a question addressed to the owner only by quoting him.** An agent may close an owner question only when a dated ruling of his answers it in so many words, and the ledger row carries the exact quote and date. An agent's own leaning, another agent's design, unquoted paraphrase, or the fact that code already behaves a certain way never counts as a principle and never closes a question; everything else stays open for him.

**D-PR1, designer's decision: only the owner's quoted, dated words are a ruling.** A ledger row may say "ruled" or name the owner only when it carries his words in quotation marks and the date. A paraphrase is recorded as a paraphrase, with who wrote it.

**D-PR2, designer's decision: a designer's decision always says so, in the row.** The row begins "Designer's decision; the owner may overturn this", as [the queue-mirror design](../design/tillr-queue-mirrors-designs.md)'s ledger already does. A document that holds one never says "Needs your ruling: None" without also naming how many designer's decisions it holds, and its title never calls itself a ruling.

**D-PR3, designer's decision: a check enforces these in the gate.** It joins the gate's document checks and refuses, on lines a change adds:

- a ledger row that says "ruled" or "owner" with no quotation and no date;
- a header that says "Needs your ruling: None" in a document whose ledger holds a designer's decision without naming how many designer's decisions it holds;
- a question closed in a change whose ledger row carries no dated quotation of the owner's words ([OQ-PR22](#OQ-PR22)).

**What has to be built (none of it is built yet):**

- **For [OQ-PR21](#OQ-PR21):** a rule in sync and the loop that a design holding an unruled decision touching the owner's documents, repository, money, models or visible surface is put to him as an open question and holds building on that design until he answers, while decisions marked as engineering detail do not hold the build; plus the authoring and compliance-check rule that requires such decisions to be filed as open questions rather than recorded as designer's decisions.
- **For [OQ-PR22](#OQ-PR22) and `D-PR1` to `D-PR3`:** the gate document check and its tests refusing added ledger rows that claim an owner ruling or close an owner question without a dated quotation of his words, and refusing a bare "Needs your ruling: None" over designer's decisions; plus updating agent instructions so "fill them in through principles" is never read as leave to close a question without a dated quote. The existing rows are a backlog, so the gate check judges added lines only, as the gate's other document checks do.
- **Across the audited corpus:** a pass over the 39 audited documents to relabel or reopen rows as the owner answers [OQ-PR1](#OQ-PR1) to [OQ-PR20](#OQ-PR20), plus removing repository persistence of the dragged queue order ([`queue-order.txt`](./queue-order.txt), its loop commit and its `.tillr.toml` setting) under [OQ-PR13](#OQ-PR13).

**What the audits did not settle.** They disagree on three ratings: `D-QP9` and `D-RS5` are high in one and low or medium in another, and `D-QP10` is high in one and medium in another; this page treats each at the higher rating (`D-PR4`). They did not verify that rows recorded as the owner's are his: most are paraphrase. They did not open the sources of landing principles 1 to 10, the earlier ruling behind `D-QP5`, or the roughly 95 roadmap answers one by one.

## Decision Ledger

[OQ-PR1](#OQ-PR1) to [OQ-PR5](#OQ-PR5) and [OQ-PR12](#OQ-PR12) to [OQ-PR14](#OQ-PR14) are the owner's rulings of 2026-11-20, as is [OQ-PR17](#OQ-PR17), ruled that night; [OQ-PR20](#OQ-PR20) is closed on his 2026-11-18 ruling of [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7); and [OQ-PR21](#OQ-PR21) and [OQ-PR22](#OQ-PR22) are his rulings of 2026-11-19. `D-PR1` to `D-PR4` are designer's decisions made in writing this review, and the owner may overturn any of them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-PR1 | **Owner ruling:** Neither option as written. Every landed commit says what was built in lasting words that make sense without anyone knowing what tillr is, and carries no reference to tillr, tillr items, runs or other ephemeral tillr state: *"Yes, we should obviously say what was built, but I don't want any references to tiller or tiller items. Those are temporarily ephemeral state. I only want references that will persist and make sense without anybody even knowing what tiller is."* Keeps `D-RS3`'s ban on `Tillr-Item:` trailers and item IDs in commit messages while requiring durable repository wording; post-landing block-deletion commits end with [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1). | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | Partly: commits omit item IDs and trailers; block-deletion commits end with [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) |
| OQ-PR2 | **Owner ruling:** Every code change is reviewed before it lands by a fresh agent with no memory of making the change, and documents-only landings skip review (overturning [OQ-JL1](../design/tillr-judge-and-lifecycle.md#decision-ledger)'s mechanical-by-default rule for code landings, as designed in [diff review](../design/tillr-diff-review.md); `D-UT16` and `D-JL2`'s criterion classes stand). He took option A: *"A, already ruled; confirm: on 2026-11-18 you asked for a fresh agent that reviews every code change before it lands."* | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | Partly: `D-UT16` and `D-JL2` built; mandatory code review before landing unbuilt ([diff review](../design/tillr-diff-review.md)) |
| OQ-PR3 | **Owner ruling:** A piece of work may run as a series of steps laid out in workflow files under `config/tillr/workflows/`, with a role per step and file inclusion (`D-IG1`, `D-IG5`, `D-IG9` and `D-IG13` in [item graphs](../reference/tillr-item-graphs.md#why-its-this-way)). He took option A: *"A: your rulings on the item graph, made after this was built, already assume it."* | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | Built (`b657ed406`, `064bf3e8a`) |
| OQ-PR4 | **Owner ruling:** How the loop steps through the queue (`D-DS1` to `D-DS16` in [the drain successor reference](../reference/tillr-drain-successor.md#why-its-this-way)) is left to the engineers as engineering detail that the owner has seen. He took option A: *"A: you have said engineering detail is the agent's to decide, and nothing you see changes with it."* | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | Partly (`a792a9df1`) |
| OQ-PR5 | **Owner ruling:** The observatory's layout — dedicated linked pages, stepping keys, home-page grouping by who must act, and fixed queue order (`D-OBS1` to `D-OBS13` and [OQ-OBD6](../reference/observatory-design.md#why-its-this-way) in [the first observatory reference](../reference/observatory-design.md#why-its-this-way); `D-OR1` to `D-OR6`, [OQ-OR2](../reference/observatory-redesign.md#why-its-this-way) and [OQ-OR3](../reference/observatory-redesign.md#why-its-this-way) in [the observatory reference](../reference/observatory-redesign.md#why-its-this-way)) — stays as the base, with changes requested as he uses it. He took option A: *"A: you use it daily and already ask for changes directly, which tests it better than rereading the alternatives."* | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | Built (`98ecde6a6`, `34fd911b8`; stepping keys not re-verified) |
| OQ-PR12 | **Owner ruling:** Comments on a design in Vantage become work for that document's design agent, and while that comment work is open, unstarted steps of that design wait (`D-CD1` to `D-CD11` in [designer sessions](../reference/tillr-designer-sessions.md#why-its-this-way)). He took option A: *"A: comment work closes quickly, and building on a design you have just objected to wastes runs."* | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | Built (`1e0fe3259`, `4b16f3af8`, `081eb89f3`) |
| OQ-PR13 | **Owner ruling:** *"database only."* The hand-dragged queue order lives only in tillr's private database (`.tillr/state.db`). Overturns `D-OA4` in [observatory actions](../reference/observatory-actions.md#why-its-this-way) and rules out saving the dragged order in the repository ([`queue-order.txt`](./queue-order.txt), loop commits `docs: record the queue's order`, and the `.tillr.toml` setting that enables them). | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | — (removal of [`queue-order.txt`](./queue-order.txt), the loop commit and the `.tillr.toml` setting unbuilt) |
| OQ-PR14 | **Owner ruling:** No cheap model rewrites wording inside the owner's human-read design documents; under [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) build steps live in the agent-only companion `<topic>-plan.md` file (or in tillr's store), so prose shaping happens only on tillr's side or inside `<topic>-plan.md`. He took option B: *"B, already settled by [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1); confirm: human-read design documents hold no build steps, so no cheap model rewrites wording inside them."* | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | — (awaits [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) build to retire in-document `prose="2"` rewrites) |
| OQ-PR17 | **Owner ruling:** Neither option as written. An agent judges as the judging step of the work's flow, with that step's instructions and references; no permission is issued to it, nobody is named as having granted one, and nothing about it expires. On 2026-11-20 about 11:03 PM ET: *"Any such rules like this should be directly encoded in the charter of the charter that you're running, I guess. I don't know if you have a noun for that. ... the charter of the system you're running. ... Those are part of the rulebook. It's not a temporary grant. It's not a thing that will change on a whim. It's encoded for a purpose. Okay. Like, an agent that is provided this set of instructions with these set of references can, is the agent that sits at this approval step. Like, you're not, like, granting it permission. Like, you're literally saying, here is the flow. There is a judge at this point, and they judge based on this. You don't, I don't even understand what you're granting."* Earlier that evening: *"Are you just turning me into a giant monkey that is like tied here with a timer for no other reason than to hit the approve button? Like what does this do other than block things? It's just a timer for coming up with a reason to block the queue and the only resuming is just me hitting a button?"* Closes [OQ-SO1](../design/agent-runtime-retirement.md#question-ids-that-outlive-the-document). Which files hold the rule, and how the 24-hour permission is taken out, are designer's decisions in [the judge design](../design/tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow). | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | — (the store still refuses an agent's verdict without an unexpired permission; a 30-day one issued by an operator session runs to 2026-12-20, 10:57 PM ET) |
| OQ-PR20 | **Owner ruling (closed under [OQ-PR22](#OQ-PR22) on his 2026-11-18 ruling of [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7)):** There is no 20-landing cap when a check is failing on the main branch; work that does not touch what the failing check reads keeps landing, and work that touches it waits for the repair (overturning [OQ-GR1](../design/gate-rework.md#decision-ledger)). On 2026-11-18 he asked *"why does a red check hold a queue?"* and took the stated leaning on [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7) in review (`c5ce34bfa`): *"C, weakly: it lets through exactly the items that cannot have made the red worse, so nothing is hidden and no 20-landing stop is needed; it is weak because a red on one of the eight whole-tree checks still holds the queue, and only B moves it then."* | 2026-11-18 | [Section 2](#2-questions-for-the-owner) | — (20-landing cap stays live until [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7)'s waiting rule is built) |
| OQ-PR21 | **Owner ruling:** A decision that touches the owner's documents, his repository, his money, his models or what he sees is put to him as a question, and nothing is built on that design until he answers; engineering detail is marked as the agent's and may be built on. He took option A, the stated leaning: *"A: it matches what you said on 2026-11-18, that you decide product direction and trade-offs and the agent decides engineering detail."* | 2026-11-19 | [Section 6](#6-how-this-stops-happening) | — |
| OQ-PR22 | **Owner ruling:** An agent closes a question addressed to the owner only when a dated ruling of his answers it in so many words, and the record carries the quote; everything else stays open for him, and an agent's own leaning never counts as a principle. He took option A, the stated leaning: *"A: it keeps what you wanted, not being asked the same thing twice, and stops an agent's own leaning from counting as a principle."* | 2026-11-19 | [Section 6](#6-how-this-stops-happening) | — |
| D-PR1 | **Designer's decision; the owner may overturn this.** Only the owner's quoted, dated words are recorded as a ruling. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | — |
| D-PR2 | **Designer's decision; the owner may overturn this.** A designer's decision says so in its row, and its document never claims that nothing needs a ruling. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | — |
| D-PR3 | **Designer's decision; the owner may overturn this.** A gate check refuses an unquoted "ruled" row, a question closed without a dated quotation of the owner's words, and a false "Needs your ruling: None", on added lines only. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | — |
| D-PR4 | **Designer's decision; the owner may overturn this.** Where two audits rate a decision differently, this review uses the higher rating. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | — |
