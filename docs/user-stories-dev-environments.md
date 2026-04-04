# User Stories: Dev Environments and Worktrees

When tillr creates worktrees for agent PRs and preview servers, it bumps
into dev environment management: dependencies, builds, ports, databases,
cleanup. This doc explores where the boundary should be — what tillr
handles vs. what a separate tool handles — through user stories that
surface the real friction.

---

## The Problem, Concretely

A tillr worktree is a git checkout. That's all git gives you. But to
actually BUILD and RUN code from a worktree, you need:

| Need | Example | Who handles it? |
|------|---------|-----------------|
| Dependencies | `node_modules/`, Go module cache | ? |
| Build artifacts | `dist/`, compiled binaries | ? |
| Config files | `.env`, `.tillr.json` | ? |
| Port allocation | Each preview needs a unique port | ? |
| Database | Separate DB per worktree? Shared? | ? |
| External services | Redis, Postgres, API keys | ? |
| Cleanup | Delete worktree + all of the above | ? |

For a Go+React project like tillr itself:
- Go: modules are cached globally (`GOPATH/pkg/mod`), builds are fast.
  A worktree just works — `go build` in any directory uses the shared
  module cache. Cost: near zero.
- React: `node_modules/` is per-directory. Each worktree needs its own
  `pnpm install`. With a warm pnpm store, this takes ~5 seconds. With
  a cold store, ~30 seconds. For 5 concurrent agents, that's 5 copies
  of `node_modules/` on disk (~200MB each = 1GB total).
- SQLite: one `tillr.db` in the project root. Worktrees can share it
  (SQLite handles concurrent access via WAL mode) or each could have
  a copy.
- No external services. No `.env` with secrets. This is a simple case.

For a typical production app (Next.js + Postgres + Redis + S3):
- `node_modules/`: same as above, but possibly 500MB+ each.
- Database: each worktree needs either its own schema/database or a
  shared database with test data isolation. Creating a Postgres database
  per worktree is doable but needs credentials, migrations, and cleanup.
- Redis: shared or per-worktree? Usually shared is fine.
- `.env`: API keys, database URLs, secrets. Each worktree needs its own
  `.env` pointing to its own database URL. But the API keys are the same.
- Docker services: some projects run Postgres/Redis in Docker. Each
  worktree would need its own Docker Compose project? That's heavy.
- Port allocation: each preview server needs a unique port. The main app
  runs on 3000. Worktree 1 on 3001. Worktree 2 on 3002. Someone needs
  to allocate and track these.

---

## Story 1: Sam — Tillr Tries to Do It All (And Hits Walls)

**Context:** Sam has a Next.js app with Postgres. He's running 2 agents
via tillr. Each agent creates a worktree. He wants to preview one of
them.

**What tillr would need to do:**

1. `tillr agent claim login-page` creates a worktree. Now tillr needs
   to make it runnable:
   ```
   cd .tillr/worktrees/login-page/
   pnpm install              # 15 seconds, 400MB disk
   cp ../.env .env           # copy env from main project
   # Edit .env to use a different database?
   # Create a new Postgres database?
   # Run migrations on the new database?
   ```

   Tillr doesn't know how to do any of this. It would need:
   - A config telling it how to install deps (`pnpm install`, `pip install -e .`, `bundle install`)
   - A config telling it how to set up a database (or not)
   - A config telling it how to run the app (`pnpm dev`, `go run .`)
   - A config telling it which ports to use

2. That config would look something like:
   ```yaml
   # .tillr.yaml
   worktree:
     setup:
       - "pnpm install"
     env:
       copy_from: ".env"
       overrides:
         DATABASE_URL: "postgres://localhost/tillr_{{branch}}"
         PORT: "{{auto_port}}"
     database:
       create: "createdb tillr_{{branch}}"
       migrate: "pnpm db:migrate"
       destroy: "dropdb tillr_{{branch}}"
     preview:
       command: "pnpm dev --port {{port}}"
       ready_check: "http://localhost:{{port}}/api/health"
   ```

