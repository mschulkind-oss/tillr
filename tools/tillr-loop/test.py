#!/usr/bin/env python3
"""tillr-loop's adapters, run against stub stand-ins for the three real programs.

Every stub is a shell script in a temp directory on PATH that answers a face with a
chosen exit code and appends its argv to a log. Nothing here runs a real gate, a real
model, or a real store: what is under test is the mapping from a program's exit code onto
the contract `tillr drain loop` reads, the refusal for a judge that cannot run, and the
self-check that names a tick's argv without running it.
"""

import hashlib
import json
import os
import runpy
import shlex
import shutil
import subprocess
import sys
import tempfile
import traceback
from pathlib import Path

HERE = Path(__file__).resolve().parent
ADAPTER = HERE / "tillr-loop"

# The loop's own contract: gate 0 pass / 1 fail / else could-not-judge, land 0 landed /
# 1 not-landed, verdict 0 recorded / non-zero refused.
GATE_PASS, GATE_FAIL, GATE_COULD_NOT_JUDGE = 0, 1, 4
LAND_LANDED, LAND_NOT_LANDED = 0, 1
VERDICT_RECORDED = 0
ADAPTER_REFUSED = 2

STUBS = {
    # The gate stand-in answers plan and run with a chosen exit, and — on request — hands
    # the `--gate`/`--gate-arg` program on exactly as the adapter spelled it, so a test can
    # read the argv the real face would execute rather than the flags that spell it.
    "tillr-gate": """#!/bin/sh
log() { printf '%s\n' "$*" >> "$STUB_LOG"; }
log "$@"
case "$1" in
  plan) exit "${STUB_PLAN:-0}" ;;
  run)
    program=""
    args=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --gate) program="$2"; shift 2 ;;
        --gate-arg) args="$args $2"; shift 2 ;;
        *) shift ;;
      esac
    done
    log "env PI_FORKS_ROOT=${PI_FORKS_ROOT:-<unset>}"
    if [ -n "${STUB_EXEC_GATE:-}" ] && [ -n "$program" ]; then
      # shellcheck disable=SC2086
      $program $args
      exit $?
    fi
    exit "${STUB_RUN:-0}" ;;
esac
exit 0
""",
    # The lander has no queue step: the gate's verdict is the only input, so the stand-in
    # answers `pass` and nothing else.
    "tillr-land": """#!/bin/sh
log() { printf '%s\n' "$*" >> "$STUB_LOG"; }
log "$@"
case "$1" in
  pass) exit "${STUB_LAND:-0}" ;;
esac
exit 0
""",
    "tillr-item": """#!/bin/sh
log() { printf '%s\n' "$*" >> "$STUB_LOG"; }
log "$@"
case "$1" in
  show) cat "$STUB_ITEM_SHOW"; exit 0 ;;
  verdict) exit "${STUB_VERDICT:-0}" ;;
esac
exit 0
""",
}

# The python stand-in plays both python roles the gate's argv names: the registry export the
# adapter runs itself, and the gate program `tillr-gate run --gate` runs. It is written only
# by the cases that are about those two argv, so a case that expects no export to run has no
# stand-in for one.
PYTHON_STUB = """#!/bin/sh
log() { printf '%s\n' "$*" >> "$STUB_LOG"; }
log "python3-argv: $*"
log "python3-cwd: $(pwd)"
case "$*" in
  *--registry-json*) printf '{"declarations": [], "in_process": []}\n' ;;
esac
exit 0
"""

ITEM_SHOW = {
    "record_version": 1,
    "kind": "item.record",
    "id": "item-1",
    "producer": {"system": "tillr", "utility": "tillr-item", "version": "0.1.0"},
    "created_at": "2026-01-01T00:00:00Z",
    "payload": {
        "item_id": "item-1",
        "title": "the item under test",
        "state": "ready",
        "criteria": [
            {"criterion_id": "c1", "text": "the first criterion", "state": "pending", "claim_class": "verification-only"},
            {"criterion_id": "c2", "text": "the second criterion", "state": "pending", "claim_class": "verification-only"},
        ],
    },
}


def git(directory, *args, env=None):
    subprocess.run(
        ["git", "-C", str(directory), *args], check=True, capture_output=True, text=True,
        env=env,
    )


def path_without_judges(path):
    """The same PATH with every directory that holds a `tillr-*` binary removed.

    A case that asserts a judge is not installed has to reach that state, and prepending an
    empty directory does not: the real binary stays further along PATH, so on a machine
    where tillr-gate is installed the adapter finds and runs it and the case passes only in
    an environment (the repository gate's) that has none. The interpreter and the tools the
    adapter itself shells out to (`git`, `sh`) keep their directories; the fixture's own
    stub directory holds the three stand-ins and is dropped with the rest.
    """
    kept = []
    for entry in path.split(os.pathsep):
        if any(Path(entry or ".").glob("tillr-*")):
            continue
        kept.append(entry)
    return os.pathsep.join(kept)


def make_repo(tmp):
    """A repository with a base commit and a candidate commit on a lane branch."""
    repo = Path(tmp) / "repo"
    repo.mkdir(parents=True)
    identity = {
        "GIT_AUTHOR_NAME": "tillr loop test",
        "GIT_AUTHOR_EMAIL": "tillr-loop@example.invalid",
        "GIT_COMMITTER_NAME": "tillr loop test",
        "GIT_COMMITTER_EMAIL": "tillr-loop@example.invalid",
    }
    env = dict(os.environ, **identity)
    git(repo, "init", "-q", "-b", "main")
    (repo / "file.txt").write_text("base\n")
    git(repo, "add", "file.txt")
    git(repo, "commit", "-q", "-m", "base", env=env)
    base = subprocess.run(
        ["git", "-C", str(repo), "rev-parse", "HEAD"], capture_output=True, text=True, check=True
    ).stdout.strip()
    git(repo, "checkout", "-q", "-b", "tillr/lane-1")
    (repo / "file.txt").write_text("base\ncandidate\n")
    git(repo, "commit", "-q", "-am", "candidate", env=env)
    head = subprocess.run(
        ["git", "-C", str(repo), "rev-parse", "HEAD"], capture_output=True, text=True, check=True
    ).stdout.strip()
    git(repo, "checkout", "-q", "main")
    return repo, base, head


