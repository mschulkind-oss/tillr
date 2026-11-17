#!/usr/bin/env python3
"""The MM-1A acceptance test: four adapters, four dialects, one report shape.

What it pins, and why each case is here rather than in a live run:

  * every extractor over both of its own fixtures produces ONE report shape — the same
    header keys, the same text — so "the shape is proved, not one harness's habits
    wearing a general name" is checked rather than asserted;
  * the NEGATIVE CONTROL: each truncated fixture (valid dialect, no `RESULT:` footer)
    exits 4 and its report says incomplete, while the body it DID extract is byte for
    byte the complete fixture's body minus the footer. That last comparison is what
    makes the refusal the footer check: remove the check and this case goes green on a
    report that says nothing, which is the defect the footer exists to catch;
  * a footer that says `failed` exits 1, not 4 — "no report" and "the report says it
    failed" are different answers to the driver;
  * the backend policy names a default backend, every adapter it names exists and is
    executable, the agy entry carries no effort of its own, and NO MODEL ID FROM THE
    POLICY APPEARS ANYWHERE IN THE TILLR TREE (design D-MM5: that tree ships publicly
    and names no model vendor);
  * each adapter's preflight refuses a missing brief, an unset model, a backend field
    naming another adapter, a brief missing a template section, and an unfilled
    placeholder — and refuses BEFORE the harness is started, which the stub proves by
    never being called;
  * the muse adapter's sandbox posture, read from the argv it actually execs: no
    `--disable-sandbox`, no `--yolo`, `--workspace` rooted at the named target;
  * the harnesses' argv/env dialects differ where the design says they must: the
    model resolves before the run, Pi gets the brief through `BOARD_DRAIN_BRIEF` and its
    own `--thinking`, Agy gets it on stdin with `YOLO_NO_LAUNCH_FLAGS=1` and no effort
    flag, and each keeps stderr out of the stream;
  * the claude adapter (docs/design/tillr-claude-backend.md §2) gets the brief on stdin,
    always passes `--strict-mcp-config` and `--dangerously-skip-permissions`, exports the
    lane markers, and passes everything after a `--` to the harness unchanged — while the
    adapters that take no harness arguments refuse that `--` before anything runs.

No model tokens are spent: every harness is a stub on PATH that records its argv and
replays a fixture stream.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
BACKENDS = ROOT / "tools" / "tillr-run" / "backends"
POLICY = ROOT / "config" / "tillr" / "backend-policy.json"
BEDROCK_POLICY = ROOT / "config" / "tillr" / "backend-policy.bedrock.json"
CLAUDE_POLICY = ROOT / "config" / "tillr" / "backend-policy.claude.json"
TEMPLATE = BACKENDS / "brief-template.md"

# backend name -> (harness command the adapter spawns, its complete fixture)
DIALECTS = {
    "muse": ("muse", "complete.jsonl"),
    "pi": ("pi", "complete.jsonl"),
    "agy": ("agy", "complete.json"),
    "claude": ("claude", "complete.jsonl"),
}
HARNESS = {name: harness for name, (harness, _) in DIALECTS.items()}

# What each fixture's stream reports it spent: the total of every token counter, and the
# cost where the harness reports one. `unknown` is a state, not a zero — muse and agy
# report no cost at all, and a zero there would read as a free run.
#
# claude's total is its `result` line's usage, 4 + 17547 + 40945 + 335, and its cost is
# that line's `total_cost_usd`.
FIXTURE_SPEND = {
    "muse": ("15", "unknown"),
    "pi": ("1500", "0.030000"),
    "agy": ("21212", "unknown"),
    "claude": (str(4 + 17547 + 40945 + 335), "0.004100"),
}

# What the cut-off claude stream spent, with no `result` line to read: the two messages'
# usage, each counted once although `msg_fixture01` arrives on two lines that repeat it.
# Counting per line would give 2 * 37669 + 21162.
CLAUDE_TRUNCATED_TOKENS = str((2 + 17547 + 20000 + 120) + (2 + 0 + 20945 + 215))

# The one body every dialect must reproduce, and the same body as a truncated run leaves
# it: a report that stops before its footer.
BODY = "paths changed: src/example.go\nverification: go test ./... -> ok\n\nRESULT: done\n"
BODY_WITHOUT_FOOTER = "paths changed: src/example.go\nverification: go test ./... -> ok\n"

# The report header design §4 fixes, stated here independently of the implementation so a
# key dropped from it fails this test instead of agreeing with itself. `tokens` and
# `cost usd` are the run's spend, which `run exec` copies onto the run record and the
# drain loop's ceiling reads: a report that stopped carrying them would leave the ceiling
# with nothing to compare and every run reading as unknown.
HEADER_KEYS = [
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
]

STUB = """#!/bin/sh
{
  printf 'call\\n'
  printf 'cwd=%s\\n' "$(pwd -P)"
  printf 'env:BOARD_DRAIN_BRIEF=%s\\n' "${BOARD_DRAIN_BRIEF-}"
  printf 'env:BOARD_DRAIN_RUN=%s\\n' "${BOARD_DRAIN_RUN-}"
  printf 'env:BOARD_DRAIN_SESSION=%s\\n' "${BOARD_DRAIN_SESSION-}"
  printf 'env:YOLO_NO_LAUNCH_FLAGS=%s\\n' "${YOLO_NO_LAUNCH_FLAGS-}"
  # The wall is not in the harness's own argv: `timeout` is its parent, so the bound is
  # read from the parent's cmdline. That is the only place a wrapped run shows it.
  printf 'parent=%s\\n' "$(tr '\\0' ' ' < "/proc/$PPID/cmdline" 2>/dev/null)"
  # One line per argument: the prompt travels in argv and is multi-line, so a newline
  # inside an argument would otherwise be read back as a new argument.
  for a in "$@"; do printf 'arg=%s\\n' "$(printf '%s' "$a" | tr '\\n' '\\001')"; done
  first=$(head -n 1 2>/dev/null)
  printf 'stdin=%s\\n' "$first"
} >> "$STUB_LOG"
if [ "${1-}" = model-profile ]; then exit 0; fi
printf 'stub stderr\\n' >&2
cat "$STUB_STREAM"
exit "${STUB_EXIT-0}"
"""


def check(condition, message):
    if not condition:
        print(f"FAIL: {message}", file=sys.stderr)
        raise SystemExit(1)


def split_report(text):
    """The header as a dict, the body as it stands after the `---` rule."""
    head, _, body = text.partition("\n---\n")
    header = {}
    for line in head.splitlines():
        if ": " in line:
            key, value = line.split(": ", 1)
            header[key] = value
    return header, body


def extract(backend, stream, *args):
    return subprocess.run(
        [str(BACKENDS / backend / "extract-report"), *args, str(stream)],
        capture_output=True,
        text=True,
        check=False,
    )


def filled_brief(lane):
    """The template with every placeholder filled, which must also pass preflight."""
    values = {
        "ITEM_ID": "7",
        "TITLE": "a staged brief for the acceptance test",
        "RUN_ID": "run_7",
        "BACKEND": "muse",
        "BASE_SHA": "0" * 40,
        "TARGET": str(lane / "worktree"),
        "TARGET_KIND": "lane worktree",
        "DATE": "2026-11-12",
        "TIMEOUT_SECONDS": "1800",
        "DRIVER_REASON": "unused in the worktree form",
        "MAIN_STATUS_BEFORE": "unused in the worktree form",
        "TASK": "Add the one line the item asks for.",
        "CRITERION_1": "the acceptance test passes (not command-verifiable in a fixture)",
        "FENCE_PATHS": "- tools/tillr-run/backends/**",
        "OUT_OF_FENCE_PATHS": "- everything else",
        "SETTLED": "- the report shape is fixed by the extractor, not by the harness.",
        "FACTS": "- backends/ holds one directory per harness.",
        "METHOD": "Write the failing case first.",
        "VERIFICATION": "```\npython3 tests/tillr-muse-backend.test.py\n```",
    }
    text = TEMPLATE.read_text()
    for name, value in values.items():
        text = text.replace("{{" + name + "}}", value)
    return text


class Workspace:
    """One temp root: the stub harnesses, their call log, and one lane per case."""

    def __init__(self, root):
        self.root = root
        self.bin = root / "bin"
        self.bin.mkdir()
        for harness in set(HARNESS.values()):
            stub = self.bin / harness
            stub.write_text(STUB)
            stub.chmod(0o755)
        self.log = root / "calls.log"
        self.log.write_text("")
        self.lanes = 0

    def lane(self, brief=None):
        self.lanes += 1
        lane = self.root / f"lane-{self.lanes}"
        (lane / "worktree").mkdir(parents=True)
        (lane / "brief.md").write_text(filled_brief(lane) if brief is None else brief)
        (lane / "fence.txt").write_text("tools/tillr-run/backends\n")
        (lane / "base_sha").write_text("0" * 40 + "\n")
        (lane / "session-id").write_text("44444444-4444-7444-8444-444444444444\n")
        return lane

    def invoke(self, backend, lane, *args, stream=None, exit_code=0, path=None):
        env = os.environ.copy()
        # These cases stage fresh dispatches, even when this suite runs in a resumed
        # worker. The enclosing lane's continuation prompt is not a fixture input.
        env.pop("TILLR_RESUME_PROMPT_FILE", None)
        env.pop("TILLR_SESSION_MODE", None)
        env.update(
            PATH=path if path is not None else f"{self.bin}:{env.get('PATH', '')}",
            STUB_LOG=str(self.log),
            STUB_STREAM=str(stream) if stream else str(self.root / "empty.stream"),
            STUB_EXIT=str(exit_code),
        )
        return subprocess.run(
            [str(BACKENDS / backend / ("run-" + backend)), *args],
            cwd=str(lane / "worktree"),
            env=env,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            check=False,
        )

    def calls(self):
        """The stub's invocations, each as a list of its recorded lines."""
        chunks = [chunk for chunk in self.log.read_text().split("call\n") if chunk.strip()]
        return [chunk.strip().splitlines() for chunk in chunks]


