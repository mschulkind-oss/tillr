# User Stories: Tillr Merge Queue

Design decisions established:
- **Tillr owns worktrees.** Not Claude Code, not Copilot. Tillr creates
  branches, creates worktrees, manages cleanup. Works across all agents.
- **PRs are a local tillr concept.** No GitHub dependency. The PR record
  lives in the DB with branch name, diff stats, validation results.
- **QA happens in isolation.** When a human reviews a feature, they're
  looking at ONLY that feature's changes against main — not polluted by
  other in-flight work.
- **Approved features enter a merge queue.** The queue processes them
  sequentially: rebase, validate, merge. This guarantees main is always
  green.

---

## 1. Val — Reviewing a Feature in Isolation

**Context:** Val has 5 features in human-qa. Three agents are still
working on other features. She wants to QA `global-inbox` without any
of the other in-flight changes affecting what she sees.

**What happens:**

1. Val opens the tillr UI. Her inbox shows 5 features ready for review.
   She clicks on `global-inbox`.

2. The feature detail shows:
   ```
   global-inbox                    Priority 9
   Branch: agent/global-inbox (3 ahead of main, 0 behind)

   Changes: 5 files, +320 -12
     web/src/pages/Inbox.tsx         +280 (new)
     web/src/App.tsx                 +3 -0
     web/src/api/client.ts           +15 -2
     web/src/api/types.ts            +12 -0
     internal/server/server.go       +10 -10

   Validation: ✓ build  ✓ typecheck  ✓ tests

   What to check:
   - Navigate to /inbox
   - See items from multiple workstreams
   - Approve one, reject one
   - Check empty state
   ```

3. Val wants to try it. She clicks [Launch Preview] or runs:
   ```
   tillr pr preview global-inbox
   ```
   Tillr builds and starts a server from the `agent/global-inbox` branch:
   ```
   Building from agent/global-inbox...
   Preview server at http://localhost:3848
   (main is still running at http://localhost:3847)
   ```

   She now has two servers: main (3847) and the feature branch (3848).
   She opens 3848 and tests the inbox. It works. She's seeing ONLY the
   global-inbox changes — nothing from the other 4 features.

4. She approves:
   ```
   [Approve → Merge Queue]
   ```
   The feature enters the merge queue. She moves on to the next feature.

**Alternative without a preview server:**

If Val doesn't need a running preview (e.g., it's a CLI feature or a
backend-only change), she reviews the diff in the UI and approves based
on the validation results and the spec. The isolation still matters —
she's looking at a clean diff against main, not a muddled diff that
includes other in-flight work.

**Why isolation matters:**

Without isolation, feature branches accumulate on top of each other.
If Val reviews feature C, which was built on top of features A and B
(both also in-flight), she can't tell which changes belong to C. If she
approves C, then rejects A, she has to re-review C because removing A
might break it. Isolation prevents this — every review is against a
clean main.

---

## 2. The Merge Queue — Sequential Processing

**Context:** Val has approved 3 features. They enter the merge queue in
approval order. The queue processes them one at a time.

**The queue state:**

```
Merge Queue (3 pending)
  Position 1: global-inbox          approved 2 min ago    Processing...
  Position 2: spec-validation       approved 1 min ago    Waiting
  Position 3: human-status-labels   approved just now     Waiting
```

**What happens for each item:**

1. **Rebase.** Take the feature branch and rebase onto current main:
   ```
   git rebase main agent/global-inbox
   ```
   This ensures the feature applies cleanly on top of whatever's already
   been merged. If main hasn't moved (this is the first merge), the
   rebase is a no-op.

2. **Validate.** Run the full validation suite on the rebased branch:
   ```
   go build ./...
   npx tsc --noEmit
   go test ./...
   pnpm --prefix web build
   ```
   This catches cases where the feature worked against old-main but
   breaks against current-main (e.g., a function signature changed in
   a previously merged feature).

3. **Merge.** If validation passes:
   ```
   git checkout main
   git merge --no-ff agent/global-inbox -m "Merge: global-inbox (#pr-id)"
   ```
   The `--no-ff` creates a merge commit, preserving the feature branch
   history. Main advances.

4. **Cleanup.** Delete the branch and worktree:
   ```
   git branch -d agent/global-inbox
   rm -rf .tillr/worktrees/global-inbox/
   ```
   Feature status → `done`. PR status → `merged`.

5. **Next item.** The queue moves to `spec-validation`. Rebase onto the
   NEW main (which now includes global-inbox). Validate. Merge. Repeat.

**When things go wrong:**