class Fixture:
    """A repository, a stub PATH, a work dir and a log of every stub invocation."""

    def __init__(self, tmp, stubs=True):
        self.tmp = Path(tmp)
        self.repo, self.base, self.head = make_repo(tmp)
        self.bin = self.tmp / "bin"
        self.bin.mkdir(parents=True)
        if stubs:
            for name, body in STUBS.items():
                path = self.bin / name
                path.write_text(body)
                path.chmod(0o755)
        self.log = self.tmp / "calls.log"
        self.log.write_text("")
        self.item_show = self.tmp / "item-show.json"
        self.item_show.write_text(json.dumps(ITEM_SHOW))
        self.work_dir = self.tmp / "work"
        self.run = "run-1"
        self.run_dir = self.work_dir / self.run

    def env(self, **extra):
        env = dict(os.environ)
        env["PATH"] = f"{self.bin}:{os.environ['PATH']}"
        env["STUB_LOG"] = str(self.log)
        env["STUB_ITEM_SHOW"] = str(self.item_show)
        # The registry export caches by the gate file's blob plus the tracked path list, and
        # a fixture's `tools/fork-check.py` is untracked, so every fixture shares one key --
        # and the cache outlives the temporary directory it was built in. A case that then
        # asserts the export RAN would read a cached file instead and a tree without a gate
        # would pass. The cache is per-fixture scratch, so it is born cold for each case.
        env["YOLO_DURABLE_DIR"] = str(self.tmp / "durable")
        env.update({key: str(value) for key, value in extra.items()})
        return env

    def candidate(self):
        return [
            "--item", "item-1", "--lane", "lane-1", "--run", self.run,
            "--worktree", str(self.repo), "--base", self.base, "--head", self.head,
        ]

    def common(self):
        return [
            "--root", str(self.repo), "--store", str(self.tmp / "state.db"),
            "--work-dir", str(self.work_dir), "--now", "2026-01-01T00:00:00Z",
        ]

    def calls(self):
        return [line for line in self.log.read_text().splitlines() if line]

    def write_stub(self, name, body):
        """Add one more stand-in to this fixture's PATH, beside the three faces."""
        path = self.bin / name
        path.write_text(body)
        path.chmod(0o755)
        return path

    def gate_verdict(self):
        self.run_dir.mkdir(parents=True, exist_ok=True)
        (self.run_dir / "gate.verdict.json").write_text("{}\n")

    def run_adapter(self, kind, extra=(), stubs=None, cwd=None, env=None, common=True):
        argv = [sys.executable, str(ADAPTER), kind, *self.candidate()]
        argv += self.common() if common else []
        argv += list(extra)
        return subprocess.run(
            argv,
            cwd=str(cwd or self.repo),
            env=env or self.env(**(stubs or {})),
            capture_output=True,
            text=True,
        )


def test_gate_exit_mapping(tmp):
    """The gate's three outcomes: 0 pass, 1 fail, and anything else could-not-judge."""
    fixture = Fixture(tmp)
    registry = ["--registry", str(fixture.tmp / "registry.json")]
    for stub_code, expected in ((0, GATE_PASS), (1, GATE_FAIL), (4, GATE_COULD_NOT_JUDGE),
                                (6, 6), (7, GATE_COULD_NOT_JUDGE)):
        result = fixture.run_adapter("gate", registry, stubs={"STUB_RUN": stub_code})
        assert result.returncode == expected, (stub_code, result.returncode, result.stderr)
    # A plan that could not be made is not a failing candidate either: no verdict exists.
    result = fixture.run_adapter("gate", registry, stubs={"STUB_PLAN": 3})
    assert result.returncode == GATE_COULD_NOT_JUDGE, (result.returncode, result.stderr)


def test_gate_names_the_gate_program_and_its_argv(tmp):
    """The gate's judgment is `tillr-gate run --gate <program>`, and the argv is exact.

    The adapter hands the program's own words on as `--gate` plus one `--gate-arg` each,
    and the default program is this tree's gate scoped to the candidate's base. That
    scoping is the whole reason a candidate can be judged at all: without `--since`
    `tools/fork-check.py` picks its own baseline and advances the shared fork-check state
    before the candidate has landed, and an absolute path would judge the checkout the
    loop runs from instead of the candidate's worktree.

    Negative control: the stub only hands the program on when the case asks it to
    (`STUB_EXEC_GATE`), and a stub that answered the exit itself while never invoking the
    program would leave the `python3-argv:` assertions with nothing to read.
    """
    fixture = Fixture(tmp)
    fixture.write_stub("python3", PYTHON_STUB)
    registry = ["--registry", str(fixture.tmp / "registry.json")]
    result = fixture.run_adapter("gate", registry, stubs={"STUB_EXEC_GATE": "1"})
    assert result.returncode == GATE_PASS, (result.returncode, result.stderr)
    runs = [call for call in fixture.calls() if call.startswith("run ")]
    assert runs, fixture.calls()
    want = (
        "--logs " + str(fixture.run_dir / "logs")
        + " --gate python3 --gate-arg tools/gate/change-gate.py --gate-arg --base"
        + " --gate-arg " + fixture.base
        + " --gate-arg --head --gate-arg " + fixture.head
        + " --gate-arg --runner --gate-arg tillr-gate"
        + " --gate-arg --logs --gate-arg " + str(fixture.run_dir / "change-logs")
    )
    assert want in runs[-1], runs[-1]
    assert "env PI_FORKS_ROOT=" + str(fixture.repo) in fixture.calls(), fixture.calls()
    # What `tillr-gate run` is handed: the program is relative, so the face's own
    # `cmd.Dir = <worktree>` resolves it inside the tree being judged, the range is the
    # candidate's base, `--tier landing` keeps the pre-publish checks, and `--lock-timeout`
    # waits for the record lock rather than failing the drain on a concurrent gate.
    programs = [c for c in fixture.calls() if c.startswith("python3-argv: ")]
    assert programs == [
        "python3-argv: tools/gate/change-gate.py --base " + fixture.base
        + " --head " + fixture.head + " --runner tillr-gate --logs "
        + str(fixture.run_dir / "change-logs")
    ], fixture.calls()
    assert "python3-cwd: " + str(fixture.repo) in fixture.calls(), fixture.calls()