def args_of(call):
    return [
        line.removeprefix("arg=").replace("\001", "\n")
        for line in call
        if line.startswith("arg=")
    ]


def field_of(call, prefix):
    for line in call:
        if line.startswith(prefix):
            return line[len(prefix) :]
    return None


def walled(call):
    """Whether a `timeout` wrapper sat between the adapter and the harness.

    Read from the harness's parent cmdline rather than its argv, because the wall is the
    harness's parent and never one of its own arguments.
    """
    return (field_of(call, "parent=") or "").startswith("timeout ")


def test_extractors(scratch):
    for backend, (_, fixture) in DIALECTS.items():
        complete = BACKENDS / backend / "testdata" / fixture
        truncated = BACKENDS / backend / "testdata" / (
            fixture.replace("complete", "truncated")
        )
        model = f"fixture-model-{backend}"

        done = extract(backend, complete, "--backend", backend, "--model", model)
        check(done.returncode == 0, f"{backend}: complete fixture exited {done.returncode}: {done.stderr}")
        header, body = split_report(done.stdout)
        check(
            list(header) == HEADER_KEYS,
            f"{backend}: header keys are {list(header)}, want {HEADER_KEYS}",
        )
        window_keys = ["rate limit kind", "fraction", "status", "reset"]
        want_window = ["five_hour", "0.5", "allowed", "1794794400"] if backend == "claude" else ["unknown"] * 4
        check([header[key] for key in window_keys] == want_window, f"{backend}: window facts {header}")
        check(header["backend"] == backend, f"{backend}: header backend is {header['backend']}")
        check(header["complete"] == "yes", f"{backend}: complete fixture is not complete")
        check(header["result"] == "done", f"{backend}: result is {header['result']}")
        check(body == BODY, f"{backend}: body is not the shared body: {body!r}")
        check(
            body.count("RESULT: done") == 1,
            f"{backend}: the footer appears {body.count('RESULT: done')} times, so a "
            f"snapshot was appended to a delta",
        )
        check(header["session"] != "unknown", f"{backend}: no session id in the header")
        want_tokens, want_cost = FIXTURE_SPEND[backend]
        check(
            header["tokens"] == want_tokens,
            f"{backend}: tokens are {header['tokens']}, want {want_tokens}; the total is "
            f"every token counter the stream reports, summed once per turn",
        )
        check(
            header["cost usd"] == want_cost,
            f"{backend}: cost is {header['cost usd']}, want {want_cost}: a backend that "
            f"reports no cost must say unknown, never 0",
        )
        check(
            header["model"] == model,
            f"{backend}: model is {header['model']}, want the one staging supplied",
        )
        check(
            header["route"] == "unknown",
            f"{backend}: route is {header['route']} with none supplied, want unknown",
        )

        # The negative control: the same dialect with its footer cut off.
        missing = extract(backend, truncated, "--backend", backend, "--model", model)
        check(
            missing.returncode == 4,
            f"{backend}: a footerless stream exited {missing.returncode}, want 4 "
            f"(incomplete): {missing.stderr}",
        )
        check(
            "INCOMPLETE" in missing.stderr,
            f"{backend}: the refusal does not say INCOMPLETE: {missing.stderr!r}",
        )
        header, body = split_report(missing.stdout)
        check(header["complete"] == "no", f"{backend}: footerless report claims complete")
        check(header["result"] == "missing", f"{backend}: footerless result is {header['result']}")
        check(
            body == BODY_WITHOUT_FOOTER,
            f"{backend}: the footerless report did not extract the text it had: {body!r}",
        )
        check(
            BODY.startswith(body),
            f"{backend}: the footerless body is not the complete body minus its footer, "
            f"so the refusal is not the footer check",
        )
        # A cut-off claude stream has no `result` line, so its spend is read from the
        # assistant lines, once per message id, and its cost is unknown rather than zero.
        check([header[key] for key in window_keys] == want_window, f"{backend}: truncated window facts {header}")
        if backend == "claude":
            no_window = scratch / "claude-no-window.jsonl"
            no_window.write_text("\n".join(line for line in complete.read_text().splitlines() if '"rate_limit_event"' not in line) + "\n")
            absent, _ = split_report(extract(backend, no_window).stdout)
            check([absent[key] for key in window_keys] == ["unknown"] * 4, f"claude: absent window facts {absent}")
            last_window = scratch / "claude-last-window.jsonl"
            last_window.write_text(complete.read_text() + json.dumps({"type": "rate_limit_event", "rate_limit_info": {"rateLimitType": "seven_day", "utilization": 1, "status": "rejected", "resetsAt": 1795000000}}) + "\n")
            last, last_body = split_report(extract(backend, last_window).stdout)
            check([last[key] for key in window_keys] == ["seven_day", "1", "rejected", "1795000000"], f"claude: not the last window {last}")
            check(last_body == BODY, "claude: window changed the model's body")
            last_window.write_text(complete.read_text() + json.dumps({"type": "rate_limit_event", "rate_limit_info": {"rateLimitType": None, "utilization": 0, "status": None, "resetsAt": None}}) + "\n")
            partial, _ = split_report(extract(backend, last_window).stdout)
            check([partial[key] for key in window_keys] == ["unknown", "0", "unknown", "unknown"], f"claude: missing facts or zero fraction {partial}")
            check(
                header["tokens"] == CLAUDE_TRUNCATED_TOKENS,
                f"claude: the cut-off stream's tokens are {header['tokens']}, want "
                f"{CLAUDE_TRUNCATED_TOKENS}: a message's usage is counted once, not once per line",
            )
            check(
                header["cost usd"] == "unknown",
                f"claude: the cut-off stream's cost is {header['cost usd']}, want unknown",
            )
            check(
                header["session"] == "33333333-3333-4333-8333-333333333333",
                f"claude: the init line's session id is not read: {header['session']}",
            )

        # The route staging asked for is a staging fact, not a stream one: a routed id
        # loses its route suffix on the wire, so the header carries it from `--route` and
        # says `unknown` when nobody supplied one rather than guessing from the model.
        routed = extract(
            backend, complete, "--backend", backend, "--route", f"fixture-route-{backend}"
        )
        header, _ = split_report(routed.stdout)
        check(
            header["route"] == f"fixture-route-{backend}",
            f"{backend}: route is {header['route']}, want the one staging supplied",
        )

        # The provenance the stream itself carries, when staging supplied no model: the
        # muse dialect reads it from `run.usage`, so a policy id that is wrong shows
        # up as a disagreement with what actually ran.
        if backend == "muse":
            derived = extract(backend, complete, "--backend", backend)
            header, _ = split_report(derived.stdout)
            check(
                header["model"] == "fixture-model-1",
                f"muse: the model the stream names is not derived: {header['model']}",
            )

        # Pi's `--mode json` opens with the session file's own header, and that is the only
        # place the session id appears; the fixture carries that shape, not a `session_start`
        # event the harness never writes.
        if backend == "pi":
            session_only = extract(backend, complete, "--backend", backend)
            header, _ = split_report(session_only.stdout)
            check(
                header["session"] == "22222222-2222-7222-8222-222222222222",
                f"pi: the session header's id is not read: session is {header['session']}",
            )

        # "The report says it failed" is a different answer from "there is no report".
        # `trunk-broken` is a fourth answer, not a shade of `failed`: it is a SUSPECT the
        # driver must replay, so it gets its own exit code (5) and the header repeats the
        # word rather than normalising it to `failed`.
        for verdict, expected in (("failed", 1), ("blocked", 3), ("trunk-broken", 5)):
            rewritten = scratch / f"{verdict}-{backend}-{fixture}"
            rewritten.write_text(
                complete.read_text().replace("RESULT: done", f"RESULT: {verdict}")
            )
            answer = extract(backend, rewritten, "--backend", backend)
            check(
                answer.returncode == expected,
                f"{backend}: a '{verdict}' footer exited {answer.returncode}, want {expected}",
            )
            header, _ = split_report(answer.stdout)
            check(
                header["result"] == verdict and header["complete"] == "yes",
                f"{backend}: a '{verdict}' footer is reported as {header}",
            )
            rewritten.unlink()


