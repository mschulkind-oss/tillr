# 25. Henry — Style Guide Enforcement and Async Reviewer Dialogue

**Context:** Henry is a staff engineer who's been on his project for four
years. He's done hundreds of code reviews and he's tired of explaining
the same things over and over: don't catch errors and silently return a
default; don't hand-roll JSON encoding when the helper exists; don't
reach into another package's private types via reflection. He wants
these encoded as **enforceable rules** — strict enough to push back when
an agent tries to cut corners, with a defined process for amending the
rule when an exception is genuine.

He doesn't want a synchronous gate that blocks the agent until he
personally weighs in. He wants this to feel like a real engineering
org: comments land on a PR, the author addresses them whenever they
come back, the reviewer re-checks later, the loop converges, the merge
queue takes it. Email pace, not chat pace.

**What happens today:**

PRs land in human-qa. Henry reads them. He sees the same shapes recur
across different agents — agent #41 catches an error and returns nil;
agent #44 does the same on a different file two days later. Henry
comments on both. The pattern doesn't propagate.

Knowledge synthesis (see [story 8](./08-ava-knowledge-synthesis.md))
helps eventually — after enough rejections, "no silent fallbacks"
shows up in the synthesized anti-patterns brief. But it's *reactive*
and *vague*: "anti-pattern: fallbacks" is one line in a packet the
agent might or might not weight. Henry wants *proactive* and
*specific*: a named rule, examples of valid and invalid code,
blocking enforcement.

He's also seen the synthesis brief get blamed for issues — "the brief
said no global state, so I rejected your `init()` data" — when the
real intent was narrower. Curated rules with examples beat synthesized
prose.

**What happens with the style enforcer:**

1. Henry authors style rules:

   ```
   tillr style add no-silent-fallbacks \
     --severity blocking \
     --description "Don't catch an error and return a default/zero value
                   silently. Surface real failures. Exception: documented
                   bootstrap paths where empty state is expected." \
     --invalid-example examples/no-silent-fallbacks/invalid.go \
     --valid-example   examples/no-silent-fallbacks/valid.go

   tillr style add use-existing-json-helpers \
     --severity requires-justification \
     --description "Use internal/json.Marshal/Unmarshal — they handle
                   the project's standard error wrapping and the
                   custom time format. Don't call encoding/json
                   directly except in the helper itself."

   tillr style add no-cross-package-private-access \
     --severity blocking \
     --description "Don't use reflection or unsafe to access another
                   package's unexported fields. Add a public accessor
                   instead."
   ```

   Severities:
   - `blocking` — PR cannot merge until the violation is removed.
   - `requires-justification` — agent must justify in a comment;
     enforcer accepts (and the rule may be amended) or rejects.
   - `advisory` — logged on the PR, doesn't block.

2. Henry adds a `style-review` step to the cycle template, between
   `implement` and `code-review`:

   ```
   tillr cycle template edit standard
     steps: implement → style-review → code-review → human-qa → merge-queue
   ```

3. Agent claims `add-feature-toggle` and implements. Cycle advances to
   `style-review`. The cycle engine invokes the enforcer with a
   **focused context envelope**:

   ```
   StyleEnforcer envelope:
     - the diff (only)
     - the style guide (rules + descriptions + examples)
     - prior style-review thread on this PR (empty on first pass)
   ```

   No project history. No philosophies. No related features. The
   enforcer doesn't need them and they'd waste tokens.

4. Enforcer reviews and posts structured comments per finding:

   ```
   [StyleEnforcer - 11:00am, on PR #88]
   Violation: no-silent-fallbacks  (internal/feature/toggle.go:42)
     if err := db.Get(...); err != nil {
         return defaultConfig    // silent fallback
     }
   Severity: blocking.
   Required: remove the fallback (return the error) OR justify why this
   case is an exception (filing a style-rule PR if so).

   Violation: use-existing-json-helpers  (internal/feature/toggle.go:71)
     json.Marshal(toggleSet)        // direct encoding/json call
   Severity: requires-justification.
   Required: switch to internal/json.Marshal, or justify.
   ```

   Cycle state moves to `style-review-pending-author-response`. The
   enforcer's job is done; it's now in the implementer's inbox.

