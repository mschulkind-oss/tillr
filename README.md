# Tillr

[![CI](https://github.com/mschulkind/tillr/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind/tillr/actions/workflows/ci.yml)

**Human-in-the-loop project management for agentic software development.**

Tillr is the project manager that sits between humans and AI agents. It defines structured iteration cycles — plan, implement, review, approve — ensuring every piece of work flows through a pipeline with human checkpoints. Agents do the heavy lifting. Humans retain steering authority. Everything is captured in a local SQLite database for full visibility and searchability.

> **Platform:** Linux. macOS is untested and unsupported. Windows is not supported.

---

## ✨ Features

- **CLI-Driven Workflow** — Manage features, iterations, and quality gates from the command line
- **Web Viewer** — Live-updating dashboard showing project status, iteration progress, and history
- **Structured Iteration Cycles** — Plan → implement → review → approve, with scoring and convergence
- **SQLite Storage** — All state stored locally in a single SQLite database, fully queryable
- **Agent Integration** — Designed for AI agents to consume and produce structured work items
- **Human Checkpoints** — Nothing ships without human approval; QA is a first-class stage

---

## 🚀 Quick Start

```bash
# Install from source
git clone https://github.com/mschulkind/tillr.git
cd tillr
just install

# Initialize a project
tillr init

# Add features to the roadmap
tillr add "User authentication system"
tillr add "API rate limiting"

# Run an iteration cycle
tillr cycle start

# Launch the web viewer
tillr serve
```

---

## 🔧 CLI Reference

```
tillr init                  # Initialize a new project
tillr add <feature>         # Add a feature to the roadmap
tillr cycle start           # Start an iteration cycle
tillr cycle status          # Show current cycle status
tillr serve                 # Start the web viewer
tillr --version             # Show version
```

See [docs/guides/](docs/guides/) for the full user guide.

---

## 🔄 Iteration Cycles

Work progresses through structured rounds with defined phases and quality gates. Each cycle converges toward completion through iteration, not luck.

See [docs/design/](docs/design/) for the full design documentation.

---

## ⚙️ Configuration

Tillr stores its configuration in `.tillr.json` in the project root.

```json
{
  "project": "my-project",
  "database": ".tillr.db"
}
```

---

## 🤝 Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, and the PR process.

---

## 📄 License

Apache 2.0 — see [LICENSE](LICENSE).