def test_policy():
    policy = json.loads(POLICY.read_text())
    check("about" in policy, "the policy does not say what it is")
    backends = policy["backends"]
    check(
        policy["default_backend"] in backends,
        f"the default backend {policy['default_backend']!r} names no entry",
    )
    # No backend names an adapter outside the dialects, and every dialect's adapter is named
    # by one of the policies (the claude adapter is on the Claude-first policy only). A
    # NAME is not the unit: `pi-designer` runs the pi adapter on another model, which is the
    # role seam (a model swap is a line in this file, never an edit to an item), so the
    # mapping is checked over adapters rather than over names.
    known = {f"tools/tillr-run/backends/{name}/run-{name}" for name in DIALECTS}
    bedrock = json.loads(BEDROCK_POLICY.read_text())
    claude = json.loads(CLAUDE_POLICY.read_text())
    named_anywhere = set()
    for label, document in (("personal", policy), ("bedrock", bedrock), ("claude", claude)):
        named = {entry["adapter"] for entry in document["backends"].values()}
        check(
            named <= known,
            f"the {label} policy names adapters {sorted(named - known)} that are not dialects {sorted(DIALECTS)}",
        )
        named_anywhere |= named
    check(
        named_anywhere == known,
        f"no policy names the adapters {sorted(known - named_anywhere)}",
    )
    for name, entry in backends.items():
        adapter = ROOT / entry["adapter"]
        check(adapter.exists(), f"{name}: the adapter {entry['adapter']} does not exist")
        check(os.access(adapter, os.X_OK), f"{name}: the adapter {entry['adapter']} is not executable")
        check(entry.get("model"), f"{name}: no model id, and an adapter carries none of its own")
        # No backend carries a wall. A total-run bound is a budget, not a fault detector:
        # the retired 1800s default killed runs that had already committed (design D-RW1,
        # docs/design/run-wall-ruling.md). `--timeout N` on the argv is the only wall left.
        check(
            "timeout_seconds" not in entry,
            f"{name}: the policy still carries a timeout_seconds wall",
        )
    # The effort is a flag for two harnesses and part of the id for the third, which is the
    # shape difference the Agy Flash proof exists to find.
    check("effort" in backends["muse"], "muse: no effort")
    check("effort" in backends["pi"], "pi: no thinking level")
    check(
        "effort" not in backends["agy"],
        "agy: an effort key, but its effort lives inside the model id and the "
        "adapter refuses --effort",
    )
    # D-MM11: failover walks one ordered preference whose head is the default, bounded by
    # max_failovers; a backend with an ordered model list starts at its own `model`.
    preference = policy.get("preference", [])
    check(preference, "the policy names no failover preference")
    check(
        preference[0] == policy["default_backend"],
        "the default backend is not the preference head: staging would read the policy two ways",
    )
    check(
        all(name in backends for name in preference),
        f"the preference {preference} names a backend with no entry",
    )
    check(
        policy.get("max_failovers") == 1,
        "max_failovers is not 1: an item could be dispatched past the one bounded failover",
    )
    for name, entry in backends.items():
        if "models" in entry:
            check(
                entry["models"] and entry["models"][0] == entry["model"],
                f"{name}: the model list does not start at its `model`",
            )
    test_claude_policy(claude)

    # D-MM5: the tillr tree ships publicly and names no model vendor, so no id from any
    # policy may appear in it — not as a default, a specimen or a fallback. The Claude
    # policy's short aliases are scanned for too: they live in config/ and nowhere else.
    every = [entry for document in (policy, bedrock, claude) for entry in document["backends"].values()]
    ids = [entry["model"] for entry in every]
    ids += [m for entry in every for m in entry.get("models", [])]
    # Tracked files only: the rule is about what the tree says, and an installed dependency
    # directory in a checkout (tools/tillr-view/web/node_modules) is not the tree. On
    # 2026-11-15 the walk of every file on disk failed the gate in the main checkout on a
    # vendored changelog containing the word "opus", while a clean worktree passed.
    listed = subprocess.run(
        ["git", "ls-files", "-z", "--", "tools/tillr*"],
        cwd=ROOT, check=True, capture_output=True,
    ).stdout.decode("utf-8").split("\0")
    for path in (ROOT / name for name in listed if name):
        if not path.is_file():
            continue
        text = path.read_bytes().decode("utf-8", "replace")
        for model_id in ids:
            check(
                model_id not in text,
                f"{path.relative_to(ROOT)} names the model id {model_id!r}: the "
                f"tillr tree must never name a model vendor (D-MM5)",
            )