5. **Async loop, email-pace.** The implementing agent isn't watching
   in real-time. It's running other features, or idle, or queued
   behind something else. At its next invocation — could be 2 minutes
   later, could be next morning — it picks up its inbox, sees PR #88
   has style-review comments to address, and responds:

   ```
   [Implementer - 11:14am, on PR #88]
   Re: no-silent-fallbacks at internal/feature/toggle.go:42
   Fixed. Removed the fallback. Errors now propagate to the caller,
   which has explicit handling for "no toggle store yet."

   Re: use-existing-json-helpers at internal/feature/toggle.go:71
   Justification: this code path runs before internal/json is
   initialized (it's the bootstrap loader for the json package's own
   config). Using internal/json here would create an init cycle.
   Proposing rule exception: "use-existing-json-helpers does not apply
   inside internal/json/bootstrap.go". Filing as style-rule PR.
   ```

   Cycle state moves to `style-review-pending-reviewer-response`. Now
   it's the enforcer's inbox.

6. **Loop again.** Enforcer next invocation: re-reads the diff, the
   updated thread. Two paths per finding:

   - Accept the change / justification: comment, mark resolved.
   - Reject: comment with reason, state stays `pending-author-response`.

   ```
   [StyleEnforcer - 11:30am, on PR #88]
   Re: no-silent-fallbacks — fix verified. Resolved.
   Re: use-existing-json-helpers — justification accepted (init cycle
   is a real concern). Approving this PR pending merge of style-rule
   PR #STYLE-12. If that PR is rejected, this finding re-opens.

   All findings resolved. Moving to code-review.
   ```

7. Cycle advances. Code-review runs (with its own envelope, no style
   guide), then human-qa, then merge queue. Henry sees the PR in his
   human-qa inbox with the style-review thread already converged. He
   sees the style-rule PR #STYLE-12 in his "process changes" inbox
   alongside any philosophy or cycle template PRs.

   He approves #STYLE-12 (the init-cycle exception is reasonable). The
   no-silent-fallbacks rule now has a documented carve-out — future
   agents in `internal/json/bootstrap.go` won't get flagged.

**Inbox-as-coordination model.** Each agent role has its own queue.
The cycle engine routes work; agents just process whatever's in their
inbox.

| Role | Inbox contents |
|------|---------------|
| Implementer | features in `draft-ready`, `style-review-pending-author-response`, `code-review-pending-author-response`, `human-qa-rejected` |
| Style enforcer | features in `style-review` (first pass) and `style-review-pending-reviewer-response` |
| Code reviewer | features in `code-review` and `code-review-pending-reviewer-response` |
| Henry (PM) | PRs in `human-qa`, style-rule PRs, philosophy PRs, cycle template PRs |

No agent ever coordinates with another directly. The substrate is
comments + cycle state. This is exactly how a real engineering org
handles a PR — Slack pings optional, the PR thread is authoritative.

**Agent-platform delta — much smaller than originally assumed.** Both
GitHub Copilot's cloud agent and Claude's agent infrastructure now
support everything this story needs:

