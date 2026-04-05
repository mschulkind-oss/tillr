# User Stories: Chrome DevTools Protocol Proxy for Multi-Agent QA

A standalone utility — separate from tillr — that lets multiple concurrent
agents share one Chrome instance for UI QA via the Chrome DevTools Protocol.

---

## Motivation: Is This Worth Building?

### The use case we have

Background agents implementing UI features in parallel. Each agent needs
a browser to QA its own work — navigate pages, take screenshots, click
buttons, fill forms, verify layout. Today each agent spawns its own
Chrome DevTools MCP server, which spawns its own headless Chrome. One
agent = one Chrome instance = ~200MB RAM. Three agents = ~600MB. Ten
agents = ~2GB just for browsers.

That's the tillr use case: `tillr agent claim` creates a worktree, the
agent implements and QAs in that worktree, submits a PR. Multiple agents
doing this concurrently each need isolated browser access.

### Is that enough?

Honestly, maybe not on its own. Saving 400MB of RAM across 3 agents is
nice but not compelling — dev machines have 32-64GB. The RAM argument
only matters at scale (10+ agents), and most people aren't there yet.
We're not there yet either — the agent workflow isn't even built.

The stronger argument is **operational simplicity**. One Chrome process
to monitor, one debug port, one thing to restart if it wedges. With 5
separate Chrome instances, if one hangs, which agent's is it? With a
proxy, there's one Chrome and a dashboard showing all sessions. But
this is a convenience, not a necessity.

### Who else would use this?

**Multi-agent coding tools.** Any system that runs multiple AI agents
concurrently where each needs browser access. This isn't tillr-specific.
Claude Code with `isolation: "worktree"` spawns agents that could each
need Chrome. Copilot Workspace, Devin, SWE-agent — any tool running
parallel AI coding agents with UI testing hits this same problem.

**CI/CD with parallel browser tests.** Test suites that fan out across
multiple workers, each needing a browser. But this is already well-served
by Playwright (which manages browser contexts with full isolation) and
Selenium Grid. Those tools solved multi-tenant browser access years ago.
A CDP proxy doesn't add much here.

**Web scraping / research agents.** Multiple AI agents browsing different
sites concurrently. But different domains already isolate cookies, so the
hostname trick isn't needed. And Browserless.io already serves this market
as a cloud service. Not a compelling gap.

**Local MCP development.** Someone building or testing an MCP server that
uses Chrome DevTools and wants to run multiple instances without multiple
Chromes. Niche but real — MCP development is growing.

### The honest assessment

The primary audience is **multi-agent AI coding tools doing UI work**.
That's a real and growing category, but it's early. Today, most people
run one agent at a time. The parallel-agents-with-browsers scenario is
emerging but not yet mainstream.

The risk: building this before the underlying workflow exists. We don't
have agents claiming features, working in worktrees, and QAing via
Chrome DevTools yet. By the time we do, the landscape may have changed
— Chrome DevTools MCP might support session scoping natively, or agent
harnesses might manage browser lifecycle themselves.

**The case for building it anyway:**

1. It's small (~500 lines). The CDP protocol is well-documented. The
   WebSocket proxying is mechanical. This isn't a 6-month project.

2. It's standalone. No coupling to tillr. If it turns out to be useless,
   nothing depends on it. If it turns out to be useful, it composes with
   anything that speaks CDP.

3. The hostname isolation trick is genuinely novel. `*.localhost` for
   per-session cookie scoping in a shared browser is not something
   existing tools do. If we don't build this, someone will — the
   multi-agent browser problem is real.

4. It's a forcing function for the agent workflow. Building the proxy
   means thinking concretely about how agents interact with browsers,
   which informs the worktree/port/session design in tillr.

**The case for waiting:**

1. Architecture 2 (separate Chrome per agent) works. The RAM cost is
   acceptable for 3-5 agents. We can always add the proxy later when
   scale demands it.

2. The agent workflow doesn't exist yet. Building browser infrastructure
   for agents that don't exist is premature.

