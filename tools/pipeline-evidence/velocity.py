#!/usr/bin/env python3
"""Where development and landing time actually goes, from what the pipeline recorded.

One command, no model in the loop. It reads the three components' own records -- the
board store, the gate's timings timeline and the merge queue's step records (plus the
pre-queue `bin/land`'s claim directories, where a repository still has them) -- adapts
them into the shared **pipeline evidence record** (`sources.py`), and reports:

  * the stages ranked by total cost, each split into **WAIT** (time spent waiting: a
    ready item undispatched, a candidate queued, a run waiting for a verdict) and
    **WORK** (time spent doing something: a lane, a gate check, a landing step);
  * a breakdown by producer, stage and step, with each row's count, items, total,
    median, worst case and the run behind it;
  * the counts that explain velocity rather than an average alone: rework cycles per
    landed item, flake rate per gate check, abandoned and re-dispatched runs, duplicate
    work (two lanes on one item), and lane utilisation against the cap;
  * a diff against an earlier run of the same report, so a regression is a changed
    number rather than a feeling.

Recording and reporting are the product. Routing a finding to the board was a
separate, optional **sink**; the board CLI is deleted, so the sink is gone. The
report still runs and says plainly that it filed nothing.

    tools/pipeline-evidence/velocity.py                     # every source it can find
    tools/pipeline-evidence/velocity.py --since 24h         # the last day
    tools/pipeline-evidence/velocity.py --json --out now.json
    tools/pipeline-evidence/velocity.py --against before.json
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import evidence  # noqa: E402  (the sibling module, not an installed package)
import sources  # noqa: E402

# The sources this report knows how to look for, in the order it prints them. Each
# name is a component of the pipeline; the first path that exists is read with the
# kind beside it, so a store is preferred over a rendered copy of the same history.
SOURCE_ORDER = ("board", "mq", "gate", "land")

# The shared stage vocabulary, in the order the report ranks ties. The order is a
# pipeline's order, not a preference: dispatch before lane before review.
STAGE_ORDER = ("dispatch", "lane", "review", "gate", "landing", "rework")

# Two lane spans must overlap by more than this to be two lanes on one item: a resume
# starts as the old run is reaped, so a millisecond overlap is one lane handing over.
DUPLICATE_OVERLAP_MS = 1000

STAGE_MEANING = {
    "dispatch": "an item ready and not yet in a lane, and a lane starting",
    "lane": "an agent working in a worktree: a run's real span",
    "review": "the wait for a verdict on an item's work",
    "gate": "the repository gate: its checks and the run around them",
    "landing": "the merge queue: queueing, picking, verifying, landing, resetting",
    "rework": "a lane returned for repair; its cost is the extra lane run",
}


def common_git_dir(root: Path) -> Path:
    out = subprocess.run(["git", "-C", str(root), "rev-parse", "--path-format=absolute",
                          "--git-common-dir"], capture_output=True, text=True)
    if out.returncode != 0:
        raise SystemExit(f"velocity: {root} is not a git repository: {out.stderr.strip()}")
    return Path(out.stdout.strip())


def repository_name(root: Path) -> str:
    """The repository as the other producers name it: its remote, else its root."""
    out = subprocess.run(["git", "-C", str(root), "config", "--get", "remote.origin.url"],
                         capture_output=True, text=True)
    url = out.stdout.strip()
    return url if out.returncode == 0 and url else str(root)


def parse_since(value: str, now: datetime) -> datetime:
    """A window's start: an ISO-8601 timestamp, or a duration back from now (`24h`)."""
    text = value.strip()
    units = {"s": 1, "m": 60, "h": 3600, "d": 86400}
    if text and text[-1] in units and text[:-1].replace(".", "", 1).isdigit():
        return now - timedelta(seconds=float(text[:-1]) * units[text[-1]])
    try:
        parsed = datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError:
        raise SystemExit(f"velocity: --since {value!r} is neither an ISO-8601 timestamp "
                         f"nor a duration like 24h") from None
    return parsed if parsed.tzinfo else parsed.replace(tzinfo=timezone.utc)


def parse_stamp(text: str) -> datetime:
    return datetime.fromisoformat(text.replace("Z", "+00:00"))


def ms(record: dict, key: str = "startedAt") -> int:
    return sources.ms_from_text(record[key])


def item_of(record: dict) -> int | None:
    return record["subject"]["item"]


def run_of(record: dict) -> str | None:
    return record["subject"]["run"]


def covers_of(record: dict) -> list[str]:
    return (record.get("detail") or {}).get("covers") or []


def name_of(record: dict) -> str:
    """The most specific name a record carries, for a "worst case" column."""
    return (run_of(record) or record["subject"]["commit"]
            or (f"item {item_of(record)}" if item_of(record) else None) or "unnamed")


# ── reading the sources ────────────────────────────────────────────────────────


# What each source is, for the report's own line, whether or not the source was found.
FORMATS = {
    ("board", "store"): "board store",
    ("mq", "store"): "merge queue store",
    ("gate", "timings"): "gate timings",
    ("land", "claims"): "claim directories",
}


def source_paths(root: Path, git_dir: Path) -> dict[str, list[tuple[Path, str]]]:
    """Each source's candidate locations, best first: a store, then a rendered copy."""
    board = [(path, "store") for path in sources.board_store_candidates(root, git_dir)]
    return {
        "board": board + [(git_dir / "board" / "evidence.jsonl", "evidence")],
        "mq": [(git_dir / "merge-queue" / "queue.sqlite3", "store"),
               (git_dir / "merge-queue" / "evidence.jsonl", "evidence")],
        "gate": [(git_dir / "yolo-fork-check" / "timings.jsonl", "timings"),
                 (git_dir / "yolo-fork-check" / "evidence.jsonl", "evidence")],
        "land": [(git_dir / "land-queue", "claims")],
    }