def test_gate_exports_the_registry_when_none_is_named(tmp):
    """No `--registry` is not no gate: the adapter exports the tree's own first.

    The plan verifies the registry's SHA-256 digest and the verdict names the digest it
    planned against, so the registry is an input the gate run owns, not a constant the
    adapter may invent. It is exported from the tree's own `tools/fork-check.py` into the
    run's scratch, and the plan reads it from there.

    Negative control: a tree with no `tools/fork-check.py` cannot export one, and that run
    is exit 4 with no verdict rather than a pass (the second half of this case).
    """
    # The negative control first: the fixture repository carries no tools/fork-check.py.
    broken = Fixture(Path(tmp) / "no-gate")
    result = broken.run_adapter("gate")
    assert result.returncode == GATE_COULD_NOT_JUDGE, (result.returncode, result.stderr)
    assert "could not prepare the judgment" in result.stderr, result.stderr
    assert not [c for c in broken.calls() if c.startswith("run ")], broken.calls()

    fixture = Fixture(Path(tmp) / "with-gate")
    fixture.write_stub("python3", PYTHON_STUB)
    script = fixture.repo / "tools" / "fork-check.py"
    script.parent.mkdir(parents=True)
    script.write_text("")
    result = fixture.run_adapter("gate")
    assert result.returncode == GATE_PASS, (result.returncode, result.stderr)
    exported = str(fixture.run_dir / "registry.json")
    # The path is RELATIVE: the export runs with its working directory at the root, and a
    # relative `tools/fork-check.py` resolves inside the tree the root names rather than at
    # an absolute path that would survive the checkout moving.
    invoked = "python3-argv: tools/fork-check.py --registry-json"
    assert [c for c in fixture.calls() if c == invoked], fixture.calls()
    assert "python3-cwd: " + str(fixture.repo) in fixture.calls(), fixture.calls()
    # The file the plan reads is the export's own stdout, not a stream someone parsed.
    assert json.loads(Path(exported).read_text()) == {"declarations": [], "in_process": []}
    plans = [call for call in fixture.calls() if call.startswith("plan ")]
    assert plans and "--registry " + exported in plans[-1], plans
    # The export is a preparation, not a judgment: it runs before the plan and its own
    # exit would leave as exit 4 rather than as a verdict about the candidate.
    assert fixture.calls().index(invoked) < fixture.calls().index(plans[-1])


def test_gate_derives_the_change_with_the_gate_binary(tmp):
    """The change record is `tillr-gate change`'s, not the adapter's Python.

    `gate.change` is the record `plan` reads, so the binary that reads it derives it: the
    adapter spells the candidate flags and nothing about the record's shape (D-AQA4). The
    change is a preparation like the registry export — a change that cannot be derived is
    no judgment at all — so it runs before the plan that reads it.

    Negative control: this adapter used to write `gate.change.json` itself, and the stub
    gate writes no files. A change record under the run directory after this tick would
    therefore be the adapter's second spelling, which is exactly what the assertion of
    absence forbids.
    """
    fixture = Fixture(tmp)
    registry = ["--registry", str(fixture.tmp / "registry.json")]
    result = fixture.run_adapter("gate", registry)
    assert result.returncode == GATE_PASS, (result.returncode, result.stderr)
    calls = fixture.calls()
    changes = [call for call in calls if call.startswith("change ")]
    assert changes, calls
    want = (
        "change --item item-1 --lane lane-1 --run " + fixture.run
        + " --worktree " + str(fixture.repo) + " --base " + fixture.base
        + " --head " + fixture.head + " --now 2026-01-01T00:00:00Z"
        + " --output " + str(fixture.run_dir / "gate.change.json")
    )
    assert changes[0] == want, (changes[0], want)
    plans = [call for call in calls if call.startswith("plan ")]
    assert plans and calls.index(changes[0]) < calls.index(plans[-1]), calls
    assert "--input " + str(fixture.run_dir / "gate.change.json") in plans[-1], plans[-1]
    assert not (fixture.run_dir / "gate.change.json").exists(), (
        "the adapter wrote the change record itself instead of naming it to the face"
    )


