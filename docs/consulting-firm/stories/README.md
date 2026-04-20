# Consulting Firm — User Stories

24 narrative stories exploring how tillr behaves when it's modelled as
a consulting firm (client = user, firm = tillr, agents = engineering
team). Each story follows a named persona through a concrete workflow
and calls out the gaps we'd need to close.

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
sub-question inside an otherwise-worked-through scenario.

## Index

| # | File | Persona | Theme | Layer emphasized |
|---|------|---------|-------|------------------|
| 1 | [01-priya-solo-pm](./01-priya-solo-pm.md) | Priya (solo PM) | Decisions summary in inbox | 1, 2, 5 |
| 2 | [02-marcus-mid-flight-steering](./02-marcus-mid-flight-steering.md) | Marcus (opinionated PM) | PM comments while agent implements | 4 |
| 3 | [03-sana-interdependent-features](./03-sana-interdependent-features.md) | Sana (design lead) | Two agents coordinate via cross-feature comments | 3 |
| 4 | [04-diego-new-engineer-onboarding](./04-diego-new-engineer-onboarding.md) | Diego (new engineer) | Search comment history for old decisions | 5 |
| 5 | [05-rachel-nontechnical-pm](./05-rachel-nontechnical-pm.md) | Rachel (non-technical PM) | Delegate design discussion, approve recommendation | 2, 4 |
| 6 | [06-tom-pm-handoff](./06-tom-pm-handoff.md) | Tom (new PM) | Onboard via decision search + philosophies | 5, 8 |
| 7 | [07-kai-context-packet](./07-kai-context-packet.md) | Kai (power user) | The context packet in action | 6 |
| 8 | [08-ava-knowledge-synthesis](./08-ava-knowledge-synthesis.md) | Ava (mature project) | Project knowledge brief from review history | 7 |
| 9 | [09-lin-new-domain-onboarding](./09-lin-new-domain-onboarding.md) | Lin (new domain) | Domain notes become the next agent's onboarding | 7 |
| 10 | [10-omar-dependency-graph](./10-omar-dependency-graph.md) | Omar (milestone planning) | Dependency graph with discovered edges | 6 |
| 11 | [11-nora-philosophies-day-one](./11-nora-philosophies-day-one.md) | Nora (first-time user) | Day-one philosophies shape first feature | 8 |
| 12 | [12-nora-philosophy-evolution](./12-nora-philosophy-evolution.md) | Nora (week 8) | Philosophy amendment via PR | 8, 9 |
| 13 | [13-jake-cycle-template-change](./13-jake-cycle-template-change.md) | Jake (process) | Cycle template PR adds design-review step | 9, 10 |
| 14 | [14-wei-knowledge-pr-review](./14-wei-knowledge-pr-review.md) | Wei (knowledge review) | Knowledge PR: update / add / remove patterns | 7, 9 |
| 15 | [15-carlos-incident-response](./15-carlos-incident-response.md) | Carlos (incident) | Accelerated diagnose→fix→verify cycle | 9 |
| 16 | [16-priya-tech-debt-dashboard](./16-priya-tech-debt-dashboard.md) | Priya (tech debt) | Debt dashboard from `tech-debt` tag | 10 |
| 17 | [17-marcus-estimation-error](./17-marcus-estimation-error.md) | Marcus (estimation) | 100x estimation error, history-based fix | 10 |
| 18 | [18-sana-specialization](./18-sana-specialization.md) | Sana (roles) | Specialized frontend-engineer role | 2, 7 |
| 19 | [19-jake-automated-retrospective](./19-jake-automated-retrospective.md) | Jake (retro) | Biweekly retro produces 3 PRs | 10, 9 |
| 20 | [20-wei-stakeholder-report](./20-wei-stakeholder-report.md) | Wei (report) | Stakeholder report generated on demand | 10 |
| 21 | [21-agent-implementer-perspective](./21-agent-implementer-perspective.md) | Agent-1 (implementer) | The claim-response JSON contract | 6, 2 |
| 22 | [22-derek-progressive-disclosure](./22-derek-progressive-disclosure.md) | Derek (minimalist) | Layer 1 alone (just comments) is valuable | 1 |
| 23 | [23-context-graph-failure](./23-context-graph-failure.md) | — (failure case) | Graph blind spot: shared DB table, no comment/tag | 6 (gap) |
| 24 | [24-questionnaires-as-checkpoints](./24-questionnaires-as-checkpoints.md) | Meera (tunable oversight) | Cycle-template questionnaires as enforced mid-flight checkpoints | 2, 9, 10 |

See also:
- [Implementation layers](../implementation-layers.md) — the 10-layer roadmap
- [Open questions](../open-questions.md) — 16 unresolved design questions
- [Overview](../README.md) — the thesis and reading order
