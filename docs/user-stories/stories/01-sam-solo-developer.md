# 1. Sam — Solo Developer, Side Project with Agents

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

« [All stories](./README.md) · [User-stories overview](../README.md)