def test_land_exit_mapping(tmp):
    """Land: 0 landed, 1 not-landed, and a store it cannot read is not a refusal."""
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    for stub_code, expected in ((0, LAND_LANDED), (1, LAND_NOT_LANDED),
                                (3, GATE_COULD_NOT_JUDGE), (2, GATE_COULD_NOT_JUDGE)):
        result = fixture.run_adapter("land", stubs={"STUB_LAND": stub_code})
        assert result.returncode == expected, (stub_code, result.returncode, result.stderr)
    # The lander is handed the verdict the gate wrote and the candidate's own four fields:
    # there is no queue row to enqueue and no candidate id to derive, so the pass call is
    # the whole of the land step.
    passes = [call for call in fixture.calls() if call.startswith("pass ")]
    assert passes, fixture.calls()
    assert not [call for call in fixture.calls() if call.startswith("enqueue ")], fixture.calls()
    want = (
        "pass --store " + str(fixture.tmp / "state.db")
        + " --repo " + str(fixture.repo)
        + " --item item-1 --run " + fixture.run
        + " --base " + fixture.base + " --head " + fixture.head
        + " --verdict " + str(fixture.run_dir / "gate.verdict.json")
        + " --base-ref main"
        + " --now 2026-01-01T00:00:00Z"
        + " --run-dir " + str(fixture.run_dir)
    )
    assert passes[-1] == want, (passes[-1], want)
    assert "--candidate" not in passes[-1], passes[-1]


def test_land_forwards_message_source(tmp):
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    result = fixture.run_adapter("land", extra=["--message-from", "worker-sha"])
    assert result.returncode == 0, result.stderr
    passes = [call for call in fixture.calls() if call.startswith("pass ")]
    assert "--message-from worker-sha" in passes[-1], passes
    result = fixture.run_adapter("land")
    assert result.returncode == 0, result.stderr
    passes = [call for call in fixture.calls() if call.startswith("pass ")]
    assert "--message-from" not in passes[-1], passes


def test_land_forwards_the_observed_trunk(tmp):
    """The drain's observed trunk reaches the lander, which checks it under the land lock.

    `tillr drain loop` appends `--observed-trunk <tip>` whenever its store holds a trunk
    observation, and `tillr-land pass` refuses with `trunk-rewritten` when the locked trunk no
    longer contains that tip. An adapter that refused the flag faulted every landing
    (2026-11-17); one that swallowed it would land across a rewrite the loop had seen.
    """
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    result = fixture.run_adapter("land", extra=["--observed-trunk", fixture.base])
    assert result.returncode == LAND_LANDED, (result.returncode, result.stderr)
    passes = [call for call in fixture.calls() if call.startswith("pass ")]
    assert passes and f"--observed-trunk {fixture.base}" in passes[-1], passes
    result = fixture.run_adapter("land")
    assert result.returncode == LAND_LANDED, (result.returncode, result.stderr)
    passes = [call for call in fixture.calls() if call.startswith("pass ")]
    assert "--observed-trunk" not in passes[-1], passes


def test_describe_answers_the_hook_contract(tmp):
    adapter = runpy.run_path(str(ADAPTER))
    result = subprocess.run([sys.executable, str(ADAPTER), "describe"], cwd=tmp,
                            capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    hooks = json.loads(result.stdout)["hooks"]
    candidate = {"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root"}
    expected = {
        "gate": candidate,
        "land": candidate | {"--observed-trunk", "--base-ref", "--message-from"},
        "verdict": candidate | {"--store", "--verdict"},
        "fixer-gate": candidate | {"--all", "--since"},
        "replay": {"--only"},
        "rolling": {"--all", "--since"},
    }
    assert set(hooks) == set(expected), hooks
    for name, required in expected.items():
        assert required <= set(hooks[name]), (name, hooks[name])
        # Capabilities must name real parser options, not an independent promise.
        parser = adapter["parser_for"](name)
        accepted = {flag for action in parser._actions for flag in action.option_strings}
        assert set(hooks[name]) <= accepted, (name, hooks[name], accepted)
    assert not list(Path(tmp).iterdir()), "describe wrote files"
    result = subprocess.run([sys.executable, str(ADAPTER), "describe", "--all"],
                            capture_output=True, text=True)
    assert result.returncode == 2, result


def test_revision_hooks_forward_defaults_and_exit_status(tmp):
    fixture = Fixture(tmp)
    stub = fixture.bin / "python3"
    stub.write_text(PYTHON_STUB.replace("exit 0", '''
log "fork-root: $PI_FORKS_ROOT"
log "spans-out: $TILLR_GATE_SPANS_OUT"
printf 'revision stdout\\n'
printf 'revision stderr\\n' >&2
exit "${STUB_REVISION:-0}"
'''))
    stub.chmod(0o755)
    for name, flags, prefix in (
        ("replay", ["--only", "test:one,test:two"], ["--replay"]),
        ("rolling", ["--all"], ["--tier", "rolling"]),
        ("rolling", ["--since", fixture.base], ["--tier", "rolling"]),
    ):
        for code in (0, 1, 2, 4, 6):
            fixture.log.write_text("")
            env = fixture.env(STUB_REVISION=code, PI_FORKS_ROOT=str(fixture.repo),
                              TILLR_GATE_SPANS_OUT=str(fixture.tmp / "spans.jsonl"))
            result = subprocess.run([sys.executable, str(ADAPTER), name, *flags],
                                    cwd=fixture.repo, env=env, capture_output=True, text=True)
            assert result.returncode == code, (name, flags, result.returncode, result.stderr)
            assert result.stdout == "revision stdout\n" and result.stderr == "revision stderr\n", result
            assert fixture.calls() == [
                "python3-argv: " + " ".join(["tools/fork-check.py", *prefix, *flags]),
                "python3-cwd: " + str(fixture.repo),
                "fork-root: " + str(fixture.repo),
                "spans-out: " + str(fixture.tmp / "spans.jsonl"),
            ], fixture.calls()
    for name, flags in (("replay", []), ("replay", ["--only", "x", "--replay"]),
                        ("rolling", []), ("rolling", ["--all", "--since", fixture.base]),
                        ("rolling", ["--tier", "rolling", "--all"])):
        result = subprocess.run([sys.executable, str(ADAPTER), name, *flags],
                                capture_output=True, text=True)
        assert result.returncode == 2, (name, flags, result)


def test_hook_dispatch_matches_drain_flag_cases(tmp):
    # The fence excludes drain Go test files. Go's overlay compiles this permanent
    # fixture as a drain-package test without writing anything into that module.
    root = HERE.parent.parent
    overlay = Path(tmp) / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {
        str(root / "tools/tillr-drain/drain/tillr_loop_hooks_test.go"):
            str(HERE / "testdata/drain-hooks_test.go"),
    }}))
    result = subprocess.run(
        ["go", "test", "-overlay", str(overlay), "./drain", "-run",
         "^TestTillrLoopDefinitionHookFlagParity$"],
        cwd=root / "tools/tillr-drain", capture_output=True, text=True)
    assert result.returncode == 0, result.stdout + result.stderr


