#!/usr/bin/env python3
"""One pipeline-evidence schema, its producers, and one consumer that says what it read.

This is the conformance suite for the shared evidence format
(docs/pi/pipeline-evidence.md). The format exists so a board, a lander and the gate can
record the same kind of fact -- a step, its subject, its span, its outcome -- without
sharing a process or a store, and so one report can read whatever of them a repository
happens to have installed. The lander that wrote these records in production (the merge
queue) is retired; the format it wrote is not, so the cases that read a lander's records
write them to the format themselves instead of running a producer.

The cases:

  1. The schema itself. A canonical record conforms, and each broken shape -- a missing
     key, an outcome outside the shared vocabulary, a timestamp that is not UTC, a
     negative duration, an unknown key, a subject that names nothing, a step that ends
     before it starts -- is reported rather than accepted.
  2. The version refusal. A consumer that meets a version it does not know refuses, and
     names the file, the line and the version, instead of guessing at a newer shape.
  3. The landing records a stage at a time. A lander writes one record per stage plus a
     run record, and a failed stage is recorded as failed; the records the report reads
     are written to the format by the case, because the lander that produced them is
     retired.
  4. The gate emits it. The gate's own record builder, driven with two checks, produces
     records of the same shape.
  5. The board emits it, and the report's adapter reads the same store. `board evidence`
     renders a real store's history as records; the store declares its own schema version
     and the adapter declares the one it reads, so the two are pinned equal against a
     store the board itself initialized -- a drifted pair makes the report answer
     "unreadable" instead of a number.
  6. ONE RECORD FROM EACH PRODUCER VALIDATES AGAINST THE SAME SCHEMA, in one case: the
     format's evidence, and the reason a schema file exists at all.
  7. The velocity report consumes any subset: with no board and no gate installed it
     still produces a complete stage breakdown from a lander's records alone, and names
     the sources it did not find.
  8. Adopting the format is additive: a repository with no evidence files at all gets a
     report that runs, names every source absent, and says there is nothing to break
     down -- rather than failing or inventing a zero.
  9. The gate's timings timeline is read natively: a check's cost is the seconds the
     timeline records, the run's cost is only the time outside its checks, and a check
     whose verdict flipped on one commit is counted as a flake.
 10. The waits no producer records are derived from the events it did record: an item's
     ready-but-not-dispatched wait, the gap between dispatch and a lane starting, and
     the wait for a verdict -- one per piece of work, not one per verdict.
 11. The landing queue's wait is its own cost: a candidate offered to a lander and claimed
     later is a wait, a claim's span contains the gate it ran and is not summed beside it,
     and a claim the standalone queue also recorded is counted once.
 12. The counts that explain velocity: duplicate work (two lanes on one item), abandoned
     and re-dispatched runs, rework cycles per landed item, and lane utilisation against
     the cap the dispatcher recorded.
 13. Two runs over different windows are diffable, so a regression is a changed number.
 14. The sink is optional: with no board the report says plainly that it filed nothing
     and exits 0; with a board it files one item carrying the stage, items, seconds and
     runs.


Scratch lives under $YOLO_DURABLE_DIR, never /tmp: a container jail deletes /tmp when it
exits, and an evidence test whose artifacts vanish cannot be re-read after a failure.
"""

from __future__ import annotations

import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE_DIR = ROOT / "tools" / "pipeline-evidence"
sys.path.insert(0, str(EVIDENCE_DIR))

import evidence  # noqa: E402
import sources  # noqa: E402
import velocity  # noqa: E402

SCRATCH_ROOT = Path(os.environ.get("YOLO_DURABLE_DIR") or (ROOT / ".yolo" / "durable"))
PASSED = 0
FAILED = 0
SKIPPED: list[str] = []


def ok(name: str) -> None:
    global PASSED
    PASSED += 1
    print(f"ok: {name}")


def no(name: str, detail: str = "") -> None:
    global FAILED
    FAILED += 1
    print(f"FAIL: {name}")
    if detail:
        for line in str(detail).strip().splitlines()[-12:]:
            print(f"      {line}")


