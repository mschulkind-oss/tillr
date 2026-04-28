# AGENTS.md — Tillr

> ## Principle Zero
>
> **All fixes are systematic and architectural. We do not tell agents
> to do things the right way and expect change. Promises are not
> change. The first priority is a tool change. The last is agent
> instructions. Agents are unreliable. Tools are not.**
>
> Full text: [`docs/principle-zero.md`](docs/principle-zero.md). Read
> this before proposing any fix in this repo. If your proposal is
> "the agent should remember to X," it isn't a fix yet.

## Project overview

Tillr is the project management tool that bridges human product
owners and AI agents. Post-reset (commit `98f4140` and forward),
tillr is built around the **conductor + persona** architecture:

- A **conductor** runs in the human's foreground Claude Code session,
  managing project state.
- **Personas** (implementer / researcher / reviewer / …) are
  specialized agents dispatched to do scoped work, each with their
  own append-style context file in `swarf/`.
- An **orchestrator process** (in progress) spawns `claude -p`
  invocations per persona, enforcing context lifecycle *as a tool*,
  not as instructions to the agent.

Stack:

- Go 1.26 (CLI, server, daemon, persona package, orchestrator)
- SQLite via `modernc.org/sqlite` (no CGO)
- React 19 + TypeScript + Vite + Tailwind v4 (web UI)
- Bubble Tea (TUI)
- swarf (gitignored, externally synced — context files, retros)

## Where things live

| Path | Purpose |
|------|---------|
| `cmd/tillr/` | CLI entry |
| `internal/cli/` | Cobra commands (`init`, `serve`, `feature`, `comment`, `persona`, `config`, `retro`, `tui`, …) |
| `internal/persona/` | Persona context CRUD + compaction |
| `internal/db/` | SQLite schema + queries (projects, features, comments, config) |
| `internal/server/` | HTTP API + embedded SPA |
| `internal/tui/` | Bubble Tea TUI |
| `web/src/` | React frontend |
| `.claude/agents/<name>.md` | Persona sub-agent definitions |
| `.claude/skills/` | Workspace-local skills (e.g., `conductor`) |
| `swarf/` | Gitignored: persona contexts, retros, conductor state |
| `docs/` | Design docs (start with `docs/README.md`) |
| `docs/consulting-firm/` | Conductor + persona vision, MVP, roadmap, stories |
| `docs/principle-zero.md` | The principle this whole project hangs on |
| `docs/reset.md` | Index for git archaeology against `archive/pre-reset` |

## Development process

### Committing

1. Commit every logical change. Never leave the working directory
   dirty.
2. Run `just format` before committing (or just let the pre-commit
   hook run).
3. Use conventional commits: `feat:`, `fix:`, `docs:`, `chore:`,
   `refactor:`, `test:`, `reset:`.
4. The pre-commit hook runs `just check-ci` (gofmt + lint + tests).
   **Do not bypass the hook.** If it fails, fix the issue and retry.
5. Bypassing the hook is a Principle Zero violation. The hook is a
   tool; bypassing it is an instruction-style fix ("just be careful
   this once"). Don't.

### Quality gate

- `just check-ci` — read-only, used by pre-commit and CI.
- `just check` — local dev, auto-fixes formatting.
- `just format` — formatting only.

### What goes where

- **Public source:** `cmd/`, `internal/`, `web/`, `tests/`,
  `go.mod`, `Justfile`, `docs/`, build config, `.claude/agents/`,
  `.claude/skills/`. All committed.
- **Private state:** `swarf/`. Gitignored. Persona contexts, retros,
  conductor state, design notes. Synced cross-machine via the swarf
  tool, not via this repo.

## Agent workflow

The post-reset surface — what agents and humans actually invoke:

```bash
# Project setup
tillr init <project-name>            # one-time per project
tillr serve                          # web UI on :3847

# Features (the unit of work)
tillr feature add "Title" --persona <name>   # type-tagged
tillr feature list [--persona X] [--status S]
tillr feature show <id>
tillr comment <feature-id> "..."

# Personas
tillr persona list                   # discovers .claude/agents/
tillr persona show <name>
tillr persona context <name>         # print context file
tillr persona append <name> --summary "..." "<body>"
tillr persona compact <name>         # archive older blocks
tillr persona claim <name>           # next pending feature for this persona

# Config & retro
tillr config show
tillr config set max-parallelism 3
tillr retro                          # analyze recent Claude session

# Inspection
tillr tui                            # Bubble Tea three-pane TUI
```

Read the help text for any command (`tillr <cmd> --help`) — the
`--json` flag is available on most for machine-readable output.

## Personas (post-reset)

Defined in `.claude/agents/<name>.md`. Each is a Claude Code sub-agent
spec with YAML frontmatter (name, description, tools allowlist) and
a body prompt.

Currently shipped:

- `implementer` — writes code; full tool set.
- `researcher` — investigates questions; read-only on code; web access.
- `reviewer` — reviews diffs; read-only.

Add a persona by creating `.claude/agents/<name>.md`. The persona
auto-appears in `tillr persona list`.

**Persona prompt content should be minimal — Principle Zero applies
here.** The orchestrator (in progress) handles lifecycle (load
context → invoke → persist context → die) as a tool, so persona
prompts should describe *the work the persona does*, not the
mechanics of remembering to call tillr CLI commands. The current
persona files still contain lifecycle instructions; those are TODOs
for orchestrator-driven enforcement, not durable design.

## Conductor pattern

In any Claude Code session in this repo, you are operating as the
**conductor**. Your job: project management, not product management.
Product decisions belong to the human.

The conductor:

- Reads tillr state via CLI / API.
- Dispatches typed work to personas.
- Writes its own significant actions to `swarf/conductor.md`.
- Asks the human (not assumes) on product decisions.

The `conductor` skill (in `.claude/skills/conductor/SKILL.md`, when
installed) re-hydrates the conductor's state after `/clear`. See
`docs/conductor-skill-template.md` for the install template.

## Forward plan

- **Now:** MVP infrastructure shipped (commit `d1404a3`). CLI surface
  + persona contexts + retro + TUI + web pages.
- **In progress:** Orchestrator process that spawns `claude -p`
  invocations per persona, enforcing lifecycle structurally
  (Principle Zero).
- **After dogfooding:** Stages 1+ of `docs/consulting-firm/roadmap.md`.

## Important rules

- **Read [`docs/principle-zero.md`](docs/principle-zero.md) before
  proposing any fix.** If your fix is an instruction, it isn't a fix.
- All work flows through tillr (`tillr feature add`, `tillr comment`,
  the persona queue). Don't bypass the queue with ad-hoc prompts.
- Use `--json` when consuming CLI output programmatically.
- Pre-reset code is at the `archive/pre-reset` tag/branch — see
  `docs/reset.md` for archaeology.