def read_source(name: str, path: Path, kind: str, repository: str) -> sources.Read:
    if kind == "store" and name == "board":
        return sources.board(path, repository)
    if kind == "store" and name == "mq":
        return sources.mq(path, repository)
    if kind == "timings":
        return sources.gate_timings(path, repository)
    if kind == "claims":
        return sources.land_claims(path, repository)
    return sources.evidence_file(path)


def collect(root: Path, git_dir: Path, overrides: dict[str, Path],
            since: datetime | None, until: datetime | None) -> tuple[list[dict], list[dict]]:
    """Read every source that exists; describe each one the way it was found.

    A source's entry is the report's claim about its own coverage: found with N
    records, found and empty, or not found at all. Those are different facts, and a
    reader deciding whether to trust a zero needs to tell them apart, so the entry
    carries the path, the kind, the counts and the components the source actually held.
    """
    repository = repository_name(root)
    candidates = source_paths(root, git_dir)
    entries: list[dict] = []
    records: list[dict] = []
    seen: dict[Path, str] = {}
    for name in SOURCE_ORDER:
        choices = ([(overrides[name], "evidence")] if name in overrides
                   else candidates[name])
        path, kind = next(((p, k) for p, k in choices if p.exists()), (choices[0][0], choices[0][1]))
        # Two sources on one file would count its records twice, and a doubled total is
        # a wrong number that still looks measured. Refuse instead.
        if path in seen:
            raise SystemExit(f"velocity: {seen[path]} and {name} name the same file "
                             f"({path}); each source must be its own file")
        seen[path] = name
        entry = {"name": name, "path": str(path), "kind": kind,
                 "format": FORMATS.get((name, kind), kind),
                 "read": path.exists(), "records": 0, "in-window": 0,
                 "components": [], "meta": {}}
        if entry["read"]:
            read = read_source(name, path, kind, repository)
            kept = [record for record in read.records
                    if (since is None or parse_stamp(record["startedAt"]) >= since)
                    and (until is None or parse_stamp(record["startedAt"]) < until)]
            entry["records"] = len(read.records)
            entry["in-window"] = len(kept)
            entry["components"] = sorted({record["component"] for record in read.records})
            entry["meta"] = read.meta
            entry["format"] = read.meta.get("kind") or kind
            records.extend(kept)
        entries.append(entry)
    return entries, records


# ── the costs that are not a step ─────────────────────────────────────────────


def _point_index(records: list[dict], stage: str, step: str) -> list[dict]:
    return [r for r in records if r["stage"] == stage and r["step"] == step]


def _first_by_item(records: list[dict], key: str = "startedAt") -> dict[int, int]:
    found: dict[int, int] = {}
    for record in records:
        item = item_of(record)
        if item is None:
            continue
        value = ms(record, key)
        found[item] = value if item not in found else min(found[item], value)
    return found


def _last_by_item(records: list[dict], key: str = "startedAt") -> dict[int, int]:
    found: dict[int, int] = {}
    for record in records:
        item = item_of(record)
        if item is None:
            continue
        value = ms(record, key)
        found[item] = value if item not in found else max(found[item], value)
    return found


def reconcile(records: list[dict]) -> tuple[list[dict], dict]:
    """Drop a legacy claim the standalone queue also recorded.

    The two landing paths overlapped while the queue replaced `bin/land`, and a
    candidate can be named by both. Counting it twice would report a landing that
    happened once, so a claim whose commit the queue's own entry names is left out and
    counted.
    """
    queued = {record["subject"]["commit"] for record in records
              if record["component"] == "mq" and record["step"] == "enqueued"
              and record["subject"]["commit"]}
    kept, dropped = [], 0
    for record in records:
        named = set((record.get("detail") or {}).get("candidates")
                    or [record["subject"]["commit"]])
        if (record["component"] == "land" and record["step"] == "claim"
                and named & queued):
            dropped += 1
            continue
        kept.append(record)
    return kept, {"duplicateClaims": dropped}


