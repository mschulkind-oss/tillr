# 6. Agent — Claiming and Implementing from the Queue

**Context:** A Claude Code agent is told "process the tillr queue." The agent
has the tillr CLI available and knows to use it. It's working in the project
directory.

**The agent's workflow:**

1. Check what's available:
   ```
   tillr agent next --json
   ```
   This claims the highest-priority feature that's ready for implementation
   (status: draft or planning, dependencies satisfied). Returns:
   ```json
   {
     "feature_id": "login-page",
     "name": "Login Page",
     "description": "Email/password login with error handling",
     "spec": "1. Form with email + password fields\n2. Error messages...",
     "priority": 8,
     "workstream": "authentication"
   }
   ```

2. The agent reads the spec and implements. During implementation it can:
   - Read the full spec: `tillr feature show login-page`
   - Check related features: `tillr feature list --workstream authentication`
   - Log progress: `tillr agent heartbeat "implementing form validation"`
   - Ask a question: `tillr discuss create --feature login-page "Should the
     password field show a strength indicator?"`

3. When done, the agent submits:
   ```
   tillr agent submit
   ```
   This runs validation (build check, type check), creates a git commit or
   PR, and advances the feature to `human-qa`.

   **Gap:** What if validation fails? Today `tillr agent submit` runs
   `go build` and `npx tsc --noEmit`. If they fail, the agent should get
   a clear error and be expected to fix it before resubmitting. But there's
   no retry loop built into the submit command — the agent has to handle
   this itself.

4. The agent then claims the next feature:
   ```
   tillr agent next --json
   ```
   And the loop continues.

**What the agent needs:**
- **Clear spec.** The feature's `spec` field must contain everything the
  agent needs to implement. If the spec says "add a login page" with no
  details, the agent will produce something mediocre. The spec quality
  is the human's responsibility — tillr should enforce that specs exist
  before features enter the queue.

  **Gap:** Today features can enter the queue with no spec. There should
  be a validation: features without specs can't be claimed. The human
  should be prompted to add a spec before the feature becomes claimable.

- **Scoped context.** The agent should know what workstream it's in, what
  other features exist, what's already been built. `tillr context` provides
  some of this but it's not automatically included in the claim response.

  **Gap:** The claim response should include enough context that the agent
  doesn't need to run 5 more commands to understand what to do. Include
  the spec, the workstream name, related feature IDs, and any open
  discussions.

- **Structured output everywhere.** Every command the agent runs should
  support `--json` for machine parsing. `tillr agent submit --json` should
  return `{"status": "submitted", "pr_url": "..."}` or
  `{"status": "failed", "errors": ["build failed: ..."]}`.

- **No ambiguity in the workflow.** The agent shouldn't have to decide
  what to do — the queue decides. "Process the queue" is the only prompt
  needed. Everything else (what to work on, in what order, when to stop)
  is encoded in tillr.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