def test_claude_policy(policy):
    """The work machine's Claude-first policy (docs/design/tillr-claude-backend.md §4, §6 step 1).

    Every seat starts on the claude adapter and names its own fallback, so a design item
    falls back to the design backend and never to the build one.
    """
    check("about" in policy, "the Claude policy does not say what it is")
    backends = policy["backends"]
    check(policy["default_backend"] == "claude", f"the Claude default is {policy['default_backend']!r}")
    check(policy.get("preference") == ["claude", "pi"], f"the Claude preference is {policy.get('preference')}")
    check(policy.get("max_failovers") == 1, "the Claude policy's max_failovers is not 1")
    check(
        policy.get("seats") == {
            "design": ["claude-designer", "pi-designer"],
            "build": ["claude", "pi"],
            "review": ["claude", "pi"],
            "scout": ["claude", "pi"],
        },
        f"the Claude seats are {policy.get('seats')}",
    )
    for seat, names in policy["seats"].items():
        for name in names if isinstance(names, list) else [names]:
            check(name in backends, f"the Claude {seat} seat names {name!r}, which is not a backend")
    for name in ("claude", "claude-designer"):
        entry = backends[name]
        check(
            entry["adapter"] == "tools/tillr-run/backends/claude/run-claude",
            f"{name}: adapter is {entry['adapter']}",
        )
        check(entry.get("model") and entry.get("effort"), f"{name}: no model or no effort")
    for name, entry in backends.items():
        adapter = ROOT / entry["adapter"]
        check(adapter.exists() and os.access(adapter, os.X_OK), f"{name}: {entry['adapter']} is not an executable")


