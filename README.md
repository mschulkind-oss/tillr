# Lifecycle

[![CI](https://github.com/mschulkind/lifecycle/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind/lifecycle/actions/workflows/ci.yml)

**Human-in-the-loop project management for agentic software development.**

Lifecycle is the project manager that sits between humans and AI agents. It defines structured iteration cycles — plan, implement, review, approve — ensuring every piece of work flows through a pipeline with human checkpoints. Agents do the heavy lifting. Humans retain steering authority. Everything is captured in a local SQLite database for full visibility and searchability.

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
git clone https://github.com/mschulkind/lifecycle.git
cd lifecycle
just install

# Initialize a project
lifecycle init

# Add features to the roadmap
lifecycle add "User authentication system"
lifecycle add "API rate limiting"

# Run an iteration cycle
lifecycle cycle start

# Launch the web viewer
lifecycle serve
```

---

## 🔧 CLI Reference

```
lifecycle init                  # Initialize a new project
lifecycle add <feature>         # Add a feature to the roadmap
lifecycle cycle start           # Start an iteration cycle
lifecycle cycle status          # Show current cycle status
lifecycle serve                 # Start the web viewer
lifecycle --version             # Show version
```

See [docs/guides/](docs/guides/) for the full user guide.

---

## 🔄 Iteration Cycles

Work progresses through structured rounds with defined phases and quality gates. Each cycle converges toward completion through iteration, not luck.

See [docs/design/](docs/design/) for the full design documentation.

---

## ⚙️ Configuration

Lifecycle stores its configuration in `.lifecycle.json` in the project root.

```json
{
  "project": "my-project",
  "database": ".lifecycle.db"
}
```

---

## 🤝 Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, and the PR process.

---

## 📄 License

Apache 2.0 — see [LICENSE](LICENSE).
