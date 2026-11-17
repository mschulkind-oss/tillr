# The tillr pack

This pack delivers tillr, the owner's work-queue toolset, to a jail in any workspace that
selects it. It delivers three things.

| What | Where it lands in the jail | Produced by |
| :--- | :--- | :--- |
| The Go source of every `tools/tillr*` module, and the eight `tillr*` launchers on PATH that build from it | `~/.local/share/tillr/<module>/`, `~/.local/bin/` | `just pack-sync`, checked by [`tests/pack-sync.test.py`](../../tests/pack-sync.test.py) |
| A dated snapshot of tillr's design documentation: the [status index](../../docs/tillr/design-status.md) and every document it marks `shipped` | `~/.local/share/tillr/docs/`, plus `~/.local/share/tillr/tillr-loop/README.md` | `just pack-docs` ([`tools/pack-docs.py`](../../tools/pack-docs.py)), checked by the same suite |
| Two skills: `tillr-operating` (running the loop) and `tillr-designs` (reading the designs from another repository, and sending tillr a request) | each agent's skill directory | [`skills/`](./skills/) |

A delivered path mirrors the repository path, with a leading `tools/` dropped. So
`docs/design/tillr-item-graphs.md` lands at
`~/.local/share/tillr/docs/design/tillr-item-graphs.md`.

## The design documentation is a snapshot

The module source is held to `tools/` on every gate run. The documentation is not. The drain
edits design documents on every landing, and a lane's fence does not include this pack's
`docs/` tree, so a freshness requirement in the gate would fail every lane after any such
landing. Instead:

- [`files/local/share/tillr/docs/snapshot.json`](./files/local/share/tillr/docs/snapshot.json) records the commit the copies were made from,
  and the blob of each source.
- The gated suite proves every copy is exactly the transform of the blob its snapshot names.
  A hand edit, a missing or stray file, or a live item directive fails it.
- `python3 tests/pack-sync.test.py --fresh` says whether HEAD has moved past the snapshot.
  `just pack-docs` runs it after a refresh.

The transform adds a banner naming the source and commit, and re-resolves links. A delivered
target gets a relative link, and anything else becomes a link to the repository on GitHub.
It also makes `<!-- tillr: ... -->` item directives inert. The planning index skips the copies
through the exclusion in [`.vantage.toml`](../../.vantage.toml).

## Refreshing the snapshot

```sh
just pack-docs
git add yolo-packs/tillr/files/local/share/tillr
git commit -m "chore(tillr-pack): refresh the design documentation snapshot" -- yolo-packs/tillr/files/local/share/tillr
```

`just pack-docs` copies from HEAD, so commit the source edits first.

## Adding a document

The index is the only list of what ships. To add a document, add its row to a table in
[`docs/tillr/design-status.md`](../../docs/tillr/design-status.md), with a first cell that
links the document and a last cell that reads exactly `shipped`. Commit, run
`just pack-docs`, and commit the result. A row whose last cell reads `not shipped` is listed
for the reader and left out. The sync refuses a row that says neither.

## Deploying a change

The pack deploys through yolo, not rcm. On the host, run `yolo host apply --assert`, then
relaunch each running jail. Run `yolo pack lint yolo-packs/tillr` after every edit to the
pack.
