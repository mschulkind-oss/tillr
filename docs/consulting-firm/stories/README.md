# Consulting Firm — User Stories

31 narrative stories exploring how tillr behaves as a consulting firm
(client = user, firm = tillr, agents = engineering team) with a
director layer (story 26) and the post-reset conductor + persona
foundation (stories 30, 31). Each story follows a named persona
through a concrete workflow and calls out the gaps we'd need to
close.

Use these as design input: the persona's **Gap:** and
**What would trip them up** callouts are where the next round of
thinking is needed.

## Shape of a story

Each file uses the same five-subsection template:

1. **Context** — who the persona is, their situation, their time budget
2. **What happens today** — the current (broken / friction-heavy) flow
3. **What happens with [the proposed layer]** — the desired flow
4. **What would trip them up** — adversarial / edge cases
5. **What makes this work** — the load-bearing mechanics

Many stories also call out a **Gap:** inline — an unresolved
sub-question inside an otherwise-worked-through scenario. Some stories
have a **Resolution** footer added later, pointing to a subsequent
story or layer that solves their gap.

## How this maps to the roadmap

The table below is **sorted by stage** (see [roadmap.md](../roadmap.md))
so reading top-to-bottom shows the evolution. Earlier stages enable
earlier stories; later stages unlock more complex scenarios. The story
**file numbers** are NOT in stage order — they're historical, in the
order each was written. The Stage column is what to read by.

Returning personas (Marcus, Sana, Nora, Jake, Wei, Priya) appear
multiple times across stages as their needs evolve.

## Index

