# 29. Anders — Platform Adapter for Claude and Copilot

**Context:** Anders is a platform engineer at a 50-person company. Two
engineering tribes:

- **Research-heavy tribe** uses Claude Opus for nuanced design
  decisions and large-context refactors.
- **Infra-heavy tribe** uses Copilot for repo-deep work with strong
  GitHub integration.

Anders wants tillr to dispatch work to either platform transparently —
same protocol, same comments substrate, different invocation
mechanics. He doesn't want the choice of agent platform to dictate
which workflow features are available.

**What happens today (without tillr platform abstraction):**

Each tribe manages their own agent invocation infrastructure. The
research tribe wires Claude SDK calls into custom scripts. The infra
tribe runs `gh copilot` invocations from their CI. Neither has shared
visibility or a consistent comments thread. Switching a team from
Claude to Copilot means re-tooling everything.

If Anders wants to standardize the workflow, he can't — every tribe's
agent invocation is bespoke. Story 25's "style enforcer agent runs as
a cycle step" reads like a thought experiment to him because there's
no abstraction to plug it into.

**What happens with the tillr platform adapter:**

1. Anders sets up tillr at the org level. Defines two adapters in
   `tillr/adapters/`:

   ```
   tillr/adapters/
     claude-sdk.json    — invokes Claude SDK; reads/writes tillr
                          comments via API
     copilot-cloud.json — invokes Copilot cloud agent via gh CLI;
                          polls for status; writes results into tillr
                          comments
   ```

   Each adapter file declares:
   - How to invoke (shell command or API call)
   - How to pass the context envelope (env var, stdin, file)
   - How to receive output (stdout parse, file read, polled API)
   - Authentication (API key env vars; respects the user's own creds)
   - Capabilities (does it support streaming? interrupts? model picker?)

2. Anders defines agent **roles** in `tillr/agents/<role>/`:

   ```
   tillr/agents/
     implementer/
       description.md   — what the role does
       prompt.md        — instructions
       tools.txt        — allowlist (read, write, bash, web)
       model_class.txt  — fast / strong / auto
     style-enforcer/
       description.md
       prompt.md
       tools.txt        — read-only
       model_class.txt  — fast (rules are mechanical; don't need Opus)
     code-reviewer/
       description.md
       prompt.md
       tools.txt        — read-only
       model_class.txt  — auto
   ```

   Roles are PLATFORM-AGNOSTIC. Same description, same prompt, same
   model class declaration regardless of where they end up running.

3. The cycle template specifies which role runs which step:

   ```
   tillr cycle template create standard \
     --step implement     --role implementer    --adapter claude-sdk \
     --step style-review  --role style-enforcer --adapter copilot-cloud \
     --step code-review   --role code-reviewer  --adapter claude-sdk
   ```

   Adapter choice can be defaulted (org-level "use claude-sdk unless
   role demands otherwise") with per-step overrides.

4. When a feature enters the cycle, the cycle engine dispatches each
   step through the right adapter:

   ```
   Cycle dispatch:
     feature: add-feature-toggle
     step: implement
     role: implementer
     adapter: claude-sdk
     model_class: strong → resolves to claude-opus-4-7
     envelope: { spec, philosophies, related_features, ... }
   ```

   Adapter receives the dispatch, invokes Claude SDK with the
   envelope, captures output, and writes tillr comments via the
   canonical API. Tillr advances cycle state when adapter reports
   completion.

5. **All steps emit tillr comments** via the canonical comment API.
   The PM doesn't see "Claude said X, Copilot said Y" — they see
   "implementer said X, style enforcer said Y." The platform is
   invisible at the user layer.

**Platform agnosticism: confirmed by research.**

Both Claude and Copilot's cloud/background agents support everything
this story needs ([source: GitHub Copilot docs, Feb 2026](https://github.blog/ai-and-ml/github-copilot/whats-new-with-github-copilot-coding-agent/)):

| Capability | Claude | Copilot |
|------------|--------|---------|
| Custom agents in repo | `.claude/agents/` files | `.github/agents/` files |
| Per-task model selection | SDK model parameter | Model picker in Agents panel |
| Async / background execution | SDK invocations are stateless | Explicit async; "Copilot works asynchronously — by the time you check back in, there's a plan to review, code to look at, or a PR ready to merge" |
| Mid-session follow-up messages | Re-invoke with appended context | Type into session log; "Copilot implements your input after it finishes its current tool call" |
| Tools allowlist | Tool permissions per invocation | Custom agent file controls |

What this means for tillr: **the protocol is universal**. Tillr defines
cycle steps, context envelopes, comment artifacts, status transitions.
The platform-specific code is a thin invocation adapter.

**Inside the adapter (what it actually does):**

A tillr adapter is a small program that:

1. **Receives a cycle-step dispatch** from the engine:
   `(role, context_envelope, agent_invocation_id, model_class)`
2. **Translates** to the platform's invocation: shell command, API
   call, etc. Resolves `model_class` to a concrete platform model name.
3. **Polls for completion** or registers a webhook. For long-running
   invocations, surfaces "in progress" status to tillr periodically.
4. **Captures the platform's output** and converts to tillr comment(s)
   via the canonical comments API. Strips platform-specific noise
   (Copilot's session log preamble, Claude's tool-use blocks).
5. **Writes tillr-comment status back** as cycle-step result. If
   adapter detects an error (rate limit, timeout, API down), reports
   it to the cycle engine for retry/escalation.

Adapters ARE platform-specific. The PROTOCOL is NOT. Anders writes
two adapters. He never writes a third for tillr — he writes a third
for whatever NEW platform comes along.

**Output normalization:**

Different platforms produce different output formats. The adapter:
- Always produces tillr comments with **canonical structure**: role
  attribution, body, metadata block, timestamp.
- Strips platform noise (auth headers, session IDs, tool-use chatter).
- Preserves fidelity for important things (file paths, line numbers,
  code blocks, decision rationale).
- When the platform produces multiple outputs (e.g., Copilot's
  session log + final PR comment), adapter routes each to the right
  tillr artifact (working comments → cycle thread; final summary →
  feature comment).

**Cost tracking:**

The adapter records:
- Model used per invocation
- Token counts (input + output)
- Cost (when known from platform)

This rolls up into Layer 10 metrics. PMs can see cost per feature, per
cycle template, per agent role — necessary for capacity planning at
scale.

**Model picker integration:**

When the cycle template says `model_class: strong`, the adapter
resolves to the platform's strongest model (`claude-opus-4-7` /
Copilot's "strong" model class). When `model_class: fast`, adapter
resolves to the fast model. `auto` lets the platform decide.

This means the cycle template author doesn't have to know
platform-specific model names. Story 18 (Sana — specialization) and
the questionnaire-checkpoint cost question both benefit: cheap model
classes for routine work, strong for high-stakes.

**Mid-session follow-up via tillr comments:**

When a PM comments on a feature with a running agent (Layer 4 / Layer
4b), tillr writes the comment. The adapter sees the comment via its
poll loop and forwards it to the platform as a follow-up message.
Both Copilot and Claude queue at the next tool boundary — semantics
match story 24's questionnaire model.

For platforms that don't support follow-up (early CLI implementations
of Copilot did not), the adapter falls back to the pre-submit-check
pattern: agent re-reads its inbox before submitting.

**Gaps:**

- **Platform feature drift.** Copilot adds capability X; Claude
  doesn't (or vice versa). Cycle templates depending on X break for
  the platform that lacks it. Mitigation: cycle template declares
  `requires_capabilities: [...]`; engine refuses to dispatch through
  an adapter that lacks them.

- **Adapter versioning.** Each platform's API changes; adapters need
  their own versioning + tillr-engine compatibility matrix. Plan for
  adapter updates as a routine operational task.

- **Output normalization edge cases.** Weird platform-specific output
  that doesn't fit canonical comment shape. Adapter falls back to
  "raw output as one comment" with a warning.

- **Failure modes.** Rate limit, timeout, API down. Adapter must
  surface these to the cycle engine with a typed error. Engine
  decides retry vs escalate. Without this, a transient platform
  outage manifests as a stuck cycle.

- **Auth and credentials.** Each adapter needs its own credential
  config. Org-level secret management. Don't store creds in tillr
  itself — adapter reads from env vars or a secret store.

- **Concurrency limits per platform.** Copilot's cloud agents have
  rate limits; Claude SDK has its own. Adapter enforces; cycle engine
  schedules accordingly.

**What would trip Anders up:**

- **Hardcoded platform assumptions in cycle templates.** A template
  that assumes Claude's specific output format breaks when run via
  Copilot. The template should reference role and model class only,
  never platform name.

- **Per-team adapter customizations that diverge from the core
  adapter and don't merge back.** Adapters become unmaintainable.
  Treat adapters as core infrastructure with PR review.

- **Cost runaway.** Agents on different platforms have very different
  cost profiles; without monitoring, surprise bills. Layer 10 metrics
  must report cost prominently from day one.

- **Assuming platform parity.** Anders shouldn't assume "if Claude
  supports X, Copilot does too." Capabilities matrix is the source of
  truth.

**What makes this work:**

- **Tillr's protocol is the abstraction.** Adapters are thin wrappers.
- **Same comments substrate regardless of source.** No platform leakage
  to users.
- **Cost / metrics / status all canonical.** Layer 10's reporting
  works across platforms uniformly.
- **Roles are platform-agnostic.** Same role file describes the same
  job on any platform.
- **Validated by research.** Both major platforms support the same
  capabilities. The protocol IS feasible.

**Position in roadmap:**

**Stage 0 — Foundational, parallel concern.** Every other stage
depends on having SOME way to invoke an agent. The most basic adapter
("invoke claude SDK with prompt, capture output as a single comment")
is needed from Stage 1. Sophistication grows over stages:

- Stage 1: minimal adapter (shell-out, single output → single comment)
- Stage 2: adapter handles inbox-routed follow-up messages
- Stage 3: adapter supports multi-step roles (style enforcer needs its
  own envelope)
- Stage 5: adapter passes context envelope from graph
- Stage 7: adapter reports cost/tokens for metrics

Don't try to build the full adapter at Stage 1. Build the minimum
that Stage 1 needs; extend the adapter alongside each subsequent
stage's needs.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)