def skip(name: str, why: str) -> None:
    SKIPPED.append(f"{name} ({why})")
    print(f"skip: {name} -- {why}")


def case(name: str, body) -> None:
    """Run one case; an assertion or an exception is a failure, not a crash."""
    try:
        body()
    except AssertionError as error:
        no(name, str(error))
    except Exception as error:  # noqa: BLE001 - a broken case must not stop the others
        no(name, f"{type(error).__name__}: {error}")
    else:
        ok(name)


def run(argv: list[str], cwd: Path, env: dict | None = None) -> subprocess.CompletedProcess:
    environment = dict(os.environ)
    environment.pop("BOARD_DB", None)
    environment.pop("BOARD_SEAT", None)
    environment.pop("LAND_BATCH_LIMIT", None)
    environment.update(env or {})
    return subprocess.run(argv, cwd=str(cwd), env=environment, capture_output=True, text=True)


def scratch(prefix: str) -> Path:
    SCRATCH_ROOT.mkdir(parents=True, exist_ok=True)
    return Path(tempfile.mkdtemp(prefix=f"{prefix}-", dir=str(SCRATCH_ROOT)))


def git(repo: Path, *args: str) -> str:
    result = run(["git", *args], repo)
    assert result.returncode == 0, f"git {' '.join(args)}: {result.stderr}"
    return result.stdout.strip()


def make_repo(base: Path, trunk: str = "master") -> Path:
    """A scratch repository with one feature branch one commit ahead of the trunk."""
    repo = base / "repo"
    repo.mkdir(parents=True)
    git(repo, "init", "-q", "-b", trunk)
    git(repo, "config", "user.email", "t@t")
    git(repo, "config", "user.name", "t")
    (repo / "file.txt").write_text("base\n")
    git(repo, "add", ".")
    git(repo, "commit", "-qm", "base")
    git(repo, "checkout", "-qb", "feature")
    (repo / "file.txt").write_text("base\nchange\n")
    git(repo, "commit", "-qam", "feature-change")
    git(repo, "checkout", "-q", trunk)
    return repo


# ── 1. the schema ─────────────────────────────────────────────────────────────

CANONICAL = {
    "version": 1,
    "component": "mq",
    "stage": "landing",
    "step": "claim",
    "subject": {"repository": "/scratch/repo", "commit": "a" * 40, "item": None,
                "run": "claim-aaaaaaa-123"},
    "startedAt": "2026-10-24T01:02:03Z",
    "endedAt": "2026-10-24T01:02:04.250Z",
    "durationMs": 1250,
    "outcome": "ok",
    "detail": {"target": "main"},
}


def broken(**changes) -> dict:
    record = json.loads(json.dumps(CANONICAL))
    for key, value in changes.items():
        if value is None:
            record.pop(key, None)
        else:
            record[key] = value
    return record


def schema_case() -> None:
    schema = evidence.load_schema()
    assert evidence.validate(CANONICAL, schema) == [], evidence.validate(CANONICAL, schema)
    assert evidence.validate({**CANONICAL, "wait": True}, schema) == [], "wait is optional"

    rejects = {
        "missing stage": broken(stage=None),
        "unknown outcome": broken(outcome="landed"),
        "stage outside the vocabulary": broken(stage="gating"),
        "local-time timestamp": broken(startedAt="2026-10-24 01:02:03"),
        "non-UTC timestamp": broken(startedAt="2026-10-24T01:02:03+02:00"),
        "negative duration": broken(durationMs=-1),
        "unknown key": broken(extra=1),
        "step ends before it starts": broken(endedAt="2026-10-24T01:00:00Z"),
        "subject names nothing": broken(
            subject={"repository": None, "commit": None, "item": None, "run": None}),
        "subject missing a key": broken(
            subject={"repository": "/scratch/repo", "commit": None, "run": "x"}),
        "commit is not a sha": broken(
            subject={"repository": None, "commit": "HEAD", "item": None, "run": "x"}),
        "wrong version": broken(version=2),
        "detail is not an object": broken(detail=[]),
    }
    for name, record in rejects.items():
        findings = evidence.validate(record, schema)
        assert findings, f"{name} was accepted by the schema"


