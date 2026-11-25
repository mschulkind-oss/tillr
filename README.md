# tillr

tillr is a work-queue toolchain: a family of command-line programs (`tillr`, `tillr-run`,
`tillr-item`, `tillr-gate`, `tillr-land`, `tillr-loop`, `tillr-roll`, `tillr-drain`, `tillr-watch` and
others) over one SQLite store. Work is tracked as items; a drain loop plans, runs an agent
in a lane (a leased git worktree), gates the result and lands one commit per item on the trunk.
New to tillr? Start with the [user guide](docs/guide.md). For design and implementation status,
see [docs/tillr/design-status.md](docs/tillr/design-status.md).

## Layout

- `tools/tillr*` — the Go modules (one per program or library); `tools/tillr` holds shared packages.
- `tools/gate` — the declared checks `tillr-gate` runs (`tools/gate/checks/*.json`); `just done` is the full local gate.
- `tools/observatory`, `tools/tillr-view` — the web view (`just observatory`).
- `config/tillr`, [`.tillr.toml`](.tillr.toml) — policy and per-repository inputs.
- `docs/design`, `docs/reference`, `docs/tillr` — designs, references, and [the roadmap](./docs/tillr/roadmap.md).
- `yolo-packs/tillr` — the yolo pack that ships the binaries and a docs snapshot.
- `.agents/skills`, `hooks`, `tests` — agent skills, git hooks, repository-level tests.

## Build and test

```console
$ just --list
$ just tillr-build        # needs refs/tillr/green; see Status below
$ (cd tools/tillr-store && go build ./... && go test ./...)
$ just go-test-shards tools/tillr-drain
$ just done               # the full local gate
```

## Status

Under active development. `just tillr-build` builds from `refs/tillr/green`, the newest
commit the full suite has proved; before that ref exists, bootstrap with
`TILLR_BUILD_UNPROVED=HEAD just tillr-build`.

## Running the loop

A *backend policy* is the JSON file that says which adapter program and model fill each
seat (the role a run plays: build, design, verify and so on). With no `--policy` the loop reads
[`config/tillr/backend-policy.json`](config/tillr/backend-policy.json) in the repository it
operates. The one here is an example with placeholder model names, so no run starts on it
as shipped. Which model fills which seat is a personal choice, so keep your own policy
outside the repository: copy the example, put in models your credentials reach, and name
your copy with `--policy`.

```console
$ export PATH="$YOLO_DURABLE_DIR/bin:$PATH"   # where `just tillr-build` puts the programs
$ tillr store init --store .tillr/state.db
$ just tillr-keep -- --policy ~/.config/tillr/my-policy.json --lanes 1
```

`tillr drain probe-backend` checks one backend before the loop depends on it. The pi adapter
also requires a pi that lists the `parked-tool-calls` feature; see
[the pi backend](tools/tillr-run/backends/pi/README.md#required-pi-features).