def test_preflight(workspace):
    for backend in DIALECTS:
        required = ["--backend", backend]
        if backend != "agy":
            required += ["--effort", "medium"]
        cases = {
            "no model": (required, None),
            "another backend": (
                ["--backend", "some-other-adapter", "--model", "fixture-model-x"]
                + (["--effort", "medium"] if backend != "agy" else []),
                None,
            ),
            "no brief": (required + ["--model", "fixture-model-x"], "delete"),
            "no sections": (required + ["--model", "fixture-model-x"], "sectionless"),
            "a placeholder": (required + ["--model", "fixture-model-x"], "placeholder"),
        }
        if backend == "agy":
            cases["an effort flag"] = (
                required + ["--model", "fixture-model-x", "--effort", "high"],
                None,
            )
        if backend == "claude":
            cases["no effort"] = (
                ["--backend", "claude", "--model", "fixture-model-x"],
                None,
            )
            # An unknown flag BEFORE the separator is still the adapter's to refuse; only
            # what follows `--` belongs to the harness.
            cases["an unknown flag before --"] = (
                required + ["--model", "fixture-model-x", "--tools", "Read", "--", "--verbose"],
                None,
            )
        else:
            # A row's harness_args reach the adapter after a `--` (tillr-claude-backend.md
            # §3). An adapter that takes none refuses it as it refuses any argument it does
            # not know, before the harness is started, rather than dropping it silently.
            cases["a harness-argument tail"] = (
                required + ["--model", "fixture-model-x", "--", "--tools", "Read"],
                None,
            )
        for label, (args, brief) in cases.items():
            lane = workspace.lane()
            if brief == "delete":
                (lane / "brief.md").unlink()
            elif brief == "sectionless":
                (lane / "brief.md").write_text(
                    filled_brief(lane).replace("## Fence", "## Not the fence")
                )
            elif brief == "placeholder":
                (lane / "brief.md").write_text(filled_brief(lane) + "\n{{UNFILLED}}\n")
            workspace.log.write_text("")
            result = workspace.invoke(backend, lane, *args)
            check(
                result.returncode == 2,
                f"{backend}: a brief with {label} exited {result.returncode}, want 2: "
                f"{result.stderr}",
            )
            check(
                result.stderr.strip() != "",
                f"{backend}: the refusal for {label} says nothing",
            )
            check(
                workspace.calls() == [],
                f"{backend}: the harness ran despite {label}, so the refusal was not a "
                f"preflight",
            )
            check(
                not (lane / "stream.err").exists(),
                f"{backend}: a refused dispatch left a stream.err",
            )


