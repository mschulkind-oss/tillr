"""Native readers: each component's own recorded data, adapted to evidence records.

The velocity report (`velocity.py`) answers "where did the pipeline's time go" from
what the pipeline actually recorded. Each component keeps its record in its own
shape: the board a SQLite store of items, runs, events and verdicts; the gate a
timings timeline; the merge queue a SQLite store of entries, runs and steps; and the
pre-queue `bin/land` a directory of claim records. This module adapts each of those
into the shared **pipeline evidence record** (`docs/pi/pipeline-evidence.md`), so the
report's analysis never learns a component's schema and a component's schema change
lands in one adapter.

Every reader returns a `Read`: the records, plus the `meta` the report prints about
what it read -- the kind of file, how many rows, the span, and what it deliberately
did not turn into records (heartbeats, imported sessions, unmapped events). Those
counts are the difference between "nothing happened" and "nothing was read", which a
reader deciding whether to trust a zero needs to be able to tell apart.

Two rules hold for every adapter:

* A record carries a duration the producer measured, or a span both of whose ends the
  producer recorded. Nothing here invents a duration.
* An unreadable source raises `Unreadable` rather than returning an empty list: a
  store this adapter cannot read is not a pipeline with no work in it.
"""

from __future__ import annotations

import json
import os
import sqlite3
import sys
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import evidence  # noqa: E402  (the sibling module, not an installed package)

# The board store schema this adapter reads. The store declares its own version and
# migrates itself, so a version this adapter does not know is refused by name rather
# than read column-by-column and reported as a wrong number. That refusal is only right
# while this number is the store's: when the two drift the report answers "the board
# source is unreadable", which reads like a broken store rather than an old reader, so
# `tests/pipeline-evidence.test.py` pins this equal to the store the board writes. Move
# it in the same commit as `SchemaVersion` in the board store's schema file.
BOARD_SCHEMA = 17

# What a lane's run is, against what the board imported from Pi's own journal. An
# imported run's span is "the session was open", not lane wall time, so it is counted
# and excluded from lane timing rather than summed into it.
IMPORTED_SEAT = "pi-import"

# Board event kinds that are a pipeline step. Anything else is counted as unmapped so
# a new event kind is seen to be unmapped rather than silently dropped.
BOARD_EVENTS = {
    "run.allocate": ("dispatch", "allocate", False),
    "land.request": ("landing", "request", False),
    "run.landed": ("landing", "landed", False),
    "run.resume": ("rework", "run.resume", False),
    "run.reap": ("rework", "run.reap", False),
    "lane.failed": ("rework", "lane.failed", False),
    "lane.interrupted": ("rework", "lane.interrupted", False),
    # The dispatch ceiling: these carry `lanes`, which is what lane utilisation is
    # measured against.
    "drain.start": ("dispatch", "cap", False),
    "drain.lanes": ("dispatch", "cap", False),
    "drain.admit": ("dispatch", "cap", False),
}

# Board events that are already recorded elsewhere in the same store, so rendering
# them again would double-count. `run.finish` is the run's span, which the runs table
# carries; `item.add` and `criterion.freeze` are rows of their own tables.
BOARD_COVERED = frozenset({"run.finish", "run.heartbeat", "item.add", "verdict.record"})

# Merge queue step names that measure waiting rather than working. A lock is the queue
# waiting for the lock; everything else it records is work the queue did.
MQ_WAIT_STEPS = frozenset({"lock"})

# The merge queue's gate step runs the repository gate in the queue's scratch worktree,
# and the gate records that same run itself. The step is a span that *contains* the
# gate's work, so it is marked rather than summed beside it.
MQ_CONTAINS = {"gate": ["gate"]}

# Merge queue events that are a pipeline step of their own. `enqueued` and `started`
# are the entry's arrival and its run's start, which the entries and runs tables carry
# with real timestamps; rendering them again would be a second copy of one fact.
MQ_EVENTS = {
    "landed": ("landing", "landed"),
    "blocked": ("landing", "blocked"),
    "needs-rework": ("rework", "needs-rework"),
}

