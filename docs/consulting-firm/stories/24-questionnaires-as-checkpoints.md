# 24. Meera — Tunable Oversight via Cycle Questionnaires

**Context:** Meera runs a project with a heterogeneous workload. Half the
queue is routine — type fixes, copy changes, small refactors, import
re-ordering. The other half is high-stakes — auth changes, billing
logic, data migrations, anything customer-facing. She's tired of
reviewing a string-replacement feature with the same scrutiny as a
payment provider swap, and she's tired of worrying that something
consequential slipped through a cursory review. She wants a dial, not
a switch.

**What happens today:**

Every feature flows through the same cycle (implement → review →
human-qa). The agent might comment on decisions, but it's *soft* —
governed by "we ask agents to explain their reasoning." For a routine
CSS tweak, that's fine. For a billing change, Meera often finds, deep
in a 15-comment thread, a decision she'd have rejected — "used a new
env var `STRIPE_TEST_MODE` for the feature flag." She only catches it
because she happened to read every comment. Twice last month, she
didn't.

Her alternatives today are both bad:

- **Over-review:** scrutinize every feature regardless of risk. Eats
  her 2-3 PM hours, slows the queue, and makes the routine work feel
  adversarial.
- **Under-review:** skim everything. Fast, but consequential decisions
  get buried in prose. This is how she shipped the `STRIPE_TEST_MODE`
  regression.

Neither scales. She needs *different features* to get *different depths
of oversight*, and she needs it enforced by the system — not by her
willpower to remember to "pay more attention" to the billing work.

**What happens with cycle questionnaires:**

1. Meera defines two cycle templates, each with a questionnaire
   attached at specific steps:

   ```
   tillr cycle template edit standard
     # one checkpoint, pre-submit
     [pre-submit]
       - Summary of changes (required, short text)
       - Test plan (required, short text)

   tillr cycle template edit high-risk
     # three checkpoints, hard-block
     [post-claim]
       - Approach (required, prose)
       - Alternatives considered + why rejected (required, prose ≥2 options)
       - New dependencies introduced (required, list; empty is valid)
       - Affects authentication / billing / data integrity? (required, yes/no
         + justification if no)
     [pre-submit]
       - What changed vs original approach? (required; "nothing" is valid)
       - Risks remaining (required, prose)
       - Test evidence (required, link or command output)
     [post-review]
       - Reviewer feedback: adopted / deferred / ignored (required per item,
         one row per reviewer comment)
   ```

   She tags routine features with `standard` and high-stakes ones with
   `high-risk`. Tag assignment can be manual or via simple rules
   (`#billing` → `high-risk`, etc.).

2. Agent claims `fix-invoice-rounding` (tagged `high-risk`). Cycle
   engine blocks at the first checkpoint:

   ```
   [Agent - 10:01am] Claimed fix-invoice-rounding. Post-claim
   checkpoint required before implementation. Filing response:

     Approach: Change the rounding mode from HALF_UP to
     HALF_EVEN in internal/billing/invoice.go. Stripe uses HALF_EVEN
     for their line items, so our totals drift on items with a
     fractional cent.

     Alternatives considered:
       1. Rewrite the summation to use decimal arithmetic throughout
          (rejected: touches 14 files, risk is higher than the bug)
       2. Keep HALF_UP but apply a correction at API boundary
          (rejected: two rounding modes is worse than one)

     New dependencies: none

     Affects auth/billing/data-integrity: YES — billing.
     Justification (required): This directly modifies invoice totals.
   ```

3. The agent then **hard-blocks** until Meera approves the checkpoint.
   Meera sees it in her inbox:

   ```
   Inbox — 1 checkpoint

     fix-invoice-rounding   high-risk · post-claim   pending 6m
       Affects: billing
       Agent proposes: HALF_EVEN rounding in invoice.go
       Alternatives considered (2 — rewrite, correction-at-boundary)
       [Approve] [Request changes] [Comment]
   ```

   She reads the structured response in 30 seconds — faster than
   skimming a prose thread because the *shape* is fixed. She sees the
   alternatives explicitly, the scope is clearly "one function, one
   file," and there's no new dependency. She approves.