**Rebase conflict:**
```
Merge Queue
  Position 1: spec-validation    REBASE CONFLICT
    Conflicts in: internal/server/server.go
    
    Options:
    [Send Back to Agent] [Send Back to Human] [Skip & Process Next]
```

If [Send Back to Agent]: feature goes back to `implementing` with the
conflict details. The agent rebases and resubmits. The feature re-enters
the queue (at the back or at its original position — configurable).

If [Skip & Process Next]: the queue moves to the next item. The
conflicting feature stays in the queue but is deferred until its
conflicts are resolved.

**Validation failure:**
```
Merge Queue
  Position 1: spec-validation    VALIDATION FAILED
    go test ./... failed:
      TestFeatureClaimRequiresSpec: expected error, got nil
    
    [Send Back to Agent] [Send Back to Human]
```

Same options. The feature worked in isolation but breaks when combined
with previously merged changes. This is exactly what the merge queue
catches — the sequential process ensures each merge is validated against
the true current state.

---

## 3. Kenji — Watching the Merge Queue Process Overnight

**Context:** Kenji approved 12 features at 6 PM and went home. The merge
queue is processing them unattended.

**What he sees the next morning:**

```
Merge Queue — Last 12 hours

  ✓ global-inbox              merged    6:02 PM    12s
  ✓ spec-validation           merged    6:03 PM    15s
  ✓ human-status-labels       merged    6:03 PM    11s
  ✓ inbox-approve-flow        merged    6:04 PM    14s
  ✗ api-rate-limiting         failed    6:05 PM    rebase conflict
  ✓ request-logging           merged    6:05 PM    13s   (processed after skip)
  ✓ dashboard-redesign        merged    6:06 PM    18s
  ✓ feature-tags              merged    6:06 PM    12s
  ✓ bulk-operations           merged    6:07 PM    16s
  ✓ search-improvements       merged    6:07 PM    14s
  ✓ mobile-layout             merged    6:08 PM    11s
  ✓ history-page-fix          merged    6:08 PM    12s

  11/12 merged. 1 failed (api-rate-limiting — rebase conflict).
  api-rate-limiting returned to implementing with conflict details.
```

10 features merged cleanly. `api-rate-limiting` hit a rebase conflict
because `request-logging` (which merged before it) modified the same
file. The queue skipped it, processed the rest, and sent it back to
the agent with the conflict details.

The agent will pick it up next time it runs: rebase, resolve, resubmit.
The feature will re-enter the queue at the next approval.

**What makes this valuable:**
- Kenji didn't babysit 12 merges. The queue handled everything.
- Main is green — every merge was validated before landing.
- The one failure was detected and handled automatically. No broken main.
- The skip-and-continue behavior means one failure doesn't block everything.

---

## 4. Agent — The Full Lifecycle with Worktrees

**Context:** An agent claims a feature from the tillr queue. Tillr manages
the entire workspace lifecycle.

**Step by step:**

1. Agent starts:
   ```
   tillr agent next --json
   ```
   Tillr picks the highest-priority ready feature, creates a branch and
   worktree:
   ```
   Creating workspace for feature: global-inbox
     Branch: agent/global-inbox (from main @ abc1234)
     Worktree: .tillr/worktrees/global-inbox/
     
   {"feature_id": "global-inbox", "worktree": ".tillr/worktrees/global-inbox/", ...}
   ```

2. Agent works in the worktree:
   ```
   cd .tillr/worktrees/global-inbox/
   # ... implement the feature ...
   git add -A && git commit -m "feat: add global inbox page"
   ```

   The agent can make multiple commits. All on the feature branch.

3. Agent submits:
   ```
   tillr agent submit
   ```
   Submit does:
   a. Verifies clean working tree (no uncommitted changes)
   b. Rebases onto latest main (catches drift)
   c. Runs validation (build, typecheck, tests)
   d. Creates a PR record in the DB
   e. Advances feature to human-qa
   f. Prints the PR summary

   ```
   PR created: pr-global-inbox
     Branch: agent/global-inbox (5 commits, 3 ahead of main)
     Files: 5 changed, +320 -12
     Validation: ✓ build  ✓ typecheck  ✓ tests
     Status: awaiting human review
   ```

   The worktree stays alive until the PR is merged or rejected.
   The agent moves on to the next feature.

4. Human approves → feature enters merge queue → queue merges → worktree
   cleaned up. Or human rejects → feature goes back to implementing →
   agent claims it again and works in the SAME worktree (branch still
   exists).

**What if the agent's implementation takes a while and main moves?**

