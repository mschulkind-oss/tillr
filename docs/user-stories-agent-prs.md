# User Stories: Agent PRs, Staged Merging, and Parallel QA

Exploring what happens when agents produce PR-like artifacts instead of
committing directly. Each story follows the work from implementation
through review and merge, with technical realities called out.

---

## 1. Val — Three Agents Working in Parallel, No Conflicts

**Context:** Val has 3 features ready in the queue: `global-inbox`,
`spec-validation`, and `human-status-labels`. She kicks off 3 agents.
Each claims one feature.

**What happens today:**

All 3 agents work in the same working tree, on main. If they touch
different files, they can commit sequentially (SQLite serializes writes).
If they touch the same file, one agent's commit overwrites the other's
changes — silent data loss. There's no isolation and no review step.

**What happens with agent PRs:**

1. Each agent claims a feature. Tillr creates an isolated workspace:
   ```
   Agent 1: tillr agent claim global-inbox
   → branch: agent/global-inbox
   → worktree: .tillr/worktrees/global-inbox/

   Agent 2: tillr agent claim spec-validation
   → branch: agent/spec-validation
   → worktree: .tillr/worktrees/spec-validation/

   Agent 3: tillr agent claim human-status-labels
   → branch: agent/human-status-labels
   → worktree: .tillr/worktrees/human-status-labels/
   ```

   Each agent works in its own directory with its own branch. Full git
   isolation. They can't interfere with each other.

2. Each agent implements, commits to its branch, then submits:
   ```
   tillr agent submit
   ```

   Submit does:
   - Runs validation (build, typecheck, tests)
   - Commits all changes to the agent branch
   - Creates a tillr PR record (NOT a GitHub PR — a local concept)
   - Advances feature to `agent-qa` or `human-qa`

   The tillr PR record stores:
   ```json
   {
     "id": "pr-global-inbox",
     "feature_id": "global-inbox",
     "branch": "agent/global-inbox",
     "base": "main",
     "status": "open",
     "diff_stats": {"files": 5, "insertions": 320, "deletions": 12},
     "validation": {"build": "pass", "typecheck": "pass", "tests": "pass"},
     "created_at": "2026-04-04T10:30:00Z"
   }
   ```

3. Val opens the inbox. She sees 3 PRs ready for review, each with:
   - What changed (file list, diff stats)
   - What to check (from the feature spec/story)
   - Validation results (build, tests — all green)
   - [Approve & Merge] [Approve & Stage] [Reject]

4. She reviews `global-inbox` first. Looks good. She clicks [Approve &
   Merge]. Tillr merges the branch into main:
   ```
   git checkout main
   git merge agent/global-inbox --no-ff
   git branch -d agent/global-inbox
   rm -rf .tillr/worktrees/global-inbox/
   ```
   Feature moves to `done`.

5. She reviews `spec-validation`. Also good. [Approve & Merge]. Clean
   merge — no conflicts because the features touched different files.

6. She reviews `human-status-labels`. Wants changes. [Reject] with note:
   "Use plain English labels but keep the status codes in the API — only
   change the display layer." Feature goes back to `implementing`. The
   agent gets the rejection note and the branch stays open for more work.

**What makes this work:**
- Git worktrees give real isolation. Each agent has its own checkout.
  They share the same `.git` directory (so branches are visible to each
  other) but have separate working trees.
- The merge is a normal git merge. If it's clean, it's instant. If there
  are conflicts, tillr can detect this BEFORE the human reviews.
- The tillr PR is a local-only concept. No GitHub dependency. Works
  offline, works in jails, works without `gh`.

**Technical reality check — worktrees:**
- Git worktrees share the `.git` directory. This means they share the
  reflog, the index lock, and the pack files. Concurrent operations are
  generally safe but `git gc` during active work can cause issues.
- Creating a worktree is fast (~100ms). Deleting is fast. They're cheap.
- Each worktree needs its own `node_modules/` (or symlinked). For a
  project with heavy dependencies, this means `pnpm install` per worktree.
  This could add 10-30 seconds per agent claim.
- SQLite DB is in the main working tree. All agents share it. This is
  fine — SQLite WAL mode handles concurrent reads, and writes are
  serialized. Tillr CLI operations are short writes (claim, heartbeat,
  submit) so lock contention is minimal.

---

## 2. Kenji — Conflict Detection Before Human Review

**Context:** Two agents worked on features that both modified the same
file. Kenji doesn't want to discover this during merge — he wants to
know before he reviews either one.

**What happens:**

1. Agent 1 submits PR for `api-rate-limiting` — modified `server.go`
   lines 200-250 and added `middleware/ratelimit.go`.

2. Agent 2 submits PR for `request-logging` — modified `server.go`
   lines 180-220 and added `middleware/logging.go`.