_OUTCOMES = {"ok", "failed", "skipped", "flaky", "aborted"}


class Unreadable(Exception):
    """A source exists but this adapter cannot read it, and says why."""


@dataclass
class Read:
    """One source's records and the report's claim about what it read."""

    records: list[dict] = field(default_factory=list)
    meta: dict = field(default_factory=dict)


# ── time ───────────────────────────────────────────────────────────────────────
# Adapters read times in three spellings -- epoch milliseconds from SQLite, ISO text
# from the merge queue, ISO seconds from the gate -- and hand the analysis one: epoch
# milliseconds. A record's own `startedAt`/`endedAt` are ISO, because that is the
# shared format.


def ms_from_text(text: str) -> int:
    """An ISO-8601 UTC timestamp as epoch milliseconds."""
    return int(round(datetime.fromisoformat(text.replace("Z", "+00:00")).timestamp() * 1000))


def ms_from_stamp(value) -> int:
    """A timestamp in either spelling the stores use: epoch milliseconds or ISO text."""
    if isinstance(value, (int, float)):
        return int(value)
    if isinstance(value, str) and value:
        return ms_from_text(value)
    raise Unreadable(f"a timestamp this adapter cannot read: {value!r}")


def text_from_ms(value: int) -> str:
    """Epoch milliseconds as the shared format's UTC timestamp, milliseconds kept."""
    stamp = datetime.fromtimestamp(value / 1000, timezone.utc)
    return stamp.strftime("%Y-%m-%dT%H:%M:%S.") + f"{value % 1000:03d}Z"


def text_from_text(value: str) -> str:
    """An ISO timestamp from a store, normalised to the format's spelling."""
    return text_from_ms(ms_from_text(value))


def clock(milliseconds: float) -> str:
    """A duration for a human: seconds under a minute, then minutes, then hours."""
    seconds = milliseconds / 1000
    if seconds < 60:
        return f"{seconds:.1f}s"
    if seconds < 3600:
        return f"{int(seconds // 60)}m{int(seconds % 60):02d}s"
    return f"{int(seconds // 3600)}h{int((seconds % 3600) // 60):02d}m"


# ── the record ─────────────────────────────────────────────────────────────────


def record(component: str, stage: str, step: str, *, start: int, duration_ms: int,
           repository: str | None = None, commit: str | None = None,
           item: int | None = None, run: str | None = None, wait: bool = False,
           outcome: str = "ok", detail: dict | None = None) -> dict:
    """One pipeline evidence record, from a span the producer recorded."""
    duration_ms = max(0, int(duration_ms))
    return {
        "version": evidence.SCHEMA_VERSION,
        "component": component,
        "stage": stage,
        "step": step,
        "wait": bool(wait),
        "subject": {"repository": repository, "commit": commit,
                    "item": item, "run": run},
        "startedAt": text_from_ms(start),
        "endedAt": text_from_ms(start + duration_ms),
        "durationMs": duration_ms,
        "outcome": outcome if outcome in _OUTCOMES else "failed",
        "detail": detail or {},
    }


def point(component: str, stage: str, step: str, *, at: int, **kwargs) -> dict:
    """A point event: both ends the same, no duration, so it never adds to a total."""
    return record(component, stage, step, start=at, duration_ms=0, **kwargs)


def _text(path: Path, name: str) -> str | None:
    try:
        value = (path / name).read_text(encoding="utf-8", errors="replace").strip()
    except OSError:
        return None
    return value or None


def _shas(text: str | None) -> list[str]:
    """Every 7-64 hex token in a file the queue wrote: a claim names its whole batch."""
    if not text:
        return []
    return [token for token in (part.strip() for part in text.split())
            if 7 <= len(token) <= 64 and all(c in "0123456789abcdef" for c in token)]


# ── the gate ───────────────────────────────────────────────────────────────────


