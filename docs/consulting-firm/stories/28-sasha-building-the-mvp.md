# 28. Sasha — Building the Tillr MVP, Stage by Stage

**Context:** Sasha is a tech lead at a 5-person startup. They've read
the consulting-firm doc cover to cover. They love the vision. They have
1-2 weeks to ship something that demonstrates value, not 6 months to
build all 10+ layers across 8 stages.

This is the meta-story: how does a real team adopt the consulting-firm
model without drowning? What's the smallest valuable shipping slice,
and how do they validate before adding the next?

**What happens today (without a staged roadmap):**

Sasha reads `implementation-layers.md`. Sees 10 numbered layers plus
sub-layers (2b, 4b, 9b). Each layer references stories. Each story
references multiple layers. The dependency graph is a tangle.

Three failure modes:

1. **Paralysis.** "We need all of this." So nothing ships. The vision
   stays a doc.
2. **Cherry-picking the AI bits.** Sasha skips ahead to Layer 7
   (knowledge synthesis) because it sounds magical. Without Layer 5
   (decision extraction) underneath, the synthesis has nothing to work
   from. Garbage in, garbage out.
3. **Big-bang.** Sasha decides to build Stages 1-3 all at once, in
   parallel. Six weeks later, there's a half-finished implementation
   of each, none shippable.

What's missing is a STAGED ROADMAP that:
- Orders work by dependency and risk (build foundations first; defer
  research bets)
- Defines explicit validation criteria per stage (so you know when to
  ship the next)
- Names the smallest shippable slice first

**What happens with the staged roadmap (`roadmap.md`):**

1. Sasha reads `roadmap.md`. Sees 8 stages + Stage 0 (foundational).
   Each stage lists:
   - Layers included
   - Stories that become possible after the stage ships
   - Risk level
   - Estimated effort
   - Validation criteria
   - Dependencies on prior stages

2. Sasha picks **Stage 0 + Stage 1**:
   - Stage 0: Platform adapter (1-2 weeks). The thinnest possible:
     "invoke Claude SDK with a prompt; capture the output as a single
     comment via the tillr API."
   - Stage 1: Comments + cycle hooks (1-2 weeks). The `comments` table,
     `tillr comment` CLI, cycle hooks emit comments at claim/submit/
     review boundaries, comment thread visible on feature detail page.

   Why these two? Stage 1 is the lowest-risk, highest-value increment
   ([story 22 (Derek)](./22-derek-progressive-disclosure.md) shows it
   works alone). Stage 0 is implicit — you can't run Stage 1 without
   SOME way to invoke an agent. Build the cheapest possible adapter
   first; sophistication grows over stages.

3. Sasha builds:
   - `comments` table with author, role, body, metadata, timestamp
   - `tillr comment <feature-id> "text"` CLI
   - `tillr show <feature-id> --comments` to view
   - Cycle hooks: on claim emit "Claimed by implementer"; on submit
     emit "Submitted for review"
   - Web UI: feature detail page shows the thread
   - Adapter: shell out to Claude SDK with prompt template; capture
     output as a comment

4. Ships in 9 days. Sasha's PMs immediately notice they can see WHY
   decisions were made.

5. After 2 weeks of usage, Sasha evaluates against Stage 1's
   validation criteria from `roadmap.md`:

   - "PMs report being faster to QA (target: 30%+)" — yes, ~40% faster
     on the 18 features reviewed in this period.
   - "Agents emit comments at expected granularity" — mostly. A few
     tweaks to the prompt template to encourage decision-flagging.
   - "Decision summaries appear in inbox" — yes, basic version. The
     summary is "N comments" rather than "N decisions" because
     decision extraction (Layer 5) hasn't shipped yet. PMs get value
     anyway.

   Stage 1 succeeded. Sasha pauses to celebrate, writes a brief
   internal note about the wins and remaining gaps.

