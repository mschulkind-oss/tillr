# When each repair finds a different defect

**Design proposal, 2026-11-20.** Before sending a refused candidate back for another repair, the drain should be able to show what has changed across repairs and ask the design seat whether this is still one landable item. Keep **one commit per item**, not one item per design. The owner said on 2026-11-20 at about 5:40 PM ET: “Split it up. That's always a good way to go.” The detection rules, routing and recording method below are the designer's proposals; the owner may overturn them. They are not implemented by this document.

**Absorbed on 2026-11-21.** [An oversized item lands in parts](../design/tillr-item-split.md) is the design for splitting, written to the owner's words of that day. It keeps this page's three comparisons and its rule that the old candidate is a donor; it replaces the hand-filed landing list and the "replaced by" record below, and answers the two questions at the end as designer's decisions the owner may overturn ([what is kept](../design/tillr-item-split.md#106-the-proposal-of-2026-11-20)).

The filing reports eleven real attempts in two days on the instant-start change, with seven verify refusals naming different defects. The operator had to read the history to discover that pattern. The [measured candidate](instant-start-slices.md#what-is-being-ported) changed 39 files under `tools/`: 26 source files, 11 tests and two READMEs, or 78 files with the pack mirror. That is evidence about one candidate, not a measurement that it grew at every attempt. The [slice list](instant-start-slices.md#items-to-file) shows a useful cut: maintain a view without letting it start work; change who may start work in a later item.

## What to measure before dispatching another repair

Make one observation per item and settled verify refusal, keyed by its run and refusal event. Update it at settlement, not on each planning pass or trunk move. Carry it across workflow restarts. Read the actual dispatch history for context; neither a displayed attempt ordinal nor time spent waiting measures repairs. Show these three comparisons side by side:

| Signal | Read from | Proposed test for attention |
| --- | --- | --- |
| The defect keeps changing | `drain.refusal` of kind `verify-not-done`; [the judge](../../tools/tillr-drain/drain/judge.go) preserves each refused criterion and the verifier's reason. | In the last three refused verifications, does each name a defect not present in the preceding ones? Group by criterion and concrete failed expectation (e.g. the named test and what failed), ignoring run IDs and paths to temporary output. Keep the quoted reasons. A new head changes the judge's evidence digest; that alone does **not** make a defect new. If the words are ambiguous, show them for diagnosis rather than claiming three distinct defects. |
| The repair keeps expanding | Candidate's recorded base and head, `git diff --numstat <base> <head>` for each distinct refused candidate. | Across three successive candidates, flag two consecutive increases in source paths or two in source additions plus deletions. Report both series; show tests and docs separately and exclude the derived pack copy. A missing revision or nonnumeric line count is unknown, not zero. Never diff against today's trunk. |
| The patch has outgrown the ask | The filed words, criteria and fence; the design slice's stated budget, compared in the same units with the candidate diff above. | Flag even the first candidate over its named budget. Without a budget, show the request beside the changed components and ask which criterion needs each. A component with no identified purpose is grounds to refer it; a broad fence or a short request is not a size estimate. No universal line limit. |

Any one positive comparison warrants design review; none is a failure verdict. Gate checks, conflicts, adapter failures and missing verifier output are not verify findings. An [identical worker ending](../../tools/tillr-drain/drain/repeated_ending.go) still takes the existing repeat route to design. Attach the observations to that diagnosis instead of starting a competing one. Likewise, keep [design-repeat routing](../../tools/tillr-drain/drain/design_repeat.go) when applicable; the operator review below is for the scope diagnosis, not an interception of a question already due to the owner. A growing test suite alone is not growing production scope.

## Who acts before the next attempt

1. **Drain → design and operator.** Before offering the next repair, pin the candidate and record one review hold with the item, triggering runs, findings, sizes, request, and next actor. Deliver the packet to the design seat and surface the hold to the operator in `tillr drain why` and on the item page; don't require a history search. Release the worker lane. Replaying a settlement must neither send another notice nor reserve the lane again.
2. **Design → planner or producer.** Try splitting first. Find the smallest behavior that can land, be tested and remain correct if the next piece never ships. Include tests, docs owed by that behavior, and its generated mirror in the same piece. Don't split by file count or put all tests in a final item. If the change really is indivisible, identify the shared invariant, the remaining finding and the narrow proof that would settle it. Record that answer and resume the producer with the kept work, or return a wrong plan to planning. Release only the reviewed hold; don't waive verify or gate. Evaluate new evidence at the next refusal, not the already answered observations on every tick.
3. **Design → operator → owner, only as needed.** If design cannot find a safe cut or the same diagnosis returns after its answer, keep the item held for the operator. The operator may correct a mistaken measurement, arrange a tooling fix or choose a scope-preserving repair with reasons. Neither role may silently drop a criterion, change the promised result or reprioritize an owner's commitment. Send the owner the disputed requirement, evidence, and concrete alternatives; keep that part held until a dated ruling answers it. Unrelated work can still run.

**One hold, not two retry policies.** [D-JL21](../design/tillr-judge-and-lifecycle.md#413-what-the-attempt-number-and-refusal-counts-tell-the-operator-d-jl21) makes the attempt number and refusal tallies descriptive. The attempt-number request (`item-request-8c40ba8929`) supplies history, not authority to stop work. The separately filed three-verify-refusal hold (`item-request-165203fd73`) would change routing on a tally alone. If adopted, its third refusal opens the **same** review, even when the measurements are inconclusive; an existing review just gains evidence. Until then, use the comparisons, not a count-only trigger. Neither an attempt count nor a time limit exhausts the item.

## One design, several receipts

At initial filing or at review, the designer adds a short **Landing pieces** list under the relevant heading of the original design. For each piece give a stable label, the original criteria it serves, a landable outcome and proof, prerequisites by label, and its filed item ID. For example (illustrative, not queue state):

| Piece | Outcome and proof | Needs | Item / landing |
| --- | --- | --- | --- |
| A · view | Maintain a shadow view; a paused refresh cannot stop legacy dispatch. | — | Filed / awaiting receipt |
| B · handoff | Switch dispatch once; two starters cannot claim the same lane. | A and the claim safeguards | Not yet filed |

The list is the split record, not a new plan document. The operator files one ordinary request per label pointing back to that heading and label, with its own fence and criteria. Reuse a recorded item for that label on retry. Register required order as [`blocked_by` edges](../../tools/tillr-item/README.md), not just queue ranks. Independent pieces may land in either order. No approval per row: the design seat checks that the union still covers the original acceptance criteria, including any end-to-end proof owed by the last required piece.

When replacing an active item, keep it held while filing and wiring its replacements; don't dispatch partial filings. Preserve its candidate as a named donor, not as an already verified child. Before releasing the children, check every required row has exactly one item and redirect any downstream dependency on the original result to all required terminal pieces. Record the original as **replaced by** those items, non-dispatchable, not landed. A restart mid-split reconciles the list and existing filings before releasing anything. Each child goes through its own verify, gate and landing.

The operator reconciles the same list from successful [landing receipts](../../tools/tillr-land/README.md): put the item ID and landed trunk commit beside each completed piece, leaving refused, held or merely filed pieces outstanding. Batch these ordinary doc updates; children need not widen their fences to edit the design. Report “2 of 5 required pieces landed,” not “design landed,” until all required receipts and the outcome proof are present. Label optional follow-ups separately; only an authorized scope decision can make a required piece optional.

## Decision ledger

| Proposed decision | Whose decision | Status |
| --- | --- | --- |
| Compare refusal findings and candidate diffs at settlement; send one packet for design review. | Designer, 2026-11-20; owner may overturn. | Proposed; count-only policy open below. |
| Split first using a landing list in the original document, dependency edges and receipts; do not make the old candidate a child landing. | Designer, 2026-11-20; owner may overturn. | Proposed; no change to the lander's verification rules. |

## Questions for the owner before building policy

- Should the third verify refusal always pause for review, or should it only notify when the evidence still shows a bounded, converging repair? The related hold request proposes the former; the current tally rule does not. Neither option fails the item.
- Should the owner see only decisions the design seat and operator cannot make, or also receive a notice whenever the operator takes over? Neither option requires approval of routine splits.

For a particular split that cannot preserve scope, ask: **Keep the original requirement and fund a separate required piece, or accept the explicitly named smaller result and defer the remainder?** This design answers neither on the owner's behalf.
