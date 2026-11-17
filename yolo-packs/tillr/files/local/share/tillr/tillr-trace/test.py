#!/usr/bin/env python3
"""tillr-trace tests: measured parsing, attribution, the temporal join, fail-open.

Fixtures are built in $YOLO_DURABLE_DIR, never /tmp. The measured run log is
tools/tillr-trace/testdata/canary-run.log, a trimmed copy of a real
`pi --mode json` stream: only the `session`, `turn_end`, `tool_execution_start` and
`tool_execution_end` lines are kept, so the token and cost totals it carries are the
ones the live run recorded (README.md names the source and the trim).
"""

import datetime
import importlib.util
import json
import os
import shutil
import sqlite3
import stat
import subprocess
import sys
import tempfile
import time
import tracemalloc
from pathlib import Path

# The suite imports the tool from a file path, which would otherwise leave a
# __pycache__ directory in the tracked tool directory.
sys.dont_write_bytecode = True

ROOT = Path(__file__).resolve().parents[2]
TOOL = Path(__file__).resolve().parent
DURABLE = Path(os.environ.get("YOLO_DURABLE_DIR") or (ROOT / ".yolo/durable"))
CLI = TOOL / "tillr-trace"
TESTDATA = TOOL / "testdata"

spec = importlib.util.spec_from_file_location("tillr_trace", TOOL / "tillr_trace.py")
tillr_trace = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tillr_trace)

# Measured from the live canary run the fixture is trimmed from, then re-measured on
# the copy: 7 turns, 36196 input / 1917 output / 216192 cache-read / 438 reasoning
# tokens, $0.004746410, 6 tool calls (5 bash, 1 write).
MEASURED = {
    "turns": 7,
    "tokens_in": 36196,
    "tokens_out": 1917,
    "cache_read": 216192,
    "reasoning": 438,
    "cost_usd": 0.004746410,
    "tool_calls": 6,
}

STORE_DDL = """
CREATE TABLE lanes (lane_id TEXT PRIMARY KEY, state TEXT NOT NULL, run_id TEXT,
  leased_until INTEGER);
CREATE TABLE runs (run_id TEXT PRIMARY KEY, item_id TEXT, lane_id TEXT NOT NULL,
  backend TEXT NOT NULL, state TEXT NOT NULL, exit_code INTEGER,
  started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL,
  artifact_refs TEXT NOT NULL DEFAULT '[]', event_seq INTEGER NOT NULL DEFAULT 0);
CREATE TABLE items (item_id TEXT PRIMARY KEY, title TEXT NOT NULL, body_ref TEXT,
  state TEXT NOT NULL, design_doc TEXT, updated_at INTEGER NOT NULL,
  event_seq INTEGER NOT NULL DEFAULT 0, allowed_paths TEXT NOT NULL DEFAULT '');
CREATE TABLE candidates (candidate_id TEXT PRIMARY KEY, item_id TEXT NOT NULL,
  base_ref TEXT NOT NULL, base_sha TEXT NOT NULL, head_sha TEXT NOT NULL,
  tree_sha TEXT NOT NULL, state TEXT NOT NULL, enqueued_at INTEGER NOT NULL,
  event_seq INTEGER NOT NULL DEFAULT 0);
CREATE TABLE decisions (decision_id TEXT PRIMARY KEY, text TEXT NOT NULL,
  scope_kind TEXT NOT NULL, scope_ref TEXT NOT NULL DEFAULT '',
  decided_by TEXT NOT NULL, decided_at INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'active', supersedes TEXT,
  session_id TEXT NOT NULL DEFAULT '');
CREATE TABLE dispatches (decision_id TEXT PRIMARY KEY, item_id TEXT NOT NULL,
  lane_id TEXT, run_id TEXT, action TEXT NOT NULL, reason TEXT NOT NULL,
  dispatched_at INTEGER NOT NULL);
"""


def turn_end(index, text, *, ts_ms, provider="openrouter",
             model="deepseek/deepseek-v4.1-flash-1017:floor", thinking="high"):
    """One `turn_end` line in the measured shape of a real pi --mode json log."""
    return {
        "type": "turn_end",
        "message": {
            "role": "assistant",
            "api": "openai-completions",
            "provider": provider,
            "model": model,
            "usage": {
                "input": 100 + index, "output": 20, "cacheRead": 0,
                "cacheWrite": 0, "reasoning": 4, "totalTokens": 124,
                "cost": {"input": 0.0001, "output": 0.00002, "cacheRead": 0,
                         "cacheWrite": 0, "total": 0.00012 + index * 0.000001,
                         "totalFromProvider": True},
            },
            "stopReason": "toolUse",
            "timestamp": ts_ms,
            "responseModel": model.split(":")[0],
            "durationMs": 1000 + index,
            "thinkingLevel": thinking,
            "requestStartedAt": ts_ms - 1000,
            "credential": {"provider": provider, "fingerprint": "967fba49",
                           "algo": "sha256:8", "source": "OPENROUTER_API_KEY"},
            "content": [{"type": "text", "text": text}],
        },
    }


def write_log(path, run_id, lines):
    header = {"type": "session", "version": 3, "id": f"session-{run_id}",
              "timestamp": "2026-11-12T19:00:00.000Z", "cwd": "/w/lane-1/worktree"}
    path.write_text("\n".join(json.dumps(line) for line in [header] + lines) + "\n")


def write_trace_run(root, run_id, *, log_lines, proc_rows, base_sha="a" * 40):
    run_dir = root / "runs" / run_id
    run_dir.mkdir(parents=True, exist_ok=True)
    if log_lines is not None:
        write_log(run_dir / f"{run_id}.log", run_id, log_lines)
    if proc_rows is not None:
        (run_dir / "proc-exec.jsonl").write_text(
            "\n".join(json.dumps(row) for row in proc_rows) + "\n")
    (run_dir / "base_sha").write_text(base_sha + "\n")
    (run_dir / "manifest.json").write_text(json.dumps(
        {"run_id": run_id, "base_sha": base_sha, "head_sha": base_sha,
         "tree_sha": base_sha, "tick_id": "tick-1"}))
    return run_dir


def proc(ts, argv, module, exit_code=0, span="sh-00001"):
    """One shim row. `module` is the repo-relative directory the command ran in."""
    return {"kind": "proc.exec", "ts": ts, "argv": argv,
            "cwd": f"/w/lane-1/worktree/{module}", "repo_rel_cwd": module,
            "exit_code": exit_code, "span_id": span}


def make_store(path, rows):
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(str(path))
    conn.executescript(STORE_DDL)
    for row in rows:
        conn.execute(
            "INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code,"
            " started_at, finished_at, artifact_refs) VALUES (?,?,?,?,?,?,?,?,?)",
            (row["run_id"], row.get("item_id"), row["lane_id"], "pi", "exited", 0,
             1794526184000, 1794526200000, json.dumps(row.get("artifact_refs", []))))
        conn.execute("INSERT OR IGNORE INTO lanes (lane_id, state) VALUES (?, 'free')",
                     (row["lane_id"],))
    conn.commit()
    conn.close()


def cli(*args, cwd=None):
    result = subprocess.run(
        [sys.executable, str(CLI), *args],
        capture_output=True, text=True, cwd=str(cwd or ROOT))
    return result


def test_parser_recovers_measured_totals():
    """The parser's numbers are the live run's numbers, not a re-estimate."""
    parsed = tillr_trace.parse_run_log(
        TESTDATA / "canary-run.log", {"run_id": "run-canary-1", "lane_id": "lane-1"})
    rows, summary = parsed.rows, parsed.summary
    assert parsed.state == "ok", (parsed.state, parsed.detail)
    for key in ("turns", "tokens_in", "tokens_out", "cache_read", "reasoning",
                "tool_calls"):
        assert summary[key] == MEASURED[key], (key, summary[key], MEASURED[key])
    assert abs(summary["cost_usd"] - MEASURED["cost_usd"]) < 1e-9, summary["cost_usd"]
    kinds = [row["kind"] for row in rows.rows]
    assert kinds.count("llm.turn") == MEASURED["turns"], kinds
    assert kinds.count("tool.call") == MEASURED["tool_calls"], kinds
    first_turn = next(json.loads(row["payload"]) for row in rows.rows
                      if row["kind"] == "llm.turn")
    assert first_turn["provider"] == "openrouter", first_turn
    assert first_turn["thinking_level"] == "high", first_turn
    assert first_turn["credential_fp"] == "967fba49", first_turn
    assert first_turn["stop_reason"] == "toolUse", first_turn
    # The write tool call carries its target path; that is what the temporal join reads.
    write_calls = [json.loads(row["payload"]) for row in rows.rows
                   if row["kind"] == "tool.call"]
    assert [call["target_path"] for call in write_calls
            if call["tool_name"] == "write"] == [
        "/workspace/.yolo/durable/canary/lanes/lane-1/worktree/CANARY.md"], write_calls
    return "parser recovered 7 turns / $%.9f / 6 tool calls from the measured log" % (
        summary["cost_usd"],)