def test_every_argv_the_drain_sends_is_accepted(tmp):
    """Each argv `tillr drain loop` builds, replayed against the face it reaches.

    `drain-argv.json` is written by the drain's own suite from its ProcessSteps code paths
    (`TestEveryArgvTheDrainSendsIsAcceptedByTheTillrLoopAdapter`), so it is the drain's
    argument lists, not a second spelling of them here. A face that stops declaring a flag the
    drain sends fails this case, at the landing tier, before a loop faults on it.
    """
    cases = json.loads((HERE / "drain-argv.json").read_text())
    assert {case["face"] for case in cases} == {"gate", "land", "verdict"}, cases
    fixture = Fixture(tmp)
    store = fixture.tmp / "state.db"
    env = fixture.env(TILLR_LOOP_REGISTRY=fixture.tmp / "registry.json")
    for case in cases:
        args = [word.replace("<worktree>", str(fixture.repo)).replace("<store>", str(store))
                .replace("<root>", str(fixture.repo)) for word in case["args"]]
        run_dir = fixture.repo / ".tillr" / "loop" / case["run"]
        run_dir.mkdir(parents=True, exist_ok=True)
        (run_dir / "gate.verdict.json").write_text("{}\n")
        fixture.log.write_text("")
        result = subprocess.run([sys.executable, str(ADAPTER), case["face"], *args],
                                cwd=str(fixture.repo), env=env, capture_output=True, text=True)
        assert result.returncode == 0, (case["case"], result.returncode, result.stderr)
        calls = [call for call in fixture.calls() if not call.startswith("env ")]
        last = calls[-1] if calls else ""
        want = {"gate": "run ", "land": "pass ", "verdict": "verdict record "}[case["face"]]
        assert last.startswith(want), (case["case"], calls)
        # Every flag the drain added after the candidate's own reaches the lander unchanged.
        for flag in ("--observed-trunk", "--message-from", "--base-ref"):
            if flag in args and case["face"] == "land":
                value = args[args.index(flag) + 1]
                assert f"{flag} {value}" in last, (case["case"], flag, last)


def test_check_argv_parses_without_reading_or_judging(tmp):
    """The startup mode accepts real argument lists even when every path is absent."""
    cases = json.loads((HERE / "drain-argv.json").read_text())
    for case in cases:
        result = subprocess.run(
            [sys.executable, str(ADAPTER), case["face"], "--check-argv", *case["args"]],
            cwd=tmp, capture_output=True, text=True)
        assert result.returncode == 0, (case["case"], result.stderr)
    assert not list(Path(tmp).iterdir()), "parse-only mode wrote files"
    help_result = subprocess.run([sys.executable, str(ADAPTER), "land", "--help"],
                                 capture_output=True, text=True)
    assert "tillr-step-argv-check-v1" in help_result.stdout, help_result
    refused = subprocess.run(
        [sys.executable, str(ADAPTER), "land", "--check-argv", *cases[3]["args"], "--unknown"],
        capture_output=True, text=True)
    assert refused.returncode == 2 and "--unknown" in refused.stderr, refused


def test_fixer_range_reaches_the_judge(tmp):
    """A fixer's `--since`/`--all` widens the default judge, and refuses a named one."""
    fixture = Fixture(tmp)
    registry = ["--registry", str(fixture.tmp / "registry.json")]
    for extra, want in ((["--since", fixture.base], f"--gate-arg --since --gate-arg {fixture.base}"),
                        (["--all"], "--gate-arg --all")):
        fixture.log.write_text("")
        result = fixture.run_adapter("gate", registry + extra)
        assert result.returncode == GATE_PASS, (extra, result.returncode, result.stderr)
        run = [call for call in fixture.calls() if call.startswith("run ")]
        assert run and run[-1].endswith(want), (extra, run)
        assert "--gate-arg --tier" not in run[-1], run
    result = fixture.run_adapter("gate", registry + ["--all", "--gate-program", "true"])
    assert result.returncode == ADAPTER_REFUSED, (result.returncode, result.stderr)
    result = fixture.run_adapter("gate", registry + ["--all", "--since", fixture.base])
    assert result.returncode == ADAPTER_REFUSED, (result.returncode, result.stderr)


def test_land_without_a_gate_verdict_refuses(tmp):
    """A landing against no verdict is a candidate nobody judged."""
    fixture = Fixture(tmp)
    result = fixture.run_adapter("land")
    assert result.returncode == GATE_COULD_NOT_JUDGE, (result.returncode, result.stderr)
    assert "verdict" in result.stderr, result.stderr
    assert fixture.calls() == [], fixture.calls()


