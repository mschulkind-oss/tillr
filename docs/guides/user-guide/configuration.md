# Configuration Reference

Tillr uses two configuration files:

## Project File: `.tillr.json`

Created by `tillr init`, this file identifies the project root and stores core settings:

```json
{
  "project_dir": ".",
  "db_path": "tillr.db",
  "server_port": 3847
}
```

Tillr finds your project by walking up from the current directory until it finds `.tillr.json`, so you can run commands from any subdirectory.

## Defaults File: `.tillr.yaml`

Created by `tillr config init`, this optional file stores configuration defaults. It is merged with built-in defaults at runtime. Create it with:

```bash
tillr config init
```

Available fields:

| Field | Default | Description |
|-------|---------|-------------|
| `default_milestone` | `""` | Default milestone for new features |
| `default_priority` | `5` | Default priority for new features (integer) |
| `server_port` | `3847` | Port for the web viewer |
| `theme` | `system` | Web viewer theme: `light`, `dark`, or `system` |
| `agent_timeout_minutes` | `30` | Minutes before an agent is considered stale |
| `db_path` | `tillr.db` | Path to the SQLite database file |

View current configuration (merged defaults + file):

```bash
tillr config show
```

Set individual values:

```bash
tillr config set server_port 8080
tillr config set theme dark
```

## Project Discovery

Tillr walks up from the current working directory to find `.tillr.json`. This means you can run commands from any subdirectory:

```bash
cd my-app/src/auth
tillr status   # finds ../../.tillr.json
```

## Database

All data is stored in a single SQLite file (`tillr.db` by default). Key tables:

| Table | Purpose |
|-------|---------|
| `projects` | Project metadata |
| `milestones` | Milestone definitions and status |
| `features` | Features with tillr state, priority, and assignment |
| `feature_deps` | Dependency graph between features |
| `work_items` | Individual work items with agent prompts and results |
| `events` | Full audit log of every state change |
| `roadmap_items` | Roadmap entries with priority and category |
| `qa_results` | QA approval/rejection records with notes |
| `heartbeats` | Agent activity heartbeats |

You can inspect the database directly with any SQLite client:

```bash
sqlite3 tillr.db "SELECT id, name, status FROM features"
```

---

« [User Guide](./README.md)
