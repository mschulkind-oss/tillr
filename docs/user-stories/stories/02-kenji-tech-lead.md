# 2. Kenji — Tech Lead, Setting Up Tillr for a Team

**Context:** Kenji leads a team of 4 developers working on a Go microservices
platform. They've been using Claude Code and Copilot agents, but coordination
is chaos — two agents modified the same service last week and the merge was
painful. He wants structured agent orchestration.

**First 10 minutes:**

1. `tillr init platform && tillr onboard` — onboard scans the repo and
   bootstraps: discovers existing services, creates workstreams from the
   directory structure, imports TODOs from code comments.

   **Gap:** Onboard today is interactive and chatty. Kenji wants
   `tillr onboard --yes` to accept defaults and just do it. It also doesn't
   auto-create workstreams from repo structure — it could look at top-level
   directories, existing AGENTS.md sections, or package.json workspaces.

2. He adds the team's roadmap:
   ```
   tillr roadmap add "Service mesh migration" --priority critical
   tillr roadmap add "Observability overhaul" --priority high
   tillr roadmap add "API v2 deprecation" --priority medium
   ```

3. He creates workstreams matching team areas:
   ```
   tillr workstream create "API Gateway" --tags api,gateway
   tillr workstream create "Auth Service" --tags auth,security
   ```

4. He sets up the daemon so the team can share one URL:
   ```
   tillr daemon init --project ~/work/platform
   tillr daemon start
   ```
   Now the UI is at a stable URL the team can bookmark.

**The multi-agent problem:**

5. Kenji wants 3 agents working simultaneously on different features. Each
   agent needs its own workspace — they can't all edit the same files.

   Today's approach: git worktrees. Each agent creates a worktree and works
   in isolation:
   ```
   tillr agent claim --worktree
   # Creates .claude/worktrees/<feature-name> with branch agent/<feature-name>
   ```

   **Gap:** Who merges the worktree back? Today the agent submits a PR and
   the human merges. But if 3 agents are working, merge conflicts are likely.
   Tillr should detect conflicts early — "agent-2's changes overlap with
   agent-1's in-progress work" — but it doesn't today.

6. Kenji wants to see all agent activity in one place. The queue page shows
   what's claimed and by whom. The workstream page shows progress. But
   there's no "agent dashboard" that answers "what are all my agents doing
   right now, and is anything stuck?"

   **Gap:** The agent sessions feature exists but isn't surfaced well in the
   UI. The queue page shows in-progress items but doesn't show which agent
   is working on what, how long they've been at it, or if they're stuck.

**What would trip him up:**
- Setting up the daemon for the team (does each dev run their own? one shared
  server?). The answer: each dev has their own local tillr.db. The daemon is
  optional — for a shared dashboard, they'd need a shared DB or a read-only
  aggregation layer. That doesn't exist yet.
- Conflict detection between concurrent agents doesn't exist.
- No built-in agent spawning — tillr tracks what agents should do, but
  someone still has to start the agents manually.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
