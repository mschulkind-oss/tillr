#!/usr/bin/env python3
"""Report current Tillr lane handoff timing as one JSON row per run.finish."""

import argparse
from bisect import bisect_right
from collections import defaultdict
from datetime import datetime
import json
from pathlib import Path
import sqlite3


KINDS = (
    "run.finish", "run.allocate", "drain.examination-claimed",
    "drain.examined", "drain.node-advanced",
)


def elapsed(start, end):
    return end - start if start is not None and end is not None and end >= start else None


def union_ms(spans):
    total, end = 0, float("-inf")
    for start, stop in sorted(spans):
        total += max(0, stop - max(start, end))
        end = max(end, stop)
    return total


def phase_totals(exams):
    bounds = []
    phases = defaultdict(list)
    for exam in exams:
        start, end = exam["start_ms"], exam["end_ms"]
        bounds.append((start, end))
        for phase in exam.get("phases", []):
            left = max(start, phase["start_ms"])
            right = min(end, phase["end_ms"])
            if left <= right:
                phases[phase["phase"]].append((left, right))
    total = union_ms(bounds)
    return total, {**{key: union_ms(value) for key, value in phases.items()},
                   "other": total - union_ms([s for value in phases.values() for s in value])}


def read_spans(records, run_id):
    if Path(run_id).name != run_id or run_id in (".", ".."):
        return []
    try:
        with (records / run_id / "handoff.jsonl").open() as src:
            spans = []
            for line in src:
                try:
                    spans.append(json.loads(line))
                except json.JSONDecodeError:
                    # A diagnostic append interrupted by process exit is not a phase.
                    continue
            return spans
    except FileNotFoundError:
        return []


def report(store, records, since=None, until=None):
    runs = defaultdict(list)
    allocations = defaultdict(list)
    with sqlite3.connect(f"{store.resolve().as_uri()}?mode=ro", uri=True) as db:
        query = "SELECT seq, ts, kind, object_id, payload FROM events WHERE kind IN (?,?,?,?,?)"
        args = KINDS
        if since is not None:
            query += " AND seq >= (SELECT MIN(seq) FROM events WHERE kind = 'run.finish' AND ts >= ?)"
            args += (since,)
        for seq, ts, kind, obj, raw in db.execute(query + " ORDER BY seq", args):
            if not obj:
                continue
            if kind == "run.allocate":
                data = json.loads(raw)
                allocations[data.get("lane_id", obj)].append((seq, ts, data))
            elif kind == "drain.node-advanced":
                parent = json.loads(raw).get("parent_run_id")
                if parent:
                    runs[parent].append((seq, ts, kind, None))
            else:
                runs[obj].append((seq, ts, kind, raw if kind == "run.finish" else None))

    for run_id, events in runs.items():
        finishes = [i for i, row in enumerate(events) if row[2] == "run.finish"]
        if not finishes:
            continue
        spans = read_spans(records, run_id)
        for i, index in enumerate(finishes):
            seq, finished, _, raw = events[index]
            if (since is not None and finished < since) or (until is not None and finished >= until):
                continue
            next_index = finishes[i + 1] if i + 1 < len(finishes) else len(events)
            next_finish = events[next_index][1] if next_index < len(events) else float("inf")
            data = json.loads(raw)
            lane = data.get("lane_id")
            exams = [s for s in spans if s["phase"] == "examination" and finished <= s["start_ms"] < next_finish]
            primary = next((s["primary_run_id"] for s in exams if "primary_run_id" in s), None)
            following = events[index + 1:next_index]
            if primary:
                # A batch has one examination, recorded against its primary run.
                end_seq = events[next_index][0] if next_index < len(events) else float("inf")
                following = [row for row in runs.get(primary, []) if seq < row[0] < end_seq]
            claim = next((ts for _, ts, kind, _ in following if kind == "drain.examination-claimed"), None)
            done = next((row for row in following if row[2] == "drain.examined"), None)
            if done is None:
                done = next((row for row in following if row[2] == "drain.node-advanced"), None)
            start = min((s["start_ms"] for s in exams), default=claim)
            boundary = [s for s in spans if s["phase"] == "boundary" and finished <= s["start_ms"] < next_finish]
            boundary_ms, boundary_phases = phase_totals(boundary) if boundary else (None, None)
            exam_ms, phases = phase_totals(exams) if exams else (None, None)
            lane_allocs = allocations[lane]
            next_alloc = None
            if done is not None:
                j = bisect_right(lane_allocs, done[0], key=lambda alloc: alloc[0])
                if j < len(lane_allocs):
                    next_alloc = lane_allocs[j]
            yield {
                "run_id": run_id, "finish_seq": seq, "item_id": data.get("item_id"), "lane_id": lane,
                "finished_at_ms": finished, "examination_start_at_ms": start,
                "examination_start_source": "measured" if exams else "claim" if claim is not None else None,
                "finish_to_examination_start_ms": elapsed(finished, start),
                "post_finish_boundary_ms": boundary_ms, "post_finish_boundary_phases_ms": boundary_phases,
                "examination_ms": exam_ms,
                "examination_phases_ms": phases, "examined_at_ms": done[1] if done else None,
                "examined_source": done[2] if done else None,
                "examination_start_to_examined_ms": elapsed(start, done[1]) if done else None,
                "next_allocate_at_ms": next_alloc[1] if next_alloc else None,
                "next_run_id": next_alloc[2].get("run_id") if next_alloc else None,
                "next_item_id": next_alloc[2].get("item_id") if next_alloc else None,
                "examined_to_next_allocate_ms": elapsed(done[1], next_alloc[1]) if next_alloc else None,
                "finish_to_next_allocate_ms": elapsed(finished, next_alloc[1]) if next_alloc else None,
            }


def utc_ms(value):
    instant = datetime.fromisoformat(value)
    if instant.tzinfo is None:
        raise argparse.ArgumentTypeError("time requires an explicit timezone")
    return int(instant.timestamp() * 1000)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--store", required=True, type=Path)
    parser.add_argument("--records", required=True, type=Path, help="path to .tillr/loop")
    parser.add_argument("--since", type=utc_ms, help="inclusive ISO 8601 finish time")
    parser.add_argument("--until", type=utc_ms, help="exclusive ISO 8601 finish time")
    args = parser.parse_args()
    for row in sorted(report(args.store, args.records, args.since, args.until), key=lambda r: r["finish_seq"]):
        print(json.dumps(row))


if __name__ == "__main__":
    main()
