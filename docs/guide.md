---
title: "User guide: what tillr is, why to use it, and your first landed item"
status: accepted
stage: CURRENT
tags: [tillr, guide, onboarding]
summary: "What tillr is, why to use it instead of running coding agents by hand, how charters and Git landing work, how to set up tillr in a yolo jail from the pack, and a step-by-step walkthrough from a fresh clone to one landed item."
---

# User guide: what tillr is, why to use it, and your first landed item

`tillr` is a work-queue toolchain for coding agents: a family of command-line programs backed by one SQLite database. You put work into the queue, and an automated loop runs coding agents in isolated Git worktrees, checks every result against your repository's tests, and lands passing changes as clean commits on your trunk branch. It is built for developers and teams who want coding agents to work through a backlog of tasks without colliding with each other or leaving broken commits on the main branch.

> **Known gaps.**
> 1. **No `yolo` jail installer for `tillr-pi` in this tree.** Building `mschulkind-oss/tillr-pi`
>    by hand from its `tillr-required` branch is verified below. This repository ships no `yolo`
>    pack or jail configuration for installing `tillr-pi` (`ls yolo-packs/` lists only `tillr`,
>    and `rg -n 'tillr-required' .` returns nothing).
> 2. **Live model credentials not exercised here.** Every build, store, filing, loop, gate, and
>    landing command below was run in a temporary directory, and the freshly built `tillr-pi`
>    binary was verified with `pi --features` and against the placeholder policy. The final
>    end-to-end landing step used a local stub `pi` instead of a live model provider because this
>    verification environment does not invoke external model credentials.

---

## 1. Why use tillr instead of running agents by hand

Running one coding agent by hand in your working directory is simple until you try to run two at once, or leave them running while you do something else. When two agents edit the same checkout, they overwrite each other's files. When an agent commits directly to your main branch, a broken change blocks everyone else until you revert it by hand. When an agent stops halfway through, you have to read its terminal scrollback to figure out what happened.

Today, the `tillr` programs solve those problems in six concrete ways:

- **Isolated worktrees per task.** Every agent run executes inside its own leased Git worktree, separate from your main checkout and from every other active agent.
- **One SQLite queue and event log.** Every task, state change, criterion verdict, run attempt, and landing is recorded in one append-only event log, so you can inspect why any task is running, finished, or waiting.
- **Automated checks before landing.** Before any change touches your trunk branch, the loop runs your repository's declared checks against the candidate commit. If a check fails, the loop replays that same check on the base commit so a pre-existing broken test on trunk does not blame the agent's work.
- **Atomic squash landing.** Passing candidates are three-way merged and squashed onto your trunk branch as one commit under a file lock, re-authored to your configured Git identity, without leaving lane branches behind.
- **Post-landing trunk proof.** After changes land, a rolling check runs against the trunk tip. If a merged change breaks a heavy check that did not run before landing, the loop files a fixer task and holds new landings until the trunk is green again.
- **Failure classification and backend failover.** When an agent fails because a model provider is down or a quota window is full, the loop separates infrastructure faults from code failures, pauses or closes the failing backend, and keeps your queue intact.

---

## 2. The core ideas

Eight terms carry the entire workflow (see [the vocabulary design](./design/tillr-vocabulary.md) for the full glossary):

