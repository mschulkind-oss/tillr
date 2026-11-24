# tillr

tillr is a work-queue toolchain: a family of command-line programs (`tillr`, `tillr-run`,
`tillr-item`, `tillr-gate`, `tillr-land`, `tillr-loop`, `tillr-roll`, `tillr-drain`, `tillr-watch` and
others) over one SQLite store. Work is tracked as items; a drain loop plans, runs an agent
in a lane (a leased git worktree), gates the result and lands one commit per item on the trunk.
Start with [docs/tillr/design-status.md](docs/tillr/design-status.md).

## Layout

- `tools/tillr*` — the Go modules (one per program or library); `tools/tillr` holds shared packages.
- `tools/gate`, `tools/fork-check.py` — the local gate behind `just done`.
- `tools/observatory`, `tools/tillr-view` — the web view (`just observatory`).
- `config/tillr`, `config/board`, `.tillr.toml`, `.workboard.toml` — policy and per-repository inputs.
- `docs/design`, `docs/reference`, `docs/tillr`, `docs/roadmap.md` — designs, references, roadmap.
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

Under active development. The local gate (`just done`) still carries checks for delivering
patch series to forked tools (`pi-forks`, `.forks`, `yolo-packs/matt`), which this repository
does not use; until they are removed, treat those references as a to-do list, not as working
behavior. `just tillr-build` builds from `refs/tillr/green`; before that ref exists, bootstrap
with `TILLR_BUILD_UNPROVED=HEAD just tillr-build`.