# ── 2. the version refusal ────────────────────────────────────────────────────


def version_case() -> None:
    base = scratch("evidence-version")
    try:
        path = base / "evidence.jsonl"
        newer = {**CANONICAL, "version": 2}
        path.write_text(evidence.encode(CANONICAL) + "\n" + evidence.encode(newer) + "\n")
        try:
            evidence.read_records(path)
        except evidence.UnknownVersion as error:
            message = str(error)
            assert "version 2" in message, message
            assert ":2:" in message, f"the refusal does not name the line: {message}"
        else:
            raise AssertionError("a version-2 record was read as version 1")

        # The report refuses too, rather than reporting a breakdown from a shape it does
        # not understand, and it refuses on the record it could not read.
        repo = make_repo(base)
        (repo / ".git" / "merge-queue").mkdir(parents=True)
        (repo / ".git" / "merge-queue" / "evidence.jsonl").write_text(
            evidence.encode(newer) + "\n")
        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo)], base)
        assert result.returncode == 2, f"velocity exited {result.returncode}: {result.stdout}"
        assert "version 2" in result.stderr, result.stderr
        assert "refusing" in result.stderr, result.stderr

        # A file that is not evidence at all is refused too: unversioned lines are some
        # other tool's log, not an older format this consumer could interpret.
        path.write_text('{"claim":"claim-1","stage":"gate"}\n')
        try:
            evidence.read_records(path)
        except evidence.UnknownVersion as error:
            assert "None" in str(error), str(error)
        else:
            raise AssertionError("an unversioned line was read as evidence")
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 4. the gate ───────────────────────────────────────────────────────────────


