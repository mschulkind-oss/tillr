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
with omitted or empty `reads` selecting the whole tree. A heavy `timeout_ms` is a
measured target, not a kill deadline. Generated declarations follow the same rule.

[The budget declaration](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/checks/gate-budget.json) runs arithmetic admission:
fast budgets may total at most 9,000 milliseconds. Its
[fixture test declaration](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/checks/gate-budget-test.json) reads only the check
directory. The fast total is 8,950 milliseconds, including the admission check.
Fast declarations may not name `timeout_ms`; `budget_ms` is their only target.

Checks keep their own verdict past their target. `budget_ms`, duration and
`over_budget_ms` record each overrun in the verdict, log and summary. The cap is
only the admission sum, never a deadline. `--tree-overrun keep` remains compatible;
`carry` is refused because stopping at a budget would discard a working judgment.

The watchdog (the progress observer defined in
[design Section 6.5](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-rework.md#65-ten-seconds-is-a-failing-test))
samples every 1,000 milliseconds. A byte of log growth or an increase in process-group
CPU time, including live and reaped children, resets its silent-sample count. Sixty
consecutive samples with neither ends the group. The result is `stalled`, exit 6,
not pass, fail, error or over-budget. The verdict entry, check log and summary carry
the last output line, group CPU time, silent samples and duration. Unreadable CPU
accounting disables stall detection explicitly: `watchdog unavailable` appears in
the verdict and summary. Linux uses an
[inherited kernel task clock](https://man7.org/linux/man-pages/man2/perf_event_open.2.html):
it counts the check and its descendants, retaining CPU when children exit even
if their parent discards terminated-child status. The clock is attached while the
check is paused before its first instruction; the check then resumes and can
create children. A denied attachment or unreadable
counter reports unavailability immediately in the live log; there is no fallback
to incomplete `/proc` snapshots. Unrounded nanoseconds determine CPU progress. Exit 4 remains a program that could not start or crashed.

The real gate adapter preserves exit 6 and the check observations. The first stall
retains the candidate with `gate-stall-rerun`; the drain waits for fewer gates in
flight, or none other, before gating that same head. A second stall on its head tree
holds the item as `gate-stalled`; diagnose it and use `tillr drain clear-fault`.
Fast per-check measurements reach the observatory alongside remaining-check spans,
including their budgets, overruns and stalls.

The `go-build` check builds with `-trimpath`, in `GOFLAGS`, so new checkouts reuse
the shared Go build cache. The adapter warms it once per Go version before the fast
tier. The gate program wrapper still has its existing `--gate-timeout` bound;
removing that clock and the remaining Python wrapper clocks is the separate
[wrapper watchdog slice](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-rework.md#route-the-gate-programs-own-time-limits-through-the-stall-watchdog).

The `documents` change check remembers a pass for each changed document in the
same result store as tree checks, under a key over the document's blob and every
input the renderer reads for it: the files and directories it links to, the names
beside it, the `.vantage.toml` and `.vantageignore` files, and the check's own
code. A pass recorded by another renderer version is judged again, and a key that
ever recorded a fail is always judged again. Because the renderer reads the
working tree and the key reads the commit, no pass is remembered for a document
whose inputs differ on disk from the commit (a link target that is ignored or
untracked but present, an edited tracked input, a config the commit lacks), and a
failing run remembers nothing unless every error finding names a document it was
given. The documents still to judge go to
one `uvx vantage-check` run. On 2026-11-16 the four-document change from the drain
fault took 1.9 to 2.0 seconds with nothing remembered, about the same as before,
and 0.42 to 0.52 seconds with all four remembered, at one, two and ten concurrent
runs. The inputs are listed in
[`change_common.py`](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/checks/change_common.py).

## Optional local refs

A declaration names an optional local Git ref in `needs` with the prefix `ref:`:

```json
{"needs": ["ref:refs/heads/patch/pi"]}
```

The name must be a full ref; a single `*` wildcard can also match a set of refs.
Git's fetch-refspec grammar validates the name, so `?`, bracket patterns and malformed
refs are refused rather than reported with a fetch command that cannot work.
Before command execution or remembered-result lookup, a missing ref makes that check
`unavailable`. Its verdict entry lists `missing_needs`, each with the need and the
`git fetch origin` command that creates the local ref. The runner never fetches it.
An unavailable check is neither passed nor failed, and a run with only passed,
remembered and unavailable checks exits 0. The summary prints those three counts and
failed checks separately. Bare path needs from older generated registries retain their
existing missing-checkout error contract.

Every matched ref's name and resolved commit enters the tree check's
[fingerprint](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-rework.md#62-a-tree-checks-result-is-remembered-by-fingerprint),
the identifier of its declared inputs. Moving a ref reruns the check; removing it makes
the check unavailable even if a pass was remembered. A ref changing while its command
runs prevents that result from being remembered under the original inputs.

The eight patch-ref declarations judge local fork delivery branches. The
[structural suite](https://github.com/mschulkind-oss/tillr/blob/main/tests/pi-patch-series.test.sh) judges only the tree's locator
and pack metadata, so it passes in a fresh clone without those branches. The pi ref
check also judges the build script stored on its branch.

## Caller-owned change checks

`checks` runs the change declarations in a directory, optionally including its fast
tree declarations. Heavy tree checks are left to the heavy runner. A change check judges the
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
[loop’s default judge](https://github.com/mschulkind-oss/tillr/blob/main/gate/change-gate.py) runs the change declarations and fast tree
checks first, then the remaining tree program; it stops if any check fails or cannot judge.
The fast tree checks cover Markdown conventions, series structure, the pack mirror, and
parallel Go builds. Structural proof is not remembered because it also reads Git refs
outside the judged tree.

## What it refuses

- **A plan with no registry** — a gate with no registry would pass everything.
- **A run with no input record** — there is no verdict against a plan that is not there.
- **A change whose candidate and worktree disagree** — the record names the mismatch rather
  than gating the wrong tree.

## Test

`go test ./...` in this directory builds the binary and drives the faces against scratch
repositories, fake gate programs and the real check registry.
