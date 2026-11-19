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
             row.get("started_at", 1794526184000), row.get("finished_at", 1794526200000),
             json.dumps(row.get("artifact_refs", []))))
        conn.execute("INSERT OR IGNORE INTO lanes (lane_id, state) VALUES (?, 'free')",
                     (row["lane_id"],))
    conn.commit()
    conn.close()


def cli(*args, cwd=None):
    result = subprocess.run(
        [sys.executable, str(CLI), *args],
        capture_output=True, text=True, cwd=str(cwd or ROOT))
    return result


def run_json(*args):
    """The `run` command's document for one run, as `tillr-view` reads it."""
    result = cli(*args)
    assert result.returncode == 0, result.stderr
    return json.loads(result.stdout)


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
    assert result.stdout.strip() == "discrepancies: none (no disproved claim in the runs read)", (
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

    doc = run_json("--trace-root", str(trace_root), "--store", str(store), "run", run_id)
    gate_rows = {event["kind"]: event["payload"] for event in doc["events"]
                 if event["kind"].startswith("gate.")}
    proc_rows = [event["payload"] for event in doc["events"] if event["kind"] == "proc.exec"]
    git_rows = [event for event in doc["events"] if event["kind"] == "git.trace2"]
    brief_path = doc["run"]["brief_path"]
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
    return "loop-directory proc read ok, a stale git trace2 stream ignored, gate read"


def test_bash_shim_records_a_command_and_keeps_the_exit_code(work):
    """The shim writes a `proc.exec` line the reader parses, and nothing else.

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


def test_a_large_source_is_streamed(work):
    """A source is parsed one line at a time, so peak memory is the rows, not the file.

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


def test_the_log_the_run_record_names_is_read(work):
    """A run's log is read from where its record says, in the record's own spelling.

    The run record names its log as `{"kind": "log", "ref": <path>}` in a lane directory,
    not in the loop directory. The reader once read the key as `Ref`, found nothing, and
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
    doc = run_json("--trace-root", str(root), "--store", str(store), "run", run_id)
    log_path, log_state = doc["run"]["log_path"], doc["run"]["log_state"]
    turns = len([event for event in doc["events"] if event["kind"] == "llm.turn"])
    assert (log_path, log_state) == (str(lane_log), "ok"), (log_path, log_state)
    assert turns == 1, turns
    return "the lane log the run record names was read, and its turn reported"


def test_a_tool_result_shaped_as_text_is_still_a_row(work):
    """Real `pi --mode json` logs carry `structuredContent` as a string for some tools.

    Measured 2026-11-17 over the loop's own run logs: 2,091 of 5,316 `tool_execution_end`
    events held a string there (a skill's text, for one). The parser read it as an object
    and raised, which aborted every read. The row is kept, with the fields that are
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
    doc = run_json("--trace-root", str(root), "--store", str(store), "run", "run-text")
    calls = {event["payload"]["tool_call_id"]: event["payload"] for event in doc["events"]
             if event["kind"] == "tool.call"}
    assert set(calls) == {"c1", "c2", "c3"}, calls
    assert calls["c1"]["target_path"] == "/w/a.md" and calls["c1"]["exit_code"] is None
    assert calls["c2"]["is_error"] is True and calls["c2"]["exit_code"] is None, calls["c2"]
    assert calls["c3"]["exit_code"] == 7, calls["c3"]
    return "string-shaped structuredContent, args and result still produced tool.call rows"


def test_one_unparseable_run_does_not_abort_the_read(work):
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
    corpus = tillr_trace.Corpus(root, store)
    try:
        corpus.load(corpus.run_ids())
        turns = {run for (run,) in corpus.conn.execute(
            "SELECT run_id FROM obs_events WHERE kind = 'llm.turn'")}
        unreadable = [(run, json.loads(payload)) for run, payload in corpus.conn.execute(
            "SELECT run_id, payload FROM obs_events WHERE kind = 'unreadable'")]
    finally:
        tillr_trace.parse_run_log = real
        corpus.close()
    assert turns == {"run-good"}, turns
    assert [run for run, _ in unreadable] == ["run-bad"], unreadable
    assert "AttributeError" in json.dumps(unreadable[0][1]), unreadable
    return "a run whose log parse raised was recorded unreadable and the other run read"


def tree_files(*roots):
    """Every file and directory under `roots`, so a test can see what a command wrote."""
    return {path for root in roots if root.exists() for path in [root, *root.rglob("*")]}


def stale_run(root, run_id):
    """A run whose suite passed before its last edit, so `owed_suite` is disproved."""
    run_dir = write_trace_run(root, run_id, log_lines=[
        {"type": "request_start", "timestamp": 1794510838000},
        {"type": "tool_execution_start", "toolCallId": "e1", "toolName": "edit",
         "args": {"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
        {"type": "tool_execution_end", "toolCallId": "e1", "toolName": "edit",
         "result": {"content": [{"type": "text", "text": "ok"}]},
         "isError": False, "durationMs": 3, "timestamp": 1794510840000},
        turn_end(1, "Edited.\nRESULT: done", ts_ms=1794510845000),
    ], proc_rows=[
        proc("2026-11-12T19:10:02.000Z", ["go", "test", "./..."], "tools/tillr-store"),
    ])
    return run_dir


def test_no_command_writes_an_index(work):
    """Every query answers from the artifacts and leaves the disk as it found it.

    The persistent index is gone: nothing may create `index.db`, or any other file, in
    the trace root, the store's directory or the default trace root. The retired
    `ingest` command is refused rather than quietly accepted.
    """
    root = work / "nowrite-root"
    stale_run(root, "run-nowrite-1")
    store = work / "nowrite-repo" / ".tillr" / "state.db"
    make_store(store, [{"run_id": "run-nowrite-1", "item_id": "574", "lane_id": "lane-1"}])
    default_root = Path(os.environ["TILLR_TRACE_ROOT"])
    before = tree_files(root, store.parent, default_root)
    common = ("--trace-root", str(root), "--store", str(store))
    for args in (("tick", "--last"), ("tick", "tick-1"), ("audit", "run-nowrite-1"),
                 ("run", "run-nowrite-1"), ("cost",), ("cost", "--runs", "0"),
                 ("discrepancies",), ("discrepancies", "--all", "--runs", "0")):
        result = cli(*common, *args)
        assert result.returncode == 0, (args, result.stderr)
    for args in (("tick", "--last"), ("cost",)):
        result = cli("--store", str(store), *args)
        assert result.returncode == 0, (args, result.stderr)
    after = tree_files(root, store.parent, default_root)
    assert after == before, sorted(str(path) for path in after ^ before)
    assert not default_root.exists(), default_root
    refused = cli(*common, "ingest")
    assert refused.returncode == 2, (refused.returncode, refused.stdout, refused.stderr)
    assert "invalid choice" in refused.stderr, refused.stderr
    return "eight queries wrote no file, no index.db exists, and `ingest` is refused"


def test_runs_bounds_the_runs_an_aggregate_reads(work):
    """`--runs N` reads the N runs that started last; `--runs 0` reads every run."""
    root = work / "bound-root"
    for run_id in ("run-old", "run-new"):
        run_dir = root / "runs" / run_id
        run_dir.mkdir(parents=True)
        shutil.copy2(TESTDATA / "canary-run.log", run_dir / f"{run_id}.log")
    store = work / "bound-store.db"
    make_store(store, [
        {"run_id": "run-old", "lane_id": "lane-old", "started_at": 1794500000000},
        {"run_id": "run-new", "lane_id": "lane-new", "started_at": 1794600000000},
    ])
    common = ("--trace-root", str(root), "--store", str(store))
    one = cli(*common, "cost", "--runs", "1")
    assert one.returncode == 0, one.stderr
    assert "lane-new" in one.stdout and "lane-old" not in one.stdout, one.stdout
    every = cli(*common, "cost", "--runs", "0")
    assert every.returncode == 0, every.stderr
    assert "lane-new" in every.stdout and "lane-old" in every.stdout, every.stdout
    helptext = cli("cost", "--help")
    assert "default 100" in " ".join(helptext.stdout.split()), helptext.stdout
    return "--runs 1 read only the most recent run and --runs 0 read both"


def test_since_compares_the_run_finish_time(work):
    """A claim's clock is the run's own finish time, not the moment it was read."""
    root = work / "since-root"
    stale_run(root, "run-since-1")
    store = work / "since-store.db"
    finished = 1794526200000
    make_store(store, [{"run_id": "run-since-1", "item_id": "574", "lane_id": "lane-1",
                        "finished_at": finished}])
    common = ("--trace-root", str(root), "--store", str(store), "discrepancies")
    at = tillr_trace._iso_from_ms(finished)
    after = tillr_trace._iso_from_ms(finished + 1000)
    kept = cli(*common, "--since", at)
    assert kept.returncode == 0, kept.stderr
    assert "owed_suite" in kept.stdout, kept.stdout
    dropped = cli(*common, "--since", after)
    assert dropped.returncode == 0, dropped.stderr
    assert "owed_suite" not in dropped.stdout, dropped.stdout
    doc = run_json("--trace-root", str(root), "--store", str(store), "run", "run-since-1")
    assert {claim["observed_at"] for claim in doc["claims"]} == {at}, doc["claims"]
    return "--since kept a run that finished at the bound and dropped it one second later"


def test_run_document_is_the_shape_tillr_view_reads(work):
    """`run` prints one JSON document: the run's row, its events with payloads, claims."""
    root = work / "doc-root"
    stale_run(root, "run-doc-1")
    store = work / "doc-store.db"
    make_store(store, [{"run_id": "run-doc-1", "item_id": "574", "lane_id": "lane-1"}])
    doc = run_json("--trace-root", str(root), "--store", str(store), "run", "run-doc-1")
    assert set(doc) == {"run", "events", "claims"}, set(doc)
    assert doc["run"]["run_id"] == "run-doc-1", doc["run"]
    assert doc["run"]["proc_state"] == "ok" and doc["run"]["log_state"] == "ok", doc["run"]
    kinds = {event["kind"] for event in doc["events"]}
    assert {"llm.turn", "tool.call", "proc.exec"} <= kinds, kinds
    for event in doc["events"]:
        assert isinstance(event["payload"], dict), event
        assert event["obs_id"].startswith("obs-run-doc-1-"), event
    assert any(claim["claim_kind"] == "owed_suite" for claim in doc["claims"]), doc["claims"]
    absent = run_json("--trace-root", str(root), "--store", str(store), "run", "no-such-run")
    assert absent["run"] is None and absent["events"] == [], absent
    return "run printed the row, %d events with object payloads, and the claims" % (
        len(doc["events"]),)


def main():
    if not DURABLE.is_dir():
        DURABLE.mkdir(parents=True, exist_ok=True)
    failures = []
    with tempfile.TemporaryDirectory(prefix="tillr-trace-test-", dir=DURABLE) as work:
        work = Path(work)
        # The suite's own trace root, inside the case's temporary directory. A case that
        # names no --trace-root reads here instead of the live default root, and the
        # `nowrite` case asserts that no command ever created it.
        os.environ["TILLR_TRACE_ROOT"] = str(work / "default-trace-root")
        cases = [
            ("parser", lambda: test_parser_recovers_measured_totals()),
            ("cost", lambda: test_cost_attributes_tokens_to_lane_and_route(work)),
            ("stale", lambda: test_discrepancies_flags_a_suite_that_ran_before_the_last_edit(work)),
            ("fresh", lambda: test_discrepancies_does_not_flag_a_suite_that_ran_after_the_last_edit(work)),
            ("unreadable", lambda: test_missing_artifact_is_unreadable_not_a_crash(work)),
            ("readonly", lambda: test_store_is_opened_read_only(work)),
            ("textshape", lambda: test_a_tool_result_shaped_as_text_is_still_a_row(work)),
            ("poison", lambda: test_one_unparseable_run_does_not_abort_the_read(work)),
            ("loopdir", lambda: test_loop_directory_supplies_proc_and_gate_records(work)),
            ("shim", lambda: test_bash_shim_records_a_command_and_keeps_the_exit_code(work)),
            ("tz", lambda: test_shim_stamps_utc_in_any_host_zone(work)),
            ("tzboundary", lambda: test_shim_stamp_is_utc_across_the_date_boundaries(work)),
            ("emptyproc", lambda: test_empty_proc_exec_is_absent_evidence_not_proof(work)),
            ("exitrow", lambda: test_shim_writes_one_row_per_command_including_the_last(work)),
            ("xtrace", lambda: test_shim_adds_no_trace_lines_to_an_x_shell(work)),
            ("stream", lambda: test_a_large_source_is_streamed(work)),
            ("namedlog", lambda: test_the_log_the_run_record_names_is_read(work)),
            ("rundoc", lambda: test_run_document_is_the_shape_tillr_view_reads(work)),
            ("bound", lambda: test_runs_bounds_the_runs_an_aggregate_reads(work)),
            ("since", lambda: test_since_compares_the_run_finish_time(work)),
            ("nowrite", lambda: test_no_command_writes_an_index(work)),
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