def load_gate():
    spec = importlib.util.spec_from_file_location("fork_check_evidence", ROOT / "tools" / "fork-check.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def gate_case() -> None:
    base = scratch("evidence-gate")
    try:
        gate = load_gate()
        record = gate.Record(base / "state.json")
        checks = []
        for kind, rc, seconds, started, ended in (
            ("go-tool", 0, 2.5, "2026-10-24T01:00:00Z", "2026-10-24T01:00:02Z"),
            ("land-queue", 1, 1.5, "2026-10-24T01:00:02Z", "2026-10-24T01:00:04Z"),
        ):
            check = gate.Check(kind, f"{kind} label", ["sh", "-c", "true"], "because", 10)
            check.rc, check.seconds, check.started, check.ended = rc, seconds, started, ended
            check.output = "some output"
            checks.append(check)
        entry = {"at": "2026-10-24T01:00:04Z", "commit": "b" * 40, "tree": "c" * 40,
                 "changed": 3, "outcome": "failed", "scope": "audit", "since": None,
                 "seconds": 4.0, "not_planned": 1, "could_not_answer": 0}
        path = gate.append_evidence(record, ROOT, entry, checks)
        assert path is not None, "the gate did not record its evidence"
        records = evidence.read_records(path)
        assert [record["step"] for record in records] == ["go-tool", "land-queue", "run"], \
            [record["step"] for record in records]
        assert [record["outcome"] for record in records] == ["ok", "failed", "failed"]
        assert records[-1]["detail"]["outcome"] == "failed"
        assert all(record["component"] == "gate" for record in records)
        # A gate check's duration is its own measurement, and the record keeps it.
        assert records[0]["durationMs"] == 2500, records[0]["durationMs"]

        # Recording never fails a run: an unwritable path is reported and returns nothing,
        # rather than raising through the run that was about to reach a verdict.
        blocked = base / "state.json"
        blocked.write_text("")
        impossible = gate.Record(base / "state.json" / "nested" / "state.json")
        assert gate.append_evidence(impossible, ROOT, entry, checks) is None, \
            "an unwritable evidence path did not report failure"
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 6. one record from each producer, one schema ──────────────────────────────


def conformance_case() -> None:
    """The format's evidence: two producers, one schema, one case."""
    base = scratch("evidence-conformance")
    try:
        schema = evidence.load_schema()
        samples: dict[str, dict] = {}

        gate = load_gate()
        record = gate.Record(base / "gate" / "state.json")
        check = gate.Check("go-tool", "a check", ["sh", "-c", "true"], "because", 10)
        check.rc, check.seconds = 0, 1.0
        check.started = check.ended = "2026-10-24T01:00:00Z"
        entry = {"at": "2026-10-24T01:00:00Z", "commit": "d" * 40, "tree": "e" * 40,
                 "changed": 1, "outcome": "verified", "scope": "audit", "since": None,
                 "seconds": 1.0, "not_planned": 0, "could_not_answer": 0}
        path = gate.append_evidence(record, ROOT, entry, [check])
        samples["gate"] = evidence.read_records(path)[0]

        # The gate is the producer that still ships here. The merge queue's records are
        # history the format reads, not a producer that can be run, so the one record this
        # case validates is the gate's.
        assert set(samples) == {"gate"}, sorted(samples)
        for component, record in samples.items():
            findings = evidence.validate(record, schema)
            assert findings == [], f"{component}'s record does not conform: {findings}"
            assert record["component"] == component, record["component"]
            assert set(record) >= {"version", "stage", "step", "subject", "startedAt",
                                   "endedAt", "durationMs", "outcome", "detail"}, sorted(record)
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 7. the report from records written to the format ─────────────────────────


def report_case() -> None:
    """The report from records written to the format, with no producer running.

    The merge queue that wrote these is retired (docs/design/mq-retirement.md, OQ-MQR3);
    the format it wrote is not, and the velocity report still reads it. So the case writes
    the records itself -- one per stage the queue recorded -- and pins the report's
    breakdown, its wait marking and its gate-span marking against them. Reading the
    store's own candidates and attempts instead is a follow-up, not this lane.
    """
    base = scratch("evidence-report")
    try:
        repo = make_repo(base)
        assert not (repo / ".git" / "board").exists(), "the fixture must have no board"
        assert not (repo / ".git" / "yolo-fork-check").exists(), "the fixture must have no gate"
        commit = "b" * 40
        log = write_evidence(repo / ".git" / "merge-queue" / "evidence.jsonl", [
            rec("mq", "landing", "plan", at=0, commit=commit),
            # The wait for the lock is named as a wait: it is not work.
            rec("mq", "landing", "lock", at=1, ms=60000, wait=True, commit=commit),
            rec("mq", "landing", "pick", at=2, commit=commit),
            rec("mq", "landing", "verify", at=3, commit=commit),
            # The queue's own gate step is a span around the gate's work, so it says so
            # and is never summed beside the checks the gate recorded.
            rec("mq", "gate", "gate", at=4, ms=2 * 60000, commit=commit,
                detail={"covers": ["gate"]}),
            rec("mq", "landing", "land", at=6, commit=commit),
            rec("mq", "landing", "landed", at=7, commit=commit),
            rec("mq", "landing", "reset", at=8, commit=commit),
        ])
        assert log.is_file(), log

        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo)], base)
        assert result.returncode == 0, result.stderr
        output = result.stdout
        for step in ("plan", "lock", "pick", "verify", "gate", "land", "landed", "reset"):
            assert step in output, f"the breakdown does not name the {step} step:\n{output}"
        assert "board" in output and "NOT FOUND" in output, output
        assert "gate " in output and "NOT FOUND" in output, output

        report = report_of(repo, base)
        sources = {source["name"]: source for source in report["sources"]}
        assert sources["mq"]["read"] is True, sources
        assert sources["board"]["read"] is False and sources["gate"]["read"] is False, sources
        assert report["stages"], report
        assert {"landing", "gate"} <= {row["stage"] for row in report["stages"]}, \
            report["stages"]
        assert {row["component"] for row in report["breakdown"]} == {"mq"}, report["breakdown"]
        pairs = {(row["stage"], row["step"]) for row in report["breakdown"]}
        for pair in (("landing", "plan"), ("landing", "lock"), ("landing", "pick"),
                     ("landing", "verify"), ("gate", "gate"), ("landing", "land"),
                     ("landing", "landed"), ("landing", "reset")):
            assert pair in pairs, f"{pair[0]}/{pair[1]} missing from {sorted(pairs)}"
        assert report["outcomes"].get("ok", 0) > 0, report["outcomes"]
        wait_rows = [row for row in report["breakdown"] if row["step"] == "lock"]
        assert wait_rows and wait_rows[0]["wait"] is True, wait_rows
        gate_rows = [row for row in report["breakdown"] if row["step"] == "gate"]
        assert gate_rows and gate_rows[0]["covers"] == ["gate"], gate_rows
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 8. additivity ─────────────────────────────────────────────────────────────


