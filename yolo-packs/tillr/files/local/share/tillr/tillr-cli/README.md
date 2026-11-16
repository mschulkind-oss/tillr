# tillr-cli — the `tillr` dispatcher

> [!WARNING]
> **Known Gaps (verified 2026-11-14 at `04957a8a83`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **The AutoQA handoff skill is not shipped** — `yolo-packs/tillr/skills/` holds
>    `tillr-operating` only, so the design's `tillr-autoqa` handoff has no skill to dispatch.
> 2. **The observatory front end is not built** — `tillr view serve` answers 404 for the UI
>    until the front end under `tools/tillr-view/web` is built and embedded, which is a
>    separate Node step and not part of a Go build.
> 3. **The `tillr` on `PATH` is the pack's rendered tree, not this working tree** — an edit
>    under `tools/tillr*` reaches the launcher only after the pack is mirrored and rendered,
>    so a session that must run its own change builds the working tree first.
>
> **Do not wire `tillr` into another repository's gate while these stand.** The toolset
> ships early so it can be reviewed, not so it can be depended on.

## What this module owns

`tillr` is the control surface over the seven `tillr-<noun>` binaries. It resolves
`tillr <noun> ...` to `tillr-<noun> ...` on `PATH` and replaces itself with that binary, so
argv, stdio and the exit code reach the caller untouched.

It holds no behavior of its own. It never parses, validates, defaults or rewrites a noun's
arguments, and it composes nothing: the binaries are the product and this is the spelling
that lets a caller type `tillr gate plan` instead of `tillr-gate plan`.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## State invariants

- **The handover is an `exec`, not a subprocess.** There is no wrapper left to observe or
  rewrite the child's exit status, signals or stdin.
- **The noun set is closed.** A noun outside the seven is refused here rather than
  forwarded anywhere, so the wrapper cannot become a second interface to anything else.
- **Only `--help` is the wrapper's own answer.** Everything after a noun belongs to the
  noun.

## Standalone invocation

Nothing built from this module does work on its own, so an external caller uses it only as
spelling. These two lines are the same program:

```sh
tillr gate plan
tillr-gate plan
```

## What it refuses

- **No noun** — names the seven.
- **A noun outside the seven** — names the seven and the refused word.
- **A noun whose binary is not on `PATH`** — the invocation was well formed and the artifact
  is missing, which is not a usage error.

## Test

`go test ./...` in this directory builds the binary and drives it against stub nouns on
`PATH`: nothing after the noun is read, an unknown noun is refused without starting
anything, and the handover preserves argv, stdin and the exit code.
