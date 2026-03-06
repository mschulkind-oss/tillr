# Committing Changes — Lifecycle Project

This project uses **jj (Jujutsu)** colocated with git, and a **public/private two-remote** model for open-source development.

## Bookmark Structure

```
@  (working copy — your changes go here)
│
○  dev           → private remote only (AGENTS.md, internal docs)
│
○  staging       → private remote only (accumulates public changes)
│
◆  main          → BOTH public and private remotes
```

## How to Commit

### Public changes (source code, tests, public docs)
```bash
# Squash into staging
jj squash --into staging
```

### Private changes (AGENTS.md, OPEN_QUESTIONS.md, internal docs)
```bash
# Squash into dev
jj squash --into dev
```

### What Goes Where

**Public (squash into staging):**
- `cmd/`, `internal/`, `web/`, `tests/`
- `go.mod`, `go.sum`
- `Justfile`, `mise.toml`, `.gitignore`
- `README.md`, `LICENSE`, `NOTICE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`
- `.github/`
- `docs/VISION.md`, `docs/design/`, `docs/guides/`

**Private (squash into dev):**
- `AGENTS.md`, `OPEN_QUESTIONS.md`
- `.copilot/`, `.gemini/`, `.yolo/`
- `yolo-jail.jsonc`
- `docs/open-source-checklist.md`, `docs/NAME_CANDIDATES.md`
- `scratch/`, `trash/`, `context/`

## Promoting to Main

```bash
just promote
```

This runs `prepromote` (quality gate), fast-forwards `main` to `staging`, creates a fresh `staging` and `dev`, and pushes everything.

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
