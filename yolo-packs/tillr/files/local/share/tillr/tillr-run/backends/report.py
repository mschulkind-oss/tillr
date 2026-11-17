"""The one report.md shape every backend's extractor produces.

Design §4 of docs/design/tillr-muse-backend.md fixes the report's header and the
`RESULT:` completeness rule; §9 gives each adapter its own extractor because each
harness's stream dialect is adapter-private. So the DIALECT is per adapter and the
SHAPE is here, once: three copies of this header would be three shapes within a month.

The window header carries Claude's last rate_limit_event: rate limit kind is the
window's rateLimitType, fraction is utilization, status is the provider's status,
and reset is resetsAt (Unix seconds). Missing facts are unknown. Other dialects
supply no window, so they keep the same keys without claiming quota knowledge.

An adapter's `extract-report` is a thin file that defines `dialect(stream_text)` and
calls `run(dialect)`. The dialect answers the text and whatever provenance its stream
carries; this module writes the header, decides completeness, and owns the exit codes:

    0  the report is complete and its footer says `RESULT: done`
    1  the report is complete and its footer says `RESULT: failed`
    3  the report is complete and its footer says `RESULT: blocked`
    4  the report is INCOMPLETE: the stream carries no `RESULT:` footer
    5  the report is complete and its footer says `RESULT: trunk-broken`
    2  usage or I/O error: nothing was extracted

4 and 1 are deliberately different answers. A run that exited 0 with no footer wrote
nothing a driver may believe, and both `codex exec` and `pi -p` have exited 0 on a task
they correctly reported as impossible — which is why this repository already holds that
a lane's verdict comes from its result, never from `$?`
(docs/research/runner-bake-off-2026-10-27.md).

5 and 1 are also deliberately different. `trunk-broken` is a SUSPECT, not a conviction:
the run is saying the trunk itself is red, so the failure may be the base's rather than
the candidate's. A driver that read it as `failed` would freeze a verdict against work
nobody proved wrong. The flow settles it by replaying the failing checks on the base — a
failure that is red there is `trunk-already-red` and the item is acquitted; a failure
that is green there stays the candidate's problem. Until that replay runs, the run's
answer is a claim, and the exit code is the one thing that keeps it from being read as
the ordinary failure it may not be.

The report is written in every case, incomplete included: the header and the text that
did arrive are the evidence that a run happened at all.

USAGE, and the three states a cost has. `tokens` and `cost usd` are header keys because
the loop's spend ceiling is read from this file and from nowhere else: `run exec` runs
this extractor after every run and copies the two keys onto the run record, so a report
that did not carry them would leave the ceiling with nothing to compare.

  * `tokens` is the sum of every token counter the harness reported (input, output,
    cache reads and reasoning all count: a cache read is billed), or `unknown` when the
    stream carries no usage at all.
  * `cost usd` has THREE states, not two, because cost is not universal: a positive
    number, `0.000000` for a backend that reported a zero, and `unknown` for a backend
    that does not report cost at all (muse and agy). `unknown` is never a zero: a total
    that silently adds a cost-less backend's nil as 0 would read as a cheap run and is
    the shape the loop's dollar ceiling refuses to compare against.
"""

import argparse
import json
import re
import sys

EXIT_DONE = 0
EXIT_FAILED = 1
EXIT_USAGE = 2
EXIT_BLOCKED = 3
EXIT_INCOMPLETE = 4
EXIT_TRUNK_BROKEN = 5

# The footer is the brief's, not any harness's: the template asks for it in plain text so
# a harness with no schema flag carries it just as well as one with a schema.
#
# `trunk-broken` is a fourth result and not a shade of `failed`: it is the run's claim that
# the base it was given is red, which the driver must replay before it convicts the
# candidate. It is in the footer's vocabulary so a backend can say it in plain text; the
# driver, not the regex, decides what it means (`RESULT: trunk-broken` never retires a
# criterion and never writes a failing verdict by itself).
FOOTER = re.compile(r"^RESULT: (done|blocked|failed|trunk-broken)\s*$")
FOOTER_RESULT_EXIT = {
    "done": EXIT_DONE,
    "failed": EXIT_FAILED,
    "blocked": EXIT_BLOCKED,
    "trunk-broken": EXIT_TRUNK_BROKEN,
}

