# pipeline-evidence — the shared record format, its validator and its velocity report

The board, a lander and the gate each record evidence in one versioned shape, so
one report can read whatever a repository happens to have installed. The format's prose
is [`docs/pi/pipeline-evidence.md`](../../docs/pi/pipeline-evidence.md); this directory
holds the machine-readable contract, the readers and the report.

| File | What it is |
| :--- | :--- |
| [`schema/evidence-v1.json`](./schema/evidence-v1.json) | Version 1's JSON Schema. The single source of truth: the validator is driven by this file, so a rule lives here and nowhere else. |
| [`evidence.py`](./evidence.py) | The validator and the reader: `validate(record)` returns the schema findings, and `read_records(path)` refuses a version it does not know. |
| [`handoff.py`](./handoff.py) | Per-run handoff timing from the current Tillr store and run records. |
| [`sources.py`](./sources.py) | The native readers: the board's SQLite store, the gate's timings timeline, the retired merge queue's store and a lander's claim directories, each adapted into the shared record. |
| [`velocity.py`](./velocity.py) | The report: ranks the stages by cost, splits WAIT from WORK, derives the waits no producer records, counts what explains velocity, diffs two windows, and files one finding where a board exists. |

## The schema is the contract

[`evidence.py`](./evidence.py) implements a JSON Schema evaluator for the keyword subset this repository's
schema uses — `type`, `const`, `enum`, `required`, `properties`,
`additionalProperties`, `pattern`, `minLength`, `maxLength`, `minimum`, `maximum`,
`items`, `allOf`, `anyOf` — and raises `UnsupportedKeyword` on anything else. That is
deliberate: a schema that grows a keyword nobody implements would read as a rule while
enforcing nothing, so the mismatch fails loudly instead.

`read_records` is where the version rule lives: a record whose `version` is not 1 raises
`UnknownVersion` naming the file, the line and the version found. A consumer that guesses
at a newer shape reports numbers that are silently wrong, which is worse than refusing.

## Sources the report reads

Each source is read from the component's own record first, so the report cannot disagree
with the producer about what happened; a rendered evidence file is the fallback, and
`--source NAME=PATH` reads one directly.

| Source | Default | Read as |
| :--- | :--- | :--- |
| `board` | `$BOARD_DB`, else `$XDG_STATE_HOME/board/<slug>/board.sqlite3` | the store's items, runs, events, verdicts, criterion revisions and edges. Imported Pi sessions are counted and left out of lane timing, because their span is the session's lifetime. |
| `mq` | `<common-git-dir>/merge-queue/queue.sqlite3` | the queue's entries, runs, steps and events; the entry's submission to its run's start is the queue's wait. |
| `gate` | `<common-git-dir>/yolo-fork-check/timings.jsonl` | one record per check and one for the time outside the check phase. |
| `land` | `<common-git-dir>/land-queue` | a lander's claim directories: a claim's span is the files it wrote, and it contains the gate it ran. |

A source with no file is named as not found; the report never treats absence as a zero.
Two sources naming one file is refused, because a doubled total is a wrong number that
still looks measured.

```sh
tools/pipeline-evidence/velocity.py                              # every source it can find
tools/pipeline-evidence/velocity.py --since 24h                  # the last day
tools/pipeline-evidence/velocity.py --since 2026-10-24T00:00:00Z --until 2026-10-25T00:00:00Z
tools/pipeline-evidence/velocity.py --json --out now.json        # machine-readable, kept
tools/pipeline-evidence/velocity.py --against before.json        # two windows, diffed
tools/pipeline-evidence/velocity.py --file                       # file one finding, where a board exists
```

The report's output is the product; `--file` is an adapter that files one board item
carrying the stage, items, seconds and runs behind the largest wait, and says plainly that
it filed nothing when there is no board to file into.

## Current Tillr lane handoffs

```sh
python3 tools/pipeline-evidence/handoff.py --store .tillr/state.db --records .tillr/loop \
  --since 2026-11-22T00:05:00Z --until 2026-11-22T00:13:00Z
```

This read-only report emits one JSON line per finish, including separate finishes of
resumed runs. It joins the finish to that run's examination and the next allocation on
its lane in event sequence order, even if the next allocation is outside the requested
finish window. Compare `item_id` and `next_item_id` to isolate intra-item handoffs.
`drain.node-advanced` supplies the examined time for a successful workflow transition
that has no `drain.examined` event. A legacy claim is labeled as a proxy for start, and
unobserved intervals or phases are null or absent, not zero.

The drain's `handoff.jsonl` captures the post-finish boundary and the examination
separately. The boundary counts toward finish-to-start, not examination duration.
`store_write` covers in-process write transactions including admission; time in an
external gate or land command is charged to that command's wall span. The phase report
unions overlapping spans for each phase and reports uncovered time as `other`; a nested
operation can appear in two named phases. The full examination span includes cleanup,
which can finish after its durable examined event.

## Tests

[`tests/pipeline-evidence.test.py`](../../tests/pipeline-evidence.test.py) is the
conformance suite: it validates one record from each producer against this schema in one
case, drives a real `board evidence` render, refuses a version-2 file, and builds a full
stage breakdown from records written to the format. It
also covers the report's own work: the gate's timeline gives each check and the run their
own seconds, the waits no producer records are derived from the events, a claim that
contains the gate is not summed beside it, the counts follow from a fixture, two windows
diff as changed numbers, and the sink files one item where a board exists.