3. Both PRs are `open`. Tillr runs a conflict check:
   ```
   tillr pr check-conflicts
   ```
   or automatically on submit:
   ```
   Conflict detected:
     PR agent/api-rate-limiting and PR agent/request-logging
     both modify server.go (overlapping lines 200-220)

     Options:
     1. Merge one first, rebase the other
     2. Review and merge together
     3. Reject one back to the agent with rebase instructions
   ```

4. Kenji sees this in the inbox:
   ```
   ⚠ Conflict: api-rate-limiting ↔ request-logging
     Both modify server.go lines 200-220

     api-rate-limiting    Priority 9    [Review First]
     request-logging      Priority 7    [Will rebase after]
   ```

5. He reviews and merges `api-rate-limiting` first (higher priority).
   Tillr automatically rebases `request-logging` onto the new main:
   ```
   git checkout agent/request-logging
   git rebase main
   ```
   If the rebase is clean, the PR updates and is ready for review.
   If the rebase has conflicts, the feature goes back to the agent:
   "Your changes conflict with the merged api-rate-limiting feature.
   Resolve conflicts and resubmit."

**Technical reality check — conflict detection:**
- Conflict detection without merging: `git merge-tree --write-tree main
  agent/feature-a agent/feature-b` (Git 2.38+). Returns exit 1 if
  conflicts exist. This is fast and doesn't modify any working tree.
- For N open PRs, you need N*(N-1)/2 pairwise checks. With 10 open PRs,
  that's 45 checks. With 50 open PRs, that's 1,225. This gets expensive.
- Optimization: only check PRs that modify the same files. A quick
  `git diff --name-only main..agent/feature-X` per PR, then intersect
  the file lists. Only run merge-tree on PRs with overlapping files.
- Auto-rebase after merge: `git rebase main` on the remaining branches.
  If rebase fails, the agent needs to handle it. Most agents CAN handle
  rebase conflicts if given clear instructions.

---

## 3. Lisa — Agent-Based PR Review Before Human QA

**Context:** Lisa doesn't read code. She can't review a diff. But she
still wants quality gates before things land. What if another agent
reviews the implementing agent's work?

**The pipeline:**

```
Agent implements → Agent PR → Agent review → Human QA → Merge
                   (code)      (code review)   (UX/judgment)
```

1. Implementing agent submits PR for `login-page`.

2. Tillr assigns a review agent:
   ```
   tillr agent claim-review pr-login-page
   ```
   The review agent gets:
   - The full diff
   - The feature spec
   - The project's coding standards (from AGENTS.md or a config)
   - The workstream story (user context)

3. The review agent produces a structured review:
   ```json
   {
     "pr_id": "pr-login-page",
     "verdict": "approve_with_comments",
     "comments": [
       {
         "file": "web/src/pages/Login.tsx",
         "line": 45,
         "severity": "suggestion",
         "body": "Password field should use type='password' not type='text'"
       },
       {
         "file": "web/src/pages/Login.tsx",
         "line": 82,
         "severity": "nit",
         "body": "Consider adding aria-label for accessibility"
       }
     ],
     "summary": "Login page implements spec correctly. One security issue
     (password visibility) and one accessibility suggestion."
   }
   ```

4. Tillr records the review and decides:
   - If the review agent found blocking issues → feature goes back to
     implementing agent with the review comments
   - If approved (with or without comments) → feature advances to
     `human-qa`

5. Lisa sees the feature in her inbox with BOTH the review summary and
   the story-level QA checks:
   ```
   login-page                    Priority 8
     Agent review: Approved with 1 suggestion
     "Password field fixed. Accessibility label added."

     What to check:
     - Go to /login
     - Try logging in with wrong password
     - Check the error message is clear
     [Approve & Merge] [Reject]
   ```

   She doesn't need to read the diff. The agent reviewer caught the
   code issues. She just checks the experience.

**Technical reality check — agent reviews:**
- Agent reviews are just another agent workflow step. The review agent
  runs `git diff main..agent/login-page`, reads the spec, and produces
  structured output.
- The review agent needs to run in a read-only context — it should NOT
  modify the code. It just reads and comments. This is a different mode
  than the implementing agent.
- Review quality depends on the review agent's prompt and context. A
  generic "review this diff" produces generic comments. A review scoped
  to the spec ("does this diff implement the spec?") produces targeted
  feedback.
- Cost: one additional agent invocation per feature. For features that
  take 10 minutes to implement, a 2-minute review is a 20% overhead.
  Worth it for high-priority features, maybe not for trivial ones.
- Tillr could make reviews configurable per workstream or priority:
  "review features with priority >= 8" or "review all features in the
  security workstream."

---

## 4. Val — Staging Multiple Features for a Coordinated Release

**Context:** Val has 5 features that together form the "Human QA
Experience" story. She wants to QA them together — not merge one at a
time and hope they work as a group.