# The header keys, in order. `tests/tillr-muse-backend.test.py` asserts every
# dialect produces exactly this set, which is how "one report shape" is checked rather
# than asserted.
#
# `route` is the route staging asked for, not one read off the stream: a harness reports
# the model it actually called, and a routed model id (a `:<route>` suffix over a shared
# wire model) loses that suffix on the wire, so the requested route is a staging fact and
# every other header field would attribute the run to the wire id.
HEADER_KEYS = (
    "backend",
    "model",
    "route",
    "effort",
    "session",
    "exit code",
    "started",
    "finished",
    "stream lines",
    "events",
    "unparseable lines",
    "tokens",
    "cost usd",
    "rate limit kind",
    "fraction",
    "status",
    "reset",
    "complete",
    "result",
)

UNKNOWN = "unknown"


def tokens_text(tokens):
    """A reported token total, or `unknown` when the stream carried no usage."""
    return UNKNOWN if tokens is None else str(int(tokens))


def cost_text(cost):
    """A reported cost in dollars, or `unknown` when the backend does not report one.

    The three states are deliberate and readable: `0.000000` is a backend that reported a
    zero, `unknown` is a backend with no cost to report. A caller that summed them as the
    same thing would turn "nobody can tell me" into "it was free".
    """
    return UNKNOWN if cost is None else f"{float(cost):.6f}"


def footer_result(text):
    """The footer's result, or None when the text does not end with one."""
    for line in reversed(text.splitlines()):
        if line.strip():
            match = FOOTER.match(line.strip())
            return match.group(1) if match else None
    return None


def render(
    *,
    backend,
    model,
    route,
    effort,
    session,
    exit_code,
    started,
    finished,
    stream_lines,
    events,
    unparseable,
    tokens,
    cost,
    text,
    window=None,
):
    """The report text and its result. Pure: the same inputs always give the same file."""
    result = footer_result(text)
    window = {key: value for key, value in (window or {}).items() if value is not None}
    header = {
        "backend": backend or UNKNOWN,
        "model": model or UNKNOWN,
        "route": route or UNKNOWN,
        "effort": effort or UNKNOWN,
        "session": session or UNKNOWN,
        "exit code": exit_code,
        "started": started or UNKNOWN,
        "finished": finished or UNKNOWN,
        "stream lines": str(stream_lines),
        "events": str(events),
        "unparseable lines": str(unparseable),
        "tokens": tokens_text(tokens),
        "cost usd": cost_text(cost),
        "rate limit kind": (window or {}).get("rateLimitType") or UNKNOWN,
        "fraction": (window or {}).get("utilization", UNKNOWN),
        "status": (window or {}).get("status") or UNKNOWN,
        "reset": (window or {}).get("resetsAt") or UNKNOWN,
        "complete": "yes" if result else "no",
        "result": result or "missing",
    }
    lines = ["# run report", ""]
    lines.extend(f"{key}: {header[key]}" for key in HEADER_KEYS)
    lines.extend(["", "---", ""])
    body = text if text.endswith("\n") or not text else text + "\n"
    return "\n".join(lines) + body, result


def _parse_args(argv, prog):
    parser = argparse.ArgumentParser(
        prog=prog,
        description="Render one harness stream into the fixed report.md shape.",
    )
    parser.add_argument("--backend", default=None, help="the adapter name, from staging")
    parser.add_argument("--model", default=None, help="the model id staging supplied")
    parser.add_argument(
        "--route",
        default=None,
        help="the route staging asked for, e.g. the `:<route>` suffix of a virtual model id",
    )
    parser.add_argument("--effort", default=None, help="the effort staging supplied")
    parser.add_argument(
        "--session-id", default=None, help="overrides the session id the stream carries"
    )
    parser.add_argument(
        "--exit-code", default=UNKNOWN, help="the harness's exit code, as staging saw it"
    )
    parser.add_argument("--out", default=None, help="report path; default stdout")
    parser.add_argument(
        "stream", nargs="?", default="-", help="stream path; default stdin"
    )
    return parser.parse_args(argv)