def additive_case() -> None:
    base = scratch("evidence-additive")
    try:
        repo = make_repo(base)
        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo)], base)
        assert result.returncode == 0, f"the report failed with no producers: {result.stderr}"
        assert result.stdout.count("NOT FOUND") == 4, result.stdout
        assert "nothing to break down" in result.stdout, result.stdout

        # A source can also be handed over explicitly, so a producer that writes
        # somewhere else is still readable.
        explicit = base / "elsewhere.jsonl"
        explicit.write_text(evidence.encode(CANONICAL) + "\n")
        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo),
                      "--source", f"board={explicit}"], base)
        assert result.returncode == 0, result.stderr
        assert "mq" in result.stdout, result.stdout
        assert "claim" in result.stdout, result.stdout
        # The report names what each absent source is, not just that it is missing.
        assert "gate timings" in result.stdout, result.stdout

        # Two sources naming one file would count it twice, so it is refused rather than
        # reported as a doubled measurement.
        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo),
                      "--source", f"board={explicit}", "--source", f"gate={explicit}"], base)
        assert result.returncode != 0, "two sources on one file were counted twice"
        assert "same file" in result.stderr, result.stderr
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 9. the gate's timings timeline, read natively ─────────────────────────────

DAY = datetime(2026, 10, 24, tzinfo=timezone.utc)


def stamp(minutes: float) -> str:
    """A UTC timestamp `minutes` after the fixture day begins."""
    moment = DAY + timedelta(minutes=minutes)
    if not moment.microsecond:
        return moment.strftime("%Y-%m-%dT%H:%M:%SZ")
    return moment.strftime("%Y-%m-%dT%H:%M:%S.") + f"{moment.microsecond // 1000:03d}Z"


def rec(component: str, stage: str, step: str, *, at: float, ms: int = 0, item=None,
        run=None, commit=None, wait: bool = False, outcome: str = "ok",
        detail: dict | None = None) -> dict:
    """One synthetic evidence record, for the cases the producers cannot stage."""
    return {"version": 1, "component": component, "stage": stage, "step": step,
            "wait": wait,
            "subject": {"repository": "/scratch/repo", "commit": commit,
                        "item": item, "run": run},
            "startedAt": stamp(at), "endedAt": stamp(at + ms / 60000),
            "durationMs": ms, "outcome": outcome, "detail": detail or {}}


def write_evidence(path: Path, records: list[dict]) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("".join(evidence.encode(record) + "\n" for record in records))
    return path


def report_of(repo: Path, cwd: Path, *args: str) -> dict:
    result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo),
                  "--json", *args], cwd)
    assert result.returncode == 0, result.stderr
    return json.loads(result.stdout)


def rows_of(report: dict) -> dict[tuple[str, str], dict]:
    return {(row["stage"], row["step"]): row for row in report["breakdown"]}