def derive(records: list[dict]) -> tuple[list[dict], dict]:
    """Add the costs a producer recorded only as the two ends of a wait.

    A producer records a step, not the gap before it. The gaps are the point of this
    report -- "the queue is slow" and "the work is slow" are different costs -- so they
    are computed here, from the events and spans the producers did record, and added as
    records of their own so the ranking and the breakdown see them like any other cost.
    """
    derived: list[dict] = []
    notes = {"neverReady": 0, "negativeWaits": 0, "neverDispatched": 0,
             "unlandedRequests": 0}
    repository = next((r["subject"]["repository"] for r in records
                       if r["subject"]["repository"]), None)

    created = _first_by_item(_point_index(records, "backlog", "item.add"))
    frozen = _last_by_item(_point_index(records, "backlog", "criterion.freeze"))
    dispatched = _first_by_item(_point_index(records, "dispatch", "allocate"))
    for record in records:
        if record["stage"] == "lane" and record["step"] == "run":
            item = item_of(record)
            if item is not None:
                start = ms(record)
                dispatched[item] = start if item not in dispatched else min(dispatched[item], start)
    completed = _last_by_item([r for r in records
                               if r["step"] == "landed" or
                               (r["stage"] == "review" and r["step"] == "verdict"
                                and (r.get("detail") or {}).get("verdict") == "done")])
    for record in records:
        if record["stage"] == "lane" and record["step"] == "run" and \
                (record.get("detail") or {}).get("outcome") == "landed":
            item = item_of(record)
            if item is not None:
                end = ms(record) + record["durationMs"]
                completed[item] = end if item not in completed else max(completed[item], end)

    blockers: dict[int, set[int]] = {}
    for record in records:
        if record["step"] == "blocked-by" and item_of(record) is not None:
            depends = (record.get("detail") or {}).get("dependsOn")
            if isinstance(depends, int):
                blockers.setdefault(item_of(record), set()).add(depends)

    # Ready-but-not-dispatched: from the moment the item could have been picked up --
    # created, its criterion frozen, and everything it waits on completed -- to its
    # first dispatch. An item whose dependency never completed never became ready, so
    # it is counted rather than given a wait it did not spend.
    for item, first in dispatched.items():
        base = max(created.get(item, first), frozen.get(item, 0))
        waiting = [b for b in blockers.get(item, ()) if b not in completed]
        if waiting:
            notes["neverReady"] += 1
            continue
        if blockers.get(item):
            base = max(base, *(completed[b] for b in blockers[item]))
        wait = first - base
        if wait < 0:
            notes["negativeWaits"] += 1
            wait = 0
        derived.append(sources.record(
            "board", "dispatch", "ready-wait", start=base, duration_ms=wait,
            repository=repository, item=item, wait=True,
            detail={"dispatchedAt": sources.text_from_ms(first),
                    "blockedBy": sorted(blockers.get(item, ()))}))
    notes["neverDispatched"] = len([item for item in created if item not in dispatched])

    # Launch: the dispatcher decided, and the lane's process started later.
    allocate = {}
    for record in _point_index(records, "dispatch", "allocate"):
        if run_of(record):
            allocate[run_of(record)] = ms(record)
    for record in records:
        if record["stage"] != "lane" or record["step"] != "run":
            continue
        start = ms(record)
        if run_of(record) in allocate:
            wait = max(0, start - allocate[run_of(record)])
            derived.append(sources.record(
                "board", "dispatch", "launch", start=allocate[run_of(record)],
                duration_ms=wait, repository=repository, item=item_of(record),
                run=run_of(record), wait=True))

    # Review: the work ended, and the verdict was recorded later.
    ends: dict[int, list[int]] = {}
    for record in records:
        if record["stage"] == "lane" and record["step"] == "run" and item_of(record) is not None:
            ends.setdefault(item_of(record), []).append(ms(record) + record["durationMs"])
    # One wait per piece of work: the first verdict recorded after a run ended. A
    # second verdict on the same run re-states a review already waited for, and
    # counting its wait again would count the same hours twice.
    first_verdict: dict[tuple[int, int], tuple[int, str | None]] = {}
    for record in _point_index(records, "review", "verdict"):
        item = item_of(record)
        if item is None or not ends.get(item):
            continue
        at = ms(record)
        earlier = [end for end in ends[item] if end <= at]
        if not earlier:
            continue
        end = max(earlier)
        key = (item, end)
        if key not in first_verdict or at < first_verdict[key][0]:
            first_verdict[key] = (at, (record.get("detail") or {}).get("verdict"))
    for (item, end), (at, verdict) in sorted(first_verdict.items()):
        derived.append(sources.record(
            "board", "review", "verdict-wait", start=end, duration_ms=at - end,
            repository=repository, item=item, wait=True, detail={"verdict": verdict}))

    # The pre-queue landing: a candidate was offered, and the queue picked it up later.
    requests: dict[str, list[dict]] = {}
    for record in _point_index(records, "landing", "request"):
        commit = record["subject"]["commit"]
        if commit:
            requests.setdefault(commit, []).append(record)
    for record in records:
        if record["component"] != "land" or record["step"] != "claim":
            continue
        # A claim names its whole batch, so any commit it carries may be the one an
        # item offered to the queue.
        named = (record.get("detail") or {}).get("candidates") or [record["subject"]["commit"]]
        matched = None
        for commit in named:
            offered = [r for r in requests.get(commit, []) if ms(r) <= ms(record)]
            if offered:
                matched = (commit, max(offered, key=ms))
                break
        if matched is None:
            continue
        commit, request = matched
        derived.append(sources.record(
            "land", "landing", "queued", start=ms(request),
            duration_ms=ms(record) - ms(request), repository=repository,
            commit=commit, item=item_of(request), wait=True,
            detail={"claim": run_of(record)}))
        requests[commit].remove(request)
    notes["unlandedRequests"] = sum(len(rest) for rest in requests.values())
    return derived, notes


# ── containment ───────────────────────────────────────────────────────────────


def mark_containment(records: list[dict]) -> set[int]:
    """Indices of rows whose span contains another stage's recorded work.

    A landing step that runs the gate records the gate's wall clock as its own span,
    and the gate records that same run check by check. Summing both would count the
    gate twice, so a row marked `covers` is counted only where the covered stage has no
    record inside it: the marking is a claim about the data, and it is checked against
    the data rather than trusted.
    """
    intervals: dict[str, list[tuple[int, int]]] = {}
    for record in records:
        if covers_of(record):
            continue
        intervals.setdefault(record["stage"], []).append(
            (ms(record), ms(record) + record["durationMs"]))
    contained: set[int] = set()
    for index, record in enumerate(records):
        covered = covers_of(record)
        if not covered:
            continue
        start, end = ms(record), ms(record) + record["durationMs"]
        for stage in covered:
            if any(a < end and start < b for a, b in intervals.get(stage, ())):
                contained.add(index)
                break
    return contained


# ── aggregation ───────────────────────────────────────────────────────────────


def _median(values: list[int]) -> int:
    """The middle cost: point events are excluded, because a zero is not a cost.

    A stage holds both spans and point events (a verdict, a landed marker), and a
    median over both would read as zero whenever the points outnumber the spans.
    """
    costs = [value for value in values if value > 0]
    return int(statistics.median(costs)) if costs else 0