def gate_timings(path: Path, repository: str | None = None) -> Read:
    """The gate's timings timeline: one line per run, each with its checks.

    The run line's `total_seconds` is the run's wall clock and `check_seconds` the sum
    of its checks' own seconds, which exceeds the wall clock when checks ran in
    parallel. The report's cost model sums work, so a check is one record of its own
    seconds and the run is one record of the time outside the check phase
    (`total_seconds - check_wall_seconds`): adding the run's wall clock as well would
    count the checks twice.
    """
    records: list[dict] = []
    runs = checks = 0
    span = [None, None]
    for number, line in enumerate(path.read_text(encoding="utf-8", errors="replace").splitlines(), 1):
        if not line.strip():
            continue
        try:
            entry = json.loads(line)
        except ValueError as error:
            raise Unreadable(f"{path}:{number}: not a timings line: {error}") from None
        if not isinstance(entry, dict) or "checks" not in entry:
            raise Unreadable(f"{path}:{number}: not a gate timings entry")
        at = ms_from_stamp(entry.get("at"))
        run = f"gate-{str(entry.get('commit') or 'unknown')[:12]}-{entry.get('at')}"
        started = at - int(round(float(entry.get("total_seconds") or 0) * 1000))
        runs += 1
        span[0] = started if span[0] is None else min(span[0], started)
        span[1] = at if span[1] is None else max(span[1], at)
        for check in entry["checks"]:
            seconds = float(check.get("seconds") or 0)
            check_start = ms_from_stamp(check["started"]) if check.get("started") else started
            records.append(record(
                "gate", "gate", str(check.get("kind") or "unknown"),
                start=check_start, duration_ms=int(round(seconds * 1000)),
                repository=repository, commit=entry.get("commit"), run=run,
                outcome="ok" if check.get("rc") == 0 else "failed",
                detail={"label": check.get("label"), "rc": check.get("rc"),
                        "command": check.get("command"),
                        "outputBytes": check.get("output_bytes")}))
            checks += 1
        wall = entry.get("check_wall_seconds", entry.get("checks_wall_seconds"))
        # The run's own seconds contain its checks. Without the check phase's wall clock
        # the overlap is unknown, so nothing is attributed outside the checks rather
        # than the whole run being added beside them (which would count them twice).
        if wall is None:
            overhead = max(0.0, float(entry.get("total_seconds") or 0)
                           - float(entry.get("check_seconds") or 0))
        else:
            overhead = max(0.0, float(entry.get("total_seconds") or 0) - float(wall))
        records.append(record(
            "gate", "gate", "run", start=started,
            duration_ms=int(round(overhead * 1000)),
            repository=repository, commit=entry.get("commit"), run=run,
            outcome={"verified": "ok", "failed": "failed", "refused": "aborted",
                     "dirty": "skipped"}.get(entry.get("outcome"), "failed"),
            detail={"outcome": entry.get("outcome"), "scope": entry.get("scope"),
                    "checks": len(entry["checks"]),
                    "totalSeconds": entry.get("total_seconds"),
                    "checkSeconds": entry.get("check_seconds"),
                    "checkWallSeconds": wall,
                    "jobs": entry.get("jobs"),
                    "peakConcurrency": entry.get("peak_concurrency")}))
    return Read(records, {"kind": "gate timings", "runs": runs, "checks": checks,
                          "from": text_from_ms(span[0]) if span[0] else None,
                          "to": text_from_ms(span[1]) if span[1] else None})


# ── the board ──────────────────────────────────────────────────────────────────


def board_slug(root: Path) -> str:
    """Pi's own session slug for a path: `--workspace--` for `/workspace`."""
    parts = [part for part in Path(root).resolve().as_posix().split("/") if part]
    return "--" + "-".join(parts) + "--"


def board_store_path(root: Path) -> Path:
    """Where `board` keeps its store for this workspace, unless BOARD_DB says otherwise."""
    given = os.environ.get("BOARD_DB")
    if given:
        return Path(given)
    state = os.environ.get("XDG_STATE_HOME") or str(Path.home() / ".local" / "state")
    return Path(state) / "board" / board_slug(root) / "board.sqlite3"