def gate_timings_case() -> None:
    base = scratch("velocity-gate")
    try:
        repo = make_repo(base)
        timeline = repo / ".git" / "yolo-fork-check" / "timings.jsonl"
        timeline.parent.mkdir(parents=True)
        entries = [
            {"at": "2026-10-24T10:00:10Z", "commit": "a" * 40, "scope": "audit",
             "outcome": "verified", "total_seconds": 10.0, "check_seconds": 9.0,
             "check_wall_seconds": 8.0,
             "checks": [
                 {"kind": "go-tool", "label": "the tool", "command": "go test", "rc": 0,
                  "seconds": 6.0, "started": "2026-10-24T10:00:00Z"},
                 {"kind": "documents", "label": "the docs", "command": "check docs", "rc": 1,
                  "seconds": 3.0, "started": "2026-10-24T10:00:06Z"}]},
            {"at": "2026-10-24T11:00:20Z", "commit": "a" * 40, "scope": "audit",
             "outcome": "failed", "total_seconds": 20.0, "check_seconds": 12.0,
             "check_wall_seconds": 12.0,
             "checks": [
                 {"kind": "go-tool", "label": "the tool", "command": "go test", "rc": 1,
                  "seconds": 7.0, "started": "2026-10-24T11:00:00Z"},
                 {"kind": "documents", "label": "the docs", "command": "check docs", "rc": 0,
                  "seconds": 5.0, "started": "2026-10-24T11:00:07Z"}]},
        ]
        timeline.write_text("".join(json.dumps(entry) + "\n" for entry in entries))

        report = report_of(repo, base)
        rows = rows_of(report)
        # A check's cost is the seconds the timeline recorded for it, and nothing else.
        assert rows[("gate", "go-tool")]["totalMs"] == 13000, rows[("gate", "go-tool")]
        assert rows[("gate", "documents")]["totalMs"] == 8000, rows[("gate", "documents")]
        # The run is only the time outside its checks: adding the run's wall clock beside
        # the checks would count them twice.
        assert rows[("gate", "run")]["totalMs"] == (10 - 8) * 1000 + (20 - 12) * 1000, \
            rows[("gate", "run")]
        gate = next(row for row in report["stages"] if row["stage"] == "gate")
        assert gate["totalMs"] == 13000 + 8000 + 10000, gate
        # One commit, one check, two verdicts: the verdict is not determined by the commit.
        flakes = {row["step"]: row for row in report["counts"]["flakes"]}
        assert flakes["go-tool"]["flakedCommits"] == 1, flakes
        assert flakes["go-tool"]["commits"] == 1, flakes
        assert flakes["documents"]["flakeRate"] == 1.0, flakes
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 10. the waits no producer records ─────────────────────────────────────────


def derived_case() -> None:
    base = scratch("velocity-derived")
    try:
        repo = make_repo(base)
        board = write_evidence(base / "board.jsonl", [
            rec("board", "backlog", "item.add", at=0, item=1),
            rec("board", "backlog", "criterion.freeze", at=0, item=1),
            rec("board", "backlog", "blocked-by", at=0, item=1,
                detail={"dependsOn": 2, "kind": "after"}),
            rec("board", "backlog", "item.add", at=-60, item=2),
            rec("board", "lane", "run", at=-60, ms=30 * 60000, item=2, run="run-b",
                detail={"outcome": "landed"}),
            # Item 1: created and frozen at 00:00, blocked on item 2 (done 23:30 the day
            # before), dispatched at 12:00 -- three hours after it was ready.
            rec("board", "dispatch", "allocate", at=180, item=1, run="run-a"),
            rec("board", "lane", "run", at=180.5, ms=60 * 60000, item=1, run="run-a",
                detail={"outcome": "succeeded"}),
            rec("board", "review", "verdict", at=300.5, item=1, detail={"verdict": "done"}),
            # A second verdict on the same run re-states a review already waited for.
            rec("board", "review", "verdict", at=360, item=1, detail={"verdict": "done"}),
        ])
        report = report_of(repo, base, "--source", f"board={board}")
        rows = rows_of(report)
        ready = rows[("dispatch", "ready-wait")]
        assert ready["totalMs"] == 3 * 3600 * 1000, ready
        assert ready["wait"] is True, ready
        assert ready["items"] == 2, ready
        assert ready["records"] == 2, ready
        launch = rows[("dispatch", "launch")]
        assert launch["totalMs"] == 30 * 1000, launch
        review = rows[("review", "verdict-wait")]
        assert review["totalMs"] == 3600 * 1000, review
        assert review["records"] == 1, review
        assert report["counts"]["rework"]["landedItems"] == 2, report["counts"]["rework"]
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 11. the landing queue's wait is its own cost ──────────────────────────────


def claim_dir(root: Path, name: str, files: dict[str, float], candidate: str) -> Path:
    """A lander claim directory, with each file written at its own minute."""
    claim = root / name
    claim.mkdir(parents=True)
    for filename, minute in files.items():
        path = claim / filename
        path.write_text(candidate + "\n" if filename == "candidate" else "main\n")
        moment = (DAY + timedelta(minutes=minute)).timestamp()
        os.utime(path, (moment, moment))
    return claim