def _union(intervals: list[tuple[int, int]]) -> int:
    """The wall clock a set of spans covers, counted once however they overlap."""
    total, current = 0, None
    for start, end in sorted(intervals):
        if current is None or start > current[1]:
            if current is not None:
                total += current[1] - current[0]
            current = [start, end]
        else:
            current[1] = max(current[1], end)
    if current is not None:
        total += current[1] - current[0]
    return total


def breakdown(records: list[dict], contained: set[int]) -> list[dict]:
    """One row per (component, stage, step): what the pipeline actually recorded.

    Grouping by the producer's own step as well as the shared stage is what makes a
    component's breakdown complete: the merge queue's pick, verify, land and reset are
    four different costs, and a stage-only table would merge them into "landing" and
    hide where a landing's time went.
    """
    rows: dict[tuple[str, str, str], dict] = {}
    for index, record in enumerate(records):
        key = (record["component"], record["stage"], record["step"])
        row = rows.setdefault(key, {
            "component": key[0], "stage": key[1], "step": key[2],
            "records": 0, "counted": 0, "items": set(), "itemsKnown": False,
            "runs": set(), "durations": [], "waitMs": 0, "workMs": 0, "worst": None,
            "wait": True, "covers": covers_of(record)})
        row["records"] += 1
        if item_of(record) is not None:
            row["items"].add(item_of(record))
            row["itemsKnown"] = True
        if index in contained:
            continue
        row["counted"] += 1
        row["durations"].append(record["durationMs"])
        if record.get("wait"):
            row["waitMs"] += record["durationMs"]
        else:
            row["wait"] = False
            row["workMs"] += record["durationMs"]
        if run_of(record):
            row["runs"].add(run_of(record))
        if row["worst"] is None or record["durationMs"] > row["worst"]["durationMs"]:
            row["worst"] = record
    table = []
    for row in rows.values():
        worst = row.pop("worst")
        table.append({
            "component": row["component"], "stage": row["stage"], "step": row["step"],
            "records": row["records"], "counted": row["counted"],
            "items": len(row["items"]), "itemsKnown": row["itemsKnown"],
            "runs": len(row["runs"]),
            "totalMs": row["waitMs"] + row["workMs"],
            "medianMs": _median(row["durations"]),
            "worstMs": worst["durationMs"] if worst else 0,
            "worstRun": name_of(worst) if worst else "unnamed",
            "wait": row["wait"] and row["counted"] > 0,
            "covers": row["covers"],
        })
    table.sort(key=lambda row: (row["component"], STAGE_ORDER.index(row["stage"])
                                if row["stage"] in STAGE_ORDER else 99,
                                -row["totalMs"], row["step"]))
    return table


def stages(records: list[dict], contained: set[int]) -> list[dict]:
    """The stages, ranked by total cost, each split into WAIT and WORK.

    A stage's total is the sum of its own rows' durations: work done, which is what a
    stage costs however much of it ran at once. Its `wallMs` is the clock time those
    spans cover, which is smaller when the work was concurrent. Both are reported so a
    summed total is never read as a wall clock.
    """
    rows: dict[str, dict] = {}
    for index, record in enumerate(records):
        stage = record["stage"]
        row = rows.setdefault(stage, {
            "stage": stage, "records": 0, "counted": 0, "items": set(),
            "itemsKnown": False, "runs": set(), "waitMs": 0, "workMs": 0,
            "durations": [], "intervals": [], "worst": None})
        row["records"] += 1
        if item_of(record) is not None:
            row["items"].add(item_of(record))
            row["itemsKnown"] = True
        if index in contained:
            continue
        row["counted"] += 1
        row["durations"].append(record["durationMs"])
        row["intervals"].append((ms(record), ms(record) + record["durationMs"]))
        if record.get("wait"):
            row["waitMs"] += record["durationMs"]
        else:
            row["workMs"] += record["durationMs"]
        if run_of(record):
            row["runs"].add(run_of(record))
        if row["worst"] is None or record["durationMs"] > row["worst"]["durationMs"]:
            row["worst"] = record
    table = []
    for row in rows.values():
        worst = row["worst"]
        table.append({
            "stage": row["stage"],
            "meaning": STAGE_MEANING.get(row["stage"], ""),
            "records": row["records"], "counted": row["counted"],
            "items": len(row["items"]), "itemsKnown": row["itemsKnown"],
            "runs": len(row["runs"]),
            "waitMs": row["waitMs"], "workMs": row["workMs"],
            "totalMs": row["waitMs"] + row["workMs"],
            "medianMs": _median(row["durations"]),
            "wallMs": _union(row["intervals"]),
            "worstMs": worst["durationMs"] if worst else 0,
            "worstRun": name_of(worst) if worst else "unnamed",
            "worstStep": f"{worst['component']}/{worst['step']}" if worst else "",
        })
    table.sort(key=lambda row: (-row["totalMs"],
                                STAGE_ORDER.index(row["stage"])
                                if row["stage"] in STAGE_ORDER else 99))
    return table


def outcomes(records: list[dict]) -> dict[str, int]:
    counts: dict[str, int] = {}
    for record in records:
        counts[record["outcome"]] = counts.get(record["outcome"], 0) + 1
    return dict(sorted(counts.items()))