def board_store_candidates(root: Path, git_dir: Path) -> list[Path]:
    """Where the board's store may be, best first.

    The store is keyed by the workspace root, and `git rev-parse --show-toplevel` in a
    linked worktree names the worktree, not the checkout the board was created in. So a
    report run from a lane's worktree looks beside that checkout too: the board is one
    store for the repository, not one per worktree.
    """
    return [board_store_path(root), board_store_path(git_dir.parent)]


def _connect(path: Path) -> sqlite3.Connection:
    try:
        connection = sqlite3.connect(f"{path.resolve().as_uri()}?mode=ro", uri=True)
        connection.row_factory = sqlite3.Row
        return connection
    except sqlite3.Error as error:
        raise Unreadable(f"{path}: cannot open read-only: {error}") from None


def _columns(connection: sqlite3.Connection, table: str) -> set[str]:
    try:
        return {row[1] for row in connection.execute(f"PRAGMA table_info({table})")}
    except sqlite3.Error as error:
        raise Unreadable(f"cannot read table {table}: {error}") from None


def _require(connection: sqlite3.Connection, table: str, columns: set[str]) -> None:
    present = _columns(connection, table)
    if not present:
        raise Unreadable(f"no {table} table: this is not a store this adapter reads")
    missing = columns - present
    if missing:
        raise Unreadable(f"the {table} table has no {', '.join(sorted(missing))} column")


