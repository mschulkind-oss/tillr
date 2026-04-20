# Troubleshooting / FAQ

## "No .tillr.json found"

You're not inside a tillr project. Run `tillr init <name>` to create one, or `cd` into an existing project directory.

## "Database schema version mismatch"

Your `tillr.db` was created with a different version of tillr. Run `tillr doctor` to diagnose. Future versions will include automatic migrations.

## Can I use tillr with multiple agents?

Yes. Multiple agents can call `tillr next` concurrently. Each call returns a different work item — work items are assigned atomically to prevent double-assignment.

## Can I edit the database directly?

You can, but it's not recommended for state changes. Use the CLI to ensure events are logged, dependencies are checked, and cycles advance correctly. Direct reads (for debugging or reporting) are fine and encouraged.

## How do I back up my project?

Copy `.tillr.json` and `tillr.db`. That's everything. Both are regular files — commit them, sync them, or back them up however you like.

## Where are logs stored?

Events are stored in the `events` table inside `tillr.db`. Use `tillr history` or query the table directly. There are no external log files.

## Can the web viewer modify data?

No. The web viewer is strictly read-only. All state changes go through the CLI. This is a deliberate design choice — the CLI is the single source of truth.

---

« [User Guide](./README.md)