def run(dialect, argv=None):
    """Parse one stream with `dialect`, write the report, and answer the exit code.

    `dialect(stream_text)` answers a dict with `text` (required) and any of `session`,
    `model`, `started`, `finished`, `events`, `unparseable`, `tokens`, `cost`. The route
    is not among them: it is what staging asked for, so it arrives as `--route` or not at
    all. `tokens` and `cost` stay out of the dict when a dialect cannot read them, and
    `render` then writes them as `unknown`.
    """
    prog = sys.argv[0].rsplit("/", 1)[-1] if argv is None else "extract-report"
    args = _parse_args(sys.argv[1:] if argv is None else argv, prog)

    try:
        if args.stream == "-":
            stream_text = sys.stdin.read()
        else:
            with open(args.stream, encoding="utf-8", errors="replace") as handle:
                stream_text = handle.read()
    except OSError as error:
        print(f"{prog}: reading the stream: {error}", file=sys.stderr)
        return EXIT_USAGE

    found = dialect(stream_text)
    text = found.get("text") or ""
    report, result = render(
        backend=args.backend,
        model=args.model or found.get("model"),
        route=args.route,
        effort=args.effort,
        session=args.session_id or found.get("session"),
        exit_code=args.exit_code,
        started=found.get("started"),
        finished=found.get("finished"),
        stream_lines=found.get("lines", len(stream_text.splitlines())),
        events=found.get("events", 0),
        unparseable=found.get("unparseable", 0),
        tokens=found.get("tokens"),
        cost=found.get("cost"),
        text=text,
        window=found.get("window"),
    )

    try:
        if args.out:
            with open(args.out, "w", encoding="utf-8") as handle:
                handle.write(report)
            # Keep the fixed Markdown header compatible with all report consumers.
            # The companion contains measurements only, never pricing.
            with open(args.out + ".usage.json", "w", encoding="utf-8") as handle:
                json.dump(found.get("usage_details"), handle)
        else:
            sys.stdout.write(report)
    except OSError as error:
        print(f"{prog}: writing the report: {error}", file=sys.stderr)
        return EXIT_USAGE

    if result is None:
        where = args.out or "stdout"
        print(
            f"{prog}: INCOMPLETE: the stream carries no 'RESULT: done|blocked|failed' "
            f"footer, so the report at {where} is not a verdict. An empty or truncated "
            f"stream is the usual cause; the remedy is a smaller brief, not more effort.",
            file=sys.stderr,
        )
        return EXIT_INCOMPLETE
    return FOOTER_RESULT_EXIT[result]


def records(stream_text):
    """The stream's JSON lines, with the count of lines that were not JSON.

    A non-JSON line is tolerated and counted, never fatal: an adapter that fails early
    can write prose to the log before it separates anything, and a count the report
    carries is how a driver sees that it happened.
    """
    parsed, unparseable = [], 0
    for line in stream_text.splitlines():
        if not line.strip():
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError:
            unparseable += 1
            continue
        if isinstance(value, dict):
            parsed.append(value)
        else:
            unparseable += 1
    return parsed, unparseable


def micros_to_rfc3339(value):
    """A stream's `recorded_at` (unix microseconds) as RFC 3339, or None."""
    if not isinstance(value, (int, float)):
        return None
    import datetime

    try:
        moment = datetime.datetime.fromtimestamp(value / 1_000_000, datetime.timezone.utc)
    except (OverflowError, OSError, ValueError):
        return None
    return moment.strftime("%Y-%m-%dT%H:%M:%SZ")
