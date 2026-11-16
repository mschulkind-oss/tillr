# tillr-run — one request to a terminal run record

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

`tillr-run` executes one run request to a terminal run record under a caller-named lane
root, and reads the lane and run state back. `exec` is the run itself; `request` builds the
request; `lanes`, `status` and `release` inspect and clean up what a tick left behind.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## State invariants

- **One candidate, one lane, one record of what the run cost.** The record is written
  whatever the run's outcome, because a run with no record is a run nobody can judge.
- **A lane released is not a candidate retried.** Release reclaims the directory; the
  candidate's outcome is the store's.
- **Spend is read from the backend's own report**, not reconstructed from a transcript, so
  the number answers to the thing that spent it.
- **The backends are the boundary.** A pi, muse or other adapter's dialect never leaks into
  the record's shape; the adapters produce one report and this module owns the mapping.
- **A backend row's `harness_args` reach the harness unchanged.** A policy row may list
  arguments for the harness itself (tools, servers, standing text); the request's argv ends
  with `--` and then those arguments, in order, and a row with none gets no `--`. Only the
  claude adapter takes them; the others refuse the `--` before anything runs
  ([the claude backend design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-claude-backend.md#3-controlling-what-a-seat-may-use)).

## Standalone invocation

```sh
tillr-run exec
tillr-run lanes
```

`exec` is what a caller drives when it runs its own candidate rather than a whole drain;
`lanes` is the read-only answer to what a tick left behind.

`exec --resume` continues a run instead of starting one. The run id is unchanged, so the
`runs` row is one row and the resume is a `run.resume` event on it; the harness session
staging pinned in the lane (`session-id`, which every adapter passes to its harness) is
reused, so the provider's prefix cache is warm; and the run's log is appended to rather than
truncated, because the killed segment's transcript is the evidence the resume continues.

## Usage attribution

Each spend extraction writes into a fresh directory inside the lane. A failed
extractor that writes no new report therefore records unknown tokens and cost, never
the preceding attempt's report. This applies to an immediately interrupted retry and
to extraction after a resumed segment; resume still parses its appended run log.
The prior report remains available rather than being deleted to make a retry safe.

Measured 2026-11-16 by the
[interrupted-retry regression](./run/spend_retry_test.go): a first attempt reported
1,500 tokens and $0.03. Before fresh extraction destinations, a retry interrupted
before output inherited both values when its extractor failed to write a report.
The regression requires the retry's returned record and stored row to have unknown
usage while preserving the first attempt's measured usage. The real Pi extractor
was also probed with an empty stream and exit code -1: it returned its documented
incomplete-report exit 4 with `tokens: unknown` and `cost usd: unknown`. The leak
was the shared report destination, not an empty stream reporting prior model usage.

## What it refuses

- **A request whose lane root is not there** — refused before anything runs.
- **A policy naming no backend** — refused, naming the backends the policy does have.
- **A release of a lane a live run holds** — refused, so a release cannot pull the directory
  out from under a process.
- **A resume with no session to continue** — refused before the lane is claimed: a cold start
  wearing a continuation's name would pay for the whole transcript again.

## Test

`go test ./...` in this directory builds the binary and drives the faces against scratch
lane roots and stub backends; the run package's own suite pins the schema and the mapping.