def velocity_counts(records: list[dict]) -> dict:
    """The counts that explain velocity, which no average can.

    Each is a count of events, not a duration: how many times an item had to be redone,
    how often a gate check's verdict flipped on one commit, how many lanes were
    abandoned and re-dispatched, how often two lanes worked one item at once, and how
    much of the lane cap the pipeline actually used.
    """
    lane_runs = [r for r in records if r["stage"] == "lane" and r["step"] == "run"]
    runs_by_item: dict[int, list[dict]] = {}
    for record in lane_runs:
        if item_of(record) is not None:
            runs_by_item.setdefault(item_of(record), []).append(record)

    landed_items = set()
    for record in lane_runs:
        if (record.get("detail") or {}).get("outcome") == "landed" and item_of(record) is not None:
            landed_items.add(item_of(record))
    for record in records:
        if (record["stage"] == "review" and record["step"] == "verdict"
                and (record.get("detail") or {}).get("verdict") == "done"
                and item_of(record) is not None):
            landed_items.add(item_of(record))
    cycles = 0
    distribution: dict[str, int] = {}
    for item in landed_items:
        count = len(runs_by_item.get(item, ()))
        distribution[str(count)] = distribution.get(str(count), 0) + 1
        cycles += max(0, count - 1)

    # A flake is one check's verdict flipping on one commit: the code did not change
    # between the two runs, so at least one of the verdicts was noise.
    checks: dict[str, dict] = {}
    for record in records:
        if record["stage"] != "gate" or record["step"] == "run" or record["component"] != "gate":
            continue
        if (record.get("detail") or {}).get("rc") is None:
            continue
        row = checks.setdefault(record["step"], {"step": record["step"], "runs": 0,
                                                 "failures": 0, "commits": {}, "flaked": set()})
        row["runs"] += 1
        commit = record["subject"]["commit"] or "unknown"
        row["commits"].setdefault(commit, set()).add(record["outcome"])
        if record["outcome"] == "failed":
            row["failures"] += 1
    flakes = []
    for row in checks.values():
        for commit, seen in row["commits"].items():
            if {"ok", "failed"} <= seen:
                row["flaked"].add(commit)
        flakes.append({"step": row["step"], "runs": row["runs"], "failures": row["failures"],
                       "commits": len(row["commits"]), "flakedCommits": len(row["flaked"]),
                       "flakeRate": round(len(row["flaked"]) / len(row["commits"]), 4)
                       if row["commits"] else 0.0})
    flakes.sort(key=lambda row: (-row["flakedCommits"], -row["flakeRate"], row["step"]))

    by_outcome: dict[str, int] = {}
    for record in lane_runs:
        outcome = (record.get("detail") or {}).get("outcome") or record["outcome"]
        by_outcome[outcome] = by_outcome.get(outcome, 0) + 1
    abandoned = sum(count for outcome, count in by_outcome.items()
                    if outcome in ("interrupted", "abandoned"))
    abandoned_ms = sum(record["durationMs"] for record in lane_runs
                       if (record.get("detail") or {}).get("outcome") in ("interrupted", "abandoned"))
    landed_ms = sum(record["durationMs"] for record in lane_runs
                    if (record.get("detail") or {}).get("outcome") in ("landed", "succeeded"))
    redispatched = {item: runs for item, runs in runs_by_item.items() if len(runs) > 1}

    # Duplicate work: two lane runs on one item whose spans overlap, which is two lanes
    # paying for one item's work.
    # A resumed lane starts as the old run is reaped, so the two spans touch to within
    # milliseconds. That is one lane handing over, not two lanes on one item, so only an
    # overlap longer than a second is duplicate work.
    duplicates = []
    for item, runs in runs_by_item.items():
        spans = sorted(((ms(r), ms(r) + r["durationMs"], r) for r in runs))
        overlap = 0
        pairs = 0
        for index, (start, end, record) in enumerate(spans):
            for later_start, later_end, later in spans[index + 1:]:
                if later_start >= end:
                    break
                shared = min(end, later_end) - later_start
                if shared <= DUPLICATE_OVERLAP_MS:
                    continue
                pairs += 1
                overlap += shared
        if pairs:
            duplicates.append({"item": item, "pairs": pairs, "overlapMs": overlap,
                               "runs": [run_of(record) for _, _, record in spans]})
    duplicates.sort(key=lambda row: -row["overlapMs"])

    # Lane utilisation: active lanes over time against the ceiling the dispatcher
    # recorded. The cap is a timeline, not a number, because an operator changes it
    # while a drain runs.
    caps = sorted((ms(record), (record.get("detail") or {}).get("lanes"))
                  for record in records
                  if record["stage"] == "dispatch" and record["step"] == "cap"
                  and isinstance((record.get("detail") or {}).get("lanes"), int))
    intervals = [(ms(r), ms(r) + r["durationMs"]) for r in lane_runs]
    utilisation = {"capKnown": bool(caps), "cap": caps[-1][1] if caps else None,
                   "caps": sorted({value for _, value in caps})}
    if intervals:
        start = min(a for a, _ in intervals)
        end = max(b for _, b in intervals)
        boundaries = sorted({value for value in
                             ({a for a, _ in intervals} | {b for _, b in intervals}
                              | {at for at, _ in caps})
                             if start <= value <= end} | {start, end})
        weighted = cap_weighted = active_time = idle = at_cap = known = 0
        peak = 0
        cap = None
        cap_index = 0
        for index, at in enumerate(boundaries):
            while cap_index < len(caps) and caps[cap_index][0] <= at:
                cap = caps[cap_index][1]
                cap_index += 1
            active = sum(1 for a, b in intervals if a <= at < b)
            peak = max(peak, active)
            if index + 1 == len(boundaries):
                break
            span = boundaries[index + 1] - at
            weighted += active * span
            active_time += span
            if active == 0:
                idle += span
            if cap is not None:
                known += span
                cap_weighted += cap * span
                if active >= cap:
                    at_cap += span
        utilisation.update({
            "windowMs": end - start,
            "meanActive": round(weighted / active_time, 2) if active_time else 0,
            "peakActive": peak,
            "idleMs": idle,
            "atCapMs": at_cap,
            "knownCapMs": known,
            "meanCap": round(cap_weighted / known, 2) if known else None,
            "share": round(weighted / cap_weighted, 4) if cap_weighted else None,
        })
    return {
        "rework": {"landedItems": len(landed_items), "cycles": cycles,
                   "perLandedItem": round(cycles / len(landed_items), 2)
                   if landed_items else 0.0,
                   "runsPerLandedItem": dict(sorted(distribution.items()))},
        "flakes": flakes,
        "runs": {"total": len(lane_runs), "byOutcome": dict(sorted(by_outcome.items())),
                 "abandoned": abandoned, "abandonedMs": abandoned_ms,
                 "landedMs": landed_ms,
                 "redispatchedItems": len(redispatched),
                 "redispatchedRuns": sum(len(runs) - 1 for runs in redispatched.values()),
                 "resumed": len([r for r in records if r["step"] == "run.resume"]),
                 "reaped": len([r for r in records if r["step"] == "run.reap"])},
        "duplicates": {"items": len(duplicates),
                       "pairs": sum(row["pairs"] for row in duplicates),
                       "overlapMs": sum(row["overlapMs"] for row in duplicates),
                       "worst": duplicates[:3]},
        "utilisation": utilisation,
    }


