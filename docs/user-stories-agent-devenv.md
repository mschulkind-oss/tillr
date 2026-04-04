# User Stories: Agents Need Running Services Too

The previous doc concluded "agents don't need running services." That's
wrong for UI features. When an agent builds a login page, it needs to
actually SEE the page — via Chrome DevTools MCP — to verify layout,
interactions, and visual correctness. This changes the dev environment
story significantly.

---

## What an Agent Actually Does for UI Work

Today's agent workflow for a UI feature in tillr:

1. Edit React components
2. `pnpm build` to check for errors
3. `npx tsc --noEmit` to typecheck
4. Start the server: `tillr serve --port 3848`
5. Use Chrome DevTools MCP to:
   - Navigate to the page
   - Take a screenshot
   - Click buttons, fill forms
   - Verify layout and behavior
6. Iterate: fix issues found in screenshots
7. Commit

Steps 4-6 require a running server and a browser. The agent IS doing
QA as it goes. It's not just building code and hoping — it's looking
at the result.

---

## The Multi-Agent Problem

### Single Agent (today's model)

```
Agent → worktree → pnpm install → tillr serve :3848 → Chrome DevTools
                                                        └→ navigate_page
                                                        └→ take_screenshot
                                                        └→ click, fill, etc.
```

One server, one browser, one DevTools connection. Simple.

### Three Concurrent Agents

```
Agent 1 → worktree-1/ → pnpm install → serve :3848 → Chrome tab 1
Agent 2 → worktree-2/ → pnpm install → serve :3849 → Chrome tab 2
Agent 3 → worktree-3/ → pnpm install → serve :3850 → Chrome tab 3
```

Each agent needs:
1. Its own worktree (git isolation) ✓ — tillr handles this
2. Its own `node_modules/` (dependency isolation) — `pnpm install`
3. Its own server on a unique port — who allocates?
4. Its own browser tab/page — how does DevTools route?
5. Its own Chrome DevTools MCP connection — how?

### Problem 1: Port Allocation

Each worktree's server needs a unique port. Options:

**a) Tillr allocates ports.**
`tillr agent claim` assigns a port from a range (3848-3899) and sets
`TILLR_PORT` in the worktree environment:
```
tillr agent claim login-page
  Branch: agent/login-page
  Worktree: .tillr/worktrees/login-page/
  Port: 3848
```
The agent starts its server with `tillr serve --port $TILLR_PORT`.

**b) Agent picks a port.**
The agent finds a free port itself. More fragile — two agents might
race for the same port.

**c) Dynamic port (port 0).**
The server binds to port 0 and reports its actual port. The agent reads
it from output or a file. Reliable but needs coordination.

**Recommendation: (a) — tillr allocates.** It tracks which ports are in
use via PR records. When the PR is merged/rejected, the port is released.

### Problem 2: Chrome DevTools MCP Routing

This is the hard part. The Chrome DevTools MCP server today runs as a
single process. How does it handle multiple agents?

**How Chrome DevTools MCP works (current):**

```
Claude Code agent
    ↕ (MCP protocol over stdio)
chrome-devtools-mcp process
    ↕ (Chrome DevTools Protocol over WebSocket)
Chromium (headless)
    ├── Tab 1 (page the agent is working with)
    ├── Tab 2 (maybe)
    └── ...
```

The MCP server manages tabs. Tools like `navigate_page`, `take_screenshot`,
`click` operate on the "current" page. `new_page` creates a new tab.
`select_page` switches between tabs.

**For multiple agents, there are several architectures:**

**Architecture 1: One Chrome, multiple tabs**

All agents share one Chrome instance. Each agent creates its own tab
via `new_page` and works in it:

```
Agent 1 → MCP → new_page("http://localhost:3848") → Tab 1
Agent 2 → MCP → new_page("http://localhost:3849") → Tab 2
Agent 3 → MCP → new_page("http://localhost:3850") → Tab 3
```

Problem: each agent talks to the SAME MCP server process. MCP calls
are serialized. Agent 1's `take_screenshot` might return Agent 2's
page if there's a race between `select_page` and `take_screenshot`.