def test_verdict_exit_mapping(tmp):
    """Verdict: 0 recorded, and any non-zero is a refused record, never a recorded one."""
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    result = fixture.run_adapter("verdict", ["--verdict", "pass"], stubs={"STUB_VERDICT": 0})
    assert result.returncode == VERDICT_RECORDED, (result.returncode, result.stderr)
    # One record per frozen criterion, each naming the hash item's store would freeze.
    records = sorted(fixture.run_dir.glob("item.verdict.*.json"))
    assert len(records) == 2, records
    record = json.loads(records[0].read_text())
    assert record["kind"] == "item.verdict", record
    assert record["payload"]["verdict"] == "done", record
    assert record["payload"]["actor_kind"] == "mechanical", record
    assert record["payload"]["criterion_hash"] != "", record
    # The record the stub was handed is the file the adapter wrote, named by --input.
    recorded = [call for call in fixture.calls() if call.startswith("verdict record ")]
    assert recorded and "--input " + str(records[0]) in recorded[0], recorded
    result = fixture.run_adapter("verdict", ["--verdict", "pass-inherited"], stubs={"STUB_VERDICT": 0})
    assert result.returncode == VERDICT_RECORDED, (result.returncode, result.stderr)
    result = fixture.run_adapter("verdict", ["--verdict", "pass"], stubs={"STUB_VERDICT": 2})
    assert result.returncode == 2, (result.returncode, result.stderr)


def test_verdict_leaves_owner_criterion(tmp):
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    shown = json.loads(fixture.item_show.read_text())
    shown["payload"]["criteria"][0]["claim_class"] = "owner-only"
    fixture.item_show.write_text(json.dumps(shown))
    result = fixture.run_adapter("verdict", ["--verdict", "pass"])
    assert result.returncode == VERDICT_RECORDED, (result.returncode, result.stderr)
    assert "c1" in result.stdout and "owner-only" in result.stdout, result.stdout
    records = list(fixture.run_dir.glob("item.verdict.*.json"))
    assert len(records) == 1, records
    payload = json.loads(records[0].read_text())["payload"]
    assert payload["criterion_id"] == "c2" and payload["verdict"] == "done", payload
    assert not (fixture.run_dir / "item.verdict.c1.json").exists()


def test_verdict_all_nonmechanical_and_missing_class(tmp):
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    shown = json.loads(fixture.item_show.read_text())
    shown["payload"]["criteria"] = [
        {"criterion_id": f"c{i}", "text": "a judgment", "claim_class": claim}
        for i, claim in enumerate(["red-gate-triage", "direction", "owner-only"], 1)
    ]
    fixture.item_show.write_text(json.dumps(shown))
    result = fixture.run_adapter("verdict", ["--verdict", "pass"])
    assert result.returncode == VERDICT_RECORDED, (result.returncode, result.stderr)
    assert result.stdout.count("left criterion") == 3, result.stdout
    assert not list(fixture.run_dir.glob("item.verdict.*.json"))
    assert not any(call.startswith("verdict record ") for call in fixture.calls())


def test_missing_class_names_both_builds(tmp):
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    shown = json.loads(fixture.item_show.read_text())
    del shown["payload"]["criteria"][0]["claim_class"]
    fixture.item_show.write_text(json.dumps(shown))
    stamp = "adapter-revision-fixture digest adapter-digest exe /build/tillr-drain"
    result = fixture.run_adapter("verdict", ["--verdict", "pass"],
                                 env=fixture.env(TILLR_LOOP_ADAPTER_BUILD=stamp))
    assert result.returncode == 4, result
    assert "tillr-item build is older than the adapter" in result.stderr, result.stderr
    assert stamp in result.stderr, result.stderr
    binary = fixture.bin / "tillr-item"
    assert str(binary.resolve()) in result.stderr, result.stderr
    assert hashlib.sha256(binary.read_bytes()).hexdigest() in result.stderr, result.stderr
    assert "launcher" in result.stderr, result.stderr
    assert "no valid stored claim_class" not in result.stderr, result.stderr
    assert not list(fixture.run_dir.glob("item.verdict.*.json"))


def test_unknown_class_refusal_is_unchanged(tmp):
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    shown = json.loads(fixture.item_show.read_text())
    for value in ("unknown-class", None, ""):
        shown["payload"]["criteria"][0]["claim_class"] = value
        fixture.item_show.write_text(json.dumps(shown))
        result = fixture.run_adapter("verdict", ["--verdict", "pass"])
        assert result.returncode == 4, result
        assert result.stderr.strip() == "tillr-loop verdict: criterion c1 has no valid stored claim_class", result.stderr
        assert not list(fixture.run_dir.glob("item.verdict.*.json"))