def test_ingest_builds_the_index_and_names_it(work):
    """`ingest` is the write the loop runs after a tick: it builds the index, and says where."""
    trace_root = work / "ingest-trace"
    store = work / "ingest-repo" / ".tillr" / "state.db"
    make_store(store, [{"run_id": "run-ingest-1", "item_id": "574", "lane_id": "lane-1"}])

    result = cli("--trace-root", str(trace_root), "--store", str(store), "ingest")
    assert result.returncode == 0, result.stderr
    assert str(trace_root / "index.db") in result.stdout, result.stdout

    conn = sqlite3.connect(str(trace_root / "index.db"))
    try:
        row = conn.execute("SELECT run_id FROM runs_index WHERE run_id = ?",
                           ("run-ingest-1",)).fetchone()
    finally:
        conn.close()
    assert row == ("run-ingest-1",), row
    return "ingest built the index and named it"


def test_cost_attributes_tokens_to_lane_and_route(work):
    """The same tokens land on the lane and on the route the turns were served by."""
    root = work / "cost-root"
    run_dir = root / "runs" / "run-canary-1"
    run_dir.mkdir(parents=True)
    shutil.copy2(TESTDATA / "canary-run.log", run_dir / "run-canary-1.log")
    store = work / "cost-store.db"
    make_store(store, [{"run_id": "run-canary-1", "item_id": "574", "lane_id": "lane-1"}])

    result = cli("--trace-root", str(root), "--store", str(store), "cost")
    assert result.returncode == 0, result.stderr
    assert "lane-1" in result.stdout, result.stdout
    assert "openrouter:deepseek/deepseek-v4.1-flash-1017:floor:high" in result.stdout, (
        result.stdout)
    line = next(line for line in result.stdout.splitlines() if "lane-1" in line)
    fields = line.split()
    # The numeric tail is runs, retries, wall_s, tok_in, cache_r, tok_out, think,
    # cost_usd — the lane and route are the only free-form columns before it.
    assert int(fields[-5]) == MEASURED["tokens_in"], line
    assert int(fields[-3]) == MEASURED["tokens_out"], line
    assert int(fields[-4]) == MEASURED["cache_read"], line
    assert abs(float(fields[-1]) - MEASURED["cost_usd"]) < 1e-6, line

    # The same tokens are what the parser totals: attribution does not invent or drop.
    summary = tillr_trace.parse_run_log(
        run_dir / "run-canary-1.log",
        {"run_id": "run-canary-1", "lane_id": "lane-1"}).summary
    assert int(fields[-5]) == summary["tokens_in"], (fields, summary)
    return "cost line attributes %d in / %d out to lane-1 and the openrouter route" % (
        MEASURED["tokens_in"], MEASURED["tokens_out"])


