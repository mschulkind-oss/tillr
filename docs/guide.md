---
title: "User guide: from a fresh clone to your first landed item"
status: accepted
stage: CURRENT
tags: [tillr, guide, onboarding]
summary: "Step-by-step guide taking a newcomer from a fresh clone of tillr to one item landed by the drain loop in a repository of their own."
---

# User guide: from a fresh clone to your first landed item

This guide takes you from a fresh clone of `tillr` to one commit landed by the drain loop in a
Git repository of your own.

> **Known gaps.**
> 1. **No `yolo` jail installer for `tillr-pi` in this tree.** Building `mschulkind-oss/tillr-pi`
>    by hand from its `tillr-required` branch is verified below. This repository ships no `yolo`
>    pack or jail configuration for installing `tillr-pi` (`ls yolo-packs/` lists only `tillr`,
>    and `rg -n 'tillr-required' .` returns nothing).
> 2. **Live model credentials not exercised here.** Every build, store, filing, loop, gate, and
>    landing command below was run in a scratch clone, and the freshly built `tillr-pi` binary was
>    verified with `pi --features` and against the placeholder policy. The final end-to-end landing
>    step used a local stub `pi` instead of a live model provider because this verification
>    environment does not invoke external model credentials.

---

## 1. What tillr is and its vocabulary

`tillr` is a work-queue toolchain: a set of command-line programs (`tillr`, `tillr-store`,
`tillr-item`, `tillr-drain`, `tillr-run`, `tillr-gate`, `tillr-land`, `tillr-view`) backed by one
SQLite store. You file work into the store, start the drain loop, and the loop dispatches coding
agents in isolated Git worktrees, checks each result, and lands passing changes onto your trunk
branch.

Seven terms carry the workflow (see [the vocabulary design](./design/tillr-vocabulary.md) for the
full glossary):