**What she wants:**

1. All 5 agent PRs are open. She reviews each one individually:
   - `global-inbox-page` — approved
   - `human-qa-checklist-field` — approved
   - `spec-required-for-queue` — approved
   - `human-status-labels` — approved
   - `inbox-approve-reject-flow` — approved

   But she doesn't merge any of them yet. She clicks [Approve & Stage]
   on each one.

2. Tillr creates a staging branch that combines all 5:
   ```
   git checkout -b staging/human-qa-experience main
   git merge agent/global-inbox-page
   git merge agent/human-qa-checklist-field
   git merge agent/spec-required-for-queue
   git merge agent/human-status-labels
   git merge agent/inbox-approve-reject-flow
   ```

   If any merge conflicts, tillr reports them before creating the staging
   branch. Val can decide which features to include and which to hold back.

3. Tillr builds and tests the staging branch:
   ```
   cd .tillr/worktrees/staging-human-qa-experience/
   go build ./...
   npx tsc --noEmit
   go test ./...
   pnpm --prefix web build
   ```

4. Val can now QA the staging branch as a whole — all 5 features
   together, running locally from the staging worktree. She tests the
   full flow: open inbox, see items, approve one, reject one, check
   empty state.

5. When satisfied, she merges the staging branch to main:
   ```
   tillr pr merge staging/human-qa-experience
   ```
   This is a single merge commit that brings all 5 features to main.
   All 5 features move to `done`. The staging worktree is cleaned up.

**Alternative: test without staging.**

Instead of a staging branch, Val could just merge features one at a
time to main (with each merge gated by validation). The CI-like
guarantees come from the merge-time validation, not from a staging
branch. This is simpler but means main gets intermediate states where
some features are merged but the full experience isn't complete.

**Technical reality check — staging:**
- Staging branches add complexity. You need to track which PRs are
  included, handle the case where a PR is updated after staging (re-stage),
  and clean up staging branches that are abandoned.
- The merge order matters. If feature A and B conflict, the staging
  branch creation fails. Tillr needs to report which pair conflicts.
- A simpler model: merge to main one at a time, but tag the merge point
  before and after the batch. If the batch breaks, revert to the tag.
  This is what most teams actually do.
- Staging branches are most valuable when features interact — when you
  NEED to test them together. For independent features, merge one at a
  time.

---

## 5. Derek — The Simplest Thing That Works

**Context:** Derek doesn't want staging branches or agent reviewers. He
wants the simplest possible PR workflow: agents work on branches, he
reviews and merges from the tillr UI.

**What he needs:**

1. Agent claims → gets a branch and worktree automatically.

2. Agent submits → branch has commits, feature is in human-qa.

3. Derek sees the PR in his inbox:
   ```
   login-page                    Priority 8    3 files changed
     agent/login-page → main

     Changes:
       web/src/pages/Login.tsx      +120 -0  (new file)
       web/src/api/client.ts        +8 -0
       web/src/App.tsx              +2 -0

     Build: ✓    TypeCheck: ✓    Tests: ✓

     What to check:
     - Navigate to /login
     - Try logging in

     [Merge] [Reject]
   ```

4. He clicks [Merge]. Done. Branch deleted, worktree cleaned up, feature
   is done.

**That's it.** No staging. No agent reviews. No conflict detection (he's
the only one running agents). Just branches, diffs, and merge buttons.