def board(path: Path, repository: str | None = None) -> Read:
    """The board store: items, runs, events, verdicts, criterion revisions, edges.

    A lane run is a span the board recorded at both ends, so it is lane wall time. An
    imported Pi session's span is the session's lifetime, not a lane's work, so it is
    counted and left out. An event is a point in time: the report derives the waits
    *between* events (ready to dispatch, run end to verdict) rather than treating a
    point as a duration.
    """
    connection = _connect(path)
    try:
        _require(connection, "meta", {"key", "value"})
        row = connection.execute("SELECT value FROM meta WHERE key = 'schema_version'").fetchone()
        version = int(row[0]) if row and row[0] else None
        if version != BOARD_SCHEMA:
            raise Unreadable(f"{path}: board store schema {version} is not schema "
                             f"{BOARD_SCHEMA}; refusing rather than guessing at its columns")
        _require(connection, "items", {"id", "status", "kind", "topic", "title",
                                       "created_at", "updated_at"})
        _require(connection, "runs", {"id", "item_id", "seat", "status", "outcome",
                                      "branch", "worktree", "base_commit", "candidate_commit",
                                      "landed_commit", "dirty", "started_at", "ended_at"})
        _require(connection, "events", {"seq", "ts", "kind", "object_type", "object_id",
                                        "payload"})
        _require(connection, "verdicts", {"item_id", "verdict", "ts", "seat", "actor",
                                          "landed"})
        _require(connection, "criteria_revisions", {"item_id", "revision", "frozen",
                                                    "created_at"})
        _require(connection, "edges", {"from_item", "to_item", "kind", "created_at"})

        records: list[dict] = []
        counts = {"items": 0, "laneRuns": 0, "importedRuns": 0, "events": 0,
                  "mappedEvents": 0, "verdicts": 0, "revisions": 0, "edges": 0,
                  "runningLanes": 0}
        # A lane still running has no end, so it is not evidence (the format's rule) and
        # is counted here instead: a report that silently dropped it would understate
        # the lanes the board is holding right now.
        counts["runningLanes"] = connection.execute(
            "SELECT COUNT(*) FROM runs WHERE ended_at IS NULL AND seat != ?",
            (IMPORTED_SEAT,)).fetchone()[0]
        span = [None, None]

        def span_to(value: int) -> None:
            span[0] = value if span[0] is None else min(span[0], value)
            span[1] = value if span[1] is None else max(span[1], value)

        for item in connection.execute(
                "SELECT id, status, kind, topic, title, created_at FROM items"):
            created = ms_from_stamp(item["created_at"])
            counts["items"] += 1
            span_to(created)
            records.append(point(
                "board", "backlog", "item.add", at=created, repository=repository,
                item=int(item["id"]),
                detail={"status": item["status"], "kind": item["kind"],
                        "topic": item["topic"], "title": (item["title"] or "")[:160]}))

        for revision in connection.execute(
                "SELECT item_id, revision, created_at FROM criteria_revisions "
                "WHERE frozen = 1"):
            frozen = ms_from_stamp(revision["created_at"])
            counts["revisions"] += 1
            span_to(frozen)
            records.append(point(
                "board", "backlog", "criterion.freeze", at=frozen, repository=repository,
                item=int(revision["item_id"]),
                detail={"revision": revision["revision"]}))

        for edge in connection.execute(
                "SELECT from_item, to_item, kind, created_at FROM edges"):
            created = ms_from_stamp(edge["created_at"])
            counts["edges"] += 1
            span_to(created)
            records.append(point(
                "board", "backlog", "blocked-by", at=created, repository=repository,
                item=int(edge["from_item"]),
                detail={"dependsOn": int(edge["to_item"]), "kind": edge["kind"]}))

        for run in connection.execute(
                "SELECT id, item_id, seat, status, outcome, branch, worktree, base_commit, "
                "candidate_commit, landed_commit, dirty, started_at, ended_at "
                "FROM runs WHERE ended_at IS NOT NULL"):
            if run["seat"] == IMPORTED_SEAT:
                counts["importedRuns"] += 1
                continue
            start, end = ms_from_stamp(run["started_at"]), ms_from_stamp(run["ended_at"])
            counts["laneRuns"] += 1
            span_to(start)
            span_to(end)
            commit = run["landed_commit"] or run["candidate_commit"] or run["base_commit"]
            records.append(record(
                "board", "lane", "run", start=start, duration_ms=end - start,
                repository=repository, commit=commit,
                item=run["item_id"], run=run["id"],
                outcome={"landed": "ok", "succeeded": "ok", "failed": "failed",
                         "interrupted": "aborted", "abandoned": "aborted"}
                .get(run["outcome"], "ok"),
                detail={"seat": run["seat"], "status": run["status"],
                        "outcome": run["outcome"], "dirty": bool(run["dirty"]),
                        "branch": run["branch"], "worktree": run["worktree"]}))

        for event in connection.execute(
                "SELECT seq, ts, kind, object_id, payload FROM events"):
            counts["events"] += 1
            mapping = BOARD_EVENTS.get(event["kind"])
            if mapping is None:
                continue
            stage, step, wait = mapping
            at = ms_from_stamp(event["ts"])
            try:
                payload = json.loads(event["payload"] or "{}")
            except ValueError:
                payload = {}
            if not isinstance(payload, dict):
                payload = {}
            item = payload.get("itemId") or payload.get("item")
            item = int(item) if isinstance(item, int) or (isinstance(item, str) and item.isdigit()) else None
            detail = dict(payload)
            detail["kind"] = event["kind"]
            detail["seq"] = event["seq"]
            if event["kind"] in ("drain.start", "drain.lanes", "drain.admit"):
                if not isinstance(payload.get("lanes"), int):
                    continue
                detail = {"lanes": payload["lanes"],
                          "lanesSource": payload.get("lanesSource") or payload.get("source"),
                          "inFlight": payload.get("inFlight")}
            counts["mappedEvents"] += 1
            span_to(at)
            records.append(point(
                "board", stage, step, at=at, repository=repository,
                commit=payload.get("candidate") or payload.get("landed"),
                item=item, run=event["object_id"], wait=wait, detail=detail))

        for verdict in connection.execute(
                "SELECT item_id, verdict, ts, seat, actor, landed FROM verdicts"):
            at = ms_from_stamp(verdict["ts"])
            counts["verdicts"] += 1
            span_to(at)
            records.append(point(
                "board", "review", "verdict", at=at, repository=repository,
                item=int(verdict["item_id"]),
                outcome="ok" if verdict["verdict"] == "done" else "failed",
                detail={"verdict": verdict["verdict"], "seat": verdict["seat"],
                        "actor": verdict["actor"], "landed": verdict["landed"]}))
    finally:
        connection.close()
    meta = {"kind": "board store", "schema": version, **counts,
            "from": text_from_ms(span[0]) if span[0] else None,
            "to": text_from_ms(span[1]) if span[1] else None}
    return Read(records, meta)