def test_fixture_does_not_inherit_resume_prompt(workspace):
    lane = workspace.lane()
    prompt = workspace.root / "inherited-resume-prompt"
    prompt.write_text("This belongs to the enclosing resumed worker, not the fixture.\n")
    workspace.log.write_text("")
    with patch.dict(os.environ, {"TILLR_RESUME_PROMPT_FILE": str(prompt),
                                 "TILLR_SESSION_MODE": "resume"}):
        result = workspace.invoke("muse", lane, "--backend", "muse", "--model",
                                  "fixture-model-1", "--effort", "high")
        check(result.returncode == 0, f"fresh dispatch fixture failed: {result.stderr}")
        argv = args_of(workspace.calls()[-1])
        check(argv[argv.index("--prompt-file") + 1] == "../brief.md",
              f"fresh fixture inherited its enclosing worker's resume prompt: {argv}")
        result = workspace.invoke("claude", lane, "--backend", "claude", "--model",
                                  "fixture-model-claude", "--effort", "medium")
        check(result.returncode == 0, f"fresh Claude fixture failed: {result.stderr}")
        argv = args_of(workspace.calls()[-1])
        check("--session-id" in argv and "--resume" not in argv,
              f"fresh fixture inherited its enclosing worker's session mode: {argv}")
    check(os.environ.get("TILLR_RESUME_PROMPT_FILE") != str(prompt),
          "fixture probe leaked its temporary resume prompt")


