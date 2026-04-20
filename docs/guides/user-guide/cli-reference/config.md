# Configuration Management

## `tillr config init`

Create a `.tillr.yaml` configuration file with default values.

```bash
tillr config init
# Created .tillr.yaml with defaults
```

## `tillr config show`

Show the current configuration (merged defaults + file overrides).

```bash
tillr config show
# default_milestone: ""
# default_priority: 5
# server_port: 3847
# theme: system
# agent_timeout_minutes: 30
# db_path: tillr.db
```

## `tillr config set <key> <value>`

Set a configuration value in `.tillr.yaml`.

```bash
tillr config set server_port 8080
tillr config set default_priority 7
tillr config set theme dark
```

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
