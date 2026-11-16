# tillr-gate — the verdict about a candidate

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

`tillr-gate` derives a change record from a candidate, plans it into the checks a gate would
run, runs them, and measures the landing tier in shadow. `change`, `plan` and `shadow` need
no store and no other utility's binary, which is what lets an external reviewer adopt them
on their own.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## State invariants

- **The gate's exit is the verdict.** Pass, fail, or an answer that says it could not judge:
  a gate that could not run is never a fail, because a broken judge and a red tree are
  different facts.
- **The plan is the authority for what ran.** A check that was not in the plan did not run,
  and the record names the difference rather than leaving it to a log.
- **`shadow` measures and never lands.** A shadow red is a measurement, not a verdict about
  the tree, and the face says so in the record it writes.
- **A refused judgment is a distinct answer.** "The harness could not judge" is not "the
  code is bad", and collapsing the two is how a red gate stops being believed.

## Standalone invocation

```sh
tillr-gate run
tillr-gate shadow
```

`run` is the verdict a landing needs; `shadow` is the measurement an operator takes before
changing the tier. Both answer in a record on stdout when no output path is named.

## Declaration directories

`plan --registry` accepts either a generated registry file or a directory whose direct
JSON files each declare one check. Programs and tests can live beside those files; only
filenames ending in `.json` are declarations. Adding a check adds its files without
editing a shared list. The complete field contract and defaults are in
[the check-file design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-rework.md#64-a-check-is-one-file).

```sh
tillr-gate plan --registry tools/gate/checks/ --input gate.change.json
```

The loader validates every declaration before emitting a plan. A missing scope, tier,
or tier-specific cost names the file and field and answers “could not judge” (exit 4);
it never emits a partial plan. The directory's filenames and bytes determine its digest,
so the same directory content copied elsewhere produces the same plan identity.

A change check receives the base and head as its final two command arguments. Such a
check needs a change record carrying `head_sha`, produced by `tillr-gate change`; an
older record without that field is refused rather than substituting a tree hash for a
commit. A tree check is selected by its declared read paths (directory paths end in `/`),
with omitted or empty `reads` selecting the whole tree. A heavy check stopped at its
declared timeout produces no judgment (exit 4), not a failed check.

[The budget declaration](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/checks/gate-budget.json) runs arithmetic admission:
fast budgets may total at most 9,000 milliseconds. Its
[fixture test declaration](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/checks/gate-budget-test.json) reads only the check
directory. These declarations carry fast/heavy policy into the plan; per-fast-check
budget deadlines and remembered tree results are separate work, not implemented by this
loader.

## Caller-owned change checks

`checks` runs only the change declarations in a directory. A change check judges the
caller’s commit range, not the tree alone; the
[design defines that distinction](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-rework.md#61-the-caller-names-the-base).
The base and head are required, and the base must be an ancestor of the head. Missing or
unreadable revisions answer “could not judge” (exit 4) before any check runs. Each program
receives both commits and reads no remembered baseline. Its exit 4 is also no judgment,
not a rejection; an unavailable judgment outranks a rejection elsewhere in the same run.

```sh
tillr-gate checks --base <lane-base> --head <candidate-head> --logs <run>/change-logs
```

The repository’s change declarations cover commit identity, the frozen seed, commit
citations, rendering changed Markdown, and refusing newly added TypeScript files over
3,000 lines. Existing TypeScript size warnings remain in the older tree program. The
[loop’s default judge](https://github.com/mschulkind-oss/tillr/blob/main/gate/change-gate.py) runs these declarations first, then the
remaining tree program; it stops if any change check fails or cannot judge.

## What it refuses

- **A plan with no registry** — a gate with no registry would pass everything.
- **A run with no input record** — there is no verdict against a plan that is not there.
- **A change whose candidate and worktree disagree** — the record names the mismatch rather
  than gating the wrong tree.

## Test

`go test ./...` in this directory builds the binary and drives the faces against scratch
repositories, fake gate programs and the real check registry.
