# 4. Marcus — Developer, Onboarding to an Existing Tillr Project

**Context:** Marcus just joined a team that uses tillr. There are 6 workstreams,
40 features, and a year of history in the database. He needs to get oriented.

**First 10 minutes:**

1. He clones the repo. He sees `.tillr.json` and `tillr.db` in the root.
   He runs `tillr serve` and opens the UI.

2. He sees the dashboard with real data — progress bars, active workstreams,
   recent activity. Good start.

3. He clicks into a workstream. He sees features, their statuses, a timeline
   of what happened. He can drill into any feature to see its full history:
   spec, QA results, agent discussions, status transitions.

4. He wants to find why a particular decision was made. He searches:
   ```
   tillr search "why did we switch to PostgreSQL"
   ```
   And finds a discussion thread from 3 months ago where the team debated
   database options. The decision and rationale are captured.

   **Gap:** Search works on feature names and descriptions, but does it
   search discussions, QA notes, and specs? It should search everything.

5. He wants to understand the architecture. The roadmap page shows the
   big picture — what's planned, what's in progress, what's done. The
   workstream view shows how features group together.

**What would trip him up:**
- The database is in `.gitignore`, so he has it locally but not on every
  clone. If he clones fresh on another machine, there's no `tillr.db`. He
  needs either:
  - A `tillr export/import` flow (export to git-tracked files, import on
    clone)
  - The daemon syncing DBs between machines (doesn't exist)
  - A team-shared DB location (not the current model)

  **Gap:** This is the "tillr is local-only" problem. History doesn't
  travel with the repo. The `export-git` feature exists but isn't wired
  into the standard workflow.

- He doesn't know the CLI commands. The UI is browse-only for most things.
  He needs a `tillr guide` or the UI needs basic mutation capabilities.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
