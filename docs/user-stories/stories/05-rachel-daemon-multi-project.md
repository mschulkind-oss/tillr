# 5. Rachel — Developer, Managing Multiple Projects via Daemon

**Context:** Rachel works on 3 projects simultaneously. She wants one browser
tab showing all of them with easy switching. She heard about `tillr daemon`.

**Setup:**

1. Each project already has tillr set up locally (`.tillr.json` + `tillr.db`).
   She runs CLI commands in each project directory — no switching, no context.
   This already works.

2. She wants the unified UI:
   ```
   tillr daemon init --project ~/work/api --project ~/work/frontend --project ~/work/infra
   ```
   This creates `~/.config/tillr/daemon.json`:
   ```json
   {
     "projects": [
       {"path": "/home/rachel/work/api", "slug": "api"},
       {"path": "/home/rachel/work/frontend", "slug": "frontend"},
       {"path": "/home/rachel/work/infra", "slug": "infra"}
     ],
     "port": 3847
   }
   ```

3. `tillr daemon start` — starts one HTTP server serving all three projects.
   The UI shows a project switcher in the sidebar.

4. She opens `localhost:3847` and sees her current project. The sidebar shows
   a dropdown with api / frontend / infra. She switches between them
   instantly — no page reload, just a different dataset.

**What she notices:**
- CLI in each project directory just works. `cd ~/work/api && tillr feature list`
  shows api's features. `cd ~/work/frontend && tillr feature list` shows
  frontend's features. No `tillr project switch`. The directory IS the context.
- The daemon is read-only for a project's data — it just serves what's in
  each project's local `tillr.db`. Writes still happen through the CLI in
  each project directory.
- Adding a new project means: `tillr init` in that directory, then edit
  `daemon.json` to add the path, then restart the daemon.

   **Gap:** `tillr daemon add ~/work/new-project` should exist — today she
   has to manually edit the JSON file. The daemon should also auto-reload
   config on change instead of requiring a restart.

**What would trip her up:**
- The daemon serves each project's local DB. If she's SSH'd into a remote
  machine, the daemon needs to run there too. There's no remote access
  story.
- The project switcher in the UI works, but there's no cross-project view.
  She can't see "what needs my attention across all projects." Each project
  is fully isolated.

  **Gap:** A global inbox across projects in daemon mode — "api has 3 items
  for QA, frontend has 1 blocked feature." This would be the daemon's
  landing page.

- WebSocket connections need to be project-scoped in daemon mode, otherwise
  changes in one project trigger refreshes in another.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
