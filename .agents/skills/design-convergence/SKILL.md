---
name: design-convergence
description: Use when a design is too big for one document — an architecture, a state machine, a migration, an operational playbook — and the work is to build the document constellation around it, fill the holes in it, and converge it through design-level review rounds. Covers splitting a subject into design doc, plan, reference and roadmap; the decision ledger and question routing; the independent coherence pass; briefing a design seat to find gaps; propagating decisions into the documents that enforce them; and answering review comments at the document level.
---

# Design convergence: from a subject to a landed constellation

Read the owning skill before writing any document: `design-doc`, `implementation-plan`,
`roadmap`, `system-doc`, `vantage-docs`. This skill is the campaign that uses them, not a
substitute for them.

## 0. THE SHAPE

One subject, one **design doc** that owns it. Everything else is derived from that document and
points back at it: the **implementation plan** (per-file build order), the **reference** documents
(how the shipped system and its machinery work), the **roadmap** entries (what is left and what
blocks it), and — when the design is an operation a person performs — a **tactics card** or runbook.
Together they are the constellation. A decision that lives in two documents will eventually
disagree with itself; a decision in none of them does not exist.

Three roles, and mixing them is what makes these campaigns drift:

- **The design seat decides.** It reads documents, not code, and it answers the questions the
  documents carry. You do not fill a decision gap by reasoning about the implementation.
- **You execute and record.** Edit the document, land it, run the machinery, report evidence. When
  the owner says "stop deciding", that is this rule.
- **The owner rules on what only the owner can.** Irreversible deletions, authorization, policy. A
  design seat's verdict does not substitute for an owner's approval of a destructive transition.

## 1. Split the subject before writing prose

Write the split down first; it is what keeps later edits from landing in the wrong document.

- [ ] **Design doc**: what we settled and why, the trade-offs, the open questions. Not build order.
- [ ] **Implementation plan**: per-file, per-task build detail, each task naming its tests. Must not be
      built from while the design still has open questions.
- [ ] **Reference**: how the shipped thing works — read when operating it, updated when it changes.
- [ ] **Roadmap**: what is unfinished, in dependency order, linking the documents rather than
      restating them.