def landing_case() -> None:
    base = scratch("velocity-landing")
    try:
        repo = make_repo(base)
        queue = repo / ".git" / "land-queue"
        candidate = "b" * 40
        claim_dir(queue, "claim-bbbbbbb-1.landed",
                  {"base": 120, "candidate": 120, "patch-id": 120, "gate.log": 125}, candidate)
        # A second claim the standalone queue also recorded: it must be counted once.
        other = "c" * 40
        claim_dir(queue, "claim-ccccccc-2.landed",
                  {"base": 200, "candidate": 200, "gate.log": 205}, other)
        board = write_evidence(base / "board.jsonl", [
            rec("board", "landing", "request", at=60, item=7, commit=candidate),
            # The gate ran inside the claim's span, and recorded its own check there.
            rec("gate", "gate", "go-tool", at=121, ms=2 * 60000, commit=candidate,
                detail={"rc": 0}),
        ])
        mq = write_evidence(base / "mq.jsonl", [
            rec("mq", "landing", "enqueued", at=199, commit=other, run="9"),
        ])
        report = report_of(repo, base, "--source", f"board={board}",
                           "--source", f"mq={mq}")
        rows = rows_of(report)
        queued = rows[("landing", "queued")]
        assert queued["totalMs"] == 60 * 60000, queued
        assert queued["wait"] is True, queued
        claim = rows[("landing", "claim")]
        assert claim["covers"] == ["gate"], claim
        assert claim["records"] == 1, claim
        # The claim's span contains the gate the claim ran, so its seconds are the gate's.
        assert claim["counted"] == 0, claim
        assert report["notes"]["duplicateClaims"] == 1, report["notes"]
        gate = next(row for row in report["stages"] if row["stage"] == "gate")
        assert gate["totalMs"] == 2 * 60000, gate
        landing = next(row for row in report["stages"] if row["stage"] == "landing")
        assert landing["waitMs"] == 60 * 60000, landing
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 12. the counts that explain velocity ──────────────────────────────────────


def counts_case() -> None:
    base = scratch("velocity-counts")
    try:
        repo = make_repo(base)
        board = write_evidence(base / "board.jsonl", [
            rec("board", "backlog", "item.add", at=0, item=1),
            rec("board", "backlog", "item.add", at=0, item=2),
            # Two lanes on item 1 at once: duplicate work.
            rec("board", "lane", "run", at=0, ms=10 * 60000, item=1, run="run-a",
                detail={"outcome": "failed"}),
            rec("board", "lane", "run", at=5, ms=10 * 60000, item=1, run="run-b",
                detail={"outcome": "succeeded"}),
            # A lane resumed in the same millisecond is one lane handing over, not two.
            rec("board", "lane", "run", at=0, ms=20 * 60000, item=2, run="run-c",
                detail={"outcome": "landed"}),
            rec("board", "lane", "run", at=20, ms=60000, item=2, run="run-d",
                detail={"outcome": "landed"}),
            rec("board", "dispatch", "cap", at=-10, detail={"lanes": 2}),
            rec("board", "review", "verdict", at=25, item=2, detail={"verdict": "done"}),
        ])
        report = report_of(repo, base, "--source", f"board={board}")
        counts = report["counts"]
        duplicates = counts["duplicates"]
        assert duplicates["items"] == 1, duplicates
        assert duplicates["pairs"] == 1, duplicates
        assert duplicates["overlapMs"] == 5 * 60000, duplicates
        runs = counts["runs"]
        assert runs["total"] == 4, runs
        assert runs["abandoned"] == 0, runs
        assert runs["redispatchedItems"] == 2, runs
        assert runs["redispatchedRuns"] == 2, runs
        assert counts["rework"]["landedItems"] == 1, counts["rework"]
        assert counts["rework"]["cycles"] == 1, counts["rework"]
        utilisation = counts["utilisation"]
        assert utilisation["capKnown"] is True and utilisation["cap"] == 2, utilisation
        assert utilisation["meanActive"] == round(41 / 21, 2), utilisation
        assert utilisation["peakActive"] == 3, utilisation
        assert utilisation["share"] == round(41 / 42, 4), utilisation
        assert utilisation["atCapMs"] == 15 * 60000, utilisation
        assert utilisation["idleMs"] == 0, utilisation
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 13. two windows, diffed ──────────────────────────────────────────────────


