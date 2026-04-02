---
name: Committing-Changes
description: >
  How to commit changes in this project using Jujutsu (jj) with
  the public/private two-remote model. Public changes squash into
  staging; private changes squash into dev.
---

# Committing Changes — Lifecycle Project

This project uses **jj (Jujutsu)** colocated with git, and a **public/private two-remote** model for open-source development.

## Bookmark Structure

```
@  (working copy — your changes go here)
│
○  dev           → private remote only (AGENTS.md, internal docs, scratch)
│
○  staging       → private remote only (accumulates public changes for next release)
│
◆  main          → BOTH public and private remotes (the public face)
```

## How to Commit

**All work happens in the working copy (@), then gets squashed into the right bookmark.**

### Public changes (source code, tests, public docs)
```bash
# Describe your work
jj describe -m "feat: add new API endpoint for search"

# Squash into staging (accumulates until promoted to main)
jj squash --into staging
```

### Private changes (agent config, internal docs)
```bash
jj describe -m "update AGENTS.md with new workflow"

# Squash into dev (never goes public)
jj squash --into dev
```

### Mixed changes (both public and private files)
```bash
# Squash specific public files into staging
jj squash --into staging cmd/ internal/ web/ go.mod go.sum

# Squash remaining private files into dev
jj squash --into dev
```

## What Goes Where

**Public (squash into staging):**
- `cmd/`, `internal/`, `web/`, `tests/` — all source code
- `go.mod`, `go.sum` — dependencies
- `Justfile`, `mise.toml`, `Dockerfile`, `.air.toml`, `.gitignore` — build config
- `README.md`, `LICENSE`, `NOTICE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CHANGELOG.md` — community files
- `.github/` — CI/CD workflows
- `docs/design/`, `docs/guides/`, `docs/plans/` — public documentation
- `scripts/` — public scripts

**Private (squash into dev):**
- `AGENTS.md`, `OPEN_QUESTIONS.md` — internal development docs
- `.copilot/`, `.gemini/`, `.yolo/` — AI agent configuration
- `yolo-jail.jsonc` — dev environment config
- `scratch/`, `trash/`, `context/` — working files

See `scripts/public-files.txt` for the canonical list.

## Pushing

```bash
just push
```

This pushes:
- `main` → public remote + private remote
- `staging`, `dev` → private remote only

## Promoting to Main

When staging has enough changes for a release:

```bash
# 1. Make sure staging has a proper description
jj describe -r staging -m "feat: your release description"

# 2. Promote (runs quality gate, moves main forward, pushes)
just promote
```

## Commit Message Convention

Use [Conventional Commits](https://www.conventionalcommits.org/):
- `feat:` — New feature
- `fix:` — Bug fix
- `docs:` — Documentation changes
- `refactor:` — Code restructuring
- `test:` — Adding/updating tests
- `chore:` — Build, CI, tooling changes

Always include the Co-authored-by trailer:
```
Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>
```

## Quick Reference

```bash
# See current bookmark state
jj --no-pager log --limit 10

# See what's in staging
jj --no-pager diff -r staging --stat

# See what's in dev
jj --no-pager diff -r dev --stat

# Push everything
just push

# Full release cycle
just promote
```
