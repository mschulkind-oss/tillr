# 10. Derek — Developer, Adding a Second Project to Tillr

**Context:** Derek has tillr running on his main project. It's going well —
agents are processing the queue, he's QAing features, everything is tracked.
Now he wants to use tillr on a second project.

**What he expects:**

1. Go to the new project directory, run `tillr init`, and have it work
   independently. His main project is unaffected.

2. Open one browser tab and see both projects, with easy switching.

3. CLI commands in each directory operate on that project's data. No
   "switching" in the CLI — the directory IS the project.

**What actually happens today:**

1. `cd ~/work/new-project && tillr init new-api` — this works. Creates
   `.tillr.json` and `tillr.db`. Completely independent from his other
   project.

2. He's been running `tillr serve` in his main project. Now he needs the
   daemon:
   ```
   tillr daemon init --project ~/work/main-project --project ~/work/new-project
   ```
   This creates `~/.config/tillr/daemon.json`. He stops `tillr serve`
   and starts `tillr daemon start`.

   **Gap:** The transition from single-project `serve` to multi-project
   `daemon` is manual and undocumented. It should be:
   - Detect that `tillr serve` is running on the same port and offer to
     replace it
   - Or better: `tillr serve` should auto-upgrade to daemon mode when it
     detects other projects exist in the daemon config

3. The UI now shows a project switcher in the sidebar. He can flip between
   projects. Each project has its own features, workstreams, queue.

4. CLI just works:
   ```
   cd ~/work/main-project && tillr feature list    # shows main project features
   cd ~/work/new-project && tillr feature list     # shows new project features
   ```
   No switching command, no context confusion. This is the right model.

**What would trip him up:**
- He doesn't know `tillr daemon` exists. After running `tillr init` on the
  second project, he might just run `tillr serve --port 3848` and juggle
  two browser tabs. Nothing suggests the daemon.

  **Gap:** `tillr init` should detect existing projects (by checking
  `~/.config/tillr/daemon.json` or scanning common paths) and suggest
  daemon mode: "You have 2 tillr projects. Run `tillr daemon start` to
  see both in one UI."

- Adding a third project later means editing `daemon.json` manually.
  `tillr daemon add ~/work/third-project` should exist.

- The daemon needs to be a persistent service (systemd/launchd), not
  something he runs in a terminal. `tillr daemon install-service` should
  exist.

  **Gap:** The daemon CLI exists (`tillr daemon start`) but there's no
  service installation. The user has to manage the process themselves.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