# ── the report ────────────────────────────────────────────────────────────────


def build(root: Path, options, overrides: dict[str, Path]) -> dict:
    git_dir = common_git_dir(root)
    now = parse_stamp(options.now) if options.now else datetime.now(timezone.utc)
    since = parse_since(options.since, now) if options.since else None
    until = parse_since(options.until, now) if options.until else None
    entries, records = collect(root, git_dir, overrides, since, until)
    records, reconciled = reconcile(records)
    derived, notes = derive(records)
    records = records + derived
    contained = mark_containment(records)
    report = {
        "repository": str(root),
        "generatedAt": now.isoformat().replace("+00:00", "Z"),
        "window": ({"since": since.isoformat().replace("+00:00", "Z") if since else None,
                    "until": until.isoformat().replace("+00:00", "Z") if until else None}
                   if since or until else None),
        "sources": entries,
        "stages": stages(records, contained),
        "breakdown": breakdown(records, contained),
        "counts": velocity_counts(records),
        "outcomes": outcomes(records),
        "records": len(records),
        "notes": {**reconciled, **notes,
                  "contained": len(contained),
                  "derived": len(derived)},
    }
    return report


def clock(milliseconds: float) -> str:
    return sources.clock(milliseconds)


def _share(part: int, whole: int) -> str:
    return f"{part / whole * 100:.0f}%" if whole else "-"


def render(report: dict) -> str:
    lines = [f"velocity -- {report['repository']}"]
    if report["window"]:
        window = report["window"]
        lines.append(f"window: records started at or after {window['since'] or 'the beginning'}"
                     + (f", before {window['until']}" if window.get("until") else ""))
    lines.append("")
    lines.append("sources")
    for source in report["sources"]:
        if not source["read"]:
            lines.append(f"  {source['name']:<7} {source['format']:<24} NOT FOUND  {source['path']}")
            continue
        components = ", ".join(source["components"]) or "no records"
        window = f", {source['in-window']} in the window" if report["window"] else ""
        lines.append(f"  {source['name']:<7} {source['format']:<24} read {source['records']:>5} "
                     f"record(s)  {source['path']}")
        lines.append(f"  {'':<7} {components}{window}")
        notes = source_notes(source)
        if notes:
            lines.append(f"  {'':<7} {notes}")

    if not report["stages"]:
        lines.append("")
        lines.append("nothing to break down: no source held a record in this window")
        return "\n".join(lines)

    lines.append("")
    lines.append("stages, ranked by total cost (WAIT is waiting, WORK is doing; wall is "
                 "clock time covered)")
    lines.append(f"  {'stage':<9} {'items':>5} {'records':>7} {'WAIT':>8} {'WORK':>8} "
                 f"{'TOTAL':>8} {'median':>8} {'wall':>8}  worst case")
    for row in report["stages"]:
        items = str(row["items"]) if row["itemsKnown"] else "-"
        lines.append(f"  {row['stage']:<9} {items:>5} {row['counted']:>7} "
                     f"{clock(row['waitMs']):>8} {clock(row['workMs']):>8} "
                     f"{clock(row['totalMs']):>8} {clock(row['medianMs']):>8} "
                     f"{clock(row['wallMs']):>8}  {row['worstStep']} {row['worstRun']}")

    lines.append("")
    lines.append("breakdown (a row marked 'contains' is a span whose contents are counted "
                 "under the stage it names)")
    lines.append(f"  {'component':<8} {'stage':<9} {'step':<18} {'n':>4} {'items':>5} "
                 f"{'runs':>5} {'total':>8} {'median':>8} {'worst':>8}  worst case")
    for row in report["breakdown"]:
        wait = " (wait)" if row["wait"] else ""
        contains = f" (contains {', '.join(row['covers'])})" if row["covers"] else ""
        if row["covers"] and row["counted"] == 0:
            contains += " [not summed]"
        items = str(row["items"]) if row["itemsKnown"] else "-"
        lines.append(f"  {row['component']:<8} {row['stage']:<9} {row['step']:<18} "
                     f"{row['records']:>4} {items:>5} {row['runs']:>5} "
                     f"{clock(row['totalMs']):>8} {clock(row['medianMs']):>8} "
                     f"{clock(row['worstMs']):>8}  {row['worstRun']}{wait}{contains}")

    counts = report["counts"]
    lines.append("")
    lines.append("velocity counts")
    rework = counts["rework"]
    lines.append(f"  rework         {rework['landedItems']} landed item(s), "
                 f"{rework['cycles']} rework cycle(s), {rework['perLandedItem']} per landed item")
    for row in counts["flakes"][:5]:
        lines.append(f"  flake          {row['step']:<18} {row['flakedCommits']}/"
                     f"{row['commits']} commit(s) flaked ({_share(row['flakedCommits'], row['commits'])})"
                     f", {row['failures']}/{row['runs']} run(s) failed")
    if not counts["flakes"]:
        lines.append("  flake          no gate check recorded")
    runs = counts["runs"]
    outcomes = ", ".join(f"{name} {count}" for name, count in runs["byOutcome"].items()) or "none"
    lines.append(f"  runs           {runs['total']} lane run(s): {outcomes}; "
                 f"{runs['abandoned']} abandoned ({clock(runs['abandonedMs'])} of lane wall "
                 f"time), {runs['redispatchedItems']} item(s) re-dispatched "
                 f"({runs['redispatchedRuns']} extra run(s)), {runs['resumed']} resumed")
    duplicates = counts["duplicates"]
    lines.append(f"  duplicates     {duplicates['items']} item(s) with overlapping lanes, "
                 f"{duplicates['pairs']} pair(s), {clock(duplicates['overlapMs'])} overlapped")
    utilisation = counts["utilisation"]
    if utilisation["capKnown"]:
        caps = utilisation["caps"]
        cap = (f"{caps[0]}-lane cap" if len(caps) == 1
               else f"cap {caps[0]}..{caps[-1]} (latest {utilisation['cap']})")
    else:
        cap = "cap not recorded"
    if "meanActive" in utilisation:
        share = (f"{utilisation['share'] * 100:.0f}%" if utilisation["share"] is not None
                 else "unknown")
        lines.append(f"  utilisation    {cap}, mean {utilisation['meanActive']} active "
                     f"({share} of the mean cap {utilisation['meanCap']}), "
                     f"peak {utilisation['peakActive']}, "
                     f"{clock(utilisation['atCapMs'])} at the cap, "
                     f"{clock(utilisation['idleMs'])} idle")
    else:
        lines.append(f"  utilisation    {cap}, no lane run recorded")
    notes = report["notes"]
    lines.append("")
    lines.append(f"records: {report['records']} ({notes['derived']} derived, "
                 f"{notes['contained']} row(s) not summed because their contents are counted "
                 f"elsewhere); never ready {notes['neverReady']}, never dispatched "
                 f"{notes['neverDispatched']}, duplicate claims {notes['duplicateClaims']}, "
                 f"unlanded requests {notes['unlandedRequests']}")
    return "\n".join(lines)


