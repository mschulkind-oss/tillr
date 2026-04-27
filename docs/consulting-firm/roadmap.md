# Roadmap

How to actually build this. The
[implementation-layers](./implementation-layers.md) doc decomposes by
ARCHITECTURE; this doc decomposes by **shipping slices**. A "stage"
groups layers that ship together to deliver a coherent value
increment. Stages are ordered by dependency and risk: ship low-risk
foundations first; defer high-risk research bets until the substrate
is stable.

For the narrative version of this roadmap from a tech lead's
perspective, see
[story 28 (Sasha)](./stories/28-sasha-building-the-mvp.md).

> **2026-04-27 update:** Stage 0 has expanded significantly. The
> post-reset architecture (see [story 30 — Rui — conductor pattern](./stories/30-rui-conductor-pattern.md))
> brings a conductor + persona model with context files in swarf, a
> retro command, max-parallelism config, and a TUI. The original
> "Stage 0: Platform Adapter" was minimal (one adapter, ~200 lines).
> The new Stage 0 is "Foundational Conductor + Persona Infrastructure"
> and is the entire MVP — see **[mvp.md](./mvp.md)** for the
> shipping plan. Subsequent stages (1+) follow as before, building
> on the conductor substrate.

## How to read this

For each stage:

- **Layers** — which architectural layers from
  [implementation-layers.md](./implementation-layers.md) are included
- **Stories unlocked** — which user stories become possible after this
  stage ships
- **Risk** — low / medium / high (with rationale)
- **Effort** — rough estimate
- **Depends on** — which prior stages must be done
- **Validation** — how you know the stage delivered value

You can re-order, but understand the tradeoffs documented in each
stage. The default order optimizes for: low-risk first, value-velocity
high, dependencies respected.

---

## Stage 0 — Foundational Conductor + Persona Infrastructure

**Layers:** Conductor pattern, persona context files, swarf storage
layout, retro command, max-parallelism, TUI (and the existing comments
+ cycle hooks from the reset, which are already in place).

**Stories unlocked:**
[22 (Derek)](./stories/22-derek-progressive-disclosure.md) — comments
already work post-reset;
[30 (Rui — conductor pattern)](./stories/30-rui-conductor-pattern.md) —
the architecture this stage is built around;
[31 (Yael — TUI)](./stories/31-yael-tui-primary-interface.md) — TUI
ships in this stage.

**Risk:** Medium. The mechanics are straightforward (file CRUD, CLI,
Task tool dispatch); the calibration is the unknown — context file
sizes, compaction strategy, retro signal-to-noise, persona prompt
quality.

**Effort:** 1-2 weeks (8-13 days), per [mvp.md](./mvp.md).

**Depends on:** Post-reset skeleton (commit 98f4140 or later). Nothing
external.

**Validation:** see the "How to know we shipped" section in
[mvp.md](./mvp.md). High-level: Rui can run a real session through the
conductor pattern end-to-end, with persona context files growing
usefully and retros producing actionable recommendations.

**Notes:** This is the entire MVP. Subsequent stages (1–8) are *post-
MVP* — they extend the dogfoodable system rather than build the
foundation. Story 29 (Anders — multi-platform adapter) is **deferred**
for MVP; Claude-only.

---

## Stage 1 — Cycle hooks emit comments (extend MVP)

