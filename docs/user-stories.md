# User Stories: Tillr

Seven human users and three agent users working with tillr.
Each story follows them through real workflows, with exact
technical steps and the gaps we need to close.

---

## 1. Sam — Solo Developer, Side Project with Agents

**Context:** Sam has a Next.js app he's been building weekends. He's been
using Claude Code ad-hoc — "add a login page," "fix the mobile layout." It
works, but he's lost track of what's done, what's half-done, and what he
told the agent to do three weeks ago. He saw tillr mentioned in a thread
about managing agent output.

**Discovery:** He finds tillr and realizes: this isn't a project management
tool for humans — it's a project management tool that sits between him and
his agents.

**First 10 minutes:**

1. Installation. Sam needs the binary and the web UI.
   - `go install github.com/mschulkind-oss/tillr@latest` — gets the CLI
   - Or downloads a release binary

2. `cd ~/code/my-app && tillr init my-app` — creates `.tillr.json` and
   `tillr.db` in the project root. Adds `tillr.db` and `.tillr-backups/`
   to `.gitignore`. Creates a default project record in the DB.

   ```
   Initialized tillr project "my-app"
     Config: .tillr.json
     Database: tillr.db
     Server: tillr serve (port 3847)
   ```

3. `tillr serve` — starts the web UI at localhost:3847. Sam opens it in
   a browser. He sees an empty dashboard — no features, no workstreams,
   no roadmap. It looks clean but he has no idea what to do next.

   **Gap:** The empty state should guide him. "Add your first workstream"
   or "Import from your existing TODO/README." Right now it's just blank
   cards showing zeros.

4. He starts adding what he knows he needs:
   ```
   tillr workstream create "Authentication" --description "Login, signup, password reset"
   tillr feature add "login-page" --description "Email/password login" --priority 8
   tillr feature add "signup-flow" --description "Registration with email verification" --priority 7
   ```

5. He opens the workstream page and sees his features listed. He can see
   status (both draft), priority ordering. Good.

**The first agent run:**

6. Sam wants an agent to implement `login-page`. Today he'd just tell
   Claude Code "implement a login page." But with tillr, the flow should be:
   ```
   tillr agent claim login-page
   ```
   This sets the feature to `implementing` and creates an agent claim. But
   Sam isn't an agent — he's a human telling an agent what to do.

   **Gap:** The bridge between "human decides what's next" and "agent starts
   working" is unclear. Does Sam:
   - (a) Run `tillr agent claim` himself, then tell Claude "you're working
     on login-page, get the spec from `tillr feature show login-page`"?
   - (b) Tell Claude "claim the next thing from the tillr queue and implement
     it"?
   - (c) Have a wrapper script that does both?

   Today the answer is (b) — the agent runs `tillr agent next` which claims
   and implements in a loop. But Sam has to know to set that up. Nothing in
   the UI or CLI tells him "here's how to connect an agent."

7. The agent finishes and runs `tillr agent submit`. The feature moves to
   `human-qa`. Sam sees it in his workstream inbox.

**The QA moment (where it breaks down today):**