def source_notes(source: dict) -> str:
    """The one line that says what an adapter left out, and why."""
    meta = source.get("meta") or {}
    kind = meta.get("kind")
    if kind == "board store":
        parts = [f"schema {meta.get('schema')}", f"{meta.get('items')} item(s)",
                 f"{meta.get('laneRuns')} lane run(s)",
                 f"{meta.get('importedRuns')} imported pi session(s) excluded from lane timing"]
        if meta.get("runningLanes"):
            parts.append(f"{meta['runningLanes']} lane(s) still running, no end to record")
        parts.append(f"{meta.get('events')} event(s), {meta.get('mappedEvents')} mapped")
        parts.append(f"{meta.get('verdicts')} verdict(s)")
        return ", ".join(parts)
    if kind == "merge queue store":
        return (f"{meta.get('entries')} entr(ies), {meta.get('runs')} run(s), "
                f"{meta.get('steps')} step(s), {meta.get('events')} event(s)")
    if kind == "gate timings":
        return (f"{meta.get('runs')} run(s), {meta.get('checks')} check(s), "
                f"{meta.get('from')} .. {meta.get('to')}")
    if kind == "landing claim directories":
        return f"{meta.get('claims')} claim(s), {meta.get('from')} .. {meta.get('to')}"
    components = meta.get("components") or []
    return "components: " + (", ".join(components) if components else "none")


# ── diffing two windows ───────────────────────────────────────────────────────