def test_build_names_revision_and_unstamped_paths(tmp):
    names = runpy.run_path(str(ADAPTER))
    binary = Path(tmp) / "tillr-item"
    binary.write_bytes(b"fixture native binary without a stamp")
    go = Path(tmp) / "go"
    go.write_text("#!/bin/sh\nprintf '\\tbuild\\tvcs.revision=fixture-revision\\n'\n")
    go.chmod(0o755)
    old_path = os.environ["PATH"]
    try:
        os.environ["PATH"] = f"{tmp}:{old_path}"
        assert names["item_build_name"](str(binary)) == "revision fixture-revision"
        go.write_text("#!/bin/sh\nexit 1\n")
        unstamped = names["item_build_name"](str(binary))
        assert str(binary) in unstamped, unstamped
        assert hashlib.sha256(binary.read_bytes()).hexdigest() in unstamped, unstamped
        launcher = HERE.parent.parent / "yolo-packs" / "tillr" / "programs" / "tillr-item" / "run"
        named = names["item_build_name"](str(launcher))
        assert "launcher" in named and str(launcher) in named, named
        assert hashlib.sha256(launcher.read_bytes()).hexdigest() in named, named
        # A direct, untracked adapter has no supplied stamp and must name itself honestly.
        direct = Path(tmp) / "adapter"
        direct.write_bytes(ADAPTER.read_bytes())
        stamp = os.environ.pop("TILLR_LOOP_ADAPTER_BUILD", None)
        try:
            direct_name = runpy.run_path(str(direct))["adapter_build_name"]()
            assert str(direct) in direct_name, direct_name
            assert hashlib.sha256(direct.read_bytes()).hexdigest() in direct_name, direct_name
        finally:
            if stamp is not None:
                os.environ["TILLR_LOOP_ADAPTER_BUILD"] = stamp
        # A clean tracked adapter identifies the revision whose bytes it actually holds.
        repo, _, _ = make_repo(Path(tmp) / "revision")
        tracked = repo / "adapter"
        tracked.write_bytes(ADAPTER.read_bytes())
        git(repo, "add", "adapter")
        git(repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid",
            "commit", "-qm", "tracked adapter")
        revision = subprocess.run(["git", "-C", str(repo), "rev-parse", "HEAD"],
                                  capture_output=True, text=True, check=True).stdout.strip()
        stamp = os.environ.pop("TILLR_LOOP_ADAPTER_BUILD", None)
        try:
            assert runpy.run_path(str(tracked))["adapter_build_name"]() == "revision " + revision
        finally:
            if stamp is not None:
                os.environ["TILLR_LOOP_ADAPTER_BUILD"] = stamp
    finally:
        os.environ["PATH"] = old_path


def test_adapter_copy_matches_source(tmp):
    copy = HERE.parent / "tillr-drain" / "drain" / "tillr-loop.py"
    assert copy.read_bytes() == ADAPTER.read_bytes(), "run python3 tools/tillr-loop/sync-adapter.py"


def test_failing_verdict_is_a_usage_error(tmp):
    """An attempt that did not land must not grade or freeze item criteria."""
    fixture = Fixture(tmp)
    result = fixture.run_adapter("verdict", ["--verdict", "fail"])
    assert result.returncode == 2, (result.returncode, result.stderr)
    assert "docs/design/tillr-judge-and-lifecycle.md#prevent-a-failed-landing-from-freezing-the-items-criteria" in result.stderr, result.stderr
    assert fixture.calls() == [], fixture.calls()
    assert not list(fixture.run_dir.glob("item.verdict.*.json"))


def test_missing_judge_is_not_a_failure(tmp):
    """A judge that is not installed judged nothing: exit 4, never 0 and never 1.

    The PATH this case gives the adapter is the machine's own with every directory that
    holds a `tillr-*` binary removed, so the binaries the assertion names are absent even
    where they are installed -- the state the case claims, rather than a machine that
    happens to lack them.
    """
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    env = fixture.env()
    env["PATH"] = path_without_judges(env["PATH"])
    # The premise, checked with the function the adapter itself uses rather than assumed:
    # no directory on this PATH holds any of the three judges, including the fixture's own
    # stub directory, which holds all three stand-ins.
    for name in STUBS:
        assert shutil.which(name, path=env["PATH"]) is None, (name, env["PATH"])
    for kind, extra in (("gate", ["--registry", str(fixture.tmp / "registry.json")]),
                        ("land", []), ("verdict", ["--verdict", "pass"])):
        result = fixture.run_adapter(kind, extra, env=env)
        assert result.returncode == GATE_COULD_NOT_JUDGE, (kind, result.returncode, result.stderr)
        assert "not on PATH" in result.stderr, (kind, result.stderr)


def test_self_check_names_argv_without_running_it(tmp):
    """The self-check prints the candidate argv and executes none of it."""
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    result = fixture.run_adapter("self-check", ["--registry", str(fixture.tmp / "registry.json")])
    assert result.returncode == 0, (result.returncode, result.stderr)
    assert fixture.calls() == [], fixture.calls()
    out = result.stdout
    # The three program strings, and the real argv each composes, candidate included.
    for kind in ("gate", "land", "verdict"):
        assert f"tools/tillr-loop/tillr-loop {kind}" in out, out
    assert "tillr-gate plan" in out, out
    assert "tillr-gate run" in out, out
    # The gate's judgment and the logs its verdict will name are visible before a night of
    # ticks, not discovered from a directory afterwards.
    assert "--gate python3 --gate-arg tools/gate/change-gate.py" in out, out
    assert "--gate-arg --base --gate-arg " + fixture.base in out, out
    assert "--gate-arg --head --gate-arg " + fixture.head in out, out
    assert "--logs " + str(fixture.run_dir / "logs") in out, out
    assert "PI_FORKS_ROOT=" + str(fixture.repo) in out, out
    assert "tillr-land pass" in out, out
    assert "tillr-item verdict record" in out, out
    assert "head=" + fixture.head in out, out
    assert "base=" + fixture.base in out, out
    assert "--worktree " + str(fixture.repo) in out, out
    assert str(fixture.run_dir / "gate.verdict.json") in out, out
    assert "tillr drain loop --root" in out, out


def test_self_check_shows_the_generated_registry(tmp):
    """A self-check with no registry names the export it would run and where it lands.

    The self-check exists so an operator sees a night's argv before spending a night on it;
    the generated registry is part of that argv, so it is printed as the redirect it is.
    """
    fixture = Fixture(tmp)
    fixture.gate_verdict()
    result = fixture.run_adapter("self-check")
    assert result.returncode == 0, (result.returncode, result.stderr)
    assert fixture.calls() == [], fixture.calls()
    out = result.stdout
    exported = str(fixture.run_dir / "registry.json")
    # The banner the adapter prints, and the export as the redirect it is: the step is
    # `sh -c <script> > <run>/registry.json`, so the shell command and its `--registry-json`
    # are visible, and so is the file the redirect creates.
    assert "tillr-loop self-check: one tick's three judgments" in out, out
    assert "--registry-json" in out, out
    assert " > " + shlex.quote(exported) in out, out
    assert "--registry " + exported in out, out