4. Agent unblocks, implements, hits the pre-submit checkpoint:

   ```
   [Agent - 10:14am] Pre-submit checkpoint:

     What changed vs original approach: nothing — HALF_EVEN swap only.
     Risks remaining: existing invoices aren't migrated (retroactive
       correction would touch historical totals; out of scope).
     Test evidence: added internal/billing/invoice_test.go with 8
       cases covering HALF_EVEN edge cases; `go test ./internal/billing`
       passes.
   ```

   Meera approves this too. Agent submits.

5. Meanwhile, `typo-in-settings-page` (tagged `standard`) runs through
   its single pre-submit questionnaire with a one-line summary and a
   one-line test plan, no blocking, auto-advances to human-qa in <1
   minute. Meera spends ~10 seconds on it.

**The dial:**

Meera's configuration knob is:

- **Number of checkpoints per cycle** — 1 for routine, 3 for high-stakes, more for incident cycles.
- **Questions per checkpoint** — few for cheap oversight, many for rich.
- **Blocking semantics per question**:
  - `hard_block`: agent waits for explicit Meera approval (default for `high-risk`).
  - `soft_block`: agent waits N minutes, then proceeds and flags "auto-proceeded" in the audit trail (useful for "I want structured answers but don't want to be a bottleneck when I'm offline").
  - `log_only`: agent answers, no block (the `standard` template mode).
- **Conditional firing** — a checkpoint can be declared conditional ("only fire post-claim if the feature touches a file in `internal/billing/**`"), so even within a cycle template the friction scales with what the agent is actually doing.

This gives Meera a true dial: she can tune review depth per cycle type,
per checkpoint, per question, and per condition.

**Gap:** Question quality is load-bearing. "Approach?" yields
variable-depth answers depending on agent disposition. The template
should support *examples* per question (one good answer + one
insufficient answer) so the agent knows the bar. Without that,
questionnaires devolve into the same soft-structure problem as
free-form comments.

**Gap:** Meera's configuration is per-template. But sometimes she
wants to override for one feature ("this specific change is routine
even though the tag says high-risk"). The feature should be able to
downgrade its own questionnaire with a PM-only override, logged in
the audit trail.

**Gap:** Hard-block semantics interact with agent concurrency. If
Meera is the PM for 6 agents and 3 of them hit hard-block checkpoints
at once, she has a queue. The inbox needs to surface "oldest blocked"
prominently and show agent wall-clock time lost to blocking, so she
can decide to relax semantics or hire more PM attention.

**What would trip her up:**

- **Over-configuration.** If Meera attaches 5 checkpoints to every
  cycle, PM time goes *up* and agents stall. The system should warn
  when a cycle template's expected PM time exceeds a threshold
  (derived from historical data, per story 17 estimation).
- **Soft-block defaults that are too aggressive.** If the default
  timeout is 10 minutes and Meera is away for an hour, the agent
  auto-proceeds on four checkpoints she would have flagged. The
  default should probably be "no auto-proceed" (hard-block) unless
  Meera explicitly opts a question into soft semantics.
- **Answer drift.** An agent's answer to "risks remaining" at
  post-claim might be contradicted by what actually happens during
  implementation. The pre-submit checkpoint should show the post-claim
  answers inline so the agent (and Meera) can see the delta — "you
  said `no new dependencies`, but you added `github.com/shopspring/decimal`."
- **PM-authored schemas are an attack surface for bureaucracy.** Every
  well-meaning question is friction. The retro agent (story 19)
  should analyze questionnaires and flag ones that never produce PM
  intervention — "this question has been answered on 40 features; PM
  has never acted on it. Consider removing." The dial needs a feedback
  loop, or it ratchets up forever.

**What makes this work:**

- The dial is *enforced*, not guidance. Meera doesn't have to remember
  to look harder at billing work — the cycle template does.
- Structured answers are **scannable**. A fixed shape means Meera's
  eyes land on the right fields; a prose thread forces her to parse.
- It handles the mid-flight race condition from [story 2](./02-marcus-mid-flight-steering.md)
  by inverting the direction. Instead of Marcus commenting mid-stream
  hoping the agent checks in time, the agent proactively pauses at a
  known point and surfaces decisions. Marcus *acts when the agent
  asks*, not when he notices.
- It composes with [Layer 9](../implementation-layers.md) universal
  PR review: the checkpoint is literally a micro-PR on the agent's
  proposed approach, with Approve / Request changes / Comment actions.
- It composes with [Layer 10](../implementation-layers.md) metrics:
  time-in-checkpoint, PM response time per question, which questions
  actually produce interventions — all measurable, all tunable.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
