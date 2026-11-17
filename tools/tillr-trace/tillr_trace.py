#!/usr/bin/env python3
"""tillr-trace: a derived read model over recorded run artifacts.

Nothing here is a source of truth. `.tillr/state.db` owns workflow state, the
`<run-id>.log` owns what the model saw and spent, and `proc-exec.jsonl` /
`git-trace2.jsonl` own what ran. This module parses those into a rebuildable
`index.db` and answers four questions over it. If the index and a source disagree,
the source is right; see README.md.

Design: docs/design/session-recording.md §3.2 (layout), §4.2 (trace_key),
§4.3 (obs.event), §6.3 (store authority), §8.3 (claim verification).
"""

from __future__ import annotations

import argparse
import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
import re
import itertools
import sqlite3
import sys
from pathlib import Path

# Artifact the indexer could not read. The design's twelve obs.event kinds describe
# what a run did; this one describes what the indexer could not see, so a missing log
# is a row that says so rather than a zero that reads as "nothing happened".
UNREADABLE = "unreadable"

EXCERPT_MAX = 200

# The index's own layout. The index is derived, so a version that does not match is
# dropped and rebuilt rather than migrated: a migration would be a second spelling of
# the build, and the one thing this tool must never do is answer from a half-built one.
SCHEMA_VERSION = 3

# The page cache one ingest may hold, in KiB (SQLite reads a negative `cache_size` as
# KiB). The default is 2 MiB, and an index of tens of millions of rows then re-reads its
# B-tree pages from the OS for nearly every insert.
CACHE_KIB = 65536

# §5.1 rule 1: a credential never reaches an index row, even inside an excerpt.
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

SCHEMA = """
CREATE TABLE IF NOT EXISTS obs_events (
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
CREATE INDEX IF NOT EXISTS obs_events_run  ON obs_events(run_id, kind, ts_start);
CREATE INDEX IF NOT EXISTS obs_events_tick ON obs_events(tick_id, ts_start);
CREATE INDEX IF NOT EXISTS obs_events_kind ON obs_events(kind, ts_start);

CREATE TABLE IF NOT EXISTS runs_index (
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

CREATE TABLE IF NOT EXISTS claim_checks (
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

CREATE TABLE IF NOT EXISTS ingested_files (
  path         TEXT PRIMARY KEY,
  run_id       TEXT,
  mtime_ns     INTEGER NOT NULL,
  size_bytes   INTEGER NOT NULL,
  parsed_bytes INTEGER NOT NULL DEFAULT 0,
  rows_parsed  INTEGER NOT NULL DEFAULT 0,
  state        TEXT NOT NULL DEFAULT 'ok',
  detail       TEXT NOT NULL DEFAULT '',
  carry        TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS index_meta (
  key   TEXT PRIMARY KEY,
  value INTEGER NOT NULL
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
    """Normalize a trace2 RFC3339 timestamp to millisecond UTC, keeping the order."""
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


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def trace_root_default():
    override = os.environ.get("TILLR_TRACE_ROOT")
    if override:
        return Path(override)
    state = os.environ.get("XDG_STATE_HOME")
    base = Path(state) if state else Path.home() / ".local" / "state"
    return base / "tillr-trace" / "v1"


# ---------------------------------------------------------------- obs rows


_SEQ = itertools.count(1)


def reset_seq(conn):
    """Continue the build-wide row counter where the index left it.

    `seq` orders two rows a run wrote in the same millisecond, so an appended row must
    sort after everything already stored. It restarts from the value the last commit
    recorded in `index_meta` rather than from `MAX(seq)`: `seq` has no index, so that
    maximum was a walk of the whole table on every sync.
    """
    global _SEQ
    row = conn.execute("SELECT value FROM index_meta WHERE key = 'next_seq'").fetchone()
    _SEQ = itertools.count(row[0] if row else 1)
    return _SEQ


def save_seq(conn):
    """Record where the row counter stands, in the transaction that used it."""
    global _SEQ
    upcoming = next(_SEQ)
    _SEQ = itertools.count(upcoming)
    conn.execute(
        "INSERT INTO index_meta (key, value) VALUES ('next_seq', ?)"
        " ON CONFLICT(key) DO UPDATE SET value = excluded.value", (upcoming,))


def _source_slug(path):
    """A short, stable stamp for one source file, used in its rows' obs ids."""
    return hashlib.sha256(str(path or "").encode("utf-8")).hexdigest()[:12]