def diff(before: dict, after: dict) -> list[str]:
    """Two runs of the report, compared number by number.

    Totals move with a window's length, so they are printed with their change and no
    verdict; the median and the per-item counts are the comparable numbers, and those
    carry a worse/better marker in the direction that costs velocity.
    """
    def window_of(report: dict) -> str:
        window = report.get("window") or {}
        return (f"{window.get('since') or 'the beginning'} .. "
                f"{window.get('until') or 'now'}")

    lines = ["diff -- before -> now", f"  before: {window_of(before)}",
             f"  now:    {window_of(after)}"]
    if window_of(before) != window_of(after):
        lines.append("  the windows differ: a total moves with the window's length, so the "
                     "medians and the per-item counts are the comparable numbers")

    def change(name: str, old, new, unit: str = "", comparable: bool = True,
               worse_when_up: bool = True) -> None:
        if old is None and new is None:
            return
        if not (isinstance(old, (int, float)) and isinstance(new, (int, float))):
            if old != new:
                lines.append(f"  {name:<34} {old!s:>10} -> {new!s:<10}")
            return
        delta = new - old
        if delta == 0:
            return
        marker = ""
        if comparable:
            marker = "  worse" if (delta > 0) == worse_when_up else "  better"
        if unit == "ms":
            shown = ("-" if delta < 0 else "+") + clock(abs(delta))
        else:
            shown = f"{delta:+g}"
        lines.append(f"  {name:<34} {old:>10} -> {new:<10} {shown:>10}{marker}")

    before_stages = {row["stage"]: row for row in before.get("stages", [])}
    for row in after.get("stages", []):
        old = before_stages.get(row["stage"], {})
        name = f"stage {row['stage']}"
        change(name + " total", old.get("totalMs"), row["totalMs"], "ms", comparable=False)
        change(name + " wait", old.get("waitMs"), row["waitMs"], "ms", comparable=False)
        change(name + " work", old.get("workMs"), row["workMs"], "ms", comparable=False)
        change(name + " median", old.get("medianMs"), row["medianMs"], "ms")

    before_rows = {(row["component"], row["stage"], row["step"]): row
                   for row in before.get("breakdown", [])}
    for row in after.get("breakdown", []):
        key = (row["component"], row["stage"], row["step"])
        old = before_rows.pop(key, None)
        label = f"{row['component']}/{row['stage']}/{row['step']}"
        if old is None:
            lines.append(f"  {label:<34} {'new':>10} {clock(row['totalMs']):>10}")
            continue
        change(label + " median", old.get("medianMs"), row["medianMs"], "ms")
        change(label + " total", old.get("totalMs"), row["totalMs"], "ms", comparable=False)
    for key in before_rows:
        lines.append(f"  {'/'.join(key):<34} {'gone':>10}")

    def count(name: str, path: list[str], worse_when_up: bool = True) -> None:
        old = (before.get("counts") or {})
        new = (after.get("counts") or {})
        for step in path:
            old = old.get(step) if isinstance(old, dict) else None
            new = new.get(step) if isinstance(new, dict) else None
        change(f"count {name}", old, new, worse_when_up=worse_when_up)

    count("rework per landed item", ["rework", "perLandedItem"])
    count("abandoned runs", ["runs", "abandoned"])
    count("re-dispatched items", ["runs", "redispatchedItems"])
    count("duplicate lane pairs", ["duplicates", "pairs"])
    count("lane utilisation share", ["utilisation", "share"], worse_when_up=False)
    old_flakes = {row["step"]: row["flakeRate"]
                  for row in before.get("counts", {}).get("flakes", [])}
    for row in after.get("counts", {}).get("flakes", []):
        change(f"flake {row['step']}", old_flakes.get(row["step"]), row["flakeRate"])
    if len(lines) <= 4:
        lines.append("  no comparable number changed")
    return lines


# ── the sink ──────────────────────────────────────────────────────────────────


def file_finding(report: dict, repo: Path, options) -> dict:
    """Report that the board sink is gone.

    The board CLI was deleted, so there is no longer anywhere to file one board item
    carrying the finding. The report itself is unchanged and still prints.
    """
    return {"name": "board", "kind": "queue", "available": False,
            "reason": "the board sink is gone: the board CLI was deleted"}


def sink_lines(report: dict) -> list[str]:
    lines = []
    for sink in report.get("sinks", []):
        if not sink["available"]:
            lines.append(f"  {sink['name']:<7} no sink: {sink['reason']}; the report is the product")
        elif sink.get("filed"):
            item = sink["filed"]
            lines.append(f"  {sink['name']:<7} filed item {item['item']}: {item['title']}")
        else:
            lines.append(f"  {sink['name']:<7} FAILED: {sink['reason']}")
    if not report.get("sinks"):
        lines.append("  none    report only; nothing was filed")
    return lines


# ── the command ───────────────────────────────────────────────────────────────


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Where the pipeline's time went, from its own recorded data.")
    parser.add_argument("--repo", default=".", help="repository root (default: .)")
    parser.add_argument("--since", help="window start: an ISO-8601 timestamp, or a duration like 24h")
    parser.add_argument("--until", help="window end: an ISO-8601 timestamp (exclusive); "
                                        "a record belongs to the window its step started in")
    parser.add_argument("--now", help="treat this ISO-8601 timestamp as now (for a reproducible run)")
    parser.add_argument("--source", action="append", default=[], metavar="NAME=PATH",
                        help="read NAME's records from an evidence file at PATH instead of "
                             "its default location")
    parser.add_argument("--json", action="store_true", help="machine-readable report")
    parser.add_argument("--out", help="also write the machine-readable report to PATH")
    parser.add_argument("--against", metavar="PATH",
                        help="diff this report against an earlier --out report")
    parser.add_argument("--file", action="store_true",
                        help="deprecated: the board sink was removed with the board")
    options = parser.parse_args(argv)

    root = Path(options.repo).resolve()
    overrides: dict[str, Path] = {}
    for given in options.source:
        name, _, path = given.partition("=")
        if not _ or name not in SOURCE_ORDER:
            raise SystemExit(f"velocity: --source takes {'|'.join(SOURCE_ORDER)} =PATH, not {given!r}")
        overrides[name] = Path(path).resolve()

    try:
        report = build(root, options, overrides)
    except sources.Unreadable as error:
        print(f"velocity: {error}", file=sys.stderr)
        return 2
    except evidence.UnknownVersion as error:
        print(f"velocity: {error}", file=sys.stderr)
        return 2
    except (evidence.MalformedLine, evidence.InvalidRecord) as error:
        print(f"velocity: {error}", file=sys.stderr)
        return 2

    sink_failed = False
    if options.file:
        sink = file_finding(report, root, options)
        report["sinks"] = [sink]
        sink_failed = True

    if options.out:
        Path(options.out).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")

    if options.against:
        report["diff"] = diff(json.loads(Path(options.against).read_text(encoding="utf-8")),
                              report)
    if options.json:
        print(json.dumps(report, indent=2))
    else:
        print(render(report))
        lines = sink_lines(report)
        if options.file or lines:
            print("")
            print("sinks")
            for line in lines:
                print(line)
        if report.get("diff"):
            print("")
            for line in report["diff"]:
                print(line)
    return 1 if sink_failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
