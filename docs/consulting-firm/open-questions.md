# Open Questions

These are the unresolved design questions the consulting-firm proposal
surfaces. Each has a tentative lean; none are decided. When a question
gets resolved, fill in the **Answer:** block.

## 1. How do agents see new comments mid-implementation?

The agent is in a single LLM call when the PM comments. There's no
interrupt mechanism. Options: polling between phases, a pre-submit
check, or accepting that PM comments only take effect on the next
iteration.

_Leaning (original):_ Pre-submit check. Agent completes its current
approach, then checks for PM comments before submitting. If a PM
comment changes direction, the agent re-implements. Simpler than
polling, handles 90% of cases.

_Refinement from [story 24](./stories/24-questionnaires-as-checkpoints.md):_
Invert the direction. Instead of the PM racing to comment before the
agent submits, the agent **hard-blocks at cycle-template-defined
checkpoints** and surfaces structured answers. The PM acts when asked,
not when they notice. Pre-submit check remains as a fallback for PM
comments that arrive between checkpoints, but the primary oversight
mechanism becomes mandated pauses.

**Answer:**
> _(empty — fill in when decided)_

## 2. Real-time or batch comment processing?

Should agents poll for comments during implementation, or only at
submission boundaries?

_Leaning:_ Batch (check before submit). Real-time adds complexity
for marginal benefit. The PM can always reject and redirect if the
pre-submit check isn't fast enough.

**Answer:**
> _(empty — fill in when decided)_

## 3. Comment format: structured or freeform?

Structured comments (JSON with fields for decision, rationale,
references) are machine-parseable but feel robotic. Freeform is
natural but harder to extract decisions from.

_Leaning:_ Agents write structured comments that render as natural
prose. Metadata is there but hidden from the PM's view. PM comments
are always freeform — forcing structure on the PM is hostile.

**Answer:**
> _(empty — fill in when decided)_

## 4. PR comments vs feature comments

PR comments (on the diff) are about code. Feature comments (on the
ticket) are about decisions and coordination. Should they be the same
table with different entity_types, or separate systems?

_Leaning:_ Same table, different entity_type. The data model is
identical (author, body, metadata, timestamp). Code review is just
another conversation on the feature.

**Answer:**
> _(empty — fill in when decided)_

## 5. Token budget for context packets

A feature touching auth might surface 15 related features with 60
comments and 8 decisions. The full graph traversal could be 20k tokens.

_Leaning:_ Hard budget of 3k tokens for context packets. Priority:
direct constraints > PM guidance > reviewer patterns > related
feature comments > historical decisions. Summarize aggressively.
Agent can request "more context on X" via tillr MCP tool if needed.

**Answer:**
> _(empty — fill in when decided)_

## 6. Knowledge synthesis freshness

How often to regenerate synthesized project knowledge? Every new
review? Daily? On demand?

_Leaning:_ Cache the synthesis, invalidate when a new review lands,
regenerate lazily on next claim. This balances freshness with cost.

**Answer:**
> _(empty — fill in when decided)_

## 7. First-in-domain detection

How does tillr know an agent is entering a new domain? By new package
creation? New tags? File-level analysis?