3. Sam fills this out. It works for his project. But then:
   - His coworker uses Docker Compose instead of local Postgres
   - Another project uses Python with venvs
   - Another uses a monorepo with Turborepo
   - Another needs Redis and Elasticsearch too

   Tillr's config grows to handle every possible setup. It becomes a
   dev environment tool. It competes with Docker, devcontainers, Nix,
   mise, tilt, skaffold...

**What breaks:**
- Tillr is a project management / workflow orchestration tool. Dev
  environment management is a completely different domain with deep
  complexity.
- The config surface area explodes. Every project is different.
- Tillr would need to understand package managers, database systems,
  container runtimes — none of which are its core competency.
- Failure modes multiply: what if `pnpm install` fails? What if the
  database can't be created? What if the port is taken? Tillr would
  need to handle all of these with good error messages.

**Verdict:** Tillr should NOT manage dev environments directly. It
should provide hooks for a tool that does.

---

## Story 2: Sam — Tillr + Devenv (Separate Tool)

**Context:** Same Sam, same Next.js + Postgres app. But now there's a
separate tool that handles dev environments. Tillr delegates to it.

**Option A: Tillr + project-defined scripts**

The simplest separation. The project defines lifecycle scripts, tillr
calls them:

```yaml
# .tillr.yaml
worktree:
  on_create: "./scripts/worktree-setup.sh"
  on_preview: "./scripts/worktree-start.sh"
  on_destroy: "./scripts/worktree-teardown.sh"
```

The scripts are project-specific. Sam writes them:

```bash
# scripts/worktree-setup.sh
#!/bin/bash
# Called by tillr after creating a worktree
# CWD is the worktree directory
# $TILLR_BRANCH, $TILLR_FEATURE_ID, $TILLR_WORKTREE_PATH are set

pnpm install
cp ../.env .env
DB_NAME="tillr_${TILLR_BRANCH//\//_}"
createdb "$DB_NAME" 2>/dev/null || true
sed -i "s|DATABASE_URL=.*|DATABASE_URL=postgres://localhost/$DB_NAME|" .env
pnpm db:migrate
```

```bash
# scripts/worktree-start.sh
#!/bin/bash
# Called by `tillr pr preview`
# Must print the URL to stdout when ready

PORT=$(tillr pr port "$TILLR_FEATURE_ID")
pnpm dev --port "$PORT" &
echo "http://localhost:$PORT"
```

```bash
# scripts/worktree-teardown.sh
#!/bin/bash
# Called by tillr after merging or abandoning a PR

DB_NAME="tillr_${TILLR_BRANCH//\//_}"
dropdb "$DB_NAME" 2>/dev/null || true
# pnpm install cleanup happens when the worktree dir is deleted
```

**What works:**
- Tillr stays simple. It creates worktrees, calls scripts, manages PRs.
- Scripts are project-specific. Each project handles its own complexity.
- No new tool to learn — just bash scripts in the project repo.
- Works for any stack: Node, Python, Go, Ruby, whatever.

**What breaks:**
- Every project reinvents the scripts. There's no reuse across projects.
- Script quality varies. No error handling, no idempotency, no cleanup
  on partial failure.
- Agents can't write these scripts — they're project-specific knowledge
  that needs to exist before agents can work effectively.
- No port management. The `tillr pr port` command would need to exist
  (allocate and track ports per worktree).

---

**Option B: Tillr + a dedicated devenv tool**

There's a separate tool — let's call it `devenv` (or it could be an
existing tool like `mise`, `devbox`, `tilt`, or `devcontainer`) — that
knows how to set up and run environments. Tillr integrates with it.

```yaml
# .tillr.yaml
worktree:
  devenv: "mise"  # or "devcontainer", "docker-compose", "nix", "custom"
```

