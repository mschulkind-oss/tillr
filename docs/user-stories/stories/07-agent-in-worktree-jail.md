# 7. Agent — Working in a Worktree/Jail

**Context:** An agent is running in a sandboxed environment (yolo jail,
Docker container, or git worktree). The project directory is mounted, but
the agent may not have access to the host system. Multiple agents may be
working concurrently in separate worktrees.

**What the agent sees:**

1. The project is mounted at `/workspace`. There's a `.tillr.json` and
   `tillr.db`. The agent can run tillr CLI commands against this DB.

2. The agent claims a feature:
   ```
   tillr agent claim login-page
   ```
   If using worktrees, this creates a new git branch `agent/login-page`
   and a worktree at `.claude/worktrees/login-page`.

3. The agent works in the worktree. All git operations (commits, branches)
   are isolated from other agents working in other worktrees.

4. When done:
   ```
   tillr agent submit
   ```
   This creates a PR from the worktree branch to main. The feature moves
   to `human-qa`.

**Concurrency issues:**

- Two agents claim different features. Both modify the same file. Neither
  knows about the other's changes until PR merge time.

  **Gap:** Tillr could detect potential conflicts by tracking which files
  each agent modifies (from git diff) and warning when two in-progress
  features touch the same files. But this doesn't exist today.

- The tillr.db is shared between worktrees (it's in the main working
  tree). Multiple agents writing to SQLite concurrently could cause
  locking issues.

  **Gap:** SQLite handles concurrent reads fine and serializes writes with
  WAL mode. But the tillr CLI doesn't set a busy timeout, so concurrent
  claims could fail with "database is locked." Need to verify this works
  under load.

- In a jail, the agent might not have network access to create PRs via
  `gh`. The submit flow needs a fallback that just creates the commit
  and lets the human merge manually.

  **Gap:** `tillr agent submit` assumes `gh` is available. It should
  degrade gracefully: commit to branch, skip PR creation, still advance
  the feature status. Log a message like "PR not created (no gh available).
  Branch agent/login-page is ready for manual merge."

**What the agent needs from tillr in a jail:**
- All commands work with just the local DB — no daemon, no network required.
- Clear error messages when something fails due to the sandboxed environment.
- A way to report completion even without PR creation.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
