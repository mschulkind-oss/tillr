# Tillr

[![CI](https://github.com/mschulkind-oss/tillr/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind-oss/tillr/actions/workflows/ci.yml)

**Human-in-the-loop project management for agentic software development.**

Tillr sits between humans and AI agents. It defines structured iteration cycles with human checkpoints, so agents do the heavy lifting while humans retain steering authority. Everything is captured in a local SQLite database.

---

## Install

```bash
# Homebrew
brew tap mschulkind-oss/tap && brew install tillr

# PyPI (works in any Python environment)
pipx install tillr   # or: uvx tillr

# Go
go install github.com/mschulkind-oss/tillr/cmd/tillr@latest

# From source (requires Go 1.24+, Node 22+, pnpm)
git clone https://github.com/mschulkind-oss/tillr.git && cd tillr && just install
```

---

## Quick Start

```bash
# Initialize a project
cd ~/my-project
tillr init my-project

# Add features
tillr feature add "User authentication"
tillr feature add "API rate limiting"

# Start an iteration cycle
tillr cycle start collaborative-design <feature-id>

# Launch the web dashboard
tillr serve
```

---

## CLI Overview

```
tillr init <name>               Initialize a new project
tillr status                    Project overview

tillr feature add <name>        Add a feature
tillr feature list              List features (--status, --json)
tillr feature show <id>         Feature details with history

tillr cycle start <type> <id>   Start an iteration cycle
tillr cycle status              Active cycle progress
tillr cycle advance --approve   Advance past a human checkpoint

tillr next --json               Get next work item (for agents)
tillr done --result "..."       Complete current work
tillr fail --reason "..."       Report failure

tillr serve                     Single-project web dashboard
tillr daemon                    Multi-project server daemon
```

See [docs/guides/user-guide.md](docs/guides/user-guide.md) for the full guide.

---

## Multi-Project Daemon

Run Tillr as a system service managing multiple projects:

```bash
# Configure projects
tillr daemon init ~/code/project-a ~/code/project-b

# Install systemd user service
just install-service

# Deploy (build + restart)
just deploy
```

The daemon serves all projects on a single port with a project switcher in the UI.

---

## Development

```bash
just dev        # Backend (air live-reload) + Vite frontend
just check      # Format + lint + test
just build      # Build binary to bin/tillr
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for full development setup.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