- **Whale** — the coordinating AI session you talk to while tillr runs, which operates tillr on
  your behalf (filing items, watching the drain, clearing faults, and recording delegated
  decisions under `whale`). See [Working with the whale 🐋](#working-with-the-whale-) below.
- **Item** — one unit of work tracked in the SQLite store, with a title, request words or
  criteria, and a lifecycle state (`in-queue`, `landed`, `blocked`, and so on).
- **Seat** — the role a run plays (`build`, `design`, `plan`, `review`, `verify`, and others),
  independent of which agent or model fills it.
- **Lane** — a leased Git worktree (under `.tillr/lanes/<lane>/worktree` by default) where one
  agent run works in isolation from your main checkout and from other lanes.
- **Gate** — the automated check suite (`tillr-gate`) that judges a lane's committed candidate
  against declared checks before anything touches the trunk.
- **Landing** — the atomic squash of a gated candidate (`tillr-land`) onto your repository's
  trunk branch as a single commit, re-authored to your configured Git identity.
- **Backend policy** — a JSON file mapping each seat to a backend entry (an adapter program, a
  model name, and an effort level).

### Working with the whale 🐋

When you run `tillr` day to day, the helper you talk to is **the whale** — the coordinating AI
session that operates `tillr` on your behalf (see
[the vocabulary design](./design/tillr-vocabulary.md#56-the-machinery)). Instead of typing every
`tillr` command yourself, you tell the whale what you want done: it files items, starts and
watches the drain, clears faults, reports status, and records decisions under its own name
(`whale`, accepted beside `owner` in the store's `decided_by` field).

- **Whale vs. lane worker.** A lane worker is a short-lived agent run that builds, reviews, or
  verifies **one item in one lane**. The whale is the outer session that steers the **whole
  drain** and never is a lane.
- **What the whale decides vs. what stays with you.** Under
  [Authority, not approval](./reference/authority-not-approval.md#terms), the whale handles
  reversible, bounded actions covered by settled policy (filing items, restarting the loop,
  reopening a fixed backend, recording delegated decisions with evidence). Decisions that risk
  irreversible loss, spending or external commitments, external publication, or changing policy
  and accepting risk stay with the owner — the whale surfaces those as a question with real
  options rather than guessing.

---

## 2. What must be installed

Check that these tools are on your `PATH` before you start:

```console
$ go version        # Go toolchain (builds the tillr programs)
$ just --version    # command runner used by the build recipe
$ git --version     # Git (worktrees, three-way merge landing)
$ python3 --version # Python 3 (runs the loop's embedded gate/land/verdict adapter)
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

---

## 3. Getting a compatible `pi` from `mschulkind-oss/tillr-pi`

[mschulkind-oss/tillr-pi](https://github.com/mschulkind-oss/tillr-pi) is a fork of upstream `pi`
that carries the `parked-tool-calls` support `tillr` requires on its `tillr-required` branch.

### Building `tillr-pi` by hand

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

### Through a `yolo` jail

This is a **known gap**: this repository does not ship a `yolo` pack or jail configuration that
installs `mschulkind-oss/tillr-pi` (`ls yolo-packs/` contains only `tillr`, and
`rg -n 'tillr-required' .` returns nothing). If you run inside a `yolo` jail whose host already
provides a `pi` binary, check `pi --features` first; if `parked-tool-calls` is absent, build
`tillr-pi` by hand as shown above.

---

## 4. Building the tillr programs

Clone `tillr` and build the eight programs (`tillr`, `tillr-drain`, `tillr-gate`, `tillr-item`,
`tillr-land`, `tillr-run`, `tillr-store`, `tillr-view`):

```console
$ git clone https://github.com/mschulkind-oss/tillr.git
$ cd tillr
$ export TILLR_CLONE="$PWD"
$ YOLO_DURABLE_DIR="$HOME/.local/share/tillr-build" TILLR_BUILD_UNPROVED=HEAD just tillr-build
$ export PATH="$HOME/.local/share/tillr-build/bin:$PATH"
$ tillr --help
tillr: store, item, land, drain, run, gate, view, watch, notify
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
[Distribution: one source-built pack with nine PATH binaries](./reference/tillr-distribution.md).

---

## 5. Writing a backend policy

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

---

## 6. Creating a store in your own repository

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

---

## 7. Filing a first item with `tillr item request`

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

---

## 8. Starting the loop and reading what it prints

First validate the flags, store, and policy without dispatching anything:

```console
$ tillr drain loop \
  --root "$PWD" \
  --store "$PWD/.tillr/state.db" \
  --policy "$HOME/.config/tillr/my-policy.json" \
  --brief-template "$TILLR_CLONE/tools/tillr-run/backends/brief-template.md" \
  --gate "builtin:tillr-loop gate --gate-program true" \
  --land "builtin:tillr-loop land" \
  --verdict "builtin:tillr-loop verdict" \
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
  --gate "builtin:tillr-loop gate --gate-program true" \
  --land "builtin:tillr-loop land" \
  --verdict "builtin:tillr-loop verdict" \
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
loop: tick gen-2 gate 0.5s over 1 run(s), longest 0.5s of a 330.0s per-run budget (...)
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

*(Known gap: this end-to-end landing output was verified with a local stub `pi` in a scratch
clone rather than a live model provider, because this verification environment does not invoke
external model credentials.)*

---

## 9. Where a landed item ends up

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
   $ tillr view status --store .tillr/state.db
   $ tillr drain why --store .tillr/state.db --policy "$HOME/.config/tillr/my-policy.json" --lanes 1
   ```

   `tillr item show` reports `"state":"landed"` with every criterion marked `"done"`, and
   `tillr drain why` reports `held []`.

3. **Run artifacts under `.tillr/loop/<run-id>/`.** The rendered `brief.md`, `run.record.json`,
   `gate.change.json`, and gate logs stay on disk under `.tillr/loop/` for inspection.

---

## 10. Three things most likely to go wrong first

### 1. Your `pi` binary lacks `parked-tool-calls`

**What it looks like:**

```text
run-pi: refused: the pi at /usr/bin/pi lacks the feature 'parked-tool-calls', which this
adapter requires: `pi --features` lists 'nothing'. Upstream pi does not have it yet; the
fork mschulkind-oss/tillr-pi does. Install a pi whose `pi --features` output names
'parked-tool-calls'.
```

**How to fix it:** Build `mschulkind-oss/tillr-pi` from its `tillr-required` branch
([§3](#3-getting-a-compatible-pi-from-mschulkind-osstillr-pi)), place that `pi` ahead of
upstream `pi` on `PATH`, and confirm `pi --features` prints `["parked-tool-calls"]`.

### 2. Placeholder model names left in the backend policy

**What it looks like:** The loop dispatches the item, the worker exits `1`, and the loop closes
the backend with an alarm:

```text
loop: item-request-... failed in the infrastructure class (1 consecutive)
loop: alarm started: ALARM backend-closed (pi-build), ... Error: Model "example-vendor/model-medium" not found. Use --list-models to see available models.
... operator: fix the reported cause, then tillr drain probe-backend --store <store> --backend pi-build -- <smoke-check>
```

**How to fix it:** Replace `example-vendor/...` in your policy file with a model from
`pi --list-models`, then reopen the backend in the store before restarting the loop:

```console
$ tillr drain probe-backend --store .tillr/state.db --backend pi-build -- true
backend pi-build: successful probe recorded
```

### 3. Missing `tools/gate/checks/` or default `change-gate.py` in your repository

**What it looks like:** After the worker commits, the loop repeats on every tick without landing:

```text
loop: gate reached no verdict on item-request-... (run run-..., lane lane-1): gate-no-verdict; the next pass gates the same candidate again
```

Inspecting `.tillr/loop/<run-id>/gate.stderr` (or the triage log it names) shows one of two
errors:

```text
tillr-gate plan: reading the check registry: open .../tools/gate/checks: no such file or directory
```

or:

```text
/bin/python3: can't open file '.../tools/gate/change-gate.py': [Errno 2] No such file or directory
```

**How to fix it:** Commit at least one check JSON file under `tools/gate/checks/` in your
repository ([§6](#6-creating-a-store-in-your-own-repository)), and pass `--gate-program` inside
`--gate` (for example `--gate "builtin:tillr-loop gate --gate-program true"` or your repository's
own test command) so the gate does not look for `tillr`'s own `tools/gate/change-gate.py`.