Also: each agent is a separate Claude Code process. They each have
their own MCP server connection (stdio). So actually, each agent
spawns its own `chrome-devtools-mcp` process.

**Architecture 2: One Chrome instance per agent**

Each agent's `chrome-devtools-mcp` process launches its own headless
Chrome. Full isolation but heavy — each Chrome instance uses ~200MB
RAM.

```
Agent 1 → MCP process 1 → Chrome instance 1 → Tab → localhost:3848
Agent 2 → MCP process 2 → Chrome instance 2 → Tab → localhost:3849
Agent 3 → MCP process 3 → Chrome instance 3 → Tab → localhost:3850
```

This is what happens today with `--isolated` flag. Each agent gets
its own Chrome. No cross-talk. But:
- 3 Chrome instances = ~600MB RAM
- 5 agents = ~1GB RAM just for browsers
- 10 agents = resource pressure

**Architecture 3: Shared Chrome, scoped MCP**

One Chrome instance, but each MCP server process connects to it and
manages only its own tabs. Chrome DevTools Protocol supports this via
separate debugging sessions.

```
Chrome (one instance, debug port 9222)
  ├── MCP process 1 → manages Tab 1 only
  ├── MCP process 2 → manages Tab 2 only
  └── MCP process 3 → manages Tab 3 only
```

This requires the MCP server to support connecting to an existing
Chrome instance (via `--cdp-url ws://localhost:9222`) instead of
launching its own. Most Chrome DevTools MCP implementations support
this.

**Recommendation:** Architecture 2 (separate Chrome per agent) is
simplest and safest. The RAM cost is manageable for 3-5 concurrent
agents. Architecture 3 is an optimization for when you have 10+ agents.

### Problem 3: Who Starts the Server?

When an agent claims a feature and creates a worktree, who starts the
dev server in that worktree?

**Option A: The agent starts it.**
The agent runs `tillr serve --port 3848` in the worktree as part of
its implementation workflow. It knows to do this because AGENTS.md or
the feature spec tells it to. The agent manages the server lifecycle.

This is what happens today. It works but it's ad-hoc — the agent has
to know to start a server, pick the right port, and stop it when done.

**Option B: Tillr starts it on claim.**
`tillr agent claim` starts a dev server automatically:
```
tillr agent claim login-page
  Worktree: .tillr/worktrees/login-page/
  Port: 3848
  Server: starting... ✓ http://localhost:3848
```

The server runs as a background process managed by tillr. `tillr agent
submit` stops it. `tillr pr merge` stops it and cleans up.

This is cleaner — the agent doesn't think about servers. It just uses
the allocated port in its DevTools calls. But tillr needs to know HOW
to start the server (the command varies by project).

**Option C: Hybrid — tillr provides the port, agent starts the server.**
`tillr agent claim` allocates a port and writes it to a file:
```
echo 3848 > .tillr/worktrees/login-page/.tillr-port
```
The agent reads the port and starts the server itself. Submit doesn't
stop it — the agent is responsible.

**Recommendation: Option C for now.** Tillr allocates ports. The agent
manages the server. Tillr provides lifecycle hooks for projects that
want auto-start, but doesn't require it.

---

## Story: Three Agents Building UI Features Concurrently

**Context:** Val queues 3 UI features for agents: `global-inbox`,
`feature-detail-redesign`, and `mobile-responsive-sidebar`. Three
agents claim them concurrently.

**What happens:**

1. Agent 1 claims `global-inbox`:
   ```
   tillr agent claim global-inbox
   → Worktree: .tillr/worktrees/global-inbox/
   → Port: 3848
   → Running: pnpm install...
   ```

   Tillr creates the worktree, allocates port 3848, runs the setup
   hook (`pnpm install`). The agent starts its server:
   ```
   cd .tillr/worktrees/global-inbox/
   tillr serve --port 3848 &
   ```

   The agent's Chrome DevTools MCP (started by Claude Code) launches
   a headless Chrome. The agent navigates:
   ```
   mcp: navigate_page("http://localhost:3848/inbox")
   mcp: take_screenshot()
   → sees the inbox page, checks layout
   ```

2. Agents 2 and 3 do the same, on ports 3849 and 3850. Three servers,
   three Chromes, three MCP connections. Full isolation.