# ── the merge queue ────────────────────────────────────────────────────────────


def mq(path: Path, repository: str | None = None) -> Read:
    """The merge queue's store: entries, runs, steps and events.

    The queue records a step's span at both ends, so its steps are the landing's work.
    The wait for the queue to pick an entry up is the gap between the entry's
    submission and its run's start: a separate cost from any step, and the one the
    report must never merge into the gate's.
    """
    connection = _connect(path)
    try:
        _require(connection, "entries", {"id", "commits", "item_id", "state",
                                         "submitted_at", "landed_sha", "attempt_of",
                                         "root_id", "run_id"})
        _require(connection, "runs", {"id", "state", "started_at", "ended_at", "base",
                                      "tip", "entry_ids"})
        _require(connection, "steps", {"id", "run_id", "entry_id", "name", "started_at",
                                       "ended_at", "duration_ms", "outcome", "detail"})
        _require(connection, "events", {"seq", "at", "kind", "entry_id", "run_id", "detail"})

        records: list[dict] = []
        counts = {"runs": 0, "steps": 0, "entries": 0, "events": 0}
        span = [None, None]
        entries: dict[int, dict] = {}

        def span_to(value: int) -> None:
            span[0] = value if span[0] is None else min(span[0], value)
            span[1] = value if span[1] is None else max(span[1], value)

        for entry in connection.execute(
                "SELECT id, commits, item_id, state, submitted_at, landed_sha, attempt_of, "
                "root_id, run_id FROM entries"):
            submitted = ms_from_stamp(entry["submitted_at"])
            try:
                commits = json.loads(entry["commits"] or "[]")
            except ValueError:
                commits = []
            commits = [c for c in commits if isinstance(c, str)]
            item = int(entry["item_id"]) if entry["item_id"] else None
            entries[int(entry["id"])] = {"submitted": submitted, "item": item}
            counts["entries"] += 1
            span_to(submitted)
            records.append(point(
                "mq", "landing", "enqueued", at=submitted, repository=repository,
                commit=commits[0] if commits else None, item=item,
                run=str(entry["run_id"]) if entry["run_id"] else None,
                outcome={"landed": "ok", "blocked": "failed", "needs-rework": "failed"}
                .get(entry["state"], "ok"),
                detail={"state": entry["state"], "commits": commits,
                        "attemptOf": entry["attempt_of"], "rootId": entry["root_id"],
                        "landedSha": entry["landed_sha"] or None}))

        for run in connection.execute(
                "SELECT id, state, started_at, ended_at, base, tip, entry_ids FROM runs"):
            started = ms_from_stamp(run["started_at"])
            counts["runs"] += 1
            span_to(started)
            try:
                ids = [int(value) for value in json.loads(run["entry_ids"] or "[]")]
            except ValueError:
                ids = []
            submitted = [entries[i]["submitted"] for i in ids if i in entries]
            items = {entries[i]["item"] for i in ids if i in entries and entries[i]["item"]}
            if submitted:
                # The entry was submitted at a recorded time and its run started at
                # another: that gap is the queue's wait, and it is not any step's work.
                records.append(record(
                    "mq", "landing", "queued", start=min(submitted),
                    duration_ms=started - min(submitted), repository=repository,
                    commit=run["tip"] or run["base"], run=str(run["id"]),
                    item=next(iter(items)) if len(items) == 1 else None,
                    wait=True,
                    detail={"entries": ids, "state": run["state"]}))
            if run["ended_at"]:
                span_to(ms_from_stamp(run["ended_at"]))

        for step in connection.execute(
                "SELECT id, run_id, entry_id, name, started_at, ended_at, duration_ms, "
                "outcome, detail FROM steps"):
            started = ms_from_stamp(step["started_at"])
            duration = int(step["duration_ms"] or 0)
            counts["steps"] += 1
            span_to(started)
            span_to(started + duration)
            try:
                detail = json.loads(step["detail"] or "{}")
            except ValueError:
                detail = {}
            if not isinstance(detail, dict):
                detail = {}
            entry = entries.get(int(step["entry_id"])) if step["entry_id"] else None
            name = str(step["name"])
            stage = "gate" if name in MQ_CONTAINS else "landing"
            if name in MQ_CONTAINS:
                detail["covers"] = MQ_CONTAINS[name]
            detail["step"] = name
            records.append(record(
                "mq", stage, name, start=started, duration_ms=duration,
                repository=repository, run=str(step["run_id"]),
                item=entry["item"] if entry else None,
                wait=name in MQ_WAIT_STEPS,
                outcome=step["outcome"] or "ok", detail=detail))

        for event in connection.execute("SELECT seq, at, kind, entry_id, run_id, detail FROM events"):
            counts["events"] += 1
            mapping = MQ_EVENTS.get(event["kind"])
            if mapping is None:
                continue
            at = ms_from_stamp(event["at"])
            entry = entries.get(int(event["entry_id"])) if event["entry_id"] else None
            try:
                detail = json.loads(event["detail"] or "{}")
            except ValueError:
                detail = {}
            if not isinstance(detail, dict):
                detail = {}
            detail = dict(detail)
            detail["kind"] = event["kind"]
            stage, step = mapping
            span_to(at)
            records.append(point(
                "mq", stage, step, at=at, repository=repository,
                commit=detail.get("commit") or detail.get("landed"),
                item=entry["item"] if entry else None,
                run=str(event["run_id"]) if event["run_id"] else None,
                outcome="ok" if event["kind"] == "landed" else "failed",
                detail=detail))
    finally:
        connection.close()
    meta = {"kind": "merge queue store", **counts,
            "from": text_from_ms(span[0]) if span[0] else None,
            "to": text_from_ms(span[1]) if span[1] else None}
    return Read(records, meta)


