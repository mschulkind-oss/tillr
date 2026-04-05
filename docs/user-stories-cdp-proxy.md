# Multi-Agent Browser Isolation for UI QA

How do multiple concurrent agents each get isolated browser access for
UI testing? This doc traces the problem through three candidate solutions,
landing on: existing MCP gateway tools already solve most of this, and
the one novel piece — cookie isolation via `*.localhost` hostnames — is
a configuration trick, not a tool to build.

---

## The Problem

When an agent builds a UI feature, it needs to SEE the result. The
workflow: start a dev server, open Chrome via Chrome DevTools MCP,
navigate pages, take screenshots, click buttons, verify layout. This
works great for one agent.

With multiple concurrent agents (the tillr model — each agent claims a
feature, works in a worktree, submits a PR), each needs isolated browser
access. Two problems arise:

### Problem 1: MCP Server Sharing

Agent harnesses (Claude Code, Copilot, Gemini CLI) run subagents that
**share the parent's MCP server processes**. Chrome DevTools MCP has
global state — "the current page." Two subagents calling `navigate_page`
and `take_screenshot` concurrently will clobber each other.

This is a known issue:
- anthropics/claude-code#32514 (agent identity context for MCP)
- anthropics/claude-code#4476 (per-agent MCP configuration)

It affects ALL stateful MCP servers, not just Chrome DevTools. Terminal
sessions, database consoles, PowerShell — anything with per-connection
state breaks when shared across subagents.

**Separate top-level processes** (e.g., multiple `claude -p` invocations)
each get their own MCP servers. But this only works for Claude Code —
Copilot's coding agent runs as a single process. And tillr shouldn't
depend on harness-specific process models.

### Problem 2: Cookie Leakage in Shared Chrome

Even with separate MCP server instances, if they share a Chrome instance
(for RAM savings), cookies leak across agents. Cookies are scoped by
domain, not port. A session cookie from `localhost:3848` is sent to
`localhost:3849`. Agent 2 sees Agent 1's login state.

---

## Solution Evolution

We explored three approaches, each building on the last:

### Approach 1: CDP Proxy (Too Low-Level)

A WebSocket proxy that sits between MCP servers and Chrome, routing CDP
commands to session-scoped tabs. Each agent gets a session; the proxy
ensures agents can only see their own tabs.

**Problem:** This solves Chrome-level isolation but not MCP-level sharing.
The MCP server itself is the bottleneck — it has global "current page"
state. Even with perfectly isolated Chrome tabs, two subagents sharing
one MCP server process still clobber each other. The proxy sits below
the problem.

### Approach 2: MCP Multiplexer (Right Idea)

An MCP server that wraps other MCP servers and adds session isolation:

```
Agent harness (Claude Code, Copilot, Gemini CLI)
    │
    ▼
MCP Multiplexer (one MCP server, the only one the harness sees)
    │
    ├── session "agent-1" → Chrome DevTools MCP instance 1 → Chrome 1
    ├── session "agent-2" → Chrome DevTools MCP instance 2 → Chrome 2
    └── session "agent-3" → Chrome DevTools MCP instance 3 → Chrome 3
```

The multiplexer exposes the same tools as the child MCP server, but adds
a `session` parameter to each. On first call with a new session ID, it
spawns a new child instance. Routes tool calls based on `session`. Tears
down instances when sessions end.

This is harness-agnostic (works with any MCP client), generic (any
stateful MCP server can sit behind it), and solves both the MCP sharing
problem and the Chrome isolation problem.

### Approach 3: Use Existing MCP Gateways (Don't Build It)

This pattern already exists. Multiple production-grade tools do exactly
this:

**Microsoft MCP Gateway** (github.com/microsoft/mcp-gateway):
- Session-aware stateful routing — all requests with the same
  `session_id` route to the same MCP server instance
- Automated spawn/teardown of isolated backend instances
- Separate control plane (manage adapters/tools) and data plane
  (route requests)

**MetaMCP** (github.com/metatool-ai/metamcp):
- Aggregates multiple MCP servers under one proxy
- Namespace-based grouping for per-tenant/session isolation
- Middleware pipeline for request/response transformation
- Multiple transport options (SSE, Streamable HTTP, OpenAPI)

**McpMux** (github.com/mcpmux/mcp-mux):
- Local gateway — configure servers once, connect through single endpoint
- "Spaces" for workspace isolation
- Feature Sets for granular access control

**The MCP protocol itself** has built-in session support via the
`Mcp-Session-Id` header. The server assigns a session ID at init;
clients include it in all subsequent requests. Gateways leverage this
to route sessions to separate backend instances.

---

## What Tillr Actually Needs to Do

Given that MCP gateways exist, tillr's job is:

### 1. Port Allocation (already planned)

Each agent gets a unique dev server port. Tillr allocates from a range
(3848-3899), writes to `.tillr-port` in the worktree.

### 2. Session Identity

When tillr creates a worktree for an agent, it assigns a session ID
(could just be the feature slug). The agent uses this session ID when
talking to the MCP gateway. Tillr provides it as an environment variable:

```
TILLR_SESSION_ID=global-inbox
TILLR_PORT=3848
TILLR_HOSTNAME=s-global-inbox.localhost
```

### 3. MCP Gateway Configuration (documentation, not code)

Tillr documents how to configure an MCP gateway for multi-agent work.
Example with Microsoft MCP Gateway:

```yaml
# mcp-gateway config
routes:
  chrome-devtools:
    command: "chrome-devtools-mcp --headless --isolated"
    routing: session    # one instance per session
    session_key: env:TILLR_SESSION_ID
```

Or with MetaMCP:

```yaml
servers:
  chrome-devtools:
    command: "chrome-devtools-mcp --headless --isolated"
    namespace: "{{env.TILLR_SESSION_ID}}"
```

Tillr doesn't manage the gateway. It provides the session identity.
The user picks and configures a gateway.

### 4. Hostname Assignment for Cookie Isolation

This is the one piece that existing tools DON'T handle, because it's
a browser-level concern, not an MCP concern.

**The trick:** `*.localhost` subdomains resolve to `127.0.0.1` per
RFC 6761. Chrome treats each subdomain as a separate cookie domain.

```
Agent 1 → s-global-inbox.localhost:3848   → isolated cookies
Agent 2 → s-feature-detail.localhost:3849 → isolated cookies
Agent 3 → s-mobile-sidebar.localhost:3850 → isolated cookies
```

Tillr assigns the hostname when it creates the worktree. The agent
navigates to `http://$TILLR_HOSTNAME:$TILLR_PORT/` instead of
`http://localhost:$TILLR_PORT/`. Full cookie, localStorage,
sessionStorage, IndexedDB, and service worker isolation.

**Caveat:** Dev servers that validate the `Host` header need
`*.localhost` in their allowed hosts config:

```js
// next.config.js
allowedDevHosts: ['*.localhost']

// vite.config.ts
server: { host: true }

// webpack-dev-server
allowedHosts: ['.localhost']
```

This is a one-line config change per project, documented by tillr.

---

## The Realistic Near-Term Path

1. **Single agent at a time** — most immediate work is sequential anyway.
   One agent claims a feature, implements, QAs with its own Chrome, submits.
   No isolation needed.

2. **Multiple agents, no UI QA** — agents that only build/test (no browser
   interaction) can run in parallel trivially. Go builds, TypeScript
   typechecking, unit tests — all stateless.

3. **Multiple agents with UI QA** — when this becomes real:
   - Tillr provides session identity + hostname + port
   - User configures an MCP gateway (Microsoft, MetaMCP, or McpMux)
   - Each agent gets an isolated Chrome DevTools MCP instance via the
     gateway's session routing
   - Cookie isolation via `*.localhost` hostnames
   - No custom tooling to build

---

## What We Don't Build

- **No CDP proxy.** MCP gateways handle the isolation at the right layer.
- **No custom MCP server.** Existing gateways do session routing.
- **No Chrome multiplexer.** With `--isolated` flag, each MCP instance
  gets its own Chrome. RAM is acceptable for 3-5 agents.

## What We Do

- **Port allocation** in tillr PR records (already planned)
- **Session identity** as environment variable (`TILLR_SESSION_ID`)
- **Hostname assignment** per worktree (`TILLR_HOSTNAME`)
- **Documentation** on configuring MCP gateways for multi-agent work
- **`.tillr.yaml` example** showing gateway + Chrome DevTools setup

---

## Open Questions

1. **Which MCP gateway to recommend.** Microsoft MCP Gateway is the most
   feature-complete but Kubernetes-oriented. McpMux is local-first but
   newer. MetaMCP is flexible but adds complexity. Need to test which
   works best for local multi-agent development.

2. **Gateway startup.** Who starts the gateway? The user manually? Tillr
   on first `agent claim`? A startup script? Same question we had with
   the CDP proxy — probably "user starts it manually" for simplicity.

3. **Agent prompt integration.** How does the agent know its session ID
   and hostname? Environment variables are the mechanism, but the agent
   prompt needs to reference them. `AGENTS.md` should say "use
   `$TILLR_HOSTNAME:$TILLR_PORT` for browser URLs."

4. **Upstream fix timeline.** If Claude Code and Copilot add per-agent
   MCP isolation natively (anthropics/claude-code#32514), the gateway
   becomes unnecessary. Worth tracking.