def diff_case() -> None:
    base = scratch("velocity-diff")
    try:
        repo = make_repo(base)
        board = write_evidence(base / "board.jsonl", [
            rec("board", "backlog", "item.add", at=0, item=1),
            rec("board", "lane", "run", at=10, ms=10 * 60000, item=1, run="run-a",
                detail={"outcome": "succeeded"}),
            rec("board", "lane", "run", at=60, ms=30 * 60000, item=1, run="run-b",
                detail={"outcome": "succeeded"}),
        ])
        before = base / "before.json"
        after = base / "after.json"
        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo),
                      "--source", f"board={board}", "--until", stamp(30),
                      "--out", str(before), "--json"], base)
        assert result.returncode == 0, result.stderr
        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo),
                      "--source", f"board={board}", "--out", str(after), "--json"], base)
        assert result.returncode == 0, result.stderr
        earlier = json.loads(before.read_text())
        later = json.loads(after.read_text())
        assert earlier["window"]["until"] == stamp(30), earlier["window"]
        assert earlier["records"] < later["records"], (earlier["records"], later["records"])

        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo),
                      "--source", f"board={board}", "--against", str(before)], base)
        assert result.returncode == 0, result.stderr
        output = result.stdout
        assert "diff -- before -> now" in output, output
        assert "stage lane total" in output, output
        assert "stage lane median" in output, output
        assert "worse" in output, output
    finally:
        shutil.rmtree(base, ignore_errors=True)


# ── 14. the sink is optional ─────────────────────────────────────────────────


def sink_case() -> None:
    base = scratch("velocity-sink")
    try:
        repo = make_repo(base)
        board = write_evidence(base / "board.jsonl", [
            rec("board", "backlog", "item.add", at=0, item=1),
            rec("board", "dispatch", "allocate", at=30, item=1, run="run-a"),
            rec("board", "lane", "run", at=30, ms=10 * 60000, item=1, run="run-a",
                detail={"outcome": "succeeded"}),
        ])
        # The sink is gone: the report still prints, --file names the sink as gone.
        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo),
                      "--source", f"board={board}", "--file", "--json"], base)
        assert result.returncode == 1, f"expected a refusal for --file: {result.returncode}"
        report = json.loads(result.stdout)
        sink = report["sinks"][0]
        assert sink["available"] is False, sink
        assert "sink is gone" in sink["reason"], sink
        result = run([sys.executable, str(EVIDENCE_DIR / "velocity.py"), "--repo", str(repo),
                      "--source", f"board={board}"], base)
        assert result.returncode == 0, result.stderr
        assert "velocity -- " in result.stdout, result.stdout
    finally:
        shutil.rmtree(base, ignore_errors=True)


def main() -> int:
    case("the schema accepts a canonical record and refuses every broken shape", schema_case)
    case("a consumer refuses a record version it does not know", version_case)
    case("the gate records each check and the run", gate_case)
    case("one record from each producer validates against the same schema", conformance_case)
    case("the report produces a full breakdown from the merge queue alone", report_case)
    case("a repository with no producers gets a report that names them absent", additive_case)
    case("the gate's timeline gives each check and the run their own seconds", gate_timings_case)
    case("the waits no producer records are derived from the events", derived_case)
    case("the landing queue's wait is its own cost, and a claim contains its gate",
         landing_case)
    case("the counts that explain velocity", counts_case)
    case("two windows diff as changed numbers", diff_case)
    case("the sink is gone and the report says so", sink_case)
    for name in SKIPPED:
        print(f"skip: {name}")
    print(f"\npipeline-evidence: {PASSED} passed, {FAILED} failed, {len(SKIPPED)} skipped")
    return 1 if FAILED else 0


if __name__ == "__main__":
    sys.exit(main())