8. Sam opens the workstream page. He sees "1 item needs QA." He clicks
   through to the feature. He sees... the feature name, description, and
   status. Maybe a diff link if the agent made a PR.

   **Gap:** There's no test plan. No "here's what to check." No screenshots.
   No "before/after." Sam has to figure out what changed and whether it's
   right by reading code or just trying the app. For a login page that's
   easy — he can just look at it. For a database migration or API refactor,
   he's lost.

   What Sam needs to see:
   - What the agent did (summary, not the full diff)
   - What to verify (human-readable checklist: "try logging in with wrong
     password," "check the signup link works")
   - Screenshots if it's UI work
   - A big approve/reject button with a notes field

9. Sam approves. The feature moves to `done`. He feels good. He goes back
   to the workstream page and sees what's next.

**What would trip him up:**
- No guided "connect your agent" flow. He has to know about `tillr agent`
  commands and how to tell his agent to use them.
- Empty state in the UI gives no direction.
- QA is a status, not an experience. There's no guided review flow.
- He doesn't know about the daemon yet — when he starts a second project,
  he'll run `tillr serve` again on a different port and juggle browser tabs.

**The aha moment:** Two weeks later, Sam has 15 features done across two
workstreams. He opens the history page and can see exactly what happened,
when, and in what order. He can search for "why did we add that validation?"
and find the feature spec and QA notes. His project has institutional memory
now, and he didn't have to maintain it — it accumulated through the workflow.

---

## 2. Kenji — Tech Lead, Setting Up Tillr for a Team

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

## 3. Lisa — Product Owner, Only Touches the UI

**Context:** Lisa is non-technical. She works with a dev team that uses tillr.
She doesn't use the CLI. She doesn't read code. She opens the tillr web UI
to see what's happening and make decisions.

**What she needs from the UI:**

1. **"What needs my attention?"** — She opens tillr and the first thing she
   sees should be her inbox. Items grouped by urgency:
   - Features waiting for QA (approve/reject decisions)
   - Blocked features (need her input to unblock)
   - Features that were rejected and resubmitted (re-review)
   - Open questions from agents or devs

   **Gap:** Today the workstream page has a "Human Inbox" section, but:
   - It's per-workstream, not global. Lisa has to click into each workstream
     to see what needs attention. She needs a global inbox across all
     workstreams.
   - Items don't explain what she should do. "Feature X is in human-qa" tells
     her nothing. She needs "Feature X: Login page is ready for review. Check
     that the layout matches the mockup. [Approve] [Reject]"
   - There's no priority ordering within the inbox. High-priority QA items
     should be at the top.

2. **"What's the state of the project?"** — The dashboard should give her
   a one-glance answer: how many features are done, how many are in
   progress, how many are waiting for her. A burndown or progress bar per
   workstream.

   **Gap:** The dashboard exists but it's developer-focused — cycle counts,
   agent sessions, technical metrics. Lisa wants: "Auth workstream: 8/12
   features done. 2 waiting for your review."

3. **"I want to change priorities."** — She should be able to drag-and-drop
   or re-order the roadmap. Today she'd have to ask a dev to run CLI
   commands.

   **Gap:** The roadmap page is read-only in the UI. All mutations go through
   the CLI. Lisa can't reprioritize from the browser.

4. **QA review flow.** When Lisa clicks on a feature in her inbox, she needs:

   a. **Summary**: "This feature adds email/password login to the app." One
      paragraph, written for a non-technical reader.

   b. **What to check**: A human-readable checklist. Not "verify the bcrypt
      rounds are >=12" but "try logging in with a wrong password — you should
      see an error message." These items are things she can actually verify
      by using the app.

   c. **Visual diff** (for UI features): Before/after screenshots, or a link
      to a preview deployment.

   d. **Approve/Reject** with a required notes field on rejection. The notes
      go back to the agent as context for the next iteration.

   **Gap:** Today QA is a status badge and a checklist that's mostly
   technical. The checklist items come from the agent's automated QA, not
   from a human-oriented test plan. The approve/reject buttons exist on the
   workstream detail page but they're small and the flow isn't guided.

**What would trip her up:**
- She can't find the approve/reject buttons. They're on the workstream
  detail page, nested under a feature, under a QA section. She needs them
  front and center in the inbox.
- She doesn't understand status labels like "human-qa" or "agent-qa." She
  wants "Ready for your review" and "Agent is checking."
- She can't add a feature or create a roadmap item from the UI. She has
  to ask a developer. Basic CRUD in the UI would help.
- No notifications. She has to remember to check tillr. Email or Slack
  notifications when something needs her attention would close the loop.

---

## 4. Marcus — Developer, Onboarding to an Existing Tillr Project

**Context:** Marcus just joined a team that uses tillr. There are 6 workstreams,
40 features, and a year of history in the database. He needs to get oriented.

**First 10 minutes:**

1. He clones the repo. He sees `.tillr.json` and `tillr.db` in the root.
   He runs `tillr serve` and opens the UI.

2. He sees the dashboard with real data — progress bars, active workstreams,
   recent activity. Good start.

3. He clicks into a workstream. He sees features, their statuses, a timeline
   of what happened. He can drill into any feature to see its full history:
   spec, QA results, agent discussions, status transitions.

4. He wants to find why a particular decision was made. He searches:
   ```
   tillr search "why did we switch to PostgreSQL"
   ```
   And finds a discussion thread from 3 months ago where the team debated
   database options. The decision and rationale are captured.

   **Gap:** Search works on feature names and descriptions, but does it
   search discussions, QA notes, and specs? It should search everything.

5. He wants to understand the architecture. The roadmap page shows the
   big picture — what's planned, what's in progress, what's done. The
   workstream view shows how features group together.

**What would trip him up:**
- The database is in `.gitignore`, so he has it locally but not on every
  clone. If he clones fresh on another machine, there's no `tillr.db`. He
  needs either:
  - A `tillr export/import` flow (export to git-tracked files, import on
    clone)
  - The daemon syncing DBs between machines (doesn't exist)
  - A team-shared DB location (not the current model)

  **Gap:** This is the "tillr is local-only" problem. History doesn't
  travel with the repo. The `export-git` feature exists but isn't wired
  into the standard workflow.

- He doesn't know the CLI commands. The UI is browse-only for most things.
  He needs a `tillr guide` or the UI needs basic mutation capabilities.

---

## 5. Rachel — Developer, Managing Multiple Projects via Daemon

**Context:** Rachel works on 3 projects simultaneously. She wants one browser
tab showing all of them with easy switching. She heard about `tillr daemon`.

**Setup:**

1. Each project already has tillr set up locally (`.tillr.json` + `tillr.db`).
   She runs CLI commands in each project directory — no switching, no context.
   This already works.

2. She wants the unified UI:
   ```
   tillr daemon init --project ~/work/api --project ~/work/frontend --project ~/work/infra
   ```
   This creates `~/.config/tillr/daemon.json`:
   ```json
   {
     "projects": [
       {"path": "/home/rachel/work/api", "slug": "api"},
       {"path": "/home/rachel/work/frontend", "slug": "frontend"},
       {"path": "/home/rachel/work/infra", "slug": "infra"}
     ],
     "port": 3847
   }
   ```

3. `tillr daemon start` — starts one HTTP server serving all three projects.
   The UI shows a project switcher in the sidebar.

4. She opens `localhost:3847` and sees her current project. The sidebar shows
   a dropdown with api / frontend / infra. She switches between them
   instantly — no page reload, just a different dataset.

**What she notices:**
- CLI in each project directory just works. `cd ~/work/api && tillr feature list`
  shows api's features. `cd ~/work/frontend && tillr feature list` shows
  frontend's features. No `tillr project switch`. The directory IS the context.
- The daemon is read-only for a project's data — it just serves what's in
  each project's local `tillr.db`. Writes still happen through the CLI in
  each project directory.
- Adding a new project means: `tillr init` in that directory, then edit
  `daemon.json` to add the path, then restart the daemon.

   **Gap:** `tillr daemon add ~/work/new-project` should exist — today she
   has to manually edit the JSON file. The daemon should also auto-reload
   config on change instead of requiring a restart.

**What would trip her up:**
- The daemon serves each project's local DB. If she's SSH'd into a remote
  machine, the daemon needs to run there too. There's no remote access
  story.
- The project switcher in the UI works, but there's no cross-project view.
  She can't see "what needs my attention across all projects." Each project
  is fully isolated.

  **Gap:** A global inbox across projects in daemon mode — "api has 3 items
  for QA, frontend has 1 blocked feature." This would be the daemon's
  landing page.

- WebSocket connections need to be project-scoped in daemon mode, otherwise
  changes in one project trigger refreshes in another.

---

## 6. Agent — Claiming and Implementing from the Queue

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

## 7. Agent — Working in a Worktree/Jail

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

## 8. Agent — Performing Automated QA

**Context:** After an implementing agent submits a feature, a QA agent can
run automated checks before the feature reaches human QA. This is the
`agent-qa` phase.

**What the QA agent does:**

1. Claims a feature in `agent-qa` status:
   ```
   tillr feature list --status agent-qa --json
   ```

2. For each feature, runs automated checks based on the feature type:
   - **UI features:** Takes screenshots, checks responsive layouts, verifies
     accessibility basics.
   - **API features:** Runs the test suite, checks endpoint responses match
     the spec.
   - **CLI features:** Runs the command with various inputs, verifies output
     format.
   - **All features:** Runs the build, runs the linter, checks for
     regressions.

3. Records results:
   ```
   tillr qa submit login-page --passed --notes "All checks pass. Screenshots attached."
   ```
   or
   ```
   tillr qa submit login-page --failed --notes "Login form doesn't handle empty email field."
   ```

4. On pass, the feature advances to `human-qa`. On fail, it goes back to
   `implementing` with the failure notes attached.

**Gap: What guides the QA agent's testing?**

Today there's no structured test plan in the feature. The QA agent has to
infer what to test from the spec. For consistent quality, each feature
should have a `test_plan` field — distinct from the `spec` — that tells
both agent and human QA what to check.

The test plan should have two sections:
- **Agent checks:** Things that can be automated. "Build succeeds."
  "Tests pass." "Endpoint returns 200." "Screenshot matches baseline."
- **Human checks:** Things only a human can judge. "Login page looks right."
  "Error messages are clear." "Flow feels natural."

The agent runs the agent checks. The human reviews the human checks. Neither
wastes time on the other's domain.

**Gap: How does the QA agent know what TYPE of feature it is?**

Features don't have a `type` field today (ui, api, cli, migration, etc.).
The QA agent has to guess from the name and spec. Adding a type would let
the QA agent pick the right test strategy automatically.

---

## 9. Val — Developer, Losing Track of What Needs Attention

**Context:** Val has been using tillr for a month. There are 4 workstreams,
60 features, and agents running periodically. Things are moving, but Val has
lost the thread. She opens tillr and doesn't know where to start.

**What she sees today:**

- Dashboard: numbers and charts. "47 features, 12 done, 8 in progress."
  Okay, but what does she need to DO?
- Workstream pages: each one has features in various states. She has to
  click into each workstream, scan the features, look for human-qa items,
  check for blocked features, read discussion threads.
- Queue page: shows what agents can work on. Not really for her.
- No inbox. No notifications. No "start here."

**What she needs — the Global Inbox:**

Val opens tillr and the first page she sees is her inbox. It shows everything
across all workstreams that needs a human decision, ordered by priority:

```
Your Inbox (7 items)

QA Review (3)
  login-page               Authentication    Priority 8    2 hours ago
    "Login form with email/password. Check: layout looks right, error
     messages are clear, forgot password link works."
    [Approve] [Reject]

  signup-validation         Authentication    Priority 7    5 hours ago
    "Email validation and password strength. Check: try weak passwords,
     check the error messages make sense."
    [Approve] [Reject]

  api-rate-limiting         API Gateway       Priority 9    1 day ago
    "Rate limiting on public endpoints. Check: hit the endpoint rapidly,
     verify you get a 429 after the limit."
    [Approve] [Reject]

Blocked (2)
  oauth-integration         Authentication    Priority 9    3 days ago
    Blocked on: "Need Google OAuth client ID. Who creates this?"
    [Provide Answer] [Defer]

  database-migration        Data Layer        Priority 8    1 day ago
    Blocked on: depends on schema-redesign (in progress)
    [No action needed — auto-resolves when dependency completes]

Previously Rejected (1)
  mobile-layout             UI Polish         Priority 6    12 hours ago
    Rejected 2 days ago: "Header overlaps on iPhone SE"
    Agent resubmitted with fix. Re-review.
    [Approve] [Reject]

Needs Spec (1)
  search-api                API Gateway       Priority 8
    "Full-text search endpoint" — no spec, can't be claimed by agents.
    [Write Spec] [Defer]
```

**Gap:** This inbox doesn't exist today. The workstream detail page has a
"Human Inbox" section that lists categories, but:
- It's per-workstream, not global
- Items show feature names but not what to do about them
- There's no inline approve/reject — you have to navigate to the feature
  detail page
- There's no "what to check" summary — you see the raw spec, not a
  human-oriented test plan
- Features without specs aren't flagged as a problem

**What Val does with the inbox:**

1. She starts at the top (highest priority). `api-rate-limiting` — she
   opens a terminal, hits the endpoint 100 times with curl, sees the 429
   response. Looks right. She clicks [Approve] and types "Verified rate
   limiting works. 429 after 50 requests."

2. `login-page` — she opens the app, tries logging in with wrong password,
   sees the error message. Tries with right password, gets in. Clicks
   [Approve].

3. `oauth-integration` is blocked. She creates a Google OAuth app, gets
   the client ID, clicks [Provide Answer] and pastes it. The feature
   unblocks and goes back to the agent queue.

4. `search-api` needs a spec. She clicks [Write Spec] and types:
   ```
   GET /api/search?q=<query>
   - Searches features, discussions, and roadmap items
   - Returns top 20 results ranked by relevance
   - Each result has: type, title, snippet, link
   - Empty query returns 400
   ```
   The feature is now claimable by agents.

5. She's done in 15 minutes. She closes tillr and goes back to other work.
   When agents finish more features, she'll have new inbox items tomorrow.

**The aha moment:** Val realizes she's spending 15 minutes a day on tillr
and getting more done than when she spent 2 hours a day manually reviewing
PRs and writing Claude prompts. The structure made her faster, not slower.

---

## 10. Derek — Developer, Adding a Second Project to Tillr

**Context:** Derek has tillr running on his main project. It's going well —
agents are processing the queue, he's QAing features, everything is tracked.
Now he wants to use tillr on a second project.

**What he expects:**

1. Go to the new project directory, run `tillr init`, and have it work
   independently. His main project is unaffected.

2. Open one browser tab and see both projects, with easy switching.

3. CLI commands in each directory operate on that project's data. No
   "switching" in the CLI — the directory IS the project.

**What actually happens today:**

1. `cd ~/work/new-project && tillr init new-api` — this works. Creates
   `.tillr.json` and `tillr.db`. Completely independent from his other
   project.

2. He's been running `tillr serve` in his main project. Now he needs the
   daemon:
   ```
   tillr daemon init --project ~/work/main-project --project ~/work/new-project
   ```
   This creates `~/.config/tillr/daemon.json`. He stops `tillr serve`
   and starts `tillr daemon start`.

   **Gap:** The transition from single-project `serve` to multi-project
   `daemon` is manual and undocumented. It should be:
   - Detect that `tillr serve` is running on the same port and offer to
     replace it
   - Or better: `tillr serve` should auto-upgrade to daemon mode when it
     detects other projects exist in the daemon config

3. The UI now shows a project switcher in the sidebar. He can flip between
   projects. Each project has its own features, workstreams, queue.

4. CLI just works:
   ```
   cd ~/work/main-project && tillr feature list    # shows main project features
   cd ~/work/new-project && tillr feature list     # shows new project features
   ```
   No switching command, no context confusion. This is the right model.

**What would trip him up:**
- He doesn't know `tillr daemon` exists. After running `tillr init` on the
  second project, he might just run `tillr serve --port 3848` and juggle
  two browser tabs. Nothing suggests the daemon.

  **Gap:** `tillr init` should detect existing projects (by checking
  `~/.config/tillr/daemon.json` or scanning common paths) and suggest
  daemon mode: "You have 2 tillr projects. Run `tillr daemon start` to
  see both in one UI."

- Adding a third project later means editing `daemon.json` manually.
  `tillr daemon add ~/work/third-project` should exist.

- The daemon needs to be a persistent service (systemd/launchd), not
  something he runs in a terminal. `tillr daemon install-service` should
  exist.

  **Gap:** The daemon CLI exists (`tillr daemon start`) but there's no
  service installation. The user has to manage the process themselves.

---

## Friction Points -> Feature Gaps

Things that came up across stories. These map to roadmap work.

| # | Friction Point | Who hits it | Status |
|---|---------------|-------------|--------|
| 1 | Empty state in UI gives no guidance | Sam, Marcus | Missing |
| 2 | No guided "connect your agent" flow | Sam, Kenji | Missing |
| 3 | QA is a status, not an experience (no test plan, no guided review) | Sam, Lisa, Val | Missing — `human-qa-experience-design` in draft |
| 4 | No global inbox across workstreams | Lisa, Val | Missing |
| 5 | Features can enter queue without specs | Val, Agent | Missing — no validation |
| 6 | Test plan field (separate from spec) with agent vs human sections | Agent QA, Lisa | Missing |
| 7 | UI is read-only for most mutations (can't add features, reprioritize) | Lisa, Marcus | Partial — some inline editing exists |
| 8 | No notifications (email, Slack, browser) | Lisa | Missing |
| 9 | `tillr daemon add <path>` command | Rachel, Derek | Missing |
| 10 | Daemon auto-reload config on change | Rachel | Missing |
| 11 | Cross-project global inbox in daemon mode | Rachel, Derek | Missing |
| 12 | `tillr serve` → daemon upgrade path | Derek | Missing |
| 13 | Service installation for daemon (systemd/launchd) | Derek | Missing |
| 14 | Conflict detection between concurrent agents | Kenji | Missing |
| 15 | SQLite busy timeout for concurrent agent access | Kenji | Unverified |
| 16 | `tillr agent submit` degrades without `gh` | Agent in jail | Missing |
| 17 | Feature type field (ui, api, cli, migration) | Agent QA | Missing |
| 18 | Richer claim response (includes spec, context, workstream) | Agent | Partial |
| 19 | `tillr onboard --yes` non-interactive mode | Kenji | Exists but may need polish |
| 20 | Export/import for portable history (DB travels with repo) | Marcus | `export-git` exists, not in workflow |
| 21 | Human-readable status labels in UI ("Ready for review" not "human-qa") | Lisa | Missing |
| 22 | Spec-required validation before features are claimable | Val, Agent | Missing |
| 23 | Inline approve/reject in global inbox with summary | Val, Lisa | Missing |

## The Key Insight (Across All Users)

Tillr's value is **making the human's 15 minutes count.** The human doesn't
need to be in the loop constantly — they need a clear, prioritized inbox of
decisions to make, with enough context to make them quickly, and confidence
that everything else is handled.

The agent's value comes from **never having to guess what to do.** The queue
decides priority. The spec defines the work. The status machine defines the
flow. The agent just processes.

Every friction point above is a place where either the human has to think
about workflow (instead of decisions) or the agent has to guess (instead of
following structure). Closing these gaps means: faster humans, better agents,
higher quality output.
