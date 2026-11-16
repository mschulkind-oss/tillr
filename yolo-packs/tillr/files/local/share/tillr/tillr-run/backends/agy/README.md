# The Agy Flash proof backend — `agy`

The proof that the contract survives a harness whose **effort is part of the model id**
and whose stream is **one envelope, not a JSONL stream**. Those are the two properties a
policy schema and a stream-shaped extractor are most likely to have baked in by accident.
Design:
[`docs/design/tillr-muse-backend.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-muse-backend.md),
sections 7 and 9.

| File | What it is |
| :--- | :--- |
| `run-agy` | The adapter. A `run.request`'s `Command[0]` names it. |
| `extract-report` | The dialect: the envelope in, `report.md` out. |
| [`testdata/complete.json`](./testdata/complete.json) | A complete envelope, ending in the `RESULT:` footer. |
| [`testdata/truncated.json`](./testdata/truncated.json) | A valid envelope whose answer stops before the footer — the negative control. |

## What the adapter does

```
run-agy --backend agy --model ID [--brief PATH] [--target PATH] [--timeout SECONDS]
```

It runs, from the target as its working directory, with the brief on stdin:

```
timeout <wall> agy --print --output-format json --model <policy model> \
    --print-timeout <wall>s --dangerously-skip-permissions  < ../brief.md
```

and with `YOLO_NO_LAUNCH_FLAGS=1` exported. There is no `--effort`: this adapter refuses
one, because the tier is a suffix of the id and a flag would be a second spelling of the
same thing.

Refusals (exit 2, nothing started): the `backend` field names neither this adapter nor a
seat alias of it (`agy` or `agy-<role>`); no model; `--effort`; the brief is missing, empty,
unfilled or missing one of the template's nine sections; the target is not the directory the
adapter runs in; `agy` is not on `PATH`.

Exit: the harness's own code, 124 from the wall, 2 from a refusal.

## Declared capabilities

- **writes-tree:** yes.
- **resumable:** no. Re-brief in the same worktree.
- **write confinement: NOTHING beyond the boundary snapshot.** The harness has a
  `--sandbox` flag, but nothing in this tree has measured whether a writing seat still
  builds under it, so this adapter does not claim it and does not pass it. The canary
  below is the measurement that would settle it; until then the snapshot is the boundary.
- **structured report:** `--json-schema` exists and is unused, for the reason above: the
  footer is a brief convention that every harness in this tree carries.
- **usage:** the envelope's `usage` (input, output, thinking, cache-read tokens), an
  estimate and never a control input.

## The two measured traps this adapter handles for you

Both are from `yolo-packs/matt-agentwork/skills/paired-design/SKILL.md`, where they cost
a seat a whole run each:

- **`YOLO_NO_LAUNCH_FLAGS=1`.** In a jail, `agy` is a launcher that injects the pack's own
  `--model` ahead of your arguments. Two `--model` flags fail the parse before anything
  runs, so a model chosen without this variable is a model that was not chosen.
- **`--dangerously-skip-permissions`.** Headless cannot prompt, so without it every tool
  call is auto-denied while the envelope still reports success, `response` comes back
  empty, and `denied_actions` names the write that never happened.
- **`--print-timeout` needs a unit** (`1800s`, not `1800`). The adapter passes the wall
  twice on purpose: `timeout` bounds the process, `--print-timeout` bounds the harness's
  own turn.

## Silent-failure signatures

- **`status: SUCCESS`, empty `response`, zero usage** — a print timeout. The run reported
  success and did nothing; the extractor says so on stderr.
- **`status: SUCCESS`, empty `response`, real usage, non-empty `denied_actions`** — a tool
  call the run could not ask permission for. The extractor names the denied actions on
  stderr.
- **An envelope that is not JSON at all** — a harness that died before answering. The
  extractor writes a report with `complete: no` and exits 4.
- **A truncated answer** is a partial `response`, not a partial object: the whole envelope
  arrives at the end of the run, so this dialect's truncation is the context cap, which is
  the failure the `RESULT:` footer exists to catch.

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
4. **Run the adapter** with the worktree as the cwd and the policy's model id on argv:
   `run-agy … > <lane>/<run-id>.log`.
5. **Extract** `report.md`: `extract-report --backend agy --exit-code $? < <run-id>.log`.
6. **Check from the tree, not from the report:** `boundary.after` diffs empty against
   `boundary.before`; `git -C <target> diff <base_sha> > diff.patch` and every name in it
   is inside `fence.txt`; every path the report claims is in the diff and every path in
   the diff is claimed or explained; and the brief's verification commands are re-run by
   the driver, never read off the report. On this adapter that step is the only boundary
   there is.
7. **Land through the gate** (`just done`) after the attempt result is assigned.

## The canary a live run owes

The first real Agy Flash run carries the instructed write just outside the worktree, and
records whether it landed. That answer decides whether `--sandbox` can be adopted for a
writing seat — and until it is recorded here, the claim stays "the snapshot is the
boundary".

## What a live run needs

A machine with `agy` installed and authenticated, a model id carrying its tier in the
backend policy, and a lane staged as above. Then:

```
tools/tillr-run/backends/agy/run-agy \
    --backend agy \
    --model "$(jq -r '.backends["agy"].model' config/tillr/backend-policy.json)" \
    --brief "$LANE/brief.md" --target "$LANE/worktree" \
    > "$LANE/<run-id>.log"
```

Run it from `$LANE/worktree`.