3. Chrome DevTools MCP is evolving. The session isolation problem might
   get solved upstream.

**Recommendation: wait.** Use separate Chrome instances (Architecture 2)
when the agent workflow is built. Track the pain. If 5+ concurrent agents
become normal and Chrome RAM is the bottleneck, build the proxy then. The
design is documented here — it won't be lost.

If the hostname isolation trick proves useful in a different context
first (testing, development), that's a signal to build it sooner.

---

## Technical Design (For When We Build It)

### What the problem actually is

When an agent builds a UI feature, it needs to SEE the result. Today's
workflow: the agent starts a dev server, launches headless Chrome via the
Chrome DevTools MCP, navigates to the page, takes screenshots, clicks
buttons, fills forms, verifies layout. This works great for one agent.

The problem is concurrency. When three agents work on three features in
three worktrees, each needs its own Chrome DevTools connection. The current
approach (Architecture 2 from the agent-devenv doc) gives each agent its
own Chrome instance — ~200MB RAM each. That works for 3-5 agents but
doesn't scale.

More importantly, the Chrome DevTools MCP server is designed for one agent.
It manages "the current page" as global state. Multiple agents talking to
the same MCP instance will clobber each other's page selection. Each agent
spawning its own MCP process (which spawns its own Chrome) is the current
workaround, but it's wasteful and doesn't compose well.

The proxy solves this: one Chrome instance, many agents, no cross-talk.

---

## The Cookie Problem (and How Hostnames Fix It)