def test_adapter_argv(workspace):
    # --- the muse adapter: the sandbox posture, from the argv it actually execs.
    lane = workspace.lane()
    stream = BACKENDS / "muse" / "testdata" / "complete.jsonl"
    workspace.log.write_text("")
    result = workspace.invoke(
        "muse",
        lane,
        "--backend",
        "muse",
        "--model",
        "fixture-model-1",
        "--effort",
        "high",
        stream=stream,
    )
    check(result.returncode == 0, f"muse: exit {result.returncode}: {result.stderr}")
    check(
        result.stdout == stream.read_text(),
        "muse: stdout is not the harness's stream verbatim, so run exec's log would "
        "not be the raw stream",
    )
    check(
        (lane / "stream.err").read_text() == "stub stderr\n",
        "muse: the harness's stderr did not land in stream.err",
    )
    calls = workspace.calls()
    check(len(calls) == 2, f"muse: {len(calls)} harness calls, want model-profile then exec")
    resolve, run = calls
    check(
        args_of(resolve)[:2] == ["model-profile", "show"]
        and "fixture-model-1" in args_of(resolve)
        and "--effort" in args_of(resolve),
        f"muse: the model was not resolved before launch: {args_of(resolve)}",
    )
    argv = args_of(run)
    check(argv[0] == "exec" and "--json" in argv, f"muse: argv is {argv}")
    check(
        not walled(run),
        f"muse: a wall was wrapped around a run that passed no --timeout: "
        f"{field_of(run, 'parent=')!r}",
    )
    check(
        not any(a.startswith("--print-timeout") for a in argv),
        f"muse: a harness turn bound reached a run that passed no --timeout: {argv}",
    )
    check(
        "--prompt-file" in argv and argv[argv.index("--prompt-file") + 1] == "../brief.md",
        f"muse: the brief is not named on argv: {argv}",
    )
    check(
        argv[argv.index("--workspace") + 1] == os.path.realpath(lane / "worktree"),
        f"muse: --workspace is not rooted at the named target: {argv}",
    )
    for forbidden in ("--disable-sandbox", "--yolo"):
        check(
            forbidden not in argv,
            f"muse: argv carries {forbidden}, which switches the boundary off",
        )
    check(
        argv[argv.index("--model") + 1] == "fixture-model-1"
        and argv[argv.index("--reasoning-effort") + 1] == "high",
        f"muse: the policy's model and effort are not on argv: {argv}",
    )
    check(
        argv[argv.index("--session-id") + 1] == "44444444-4444-7444-8444-444444444444",
        f"muse: the stager-minted session id is not on argv: {argv}",
    )
    check(
        field_of(run, "cwd=") == os.path.realpath(lane / "worktree"),
        f"muse: the harness ran in {field_of(run, 'cwd=')}, not the target",
    )
    # The wall is a wrapper, so a hang is bounded by the brief's number, not the default.
    lane = workspace.lane()
    workspace.log.write_text("")
    result = workspace.invoke(
        "muse",
        lane,
        "--backend",
        "muse",
        "--model",
        "fixture-model-1",
        "--effort",
        "high",
        "--timeout",
        "90",
        "--provider",
        "echo",
        stream=stream,
    )
    run = workspace.calls()[-1]
    check(
        (field_of(run, "parent=") or "").startswith("timeout 90 "),
        f"muse: the wall is not the brief's: {field_of(run, 'parent=')!r}",
    )
    # A `--timeout 0` is the same as no wall, so nothing is wrapped around the harness.
    workspace.log.write_text("")
    result = workspace.invoke(
        "muse",
        lane,
        "--backend",
        "muse",
        "--model",
        "fixture-model-1",
        "--effort",
        "high",
        "--timeout",
        "0",
        "--provider",
        "echo",
        stream=stream,
    )
    check(result.returncode == 0, f"muse: a zero wall exited {result.returncode}: {result.stderr}")
    run = workspace.calls()[-1]
    check(
        not walled(run),
        f"muse: --timeout 0 still wrapped the harness: {field_of(run, 'parent=')!r}",
    )
    check(
        "--provider" in args_of(run)
        and args_of(run)[args_of(run).index("--provider") + 1] == "echo",
        f"muse: the operator's no-token knob did not reach the harness: {args_of(run)}",
    )

    # A target that is not the directory the run sits in is refused, because the target is
    # the harness's write root and a mismatch would root it where the fence does not look.
    lane = workspace.lane()
    workspace.log.write_text("")
    result = workspace.invoke(
        "muse",
        lane,
        "--backend",
        "muse",
        "--model",
        "fixture-model-1",
        "--effort",
        "high",
        "--target",
        str(workspace.root),
        stream=stream,
    )
    check(
        result.returncode == 2,
        f"muse: a target outside the run exited {result.returncode}, want 2: {result.stderr}",
    )
    check(workspace.calls() == [], "muse: the harness ran with a target it was not in")

    # The harness's exit code is the adapter's, which is what run.record records.
    lane = workspace.lane()
    result = workspace.invoke(
        "muse",
        lane,
        "--backend",
        "muse",
        "--model",
        "fixture-model-1",
        "--effort",
        "high",
        stream=stream,
        exit_code=7,
    )
    check(result.returncode == 7, f"muse: exit {result.returncode}, want the harness's 7")

    # --- the Pi adapter: the brief through its own seam, effort as its own flag.
    lane = workspace.lane()
    stream = BACKENDS / "pi" / "testdata" / "complete.jsonl"
    workspace.log.write_text("")
    result = workspace.invoke(
        "pi",
        lane,
        "--backend",
        "pi",
        "--model",
        "fixture-model-pi",
        "--effort",
        "medium",
        stream=stream,
    )
    check(result.returncode == 0, f"pi: exit {result.returncode}: {result.stderr}")
    calls = workspace.calls()
    check(len(calls) == 1, f"pi: {len(calls)} harness calls, want 1 (no resolution probe)")
    argv = args_of(calls[0])
    check(
        "--print" in argv and "--mode" in argv and argv[argv.index("--mode") + 1] == "json",
        f"pi: argv is {argv}",
    )
    check(
        argv[argv.index("--thinking") + 1] == "medium",
        f"pi: the thinking level is not on argv: {argv}",
    )
    brief = lane / "brief.md"
    check(
        field_of(calls[0], "stdin=") == brief.read_text().splitlines()[0],
        "pi: the brief's text did not travel on stdin",
    )
    check(
        "-p" not in argv and not any(a.startswith("# ") for a in argv),
        "pi: brief text still travels in argv and is subject to the OS argument limit",
    )
    check(
        field_of(calls[0], "env:BOARD_DRAIN_BRIEF=") == str(brief),
        f"pi: BOARD_DRAIN_BRIEF is {field_of(calls[0], 'env:BOARD_DRAIN_BRIEF=')}",
    )
    check(
        (lane / "stream.err").read_text() == "stub stderr\n",
        "pi: the harness's stderr did not land in stream.err",
    )

    # --- the Agy Flash adapter: the brief on stdin, the effort inside the id, the
    # launcher's flag injection off.
    lane = workspace.lane()
    stream = BACKENDS / "agy" / "testdata" / "complete.json"
    workspace.log.write_text("")
    result = workspace.invoke(
        "agy",
        lane,
        "--backend",
        "agy",
        "--model",
        "fixture-model-agy",
        stream=stream,
    )
    check(result.returncode == 0, f"agy: exit {result.returncode}: {result.stderr}")
    calls = workspace.calls()
    check(len(calls) == 1, f"agy: {len(calls)} harness calls, want 1")
    argv = args_of(calls[0])
    check(
        argv[argv.index("--output-format") + 1] == "json",
        f"agy: the envelope format is not asked for: {argv}",
    )
    check(
        not walled(calls[0]) and "--print-timeout" not in argv,
        f"agy: a bound was passed for a run that passed no --timeout: "
        f"{field_of(calls[0], 'parent=')!r} {argv}",
    )
    check(
        field_of(calls[0], "env:YOLO_NO_LAUNCH_FLAGS=") == "1",
        "agy: the launcher's flag injection is not off, so the model on argv is not "
        "the model that would run",
    )
    check(
        field_of(calls[0], "stdin=") == (lane / "brief.md").read_text().splitlines()[0],
        f"agy: the brief did not reach the harness on stdin: "
        f"{field_of(calls[0], 'stdin=')}",
    )
    check(
        "--effort" not in argv,
        "agy: an effort flag reached the harness, which carries the tier in the id",
    )

    # An explicit wall is passed twice: `timeout` bounds the process, and `--print-timeout`
    # bounds the harness's own turn.
    lane = workspace.lane()
    workspace.log.write_text("")
    result = workspace.invoke(
        "agy",
        lane,
        "--backend",
        "agy",
        "--model",
        "fixture-model-agy",
        "--timeout",
        "90",
        stream=stream,
    )
    check(result.returncode == 0, f"agy: exit {result.returncode}: {result.stderr}")
    run = workspace.calls()[-1]
    argv = args_of(run)
    check(
        (field_of(run, "parent=") or "").startswith("timeout 90 "),
        f"agy: the wall is not the brief's: {field_of(run, 'parent=')!r}",
    )
    check(
        argv[argv.index("--print-timeout") + 1] == "90s",
        f"agy: --print-timeout is not the brief's wall with its unit: {argv}",
    )

    test_claude_argv(workspace)


