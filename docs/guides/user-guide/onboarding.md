# Onboarding an Existing Project

The Quick Start above covers brand-new projects. But most of the time you already have a codebase with history, milestones, and half-finished work. This section walks you through bringing an existing project under tillr management.

## When to Use Onboarding

Use this workflow when:

- You have a working codebase and want tillr to track its features going forward.
- You want to retroactively record completed work so the dashboard reflects reality.
- You're adopting tillr mid-project and need to capture in-progress and planned work.

You don't need to model everything — just the user-facing capabilities you want to track, iterate on, and QA.

## The Onboard Command

```bash
tillr onboard --name my-project --scan
# Scanning project...
# Detected: Go (go.mod), Git history (847 commits), CI config (.github/workflows/ci.yml), README.md
# Initialized project "my-project"
# Suggested milestones and features written to .tillr-onboard.json — review and apply.
```

The `--scan` flag inspects your repository for:

- **Languages and frameworks** — `go.mod`, `package.json`, `pyproject.toml`, etc.
- **Git history** — recent activity, contributors, branch structure.
- **CI configuration** — GitHub Actions, GitLab CI, or similar.
- **README and docs** — project description and existing documentation.

The scan produces suggestions, not changes. You review them and decide what to track.

For non-interactive use (CI or agent-driven onboarding), pass `--yes` to automatically create suggested milestones and features:

```bash
tillr onboard --name my-project --yes
```

You can also run the steps below manually for full control.

## Step-by-Step Walkthrough

### 1. Initialize the Project

Start by creating the tillr database in your project root:

```bash
cd ~/projects/my-api
tillr init my-api
# Initializing project: my-api
# Created .tillr.json
# Created tillr.db
# Project "my-api" is ready.
```

### 2. Create Milestones for Your Project Phases

Think about how your project is organized — shipped releases, the current sprint, and what's next. Create a milestone for each:

```bash
tillr milestone add "v1.0 — Launch"
tillr milestone add "v1.1 — Performance"
tillr milestone add "v2.0 — Multi-tenant"
```

### 3. Record Completed Features

For work that's already shipped, add features with `--status done` so the dashboard reflects your actual progress:

```bash
tillr feature add "REST API endpoints" --milestone "v1.0 — Launch" --priority high
tillr feature edit feat-1 --status done

tillr feature add "Database migrations" --milestone "v1.0 — Launch" --priority high
tillr feature edit feat-2 --status done

tillr feature add "CI/CD pipeline" --milestone "v1.0 — Launch" --priority medium
tillr feature edit feat-3 --status done
```

This gives you an accurate history and makes milestone progress bars meaningful from day one.

### 4. Add In-Progress Features

Capture what's actively being worked on:

```bash
tillr feature add "Rate limiting" --milestone "v1.1 — Performance" --priority high
tillr feature edit feat-4 --status implementing

tillr feature add "Response caching" --milestone "v1.1 — Performance" --priority medium
tillr feature edit feat-5 --status implementing
```

### 5. Add Planned Features as Drafts

Future work stays in `draft` until you're ready to plan it:

```bash
tillr feature add "Tenant isolation" --milestone "v2.0 — Multi-tenant" --priority high
tillr feature add "Per-tenant billing" --milestone "v2.0 — Multi-tenant" --priority medium
tillr feature add "Admin dashboard" --milestone "v2.0 — Multi-tenant" --priority low
```

### 6. Set Up Roadmap Items

The roadmap gives a higher-level, categorized view of where the project is headed. Add items for themes that span multiple features:

```bash
tillr roadmap add "API performance overhaul" --priority high --category Performance
tillr roadmap add "Multi-tenancy support" --priority high --category Architecture
tillr roadmap add "Developer portal" --priority medium --category DX
```

## Tips for Choosing What to Track

- **Track user-facing capabilities**, not implementation tasks. "User authentication" is a feature; "refactor the auth middleware" is not.
- **Don't over-model.** If a piece of work doesn't need QA gating or agent iteration, it probably doesn't need a feature entry.
- **Use milestones for sequencing**, not for every sprint. Milestones work best as meaningful delivery checkpoints (releases, betas, demos).
- **Start small.** You can always add more features later. Begin with what you're actively building and expand as you go.

## Verifying the Onboard

Once you've added your milestones and features, verify that everything looks right:

```bash
# Check project health
tillr doctor
# ✓ .tillr.json found
# ✓ tillr.db schema is current (v1)
# ✓ No orphaned work items
# All checks passed.

# Review the overall picture
tillr status
# Project: my-api
#
# Features:  3 draft · 2 implementing · 3 done
# Milestones: v1.0 — Launch (3/3 done) · v1.1 — Performance (0/2 done) · v2.0 — Multi-tenant (0/3 done)

# See it all in the web viewer
tillr serve
# Listening on http://localhost:3847
```

Open the web viewer and confirm the dashboard, feature board, and roadmap all reflect your project's current state. From here you can start iteration cycles on your in-progress features and hand work off to agents.

---

« [User Guide](./README.md)