# ── the pre-queue landing (`bin/land`) ─────────────────────────────────────────


def land_claims(path: Path, repository: str | None = None) -> Read:
    """`bin/land`'s claim records: one directory per claim, its files the record.

    The pre-queue landing wrote no durations, so a claim's span is what its directory
    recorded: the earliest and latest file it wrote, from the claim's own inputs to the
    gate's log. That span contains the gate run the claim started, which the gate
    records itself, so the claim is marked as containing gate work rather than summed
    beside it.
    """
    records: list[dict] = []
    claims = 0
    span = [None, None]
    for entry in sorted(path.iterdir()):
        if not entry.is_dir() or not entry.name.startswith("claim-"):
            continue
        stamps = []
        for child in entry.iterdir():
            try:
                stamps.append(int(child.stat().st_mtime * 1000))
            except OSError:
                continue
        if not stamps:
            continue
        start, end = min(stamps), max(stamps)
        claims += 1
        span[0] = start if span[0] is None else min(span[0], start)
        span[1] = end if span[1] is None else max(span[1], end)
        suffix = entry.name.rsplit(".", 1)[-1]
        outcome = {"landed": "ok", "failed": "failed"}.get(suffix, "aborted")
        files = sorted(child.name for child in entry.iterdir())
        candidates = _shas(_text(entry, "candidate"))
        records.append(record(
            "land", "landing", "claim", start=start, duration_ms=end - start,
            repository=repository, commit=candidates[-1] if candidates else None,
            run=entry.name, outcome=outcome,
            detail={"target": _text(entry, "target"), "files": files,
                    "candidates": candidates,
                    "covers": ["gate"] if "gate.log" in files else []}))
    return Read(records, {"kind": "landing claim directories", "claims": claims,
                          "from": text_from_ms(span[0]) if span[0] else None,
                          "to": text_from_ms(span[1]) if span[1] else None})


# ── an evidence file ───────────────────────────────────────────────────────────


def evidence_file(path: Path) -> Read:
    """A file already in the shared format, read by the format's own reader."""
    records = evidence.read_records(path)
    components = sorted({record["component"] for record in records})
    return Read(records, {"kind": "evidence file", "components": components})