- [ ] **Tactics card / runbook**: the operation, command by command, with what the operator sees.
      This is a reference that grows by discovery; see [Section 8](#8-recognise-the-document-that-is-becoming-something-else).

If a document cannot answer "who reads this and when", it is not one of these — delete it.

## 2. Give every decision an id and every question an owner

- **Decisions** get numbers (D1, D2, …) in a **Decision Ledger** section: the decision, its date, the
  section that governs it. Downstream documents cite the number, never paraphrase the decision.
- **Questions** get ids too (OQ-…), filed **in the document that owns the subject**, with what is
  being asked, the options, and who can answer. An unfiled question is not a question: it is a hole
  that reappears in three documents.
- **Answering a question moves it, it does not delete it**: fold the ruling into the section it
  governs, then compact the question into a ledger row that keeps its exact id.
- **Routing is a sweep of its own.** When a constellation is written, questions land in the wrong
  document: a question about a superseded recommendation sits on a `superseded` document and blocks a
  live plan. Do the routing pass as one job, and expect it to make *more* questions visible first.
- **Check the stage against the status.** A `superseded` document still staged as a live design will
  offer its questions for ruling and hold live work hostage. Fix the pair together.

## 3. Run the coherence pass

The constellation is written by several hands, so it disagrees with itself. Read the whole set for
one thing only: **contradictions, duplicates, and orphans**. Not prose quality.

- [ ] Every decision cited consistently, and defined once.
- [ ] Every question either answered-and-compacted or open-with-an-owner, and no question owed by a
      document nobody will read.
- [ ] Every cross-document link resolves to an anchor that exists (a heading rename is the usual rot).
- [ ] Every count, threshold or table appears in exactly one document; the others link to it.
- [ ] Land the corrections as one commit with a message that says what disagreed.

Do not run this pass and an independent review at the same time; a reviewer reading a set you are
mid-edit on reports the edit, not the design.

## 4. Fill the holes with a gap analysis

A design that reads well can still be unbuildable or unoperable. The honest question is *can a person
do the playbook tomorrow, and does the plan build the thing the playbook assumes?*

Brief the design seat once, with the facts, and ask for a bounded verdict:

- **Give it a `<FACTS>` block** — the state of the machinery as measured, not as hoped: counts,
  flags, what a command printed, what is landed. Name the files to read by path.
- **Ask for the one document you want back**, in lines: findings, each with what it falsifies and the
  smallest change; then a verdict of the form *"run it after N named fixes"*. A verdict without named
  fixes cannot be executed.
- **Expect it to reason from documents only.** A fact that is missing from the brief and the documents
  is a gap, named in one line — that is the finding, not a failure. Do not ask it to go read the code;
  a read-only facts pass is a separate worker job.
- **Act on the fixes, then correct the record.** Every finding that changes a document changes that
  document in the same round, and the plan records what was falsified. A gap analysis that produces a
  list nobody applies is a fifth document that disagrees with the other four.

## 5. Propagate, or the decision stays in one place

The recurring defect: a ruling gets written into the document where it was discussed and nowhere it
is enforced. After every design round, sweep for decisions that changed behaviour and ask *where is
this enforced?*

- A ruling about a state machine belongs in the state machine's document **and** in the plan task
  that builds it.
- A ruling about an operation belongs in the tactics card **and** in the reference's section on it.
- A ruling that retires a recommendation belongs on the retired document (status, stage) **and** on
  every roadmap entry that pointed at it.
- Number them (G1, G2, …) in the round's report if that helps you not lose any, and land them in one
  commit. Then delete the numbering from your head; the documents are the record.

## 6. Answer review at the document level

Review comments arrive attached to a paragraph. Answer them by changing the document, in the same
round, and say what changed.

- **Save the document before delivering any response.** The reviewer reads it from disk at delivery
  time; delivering first records the stale version.
- **A ruling goes into the document, not into the reply.** Record the decision first, then reply.
- **Prefer one restated section to five scattered corrections.** If the comment reveals that a whole
  area is thin, rewrite that area and say so — a patch that satisfies the sentence usually leaves the
  defect.
- **Answer concretely**: what changed, what stays unbuilt, what the reader now does differently. A
  reply that says "clarified" teaches nobody anything.
- **When the comment is about an operation, add the command that shows the state** rather than another
  paragraph describing it. A reader who can run something stops needing prose.

## 7. Make the document set machine-checkable, then distrust the checks

- Run the document checker on the changed files (`uvx vantage-check check <paths…>`). Fix what it
  reports; a broken anchor or a malformed directive costs a reviewer a round.
- Declare the **stage vocabulary** for the design doc kind in the repository's checker config, and use
  only those words. Off-vocabulary stages are not style: they make the planning view lie about what is
  decided.
- Citations: an anchor or a quoted line, never a bare line number; a count that cannot be re-derived
  from a command is a claim, not evidence.
- A commit sha is evidence only when the current branch contains it. A record of unlanded work says so.
- Nothing tracked cites a scratch or retention-managed directory. State the command, the result and
  the date instead.
- **Know what the checker cannot see**: it may skip hidden directories, it cannot see a document's
  *meaning*, and a pack lint that does not read frontmatter will not catch a broken one. Green means
  the syntax holds, never that the design is right.

## 8. Recognise the document that is becoming something else

A design doc that grows a command, a screenshot of output, and a "what you'll see" for every step has
stopped being a design and become a runbook. That is not decay, it is the document telling you what it
is:

- **The tell**: the last five edits answered "how do I do this" rather than "what did we decide". 
- **The move**: graduate it to a reference, keep the decisions in the design doc, and put a short skill
  or roadmap entry in front of it. A runbook is a reference that grows by discovery; a design doc is
  one that stops changing once it is decided.
- When a runbook needs to be *found* rather than read, write the thin skill that says "when a task
  looks like this, read that document and follow it". Do not copy the document into the skill: the copy
  is the version that goes stale.

## 9. The working contract with a design seat

- **Name the model explicitly** on every dispatch. A pack floor model will fill in silently, and a
  cheap model produces workflow scripts where you asked for a document.
- **One brief, wrapped in a directory**: the question, the state, the files to read by path, the shape
  of the answer, and what it must not do (edit anything, decide the owner's call, dwell on the code).
- **If it may research, tell it to batch**: one fan-out, results read directly. Sequential scouts burn
  a session and change no conclusion.
- **Effort is per stage**, not per session: mechanical passes low, implementation and research medium,
  verification and design critique high. A design review at low effort is a summary, not a review.
- **Its output is a document you land**, with its decisions recorded as decisions. A chat summary of a
  design seat's verdict is not a record.

## 10. What converged looks like

- Every question is either answered with a ledger row or open with a named owner, and the planning
  view shows exactly those.
- Every document in the set is the right kind, staged with the repository's own vocabulary.
- The plan builds what the playbook assumes; the playbook's preconditions are true today, checked by
  commands rather than remembered.
- The reference describes the machinery as it is, and its numbers come from commands.
- The last round's findings are all either landed or filed; none exist only in a report.

Then, and only then, the next question is whether the thing should be built — which is a different
campaign, and the plan is where it starts.
