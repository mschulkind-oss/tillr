# tillr-cli — the `tillr` dispatcher

> [!WARNING]
> **Known gaps.** Two things about `tillr` are not what a first reading assumes:
> 1. **The observatory front end is a separate build** — a `tillr-view` built by a plain
>    `go build` answers 404 for the UI. The front end under `tools/tillr-view/web` is a
>    Node build, embedded only under the `observatory` build tag (`just observatory` does
>    both) or served from a directory named with `--web-dist`.
> 2. **A `tillr` installed from the pack is not this working tree** — the pack under
>    `yolo-packs/tillr` builds from its own mirrored copy of the source, which
>    `just pack-sync` regenerates. To run an edit under `tools/tillr*`, build the working
>    tree with `just tillr-build`.

## What this module owns

`tillr` is the control surface over the eight `tillr-<noun>` binaries: `store`, `item`,
`land`, `drain`, `run`, `gate`, `view` and `watch`. It resolves `tillr <noun> ...` to
`tillr-<noun> ...` on `PATH` and replaces itself with that binary, so argv, stdio and the
exit code reach the caller untouched.

For those nouns it holds no behavior of its own. It never parses, validates, defaults or
rewrites a noun's arguments, and it composes nothing: the binaries are the product and this
is the spelling that lets a caller type `tillr gate plan` instead of `tillr-gate plan`.

The ninth noun, `notify`, is built in and has no binary of its own. Its one face, `serve`,
runs the local Unix-socket relay that passes wake hints between programs sharing a store;
it requires `--store` and exits once no connection has remained for its idle period.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md).

## State invariants

- **The handover is an `exec`, not a subprocess.** There is no wrapper left to observe or
  rewrite the child's exit status, signals or stdin.
- **The noun set is closed.** A noun outside the nine is refused here rather than
  forwarded anywhere, so the wrapper cannot become a second interface to anything else.
- **Only `--help` and `notify` are the wrapper's own answers.** Everything after any
  other noun belongs to that noun.

## Standalone invocation

Apart from `notify`, nothing built from this module does work on its own, so an external
caller uses it only as spelling. These two lines are the same program:

```sh
tillr gate plan
tillr-gate plan
```

## What it refuses

- **No noun** — names the nine.
- **A noun outside the nine** — names the nine and the refused word.
- **A noun whose binary is not on `PATH`** — the invocation was well formed and the artifact
  is missing, which is not a usage error.

## Test

`go test ./...` in this directory builds the binary and drives it against stub nouns on
`PATH`: nothing after the noun is read, an unknown noun is refused without starting
anything, and the handover preserves argv, stdin and the exit code.