- **The store** — the single SQLite database (`.tillr/state.db` by default) that holds all queued tasks, their criteria, the append-only event log, lane leases, and landing records. Every `tillr` program reads and writes this store.
- **An item** — one unit of work tracked in the store, with a title, request words or testable **criteria** (the conditions its result is judged against), and a lifecycle state (`in-queue`, `blocked`, `landed`, `closed`, and so on).
- **A lane** — a leased slot (under `.tillr/lanes/<lane>/worktree` by default) that holds a dedicated Git worktree where one agent run works in isolation from your main checkout and from other lanes.
- **The loop** — the long-running drain process (`tillr drain loop`) that scans the store, picks ready items in rank order, assigns them to free lanes, starts agent runs, and routes finished candidates through checking and landing.
- **The gate** — the automated check runner (`tillr-gate`) that runs your repository's declared JSON checks against a lane's committed candidate before anything touches the trunk.
- **Landing** — the atomic squash (`tillr-land`) of a gated candidate onto your trunk branch as one commit under a lock, re-authored to your configured Git identity.
- **The trunk proof** — the rolling check that runs on the trunk tip after landings to confirm the full suite still passes; when it fails, the loop files a fixer item and holds other landings until the trunk is green.
- **The whale 🐋** — the coordinating AI session you talk to while `tillr` runs. Instead of typing every command yourself, you tell the whale what you want done: it files items, starts and watches the drain, clears faults, and records delegated decisions under `whale` (accepted beside `owner` in the store). A lane worker builds or checks **one item in one lane**; the whale steers the **whole drain** and never runs inside a lane. Under [Authority, not approval](./reference/authority-not-approval.md#terms), reversible actions stay with the whale while irreversible loss, spending, external publication, and policy choices stay with you.

Two smaller terms also appear in configuration:

- **A seat** — the role a run plays (`build`, `design`, `plan`, `review`, `verify`), independent of which agent program or model fills it.
- **A backend policy** — a JSON file that maps each seat to a backend entry (an adapter program, a model name, and an effort level).

---

## 3. Charters: how tillr adapts to different workflows

A **charter** is the layer that turns `tillr`'s core queue and workflow engine into one specific use. It declares the item states a store allows, which state means ready for dispatch, which states count as finished, which state transitions are valid, which prerequisites an item needs before dispatch, and how an item finishes (either by landing a Git commit or by calling a hook program). In the Go code and CLI flags, a charter is called a **definition** (`--definition`).

When you initialize a store without `--definition`, `tillr` uses the built-in **`git-landing`** charter. That default charter sets up six states (`not-started`, `blocked`, `in-queue`, `landed`, `resolved-elsewhere`, `closed`), dispatches items from `in-queue`, checks prerequisites (`design-document`, `design-questions`, `owner-clean`), and finishes items by landing a commit on your trunk branch.

You write a **custom charter** when you want `tillr`'s queue, isolated lanes, workflow graphs, and event tracking for work that does not land in Git — for example, triaging bug reports, running scheduled investigations, or verifying findings through a hook program (`completes: "hook"`). See [Charters: how they work and how to write your own](./guide/charters.md) for a step-by-step walkthrough and a runnable example.

---

## 4. How Git landing works

Under the default `git-landing` charter, a finished change goes through a strict sequence before and after it reaches your trunk branch:

1. **Candidate and boundary check.** When a lane worker finishes, the loop checks that the lane's `HEAD` moved forward from its base commit (an unchanged branch is refused as `candidate-no-op`). It also compares snapshots of refs and worktrees taken before and after the run to verify the worker touched only its own lane.
2. **Gate and base replay.** The loop runs `tillr-gate` against the candidate commit (`base..head`). If a fast check fails, the loop replays that exact failing check on the base commit in a detached worktree. If the check already failed on the base commit, the candidate is acquitted of breaking it; if the base was green, the candidate is rejected and returned to the worker or held for repair.
3. **Atomic squash under the land lock.** Once the gate records a passing verdict bound to `(base_sha, head_sha^{tree})`, `tillr-land` acquires the repository's land lock, three-way merges `base..head` onto the current trunk tip, writes a single squashed commit re-authored to your configured Git identity, writes a `landings` row in the store, and fast-forwards your trunk branch. If your main checkout has uncommitted changes on a path the landing would overwrite, the loop refuses to touch your checkout and holds the item as `trunk-path-dirty` until your working tree is clean.
4. **Post-landing trunk proof.** After commits land, the loop runs the rolling heavy suite against the new trunk tip. If a heavy check fails, the loop marks the trunk broken, files a high-priority fixer item, and holds ordinary landings until the trunk is green again.
5. **What the owner sees.** On the trunk branch, you see one clean, linear commit per landed item authored under your Git identity, with no merge commits and no private queue metadata in commit messages. In the store, `tillr view status`, `tillr view waiting`, and `tillr item show` show the landed item, its recorded criterion verdicts, and the exact landing commit SHA.

---

## 5. Setting up tillr in a yolo jail from the pack

If you already use `yolo` (`yolo-jail`) on your host machine, you do not need to clone and build `tillr` in every repository. Instead, you can select the `tillr` pack once in your user configuration and get the `tillr` programs and skills inside every `yolo` jail you launch.

Four terms apply to this setup:

- **A `yolo` jail** — an isolated container environment launched by `yolo` where coding agents work against a bind-mounted repository without direct access to your host credentials or home directory.
- **A pack** — a directory of jail configuration (files, lazy program launchers, and agent skills) that `yolo` delivers into a jail at launch.
- **User scope vs. workspace scope** — `yolo` keeps pack selection in **user scope** (`~/.config/yolo-jail/config.toml`), never in a repository's `yolo-jail.toml`. Because a workspace config travels with the repository and is agent-editable, `yolo check` refuses a `packs` key in `yolo-jail.toml`. Selecting `yolo-packs/tillr` in user scope makes it available in every workspace you launch, and nothing in the pack runs `tillr` unasked.
- **A skill** — a `SKILL.md` instruction file delivered into each agent's skill directory that teaches the agent how to follow a specific workflow.

### Selecting `yolo-packs/tillr` on the host

Run the selection commands **on the host** (inside a jail, `yolo pack add` refuses with exit `1` because only the host edits `~/.config/yolo-jail/config.toml`).

First, inspect what the pack claims before selecting it. From a local clone of `tillr`, run `yolo pack lint yolo-packs/tillr` (or `yolo pack footprint ./yolo-packs/tillr`). Observed in this jail on `yolo-packs/tillr`:

```console
$ yolo pack lint yolo-packs/tillr
✓ pack ok — 2123 file(s) stage
delivers:
  skills         skills/ → every agent (declared broadcast)  (contributes[12])
declares 14 claim(s):
  executables    22 files  ...
  files          .local/share/tillr  read-only tree
  program        tillr  pack tree: programs/tillr
  program        tillr-drain  pack tree: programs/tillr-drain
  program        tillr-gate  pack tree: programs/tillr-gate
  program        tillr-item  pack tree: programs/tillr-item
  program        tillr-land  pack tree: programs/tillr-land
  program        tillr-loop  pack tree: programs/tillr-loop
  program        tillr-run  pack tree: programs/tillr-run
  program        tillr-store  pack tree: programs/tillr-store
  program        tillr-trace  pack tree: programs/tillr-trace
  program        tillr-view  pack tree: programs/tillr-view
  program        tillr-watch  pack tree: programs/tillr-watch
  skills         → every agent  from skills/ — merged (workspace < built-in < packs < your local pack)
```

Next, add the pack on the host in one of two ways:

- **From GitHub (recommended: pin a tag or full 40-hex commit SHA).** A tag or commit SHA is never re-fetched automatically by a launch; `--ref main` follows `main` and re-fetches at most hourly:

  ```console
  $ yolo pack add --git https://github.com/mschulkind-oss/tillr \
      --subdir yolo-packs/tillr \
      --ref <tag-or-commit-sha>
  ```

- **From a local clone of `tillr`.** Register the clone's `yolo-packs` directory under `[pack_sources]` in user config, then add `tillr` from that source:

  ```console
  $ yolo config set pack_sources.tillr-local '"~/code/tillr/yolo-packs"' --user
  $ yolo pack add tillr-local/tillr
  ```

Optionally run `yolo pack install` on the host to fetch a Git pack immediately instead of waiting for the next launch.

*(Not verified in this jail: `yolo pack add`, `yolo config set --user`, and `yolo pack install` edit or fetch into the host's user config and pack store, so they cannot be executed from inside a jail. Their syntax was checked against `yolo pack --help` and `yolo config-ref` inside this jail.)*

### Applying the change and relaunching the jail

On the host:

1. **Apply to host config surfaces (when you also run agents on the host):**

   ```console
   $ yolo host apply --assert
   ```

   Without `--assert`, `yolo host apply` runs as a dry run and writes nothing. Running it inside a jail refuses with exit `1`.

2. **Relaunch the workspace jail** so `yolo` renders the pack's files, launchers, and skills into the jail's home directory (`~/.local/bin/` and `~/.local/share/tillr/`):

   ```console
   $ yolo stop && yolo -- bash
   ```

*(Not verified in this jail: `yolo host apply --assert` and `yolo stop && yolo -- bash` are host-only commands. Their syntax was checked against `yolo host --help` and `yolo stop --help` inside this jail.)*

### Proving all eleven programs are on `PATH`

The pack puts **eleven** source-built launchers on `PATH` (`~/.local/bin/`): `tillr`, `tillr-drain`, `tillr-gate`, `tillr-item`, `tillr-land`, `tillr-loop`, `tillr-run`, `tillr-store`, `tillr-trace`, `tillr-view`, and `tillr-watch`. A **source-built launcher** compiles its Go binary from `~/.local/share/tillr/<module>/` on first call for a given source digest and caches it under `${XDG_CACHE_HOME:-$HOME/.cache}/tillr/bin/`; subsequent calls run the cached binary without invoking Go (see [Distribution: one source-built pack with eleven PATH binaries](./reference/tillr-distribution.md)).

Inside the relaunched jail, confirm that all eleven programs resolve on `PATH` and answer `--help`:

```console
$ for bin in tillr tillr-drain tillr-gate tillr-item tillr-land tillr-loop tillr-run tillr-store tillr-trace tillr-view tillr-watch; do
    printf '%-12s %s\n' "$bin" "$("$bin" --help 2>&1 | head -n 1)"
  done
tillr        tillr: store, item, land, drain, run, gate, view, watch, trace, notify, doctor
tillr-drain  tillr-drain: probe-backend, workflow, why, workers, policy, plan, once, split, retry, clear-fault, accept-rewrite, acknowledge-patch-move, push, sweep-refs, acquit, examine, set-aside, boundary, resume, scale, stop, restart, keep, fork-gate, loop-adapter or loop
tillr-gate   tillr-gate: change, checks, classify, enable, plan, run, shadow
tillr-item   tillr-item: apply, rank, dep, sync, reconcile, comments, list, show, state, place, idea, doc-triage, request, close, reopen, decision, mail, verdict or judge
tillr-land   tillr-land: pass or one
tillr-loop   tillr-loop: the Git-landing definition's hook program.
tillr-run    tillr-run: exec, pause, request, lanes, status, release, mark-copied-usage, copied-usage, claude-landing, claude-quota, pi-build-agent-dir, pi-stop-at-gap, pi-compact, pi-landing-call-held or extract-report
tillr-store  tillr-store: init, definition, seed, check, recover, checkpoint, inventory or events
tillr-trace  usage: tillr-trace [-h] [--trace-root TRACE_ROOT] [--store STORE] [--loop-dir LOOP_DIR]
tillr-view   tillr-view: status, events, heavy-misses, waiting, alarms, passes, lanes, running, run, trace, serve, restart or perf-log
tillr-watch  Usage:
```

*(Observed in this jail on 2026-11-25. In this verification jail, `tillr-loop` and `tillr-trace` were served from the repository's `PATH`-prepended build because the host's rendered pack predated those two launchers; on a freshly applied pack all eleven resolve from `~/.local/bin/`.)*

### Skills delivered by the pack — and skills not in the pack yet

`yolo-packs/tillr` delivers **two** skills into every agent's skill directory:

- **`tillr-operating`** ([`yolo-packs/tillr/skills/tillr-operating/SKILL.md`](../yolo-packs/tillr/skills/tillr-operating/SKILL.md)) — how to operate and recover the drain loop: how the binaries compose across the seven-step loop (`plan → stage → run → judge → land → verify → audit`), which exit code means what, and how to learn flags from each binary's `--help`.
- **`tillr-designs`** ([`yolo-packs/tillr/skills/tillr-designs/SKILL.md`](../yolo-packs/tillr/skills/tillr-designs/SKILL.md)) — how to read the delivered design documentation snapshot (`~/.local/share/tillr/docs/`), tell what is built versus designed, open, or parked, see which surfaces are stable, and send requests or comments from another repository.

Two things are **not** delivered by the pack today:

- **Other operator skills** — skills an operating agent uses for timed drain check-ins and lane work are not in `yolo-packs/tillr` yet.
- **Repository-local skills** — [`tillr-prose-style`](../.agents/skills/tillr-prose-style/SKILL.md) (writing readable queue item prose) lives in this repository's `.agents/skills/` directory rather than in the pack.

### Getting a `pi` with `parked-tool-calls` inside a jail

No pack in this repository installs `mschulkind-oss/tillr-pi` (`ls yolo-packs/` lists only `tillr`). Inside your jail, check first whether the `pi` already on `PATH` advertises `parked-tool-calls`:

```console
$ pi --features
["parked-tool-calls"]
```

*(Observed in this jail on 2026-11-25.)*

If `parked-tool-calls` is missing, build `mschulkind-oss/tillr-pi` from its `tillr-required` branch following [Getting a compatible `pi` from `mschulkind-oss/tillr-pi`](#getting-a-compatible-pi-from-mschulkind-osstillr-pi). Place the clone in a directory that persists across jail restarts rather than `/tmp` or a throwaway home path, then put its `cli.js` symlink ahead of upstream `pi` on `PATH` and re-run `pi --features`.

### First steps once the pack is active

With the eleven `tillr*` binaries and a compatible `pi` on `PATH`, the existing walkthrough below covers the rest of the setup:

1. **Create a store** in your repository — follow [Creating a store in your own repository](#creating-a-store-in-your-own-repository) (`tillr store init --store .tillr/state.db`).
2. **Write a backend policy** — follow [Writing a backend policy](#writing-a-backend-policy). When running from the pack without a `tillr` clone, point `adapter` at `~/.local/share/tillr/tillr-run/backends/pi/run-pi` (and pass `--brief-template ~/.local/share/tillr/tillr-run/backends/brief-template.md` if your repository does not carry its own template).
3. **Probe a backend** before relying on it — run `tillr drain probe-backend --store .tillr/state.db --backend <name> -- <smoke-command>` (see [Three things most likely to go wrong first](#three-things-most-likely-to-go-wrong-first)).
4. **Start the keeper** — a **keeper** (`tillr drain keep`) is the lightweight supervisor that keeps `tillr drain loop` alive for a checkout without changing any item decision. Start it with `tillr drain keep -- --policy ~/.config/tillr/my-policy.json --lanes 1` (or `just tillr-keep -- --policy ~/.config/tillr/my-policy.json --lanes 1` inside a `tillr` clone), and see [Starting the loop and reading what it prints](#starting-the-loop-and-reading-what-it-prints) and [Where a landed item ends up](#where-a-landed-item-ends-up) to read loop output and inspect landed work.

---

## 6. Walkthrough: from a fresh clone to your first landed item

The steps below take you from an empty environment to one commit landed by the drain loop in a Git repository of your own.

### What must be installed

Check that these tools are on your `PATH` before you start:

```console
$ go version        # Go toolchain (builds the tillr programs)
$ just --version    # command runner used by the build recipe
$ git --version     # Git (worktrees, three-way merge landing)
$ node --version    # Node.js >= 22.19 (required to build tillr-pi)
```

You also need a `pi` binary on `PATH` whose `pi --features` output includes
`parked-tool-calls`:

```console
$ pi --features
["parked-tool-calls"]
```

A *feature* is a capability name printed as a JSON array by `pi --features`. Upstream
[earendil-works/pi](https://github.com/earendil-works/pi) does not include `parked-tool-calls`
yet, and `tillr`'s pi adapter refuses to run without it (see
[the pi backend README](../tools/tillr-run/backends/pi/README.md#required-pi-features)).

### Getting a compatible `pi` from `mschulkind-oss/tillr-pi`

[mschulkind-oss/tillr-pi](https://github.com/mschulkind-oss/tillr-pi) is a fork of upstream `pi`
that carries the `parked-tool-calls` support `tillr` requires on its `tillr-required` branch.

Clone the `tillr-required` branch, install its dependencies, build the bundle, and symlink the
CLI onto your `PATH`:

```console
$ git clone --branch tillr-required https://github.com/mschulkind-oss/tillr-pi.git
$ cd tillr-pi
$ npm ci --ignore-scripts
$ npm run build
$ mkdir -p "$HOME/.local/bin"
$ ln -sf "$PWD/packages/coding-agent/dist/bundle/cli.js" "$HOME/.local/bin/pi"
$ export PATH="$HOME/.local/bin:$PATH"
$ pi --features
["parked-tool-calls"]
```

Then connect a model provider inside `pi` (for example by running `pi` interactively and using
`/login`, or exporting your provider's API key) and confirm which models are available:

```console
$ pi --list-models
```

*(Known gap: this repository does not ship a `yolo` pack or jail configuration that installs
`mschulkind-oss/tillr-pi`. If you run inside a `yolo` jail whose host already provides a `pi`
binary, check `pi --features` first; if `parked-tool-calls` is absent, build `tillr-pi` by hand
as shown above.)*

### Building the tillr programs

Clone `tillr` and build the programs (`tillr`, `tillr-drain`, `tillr-gate`, `tillr-item`,
`tillr-land`, `tillr-run`, `tillr-store`, `tillr-view`, `tillr-watch`):

```console
$ git clone https://github.com/mschulkind-oss/tillr.git
$ cd tillr
$ export TILLR_CLONE="$PWD"
$ YOLO_DURABLE_DIR="$HOME/.local/share/tillr-build" TILLR_BUILD_UNPROVED=HEAD just tillr-build
$ export PATH="$HOME/.local/share/tillr-build/bin:$PATH"
$ tillr --help
tillr: store, item, land, drain, run, gate, view, watch, notify, doctor
Run `tillr <noun> --help` for one noun's faces.
```

Two environment variables are required on a fresh clone:

- `TILLR_BUILD_UNPROVED=HEAD` — `just tillr-build` normally builds from `refs/tillr/green`, a
  ref the loop creates after proving the trunk. A fresh clone has no `refs/tillr/green`, so
  `TILLR_BUILD_UNPROVED=HEAD` bootstraps the first build.
- **The build output directory variable** (shown above set to
  `"$HOME/.local/share/tillr-build"`) — the directory where `just tillr-build` writes its `bin/`
  output and temporary build clone. Outside a `yolo` jail, point it at any writable directory you
  want to keep the binaries in, then prepend its `bin/` subdirectory to `PATH` so `tillr` and
  `tillr-drain` find their sibling binaries.

If you instead want `tillr` on `PATH` across repositories that carry no `tillr` source via the
`yolo` pack (`yolo-packs/tillr`), see
[Setting up tillr in a yolo jail from the pack](#5-setting-up-tillr-in-a-yolo-jail-from-the-pack)
and [Distribution: one source-built pack with eleven PATH binaries](./reference/tillr-distribution.md).

### Writing a backend policy

With no `--policy` flag, the loop looks for `config/tillr/backend-policy.json` in the repository
it operates. The tracked example at
[`config/tillr/backend-policy.json`](../config/tillr/backend-policy.json) uses placeholder model
names (`example-vendor/model-medium`, and so on) so no run starts on it as shipped.

Because model choices depend on your credentials, keep your policy **outside** any repository:

```console
$ mkdir -p "$HOME/.config/tillr"
$ cp "$TILLR_CLONE/config/tillr/backend-policy.json" "$HOME/.config/tillr/my-policy.json"
```

Edit `~/.config/tillr/my-policy.json` in two places:

1. **Replace every placeholder `model`** under `backends` with a model name from
   `pi --list-models` that your credentials can reach (for example `anthropic/claude-sonnet-4-5`
   or another provider/model pair).
2. **Use an absolute `adapter` path** pointing to `run-pi` inside your `tillr` clone, so the
   policy works when you run the loop against a repository of your own that has no `tools/`
   directory:

```json
{
  "default_backend": "pi-build",
  "seats": {
    "build": "pi-build",
    "design": "pi-build",
    "plan": "pi-build",
    "review": "pi-build",
    "verify": "pi-build"
  },
  "backends": {
    "pi-build": {
      "adapter": "/path/to/tillr/tools/tillr-run/backends/pi/run-pi",
      "model": "<provider/model-from-pi-list-models>",
      "effort": "medium"
    }
  }
}
```

Replace `/path/to/tillr` with your `$TILLR_CLONE` path and `<provider/model-from-pi-list-models>`
with a real model name.

### Creating a store in your own repository

Move into (or create) the Git repository you want `tillr` to work on. Three small pieces of setup
are needed before the loop can land a commit there:

1. **Git identity in global config.** `tillr-land` reads `user.name` and `user.email` from your
   global Git config and refuses to land if they are missing or overridden in repo-local config.
2. **Ignore `.tillr/`.** The store, lane worktrees, and run logs live under `.tillr/` and should
   stay untracked.
3. **Declare at least one gate check.** The loop's embedded gate reads declared JSON check files
   from `tools/gate/checks/` in your repository. Start with one minimal check (you can replace
   `["true"]` with your real test command later) and commit it.

```console
$ mkdir my-repo && cd my-repo
$ git init -b main
$ printf '.tillr/\n' > .gitignore
$ printf '# My Project\n' > README.md
$ mkdir -p tools/gate/checks
$ cat > tools/gate/checks/smoke.json <<'JSON'
{
  "id": "smoke",
  "label": "Smoke check",
  "command": ["true"],
  "scope": "tree",
  "tier": "fast",
  "budget_ms": 1000,
  "reads": ["."],
  "tools": []
}
JSON
$ git add .gitignore README.md tools/gate/checks/smoke.json
$ git commit -m "chore: initial commit"
```

Now initialize the SQLite store:

```console
$ tillr store init --store .tillr/state.db
```

`tillr store init` prints a `store.report` JSON object ending with `"integrity":"ok"`.

### Filing a first item with `tillr item request`

`tillr item request` reads the request's prose from standard input and writes one item to the
store. In a repository that does not carry `config/tillr/workflows/planned-build@v1.json`, pass
`--workflow single-worker@v1` to run the built-in single-worker workflow:

```console
$ printf 'Add a HELLO.txt file containing hello world.\n' | \
  tillr item request \
    --store .tillr/state.db \
    --title "Add HELLO.txt" \
    --workflow single-worker@v1
```

It prints an `item.request` JSON record naming the new `item_id` (for example
`item-request-4aec62684b`). Confirm it is queued:

```console
$ tillr item list --store .tillr/state.db
```

You should see your item with `"state":"in-queue"`.

### Starting the loop and reading what it prints

First check the repository's whole tillr setup in one read-only pass:

```console
$ tillr doctor --root "$PWD" --policy "$HOME/.config/tillr/my-policy.json"
```

`tillr doctor` reports, one line each, the store, settings file, seat policy and adapters,
brief template, default workflow, watch rules directory, and gate check declarations, and
exits `1` (naming the file or command that fixes any failing check) if anything is missing or
invalid.

Next validate the flags, store, and policy without dispatching anything:

```console
$ tillr drain loop \
  --root "$PWD" \
  --store "$PWD/.tillr/state.db" \
  --policy "$HOME/.config/tillr/my-policy.json" \
  --brief-template "$TILLR_CLONE/tools/tillr-run/backends/brief-template.md" \
  --gate "tillr-loop gate --gate-program true" \
  --land "tillr-loop land" \
  --verdict "tillr-loop verdict" \
  --replay "" \
  --lanes 1 \
  --check
```

`--check` prints the resolved step adapters and settings and exits `0`. Replace `true` in
`--gate-program true` with your repository's own test command once you have one.

Now drop `--check` and pass `--exit-when-drained` so the loop exits once your item lands
(without `--exit-when-drained`, `tillr drain loop` stays running as a daemon waiting for new
items):

```console
$ tillr drain loop \
  --root "$PWD" \
  --store "$PWD/.tillr/state.db" \
  --policy "$HOME/.config/tillr/my-policy.json" \
  --brief-template "$TILLR_CLONE/tools/tillr-run/backends/brief-template.md" \
  --gate "tillr-loop gate --gate-program true" \
  --land "tillr-loop land" \
  --verdict "tillr-loop verdict" \
  --replay "" \
  --lanes 1 \
  --exit-when-drained
```

Here is what the key output lines mean (values shortened for readability):

```text
loop: build <rev> (<timestamp>) green unknown digest <digest> exe .../tillr-drain
loop: ordinary lanes: 1 (from --lanes flag)
loop: policy in force <sha256>: default=pi-build; build=pi-build; ...
loop: tick 1 dispatch item-request-4aec62684b -> pi-build on lane-1
loop: tick gen-1 gate 0.0s over 0 run(s), longest 0.0s of a 330.0s per-run budget (...)
loop: landed item-request-4aec62684b (<candidate-sha>)
loop: tick gen-2 gate 0.3s over 1 run(s), longest 0.3s of a 330.0s per-run budget (...)
loop: stopped after 2 tick(s): queue-drained — ...
loop: dispatched 1, landed 1, rejected 0, infra failures 0
```

- **`loop: build ...`** — the exact `tillr-drain` build running (`green unknown` is expected for
  a `TILLR_BUILD_UNPROVED=HEAD` bootstrap build).
- **`loop: policy in force ...`** — the SHA-256 digest of your backend policy and which backend
  fills each seat.
- **`loop: tick 1 dispatch <item-id> -> <backend> on lane-1`** — the loop staged a worktree in
  `.tillr/lanes/lane-1/worktree` and started the worker.
- **`loop: landed <item-id> (<sha>)`** — the candidate passed the gate and was squashed onto your
  trunk branch.
- **`loop: stopped after 2 tick(s): queue-drained`** — nothing else is dispatchable and
  `--exit-when-drained` ended the loop with exit `0`.

*(Known gap: this end-to-end landing output was verified with a local stub `pi` in a temporary
directory rather than a live model provider, because this verification environment does not
invoke external model credentials.)*

### Where a landed item ends up

A landed item leaves three durable traces:

1. **One commit on your trunk branch.** Check `git log` and the working tree in your repository:

   ```console
   $ git log -n 2 --stat
   $ cat HELLO.txt
   hello world
   ```

   The commit is a single squashed commit on `main` (re-authored to your global Git identity),
   not a merge commit from the lane branch.

2. **Item state in the SQLite store.** Inspect the item or the whole board:

   ```console
   $ tillr item show --store .tillr/state.db --item item-request-4aec62684b
   $ tillr view status --store .tillr/state.db --text
   $ tillr drain why --store .tillr/state.db --policy "$HOME/.config/tillr/my-policy.json" --lanes 1
   ```

   `tillr item show` reports `"state":"landed"` with every criterion marked `"done"`, and
   `tillr drain why` reports `held []`.

3. **Run artifacts under `.tillr/loop/<run-id>/`.** The rendered `brief.md`, `run.record.json`,
   `gate.change.json`, and gate logs stay on disk under `.tillr/loop/` for inspection.

### Three things most likely to go wrong first

1. **Your `pi` binary lacks `parked-tool-calls`.** `run-pi` refuses to start and prints a message
   naming `mschulkind-oss/tillr-pi`. Build `tillr-pi` from its `tillr-required` branch, place it
   ahead of upstream `pi` on `PATH`, and confirm `pi --features` prints `["parked-tool-calls"]`.
2. **Placeholder model names left in the backend policy.** The worker exits `1`, and the loop
   closes the backend with `ALARM backend-closed (pi-build)`. Replace `example-vendor/...` in your
   policy file with a model from `pi --list-models`, then reopen the backend:

   ```console
   $ tillr drain probe-backend --store .tillr/state.db --backend pi-build -- true
   backend pi-build: successful probe recorded
   ```

3. **Missing `tools/gate/checks/` or default `change-gate.py` in your repository.** The loop
   reports `gate-no-verdict`. Commit at least one check JSON file under `tools/gate/checks/`, and
   pass `--gate-program` inside `--gate` (for example `--gate "tillr-loop gate --gate-program true"`
   or your repository's own test command).

---

## 7. Where to go next

- **Charters:** [Charters: how they work and how to write your own](./guide/charters.md) — how
  to define custom item states and finish items through a hook without Git landing, with a
  runnable worked example.
- **For agents:** [Agent operating guide](./guide/agent-guide.md) — exact `tillr` commands,
  exit codes, and the rules an agent operating `tillr` must follow.
- **Design and implementation status:** [tillr designs: what is built, what is designed, and what is still open](./tillr/design-status.md) — the index of every design and reference document and whether each piece is built today.
- **Reference documents:**
  - [Authority, not approval](./reference/authority-not-approval.md) — which decisions belong to the whale, which belong to the owner, and why a judge never judges its own work.
  - [Gate](./reference/gate.md) — how check declarations, fast and heavy tiers, remembered passes, and the trunk fixer work.
  - [Gate check rules](./reference/gate-check-rules.md) — the rules for red checks, recorded assertions, set-aside checks, and commit citations.
  - [Item graphs](./reference/tillr-item-graphs.md) — how tracked JSON workflows expand into graphs of agent and command nodes.
  - [Item states](./reference/tillr-item-states.md) — the item lifecycle states and frozen criteria.
  - [Queue persistence](./reference/tillr-queue-persistence.md) — how `tillr item sync` reads item blocks from Markdown documents and retires them on landing.
  - [Drain successor](./reference/tillr-drain-successor.md) — how the planner derives each item's next step from the store without clocks.
  - [Build-commit continuation](./reference/tillr-build-commit.md) — how the drain resumes a build run that exits with uncommitted changes.
  - [Run wall](./reference/run-wall-ruling.md) — why runs have no default wall-clock timeout and committed candidates are always judged.
  - [Restart](./reference/tillr-restart.md) — how `.tillr.toml`, in-place restarts, and post-landing roll and push work.
  - [Refused push](./reference/tillr-refused-push.md) — how the drain handles a rejected push and replays unpublished landings.
  - [Store contention](./reference/tillr-store-contention.md) — why write transactions avoid Git or file I/O and how contention is traced.
  - [Designer sessions](./reference/tillr-designer-sessions.md) — how review comments enter design sessions and receive replies.
  - [Claude backend](./reference/tillr-claude-backend.md) — how the `run-claude` adapter runs headless sessions and handles quota windows.
  - [Backend adapters and the Muse backend](./reference/tillr-muse-backend.md) — the adapter contract, brief staging, write-boundary snapshots, and failover.
  - [Distribution](./reference/tillr-distribution.md) — how the `yolo` pack delivers the `tillr` binaries and documentation snapshot.
  - [Observatory truthfulness and primitives](./reference/observatory-design.md) — how the read-only observatory server maintains count parity with the CLI.
  - [Observatory pages and read faces](./reference/observatory-redesign.md) — the observatory web routes, waiting classes, and run waterfalls.
  - [Observatory actions](./reference/observatory-actions.md) — how queue reordering and idea intake work over HTTP.