3. Agent 1 finishes implementing. It takes a final screenshot to verify:
   ```
   mcp: navigate_page("http://localhost:3848/inbox")
   mcp: take_screenshot()
   → looks good
   ```

   It stops the server and submits:
   ```
   kill %1  # stop background server
   tillr agent submit
   → PR created, port 3848 released, feature → human-qa
   ```

4. Port 3848 is now free. If a 4th agent claims a feature, it can reuse
   the port.

**Resource usage for 3 concurrent agents:**
- 3 worktrees: ~600MB disk (200MB node_modules each, shared Go cache)
- 3 dev servers: ~150MB RAM (Go servers are light)
- 3 Chrome instances: ~600MB RAM (headless Chrome)
- Total: ~750MB RAM, ~600MB disk

For 5 concurrent agents: ~1.25GB RAM, ~1GB disk. Manageable.
For 10: ~2.5GB RAM, ~2GB disk. Starting to get heavy. This is where
Architecture 3 (shared Chrome) would help.

---

## What Tillr Needs to Add

Based on this analysis, the dev environment story for tillr is:

### Must have (for agent worktrees to work):

1. **Port allocation** in PR/claim records
   - Range: configurable, default 3848-3899
   - Allocated on claim, released on merge/reject/abandon
   - Stored in PR record and written to worktree as `.tillr-port`
   - `tillr pr port <id>` to query

2. **Setup hooks** (already discussed)
   ```yaml
   worktree:
     setup: ["pnpm install"]
   ```
   Run after worktree creation. Auto-detect `pnpm-lock.yaml` if no
   explicit config.

3. **Cleanup on merge/reject**
   - Stop any server running on the allocated port
   - Delete the worktree directory
   - Release the port

### Nice to have:

4. **Auto-start server on claim**
   ```yaml
   worktree:
     serve: "tillr serve --port {{port}}"
   ```
   Tillr starts the server as a background process, waits for ready.

5. **Port discovery for DevTools**
   Agent reads `$TILLR_PORT` or `.tillr-port` to know which port to
   navigate to. AGENTS.md documents this convention.

6. **Resource monitoring**
   `tillr pr list` shows disk and memory usage per worktree. Warns
   when approaching limits.

### Not tillr's job:

- Database per worktree (project scripts)
- Docker/container management
- Chrome lifecycle (Claude Code / agent harness handles this)
- MCP server routing (each agent gets its own MCP process)

---

## The Bigger Picture: Agent Harness Integration

The real question is: how does tillr integrate with the agent harness
(Claude Code, Copilot, Gemini CLI)? Today, the agent harness:

1. Launches the agent process
2. Provides MCP servers (Chrome DevTools, etc.)
3. Manages the conversation context
4. Handles worktrees (Claude Code has `isolation: "worktree"`)

And tillr:
1. Manages the work queue
2. Tracks feature status
3. Creates worktrees for agents
4. Manages PRs and merge queue

The overlap is in worktree management. The resolution (decided earlier):
**tillr owns worktrees.** The agent harness should NOT create its own
worktrees — tillr has already created one for the feature.

But the agent harness still manages Chrome/MCP. So the integration is:

```
Agent harness (Claude Code)
  ├── Launches agent with MCP servers (Chrome, etc.)
  ├── Agent runs `tillr agent claim` → gets worktree + port
  ├── Agent starts server on allocated port
  ├── Agent uses Chrome DevTools MCP to test on that port
  ├── Agent runs `tillr agent submit` → PR created
  └── Agent exits, harness manages Chrome lifecycle

Tillr (background)
  ├── Merge queue processes approved PRs
  ├── Rebases, validates, merges
  └── Cleans up worktrees + ports
```

The agent prompt is simple:
```
Process the tillr queue. For each feature:
1. Run `tillr agent claim` (gives you a worktree and port)
2. Implement in the worktree
3. Start the server on your allocated port
4. Test with Chrome DevTools
5. Run `tillr agent submit`
```

No environment management in the prompt. Tillr handles worktrees and
ports. The agent harness handles Chrome. The project handles dependencies
(via setup hooks). Clean separation.
