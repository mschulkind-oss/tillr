# The muse backend — `muse exec`

One `muse exec` run, from a staged brief to a stream a driver can check. Design:
[`docs/design/tillr-muse-backend.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-muse-backend.md),
sections 1, 3, 4, 6, 7 and 9.

| File | What it is |
| :--- | :--- |
| `run-muse` | The adapter. A `run.request`'s `Command[0]` names it. |
| `extract-report` | The dialect: the JSONL stream in, `report.md` out. |
| [`testdata/complete.jsonl`](./testdata/complete.jsonl) | A complete stream, with the non-JSON noise line every real one carries. |
| [`testdata/truncated.jsonl`](./testdata/truncated.jsonl) | The same stream cut off before its `RESULT:` footer — the negative control. |

## What the adapter does

```
run-muse --backend muse --model ID --effort TIER
              [--brief PATH] [--target PATH] [--timeout SECONDS]
              [--permission-profile ID] [--provider echo|meta]
```

It runs, from the target as its working directory:

```
timeout <wall> muse exec --json --prompt-file ../brief.md --workspace <target> \
    [--session-id <lane/session-id>] [--provider <knob>] [--permission-profile <id>] \
    [--model <policy model> --reasoning-effort <policy effort>]
```

`--provider echo` is the no-token rehearsal: the echo provider answers with the prompt
and opens no provider connection, so the whole pipeline can be exercised for the cost of
a process start. The model half of that argv is the one thing the knob changes: `muse
exec` refuses `--model` and `--reasoning-effort` under the echo provider (`--model
requires --provider meta or local`, Muse Code 1.4.18, measured 2026-11-12), so the
adapter passes the provider knob and withholds the pair. The driver's argv is the same
in both forms — the model and the effort are always supplied — and the preflight's model
resolution still runs under echo.

Refusals (exit 2, nothing started, no model tokens spent): the `backend` field names
neither this adapter nor a seat alias of it (`muse` or `muse-<role>`); no model; no effort;
the brief is missing, empty, unfilled or missing one of the template's ten sections; the
target is not the directory the adapter runs in; the harness is not on `PATH`; the model id
does not resolve through
`muse model-profile show <id> --effort <tier>` — a check only as good as that resolver,
which exits 0 for any string and echoes it back (measured 2026-11-12), so a wrong id
reaches the harness and is caught there as `does not exist or you lack access`.

Exit: the harness's own code, 124 from the wall, 2 from a refusal.

## Pausing and continuing

Pause kind: `cannot-resume`, declared in [`pause-kind`](./pause-kind).
SIGTERM resume behavior is unmeasured as of 2026-11-16; a session id flag alone does
not establish that an interrupted session can be continued. The runner sends
SIGTERM, waits ten seconds, then kills any surviving process group. Continuing
starts a fresh session in the same lane and worktree as the same attempt. Its
first prompt names the earlier transcript, report when present, and worktree
with uncommitted changes, followed by the original brief. No retry is spent.

## Declared capabilities

- **writes-tree:** yes.
- **resumable:** no. No headless, prompt-carrying continuation of a finished `muse exec`
  run was found in the CLI's help surface, so steering is re-brief in the same worktree
  with a correction section, as design [§5](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-muse-backend.md#5-steering) rules.
- **write confinement:** the CLI's own sandbox, which is on by default ("approval and the
  sandbox are ON by default", `muse exec --help`). The adapter never passes
  `--disable-sandbox` or `--yolo` and always roots `--workspace` at the named target, and
  the committed test asserts both from the argv the adapter actually execs. The
  confinement is the vendor's, so a CLI change could widen its roots silently — which is
  why the first live run carries the canary below.
- **structured report:** available (`--output-schema`, Meta provider only) and not used:
  the `RESULT:` footer is a brief convention that works for every harness in this tree.
- **usage:** whatever the stream's `run.usage` carries, recorded as an estimate and never
  as a control input.

## Silent-failure signatures

- **A wrong model id** reports as `does not exist or you lack access` in `stream.err` and
  a nonzero exit. The preflight catches it before the run.
- **A write refused by the permission profile** leaves the process running to its wall
  with nothing written and an unremarkable exit — the seat's own measured variant of the
  false-green report. The signature is `tool policy denied` in the stream; the answer is
  a profile that still writes inside the workspace, never a longer timeout.
- **The echo provider** ends with no `RESULT:` footer. That is the correct shape for the
  rehearsal, and the extractor refuses it with exit 4.

## Stage 1, end to end

1. **Stage the lane.** `<lane>/worktree` is a `git worktree add --lock` at the base sha;
   `<lane>/` holds `brief.md`, `fence.txt`, `base_sha` and `session-id`. The worktree is
   the default target. Naming the main checkout instead is allowed and weaker: record the
   driver's reason in the brief's header and carry the checkout's before-status as the
   fence evidence.
2. **Take the boundary snapshot** into `boundary.before`: the store file's size and
   mtime, `git for-each-ref`, `git worktree list`, and the main checkout's `git status`.
3. **Write the brief** from [`../brief-template.md`](../brief-template.md), filling every
   placeholder. An unfilled one is refused, so a template nobody filled cannot dispatch.
4. **Run the adapter** with the worktree as the cwd and the policy's model data on argv:
   `run-muse … > <lane>/<run-id>.log`.
5. **Extract** `report.md`: `extract-report --backend muse --exit-code $? < <run-id>.log`.
6. **Check from the tree, not from the report:** `boundary.after` diffs empty against
   `boundary.before`; `git -C <target> diff <base_sha> > diff.patch` and every name in it
   is inside `fence.txt`; every path the report claims is in the diff and every path in
   the diff is claimed or explained; and the brief's verification commands are re-run by
   the driver, never read off the report.
7. **Land through the gate** (`just done`) after the attempt result is assigned. The
   report's green is advisory; the gate judges the tree.

## The canary a live run owes

The first real Ember run carries design [§1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-muse-backend.md#1-what-a-backend-is)'s canary: the brief instructs one write just
outside the worktree, and the run counts as proven only when the stream records the
denial and `boundary.before`/`boundary.after` show the write never landed. If the canary
write succeeds under the profile a writing seat needs, the profile is wrong and the
adapter does not land until a narrower one passes.

## What a live run needs

A machine with `muse` installed and authenticated, a resolved model id and effort in the
backend policy, and a lane staged as above. Then:

```
tools/tillr-run/backends/muse/run-muse \
    --backend muse \
    --model "$(jq -r '.backends.muse.model' config/tillr/backend-policy.json)" \
    --effort "$(jq -r '.backends.muse.effort' config/tillr/backend-policy.json)" \
    --brief "$LANE/brief.md" --target "$LANE/worktree" \
    > "$LANE/<run-id>.log"
```

Run it from `$LANE/worktree`. Nothing about the machinery changes for a live run: the
model data is the only difference between it and the echo rehearsal.