class Rows:
    """Accumulates obs.event rows for one artifact, in file order.

    `seq` is one build-wide counter so a query can break a tie between two rows the
    trace wrote in the same millisecond; the artifact's own order is the only ordering
    the files carry. The obs id is the source file's stamp plus the row's ordinal in
    that file, which is what lets a later build append a row without renumbering, or
    renumbering, the rows already stored for it. `ordinal` is therefore the count of
    ordinal-consuming rows this artifact has produced across every build, and a resumed
    parse passes the previous count in.
    """

    def __init__(self, origin="run", source="", ordinal=0):
        self.rows = []
        self.origin = origin
        self.source = str(source or "")
        self.ordinal = ordinal
        self._slug = _source_slug(self.source)

    def add(self, kind, trace, *, ts_start, ts_end=None, duration_ms=None,
            seam=None, span_id=None, summary=None, payload=None):
        seq = next(_SEQ)
        ordinal = self.ordinal
        self.ordinal += 1
        run_id = (trace or {}).get("run_id") or "unattributed"
        self.rows.append(
            {
                "obs_id": f"obs-{run_id}-{self._slug}-{ordinal:06d}",
                "seq": seq,
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
        """One row saying an artifact could not be read.

        Its id is derived from the run and the artifact, not from the row counter: the
        row is a statement about the artifact's state, so deriving it again on the next
        build must replace it rather than add a second one.
        """
        run_id = (trace or {}).get("run_id") or "unattributed"
        self.rows.append(
            {
                "obs_id": f"obs-{run_id}-unreadable-{artifact}",
                "seq": next(_SEQ),
                "kind": UNREADABLE,
                "ts_start": now_iso(),
                "ts_end": None,
                "duration_ms": None,
                "seam": "indexer",
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
    """What one artifact parse produced, and where the next one resumes.

    `consumed` is the byte offset the next build starts from, `carry` is the parser
    state that has to cross that boundary — an unpaired tool call's args, the last clock
    the stream wrote, a git command still open — and `summary` is the running log total a
    resumed parse adds to instead of starting over.

    A plain class rather than a dataclass on purpose: the suite imports this module from
    a file path, so it is not in `sys.modules` under its own name, and a dataclass
    resolves its annotations through that entry.
    """

    __slots__ = ("rows", "state", "detail", "consumed", "carry", "summary")

    def __init__(self, rows, state="ok", detail="", consumed=0, carry=None, summary=None):
        self.rows = rows
        self.state = state
        self.detail = detail
        self.consumed = consumed
        self.carry = carry if carry is not None else {}
        self.summary = summary if summary is not None else {}


def _jsonl_lines(path, start=0, wanted=None):
    """Yield `(line_start, line_end, obj, parsed, complete)` for each line from `start`.

    One line is read, decoded and handed on before the next is read, so a caller holds
    one line and its row — never the whole file. `complete` is False for a final line
    the writer has not finished appending; the caller leaves that line unread so the
    next build parses it whole instead of dropping or half-parsing it.

    `wanted` is the byte strings a line the caller reads must contain. A complete line
    holding none of them is passed on as `None` without being decoded: most of a git
    trace is region and data events no row is made from, and decoding them was most of
    the parse.
    """
    with open(path, "rb") as handle:
        handle.seek(start)
        offset = start
        while True:
            raw = handle.readline()
            if not raw:
                return
            line_start = offset
            offset += len(raw)
            complete = raw.endswith(b"\n")
            if wanted and complete and not any(marker in raw for marker in wanted):
                yield line_start, offset, None, True, complete
                continue
            text = raw.decode("utf-8", "replace").strip()
            if not text:
                yield line_start, offset, None, True, complete
                continue
            try:
                yield line_start, offset, json.loads(text), True, complete
            except json.JSONDecodeError:
                yield line_start, offset, None, False, complete


class Stream:
    """How far one JSONL parse got, and what it could not read on the way.

    `state` is `ok`, `truncated` (the final line is incomplete — a writer caught
    mid-append) or `corrupt` (a complete line failed to parse, which no live writer
    does). `consumed` stops before the incomplete tail, so the next build re-reads it
    once it is whole.
    """

    def __init__(self, start=0):
        self.consumed = start
        self.bad = 0
        self.truncated = False

    def feed(self, line_start, line_end, parsed, complete):
        """Record one line; answer False when the parse must stop here."""
        if not parsed:
            if not complete:
                self.truncated = True
                self.consumed = line_start
                return False
            self.bad += 1
        self.consumed = line_end
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


def parse_run_log(path, trace, start=0, ordinal=0, carry=None):
    """Recover llm.turn and tool.call rows from `pi --mode json` output.

    Measured shape (2026-11-12 canary, tools/tillr-trace/testdata/canary-run.log):
    a `session` header carrying `id`/`cwd`, one `turn_end` per model turn whose
    `message` holds `provider`, `model`, `responseModel`, `thinkingLevel`, `usage`
    (`input`/`output`/`cacheRead`/`cacheWrite`/`reasoning` and `cost.total`),
    `credential.fingerprint`, `stopReason` and `durationMs`, and one
    `tool_execution_start`/`tool_execution_end` pair per tool call.

    A resumed parse starts at `start` with the args, clock, turn number, session id and
    running totals the previous build carried, so a pair or a total split across two
    builds is neither lost nor counted twice.
    """
    carry = dict(carry or {})
    summary = dict(carry.get("summary") or _empty_summary())
    rows = Rows("log", source=str(path or ""), ordinal=ordinal)
    if not path:
        return Parsed(rows, "missing", "no log path recorded", start, carry, summary)

    args_by_call = dict(carry.get("args") or {})
    turn_no = int(carry.get("turn_no") or 0)
    final_text = summary.get("final_text") or ""
    if carry.get("session_id") and not (trace or {}).get("session_id"):
        trace = dict(trace or {})
        trace["session_id"] = carry["session_id"]
    # `tool_execution_*` events carry no clock of their own — only the session header,
    # `request_start` and `turn_end.message` do. A tool call is therefore stamped with
    # the last clock the stream wrote before it, and its payload says the time is
    # derived, so a reader never takes an anchored time for an observed one.
    anchor = {"ms": carry.get("anchor_ms"), "derived": bool(carry.get("anchor_derived"))}
    stream = Stream(start)
    try:
        for line_start, line_end, event, parsed, complete in _jsonl_lines(path, start):
            if not stream.feed(line_start, line_end, parsed, complete):
                break
            if not parsed or not isinstance(event, dict):
                continue
            kind = event.get("type")
            if kind == "session":
                trace = dict(trace or {})
                trace.setdefault("session_id", event.get("id"))
                trace["worktree"] = event.get("cwd")
                carry["session_id"] = trace.get("session_id")
                if _ms_from_iso(event.get("timestamp")) is not None:
                    anchor["ms"] = _ms_from_iso(event.get("timestamp"))
                    anchor["derived"] = False
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
                    anchor["ms"], anchor["derived"] = event["timestamp"], False
            elif kind == "tool_execution_start":
                args_by_call[event.get("toolCallId")] = event.get("args") or {}
            elif kind == "tool_execution_end":
                call_id = event.get("toolCallId")
                args = args_by_call.pop(call_id, {})
                tool = event.get("toolName") or "unknown"
                result = event.get("result") or {}
                structured = result.get("structuredContent") or {}
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
                message = event.get("message") or {}
                if message.get("role") != "assistant":
                    continue
                turn_no += 1
                if message.get("timestamp") is not None:
                    anchor["ms"], anchor["derived"] = message["timestamp"], False
                usage = message.get("usage") or {}
                cost = (usage.get("cost") or {})
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
        return Parsed(rows, "missing", f"no file at {path}", start, carry, summary)
    except OSError as error:
        # The rows read before the failure are kept, and so is where they stopped, so the
        # next build resumes after them instead of re-reading and renumbering them.
        return Parsed(rows, "unreadable", f"{error.__class__.__name__}: {error}",
                      stream.consumed, carry, summary)
    carry["anchor_ms"] = anchor["ms"]
    carry["anchor_derived"] = anchor["derived"]
    carry["args"] = args_by_call
    carry["turn_no"] = turn_no
    summary["final_text"] = scrub(final_text, limit=20000)
    carry["summary"] = summary
    state, detail = stream.state(rows.ordinal)
    return Parsed(rows, state, detail, stream.consumed, carry, summary)


# ---------------------------------------------------------------- proc / git


def parse_proc_exec(path, trace, start=0, ordinal=0, carry=None):
    """Raw shim rows (§4.3 `proc.exec`, specimen in §7.1) to normalized rows.

    The file only ever grows, so a resumed parse starts at the last byte it read and
    hands back one row per new line. Nothing about a `proc.exec` row spans two lines,
    which is why this parser carries no state across that boundary.
    """
    rows = Rows("proc", source=str(path or ""), ordinal=ordinal)
    if not path:
        return Parsed(rows, "missing", "no proc-exec.jsonl recorded", start)
    stream = Stream(start)
    try:
        for line_start, line_end, event, parsed, complete in _jsonl_lines(path, start):
            if not stream.feed(line_start, line_end, parsed, complete):
                break
            if not parsed or not isinstance(event, dict):
                continue
            _proc_row(rows, trace, event)
    except FileNotFoundError:
        return Parsed(rows, "missing", f"no file at {path}", start)
    except OSError as error:
        return Parsed(rows, "unreadable", f"{error.__class__.__name__}: {error}",
                      stream.consumed)
    # A present-but-empty file is the same evidence as no file at all: the shim creates
    # nothing and only appends, so 0 bytes says a writer touched the path and nothing
    # more. Read as `ok` it makes `have_proc` true and turns every owed suite into a
    # `disproved` "no suite ran at all" — absence of evidence promoted to proof (§8.3).
    if not rows.rows and rows.ordinal == 0:
        return Parsed(rows, "missing", f"{path}: no proc.exec rows", stream.consumed)
    state, detail = stream.state(rows.ordinal)
    return Parsed(rows, state, detail, stream.consumed)


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


def parse_git_trace2(path, trace, start=0, ordinal=0, carry=None):
    """GIT_TRACE2_EVENT start/exit pairs to §4.3 `git.trace2` rows.

    A `start` and its `exit` are two lines, and a build can land between them. The
    commands still open when the parse stops travel in `carry`, so the line that closes
    one in a later build still pairs with it instead of reading as an exit with no
    command.
    """
    carry = dict(carry or {})
    rows = Rows("git", source=str(path or ""), ordinal=ordinal)
    if not path:
        return Parsed(rows, "missing", "no git-trace2.jsonl recorded", start, carry)
    open_sids = dict(carry.get("open") or {})
    stream = Stream(start)
    try:
        for line_start, line_end, event, parsed, complete in _jsonl_lines(
                path, start, wanted=GIT_EVENTS_READ):
            if not stream.feed(line_start, line_end, parsed, complete):
                break
            if not parsed or not isinstance(event, dict):
                continue
            sid = event.get("sid")
            name = event.get("event")
            if name == "start":
                open_sids[sid] = event
            elif name == "exit":
                _git_row(rows, trace, open_sids.pop(sid, {}), event)
    except FileNotFoundError:
        return Parsed(rows, "missing", f"no file at {path}", start, carry)
    except OSError as error:
        return Parsed(rows, "unreadable", f"{error.__class__.__name__}: {error}",
                      stream.consumed, carry)
    carry["open"] = open_sids
    state, detail = stream.state(rows.ordinal)
    return Parsed(rows, state, detail, stream.consumed, carry)


# The two trace2 events a `git.trace2` row is made from. Every event line names its kind
# as a JSON string, so a line holding neither string is not one of them.
GIT_EVENTS_READ = (b'"start"', b'"exit"')


def _git_row(rows, trace, started, event):
    sid = event.get("sid")
    argv = started.get("argv") or []
    rows.add(
        "git.trace2", trace,
        ts_start=_iso_from_text(started.get("time")),
        ts_end=_iso_from_text(event.get("time")),
        duration_ms=int(round(float(event.get("t_abs") or 0) * 1000)) or None,
        seam="git-trace2",
        span_id=sid,
        summary=scrub(" ".join(str(part) for part in argv), 120),
        payload={
            "git_sid": sid,
            "command": _git_command(argv),
            "argv": [scrub(str(part), 512) for part in argv],
            "repo_rel_cwd": None,
            "exit_code": event.get("code"),
            "elapsed_ms": int(round(float(event.get("t_abs") or 0) * 1000)) or None,
        },
    )


def parse_gate_record(path, trace, start=0, ordinal=0, carry=None):
    """One `.tillr/loop/<run-id>/gate.{plan,verdict}.json` to a §4.3 row.

    Both are `record.Envelope` files the gate writes through `tillr-loop`, one record
    each, so each is read as a single JSON object rather than as JSONL. A file that does
    not parse becomes an `unreadable` row naming it: a gate that ran and left a record
    nobody can read must never look like a gate that never ran. These files are small
    and rewritten whole, so they are always parsed whole and their rows replaced.
    """
    path = Path(path)
    kind = "gate.verdict" if path.name.startswith("gate.verdict") else "gate.plan"
    rows = Rows("gate", source=str(path))
    try:
        envelope = json.loads(path.read_text(encoding="utf-8", errors="replace"))
    except FileNotFoundError:
        return Parsed(rows, "missing", f"no file at {path}", 0)
    except (OSError, ValueError) as error:
        rows.add_unreadable(trace, path.name, path, f"unparseable: {error}")
        return Parsed(rows, "corrupt", f"unparseable: {error}", 0)
    if not isinstance(envelope, dict) or envelope.get("kind") != kind:
        rows.add_unreadable(trace, path.name, path, f"not a {kind} record")
        return Parsed(rows, "corrupt", f"not a {kind} record", 0)
    payload = envelope.get("payload")
    if not isinstance(payload, dict):
        rows.add_unreadable(trace, path.name, path, f"{kind} payload is not an object")
        return Parsed(rows, "corrupt", f"{kind} payload is not an object", 0)
    rows.add(
        kind, trace,
        ts_start=_iso_from_text(envelope.get("created_at")) or now_iso(),
        seam="tillr-gate",
        span_id=str(envelope.get("id") or kind),
        summary=f"{kind} {payload.get('plan_id') or payload.get('verdict_id') or ''}".strip(),
        payload=_gate_payload(kind, payload),
    )
    return Parsed(rows, "ok", f"{kind} read", 0)


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


def _git_command(argv):
    for part in argv:
        if part in ("-c",):
            continue
        if not str(part).startswith("-") and not str(part).startswith("/"):
            return str(part)
    return None


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


# ---------------------------------------------------------------- build


# The runs_index columns, in the order every INSERT and the claim join read them.
RUN_COLUMNS = (
    "run_id", "lane_id", "item_id", "backend", "store_state", "exit_code",
    "started_at", "finished_at", "log_path", "log_state", "proc_path", "proc_state",
    "git_path", "git_state", "brief_path", "fence_path", "base_sha", "head_sha",
    "tree_sha", "detail",
)

RUN_INSERT = (
    "INSERT INTO runs_index (" + ", ".join(RUN_COLUMNS) + ")"
    " VALUES (" + ",".join("?" for _ in RUN_COLUMNS) + ")"
    " ON CONFLICT(run_id) DO UPDATE SET "
    + ", ".join(f"{name}=excluded.{name}" for name in RUN_COLUMNS if name != "run_id")
)


class Index:
    """The build: a derived index kept current by parsing only what changed.

    Two kinds of source feed it, and they are read differently on purpose. The store
    is small and authoritative and is read on every sync — its modification time moves
    on every tick by design, so using it as a rebuild trigger would rebuild the whole
    corpus every tick. The JSONL and per-run artifacts are large and only ever grow, so
    each is watermarked by its own size and modification time and parsed from the last
    byte it was read to. A run with nothing new parses nothing.
    """

    def __init__(self, trace_root, store_path, index_path=None, rebuild=False):
        self.trace_root = Path(trace_root)
        self.store_path = Path(store_path)
        # A run's records live in two places: `<trace-root>/runs/<run-id>/` (the
        # recorder's own directory, §3.2) and `<root>/.tillr/loop/<run-id>/` (the drain's
        # per-run record directory, where staging keeps the brief, fence and base sha,
        # where `run-pi`'s emitters append `proc-exec.jsonl` and `git-trace2.jsonl`, and
        # where the gate leaves `gate.plan.json` / `gate.verdict.json`). `.tillr/loop` is
        # derived from the store path, which is the one path the caller already names.
        self.loop_root = self.store_path.parent / "loop"
        self.index_path = Path(index_path or self.trace_root / "index.db")
        self.rebuild = rebuild

    # -- source enumeration

    def _run_dirs(self):
        """run_id -> its artifact directories, most specific first.

        The loop directory is read first for every artifact, because it is where this
        workspace's emitters write; the trace-root directory stays readable so a run
        recorded by the design's own recorder layout is still indexed.
        """
        found = {}
        for root in (self.loop_root, self.trace_root / "runs"):
            if not root.is_dir():
                continue
            for child in root.iterdir():
                if child.is_dir():
                    found.setdefault(child.name, []).append(child)
        return found

    def _store_runs(self):
        store = Store(self.store_path)
        try:
            return {row["run_id"]: row for row in store.runs()}
        finally:
            store.close()

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

    def _run_sources(self, run_id, dirs, store_row):
        """The exact files one run's derivation reads, by role.

        `_source_map` and `_derive` both answer from here, so the watermark can never
        cover a file the build does not read (which would mark the run changed on every
        sync) or miss one it does read (which would silently skip a change).
        """
        dirs = [Path(directory) for directory in (dirs or [])]
        loop_dir = self.loop_root / run_id
        return {
            "log": self._log_path(run_id, dirs, store_row),
            "proc": run_artifact(dirs, "proc-exec.jsonl") if dirs else None,
            "git": run_artifact(dirs, "git-trace2.jsonl") if dirs else None,
            "manifest": run_artifact(dirs, "manifest.json") if dirs else None,
            "base_sha": run_artifact(dirs, "base_sha") if dirs else None,
            "brief": run_artifact(dirs, "brief.md") if dirs else None,
            "fence": run_artifact(dirs, "fence.txt") if dirs else None,
            "gate_plan": loop_dir / "gate.plan.json",
            "gate_verdict": loop_dir / "gate.verdict.json",
        }

    def _source_map(self, store_runs=None, run_dirs=None):
        """path -> run_id for every file a build reads, the store excluded.

        The store is absent on purpose: its modification time changes on every tick by
        design, so a watermark that covered it would differ after every tick and force a
        whole-corpus rebuild — which is the defect this exists to remove. It is read on
        every sync instead, because it is small and authoritative for the rows it owns.
        """
        store_runs = self._store_runs() if store_runs is None else store_runs
        run_dirs = self._run_dirs() if run_dirs is None else run_dirs
        files = {}
        for run_id in set(store_runs) | set(run_dirs):
            for path in self._run_sources(run_id, run_dirs.get(run_id),
                                          store_runs.get(run_id)).values():
                if path is not None:
                    files.setdefault(str(path), run_id)
        return files

    def _watermark(self, sources=None):
        stamp = {}
        for path in (self._source_map() if sources is None else sources):
            try:
                stat = Path(path).stat()
            except OSError:
                continue
            stamp[path] = (stat.st_mtime_ns, stat.st_size)
        return stamp

    def _lock_path(self):
        return self.index_path.with_name(self.index_path.name + ".lock")

    @contextlib.contextmanager
    def _writer(self):
        """Hold the index's one-writer lock, answering False when another sync holds it.

        The lock is taken without waiting. A second writer would either wait on the first
        one's transactions or parse the same new bytes again, and neither is work the
        caller needs: the sync already running, or the next one, reads what is new.
        """
        self.index_path.parent.mkdir(parents=True, exist_ok=True)
        with open(self._lock_path(), "a") as handle:
            try:
                fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                yield False
                return
            try:
                yield True
            finally:
                fcntl.flock(handle, fcntl.LOCK_UN)

    def _discard(self):
        """Remove the index and its side files, so the next open starts an empty one.

        The index is derived, so a layout it cannot trust is deleted rather than emptied.
        Emptying it in place is a `DELETE` of every row inside one transaction, which
        journals every page of the old file: measured on 2026-11-17, a 32 GB index grew a
        32 GB rollback journal that way before the rebuild had parsed anything.
        """
        for suffix in ("", "-wal", "-shm", "-journal"):
            with contextlib.suppress(FileNotFoundError):
                Path(str(self.index_path) + suffix).unlink()

    def _foreign_layout(self):
        """Whether the file at the index path is not this layout's index.

        A rollback journal beside the index can only be a writer of an older layout's —
        this one writes a WAL — and opening the file read-write would first roll that
        journal back, which costs as much as the transaction it undoes. Its version is
        therefore read from the file as it stands, without recovering anything.
        """
        if not self.index_path.exists():
            return False
        journal = Path(str(self.index_path) + "-journal")
        if not journal.exists():
            return False
        try:
            conn = sqlite3.connect(f"file:{self.index_path}?immutable=1", uri=True)
        except sqlite3.Error:
            return True
        try:
            return conn.execute("PRAGMA user_version").fetchone()[0] != SCHEMA_VERSION
        except sqlite3.DatabaseError:
            return True
        finally:
            conn.close()

    def _open(self):
        """The index's one read-write connection, on this layout, in WAL mode.

        WAL is what makes a killed writer cheap: its uncommitted frames are ignored by the
        next open instead of being rolled back into the file, and a read-only reader never
        meets a hot journal it cannot recover. An index of another layout, or a file that
        is not a database, is discarded and an empty index opened in its place.
        """
        if self._foreign_layout():
            self._discard()
        for attempt in (1, 2):
            conn = sqlite3.connect(str(self.index_path))
            try:
                conn.execute("PRAGMA journal_mode = WAL")
                conn.execute(f"PRAGMA cache_size = -{CACHE_KIB}")
                version = conn.execute("PRAGMA user_version").fetchone()[0]
            except sqlite3.DatabaseError:
                conn.close()
                if attempt == 2:
                    raise
                self._discard()
                continue
            if version == SCHEMA_VERSION:
                return conn
            fresh = not conn.execute(
                "SELECT 1 FROM sqlite_master WHERE type = 'table' LIMIT 1").fetchone()
            if fresh:
                conn.executescript(SCHEMA)
                conn.execute(f"PRAGMA user_version = {SCHEMA_VERSION}")
                conn.commit()
                return conn
            conn.close()
            if attempt == 2:
                raise sqlite3.DatabaseError(f"{self.index_path}: layout {version} remains")
            self._discard()
        raise AssertionError("unreachable")

    def _stored_sources(self, conn):
        """What the index last recorded for each source; empty for a new index."""
        return {
            row[0]: {"path": row[0], "run_id": row[1], "mtime_ns": row[2],
                     "size_bytes": row[3], "parsed_bytes": row[4],
                     "rows_parsed": row[5], "state": row[6], "detail": row[7],
                     "carry": row[8]}
            for row in conn.execute(
                "SELECT path, run_id, mtime_ns, size_bytes, parsed_bytes,"
                " rows_parsed, state, detail, carry FROM ingested_files")}

    # -- the sync

    def sync(self, rebuild=False):
        """Bring the index up to date with its sources, parsing only what changed.

        Answers `reused` when no artifact changed, `rebuilt` when at least one was read,
        and `busy` when another sync holds the index's writer lock — that sync, or the
        next, reads what is new. None is a judgment: the sources are right and the index
        is only ever their shadow.
        """
        with self._writer() as held:
            if not held:
                return "busy"
            if rebuild or self.rebuild:
                self._discard()
            return self._sync()

    def build(self, watermark=None):
        """A full rebuild of the index from its artifacts: discard it and sync."""
        return self.sync(rebuild=True)

    def _sync(self):
        store_runs = self._store_runs()
        run_dirs = self._run_dirs()
        sources = self._source_map(store_runs, run_dirs)
        wanted = self._watermark(sources)
        conn = self._open()
        try:
            stored = self._stored_sources(conn)
            changed = {
                path for path, (mtime, size) in wanted.items()
                if path not in stored or (stored[path]["mtime_ns"],
                                          stored[path]["size_bytes"]) != (mtime, size)}
            gone = set(stored) - set(wanted)
            derived = self._build(conn, wanted, stored, store_runs, run_dirs, sources,
                                  changed, gone)
        finally:
            conn.close()
        return "rebuilt" if derived or changed or gone else "reused"

    # -- the build

    def _build(self, conn, wanted, stored, store_runs, run_dirs, sources, changed, gone):
        """Bring the index up to date, answering how many runs were derived.

        A run with nothing new is not derived at all: its rows and its claims are already
        what a derivation would produce, so the store columns it owns are refreshed and
        nothing is parsed. Each derived run is its own transaction, so a sync stopped part
        way keeps every run it finished and the next one starts after them.
        """
        derived = 0
        reset_seq(conn)
        existing = {row[0] for row in conn.execute("SELECT run_id FROM runs_index")}
        run_ids = set(store_runs) | set(run_dirs)
        # A changed file marks its run; a removed one marks the run it belonged to; a run
        # the index has never seen is derived whatever its files say.
        dirty = {sources[path] for path in changed if path in sources}
        dirty |= {stored[path].get("run_id") for path in gone
                  if path in stored and stored[path].get("run_id")}
        dirty |= {run_id for run_id in run_ids if run_id not in existing}
        if self._store_readable():
            # A run the store no longer holds and no directory remains for is gone from
            # the sources, so it goes from the index too: a lingering row is a claim
            # about a run that nothing on disk supports.
            for run_id in existing - run_ids:
                for table in ("obs_events", "runs_index", "claim_checks"):
                    conn.execute(f"DELETE FROM {table} WHERE run_id = ?", (run_id,))
        self._prune_sources(conn, gone, stored)
        self._tick_decisions(conn)
        self._sync_unreadable_store(conn)
        save_seq(conn)
        conn.commit()
        for run_id in sorted(run_ids):
            store_row = store_runs.get(run_id)
            if run_id in dirty:
                meta, summary, updates = self._derive(
                    conn, run_id, run_dirs.get(run_id), store_row, wanted, stored)
                self._upsert_run(conn, meta)
                self._replace_claims(conn, meta, summary)
                self._record_sources(conn, run_id, updates)
                save_seq(conn)
                conn.commit()
                derived += 1
            else:
                self._update_store_columns(conn, run_id, store_row)
        conn.commit()
        return derived

    def _store_readable(self):
        """Whether the store can be read at all, so its rows may be pruned.

        A store that is absent or unreadable is not evidence that its runs are gone.
        Pruning on that would delete a whole index over a transient lock.
        """
        store = Store(self.store_path)
        try:
            return store.state == "ok" and "runs" in store.tables
        finally:
            store.close()

    def _derive(self, conn, run_id, run_dirs, store_row, wanted, stored):
        """Derive one run: its runs_index row, its new obs rows, its claims.

        Answers `(meta, summary, source_updates)`. Only the files that changed are
        parsed; the rest keep the rows and the recorded state they already have.
        """
        dict_row = store_row or {}
        trace = {"run_id": run_id, "lane_id": dict_row.get("lane_id"),
                 "item_id": dict_row.get("item_id")}
        dirs = [Path(directory) for directory in (run_dirs or [])]
        sources = self._run_sources(run_id, dirs, store_row)
        manifest = _read_json(sources.get("manifest"))
        if manifest.get("tick_id"):
            trace["tick_id"] = manifest["tick_id"]

        # These rows are derived from the state of the artifacts rather than from their
        # contents, so they are dropped and re-derived whenever the run is.
        conn.execute("DELETE FROM obs_events WHERE run_id = ? AND kind = ?",
                     (run_id, UNREADABLE))

        updates = {}
        log = self._parse_file(conn, trace, sources.get("log"), wanted, stored,
                               parse_run_log, updates)
        proc = self._parse_file(conn, trace, sources.get("proc"), wanted, stored,
                                parse_proc_exec, updates)
        git = self._parse_file(conn, trace, sources.get("git"), wanted, stored,
                               parse_git_trace2, updates)
        for name in ("gate_plan", "gate_verdict"):
            self._parse_file(conn, trace, sources.get(name), wanted, stored,
                             parse_gate_record, updates, force_full=True)
        # The small artifacts this run reads whole are watermarked like the rest: a
        # manifest that gained its head sha, or a brief that appeared, has to mark the
        # run changed. Leaving them out of `ingested_files` would mark it changed on
        # EVERY sync, which is the rebuild this exists to remove.
        for name in ("manifest", "base_sha", "brief", "fence"):
            self._record_metadata(sources.get(name), wanted, updates)

        if log.state in ("missing", "unreadable", "corrupt"):
            self._unreadable(conn, run_id, trace, "run-log", sources.get("log"),
                             f"{log.state}: {log.detail}")
        if proc.state in ("unreadable", "corrupt"):
            self._unreadable(conn, run_id, trace, "proc-exec.jsonl",
                             sources.get("proc"), proc.state)
        if git.state in ("unreadable", "corrupt"):
            self._unreadable(conn, run_id, trace, "git-trace2.jsonl",
                             sources.get("git"), git.state)

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
            str(sources["git"]) if sources.get("git") else None, git.state,
            _existing(dirs, "brief.md"), _existing(dirs, "fence.txt"),
            base_sha, manifest.get("head_sha"), manifest.get("tree_sha"),
            "; ".join(detail_bits),
        )
        return meta, log.summary, updates

    def _source_plan(self, path, wanted, stored, force_full=False):
        """How to read one source: `skip`, `resume` from an offset, or `full`."""
        if path is None:
            return "skip", 0, None
        row = stored.get(str(path))
        stat = wanted.get(str(path))
        if stat is None:
            # Nothing to read now. A source that was read before and is gone is parsed
            # again so it reads `missing` instead of keeping a state it no longer has.
            return ("skip", 0, None) if row is None else ("full", 0, row)
        if row is None:
            return "full", 0, row
        if (row["mtime_ns"], row["size_bytes"]) == stat:
            return "skip", row["parsed_bytes"], row
        if force_full:
            # A record rewritten whole is re-read whole, but only when it changed: an
            # unchanged one is skipped like any other source.
            return "full", 0, row
        if stat[1] > row["parsed_bytes"]:
            return "resume", row["parsed_bytes"], row
        return "full", 0, row

    def _parse_file(self, conn, trace, path, wanted, stored, parser, updates,
                    force_full=False):
        mode, offset, row = self._source_plan(path, wanted, stored, force_full)
        if mode == "skip":
            if row is None:
                return Parsed(Rows("skip"), "missing", "", 0)
            return Parsed(Rows("skip"), row["state"], row["detail"],
                          row["parsed_bytes"], _loads(row.get("carry")),
                          _loads(row.get("carry")).get("summary") or {})
        if mode == "full":
            _delete_source_rows(conn, trace["run_id"], path)
            ordinal, carry = 0, {}
        else:
            ordinal, carry = row["rows_parsed"], _loads(row.get("carry"))
        parsed = parser(path, trace, start=offset, ordinal=ordinal, carry=carry)
        self._insert_rows(conn, parsed.rows)
        # The stat is read AFTER the parse: a run still writing appends while the build
        # reads, so the size recorded has to be the one the build actually consumed from,
        # not the one it saw before it started.
        try:
            after = Path(path).stat()
        except OSError:
            after = None
        stat = (after.st_mtime_ns, after.st_size) if after else wanted.get(str(path))
        updates[str(path)] = {
            "mtime_ns": stat[0] if stat else (row or {}).get("mtime_ns", 0),
            "size_bytes": stat[1] if stat else (row or {}).get("size_bytes", 0),
            # A resumed parse that read nothing new keeps the offset it started at, so
            # the next build starts from the same place instead of from zero.
            "parsed_bytes": parsed.consumed,
            "rows_parsed": parsed.rows.ordinal,
            "state": parsed.state,
            "detail": parsed.detail,
            "carry": json.dumps(parsed.carry, sort_keys=True),
        }
        return parsed

    def _record_metadata(self, path, wanted, updates):
        """Record a small artifact this run reads whole, so it is watermarked too."""
        stat = wanted.get(str(path)) if path is not None else None
        if stat is None:
            return
        updates[str(path)] = {
            "mtime_ns": stat[0], "size_bytes": stat[1], "parsed_bytes": stat[1],
            "rows_parsed": 0, "state": "ok", "detail": "", "carry": "{}",
        }

    def _insert_rows(self, conn, rows):
        if not rows.rows:
            return
        conn.executemany(
            "INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start, ts_end,"
            " duration_ms, seam, run_id, lane_id, item_id, tick_id, session_id,"
            " route_id, span_id, summary, payload, source)"
            " VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
            [(row["obs_id"], row["seq"], row["kind"], row["ts_start"], row["ts_end"],
              row["duration_ms"], row["seam"], row["run_id"], row["lane_id"],
              row["item_id"], row["tick_id"], row["session_id"], row["route_id"],
              row["span_id"], row["summary"], row["payload"], row["source"])
             for row in rows.rows])

    def _unreadable(self, conn, run_id, trace, artifact, path, reason):
        rows = Rows("unreadable", source=str(path or ""))
        rows.add_unreadable(trace, artifact, path or "", reason)
        self._insert_rows(conn, rows)

    def _upsert_run(self, conn, meta):
        conn.execute(RUN_INSERT, meta)

    def _update_store_columns(self, conn, run_id, store_row):
        """Refresh what the store owns for a run whose artifacts did not change."""
        row = store_row or {}
        conn.execute(
            "UPDATE runs_index SET lane_id=?, item_id=?, backend=?, store_state=?,"
            " exit_code=?, started_at=?, finished_at=? WHERE run_id=?",
            (row.get("lane_id"), row.get("item_id"), row.get("backend"),
             row.get("state") or "unrecorded", row.get("exit_code"),
             row.get("started_at"), row.get("finished_at"), run_id))

    def _replace_claims(self, conn, meta, summary):
        run = dict(zip(RUN_COLUMNS, meta))
        conn.execute("DELETE FROM claim_checks WHERE run_id = ?", (run["run_id"],))
        for claim in verify_run(conn, run, summary or {}):
            conn.execute(
                "INSERT OR REPLACE INTO claim_checks (claim_id, run_id, lane_id,"
                " item_id, claim_kind, claim_text, observed_status,"
                " discrepancy_reason, evidence_span_ids, last_dir_edit_at,"
                " observed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)", claim)

    def _record_sources(self, conn, run_id, updates):
        for path, rec in updates.items():
            conn.execute(
                "INSERT INTO ingested_files (path, run_id, mtime_ns, size_bytes,"
                " parsed_bytes, rows_parsed, state, detail, carry)"
                " VALUES (?,?,?,?,?,?,?,?,?)"
                " ON CONFLICT(path) DO UPDATE SET run_id=excluded.run_id,"
                " mtime_ns=excluded.mtime_ns, size_bytes=excluded.size_bytes,"
                " parsed_bytes=excluded.parsed_bytes, rows_parsed=excluded.rows_parsed,"
                " state=excluded.state, detail=excluded.detail, carry=excluded.carry",
                (path, run_id, rec["mtime_ns"], rec["size_bytes"], rec["parsed_bytes"],
                 rec["rows_parsed"], rec["state"], rec["detail"], rec["carry"]))

    def _prune_sources(self, conn, gone, stored):
        for path in gone:
            run_id = (stored.get(path) or {}).get("run_id")
            if run_id:
                _delete_source_rows(conn, run_id, path)
            conn.execute("DELETE FROM ingested_files WHERE path = ?", (path,))

    def _tick_decisions(self, conn):
        """`tick.decision` rows from the store's `dispatches` (§4.3).

        The store is authoritative for the decision, so the row is stamped with the
        decision's own time and the run it names, not with an index timestamp. The
        table is small and read on every sync, so a decision the store no longer holds
        is dropped rather than left to read as a decision that still stands.
        """
        store = Store(self.store_path)
        held = set()
        indexed = {obs_id for (obs_id,) in conn.execute(
            "SELECT obs_id FROM obs_events WHERE kind = 'tick.decision'")}
        try:
            if store.state != "ok" or "dispatches" not in store.tables:
                # The store is the authority for these rows. With none to read there is
                # nothing to say, and dropping the rows already indexed would be deleting
                # evidence over what may be a transient lock.
                return
            for row in store.rows(
                    "SELECT decision_id, item_id, lane_id, run_id, action, reason,"
                    " dispatched_at FROM dispatches"):
                obs_id = f"obs-dispatch-{row['decision_id']}"
                held.add(obs_id)
                if obs_id in indexed:
                    # A dispatch row is written once and never changed, so one already
                    # indexed is not written again: rewriting every decision on every
                    # sync made a sync's cost follow the store's whole history.
                    continue
                payload = {"outcome": row["action"], "reason": row["reason"]}
                conn.execute(
                    "INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start,"
                    " run_id, lane_id, item_id, span_id, summary, payload, source)"
                    " VALUES (?,?,?,?,?,?,?,?,?,?,?)",
                    (obs_id, -1, "tick.decision", _iso_from_ms(row["dispatched_at"]),
                     row["run_id"], row["lane_id"], row["item_id"], row["decision_id"],
                     f"{row['action']} {row['reason']}".strip()[:EXCERPT_MAX],
                     json.dumps(payload), str(self.store_path)))
        finally:
            store.close()
        for obs_id in indexed - held:
            conn.execute("DELETE FROM obs_events WHERE obs_id = ?", (obs_id,))

    def _sync_unreadable_store(self, conn):
        obs_id = f"obs-store-{UNREADABLE}"
        if self.store_path.exists():
            conn.execute("DELETE FROM obs_events WHERE obs_id = ?", (obs_id,))
            return
        conn.execute(
            "INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start, run_id,"
            " span_id, summary, payload, source) VALUES (?,?,?,?,?,?,?,?,?)",
            (obs_id, 0, UNREADABLE, now_iso(), "unattributed", "unreadable-store",
             f"store: unreadable (no store at {self.store_path})",
             json.dumps({"artifact": "state.db", "path": str(self.store_path),
                         "reason": "absent"}), str(self.store_path)))


def _delete_source_rows(conn, run_id, path):
    """Delete the rows one run derived from one source, by their id range.

    A row's id is `obs-<run>-<source stamp>-<ordinal>`, so one source's rows are one
    contiguous range of the primary key. `source` has no index, and a `DELETE ... WHERE
    source = ?` walked the whole table once per source re-read.
    """
    prefix = f"obs-{run_id}-{_source_slug(path)}-"
    conn.execute("DELETE FROM obs_events WHERE obs_id >= ? AND obs_id < ?",
                 (prefix, prefix[:-1] + "."))


def _loads(raw):
    try:
        value = json.loads(raw or "{}")
    except (TypeError, ValueError):
        return {}
    return value if isinstance(value, dict) else {}


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


def _claim_row(run, kind, text, status, reason, evidence, last_edit):
    return (
        f"claim-{run['run_id']}-{kind}-{hashlib.sha256(text.encode()).hexdigest()[:8]}",
        run["run_id"], run.get("lane_id"), run.get("item_id"), kind, text[:EXCERPT_MAX],
        status, reason[:EXCERPT_MAX], json.dumps(list(evidence)), last_edit, now_iso(),
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


def _conn(index_path):
    """The index itself is opened read-only for every query."""
    conn = sqlite3.connect(f"file:{index_path}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def q_tick(conn, tick_id=None, last=False):
    if last and not tick_id:
        row = conn.execute(
            "SELECT tick_id FROM obs_events WHERE tick_id IS NOT NULL AND tick_id != ''"
            " ORDER BY ts_start DESC, seq DESC LIMIT 1").fetchone()
        tick_id = row["tick_id"] if row else None
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
        f"  log {run['log_path']} [{run['log_state']}]  proc [{run['proc_state']}]"
        f"  git [{run['git_state']}]",
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
        return "cost: no llm.turn row — no run log was parsed (see `audit <run-id>`)"
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
        return f"discrepancies: none (no {what} in the index)"
    lines = [f"discrepancies: {len(rows)} row(s)"]
    for row in rows:
        lines.append(f"  {row['observed_at'][:19]} {row['observed_status']:<10}"
                     f" {row['run_id']} {row['claim_kind']:<14}"
                     f" {row['claim_text']}")
        if row["discrepancy_reason"]:
            lines.append(f"      {row['discrepancy_reason']}")
    return "\n".join(lines)


# ---------------------------------------------------------------- main


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="tillr-trace",
        description="Derived read model over recorded runs. Never a source of truth.")
    parser.add_argument("--trace-root", type=Path, default=trace_root_default())
    parser.add_argument("--store", type=Path, default=Path(".tillr/state.db"))
    parser.add_argument("--index", type=Path, default=None)
    parser.add_argument("--rebuild", action="store_true",
                       help="drop and rebuild index.db from the Tier 1 artifacts")
    sub = parser.add_subparsers(dest="command", required=True)

    # `ingest` is the write the loop asks for after a tick: it brings index.db up to
    # date with the artifacts the tick's runs left and prints nothing else. Every other face syncs
    # too, because a query that read a stale index would answer the wrong question, but
    # a supervisor has no query to make -- it only needs the derived rows to exist.
    sub.add_parser("ingest", help="index the recorded artifacts, and print where")

    tick = sub.add_parser("tick", help="one tick, in order")
    tick.add_argument("tick_id", nargs="?")
    tick.add_argument("--last", action="store_true")

    audit = sub.add_parser("audit", help="one run: asked, ran, changed, cost")
    audit.add_argument("run_id")

    sub.add_parser("cost", help="per lane, per model, per route")

    disc = sub.add_parser("discrepancies", help="claims the record does not support")
    disc.add_argument("--since")
    disc.add_argument("--all", action="store_true", help="include unverified rows")

    args = parser.parse_args(argv)
    index = Index(args.trace_root, args.store, args.index, args.rebuild)
    state = index.sync(rebuild=args.rebuild)
    if args.command == "ingest":
        if state == "busy":
            print(f"index: busy {index.index_path} (another ingest holds"
                  f" {index._lock_path()}; it, or the next one, reads what is new)")
        else:
            print(f"index: {state} {index.index_path}")
        return 0
    if state == "busy" and not index.index_path.exists():
        print(f"tillr-trace: no index at {index.index_path} yet, and another ingest is"
              " building it", file=sys.stderr)
        return 1
    conn = _conn(index.index_path)
    try:
        if args.command == "tick":
            print(render_tick(q_tick(conn, args.tick_id, args.last)))
        elif args.command == "audit":
            print(render_audit(q_audit(conn, args.run_id)))
        elif args.command == "cost":
            print(render_cost(q_cost(conn)))
        elif args.command == "discrepancies":
            print(render_discrepancies(q_discrepancies(conn, args.since, args.all),
                                       args.all))
    finally:
        conn.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
