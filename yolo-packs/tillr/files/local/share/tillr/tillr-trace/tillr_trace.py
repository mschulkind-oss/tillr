#!/usr/bin/env python3
"""tillr-trace: answers over recorded run artifacts, read on demand.

Nothing here is a source of truth, and nothing here is kept. `.tillr/state.db` owns
workflow state, the `<run-id>.log` owns what the model saw and spent, and
`proc-exec.jsonl` owns what ran. Every command parses the artifacts of the runs it is
asked about into an in-memory SQLite database, answers from it, and drops it on exit.
There is no index file: a persistent copy of these rows was removed on 2026-11-18 by
the owner's ruling, to come back only once a measured performance problem asks for it.
See README.md.

Design: docs/design/session-recording.md §3.2 (layout), §4.2 (trace_key),
§4.3 (obs.event), §6.3 (store authority), §8.3 (claim verification).
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import itertools
import json
import os
import re
import sqlite3
import sys
from pathlib import Path

# Artifact a reader could not read. The design's twelve obs.event kinds describe what a
# run did; this one describes what could not be seen, so a missing log is a row that says
# so rather than a zero that reads as "nothing happened".
UNREADABLE = "unreadable"

EXCERPT_MAX = 200

# `runs_index` still carries `git_path` / `git_state` because `tillr-view` reads them by
# name. Git trace2 recording was removed on 2026-11-17 (docs/design/session-recording.md,
# the owner ruling in §2.2), so the path is always NULL and the state says why.
GIT_RETIRED = "retired"

# How many runs a whole-queue aggregate (`cost`, `discrepancies`) reads when the caller
# names no `--runs`: the most recent ones, by the store's start time. `--runs 0` reads
# every run. The bound exists because an aggregate parses every artifact it covers on
# every call; see README.md for the measured cost of the whole corpus.
DEFAULT_RUNS = 100

# §5.1 rule 1: a credential never reaches a row, even inside an excerpt.
CREDENTIAL_RE = re.compile(
    r"(Bearer\s+\S+"
    r"|sk-[A-Za-z0-9_-]{16,}"
    r"|gh[pousr]_[A-Za-z0-9_]{20,}"
    r"|AKIA[0-9A-Z]{16}"
    r"|(--token|--api-key|--password|--secret)[= ]\S+)"
)

# §8.3 owed-suite detection: a command that runs a module's suite.
TEST_COMMAND_RE = re.compile(
    r"(^|[\s;&|])(go test|pytest|py\.test|node --test|python3? [\w./-]*test[\w.-]*\.py"
    r"|just done|uvx? [\w-]+|npm (run )?test)\b"
)

EDIT_TOOLS = frozenset({"edit", "write"})

# The in-memory layout one command's answers are read from. It lives only as long as the
# process that built it.
SCHEMA = """
CREATE TABLE obs_events (
  obs_id      TEXT PRIMARY KEY,
  seq         INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  ts_start    TEXT NOT NULL,
  ts_end      TEXT,
  duration_ms INTEGER,
  seam        TEXT,
  run_id      TEXT,
  lane_id     TEXT,
  item_id     TEXT,
  tick_id     TEXT,
  session_id  TEXT,
  route_id    TEXT,
  span_id     TEXT,
  summary     TEXT,
  payload     TEXT NOT NULL DEFAULT '{}',
  source      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX obs_events_run  ON obs_events(run_id, kind, ts_start);

CREATE TABLE runs_index (
  run_id      TEXT PRIMARY KEY,
  lane_id     TEXT,
  item_id     TEXT,
  backend     TEXT,
  store_state TEXT,
  exit_code   INTEGER,
  started_at  INTEGER,
  finished_at INTEGER,
  log_path    TEXT,
  log_state   TEXT,
  proc_path   TEXT,
  proc_state  TEXT,
  git_path    TEXT,
  git_state   TEXT,
  brief_path  TEXT,
  fence_path  TEXT,
  base_sha    TEXT,
  head_sha    TEXT,
  tree_sha    TEXT,
  detail      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE claim_checks (
  claim_id            TEXT PRIMARY KEY,
  run_id              TEXT NOT NULL,
  lane_id             TEXT,
  item_id             TEXT,
  claim_kind          TEXT NOT NULL,
  claim_text          TEXT NOT NULL,
  observed_status     TEXT NOT NULL
    CHECK (observed_status IN ('verified','disproved','unverified')),
  discrepancy_reason  TEXT NOT NULL DEFAULT '',
  evidence_span_ids   TEXT NOT NULL DEFAULT '[]',
  last_dir_edit_at    TEXT,
  observed_at         TEXT NOT NULL
);
"""


# ---------------------------------------------------------------- small helpers


def _iso_from_ms(ms):
    """Epoch milliseconds to the design's `2026-11-12T19:04:12.104Z` form."""
    if ms is None:
        return None
    try:
        value = float(ms)
    except (TypeError, ValueError):
        return str(ms)
    stamp = dt.datetime.fromtimestamp(value / 1000.0, tz=dt.timezone.utc)
    return stamp.strftime("%Y-%m-%dT%H:%M:%S.") + f"{stamp.microsecond // 1000:03d}Z"


def _iso_from_text(text):
    """Normalize an RFC3339 timestamp to millisecond UTC, keeping the order."""
    if not text:
        return None
    text = str(text)
    if text.endswith("Z") and "." in text:
        head, frac = text[:-1].split(".", 1)
        return f"{head}.{frac[:3]}Z"
    return text


def _ms_from_iso(text):
    """Epoch milliseconds from a pi ISO timestamp, for anchoring events that lack one."""
    if not text:
        return None
    try:
        stamp = dt.datetime.fromisoformat(str(text).replace("Z", "+00:00"))
    except ValueError:
        return None
    return int(stamp.timestamp() * 1000)


def now_iso():
    stamp = dt.datetime.now(dt.timezone.utc)
    return stamp.strftime("%Y-%m-%dT%H:%M:%S.") + f"{stamp.microsecond // 1000:03d}Z"


def scrub(text, limit=EXCERPT_MAX):
    """Bound an excerpt and strip anything shaped like a credential."""
    if not text:
        return ""
    text = CREDENTIAL_RE.sub("[REDACTED]", str(text))
    return text[:limit]


def trace_root_default():
    override = os.environ.get("TILLR_TRACE_ROOT")
    if override:
        return Path(override)
    state = os.environ.get("XDG_STATE_HOME")
    base = Path(state) if state else Path.home() / ".local" / "state"
    return base / "tillr-trace" / "v1"


# ---------------------------------------------------------------- obs rows


_SEQ = itertools.count(1)


def _source_slug(path):
    """A short, stable stamp for one source file, used in its rows' obs ids."""
    return hashlib.sha256(str(path or "").encode("utf-8")).hexdigest()[:12]


class Rows:
    """Accumulates obs.event rows for one artifact, in file order.

    `seq` is one process-wide counter so a query can break a tie between two rows a run
    wrote in the same millisecond; the artifact's own order is the only ordering the files
    carry. The obs id is the source file's stamp plus the row's ordinal in that file, so
    the same artifact yields the same ids on every read.
    """

    def __init__(self, origin="run", source=""):
        self.rows = []
        self.origin = origin
        self.source = str(source or "")
        self.ordinal = 0
        self._slug = _source_slug(self.source)

    def add(self, kind, trace, *, ts_start, ts_end=None, duration_ms=None,
            seam=None, span_id=None, summary=None, payload=None):
        ordinal = self.ordinal
        self.ordinal += 1
        run_id = (trace or {}).get("run_id") or "unattributed"
        self.rows.append(
            {
                "obs_id": f"obs-{run_id}-{self._slug}-{ordinal:06d}",
                "seq": next(_SEQ),
                "kind": kind,
                "ts_start": ts_start or "",
                "ts_end": ts_end,
                "duration_ms": duration_ms,
                "seam": seam,
                "run_id": run_id,
                "lane_id": (trace or {}).get("lane_id"),
                "item_id": (trace or {}).get("item_id"),
                "tick_id": (trace or {}).get("tick_id"),
                "session_id": (trace or {}).get("session_id"),
                "route_id": (trace or {}).get("route_id"),
                "span_id": span_id or (trace or {}).get("span_id"),
                "summary": summary,
                "payload": json.dumps(payload or {}, sort_keys=True),
                "source": self.source,
            }
        )

    def add_unreadable(self, trace, artifact, path, reason):
        """One row saying an artifact could not be read, with an id the artifact names."""
        run_id = (trace or {}).get("run_id") or "unattributed"
        self.rows.append(
            {
                "obs_id": f"obs-{run_id}-unreadable-{artifact}",
                "seq": next(_SEQ),
                "kind": UNREADABLE,
                "ts_start": now_iso(),
                "ts_end": None,
                "duration_ms": None,
                "seam": "reader",
                "run_id": run_id,
                "lane_id": (trace or {}).get("lane_id"),
                "item_id": (trace or {}).get("item_id"),
                "tick_id": (trace or {}).get("tick_id"),
                "session_id": (trace or {}).get("session_id"),
                "route_id": None,
                "span_id": f"unreadable-{artifact}",
                "summary": f"{artifact}: unreadable ({reason})",
                "payload": json.dumps(
                    {"artifact": artifact, "path": str(path), "reason": reason},
                    sort_keys=True),
                "source": self.source,
            }
        )


class Parsed:
    """What one artifact parse produced: its rows, its state, and the log's totals.

    A plain class rather than a dataclass on purpose: the suite imports this module from
    a file path, so it is not in `sys.modules` under its own name, and a dataclass
    resolves its annotations through that entry.
    """

    __slots__ = ("rows", "state", "detail", "summary")

    def __init__(self, rows, state="ok", detail="", summary=None):
        self.rows = rows
        self.state = state
        self.detail = detail
        self.summary = summary if summary is not None else {}


def _jsonl_lines(path, wanted=None):
    """Yield `(obj, parsed, complete)` for each line of a JSONL file.

    One line is read, decoded and handed on before the next is read, so a caller holds
    one line and its row — never the whole file. `complete` is False for a final line
    the writer has not finished appending.

    `wanted` is the byte strings a line the caller reads must contain. A complete line
    holding none of them is passed on as `None` without being decoded: most of a pi log
    is `message_update` streaming that no row is made from, and decoding it was most of
    the parse.
    """
    with open(path, "rb") as handle:
        for raw in handle:
            complete = raw.endswith(b"\n")
            if wanted and complete and not any(marker in raw for marker in wanted):
                yield None, True, complete
                continue
            text = raw.decode("utf-8", "replace").strip()
            if not text:
                yield None, True, complete
                continue
            try:
                yield json.loads(text), True, complete
            except json.JSONDecodeError:
                yield None, False, complete


class Stream:
    """What one JSONL parse could not read on the way.

    `state` is `ok`, `truncated` (the final line is incomplete — a writer caught
    mid-append) or `corrupt` (a complete line failed to parse, which no live writer
    does).
    """

    def __init__(self):
        self.bad = 0
        self.truncated = False

    def feed(self, parsed, complete):
        """Record one line; answer False when the parse must stop here."""
        if not parsed:
            if not complete:
                self.truncated = True
                return False
            self.bad += 1
        return True

    def state(self, rows):
        if self.truncated and not self.bad:
            return "truncated", f"{rows} rows, final line incomplete"
        if self.bad:
            return "corrupt", f"{rows} rows, {self.bad} unparseable line(s)"
        return "ok", f"{rows} rows"


def _empty_summary():
    return {"turns": 0, "tokens_in": 0, "tokens_out": 0, "cache_read": 0,
            "reasoning": 0, "cost_usd": 0.0, "tool_calls": 0, "final_text": ""}


def _read_json(path):
    """A small JSON artifact as an object, or an empty one when it is absent."""
    if path is None:
        return {}
    try:
        value = json.loads(Path(path).read_text(encoding="utf-8", errors="replace"))
    except (OSError, ValueError):
        return {}
    return value if isinstance(value, dict) else {}


# ---------------------------------------------------------------- pi run log


def _obj(value):
    """`value` when it is a JSON object, else an empty one.

    A pi log's tool events are not uniformly shaped: measured 2026-11-17, a tool result's
    `structuredContent` was a string in 2,091 of 5,316 `tool_execution_end` events. A field
    that is read for its keys is read through this, so a text where an object was expected
    costs that field and never the row, or the run.
    """
    return value if isinstance(value, dict) else {}


# The lines of a pi log a row is made from. Every other line is skipped undecoded.
LOG_MARKERS = (b'"session"', b'"request_start"', b'"tool_execution_start"',
               b'"tool_execution_end"', b'"turn_end"')
# The lines a turns-only read needs: `cost` reads nothing else.
TURN_MARKERS = (b'"turn_end"',)


def parse_run_log(path, trace, turns_only=False):
    """Recover llm.turn and tool.call rows from `pi --mode json` output.

    Measured shape (2026-11-12 canary, tools/tillr-trace/testdata/canary-run.log):
    a `session` header carrying `id`/`cwd`, one `turn_end` per model turn whose
    `message` holds `provider`, `model`, `responseModel`, `thinkingLevel`, `usage`
    (`input`/`output`/`cacheRead`/`cacheWrite`/`reasoning` and `cost.total`),
    `credential.fingerprint`, `stopReason` and `durationMs`, and one
    `tool_execution_start`/`tool_execution_end` pair per tool call.

    `turns_only` reads the `turn_end` lines alone, which is all a cost answer needs.
    """
    summary = _empty_summary()
    rows = Rows("log", source=str(path or ""))
    if not path:
        return Parsed(rows, "missing", "no log path recorded", summary)

    args_by_call = {}
    turn_no = 0
    final_text = ""
    # `tool_execution_*` events carry no clock of their own — only the session header,
    # `request_start` and `turn_end.message` do. A tool call is therefore stamped with
    # the last clock the stream wrote before it, and its payload says the time is
    # derived, so a reader never takes an anchored time for an observed one.
    anchor = {"ms": None}
    stream = Stream()
    markers = TURN_MARKERS if turns_only else LOG_MARKERS
    try:
        for event, parsed, complete in _jsonl_lines(path, markers):
            if not stream.feed(parsed, complete):
                break
            if not parsed or not isinstance(event, dict):
                continue
            kind = event.get("type")
            if kind == "session":
                trace = dict(trace or {})
                trace.setdefault("session_id", event.get("id"))
                trace["worktree"] = event.get("cwd")
                if _ms_from_iso(event.get("timestamp")) is not None:
                    anchor["ms"] = _ms_from_iso(event.get("timestamp"))
                rows.add(
                    "run.lifecycle", trace,
                    ts_start=_iso_from_text(event.get("timestamp")) or now_iso(),
                    seam="pi-log",
                    span_id="session-header",
                    summary=f"session {event.get('id')}",
                    payload={"state": "started", "cwd": event.get("cwd"),
                             "version": event.get("version")},
                )
            elif kind == "request_start":
                if event.get("timestamp") is not None:
                    anchor["ms"] = event["timestamp"]
            elif kind == "tool_execution_start":
                args_by_call[event.get("toolCallId")] = _obj(event.get("args"))
            elif kind == "tool_execution_end":
                call_id = event.get("toolCallId")
                args = _obj(args_by_call.pop(call_id, {}))
                tool = event.get("toolName") or "unknown"
                result = event.get("result") or {}
                structured = _obj(_obj(result).get("structuredContent"))
                target = args.get("path")
                command = args.get("command")
                rows.add(
                    "tool.call", trace,
                    ts_start=_iso_from_ms(anchor["ms"]) or "",
                    duration_ms=event.get("durationMs"),
                    seam="pi-log",
                    span_id=call_id,
                    summary=f"{tool} {(target or command or '')}".strip()[:EXCERPT_MAX],
                    payload={
                        "tool_call_id": call_id,
                        "tool_name": tool,
                        "target_path": target,
                        "command_preview": scrub(command) if command else None,
                        "repo_rel_cwd": None,
                        "is_error": bool(event.get("isError")),
                        "exit_code": structured.get("exit_code"),
                        "ts_source": "derived_from_preceding_clock" if anchor["ms"]
                        else "unknown",
                        "result_bytes": len(json.dumps(result)),
                        "duration_ms": event.get("durationMs"),
                    },
                )
                summary["tool_calls"] += 1
            elif kind == "turn_end":
                message = _obj(event.get("message"))
                if message.get("role") != "assistant":
                    continue
                turn_no += 1
                if message.get("timestamp") is not None:
                    anchor["ms"] = message["timestamp"]
                usage = _obj(message.get("usage"))
                cost = _obj(usage.get("cost"))
                provider = message.get("provider")
                model = message.get("model")
                thinking = message.get("thinkingLevel")
                route_id = ":".join(part for part in (provider, model, thinking) if part)
                turn_trace = dict(trace or {})
                turn_trace["route_id"] = route_id
                started = message.get("requestStartedAt")
                ended = message.get("timestamp")
                rows.add(
                    "llm.turn", turn_trace,
                    ts_start=_iso_from_ms(started) or now_iso(),
                    ts_end=_iso_from_ms(ended),
                    duration_ms=message.get("durationMs"),
                    seam="pi-log",
                    span_id=f"turn-{turn_no:03d}",
                    summary=f"{provider}/{message.get('responseModel') or model} {thinking}",
                    payload={
                        "provider": provider,
                        "model": model,
                        "response_model": message.get("responseModel"),
                        "upstream_route": model,
                        "thinking_level": thinking,
                        "tokens": {
                            "in": usage.get("input", 0),
                            "out": usage.get("output", 0),
                            "cache_read": usage.get("cacheRead", 0),
                            "cache_write": usage.get("cacheWrite", 0),
                            "reasoning": usage.get("reasoning", 0),
                        },
                        "cost_usd": cost.get("total", 0.0),
                        "credential_fp": (message.get("credential") or {}).get("fingerprint"),
                        "stop_reason": message.get("stopReason"),
                    },
                )
                summary["turns"] += 1
                summary["tokens_in"] += usage.get("input", 0) or 0
                summary["tokens_out"] += usage.get("output", 0) or 0
                summary["cache_read"] += usage.get("cacheRead", 0) or 0
                summary["reasoning"] += usage.get("reasoning", 0) or 0
                summary["cost_usd"] += cost.get("total", 0.0) or 0.0
                texts = [part.get("text", "") for part in message.get("content") or []
                         if isinstance(part, dict) and part.get("type") == "text"]
                if any(texts):
                    final_text = "\n".join(texts)
    except FileNotFoundError:
        return Parsed(rows, "missing", f"no file at {path}", summary)
    except OSError as error:
        return Parsed(rows, "unreadable", f"{error.__class__.__name__}: {error}", summary)
    summary["final_text"] = scrub(final_text, limit=20000)
    state, detail = stream.state(rows.ordinal)
    return Parsed(rows, state, detail, summary)


# ---------------------------------------------------------------- proc / gate


def parse_proc_exec(path, trace):
    """Raw shim rows (§4.3 `proc.exec`, specimen in §7.1) to normalized rows."""
    rows = Rows("proc", source=str(path or ""))
    if not path:
        return Parsed(rows, "missing", "no proc-exec.jsonl recorded")
    stream = Stream()
    try:
        for event, parsed, complete in _jsonl_lines(path):
            if not stream.feed(parsed, complete):
                break
            if not parsed or not isinstance(event, dict):
                continue
            _proc_row(rows, trace, event)
    except FileNotFoundError:
        return Parsed(rows, "missing", f"no file at {path}")
    except OSError as error:
        return Parsed(rows, "unreadable", f"{error.__class__.__name__}: {error}")
    # A present-but-empty file is the same evidence as no file at all: the shim creates
    # nothing and only appends, so 0 bytes says a writer touched the path and nothing
    # more. Read as `ok` it makes `have_proc` true and turns every owed suite into a
    # `disproved` "no suite ran at all" — absence of evidence promoted to proof (§8.3).
    if not rows.rows:
        return Parsed(rows, "missing", f"{path}: no proc.exec rows")
    state, detail = stream.state(rows.ordinal)
    return Parsed(rows, state, detail)


def _proc_row(rows, trace, event):
    argv = event.get("argv")
    if argv is None and event.get("cmd"):
        argv = [event["cmd"]]
    argv = argv or []
    span = event.get("span_id") or f"sh-{rows.ordinal + 1:05d}"
    repo_rel = event.get("repo_rel_cwd")
    rows.add(
        "proc.exec", trace,
        ts_start=_iso_from_text(event.get("ts") or event.get("ts_start")) or now_iso(),
        ts_end=_iso_from_text(event.get("ts_end")),
        duration_ms=event.get("duration_ms"),
        seam=event.get("seam") or "bash-shim",
        span_id=span,
        summary=scrub(" ".join(str(part) for part in argv), 120),
        payload={
            "argv": [scrub(str(part), 512) for part in argv],
            "cwd": event.get("cwd"),
            "repo_rel_cwd": repo_rel,
            "exit_code": event.get("exit_code", event.get("rc")),
            "signal": event.get("signal"),
            "stdout_sha256": event.get("stdout_sha256"),
            "tail_excerpt": scrub(event.get("tail_excerpt")),
            "pid": event.get("pid"),
        },
    )


def parse_gate_record(path, trace):
    """One `.tillr/loop/<run-id>/gate.{plan,verdict}.json` to a §4.3 row.

    Both are `record.Envelope` files the gate writes through `tillr-loop`, one record
    each, so each is read as a single JSON object rather than as JSONL. A file that does
    not parse becomes an `unreadable` row naming it: a gate that ran and left a record
    nobody can read must never look like a gate that never ran.
    """
    path = Path(path)
    kind = "gate.verdict" if path.name.startswith("gate.verdict") else "gate.plan"
    rows = Rows("gate", source=str(path))
    try:
        envelope = json.loads(path.read_text(encoding="utf-8", errors="replace"))
    except FileNotFoundError:
        return Parsed(rows, "missing", f"no file at {path}")
    except (OSError, ValueError) as error:
        rows.add_unreadable(trace, path.name, path, f"unparseable: {error}")
        return Parsed(rows, "corrupt", f"unparseable: {error}")
    if not isinstance(envelope, dict) or envelope.get("kind") != kind:
        rows.add_unreadable(trace, path.name, path, f"not a {kind} record")
        return Parsed(rows, "corrupt", f"not a {kind} record")
    payload = envelope.get("payload")
    if not isinstance(payload, dict):
        rows.add_unreadable(trace, path.name, path, f"{kind} payload is not an object")
        return Parsed(rows, "corrupt", f"{kind} payload is not an object")
    rows.add(
        kind, trace,
        ts_start=_iso_from_text(envelope.get("created_at")) or now_iso(),
        seam="tillr-gate",
        span_id=str(envelope.get("id") or kind),
        summary=f"{kind} {payload.get('plan_id') or payload.get('verdict_id') or ''}".strip(),
        payload=_gate_payload(kind, payload),
    )
    return Parsed(rows, "ok", f"{kind} read")


def _gate_payload(kind, payload):
    """The §4.3 fields a plan or verdict carries, read from the record's own spelling.

    The gate names the same facts differently in each record: a plan's checks are a list
    of `PlanCheck` objects and its count is that list's length, and a verdict's outcome
    is `result` over `pass` / `fail` / `error` with one `VerdictCheck` per planned check.
    Both are projected onto the design's field names rather than copied, so a query reads
    one spelling whichever record it came from.
    """
    checks = payload.get("checks") or []
    check_ids = [str(check.get("check_id")) for check in checks
                 if isinstance(check, dict) and check.get("check_id")]
    common = {
        "plan_id": payload.get("plan_id"),
        "base_sha": payload.get("base_sha"),
        "tree_sha": payload.get("tree_sha"),
        "registry_digest": payload.get("registry_digest"),
        "check_ids": check_ids,
    }
    if kind == "gate.plan":
        common["checks_count"] = len(checks)
        return common
    common["verdict"] = payload.get("result")
    common["verdict_id"] = payload.get("verdict_id")
    common["failed_check_ids"] = [
        str(check.get("check_id")) for check in checks
        if isinstance(check, dict) and check.get("result") != "pass"]
    common["check_timings_ms"] = {
        str(check.get("check_id")): check.get("duration_ms") for check in checks
        if isinstance(check, dict) and check.get("check_id")}
    return common


def run_artifact(dirs, name):
    """The first of a run's directories holding `name`, else the first directory's path.

    The path is answered even when no directory holds the file, so an absent artifact is
    read as `missing` at a named location instead of as nothing at all.
    """
    dirs = [Path(directory) for directory in dirs]
    for directory in dirs:
        candidate = directory / name
        if candidate.exists():
            return candidate
    return dirs[0] / name if dirs else None


def _existing(dirs, name):
    """The path of `name` when one of a run's directories holds it, else None."""
    path = run_artifact(dirs, name) if dirs else None
    return str(path) if path is not None and path.exists() else None


# ---------------------------------------------------------------- store


class Store:
    """`.tillr/state.db` opened strictly read-only.

    §6.3: the store owns workflow state and this tool never writes it, so the
    connection is created with `mode=ro` and `query_only` as a second lock on the
    same door. A store that is absent or older than a table we ask for answers
    `unreadable` per fact instead of raising.
    """

    TABLES = ("runs", "lanes", "items", "candidates", "decisions", "dispatches")

    def __init__(self, path):
        self.path = Path(path)
        self.conn = None
        self.state = "ok"
        self.detail = ""
        self.tables = set()
        if not self.path.exists():
            self.state, self.detail = "missing", f"no store at {self.path}"
            return
        try:
            self.conn = sqlite3.connect(f"file:{self.path}?mode=ro", uri=True)
            self.conn.execute("PRAGMA query_only = ON")
            self.tables = {
                row[0]
                for row in self.conn.execute(
                    "SELECT name FROM sqlite_master WHERE type = 'table'")
            }
        except sqlite3.Error as error:
            self.state, self.detail = "unreadable", f"{error.__class__.__name__}: {error}"

    def close(self):
        if self.conn is not None:
            self.conn.close()

    def rows(self, sql, params=()):
        """Run a store query, returning [] when the table or column is absent."""
        if self.conn is None:
            return []
        try:
            return [dict(zip([col[0] for col in cur.description], row))
                    for cur in [self.conn.execute(sql, params)]
                    for row in cur.fetchall()]
        except sqlite3.Error:
            return []

    def runs(self):
        return self.rows(
            "SELECT run_id, item_id, lane_id, backend, state, exit_code,"
            " started_at, finished_at, artifact_refs FROM runs")


def _artifact_refs(raw):
    try:
        refs = json.loads(raw or "[]")
    except (TypeError, ValueError):
        return {}
    # The run record spells the key `ref` (tools/tillr-run, `ArtifactRef`). `Ref` is kept
    # for a record written before the field had a JSON name.
    return {ref.get("kind"): ref.get("ref") or ref.get("Ref")
            for ref in refs if isinstance(ref, dict)}


# ---------------------------------------------------------------- reading runs


# The runs_index columns, in the order every INSERT and the claim join read them.
RUN_COLUMNS = (
    "run_id", "lane_id", "item_id", "backend", "store_state", "exit_code",
    "started_at", "finished_at", "log_path", "log_state", "proc_path", "proc_state",
    "git_path", "git_state", "brief_path", "fence_path", "base_sha", "head_sha",
    "tree_sha", "detail",
)

RUN_INSERT = ("INSERT OR REPLACE INTO runs_index (" + ", ".join(RUN_COLUMNS) + ")"
              " VALUES (" + ",".join("?" for _ in RUN_COLUMNS) + ")")

OBS_INSERT = (
    "INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start, ts_end,"
    " duration_ms, seam, run_id, lane_id, item_id, tick_id, session_id,"
    " route_id, span_id, summary, payload, source)"
    " VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")


class Corpus:
    """The recorded runs, read on demand into one in-memory database.

    Nothing is written to disk. `load` parses the artifacts of the runs it is given, and
    every query reads what was loaded; the database is dropped with the process. Each
    command loads only the runs and the row kinds its answer needs.
    """

    def __init__(self, trace_root, store_path, loop_root=None):
        self.trace_root = Path(trace_root)
        self.store_path = Path(store_path)
        # A run's records live in two places: `<trace-root>/runs/<run-id>/` (the
        # recorder's own directory, §3.2) and `<root>/.tillr/loop/<run-id>/` (the drain's
        # per-run record directory, where staging keeps the brief, fence and base sha,
        # where `run-pi`'s emitters append `proc-exec.jsonl`, and where the gate leaves
        # `gate.plan.json` / `gate.verdict.json`). `.tillr/loop` is the store's sibling
        # unless the caller names another.
        self.loop_root = Path(loop_root) if loop_root else self.store_path.parent / "loop"
        self.conn = sqlite3.connect(":memory:")
        self.conn.row_factory = sqlite3.Row
        self.conn.executescript(SCHEMA)
        self._store_runs = None
        self._run_dirs = None
        self._store_state = None

    def close(self):
        self.conn.close()

    # -- source enumeration

    def run_dirs(self):
        """run_id -> its artifact directories, most specific first."""
        if self._run_dirs is None:
            found = {}
            for root in (self.loop_root, self.trace_root / "runs"):
                if not root.is_dir():
                    continue
                for child in root.iterdir():
                    if child.is_dir():
                        found.setdefault(child.name, []).append(child)
            self._run_dirs = found
        return self._run_dirs

    def store_runs(self):
        if self._store_runs is None:
            store = Store(self.store_path)
            try:
                self._store_state = (store.state, store.detail)
                self._store_runs = {row["run_id"]: row for row in store.runs()}
            finally:
                store.close()
        return self._store_runs

    def run_ids(self, limit=0):
        """Every known run, most recent first; the first `limit` of them when it is set.

        Recency is the store's start time, and a run the store does not hold sorts by its
        directory's modification time. Neither clock is a judgment, only an order.
        """
        store_runs = self.store_runs()
        dirs = self.run_dirs()

        def started(run_id):
            row = store_runs.get(run_id)
            if row and row.get("started_at"):
                return int(row["started_at"])
            mtimes = []
            for directory in dirs.get(run_id, []):
                try:
                    mtimes.append(int(directory.stat().st_mtime * 1000))
                except OSError:
                    pass
            return max(mtimes) if mtimes else 0

        ordered = sorted(set(store_runs) | set(dirs), key=lambda r: (started(r), r),
                         reverse=True)
        return ordered[:limit] if limit else ordered

    def known(self, run_id):
        return run_id in self.store_runs() or run_id in self.run_dirs()

    def _log_path(self, run_id, dirs, store_row):
        """The log this run is actually parsed from, by the §3.2 precedence."""
        refs = _artifact_refs((store_row or {}).get("artifact_refs")) if store_row else {}
        named = Path(refs["log"]) if refs.get("log") else None
        if named is not None and named.exists():
            return named
        fallback = run_artifact(dirs, f"{run_id}.log") if dirs else None
        if fallback is not None and (fallback.exists() or named is None):
            return fallback
        return named

    def run_sources(self, run_id):
        """The exact files one run's derivation reads, by role."""
        dirs = [Path(directory) for directory in self.run_dirs().get(run_id, [])]
        loop_dir = self.loop_root / run_id
        return {
            "log": self._log_path(run_id, dirs, self.store_runs().get(run_id)),
            "proc": run_artifact(dirs, "proc-exec.jsonl") if dirs else None,
            "manifest": run_artifact(dirs, "manifest.json") if dirs else None,
            "base_sha": run_artifact(dirs, "base_sha") if dirs else None,
            "brief": run_artifact(dirs, "brief.md") if dirs else None,
            "fence": run_artifact(dirs, "fence.txt") if dirs else None,
            "gate_plan": loop_dir / "gate.plan.json",
            "gate_verdict": loop_dir / "gate.verdict.json",
        }

    def tick_of(self, run_id):
        """The tick a run's manifest names, or None."""
        return _read_json(self.run_sources(run_id).get("manifest")).get("tick_id")

    # -- loading

    def load(self, run_ids, *, kinds=None, claims=True):
        """Parse the given runs into the in-memory database.

        `kinds` keeps only those obs.event kinds once a run's claims are computed (None
        keeps every kind); a run's rows of other kinds are dropped as soon as its claims
        are written, so a whole-queue answer holds one run's events at a time. `claims`
        is whether the §8.3 join is run for each.
        """
        self.store_runs()
        self._sync_unreadable_store()
        for run_id in run_ids:
            self._derive(run_id, kinds, claims)
        self._tick_decisions(set(run_ids))
        self.conn.commit()

    def _derive(self, run_id, kinds, claims):
        store_row = self.store_runs().get(run_id)
        dict_row = store_row or {}
        trace = {"run_id": run_id, "lane_id": dict_row.get("lane_id"),
                 "item_id": dict_row.get("item_id")}
        dirs = [Path(directory) for directory in self.run_dirs().get(run_id, [])]
        sources = self.run_sources(run_id)
        manifest = _read_json(sources.get("manifest"))
        if manifest.get("tick_id"):
            trace["tick_id"] = manifest["tick_id"]

        turns_only = kinds is not None and not claims and set(kinds) <= {"llm.turn"}
        log = self._parse(parse_run_log, sources.get("log"), trace, turns_only=turns_only)
        if turns_only:
            proc = Parsed(Rows("skip"), "unread", "not read for this answer")
        else:
            proc = self._parse(parse_proc_exec, sources.get("proc"), trace)
            for name in ("gate_plan", "gate_verdict"):
                if Path(sources[name]).exists():
                    self._parse(parse_gate_record, sources[name], trace)

        if log.state in ("missing", "unreadable", "corrupt"):
            self._unreadable(trace, "run-log", sources.get("log"),
                             f"{log.state}: {log.detail}")
        if proc.state in ("unreadable", "corrupt"):
            self._unreadable(trace, "proc-exec.jsonl", sources.get("proc"), proc.state)

        base_sha = None
        base_path = sources.get("base_sha")
        if base_path is not None and Path(base_path).exists():
            try:
                base_sha = Path(base_path).read_text().strip() or None
            except OSError:
                base_sha = None
        if not base_sha and manifest.get("base_sha"):
            base_sha = manifest["base_sha"]

        detail_bits = [log.detail] if log.detail else []
        if not store_row:
            detail_bits.append("no store row for this run")
        meta = (
            run_id, dict_row.get("lane_id"), dict_row.get("item_id"),
            dict_row.get("backend"), dict_row.get("state") or "unrecorded",
            dict_row.get("exit_code"), dict_row.get("started_at"),
            dict_row.get("finished_at"),
            str(sources["log"]) if sources.get("log") else None, log.state,
            str(sources["proc"]) if sources.get("proc") else None, proc.state,
            None, GIT_RETIRED,
            _existing(dirs, "brief.md"), _existing(dirs, "fence.txt"),
            base_sha, manifest.get("head_sha"), manifest.get("tree_sha"),
            "; ".join(detail_bits),
        )
        self.conn.execute(RUN_INSERT, meta)
        if claims:
            run = dict(zip(RUN_COLUMNS, meta))
            for claim in verify_run(self.conn, run, log.summary or {}):
                self.conn.execute(
                    "INSERT OR REPLACE INTO claim_checks (claim_id, run_id, lane_id,"
                    " item_id, claim_kind, claim_text, observed_status,"
                    " discrepancy_reason, evidence_span_ids, last_dir_edit_at,"
                    " observed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)", claim)
        if kinds is not None:
            wanted = sorted(set(kinds) | {UNREADABLE})
            marks = ",".join("?" for _ in wanted)
            self.conn.execute(
                f"DELETE FROM obs_events WHERE run_id = ? AND kind NOT IN ({marks})",
                [run_id, *wanted])

    def _parse(self, parser, path, trace, **options):
        """Run one parser, keeping the rest of the run when it raises."""
        if path is None:
            parsed = parser(None, trace, **options)
        else:
            try:
                parsed = parser(path, trace, **options)
            except Exception as error:  # noqa: BLE001 - one source must not cost the run
                # A parser that raises is a defect in the parser or a shape nobody has
                # seen. Either way the run's other sources still answer, so this source
                # reads corrupt with the reason and its `unreadable` row names it (§7.1).
                parsed = Parsed(Rows(Path(path).name), "corrupt",
                                f"parser raised {error.__class__.__name__}: {error}")
        self._insert(parsed.rows)
        return parsed

    def _insert(self, rows):
        if not rows.rows:
            return
        self.conn.executemany(
            OBS_INSERT,
            [(row["obs_id"], row["seq"], row["kind"], row["ts_start"], row["ts_end"],
              row["duration_ms"], row["seam"], row["run_id"], row["lane_id"],
              row["item_id"], row["tick_id"], row["session_id"], row["route_id"],
              row["span_id"], row["summary"], row["payload"], row["source"])
             for row in rows.rows])

    def _unreadable(self, trace, artifact, path, reason):
        rows = Rows("unreadable", source=str(path or ""))
        rows.add_unreadable(trace, artifact, path or "", reason)
        self._insert(rows)

    def _tick_decisions(self, run_ids):
        """`tick.decision` rows from the store's `dispatches` (§4.3), for the loaded runs.

        The store is authoritative for the decision, so the row is stamped with the
        decision's own time and the run it names.
        """
        store = Store(self.store_path)
        try:
            if store.state != "ok" or "dispatches" not in store.tables:
                return
            for row in store.rows(
                    "SELECT decision_id, item_id, lane_id, run_id, action, reason,"
                    " dispatched_at FROM dispatches"):
                if row["run_id"] not in run_ids:
                    continue
                payload = {"outcome": row["action"], "reason": row["reason"]}
                self.conn.execute(
                    "INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start,"
                    " run_id, lane_id, item_id, span_id, summary, payload, source)"
                    " VALUES (?,?,?,?,?,?,?,?,?,?,?)",
                    (f"obs-dispatch-{row['decision_id']}", -1, "tick.decision",
                     _iso_from_ms(row["dispatched_at"]), row["run_id"], row["lane_id"],
                     row["item_id"], row["decision_id"],
                     f"{row['action']} {row['reason']}".strip()[:EXCERPT_MAX],
                     json.dumps(payload), str(self.store_path)))
        finally:
            store.close()

    def _sync_unreadable_store(self):
        if self.store_path.exists():
            return
        self.conn.execute(
            "INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start, run_id,"
            " span_id, summary, payload, source) VALUES (?,?,?,?,?,?,?,?,?)",
            (f"obs-store-{UNREADABLE}", 0, UNREADABLE, now_iso(), "unattributed",
             "unreadable-store", f"store: unreadable (no store at {self.store_path})",
             json.dumps({"artifact": "state.db", "path": str(self.store_path),
                         "reason": "absent"}), str(self.store_path)))


# ---------------------------------------------------------------- claims


MODULE_ROOTS = {"tools": 2, "yolo-packs": 2, "tests": 1}


def module_of(path):
    """The directory a §8.3 owed suite is scoped to, or None for a path owing none.

    Tool-call paths are absolute inside the run's worktree, so the module is found by
    the depth the repository puts it at, not by the first path component: an edit to
    `.../worktree/tools/tillr-store/read.go` is `tools/tillr-store` whatever the
    worktree is called. A path outside those roots — a root-level file such as
    `AGENTS.md`, or a document under `docs/` — owes no module suite and yields None
    rather than a directory-shaped claim nobody can test.
    """
    if not path:
        return None
    parts = [part for part in str(path).split("/") if part not in ("", ".")]
    for index, part in enumerate(parts):
        depth = MODULE_ROOTS.get(part)
        if depth and len(parts) >= index + depth:
            return "/".join(parts[index:index + depth])
    return None


def observed_edits(conn, run_id):
    """(ts, module) for every file a run edited, from `tool.call` rows.

    Only the tools that write are read. A `bash` command that redirects into a file
    is deliberately not counted: guessing which shell string writes which path is
    how a checker produces false disprovals, and the shim's `proc.exec` rows carry no
    path list either. The consequence is stated in the README: an edit made only
    through a shell heredoc is invisible to the temporal join.
    """
    edits = []
    for ts, payload in conn.execute(
            "SELECT ts_start, payload FROM obs_events WHERE run_id = ? AND"
            " kind = 'tool.call'", (run_id,)):
        data = json.loads(payload)
        if data.get("tool_name") in EDIT_TOOLS and data.get("target_path"):
            module = module_of(data["target_path"])
            if module:
                edits.append((ts, module))
    return edits


def observed_tests(conn, run_id):
    """(ts_start, module, exit_code, span_id, command) for every test command run."""
    tests = []
    for ts, span, payload in conn.execute(
            "SELECT ts_start, span_id, payload FROM obs_events WHERE run_id = ? AND"
            " kind = 'proc.exec'", (run_id,)):
        data = json.loads(payload)
        command = " ".join(data.get("argv") or [])
        if not TEST_COMMAND_RE.search(command):
            continue
        module = data.get("repo_rel_cwd") or module_of(data.get("cwd"))
        tests.append((ts, module, data.get("exit_code"), span, command))
    return tests


COUNT_WORDS = {"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
               "seven": 7, "eight": 8, "nine": 9, "ten": 10}
QUANTIFIER_RE = re.compile(
    r"all\s+(\d+|one|two|three|four|five|six|seven|eight|nine|ten)\s+"
    r"(suites|tests|packages|modules)\s+"
    r"(pass|passed|exit 0|exits 0|succeeded|succeed|are green|green)",
    re.IGNORECASE)
BACKTICK_RE = re.compile(r"`([^`\n]{3,120})`")


def _run_clock(run):
    """When a run's record says it finished (else started), as the claim's time.

    A claim is derived from the run's artifacts, so its time is the run's own: the clock
    a reader happened to read it at would differ on every read and say nothing.
    """
    return _iso_from_ms(run.get("finished_at") or run.get("started_at")) or ""


def _claim_row(run, kind, text, status, reason, evidence, last_edit):
    return (
        f"claim-{run['run_id']}-{kind}-{hashlib.sha256(text.encode()).hexdigest()[:8]}",
        run["run_id"], run.get("lane_id"), run.get("item_id"), kind, text[:EXCERPT_MAX],
        status, reason[:EXCERPT_MAX], json.dumps(list(evidence)), last_edit,
        _run_clock(run),
    )


def verify_run(conn, run, summary):
    """The §8.3 post-edit temporal join, plus it's two prose classes.

    Every verdict is `verified`, `disproved` or `unverified` — and `unverified` is
    what a claim gets when the record cannot answer it: no proc rows at all, no
    candidate shas, a truncated log. Absence of evidence is never reported as proof.
    """
    claims = []
    proc_state = run.get("proc_state")
    have_proc = proc_state == "ok"
    edits = observed_edits(conn, run["run_id"])
    tests = observed_tests(conn, run["run_id"])
    passed = [test for test in tests if test[2] == 0]

    # Class 1: owed suites. For each module the run edited, the suite must have
    # passed AFTER the last edit under it.
    for module in sorted({module for _, module in edits}):
        module_edits = [ts for ts, mod in edits if mod == module]
        last_edit = max(module_edits)
        after = [test for test in passed
                 if test[1] == module and test[0] and test[0] > last_edit]
        before = [test for test in passed
                  if test[1] == module and test[0] and test[0] <= last_edit]
        text = f"owed suite: {module}"
        if after:
            claims.append(_claim_row(
                run, "owed_suite", text, "verified", "",
                [test[3] for test in after], last_edit))
        elif not have_proc:
            claims.append(_claim_row(
                run, "owed_suite", text, "unverified",
                f"no proc.exec record for this run (proc-exec.jsonl: {proc_state})",
                [], last_edit))
        elif before:
            claims.append(_claim_row(
                run, "owed_suite", text, "disproved",
                f"{module}: suite passed only before the last edit at {last_edit}",
                [test[3] for test in before], last_edit))
        else:
            claims.append(_claim_row(
                run, "owed_suite", text, "disproved",
                f"{module}: no suite ran at all (last edit {last_edit})",
                [], last_edit))

    # Class 2a: prose quantifiers from the final assistant message.
    final_text = summary.get("final_text") or ""
    for match in QUANTIFIER_RE.finditer(final_text):
        raw, noun = match.group(1).lower(), match.group(2).lower()
        claimed = int(raw) if raw.isdigit() else COUNT_WORDS.get(raw)
        if not claimed:
            continue
        passing_modules = sorted({test[1] for test in passed if test[1]})
        text = match.group(0)
        if claimed <= len(passing_modules):
            claims.append(_claim_row(run, "suite_count", text, "verified", "",
                                     [test[3] for test in passed], None))
        elif not have_proc:
            claims.append(_claim_row(
                run, "suite_count", text, "unverified",
                f"no proc.exec record for this run (proc-exec.jsonl: {proc_state})", [], None))
        else:
            claims.append(_claim_row(
                run, "suite_count", text, "disproved",
                f"claimed {claimed} passing {noun}; observed {len(passing_modules)}"
                f" distinct passing module(s)", [test[3] for test in passed], None))

    # Class 2b: backticked command claims.
    for command in dict.fromkeys(BACKTICK_RE.findall(final_text)):
        if not TEST_COMMAND_RE.search(command):
            continue
        needle = command.strip().split("&&")[-1].strip()[:60]
        matches = [test for test in tests if needle and needle in test[4]]
        text = f"`{needle}`"
        if any(test[2] == 0 for test in matches):
            claims.append(_claim_row(run, "command_exit", text, "verified", "",
                                     [test[3] for test in matches], None))
        elif matches:
            claims.append(_claim_row(
                run, "command_exit", text, "disproved",
                "command ran and exited nonzero: "
                + ",".join(str(test[2]) for test in matches),
                [test[3] for test in matches], None))
        elif not have_proc:
            claims.append(_claim_row(
                run, "command_exit", text, "unverified",
                f"no proc.exec record for this run (proc-exec.jsonl: {proc_state})", [], None))
        else:
            claims.append(_claim_row(run, "command_exit", text, "unverified",
                                     "command appears in no proc.exec row", [], None))

    # Class 3: a `RESULT: done` on a run that committed nothing.
    if re.search(r"RESULT:\s*done", final_text):
        base, head, tree = run.get("base_sha"), run.get("head_sha"), run.get("tree_sha")
        if head and base and tree:
            if head == base and tree != base:
                claims.append(_claim_row(
                    run, "candidate_commit", "RESULT: done", "disproved",
                    "uncommitted_worktree_with_result_done: head_sha == base_sha"
                    " but tree_sha differs", [], None))
            elif head == base:
                claims.append(_claim_row(
                    run, "candidate_commit", "RESULT: done", "disproved",
                    "no candidate commit: head_sha == base_sha and tree unchanged",
                    [], None))
            else:
                claims.append(_claim_row(
                    run, "candidate_commit", "RESULT: done", "verified", "", [], None))
        else:
            claims.append(_claim_row(
                run, "candidate_commit", "RESULT: done", "unverified",
                "no candidate shas recorded for this run", [], None))
    return claims


# ---------------------------------------------------------------- queries


def q_tick(conn, tick_id):
    if not tick_id:
        return {"tick_id": None, "reason": "no tick_id recorded in any artifact", "rows": []}
    rows = conn.execute(
        "SELECT ts_start, lane_id, item_id, run_id, kind, summary, duration_ms, payload"
        " FROM obs_events WHERE tick_id = ? ORDER BY ts_start ASC, seq ASC",
        (tick_id,)).fetchall()
    out = []
    for row in rows:
        payload = json.loads(row["payload"])
        out.append({
            "ts": row["ts_start"], "lane": row["lane_id"], "item": row["item_id"],
            "run": row["run_id"], "kind": row["kind"],
            "summary": row["summary"], "duration_ms": row["duration_ms"],
            "exit_code": payload.get("exit_code"),
        })
    return {"tick_id": tick_id, "rows": out}


def q_audit(conn, run_id):
    run = conn.execute("SELECT * FROM runs_index WHERE run_id = ?", (run_id,)).fetchone()
    if run is None:
        return None
    rows = conn.execute(
        "SELECT ts_start, kind, summary, duration_ms, payload FROM obs_events"
        " WHERE run_id = ? ORDER BY ts_start ASC, seq ASC", (run_id,)).fetchall()
    claims = conn.execute(
        "SELECT claim_kind, claim_text, observed_status, discrepancy_reason,"
        " last_dir_edit_at FROM claim_checks WHERE run_id = ?", (run_id,)).fetchall()
    turns = []
    commands = []
    unreadable = []
    for row in rows:
        payload = json.loads(row["payload"])
        if row["kind"] == UNREADABLE:
            unreadable.append(f"{payload.get('artifact')}: {payload.get('reason')}")
        elif row["kind"] == "llm.turn":
            turns.append({
                "model": payload.get("response_model") or payload.get("model"),
                "thinking": payload.get("thinking_level"),
                "tokens_in": (payload.get("tokens") or {}).get("in"),
                "tokens_out": (payload.get("tokens") or {}).get("out"),
                "cost_usd": payload.get("cost_usd"),
                "duration_ms": row["duration_ms"],
            })
        elif row["kind"] in ("proc.exec", "tool.call"):
            commands.append({
                "ts": row["ts_start"], "kind": row["kind"],
                "summary": row["summary"], "exit_code": payload.get("exit_code"),
                "cwd": payload.get("repo_rel_cwd") or payload.get("cwd"),
            })
    return {"run": dict(run), "turns": turns, "commands": commands,
            "unreadable": unreadable,
            "claims": [dict(claim) for claim in claims]}


def q_run(conn, run_id):
    """One run as `tillr-view` reads it: its row, its events in order, its claims.

    The events are every kind recorded for the run, ordered as they were recorded. An
    absent run answers `{"run": null}`. `payload` is each row's stored JSON text, so it is
    written into the document as it is rather than decoded and encoded again.
    """
    run = conn.execute("SELECT * FROM runs_index WHERE run_id = ?", (run_id,)).fetchone()
    if run is None:
        return {"run": None, "events": [], "claims": []}
    events = [
        ({"obs_id": row["obs_id"], "kind": row["kind"], "ts_start": row["ts_start"],
          "ts_end": row["ts_end"], "duration_ms": row["duration_ms"],
          "seam": row["seam"] or "", "span_id": row["span_id"] or "",
          "summary": row["summary"] or ""}, row["payload"])
        for row in conn.execute(
            "SELECT obs_id, kind, ts_start, ts_end, duration_ms, seam, span_id, summary,"
            " payload FROM obs_events WHERE run_id = ? ORDER BY ts_start, seq", (run_id,))]
    claims = [dict(row) for row in conn.execute(
        "SELECT claim_id, claim_kind, claim_text, observed_status, discrepancy_reason,"
        " observed_at FROM claim_checks WHERE run_id = ? ORDER BY claim_id", (run_id,))]
    return {"run": dict(run), "events": events, "claims": claims}


def write_run(result, out):
    """Write a `q_run` answer as one JSON document, splicing each stored payload in."""
    out.write('{"run": ' + json.dumps(result["run"]) + ', "claims": '
              + json.dumps(result["claims"]) + ', "events": [')
    for number, (event, payload) in enumerate(result["events"]):
        head = json.dumps(event)
        out.write(("," if number else "") + head[:-1] + ', "payload": ' + payload + "}")
    out.write("]}\n")


def q_cost(conn):
    rows = conn.execute(
        "SELECT COALESCE(lane_id, 'coordinator/subagent') AS lane,"
        " COALESCE(route_id, 'unrouted') AS route,"
        " json_extract(payload, '$.response_model') AS model,"
        " COUNT(DISTINCT run_id) AS runs,"
        " SUM(CASE WHEN json_extract(payload, '$.stop_reason') IN ('error','abort')"
        "     THEN 1 ELSE 0 END) AS retry_turns,"
        " ROUND(SUM(duration_ms) / 1000.0, 1) AS llm_wall_s,"
        " SUM(json_extract(payload, '$.tokens.in')) AS tok_in,"
        " SUM(json_extract(payload, '$.tokens.cache_read')) AS tok_cache_r,"
        " SUM(json_extract(payload, '$.tokens.out')) AS tok_out,"
        " SUM(json_extract(payload, '$.tokens.reasoning')) AS tok_think,"
        " ROUND(SUM(json_extract(payload, '$.cost_usd')), 6) AS cost_usd"
        " FROM obs_events WHERE kind = 'llm.turn'"
        " GROUP BY lane, route, model ORDER BY cost_usd DESC").fetchall()
    return [dict(row) for row in rows]


def q_discrepancies(conn, since=None, include_unverified=False):
    statuses = ("disproved", "unverified") if include_unverified else ("disproved",)
    placeholders = ",".join("?" for _ in statuses)
    sql = ("SELECT observed_at, run_id, lane_id, item_id, claim_kind, claim_text,"
           " observed_status, discrepancy_reason, last_dir_edit_at FROM claim_checks"
           f" WHERE observed_status IN ({placeholders})")
    params = list(statuses)
    if since:
        sql += " AND observed_at >= ?"
        params.append(since)
    sql += " ORDER BY observed_at DESC, run_id ASC"
    return [dict(row) for row in conn.execute(sql, params).fetchall()]


# ---------------------------------------------------------------- rendering


def render_tick(result):
    if not result.get("tick_id"):
        return f"tick: no rows ({result.get('reason')})"
    lines = [f"tick {result['tick_id']} — {len(result['rows'])} observed row(s)"]
    if not result["rows"]:
        lines.append("  (no obs.event row carries this tick_id; the store answered"
                     " nothing either — an empty tick, not a verified quiet one)")
    for row in result["rows"]:
        clock = (row["ts"] or "")[11:23]
        ec = "" if row["exit_code"] is None else f" rc={row['exit_code']}"
        lines.append(f"  {clock} {row['lane'] or '-':<8} {row['item'] or '-':<5}"
                     f" {row['kind']:<16} {row['summary'] or ''}{ec}")
    return "\n".join(lines)


def render_audit(result):
    if result is None:
        return "audit: no run row — no store row and no trace directory for that run-id"
    run = result["run"]
    lines = [
        f"run {run['run_id']}  lane={run['lane_id']} item={run['item_id']}"
        f" store_state={run['store_state']} exit={run['exit_code']}",
        f"  log {run['log_path']} [{run['log_state']}]  proc [{run['proc_state']}]",
        f"  base_sha={run['base_sha']} head_sha={run['head_sha']}"
        f" tree_sha={run['tree_sha']}",
    ]
    if run["detail"]:
        lines.append(f"  detail: {run['detail']}")
    for note in result["unreadable"]:
        lines.append(f"  unreadable: {note}")
    if result["turns"]:
        total = sum(turn["cost_usd"] or 0 for turn in result["turns"])
        tokens = sum(turn["tokens_out"] or 0 for turn in result["turns"])
        lines.append(f"  asked: {len(result['turns'])} model turn(s),"
                     f" {tokens} output token(s), ${total:.6f}")
    for turn in result["turns"][:10]:
        lines.append(f"    turn {turn['model']} thinking={turn['thinking']}"
                     f" in={turn['tokens_in']} out={turn['tokens_out']}"
                     f" ${turn['cost_usd']} {turn['duration_ms']}ms")
    if result["commands"]:
        lines.append(f"  ran: {len(result['commands'])} observed call(s)")
    for command in result["commands"][:25]:
        ec = "" if command["exit_code"] is None else f" rc={command['exit_code']}"
        lines.append(f"    {command['ts'][11:23]} {command['kind']:<10}"
                     f" {command['summary'] or ''}{ec}")
    if result["claims"]:
        lines.append("  claims:")
    for claim in result["claims"]:
        reason = f" — {claim['discrepancy_reason']}" if claim["discrepancy_reason"] else ""
        lines.append(f"    {claim['observed_status']:<10} {claim['claim_kind']:<14}"
                     f" {claim['claim_text']}{reason}")
    return "\n".join(lines)


def render_cost(rows):
    if not rows:
        return "cost: no llm.turn row — no run log held a model turn (see `audit <run-id>`)"
    head = (f"{'lane':<22} {'route':<58} {'runs':>4} {'retry':>5} {'wall_s':>8}"
            f" {'tok_in':>8} {'cache_r':>9} {'tok_out':>7} {'think':>6} {'cost_usd':>10}")
    lines = [head]
    for row in rows:
        lines.append(
            f"{row['lane'][:22]:<22} {(row['route'] or ''):<58} {row['runs']:>4}"
            f" {row['retry_turns'] or 0:>5} {row['llm_wall_s'] or 0:>8}"
            f" {row['tok_in'] or 0:>8} {row['tok_cache_r'] or 0:>9}"
            f" {row['tok_out'] or 0:>7} {row['tok_think'] or 0:>6}"
            f" {row['cost_usd'] or 0:>10.6f}")
    lines.append("(retry = turns whose stop_reason is error/abort; llm.retry rows are"
                 " Slice 3, so a 429 retried inside one turn is not counted)")
    return "\n".join(lines)


def render_discrepancies(rows, include_unverified=False):
    if not rows:
        what = "claim" if include_unverified else "disproved claim"
        return f"discrepancies: none (no {what} in the runs read)"
    lines = [f"discrepancies: {len(rows)} row(s)"]
    for row in rows:
        lines.append(f"  {row['observed_at'][:19]} {row['observed_status']:<10}"
                     f" {row['run_id']} {row['claim_kind']:<14}"
                     f" {row['claim_text']}")
        if row["discrepancy_reason"]:
            lines.append(f"      {row['discrepancy_reason']}")
    return "\n".join(lines)


# ---------------------------------------------------------------- main


RUNS_HELP = (f"read the N most recent runs, by the store's start time (default"
             f" {DEFAULT_RUNS}; 0 reads every run, which parses every recorded artifact"
             f" and takes minutes on a large corpus)")


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="tillr-trace",
        description="Answers over recorded run artifacts, read on demand. Never a source"
                    " of truth, and keeps nothing: every command reads the artifacts it"
                    " needs and writes no file.")
    parser.add_argument("--trace-root", type=Path, default=trace_root_default(),
                        help="where the recorder's `runs/<run-id>/` directories are"
                             " (default: $TILLR_TRACE_ROOT or the state home)")
    parser.add_argument("--store", type=Path, default=Path(".tillr/state.db"))
    parser.add_argument("--loop-dir", type=Path, default=None,
                        help="the drain's per-run record directory (default: the store's"
                             " sibling `loop`)")
    sub = parser.add_subparsers(dest="command", required=True)

    tick = sub.add_parser("tick", help="one tick, in order")
    tick.add_argument("tick_id", nargs="?")
    tick.add_argument("--last", action="store_true",
                      help="the tick named by the most recent run that names one")

    audit = sub.add_parser("audit", help="one run: asked, ran, changed, cost")
    audit.add_argument("run_id")

    run = sub.add_parser("run", help="one run's row, events and claims, as JSON"
                                     " (what tillr-view reads)")
    run.add_argument("run_id")

    cost = sub.add_parser("cost", help="per lane, per model, per route")
    cost.add_argument("--runs", type=int, default=DEFAULT_RUNS, metavar="N", help=RUNS_HELP)

    disc = sub.add_parser("discrepancies", help="claims the record does not support")
    disc.add_argument("--since", help="only claims of runs that finished at or after this"
                                      " UTC time (ISO 8601, e.g. 2026-11-18T00:00:00Z)")
    disc.add_argument("--all", action="store_true", help="include unverified rows")
    disc.add_argument("--runs", type=int, default=DEFAULT_RUNS, metavar="N", help=RUNS_HELP)

    args = parser.parse_args(argv)
    corpus = Corpus(args.trace_root, args.store, args.loop_dir)
    try:
        conn = corpus.conn
        if args.command == "tick":
            tick_id = args.tick_id
            ticks = {run_id: corpus.tick_of(run_id) for run_id in corpus.run_ids()}
            if args.last and not tick_id:
                tick_id = next((t for t in ticks.values() if t), None)
            corpus.load([run_id for run_id, t in ticks.items() if t and t == tick_id],
                        claims=False)
            print(render_tick(q_tick(conn, tick_id)))
        elif args.command in ("audit", "run"):
            if corpus.known(args.run_id):
                corpus.load([args.run_id])
            if args.command == "audit":
                print(render_audit(q_audit(conn, args.run_id)))
            else:
                write_run(q_run(conn, args.run_id), sys.stdout)
        elif args.command == "cost":
            corpus.load(corpus.run_ids(args.runs), kinds={"llm.turn"}, claims=False)
            print(render_cost(q_cost(conn)))
        elif args.command == "discrepancies":
            corpus.load(corpus.run_ids(args.runs), kinds=set(), claims=True)
            print(render_discrepancies(q_discrepancies(conn, args.since, args.all),
                                       args.all))
    finally:
        corpus.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