When tillr creates a worktree:
1. Tillr creates the git worktree (its job)
2. Tillr calls `mise install` in the worktree (devenv's job)
3. Tillr calls project-defined setup hooks (see Option A)

When tillr creates a preview:
1. Tillr calls `mise run preview --port $PORT` (devenv's job)
2. Devenv knows how to start the app, create databases, etc.

**What works:**
- Clean separation of concerns. Tillr = workflow. Devenv = environment.
- Devenv tools already exist and are mature (mise, devbox, nix).
- The integration surface is small: "create env", "start preview",
  "destroy env".

**What breaks:**
- Now you need two tools. Users have to set up both.
- The devenv tool might not know about worktrees. Most devenv tools
  assume one environment per project directory, not multiple concurrent
  environments in worktrees.
- Which devenv tool? There's no standard. mise, devbox, nix flakes,
  devcontainers, docker-compose — each has different conventions.

---

## Story 3: The Agent's Perspective — What Do They Actually Need?

**Context:** An agent claims a feature and gets a worktree. What does
it actually need to do its job?

**For implementing (the common case):**

The agent needs to:
1. Edit files
2. Run `go build ./...` (or equivalent)
3. Run `npx tsc --noEmit`
4. Run tests
5. Commit

For a Go project: the worktree just works. Go modules are cached
globally. `go build` works in any directory.

For the React frontend: the agent needs `node_modules/`. But does it
need to RUN the app? Usually no — it just needs to build and typecheck.
`pnpm install && pnpm build` is enough. The agent doesn't preview the
UI; the human does that.

**For QA (agent-based review):**

The review agent needs to:
1. Read the diff
2. Read the spec
3. Maybe run tests
4. Produce a review

It does NOT need to run the app. It works from the diff and the code.
The worktree gives it the full codebase to reference, but it doesn't
need a running server, a database, or ports.

**For human QA preview:**

The human needs a running app to test. THIS is where dev environment
management matters. But this is the human's action, not the agent's.

**The insight:** Agents need worktrees with dependencies installed (for
building and testing). Humans need running services (for preview). These
are different requirements:

| Need | Agent (implementing) | Agent (reviewing) | Human (QA preview) |
|------|---------------------|-------------------|-------------------|
| Worktree | ✓ | ✓ (or just diff) | ✓ |
| Dependencies | ✓ (for build/test) | Maybe | ✓ |
| Running app | ✗ | ✗ | ✓ |
| Database | ✗ (tests use SQLite) | ✗ | Maybe |
| Port | ✗ | ✗ | ✓ |

So the heavy environment management (database, ports, services) is only
needed for human QA preview — which is optional and human-initiated.

---

## Story 4: The Minimal Viable Approach

**Context:** What if tillr just does the minimum and lets the human
handle the preview setup themselves?

**Tillr's job:**
1. Create worktree (git)
2. Run `pnpm install` if `package.json` exists (simple detection)
3. That's it for setup

**For agent implementation:**
- Agent works in the worktree. `go build` and `pnpm build` work because
  dependencies are installed. Tests run. The agent doesn't need anything
  else.

**For human QA preview:**
- The human can preview in two ways:

  **Way 1: Trust the build.** If the build passes and tests pass, and
  the spec says what to check, many features can be QA'd without running
  the app. "Did you add the right API endpoint? Build passes, tests pass,
  spec looks implemented. Approved."

  **Way 2: Run it yourself.** The human `cd`s into the worktree and
  starts the app manually:
  ```
  cd .tillr/worktrees/login-page/
  # Already has node_modules from setup
  tillr serve --port 3848
  # Or: pnpm dev --port 3001
  ```
  They know how to run their own project. They don't need tillr to do it.

  **Way 3: tillr pr preview with hooks.** For convenience, tillr can
  call a user-defined preview command:
  ```yaml
  # .tillr.yaml
  worktree:
    setup: ["pnpm install"]
    preview: "tillr serve --port {{port}}"
  ```
  But this is a convenience, not a requirement. The human can always
  just run the command themselves.

**What this means for tillr:**
- Tillr manages: git worktrees, branches, PR records, merge queue
- Tillr optionally runs: a setup command and a preview command (from config)
- Tillr does NOT manage: databases, external services, Docker, env files
- The human handles: anything beyond basic setup, at their discretion

---

## Story 5: Thinking About a Separate Tool

**Context:** The environment management problem is real, not just for
tillr. Anyone running multiple concurrent dev environments (worktrees,
branches, PRs) hits the same issues. What would a standalone tool look
like?

**The tool: `grove` (working name — manages worktrees and their environments)**

grove sits between git worktrees and your dev toolchain. It knows how
to create, setup, run, and tear down environments for worktrees.

```yaml
# grove.yaml (in project root)
runtime: node          # auto-detects: node, go, python, ruby, rust
package_manager: pnpm  # auto-detects from lockfile

setup:
  - pnpm install
  - pnpm db:migrate

services:
  app:
    command: pnpm dev --port $PORT
    port: auto               # grove allocates from a range
    ready: http://localhost:$PORT/api/health
    env:
      DATABASE_URL: $DATABASE_URL

  # Optional: per-worktree database
  database:
    type: postgres           # or sqlite, mysql
    auto_create: true        # create DB named grove_{branch}
    auto_migrate: pnpm db:migrate
    auto_destroy: true       # drop DB on worktree cleanup

env:
  # Shared across worktrees
  inherit: [API_KEY, AWS_REGION, REDIS_URL]
  # Per-worktree overrides
  PORT: auto
  DATABASE_URL: auto         # grove generates based on database config
```

Usage:
```bash
# Create a worktree with environment
grove create feature/login-page
# → git worktree, pnpm install, database created, migrations run

# Start the environment
grove start feature/login-page
# → app running on auto-assigned port
# → prints URL

# List active environments
grove list
# feature/login-page    :3001    running    420MB
# feature/signup-flow   :3002    stopped    380MB
# main                  :3000    running    400MB

# Stop and clean up
grove destroy feature/login-page
# → app stopped, database dropped, worktree deleted
```

**Integration with tillr:**

```yaml
# .tillr.yaml
worktree:
  manager: grove         # tillr delegates to grove
```

Now:
- `tillr agent claim` calls `grove create` instead of raw `git worktree add`
- `tillr pr preview` calls `grove start` instead of running commands directly
- `tillr pr merge` calls `grove destroy` for cleanup
- Tillr doesn't know about pnpm, Postgres, or ports. Grove does.

**Is grove worth building?**

Existing tools that overlap:
- **mise**: task runner + version manager. Doesn't manage per-worktree
  environments or databases. Could be extended.
- **devcontainers**: full container per environment. Heavy but isolated.
  Doesn't know about worktrees.
- **docker-compose**: service management. Could work per-worktree with
  project naming. Heavy.
- **tilt**: Kubernetes dev environments. Way too heavy for local dev.
- **devbox**: Nix-based environments. Per-project, not per-worktree.

None of them are worktree-aware. They all assume one environment per
project directory. The worktree-specific angle (multiple concurrent
environments from the same project, with shared resources and
per-environment overrides) is the gap.

**The question:** Is this gap big enough to justify a new tool? Or is
"project-defined scripts" (Story 2, Option A) good enough?

---

## Recommendation

**For tillr right now:** Option A from Story 2 — project-defined lifecycle
scripts. Simple hooks in `.tillr.yaml`:

```yaml
worktree:
  setup: ["pnpm install"]                    # run after worktree creation
  preview: "tillr serve --port {{port}}"     # run for preview
  teardown: []                               # run before worktree deletion
```

Plus:
- Port allocation in tillr (`tillr pr port <id>` auto-assigns from a range)
- `pnpm install` auto-detection (if `pnpm-lock.yaml` exists, run it)
- Worktree cleanup on merge/reject (delete directory, reclaim port)

**For tillr's own dogfooding:** Since tillr is a Go+React project with
SQLite, the environment story is simple:
- `pnpm install` in the worktree (for the frontend)
- `go build` just works (shared module cache)
- SQLite DB is shared (WAL mode handles concurrent access)
- Preview is just `tillr serve --port $PORT` in the worktree

This is a ~20 line `.tillr.yaml` config change and ~100 lines of Go
for port management and hook execution.

**For the future:** If the pattern proves valuable across multiple
projects, the worktree lifecycle hooks could be extracted into a
standalone tool (grove or similar). But don't build it until the hooks
in tillr feel limiting — premature extraction is premature abstraction.

**What explicitly NOT to build in tillr:**
- Database management (create/migrate/destroy)
- Docker/container management
- Package manager detection beyond simple heuristics
- Service discovery or health checking
- Environment variable management beyond simple copy/override

These belong in the project's scripts or a dedicated devenv tool.
Tillr's job is workflow orchestration, not infrastructure.
