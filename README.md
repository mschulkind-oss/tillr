# tillr

`tillr` is a work-queue toolchain for coding agents: a family of command-line programs (`tillr`, `tillr-drain`, `tillr-gate`, `tillr-item`, `tillr-land`, `tillr-run`, `tillr-store`, `tillr-view`, `tillr-watch`) backed by one SQLite **store** (the database that holds all queued tasks, their states, and an append-only event log). You put tasks—called **items**—into the queue, and a background **drain loop** runs an agent for each item in its own isolated Git worktree (a **lane**), checks the result against your repository's tests (the **gate**), and squashes passing work onto your trunk branch as one clean commit (**landing**). It is built for developers and teams who want coding agents to work through a backlog in parallel without colliding with each other or leaving broken commits on the main branch.

## Why use tillr

Running one coding agent by hand in a working directory works until you run two at once or step away. When two agents share one checkout, they overwrite each other's files. When an agent commits straight to the main branch, a broken change blocks everyone else until you revert it by hand. When an agent stops halfway through, you have to read terminal scrollback to find out why.

Today, the `tillr` programs handle that coordination for you:

- **Isolated worktrees:** each run works inside its own leased Git worktree, separate from your main checkout and from every other active run.
- **Automated gating with base replay:** the loop runs your repository's declared checks against each candidate commit before landing, and replays any failing check on the base commit so pre-existing trunk failures do not blame the agent.
- **Atomic squash landing and trunk proof:** passing candidates merge and squash onto the trunk branch under a file lock, re-authored to your configured Git identity, while a rolling **trunk proof** checks the trunk tip after landings and files a fixer item if a heavy check breaks.
- **One event log and fault isolation:** every task, criterion verdict, and landing sits in one SQLite store, and the loop separates model-provider outages from code failures so a down backend does not corrupt your queue.

## Charters

A **charter** (called a **definition** in the CLI and Go code) is the rulebook that adapts `tillr`'s queue and workflow engine to a specific use. It declares the states an item can hold, which state means ready for dispatch, which states count as finished, which transitions are valid, and how an item finishes. By default, a new store uses the built-in `git-landing` charter to land commits on a Git trunk; you can also write a custom charter that finishes items through a hook program without touching Git.

## Where to start

- **New users:** read the [user guide](docs/guide.md) for why `tillr` works this way, how Git landing operates, and a step-by-step walkthrough from a fresh clone to your first landed item.
- **Custom workflows:** read the [charter guide](docs/guide/charters.md) for how the default `git-landing` charter works and how to build and run a custom charter with a worked example.
- **AI agents:** read the [agent operating guide](docs/guide/agent-guide.md) for exact commands, exit codes, and hard rules for operating `tillr` in a repository.
- **Implementation status:** see [design status](docs/tillr/design-status.md).

## The whale 🐋

While `tillr` runs, you talk to **the whale**—the coordinating AI session that operates `tillr` on your behalf. The whale files items, starts and watches the drain loop, clears faults, and records delegated decisions under `whale` beside `owner` in the store. A lane worker builds or checks one item inside one lane, whereas the whale steers the whole drain and never runs inside a lane. Reversible, delegated actions stay with the whale, while irreversible loss, spending, external publication, and policy choices stay with the owner (see [Authority, not approval](docs/reference/authority-not-approval.md#terms) and [the vocabulary design](docs/design/tillr-vocabulary.md#56-the-machinery)).

## Repository layout

- `tools/tillr*` — the Go modules (one per program or library); `tools/tillr` holds shared packages.
- `tools/gate` — the declared checks `tillr-gate` runs (`tools/gate/checks/*.json`); `just done` runs the full local gate.
- `tools/observatory`, `tools/tillr-view` — the web view (`just observatory`).
- `config/tillr`, [`.tillr.toml`](.tillr.toml) — policy and per-repository configuration.
- `docs/design`, `docs/reference`, `docs/tillr` — designs, references, and [the roadmap](docs/tillr/roadmap.md).
- `yolo-packs/tillr` — the yolo pack that ships the binaries and a docs snapshot.
- `.agents/skills`, `hooks`, `tests` — agent skills, git hooks, and repository-level tests.

## Build and test

```console
$ just --list
$ just tillr-build        # builds from refs/tillr/green; see Status below
$ just go-test-shards tools/tillr-drain
$ just done               # the full local gate (run outside lanes)
```

## Status

`tillr` is under active development. `just tillr-build` builds from `refs/tillr/green`, the newest commit that the full suite has proved; before that ref exists in a fresh clone, bootstrap with `TILLR_BUILD_UNPROVED=HEAD just tillr-build`.

## Running the loop

A **backend policy** is a JSON file that maps each **seat** (the role a run plays, such as `build`, `design`, `plan`, `review`, or `verify`) to an adapter program and model. Without `--policy`, the loop reads [`config/tillr/backend-policy.json`](config/tillr/backend-policy.json), which uses placeholder model names so no run starts on it as shipped. Copy it outside the repository, insert models your credentials can reach, and pass your copy with `--policy`:

```console
$ export PATH="$YOLO_DURABLE_DIR/bin:$PATH"   # where `just tillr-build` writes binaries
$ tillr store init --store .tillr/state.db
$ just tillr-keep -- --policy ~/.config/tillr/my-policy.json --lanes 1
```

Use `tillr drain probe-backend` to verify a backend before the loop depends on it. The `pi` adapter also requires a `pi` binary that lists `parked-tool-calls` in `pi --features`; see [the pi backend README](tools/tillr-run/backends/pi/README.md#required-pi-features) and the [user guide](docs/guide.md) for setup details.