- **Custom agent definitions in repo.** Copilot uses `.github/agents/`
  files; Claude uses `.claude/agents/` files. Same content shape
  ([source](https://github.blog/ai-and-ml/github-copilot/whats-new-with-github-copilot-coding-agent/)).
- **Per-task model selection.** Copilot has a model picker in the
  Agents panel; Claude SDK accepts a model parameter per invocation.
  A cycle-template field "model: opus / sonnet / auto" works for both
  ([source](https://github.blog/changelog/2026-02-11-github-mobile-model-picker-for-copilot-coding-agent/)).
- **Async / background execution.** Copilot explicitly: "Copilot works
  asynchronously — so by the time you check back in, there's a plan to
  review, code to look at, or a PR ready to merge"
  ([source](https://docs.github.com/copilot/concepts/agents/coding-agent/about-coding-agent)).
  Claude: SDK invocations are stateless and resumable.
- **Mid-session follow-up messages.** Copilot accepts follow-ups in the
  session log; "Copilot implements your input after it finishes its
  current tool call" — queued at the next tool boundary, not preemptive
  ([source](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/manage-agents)).
  This matches the questionnaire-checkpoint model from
  [story 24](./24-questionnaires-as-checkpoints.md) and resolves
  open question #1 the same way for both platforms.

What this means for tillr: **the protocol is universal**. Tillr defines
cycle steps, context envelopes, comment artifacts, status transitions,
and a small set of agent-role files. The platform-specific code is a
thin invocation adapter (call `gh copilot` vs. the Claude SDK, poll for
status, surface results into tillr comments). No platform-specific
features required for the consulting-firm vision.

**Gap: rule format must include examples.** Prose like "no silent
fallbacks" is ambiguous. The enforcer and the implementer will disagree
on edge cases without concrete invalid/valid code samples per rule.
Tillr should require an example pair for any `blocking` severity rule.

**Gap: rule conflicts.** Two rules might apply to the same code with
opposing verdicts. Need a precedence model (last-added wins? explicit
priority field? PM resolves at conflict time?).

**Gap: weak-justification erosion.** An implementer might "justify"
a violation with thin reasoning ("this case is special") to skip the
work. The enforcer needs a calibrated bar for accepting justifications.
The retro agent ([story 19](./19-jake-automated-retrospective.md))
should track "rules where >50% of justifications were accepted" — that's
a signal the rule is mis-scoped or the bar is too low. Without this
loop, rules degrade via easy exceptions.

**Gap: stall detection.** Long enforcer ↔ implementer loops can stall
a PR for days. After N rounds, escalate to the PM. Surface "PRs in
style-review for >24h" prominently in Henry's inbox.

**Gap: rule introduction date.** When Henry adds a rule, it shouldn't
retroactively flag in-flight PRs that were already past style-review.
Rules apply to PRs that *enter* style-review after the rule's creation.
PRs already in later stages aren't reopened.

**What would trip Henry up:**

- **Authoring rules well is hard.** His first ten rules will get
  refined through use — agents will hit edge cases he didn't anticipate
  and propose exceptions. He should expect a "rule debugging" period.
- **Too strict and the queue jams.** If every PR loops 5+ times in
  style-review, agents are doing more arguing than coding. Need
  observability: "average style-review iterations per PR" as a metric
  in the retro report.
- **Too lenient and the rules become decorative.** If the enforcer
  rubber-stamps every justification, the rules don't prevent the
  issues Henry was tired of. The retro agent should track "issues
  Henry caught at human-qa that the enforcer missed" — those are the
  training signals for tightening rules.
- **Style guide bloat.** With 50 rules, the enforcer's envelope is
  large and many rules are dead weight on most PRs. Consider
  domain-scoped rules (per file glob, per tag) so the envelope only
  loads what applies to the current diff.

**What makes this work:**

- **Async dialogue mirrors a real engineering org.** Comments persist;
  agents process inboxes when they run; state machine handles routing.
  No special sync-coordination primitive needed. The substrate is
  Layer 1 (comments) + cycle-state extensions.
- **Style guide is curated, distinct from synthesized anti-patterns.**
  Both coexist — the synthesized brief catches things the PM hasn't
  thought to encode; the style guide enforces the things they have.
- **Rules evolve through PRs.** Same shape as philosophy PRs
  ([story 12](./12-nora-philosophy-evolution.md)). Universal PR pipeline
  ([Layer 9](../implementation-layers.md)) absorbs style-rule PRs
  naturally.
- **Per-step context envelopes scale.** The enforcer doesn't carry
  project history; the implementer doesn't carry the full style guide
  while writing code. Each agent loads exactly what its job needs.
- **Agent-agnostic at the protocol level.** Both Claude and Copilot
  support custom agents, model picking, async execution, and
  follow-up messages. The platform delta is two filenames and two
  invocation adapters.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