_Leaning:_ Tag-based initially (if no prior features share tags with
this one, flag it). Add file-level detection later (if the feature
creates new directories, that's a domain boundary). Both are
heuristics — accept false positives over false negatives.

**Answer:**
> _(empty — fill in when decided)_

## 8. Graph blind spot: code-level dependencies

[Story 23](./stories/23-context-graph-failure.md) shows features that
share a database table but have no tag, comment, or declaration
connecting them. Should the graph analyze code?

_Leaning:_ Yes, but carefully. Start with SQL table references
(detectable from migration files and query patterns). Skip generic
shared files (go.sum, package.json). Add git-history analysis later
("these features both modified internal/db/events.go").

**Answer:**
> _(empty — fill in when decided)_

## 9. Agent graph queries vs static packets

Should agents query the graph during implementation ("what other
features touch this table?") via tillr MCP tools, instead of receiving
a pre-assembled packet?

_Leaning:_ Both. Static packet for initial context, MCP tool for
on-demand queries. Real engineers don't just read a briefing — they
search JIRA, ask teammates, look things up mid-task.

**Answer:**
> _(empty — fill in when decided)_

## 10. Does the context graph replace CLAUDE.md/AGENTS.md?

Static markdown files are today's agent context. The graph subsumes
most of what they do.

_Leaning:_ Coexist. Static files for universal truths (build
commands, repo structure, tool setup). Graph for dynamic context
(patterns, decisions, PM preferences). The graph eventually makes
static files thinner, but doesn't eliminate them.

**Answer:**
> _(empty — fill in when decided)_

## 11. Philosophy scope and granularity

How many philosophies is too many? If the project has 20, the context
packet bloats.

_Leaning:_ Enforce a soft limit of 5 with a warning. Allow
domain-scoped philosophies ("frontend philosophy" only applies to
frontend-tagged features) to keep packets focused.

**Answer:**
> _(empty — fill in when decided)_

## 12. PR merge ordering across types

If a philosophy PR and a code PR are both pending, and the code PR
was built under the old philosophy, what happens?

_Leaning:_ Philosophy PRs merge first. Affected code PRs get a
"philosophy changed" warning and go back for review. Strategic
changes take precedence over tactical ones.

**Answer:**
> _(empty — fill in when decided)_

## 13. Estimation confidence and feedback loop

How do we validate estimates? If estimated 40 min but took 90, how
does the model learn?

_Leaning:_ Track estimated vs actual for every feature. After 20+
features, report calibration: "estimates are 1.3x optimistic on
average." Adjust future estimates by the calibration factor. Show
confidence intervals based on number of similar features.

**Answer:**
> _(empty — fill in when decided)_

## 14. Retro frequency

Biweekly? Event-triggered (10 features completed, rejection rate
spike)?

_Leaning:_ Both. Scheduled biweekly for routine analysis.
Event-triggered when a metric crosses a threshold (rejection rate
>30%, 3 rejections in a row). Event-triggered retros are scoped to
the triggering metric, not full analysis.

**Answer:**
> _(empty — fill in when decided)_

## 15. Domain maturity signal

When should agents stop writing tutorials and just follow existing
patterns?

_Leaning:_ After N features (maybe 5) confirm the domain patterns
without changes, mark the domain as "mature." Context packet says
"well-documented domain — follow existing patterns, don't
re-document." Agents still flag if they discover something new.

**Answer:**
> _(empty — fill in when decided)_

## 16. User stories as specs

Should PMs write narrative user stories (per the skill format) instead
of bullet-point specs? Stories give agents richer context: who the
user is, what the workflow looks like, where the gaps and uncertainty
are.

_Leaning:_ Yes. User stories are strictly better input for agents
than bullet specs. The narrative format surfaces edge cases, the
gap callouts tell agents where to use judgment, and the persona
grounding prevents agents from building for abstract "users."
See the "Stories as Specs" section in the consulting firm design
doc.

**Answer:**
> _(empty — fill in when decided)_

---

## Questions raised by questionnaires (story 24)

The questionnaire mechanism in
[story 24](./stories/24-questionnaires-as-checkpoints.md) is a new
proposal. These are its specific open questions.

## 17. Questionnaire scope

Do questionnaires live on cycle templates (per type), on workstreams
(per area of the codebase), on features (per-feature overrides), or
all three with layered resolution?

_Leaning:_ Primary home is the cycle template (versioned, reviewable
via Cycle Template PRs). Per-feature override allowed for "downgrade
to standard even though the tag says high-risk," logged in audit
trail. Workstream-level defaults deferred until there's demand.

**Answer:**
> _(empty — fill in when decided)_

## 18. Blocking semantics default

Should `hard_block` (agent idle until PM approves) or `soft_block`
(auto-proceed after N minutes) be the default?

_Leaning:_ Hard-block by default. Soft-block is an explicit opt-in
per question. Rationale: defaults that can auto-proceed past PM
oversight create surprise when the PM is away. Better to have the
agent stall loudly than silently move forward. The dial is in the
PM's hands.

**Answer:**
> _(empty — fill in when decided)_

## 19. Checkpoint trigger: fixed or conditional?

Should checkpoints fire at fixed cycle-step boundaries (post-claim,
pre-submit), or should they also support conditional triggers
("agent proposes a new external dependency", "feature touches
`internal/billing/**`")?

_Leaning:_ Both, layered. Fixed points are the baseline. Conditional
triggers add targeted oversight without bloating the default
questionnaire. Start with file-glob conditions; add "agent declares
X" conditions once we see which declarations matter.

**Answer:**
> _(empty — fill in when decided)_

## 20. Answer format schema

Are questions free-form text, or do they support structured types
(short text, prose, yes/no + justification, list, multiple choice)?

_Leaning:_ A small fixed schema of ~5 question types, with the PM
authoring specific questions of each type. Rich enough to shape
answers (e.g., "yes/no + justification if no" forces rationale on
the risky answer). Simple enough that an agent can reliably produce
well-formed responses.

**Answer:**
> _(empty — fill in when decided)_

## 21. Question-quality feedback loop

PM-authored questionnaires can become bureaucracy. A well-meaning
question that never produces an intervention is pure friction. How
do we keep the dial tunable *down* as well as up?

_Leaning:_ The retro agent (story 19) analyzes questionnaires and
flags ones that have been answered N times with no PM intervention,
proposing removal via Cycle Template PR. Track "PM acted on this
answer (rejected, commented, requested changes)" as the signal;
high intervention rate = keep, zero = prune candidate.

**Answer:**
> _(empty — fill in when decided)_

## 22. Answer drift between checkpoints

Post-claim answers ("no new dependencies") can contradict pre-submit
reality ("added github.com/foo/bar"). Should the system detect and
surface this automatically?

_Leaning:_ Yes. Pre-submit checkpoint should render prior answers
inline, with deltas highlighted for any question that has a
mechanical correctness check (dependencies added, files touched,
estimate drift). This makes drift visible without the PM having to
hold both states in their head.

**Answer:**
> _(empty — fill in when decided)_

## 23. Checkpoint inbox prioritization

If 3 agents hit hard-block checkpoints at once, the PM has a queue.
How do we surface the right one first?

_Leaning:_ Inbox sorts blocked checkpoints by wall-clock time lost
to blocking (oldest first), weighted by cycle priority. Show
aggregate "agent time stalled today" so the PM can decide whether
to relax semantics or spend more PM time.

**Answer:**
> _(empty — fill in when decided)_

---

## Questions raised by the style enforcer (story 25)

The style guide + async reviewer dialogue mechanism in
[story 25](./stories/25-style-enforcer-async-dialogue.md) raises its
own design questions.

## 24. Style rule format and required examples

What's the minimum viable shape of a style rule? Pure prose, or are
invalid/valid code examples mandatory?

_Leaning:_ Examples are **required** for `blocking` severity rules,
**recommended** for `requires-justification`, **optional** for
`advisory`. Prose alone is too ambiguous for an enforcer to apply
consistently — the example pair is what makes the rule
machine-applicable. Tillr should refuse to create a `blocking` rule
without an example pair.

**Answer:**
> _(empty — fill in when decided)_

## 25. Style rule conflicts

Two rules might apply to the same code with opposing verdicts. How
do we resolve?

_Leaning:_ Explicit `priority` field on each rule (integer, higher
wins). On conflict, the higher-priority rule applies and the lower
is logged as "shadowed by rule X" in the enforcer's comment. PM can
re-prioritize via style-rule PR.

**Answer:**
> _(empty — fill in when decided)_

## 26. Justification erosion / weak-justification bar

If implementers can justify any violation with thin reasoning, rules
become decorative. Where's the bar, and who calibrates it?

_Leaning:_ The retro agent (story 19) tracks acceptance rate per rule.
Rules where >50% of justifications are accepted are flagged as
"miscalibrated — consider tightening or splitting." The enforcer
itself doesn't have a hard threshold; it accepts well-reasoned
justifications and the system learns from outcomes.

**Answer:**
> _(empty — fill in when decided)_

## 27. Stall detection and PM escalation

Long enforcer ↔ implementer loops can stall a PR for days. When does
the PM get pulled in?

_Leaning:_ After N rounds (default 3) without resolution, escalate
to PM. Surface "PRs in style-review for >24h" prominently. The PM
can resolve, or amend the rule, or accept the implementer's stance
and override the enforcer.

**Answer:**
> _(empty — fill in when decided)_

## 28. Rule application date semantics

When a new rule is added, does it retroactively flag in-flight PRs?

_Leaning:_ No. Rules apply to PRs that *enter* style-review after the
rule's `created_at`. PRs already past style-review are not reopened.
This avoids breaking work in flight; the next PR picks up the new
rule naturally.

**Answer:**
> _(empty — fill in when decided)_

## 29. Style guide bloat and domain scoping

With 50 rules, the enforcer's envelope is large and many rules don't
apply to most diffs. How do we keep envelopes focused?

_Leaning:_ Per-rule `scope` field (file globs and/or tags). Enforcer
loads only the rules whose scope matches the diff. Default scope is
"all files" if unspecified. PM can refactor a too-broad rule into
narrower scoped rules via style-rule PR.

**Answer:**
> _(empty — fill in when decided)_

## 30. Reviewer-implementer loop iteration limit

Pure async dialogue can loop forever. Should the cycle engine cap
iterations per step?

_Leaning:_ Soft cap (warning) at 3 iterations, hard cap (PM escalation)
at 5. Track per-rule iteration counts in the retro report — rules
that consistently trigger many rounds are signals of mis-scope.

**Answer:**
> _(empty — fill in when decided)_

---

« [Consulting-firm overview](./README.md) · [Implementation layers](./implementation-layers.md) · [All stories](./stories/README.md)