The point: the PR model should START simple (Derek's version) and layer
on complexity (conflict detection, agent reviews, staging) as opt-in
features. Not everyone needs the full pipeline.

---

## 6. Agent in a Jail — PRs Without GitHub

**Context:** An agent is running in a sandboxed container. It can't push
to GitHub. It can't create GitHub PRs. But it can create local branches
and commit to them.

**What happens:**

1. The agent claims a feature. Tillr creates a branch (no worktree — the
   agent is already in its own container/worktree):
   ```
   tillr agent claim login-page
   → branch: agent/login-page (created from current main)
   ```

2. The agent implements and commits to the branch.

3. The agent submits:
   ```
   tillr agent submit
   ```
   Submit creates a tillr PR record in the DB. No GitHub involved. The
   PR is a local concept — branch name, base branch, diff stats, and
   validation results.

4. The human sees the PR in the tillr UI. They can view the diff (tillr
   serves it from git), approve, reject, and merge — all through the
   local tillr interface. No GitHub dependency.

**Technical reality check:**
- Serving diffs from git: `git diff main..agent/login-page` gives the
  unified diff. The tillr API endpoint serves this. The UI renders it
  with syntax highlighting (there are React diff viewer libraries).
- Merging from the UI: the server runs `git merge` when the user clicks
  merge. This requires the server process to have write access to the
  repo. In the jail model, the server runs on the host (not in the jail),
  so it has access.
- If the agent is in a jail and the server is also in the jail... the
  merge happens inside the jail. The host picks up the merged main branch
  next time it syncs. This is the same model as today.

---

## Implementation Layers

The PR model can be implemented incrementally. Each layer adds value
independently:

### Layer 1: Branches and Records (foundation)
- `tillr agent claim` creates a branch (and optionally a worktree)
- `tillr agent submit` commits, validates, creates a PR record in DB
- PR record: branch, base, diff stats, validation results, status
- `tillr pr list` shows open PRs
- `tillr pr show <id>` shows diff
- `tillr pr merge <id>` merges the branch to main
- UI: inbox shows PRs with merge/reject buttons
- **This is Derek's version. Start here.**

### Layer 2: Diff Serving and UI Review
- API endpoint: `GET /api/pr/{id}/diff` returns unified diff
- UI: inline diff viewer on PR detail page
- UI: file tree showing changed files with +/- counts
- Reject with comments that go back to the agent

### Layer 3: Conflict Detection
- On submit, check all open PRs for file overlap
- `git merge-tree` for pairwise conflict detection
- Inbox shows conflict warnings
- Auto-rebase after merge

### Layer 4: Agent Reviews
- `tillr agent claim-review <pr-id>` assigns a review agent
- Review agent produces structured comments (file, line, severity, body)
- Review stored in DB, visible in UI
- Configurable: which features get agent review (priority, workstream)

### Layer 5: Staging
- `tillr pr stage <pr-id> [<pr-id>...]` creates a staging branch
- Staging branch combines multiple PRs for integrated testing
- `tillr pr merge-staged <staging-id>` merges the batch to main
- Staging branch cleanup on merge or abandon

### What already exists:
- Git worktree creation in `tillr agent claim` (partial — the worktree
  path and branch naming exists in agent_workflow.go)
- `tillr agent submit` with validation (build + typecheck)
- Feature status transitions through the pipeline
- WebSocket notifications on status changes

### What needs building for Layer 1:
- `pr_records` table: id, feature_id, branch, base, status (open/merged/
  rejected/closed), diff_stats JSON, validation JSON, created_at,
  merged_at, reviewer_notes
- `tillr pr list/show/merge/reject` CLI commands
- API endpoints: GET /api/prs, GET /api/pr/{id}, POST /api/pr/{id}/merge,
  POST /api/pr/{id}/reject
- Modify `tillr agent submit` to create PR record
- Modify `tillr agent claim` to always create a branch
- Inbox integration: show PRs as mergeable items
- Merge logic: `git merge --no-ff`, branch cleanup, worktree cleanup
- Estimated: ~400 lines Go (table + queries + CLI), ~200 lines React (UI)

---

## Open Questions

1. **Should PRs be a tillr concept or actual GitHub PRs?**
   Tillr PRs work offline and in jails. GitHub PRs integrate with existing
   code review workflows and CI. Could support both: tillr PRs by default,
   `--github` flag to also create a GitHub PR. The tillr PR is always
   created; the GitHub PR is optional.

2. **Who merges?**
   Options:
   - Human clicks merge in tillr UI (simplest, Derek's model)
   - Human approves in tillr, merge is automatic (less control)
   - Human approves in tillr, but merge waits for a "release" action
     (staging model)
   
   Probably: default to human clicks merge. Staging and auto-merge are
   opt-in.

3. **Do agents always get worktrees?**
   Worktrees add overhead (disk space, `pnpm install` time). For simple
   features that touch 1-2 files, a branch without a worktree might be
   enough — the agent commits to the branch and switches back. But this
   means agents need to stash/switch, which is fragile.
   
   Probably: always use worktrees. The overhead is worth the isolation.
   Disk space is cheap. `pnpm install` can be optimized with symlinked
   node_modules or a shared cache.

4. **How does this interact with Claude Code's built-in worktree support?**
   Claude Code already has `isolation: "worktree"` in its Agent tool. This
   creates its own worktree at `.claude/worktrees/`. If tillr ALSO creates
   a worktree, you get nested worktrees or conflicting worktree management.
   
   Options:
   - Let Claude Code manage worktrees, tillr just tracks the branch name
   - Tillr manages worktrees, agent prompt says "don't use your own"
   - Detect Claude Code's worktree and use it instead of creating a new one
   
   This needs investigation. The worktree management should live in ONE
   place, not two.

5. **What about long-running branches?**
   If an agent takes 30 minutes and main has moved, the branch may be
   behind. Merge might conflict. Options:
   - Rebase before submit: `tillr agent submit` rebases onto main first
   - Rebase on merge: merge detects the branch is behind, rebases, then
     merges
   - Fail and tell the agent: "main has moved, rebase and resubmit"
   
   Probably: rebase before submit. The agent is still running, it can
   handle conflicts. After submit, the PR is a snapshot — don't auto-modify
   it.