The rebase in step 3b handles this. If the rebase has conflicts, submit
fails:
```
tillr agent submit
  Error: rebase conflict with main
    Conflicts in: internal/server/server.go

  Resolve conflicts and run 'tillr agent submit' again.
```

The agent resolves the conflicts (most agents can do this), commits the
resolution, and retries submit. This is better than discovering conflicts
at merge time — the agent is still in context and can fix things
immediately.

**What if two agents claim features that might conflict?**

Tillr tracks which files each in-progress agent is modifying (from their
commits). When a new claim happens, tillr can warn:
```
tillr agent claim api-rate-limiting
  Warning: agent/request-logging (in progress) also modifies server.go
  Proceeding anyway. Be aware of potential merge conflicts.
```

This is advisory, not blocking. Conflicts are resolved at submit time
(rebase) or merge queue time (rebase + validate). The warning just gives
the agent a heads-up.

---

## 5. The Preview Server — QA Without Switching Branches

**Context:** Val wants to test a feature without leaving her current
terminal session or switching branches. She's on main. The feature is
on its branch.

**Option A: tillr pr preview (runs a second server)**

```
tillr pr preview global-inbox --port 3848
```

Tillr:
1. Finds the worktree at `.tillr/worktrees/global-inbox/`
2. Builds the frontend in that worktree (`pnpm build`)
3. Starts a tillr server in that worktree on port 3848
4. Opens the browser (or prints the URL)

Val now has two servers side by side:
- `localhost:3847` — main (current state)
- `localhost:3848` — main + global-inbox changes

She can compare them. Test the new feature. Verify nothing else broke.

When done:
```
tillr pr preview --stop global-inbox
```

**Option B: tillr pr checkout (temporary checkout)**

For CLI-only features or when a full server isn't needed:
```
tillr pr checkout global-inbox
# You're now in .tillr/worktrees/global-inbox/
# Do your testing...
exit
# Back to your original directory
```

**Option C: the UI shows enough**

For many features, the diff + validation results + screenshots (if
attached) are enough. Val doesn't need to run the code — she just needs
to see what changed and trust the validation.

The preview server is most valuable for UI features where "does it look
right?" requires actually seeing it.

---

## 6. Derek — Just the Simple Version

**Context:** Derek doesn't want merge queues yet. He just wants branches
and merge buttons. He'll add the queue later.

**The minimal workflow:**

1. Agent claims → tillr creates branch + worktree
2. Agent implements → commits to branch
3. Agent submits → PR record created, feature to human-qa
4. Derek reviews in UI → sees diff, validation results
5. Derek clicks [Merge] → branch merges to main, cleanup
6. Done.

No queue. No rebase. No preview server. No conflict detection. Just
isolated branches with a merge button. If a merge has conflicts, Derek
sees an error and sends it back to the agent.

**This is Layer 1.** Everything else builds on top:
- Layer 2: Add diff viewer in UI
- Layer 3: Add conflict detection (advisory warnings)
- Layer 4: Add merge queue (sequential rebase + validate + merge)
- Layer 5: Add preview server
- Layer 6: Add agent reviews

Each layer is independently deployable and valuable. Derek starts at
Layer 1 and adds layers as his needs grow.

---

## Technical Architecture

### PR Records Table

```sql
CREATE TABLE pr_records (
    id TEXT PRIMARY KEY,           -- "pr-{feature-id}"
    feature_id TEXT NOT NULL REFERENCES features(id),
    project_id TEXT NOT NULL,
    branch TEXT NOT NULL,          -- "agent/{feature-id}"
    base TEXT NOT NULL DEFAULT 'main',
    status TEXT NOT NULL DEFAULT 'open'
        CHECK(status IN ('open','approved','queued','merging','merged',
                         'rejected','conflict','failed')),
    diff_stats TEXT,               -- JSON: {files, insertions, deletions, file_list}
    validation TEXT,               -- JSON: {build, typecheck, tests, timestamp}
    reviewer_notes TEXT,
    queue_position INTEGER,        -- position in merge queue (NULL if not queued)
    worktree_path TEXT,            -- path to the git worktree
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    merged_at TEXT,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_pr_feature ON pr_records(feature_id);
CREATE INDEX idx_pr_status ON pr_records(status);
CREATE INDEX idx_pr_queue ON pr_records(queue_position) WHERE queue_position IS NOT NULL;
```

### Status Flow

```
Agent submits           Human reviews         Merge queue processes
    |                       |                       |
    v                       v                       v
  open ──────────────> approved ──────────────> queued
    |                       |                       |
    |  (reject)             |                       ├── merging ──> merged ✓
    v                       v                       |
  rejected             (re-review)                  ├── conflict ──> (back to agent)
    |                                               |
    v                                               └── failed ──> (back to agent)
  (back to implementing)
```