def test_discrepancies_flags_a_suite_that_ran_before_the_last_edit(work):
    """The stale-green trap: a suite green at 19:10 cannot attest an edit at 19:14."""
    root = work / "stale-root"
    run_dir = root / "runs" / "run-stale-1"
    run_dir.mkdir(parents=True)
    write_log(run_dir / "run-stale-1.log", "run-stale-1", [
        # A tool call has no clock of its own; the preceding request_start is the
        # anchor a real stream gives it (measured: tool_execution_end has no timestamp).
        {"type": "request_start", "timestamp": 1794510838000},  # 19:13:58
        {"type": "tool_execution_start", "toolCallId": "call-edit-1", "toolName": "edit",
         "args": {"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
        {"type": "tool_execution_end", "toolCallId": "call-edit-1", "toolName": "edit",
         "result": {"content": [{"type": "text", "text": "ok"}]},
         "isError": False, "durationMs": 3,
         "timestamp": 1794510840000},  # 2026-11-12T19:14:00Z edit
        turn_end(1, "Edited the store.\nRESULT: done", ts_ms=1794510845000),
    ])
    (run_dir / "proc-exec.jsonl").write_text("\n".join(json.dumps(row) for row in [
        proc("2026-11-12T19:10:02.000Z", ["go", "test", "./..."],
             "tools/tillr-store", span="sh-00001"),
        proc("2026-11-12T19:12:02.000Z", ["go", "test", "./..."],
             "tools/tillr-gate", span="sh-00002"),
    ]) + "\n")
    (run_dir / "manifest.json").write_text(json.dumps(
        {"run_id": "run-stale-1", "base_sha": "a" * 40, "head_sha": "a" * 40,
         "tree_sha": "b" * 40, "tick_id": "tick-1"}))
    store = work / "stale-store.db"
    make_store(store, [{"run_id": "run-stale-1", "item_id": "574", "lane_id": "lane-1"}])

    result = cli("--trace-root", str(root), "--store", str(store), "discrepancies")
    assert result.returncode == 0, result.stderr
    assert "disproved" in result.stdout, result.stdout
    assert "owed_suite" in result.stdout, result.stdout
    assert "suite passed only before the last edit" in result.stdout, result.stdout
    # RESULT: done with head_sha == base_sha and a changed tree is the third class.
    assert "uncommitted_worktree_with_result_done" in result.stdout, result.stdout
    return "discrepancies disproved the stale suite and the uncommitted RESULT: done"


def test_discrepancies_does_not_flag_a_suite_that_ran_after_the_last_edit(work):
    """The negative control: a suite that ran after the edit is not a discrepancy."""
    root = work / "fresh-root"
    run_dir = write_trace_run(root, "run-fresh-1", log_lines=[
        # A tool call has no clock of its own; the preceding request_start is the
        # anchor a real stream gives it (measured: tool_execution_end has no timestamp).
        {"type": "request_start", "timestamp": 1794510838000},  # 19:13:58
        {"type": "tool_execution_start", "toolCallId": "call-edit-1", "toolName": "edit",
         "args": {"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
        {"type": "tool_execution_end", "toolCallId": "call-edit-1", "toolName": "edit",
         "result": {"content": [{"type": "text", "text": "ok"}]},
         "isError": False, "durationMs": 3, "timestamp": 1794510840000},  # 19:14 edit
        turn_end(1, "Edited and re-ran.\nRESULT: done", ts_ms=1794510845000),
    ], proc_rows=[
        proc("2026-11-12T19:20:02.000Z", ["go", "test", "./..."],
             "tools/tillr-store", span="sh-00009"),
    ])
    # The fresh run committed, so the candidate class is satisfied too.
    manifest = json.loads((run_dir / "manifest.json").read_text())
    manifest["head_sha"] = "c" * 40
    (run_dir / "manifest.json").write_text(json.dumps(manifest))
    store = work / "fresh-store.db"
    make_store(store, [{"run_id": "run-fresh-1", "item_id": "574", "lane_id": "lane-1"}])

    result = cli("--trace-root", str(root), "--store", str(store), "discrepancies")
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == "discrepancies: none (no disproved claim in the index)", (
        result.stdout)

    audit = cli("--trace-root", str(root), "--store", str(store),
                "audit", "run-fresh-1")
    assert audit.returncode == 0, audit.stderr
    assert "verified" in audit.stdout and "disproved" not in audit.stdout, audit.stdout
    return "a suite run after the last edit stays verified and is not a discrepancy"


def test_missing_artifact_is_unreadable_not_a_crash(work):
    """No log, no trace jsonl and no store: rows that say so, and exit 0."""
    root = work / "empty-root"
    (root / "runs" / "run-empty-1").mkdir(parents=True)
    store = work / "absent-store.db"

    result = cli("--trace-root", str(root), "--store", str(store),
                 "audit", "run-empty-1")
    assert result.returncode == 0, (result.returncode, result.stderr)
    assert "unreadable" in result.stdout, result.stdout
    assert "log_state" not in result.stdout, result.stdout

    cost = cli("--trace-root", str(root), "--store", str(store), "cost")
    assert cost.returncode == 0, cost.stderr
    assert "cost: no llm.turn row" in cost.stdout, cost.stdout

    # A truncated stream keeps its parsed rows and reports the incomplete tail rather
    # than dropping the whole file.
    truncated = work / "truncated-root"
    run_dir = truncated / "runs" / "run-trunc-1"
    run_dir.mkdir(parents=True)
    write_log(run_dir / "run-trunc-1.log", "run-trunc-1", [turn_end(1, "hi", ts_ms=1)])
    with open(run_dir / "run-trunc-1.log", "a") as handle:
        handle.write('{"type":"turn_end","message":{"role":"assist')
    parsed = tillr_trace.parse_run_log(
        run_dir / "run-trunc-1.log", {"run_id": "run-trunc-1"})
    assert parsed.state == "truncated", (parsed.state, parsed.detail)
    assert parsed.summary["turns"] == 1, parsed.summary
    return "a missing log is an unreadable row and a truncated one keeps its rows"


def test_store_is_opened_read_only(work):
    """A chmod-555 store copy still answers, and this tool cannot write to it."""
    root = work / "readonly-root"
    run_dir = root / "runs" / "run-canary-1"
    run_dir.mkdir(parents=True)
    shutil.copy2(TESTDATA / "canary-run.log", run_dir / "run-canary-1.log")
    store_dir = work / "readonly"
    store_dir.mkdir()
    store = store_dir / "state.db"
    make_store(store, [{"run_id": "run-canary-1", "item_id": "574", "lane_id": "lane-1"}])
    os.chmod(store, stat.S_IRUSR | stat.S_IRGRP | stat.S_IROTH)
    os.chmod(store_dir, stat.S_IRUSR | stat.S_IXUSR)

    result = cli("--trace-root", str(root), "--store", str(store), "cost")
    assert result.returncode == 0, result.stderr
    assert "lane-1" in result.stdout, result.stdout

    opened = tillr_trace.Store(store)
    assert opened.state == "ok", (opened.state, opened.detail)
    try:
        opened.conn.execute("CREATE TABLE should_not_exist (a TEXT)")
    except sqlite3.OperationalError:
        pass
    else:
        raise AssertionError("the store connection accepted a write; it is not read-only")
    finally:
        opened.close()
    return "a chmod-555 store answers every query and refuses every write"


def test_loop_directory_supplies_proc_and_gate_records(work):
    """A run recorded under `.tillr/loop/<run-id>/` reads observed, not missing.

    This is the seam `run-pi`'s emitters write into: the run's own loop directory, beside
    the brief, fence and base sha staging keeps there. The trace root is deliberately
    empty, so a `proc [ok]` can only come from the loop directory.
    """
    trace_root = work / "loop-trace"
    store = work / "loop-repo" / ".tillr" / "state.db"
    run_id = "run-loop-1"
    loop_dir = store.parent / "loop" / run_id
    loop_dir.mkdir(parents=True)
    write_log(loop_dir / f"{run_id}.log", run_id, [
        turn_end(1, "Ran the suite.\nRESULT: done", ts_ms=1794510845000),
    ])
    (loop_dir / "proc-exec.jsonl").write_text(json.dumps(proc(
        "2026-11-12T19:20:02.000Z", ["go", "test", "./..."], "tools/tillr-store",
        span="sh-00001")) + "\n")
    (loop_dir / "git-trace2.jsonl").write_text("\n".join(json.dumps(row) for row in [
        {"event": "start", "sid": f"{run_id}/1", "time": "2026-11-12T19:20:03.000Z",
         "argv": ["/bin/git", "commit", "-m", "the fix"]},
        {"event": "exit", "sid": f"{run_id}/1", "time": "2026-11-12T19:20:03.100Z",
         "t_abs": 0.1, "code": 0},
    ]) + "\n")
    (loop_dir / "base_sha").write_text("a" * 40 + "\n")
    (loop_dir / "brief.md").write_text("# brief\n")
    (loop_dir / "fence.txt").write_text("tools/tillr-store\n")
    (loop_dir / "gate.plan.json").write_text(json.dumps({
        "record_version": 1, "kind": "gate.plan", "id": "gate-plan-1",
        "created_at": "2026-11-12T19:20:04Z", "payload": {
            "plan_id": "plan-abc", "base_sha": "a" * 40, "tree_sha": "b" * 40,
            "registry_digest": "digest-1",
            "checks": [{"check_id": "store-test", "command": ["go", "test", "./..."]},
                       {"check_id": "trace-test", "command": ["go", "test", "./..."]}]}}))
    (loop_dir / "gate.verdict.json").write_text(json.dumps({
        "record_version": 1, "kind": "gate.verdict", "id": "gate-verdict-1",
        "created_at": "2026-11-12T19:20:09Z", "payload": {
            "verdict_id": "verdict-1", "plan_id": "plan-abc",
            "registry_digest": "digest-1", "base_sha": "a" * 40, "tree_sha": "b" * 40,
            "result": "fail", "checks": [
                {"check_id": "store-test", "result": "pass", "duration_ms": 1200},
                {"check_id": "trace-test", "result": "fail", "duration_ms": 800}]}}))
    make_store(store, [{"run_id": run_id, "item_id": "574", "lane_id": "lane-1"}])

    audit = cli("--trace-root", str(trace_root), "--store", str(store), "audit", run_id)
    assert audit.returncode == 0, audit.stderr
    assert "proc [ok]" in audit.stdout, audit.stdout
    assert "git [" not in audit.stdout, audit.stdout

    conn = sqlite3.connect(str(trace_root / "index.db"))
    try:
        gate_rows = {
            kind: json.loads(payload) for kind, payload in conn.execute(
                "SELECT kind, payload FROM obs_events WHERE run_id = ? AND"
                " kind LIKE 'gate.%'", (run_id,))}
        proc_rows = [json.loads(payload) for (payload,) in conn.execute(
            "SELECT payload FROM obs_events WHERE run_id = ? AND kind = 'proc.exec'",
            (run_id,))]
        git_rows = conn.execute(
            "SELECT kind FROM obs_events WHERE run_id = ? AND kind = 'git.trace2'",
            (run_id,)).fetchall()
        brief_path = conn.execute(
            "SELECT brief_path FROM runs_index WHERE run_id = ?", (run_id,)).fetchone()[0]
    finally:
        conn.close()
    assert brief_path == str(loop_dir / "brief.md"), brief_path
    assert set(gate_rows) == {"gate.plan", "gate.verdict"}, gate_rows
    assert gate_rows["gate.plan"]["checks_count"] == 2, gate_rows["gate.plan"]
    assert gate_rows["gate.plan"]["check_ids"] == ["store-test", "trace-test"]
    assert gate_rows["gate.verdict"]["verdict"] == "fail", gate_rows["gate.verdict"]
    assert gate_rows["gate.verdict"]["failed_check_ids"] == ["trace-test"]
    assert gate_rows["gate.verdict"]["check_timings_ms"] == {
        "store-test": 1200, "trace-test": 800}, gate_rows["gate.verdict"]
    assert [row["exit_code"] for row in proc_rows] == [0], proc_rows
    # A `git-trace2.jsonl` left on disk by the recorder removed on 2026-11-17 is not read.
    assert git_rows == [], git_rows
    return "loop-directory proc read ok, a stale git trace2 stream ignored, gate indexed"


def test_bash_shim_records_a_command_and_keeps_the_exit_code(work):
    """The shim writes a `proc.exec` line the indexer parses, and nothing else.

    A unit test that only asserted the environment is set would prove nothing: this runs
    a real `bash -c` under the environment `run-pi` exports, reads the line it wrote, and
    indexes it.
    """
    run_id = "run-shim-1"
    trace_root = work / "shim-trace"
    store = work / "shim-repo" / ".tillr" / "state.db"
    loop_dir = store.parent / "loop" / run_id
    loop_dir.mkdir(parents=True)
    make_store(store, [{"run_id": run_id, "item_id": "574", "lane_id": "lane-1"}])

    env = dict(os.environ,
               BASH_ENV=str(TOOL / "bash-shim.sh"),
               TILLR_TRACE_DIR=str(loop_dir),
               TILLR_RUN_ID=run_id,
               TILLR_LANE_ID="lane-1",
               TILLR_REPO_ROOT=str(ROOT))
    result = subprocess.run(
        ["bash", "-c", "cd tools/tillr-store && false; echo after"],
        cwd=str(ROOT), env=env, capture_output=True, text=True)
    assert result.returncode == 0, (result.returncode, result.stderr)
    assert result.stdout == "after\n", result.stdout
    assert result.stderr == "", result.stderr

    lines = (loop_dir / "proc-exec.jsonl").read_text().splitlines()
    rows = [json.loads(line) for line in lines]
    assert all(len(line.encode()) < 2048 for line in lines), [len(line) for line in lines]
    commands = [row["cmd"] for row in rows]
    assert commands == ["cd tools/tillr-store", "false", "echo after"], commands
    failed = rows[1]
    assert failed["exit_code"] == 1, failed
    assert failed["repo_rel_cwd"] == "tools/tillr-store", failed
    assert failed["cwd"] == str(ROOT / "tools/tillr-store"), failed
    assert failed["run_id"] == run_id and failed["lane_id"] == "lane-1", failed
    assert failed["span_id"].startswith("sh-"), failed

    # Fail-open (§7.1 guarantees 1 and 2): a trace directory that cannot be written
    # leaves the command's exit code, stdout and stderr exactly as they were.
    broken_env = dict(env, TILLR_TRACE_DIR=str(work / "not-a-directory"))
    (work / "not-a-directory").write_text("a file, not a directory\n")
    broken = subprocess.run(["bash", "-c", "echo still-ok; exit 5"],
                            cwd=str(ROOT), env=broken_env,
                            capture_output=True, text=True)
    assert broken.returncode == 5, (broken.returncode, broken.stderr)
    assert broken.stdout == "still-ok\n", broken.stdout
    assert broken.stderr == "", broken.stderr
    return ("the shim wrote 3 proc.exec rows and the"
            " shell's own exit code survived")


def shim_env(loop_dir, run_id="run-shell-1", **extra):
    """The environment `run-pi` exports, over the real shim. `extra` overrides it."""
    return dict(os.environ, BASH_ENV=str(TOOL / "bash-shim.sh"),
                TILLR_TRACE_DIR=str(loop_dir), TILLR_RUN_ID=run_id,
                TILLR_LANE_ID="lane-1", **extra)


def shim_rows(loop_dir):
    return [json.loads(line) for line in
            (loop_dir / "proc-exec.jsonl").read_text().splitlines() if line.strip()]


def test_shim_stamps_utc_in_any_host_zone(work):
    """`ts` carries `Z`, so it must be the UTC clock, not the shell's local one.

    The §8.3 join compares this stamp against pi's real UTC `tool.call` times. A stamp
    formatted in local time under a `Z` label puts a suite that genuinely ran *after*
    the last edit *before* it on every host that is not already on UTC, and the owed
    suite is then reported `disproved`. Measured on a `TZ=America/New_York` host before
    the fix: the row said 01:01:19Z for a command `date -u` printed 06:01:19Z.
    """
    failures = []
    for zone in ("America/New_York", "Asia/Tokyo"):
        loop_dir = work / f"tz-loop-{zone.replace('/', '-')}"
        loop_dir.mkdir()
        before = time.time()
        result = subprocess.run(
            ["bash", "-c", "true"], env=shim_env(loop_dir, TZ=zone),
            capture_output=True, text=True)
        assert result.returncode == 0, (zone, result.returncode, result.stderr)
        row = shim_rows(loop_dir)[-1]
        assert row["ts"].endswith("Z"), (zone, row)
        stamp = datetime.datetime.strptime(
            row["ts"], "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=datetime.timezone.utc)
        skew = stamp.timestamp() - before
        if abs(skew) >= 5:
            failures.append(f"TZ={zone}: row ts {row['ts']} is {skew:.0f}s from the real"
                            f" UTC clock")
    assert not failures, failures
    return "both non-UTC zones stamp the real UTC clock within 5 s"


def test_shim_stamp_is_utc_across_the_date_boundaries(work):
    """The stamp's own conversion is checked where a hand-written one goes wrong.

    The shim reads a clock it cannot be handed, so the formatter is taken from the
    shipped file and asked for the epochs a civil-date decomposition gets wrong: the
    epoch itself, the last second of a day, a leap day, a month and year boundary.
    """
    body = (TOOL / "bash-shim.sh").read_text()
    start = body.index("__tillr_proc_iso() {")
    script = work / "iso-boundaries.sh"
    epochs = ["0", "59", "86399", "86400", "951782400", "1709164800",
              "1709168400", "4102444800", "1794550017"]
    script.write_text(body[start:body.index("\n}\n", start) + 3] + "".join(
        f'__tillr_proc_iso "{epoch}.500000"\nprintf "%s %s\\n" "{epoch}" '
        '"$__tillr_proc_ts"\n' for epoch in epochs))
    # The formatter is the subject, not the shim: if this test process itself runs under
    # `BASH_ENV`, the shim's DEBUG trap would overwrite `__tillr_proc_ts` before each
    # printf and the case would fail with the wall clock instead of the conversion.
    env = {key: value for key, value in os.environ.items() if key != "BASH_ENV"}
    result = subprocess.run(["bash", str(script)], capture_output=True, text=True, env=env)
    assert result.returncode == 0, result.stderr
    lines = result.stdout.splitlines()
    assert len(lines) == len(epochs), (lines, result.stderr)
    for line in lines:
        epoch, stamp = line.split()
        want = datetime.datetime.fromtimestamp(
            int(epoch), datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S") + ".500Z"
        assert stamp == want, (epoch, stamp, want)
    return f"{len(epochs)} epochs, leap day and year boundary included, convert to UTC"


def test_empty_proc_exec_is_absent_evidence_not_proof(work):
    """A 0-byte `proc-exec.jsonl` answers `unverified`, never `disproved`.

    Read as `ok`, an empty file made `have_proc` true and turned every owed suite into
    "no suite ran at all" — absence of evidence promoted to proof, which §8.3 forbids
    and the truncated-file path already gets right.
    """
    run_id = "run-emptyproc-1"
    root = work / "emptyproc-root"
    run_dir = root / "runs" / run_id
    run_dir.mkdir(parents=True)
    write_log(run_dir / f"{run_id}.log", run_id, [
        {"type": "request_start", "timestamp": 1794510838000},
        {"type": "tool_execution_start", "toolCallId": "call-edit-1", "toolName": "edit",
         "args": {"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
        {"type": "tool_execution_end", "toolCallId": "call-edit-1", "toolName": "edit",
         "result": {"content": [{"type": "text", "text": "ok"}]},
         "isError": False, "durationMs": 3, "timestamp": 1794510840000},
        turn_end(1, "Edited the store, then ran the suite.\nRESULT: done",
                 ts_ms=1794510845000),
    ])
    (run_dir / "proc-exec.jsonl").write_text("")
    (run_dir / "manifest.json").write_text(json.dumps(
        {"run_id": run_id, "base_sha": "a" * 40, "head_sha": "c" * 40,
         "tree_sha": "b" * 40, "tick_id": "tick-1"}))
    store = work / "emptyproc-store.db"
    make_store(store, [{"run_id": run_id, "item_id": "574", "lane_id": "lane-1"}])

    parsed = tillr_trace.parse_proc_exec(
        run_dir / "proc-exec.jsonl", {"run_id": run_id})
    assert parsed.state == "missing", (parsed.state, parsed.detail)
    assert parsed.rows.rows == [], parsed.rows.rows

    audit = cli("--trace-root", str(root), "--store", str(store), "audit", run_id)
    assert audit.returncode == 0, audit.stderr
    assert "owed_suite" in audit.stdout, audit.stdout
    assert "unverified" in audit.stdout, audit.stdout
    assert "no proc.exec record" in audit.stdout, audit.stdout
    assert "disproved" not in audit.stdout, audit.stdout
    return "an empty proc-exec.jsonl reads as missing, so the owed suite is unverified"


def test_shim_writes_one_row_per_command_including_the_last(work):
    """An explicit `exit N` is recorded once, and the shell still exits N."""
    loop_dir = work / "exitrow-loop"
    loop_dir.mkdir()
    plain = work / "exitrow-plain.sh"
    plain.write_text("set -euo pipefail\necho one\nexit 0\n")
    seven = work / "exitrow-seven.sh"
    seven.write_text("echo out\necho err >&2\nexit 7\n")

    ok = subprocess.run(["bash", str(plain)], env=shim_env(loop_dir),
                        capture_output=True, text=True)
    assert ok.returncode == 0, (ok.returncode, ok.stderr)
    assert ok.stdout == "one\n", ok.stdout
    assert ok.stderr == "", ok.stderr

    failed = subprocess.run(["bash", str(seven)], env=shim_env(loop_dir),
                            capture_output=True, text=True)
    assert failed.returncode == 7, (failed.returncode, failed.stderr)
    assert failed.stdout == "out\n", failed.stdout
    assert failed.stderr == "err\n", failed.stderr

    rows = shim_rows(loop_dir)
    commands = [row["cmd"] for row in rows]
    assert commands == ["set -euo pipefail", "echo one", "exit 0",
                        "echo out", "echo err 1>&2", "exit 7"], commands
    assert [row["exit_code"] for row in rows] == [0, 0, 0, 0, 0, 7], rows
    return "six commands wrote six rows, and the scripts still exited 0 and 7"


def test_shim_adds_no_trace_lines_to_an_x_shell(work):
    """`set -x` in a run's shell shows the run's commands and no shim internals."""
    loop_dir = work / "xtrace-loop"
    loop_dir.mkdir()

    def xtrace(script):
        result = subprocess.run(["bash", "-x", "-c", script],
                                env=shim_env(loop_dir), capture_output=True, text=True)
        assert result.returncode == 0, (result.returncode, result.stderr)
        return result

    one = xtrace("true")
    two = xtrace("true; true")
    # One more command, one more traced line: everything else in either transcript is the
    # shim's one-time load. Before the traps were wrapped, each command added ~60 lines.
    assert len(two.stderr.splitlines()) - len(one.stderr.splitlines()) == 1, (
        one.stderr, two.stderr)

    # A command's own stderr is not part of the shim's silence.
    noisy = xtrace("echo keep-this >&2")
    assert noisy.stderr.splitlines()[-1] == "keep-this", noisy.stderr
    assert [row["cmd"] for row in shim_rows(loop_dir)] == [
        "true", "true", "true", "echo keep-this 1>&2"], shim_rows(loop_dir)
    return "a `bash -x` shell gains one line per command, none of them the shim's"


def index_rows(root):
    """Every obs row's identity and order, which is what a rebuild disturbs."""
    conn = sqlite3.connect(str(root / "index.db"))
    try:
        return conn.execute(
            "SELECT obs_id, seq, kind FROM obs_events ORDER BY seq").fetchall()
    finally:
        conn.close()


def increment_fixture(work, name, lines, proc_rows=None):
    """A trace root and a store holding one run, ready to index."""
    root = work / name
    run_dir = write_trace_run(root, f"run-{name}", log_lines=lines, proc_rows=proc_rows)
    store = work / f"{name}-store" / ".tillr" / "state.db"
    make_store(store, [{"run_id": f"run-{name}", "item_id": "574", "lane_id": "lane-1"}])
    return root, run_dir, store


def test_ingest_parses_only_what_changed(work):
    """A digest with nothing new parses nothing, and the store never triggers a build.

    The store's modification time moves on every tick by design, so a watermark that
    included it made every tick rebuild the whole corpus — measured on the live index,
    968040 rows were deleted and re-inserted from 6.9 GB of artifacts. The watermark
    covers the artifacts the build reads; the store is read on every sync instead.
    """
    root, run_dir, store = increment_fixture(work, "quiet", [
        turn_end(1, "first\nRESULT: done", ts_ms=1794510845000)])
    index = tillr_trace.Index(root, store)
    assert index.sync() == "rebuilt"
    first = index_rows(root)

    # The store's own mtime moves on every tick; that is the whole reason the watermark
    # must not include it.
    os.utime(store, (time.time() + 60, time.time() + 60))
    calls = []
    original = tillr_trace.parse_run_log
    tillr_trace.parse_run_log = lambda *args, **kwargs: calls.append(args[0]) or original(
        *args, **kwargs)
    try:
        assert index.sync() == "reused"
    finally:
        tillr_trace.parse_run_log = original
    assert calls == [], calls
    assert index_rows(root) == first, "a reused sync changed the index"

    conn = sqlite3.connect(str(root / "index.db"))
    try:
        paths = [row[0] for row in conn.execute("SELECT path FROM ingested_files")]
    finally:
        conn.close()
    assert str(store) not in paths, paths
    assert str(run_dir / "run-quiet.log") in paths, paths
    return "a second sync over an unchanged corpus parsed nothing and reused the index"


def test_ingest_appends_only_the_new_lines(work):
    """A new line is appended; the rows already stored are neither re-read nor renumbered."""
    root, run_dir, store = increment_fixture(work, "append", [
        turn_end(1, "first", ts_ms=1794510845000)])
    index = tillr_trace.Index(root, store)
    assert index.sync() == "rebuilt"
    before = index_rows(root)

    # The prefix is overwritten with a broken line of the same length before the file
    # grows. A build that re-read the file from zero would report it corrupt; a build
    # that resumes at the recorded offset never looks at it.
    log = run_dir / "run-append.log"
    with open(log, "r+") as handle:
        handle.seek(0)
        handle.write('{"type":"WRECKED-prefix')
    with open(log, "a") as handle:
        handle.write(json.dumps(turn_end(2, "second", ts_ms=1794510855000)) + "\n")

    assert index.sync() == "rebuilt"
    after = index_rows(root)
    assert len(after) == len(before) + 1, (before, after)
    assert all(row in after for row in before), "an appended line renumbered a stored row"
    assert len([row for row in after if row[2] == "llm.turn"]) == 2, after
    return "an appended line added one row and left the stored ones untouched"


def test_ingest_rebuilds_a_truncated_source(work):
    """A source that got shorter is read from zero, and its old rows are replaced."""
    root, run_dir, store = increment_fixture(work, "trunc", [
        turn_end(1, "first", ts_ms=1794510845000),
        turn_end(2, "second", ts_ms=1794510855000)])
    index = tillr_trace.Index(root, store)
    assert index.sync() == "rebuilt"
    assert len(index_rows(root)) == 3, index_rows(root)

    write_log(run_dir / "run-trunc.log", "run-trunc",
              [turn_end(1, "only", ts_ms=1794510845000)])
    assert index.sync() == "rebuilt"
    rows = index_rows(root)
    assert len(rows) == 2, rows
    assert len({row[0] for row in rows}) == 2, rows
    return "a shortened source replaced its rows instead of doubling them"


def test_ingest_keeps_a_pair_split_across_builds(work):
    """A call whose start and end land in different builds keeps its arguments.

    The live index is appended to while a run is still writing, so a build can land
    between a `tool_execution_start` and its end, or between a command's start and
    exit. What crosses that boundary travels in the source's carry; without it the second
    build sees an end with no start and the row loses the path the claim join reads.
    """
    root = work / "split-root"
    run_dir = root / "runs" / "run-split"
    run_dir.mkdir(parents=True)
    log = run_dir / "run-split.log"
    log.write_text("\n".join(json.dumps(line) for line in [
        {"type": "session", "version": 3, "id": "session-split",
         "timestamp": "2026-11-12T19:00:00.000Z", "cwd": "/w/lane-1/worktree"},
        {"type": "request_start", "timestamp": 1794510838000},
        {"type": "tool_execution_start", "toolCallId": "call-edit-1", "toolName": "edit",
         "args": {"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
    ]) + "\n")
    (run_dir / "base_sha").write_text("a" * 40 + "\n")
    store = work / "split-store" / ".tillr" / "state.db"
    make_store(store, [{"run_id": "run-split", "item_id": "574", "lane_id": "lane-1"}])

    index = tillr_trace.Index(root, store)
    assert index.sync() == "rebuilt"

    with open(log, "a") as handle:
        handle.write(json.dumps(
            {"type": "tool_execution_end", "toolCallId": "call-edit-1", "toolName": "edit",
             "result": {"content": [{"type": "text", "text": "ok"}]},
             "isError": False, "durationMs": 3, "timestamp": 1794510840000}) + "\n")
        handle.write(json.dumps(turn_end(1, "edited", ts_ms=1794510845000)) + "\n")
    assert index.sync() == "rebuilt"

    conn = sqlite3.connect(str(root / "index.db"))
    try:
        tool = [json.loads(payload) for (payload,) in conn.execute(
            "SELECT payload FROM obs_events WHERE kind = 'tool.call'")]
    finally:
        conn.close()
    assert len(tool) == 1, tool
    assert tool[0]["target_path"] == "/w/lane-1/worktree/tools/tillr-store/read.go", tool
    assert tool[0]["ts_source"] == "derived_from_preceding_clock", tool
    return "a tool call split across two builds kept its arguments"


def test_ingest_streams_a_large_source(work):
    """A source is parsed one line at a time, so peak memory is the index, not the file.

    The fixture is dominated by lines that yield no row, so a parse that materialised the
    file first would peak at the file's size while a streaming one peaks at one line.
    """
    run_dir = work / "stream-root" / "runs" / "run-stream"
    run_dir.mkdir(parents=True)
    log = run_dir / "run-stream.log"
    filler = "x" * (64 * 1024)
    with open(log, "w") as handle:
        handle.write(json.dumps({"type": "session", "version": 3, "id": "session-stream",
                                 "timestamp": "2026-11-12T19:00:00.000Z"}) + "\n")
        for _ in range(512):
            handle.write(json.dumps({"type": "message_update", "delta": filler}) + "\n")
        handle.write(json.dumps(turn_end(1, "done", ts_ms=1794510845000)) + "\n")
    size = log.stat().st_size
    assert size > 32 * 1024 * 1024, size

    tracemalloc.start()
    try:
        parsed = tillr_trace.parse_run_log(log, {"run_id": "run-stream"})
        _, peak = tracemalloc.get_traced_memory()
    finally:
        tracemalloc.stop()
    assert parsed.state == "ok", (parsed.state, parsed.detail)
    assert parsed.summary["turns"] == 1, parsed.summary
    assert peak < size // 4, (peak, size)
    return (f"a {size // (1024 * 1024)} MiB log peaked at {peak // 1024} KiB while parsing")


def test_ingest_drops_a_run_its_sources_no_longer_hold(work):
    """A run the store dropped and no directory remains for leaves the index too.

    The index is a shadow of its sources, so a row nothing on disk supports is a lie. The
    store is checked first: a store that cannot be read is not evidence that its runs are
    gone, and pruning on one would empty the index over a transient lock.
    """
    root, run_dir, store = increment_fixture(work, "vanish", [
        turn_end(1, "first\nRESULT: done", ts_ms=1794510845000)])
    index = tillr_trace.Index(root, store)
    assert index.sync() == "rebuilt"
    assert index_rows(root), index_rows(root)

    conn = sqlite3.connect(str(store))
    try:
        conn.execute("DELETE FROM runs WHERE run_id = ?", ("run-vanish",))
        conn.commit()
    finally:
        conn.close()
    shutil.rmtree(run_dir)
    assert index.sync() == "rebuilt"
    assert index_rows(root) == [], index_rows(root)
    return "a run whose store row and directory were removed left the index with it"


def test_ingest_twice_leaves_the_first_index_alone(work):
    """The second `ingest` leaves the first run's index alone, and writes no default one.

    The default trace root is a marker: a case that forgot `--trace-root` would write it,
    so this asserts the suite's own isolation as well as the second run's reuse.
    """
    root, run_dir, store = increment_fixture(work, "twice", [
        turn_end(1, "first\nRESULT: done", ts_ms=1794510845000)])
    first = cli("--trace-root", str(root), "--store", str(store), "ingest")
    assert first.returncode == 0, first.stderr
    before = index_rows(root)

    second = cli("--trace-root", str(root), "--store", str(store), "ingest")
    assert second.returncode == 0, second.stderr
    assert "reused" in second.stdout, second.stdout
    assert index_rows(root) == before, "the second ingest rewrote the index"

    shared = Path(os.environ["TILLR_TRACE_ROOT"])
    assert not (shared / "index.db").exists(), (
        f"a case wrote the suite's default trace root at {shared}")
    return "two ingests left the first index untouched and the default root unwritten"


def loop_corpus(work, name, run_ids):
    """A store and a loop directory holding every artifact kind for each run."""
    store = work / f"{name}-repo" / ".tillr" / "state.db"
    for number, run_id in enumerate(run_ids, start=1):
        loop_dir = store.parent / "loop" / run_id
        loop_dir.mkdir(parents=True)
        write_log(loop_dir / f"{run_id}.log", run_id, [
            turn_end(1, "Ran the suite.\nRESULT: done", ts_ms=1794510845000 + number)])
        (loop_dir / "proc-exec.jsonl").write_text(json.dumps(proc(
            "2026-11-12T19:20:02.000Z", ["go", "test", "./..."], "tools/tillr-store")) + "\n")
        (loop_dir / "base_sha").write_text("a" * 40 + "\n")
        (loop_dir / "gate.plan.json").write_text(json.dumps({
            "record_version": 1, "kind": "gate.plan", "id": f"gate-plan-{number}",
            "created_at": "2026-11-12T19:20:04Z", "payload": {
                "plan_id": f"plan-{number}", "checks": [{"check_id": "store-test"}]}}))
    make_store(store, [{"run_id": run_id, "item_id": "574", "lane_id": "lane-1"}
                       for run_id in run_ids])
    return work / f"{name}-trace", store


def table_scans(index_path, statements):
    """The statements among `statements` that would walk the whole obs_events table."""
    conn = sqlite3.connect(f"file:{index_path}?mode=ro", uri=True)
    scans = []
    try:
        for sql in statements:
            head = sql.lstrip().split(None, 1)[0].upper() if sql.strip() else ""
            if head not in ("SELECT", "DELETE", "UPDATE") or "obs_events" not in sql:
                continue
            steps = [row[3] for row in conn.execute("EXPLAIN QUERY PLAN " + sql)]
            # A walk is a `SCAN`, or a `SEARCH` that names no index: the planner reports
            # `MAX(col)` over an unindexed column as `SEARCH obs_events`.
            if any("obs_events" in step and (step.startswith("SCAN") or "USING" not in step)
                   for step in steps):
                scans.append((sql, " | ".join(steps)))
    finally:
        conn.close()
    return scans


class traced_connections:
    """Record every statement any sqlite connection the indexer opens executes."""

    def __init__(self):
        self.statements = []
        self.original = sqlite3.connect

    def __enter__(self):
        original, statements = self.original, self.statements

        def connect(*args, **kwargs):
            conn = original(*args, **kwargs)
            conn.set_trace_callback(statements.append)
            return conn

        tillr_trace.sqlite3.connect = connect
        return self

    def __exit__(self, *exc):
        tillr_trace.sqlite3.connect = self.original
        return False


def test_ingest_cost_follows_what_is_new(work):
    """A sync that appends one line walks no table whole, and leaves unchanged runs unread.

    Measured on 2026-11-17: the live index held 27 million obs rows (32 GB), and every
    sync read the highest `seq` with a full scan and deleted a dirty run's gate rows with
    `DELETE ... WHERE source = ?`, which has no index. Each was a pass over the whole
    table per sync, so a sync's cost followed the index's size instead of what was new.
    The query planner is asked, so no clock is involved.
    """
    root, store = loop_corpus(work, "scans", ["run-scan-1", "run-scan-2"])
    index = tillr_trace.Index(root, store)
    assert index.sync() == "rebuilt"

    log = store.parent / "loop" / "run-scan-1" / "run-scan-1.log"
    with open(log, "a") as handle:
        handle.write(json.dumps(turn_end(2, "again", ts_ms=1794510855000)) + "\n")
    calls = []
    original = tillr_trace.parse_gate_record
    tillr_trace.parse_gate_record = lambda *args, **kwargs: calls.append(args[0]) or original(
        *args, **kwargs)
    try:
        with traced_connections() as traced:
            assert index.sync() == "rebuilt"
    finally:
        tillr_trace.parse_gate_record = original
    assert calls == [], f"unchanged gate records were parsed again: {calls}"
    scans = table_scans(root / "index.db", traced.statements)
    assert scans == [], "a sync walked the whole obs_events table:\n" + "\n".join(
        f"  {sql}\n    -> {plan}" for sql, plan in scans)
    llm = [row for row in index_rows(root) if row[2] == "llm.turn"]
    assert len(llm) == 3, llm
    return f"{len(traced.statements)} statements, none of them a whole-table walk"


def test_an_ingest_killed_mid_write_is_resumed_not_rebuilt(work):
    """A writer killed mid-transaction leaves an index the next sync resumes from.

    Measured on 2026-11-17: a jail relaunch killed an ingest mid-transaction and left a
    hot rollback journal. The next sync's read-only open of the index failed on it
    ("attempt to write a readonly database"), was read as "no index", and started a
    whole-corpus rebuild that ran for half an hour. Every kill of that rebuild left
    another hot journal, so each later ingest started the same rebuild again.
    """
    root, store = loop_corpus(work, "killed", ["run-killed-1", "run-killed-2"])
    index = tillr_trace.Index(root, store)
    assert index.sync() == "rebuilt"
    before = index_rows(root)

    killed = subprocess.run([sys.executable, "-I", "-c", (
        "import os, sqlite3, sys\n"
        "conn = sqlite3.connect(sys.argv[1])\n"
        "conn.execute('PRAGMA cache_size = 1')\n"
        "conn.execute('BEGIN IMMEDIATE')\n"
        "conn.execute('DELETE FROM obs_events')\n"
        "conn.execute('DELETE FROM ingested_files')\n"
        "conn.executemany('INSERT INTO ingested_files (path, mtime_ns, size_bytes)'\n"
        "                 ' VALUES (?, 0, 0)', [(str(n) * 400,) for n in range(2000)])\n"
        "os._exit(9)\n"), str(root / "index.db")], capture_output=True, text=True)
    assert killed.returncode == 9, killed.stderr

    calls = []
    parsers = {name: getattr(tillr_trace, name) for name in (
        "parse_run_log", "parse_proc_exec", "parse_gate_record")}
    for name, parser in parsers.items():
        setattr(tillr_trace, name, lambda *args, _parser=parser, **kwargs: calls.append(
            args[0]) or _parser(*args, **kwargs))
    try:
        state = index.sync()
    finally:
        for name, parser in parsers.items():
            setattr(tillr_trace, name, parser)
    assert calls == [], f"a sync after a killed writer re-parsed the corpus: {calls}"
    assert state == "reused", state
    assert index_rows(root) == before, "the killed writer's half-done work reached the index"
    return "a killed writer's transaction was discarded and nothing was re-parsed"


def test_a_failure_part_way_keeps_the_runs_already_indexed(work):
    """Each run is committed as it is derived, so a stop loses one run, not the build.

    The whole sync was one transaction: an ingest stopped after deriving most of the
    corpus kept none of it, and the next one began again from nothing.
    """
    root, store = loop_corpus(work, "partial", ["run-part-1", "run-part-2"])
    index = tillr_trace.Index(root, store)
    original = tillr_trace.parse_proc_exec

    def failing(path, trace, **kwargs):
        if trace.get("run_id") == "run-part-2":
            # An interrupt, not a parser defect: a parser that raises an ordinary
            # exception is skipped with a reason (test_one_unparseable_run_...).
            raise KeyboardInterrupt("stopped part way")
        return original(path, trace, **kwargs)

    tillr_trace.parse_proc_exec = failing
    try:
        index.sync()
    except KeyboardInterrupt:
        pass
    else:
        raise AssertionError("the injected failure did not stop the sync")
    finally:
        tillr_trace.parse_proc_exec = original

    calls = []
    log_parser = tillr_trace.parse_run_log
    tillr_trace.parse_run_log = lambda *args, **kwargs: (
        calls.append(str(args[0])) or log_parser(*args, **kwargs))
    try:
        assert index.sync() == "rebuilt"
    finally:
        tillr_trace.parse_run_log = log_parser
    assert all("run-part-1" not in path for path in calls), calls
    assert any("run-part-2" in path for path in calls), calls
    conn = sqlite3.connect(str(root / "index.db"))
    try:
        runs = {row[0] for row in conn.execute("SELECT run_id FROM obs_events")}
    finally:
        conn.close()
    assert {"run-part-1", "run-part-2"} <= runs, runs
    return "the run derived before the stop was kept and not parsed again"


def test_the_log_the_run_record_names_is_indexed(work):
    """A run's log is read from where its record says, in the record's own spelling.

    The run record names its log as `{"kind": "log", "ref": <path>}` in a lane directory,
    not in the loop directory. The indexer read the key as `Ref`, found nothing, and
    looked in the loop directory instead: measured on 2026-11-17, all 789 runs in the
    live index read `log_state = missing`, and the index held no model turn at all.
    """
    store = work / "named-log-repo" / ".tillr" / "state.db"
    run_id = "run-named-log"
    lane_log = work / "named-log-repo" / ".tillr" / "lanes" / "lane-1" / f"{run_id}.log"
    lane_log.parent.mkdir(parents=True)
    write_log(lane_log, run_id, [turn_end(1, "done", ts_ms=1794510845000)])
    (store.parent / "loop" / run_id).mkdir(parents=True)
    make_store(store, [{"run_id": run_id, "item_id": "574", "lane_id": "lane-1",
                        "artifact_refs": [{"kind": "lane-root", "ref": str(lane_log.parent)},
                                          {"kind": "log", "ref": str(lane_log)}]}])
    root = work / "named-log-trace"
    assert tillr_trace.Index(root, store).sync() == "rebuilt"
    conn = sqlite3.connect(str(root / "index.db"))
    try:
        log_path, log_state = conn.execute(
            "SELECT log_path, log_state FROM runs_index WHERE run_id = ?", (run_id,)).fetchone()
        turns = conn.execute(
            "SELECT COUNT(*) FROM obs_events WHERE kind = 'llm.turn'").fetchone()[0]
    finally:
        conn.close()
    assert (log_path, log_state) == (str(lane_log), "ok"), (log_path, log_state)
    assert turns == 1, turns
    return "the lane log the run record names was read, and its turn indexed"


def test_a_second_ingest_does_not_wait_for_a_running_one(work):
    """An ingest that finds another one writing answers `busy` at once.

    Two writers of one index either wait on each other's lock or both parse the same new
    bytes. The writer holds an exclusive lock beside the index for its whole sync, and a
    second one answers without touching the index: the running one, or the next one,
    picks up what is new.
    """
    import fcntl

    root, store = loop_corpus(work, "busy", ["run-busy-1"])
    first = cli("--trace-root", str(root), "--store", str(store), "ingest")
    assert first.returncode == 0, first.stderr
    before = index_rows(root)
    log = store.parent / "loop" / "run-busy-1" / "run-busy-1.log"
    with open(log, "a") as handle:
        handle.write(json.dumps(turn_end(2, "again", ts_ms=1794510855000)) + "\n")
    with open(root / "index.db.lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        second = cli("--trace-root", str(root), "--store", str(store), "ingest")
    assert second.returncode == 0, second.stderr
    assert "busy" in second.stdout, second.stdout
    assert index_rows(root) == before, "a second writer changed the index"
    third = cli("--trace-root", str(root), "--store", str(store), "ingest")
    assert "rebuilt" in third.stdout, third.stdout
    return "a second ingest answered busy at once, and the next one caught up"



def test_an_index_of_the_git_trace2_layout_is_rebuilt(work):
    """An index built before git trace2 recording was removed is deleted, not read.

    Layout 3 held `git.trace2` rows. The ruling of 2026-11-17 removed them, so the layout
    number moved and the existing derived index must come back without them.
    """
    root = work / "retired-trace"
    run_dir = root / "runs" / "run-old"
    run_dir.mkdir(parents=True)
    write_log(run_dir / "run-old.log", "run-old", [turn_end(1, "done", ts_ms=1794510845000)])
    (run_dir / "proc-exec.jsonl").write_text(json.dumps(proc(
        "2026-11-12T19:20:02.000Z", ["ls"], "tools")) + "\n")
    store = work / "retired-store" / ".tillr" / "state.db"
    make_store(store, [{"run_id": "run-old", "item_id": "1", "lane_id": "lane-1"}])
    assert tillr_trace.Index(root, store).sync() == "rebuilt"
    conn = sqlite3.connect(str(root / "index.db"))
    conn.execute("UPDATE obs_events SET kind = 'git.trace2' WHERE kind = 'proc.exec'")
    conn.execute("PRAGMA user_version = 3")
    conn.commit()
    conn.close()
    assert tillr_trace.Index(root, store).sync() == "rebuilt"
    conn = sqlite3.connect(str(root / "index.db"))
    try:
        kinds = {kind for (kind,) in conn.execute("SELECT kind FROM obs_events")}
        version = conn.execute("PRAGMA user_version").fetchone()[0]
    finally:
        conn.close()
    assert "git.trace2" not in kinds, kinds
    assert version == tillr_trace.SCHEMA_VERSION, version
    return "a layout-3 index holding git.trace2 rows was deleted and rebuilt without them"


def test_a_tool_result_shaped_as_text_is_still_a_row(work):
    """Real `pi --mode json` logs carry `structuredContent` as a string for some tools.

    Measured 2026-11-17 over the loop's own run logs: 2,091 of 5,316 `tool_execution_end`
    events held a string there (a skill's text, for one). The parser read it as an object
    and raised, which aborted every ingest. The row is kept, with the fields that are
    present, and no exit code it cannot read.
    """
    root = work / "textshape-root"
    run_dir = root / "runs" / "run-text"
    run_dir.mkdir(parents=True)
    write_log(run_dir / "run-text.log", "run-text", [
        {"type": "tool_execution_start", "toolCallId": "c1", "toolName": "read",
         "args": {"path": "/w/a.md"}},
        {"type": "tool_execution_end", "toolCallId": "c1", "toolName": "read",
         "result": {"content": [{"type": "text", "text": "x"}],
                    "structuredContent": "---\nname: lane-work\n"},
         "isError": False, "durationMs": 3},
        {"type": "tool_execution_start", "toolCallId": "c2", "toolName": "bash",
         "args": "ls"},
        {"type": "tool_execution_end", "toolCallId": "c2", "toolName": "bash",
         "result": "plain text result", "isError": True, "durationMs": 4},
        {"type": "tool_execution_start", "toolCallId": "c3", "toolName": "bash",
         "args": {"command": "ls"}},
        {"type": "tool_execution_end", "toolCallId": "c3", "toolName": "bash",
         "result": {"structuredContent": {"exit_code": 7}}, "isError": True,
         "durationMs": 5},
        turn_end(1, "done", ts_ms=1794510845000),
    ])
    store = work / "textshape-store" / ".tillr" / "state.db"
    make_store(store, [{"run_id": "run-text", "item_id": "1", "lane_id": "lane-1"}])
    result = cli("--trace-root", str(root), "--store", str(store), "ingest")
    assert result.returncode == 0, result.stderr
    conn = sqlite3.connect(str(root / "index.db"))
    try:
        calls = {json.loads(payload)["tool_call_id"]: json.loads(payload)
                 for (payload,) in conn.execute(
                     "SELECT payload FROM obs_events WHERE kind = 'tool.call'")}
    finally:
        conn.close()
    assert set(calls) == {"c1", "c2", "c3"}, calls
    assert calls["c1"]["target_path"] == "/w/a.md" and calls["c1"]["exit_code"] is None
    assert calls["c2"]["is_error"] is True and calls["c2"]["exit_code"] is None, calls["c2"]
    assert calls["c3"]["exit_code"] == 7, calls["c3"]
    return "string-shaped structuredContent, args and result still produced tool.call rows"


def test_one_unparseable_run_does_not_abort_the_ingest(work):
    """A parser that raises on one run skips that run with a reason; the rest proceed."""
    root = work / "poison-root"
    for run_id in ("run-bad", "run-good"):
        run_dir = root / "runs" / run_id
        run_dir.mkdir(parents=True)
        write_log(run_dir / f"{run_id}.log", run_id, [turn_end(1, "done", ts_ms=1794510845000)])
    store = work / "poison-store" / ".tillr" / "state.db"
    make_store(store, [{"run_id": r, "item_id": "1", "lane_id": "lane-1"}
                       for r in ("run-bad", "run-good")])
    real = tillr_trace.parse_run_log

    def poisoned(path, trace, *args, **kwargs):
        if "run-bad" in str(path):
            raise AttributeError("'str' object has no attribute 'get'")
        return real(path, trace, *args, **kwargs)

    tillr_trace.parse_run_log = poisoned
    try:
        assert tillr_trace.Index(root, store).sync() == "rebuilt"
    finally:
        tillr_trace.parse_run_log = real
    conn = sqlite3.connect(str(root / "index.db"))
    try:
        turns = {run for (run,) in conn.execute(
            "SELECT run_id FROM obs_events WHERE kind = 'llm.turn'")}
        unreadable = [(run, json.loads(payload)) for run, payload in conn.execute(
            "SELECT run_id, payload FROM obs_events WHERE kind = 'unreadable'")]
    finally:
        conn.close()
    assert turns == {"run-good"}, turns
    assert [run for run, _ in unreadable] == ["run-bad"], unreadable
    assert "AttributeError" in json.dumps(unreadable[0][1]), unreadable
    return "a run whose log parse raised was recorded unreadable and the other run indexed"


def main():
    if not DURABLE.is_dir():
        DURABLE.mkdir(parents=True, exist_ok=True)
    failures = []
    with tempfile.TemporaryDirectory(prefix="tillr-trace-test-", dir=DURABLE) as work:
        work = Path(work)
        # The suite's own trace root, inside the case's temporary directory. A case that
        # names no --trace-root then writes here instead of the live default index, and
        # the last case asserts that this root was never written at all.
        os.environ["TILLR_TRACE_ROOT"] = str(work / "default-trace-root")
        cases = [
            ("parser", lambda: test_parser_recovers_measured_totals()),
            ("ingest", lambda: test_ingest_builds_the_index_and_names_it(work)),
            ("cost", lambda: test_cost_attributes_tokens_to_lane_and_route(work)),
            ("stale", lambda: test_discrepancies_flags_a_suite_that_ran_before_the_last_edit(work)),
            ("fresh", lambda: test_discrepancies_does_not_flag_a_suite_that_ran_after_the_last_edit(work)),
            ("unreadable", lambda: test_missing_artifact_is_unreadable_not_a_crash(work)),
            ("readonly", lambda: test_store_is_opened_read_only(work)),
            ("retired-layout", lambda: test_an_index_of_the_git_trace2_layout_is_rebuilt(work)),
            ("textshape", lambda: test_a_tool_result_shaped_as_text_is_still_a_row(work)),
            ("poison", lambda: test_one_unparseable_run_does_not_abort_the_ingest(work)),
            ("loopdir", lambda: test_loop_directory_supplies_proc_and_gate_records(work)),
            ("shim", lambda: test_bash_shim_records_a_command_and_keeps_the_exit_code(work)),
            ("tz", lambda: test_shim_stamps_utc_in_any_host_zone(work)),
            ("tzboundary", lambda: test_shim_stamp_is_utc_across_the_date_boundaries(work)),
            ("emptyproc", lambda: test_empty_proc_exec_is_absent_evidence_not_proof(work)),
            ("exitrow", lambda: test_shim_writes_one_row_per_command_including_the_last(work)),
            ("xtrace", lambda: test_shim_adds_no_trace_lines_to_an_x_shell(work)),
            ("incremental", lambda: test_ingest_parses_only_what_changed(work)),
            ("append", lambda: test_ingest_appends_only_the_new_lines(work)),
            ("truncate", lambda: test_ingest_rebuilds_a_truncated_source(work)),
            ("splitpair", lambda: test_ingest_keeps_a_pair_split_across_builds(work)),
            ("stream", lambda: test_ingest_streams_a_large_source(work)),
            ("vanish", lambda: test_ingest_drops_a_run_its_sources_no_longer_hold(work)),
            ("twice", lambda: test_ingest_twice_leaves_the_first_index_alone(work)),
            ("scans", lambda: test_ingest_cost_follows_what_is_new(work)),
            ("killed", lambda: test_an_ingest_killed_mid_write_is_resumed_not_rebuilt(work)),
            ("partial", lambda: test_a_failure_part_way_keeps_the_runs_already_indexed(work)),
            ("namedlog", lambda: test_the_log_the_run_record_names_is_indexed(work)),
            ("busy", lambda: test_a_second_ingest_does_not_wait_for_a_running_one(work)),
        ]
        for name, case in cases:
            try:
                note = case()
            except AssertionError as error:
                failures.append((name, error))
                print(f"FAIL {name}: {error}")
            else:
                print(f"ok   {name}: {note}")
    if failures:
        print(f"{len(failures)} of {len(cases)} cases failed")
        return 1
    print(f"all {len(cases)} tillr-trace cases passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