6. Sasha picks **Stage 2** next: Layer 4 (PM mid-flight comments) +
   Layer 4b (async dialogue cycle states). Reasons:
   - The most painful gap from Stage 1: PMs see what agents are doing
     but can't redirect mid-flight without a full reject.
   - Stage 2 sets up Stage 3 (style enforcer + questionnaires need 4b
     for async dialogue).
   - Estimated 2-3 weeks.

7. Sasha builds Stage 2 in 3 weeks. Validates against criteria. Picks
   Stage 3.

8. **The pattern.** Read roadmap.md → pick the next stage → build →
   validate against the stage's explicit criteria → decide what's
   next.

**Discipline required:**

- **Don't stack stages without validation.** The temptation is huge.
  Each stage's value depends on the prior stage being USED, not just
  installed. Validation needs at least 1-2 weeks of real usage data.

- **Some stages MUST ship together.** Layer 9 (universal PR pipeline)
  + Layer 9b (style guide) is one example — the style guide PR
  mechanism needs the universal pipeline. Stage 3 bundles them
  intentionally.

- **Stage 0 is implicit but required.** Every stage assumes you can
  invoke an agent. The Stage 0 adapter starts MINIMAL ("call SDK,
  capture output") and grows over time (model picker, multi-platform
  routing per [story 29](./29-anders-platform-adapter.md)).

- **The order is a default, not a mandate.** If your team has a
  specific pain that would be solved by a later stage and you can
  afford the foundations, jumping ahead is OK. But know what you're
  skipping.

**What would trip Sasha up:**

- **Pressure from leadership to "ship the AI stuff faster."** Layer 7
  (knowledge synthesis) without Layer 5 (decision extraction) produces
  hallucinated patterns. Layer 6 (context graph) without Layer 1
  (comments) has nothing to assemble. Discipline matters most here.

- **Skipping Stage 2.** "Comments are working fine, why add async
  dialogue?" Style guide and code reviewer dialogue (Stage 3) are
  blocked by 4b. Without Stage 2, Stage 3 reduces to "synchronous
  reviewer agent that interrupts the implementer" — less valuable.

- **Building Stage 0 too late.** Day 1 needs SOME adapter. The
  minimum viable adapter is small (~200 lines of code) but essential.
  Don't accidentally build Stage 1 with no way to invoke agents.

- **Stage skipping based on hype.** "Director hierarchy sounds
  cool, let's build Stage 8 first." Stage 8 (story 26) requires Stages
  1-7 to be MATURE. Building it first produces a hierarchy with no
  data flowing through it.

- **Treating validation criteria as soft.** "We shipped Stage 2 but
  validation showed only 5% improvement, not 30%. Let's ship Stage 3
  anyway." This is how the AI parts of tillr become decorative —
  they look good in slides but don't deliver. If validation fails,
  diagnose (was the stage built right? was the metric wrong? is the
  hypothesis wrong?) BEFORE moving on.

**What makes this work:**

- **The roadmap is ORDERED.** Read top to bottom; implement top to
  bottom. The order encodes dependency + risk + value-velocity
  reasoning that's been thought through.

- **Each stage is small enough to ship in 1-3 weeks.** No 6-month
  tunnels. Validation checkpoints between stages catch errors early.

- **Validation criteria are EXPLICIT** (in `roadmap.md`). You don't
  argue about whether a stage shipped — you measure.

- **Risks are documented per stage.** Sasha knows in advance where
  the bear traps are (synthesis quality at Stage 6, context graph
  edges at Stage 5, hierarchy at Stage 8) and can plan mitigations.

- **The roadmap is a default, not a mandate.** Teams with specific
  pain points or different priorities can re-order, with awareness of
  what they're trading.

**Position in roadmap:**

This story is **meta** — it doesn't require any layer; it explains
how to USE the roadmap. It's the recommended entry point for anyone
evaluating the consulting-firm doc for adoption.

For an even more structured view, read [roadmap.md](../roadmap.md).
This story is the narrative version of that doc.

---

« [All stories](./README.md) · [Roadmap](../roadmap.md) · [Consulting-firm overview](../README.md)
