# Tillr

[![CI](https://github.com/mschulkind-oss/tillr/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind-oss/tillr/actions/workflows/ci.yml)

**The orchestrator between humans and AI agents.**

Tillr runs the `claude` CLI as a structural enforcement layer: agents
get spawned per task, with their own context, with hard caps on cost
and turns, and the lifecycle (load context → work → persist context
→ die) is the orchestrator's job, not the agent's prompt.

> ## Principle Zero
>
> All fixes are systematic and architectural. We do not tell agents
> to do things the right way and expect change. Promises are not
> change. The first priority is a tool change. The last is agent
> instructions. **Agents are unreliable. Tools are not.**
>
> Full text: [`docs/principle-zero.md`](docs/principle-zero.md).

---

## Phoenix rising

Tillr was reset on 2026-04-27 (commit `98f4140`). The pre-reset
codebase — 60+ CLI subcommands, 25+ web pages, a sprawling cycle
engine, decisions/discussions/ideas/sprints/roadmap subsystems — is
preserved at the `archive/pre-reset` tag and branch. See
[`docs/reset.md`](docs/reset.md) for the inventory and how to find
anything in git history.

What rose from the ashes:

- A **conductor + persona** architecture (foreground Claude session
  manages project state; background personas do scoped work).
- An **orchestrator daemon** that spawns `claude -p` invocations per
  persona and enforces the lifecycle structurally (Principle Zero).
- A minimal data surface: **projects, features, comments, runs**.
  That's it. Add complexity when there's actual demand.
- Two surfaces: a **Bubble Tea TUI** (`tillr tui`) and a **React web
  UI** (`tillr serve`). CLI is always there.
- **Self-hosting:** tillr now tracks its own remaining development
  in its own database. Run `tillr feature list` in this repo to see.

The full design lives in [`docs/consulting-firm/`](docs/consulting-firm/) —
the architecture vision, the [MVP plan](docs/consulting-firm/mvp.md),
the [staged roadmap](docs/consulting-firm/roadmap.md), and 31 user
stories.

---

## Install

Single supported path (works inside or outside the YOLO jail):

```bash
git clone https://github.com/mschulkind-oss/tillr.git
cd tillr
just setup     # one-time: mise tools, pnpm via corepack, go modules
just install   # build frontend + go install -> $GOPATH/bin/tillr
```

After `just install`, `tillr` is on PATH (via mise's Go install
directory). Verify:

```bash
which tillr
tillr --version
```

If the binary isn't found, your shell hasn't picked up `$GOPATH/bin`
— `eval "$(mise activate bash)"` (or your shell's equivalent).

---

## Quick start

```bash
# One-time setup in any project directory
cd ~/code/your-project
tillr init your-project

# Add a feature, optionally typed for a persona
tillr feature add "Implement OAuth" --persona implementer
tillr feature list

# Start the orchestrator daemon in another terminal
tillr orchestrator start --max-parallelism 2

# Or, smoke without invoking real Claude:
tillr orchestrator start --dry-run --max-parallelism 2

# Inspect from the terminal
tillr tui

# Or in a browser
tillr serve   # http://localhost:3847
```

---

## CLI surface

```
# Project
tillr init <name>                       Initialize a project
tillr serve [--port N]                  Start the web UI
tillr tui                               Open the Bubble Tea TUI

# Features (the unit of work)
tillr feature add "Title"               --persona, --description, --status
tillr feature list                      --persona, --status
tillr feature show <id>                 With comment thread

# Comments
tillr comment <feature-id> "..."        --role <persona>

# Personas
tillr persona list                      Discovers .claude/agents/
tillr persona show <name>
tillr persona context <name>            Print the context file
tillr persona append <name> [body]      --summary; reads stdin if no body
tillr persona compact <name>            --keep N (default 20)
tillr persona claim <name>              Next pending feature for this persona

# Orchestrator (Stage 0 / MVP)
tillr orchestrator start                --max-parallelism, --max-budget-usd, --dry-run, ...
tillr orchestrator stop
tillr orchestrator status               Active + recent runs, config
tillr orchestrator runs                 --feature, --limit

# Config
tillr config show
tillr config set <key> <value>          e.g. orchestrator.max-parallelism 3
tillr config get <key>

# Retro (analyzes Claude session transcripts)
tillr retro                             --transcript <path>
```

`--json` works on most commands for machine-readable output.

---

## Architecture, in one paragraph

Personas are sub-agent definitions in `.claude/agents/<name>.md`.
Each has its own append-style markdown context file at
`swarf/agents/<name>/context.md` (gitignored, externally synced via
the [swarf](https://github.com/mschulkind/swarf) tool). The
orchestrator daemon polls the queue, claims pending features by
persona, and spawns `claude -p` with the persona's context loaded as
the system prompt — `--json-schema` validates the agent's structured
output, `--max-turns` and `--max-budget-usd` cap cost and runtime,
`--worktree` keeps concurrent agents from stomping each other. The
orchestrator parses the JSON output and applies side effects: appends
to context, comments on the feature, files follow-ups, transitions
status. The agent never has to remember any of this — that's the
[Principle Zero](docs/principle-zero.md) point.

---

## Documentation

- [`docs/principle-zero.md`](docs/principle-zero.md) — read this first
- [`AGENTS.md`](AGENTS.md) — how to operate inside this repo (for
  Claude Code sessions and humans)
- [`docs/consulting-firm/mvp.md`](docs/consulting-firm/mvp.md) — the
  shipping plan
- [`docs/consulting-firm/roadmap.md`](docs/consulting-firm/roadmap.md) —
  long-term staged ordering after MVP
- [`docs/consulting-firm/`](docs/consulting-firm/README.md) — the full
  architecture vision (31 user stories, implementation layers, open
  questions)
- [`docs/reset.md`](docs/reset.md) — what's at `archive/pre-reset` and
  how to find it
- [`docs/conductor-skill-template.md`](docs/conductor-skill-template.md) —
  per-user `~/.claude/skills/conductor/` install template

---

## Development

```bash
just dev            # backend (live reload) + Vite frontend, via overmind
just check-ci       # gofmt + golangci-lint + tests (CI gate; pre-commit hook)
just check          # local: format + lint + test
just install        # build + go install -> $GOPATH/bin/tillr
```

The pre-commit hook runs `just check-ci` and **must not be
bypassed**. Bypassing the hook is a Principle Zero violation.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for full setup notes.

---

## Status

This is a **post-reset MVP under active dogfood**. The core surface
(features / comments / personas / orchestrator / TUI / web) ships
and works end-to-end with `--dry-run`. Wiring it to a real `claude`
binary and observing how the personas behave on tillr's own queued
work is the next step.

Tillr's own next-round development is tracked in tillr:

```bash
tillr feature list
```

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