A shared Chrome instance means a shared browser profile: cookies,
HTTP cache, service workers, autofill. Most of these are scoped by
origin (scheme+host+port), so different ports provide some isolation.
But **cookies are the exception** — they're scoped by domain, not by
origin. A cookie set on `localhost` by Agent 1's server on port 3848
is sent to Agent 2's server on port 3849 too. This breaks auth,
leaks session state across agents, and can mask bugs (Agent 2 sees
Agent 1's login session and incorrectly concludes its own auth works).

**The fix: per-session hostnames via `*.localhost` subdomains.**

RFC 6761 reserves `localhost` and its subdomains. Chrome resolves
`anything.localhost` to `127.0.0.1` without any `/etc/hosts` changes.
The proxy assigns each session a unique subdomain:

```
Agent 1 → s-abc.localhost:3848  → cookies scoped to s-abc.localhost
Agent 2 → s-def.localhost:3849  → cookies scoped to s-def.localhost
Agent 3 → s-ghi.localhost:3850  → cookies scoped to s-ghi.localhost
```

All three resolve to `127.0.0.1`. The dev servers receive the requests
normally — they don't care about the `Host` header (usually). But
Chrome treats each subdomain as a separate cookie domain. Full
isolation: cookies, localStorage, sessionStorage, IndexedDB, service
workers, HTTP cache — everything is scoped by origin, and each agent
has a unique origin.

**Session creation returns the hostname:**

```
POST /session
→ {
    "sessionId": "abc",
    "hostname": "s-abc.localhost",
    "wsUrl": "ws://localhost:9223/cdp/abc"
  }
```

The agent navigates to `http://s-abc.localhost:3848/inbox` instead of
`http://localhost:3848/inbox`. The proxy rewrites `Target.createTarget`
URLs to use the session hostname automatically if the agent forgets.

**One caveat: Host header validation.** Some dev servers validate the
`Host` header and reject requests from unknown hosts (Next.js,
webpack-dev-server, Vite with strict mode). Projects using the proxy
need to allow `*.localhost` in their dev server config. This is a
one-line config change, not a proxy problem:

```js
// next.config.js
allowedDevHosts: ['*.localhost']

// vite.config.ts
server: { host: true }  // or hmr: { host: 'localhost' }

// webpack-dev-server
allowedHosts: ['.localhost']
```

**Without this fix, a shared Chrome is unsafe for any app that uses
cookies** — which is basically every web app. With hostname isolation,
shared Chrome is as safe as separate Chrome instances.

---

### What the Proxy Does

```
Agent 1 (Claude Code)                Agent 2                Agent 3
    │                                    │                      │
    ▼                                    ▼                      ▼
Chrome DevTools MCP 1           Chrome DevTools MCP 2    Chrome DevTools MCP 3
    │                                    │                      │
    ▼                                    ▼                      ▼
┌──────────────────────────────────────────────────────────────────┐
│                     CDP Proxy (this tool)                        │
│                                                                  │
│  Session A ──► Tab 1 (s-a.localhost:3848)                        │
│  Session B ──► Tab 2 (s-b.localhost:3849)                        │
│  Session C ──► Tab 3 (s-c.localhost:3850)                        │
└──────────────────────────────────────────────────────────────────┘
    │
    ▼
Chrome (single instance, headless, debug port 9222)
    ├── Tab 1: s-a.localhost:3848 (Agent 1's feature)
    ├── Tab 2: s-b.localhost:3849 (Agent 2's feature)
    └── Tab 3: s-c.localhost:3850 (Agent 3's feature)
```

The proxy is a lightweight process that:
1. Manages one shared Chrome instance
2. Creates isolated sessions — each session owns one or more tabs
3. Assigns per-session hostnames (`s-{id}.localhost`) for cookie/storage isolation
4. Routes CDP commands from a session to its tabs only
5. Prevents cross-session interference (Agent 1 can't see Agent 2's tabs)
6. Cleans up tabs when sessions end

---

## Story 1: Agent Creates a Session and Tests a Page

**Context:** Agent 1 has claimed `global-inbox`, started a dev server on
port 3848. Its Chrome DevTools MCP needs to connect to a browser.

**Today (no proxy):**

The MCP server launches its own headless Chrome. It manages the browser
lifecycle. When the agent is done, the MCP server kills Chrome. Each agent
gets ~200MB of Chrome overhead.

**With the proxy:**

The MCP server connects to the proxy instead of launching Chrome. The proxy
is already running with a shared Chrome instance.

```
# Proxy is running on port 9223 (distinct from Chrome's 9222)

# MCP server connects to proxy, gets a session
POST /session
→ { "sessionId": "abc123", "hostname": "s-abc123.localhost",
    "wsUrl": "ws://localhost:9223/cdp/abc123" }

# MCP server connects to the WebSocket
# All CDP commands sent over this WebSocket are scoped to session abc123
# The proxy creates tabs for this session and routes commands to them

# Agent navigates (using session hostname for cookie isolation)
→ CDP: Target.createTarget { url: "http://s-abc123.localhost:3848/inbox" }
← CDP: Target.targetCreated { targetId: "tab-7" }
# Proxy records: session abc123 owns tab-7

# Agent takes screenshot
→ CDP: Page.captureScreenshot (on tab-7)
← CDP: screenshot data

# Agent is done
DELETE /session/abc123
# Proxy closes tab-7, cleans up session state
```

**What the MCP server sees:** A standard CDP endpoint. It doesn't know or
care that there's a proxy. It thinks it's talking directly to Chrome. The
proxy speaks the same protocol.

**What changes in the MCP server:** Instead of launching Chrome itself, it
connects to an existing CDP endpoint. Most Chrome DevTools MCP
implementations already support this via a `--cdp-url` or `--browser-url`
flag. The proxy just provides that URL, scoped to a session.

---

## Story 2: Three Agents Working Concurrently

**Context:** Three agents claimed three features. Tillr allocated ports
3848, 3849, 3850. The proxy is running.

**Sequence:**

```
t=0   Proxy starts, launches Chrome (one instance)
      Chrome listening on debug port 9222
      Proxy listening on port 9223

t=1   Agent 1's MCP: POST /session → session "s1", hostname "s-s1.localhost"
      Agent 1's MCP: connects ws://localhost:9223/cdp/s1
      Agent 1: createTarget("http://s-s1.localhost:3848") → tab-1

t=2   Agent 2's MCP: POST /session → session "s2", hostname "s-s2.localhost"
      Agent 2's MCP: connects ws://localhost:9223/cdp/s2
      Agent 2: createTarget("http://s-s2.localhost:3849") → tab-2

t=3   Agent 3's MCP: POST /session → session "s3", hostname "s-s3.localhost"
      Agent 3's MCP: connects ws://localhost:9223/cdp/s3
      Agent 3: createTarget("http://s-s3.localhost:3850") → tab-3

t=4   Agent 1: Page.captureScreenshot (on tab-1) → screenshot of inbox
      Agent 2: Page.captureScreenshot (on tab-2) → screenshot of feature detail
      Agent 3: Page.captureScreenshot (on tab-3) → screenshot of sidebar
      # All three screenshots happen concurrently. No cross-talk.

t=5   Agent 1: Runtime.evaluate("document.querySelector('.inbox-item').click()")
      # Executes on tab-1 only. Agents 2 and 3 are unaffected.

t=10  Agent 1 finishes. DELETE /session/s1 → tab-1 closed
      Agents 2 and 3 still working, unaffected.
```

**Isolation guarantees:**
- Session s1 can only see and interact with tabs it created
- `Target.getTargets` from session s1 returns only tab-1
- If Agent 1 sends a command targeting tab-2, the proxy rejects it
- Tab lifecycle is tied to session lifecycle — close session, close its tabs
- Per-session hostnames (`s-s1.localhost`, `s-s2.localhost`) isolate
  cookies, localStorage, sessionStorage, IndexedDB, and service workers
  — Chrome treats each subdomain as a separate origin

**Resource usage:**
- 1 Chrome instance: ~200MB RAM (vs. 600MB for 3 instances)
- Proxy overhead: ~10MB RAM
- Total: ~210MB (vs. ~600MB without proxy)
- Savings grow linearly with agent count

---

## Story 3: Session Crashes or Agent Disconnects

**Context:** Agent 2 crashes mid-session. Its MCP process dies. The
WebSocket connection drops.

**What the proxy does:**

1. Detects the WebSocket disconnect for session s2
2. Waits a grace period (configurable, default 30 seconds) in case of
   reconnect
3. No reconnect → closes all tabs owned by s2
4. Releases session s2
5. Agents 1 and 3 are completely unaffected

**What about Chrome crashes?**

If Chrome itself crashes (segfault, OOM):
1. Proxy detects the Chrome process exit
2. Restarts Chrome
3. All existing sessions are invalidated — their tabs are gone
4. Each MCP server gets a WebSocket error
5. Each MCP server reconnects and creates a new session
6. Agents restart their browser workflow (navigate, etc.)

This is the same behavior as if each agent's own Chrome crashed, but it
affects all agents at once. Chrome crashes are rare in headless mode, so
this is an acceptable tradeoff.

---

## Story 4: MCP Server Integration

**Context:** How does the Chrome DevTools MCP server actually connect to
the proxy instead of launching its own Chrome?

**Current MCP server startup (typical):**

```json
{
  "mcpServers": {
    "chrome-devtools": {
      "command": "npx",
      "args": ["@anthropic/chrome-devtools-mcp", "--headless"]
    }
  }
}
```

The MCP server launches Chrome internally with `--headless` and connects
to it via CDP.

**With the proxy:**

```json
{
  "mcpServers": {
    "chrome-devtools": {
      "command": "npx",
      "args": [
        "@anthropic/chrome-devtools-mcp",
        "--cdp-url", "ws://localhost:9223/cdp/auto"
      ]
    }
  }
}
```

The `--cdp-url` flag tells the MCP server to connect to an existing
browser instead of launching one. The `/cdp/auto` path tells the proxy to
auto-create a session for this connection.

**If the MCP server doesn't support `--cdp-url`:**

Some MCP implementations don't support connecting to an external browser.
In that case, the proxy can also present itself as a Chrome instance by
implementing Chrome's `/json` discovery endpoints:

```
GET http://localhost:9223/json/version
→ { "Browser": "Chrome/xxx", "webSocketDebuggerUrl": "ws://..." }

GET http://localhost:9223/json/list
→ [ { "id": "tab-1", "url": "...", "webSocketDebuggerUrl": "ws://..." } ]
```

The MCP server discovers "Chrome" at port 9223 and connects. It doesn't
know it's talking to a proxy.

---

## Story 5: Tillr Integration

**Context:** How does tillr coordinate with the proxy? Who starts it?

**Tillr does NOT manage the proxy.** The proxy is a separate tool that
runs independently. Tillr's only job is port allocation for dev servers.

**Option A: User starts the proxy manually**

```bash
# Start the proxy (once, stays running)
cdp-proxy start
# → Chrome started on :9222
# → Proxy listening on :9223

# In another terminal, start agents via tillr
tillr agent claim global-inbox
# → Worktree created, port 3848 allocated
```

The agent's MCP config points to `localhost:9223`. The proxy is already
running. Simple.

**Option B: Tillr starts the proxy on demand**

```yaml
# .tillr.yaml
chrome_proxy:
  enabled: true
  port: 9223
```

When the first agent claims a feature that needs UI QA, tillr starts the
proxy as a background process. When all agents are done, tillr stops it.

This is convenient but couples tillr to the proxy tool. Not recommended
for the initial version.

**Option C: The agent starts the proxy if not running**

The agent checks if the proxy is running (health check on port 9223).
If not, it starts it. This is self-healing but creates a race condition
if two agents start simultaneously.

**Recommendation: Option A.** The human (or a startup script) runs
`cdp-proxy start` before running agents. Simple, explicit, no magic.
The proxy stays running across agent sessions.

---

## Story 6: Human Developer Wants to Watch

**Context:** Val has three agents running. She wants to see what they're
doing — watch their browser sessions in real-time.

**The proxy can expose a debug UI:**

```
http://localhost:9223/
→ Dashboard showing:
  - Active sessions (3)
  - Session s1: 1 tab, localhost:3848/inbox, last activity 5s ago
  - Session s2: 1 tab, localhost:3849/features, last activity 2s ago
  - Session s3: 2 tabs, localhost:3850/sidebar + /sidebar/settings

  [Click any session to view its tabs]
  [Click any tab to see live screenshot stream]
```

This is a nice-to-have, not a must-have. But it's trivial to build because
Chrome's remote debugging already supports this — the proxy just needs to
expose Chrome's built-in `devtools://` URLs scoped to each session's tabs.

**Alternatively:** If Chrome is running with `--remote-debugging-port=9222`,
the human can open `chrome://inspect` in their local Chrome and see all
tabs. The proxy doesn't need to add anything — Chrome's native tooling
works.

---

## Architecture

### The Proxy Process

```
cdp-proxy
├── HTTP server (:9223)
│   ├── POST /session          → create session, return wsUrl + hostname
│   ├── DELETE /session/:id    → close session and its tabs
│   ├── GET /session           → list active sessions
│   ├── GET /health            → health check
│   └── GET /json/*            → Chrome discovery protocol emulation
│
├── WebSocket server
│   └── /cdp/:sessionId        → proxied CDP connection
│       ├── Intercepts Target.* commands (enforce session scope)
│       ├── Passes through Page.*, Runtime.*, DOM.*, Network.*, etc.
│       └── Tracks which targets belong to which session
│
├── Chrome manager
│   ├── Launches Chrome with --headless --remote-debugging-port=9222
│   ├── Monitors Chrome process health
│   └── Restarts on crash
│
└── Session store (in-memory)
    ├── session → { hostname, [targetId, ...] }
    ├── targetId → session (reverse lookup)
    └── Cleanup on disconnect/timeout
```

### CDP Command Routing

Not all CDP commands need interception. The proxy's job is scoping, not
transformation:

**Intercepted (session-scoped):**
- `Target.createTarget` → proxy records new target in session
- `Target.closeTarget` → only if target belongs to session
- `Target.getTargets` → filtered to session's targets only
- `Target.attachToTarget` → only if target belongs to session

**Passed through (after target validation):**
- `Page.*` (navigate, screenshot, etc.)
- `Runtime.*` (evaluate, etc.)
- `DOM.*` (querySelector, etc.)
- `Network.*` (intercept, etc.)
- `Input.*` (click, type, etc.)
- Everything else

The proxy attaches to each target via `Target.attachToTarget` with
`flatten: true`, which means CDP commands for a specific target are
multiplexed over the same WebSocket. The proxy just needs to verify
that the `sessionId` in each CDP message belongs to the requesting
session.

### State

All state is in-memory. No database, no files. If the proxy restarts,
sessions are lost and agents reconnect. This is fine — browser state is
ephemeral. The agent just re-navigates.

---

## Implementation Plan

### Phase 1: Minimal Viable Proxy

- Single Go binary (or Node.js — language TBD based on ecosystem)
- Launches headless Chrome
- HTTP API: create/delete/list sessions
- Per-session hostname assignment (`s-{id}.localhost`) for cookie isolation
- WebSocket proxy with Target.* interception
- Session-scoped tab isolation
- Auto-cleanup on disconnect

This is ~500 lines of code. The CDP protocol is well-documented and the
WebSocket proxying is straightforward.

### Phase 2: Robustness

- Chrome crash detection and restart
- Session timeout (configurable grace period)
- Health check endpoint
- Structured logging (which session did what)
- Resource limits (max sessions, max tabs per session)

### Phase 3: Developer Experience

- Debug dashboard (web UI showing active sessions)
- Live screenshot streaming for human observers
- Metrics (sessions created, tabs opened, CDP messages proxied)
- Chrome discovery protocol emulation (`/json/*` endpoints)

---

## What This Tool Is NOT

- **Not an MCP server.** It speaks CDP, not MCP. MCP servers connect to
  it as if it were Chrome.
- **Not part of tillr.** It's a standalone utility. Tillr doesn't know
  about it. The agent's MCP config points to it directly.
- **Not a browser automation framework.** It doesn't add features on top
  of CDP. It just proxies and scopes.
- **Not a testing tool.** It doesn't run tests. It provides browser access
  for agents that do their own testing.

---

## Naming

Working name: `cdp-proxy` (descriptive) or `chromux` (Chrome multiplexer).
The name should communicate: "multiple users, one Chrome."

---

## Open Questions

1. **Language choice.** Go is natural (matches tillr, single binary
   distribution). Node.js has better CDP libraries (`chrome-remote-interface`,
   `puppeteer-core`). The proxy is simple enough that either works.

2. **Chrome lifecycle.** Should the proxy launch Chrome itself, or connect
   to an existing Chrome? Launching is simpler for the user. Connecting
   gives more control (the user can configure Chrome flags, use a specific
   version, etc.). Could support both modes.

3. **Auto-session from WebSocket.** If an MCP server connects to
   `ws://localhost:9223/cdp/auto`, should the proxy auto-create a session?
   This avoids the HTTP create-session step, which is convenient but means
   the MCP server can't be told its session ID upfront. Probably fine —
   the session ID is implicit in the WebSocket URL.

4. **Max tabs per session.** Should the proxy enforce a limit? An agent
   that opens 50 tabs is probably buggy. A default limit of 10 with
   override seems reasonable.

5. **Existing tools.** Does something like this already exist? Chrome's
   `--remote-debugging-port` supports multiple connections, but without
   session scoping — all connections see all tabs. Browserless.io does
   something similar as a cloud service. A lightweight local proxy appears
   to be a gap in the ecosystem.

6. **Hostname compatibility.** `*.localhost` resolution is guaranteed by
   RFC 6761 and works in Chrome/Chromium. Need to verify behavior in
   Firefox (Gecko) and Safari (WebKit) if agents ever use non-Chromium
   browsers. Also need to confirm headless Chrome respects RFC 6761 —
   it should, since it uses the same network stack as headed Chrome.