**Layers:** [2 (cycle hooks emit comments)](./implementation-layers.md#layer-2-agent-comments-in-cycles)

**Stories unlocked:**
[1 (Priya)](./stories/01-priya-solo-pm.md),
[3 (Sana — partial, with timing-race gap)](./stories/03-sana-interdependent-features.md)

**Risk:** Low. Simple integration: persona invocations emit comments
on the feature they were dispatched for.

**Effort:** 3-5 days.

**Depends on:** Stage 0 / MVP (personas + comments substrate).

**Validation:**
- PMs report being faster to QA (target: 30%+ time reduction)
- Agents emit comments at expected granularity (verified by sampling
  10 features; iterate prompt template if not)
- Decision summaries appear in inbox (basic version: "N comments")

**Notes:** This stage alone delivers most of the user-visible value of
the consulting-firm vision. Story 22 (Derek) shows a happy user with
JUST this stage. Resist the temptation to skip ahead — many
implementations should pause here for at least 4-6 weeks of validation
before adding Stage 2.

---

## Stage 2 — Close the human loop

**Layers:** [4 (PM mid-flight comments)](./implementation-layers.md#layer-4-pm-interaction),
[4b (async dialogue cycle states)](./implementation-layers.md#layer-4b-async-reviewer--implementer-dialogue)

**Stories unlocked:**
[2 (Marcus)](./stories/02-marcus-mid-flight-steering.md),
[5 (Rachel)](./stories/05-rachel-nontechnical-pm.md),
[27 (Sana resolved)](./stories/27-sana-cross-feature-coordination-resolved.md),
[28 (Sasha)](./stories/28-sasha-building-the-mvp.md) (meta)

**Risk:** Medium. Cycle state extension; agent harness must check
inbox at boundaries.

**Effort:** 2-3 weeks.

**Depends on:** Stage 1.

**Validation:**
- PMs successfully redirect mid-flight on test cases
- Reviewer↔implementer dialogue converges in <5 iterations on
  representative PRs
- Cross-feature coordination works without race conditions (Sana case
  from story 27)

**Notes:** Stage 2 sets up Stage 3 (style enforcer + questionnaires
need 4b for async dialogue). Skipping Stage 2 cripples Stage 3 — style
guide enforcement reduces to "synchronous reviewer that interrupts the
implementer," much less valuable.

---

## Stage 3 — Enforced quality gates

**Layers:** [9 (universal PR pipeline)](./implementation-layers.md#layer-9-universal-pr-pipeline),
[9b (style guide as artifact)](./implementation-layers.md#layer-9b-style-guide-as-first-class-artifact),
[2b (cycle questionnaires)](./implementation-layers.md#layer-2b-cycle-questionnaires-enforced-structure)

**Stories unlocked:**
[12 (Nora amendment)](./stories/12-nora-philosophy-evolution.md),
[13 (Jake cycle template)](./stories/13-jake-cycle-template-change.md),
[14 (Wei knowledge PR)](./stories/14-wei-knowledge-pr-review.md),
[15 (Carlos incident)](./stories/15-carlos-incident-response.md),
[18 (Sana specialization)](./stories/18-sana-specialization.md),
[24 (Meera questionnaires)](./stories/24-questionnaires-as-checkpoints.md),
[25 (Henry style enforcer)](./stories/25-style-enforcer-async-dialogue.md)

**Risk:** Medium. PR pipeline mechanical; bureaucracy risk is real.

**Effort:** 4-6 weeks (largest stage).

**Depends on:** Stage 2.

**Validation:**
- Style enforcer catches >80% of issues PMs were catching at human-qa
- Questionnaire dial works (high-risk template causes more PM time;
  standard template doesn't)
- PR types coexist in inbox without overwhelming PM

**Notes:** This stage is what differentiates tillr from a smart logging
tool. Three sub-layers ship together because the style guide PRs
(9b) need the universal pipeline (9), and questionnaires (2b) reuse
the same PR-style review action.

---

## Stage 4 — Memory and search

**Layers:** [5 (decision extraction + search)](./implementation-layers.md#layer-5-decision-extraction),
[8 (philosophies)](./implementation-layers.md#layer-8-driving-philosophies)

**Stories unlocked:**
[4 (Diego)](./stories/04-diego-new-engineer-onboarding.md),
[6 (Tom)](./stories/06-tom-pm-handoff.md),
[11 (Nora day-one)](./stories/11-nora-philosophies-day-one.md)

**Risk:** Low for Layer 8 (just CRUD). Medium for Layer 5 (decision
metadata extraction quality matters).

**Effort:** 2-3 weeks.

**Depends on:** Stage 1 (need comments to extract decisions from).

**Validation:**
- Search finds relevant decisions (manual precision/recall on a test
  set of queries)
- Philosophies cited in agent comments (verifying they're being read,
  not just stored)
- Tom-style PM handoff works in 30 minutes (story 6)

**Notes:** Layer 8 (philosophies) is mostly mechanical — table with
version history + append to context. Layer 5 (decision extraction)
gets harder if comments are inconsistent in format; Layer 2's prompt
template should encourage decision-style comments to make extraction
easy.

---

## Stage 5 — Context graph

**Layers:** [6 (context packet assembly)](./implementation-layers.md#layer-6-context-graph-assembly)

**Stories unlocked:**
[7 (Kai)](./stories/07-kai-context-packet.md),
[9 (Lin)](./stories/09-lin-new-domain-onboarding.md),
[10 (Omar)](./stories/10-omar-dependency-graph.md),
[21 (Agent-1)](./stories/21-agent-implementer-perspective.md),
[23 (failure case)](./stories/23-context-graph-failure.md)

**Risk:** **HIGH.** Token budget tuning, summarization quality, edge
type design — these are research problems, not just engineering. Story
23 documents a known failure mode.

**Effort:** 6-12 weeks.

**Depends on:** Stages 1-4.

**Validation:**
- Rejection rate drops measurably after context graph (target: 30%+)
- Context packet stays under token budget (3k default, see open
  question 5) on representative features
- "Context used: N decisions, M features" disclosure surfaces correctly

**Notes:** This is the highest-leverage layer in the consulting-firm
vision and also the highest-risk. Build incrementally — start with
explicit edges (tags, comments, declared deps) and add inferred edges
(file overlap, shared SQL tables) carefully. Story 23 shows the failure
mode of inferring too aggressively.

---

## Stage 6 — Knowledge synthesis

**Layers:** [7 (synthesis from review history)](./implementation-layers.md#layer-7-knowledge-synthesis-and-agent-onboarding)

**Stories unlocked:** [8 (Ava)](./stories/08-ava-knowledge-synthesis.md),
full [9 (Lin)](./stories/09-lin-new-domain-onboarding.md),
full [14 (Wei knowledge PR)](./stories/14-wei-knowledge-pr-review.md)

**Risk:** **HIGH.** Synthesis quality propagates errors to every future
agent. Wrong patterns in the brief = wrong code in 50 features.

**Effort:** 4-6 weeks.

**Depends on:** Stages 1-5; data accumulation (need ~30+ completed
features for synthesis to be useful).

**Validation:**
- Synthesized brief catches patterns the PM agrees with on manual
  review
- Knowledge PRs accepted >50% (lower means synthesis is wrong; track
  trend)

**Notes:** Defer this until you have data. Running synthesis on 5
features produces noise. Start synthesis cautiously — the Knowledge PR
flow (story 14) lets the PM curate before patterns propagate.

---

## Stage 7 — Process improvement

**Layers:** [10 (metrics, retro, estimation, reporting)](./implementation-layers.md#layer-10-metrics-estimation-and-reporting)

**Stories unlocked:**
[16 (Priya tech debt)](./stories/16-priya-tech-debt-dashboard.md),
[17 (Marcus estimation)](./stories/17-marcus-estimation-error.md),
[19 (Jake retro)](./stories/19-jake-automated-retrospective.md),
[20 (Wei stakeholder)](./stories/20-wei-stakeholder-report.md)

**Risk:** Medium. Estimation similarity matching is non-trivial.

**Effort:** 4-6 weeks.

**Depends on:** Stages 1-6 (needs data to analyze).

**Validation:**
- Estimates within 2x of actuals on 80% of features
- Retro PRs result in measurable improvement on next sprint
- Tech debt dashboard correctly categorizes (audit a sample)

**Notes:** Layer 10 turns the system back on itself — process improves
the process. Critical for long-term viability. Without it, cycle
templates / questionnaires / style guides ratchet up forever.

---

## Stage 8 — Hierarchical org structure

**Layers:** Layer 11 (NEW: director / nested PM hierarchy)

**Stories unlocked:** [26 (Olivia)](./stories/26-olivia-director-hierarchy.md)

**Risk:** **HIGHEST.** New data model; aggregate summarization at scale;
recursive escalation rules; agent-PM trust calibration.

**Effort:** 8-12 weeks.

**Depends on:** ALL prior stages mature. Don't start until you have at
least 3 mature tillr projects feeling the pain.

**Validation:**
- Cross-project decisions propagate correctly
- Director can answer "what's happening across all projects" in <30
  seconds
- Agent-PM mode handles routine without escalating constantly

**Notes:** Optional for single-project users. Many teams will be happy
in the flat model from Stages 1-7 indefinitely. Stage 8 is for
organizations large enough to need cross-project coordination.

---

## Risks summary

Cross-cutting risks (apply to multiple stages, with which stage they
first manifest):

| Risk | First manifests | Mitigation |
|------|-----------------|------------|
| Comment summarization quality | Stage 1 | Prompt template iteration; validation by sampling |
| Mid-flight race condition | Stage 2 | Layer 4b cycle states + pre-submit check (90% solved, not 100%) |
| Cycle template / questionnaire bureaucracy | Stage 3 | Stage 7 retro agent prunes unused questions |
| Style rule degradation via easy exceptions | Stage 3 | Stage 7 retro agent flags rules with high acceptance rate |
| Context envelope too big or too small | Stage 5 | 3k token default + per-step envelopes (story 25); revisit per cycle template |
| Knowledge synthesis hallucinations propagate | Stage 6 | Knowledge PR flow (story 14) requires PM curation |
| Cross-feature blind spots from code-level deps | Stage 5 | Story 23 documents known failure; mitigation TBD |
| Agent-platform feature drift | Stage 0 ongoing | Capabilities matrix per adapter; cycle template declares required capabilities |
| Token cost compounds across roles | Stage 3 | Per-step model class (fast for cheap roles, strong for expensive) |
| PM inbox overload | Stage 3 | Inbox prioritization (open question 23) |
| First-slice scope creep | Stage 1 | Validation discipline between stages — don't ship Stage 2 without Stage 1 data |
| Hierarchical context flow design | Stage 8 | Far-future; design after foundations mature |

Read each stage's "Notes" section for stage-specific mitigations.

---

## What composes / what to skip

**The minimum viable tillr (consulting firm flavor):** Stages 0 + 1.
This is "tillr 2.0 basic." Most users will be happy here for a long
time. Story 22 (Derek) shows it.

**The differentiated tillr:** Stages 0-3. Adds tunable oversight,
async dialogue, style enforcement. This is what makes tillr more than
a logging tool. Many users stop here.

**The full consulting firm:** Stages 0-7. Full vision, including
context graph and synthesis. Big investment, big payoff for high-volume
agent shops.

**The nested-org tillr:** Stages 0-8. For orgs with multiple projects
needing cross-project coordination. Most users will never need this.

**Optional stages:** 6 (synthesis) is optional if PM is happy curating
manually (Stage 4 + Stage 3 style guide together cover most). 7
(metrics) is optional but strongly recommended for any team going past
Stage 3 — without it, configuration ratchets up forever.

---

## Where to start

If you're reading this fresh, the answer is **Stage 0 + Stage 1**.
Build the minimum adapter and ship Layer 1 + Layer 2. Validate for 4-6
weeks of real usage. Then come back and pick Stage 2.

For the narrative version of this from a persona perspective, see
[story 28 (Sasha)](./stories/28-sasha-building-the-mvp.md).

For the full architectural decomposition that this doc reorganizes,
see [implementation-layers.md](./implementation-layers.md).

For unresolved design questions per stage, see
[open-questions.md](./open-questions.md).

---

« [Consulting-firm overview](./README.md) · [Implementation layers](./implementation-layers.md) · [Open questions](./open-questions.md) · [All stories](./stories/README.md)