### CLI Commands

```
tillr pr list                    # List all PRs (open, queued, recent merged)
tillr pr show <id>               # Show PR details + diff summary
tillr pr diff <id>               # Show full diff
tillr pr approve <id>            # Approve → enters merge queue
tillr pr reject <id> --notes "..." # Reject → back to implementing
tillr pr merge <id>              # Direct merge (skip queue, for Derek)
tillr pr preview <id> [--port N] # Start preview server from branch
tillr pr queue                   # Show merge queue status
tillr pr queue retry <id>        # Retry a failed/conflicted PR
```

### Agent Commands (updated)

```
tillr agent claim [feature-id]   # Creates branch + worktree, returns PR-ready
tillr agent submit               # Commits, validates, creates PR, → human-qa
tillr agent next                 # Submit current + claim next
tillr agent heartbeat "msg"      # Progress update
```

### Merge Queue Process (background goroutine)

```go
func (mq *MergeQueue) Process() {
    for {
        pr := mq.NextPending()
        if pr == nil {
            time.Sleep(5 * time.Second)
            continue
        }
        
        pr.Status = "merging"
        
        // 1. Rebase onto main
        if err := gitRebase(pr.Branch, "main"); err != nil {
            pr.Status = "conflict"
            sendBackToAgent(pr, err)
            continue
        }
        
        // 2. Validate
        if err := runValidation(pr.WorktreePath); err != nil {
            pr.Status = "failed"
            sendBackToAgent(pr, err)
            continue
        }
        
        // 3. Merge
        if err := gitMerge(pr.Branch); err != nil {
            pr.Status = "failed"
            sendBackToAgent(pr, err)
            continue
        }
        
        // 4. Cleanup
        pr.Status = "merged"
        cleanupWorktree(pr)
        advanceFeature(pr.FeatureID, "done")
    }
}
```

The queue runs as a background goroutine in the tillr server. It polls
for `queued` PRs, processes them sequentially, and updates the DB. The
UI polls or listens via WebSocket for updates.

### Worktree Management

```
.tillr/
  worktrees/
    global-inbox/          ← git worktree for this feature
    spec-validation/       ← another agent's worktree
    staging-human-qa/      ← optional staging worktree
```

Each worktree is a full checkout. Created by `tillr agent claim`,
cleaned up by the merge queue after merge, or by `tillr pr cleanup`
for abandoned PRs.

Worktrees share the `.git` directory with the main working tree. This
means:
- Branches are visible across all worktrees
- `git log` from any worktree shows the full history
- Disk cost is only the working tree files (not a full clone)
- `node_modules/` needs to be per-worktree (or symlinked with pnpm)

### Config

```yaml
# .tillr.yaml
merge_queue:
  enabled: true                    # false = direct merge (Derek mode)
  auto_skip_conflicts: true        # skip conflicted PRs, process next
  validation:
    - "go build ./..."
    - "npx tsc --noEmit"
    - "go test ./..."
  rebase_before_merge: true        # always rebase onto latest main
```

When `merge_queue.enabled` is false, approve = immediate merge (Layer 1).
When true, approve = enters queue (Layer 4+).

---

## Open Questions

1. **Queue position on re-entry.** When a PR fails and the agent fixes
   it, does it go to the back of the queue or retain its position?
   Probably back of the queue — the fix might interact with things that
   merged while it was out.

2. **Concurrent agents sharing the DB.** Multiple agents write to
   `tillr.db` (claims, heartbeats, submits). SQLite WAL mode handles
   this, but we should set a busy timeout (5s) to avoid "database is
   locked" errors under load.

3. **Worktree disk pressure.** Each worktree is a full checkout. A
   project with 500MB of dependencies and 5 concurrent agents means
   2.5GB of worktree disk. Solutions: shared pnpm store, symlinked
   node_modules, or a max concurrent worktrees config.

4. **Preview server lifecycle.** Who stops the preview server? Options:
   - Timeout (auto-stop after 30 minutes)
   - Explicit stop command
   - Stopped when PR is merged/rejected
   Probably: stopped when PR is merged/rejected, with a manual stop
   command as a fallback.

5. **Merge commit messages.** The merge queue creates merge commits.
   What's the format? Suggestion:
   ```
   Merge: global-inbox (PR #pr-global-inbox)
   
   Feature: Global Inbox Page
   Priority: 9 | Workstream: human-qa-experience
   Approved by: val | Merged by: tillr merge queue
   ```
   This makes `git log --first-parent` on main a clean record of
   merged features.