def test_no_root_refuses(tmp):
    """An adapter with no root, no candidate checkout and no cwd checkout refuses.

    The root is now derived from the candidate worktree when one is named, so a case that
    means "nothing to derive from" has to give the adapter no checkout at all: not the cwd,
    and not the worktree it names. Both are replaced by directories that are not
    repositories, and the ceiling stops git's search from finding one above them.
    """
    fixture = Fixture(tmp)
    elsewhere = fixture.tmp / "not-a-repository"
    elsewhere.mkdir()
    # A directory that is not a checkout: the ceiling stops git's search at it, so the
    # adapter has no root to derive and must not invent one.
    env = fixture.env()
    env["GIT_CEILING_DIRECTORIES"] = str(fixture.tmp)
    result = fixture.run_adapter(
        "land", ["--work-dir", str(fixture.work_dir), "--base-ref", "main",
                 "--worktree", str(elsewhere)],
        cwd=elsewhere, env=env, common=False,
    )
    assert result.returncode == ADAPTER_REFUSED, (result.returncode, result.stderr)
    assert "no root" in result.stderr, result.stderr




def test_the_three_faces_agree_on_the_run_directory_from_any_cwd(tmp):
    """A passing gate's verdict must be where the lander looks for it.

    Measured on the fifth live canary: the loop runs the gate in the candidate's *worktree*
    and the lander in the *root*, so a root derived from the working directory made the two
    disagree and the lander refused a candidate the gate had passed — "a candidate nobody
    judged". The root now comes from the candidate worktree's git common directory, which is
    the main checkout's `.git` however the adapter was reached.

    The case is written to fail without that derivation: the two runs happen in different
    working directories, and the paths they name must still be one path. A case that ran both
    from the same directory would pass either way — measured, and that is why this one does
    not.
    """
    fixture = Fixture(tmp)
    lane = fixture.tmp / "lane-worktree"
    subprocess.run(["git", "-C", str(fixture.repo), "worktree", "add", "-q", "--detach", str(lane), fixture.head],
                   check=True, capture_output=True)
    argv = ["--registry", str(fixture.tmp / "registry.json")]

    # The candidate's worktree is the lane worktree, because that is what the loop hands the
    # gate; the lander is reached from the root. Both name the same candidate.
    candidate = [
        "--item", "item-1", "--lane", "lane-1", "--run", fixture.run,
        "--worktree", str(lane), "--base", fixture.base, "--head", fixture.head,
    ]

    def named(cwd):
        result = subprocess.run(
            [sys.executable, str(ADAPTER), "self-check", *candidate, *argv],
            cwd=str(cwd), env=fixture.env(), capture_output=True, text=True,
        )
        # The self-check exits nonzero when a judge is missing from this fixture's PATH, and
        # that refusal is not what this case is about: it prints the paths it would use before
        # it refuses, and those paths are the whole question.
        return result.stdout + result.stderr

    from_root = named(fixture.repo)
    from_lane = named(lane)
    want = str(fixture.repo / ".tillr" / "loop" / fixture.run)
    assert want in from_root, ("the root's own run directory is not the repository's:", from_root)
    assert want in from_lane, (
        "the same candidate names a different run directory when the adapter is reached from "
        "its worktree, so the gate's verdict and the lander's read are different paths:\n"
        + from_lane)



def main():
    failures = []
    with tempfile.TemporaryDirectory() as tmp:
        cases = [
            test_gate_exit_mapping,
            test_gate_names_the_gate_program_and_its_argv,
            test_gate_exports_the_registry_when_none_is_named,
            test_gate_derives_the_change_with_the_gate_binary,
            test_land_exit_mapping,
            test_land_forwards_message_source,
            test_land_forwards_the_observed_trunk,
            test_describe_answers_the_hook_contract,
            test_revision_hooks_forward_defaults_and_exit_status,
            test_hook_dispatch_matches_drain_flag_cases,
            test_every_argv_the_drain_sends_is_accepted,
            test_check_argv_parses_without_reading_or_judging,
            test_fixer_range_reaches_the_judge,
            test_land_without_a_gate_verdict_refuses,
            test_verdict_exit_mapping,
            test_verdict_leaves_owner_criterion,
            test_verdict_all_nonmechanical_and_missing_class,
            test_missing_class_names_both_builds,
            test_unknown_class_refusal_is_unchanged,
            test_adapter_copy_matches_source,
            test_build_names_revision_and_unstamped_paths,
            test_failing_verdict_is_a_usage_error,
            test_missing_judge_is_not_a_failure,
            test_self_check_names_argv_without_running_it,
            test_self_check_shows_the_generated_registry,
            test_no_root_refuses,
            test_the_three_faces_agree_on_the_run_directory_from_any_cwd,
        ]
        # Every case runs, and every failure is reported before the exit: one broken case
        # used to stop the run, so the cases after it were never read and a second stale
        # assertion stayed hidden behind the first.
        for case in cases:
            with tempfile.TemporaryDirectory() as case_tmp:
                try:
                    case(case_tmp)
                except Exception as err:  # noqa: BLE001 - one case's break must not hide the rest
                    failures.append(case.__name__)
                    print(f"FAIL {case.__name__}: {err}", file=sys.stderr)
                    traceback.print_exc()
                else:
                    print(f"ok  {case.__name__}")
    if failures:
        print(
            f"tillr-loop test: {len(failures)} of {len(cases)} case(s) failed: "
            + ", ".join(failures),
            file=sys.stderr,
        )
        return 1
    print("tillr-loop adapters translate, refuse and self-check as documented")
    return 0


if __name__ == "__main__":
    sys.exit(main())