| Stage | # | File | Persona | Theme | Layers needed |
|-------|---|------|---------|-------|---------------|
| 0 (MVP / Foundational) | 30 | [30-rui-conductor-pattern](./30-rui-conductor-pattern.md) | Rui (solo dev) | Conductor + persona lifecycle; swarf context files; retro | conductor pattern |
| 0 (MVP / Foundational) | 31 | [31-yael-tui-primary-interface](./31-yael-tui-primary-interface.md) | Yael (terminal-native) | TUI as primary interface for inspection | TUI |
| (deferred — post-MVP) | 29 | [29-anders-platform-adapter](./29-anders-platform-adapter.md) | Anders (platform engineer) | Claude + Copilot multi-platform — *MVP is Claude-only* | adapter |
| 1 (Make agents legible) | 22 | [22-derek-progressive-disclosure](./22-derek-progressive-disclosure.md) | Derek (minimalist) | Layer 1 alone (just comments) is valuable | 1 |
| 1 | 1 | [01-priya-solo-pm](./01-priya-solo-pm.md) | Priya (solo PM) | Decisions summary in inbox | 1, 2 |
| 1 | 3 | [03-sana-interdependent-features](./03-sana-interdependent-features.md) | Sana (design lead) | Cross-feature comments — works but has timing race; resolved in Stage 2 (story 27) | 1, 2 |
| 2 (Close the human loop) | 28 | [28-sasha-building-the-mvp](./28-sasha-building-the-mvp.md) | Sasha (tech lead) | **Meta**: how to actually adopt this stage by stage | meta |
| 2 | 2 | [02-marcus-mid-flight-steering](./02-marcus-mid-flight-steering.md) | Marcus (opinionated PM) | PM comments while agent implements | 1, 2, 4 |
| 2 | 5 | [05-rachel-nontechnical-pm](./05-rachel-nontechnical-pm.md) | Rachel (non-technical PM) | Delegate design discussion, approve recommendation | 1, 2, 4, 4b |
| 2 | 27 | [27-sana-cross-feature-coordination-resolved](./27-sana-cross-feature-coordination-resolved.md) | Sana (returning) | Story 3's race resolved via async cycle states | 1, 4b |
| 3 (Enforced quality gates) | 25 | [25-style-enforcer-async-dialogue](./25-style-enforcer-async-dialogue.md) | Henry (staff engineer) | Style guide + enforcer agent + async dialogue | 1, 4b, 9b |
| 3 | 24 | [24-questionnaires-as-checkpoints](./24-questionnaires-as-checkpoints.md) | Meera (tunable oversight) | Cycle-template questionnaires as checkpoints | 1, 2, 2b, 4b |
| 3 | 12 | [12-nora-philosophy-evolution](./12-nora-philosophy-evolution.md) | Nora (week 8) | Philosophy amendment via PR | 1, 4b, 8, 9 |
| 3 | 13 | [13-jake-cycle-template-change](./13-jake-cycle-template-change.md) | Jake (process) | Cycle template PR adds design-review step | 1, 9, 10 |
| 3 | 14 | [14-wei-knowledge-pr-review](./14-wei-knowledge-pr-review.md) | Wei (knowledge review) | Knowledge PR: update / add / remove patterns | 1, 7, 9 |
| 3 | 15 | [15-carlos-incident-response](./15-carlos-incident-response.md) | Carlos (incident) | Accelerated diagnose→fix→verify cycle | 1, 9 |
| 3 | 18 | [18-sana-specialization](./18-sana-specialization.md) | Sana (returning) | Specialized frontend-engineer role | adapter, 7 |
| 4 (Memory and search) | 4 | [04-diego-new-engineer-onboarding](./04-diego-new-engineer-onboarding.md) | Diego (new engineer) | Search comment history for old decisions | 1, 5 |
| 4 | 6 | [06-tom-pm-handoff](./06-tom-pm-handoff.md) | Tom (new PM) | Onboard via decision search + philosophies | 1, 5, 8 |
| 4 | 11 | [11-nora-philosophies-day-one](./11-nora-philosophies-day-one.md) | Nora (first-time user) | Day-one philosophies shape first feature | 1, 8 |
| 5 (Context graph) | 7 | [07-kai-context-packet](./07-kai-context-packet.md) | Kai (power user) | The context packet in action | 1, 6 |
| 5 | 9 | [09-lin-new-domain-onboarding](./09-lin-new-domain-onboarding.md) | Lin (new domain) | Domain notes become next agent's onboarding | 1, 6, 7 |
| 5 | 10 | [10-omar-dependency-graph](./10-omar-dependency-graph.md) | Omar (milestone planning) | Dependency graph with discovered edges | 1, 6 |
| 5 | 21 | [21-agent-implementer-perspective](./21-agent-implementer-perspective.md) | Agent-1 (implementer) | The claim-response JSON contract | 6 |
| 5 | 23 | [23-context-graph-failure](./23-context-graph-failure.md) | — (failure case) | Graph blind spot: shared DB table, no comment/tag | 6 (gap) |
| 6 (Knowledge synthesis) | 8 | [08-ava-knowledge-synthesis](./08-ava-knowledge-synthesis.md) | Ava (mature project) | Project knowledge brief from review history | 1, 5, 7 |
| 7 (Process improvement) | 16 | [16-priya-tech-debt-dashboard](./16-priya-tech-debt-dashboard.md) | Priya (returning) | Debt dashboard from `tech-debt` tag | 1, 10 |
| 7 | 17 | [17-marcus-estimation-error](./17-marcus-estimation-error.md) | Marcus (returning) | 100x estimation error, history-based fix | 1, 10 |
| 7 | 19 | [19-jake-automated-retrospective](./19-jake-automated-retrospective.md) | Jake (returning) | Biweekly retro produces 3 PRs | 1, 9, 10 |
| 7 | 20 | [20-wei-stakeholder-report](./20-wei-stakeholder-report.md) | Wei (returning) | Stakeholder report generated on demand | 1, 5, 7, 10 |
| 8 (Hierarchy — far future) | 26 | [26-olivia-director-hierarchy](./26-olivia-director-hierarchy.md) | Olivia (director) | Director / nested PM tree; cross-project coord | 11 (new) |

See also:
- [MVP](../mvp.md) — the post-reset shipping plan (1-2 weeks of focused build)
- [Roadmap](../roadmap.md) — staged long-term ordering with risk and validation per stage
- [Implementation layers](../implementation-layers.md) — architectural decomposition
- [Open questions](../open-questions.md) — 51 unresolved design questions
- [Overview](../README.md) — the thesis and reading order