def test_claude_argv(workspace):
    """The claude adapter's argv, env and stdin, read from what the stub was handed."""
    stream = BACKENDS / "claude" / "testdata" / "complete.jsonl"
    session = "44444444-4444-7444-8444-444444444444"
    # One harness argument holds a space and a newline, so the test sees whether the tail
    # arrives as the same words or is re-split on the way.
    tail = ["--tools", "Read,Edit,Bash", "--append-system-prompt", "two words\nand a second line"]

    lane = workspace.lane()
    workspace.log.write_text("")
    result = workspace.invoke(
        "claude",
        lane,
        "--backend",
        "claude-designer",
        "--model",
        "fixture-model-claude",
        "--effort",
        "medium",
        "--",
        *tail,
        stream=stream,
    )
    check(result.returncode == 0, f"claude: exit {result.returncode}: {result.stderr}")
    check(
        result.stdout == stream.read_text(),
        "claude: stdout is not the harness's stream verbatim, so run exec's log would not "
        "be the raw stream",
    )
    check(
        (lane / "stream.err").read_text() == "stub stderr\n",
        "claude: the harness's stderr did not land in stream.err",
    )
    calls = workspace.calls()
    check(len(calls) == 1, f"claude: {len(calls)} harness calls, want 1")
    call = calls[0]
    want = [
        "-p",
        "--output-format",
        "stream-json",
        "--verbose",
        "--model",
        "fixture-model-claude",
        "--effort",
        "medium",
        "--session-id",
        session,
        "--strict-mcp-config",
        "--dangerously-skip-permissions",
        *tail,
    ]
    check(args_of(call) == want, f"claude: argv is {args_of(call)}, want {want}")
    check(not walled(call), f"claude: a wall wrapped a run that passed no --timeout: {field_of(call, 'parent=')!r}")
    brief = lane / "brief.md"
    check(
        field_of(call, "stdin=") == brief.read_text().splitlines()[0],
        f"claude: the brief did not reach the harness on stdin: {field_of(call, 'stdin=')}",
    )
    check(
        field_of(call, "env:YOLO_NO_LAUNCH_FLAGS=") == "1",
        "claude: the launcher's flag injection is not off, so the jail would add flags of its own",
    )
    check(
        field_of(call, "env:BOARD_DRAIN_BRIEF=") == str(brief),
        f"claude: BOARD_DRAIN_BRIEF is {field_of(call, 'env:BOARD_DRAIN_BRIEF=')}",
    )
    check(
        field_of(call, "env:BOARD_DRAIN_RUN=") == (os.environ.get("TILLR_RUN_ID") or "tillr-lane"),
        f"claude: BOARD_DRAIN_RUN is {field_of(call, 'env:BOARD_DRAIN_RUN=')!r}; an empty marker reads as not a lane",
    )
    check(
        field_of(call, "env:BOARD_DRAIN_SESSION=") == session,
        f"claude: BOARD_DRAIN_SESSION is {field_of(call, 'env:BOARD_DRAIN_SESSION=')!r}",
    )

    # No tail, no separator: the fixed flags are the whole argv, and a wall wraps it.
    lane = workspace.lane()
    workspace.log.write_text("")
    result = workspace.invoke(
        "claude",
        lane,
        "--backend",
        "claude",
        "--model",
        "fixture-model-claude",
        "--effort",
        "low",
        "--timeout",
        "90",
        stream=stream,
        exit_code=7,
    )
    check(result.returncode == 7, f"claude: exit {result.returncode}, want the harness's 7")
    call = workspace.calls()[-1]
    argv = args_of(call)
    check(
        argv[-2:] == ["--strict-mcp-config", "--dangerously-skip-permissions"] and "--" not in argv,
        f"claude: a run with no harness arguments has argv {argv}",
    )
    check(
        (field_of(call, "parent=") or "").startswith("timeout 90 "),
        f"claude: the wall is not the brief's: {field_of(call, 'parent=')!r}",
    )

    # A target that is not the directory the run sits in is refused before anything runs.
    lane = workspace.lane()
    workspace.log.write_text("")
    result = workspace.invoke(
        "claude",
        lane,
        "--backend",
        "claude",
        "--model",
        "fixture-model-claude",
        "--effort",
        "medium",
        "--target",
        str(workspace.root),
    )
    check(result.returncode == 2, f"claude: a target outside the run exited {result.returncode}, want 2")
    check(workspace.calls() == [], "claude: the harness ran with a target it was not in")

    # No `claude` on PATH is an environment defect, refused as one. The PATH holds only the
    # two programs the preflight itself needs, so a `claude` installed on this machine
    # cannot make the case pass by accident.
    bare = workspace.root / "bare-bin"
    bare.mkdir(exist_ok=True)
    for tool in ("dirname", "grep"):
        link = bare / tool
        if not link.exists():
            link.symlink_to(shutil.which(tool))
    lane = workspace.lane()
    result = workspace.invoke(
        "claude",
        lane,
        "--backend",
        "claude",
        "--model",
        "fixture-model-claude",
        "--effort",
        "medium",
        path=str(bare),
    )
    check(
        result.returncode == 2 and "'claude' is not on PATH" in result.stderr,
        f"claude: a missing harness exited {result.returncode}: {result.stderr!r}",
    )


def main():
    root = Path(tempfile.mkdtemp(dir=os.environ.get("YOLO_DURABLE_DIR")))
    try:
        test_extractors(root)
        print("ok: every extractor renders both fixtures, and the footerless one is refused")
        test_policy()
        print("ok: the policy names a default backend, its adapters exist, and no id is in the tillr tree")
        workspace = Workspace(root)
        test_preflight(workspace)
        print("ok: every adapter refuses a bad dispatch before its harness runs")
        test_fixture_does_not_inherit_resume_prompt(workspace)
        print("ok: fresh adapter fixtures do not inherit the enclosing worker's resume prompt")
        test_adapter_argv(workspace)
        print("ok: each adapter's argv carries the posture, the dialect and the wall it declares")
    finally:
        shutil.rmtree(root, ignore_errors=True)
    print("the four backends produce one report shape, and the truncated stream is refused")


if __name__ == "__main__":
    main()
