#!/usr/bin/env python3
"""fork-check's routing: what `just done` runs, and what it refuses to run.

A synthetic repository, two real upstreams the fingerprints are read from, and a
stub `yolo` and `bin/pi-forks` that record every call. The cases pin the whole point
of the command:

  * a fresh repository checks each pack once, and a verified run records the
    baseline so the next clean run has nothing left to ask;
  * an upstream that moves is caught by `git ls-remote` alone -- a release tag for a
    pack that follows releases, a branch commit for one that says `follow: "head"`;
  * a series that changes pulls in the working-copy check and the build; a series whose
    working copy is absent refuses the run -- naming the missing checkout and its setup --
    instead of recording a verdict over steps that never ran;
  * Markdown convention guards, document rendering, structural proof, pack sync and
    parallel Go builds are owned by the declaration runner's fixture suites;
  * a change that can change what the roster storybook renders regenerates it, and a
    change that cannot leaves it alone;
  * a change to the meta-muse pack, the personal layer that selects it, or either of the
    two moved `tests/meta-muse-*.test.mjs` files runs both of them through `node --test`,
    and a change that touches none of them names the skip;
  * a change to a path the matt pack's four render/overlay tests read runs them through
    `node --test`, a failing one is a verdict, and a machine without `node` or `yolo`
    names the skip;
  * a dirty path a check reads is refused before anything runs, while a dirty path
    the checks do not read -- a config file sitting in the tree -- is named and
    ignored, so the commit still gets a verdict; a conflict verdict is a failure
    that does not advance the baseline, and a check that names no conflict is
    reported as "not verified" rather than as a verdict;
  * a pack that declares `patch_branch` and no `patches` is still planned -- its
    series is the one the contribution's `into` derives -- and the two patch-branch
    invariants run the bridge for the fork, watching the pack's directory and
    bin/pi-forks;
  * a verified run counts the gate steps it did not run apart from the ones that passed
    and the ones this machine could not answer, and names each of them, so a plan that
    examined a subset of the tree cannot read as full coverage;
  * the upstream a launch would build is asked of the bridge that carries the launch's
    own rule (`bin/pi-forks patch-branch target --fetch`), and its answer -- not a tag
    list -- is the fingerprint the record keeps and the reason the INV-2 step is asked
    with `--fetch`; when no checkout can answer, the tag-list probe answers instead and
    the plan SAYS the launch's resolution was unavailable, so a green headline never
    reads as a question asked of the ref a launch builds;
  * the installed yolo build is ADVISORY: a different one is reported and plans nothing,
    so the five steps it decides -- the series check, `yolo-config`, `meta-muse`,
    `matt-render` and `seat-profiles` -- run only when a path they watch changes, a same-commit rebuild is not
    even news, and a build that cannot name its sha256, or a baseline that names none, is
    named in the report;
  * a machine-local input is DECLARED elsewhere in one line with the command that answers
    it, and does not turn the run into 'not a verdict': a series whose patch-branch stamp
    is the host's is named while the pack's other series still decide the check, the run
    verifies and the baseline advances, and a conflict in the same transcript still fails
    it -- so nothing is loosened;
  * the baseline is kept per branch and planned from the newest verified commit this
    history contains: two worktrees started together both reach a verdict and neither
    refuses, a candidate no ref contains -- another branch's commit, or a candidate that
    never landed -- is skipped rather than refused or diffed into an empty plan, an entry
    this branch is behind is planned from the fork point rather than read as a first run,
    a branch's own entry that vanished from its own history is named, a first run and an
    incremental run still plan what they should, and a record written before per-branch
    entries still plans from its shared entry;
  * a change to `tools/fork-check.py` or to this suite plans the gate's own audit
    suite, and a failing assertion it names is a verdict -- FAILED, exit 1 -- while a
    failure that names no case (a crash, a timeout, an import error, a `SystemExit`
    with only a message) stays NOT VERIFIED, so the gate's own integrity is unknown
    rather than judged; a red self-suite never passes the batch either way;
  * a change confined to the gate's own selection or reporting code (`GATE_CODE`)
    plans the self-suite and no fork check, while a change to the code that
    materializes or judges a series (`MECHANISM`) still forces every fork check -- so a
    gate-only change lands in a pass while a fork's patch branch is behind its upstream
    (board item 412);
  * a change under any Go tool directory plans that directory's whole behavior
    suite, watching that directory alone; a machine without `go` names its skip;
  * a change to the rank-order suite, the rank alphabet it reads, or the pi-subagents
    series that builds its working copy runs the suite through `node --test`, and a
    missing working copy refuses the run (exit 2) instead of reporting an all-skipped
    suite as a pass, while a machine without `node` still names the skip;
  * a lane worktree with no fork checkouts refuses the run -- naming each missing working
    copy, its setup, and PI_FORKS_ROOT -- instead of verifying over steps that never ran
    and advancing the shared baseline, while PI_FORKS_ROOT naming a repository root whose
    `.forks/` holds populated checkouts restores the main-checkout verdict;
  * PI_FORKS_ROOT has one reading -- a repository root, never a forks directory -- and the
    gate and `bin/pi-forks` derive the same `.forks/` from it, so one value cannot misdirect
    either tool into a tree that exists but is the wrong one;
  * a node step whose suite drives the installed Pi is handed the package root the plan
    resolved -- so a landing's verdict never depends on what its shell exported -- and a
    machine where no root resolves names the skip with what it looked for, never the
    `Cannot find package 'undefined'` failure a missing variable produced;
  * a failed check costs exactly ONE run: the gate never re-runs it, there is no FLAKY
    outcome and no YELLOW, and the record keeps no flake ledger. What it produces instead is
    a triage packet read out of that run's own output -- the failing case, its file, the
    assertion, the paths it names, the commits that touched them, and the one-file command
    that reproduces it -- written machine-readably beside the record, printed in the report,
    and retrievable with `--triage` so the repair path can dispatch a debugger with it;
  * the gate's own audit and the pi-subagents unit suite are admitted alongside independent
    checks, and no fork-quality case is quarantined by default;
  * the shared pipeline-evidence format's conformance suite is planned when the format,
    its suite, or the gate's own record builder moves -- and a real gate run writes its own
    records in that format beside its record.
  * `--preflight` runs the SAME planned checks in a matrix of the gate's conditions -- the
    caller's shell, and the gate's own environment: HOME and XDG_STATE_HOME scrubbed,
    BOARD_STORE_STRICT, one check at a time -- and reports every failure in one pass,
    classifying a check that passed one cell and failed another as environment-conditional.
    A check that cannot pass a cell declares it in tools/preflight-declarations.json with
    the reason and the repair it waits on; a declaration covers the cell it names and no
    other; a malformed declarations file is refused whole; and a narrowed run (`--cells`,
    `--only`) names what it omitted. There is no cell that adds CPU load, and the self-suite
    invokes the preflight nowhere: it is a manual diagnostic, and case 53 fails the gate if
    a check asserts elapsed time or spawns load.
  * `--explain` runs nothing at all.
  * what a check's non-zero exit means is declared per kind and every kind the planner
    names is declared, and the default is the verdict -- a kind nobody classified fails
    closed rather than reaching "not verified" -- while the invariant kinds keep their own
    wording: a driver's named refusal is a failure and the same exit without it is an
  * a node upgrade is ADVISORY, like a yolo upgrade: a different version is reported
    and plans nothing, so the eleven steps it decides run only when a path they watch
    changes (item 114's rule, applied to node by item 126); a version that cannot be
    read, or a baseline that names none, is named in the report and plans nothing;
  * the exact set of suites a representative diff executes is pinned for the design's
    change classes -- board-only, driver-test-only, driver-behavior, one fork's series,
    and a pin bump -- so a routing change that widens or narrows execution fails here,
    and a carried check whose verdict came from the cache counts as not executed
    (board item 403);
  * the loop adapter's own suite is planned by a change under `tools/tillr-loop/` and
    runs `python3 tools/tillr-loop/test.py`, while a path outside that directory plans
    none -- so the suite that describes the front end cannot rot unrun again;
"""

import ast
import concurrent.futures
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import types
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "tools" / "fork-check.py"

_SELF_SUITE_TIMING_OUT = os.environ.pop("TILLR_SELF_SUITE_TIMING_OUT", "")
_PREAMBLE_STARTED_NS = time.monotonic_ns()
_SELF_SUITE_SHARD = None
_SELF_SUITE_SOURCE = None
_SELF_SUITE_JOBS = None
if sys.argv[1:2] == ["--self-suite-shard"] and len(sys.argv) in (3, 5):
    _SELF_SUITE_SHARD = int(sys.argv[2])
    if len(sys.argv) == 5 and sys.argv[3] == "--self-suite-source":
        _SELF_SUITE_SOURCE = Path(sys.argv[4]).resolve()
elif sys.argv[1:2] == ["--self-suite-jobs"] and len(sys.argv) == 3:
    _SELF_SUITE_JOBS = int(sys.argv[2])
elif os.environ.get("FORK_CHECK_SELF_SUITE_JOBS"):
    _SELF_SUITE_JOBS = int(os.environ["FORK_CHECK_SELF_SUITE_JOBS"])
_RUN_PREAMBLE_TESTS = _SELF_SUITE_SHARD is None


def load_tool(name):
    """One freshly executed copy of the gate, for the in-process cases below."""
    spec = importlib.util.spec_from_file_location(name, SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_landing_go_measurements():
    gate = load_tool("fork_check_landing_measurements")
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        spans = root / "spans.json"
        plan = gate.Plan()
        plan.tier = gate.TIER_LANDING
        plan.measure_landing = True
        plan.checks = [
            gate.Check("go-substrate", "substrate", ["go-test"], "watch", 0,
                       go_module="tools/tillr"),
            gate.Check("gate-runner", "runner", ["shards"], "watch", 0,
                       go_module="tools/tillr-gate"),
            gate.Check("gate-runner", "partial runner", ["partial"], "watch", 0,
                       go_module="tools/tillr-gate"),
            gate.Check("gate-runner", "cached runner", ["cached", "--cache"], "watch", 0,
                       go_module="tools/tillr-gate"),
            gate.Check("gate-runner", "cached fallback", ["cached-fallback", "--cache"], "watch", 0,
                       go_module="tools/tillr-gate"),
            gate.Check("waybar", "waybar", ["reused"], "watch", 0),
            gate.Check("go-substrate", "unstarted", ["unused"], "watch", 0,
                       go_module="tools/tillr"),
        ]
        suite, runner, partial, cached, cached_fallback, reused, unstarted = plan.checks
        handoffs = []
        calls = []
        key = "TILLR_GO_SHARDS_EVIDENCE_OUT"

        def run_command(_, argv, timeout, env, drop, cancel):
            calls.append(argv[0])
            child_env = gate.environ(env, drop)
            assert "TILLR_GATE_SPANS_OUT" not in child_env
            if argv[0] in {"shards", "partial", "cached", "cached-fallback", "legacy"}:
                assert child_env["GO_TEST_SHARDS_HOLDER"] == "gate:fixture-run"
            if argv[0] in {"shards", "partial", "cached", "cached-fallback"}:
                assert key not in drop
                target = Path(env[key])
                assert target.read_text() == "" and target not in handoffs
                handoffs.append(target)
                if argv[0] == "partial":
                    target.write_text(json.dumps({"discovery": "partial", "packages": [
                        {"import_path": "example/partial", "test_cache": "not-started"},
                    ]}))
                    return 1, "go list failed", 0.021
                if argv[0] == "cached":
                    target.write_text(json.dumps({"mode": "cached-shards", "packages": [
                        {"import_path": "example/hit", "test_cache": "hit"},
                        {"import_path": "example/miss", "test_cache": "miss"},
                        {"import_path": "example/lib", "test_cache": "not-applicable"},
                    ]}))
                    return 0, "ok\texample/hit\t(cached)\n", 0.015
                if argv[0] == "cached-fallback":
                    target.write_text(json.dumps([
                        {"import_path": "example/fallback", "test_cache": "hit"},
                    ]))
                    return 0, "", 0.012
                target.write_text(json.dumps([
                    {"import_path": "example/runner", "test_cache": "not-applicable"},
                ]))
                return 1, "go test -c failed", 0.021
            assert key not in env and key in drop
            if argv[0] == "go-test":
                return 0, ('ok\texample/native\t(cached) [no tests to run]\n'
                           '?\texample/library\t[no test files]\n'
                           '{"Action":"output","Package":"example/json","Output":"ok\\texample/json\\t0.001s\\n"}\n'), 0.01
            assert argv == ["legacy"], argv
            return 0, "", 0.0

        gate.run_command = run_command
        ticks = iter((1_000_000, 5_000_000, 10_000_000, 23_000_000,
                      30_000_000, 51_000_000, 60_000_000, 82_000_000,
                      90_000_000, 105_000_000, 110_000_000, 122_000_000))
        gate.time = types.SimpleNamespace(monotonic_ns=lambda: next(ticks), time_ns=lambda: 100)
        plan.checks = [reused]
        gate.apply_reuse(plan, ["waybar"])
        plan.checks = [suite, runner, partial, cached, cached_fallback, unstarted]
        with patch.dict(os.environ, {"TILLR_GATE_SPANS_OUT": str(root / "fixture-run" / "spans.json"),
                                     "TILLR_RUN_ID": "", "GO_TEST_SHARDS_HOLDER": ""}):
            for check in (suite, runner, partial, cached, cached_fallback):
                gate.execute_check(check, root, None, measure_landing=plan.measure_landing)
            legacy = gate.Check("gate-runner", "legacy", ["legacy"], "watch", 0,
                                go_module="tools/tillr-gate")
            gate.execute_attempt(root, legacy, "HEAD")
        assert not legacy.go_build
        assert calls == ["go-test", "shards", "partial", "cached", "cached-fallback", "legacy"], calls
        assert all(not path.exists() for path in handoffs), handoffs

        gate.write_gate_spans(str(spans), plan, root, 1, "failed", "", "", [], 1, 1, 0.05)
        body = json.loads(spans.read_text())
        assert body["not_run_checks"] == [gate.declaration_id(unstarted.kind, unstarted.label, root)]
        reused_id = gate.declaration_id(reused.kind, reused.label, root)
        assert body["reused_checks"] == [reused_id] and body["reused_wall_ms"] == {reused_id: 4}
        statuses = [span["status"] for span in body["check_spans"]]
        assert statuses == ["ok", "FAIL", "FAIL", "ok", "ok"], statuses
        native, sharded, incomplete, cached_span, fallback_span = [
            json.loads(row["attributes_json"]) for row in body["check_spans"]
        ]
        assert [(row["execution"], row["wall_ms"])
                for row in (native, sharded, incomplete, cached_span, fallback_span)] == [
            ("ran", 13), ("ran", 21), ("ran", 22), ("ran", 15), ("ran", 12),
        ]
        assert native["go_build"][0]["mode"] == "go-test", native
        assert native["go_build"][0]["packages"] == [
            {"import_path": "example/json", "test_cache": "miss"},
            {"import_path": "example/library", "test_cache": "not-applicable"},
            {"import_path": "example/native", "test_cache": "hit"},
        ], native
        assert sharded["go_build"][0]["mode"] == "compiled-shards", sharded
        assert sharded["go_build"][0]["packages"] == [
            {"import_path": "example/runner", "test_cache": "not-applicable"},
        ], sharded
        assert incomplete["go_build"][0]["mode"] == "compiled-shards", incomplete
        assert incomplete["go_build"][0]["discovery"] == "partial", incomplete
        assert incomplete["go_build"][0]["packages"] == [
            {"import_path": "example/partial", "test_cache": "not-started"},
        ], incomplete
        assert cached_span["go_build"][0]["mode"] == "cached-shards", cached_span
        assert "discovery" not in cached_span["go_build"][0], cached_span
        assert cached_span["go_build"][0]["packages"] == [
            {"import_path": "example/hit", "test_cache": "hit"},
            {"import_path": "example/miss", "test_cache": "miss"},
            {"import_path": "example/lib", "test_cache": "not-applicable"},
        ], cached_span
        assert fallback_span["go_build"][0]["mode"] == "cached-shards", fallback_span
        assert fallback_span["go_build"][0]["packages"] == [
            {"import_path": "example/fallback", "test_cache": "hit"},
        ], fallback_span


def test_cached_shards_go_tool_command_hits_go_test_cache():
    if shutil.which("go") is None:
        return
    gate = load_tool("fork_check_cached_shards_e2e")
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        mod_dir = root / "tools" / "tillr-store"
        mod_dir.mkdir(parents=True)
        (mod_dir / "go.mod").write_text("module example.com/fixture\n\ngo 1.23\n")
        (mod_dir / "lib.go").write_text("package fixture\nfunc One() int { return 1 }\n")
        (mod_dir / "lib_test.go").write_text(
            'package fixture\nimport "testing"\nfunc TestOne(t *testing.T) { if One() != 1 { t.Fatal(1) } }\n'
        )
        (root / "tools" / "gate").mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "go-test-shards.py", root / "tools" / "go-test-shards.py")
        shutil.copy2(ROOT / "tools" / "gate" / "go_affected.py", root / "tools" / "gate" / "go_affected.py")
        (root / "tools" / "go-test-durations.json").write_text("{}\n")
        durable = root / "durable"
        gocache = root / "gocache"
        env = {**os.environ, "YOLO_DURABLE_DIR": str(durable), "GOCACHE": str(gocache), "TILLR_RUN_ID": ""}
        cmd = gate.go_tool_command("tools/tillr-store")
        first = gate.Check("gate-runner", "tools/tillr-store's whole suite passes",
                           ["sh", "-c", cmd], "watch", 300, go_module="tools/tillr-store")
        rc1, out1, _ = gate.run_landing_go_check(root, first, env, (), None)
        assert rc1 == 0, out1
        assert first.go_build[0]["mode"] == "cached-shards", first.go_build
        assert first.go_build[0]["packages"] == [
            {"import_path": "example.com/fixture", "test_cache": "miss"},
        ], first.go_build

        second = gate.Check("gate-runner", "tools/tillr-store's whole suite passes",
                            ["sh", "-c", cmd], "watch", 300, go_module="tools/tillr-store")
        rc2, out2, _ = gate.run_landing_go_check(root, second, env, (), None)
        assert rc2 == 0, out2
        assert second.go_build[0]["mode"] == "cached-shards", second.go_build
        assert second.go_build[0]["packages"] == [
            {"import_path": "example.com/fixture", "test_cache": "hit"},
        ], second.go_build


def test_landing_measurements_require_change_gate_handoff():
    gate = load_tool("fork_check_measurement_scope")
    handoffs = {"TILLR_FAST_VERDICT_OUT": "verdict", "TILLR_GATE_SPANS_OUT": "spans"}
    opted_in = dict(handoffs, TILLR_ORDINARY_GATE_TIMING="1")
    cases = (
        (["--tier", "landing", "--since", "base"], opted_in, True),
        (["--tier", "landing", "--since", "base"], handoffs, False),
        (["--tier", "rolling", "--since", "base"], opted_in, False),
        (["--since", "base"], opted_in, False),
        (["--tier", "landing", "--since", "base", "--spans-out", "spans"],
         {"TILLR_ORDINARY_GATE_TIMING": "1"}, False),
    )
    for args, env, expected in cases:
        def stop_after_parsing(_, options):
            assert options.measure_landing is expected, (args, env, options)
            raise gate.PlanningRefusal("fixture: no planning or checks")

        with patch.dict(os.environ, dict.fromkeys(opted_in, "") | env), \
                patch.object(gate, "plan_gate", stop_after_parsing), contextlib.redirect_stderr(io.StringIO()):
            assert gate.run_gate(args) == 2


def test_gate_runner_uses_gate_slot_class_in_landing_and_proof_in_rolling():
    gate = load_tool("fork_check_gate_runner_slot_class")
    scratch_parent = os.environ.get("TMPDIR") or os.environ.get("YOLO_DURABLE_DIR")
    with tempfile.TemporaryDirectory(dir=scratch_parent) as directory:
        base = Path(directory)
        changed = ["tools/tillr-gate/main.go", "tools/tillr-drain/main.go"]

        # Landing-tier gate-runner uses --slot-class gate; go-tool is owed to rolling.
        landing = gate.build_plan(
            ROOT, [], gate.Record(base / "landing-state.json"),
            changed, None, True, tier=gate.TIER_LANDING,
        )
        landing_runner = next(c for c in landing.checks if c.kind == "gate-runner")
        assert "--slot-class gate" in landing_runner.argv[-1], landing_runner.argv
        assert "--slot-class proof" not in landing_runner.argv[-1], landing_runner.argv

        #Untiered + apply_tier(TIER_LANDING) also keeps --slot-class gate.
        untiered = gate.build_plan(
            ROOT, [], gate.Record(base / "untiered-state.json"), changed, None, True,
        )
        gate.apply_tier(untiered, gate.TIER_LANDING)
        untiered_runner = next(c for c in untiered.checks if c.kind == "gate-runner")
        assert "--slot-class gate" in untiered_runner.argv[-1], untiered_runner.argv

        # Rolling proof runs both gate-runner and go-tool under --slot-class proof,
        # whether tier=TIER_ROLLING is passed to build_plan or applied afterward.
        for via_build_plan in (True, False):
            rolling = gate.build_plan(
                ROOT, [], gate.Record(base / f"rolling-{via_build_plan}.json"),
                changed, None, True,
                tier=gate.TIER_ROLLING if via_build_plan else None,
            )
            if not via_build_plan:
                gate.apply_tier(rolling, gate.TIER_ROLLING)
            rolling_runner = next(c for c in rolling.checks if c.kind == "gate-runner")
            rolling_drain = next(c for c in rolling.checks if c.kind == "go-tool")
            assert "--slot-class proof" in rolling_runner.argv[-1], rolling_runner.argv
            assert "--slot-class gate" not in rolling_runner.argv[-1], rolling_runner.argv
            assert "--slot-class proof" in rolling_drain.argv[-1], rolling_drain.argv


def test_base_replay_queues_behind_trunk_proof():
    import importlib.util
    import threading

    gate = load_tool("fork_check_base_replay_slot_class")
    shards_spec = importlib.util.spec_from_file_location(
        "go_test_shards_replay_test", ROOT / "tools" / "go-test-shards.py",
    )
    shards = importlib.util.module_from_spec(shards_spec)
    shards_spec.loader.exec_module(shards)
    shards.ENFORCE_SLOT_LIMITS = True

    scratch_parent = os.environ.get("TMPDIR") or os.environ.get("YOLO_DURABLE_DIR")
    with tempfile.TemporaryDirectory(dir=scratch_parent) as directory:
        base = Path(directory)
        changed = ["tools/tillr-gate/main.go", "tools/tillr-drain/main.go"]
        plan = gate.build_plan(
            ROOT, [], gate.Record(base / "replay-state.json"), changed, None, True,
        )
        fake_gate = type("FakeGate", (), {
            "plan": plan, "missing": [], "diff_base": None, "head": "0" * 40,
        })()
        captured = []

        def fake_execute(root, check, diff_base, cancel=None):
            captured.append((check.kind, list(check.argv), dict(check.env)))
            check.rc = 0
            check.output = ""

        gate.plan_gate = lambda *args, **kwargs: fake_gate
        gate.execute_attempt = fake_execute
        gate.capture_fork_conflict = lambda *args, **kwargs: None

        rc = gate.run_replay([
            "--only", "go-tool:tools/tillr-drain's whole suite passes",
            "--only", "go-affected",
        ])
        assert rc == 0, rc
        by_kind = {kind: (argv, env) for kind, argv, env in captured}
        assert "go-tool" in by_kind and "go-affected" in by_kind, by_kind
        go_tool_argv, go_tool_env = by_kind["go-tool"]
        go_affected_argv, go_affected_env = by_kind["go-affected"]
        assert "--slot-class ordinary" in go_tool_argv[-1], go_tool_argv
        assert "--slot-class proof" not in go_tool_argv[-1], go_tool_argv
        assert "--slot-class gate" not in go_tool_argv[-1], go_tool_argv
        assert go_tool_env.get("GO_TEST_SHARDS_CLASS") == "ordinary", go_tool_env
        assert go_affected_env.get("GO_TEST_SHARDS_CLASS") == "ordinary", go_affected_env
        replay_slot_class = go_affected_env["GO_TEST_SHARDS_CLASS"]

        # Fixer-gate (--replay --fixer) is not a base replay and is not downgraded.
        captured.clear()
        rc = gate.run_replay([
            "--fixer", "--only", "go-tool:tools/tillr-drain's whole suite passes",
        ])
        assert rc == 0, rc
        fixer_argv, fixer_env = captured[0][1], captured[0][2]
        assert "--slot-class ordinary" not in fixer_argv[-1], fixer_argv
        assert "GO_TEST_SHARDS_CLASS" not in fixer_env, fixer_env

        # With a replay and a trunk proof both waiting on the same slot pool,
        # the trunk proof gets the next free slot ahead of the replay.
        lock_dir = base / "slot-locks"
        order = []
        order_lock = threading.Lock()
        replay_waiting = threading.Event()
        proof_waiting = threading.Event()
        proof_acquired = threading.Event()
        proof_release = threading.Event()
        replay_acquired = threading.Event()
        errors = []

        def run_waiter(name, slot_class, wait_evt, acq_evt, rel_evt):
            try:
                with shards.acquire_shard_slot(
                    lock_dir, 1,
                    notify=lambda _msg: wait_evt.set(),
                    slot_class=slot_class,
                    proof_limit=0,
                    gate_limit=0,
                    holder=name,
                ):
                    with order_lock:
                        order.append(name)
                    acq_evt.set()
                    if rel_evt is not None:
                        rel_evt.wait(30)
            except BaseException as exc:
                errors.append(exc)

        with shards.acquire_shard_slot(
            lock_dir, 1, slot_class="ordinary", proof_limit=0, gate_limit=0, holder="initial",
        ):
            t_replay = threading.Thread(
                target=run_waiter,
                args=("replay", replay_slot_class, replay_waiting, replay_acquired, None),
            )
            t_proof = threading.Thread(
                target=run_waiter,
                args=("trunk-proof", "proof", proof_waiting, proof_acquired, proof_release),
            )
            t_replay.start()
            assert replay_waiting.wait(30), "replay did not enter slot wait"
            t_proof.start()
            assert proof_waiting.wait(30), "trunk proof did not enter slot wait"

        assert proof_acquired.wait(30), "trunk proof did not acquire freed slot"
        proof_release.set()
        assert replay_acquired.wait(30), "replay did not acquire slot after proof"
        t_proof.join(30)
        t_replay.join(30)
        assert not errors, errors
        assert order == ["trunk-proof", "replay"], order


if _RUN_PREAMBLE_TESTS:
    test_landing_go_measurements()
    test_cached_shards_go_tool_command_hits_go_test_cache()
    test_landing_measurements_require_change_gate_handoff()
    test_gate_runner_uses_gate_slot_class_in_landing_and_proof_in_rolling()
    test_base_replay_queues_behind_trunk_proof()


def test_sigterm_ends_checks(mode):
    """Drive the real plan runner with a check in its own session and a grandchild."""
    with tempfile.TemporaryDirectory() as directory:
        base = Path(directory)
        ready = base / "ready"
        fixture = base / "runner.py"
        fixture.write_text('''import importlib.util, pathlib, sys, types
spec = importlib.util.spec_from_file_location("gate", sys.argv[1])
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)
check = gate.Check("rank-order", "detached check", [sys.executable, "-c",
    "import os,pathlib,subprocess,sys,time\\n"
    "child=subprocess.Popen(['sleep','300'])\\n"
    "pathlib.Path(sys.argv[1]).write_text(str(os.getpid())+' '+str(child.pid))\\n"
    "time.sleep(300)", sys.argv[2]], "", 0)
gate.execute_check = lambda check, root, diff_base, cancel, **kw: gate.run_command(root, check.argv, 0, cancel=cancel)
if sys.argv[3] == 'repeated':
    # A second signal arrives while Event.set holds its non-reentrant lock.
    # This is a deterministic reentrancy probe, not a timing/rate assertion.
    import os, signal, threading
    main_thread_id = threading.get_ident()
    class ReentrantEvent(threading.Event):
        fired = False
        def set(self):
            if threading.get_ident() == main_thread_id and not self.fired:
                self.fired = True
                with self._cond:
                    os.kill(os.getpid(), signal.SIGTERM)
            super().set()
    gate.threading.Event = ReentrantEvent
if sys.argv[3] == 'replay':
    gate.plan_gate = lambda *args: types.SimpleNamespace(
        plan=types.SimpleNamespace(checks=[check]), missing=[], diff_base=None, head='fixture')
    gate.execute_attempt = lambda root, check, diff_base, cancel=None: gate.run_command(root, check.argv, 0, cancel=cancel)
    gate.main(['--replay', '--only', 'rank-order'])
else:
    gate.run_plan_checks(types.SimpleNamespace(checks=[check]), pathlib.Path.cwd(), None, 1)
''')
        proc = subprocess.Popen([sys.executable, str(fixture), str(SCRIPT), str(ready), mode],
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        pids = []
        try:
            deadline = time.monotonic() + 15
            while not ready.exists() and proc.poll() is None and time.monotonic() < deadline:
                time.sleep(0.01)
            if not ready.exists():
                proc.kill()
                output = proc.communicate(timeout=15)
                raise AssertionError(f"SIGTERM fixture check never started: {output}")
            pids = [int(pid) for pid in ready.read_text().split()]
            proc.terminate()
            proc.communicate(timeout=15)
            for pid in pids:
                stat = Path(f"/proc/{pid}/stat")
                assert not stat.exists() or stat.read_text().split(") ", 1)[1].startswith("Z "), (
                    f"SIGTERM left {mode} check process {pid} alive")
            assert proc.returncode == 143, (mode, proc.returncode)
        finally:
            proc.kill() if proc.poll() is None else None
            proc.communicate(timeout=15)
            for pid in pids:
                try:
                    os.kill(pid, 9)
                except ProcessLookupError:
                    pass
    print(f"fork-check SIGTERM ({mode}): detached checks and descendants ended, exit 143")


if _RUN_PREAMBLE_TESTS:
    for sigterm_mode in (sys.argv[2:] or ["plan", "replay", "repeated"]):
        test_sigterm_ends_checks(sigterm_mode)
    if sys.argv[1:2] == ["--sigterm-only"]:
        sys.exit(0)

# Hermetic by construction. `just done` in a lane worktree exports PI_FORKS_ROOT, and the
# gate's own audit step runs THIS file inside that run, so an inherited value would point
# every fixture at the real checkouts and the missing-checkout cases would stop refusing.
# Each case that wants one sets it and removes it again.
os.environ.pop("PI_FORKS_ROOT", None)
# The same for the landing path's batch base. A landing exports FORK_CHECK_PLAN_BASE to
# every step it runs, this suite included, and names a commit the fixture repository does
# not contain; read from here, it refuses every fixture run before its first case. This
# process needs the strip too, because the in-process `build_plan` calls read `os.environ`.
os.environ.pop("FORK_CHECK_PLAN_BASE", None)
# The same for the drain's span file. `tools/tillr-drain/drain/steps.go` exports
# TILLR_GATE_SPANS_OUT to every gate child, and the gate's own audit step runs THIS file
# inside that child. The value names a run directory the drain has already swept, so an
# inherited copy aims each fixture gate's span write at a missing directory and
# `write_gate_spans` raises FileNotFoundError -- which failed the audit step and every item
# in the 2026-11-15 drain. This process needs the strip too: `run_gate` reads
# `os.environ` when its caller passes no `--spans-out`.
os.environ.pop("TILLR_GATE_SPANS_OUT", None)
# The drain also opts into exact-target drift observations. The routing fixtures
# stub merge-check verdicts, not the separate read of a real release target.
os.environ.pop("TILLR_FORK_DRIFT", None)
for _repo_var in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"):
    os.environ.pop(_repo_var, None)
# The variable the gate supplies to a node step that drives the installed Pi, and the
# fixture package root every plan in this suite resolves instead of the real build.
PI_PACKAGE_NAME = "PI_SUBAGENTS_PI_CODING_AGENT_PACKAGE_ROOT"
PI_PACKAGE_FIXTURE = Path("pi-package")

# The matt-render step's four tests, sorted the way every case's recorded call list is. The
# stubs in the fixture repository append these labels, so each case that plans the step pins
# the whole list: a test added to the check without a stub here, or a stub the step stopped
# running, fails. The `_EDITED` list is the same with the bedrock-context stub replaced by the
# marker version, so a case proves the step re-ran the edited file rather than a stale log.
MATT_RENDER_STUBS = ["agent-inherit-skills", "bedrock-context", "effort-thinking", "research-tools"]
MATT_RENDER_STUBS_EDITED = ["agent-inherit-skills", "bedrock-context-edited", "effort-thinking",
                            "research-tools"]


IDENTITY_POLICY = Path("tools/gate/allowed-commit-identities.json")
FIXTURE_NAME, FIXTURE_EMAIL = json.loads((ROOT / IDENTITY_POLICY).read_text(encoding="utf-8"))[0]
ENV = {
    "GIT_AUTHOR_NAME": FIXTURE_NAME,
    "GIT_AUTHOR_EMAIL": FIXTURE_EMAIL,
    "GIT_COMMITTER_NAME": FIXTURE_NAME,
    "GIT_COMMITTER_EMAIL": FIXTURE_EMAIL,
    "GIT_CONFIG_GLOBAL": "/dev/null",
    "GIT_CONFIG_SYSTEM": "/dev/null",
    # The fixtures DECLARE their identity in the config, because the gate's integrity
    # set reads `git config user.name`/`user.email` and refuses an unset one -- the
    # refusal that replaced a `@localhost` fallback which authored published patch
    # branches. `GIT_AUTHOR_*` above is not enough: it is what a commit uses, not what
    # `git config` answers.
    "GIT_CONFIG_COUNT": "2",
    "GIT_CONFIG_KEY_0": "user.name", "GIT_CONFIG_VALUE_0": FIXTURE_NAME,
    "GIT_CONFIG_KEY_1": "user.email", "GIT_CONFIG_VALUE_1": FIXTURE_EMAIL,
}


# Variables that pick the repository a git command acts on. Inherited from a hook or a
# harness, they aim every fixture `git add -A` / `git commit -m initial` at the REAL repository
# (6f2d9d938 committed a lane's dirty working tree as `fork-check test` that way), so they are
# removed from this process and from every fixture git command.
REPO_SELECTING = ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
                  "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE",
                  "GIT_PREFIX", "GIT_CEILING_DIRECTORIES")


def fixture_env(cwd, env=None):
    """The environment for a fixture git command: no inherited repository selection, and a
    ceiling at the fixture's parent so git cannot walk up into an enclosing repository."""
    merged = dict(env) if env else {**os.environ, **ENV}
    for key in REPO_SELECTING:
        merged.pop(key, None)
    merged["GIT_CEILING_DIRECTORIES"] = str(Path(cwd).resolve().parent)
    return merged


def sh(cwd, *args, check=True, env=None):
    result = subprocess.run(
        args, cwd=str(cwd), capture_output=True, text=True, env=fixture_env(cwd, env)
    )
    if check and result.returncode != 0:
        raise AssertionError(f"{' '.join(args)} failed in {cwd}:\n{result.stdout}{result.stderr}")
    return result


def require_own_repo(path):
    """Refuse a fixture directory that is not itself a repository root, before any add."""
    top = sh(path, "git", "rev-parse", "--show-toplevel", check=False)
    assert top.returncode == 0 and Path(top.stdout.strip()).resolve() == Path(path).resolve(), (
        f"fixture {path} is not its own repository (toplevel: {top.stdout.strip()!r}{top.stderr.strip()})")


def git_init(path, *args):
    """`git init` a fixture and prove it is its own repository before anything is committed."""
    sh(path, "git", "init", "--quiet", *args)
    require_own_repo(path)


def commit(cwd, message):
    sh(cwd, "git", "add", "-A")
    sh(cwd, "git", "commit", "--quiet", "-m", message)


def upstream(base: Path, name: str):
    """A real repository the fingerprint is read from."""
    path = base / name
    path.mkdir()
    git_init(path, "--initial-branch=main")
    (path / "src").mkdir()
    (path / "src" / "index.ts").write_text("export const x = 1;\n")
    commit(path, "first")
    return path


def copy_identity_check(repo):
    script = Path("tests/git-identity.test.py")
    shutil.copy(ROOT / script, repo / script)
    (repo / IDENTITY_POLICY).parent.mkdir(parents=True, exist_ok=True)
    shutil.copy(ROOT / IDENTITY_POLICY, repo / IDENTITY_POLICY)
    # The policy lives under tools/gate/, a path the gate-runner step watches, so a full
    # run in this fixture now plans that step. Its two Python suites are stubbed like the
    # fixture's other suites: the step runs, and the real suites are not this fixture's.
    for suite in ("change-gate.test.py", "runner-first.test.py"):
        stub = repo / "tools/gate" / suite
        if not stub.exists():
            stub.write_text("")


# The stand-in for the file named by the fixture quarantine, and its git blob id:
# the hash of `blob <size>`, a NUL and the bytes, which is what `git hash-object` prints.
QUARANTINE_FIXTURE_PATH = "test/unit/herdr-native-placement.test.ts"
QUARANTINE_FIXTURE_CONTENT = b"// the fixture's copy of the quarantined test file\n"
QUARANTINE_FIXTURE_BLOB = hashlib.sha1(
    b"blob %d\0" % len(QUARANTINE_FIXTURE_CONTENT) + QUARANTINE_FIXTURE_CONTENT).hexdigest()


def quarantine_fixture_file(checkout: Path, content: bytes = QUARANTINE_FIXTURE_CONTENT) -> Path:
    """Write the quarantined test file into a candidate checkout, and return its path."""
    path = checkout / QUARANTINE_FIXTURE_PATH
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(content)
    return path


QUARANTINE_FIXTURE_CASE = "leaves the remote operator's APPEND_SYSTEM.md out of the child's prompt"
QUARANTINE_FIXTURE_ARGS = {
    "check": "fork pi-subagents: npm run test:unit",
    "path": QUARANTINE_FIXTURE_PATH,
    "cases": frozenset({
        QUARANTINE_FIXTURE_CASE,
        "preserves canonical contact_supervisor progress and structured-interview semantics",
    }),
    "item": "quarantine/herdr-native-placement",
    "blob": QUARANTINE_FIXTURE_BLOB,
    "reason": "Fixture quarantine for an upstream failure",
    "classification": "upstream",
}


def build_repo(base: Path, *, quarantine=False):
    releases = upstream(base, "pi-subagents")   # follows its newest release tag
    heads = upstream(base, "pi-background-tasks")  # says follow: head

    repo = base / "repo"
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents").mkdir(parents=True)
    (repo / "yolo-packs" / "matt" / "patches" / "pi-background-tasks").mkdir(parents=True)
    for series in ("pi-subagents", "pi-background-tasks"):
        (repo / "yolo-packs" / "matt" / "patches" / series / "0001-x.patch").write_text(
            "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nx\n"
        )
    (repo / "bin").mkdir()
    (repo / "tests").mkdir()
    (repo / "tools").mkdir()
    gate_source = SCRIPT.read_text()
    if quarantine:
        # Install the declaration only in the report fixture's subprocess.
        entry_point = '\nif __name__ == "__main__":'
        assert gate_source.count(entry_point) == 1
        gate_source = gate_source.replace(
            entry_point,
            f"\nQUARANTINED_FORK_QUALITY = (Quarantine(**{QUARANTINE_FIXTURE_ARGS!r}),)\n"
            + entry_point,
        )
    (repo / "tools" / "fork-check.py").write_text(gate_source)
    (repo / "tools" / "gate").mkdir(exist_ok=True)
    shutil.copy(ROOT / "tools/gate/classify.py", repo / "tools/gate/classify.py")
    shutil.copymode(SCRIPT, repo / "tools" / "fork-check.py")

    (repo / "yolo-packs" / "matt" / "pack.json").write_text(json.dumps({
        "name": "matt",
        "contributes": [
            {"kind": "files", "into": ".pi/agent/yolo-patched/pi-subagents",
             "source": f"git+{releases}?ref=main", "patches": "patches/pi-subagents"},
            {"kind": "files", "into": ".pi/agent/yolo-patched/pi-background-tasks",
             "source": f"git+{heads}?ref=main", "follow": "head",
             "patches": "patches/pi-background-tasks"},
        ],
    }, indent=2) + "\n")
    (repo / "bin" / "pi-forks.list").write_text(
        "# comment\n"
        "nicobailon/pi-subagents          yolo-packs/matt/patches/pi-subagents\n"
        "Jawfish/pi-background-tasks      yolo-packs/matt/patches/pi-background-tasks\n"
    )
    (repo / "tests" / "pi-patch-series.test.sh").write_text("#!/bin/sh\necho 'ok   stub structural'\n")
    # The muse backend adapters' suite. A change under config/tillr/ plans it;
    # the fixture carries a stand-in that records being run.
    (repo / "tests" / "tillr-muse-backend.test.py").write_text(
        "from pathlib import Path\n"
        f"Path({str(base / 'tillr-backends-calls')!r}).open('a').write('backend\\n')\n"
    )
    # Copy the real check and its policy. The fixture identity is allowed by the policy
    # and declared in command-scope config, which the check still requires.
    copy_identity_check(repo)
    # The pi-forks driver's own suite. The fixture's series are stubs, so nothing here can
    # use the real one; the stub records that the step ran, and a case that makes it fail
    # proves a failing case is a verdict about the change.
    (repo / "tests" / "pi-forks.test.sh").write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$0" >> "{base}/pi-forks-suite-calls"\n'
        "echo 'ok   stub pi-forks driver'\n"
    )
    storybook = repo / "tools" / "roster-storybook"
    storybook.mkdir()
    (storybook / "render.mts").write_text("// frames\n")
    (storybook / "run.sh").write_text(
        f'#!/bin/sh\nprintf "%s\\n" "$*" >> "{base}/storybook-calls"\nexit 0\n'
    )
    footer = repo / "tools" / "footer-storybook"
    footer.mkdir()
    (footer / "render.mts").write_text("// footer frames\n")
    (footer / "run.sh").write_text(
        f'#!/bin/sh\nprintf "%s\\n" "$*" >> "{base}/footer-storybook-calls"\nexit 0\n'
    )
    (repo / "tests" / "footer-storybook.test.sh").write_text("#!/bin/sh\necho 'ok   stub footer'\n")
    # The meta-muse surfaces: a pack that stages no deployable files, and the two tests that
    # moved to this repository's tests/ dir. Each stub records that the gate ran it.
    (repo / "yolo-packs" / "meta-muse").mkdir(parents=True)
    (repo / "yolo-packs" / "meta-muse" / "pack.json").write_text(
        json.dumps({"name": "meta-muse", "contributes": []}, indent=2) + "\n"
    )
    for label in ("settings", "wonder-pi-models"):
        (repo / "tests" / f"meta-muse-{label}.test.mjs").write_text(
            "import { appendFileSync } from 'node:fs';\n"
            f"appendFileSync('{base}/meta-muse-calls', '{label}\\n');\n"
        )
    # The seat-profile encoding: a pack that ships one data file, and the one test that pins
    # it. The stub records that the gate ran it, so a case proves the step executes.
    (repo / "yolo-packs" / "seat-profiles" / "files").mkdir(parents=True)
    (repo / "yolo-packs" / "seat-profiles" / "pack.json").write_text(
        json.dumps({"name": "seat-profiles", "contributes": []}, indent=2) + "\n"
    )
    (repo / "yolo-packs" / "seat-profiles" / "files" / "seat-profiles.json").write_text(
        '{"schema": 1}\n'
    )
    (repo / "tests" / "seat-profiles.test.mjs").write_text(
        "import { appendFileSync } from 'node:fs';\n"
        f"appendFileSync('{base}/seat-profiles-calls', 'pin\\n');\n"
    )
    # tests/yolo-config.test.sh is the repository's only assertion on the yolo config
    # layers. The stub records that the gate ran it, so a case proves it executes.
    (repo / "tests" / "yolo-config.test.sh").write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$0" >> "{base}/yolo-config-calls"\n'
        "echo 'ok   stub yolo config'\n"
    )
    # The matt pack's four render/overlay tests. Each stub records that the gate ran it, so a
    # case proves the step executes rather than merely being listed.
    (repo / "yolo-packs" / "matt" / "tests").mkdir(parents=True)
    (repo / "yolo-packs" / "matt" / "files" / "extensions").mkdir(parents=True)
    (repo / "yolo-packs" / "matt" / "files" / "extensions" / "bedrock-context.ts").write_text(
        "export const x = 1;\n"
    )
    for label in MATT_RENDER_STUBS:
        (repo / "yolo-packs" / "matt" / "tests" / f"{label}.test.mjs").write_text(
            "import { appendFileSync } from 'node:fs';\n"
            f"appendFileSync('{base}/matt-render-calls', '{label}\\n');\n"
        )
    go_tool = repo / "tools" / "tillr-land"
    go_tool.mkdir(parents=True, exist_ok=True)
    (go_tool / "go.mod").write_text("module tillr-land\n\ngo 1.21\n")
    (go_tool / "main.go").write_text("package main\n\nfunc main() {}\n")
    # The reload-residue audit. A case that edits an extension source, the pack manifest that
    # stages it, or the registry document plans that step, so the fixture carries a stand-in
    # that records being run: a missing file would read as a failing step rather than as the
    # gate doing what it was asked.
    (repo / "tests" / "extension-residue.test.py").write_text(
        "from pathlib import Path\n"
        f"Path({str(base / 'extension-residue-calls')!r}).open('a').write('residue\\n')\n"
    )
    # The active-steering skill and the one test that pins its text. A case that edits the
    # skill plans that step, so the fixture carries both the file a case can edit and a
    # stand-in that records being run: a missing file would read as a failing step rather
    # than as the gate doing what it was asked.
    (repo / "yolo-packs" / "matt-agentwork" / "skills" / "active-steering").mkdir(parents=True)
    (repo / "yolo-packs" / "matt-agentwork" / "skills" / "active-steering" / "SKILL.md").write_text(
        "# Active steering\n"
    )
    (repo / "tests" / "active-steering-skill.test.py").write_text(
        "from pathlib import Path\n"
        f"Path({str(base / 'active-steering-skill-calls')!r}).open('a').write('skill\\n')\n"
    )
    # The pipeline-evidence conformance suite. A case that edits the gate's own record
    # builder, or the format itself, plans that
    # step, so the fixture carries a stand-in that records being run: a missing file would
    # read as a failing step rather than as the gate doing what it was asked.
    (repo / "tests" / "pipeline-evidence.test.py").write_text(
        "from pathlib import Path\n"
        f"Path({str(base / 'pipeline-evidence-calls')!r}).open('a').write('pipeline-evidence\\n')\n"
    )
    # The driver stand-in records its argv, and fails a chosen call when a case asks it
    # to: the gate's fork steps run through `bin/pi-forks`, so a case that proves a
    # failing source check is red has to fail the driver, exactly as a real `npm run test`
    # would make it fail. `pi-forks-fail` names a substring of the call, `pi-forks-rc` the
    # exit status, and `pi-forks-out` the transcript the failing call prints -- a suite's
    # own words are what the gate reads a quarantined step out of, so a case that pins a
    # quarantine has to hand it one.
    forks = repo / "bin" / "pi-forks"
    # Each call records its arguments and the tree the gate told it to read the series
    # from: the gate pins that tree rather than inherit one, so the record says which
    # tree every fork step in this suite verified.
    forks.write_text(
        "#!/bin/sh\n"
        # The candidate preflight is answered BEFORE the call log, so a case that pins
        # the fork steps' calls is not changed by a step the gate runs first. By default
        # the stand-in says the candidate is current; a case that wants the real
        # preflight writes `pi-forks-check-real` and the stand-in execs the real driver
        # copied beside it (board item 194).
        'case "$*" in\n'
        '  "candidate --check"*)\n'
        f'    if [ -f "{base}/pi-forks-check-real" ]; then exec "{base}/pi-forks-real" "$@"; fi\n'
        '    exit 0 ;;\n'
        'esac\n'
        f'printf "%s\\n" "$*" >> "{base}/pi-forks-calls"\n'
        f'printf "%s\\n" "$PI_FORKS_SERIES_ROOT" >> "{base}/pi-forks-series-roots"\n'
        # The bridge's own resolution of the upstream a launch would build. A case that
        # wants the gate to read one writes `target-lines`; without the file the stand-in
        # answers nothing, which is what a machine with no checkouts looks like.
        'case "$*" in\n'
        f'  *"patch-branch target"*) if [ -f "{base}/target-lines" ]; then cat "{base}/target-lines"; fi ;;\n'
        'esac\n'
        f'if [ -f "{base}/pi-forks-fail" ]; then\n'
        f'  want="$(cat "{base}/pi-forks-fail")"\n'
        '  case "$*" in\n'
        f'    *"$want"*) if [ -f "{base}/pi-forks-out" ]; then cat "{base}/pi-forks-out"; fi;\n'
        f'               exit "$(cat "{base}/pi-forks-rc" 2>/dev/null || echo 1)" ;;\n'
        '  esac\n'
        'fi\n'
        'exit 0\n'
    )
    forks.chmod(0o755)
    # Both forks' working copies exist, the way a main checkout that ran every setup
    # looks: a planned fork step without its checkout refuses the run, so a first run
    # over this fixture must verify rather than refuse. The checkouts stay untracked
    # exactly as the real `.forks/` is, including the file the rank-order suite imports
    # and that suite itself as a no-op stub (the block that exercises the step overwrites
    # both with the recording versions).
    (repo / ".forks" / "pi-subagents").mkdir(parents=True)
    (repo / ".forks" / "pi-background-tasks").mkdir(parents=True)
    (repo / ".gitignore").write_text(".forks/\n")
    queue_rank = repo / ".forks" / "pi-subagents" / "src" / "runs" / "background"
    queue_rank.mkdir(parents=True)
    (queue_rank / "queue-rank.ts").write_text("export const compareQueueEntries = () => 0;\n")
    (repo / "yolo-packs" / "matt" / "tests" / "pi-subagents-rank-order.test.mjs").write_text(
        "// no-op stub: the rank-order block replaces it with the recording suite.\n"
    )
    (repo / "docs").mkdir()
    (repo / "docs" / "guide.md").write_text("# Guide\n\nA document.\n")
    (repo / "README.md").write_text("# Synthetic fork-check repository\n")
    (repo / "config" / "tillr").mkdir(parents=True)
    git_init(repo, "--initial-branch=main")
    commit(repo, "initial")
    return repo, releases, heads


def yolo_version_full(sha: str, stamp: str = "v0.14.0-428-g772997837bbd",
                      commit: str = "772997837bbd") -> str:
    """What `yolo --version --full` prints, with a chosen binary sha256."""
    return (
        f"yolo-jail 0.14.0+428.g{commit}\n"
        f"build stamp: {stamp}\n"
        f"commit: {commit}\n"
        f"binary sha256: {sha}\n"
    )


def build_stubs(base: Path):
    """A `yolo` on PATH that records its arguments and answers from files.

    The identity probe (`--version`) is answered from `yolo-identity` and recorded on
    its own `yolo-version-calls`, so a case can assert that the gate read the installed
    build without every existing `yolo-calls` expectation changing.
    """
    stubs = base / "stubs"
    stubs.mkdir()
    # Routing fixtures preserve their Go stub's result through the shard command.
    python = stubs / "python3"
    python.write_text(
        '#!/bin/sh\nif [ "$1" = "tools/go-test-shards.py" ]; then\n'
        '  if [ "$2" = "--edge-digest" ]; then exit 0; fi\n'
        '  exec go test -count=1 ./...\nfi\n'
        f'exec {shlex.quote(sys.executable)} "$@"\n'
    )
    python.chmod(0o755)
    # A stand-in for the installed Pi's package root. The gate resolves one and hands it to
    # every node step that drives the installed Pi, so a plan made here must find one -- in
    # the subprocess environment below and in this process, because `build_plan` reads
    # `os.environ` while `run` gets the dict.
    pi_package = base / PI_PACKAGE_FIXTURE
    (pi_package / "dist").mkdir(parents=True)
    (pi_package / "dist" / "index.js").write_text("export {};\n")
    # pi-tui is a SIBLING of the coding agent's package, which is how the suites import
    # it; a root without one is not a usable environment (see the case below).
    (base / "pi-tui" / "dist").mkdir(parents=True)
    (base / "pi-tui" / "dist" / "index.js").write_text("export {};\n")
    os.environ[PI_PACKAGE_NAME] = str(pi_package)
    yolo = stubs / "yolo"
    yolo.write_text(
        "#!/bin/sh\n"
        'if [ "$1" = "--version" ]; then\n'
        f'  printf "%s\\n" "$*" >> "{base}/yolo-version-calls"\n'
        f'  if [ -f "{base}/yolo-identity" ]; then cat "{base}/yolo-identity"; fi\n'
        "  exit 0\n"
        "fi\n"
        f'printf "%s\\n" "$*" >> "{base}/yolo-calls"\n'
        f'if [ -f "{base}/yolo-out" ]; then cat "{base}/yolo-out"; fi\n'
        f'if [ -f "{base}/yolo-rc" ]; then exit "$(cat "{base}/yolo-rc")"; fi\n'
        "exit 0\n"
    )
    yolo.chmod(0o755)
    uvx = stubs / "uvx"
    uvx.write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$*" >> "{base}/uvx-calls"\n'
        f'if [ -f "{base}/uvx-out" ]; then cat "{base}/uvx-out"; fi\n'
        f'if [ -f "{base}/uvx-err" ]; then cat "{base}/uvx-err" >&2; fi\n'
        f'if [ -f "{base}/uvx-rc" ]; then exit "$(cat "{base}/uvx-rc")"; fi\n'
        "exit 0\n"
    )
    uvx.chmod(0o755)
    # The runtime half of the verdict cache's key (board item 398). `npm --version` is the
    # only call the gate makes here; a case that changes the runtime writes `npm-version`.
    # The fixture's `bin/pi-forks` stand-in never runs npm, so shadowing the real one is
    # safe, and it keeps the key hermetic across machines.
    npm = stubs / "npm"
    npm.write_text(
        "#!/bin/sh\n"
        'if [ "$1" = "--version" ]; then\n'
        f'  if [ -f "{base}/npm-version" ]; then cat "{base}/npm-version"; else echo "10.9.0"; fi\n'
        "  exit 0\n"
        "fi\n"
        f'printf "%s\\n" "$*" >> "{base}/npm-calls"\n'
        "exit 0\n"
    )
    npm.chmod(0o755)
    # The suite is hermetic: a PI_FORKS_ROOT exported by whoever runs it -- `just done` in a
    # lane worktree exports one, and the gate's own audit step runs this file inside that
    # run -- must not steer the fixture's checkouts, and a landing's FORK_CHECK_PLAN_BASE
    # names a commit the fixture history lacks. Every case that wants either names it.
    inherited = {key: value for key, value in os.environ.items()
                 if key not in ("PI_FORKS_ROOT", "FORK_CHECK_PLAN_BASE",
                                "TILLR_GATE_SPANS_OUT")}
    return {**inherited, **ENV, "PATH": f"{stubs}:{os.environ['PATH']}"}


def run(repo: Path, env, *args):
    """One fixture gate run, one check at a time.

    This suite pins ROUTING: which steps a change plans, what each one runs, and the order
    their calls reach the stubs. The stubs append to one log per command, so two steps
    running at the same time would make that log's order a race -- and the order is the
    evidence most cases here assert. The gate's concurrency has its own cases (`write_conflict`
    and `run_checks`, plus a fixture run with an explicit `--jobs`), so pinning serial order
    here costs no coverage of the scheduler itself.
    """
    return subprocess.run(
        [sys.executable, str(repo / "tools" / "fork-check.py"), "--jobs", "1", *args],
        cwd=str(repo), capture_output=True, text=True, env=env,
    )


def run_parallel_gates(lanes, env):
    """Run the fixture gates together, with a private output file for each stream.

    Waiting on one gate must not stop another from writing. With serial communicate()
    on pipes, a gate that filled its pipe could get stuck holding the old shared record
    lock while we waited for the other gate to acquire it. Files remove the backpressure
    independently of which gate starts, finishes or gets collected first.
    """
    with contextlib.ExitStack() as stack:
        running = {}
        for name, path in lanes.items():
            stdout = stack.enter_context(tempfile.TemporaryFile(mode="w+t"))
            stderr = stack.enter_context(tempfile.TemporaryFile(mode="w+t"))
            proc = stack.enter_context(subprocess.Popen(
                [sys.executable, str(path / "tools" / "fork-check.py"), "--jobs", "1"],
                cwd=path, stdout=stdout, stderr=stderr, env=env,
            ))
            running[name] = proc, stdout, stderr
        finished = {}
        for name, (proc, stdout, stderr) in running.items():
            code = proc.wait()
            stdout.seek(0)
            stderr.seek(0)
            finished[name] = code, stdout.read(), stderr.read()
        return finished


# A concurrent fixture gate must be able to finish even when its output is collected
# after another gate's. Check the file descriptors in the children rather than trying
# to recreate a deadlock using a timeout or assuming a particular kernel pipe capacity.
if _RUN_PREAMBLE_TESTS:
    with tempfile.TemporaryDirectory() as tmp:
        lanes = {}
        for name in ("a", "b"):
            lane = lanes[name] = Path(tmp) / name
            (lane / "tools").mkdir(parents=True)
            (lane / "tools/fork-check.py").write_text(
                "import os, stat, sys\n"
                "assert all(stat.S_ISREG(os.fstat(fd).st_mode) for fd in (1, 2)), 'output can back up in a pipe'\n"
                f"print({name!r} * (1024 * 1024))\n"
                f"print({name.upper()!r} * (1024 * 1024), file=sys.stderr)\n"
            )
        for name, (code, out, err) in run_parallel_gates(lanes, os.environ).items():
            assert code == 0, err
            assert out == name * (1024 * 1024) + "\n", name
            assert err == name.upper() * (1024 * 1024) + "\n", name
        print("fork-check fixture output: both concurrent gates can finish before capture is read")


def waybar_fixture(repo):
    (repo / "config/waybar").mkdir(parents=True, exist_ok=True)
    (repo / "tests/waybar-ai-modules.test.sh").write_text("#!/bin/sh\necho ok fixture\n")
    (repo / "tests/test_waybar_cpu.py").write_text(
        "import unittest\nclass Fixture(unittest.TestCase):\n def test_cpu(self): pass\n")


def calls(base: Path, name: str):
    path = base / name
    return path.read_text().splitlines() if path.exists() else []


def clear(base: Path, *names: str):
    for name in names:
        (base / name).unlink(missing_ok=True)


# The record is an append-only JSONL log, so a case reads it the way a reader does: fold
# the lines. The module is loaded once here, because the helpers below run before the case
# blocks that load their own copies.
_RECORD_TOOL = load_tool("fork_check_test_record")


def record_log(repo: Path) -> Path:
    return _RECORD_TOOL.record_log_path(repo / ".git")


def record(repo: Path) -> dict:
    """The record a reader sees: the log folded."""
    return _RECORD_TOOL.Record(record_log(repo)).data


def write_record(repo: Path, state: dict) -> None:
    """Replace the log with one snapshot holding `state`.

    A case that rewrites the record is seeding a state, not appending a run, so a
    snapshot is the honest line: it says "this is the record" rather than "one more run".
    """
    path = record_log(repo)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"schema": 1, "kind": "snapshot",
                                "at": "2026-11-15T00:00:00Z", "state": state}) + "\n")


# Regression for 6f2d9d938: a run that inherits GIT_DIR/GIT_WORK_TREE (a hook or a harness
# exporting them) pointed every fixture `git add -A` and `git commit -m initial` at the real
# repository and committed its dirty working tree as `fork-check test`. The fixture helpers
# must neither touch an enclosing repository nor accept a fixture that is not its own
# repository. The outer repository here has a dirty tracked file and the variables aimed at it.
if _RUN_PREAMBLE_TESTS:
    with tempfile.TemporaryDirectory() as tmp:
        outer = Path(tmp) / "outer"
        outer.mkdir()
        git_init(outer, "--initial-branch=main")
        (outer / "tracked.txt").write_text("one\n")
        commit(outer, "outer")
        (outer / "tracked.txt").write_text("dirty\n")
        outer_head = sh(outer, "git", "rev-parse", "HEAD").stdout
        outer_index = sh(outer, "git", "ls-files", "-s").stdout
        saved = {k: os.environ.get(k) for k in ("GIT_DIR", "GIT_WORK_TREE")}
        os.environ["GIT_DIR"] = str(outer / ".git")
        os.environ["GIT_WORK_TREE"] = str(outer)
        (outer / "fixtures").mkdir()
        try:
            inner = upstream(outer / "fixtures", "fixture")
        finally:
            for k, v in saved.items():
                if v is None:
                    os.environ.pop(k, None)
                else:
                    os.environ[k] = v
        assert sh(outer, "git", "rev-parse", "HEAD").stdout == outer_head, "fixture moved the outer HEAD"
        assert sh(outer, "git", "ls-files", "-s").stdout == outer_index, "fixture changed the outer index"
        assert sh(outer, "git", "log", "-1", "--format=%s").stdout.strip() == "outer"
        assert sh(inner, "git", "rev-parse", "--show-toplevel").stdout.strip() == str(inner.resolve())
        # A directory that is not itself a repository is refused before any add or commit.
        (outer / "not-a-repo").mkdir()
        refused = False
        try:
            require_own_repo(outer / "not-a-repo")
        except AssertionError:
            refused = True
        assert refused, "a directory that is not itself a repository must be refused"
    print("fork-check fixture isolation: a throwaway repository cannot commit to the repository around it")

PACK_CHECK = "pack series check yolo-packs/matt"


def _shard_boundary(_name: str, _weight: int = 10) -> None:
    """Marker separating independent top-level case groups for parallel shard runs."""


_CASE_HEADING_RE = re.compile(r"^\s*#\s*(?:──\s*)?(\d+[a-z]?\.\s+\S.*?)(?:\s*─+)?\s*$")
_ACTIVE_SHARD_NAME = ""
_ACTIVE_CASE_NAME: str | None = None
_ACTIVE_CASE_STARTED_NS = 0
_RECORDED_SHARD_TESTS: list[dict] = []


def _self_suite_end_case() -> None:
    global _ACTIVE_CASE_NAME, _ACTIVE_CASE_STARTED_NS
    if _ACTIVE_CASE_NAME is None:
        return
    elapsed_ms = max(0, (time.monotonic_ns() - _ACTIVE_CASE_STARTED_NS) // 1_000_000)
    _RECORDED_SHARD_TESTS.append({
        "name": _ACTIVE_CASE_NAME,
        "shard": _ACTIVE_SHARD_NAME,
        "wall_ms": int(elapsed_ms),
    })
    _ACTIVE_CASE_NAME = None
    _ACTIVE_CASE_STARTED_NS = 0


def _self_suite_begin_case(name: str) -> None:
    global _ACTIVE_CASE_NAME, _ACTIVE_CASE_STARTED_NS
    _self_suite_end_case()
    _ACTIVE_CASE_NAME = name
    _ACTIVE_CASE_STARTED_NS = time.monotonic_ns()


def _write_timing_json_atomic(dest_str: str, payload: dict) -> None:
    if not dest_str:
        return
    dest = Path(dest_str)
    dest.parent.mkdir(parents=True, exist_ok=True)
    tmp = dest.with_name(f".{dest.name}.tmp.{os.getpid()}")
    tmp.write_text(json.dumps(payload, sort_keys=True) + "\n", encoding="utf-8")
    os.replace(tmp, dest)


def _extract_case_label(source_lines: list[str], start_line: int, end_line: int,
                        allow_unnumbered_block: bool = False) -> str | None:
    lo = max(0, start_line - 1)
    hi = max(0, min(len(source_lines), end_line - 1))
    for idx in range(lo, hi):
        match = _CASE_HEADING_RE.match(source_lines[idx])
        if match:
            return " ".join(match.group(1).split())
    if not allow_unnumbered_block or hi <= lo:
        return None
    block_start = hi
    while block_start > lo and source_lines[block_start - 1].lstrip().startswith("#"):
        block_start -= 1
    if block_start == hi:
        return None
    first = source_lines[block_start].lstrip().lstrip("#").strip()
    return " ".join(first.split()) if first else None


def _is_tempdir_with(stmt: ast.stmt) -> bool:
    if not isinstance(stmt, ast.With) or not stmt.body:
        return False
    return any("TemporaryDirectory" in ast.unparse(item.context_expr) for item in stmt.items)


def _instrument_case_stmts(stmts: list[ast.stmt], source_lines: list[str],
                           start_line: int, depth: int = 0) -> list[ast.stmt]:
    out: list[ast.stmt] = []
    prev_line = start_line
    for stmt in stmts:
        is_top_with = depth == 0 and isinstance(stmt, ast.With) and bool(stmt.body)
        has_first_child_heading = False
        if is_top_with:
            body_start = max((item.context_expr.end_lineno or stmt.lineno) for item in stmt.items) + 1
            has_first_child_heading = (
                _extract_case_label(source_lines, body_start, stmt.body[0].lineno) is not None
            )
        label = _extract_case_label(
            source_lines, prev_line, stmt.lineno,
            allow_unnumbered_block=(depth == 0 and _is_tempdir_with(stmt) and not has_first_child_heading),
        )
        if label is not None:
            marker = ast.Expr(value=ast.Call(
                func=ast.Name(id="_self_suite_begin_case", ctx=ast.Load()),
                args=[ast.Constant(value=label)],
                keywords=[],
            ))
            ast.copy_location(marker, stmt)
            ast.fix_missing_locations(marker)
            out.append(marker)
        if is_top_with:
            body_start = max((item.context_expr.end_lineno or stmt.lineno) for item in stmt.items) + 1
            instrumented_body = _instrument_case_stmts(stmt.body, source_lines, body_start, depth + 1)
            cloned = ast.With(
                items=stmt.items,
                body=instrumented_body,
                type_comment=getattr(stmt, "type_comment", None),
            )
            ast.copy_location(cloned, stmt)
            stmt = cloned
        out.append(stmt)
        prev_line = (stmt.end_lineno or stmt.lineno) + 1
    return out


def _collect_shards(path: Path) -> list[tuple[str, int, int, list[ast.stmt]]]:
    source = path.read_text(encoding="utf-8")
    tree = ast.parse(source, filename=str(path))
    shards: list[tuple[str, int, int, list[ast.stmt]]] = []
    cur_name = None
    cur_weight = 10
    cur_start_line = 1
    cur_stmts: list[ast.stmt] = []
    for node in tree.body:
        if (isinstance(node, ast.Expr) and isinstance(node.value, ast.Call)
                and isinstance(node.value.func, ast.Name)
                and node.value.func.id == "_shard_boundary"):
            if cur_name is not None:
                shards.append((cur_name, cur_weight, cur_start_line, cur_stmts))
            cur_name = ast.literal_eval(node.value.args[0])
            cur_weight = ast.literal_eval(node.value.args[1]) if len(node.value.args) > 1 else 10
            cur_start_line = (node.end_lineno or node.lineno) + 1
            cur_stmts = []
        elif cur_name is not None:
            cur_stmts.append(node)
    if cur_name is not None:
        shards.append((cur_name, cur_weight, cur_start_line, cur_stmts))
    return shards


def _dispatch_shards() -> None:
    global _ACTIVE_SHARD_NAME
    path = _SELF_SUITE_SOURCE or Path(__file__).resolve()
    source_lines = path.read_text(encoding="utf-8").splitlines()
    shards = _collect_shards(path)
    if _SELF_SUITE_SHARD is not None:
        shard_name, _weight, start_line, stmts = shards[_SELF_SUITE_SHARD]
        _ACTIVE_SHARD_NAME = shard_name
        instrumented = _instrument_case_stmts(stmts, source_lines, start_line)
        code = compile(ast.Module(body=instrumented, type_ignores=[]), str(path), "exec")
        shard_started_ns = time.monotonic_ns()
        _self_suite_begin_case(f"{shard_name}:setup")
        try:
            exec(code, globals())
        finally:
            _self_suite_end_case()
            shard_wall_ms = int(max(0, (time.monotonic_ns() - shard_started_ns) // 1_000_000))
            tests = [
                t for t in _RECORDED_SHARD_TESTS
                if not (t["name"] == f"{shard_name}:setup" and len(_RECORDED_SHARD_TESTS) > 1 and t["wall_ms"] == 0)
            ]
            if not tests:
                tests = [{"name": shard_name, "shard": shard_name, "wall_ms": shard_wall_ms}]
            _write_timing_json_atomic(_SELF_SUITE_TIMING_OUT, {
                "shards": [{"name": shard_name, "wall_ms": shard_wall_ms}],
                "tests": tests,
            })
        sys.exit(0)
    preamble_wall_ms = int(max(0, (time.monotonic_ns() - _PREAMBLE_STARTED_NS) // 1_000_000))
    default_jobs = min(len(shards), max(8, (os.cpu_count() or 16) // 2))
    jobs = _SELF_SUITE_JOBS if _SELF_SUITE_JOBS is not None else default_jobs
    if (jobs <= 1 and not _SELF_SUITE_TIMING_OUT) or len(shards) <= 1:
        return
    # Run the heaviest shards first so the critical path starts immediately.
    order = sorted(range(len(shards)), key=lambda i: (-shards[i][1], i))
    active: list[subprocess.Popen] = []
    active_lock = threading.Lock()
    stop = threading.Event()
    outputs: dict[int, tuple[int, str, str]] = {}
    shard_spans: dict[int, tuple[int, int]] = {}
    dispatch_started_ns = time.monotonic_ns()
    scratch_parent = os.environ.get("TMPDIR") or os.environ.get("YOLO_DURABLE_DIR")
    with tempfile.TemporaryDirectory(dir=scratch_parent, prefix="self-suite-timing-") as timing_dir:
        timing_root = Path(timing_dir)

        def run_one(index: int) -> tuple[int, int, str, str]:
            if stop.is_set():
                return index, 0, "", ""
            shard_out = timing_root / f"shard-{index}.json"
            started_ns = time.monotonic_ns()
            proc = subprocess.Popen(
                [sys.executable, str(path), "--self-suite-shard", str(index)],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                env={**os.environ, "TILLR_SELF_SUITE_TIMING_OUT": str(shard_out)},
            )
            with active_lock:
                if stop.is_set():
                    proc.terminate()
                active.append(proc)
            out, err = proc.communicate()
            ended_ns = time.monotonic_ns()
            shard_spans[index] = (
                int(max(0, (started_ns - dispatch_started_ns) // 1_000_000)),
                int(max(0, (ended_ns - started_ns) // 1_000_000)),
            )
            with active_lock:
                if proc in active:
                    active.remove(proc)
            return index, proc.returncode, out, err

        failed = None
        workers = max(1, min(jobs, len(shards)))
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
                futures = [pool.submit(run_one, idx) for idx in order]
                for fut in concurrent.futures.as_completed(futures):
                    idx, rc, out, err = fut.result()
                    outputs[idx] = (rc, out, err)
                    if rc != 0 and failed is None:
                        failed = (idx, rc, out, err)
                        stop.set()
                        with active_lock:
                            for proc in list(active):
                                proc.terminate()
        finally:
            if _SELF_SUITE_TIMING_OUT:
                merged_shards: list[dict] = []
                merged_tests: list[dict] = [
                    {"name": "preamble", "shard": "preamble", "wall_ms": preamble_wall_ms}
                ]
                for idx, (shard_name, _w, _l, _s) in enumerate(shards):
                    shard_file = timing_root / f"shard-{idx}.json"
                    span = shard_spans.get(idx)
                    if shard_file.is_file():
                        try:
                            data = json.loads(shard_file.read_text(encoding="utf-8"))
                        except (OSError, ValueError):
                            data = {}
                        shard_entry = (data.get("shards") or [{"name": shard_name}])[0]
                        if span is not None:
                            shard_entry["start_offset_ms"] = span[0]
                            shard_entry.setdefault("wall_ms", span[1])
                        merged_shards.append(shard_entry)
                        merged_tests.extend(data.get("tests") or [])
                    elif span is not None:
                        merged_shards.append({
                            "name": shard_name,
                            "start_offset_ms": span[0],
                            "wall_ms": span[1],
                        })
                _write_timing_json_atomic(_SELF_SUITE_TIMING_OUT, {
                    "preamble_wall_ms": preamble_wall_ms,
                    "shards": merged_shards,
                    "tests": merged_tests,
                })
    if failed is not None:
        _idx, rc, out, err = failed
        if out:
            sys.stdout.write(out)
            sys.stdout.flush()
        if err:
            sys.stderr.write(err)
            sys.stderr.flush()
        sys.exit(rc)
    for idx in range(len(shards)):
        _rc, out, err = outputs[idx]
        if out:
            sys.stdout.write(out)
        if err:
            sys.stderr.write(err)
    sys.stdout.flush()
    sys.stderr.flush()
    sys.exit(0)


def test_self_suite_timing_handoff_and_recording():
    gate = load_tool("fork_check_self_suite_timing")
    scratch_parent = os.environ.get("TMPDIR") or os.environ.get("YOLO_DURABLE_DIR")
    with tempfile.TemporaryDirectory(dir=scratch_parent) as directory:
        root = Path(directory)
        spans = root / "spans.json"
        plan = gate.Plan()
        plan.tier = gate.TIER_LANDING
        plan.measure_landing = True
        self_check = gate.Check(
            "self-suite", "the gate's own audit suite still passes",
            ["self-suite-stub"], "watch", 0,
        )
        other_check = gate.Check("waybar", "waybar", ["other-stub"], "watch", 0)
        plan.checks = [self_check, other_check]
        handoffs = []
        key = gate.SELF_SUITE_TIMING_ENV

        def run_command(_, argv, timeout, env, drop, cancel):
            child_env = gate.environ(env, drop)
            if argv[0] == "self-suite-stub":
                assert key not in drop and key in child_env
                target = Path(child_env[key])
                assert target.read_text() == ""
                handoffs.append(target)
                target.write_text(json.dumps({
                    "preamble_wall_ms": 120,
                    "shards": [{"name": "core-routing", "wall_ms": 450}],
                    "tests": [{"name": "1. A fresh repository", "shard": "core-routing", "wall_ms": 450}],
                }))
                return 0, "ok\n", 0.45
            assert key not in env and key in drop and key not in child_env
            return 0, "ok\n", 0.01

        gate.run_command = run_command
        for check in plan.checks:
            gate.execute_check(check, root, None, measure_landing=True)
        assert len(handoffs) == 1 and not handoffs[0].exists(), handoffs
        gate.write_gate_spans(str(spans), plan, root, 1, "verified", "", "", [], 1, 1, 0.5)
        body = json.loads(spans.read_text())
        by_kind = {row["kind"]: json.loads(row["attributes_json"]) for row in body["check_spans"]}
        assert by_kind["self-suite"]["self_suite"] == {
            "preamble_wall_ms": 120,
            "shards": [{"name": "core-routing", "wall_ms": 450}],
            "tests": [{"name": "1. A fresh repository", "shard": "core-routing", "wall_ms": 450}],
        }, by_kind["self-suite"]
        assert "self_suite" not in by_kind["waybar"], by_kind["waybar"]

        # Verify that running a shard subprocess with TILLR_SELF_SUITE_TIMING_OUT
        # pops the variable from child environments and writes per-shard and per-test wall_ms.
        fixture_suite = root / "synthetic-suite.py"
        fixture_suite.write_text(
            '_shard_boundary("synthetic-shard", 10)\n'
            'with tempfile.TemporaryDirectory() as _d:\n'
            '    # 1. Outer timing env is stripped before cases run\n'
            '    assert "TILLR_SELF_SUITE_TIMING_OUT" not in os.environ\n'
            '    # 2a. Second numbered case inside the with block\n'
            '    assert Path(_d).is_dir()\n',
            encoding="utf-8",
        )
        shard_out = root / "real-shard-timing.json"
        proc = subprocess.run(
            [
                sys.executable, str(Path(__file__).resolve()),
                "--self-suite-shard", "0", "--self-suite-source", str(fixture_suite),
            ],
            cwd=str(ROOT),
            env={**os.environ, key: str(shard_out)},
            capture_output=True, text=True,
        )
        assert proc.returncode == 0, proc.stdout + proc.stderr
        recorded = json.loads(shard_out.read_text())
        assert [s["name"] for s in recorded["shards"]] == ["synthetic-shard"], recorded
        assert [t["name"] for t in recorded["tests"] if not t["name"].endswith(":setup")] == [
            "1. Outer timing env is stripped before cases run",
            "2a. Second numbered case inside the with block",
        ], recorded["tests"]
        for entry in [*recorded["shards"], *recorded["tests"]]:
            assert isinstance(entry["wall_ms"], int) and not isinstance(entry["wall_ms"], bool), entry
            assert str(entry["wall_ms"]).isdigit(), entry


def test_denied_counter_avoids_proc_scan_before_first_sample():
    gate = load_tool("fork_check_denied_fast_start")
    scratch_parent = os.environ.get("TMPDIR") or os.environ.get("YOLO_DURABLE_DIR")
    with tempfile.TemporaryDirectory(dir=scratch_parent) as directory:
        base = Path(directory)
        nonexec = base / "not-exec"
        nonexec.write_text("#!/bin/sh\nexit 0\n")
        with patch.object(gate._start_cpu_sampler, "_probed_denied",
                          OSError(13, "Permission denied"), create=True):
            with patch.object(gate.os, "scandir", side_effect=AssertionError("unexpected full /proc scan")):
                rc, out, _ = gate.run_command(ROOT, [sys.executable, "-c", "print('fast-ok')"], 5)
                assert rc == 0 and "fast-ok" in out, (rc, out)
                rc_missing, _, _ = gate.run_command(ROOT, [str(base / "missing-bin")], 5)
                assert rc_missing == 127, rc_missing
                rc_nonexec, _, _ = gate.run_command(ROOT, [str(nonexec)], 5)
                assert rc_nonexec == 126, rc_nonexec
            warnings = []
            real_sps = gate._start_process_sampler
            with patch.object(gate, "_start_process_sampler",
                              side_effect=lambda err: real_sps(err, base / "missing-proc")), \
                 patch.object(gate, "progress", side_effect=warnings.append):
                rc_unavail, _, _ = gate.run_command(ROOT, ["true"], 5)
            assert rc_unavail == 0, (rc_unavail, warnings)
            assert any("watchdog unavailable" in w and "process accounting" in w for w in warnings), warnings


if _RUN_PREAMBLE_TESTS:
    test_self_suite_timing_handoff_and_recording()
    test_denied_counter_avoids_proc_scan_before_first_sample()

_dispatch_shards()

_shard_boundary("core-routing", 30)

with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    # Set before the stubs are built, so the strip is exercised rather than assumed. The
    # parent directory deliberately does not exist: an inherited value must be removed, not
    # merely pointed somewhere writable, or the fixture writes another run's span file.
    os.environ["TILLR_GATE_SPANS_OUT"] = str(base / "swept-run" / "gate.spans.json")
    env = build_stubs(base)
    # Pinned, because the failure is silent and total: a landing's own batch base reaches
    # this suite's environment, and the fixture repository does not contain that commit.
    # With either strip removed, every case below refuses before it plans anything.
    assert "FORK_CHECK_PLAN_BASE" not in os.environ, os.environ.get("FORK_CHECK_PLAN_BASE")
    assert "FORK_CHECK_PLAN_BASE" not in env, env.get("FORK_CHECK_PLAN_BASE")
    # The same strip, and the reason it is pinned here: case 1 below is the proof. With the
    # value left in `env`, its gate reaches `write_gate_spans`, whose `os.replace` into the
    # missing directory raises and fails the run.
    assert "TILLR_GATE_SPANS_OUT" not in env, env.get("TILLR_GATE_SPANS_OUT")
    del os.environ["TILLR_GATE_SPANS_OUT"]

    # 1. A fresh repository: no baseline, so each series is checked, by its pack,
    #    in one call, and the baseline is recorded. Every affected series also gets
    #    the launch's merge question (INV-2) for each fork whose working copy is here.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "VERIFIED" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    assert calls(base, "pi-forks-calls") == [
        "patch-branch target --fetch",
        "patch-branch merge-check --candidate --fetch --fork pi-background-tasks", "patch-branch merge-check --candidate --fetch --fork pi-subagents",
        "candidate --fork pi-subagents --deps",
        "candidate --fork pi-subagents --prepared -- npm run typecheck",
        "candidate --fork pi-subagents --prepared -- npm run test:unit",
    ], calls(base, "pi-forks-calls")
    # The tree's own `.forks/`: nothing to say about which tree the series came from.
    assert "series are read from" not in result.stdout, result.stdout
    assert "build verification did not run" in result.stdout, result.stdout
    # The report declares what each planned check watches, and that line is a
    # contract rather than a nicety: the board's scoped dispatch barrier reads it to
    # decide which items a red trunk still lets start, so a report that stopped
    # printing it would silently widen every red to the drain-wide block.
    assert "       watches: yolo-packs/matt/" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    targets = record(repo)["targets"]
    assert targets["matt/pi-subagents"]["fingerprint"] == "release:no-version-tag", targets
    assert targets["matt/pi-background-tasks"]["fingerprint"].startswith("head:main:"), targets

    # 1b. Two writers aimed at ONE span file do not collide. The drain exports
    #     TILLR_GATE_SPANS_OUT to every gate child, and the gate's own audit step runs
    #     this suite inside that child, so a nested gate inherits the same path. With a
    #     fixed `<name>.tmp`, the first `os.replace` consumes the file the second was
    #     about to rename and the loser dies with FileNotFoundError on a span file it had
    #     no reason to share -- which reddened the audit step and every item in the
    #     2026-11-15 drain. The barrier forces the interleaving instead of hoping for it:
    #     with a shared temp name both writers reach `os.replace` with one file between
    #     them, so the second raises.
    spans = base / "shared-spans" / "gate.spans.json"
    plan_with_nothing = load_tool("fork_check_spans_plan").Plan()
    barrier = threading.Barrier(2)
    real_write_text = Path.write_text

    def gated_write_text(self, *args, **kwargs):
        written = real_write_text(self, *args, **kwargs)
        if str(self).endswith(".tmp"):
            barrier.wait(timeout=10)
        return written

    failures = []

    def one_span_write():
        try:
            gate = load_tool("fork_check_spans")
            gate.write_gate_spans(str(spans), plan_with_nothing, base, 0, "verified",
                                  "h", "t", [], 1, 1, 0.0)
        except BaseException as error:  # noqa: BLE001 - the point is to report it
            failures.append(error)

    Path.write_text = gated_write_text
    try:
        writers = [threading.Thread(target=one_span_write) for _ in range(2)]
        for writer in writers:
            writer.start()
        for writer in writers:
            writer.join(timeout=30)
    finally:
        Path.write_text = real_write_text
    assert not failures, failures
    assert json.loads(spans.read_text())["summary"]["outcome"] == "verified"
    # Nothing is left behind under a temp name.
    assert sorted(p.name for p in spans.parent.iterdir()) == ["gate.spans.json"], \
        sorted(p.name for p in spans.parent.iterdir())

    # 2. Nothing changed and no upstream moved: there is nothing to ask.
    clear(base, "yolo-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert "gate step(s) did not run (nothing they watch changed)" in result.stdout, result.stdout

    # 3. A release tag on the release-following upstream is caught by ls-remote,
    #    with nothing changed here.
    sh(releases, "git", "tag", "v1.0.0")
    clear(base, "yolo-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    assert "upstream moved: newest release v1.0.0" in result.stdout, result.stdout

    # 4. A commit on the head-following upstream is caught the same way, and a
    #    commit on the release-following one is not: a launch builds its newest
    #    release, which has not moved.
    clear(base, "yolo-calls")
    result = run(repo, env)
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    (releases / "src" / "index.ts").write_text("export const x = 2;\n")
    commit(releases, "unreleased work on main")
    result = run(repo, env)
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert "gate step(s) did not run (nothing they watch changed)" in result.stdout, result.stdout
    (heads / "src" / "index.ts").write_text("export const x = 2;\n")
    commit(heads, "head moves")
    result = run(repo, env)
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    assert "branch main at" in result.stdout, result.stdout

    # 5. A series that changed pulls in the working-copy check and the build. A fork
    #    whose working copy is absent refuses the run: the series can be neither replayed
    #    nor built here, so the report names the missing checkout and the setup command
    #    instead of recording a verdict over steps that never ran -- and the shared
    #    baseline does not advance past the unverified change.
    clear(base, "yolo-calls", "pi-forks-calls")
    verified = record(repo)["baseline"]["commit"]
    shutil.rmtree(repo / ".forks" / "pi-background-tasks")
    (repo / "yolo-packs" / "matt" / "patches" / "pi-background-tasks" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nb\n"
    )
    commit(repo, "patch the series with no working copy")
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "fork-check: REFUSED" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" not in result.stdout, result.stdout
    assert str(repo / ".forks" / "pi-background-tasks") in result.stdout, result.stdout
    assert "bin/pi-forks setup --fork pi-background-tasks" in result.stdout, result.stdout
    assert "PI_FORKS_ROOT" in result.stdout, result.stdout
    assert "fork-check: NOT VERIFIED" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    # The one bridge call is the plan's own drift probe, not a fork step: the run asks
    # which upstream a launch would build before it decides what moved, and refuses
    # before any step that needs the missing checkout runs.
    assert calls(base, "pi-forks-calls") == ["patch-branch target --fetch"], calls(base, "pi-forks-calls")
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "refused", record(repo)["runs"][-1]
    assert str(repo / ".forks" / "pi-background-tasks") in record(repo)["runs"][-1]["missing_needs"], \
        record(repo)["runs"][-1]

    # Providing the checkout restores the verdict: the same HEAD then runs the patch-branch
    # checks, the replay and the build, and the baseline advances past them.
    (repo / ".forks" / "pi-background-tasks").mkdir(parents=True)
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert calls(base, "pi-forks-calls") == [
        "patch-branch target --fetch",
        "patch-branch merge-check --candidate --fetch --fork pi-background-tasks", "patch-branch verify --fork pi-background-tasks",
        "candidate --fork pi-background-tasks", "candidate --fork pi-background-tasks --build",
    ], calls(base, "pi-forks-calls")
    assert "fork pi-background-tasks: the series builds" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip()

    clear(base, "pi-forks-calls")
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\ny\n"
    )
    commit(repo, "patch the series")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    # The patch-branch invariants ask the same working copy first: INV-2 (the launch's merge
    # question) for the affected series, INV-3 (the series is the export) because the
    # series changed. Both run through the same bin/pi-forks the replay and build do.
    assert calls(base, "pi-forks-calls") == [
        "patch-branch target --fetch",
        "patch-branch merge-check --candidate --fetch --fork pi-subagents", "patch-branch verify --fork pi-subagents",
        "candidate --fork pi-subagents", "candidate --fork pi-subagents --build",
        "candidate --fork pi-subagents --deps",
        "candidate --fork pi-subagents --prepared -- npm run typecheck",
        "candidate --fork pi-subagents --prepared -- npm run test:unit",
    ], calls(base, "pi-forks-calls")
    assert "fork pi-subagents: the series builds" in result.stdout, result.stdout

    # A further edit to the same series, with its working copy present, runs the same
    # four calls again.
    clear(base, "pi-forks-calls")
    (repo / "yolo-packs" / "matt" / "patches" / "pi-background-tasks" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nbb\n"
    )
    commit(repo, "the same series with a working copy")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "pi-forks-calls") == [
        "patch-branch target --fetch",
        "patch-branch merge-check --candidate --fetch --fork pi-background-tasks", "patch-branch verify --fork pi-background-tasks",
        "candidate --fork pi-background-tasks", "candidate --fork pi-background-tasks --build",
    ], calls(base, "pi-forks-calls")
    assert "fork pi-background-tasks: the series builds" in result.stdout, result.stdout

    # --no-build keeps the working-copy checks and drops the build, saying so.
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nz\n"
    )
    commit(repo, "patch it again")
    clear(base, "pi-forks-calls")
    result = run(repo, env, "--no-build")
    assert calls(base, "pi-forks-calls") == [
        "patch-branch target --fetch",
        "patch-branch merge-check --candidate --fetch --fork pi-subagents", "patch-branch verify --fork pi-subagents",
        "candidate --fork pi-subagents",
        "candidate --fork pi-subagents --deps",
        "candidate --fork pi-subagents --prepared -- npm run typecheck",
        "candidate --fork pi-subagents --prepared -- npm run test:unit",
    ], calls(base, "pi-forks-calls")
    assert "build verification did not run" in result.stdout, result.stdout
    assert "fork pi-subagents: the series builds" not in result.stdout, result.stdout

    # A change that reaches no series plans no build, and the report still says
    # build verification did not run -- the fork section never falls silent.
    clear(base, "pi-forks-calls")
    (repo / "notes.txt").write_text("unrelated\n")
    commit(repo, "a change that reaches no series")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    # Only the plan's drift probe: no series changed, so no fork step is planned.
    assert calls(base, "pi-forks-calls") == ["patch-branch target --fetch"], calls(base, "pi-forks-calls")
    assert "run  build" not in result.stdout, result.stdout
    assert "build verification did not run" in result.stdout, result.stdout

    # 6. A conflict verdict is a failure, and the baseline does not advance.
    before = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nw\n"
    )
    commit(repo, "a series that conflicts")
    (base / "yolo-out").write_text("extension matt/pi-subagents: upstream v9 does not take the patch series\n")
    (base / "yolo-rc").write_text("1")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "FAILED" in result.stdout and "does not take" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == before, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

    # A check that fails without naming a conflict is "not verified", not a verdict.
    (base / "yolo-out").write_text("could not fetch the upstream\n")
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "NOT VERIFIED" in result.stdout, result.stdout

    # 6a. A record that is PRESENT and disagrees is still a hard failure, even when the
    #     same transcript also declares a series this machine cannot answer for: the
    #     declaration must not swallow a genuine conflict.
    (base / "yolo-out").write_text(
        "yolo pack series check: extension matt/pi-manage-todo-list: the patch branch is not "
        "yet resolved on this machine — run `yolo pack update` on the host or start a launch "
        "to record a current verdict\n"
        "extension matt/pi-subagents: upstream v9 does not take the patch series\n"
    )
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "FAILED" in result.stdout and "does not take" in result.stdout, result.stdout
    # The declaration is still named -- it is what this machine could not answer -- and
    # the check it could not cover still failed the run.
    assert "declared elsewhere -- the patch-branch stamp (matt/pi-manage-todo-list)" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == before, record(repo)["baseline"]

    # 6b. The same absent record on its own is a machine-local input, DECLARED elsewhere in
    #     one line with the command that answers it -- not a verdict about the series and
    #     not a reason to withhold one. The gate reaches VERIFIED and advances the baseline.
    (base / "yolo-out").write_text(
        "yolo pack series check: extension matt/pi-manage-todo-list: the patch branch is not "
        "yet resolved on this machine — run `yolo pack update` on the host or start a launch "
        "to record a current verdict; `yolo pack series check --online <path>` checks a "
        "disposable scratch copy and does not populate the record\n"
    )
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "declared elsewhere -- the patch-branch stamp (matt/pi-manage-todo-list)" in result.stdout, result.stdout
    assert "machine-local patch-branch record is absent here" in result.stdout, result.stdout
    assert "checks with `yolo pack update` on the host" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip(), record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "verified", record(repo)["runs"][-1]

    # 6c. The reading is per PACK, and the matt pack reports seven series in one
    #     transcript. An unresolved patch-branch record for one of them must not
    #     swallow a genuinely broken series beside it: the break line is an
    #     unrecognised shape, so the step falls through to NOT VERIFIED, exit 2, and
    #     the shared baseline stays where it was.
    before = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nb\n"
    )
    commit(repo, "a second series change so the check is planned")
    (base / "yolo-out").write_text(
        "yolo-jail 0.14.0+591.ga866b30b2d0c | linux/x86_64 | in-jail\n"
        "extension matt/pi-manage-todo-list: 4 patches in patches/pi-manage-todo-list "
        "(series 1a2b3c4d), base 5e6f7a8b\n"
        "yolo pack series check: extension matt/pi-manage-todo-list: the patch branch is not "
        "yet resolved on this machine — run `yolo pack update` on the host or start a launch "
        "to record a current verdict; `yolo pack series check --online <path>` checks a "
        "disposable scratch copy and does not populate the record\n"
        "extension matt/pi-automode: 8 patches in patches/pi-automode (series ad44f9fb), "
        "base e69c0203\n"
        "⚠ extension /workspace/yolo-packs/matt/pi-automode: 0002-x.patch does not apply "
        "at the series' base e69c0203 (git am: exit status 128) — re-export the series\n"
    )
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "fork-check: NOT VERIFIED" in result.stdout, result.stdout
    assert "because: the patch branch is not yet resolved" not in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == before, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "unproven", record(repo)["runs"][-1]

    # 7. A dirty path a check reads is refused before anything runs; a dirty path the
    #    checks do not read -- a config file sitting in the tree -- is named and
    #    ignored, so the commit still gets a verdict. The refusal exits 2, the
    #    not-a-verdict channel: nothing ran, so 1 -- "a check failed" -- would be a
    #    red this run never reached. A landing pass reading 1 as the change's own red
    #    is how a candidate got a `gate-refused check=exit:1` for a gate that judged
    #    nothing (item 262).
    clear(base, "yolo-calls", "yolo-rc", "yolo-out")
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text("dirty\n")
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "REFUSED" in result.stdout, result.stdout
    assert "the checks read" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")

    sh(repo, "git", "checkout", "--quiet", "--", ".")
    (repo / "config").mkdir(exist_ok=True)
    (repo / "config" / "personal.jsonc").write_text('{"profile": "or"}\n')
    commit(repo, "add a config file")
    (repo / "config" / "personal.jsonc").write_text('{"profile": "deepseek"}\n')  # dirty, uncommitted
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "ignoring" in result.stdout and "config/personal.jsonc" in result.stdout, result.stdout
    assert "REFUSED" not in result.stdout, result.stdout
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    sh(repo, "git", "checkout", "--quiet", "--", ".")

    # 8. --explain runs nothing, and still says what each planned step reads. The
    #    watches line is a contract, not a nicety: the board's breakage record keys a
    #    failure by the failing check and the paths it watches, so a report that
    #    stopped printing them would leave every breakage a coarse one.
    sh(repo, "git", "checkout", "--quiet", "--", ".")
    clear(base, "yolo-calls", "pi-forks-calls")
    result = run(repo, env, "--explain")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "would run" in result.stdout, result.stdout
    assert "would run  file-size" in result.stdout, result.stdout
    # It runs no check, but the plan still asks the bridge which upstream a launch
    # would build -- one call, and it fetches, so the plan cannot name a stale release.
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "pi-forks-calls") == ["patch-branch target --fetch"], calls(base, "pi-forks-calls")

    # 9. A change that touches no document does not invoke the document check.
    clear(base, "uvx-calls", "yolo-calls", "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")
    assert "fork-check: VERIFIED" in result.stdout, result.stdout

_shard_boundary("storybooks-and-packs", 30)

with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    assert run(repo, env).returncode == 0

    # 13. A change that can change what the roster storybook renders regenerates it
    #     into the durable directory, not the checkout; an unrelated change does not.
    clear(base, "storybook-calls", "yolo-calls", "pi-forks-calls", "uvx-calls", "uvx-rc", "uvx-out", "uvx-err")
    (repo / "tools" / "roster-storybook" / "render.mts").write_text("// frames, edited\n")
    commit(repo, "edit the roster storybook")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  storybook" in result.stdout, result.stdout
    roster_calls = calls(base, "storybook-calls")
    assert roster_calls and roster_calls[0].split()[0].endswith("/roster-storybook"), roster_calls
    # The renderer is handed the working copies `forks_root()` resolved. Left to find
    # `.forks/` beside its own script, it fails in any checkout but the main one -- a lane
    # worktree, or the drain's detached heavy checkout, where it kept every heavy run on
    # 2026-11-17 from proving the trunk.
    forks = repo / ".forks"
    assert f"--source {forks / 'pi-subagents'} --pi-source {forks / 'pi'}" in roster_calls[0], roster_calls
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    clear(base, "storybook-calls")
    # A checkout with no `.forks/` of its own reads the shared one PI_FORKS_ROOT names.
    elsewhere = base / "shared-root"
    result = run(repo, {**env, "PI_FORKS_ROOT": str(elsewhere)}, "--since", "HEAD~1")
    roster_calls = calls(base, "storybook-calls")
    assert roster_calls, result.stdout + result.stderr
    assert f"--source {elsewhere / '.forks' / 'pi-subagents'} --pi-source {elsewhere / '.forks' / 'pi'}" in roster_calls[0], roster_calls
    clear(base, "storybook-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "storybook-calls") == [], calls(base, "storybook-calls")
    assert "no roster or storybook tooling changed" in result.stdout, result.stdout

    # 14. A change that can change what the footer storybook renders -- the footer
    #     tooling, its own repository check, or the pi-archimedes footer source it
    #     imports -- regenerates it into the durable directory, not the checkout; an
    #     unrelated change leaves both storybooks alone.
    clear(base, "footer-storybook-calls")
    (repo / "tools" / "footer-storybook" / "render.mts").write_text("// footer frames, edited\n")
    commit(repo, "edit the footer storybook")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "regenerate the pi-archimedes footer storybook" in result.stdout, result.stdout
    footer_calls = calls(base, "footer-storybook-calls")
    assert footer_calls and footer_calls[0].split()[0].endswith("/footer-storybook"), footer_calls
    assert f"--source {repo / '.forks' / 'pi-archimedes' / 'packages' / 'footer'}" in footer_calls[0], footer_calls
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    clear(base, "footer-storybook-calls")

    (repo / "tests" / "footer-storybook.test.sh").write_text("#!/bin/sh\necho 'ok   stub footer, edited'\n")
    commit(repo, "edit the footer storybook test")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "regenerate the pi-archimedes footer storybook" in result.stdout, result.stdout
    assert calls(base, "footer-storybook-calls"), calls(base, "footer-storybook-calls")
    clear(base, "footer-storybook-calls")

    (repo / "yolo-packs" / "matt" / "patches" / "pi-archimedes").mkdir(parents=True)
    (repo / "yolo-packs" / "matt" / "patches" / "pi-archimedes" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nf\n"
    )
    commit(repo, "patch the pi-archimedes footer series")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "regenerate the pi-archimedes footer storybook" in result.stdout, result.stdout
    assert calls(base, "footer-storybook-calls"), calls(base, "footer-storybook-calls")
    clear(base, "footer-storybook-calls")

    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "footer-storybook-calls") == [], calls(base, "footer-storybook-calls")
    assert "no footer or storybook tooling changed" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" in result.stdout, result.stdout

    # 16. A pack that declares `patch_branch` and no `patches` still deploys a
    #     generated series, and fork-check plans it: the series directory is the one
    #     the contribution's `into` derives (<pack>/patches/<last segment of into>,
    #     the layout docs/pi/patch-branches/design.md section 2 fixes), which is where
    #     the series already sits here. Both series in the pack are then in the plan,
    #     and the patch-branch one keeps its upstream fingerprint in the record.
    sh(repo, "git", "checkout", "--quiet", "--", ".")
    pack_path = repo / "yolo-packs" / "matt" / "pack.json"
    pack_data = json.loads(pack_path.read_text())
    for index, contribution in enumerate(pack_data["contributes"]):
        if contribution.get("into", "").endswith("pi-subagents"):
            pack_data["contributes"][index] = {
                "kind": "files", "into": contribution["into"],
                "source": contribution["source"], "patch_branch": "../..?ref=patch/pi-subagents",
            }
    pack_path.write_text(json.dumps(pack_data, indent=2) + "\n")
    commit(repo, "name the pi-subagents patch branch with patch_branch")
    clear(base, "yolo-calls", "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "pack matt: 2 series" in result.stdout, result.stdout
    assert record(repo)["targets"]["matt/pi-subagents"]["fingerprint"].startswith("release:"), record(repo)["targets"]
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")

    # 17. The two patch-branch invariants' shape: INV-3 for a series that changed, INV-2 for
    #     the affected series, each running the bridge for the fork and watching the
    #     pack's directory and bin/pi-forks -- so a dirty path either check reads
    #     refuses before it runs. argv and watches are not in the report, so this reads
    #     the plan build_plan builds.
    spec = importlib.util.spec_from_file_location("fork_check_under_test", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    planned = module.build_plan(
        repo,
        [module.Series(pack="matt", rel="patches/pi-subagents",
                       source=f"git+{releases}?ref=main", follow=None)],
        module.Record(base / "plan-state.json"),
        ["yolo-packs/matt/patches/pi-subagents/0001-x.patch"], None, True,
    )
    invariants = {check.kind: check for check in planned.checks if check.kind.startswith("patch-branch")}
    assert set(invariants) == {"patch-branch-inv2", "patch-branch-inv3"}, [(c.kind, c.label) for c in planned.checks]
    # --fetch is part of the question: a launch fetches before it resolves, so a verdict
    # taken from whatever this checkout last fetched can bless a release it never builds.
    assert invariants["patch-branch-inv2"].argv[1:] == [
        "patch-branch", "merge-check", "--candidate", "--fetch", "--fork", "pi-subagents"
    ], invariants["patch-branch-inv2"].argv
    assert invariants["patch-branch-inv3"].argv[1:] == ["patch-branch", "verify", "--fork", "pi-subagents"], invariants["patch-branch-inv3"].argv
    expected_watches = ("yolo-packs/matt/", "yolo-packs/fork-pins/pi-subagents", "bin/pi-forks")
    assert invariants["patch-branch-inv2"].watches == expected_watches, invariants["patch-branch-inv2"].watches
    assert invariants["patch-branch-inv3"].watches == expected_watches, invariants["patch-branch-inv3"].watches

    converted = module.Series(pack="matt", rel="", branch="patch/pi-subagents",
                              source=f"git+{releases}?ref=main", follow=None)
    pinned = module.build_plan(repo, [converted], module.Record(base / "pin-state.json"),
                               ["yolo-packs/fork-pins/pi-subagents"], None, True,
                               audit_upstreams=False)
    pin_steps = [check for check in pinned.checks if check.kind in
                 ("patch-branch-inv2", "patch-branch-inv3", "forks-check", "build", "fork-quality")]
    assert pin_steps and all(not check.needs for check in pin_steps), [(c.kind, c.needs) for c in pin_steps]
    assert all("yolo-packs/fork-pins/pi-subagents" in c.watches for c in pin_steps), [(c.kind, c.watches) for c in pin_steps]
    assert all("yolo-packs/fork-pins/pi-subagents" in c.selected_by for c in pin_steps), [(c.kind, c.selected_by) for c in pin_steps]
    prepare = next(c for c in pin_steps if c.kind == "fork-quality" and "--deps" in c.argv)
    assert str(repo / ".forks/inputs/pi-subagents") in prepare.writes, prepare.writes

    # A failure from either check is the bridge's own refusal, and reads as FAILED
    # (a verdict); anything else it could not answer reads as NOT VERIFIED. The stub
    # answers every bin/pi-forks call, so the replay and build fail the same way.
    (repo / "bin" / "pi-forks").write_text(
        '#!/bin/sh\nprintf "%s\\n" "$*" >> "{base}/pi-forks-calls"\n'
        # The npm steps are not what this case is about: they answer the fork's own source,
        # not the bridge's refusal, so they succeed and leave the classification to the
        # patch-branch and candidate calls below. A quality run is a `--deps` prepare step
        # and then `--prepared` readers, so both spellings answer here.
        'case "$*" in *"--deps"*|*"--prepared"*) exit 0 ;; esac\n'
        'if [ -f "{base}/pi-forks-out" ]; then cat "{base}/pi-forks-out"; fi\n'
        'if [ -f "{base}/pi-forks-rc" ]; then exit "$(cat "{base}/pi-forks-rc")"; fi\n'
        'exit 0\n'.format(base=base)
    )
    (repo / "bin" / "pi-forks").chmod(0o755)
    for rc, out, expected in (
        ("1", "   FAILED: main conflicts with refs/tags/v9 (INV-2), the followed release -- the paths:\n", 1),
        ("2", "   FAILED: could not resolve the upstream a launch would build (lag)\n", 2),
    ):
        (base / "pi-forks-rc").write_text(rc)
        (base / "pi-forks-out").write_text(out)
        (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
            f"From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\n{rc}\n"
        )
        commit(repo, f"patch-branch verdict {rc}")
        result = run(repo, env)
        assert result.returncode == expected, (rc, result.stdout + result.stderr)
        assert ("FAILED" if expected == 1 else "NOT VERIFIED") in result.stdout, (rc, result.stdout)
    clear(base, "pi-forks-rc", "pi-forks-out")

    # 18. The two meta-muse tests run in the gate. They were moved out of the pack (which
    #     stages no deployable files, so it cannot keep a tests dir), and a change to the
    #     pack, the personal layer that selects it, or the tests themselves plans the
    #     check; an unrelated change names the skip. Both stubs record that the gate ran
    #     them, so this proves they execute rather than merely being listed.
    sh(repo, "git", "checkout", "--quiet", "--", ".")
    clear(base, "meta-muse-calls", "yolo-calls", "yolo-rc", "yolo-out", "pi-forks-calls",
          "pi-forks-rc", "pi-forks-out", "uvx-calls", "uvx-rc", "uvx-out", "uvx-err",
          "storybook-calls", "footer-storybook-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "no meta-muse surface changed" in result.stdout, result.stdout
    assert calls(base, "meta-muse-calls") == [], calls(base, "meta-muse-calls")

    (repo / "yolo-packs" / "meta-muse" / "pack.json").write_text(
        json.dumps({"name": "meta-muse", "description": "edited by the gate test", "contributes": []}, indent=2) + "\n"
    )
    commit(repo, "edit the meta-muse pack")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  meta-muse" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert sorted(calls(base, "meta-muse-calls")) == ["settings", "wonder-pi-models"], calls(base, "meta-muse-calls")

    # The tests themselves are an input too: editing one plans the check again.
    clear(base, "meta-muse-calls", "yolo-calls", "pi-forks-calls")
    (repo / "tests" / "meta-muse-settings.test.mjs").write_text(
        "import { appendFileSync } from 'node:fs';\n"
        f"appendFileSync('{base}/meta-muse-calls', 'settings-edited\\n');\n"
    )
    commit(repo, "edit a meta-muse test")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  meta-muse" in result.stdout, result.stdout
    assert sorted(calls(base, "meta-muse-calls")) == ["settings-edited", "wonder-pi-models"], calls(base, "meta-muse-calls")

    # 18b. The seat-profile encoding's own test runs in the gate. The encoding is data, so
    #      the only thing that can tell a moved row or a loosened bound from a deliberate one
    #      is this test; a change to the data, the pack, the test, or a yolo layer that names
    #      a set plans the check, and the stub records that the gate ran it.
    sh(repo, "git", "checkout", "--quiet", "--", ".")
    clear(base, "seat-profiles-calls", "yolo-calls", "yolo-rc", "yolo-out", "pi-forks-calls",
          "pi-forks-rc", "pi-forks-out", "uvx-calls", "uvx-rc", "uvx-out", "uvx-err",
          "storybook-calls", "footer-storybook-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "no seat-profile encoding path changed" in result.stdout, result.stdout
    assert calls(base, "seat-profiles-calls") == [], calls(base, "seat-profiles-calls")

    (repo / "yolo-packs" / "seat-profiles" / "files" / "seat-profiles.json").write_text(
        '{"schema": 2}\n'
    )
    commit(repo, "move the seat-profile encoding")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  seat-profiles" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert calls(base, "seat-profiles-calls") == ["pin"], calls(base, "seat-profiles-calls")

    # 18c. The design document is an input too. The test reads its tabled sections back out,
    #      so a row the document moves and the encoding does not is a red -- which only works
    #      if a document-only change plans the check. Without this the two could drift and the
    #      gate would call the result verified.
    sh(repo, "git", "checkout", "--quiet", "--", ".")
    clear(base, "seat-profiles-calls", "uvx-calls", "uvx-rc", "uvx-out", "uvx-err")
    (repo / "docs" / "design").mkdir(parents=True, exist_ok=True)
    (repo / "docs" / "design" / "seat-profiles.md").write_text(
        "# Seat profiles\n\n| Seat | Model | Thinking |\n| :--- | :--- | :--- |\n"
    )
    commit(repo, "move a seat-profile document row")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  seat-profiles" in result.stdout, result.stdout
    assert calls(base, "seat-profiles-calls") == ["pin"], calls(base, "seat-profiles-calls")
    assert "seat-profiles" in result.stdout and "docs/design/seat-profiles.md" in result.stdout, result.stdout

    # 19. tests/yolo-config.test.sh runs in the gate. It is the repository's only assertion
    #     on the yolo config layers -- the work class's `profile` selection among them -- and
    #     nothing else pinned them: a change to a layer, the matt manifest, or the test itself
    #     plans the check; an unrelated change names the skip; a machine without `yolo` or
    #     `python3` names the skip rather than failing; and a failing run is a verdict
    #     (FAILED), not "not verified". The stub records that the gate ran it.
    sh(repo, "git", "checkout", "--quiet", "--", ".")
    clear(base, "yolo-config-calls", "yolo-calls", "yolo-rc", "yolo-out", "pi-forks-calls",
          "pi-forks-rc", "pi-forks-out", "uvx-calls", "uvx-rc", "uvx-out", "uvx-err",
          "meta-muse-calls", "storybook-calls", "footer-storybook-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "no yolo config file changed" in result.stdout, result.stdout
    assert calls(base, "yolo-config-calls") == [], calls(base, "yolo-config-calls")

    work_layer = repo / "tag-work" / "config" / "yolo-jail" / "config.d"
    work_layer.mkdir(parents=True)
    (work_layer / "work.toml").write_text('[active_provider]\ndefault = "bedrock"\n')
    commit(repo, "add the work yolo layer")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  yolo-config" in result.stdout, result.stdout
    assert calls(base, "yolo-config-calls") == ["tests/yolo-config.test.sh"], calls(base, "yolo-config-calls")

    # The test itself is an input too.
    clear(base, "yolo-config-calls")
    (repo / "tests" / "yolo-config.test.sh").write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$0" >> "{base}/yolo-config-calls"\n'
        "echo 'ok   stub yolo config, edited'\n"
    )
    commit(repo, "edit the yolo config test")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  yolo-config" in result.stdout, result.stdout
    assert calls(base, "yolo-config-calls") == ["tests/yolo-config.test.sh"], calls(base, "yolo-config-calls")

    # A failing config assertion is a verdict, so the run FAILS and the baseline does not
    # advance -- not "not verified", which is a check that could not answer the question.
    failing = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "tests" / "yolo-config.test.sh").write_text(
        "#!/bin/sh\necho 'FAIL work.toml runs every agent on bedrock' >&2\nexit 1\n"
    )
    commit(repo, "break the yolo config")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  yolo-config" in result.stdout and "FAILED" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == failing, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

    # A machine without `yolo` (or `python3`) cannot answer it: a named skip, not a failure.
    (repo / "tests" / "yolo-config.test.sh").write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$0" >> "{base}/yolo-config-calls"\n'
        "echo 'ok   stub yolo config, restored'\n"
    )
    commit(repo, "restore the yolo config test")
    saved_path = os.environ["PATH"]
    os.environ["PATH"] = str(base / "no-bin")
    try:
        bare = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"),
            ["tag-work/config/yolo-jail/config.d/work.toml"], None, True,
        )
    finally:
        os.environ["PATH"] = saved_path
    assert not any(check.kind == "yolo-config" for check in bare.checks), [c.kind for c in bare.checks]
    assert any(skip.label.startswith("yolo-config") for skip in bare.skips), bare.skips

    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  yolo-config" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" in result.stdout, result.stdout

    # 20. The matt pack's four render/overlay tests run in the gate. Nothing else asserts
    #     on a work-class host render -- the derive the `bedrock` pack selection keeps
    #     non-empty -- and neither render test was in the gate before: a change to a path they
    #     read (the work layer, the pack manifest or `files/`, or any of the four) plans the
    #     check; an unrelated change names the skip; a machine without `node` or `yolo` names
    #     the skip rather than failing; and a failing run is a verdict (FAILED), not "not
    #     verified". Each stub records that the gate ran it, so this proves all four execute.
    sh(repo, "git", "checkout", "--quiet", "--", ".")
    clear(base, "matt-render-calls", "yolo-calls", "yolo-rc", "yolo-out", "pi-forks-calls",
          "pi-forks-rc", "pi-forks-out", "uvx-calls", "uvx-rc", "uvx-out", "uvx-err",
          "meta-muse-calls", "storybook-calls", "footer-storybook-calls", "yolo-config-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "no work-class render test or layer changed" in result.stdout, result.stdout
    assert calls(base, "matt-render-calls") == [], calls(base, "matt-render-calls")

    # The work layer is an input: restoring the `bedrock` pack selection it needs plans the
    # check, and both tests run against it.
    (repo / "tag-work" / "config" / "yolo-jail" / "config.d" / "work.toml").write_text(
        'packs = ["bedrock"]\n\n[active_provider]\ndefault = "bedrock"\n'
    )
    commit(repo, "select the bedrock pack in the work layer")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  matt-render" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert sorted(calls(base, "matt-render-calls")) == MATT_RENDER_STUBS, calls(base, "matt-render-calls")

    # The tests themselves are an input too: editing one plans the check again.
    clear(base, "matt-render-calls")
    (repo / "yolo-packs" / "matt" / "tests" / "bedrock-context.test.mjs").write_text(
        "import { appendFileSync } from 'node:fs';\n"
        f"appendFileSync('{base}/matt-render-calls', 'bedrock-context-edited\\n');\n"
    )
    commit(repo, "edit a work-class render test")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  matt-render" in result.stdout, result.stdout
    assert sorted(calls(base, "matt-render-calls")) == MATT_RENDER_STUBS_EDITED, calls(base, "matt-render-calls")

    # The pack's files/ is an input: the render the tests read is the pack's own overlays.
    clear(base, "matt-render-calls")
    (repo / "yolo-packs" / "matt" / "files" / "extensions" / "bedrock-context.ts").write_text(
        "export const x = 2;\n"
    )
    commit(repo, "edit a matt pack extension")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  matt-render" in result.stdout, result.stdout
    assert sorted(calls(base, "matt-render-calls")) == MATT_RENDER_STUBS_EDITED, calls(base, "matt-render-calls")

    # A failing render test is a verdict, so the run FAILS and the baseline does not advance.
    failing = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "yolo-packs" / "matt" / "tests" / "effort-thinking.test.mjs").write_text(
        "process.exit(1);\n"
    )
    commit(repo, "break a work-class render test")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  matt-render" in result.stdout and "FAILED" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == failing, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

    # A machine without `node` (or `yolo`) cannot answer it: a named skip, not a failure.
    # The restore writes a distinct marker, so its tree differs from the baseline's and the
    # check is planned again rather than read as an unchanged tree.
    (repo / "yolo-packs" / "matt" / "tests" / "effort-thinking.test.mjs").write_text(
        "import { appendFileSync } from 'node:fs';\n"
        f"appendFileSync('{base}/matt-render-calls', 'effort-thinking-restored\\n');\n"
    )
    commit(repo, "restore the work-class render test")
    saved_path = os.environ["PATH"]
    os.environ["PATH"] = str(base / "no-bin")
    try:
        bare = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"),
            ["yolo-packs/matt/tests/effort-thinking.test.mjs"], None, True,
        )
    finally:
        os.environ["PATH"] = saved_path
    assert not any(check.kind == "matt-render" for check in bare.checks), [c.kind for c in bare.checks]
    assert any(skip.label.startswith("matt-render") for skip in bare.skips), bare.skips

    clear(base, "matt-render-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  matt-render" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" in result.stdout, result.stdout

_shard_boundary("targeted-range", 28)

# 21a. A targeted committed range never inherits an audit's accumulated failures or
# unrelated upstream drift, and never records a repository-wide verified baseline.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    (repo / "config" / "waybar").mkdir(parents=True)
    (repo / "config" / "waybar" / "config").write_text('{"cpu": {"format": "{usage}%"}}\n')
    (repo / "tests" / "waybar-ai-modules.test.sh").write_text(
        f'#!/bin/sh\nprintf "ai\\n" >> "{base}/waybar-calls"\n'
    )
    (repo / "tests" / "test_waybar_cpu.py").write_text(
        f'from pathlib import Path\nimport unittest\n'
        f'class CpuTest(unittest.TestCase):\n'
        f'    def test_cpu(self):\n        Path("{base}/waybar-cpu-ran").touch()\n'
    )
    commit(repo, "Waybar-only change")
    result = run(repo, env, "--since", "HEAD^")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "TARGETED" in result.stdout, result.stdout
    assert "DIAGNOSTIC PASSED (not completion approval)" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" not in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "pi-forks-calls") == [], calls(base, "pi-forks-calls")
    assert calls(base, "waybar-calls") == ["ai"], calls(base, "waybar-calls")
    assert (base / "waybar-cpu-ran").exists()
    assert "baseline" not in record(repo), record(repo)
    assert record(repo)["targets"] == {}, record(repo)
    assert record(repo)["runs"][-1]["scope"] == "targeted", record(repo)

    # No unrelated network probes: a missing upstream must not affect Waybar.
    sh(heads, "git", "tag", "v9.0.0")
    shutil.rmtree(releases)
    result = run(repo, env, "--since", "HEAD^", "--explain")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "would run  series" not in result.stdout, result.stdout
    assert "unreachable" not in result.stdout, result.stdout

    # Invalid bases must fail explicitly, not fall back to the audit baseline.
    before = record(repo)
    for invalid in ("no-such-ref", ""):
        result = run(repo, env, "--since", invalid)
        assert result.returncode != 0, result.stdout + result.stderr
        assert "cannot resolve" in result.stdout + result.stderr, result.stdout + result.stderr
        assert record(repo) == before, record(repo)

    # A dirty input that the targeted check reads still requires commit first, and a
    # targeted run refuses on the same channel as an audit: 2, not a red.
    (repo / "config" / "waybar" / "config").write_text("dirty\n")
    result = run(repo, env, "--since", "HEAD^")
    assert result.returncode == 2, result.stdout + result.stderr
    assert "REFUSED" in result.stdout, result.stdout
    sh(repo, "git", "checkout", "--", "config/waybar/config")

    # --all wins over the recipe's default --since and still asks about drift.
    clear(base, "yolo-calls")
    result = run(repo, env, "--since", "HEAD^", "--all")
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    assert "TARGETED" not in result.stdout, result.stdout

    # A scoped series edit still runs its pack, patch-branch checks, replay and build.
    clear(base, "yolo-calls", "pi-forks-calls")
    patch = repo / "yolo-packs/matt/patches/pi-subagents/0001-x.patch"
    patch.write_text(patch.read_text() + "edit\n")
    commit(repo, "edit a series")
    result = run(repo, env, "--since", "HEAD^")
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    for expected in ("patch-branch merge-check --candidate --fetch --fork pi-subagents", "patch-branch verify --fork pi-subagents",
                     "candidate --fork pi-subagents", "candidate --fork pi-subagents --build"):
        assert expected in calls(base, "pi-forks-calls"), calls(base, "pi-forks-calls")

# A Waybar case that needs a tool this machine does not have is a named skip, never a
# failure: the script ran everything else, says which case it left undecided, and exits
# 77, and the report carries that reason instead of failing a case no change can clear.
# A failing run of the same script still fails, so the skip is not a blanket excuse.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    (repo / "config" / "waybar").mkdir(parents=True)
    (repo / "config" / "waybar" / "config").write_text('{"cpu": {"format": "{usage}%"}}\n')
    script = repo / "tests" / "waybar-ai-modules.test.sh"
    script.write_text(
        '#!/bin/sh\n'
        'echo "skip the Meta vendor matches the installed binary: ai-usagebar is not here"\n'
        'echo "1 case(s) had no verdict here (exit 77)" >&2\n'
        'exit 77\n'
    )
    (repo / "tests" / "test_waybar_cpu.py").write_text(
        f'from pathlib import Path\nimport unittest\n'
        f'class CpuTest(unittest.TestCase):\n'
        f'    def test_cpu(self):\n        Path("{base}/waybar-cpu-ran").touch()\n'
    )
    commit(repo, "Waybar-only change")
    result = run(repo, env, "--since", "HEAD^")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "DIAGNOSTIC PASSED" in result.stdout, result.stdout
    assert "FAIL" not in result.stdout, result.stdout
    assert (base / "waybar-cpu-ran").exists(), "The CPU check must still run"
    assert "skip             waybar (sh tests/waybar-ai-modules.test.sh)" in result.stdout, result.stdout
    assert ("because: the Meta vendor matches the installed binary: "
            "ai-usagebar is not here") in result.stdout, result.stdout
    assert record(repo)["runs"][-1]["outcome"] == "verified", record(repo)["runs"][-1]
    assert not any(c["kind"] == "waybar" and c["rc"] != 0 for c in record(repo)["runs"][-1]["checks"]), \
        record(repo)["runs"][-1]["checks"]
    # The progress stream names the same outcome the report does, as it completes:
    # a check the report calls a skip streams as a skip rather than as a failure.
    assert "fork-check: skip waybar -- Waybar module configuration" in result.stderr, result.stderr
    assert "FAIL waybar" not in result.stderr, result.stderr

    # The same script exiting 1 is a verdict about the change, not a skip.
    (repo / "config" / "waybar" / "config").write_text('{"cpu": {"format": "broken"}}\n')
    script.write_text('#!/bin/sh\necho "FAIL a case" >&2\nexit 1\n')
    commit(repo, "break a Waybar case")
    result = run(repo, env, "--since", "HEAD^")
    assert result.returncode == 1, result.stdout + result.stderr
    assert "FAILED" in result.stdout, result.stdout
    assert "waybar: Waybar module configuration" in result.stdout, result.stdout
    # The ordinary failure is unchanged on both surfaces.
    assert "fork-check: FAIL waybar -- Waybar module configuration" in result.stderr, result.stderr

# A successful targeted run cannot erase a failed full audit or its baseline.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    assert run(repo, env).returncode == 0
    baseline = record(repo)["baseline"]
    targets = record(repo)["targets"]
    waybar_fixture(repo)
    (repo / "config/waybar/config").write_text("broken\n")
    (repo / "tests/waybar-ai-modules.test.sh").write_text("#!/bin/sh\necho FAIL fixture\nexit 1\n")
    commit(repo, "break documentation")
    (base / "uvx-out").write_text("missing link\n")
    (base / "uvx-rc").write_text("1\n")
    assert run(repo, env).returncode == 1
    (repo / "unrelated-config").write_text("small change\n")
    commit(repo, "unrelated small change")
    clear(base, "uvx-calls", "yolo-calls")
    result = run(repo, env, "--since", "HEAD^")
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert record(repo)["baseline"] == baseline, record(repo)
    assert record(repo)["targets"] == targets, record(repo)
    assert "audit" in result.stdout.lower(), result.stdout
    assert "failed" in result.stdout.lower(), result.stdout
    # Even after bounded run history no longer contains an audit, its result remains.
    state = record(repo)
    state["runs"] = [state["runs"][-1]]
    write_record(repo, state)
    result = run(repo, env, "--since", "HEAD^")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "last full audit remains failed" in result.stdout, result.stdout
    assert record(repo)["last_audit"]["outcome"] == "failed", record(repo)
    result = run(repo, env, "--since", "HEAD~2")
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  waybar" in result.stdout, result.stdout

# The real recipe must inherit the approved point, not silently narrow to HEAD^.
# Run its actual default arguments in a fixture with failure from an earlier task.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    recipe = (ROOT / ".justfile").read_text()
    line = next(line for line in recipe.splitlines()
                if line.strip().startswith("@python3 tools/fork-check.py"))
    default_args = shlex.split(line.strip().removeprefix("@"))
    default_args = default_args[2:-1]  # Drop python, script, and {{args}}.

    assert run(repo, env).returncode == 0
    approved = record(repo)["baseline"]
    waybar_fixture(repo)
    (repo / "config/waybar/config").write_text("broken\n")
    (repo / "tests/waybar-ai-modules.test.sh").write_text("#!/bin/sh\necho FAIL fixture\nexit 1\n")
    commit(repo, "earlier task leaves a failed document check")
    (base / "uvx-out").write_text("missing link\n")
    (base / "uvx-rc").write_text("1\n")
    assert run(repo, env).returncode == 1
    (repo / "unrelated-config").write_text("later task\n")
    commit(repo, "later unrelated task")
    clear(base, "uvx-calls")
    result = run(repo, env, *default_args)
    assert result.returncode == 1, "A later commit must not hide the earlier failure:\n" + result.stdout
    assert "run  waybar" in result.stdout, "Default completion must check earlier unapproved documents"
    assert record(repo)["baseline"] == approved, record(repo)

    # Once all pending checks pass, the approved point rolls forward to HEAD.
    clear(base, "uvx-rc", "uvx-out")
    (repo / "tests/waybar-ai-modules.test.sh").write_text("#!/bin/sh\necho ok fixture\n")
    commit(repo, "repair the pending failure")
    result = run(repo, env, *default_args)
    assert result.returncode == 0, result.stdout + result.stderr
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip()

    # Now a small unrelated change inherits that approval, without rechecking docs.
    (repo / "unrelated-config").write_text("another small change\n")
    commit(repo, "small change after approval")
    clear(base, "uvx-calls", "yolo-calls")
    result = run(repo, env, *default_args)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "uvx-calls") == [] and calls(base, "yolo-calls") == [], result.stdout
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip()

_shard_boundary("session-search-quality", 24)

# Building a changed session-search series is not enough: its source must also
# typecheck and pass the tests that isolate discovery from the real Claude home.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    assert run(repo, env).returncode == 0
    approved = record(repo)["baseline"]
    search = upstream(base, "pi-session-search")
    manifest = repo / "yolo-packs/matt/pack.json"
    data = json.loads(manifest.read_text())
    data["contributes"].append({
        "kind": "files", "into": ".pi/agent/yolo-patched/pi-session-search",
        "source": f"git+{search}?ref=main", "patches": "patches/pi-session-search",
    })
    manifest.write_text(json.dumps(data) + "\n")
    patch = repo / "yolo-packs/matt/patches/pi-session-search/0001-x.patch"
    patch.parent.mkdir()
    patch.write_text("From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nx\n")
    checkout = repo / ".forks/pi-session-search"
    checkout.mkdir()
    (checkout / "package.json").write_text('{"scripts":{"check":"tsc","test":"node --test"}}\n')
    candidate = repo / ".forks" / "candidate" / "pi-session-search"
    commit(repo, "add Claude discovery with a failing isolated test")
    (base / "pi-forks-fail").write_text("npm run test")
    (base / "pi-forks-rc").write_text("1")
    result = run(repo, env)
    assert result.returncode == 1, "A successful build must not hide failing session-search tests:\n" + result.stdout
    assert "run  fork-quality" in result.stdout and "FAILED" in result.stdout, result.stdout
    # The source checks read the CANDIDATE checkout, not the shared working tree: the
    # report names that path, and no step is handed `.forks/pi-session-search`'s tree.
    # The failing step runs ONCE, and the typecheck that passed is not repeated either:
    # a failure is triaged from its own output, never re-run.
    assert [call for call in calls(base, "pi-forks-calls")
            if call.startswith("candidate --fork pi-session-search --prepared -- npm")] == [
        "candidate --fork pi-session-search --prepared -- npm run check",
        "candidate --fork pi-session-search --prepared -- npm run test",
    ], calls(base, "pi-forks-calls")
    assert f"npm run test in the clean checkout at {candidate}" in result.stdout, result.stdout
    assert str(candidate) in result.stdout, result.stdout
    assert record(repo)["baseline"] == approved, record(repo)

    # A failed typecheck also leaves approval where it was.
    (base / "pi-forks-fail").write_text("npm run check")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout
    assert record(repo)["baseline"] == approved, record(repo)

    # Missing tooling is explicitly NOT VERIFIED, never a silent skipped pass.
    (base / "pi-forks-rc").write_text("127")
    result = run(repo, env)
    assert result.returncode == 2 and "NOT VERIFIED" in result.stdout, result.stdout
    assert record(repo)["baseline"] == approved, record(repo)

    # A non-applying combined series blocks approval even if its checkout tests pass.
    clear(base, "pi-forks-fail", "pi-forks-rc")
    (base / "yolo-rc").write_text("1")
    (base / "yolo-out").write_text("ERROR: patch application failed at 0002\n")
    result = run(repo, env)
    assert result.returncode != 0, result.stdout
    assert record(repo)["baseline"] == approved, record(repo)

    clear(base, "yolo-rc", "yolo-out", "pi-forks-calls")
    result = run(repo, env, "--no-build")
    assert result.returncode == 0, result.stdout
    quality = [call for call in calls(base, "pi-forks-calls") if call.startswith("candidate --fork pi-session-search")]
    assert quality == [
        "candidate --fork pi-session-search",
        # --no-build plans no build, so the quality run gets a prepare step of its own.
        "candidate --fork pi-session-search --deps",
        "candidate --fork pi-session-search --prepared -- npm run check",
        "candidate --fork pi-session-search --prepared -- npm run test",
    ], calls(base, "pi-forks-calls")
    # ONE PREPARE STEP WRITES THE CHECKOUT; THE SCRIPTS READ IT. Materializing the series
    # and installing its dependencies is the only shared write a quality run makes, so it
    # is planned once. Each script then runs with `--prepared`, which touches nothing, and
    # declares only what it actually writes: the typecheck writes nothing, and the suite's
    # `pretest` rebuilds the bundle into `dist/`. The two are disjoint, so they run at the
    # same time, ordered behind the prepare step by `after` rather than by a shared write
    # (board item 405; the measured write sets are in docs/pi/fork-quality-write-sets.md).
    quality_writes = [line.split("writes:", 1)[1].strip()
                      for line in result.stdout.splitlines()
                      if "writes:" in line and "candidate/pi-session-search" in line]
    assert quality_writes == [
        f"{repo / '.forks' / 'pi-session-search'}, {candidate}",  # merge-check may populate the input cache
        # the replay materializes it too, and writes the shared checkout it replays from
        f"{repo / '.forks' / 'pi-session-search'}, {candidate}",
        # the prepare step materializes and installs it, and may fetch the series base into
        # the shared checkout, so it declares both like the replay
        f"{repo / '.forks' / 'pi-session-search'}, {candidate}",
        str(candidate / "dist"),  # the suite's pretest rebuilds the bundle
    ], quality_writes
    # The materialized tree each quality step tested is recorded, so a later reader can
    # count the executions whose fingerprint had already passed (board item 396). The
    # fixture's candidate checkout is a stub directory rather than a git tree, so the value
    # is null here; the key's presence is what this pins, and the helper's own case below
    # pins the value. The prepare step is a fork-quality step too, and it materializes the
    # same candidate, so it carries the same fingerprint (board item 405).
    timed_quality = [one for one in json.loads(
        (repo / ".git" / "yolo-fork-check" / "timings.jsonl").read_text().splitlines()[-1])["checks"]
        if one["kind"] == "fork-quality"]
    assert timed_quality, timed_quality
    assert all("fingerprint" in one for one in timed_quality), timed_quality
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip()

    # A DIRTY shared checkout still verifies the candidate: the six edited test files a
    # stale writer left in `.forks/pi-session-search` on 2026-10-24 are not a path any
    # step reads. The tree the report names for every judging step is the candidate.
    (checkout / "src").mkdir()
    (checkout / "src" / "index.test.ts").write_text("// a writer's half-finished edit\n")
    (repo / "yolo-packs" / "matt" / "patches" / "pi-session-search" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\ny\n"
    )
    commit(repo, "the series moves while the shared checkout is dirty")
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    judged = [line for line in result.stdout.splitlines()
              if line.startswith("  run  ") and "fork pi-session-search:" in line
              and ("the series replays" in line or "the series builds" in line
                   or "npm run" in line)]
    assert judged, result.stdout
    for line in judged:
        assert str(candidate) in line, (line, result.stdout)
        assert f"in {checkout}" not in line and f"at {checkout}" not in line, (line, result.stdout)
    assert calls(base, "pi-forks-calls")[3:5] == [
        "candidate --fork pi-session-search",
        "candidate --fork pi-session-search --build",
    ], calls(base, "pi-forks-calls")

    # After approval, an unrelated edit does not rerun the source suite.
    (repo / "unrelated-config").write_text("small change\n")
    commit(repo, "unrelated edit after source approval")
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout
    assert [call for call in calls(base, "pi-forks-calls") if "-- npm run" in call] == [], \
        calls(base, "pi-forks-calls")

    # The fingerprint helper reads the candidate checkout's HEAD tree, and answers None
    # rather than inventing one when there is no tree to read. A fingerprint that cannot
    # be read is not a fingerprint, and instrumentation must never fail the run.
    spec = importlib.util.spec_from_file_location("fork_check_fingerprint", SCRIPT)
    fingerprint_module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(fingerprint_module)
    with tempfile.TemporaryDirectory() as tree_tmp:
        tree_repo = Path(tree_tmp)
        git_init(tree_repo)
        (tree_repo / "a.txt").write_text("x\n")
        sh(tree_repo, "git", "add", ".")
        sh(tree_repo, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.com",
           "commit", "--quiet", "-m", "one tree")
        expected = sh(tree_repo, "git", "rev-parse", "HEAD^{tree}").stdout.strip()
        assert fingerprint_module.materialized_fingerprint(str(tree_repo)) == f"tree:{expected}", \
            fingerprint_module.materialized_fingerprint(str(tree_repo))
        assert fingerprint_module.materialized_fingerprint(str(tree_repo / "missing")) is None
        assert fingerprint_module.materialized_fingerprint("") is None

    # 23. A write that lands after the dirty snapshot but before the refusal decision
    #     must not be judged. The plan issues one `git ls-remote` per distinct upstream,
    #     so that window is seconds wide, and the 2026-10-17 21:05:51Z write to
    #     personal.jsonc fell into it: the audit that finished 21:10:24Z had snapshotted
    #     at 21:05:35Z and read the new bytes anyway
    #     (docs/gate-investigations-2026-10-17.md, "the two full audits that bracket the
    #     write"). Drive main() in-process and make the probe itself dirty a path a
    #     planned check reads, so the write happens while the plan runs. Before the fix
    #     the pre-plan snapshot is clean, the checks run, and the run verifies; after it
    #     the re-read sees the write and refuses.
    sh(repo, "git", "checkout", "--quiet", "--", ".")
    raced_path = repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch"
    raced_path.write_text("a changed series selects the fork checks\n")
    commit(repo, "select a fork check before racing its input")
    spec = importlib.util.spec_from_file_location("fork_check_race", repo / "tools" / "fork-check.py")
    racer = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(racer)
    real_ls_remote = racer.ls_remote

    def ls_remote_racing_the_plan(url):
        raced_path.write_text("written by the upstream probe the plan is running\n")
        return real_ls_remote(url)

    racer.ls_remote = ls_remote_racing_the_plan
    raced_out, raced_err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(raced_out), contextlib.redirect_stderr(raced_err):
        raced_rc = racer.main([])
    raced_text = raced_out.getvalue() + raced_err.getvalue()
    assert raced_rc == 2, raced_text
    assert "REFUSED" in raced_text, raced_text
    assert "changed while the plan was being built" in raced_text, raced_text
    assert "yolo-packs/matt/patches/pi-subagents/0001-x.patch" in raced_text, raced_text
    assert record(repo)["runs"][-1]["changed_while_planning"] == 1, record(repo)["runs"][-1]
    assert record(repo)["runs"][-1]["outcome"] == "dirty", record(repo)["runs"][-1]
    sh(repo, "git", "checkout", "--quiet", "--", ".")

_shard_boundary("quarantine-and-dirty-race", 20)

# 24a. A SOURCE-ONLY fork's own source is checked too, and its steps install the toolchain they
# need. pi-subagents declares no `build` script (its pi entry point is TypeScript loaded
# through jiti), so `candidate --build` installs nothing and a typecheck or unit suite
# would run against no node_modules at all. That is the hole item 190 was filed for: a
# broken build or a failing case in the fork this session's own `subagent` tool runs on
# reached the owner and never the gate. The steps ask for `--deps`, and a failing one is red.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base, quarantine=True)
    env = build_stubs(base)
    assert run(repo, env).returncode == 0
    patch = repo / "yolo-packs/matt/patches/pi-subagents/0001-x.patch"
    patch.write_text(patch.read_text() + "\n")
    commit(repo, "change the pi-subagents series")
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    fork_calls = [call for call in calls(base, "pi-forks-calls")
                  if call.startswith("candidate --fork pi-subagents")]
    assert "candidate --fork pi-subagents --deps" in fork_calls, fork_calls
    assert "candidate --fork pi-subagents --prepared -- npm run typecheck" in fork_calls, fork_calls
    assert "candidate --fork pi-subagents --prepared -- npm run test:unit" in fork_calls, fork_calls
    assert "fork-quality fork pi-subagents: npm run typecheck" in result.stdout, result.stdout
    assert "fork-quality fork pi-subagents: npm run test:unit" in result.stdout, result.stdout
    # A fork WITH a build script is not handed --deps: its build already installed the
    # dependencies, with that pack's own npm flags.
    assert not [call for call in fork_calls if call.startswith("candidate --fork pi-session-search --deps")], fork_calls
    # A failing unit case in the fork is the gate's red, not the owner's. The series moves
    # again so the steps are planned for a change the last green run did not already
    # approve, and the driver is told to fail the one call the unit suite runs.
    patch.write_text(patch.read_text() + "\n")
    commit(repo, "move the pi-subagents series again")
    (base / "pi-forks-fail").write_text("npm run test:unit")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout
    assert "fork-quality fork pi-subagents: npm run test:unit" in result.stdout, result.stdout
    assert "FAILED" in result.stdout, result.stdout

    # A QUARANTINED step must not stop the run reaching a verdict. The unit suite's only
    # failing case is covered by the fixture declaration in QUARANTINED_FORK_QUALITY,
    # and the test file in the candidate checkout has the content
    # the declaration was written against, so the run verifies and the baseline moves. It
    # is still named -- on its own line, with the reason, counted as a quarantined case and
    # never as one that passed -- because a quarantine is not a claim that the case passes
    # (docs/reference/gate-check-rules.md).
    quarantined_file = quarantine_fixture_file(repo / ".forks/candidate/pi-subagents")
    quality = f"npm run test:unit in the clean checkout at {repo / '.forks/candidate/pi-subagents'}"
    quarantined_case = QUARANTINE_FIXTURE_CASE
    (base / "pi-forks-out").write_text(
        "\u2139 tests 4671\n\u2139 pass 4651\n\u2139 fail 1\n\n\u2716 failing tests:\n\n"
        "test at test/unit/herdr-native-placement.test.ts:186:2\n"
        f"\u2716 {quarantined_case} (381.543693ms)\n"
    )
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip(), \
        record(repo)["baseline"]
    # Named on its own line, with the reason, and not as one of the passing checks.
    assert f"  quarantined      fork pi-subagents: {quality}" in result.stdout, result.stdout
    assert f"because: {QUARANTINE_FIXTURE_ARGS['reason']}" in result.stdout, result.stdout
    assert f"  run  fork-quality fork pi-subagents: {quality}" not in result.stdout, result.stdout
    # The case, the item and the content the declaration was written against are printed
    # with it, with the classification: the cases ran and failed on an upstream timing
    # race, which is not "could not answer here".
    assert ("       case: test/unit/herdr-native-placement.test.ts -- "
            f"{quarantined_case}, ") in result.stdout, result.stdout
    assert (f"       item quarantine/herdr-native-placement, declared against blob "
            f"{QUARANTINE_FIXTURE_BLOB} (upstream)") in result.stdout, result.stdout
    # It is counted as a quarantined case, never as one that passed and never folded into
    # the "could not answer" tally: it ran, it failed, and a recorded decision suppressed it.
    assert "1 case(s) quarantined -- each named above" in result.stdout, result.stdout
    assert "a quarantined case is not a passing one" in result.stdout, result.stdout
    assert "unanswerable" not in result.stdout, result.stdout
    assert "expire" not in result.stdout, result.stdout
    assert "FAILED" not in result.stdout, result.stdout
    # The run's record carries the declaration with the skip: item, blob and classification.
    recorded_quarantines = [skip["quarantine"] for skip in record(repo)["runs"][-1]["skips"]
                            if "quarantine" in skip]
    assert recorded_quarantines == [{"item": "quarantine/herdr-native-placement",
                                     "blob": QUARANTINE_FIXTURE_BLOB,
                                     "classification": "upstream"}], recorded_quarantines
    approved = record(repo)["baseline"]["commit"]

    # The declaration applies to the content it was written against and to nothing else.
    # The series moves again and the same case fails, but the test file in the candidate
    # checkout has changed: a changed file is a new subject the old evidence does not
    # cover, so the step is the red it already is, with the reason beside it.
    patch.write_text(patch.read_text() + "\n")
    commit(repo, "move the pi-subagents series under a changed test file")
    changed = QUARANTINE_FIXTURE_CONTENT + b"// edited\n"
    quarantine_fixture_file(repo / ".forks/candidate/pi-subagents", changed)
    changed_blob = hashlib.sha1(b"blob %d\0" % len(changed) + changed).hexdigest()
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "FAILED" in result.stdout, result.stdout
    assert "  quarantined      " not in result.stdout, result.stdout
    assert "case(s) quarantined" not in result.stdout, result.stdout
    assert (f"warn: the quarantine declaration for test/unit/herdr-native-placement.test.ts is "
            f"not applied: it was written against blob {QUARANTINE_FIXTURE_BLOB} and the file "
            f"at {quarantined_file} is now blob {changed_blob}") in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == approved, record(repo)["baseline"]
    # So is a file that is not there to read.
    quarantined_file.unlink()
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert f"the file could not be read at {quarantined_file}" in result.stdout, result.stdout
    quarantine_fixture_file(repo / ".forks/candidate/pi-subagents")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "1 case(s) quarantined" in result.stdout, result.stdout
    approved = record(repo)["baseline"]["commit"]

    # The safety property is untouched: a quarantine must never mask a second failure. The
    # same series moves again, and this time the typecheck fails too -- the same transcript
    # on a step the declaration does not name, so only the unit suite is quarantined and the
    # typecheck's red is the run's.
    patch.write_text(patch.read_text() + "\n")
    commit(repo, "move the pi-subagents series beside a quarantined step")
    (base / "pi-forks-fail").write_text("npm run ")
    result = run(repo, env)
    assert result.returncode != 0, result.stdout
    assert "fork-check: VERIFIED" not in result.stdout, result.stdout
    assert "fork-quality fork pi-subagents: npm run typecheck" in result.stdout, result.stdout
    # The red typecheck stops the pass (board item 337): the unit suite beside it does not
    # run, so a quarantine it might have applied could never hide the red. It is named
    # not-run with the reason rather than silently missing or read as a pass.
    assert not any("QUARANTINED" in line for line in result.stdout.splitlines()), result.stdout
    assert ("  not run  fork-quality fork pi-subagents: npm run test:unit"
            in result.stdout), result.stdout
    assert "did not run because the verdict was already red" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == approved, record(repo)["baseline"]

assert "@python3 tools/fork-check.py {{args}}" in (ROOT / ".justfile").read_text()

# A long run is observable WHILE it runs. The report is printed once, at the end, so
# without this a healthy run of many minutes is indistinguishable from a wedged tool
# call -- which is what made a watcher's silence alarm fire on healthy runs. Each check
# says what it found as it completes, on stderr; the report is stdout and is unchanged,
# so a caller that pipes it through `tee` or `tail` still sees the run move. One line per
# check, in plan order: a concurrent run then reads exactly like a serial one, and the
# heartbeat is what names work that is still in flight.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    (repo / "docs/guide.md").write_text("# Guide\n")
    commit(repo, "start from a checked document")
    assert run(repo, env).returncode == 0
    (repo / "docs/guide.md").write_text("# Guide, edited\n")
    commit(repo, "edit the guide")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    # CPU accounting advisories may precede the first gate lifecycle status.
    statuses = [line for line in result.stderr.splitlines() if line.startswith("fork-check:")]
    assert statuses[0].startswith("fork-check: planning"), result.stderr
    assert "fork-check: plan ready" in result.stderr, result.stderr
    assert "fork-check: ok file-size" in result.stderr, result.stderr
    assert "fork-check: ok file-size" in result.stderr, result.stderr
    # One line per check, and no second one announcing a start: a start line would be in
    # start order while the results are in plan order, so the two would disagree.
    assert "fork-check: running" not in result.stderr, result.stderr
    # Every check the run's own record says it ran has its one line, so a check cannot
    # finish without saying so.
    for one in record(repo)["runs"][-1]["checks"]:
        assert f"{one['kind']} -- {one['label']}" in result.stderr, one
    # The verdict is still the report's, and the report is still stdout.
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" not in result.stderr, result.stderr
    # The timings line names the wall clock, the checks' own sum and the concurrency
    # actually achieved -- and with --jobs 1 the peak is 1, which is how a reader tells a
    # serial run from a concurrent one that happened to have nothing to overlap.
    timings = [line for line in result.stdout.splitlines() if line.startswith("fork-check: timings --")]
    assert len(timings) == 1, result.stdout
    assert "wall clock" in timings[0], timings[0]
    assert "concurrent (peak 1, 1 at a time)" in timings[0], timings[0]

    # 24. A verified run counts what it did not run. A step gated on repository paths
    #     cannot be planned by drift from outside the repository -- the matt-render
    #     blindness of 2026-10-16 to 2026-10-18, where a yolo upgrade changed what the
    #     pack renders and no path here moved -- so the headline counts the gate steps
    #     that did not run apart from the checks that passed and the ones this machine
    #     could not answer, and the report names every one of them.
    subset = base / "subset"
    subset.mkdir()
    repo2, _, _ = build_repo(subset)
    env2 = build_stubs(subset)
    result = run(repo2, env2)
    assert result.returncode == 0, result.stdout + result.stderr
    headline = [line for line in result.stdout.splitlines()
                if line.startswith("fork-check: VERIFIED")][0]
    # 8 checks ran (structural, the `.ts` file-size budget, the series
    # check, the two
    # patch-branch merge-checks -- both working copies are in the fixture, the way a main
    # checkout that ran every setup looks -- pi-subagents' one prepare step and its two
    # source checks, which a fresh repository has never approved either); 39 gate steps did
    # not run (the group steps a fresh repository plans nothing for -- one per Go tool directory, one per
    # suite, the session-forensics test, the liveness harness, the
    # pi-forks driver's own suite, the matt pack's board delivery and its launcher's build
    # cache, and the commit-citation guard among them -- plus one entry per untouched fork,
    # each naming the checks it did not run); none could not answer. The commit-citation
    # guard moved this count from 21 when it landed (item 250), the land-queue step from 22
    # (item 314), registering the retired board tool as a Go tool directory from 23, the
    # a fleet step and the patch-branch pre-push guard from 24, the matt pack's
    # board delivery from 26, the pack's board launcher cache from 27 to 28, registering
    # `tools/land` as a Go tool directory from 28 to 29, the pi-forks driver's own suite
    # from 29 to 30, retiring the transitional whale-board Go tool from 30 back to 29, the
    # hooks-install step from 29 to 30, the pipeline-evidence conformance step from 30 to
    # 31, and registering the merge queue's Go tool directory from 31 to 32 (it is
    # `tools/tillr-land/` now); each is
    # one more named step on a fresh run, and the retirement is the one removal. The size
    # budget is planned every run, so it is one of the checks that passed rather than one of
    # these steps. The merge queue then replaced `tools/land` in the same change, so that
    # directory's step left the plan as its successor's joined it, and the count is 31. The
    # reload-residue audit made it 32, and two later steps took it to 34 -- the lane
    # steering channel's suite and the seat-profile encoding -- each for the same reason as
    # the land-queue step: a fresh repository plans nothing for a path it watches. Two, not
    # one, because they landed from different lanes and each was counted against 32 alone.
    # The skill-less probe transcript took it to 35: a fresh repository plans nothing for
    # the pack trees it watches. The prepare step is a check that runs, not a step that did
    # not, so it moves the passed count and leaves this one there (board item 405). The
    # active-steering skill's test took it to 36: a fresh repository plans nothing for a
    # pack skill's text. The pi-inspector test took it to 37: a fresh repository plans
    # nothing for the extension, the profiler, or the test it watches. The durable steering
    # timer's suite took it to 38, for the same reason as the lane steering channel's: a
    # fresh repository plans nothing for the extension or the suite it watches. The
    # session-budget test took it to 39: a fresh repository plans nothing for the test or
    # the tool it reads. The Go split took it to 42: each of the three Go tool directories
    # now plans a `go-build` step beside its `go-tool` step, and a fresh repository plans
    # nothing for either, so the split adds one named step per directory. Retiring
    # the board tool took it back to 40: its two steps left the plan. Registering the
    # seven tillr modules took it to 54: seven directories x two steps, each named as not
    # run on a fresh repository. The arithmetic is 40 + 7 x 2 = 54, and the next change
    # adds or removes two per directory it registers or retires.
    # MM-1A's backend suite took it to 27: a fresh repository plans nothing for the three
    # adapters or the policy that configures them, so the step is named and does not run.
    # The count is DERIVED, not copied: 26 non-Go-tool steps plus two per registered Go
    # tool directory. Retiring the merge queue (docs/design/mq-retirement.md) took it from
    # 49 to 46: the land-queue step left the plan and `tools/mq` left GO_TOOL_DIRS, which
    # is three named steps, and neither is replaced by a step that does not run. A registry change then needs no edit here, which is the class fix
    # the steering ruling required after three hand-sweeps failed.
    spec = importlib.util.spec_from_file_location("fork_check_go_tools", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    go_tool_steps = 2 * len(module.GO_TOOL_DIRS)
    assert "7 check(s) passed" in headline, headline
    # The count is DERIVED from the report, not from a constant: the headline's number must
    # equal the number of steps the report names as not run. A constant here was a hand
    # sweep waiting to happen -- it went stale twice in one day, once when the pack-sync
    # step was registered and once when the observatory's web check was, and each time the
    # failure named the arithmetic rather than the missing step.
    reported = re.findall(r"^(\s+)not run\s", result.stdout, re.MULTILINE)
    headline_count = re.search(r"(\d+) gate step\(s\) did not run", headline)
    assert headline_count, headline
    assert int(headline_count.group(1)) == len(reported), (
        f"the headline says {headline_count.group(1)} step(s) did not run, and the report "
        f"names {len(reported)}: {headline}"
    )
    assert go_tool_steps <= len(reported), (
        f"{go_tool_steps} Go tool step(s) are registered, and only {len(reported)} step(s) "
        "did not run: a registered directory must plan its two steps"
    )
    assert "0 could not answer here" in headline, headline
    # A step that did not run is named, and reads as "not run" rather than "skip".
    assert f"  {'not run':<16} matt-render (node --test)" in result.stdout, result.stdout
    assert f"  {'not run':<16} steering-timer (node --test)" in result.stdout, result.stdout
    assert f"  {'not run':<16} rank-order (node --test)" in result.stdout, result.stdout
    assert f"  {'not run':<16} fork pi-subagents (patch-branch verify, candidate replay, candidate build)" in result.stdout, result.stdout
    assert f"  {'not run':<16} fork pi-background-tasks (patch-branch verify, candidate replay, candidate build)" in result.stdout, result.stdout
    # And the report names what the plan cannot see, with the command that covers each.
    assert "not visible to this plan" in result.stdout, result.stdout
    assert "yolo --version --full" in result.stdout, result.stdout
    # pi-forks driver's own suite took it from 29 to 30, retiring the transitional
    # whale-board Go tool brought it back to 29, the hooks-install step took it to 30, the
    # pipeline-evidence conformance step took it to 31, and registering the merge queue's Go
    # tool directory took it to 32: one Go tool directory left the plan, and three steps
    # joined it. The merge queue replaced `tools/land`, whose step left the plan as its
    # successor's joined it, leaving the count where it was. The reload-residue audit, the
    # lane steering channel's suite and the seat-profile encoding each added one more,
    # taking it to 34, for the same reason as the land-queue step: a fresh repository plans
    # nothing for a path it watches. The skill-less probe transcript took it to 35, the
    # active-steering skill's test to 36, the pi-inspector test to 37, the durable
    # steering timer's suite to 38, the session-budget test to 39, and the Go split to 42
    # (one `go-build` step per Go tool directory, beside the `go-tool` step it split from).
    # Retiring the board tool took it to 40, and registering the seven tillr modules took
    # it to 54: 40 + 7 x 2, two named steps per directory.
    # The count is DERIVED from the report rather than kept as a constant: the headline's
    # number must equal the number of steps the report names as not run, and every
    # registered Go tool directory must contribute its two. A constant here went stale
    # twice in one day -- once when the pack-sync step was registered, once when the
    # observatory's web check was -- and each failure named the arithmetic rather than
    # the missing step. The observatory-dev step adds one more untouched-input line;
    # its count is derived by the same rule, with no constant to update.
    reported = result.stdout.count(f"  {'not run':<16}")
    headline_count = re.search(r"(\d+) gate step\(s\) did not run", result.stdout)
    assert headline_count, result.stdout
    assert int(headline_count.group(1)) == reported, result.stdout
    assert go_tool_steps <= reported, result.stdout

_shard_boundary("advisory-yolo", 11)

# 25. The installed yolo build is ADVISORY: it is reported, and it plans nothing. The
#     five steps it decides -- the series check (it applies the series), the yolo config
#     test (it shells `yolo check`), the meta-muse tests and the matt pack's render tests
#     (they drive yolo's derives) -- were planned from the build's identity, and that was
#     wrong twice over: the record is shared through the common Git directory, so a run on
#     another machine (a jail, whose `yolo` is its own build) planned them on every run for
#     a build nothing here had changed; and a rebuild at the same commit forced the same
#     replan. So the identity is still recorded and a different one is still reported, but
#     those five steps run only when a path they watch changes. The identity compared is
#     the build's COMMIT, so a same-commit rebuild never even reads as a change.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    # 25f deletes the whole inputs map -- a record from before either pin -- so the
    # node upgrade replans its eleven steps there too: the fixture links the pi checkout
    # and stubs the liveness harness, so the planned session-liveness step runs instead
    # of refusing for its missing need.
    checkout = repo / ".forks" / "pi" / "packages" / "coding-agent" / "src"
    checkout.mkdir(parents=True)
    (checkout / "session.ts").write_text("export class AgentSession {}\n")
    (repo / "tools" / "session-liveness").mkdir()
    (repo / "tools" / "session-liveness" / "run").write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$0" >> "{base}/session-liveness-calls"\n'
        "exit 0\n"
    )
    commit(repo, "the liveness harness and the checkout it reads")
    build_a, build_b, build_c, build_d = ("a" * 64, "b" * 64, "c" * 64, "d" * 64)
    (base / "yolo-identity").write_text(yolo_version_full(build_a))

    # 25a. The first verified run pins the installed build. It plans no yolo-decided
    #      step: there is no earlier verification for a build to differ from.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert calls(base, "yolo-version-calls") == ["--version --full"], calls(base, "yolo-version-calls")
    assert record(repo)["inputs"]["yolo"]["fingerprint"] == f"sha256:{build_a}", record(repo)["inputs"]
    assert record(repo)["inputs"]["yolo"]["build"] == "v0.14.0-428-g772997837bbd", record(repo)["inputs"]
    assert "the installed yolo build is v0.14.0-428-g772997837bbd, binary sha256 aaaaaaaaaaaa" in result.stdout, result.stdout
    for kind in ("matt-render", "meta-muse", "yolo-config"):
        assert f"run  {kind}" not in result.stdout, (kind, result.stdout)

    # 25b. The same build, nothing changed: the identity is read, and plans nothing.
    clear(base, "yolo-calls", "yolo-version-calls", "matt-render-calls", "meta-muse-calls",
          "yolo-config-calls", "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "yolo-version-calls") == ["--version --full"], calls(base, "yolo-version-calls")
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "matt-render-calls") == [], calls(base, "matt-render-calls")
    assert calls(base, "meta-muse-calls") == [], calls(base, "meta-muse-calls")
    assert calls(base, "yolo-config-calls") == [], calls(base, "yolo-config-calls")
    assert "gate step(s) did not run (nothing they watch changed)" in result.stdout, result.stdout

    # 25c. A different build at a different commit -- a yolo upgrade, with no path here
    #      moved -- is REPORTED and plans nothing: the five steps it decides run when a
    #      path they watch changes, not because the binary answering moved.
    (base / "yolo-identity").write_text(yolo_version_full(build_b, stamp="v0.15.0-3-gabc1234", commit="abc1234"))
    clear(base, "yolo-calls", "matt-render-calls", "meta-muse-calls", "yolo-config-calls",
          "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert sorted(calls(base, "matt-render-calls")) == [], calls(base, "matt-render-calls")
    assert sorted(calls(base, "meta-muse-calls")) == [], calls(base, "meta-muse-calls")
    assert calls(base, "yolo-config-calls") == [], calls(base, "yolo-config-calls")
    assert "the installed yolo build changed" in result.stdout, result.stdout
    assert "reported, not replanned" in result.stdout, result.stdout
    assert "binary sha256 bbbbbbbbbbbb" in result.stdout, result.stdout
    assert "binary sha256 aaaaaaaaaaaa" in result.stdout, result.stdout
    # The verdict still records the build it was reached on, so the next run compares
    # against this one rather than against the upgrade it already reported.
    assert record(repo)["inputs"]["yolo"]["fingerprint"] == f"sha256:{build_b}", record(repo)["inputs"]
    # The checks about the patch branch and what the fork's source compiles to read git
    # content, not which yolo answers, so a build never planned them either -- only the
    # plan's drift probe asks the bridge, and it asks about the upstream, not the build.
    assert calls(base, "pi-forks-calls") == ["patch-branch target --fetch"], calls(base, "pi-forks-calls")
    assert "run  patch-branch-inv2" not in result.stdout, result.stdout
    assert "run  fork-quality" not in result.stdout, result.stdout

    # 25d. A SAME-COMMIT REBUILD is not a change at all: a different binary sha256 at the
    #      commit the record names is what a rebuild produces, so it is not reported and
    #      cannot force a replan. The record keeps the identity this run read.
    (base / "yolo-identity").write_text(
        yolo_version_full("e" * 64, stamp="v0.15.0-3-gabc1234-rebuilt", commit="abc1234"))
    clear(base, "yolo-calls", "matt-render-calls", "meta-muse-calls", "yolo-config-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the installed yolo build changed" not in result.stdout, result.stdout
    assert "binary sha256 eeeeeeeeeeee" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "matt-render-calls") == [], calls(base, "matt-render-calls")
    assert record(repo)["inputs"]["yolo"]["fingerprint"] == f"sha256:{'e' * 64}", record(repo)["inputs"]

    # 25e. A build whose steps fail is not pinned: the run that would have recorded it
    #      failed, so the next run asks again instead of trusting a build nothing verified.
    #      A path the series check watches is what plans it now that the build cannot.
    (base / "yolo-identity").write_text(yolo_version_full(build_c, stamp="v0.15.0-4-gdef5678", commit="def5678"))
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nc\n"
    )
    commit(repo, "a series edit plans the series check")
    (base / "yolo-out").write_text("extension matt/pi-subagents: upstream v9 does not take the patch series\n")
    (base / "yolo-rc").write_text("1")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "FAILED" in result.stdout, result.stdout
    assert record(repo)["inputs"]["yolo"]["fingerprint"] == f"sha256:{'e' * 64}", record(repo)["inputs"]
    clear(base, "yolo-calls", "yolo-rc", "yolo-out")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    assert record(repo)["inputs"]["yolo"]["fingerprint"] == f"sha256:{build_c}", record(repo)["inputs"]

    # 25f. An identity that cannot be read is declared, with the command a reader must run
    #      instead -- never guessed at, and never silently read as unchanged.
    (base / "yolo-identity").unlink()
    clear(base, "yolo-calls", "matt-render-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the installed yolo build could not be identified" in result.stdout, result.stdout
    assert "yolo --version --full" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "matt-render-calls") == [], calls(base, "matt-render-calls")

    # 25g. A record from before this gate kept an identity -- a baseline that names no
    #      build -- is reported once (this run records one) and plans nothing from it.
    state = record(repo)
    del state["inputs"]
    write_record(repo, state)
    (base / "yolo-identity").write_text(yolo_version_full(build_d))
    clear(base, "yolo-calls", "matt-render-calls", "meta-muse-calls", "yolo-config-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "no yolo build identity was recorded with the last verified run" in result.stdout, result.stdout
    assert "nothing was planned from it" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "yolo-config-calls") == [], calls(base, "yolo-config-calls")
    assert record(repo)["inputs"]["yolo"]["fingerprint"] == f"sha256:{build_d}", record(repo)["inputs"]

# 25 (continued). A machine-local input is DECLARED elsewhere rather than left to veto a
#     verdict. The patch-branch stamp is written on the host and never reaches a jail, so
#     `yolo pack series check` reports the series it could not resolve among the ones it
#     did: that series is named in ONE report line with the command that answers it, the
#     rest of the pack's transcript still decides the check, and the run reaches a verdict
#     and advances the baseline -- which is what a jail landing could not do while a
#     host-written record could block it. What CAN be checked must still pass: a conflict
#     in the same transcript still fails the run, so a declaration loosens nothing.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    # The two shapes `yolo pack series check` prints: the series it applied, and the one
    # whose `patch_branch` this machine cannot resolve because the stamp is the host's.
    benign = (
        "yolo-jail 0.15.1 | linux/x86_64 | in-jail\n"
        "extension matt/pi-subagents: 1 patches in patches/pi-subagents (series 9c203dcf), base 1bc94136\n"
        "checking extension matt/pi-subagents's upstream git+https://example.invalid/x?ref=main in a scratch copy\n"
        "extension matt/pi-subagents: upstream v0.81.0 (1bc94136) takes the series (1 patches, series 9c203dcf): applies\n"
    )
    unresolved = (
        "yolo pack series check: extension matt/pi-manage-todo-list: the patch branch is "
        "not yet resolved on this machine — run `yolo pack update` on the host or start a "
        "launch to record a current verdict; `yolo pack series check --online "
        "/workspace/yolo-packs/matt` checks a disposable scratch copy and does not populate "
        "the record\n"
    )

    # (a) The whole pack is unresolved (exit 1) -- the shape a jail sees for a pack whose
    #     only series carries a `patch_branch` locator. The run still verifies, names the
    #     series, records the declaration, and advances the baseline.
    (base / "yolo-out").write_text(benign + unresolved)
    (base / "yolo-rc").write_text("1")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert ("fork-check: declared elsewhere -- the patch-branch stamp (matt/pi-manage-todo-list):"
            in result.stdout), result.stdout
    assert "checks with `yolo pack update` on the host" in result.stdout, result.stdout
    assert "input(s) declared elsewhere" in result.stdout, result.stdout
    assert "skip series" not in result.stdout, result.stdout
    assert "FAIL series" not in result.stdout, result.stdout
    head = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    assert record(repo)["baseline"]["commit"] == head, record(repo)["baseline"]
    entry = record(repo)["runs"][-1]
    assert entry["outcome"] == "verified", entry
    labels = [one["label"] for one in entry["declared"]]
    assert "the patch-branch stamp (matt/pi-manage-todo-list)" in labels, labels

    # (b) The same line beside a clean exit (0): today's real shape, because the pack's
    #     other series applied. The declaration is still made -- the unanswered series must
    #     not read as coverage just because the command exited 0.
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nb\n"
    )
    commit(repo, "a series edit plans the series check again")
    (base / "yolo-rc").write_text("0")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "the patch-branch stamp (matt/pi-manage-todo-list)" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip(), \
        record(repo)["baseline"]

    # (c) A GENUINELY failing check in the same transcript still blocks the verdict: the
    #     declaration covers the series this machine could not answer for, never a series
    #     that does not take its patch, and the baseline does not advance.
    (repo / "yolo-packs" / "matt" / "patches" / "pi-background-tasks" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nb\n"
    )
    commit(repo, "another series edit")
    verified = record(repo)["baseline"]["commit"]
    (base / "yolo-out").write_text(
        benign + unresolved
        + "extension matt/pi-subagents: upstream v9 does not take the patch series (INV-1)\n"
    )
    (base / "yolo-rc").write_text("1")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "fork-check: FAILED" in result.stdout, result.stdout
    assert "does not take the patch series" in result.stdout, result.stdout
    # The declaration is still named -- it is what the machine could not answer -- but it
    # did not stand in for the check that failed.
    assert "the patch-branch stamp (matt/pi-manage-todo-list)" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

_shard_boundary("per-branch-baselines", 18)

# 26. The baseline is per branch, and planned from the newest verified commit this history
#     contains. The record is shared through the common Git directory, so a single shared
#     baseline was moved by whichever worktree ran last: `git diff baseline..HEAD` over a
#     commit this branch does not contain names the paths that differ between two unrelated
#     trees -- none at all when their content matches -- so `documents` and `scratch` were
#     never planned while the headline read VERIFIED. Refusing that diff made the vacuous
#     plan unplannable, but it also let one worktree's verified run stop another worktree's
#     run. So each branch keeps its own entry, every entry is a candidate, and only one this
#     history contains is ever diffed against: two worktrees cannot invalidate each other,
#     and a candidate that never landed is skipped rather than refused.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)

    def write_state(state):
        write_record(repo, state)

    # 26a. No baseline at all is the first run, not a refusal: every series is checked, and
    #      the entry for the branch this checkout has is recorded beside the shared entry
    #      that names the newest verified commit.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "no verified baseline this history contains" in result.stdout, result.stdout
    # The report names the baseline the PLAN used, so a first run reads as one.
    assert "no baseline -- no verified commit this history contains" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    first = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    state = record(repo)
    assert state["baselines"]["refs/heads/main"]["commit"] == first, state["baselines"]
    assert state["baseline"]["commit"] == first, state["baseline"]

    # 26b. A legitimate incremental run still plans only what changed: one document, and no
    #      series step, because no path they watch moved. The report names the entry the
    #      plan used and the branch that recorded it.
    (repo / "docs" / "guide.md").write_text("# Guide\n\nA document, revised.\n")
    commit(repo, "revise the guide")
    clear(base, "yolo-calls", "uvx-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert f"baseline {first[:12]}" in result.stdout, result.stdout
    assert "recorded on refs/heads/main" in result.stdout, result.stdout
    assert "1 path(s) changed since it" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")
    assert "fork-check: VERIFIED" in result.stdout, result.stdout

    # 26c. Two worktrees, both runs started together, both reach a verdict. Each lane is its
    #      own branch, so each run advances its own entry and neither can move the other's
    #      base: neither refuses, and each plans its own single change against the integration
    #      branch's verified commit -- not a vacuous zero-path range, and not the other lane's
    #      commit. The document check sleeps, so the runs overlap the way two lane `just done`s
    #      do; the record lock is taken only for the save, so neither run queues behind the
    #      other's checks and both saves land.
    main_baseline = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    lanes = {}
    for name in ("lane-a", "lane-b"):
        path = base / name
        sh(repo, "git", "worktree", "add", "--quiet", "-b", f"feat/{name}", str(path), "main")
        (path / "docs" / "guide.md").write_text(f"# Guide\n\n{name} edits this document.\n")
        commit(path, f"{name} edits the guide")
        lanes[name] = path
    uvx_stub = base / "stubs" / "uvx"
    uvx_stub.write_text(uvx_stub.read_text().replace("exit 0", "sleep 2\nexit 0"))
    clear(base, "uvx-calls")
    for name, (code, out, err) in run_parallel_gates(lanes, env).items():
        assert code == 0, (name, out, err)
        assert "fork-check: VERIFIED" in out, (name, out)
        assert "REFUSED" not in out, (name, out)
        assert "is not an ancestor of HEAD" not in out, (name, out)
        assert f"baseline {main_baseline[:12]}" in out, (name, out)
        assert "1 path(s) changed since it" in out, (name, out)
    # The lock is not held during the checks any more, so the waits this case used to
    # pin are gone by design. What still has to hold is that both saves land and both
    # per-branch entries survive; that is asserted below.
    state = record(repo)
    lane_heads = {}
    for name, path in lanes.items():
        lane_heads[name] = sh(path, "git", "rev-parse", "HEAD").stdout.strip()
        assert state["baselines"][f"refs/heads/feat/{name}"]["commit"] == lane_heads[name], \
            state["baselines"]
    # The other branch's entry, and the branch both lanes forked from, are untouched.
    assert state["baselines"]["refs/heads/main"]["commit"] == main_baseline, state["baselines"]
    lane_runs = [entry for entry in state["runs"] if entry.get("commit") in set(lane_heads.values())]
    assert len(lane_runs) == 2, json.dumps(lane_runs, indent=2)
    assert all(entry["outcome"] == "verified" for entry in lane_runs), json.dumps(lane_runs, indent=2)
    print("fork-check per-branch baselines:")
    for name, head in lane_heads.items():
        print(f"  {name}: {head[:12]}")

    # 26d. A branch's own entry that its own history no longer contains -- a rebase or a
    #      reset -- is not a refusal either: the run says so, and plans from the newest
    #      verified commit this history does contain. `--explain` first, because it runs
    #      nothing and leaves the seeded state in place, so the same run can then verify.
    lane_a_head = lane_heads["lane-a"]
    state = record(repo)
    state["baselines"]["refs/heads/feat/lane-b"] = {
        "commit": lane_a_head, "tree": "0" * 40, "at": "2026-10-24T00:00:00Z",
    }
    write_state(state)
    (lanes["lane-b"] / "docs" / "guide.md").write_text("# Guide\n\nlane-b edits it again.\n")
    commit(lanes["lane-b"], "lane-b edits the guide again")
    clear(base, "yolo-calls", "uvx-calls")
    result = run(lanes["lane-b"], env, "--explain")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the baseline recorded on refs/heads/feat/lane-b" in result.stdout, result.stdout
    assert "it is on feat/lane-a" in result.stdout, result.stdout
    # The replacement base is another entry this history contains, so it is a verified
    # commit or the fork point with one -- never a first run and never a refusal.
    assert "so this run plans from " in result.stdout, result.stdout
    assert "history was rewritten under it" in result.stdout, result.stdout
    assert "would run  file-size" in result.stdout, result.stdout
    assert "REFUSED" not in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")
    assert record(repo)["baselines"]["refs/heads/feat/lane-b"]["commit"] == lane_a_head, \
        record(repo)["baselines"]

    # 26e. The same state, run for real: a verdict, from the newest verified commit this
    #      history does contain, and this branch's own entry re-pointed at its HEAD.
    clear(base, "yolo-calls", "uvx-calls")
    result = run(lanes["lane-b"], env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "REFUSED" not in result.stdout, result.stdout
    assert "is not an ancestor of HEAD" not in result.stdout, result.stdout
    assert "the baseline recorded on refs/heads/feat/lane-b" in result.stdout, result.stdout
    assert "1 path(s) changed since it" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")
    assert record(repo)["baselines"]["refs/heads/feat/lane-b"]["commit"] == \
        sh(lanes["lane-b"], "git", "rev-parse", "HEAD").stdout.strip(), record(repo)["baselines"]

    # 26f. A record written before per-branch entries -- only the shared `baseline` -- still
    #      plans incrementally when this history contains it, so an upgrade does not turn
    #      every checkout into a first run. The report says which entry it used.
    state = record(repo)
    del state["baselines"]
    state["baseline"] = {"commit": main_baseline, "tree": "0" * 40, "at": "2026-10-24T00:00:00Z"}
    write_state(state)
    (lanes["lane-b"] / "docs" / "guide.md").write_text("# Guide\n\nlane-b edits it a third time.\n")
    commit(lanes["lane-b"], "lane-b edits the guide a third time")
    clear(base, "yolo-calls", "uvx-calls")
    result = run(lanes["lane-b"], env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "recorded on the record's shared entry" in result.stdout, result.stdout
    assert "1 path(s) changed since it" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")
    assert record(repo)["baselines"]["refs/heads/feat/lane-b"]["commit"] == \
        sh(lanes["lane-b"], "git", "rev-parse", "HEAD").stdout.strip(), record(repo)["baselines"]

    # 26g. An entry this clone does not have -- recorded by a checkout that had that commit
    #      and never fetched it here, or a candidate that never landed -- is skipped, not
    #      refused: with no usable candidate the run is a first run, checks everything, and
    #      records the entry for the branch it ran on.
    state = record(repo)
    del state["baselines"]
    state["baseline"] = {"commit": "0" * 40, "tree": "1" * 40, "at": "2026-10-24T00:00:00Z"}
    write_state(state)
    clear(base, "yolo-calls", "uvx-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "no baseline -- no verified commit this history contains" in result.stdout, result.stdout
    assert "is not an ancestor of HEAD" not in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    assert record(repo)["baselines"]["refs/heads/main"]["commit"] == \
        sh(repo, "git", "rev-parse", "HEAD").stdout.strip(), record(repo)["baselines"]

    # 26h. `--all` still forces every series step from a usable baseline, and the document
    #      steps still come from the diff -- one flag, two jobs, neither turned off.
    (repo / "docs" / "guide.md").write_text("# Guide\n\nA document, revised on main.\n")
    commit(repo, "revise the guide on main")
    clear(base, "yolo-calls", "uvx-calls")
    result = run(repo, env, "--all")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")

    # 26i. An entry this branch is BEHIND -- the integration branch advanced past the commit
    #      this branch was cut from -- is neither a refusal nor a first run: the run plans
    #      from the fork point, so this branch's own change is checked. A first run here
    #      would plan every series step from nothing and never plan the document step, which
    #      is a VERIFIED headline over a change nothing examined.
    lane_c = base / "lane-c"
    sh(repo, "git", "worktree", "add", "--quiet", "-b", "feat/lane-c", str(lane_c), main_baseline)
    (lane_c / "docs" / "guide.md").write_text("# Guide\n\nlane-c edits this document.\n")
    commit(lane_c, "lane-c edits the guide")
    clear(base, "yolo-calls", "uvx-calls")
    result = run(lane_c, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "is not an ancestor of HEAD" not in result.stdout, result.stdout
    assert f"baseline {main_baseline[:12]}" in result.stdout, result.stdout
    assert "the fork point with refs/heads/main" in result.stdout, result.stdout
    assert "1 path(s) changed since it" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")
    assert record(repo)["baselines"]["refs/heads/feat/lane-c"]["commit"] == \
        sh(lane_c, "git", "rev-parse", "HEAD").stdout.strip(), record(repo)["baselines"]

    # 26j. A rebased integration branch: its own entry is orphaned, and the newest
    #      VERIFIED entry this history still contains is a stale lane baseline from before
    #      the point the branch was replayed onto. Planning from that entry re-checks
    #      history the branch never touched -- which is how a rebased `main` came to plan
    #      1089 paths from a lane baseline three weeks old and refuse on citations the
    #      rebase had orphaned. The fork point with the branch's own orphaned entry is
    #      where its rewritten history begins, so it is preferred over that entry.
    root_commit = sh(repo, "git", "rev-list", "--max-parents=0", "HEAD").stdout.strip()
    state = record(repo)
    state["baselines"]["refs/heads/feat/lane-d"] = {
        "commit": root_commit, "tree": "0" * 40, "at": "2026-10-24T00:00:00Z",
    }
    write_state(state)
    # The rebase: main is replayed onto an older commit, so the entry it recorded is
    # orphaned and its own fork point is the commit it was replayed onto -- newer than
    # the stale lane entry, which is what makes the newest-verified rule pick the wrong
    # one.
    sh(repo, "git", "reset", "--hard", "--quiet", "HEAD~1")
    (repo / "docs" / "other.md").write_text("# Other\n\nA second document.\n")
    commit(repo, "add a second document")
    clear(base, "yolo-calls", "uvx-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "the baseline recorded on refs/heads/main" in result.stdout, result.stdout
    assert "the fork point with refs/heads/main" in result.stdout, result.stdout
    assert "recorded on refs/heads/feat/lane-d" not in result.stdout, result.stdout
    assert "1 path(s) changed since it" in result.stdout, result.stdout
    assert calls(base, "uvx-calls") == [], calls(base, "uvx-calls")

# 26k. THE REWRITTEN-BRANCH BOUNDARY, BOTH SIDES. A branch whose own recorded entry is
#      orphaned plans from the fork point with that entry, so a line added BEFORE the fork
#      point is in the base tree and is not judged, while the same line added AFTER it is
#      an added line and is. The pair is what makes the rule falsifiable: a planner that
#      widened the base to the newest verified ancestor would refuse the first case, and
#      one that narrowed it to HEAD would pass the second.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)

    def write_state(state):
        write_record(repo, state)

    # A lane commit: it resolves in this clone and is never an ancestor of HEAD, so a
    # citation of it is exactly what the guard refuses.
    sh(repo, "git", "checkout", "--quiet", "-b", "lane")
    (repo / "lane.txt").write_text("lane work\n")
    commit(repo, "lane-only commit")
    lane = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    sh(repo, "git", "checkout", "--quiet", "main")

    # The citation is added BEFORE the fork point. This commit becomes the fork point the
    # rewrite below plans from, so the line sits in the base tree rather than the diff.
    (repo / "docs" / "guide.md").write_text(
        f"# Guide\n\nThe suite counts are in `{lane[:12]}`.\n"
    )
    commit(repo, "cite a lane commit before the fork point")
    fork_point = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()

    # A commit after it, so the old tip is not itself the fork point.
    (repo / "docs" / "other.md").write_text("# Other\n")
    commit(repo, "a commit after the fork point")
    old_tip = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()

    # The rewrite: main is replayed onto the fork point with a different commit, so the
    # old tip is orphaned and merge_base(old_tip, HEAD) is the fork point.
    sh(repo, "git", "reset", "--hard", "--quiet", fork_point)
    (repo / "docs" / "other.md").write_text("# Other\n\nA different second document.\n")
    commit(repo, "a different commit after the fork point")

    # A first run records the state file this case then rewrites. It is green because a
    # first run has no base, so the citation guard judges no line.
    assert run(repo, env).returncode == 0

    # The branch's own entry is the orphaned old tip, which is what makes the rewritten
    # rule fire rather than the newest-verified fallback.
    state = record(repo)
    state["baselines"]["refs/heads/main"] = {
        "commit": old_tip, "tree": "0" * 40, "at": "2026-11-10T00:00:00Z",
    }
    write_state(state)

    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert f"baseline {fork_point[:12]}" in result.stdout, result.stdout
    assert "the fork point with refs/heads/main" in result.stdout, result.stdout

# The gate's own audit suite is part of the gate. `tests/fork-check.test.py` is the
# only thing that exercises the checker's routing, so a change to `tools/fork-check.py`
# was verified by the gate it had just changed, and a landing lane had to run the suite
# by hand and report it separately.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)

    # 27a. Either input plans the step, and both are watched -- so a dirty one refuses
    #      before the suite judges the working tree instead of the commit. No other path
    #      plans it, and the skip names the step it did not run.
    spec = importlib.util.spec_from_file_location("fork_check_self_suite", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    for path in ("tools/fork-check.py", "tests/fork-check.test.py"):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), [path], None, True,
        )
        steps = [check for check in planned.checks if check.kind == "self-suite"]
        assert len(steps) == 1, [(c.kind, c.label) for c in planned.checks]
        assert steps[0].argv == [sys.executable, "tests/fork-check.test.py"], steps[0].argv
        assert steps[0].watches == ("tools/fork-check.py", "tests/fork-check.test.py",
                                    ".justfile", "tools/gate/", "tests/fast-tree-in-done.test.py",
                                    "tools/preflight-declarations.json"), steps[0].watches
        assert not hasattr(steps[0], "exclusive")
        assert not hasattr(steps[0], "exclusive_reason")
    planned = module.build_plan(
        repo, [], module.Record(base / "plan-state.json"), ["bashrc"], None, True,
    )
    assert not any(check.kind == "self-suite" for check in planned.checks), \
        [(c.kind, c.label) for c in planned.checks]
    assert any(skip.label.startswith("self-suite") and not skip.planned for skip in planned.skips), \
        [(skip.label, skip.why) for skip in planned.skips]

    # 27b. The gate executes it. The suite is a stand-in here, so its own record of
    #      being invoked is the proof, and the report names the step.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    (repo / "tests" / "fork-check.test.py").write_text(
        f'from pathlib import Path\nPath("{base}/self-suite-calls").write_text("ran\\n")\n'
    )
    commit(repo, "give the gate a stand-in suite")
    clear(base, "self-suite-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  self-suite" in result.stdout, result.stdout
    assert calls(base, "self-suite-calls") == ["ran"], calls(base, "self-suite-calls")
    plan_line = result.stdout.splitlines()[-1]
    assert plan_line.startswith("fork-check: plan "), plan_line
    assert "exclusive=" not in plan_line, plan_line
    assert "exclusive: runs alone" not in result.stdout, result.stdout
    last_run = record(repo)["runs"][-1]
    assert "exclusive" not in last_run, last_run

    #      The checker is the other input, and it plans the same step on its own.
    clear(base, "self-suite-calls")
    with (repo / "tools" / "fork-check.py").open("a") as edited:
        edited.write("\n# a checker edit the suite must judge\n")
    commit(repo, "edit the checker")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  self-suite" in result.stdout, result.stdout
    assert calls(base, "self-suite-calls") == ["ran"], calls(base, "self-suite-calls")

    # 27c. A suite that fails and names NO case is NOT VERIFIED -- no verdict, no skip --
    #      and the baseline does not advance. The case's own transcript quotes a series
    #      conflict, which must not read as a series verdict: the phrase alone is not the
    #      bridge's refusal. A `SystemExit` with only a message is the suite not completing
    #      in a way that names an assertion, so the gate's own integrity is unknown.
    verified = record(repo)["baseline"]["commit"]
    (repo / "tests" / "fork-check.test.py").write_text(
        'print("a case that no longer holds: does not take the patch series")\n'
        'raise SystemExit(1)\n'
    )
    commit(repo, "a suite that fails and names no case")
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "NOT VERIFIED" in result.stdout, result.stdout
    assert "a case that no longer holds" in result.stdout, result.stdout
    assert "fork-check: FAILED" not in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "unproven", record(repo)["runs"][-1]

    # 27d. A suite that runs to completion and names the assertion that failed is a
    #      VERDICT about the tree -- FAILED, exit 1 -- and the report names the case. The
    #      baseline still does not advance, and the batch still does not pass: only the
    #      routing changes, so the lander can isolate the candidate that touches these
    #      paths and send it to a repair lane (board item 420).
    (repo / "tests" / "fork-check.test.py").write_text(
        'print("a case that no longer holds: does not take the patch series")\n'
        'assert False, "the timings line still says concurrent (peak 1, 1 at a time)"\n'
    )
    commit(repo, "a suite that fails and names its assertion")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "fork-check: FAILED" in result.stdout, result.stdout
    assert "NOT VERIFIED" not in result.stdout, result.stdout
    assert "self-suite: the gate's own audit suite still passes" in result.stdout, result.stdout
    assert "case: the timings line still says concurrent (peak 1, 1 at a time)" in result.stdout, \
        result.stdout
    assert "tests/fork-check.test.py" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

_shard_boundary("gate-code-and-suites", 26)

# Board item 412: a change to the gate's own selection or reporting code plans the
# self-suite and no fork check, while a change to the code that materializes or judges a
# series still forces every fork check. The two sets are pinned here, so a later edit
# cannot quietly move a path from one to the other, and the demonstration runs a real
# gate pass over a gate-only change while a fork's patch branch is behind its upstream.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    # The fixture's checker is the real one, but its suite is not, and a gate-only change
    # plans the step. Committed before the baseline so the demonstration's diff is the
    # checker alone.
    (repo / "tests" / "fork-check.test.py").write_text("raise SystemExit(0)\n")
    commit(repo, "a stand-in self-suite")
    assert run(repo, env).returncode == 0

    spec = importlib.util.spec_from_file_location("fork_check_gate_code", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    # 28a. The sets are the boundary, and they are disjoint. MECHANISM is the code that
    #      materializes or judges a series; GATE_CODE is the gate's own selection and
    #      reporting code, and every one of its paths is an input to the self-suite.
    assert module.MECHANISM == ("bin/pi-forks", "bin/pi-forks.list",
                                "tests/pi-patch-series.test.sh"), module.MECHANISM
    assert module.GATE_CODE == ("tools/fork-check.py", "tests/fork-check.test.py",
                                ".justfile", "tools/gate/", "tests/fast-tree-in-done.test.py"), module.GATE_CODE
    assert not set(module.MECHANISM) & set(module.GATE_CODE), (module.MECHANISM, module.GATE_CODE)
    assert set(module.GATE_CODE) <= set(module.SELF_SUITE), (module.GATE_CODE, module.SELF_SUITE)
    series = module.read_series(repo)
    gate_class = module.classify_change(repo, ["tools/fork-check.py"], series)
    assert gate_class.name == "gate-code", gate_class.name
    assert gate_class.target == module.CHANGE_CLASS_TARGETS["gate-code"], gate_class.target
    judge_class = module.classify_change(repo, ["tests/pi-patch-series.test.sh"], series)
    assert judge_class.name == "driver-behavior", judge_class.name
    driver_class = module.classify_change(repo, ["bin/pi-forks"], series)
    assert driver_class.name == "driver-behavior", driver_class.name
    assert "driver or its manifest" in driver_class.detail, driver_class.detail

    # 28b. A plan from each GATE_CODE path plans the self-suite and no fork check. The
    #      fork kinds are the steps a carried series would add: the pack's series check,
    #      the two patch-branch invariants, the replay, the build and the quality scripts.
    fork_kinds = {"series", "patch-branch-inv2", "patch-branch-inv3", "forks-check",
                  "build", "fork-quality"}
    for path in module.GATE_CODE:
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), [path], None, True,
        )
        kinds = {check.kind for check in planned.checks}
        assert "self-suite" in kinds, (path, sorted(kinds))
        assert not kinds & fork_kinds, (path, sorted(kinds & fork_kinds))

    # 28c. A plan from each MECHANISM path carries every series: the pack's series check
    #      and the patch-branch invariant for each fork. The driver's two paths are the
    #      materializer and keep their own `driver` cause; the structural judge is the
    #      other half.
    for path in module.MECHANISM:
        planned = module.build_plan(
            repo, series, module.Record(base / "plan-state.json"), [path], None, True,
        )
        kinds = [check.kind for check in planned.checks]
        assert "series" in kinds, (path, kinds)
        assert kinds.count("patch-branch-inv2") == len(series), (path, kinds)
        assert "self-suite" not in kinds, (path, kinds)

    # 28d. The demonstration: a fork's patch branch is behind its upstream -- its
    #      merge-check refuses -- and a gate-only change still lands in a pass, because
    #      the gate plans the self-suite and no fork check. The upstream really moves
    #      here (a release tag on the release-following fork), so the drift reason is
    #      live: without the gate-only suppression the plan carries that fork and the
    #      merge-check's refusal fails the pass. The landing pass plans from the batch
    #      base with the upstream audit on, which is exactly this shape.
    before = record(repo)["targets"]["matt/pi-subagents"]["fingerprint"]
    sh(releases, "git", "tag", "v1.0.0")
    (base / "pi-forks-fail").write_text("patch-branch merge-check\n")
    (base / "pi-forks-rc").write_text("1\n")
    (base / "pi-forks-out").write_text(
        "patch/pi-subagents conflicts with the upstream a launch would build\n")
    with (repo / "tools" / "fork-check.py").open("a") as edited:
        edited.write("\n# a gate-only edit the self-suite must judge\n")
    commit(repo, "a gate-only change while a fork is behind its upstream")
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  self-suite" in result.stdout, result.stdout
    assert "run  fork-quality" not in result.stdout, result.stdout
    assert "run  patch-branch-inv2" not in result.stdout, result.stdout
    assert "run  series" not in result.stdout, result.stdout
    # The drift was live, not absent: the gate asked the bridge and recorded the moved
    # upstream, and the fork is named as not planned for the gate-only reason.
    assert record(repo)["targets"]["matt/pi-subagents"]["fingerprint"] != before, record(repo)["targets"]
    assert "the change is confined to the gate's own selection or reporting code" in result.stdout, result.stdout
    fork_calls = [call for call in calls(base, "pi-forks-calls")
                  if "merge-check" in call or "candidate" in call or "verify" in call]
    assert fork_calls == [], fork_calls

    # 28e. The other half: a change to the structural judge still forces every fork
    #      check, so the same behind-upstream fork fails the pass. The judge is a stub
    #      here, so the change is a comment; the routing is what this pins.
    with (repo / "tests" / "pi-patch-series.test.sh").open("a") as edited:
        edited.write("\n# a judge edit\n")
    commit(repo, "a change to the series judge")
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  patch-branch-inv2" in result.stdout, result.stdout
    assert "conflicts with the upstream a launch would build" in result.stdout, result.stdout

# The Go tool directories. Each is built and tested from this checkout by a step of its
# own, so a change under one directory is compiled by the check that names it -- not by
# a sibling's step, and not by every Go tool at once. The tuple here is kept equal to the
# module's GO_TOOL_DIRS -- asserted below -- so a newly registered directory cannot go
# untested. The retired board tool is deliberately absent: the board was replaced by
# tillr, its suite has been red since 2026-11-07, and the owner ruled on 2026-11-10 that
# a tool being retired is not worth a gate step. The tillr modules are present because
# this check is the only thing in the repository that compiles them.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    spec = importlib.util.spec_from_file_location("fork_check_go_tools", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    # DERIVED from the registry, not copied: a directory registered in GO_TOOL_DIRS is
    # tested here by construction, so a new module cannot go untested.
    tool_dirs = module.GO_TOOL_DIRS
    for directory in tool_dirs:
        # A directory with no Go source at all needs creating, and this case is about the plan.
        (repo / directory).mkdir(exist_ok=True)
        (repo / directory / "main.go").write_text("package main\n")
    # A `go` on PATH that answers without building anything: this case is the plan, not
    # the build. The step's argv is asserted whole, which is what a build would obey.
    go_stub = base / "go-stub"
    go_stub.mkdir()
    (go_stub / "go").write_text("#!/bin/sh\nexit 0\n")
    (go_stub / "go").chmod(0o755)
    # The substrate suite lands; the other behavior suites roll. Both still share the
    # build's watch and run the whole suite.
    GO_KINDS = ("go-tool", "go-substrate", "gate-runner")
    # Suite kinds are selected dynamically, so the later literal-Check inventory
    # cannot discover them. Name both declarations here rather than letting a removed
    # kind hide behind the landing-tier default.
    assert set(GO_KINDS) <= set(module.CHECK_KIND_GROUP), module.CHECK_KIND_GROUP
    assert set(GO_KINDS) <= set(module.CHECK_TIER), module.CHECK_TIER
    assert set(GO_KINDS) <= set(module.CHECK_COST_ESTIMATE), module.CHECK_COST_ESTIMATE
    assert set(module.GO_LANDING_SUITE_DIRS) <= set(tool_dirs), module.GO_LANDING_SUITE_DIRS
    assert set(module.GO_TOOL_COST_ESTIMATE) == set(tool_dirs), module.GO_TOOL_COST_ESTIMATE
    assert all(cost > 0 for cost in module.GO_TOOL_COST_ESTIMATE.values())
    assert module.GO_TOOL_PARALLEL == 1, module.GO_TOOL_PARALLEL
    assert {"tools/gate/go-test-edges.json", "tools/gate/go_affected.py"} <= set(module.GO_SHARD_INPUTS)
    assert "tools/tillr-drain" in module.GO_SHARD_DIRS
    for directory in module.GO_SHARD_DIRS:
        command = module.go_tool_command(directory)
        assert ("tools/go-test-shards.py" in command and "-k 6" in command
                and "--cache" in command and "--slot-class proof" in command), command
        watches = module.go_tool_watches(directory, repo)
        assert set(module.GO_SHARD_INPUTS) <= set(watches)
        assert len(watches) == len(set(watches)), watches
    for directory in set(tool_dirs) - set(module.GO_SHARD_DIRS):
        command = module.go_tool_command(directory)
        assert ("GOFLAGS=-trimpath" in command
                and " go test -parallel 1 -timeout 0 ./..." in command
                and "-count=1" not in command), command
        if directory == "tools/tillr-watch":
            assert "--edge-digest github.com/mschulkind-oss/tillr-watch" in command, command
            assert "TILLR_GATE_EDGE_INPUTS=" in command, command
            watches = module.go_tool_watches(directory, repo)
            assert {"tools/gate/go-test-edges.json", "tools/go-test-shards.py"} <= set(watches), watches
            assert len(watches) == len(set(watches)), watches
    shard_planned = module.build_plan(
        repo, [], module.Record(base / "shard-plan-state.json"),
        ["tools/go-test-shards.py"], None, True)
    shard_suites = {check.label for check in shard_planned.checks if check.kind in GO_KINDS}
    assert shard_suites == {
        f"{directory}'s whole suite passes" for directory in (*module.GO_SHARD_DIRS, "tools/tillr-watch")
    }, shard_suites
    gate_suite = next(check for check in shard_planned.checks if check.kind == 'gate-runner')
    assert '&& cd ../..' not in gate_suite.argv[-1], gate_suite.argv
    assert '--slot-class gate' in gate_suite.argv[-1], gate_suite.argv
    assert set(module.GATE_RUNNER_WATCHES) <= set(gate_suite.watches), gate_suite.watches
    assert 'python3 tools/gate/runner-first.test.py' in gate_suite.argv[-1], gate_suite.argv
    assert module.CHECK_COST_ESTIMATE["go-substrate"] == max(
        module.GO_TOOL_COST_ESTIMATE[directory] for directory in module.GO_LANDING_SUITE_DIRS
    )
    assert module.CHECK_COST_ESTIMATE["go-tool"] == max(
        cost for directory, cost in module.GO_TOOL_COST_ESTIMATE.items()
        if directory not in module.GO_LANDING_SUITE_DIRS
    )

    def suite_kind(directory):
        return ("gate-runner" if directory == "tools/tillr-gate" else
                "go-substrate" if directory in module.GO_LANDING_SUITE_DIRS else "go-tool")

    def directory_kinds(directory):
        return (suite_kind(directory),)

    def go_plan(changed):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), changed, None, True,
        )
        return ([check for check in planned.checks if check.kind in GO_KINDS],
                [skip for skip in planned.skips
                 if skip.label.startswith(GO_KINDS)])

    original_path = os.environ["PATH"]
    os.environ["PATH"] = f"{go_stub}:{original_path}"
    try:
        # The substrate owns the store, migration path, and event-kind oracle: its
        # behavior proof must run before landing. The drain is the negative control;
        # its build lands, but its expensive behavior suite remains owed to rolling.
        for directory, expected_suite_kind, suite_tier in (
            ("tools/tillr", "go-substrate", module.TIER_LANDING),
            ("tools/tillr-drain", "go-tool", module.TIER_ROLLING),
        ):
            changed = [f"{directory}/main.go"]
            full = module.build_plan(
                repo, [], module.Record(base / "split-state.json"), changed, None, True,
            )
            suites = [check for check in full.checks
                      if check.label == f"{directory}'s whole suite passes"]
            assert len(suites) == 1 and suites[0].kind == expected_suite_kind, (
                directory, [(check.kind, check.label) for check in suites])
            assert module.check_tier(expected_suite_kind) == suite_tier, expected_suite_kind
            for tier in (module.TIER_LANDING, module.TIER_ROLLING):
                plan = module.build_plan(
                    repo, [], module.Record(base / "split-state.json"), changed, None, True,
                )
                module.apply_tier(plan, tier)
                selected = [check.kind for check in plan.checks
                            if check.watches == module.go_tool_watches(directory, repo)]
                expected = []
                # The rolling tier is the after-merge proof and judges the landing
                # kinds the range selected too (section 59a).
                if tier == suite_tier or tier == module.TIER_ROLLING:
                    expected.append(expected_suite_kind)
                assert selected == expected, (directory, tier, selected)
                # A proof runs the drain's suite once: one rolling command names it, the
                # sharded one. The parity case in tests/go-test-shards.test.py starts no run of it.
                if directory == "tools/tillr-drain" and tier == module.TIER_ROLLING:
                    drain_runs = [check.argv[-1] for check in plan.checks
                                  if check.argv and "tools/tillr-drain" in check.argv[-1]
                                  and ("go-test-shards.py" in check.argv[-1]
                                       or "go test" in check.argv[-1])]
                    assert len(drain_runs) == 1 and "go-test-shards.py" in drain_runs[0], drain_runs
                assert any(skip.owed and skip.kind == "go-tool" for skip in plan.skips) == (
                    directory == "tools/tillr-drain" and tier == module.TIER_LANDING
                ), (directory, tier, plan.skips)

        # 28a. Each directory's change plans that directory's two steps, in order, and
        #      only that one's: the other directory's steps are named as not run, so
        #      neither a missed build nor a second one hides behind a shared label.
        plans = {directory: go_plan([f"{directory}/main.go"]) for directory in tool_dirs}
        for directory in tool_dirs:
            checks, skips = plans[directory]
            assert [check.kind for check in checks] == list(directory_kinds(directory)), (
                f"{directory} plans {[(check.kind, check.label) for check in checks]}; "
                f"skips: {[skip.label for skip in skips]}")
            # The suite step is the WHOLE suite: `-short` would skip the
            # integration cases, which is the blind spot item 570 closed. The build is
            # the other step's job, so this command does not repeat it.
            command = module.go_tool_command(
                directory, slot_class="gate" if directory == "tools/tillr-gate" else "proof"
            )
            if directory == "tools/tillr-gate":
                command += (("" if directory in module.GO_SHARD_DIRS else " && cd ../..")
                            + " && python3 tools/gate/change-gate.test.py"
                            " && python3 tools/gate/runner-first.test.py")
            assert checks[0].argv == ["sh", "-c", command], checks[0].argv
            assert "-short" not in checks[0].argv[-1], checks[0].argv
            assert "go build" not in checks[0].argv[-1], checks[0].argv
            for check in checks:
                expected_watches = module.go_tool_watches(directory, repo)
                assert check.watches == expected_watches, check.watches
            assert checks[0].timeout == module.GO_TOOL_TIMEOUT, checks[0].timeout
            assert checks[0].cost == module.GO_TOOL_COST_ESTIMATE[directory], checks[0].cost
        #      The directory the change did not touch is named as not run, so the plan
        #      accounts for every Go tool it knows.
        for directory in tool_dirs:
            checks, skips = plans[directory]
            expected = [f"{kind} ({other})" for other in tool_dirs if other != directory
                        for kind in directory_kinds(other)]
            assert sorted(skip.label for skip in skips) == sorted(expected), \
                [skip.label for skip in skips]

        #      A path that is neither directory plans no Go step, and both are named.
        checks, skips = go_plan(["bashrc"])
        assert checks == [], [check.label for check in checks]
        assert sorted(skip.label for skip in skips) == sorted(
            f"{kind} ({directory})" for directory in tool_dirs
            for kind in directory_kinds(directory)
        ), [skip.label for skip in skips]
        assert not any(skip.planned for skip in skips), [(skip.label, skip.planned) for skip in skips]

        # 28c. THE ROUTINE SUITE STEP IS THE WHOLE SUITE (board item 570). Every
        #      directory plans a build step and a suite step, and the suite runs the whole
        #      thing: no `-short`, and no second `go-tool-integration` step for any
        #      directory. The integration cases used to be a second step planned
        #      only by `--all`, which the landing path never passes, so no gate ran them
        #      and a red case landed (item 554). The quarantine that declared the skip, the
        #      second step and the `-short` split are gone, so a red in those cases is now
        #      the suite step's verdict.
        for directory in tool_dirs:
            checks, skips = go_plan([f"{directory}/main.go"])
            assert [check.kind for check in checks] == list(directory_kinds(directory)), \
                (directory, [check.kind for check in checks])
            assert checks[0].label == f"{directory}'s whole suite passes", checks[0].label
            assert "-short" not in checks[0].argv[-1], checks[0].argv
            assert not [skip for skip in skips if "integration" in skip.label], \
                (directory, [skip.label for skip in skips])
        #      The declaration, the second step's command and the directory tuple that
        #      named the split are deleted, not merely unused: a reader must not find a
        #      quarantine that no longer suppresses anything.
        for gone in ("GO_TOOL_INTEGRATION_QUARANTINE", "GO_TOOL_INTEGRATION_DIRS",
                     "GO_TOOL_INTEGRATION_TIMEOUT", "go_tool_integration_command",
                     "go_tool_full_command", "go_tool_integration_warning"):
            assert not hasattr(module, gone), gone
    finally:
        os.environ["PATH"] = original_path

    # 28e. THE PLAN NAMES ITS CHECKS (board item 299). A landing refuses a green whose
    #      plan did not run a check the change's own paths reach, so the plan line has to
    #      name the kinds it planned and the kinds its diff selected: a tools/tillr-land change
    #      plans and requires go-tool, and a check the diff selects that this machine could
    #      not answer stays REQUIRED (so the landing cannot read the skip as a pass) while
    #      dropping out of the planned set. The specimen is a registered Go tool directory;
    #      the retired board tool left GO_TOOL_DIRS when the board was retired, so it plans no
    #      Go step to name.
    plan = module.build_plan(
        repo, [], module.Record(base / "names-state.json"), ["tools/tillr-land/main.go"], None, True,
    )
    planned, required = module.plan_kinds(plan)
    assert "go-build" not in planned, planned
    assert "go-tool" in planned, planned
    assert set(required) <= set(planned), (planned, required)
    assert {"file-size"} <= set(required), required
    line_buffer = io.StringIO()
    with contextlib.redirect_stdout(line_buffer):
        module.print_plan(module.Record(base / "names-state.json"), plan, "0" * 40, "1" * 40)
    plan_line = line_buffer.getvalue().strip().splitlines()[-1]
    plan_fields = dict(field.split("=", 1) for field in plan_line.split()[2:])
    assert "go-build" not in plan_fields["checks"].split(","), plan_fields
    assert "go-tool" in plan_fields["checks"].split(","), plan_fields
    assert set(plan_fields["required"].split(",")) <= set(plan_fields["checks"].split(",")), plan_fields
    assert plan_fields["checks"] == ",".join(planned), plan_fields
    assert plan_fields["required"] == ",".join(required), plan_fields

    # 28b. A machine without `go` names the skip for the directory the change touched --
    #      the step is not silently dropped because another directory's tool was missing.
    original_which = module.shutil.which
    module.shutil.which = lambda name, *a, **k: None if name == "go" else original_which(name, *a, **k)
    try:
        for directory in tool_dirs:
            checks, skips = go_plan([f"{directory}/main.go"])
            assert checks == [], [check.label for check in checks]
            expected = [(f"{kind} ({directory})", True) for kind in directory_kinds(directory)] + [
                (f"{kind} ({other})", False) for other in tool_dirs if other != directory
                for kind in directory_kinds(other)
            ]
            assert sorted((skip.label, skip.planned) for skip in skips) == sorted(expected), \
                [(skip.label, skip.planned, skip.why) for skip in skips]
    finally:
        module.shutil.which = original_which

    # 28f. A check the diff selected that this machine could not answer stays REQUIRED:
    #      the plan names it there and not among the checks that ran, which is exactly the
    #      gap a landing refuses rather than reading the skip as a pass. The specimen is a
    #      registered Go tool directory, because a retired one selects no Go step to leave
    #      unanswered.
    original_which = module.shutil.which
    module.shutil.which = lambda name, *a, **k: None if name == "go" else original_which(name, *a, **k)
    try:
        unanswered = module.build_plan(
            repo, [], module.Record(base / "names-state.json"), ["tools/tillr-land/queue.go"], None, True,
        )
        planned, required = module.plan_kinds(unanswered)
        assert "go-build" not in planned, planned
        assert "go-tool" not in planned, planned
        assert "go-build" not in required, required
        assert "go-tool" in required, required
        substrate = module.build_plan(
            repo, [], module.Record(base / "names-state.json"), ["tools/tillr/store/store.go"], None, True,
        )
        module.apply_tier(substrate, module.TIER_LANDING)
        planned, required = module.plan_kinds(substrate)
        assert "go-substrate" not in planned and "go-substrate" in required, (planned, required)
        assert any(skip.kind == "go-substrate" and skip.planned and not skip.owed
                   for skip in substrate.skips), substrate.skips
    finally:
        module.shutil.which = original_which

    # 28d. The merge queue's module is registered by name, and pinned in both directions:
    #      a path under `tools/tillr-land/` plans that module's own build and unit tests, and a
    #      path outside it plans none -- the queue's suite is never answered by a sibling
    #      tool's step, and it is never left to rot with nothing compiling it.
    checks, skips = go_plan(["tools/tillr-land/queue.go"])
    # The suite label names the whole suite, which is what the step runs: a label that read
    # as a subset let a landing cite this pass as less than the package's verdict (item
    # 544). The build label names the compile, which is the other step.
    assert [check.label for check in checks] == [
        "tools/tillr-land's whole suite passes"], [check.label for check in checks]
    assert checks[0].argv == ["sh", "-c", module.go_tool_command("tools/tillr-land")], checks[0].argv
    for outside in ("bashrc", "tools/tillr-gate/main.go"):
        checks, skips = go_plan([outside])
        assert not [check for check in checks if "tillr-land" in check.label], \
            (outside, [check.label for check in checks])
        for kind in directory_kinds("tools/tillr-land"):
            assert {skip.label: skip.planned for skip in skips}.get(
                f"{kind} (tools/tillr-land)") is False, (outside, [skip.label for skip in skips])

# 28g2. A GO SUITE READS EVERY IN-REPO MODULE IT BUILDS AGAINST. tillr-view's module
#      replaces tillr-drain with `../tillr-drain`, and its suite enumerates the drain's
#      exported hold reasons; its watches named only `tools/tillr-view/`, so on 2026-11-18
#      a drain-only landing that added two reasons selected no tillr-view suite and the
#      trunk went red behind it. A module's watches are its own directory plus every
#      directory its go.mod `replace`s with a relative path: an unpublished `v0.0.0` module
#      cannot build without that directive, so the directives are the complete in-repo
#      dependency set, read from the tree being planned. Pinned on a fixture (the plan
#      selects the dependent's suite) and on this repository's own modules.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    spec = importlib.util.spec_from_file_location("fork_check_go_deps", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    for directory in module.GO_TOOL_DIRS:
        (repo / directory).mkdir(exist_ok=True)
        (repo / directory / "main.go").write_text("package main\n")
    (repo / "tools/tillr-view/go.mod").write_text(
        "module example.invalid/tillr-view\n\ngo 1.26.0\n\n"
        "require example.invalid/tillr-drain v0.0.0\n\n"
        "replace example.invalid/tillr-drain => ../tillr-drain\n"
        "replace example.invalid/elsewhere => /opt/elsewhere\n")
    watches = module.go_tool_watches("tools/tillr-view", repo)
    assert "tools/tillr-drain/" in watches, watches
    # An absolute or out-of-tree replacement is not a repository path a diff can name.
    assert not [watch for watch in watches if "elsewhere" in watch], watches
    go_stub = base / "go-stub"
    go_stub.mkdir()
    (go_stub / "go").write_text("#!/bin/sh\nexit 0\n")
    (go_stub / "go").chmod(0o755)
    original_path = os.environ["PATH"]
    os.environ["PATH"] = f"{go_stub}:{original_path}"
    try:
        planned = module.build_plan(
            repo, [], module.Record(base / "deps-state.json"),
            ["tools/tillr-drain/drain/schema.go"], None, True)
    finally:
        os.environ["PATH"] = original_path
    labels = {check.label for check in planned.checks}
    assert "tools/tillr-view's whole suite passes" in labels, labels
    assert "tools/tillr-drain's whole suite passes" in labels, labels
    # Negative control: a module that replaces nothing still selects only its own suite.
    assert "tools/tillr-cli's whole suite passes" not in labels, labels
    # This repository: every relative `replace` in each registered module's go.mod is in
    # that module's watches, and the incident's pair is named outright.
    for directory in module.GO_TOOL_DIRS:
        watches = module.go_tool_watches(directory, ROOT)
        assert directory + "/" in watches or directory == "tools/tillr-gate", (directory, watches)
        for line in (ROOT / directory / "go.mod").read_text().splitlines():
            if "=>" in line and line.split("=>", 1)[1].strip().startswith("../"):
                target = line.split("=>", 1)[1].split()[0]
                dependency = os.path.normpath(os.path.join(directory, target)) + "/"
                assert dependency in watches, (directory, dependency, watches)
    assert "tools/tillr-drain/" in module.go_tool_watches("tools/tillr-view", ROOT)
    assert "tools/tillr/" in module.go_tool_watches("tools/tillr-gate", ROOT)

# 28h. THE ROUTINE SUITE STEP IS THE WHOLE SUITE (board item 570), AND THE BUILD IS ITS
#      OWN STEP. The suite's label says so, and a red in the merge queue's integration
#      cases is the suite step's verdict: there is no second `go-tool-integration` step for
#      `--all` to plan and no "the last full audit is red" warning to soften a routine
#      pass, because the routine pass is the full audit of that package. The record keeps
#      no `last_full_audit` field: the split it existed to remember is gone. The build and
#      the suite are separate steps, so a red in one is blamed on that one and the other
#      still reports its own result. The specimen is `tools/tillr-land`, a registered Go tool
#      directory; the retired board tool left GO_TOOL_DIRS when the board was retired, so it
#      plans no Go step to attribute.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    # A `go` on PATH that answers without building: the case is the report, not a build.
    # It passes the build and the test-compile and fails the behavior suite, so the suite
    # step is red while the build step is green.
    go_stub = base / "go-stub"
    go_stub.mkdir()
    (go_stub / "go").write_text(
        "#!/bin/sh\n"
        'if [ "$1" = "test" ]; then\n'
        '  case "$*" in *"-run ^$"*) exit 0 ;; esac\n'
        "  echo '--- FAIL: TestFirstReplayFailure (0.00s)'\n"
        "  echo '    queue_test.go:42: first assertion failed'\n"
        "  echo '--- FAIL: TestSecondReplayFailure (0.00s)'\n"
        "  echo '    queue_test.go:54: second assertion failed'\n"
        "  exit 1\n"
        "fi\n"
        "exit 0\n"
    )
    (go_stub / "go").chmod(0o755)
    env = {**env, "PATH": f"{go_stub}:{env['PATH']}"}

    # A verified baseline first, so the lander edit below is the diff the plan reads.
    assert run(repo, env).returncode == 0
    (repo / "tools" / "tillr-land").mkdir(exist_ok=True)
    (repo / "tools" / "tillr-land" / "queue.go").write_text("package main\n")
    commit(repo, "change the lander")

    # A routine run plans the build and the whole suite, and is red on the suite alone.
    # No second step is planned, and the report does not claim a subset ran.
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "tools/tillr-land's whole suite passes" in result.stdout, result.stdout
    # The verdict names the suite step, and only it: the build step passed.
    assert "go-tool: tools/tillr-land's whole suite passes" in result.stdout, result.stdout
    assert "go-build: tools/tillr-land" not in result.stdout, result.stdout
    assert "go-tool-integration" not in result.stdout, result.stdout
    assert "-short unit subset" not in result.stdout, result.stdout
    assert "last_full_audit" not in record(repo), record(repo)

    before_replay = record_log(repo).read_bytes()
    replay = run(repo, env, "--replay", "--only", "go-tool:tools/tillr-land's whole suite passes")
    assert replay.returncode == 1, replay.stdout + replay.stderr
    for name in ("TestFirstReplayFailure", "TestSecondReplayFailure"):
        assert f"--- FAIL: {name}" in replay.stdout, replay.stdout
        assert f"case: {name} --" in replay.stdout, replay.stdout
    assert record_log(repo).read_bytes() == before_replay, "replay rewrote the gate record"

# 29. The rank-order suite runs in the gate. It proves a capacity-queued dispatch follows
#     the pile's rank -- it imports the production pump and the ordering helper from the
#     `.forks/pi-subagents` working copy and reads the queue's own rank alphabet
#     -- and it was hand-run only, so the gate never planned it. Its two inputs
#     are the suite and the series that builds the working copy. Without the
#     working copy every case skips and `node --test` still exits 0, so a missing checkout
#     refuses the run (exit 2) rather than a green headline over eight skipped cases, and
#     a failing run is a verdict (FAILED), not "not verified".
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    rank_test = "yolo-packs/matt/tests/pi-subagents-rank-order.test.mjs"
    series = "yolo-packs/matt/patches/pi-subagents/0001-x.patch"
    # The working copy the suite imports from, untracked exactly as the real `.forks/` is
    # (the fixture already ignores `.forks/` and carries the imported file; this block
    # overwrites both with the recording versions).
    (repo / ".gitignore").write_text(".forks/\n")
    checkout = repo / ".forks" / "pi-subagents" / "src" / "runs" / "background"
    checkout.mkdir(parents=True, exist_ok=True)
    (checkout / "queue-rank.ts").write_text("export const compareQueueEntries = () => 0;\n")
    (checkout / "active-async-capacity.ts").write_text("export const pump = () => {};\n")
    # The stub records that the gate ran it, so a case proves it executes rather than
    # merely being listed.
    (repo / rank_test).write_text(
        "import { appendFileSync } from 'node:fs';\n"
        f"appendFileSync('{base}/rank-order-calls', 'rank\\n');\n"
    )
    commit(repo, "the rank-order suite, its alphabet and its working copy")

    spec = importlib.util.spec_from_file_location("fork_check_rank_order", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    def rank_order_plan(changed):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), changed, None, True,
        )
        return [check for check in planned.checks if check.kind == "rank-order"], planned

    # 29a. Nothing it watches changed: no step, and the report names the skip.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the rank alphabet and its suite are unchanged" in result.stdout, result.stdout
    assert calls(base, "rank-order-calls") == [], calls(base, "rank-order-calls")

    # 29b. Each of the two inputs plans it, with the suite's own command and its tracked
    #      path watched, so a dirty one of them refuses instead of judging the tree.
    for path in (rank_test, series):
        checks, planned = rank_order_plan([path])
        assert len(checks) == 1, (path, [c.kind for c in planned.checks])
        assert checks[0].argv == ["node", "--test", rank_test], checks[0].argv
        assert checks[0].watches == (rank_test,), checks[0].watches
        assert checks[0].timeout == 600, checks[0].timeout

    # 29c. Paths it does not read never select it -- a sibling suite, a sibling extension,
    #      another series, and a file that merely shares its name.
    for path in ("bashrc", "yolo-packs/matt/tests/bedrock-context.test.mjs",
                 "yolo-packs/matt/files/extensions/bedrock-context.ts",
                 "yolo-packs/matt/patches/pi-archimedes/0001-x.patch",
                 rank_test + ".bak"):
        checks, planned = rank_order_plan([path])
        assert checks == [], (path, [c.kind for c in planned.checks])
        assert any(skip.label == "rank-order (node --test)" and not skip.planned
                   for skip in planned.skips), planned.skips

    # 29d. The gate executes it, once per input.
    for path, edit in ((series, "\n"),
                       (rank_test, "// the suite, edited\n")):
        clear(base, "rank-order-calls")
        with (repo / path).open("a") as edited:
            edited.write(edit)
        commit(repo, f"edit {path} for the rank-order gate")
        result = run(repo, env)
        assert result.returncode == 0, result.stdout + result.stderr
        assert "run  rank-order " in result.stdout, result.stdout
        assert calls(base, "rank-order-calls") == ["rank"], calls(base, "rank-order-calls")

    # 29e. A failing suite is a verdict about the change, so the run FAILS and the baseline
    #      does not advance -- not "not verified", which is a check that could not answer.
    verified = record(repo)["baseline"]["commit"]
    (repo / rank_test).write_text("throw new Error('rank order assertion failed');\n")
    commit(repo, "fail the rank-order suite")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  rank-order " in result.stdout and "FAILED" in result.stdout, result.stdout
    assert "rank order assertion failed" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

    # 29f. A machine without `node` cannot answer it: a named skip, not a failure.
    original_which = module.shutil.which
    module.shutil.which = lambda name, *a, **k: None if name == "node" else original_which(name, *a, **k)
    try:
        checks, planned = rank_order_plan([rank_test])
    finally:
        module.shutil.which = original_which
    assert checks == [], [c.kind for c in planned.checks]
    assert any(skip.label == "rank-order (node --test)" and skip.planned
               and skip.why == "node is unavailable here" for skip in planned.skips), planned.skips

    # 29g. A missing working copy refuses the run: the suite would skip every case and
    #      still exit 0, so the step carries the file it imports in `needs`, with the
    #      setup command as the remedy, instead of planning a skip that reads as a pass.
    shutil.rmtree(repo / ".forks" / "pi-subagents" / "src")
    checks, planned = rank_order_plan([rank_test])
    assert len(checks) == 1, [c.kind for c in planned.checks]
    assert checks[0].needs == (
        str(repo / ".forks" / "pi-subagents" / "src" / "runs" / "background" / "queue-rank.ts"),
    ), checks[0].needs
    assert checks[0].remedy == "bin/pi-forks setup --fork pi-subagents", checks[0].remedy

    # 29h. The refusal is the exit-2 channel, before anything runs: the missing path and
    #      its remedy are named, the harness never executes, and the baseline stays where
    #      the last verified run left it.
    with (repo / rank_test).open("a") as edited:
        edited.write("// the suite, edited with no checkout to read\n")
    commit(repo, "edit the suite with no checkout to read")
    clear(base, "rank-order-calls")
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "fork-check: REFUSED" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" not in result.stdout, result.stdout
    assert "queue-rank.ts" in result.stdout, result.stdout
    assert "bin/pi-forks setup --fork pi-subagents" in result.stdout, result.stdout
    assert "fork-check: NOT VERIFIED" in result.stdout, result.stdout
    assert calls(base, "rank-order-calls") == [], calls(base, "rank-order-calls")
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "refused", record(repo)["runs"][-1]

_shard_boundary("forensics-budget-and-loop", 22)

# 31. tests/session-forensics.test.py is the only assertion on the hang-analysis CLI's own
#     report -- judge passes, slow tool calls, telemetry gaps, a turn that never closed --
#     and it landed with the tool while no step here named it, so the gate would never run
#     it and it could rot. It reads the CLI it exercises, so the test and anything under
#     tools/session-forensics/ plan it, and nothing else does.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    forensics_test = "tests/session-forensics.test.py"
    forensics_tool = "tools/session-forensics/report.py"
    (repo / "tools" / "session-forensics").mkdir()
    (repo / forensics_tool).write_text("def report():\n    return {}\n")
    # The stub records that the gate ran it, so a case proves the step executes rather
    # than merely being listed.
    (repo / forensics_test).write_text(
        "from pathlib import Path\n"
        f"Path('{base}/session-forensics-calls').open('a').write('forensics\\n')\n"
    )
    commit(repo, "the session-forensics test and the tool it reads")

    spec = importlib.util.spec_from_file_location("fork_check_session_forensics", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    def forensics_plan(changed):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), changed, None, True,
        )
        return [check for check in planned.checks if check.kind == "session-forensics"], planned

    # 31a. Nothing it reads changed: no step, and the report names the skip.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the session-forensics test and the tool it reads are unchanged" in result.stdout, result.stdout
    assert calls(base, "session-forensics-calls") == [], calls(base, "session-forensics-calls")

    # 31b. Each input plans it, with the command the justfile would run and both watched
    #      paths, so a dirty one of them refuses instead of judging the tree.
    for path in (forensics_test, forensics_tool):
        checks, planned = forensics_plan([path])
        assert len(checks) == 1, (path, [c.kind for c in planned.checks])
        assert checks[0].argv == ["python3", forensics_test], checks[0].argv
        assert checks[0].watches == (forensics_test, "tools/session-forensics/"), checks[0].watches
        assert checks[0].timeout == 300, checks[0].timeout

    # 31c. Paths it does not read never select it -- another test, a sibling tool, and a
    #      file that merely shares its name.
    for path in ("bashrc", "tests/yolo-config.test.sh", "tools/pi-profiler/main.go",
                 forensics_test + ".bak"):
        checks, planned = forensics_plan([path])
        assert checks == [], (path, [c.kind for c in planned.checks])
        assert any(skip.label == "session-forensics (python3 tests/session-forensics.test.py)"
                   and not skip.planned for skip in planned.skips), planned.skips

    # 31d. The gate executes it, once per input.
    for path, edit in ((forensics_test, "\n# the test, edited\n"),
                       (forensics_tool, "\n# the tool, edited\n")):
        clear(base, "session-forensics-calls")
        with (repo / path).open("a") as edited:
            edited.write(edit)
        commit(repo, f"edit {path} for the session-forensics gate")
        result = run(repo, env)
        assert result.returncode == 0, result.stdout + result.stderr
        assert "run  session-forensics " in result.stdout, result.stdout
        assert calls(base, "session-forensics-calls") == ["forensics"], calls(base, "session-forensics-calls")

    # 31e. A failing case is a verdict about the change, so the run FAILS and the baseline
    #      does not advance -- not "not verified", which is a check that could not answer.
    verified = record(repo)["baseline"]["commit"]
    (repo / forensics_test).write_text("raise SystemExit('session-forensics assertion failed')\n")
    commit(repo, "fail the session-forensics test")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  session-forensics " in result.stdout and "FAILED" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

# 31b. tests/session-budget.test.py is the only assertion on the session-budget CLI's own
#      report -- prompt, lag and telemetry against their stated bounds -- and it landed with
#      the tool, so the gate must run it or it could rot. It reads the CLI it exercises, so
#      the test and anything under tools/session-budget/ plan it, and nothing else does.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    budget_test = "tests/session-budget.test.py"
    budget_tool = "tools/session-budget/session-budget"
    (repo / "tools" / "session-budget").mkdir()
    (repo / budget_tool).write_text("#!/usr/bin/env python3\n")
    (repo / budget_test).write_text(
        "from pathlib import Path\n"
        f"Path('{base}/session-budget-calls').open('a').write('budget\\n')\n"
    )
    commit(repo, "the session-budget test and the tool it reads")

    spec = importlib.util.spec_from_file_location("fork_check_session_budget", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    def budget_plan(changed):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), changed, None, True,
        )
        return [check for check in planned.checks if check.kind == "session-budget"], planned

    # 31b-a. Nothing it reads changed: no step, and the report names the skip.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the session-budget test and the tool it reads are unchanged" in result.stdout, result.stdout
    assert calls(base, "session-budget-calls") == [], calls(base, "session-budget-calls")

    # 31b-b. Each input plans it, with the command the justfile would run and both watched
    #        paths, so a dirty one of them refuses instead of judging the tree.
    for path in (budget_test, budget_tool):
        checks, planned = budget_plan([path])
        assert len(checks) == 1, (path, [c.kind for c in planned.checks])
        assert checks[0].argv == ["python3", budget_test], checks[0].argv
        assert checks[0].watches == (budget_test, "tools/session-budget/"), checks[0].watches
        assert checks[0].timeout == 300, checks[0].timeout

    # 31b-c. Paths it does not read never select it.
    for path in ("bashrc", "tests/session-forensics.test.py", "tools/pi-profiler/main.go",
                 budget_test + ".bak"):
        checks, planned = budget_plan([path])
        assert checks == [], (path, [c.kind for c in planned.checks])
        assert any(skip.label == "session-budget (python3 tests/session-budget.test.py)"
                   and not skip.planned for skip in planned.skips), planned.skips

    # 31b-d. The gate executes it, once per input.
    for path, edit in ((budget_test, "\n# the test, edited\n"),
                       (budget_tool, "\n# the tool, edited\n")):
        clear(base, "session-budget-calls")
        with (repo / path).open("a") as edited:
            edited.write(edit)
        commit(repo, f"edit {path} for the session-budget gate")
        result = run(repo, env)
        assert result.returncode == 0, result.stdout + result.stderr
        assert "run  session-budget " in result.stdout, result.stdout
        assert calls(base, "session-budget-calls") == ["budget"], calls(base, "session-budget-calls")

    # 31b-e. A failing case is a verdict about the change, so the run FAILS and the baseline
    #        does not advance.
    verified = record(repo)["baseline"]["commit"]
    (repo / budget_test).write_text("raise SystemExit('session-budget assertion failed')\n")
    commit(repo, "fail the session-budget test")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  session-budget " in result.stdout and "FAILED" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

# 31d. tests/tillr-docs.test.py runs two regression suites as child processes (`tillr-build`
#      and `tillr-scratch`), and `tests/tillr-build.test.py` reads `.justfile`.
#      tests/tillr-roll.test.py and tests/tillr-keep.test.py have their own lightweight
#      `tillr-roll` step watching `.tillr.toml`, `.justfile`, `tools/tillr-roll/` and both
#      suites, so a `.tillr.toml` edit runs only `tillr-roll` and not `tillr-docs` (which
#      rebuilds eight Go binaries).
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    spec = importlib.util.spec_from_file_location("fork_check_tillr_docs", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    (repo / "tests").mkdir(exist_ok=True)
    (repo / module.TILLR_DOCS_TEST).write_text("")
    for roll_test in module.TILLR_ROLL_TESTS:
        (repo / roll_test).write_text("")
    commit(repo, "the tillr documentation and roll suites")

    def docs_checks(changed):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), changed, None, True,
        )
        return [check for check in planned.checks if check.kind == "tillr-docs"], planned

    def roll_checks(changed):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), changed, None, True,
        )
        return [check for check in planned.checks if check.kind == "tillr-roll"], planned

    if shutil.which("go"):
        for path in (".justfile", "tests/tillr-build.test.py", "tests/tillr-scratch.test.py"):
            checks, planned = docs_checks([path])
            assert len(checks) == 1, (path, [check.kind for check in planned.checks])
            assert any(module.watch_matches(path, watch) for watch in checks[0].watches), \
                (path, checks[0].watches)
        # `.tillr.toml`, `tests/tillr-roll.test.py`, `tests/tillr-keep.test.py`, and
        # `tools/tillr-roll/` select `tillr-roll` and NOT `tillr-docs`.
        for path in (".tillr.toml", "tests/tillr-roll.test.py", "tests/tillr-keep.test.py",
                     ".justfile", "tools/tillr-roll/tillr-roll.py"):
            r_checks, r_planned = roll_checks([path])
            assert len(r_checks) == 1, (path, [check.kind for check in r_planned.checks])
            assert any(module.watch_matches(path, watch) for watch in r_checks[0].watches), \
                (path, r_checks[0].watches)
        for path in (".tillr.toml", "tests/tillr-roll.test.py", "tests/tillr-keep.test.py",
                     "tools/tillr-roll/tillr-roll.py", "bashrc"):
            d_checks, d_planned = docs_checks([path])
            assert d_checks == [], (path, [check.kind for check in d_planned.checks])
        r_checks, r_planned = roll_checks(["bashrc"])
        assert r_checks == [], [check.kind for check in r_planned.checks]
    # The guard that keeps the list honest: every suite the real documentation test starts
    # as a child process is one of its watches.
    started = set(re.findall(r'ROOT / "(tests/[^"]+\.py)"',
                             (Path(__file__).resolve().parent / "tillr-docs.test.py").read_text()))
    assert started, "tests/tillr-docs.test.py starts no suite the guard can read"
    missing = sorted(started - set(module.TILLR_DOCS_WATCHES))
    assert not missing, f"tests/tillr-docs.test.py runs {missing}, which tillr-docs does not watch"

# 31c. tools/tillr-loop/test.py is the loop adapter's own suite, and it asserted the
#      adapter a refactor had replaced: the gate's `--no-wait`, the lander's deleted
#      `enqueue`, the registry's absolute path. Nothing ran it, so twelve cases described
#      a front end nobody had. A change to the adapter directory plans it, and nothing
#      outside that directory does -- a sibling tillr module, a directory whose name only
#      starts with the same words, and the module's own path without its slash.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    loop_test = "tools/tillr-loop/test.py"
    loop_adapter = "tools/tillr-loop/tillr-loop"
    (repo / "tools" / "tillr-loop").mkdir()
    (repo / loop_adapter).write_text("#!/usr/bin/env python3\n")
    # The stub records that the gate ran it, so a case proves the step executes rather
    # than merely being listed.
    (repo / loop_test).write_text(
        "from pathlib import Path\n"
        f"Path('{base}/tillr-loop-calls').open('a').write('loop\\n')\n"
    )
    commit(repo, "the loop adapter and its own suite")

    spec = importlib.util.spec_from_file_location("fork_check_tillr_loop", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    def loop_plan(changed):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), changed, None, True,
        )
        return [check for check in planned.checks if check.kind == "tillr-loop"], planned

    # 31c-a. Nothing it reads changed: no step, and the report names the skip.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the loop adapter, its suite and its README are unchanged" in result.stdout, result.stdout
    assert calls(base, "tillr-loop-calls") == [], calls(base, "tillr-loop-calls")

    # 31c-b. The adapter directory selects it, exactly once, with the command the gate
    #        runs and the directory it watches.
    for path in (loop_adapter, loop_test):
        checks, planned = loop_plan([path])
        assert len(checks) == 1, (path, [c.kind for c in planned.checks])
        assert checks[0].argv == ["python3", loop_test], checks[0].argv
        assert checks[0].watches == ("tools/tillr-loop/",), checks[0].watches
        assert checks[0].timeout == 300, checks[0].timeout

    # 31c-c. Paths outside the directory never select it.
    for path in ("bashrc", "tools/tillr-gate/main.go", "tools/tillr-loop-extra/test.py",
                 "tools/tillr-loop"):
        checks, planned = loop_plan([path])
        assert checks == [], (path, [c.kind for c in planned.checks])
        assert any(skip.label == "tillr-loop (python3 tools/tillr-loop/test.py)"
                   and not skip.planned for skip in planned.skips), planned.skips

    # 31c-d. The gate executes it.
    clear(base, "tillr-loop-calls")
    with (repo / loop_adapter).open("a") as edited:
        edited.write("\n# the adapter, edited\n")
    commit(repo, "edit the loop adapter for the tillr-loop gate")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  tillr-loop " in result.stdout, result.stdout
    assert calls(base, "tillr-loop-calls") == ["loop"], calls(base, "tillr-loop-calls")

    # 31c-e. A failing case is a verdict about the change: the run FAILS and the baseline
    #        does not advance.
    verified = record(repo)["baseline"]["commit"]
    (repo / loop_test).write_text("raise SystemExit('tillr-loop assertion failed')\n")
    commit(repo, "fail the loop adapter's own suite")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  tillr-loop " in result.stdout and "FAILED" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

print("fork-check tillr-loop: a tools/tillr-loop change plans the loop adapter's own suite "
      "and runs it, a failing case is a verdict, and a path outside that directory plans none")

_shard_boundary("liveness-and-worktrees", 24)

# 32. tools/session-liveness is the headless liveness harness: from a state a session can
#     reach, does it return to idle within a bound, and does a later send-now start work? It
#     landed with the fork fix it measures and nothing else executes it. A verdict is a
#     function of the tree, never of ambient state (D38), so it plans TWO steps: a candidate
#     checkout built from this tree's pi series, and the harness run against that checkout
#     through PI_FORK. The shared `.forks/pi` is only a construction substrate -- the candidate
#     is cloned from it by ref -- so a managed worktree without one refuses the run with the
#     setup command, never runs either step, and never reads the harness's exit 127 as a
#     pass. A failing spec is the verdict the harness exists to make, and exit 127 at run
#     time is an inability to answer rather than a verdict.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    # `.forks/` is gitignored exactly as it is here, so the checkout the harness reads can
    # never appear in the diff the plan is built from.
    (repo / ".gitignore").write_text(".forks/\n")
    checkout = repo / ".forks" / "pi" / "packages" / "coding-agent" / "src"
    checkout.mkdir(parents=True)
    (checkout / "session.ts").write_text("export class AgentSession {}\n")
    harness = "tools/session-liveness/run"
    specs = "tools/session-liveness/open-run.test.ts"
    series = "yolo-packs/pi-fork/patches/0149-fix-agent-session.patch"
    (repo / "tools" / "session-liveness").mkdir()
    (repo / specs).write_text("// the open-run spec\n")
    (repo / "yolo-packs" / "pi-fork" / "patches").mkdir(parents=True)
    (repo / series).write_text("Subject: [PATCH] bound every abandonment\n")
    # The stub records that the gate ran it, so a case proves the step executes rather than
    # merely being listed.
    (repo / harness).write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$0" >> "{base}/session-liveness-calls"\n'
        "exit 0\n"
    )
    commit(repo, "the liveness harness and the checkout it reads")

    spec = importlib.util.spec_from_file_location("fork_check_session_liveness", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    def liveness_plan(changed):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), changed, None, True,
        )
        return [check for check in planned.checks if check.kind == "session-liveness"], planned

    # 32a. Nothing it reads changed: no step, and the report names the skip.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the liveness harness is unchanged" in result.stdout, result.stdout
    assert calls(base, "session-liveness-calls") == [], calls(base, "session-liveness-calls")

    # 32b. Each input plans the construction and the run, in that order: the checkout is built
    #      from the tree's series and the harness is handed it through PI_FORK, so what the
    #      verdict rests on is the tree rather than whatever the shared working copy holds.
    judged = repo / ".forks" / "candidate" / "pi"
    for path in (harness, specs, series):
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"), [path], None, True,
        )
        kinds = [check.kind for check in planned.checks]
        assert kinds.count("session-liveness") == 1, (path, kinds)
        build = planned.checks[kinds.index("fork-construct")]
        run_check = planned.checks[kinds.index("session-liveness")]
        assert kinds.index("fork-construct") < kinds.index("session-liveness"), kinds
        assert build.argv == [str(repo / "bin" / "pi-forks"), "candidate", "--fork", "pi", "--build"], build.argv
        assert build.writes == (str(judged),), build.writes
        assert build.needs == (str(repo / ".forks" / "pi"),), build.needs
        assert build.remedy == "bin/pi-forks setup --fork pi", build.remedy
        assert build.env == module.series_env(repo), build.env
        assert run_check.argv == ["sh", harness], run_check.argv
        assert run_check.env == {"PI_FORK": str(judged)}, run_check.env
        # The construction and the run are the same resource: the plan orders them, and a
        # concurrent run cannot start the harness while its checkout is still being built.
        assert run_check.writes == (str(judged),), run_check.writes
        # The candidate is what the step builds, so it can be no plan-time need: a need on a
        # path no step has written yet would refuse the run before the build could happen.
        assert run_check.needs == (), run_check.needs
        assert run_check.watches == ("tools/session-liveness/", "yolo-packs/pi-fork/patches/"), run_check.watches
        assert run_check.timeout == 600, run_check.timeout

    # 32c. Paths it does not read never select it -- another tool's test, a sibling Go tool,
    #      a file that merely shares the directory's name, a sibling pack's series, and a file
    #      beside the pi-fork series rather than in it.
    for path in ("bashrc", "tests/session-forensics.test.py", "tools/pi-profiler/main.go",
                 "tools/session-liveness-notes.md",
                 "yolo-packs/matt/patches/pi-subagents/0001-fleet.patch",
                 "yolo-packs/pi-fork/README.md"):
        checks, planned = liveness_plan([path])
        assert checks == [], (path, [c.kind for c in planned.checks])
        assert any(skip.label == "session-liveness (tools/session-liveness/run)"
                   and not skip.planned for skip in planned.skips), planned.skips

    # 32d. The gate builds the checkout and then executes the harness, once per input, and the
    #      build is what the run reads: the run script's PI_FORK names the candidate.
    for path, edit in ((harness, "\n# the run script, edited\n"),
                       (specs, "// the spec, edited\n"),
                       (series, "+a series line\n")):
        clear(base, "session-liveness-calls", "pi-forks-calls")
        with (repo / path).open("a") as edited:
            edited.write(edit)
        commit(repo, f"edit {path} for the session-liveness gate")
        result = run(repo, env)
        assert result.returncode == 0, result.stdout + result.stderr
        assert "run  fork-construct " in result.stdout, result.stdout
        assert "run  session-liveness " in result.stdout, result.stdout
        forks_calls = calls(base, "pi-forks-calls")
        assert forks_calls[-1] == "candidate --fork pi --build", forks_calls
        assert calls(base, "session-liveness-calls") == [harness], calls(base, "session-liveness-calls")

    # 32e. A machine without `node` cannot answer it: a named skip, not a failure, and no
    #      construction is planned either -- building a checkout nothing will judge is work
    #      with no verdict at the end of it.
    original_which = module.shutil.which
    module.shutil.which = lambda name, *a, **k: None if name == "node" else original_which(name, *a, **k)
    try:
        checks, planned = liveness_plan([harness])
    finally:
        module.shutil.which = original_which
    assert checks == [], [c.kind for c in planned.checks]
    assert any(skip.label == "session-liveness (tools/session-liveness/run)" and skip.planned
               and skip.why == "node is unavailable here" for skip in planned.skips), planned.skips

    # 32f. A construction substrate it cannot find refuses the run (exit 2) with the command
    #      that provides it, and neither step executes: a step that judged nothing must not
    #      look like a pass, and the baseline must not advance past it.
    shutil.rmtree(repo / ".forks" / "pi")
    verified = record(repo)["baseline"]["commit"]
    with (repo / harness).open("a") as edited:
        edited.write("\n# the harness, edited with no checkout to read\n")
    commit(repo, "edit the liveness harness with no checkout to read")
    clear(base, "session-liveness-calls")
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "fork-check: REFUSED" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" not in result.stdout, result.stdout
    assert str(repo / ".forks" / "pi") in result.stdout, result.stdout
    assert "bin/pi-forks setup --fork pi" in result.stdout, result.stdout
    assert "fork-check: NOT VERIFIED" in result.stdout, result.stdout
    assert calls(base, "session-liveness-calls") == [], calls(base, "session-liveness-calls")
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "refused", record(repo)["runs"][-1]

    # 32g. A failing spec is the verdict the harness exists to make: the run FAILS and the
    #      baseline does not advance.
    checkout.mkdir(parents=True)
    verified = record(repo)["baseline"]["commit"]
    (repo / harness).write_text(
        "#!/bin/sh\n"
        "printf '%s\\n' 'open-run: no agent_end within 5000ms' >&2\n"
        "exit 1\n"
    )
    commit(repo, "fail the liveness harness")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "run  session-liveness " in result.stdout and "FAILED" in result.stdout, result.stdout
    assert "open-run: no agent_end within 5000ms" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

    # 32h. Exit 127 is the run script saying it could not resolve the checkout or its vitest:
    #      an inability to answer, reported as NOT VERIFIED rather than as a pass or a verdict.
    (repo / harness).write_text(
        "#!/bin/sh\n"
        "printf '%s\\n' 'session-liveness: no pi checkout' >&2\n"
        "exit 127\n"
    )
    commit(repo, "the liveness harness cannot resolve its checkout")
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "NOT VERIFIED" in result.stdout, result.stdout
    assert "session-liveness: no pi checkout" in result.stdout, result.stdout
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    assert record(repo)["runs"][-1]["outcome"] == "unproven", record(repo)["runs"][-1]

# 33. A node upgrade is an input no diff in this repository can show -- the same blindness
#     class as the yolo upgrade in 25. The installed node's version is recorded with the
#     baseline and REPORTED, but it is ADVISORY: it plans nothing. The steps it decides run
#     only when a path they watch changes, so a node upgrade with no path here moved costs
#     a report line and no work (item 114's rule, applied to node by item 126). The `node`
#     stub answers `--version` from a file and delegates everything else to the real node,
#     so the stub suites still execute when a path does plan them. The stub delegates to
#     the real node binary directly, resolved once through `mise where node` -- never
#     through the `/mise/shims/node` shim: any executable named `node` earlier on PATH makes
#     the shim itself hang (observed: the shim spinning at full CPU on a one-file suite,
#     with no stub code running at all), so the delegate must bypass mise entirely.
#     This case links the pi checkout and stubs the liveness harness anyway, so the later
#     path-driven cases have their needs; the upgrade itself plans none of them.

# 34. A lane worktree shares the record but not the checkouts. `just done` there must
#     refuse -- naming every missing working copy and its setup -- rather than print
#     VERIFIED over fork steps that never ran and advance the shared baseline past an
#     unverified change. PI_FORKS_ROOT points the probes at checkouts that exist, which
#     restores the main-checkout verdict without touching the worktree.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    # The liveness harness and the checkout it reads, so the node-planned
    # session-liveness step runs instead of refusing for its missing need.
    checkout = repo / ".forks" / "pi" / "packages" / "coding-agent" / "src"
    checkout.mkdir(parents=True)
    (checkout / "session.ts").write_text("export class AgentSession {}\n")
    (repo / "tools" / "session-liveness").mkdir()
    (repo / "tools" / "session-liveness" / "run").write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$0" >> "{base}/session-liveness-calls"\n'
        "exit 0\n"
    )
    commit(repo, "the liveness harness and the checkout it reads")
    where = sh(base, "mise", "where", "node", check=False,
               env={**os.environ, "PATH": os.environ["PATH"]})
    real_node = Path(where.stdout.strip()) / "bin" / "node" if where.returncode == 0 else None
    assert real_node is not None and real_node.is_file(), \
        "this case needs `mise where node` to name the real node binary"
    nodebin = base / "nodebin"
    nodebin.mkdir()
    (nodebin / "node").write_text(
        "#!/bin/sh\n"
        'if [ "$1" = "--version" ]; then\n'
        f'  printf "%s\\n" "$*" >> "{base}/node-version-calls"\n'
        f'  if [ -f "{base}/node-identity" ]; then cat "{base}/node-identity"; fi\n'
        f'  if [ -f "{base}/node-version-rc" ]; then exit "$(cat "{base}/node-version-rc")"; fi\n'
        "  exit 0\n"
        "fi\n"
        f'"{real_node}" "$@"\n'
    )
    (nodebin / "node").chmod(0o755)
    env = {**env, "PATH": f"{nodebin}:{env['PATH']}"}
    (base / "node-identity").write_text("v24.12.0\n")

    # 33a. The first verified run pins the installed node. It plans no node-decided step:
    #      there is no earlier verification for a version to differ from.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert calls(base, "node-version-calls") == ["--version"], calls(base, "node-version-calls")
    assert record(repo)["inputs"]["node"]["fingerprint"] == "v24.12.0", record(repo)["inputs"]
    assert "the installed node version is node v24.12.0" in result.stdout, result.stdout
    assert "a different one is reported here and plans nothing" in result.stdout, result.stdout
    for kind in ("storybook", "rank-order", "session-liveness", "meta-muse", "matt-render"):
        assert f"run  {kind} " not in result.stdout, (kind, result.stdout)

    # 33b. The same node, nothing changed: the version is read, and plans nothing.
    clear(base, "node-version-calls", "matt-render-calls", "meta-muse-calls",
          "storybook-calls", "footer-storybook-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "node-version-calls") == ["--version"], calls(base, "node-version-calls")
    assert calls(base, "matt-render-calls") == [], calls(base, "matt-render-calls")
    assert calls(base, "meta-muse-calls") == [], calls(base, "meta-muse-calls")
    assert "gate step(s) did not run (nothing they watch changed)" in result.stdout, result.stdout

    # 33c. A different node -- an upgrade, with no path here moved -- is REPORTED and plans
    #      nothing. None of the eleven steps that read it runs, and the new version is pinned
    #      once the run passes, because the run did examine the tree it is pinning.
    (base / "node-identity").write_text("v25.0.0\n")
    clear(base, "yolo-calls", "matt-render-calls", "meta-muse-calls",
          "storybook-calls", "footer-storybook-calls",
          "session-liveness-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "matt-render-calls") == [], calls(base, "matt-render-calls")
    assert "run  rank-order " not in result.stdout, result.stdout
    assert calls(base, "meta-muse-calls") == [], calls(base, "meta-muse-calls")
    assert calls(base, "storybook-calls") == [], calls(base, "storybook-calls")
    assert calls(base, "footer-storybook-calls") == [], calls(base, "footer-storybook-calls")
    assert calls(base, "session-liveness-calls") == [], calls(base, "session-liveness-calls")
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    assert "the installed node version changed: node v25.0.0" in result.stdout, result.stdout
    assert "last verified run: node v24.12.0" in result.stdout, result.stdout
    assert ("reported, not replanned: storybook, steering-timer, rank-order, "
            "session-liveness, meta-muse, matt-render, seat-profiles, observatory-web run "
            "when a path they watch changes" in result.stdout), result.stdout
    assert "run  rank-order " not in result.stdout, result.stdout
    assert "run  session-liveness " not in result.stdout, result.stdout
    assert record(repo)["inputs"]["node"]["fingerprint"] == "v25.0.0", record(repo)["inputs"]

    # 33d. A version that cannot be read is declared, with the command a reader must run
    #      instead -- never guessed at, and never silently read as unchanged.
    (base / "node-identity").unlink()
    (base / "node-version-rc").write_text("1")
    clear(base, "matt-render-calls", "meta-muse-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the installed node version could not be identified" in result.stdout, result.stdout
    assert "node --version" in result.stdout, result.stdout
    assert calls(base, "matt-render-calls") == [], calls(base, "matt-render-calls")
    assert calls(base, "meta-muse-calls") == [], calls(base, "meta-muse-calls")

    # 33e. A record from before this gate kept a node version -- a baseline that names none
    #      -- is news once: this run records one and plans nothing from it. A comparison never
    #      made is not a match, but it is also not a reason to run steps no path reaches.
    clear(base, "node-version-rc")
    (base / "node-identity").write_text("v25.0.0\n")
    state = record(repo)
    del state["inputs"]["node"]
    write_record(repo, state)
    clear(base, "matt-render-calls", "meta-muse-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "no node version was recorded with the last verified run" in result.stdout, result.stdout
    assert "nothing was planned from it" in result.stdout, result.stdout
    assert calls(base, "matt-render-calls") == [], calls(base, "matt-render-calls")
    assert calls(base, "meta-muse-calls") == [], calls(base, "meta-muse-calls")
    assert record(repo)["inputs"]["node"]["fingerprint"] == "v25.0.0", record(repo)["inputs"]

# 34 (continued). The lane worktree shape, exercised on this same fixture repository
# after the node case verified it: the first run below re-verifies a clean HEAD,
# so `verified` below is this repository's own baseline, not the node's.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    verified = record(repo)["baseline"]["commit"]

    # The lane shape: no .forks/ directory at all, and series edits on top -- both
    # forks, so every fork-dependent step is planned with nothing to read it from.
    shutil.rmtree(repo / ".forks")
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nlane\n"
    )
    (repo / "yolo-packs" / "matt" / "patches" / "pi-background-tasks" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nlane\n"
    )
    commit(repo, "a lane worktree edits both series with no checkouts")
    clear(base, "yolo-calls", "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 2, result.stdout + result.stderr
    assert "fork-check: REFUSED" in result.stdout, result.stdout
    assert "fork-check: VERIFIED" not in result.stdout, result.stdout
    assert str(repo / ".forks" / "pi-subagents") in result.stdout, result.stdout
    assert str(repo / ".forks" / "pi-background-tasks") in result.stdout, result.stdout
    assert "bin/pi-forks setup --fork pi-subagents" in result.stdout, result.stdout
    assert "bin/pi-forks setup --fork pi-background-tasks" in result.stdout, result.stdout
    assert "PI_FORKS_ROOT=/workspace" in result.stdout, result.stdout
    assert "fork-check: NOT VERIFIED" in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")
    # The drift probe is the plan's own question and answers nothing here (no
    # checkouts), so the run refuses before any fork step.
    assert calls(base, "pi-forks-calls") == ["patch-branch target --fetch"], calls(base, "pi-forks-calls")
    assert record(repo)["baseline"]["commit"] == verified, record(repo)["baseline"]
    refused = record(repo)["runs"][-1]
    assert refused["outcome"] == "refused", refused
    assert str(repo / ".forks" / "pi-subagents") in refused["missing_needs"], refused
    assert str(repo / ".forks" / "pi-background-tasks") in refused["missing_needs"], refused

    # --explain runs nothing and says the default run would refuse.
    result = run(repo, env, "--explain")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "would refuse" in result.stdout, result.stdout
    assert str(repo / ".forks" / "pi-subagents") in result.stdout, result.stdout
    assert "fork-check: VERIFIED" not in result.stdout, result.stdout
    assert calls(base, "yolo-calls") == [], calls(base, "yolo-calls")

    # The probes honour PI_FORKS_ROOT: with it naming a repository root whose .forks/ has
    # the checkouts, the same HEAD verifies exactly as a main checkout would.
    shared_root = base / "shared-checkout"
    shared = shared_root / ".forks"
    (shared / "pi-subagents").mkdir(parents=True)
    (shared / "pi-background-tasks").mkdir(parents=True)
    # The rank-order suite is planned by the same series edit, so the shared forks
    # carry the file it imports too.
    shared_rank = shared / "pi-subagents" / "src" / "runs" / "background"
    shared_rank.mkdir(parents=True)
    (shared_rank / "queue-rank.ts").write_text("export const compareQueueEntries = () => 0;\n")
    # An inherited PI_FORKS_SERIES_ROOT is overridden, not obeyed: the gate reads the
    # series of the tree it is verifying (this one), so a value left in a shell cannot
    # move a gate run to another tree's series -- the 2026-10-24 failure, where a
    # landing run read /workspace's 11-patch series and called a correct 12-patch one
    # not its export.
    shared_env = {**env, "PI_FORKS_ROOT": str(shared_root),
                  "PI_FORKS_SERIES_ROOT": str(shared_root)}
    clear(base, "yolo-calls", "pi-forks-calls")
    result = run(repo, shared_env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert (f"fork-check: series are read from {repo} (the tree this run is in); "
            f"fork checkouts from {shared}") in result.stdout, result.stdout
    assert set(calls(base, "pi-forks-series-roots")) == {str(repo)}, \
        calls(base, "pi-forks-series-roots")
    assert calls(base, "yolo-calls") == [PACK_CHECK], calls(base, "yolo-calls")
    assert calls(base, "pi-forks-calls") == [
        "patch-branch target --fetch",
        "patch-branch merge-check --candidate --fetch --fork pi-background-tasks", "patch-branch merge-check --candidate --fetch --fork pi-subagents",
        "patch-branch verify --fork pi-background-tasks", "patch-branch verify --fork pi-subagents",
        "candidate --fork pi-background-tasks", "candidate --fork pi-background-tasks --build",
        "candidate --fork pi-subagents", "candidate --fork pi-subagents --build",
        "candidate --fork pi-subagents --deps",
        "candidate --fork pi-subagents --prepared -- npm run typecheck",
        "candidate --fork pi-subagents --prepared -- npm run test:unit",
    ], calls(base, "pi-forks-calls")
    assert record(repo)["baseline"]["commit"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip()

    # Plan-level: needs resolve against the forks root -- the checkout's own .forks/ by
    # default, PI_FORKS_ROOT when it names another directory.
    spec = importlib.util.spec_from_file_location("fork_check_forks_root", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    assert module.forks_root(repo) == repo / ".forks"
    planned = module.build_plan(
        repo, [], module.Record(base / "plan-state.json"),
        [rank_test := "yolo-packs/matt/tests/pi-subagents-rank-order.test.mjs", "tools/session-liveness/run"],
        None, True,
    )
    rank = [check for check in planned.checks if check.kind == "rank-order"]
    live = [check for check in planned.checks if check.kind == "session-liveness"]
    assert len(rank) == 1 and len(live) == 1, [c.kind for c in planned.checks]
    assert rank[0].needs == (
        str(repo / ".forks" / "pi-subagents" / "src" / "runs" / "background" / "queue-rank.ts"),
    ), rank[0].needs
    # The liveness step builds the checkout it judges and so needs only the construction
    # substrate; the build step is the one that names it, with the setup command that
    # provides it.
    assert live[0].needs == (), live[0].needs
    assert live[0].env == {"PI_FORK": str(repo / ".forks" / "candidate" / "pi")}, live[0].env
    build = [check for check in planned.checks if check.kind == "fork-construct"]
    assert len(build) == 1, [c.kind for c in planned.checks]
    assert build[0].needs == (str(repo / ".forks" / "pi"),), build[0].needs
    assert build[0].remedy == module.fork_setup("pi"), build[0].remedy
    os.environ["PI_FORKS_ROOT"] = str(shared_root)
    try:
        assert module.forks_root(repo) == shared
        planned = module.build_plan(
            repo, [], module.Record(base / "plan-state.json"),
            ["yolo-packs/matt/tests/pi-subagents-rank-order.test.mjs"], None, True,
        )
        rank = [check for check in planned.checks if check.kind == "rank-order"]
        assert len(rank) == 1, [c.kind for c in planned.checks]
        assert rank[0].needs == (
            str(shared / "pi-subagents" / "src" / "runs" / "background" / "queue-rank.ts"),
        ), rank[0].needs
    finally:
        del os.environ["PI_FORKS_ROOT"]

    # One variable, one reading: with PI_FORKS_ROOT naming a repository root, the gate reads
    # `<root>/.forks` and `bin/pi-forks` reads that same directory -- so one exported value
    # cannot point either tool at a tree that exists but is the wrong one.
    agreed = base / "agreed-root"
    (agreed / "bin").mkdir(parents=True)
    (agreed / "bin" / "pi-forks.list").write_text(
        "earendil-works/pi  yolo-packs/pi-fork/patches\n"
    )
    os.environ["PI_FORKS_ROOT"] = str(agreed)
    try:
        gate_forks = module.forks_root(repo)
    finally:
        del os.environ["PI_FORKS_ROOT"]
    listed = sh(ROOT, "bash", str(ROOT / "bin" / "pi-forks"), "list",
                env={**os.environ, **ENV, "PI_FORKS_ROOT": str(agreed)})
    # `list` prints `<owner/repo>`, the forks path it resolved, and the series directory.
    driver_forks = Path(listed.stdout.split()[1]).parent
    assert gate_forks == driver_forks == agreed / ".forks", (gate_forks, driver_forks)

    # The fork steps read the series of the tree being verified, and each carries that
    # tree in PI_FORKS_SERIES_ROOT -- pinned, not inherited, so a value left in a shell
    # cannot move a gate run to another tree's series. PI_FORKS_ROOT still moves only the
    # checkouts. The failure this pins (2026-10-24): a landing run in a worktree read
    # /workspace's 11-patch series and reported a correct 12-patch series as not its
    # export.
    os.environ["PI_FORKS_ROOT"] = str(shared_root)
    os.environ["PI_FORKS_SERIES_ROOT"] = str(shared_root)
    try:
        planned = module.build_plan(
            repo, module.read_series(repo), module.Record(base / "plan-state.json"),
            ["yolo-packs/matt/patches/pi-subagents/0001-x.patch"], None, True,
            audit_upstreams=False,
        )
    finally:
        del os.environ["PI_FORKS_ROOT"]
        del os.environ["PI_FORKS_SERIES_ROOT"]
    series_steps = [check for check in planned.checks
                    if check.kind in ("patch-branch-inv2", "patch-branch-inv3", "forks-check")]
    assert {check.kind for check in series_steps} == {
        "patch-branch-inv2", "patch-branch-inv3", "forks-check",
    }, [check.kind for check in planned.checks]
    for check in series_steps:
        assert check.env == {"PI_FORKS_SERIES_ROOT": str(repo)}, (check.kind, check.env)
        assert all(need.startswith(str(shared)) for need in check.needs), (check.kind, check.needs)

# The bound itself: a phase that outlasts PROGRESS_INTERVAL says so while it runs. A
# single check's own timeout is minutes -- a build's is an hour -- and its result
# exists only when it returns, so the beat is the only thing that keeps that check
# from being silent for all of it.
spec = importlib.util.spec_from_file_location("fork_check_progress", SCRIPT)
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
# The count is the pass condition, and the interval is what leaves it margin: two beats inside a
# 0.3 s phase needs 20 ms of scheduling, so a loaded machine cannot lose them the way it could at
# 0.05 s, where two beats needed 100 ms (board item 133).
progress.PROGRESS_INTERVAL = 0.01
heard = io.StringIO()
with contextlib.redirect_stderr(heard):
    with progress.heartbeat("a phase that takes its time"):
        time.sleep(0.3)
beats = [line for line in heard.getvalue().splitlines() if "still running" in line]
assert len(beats) >= 2, heard.getvalue()
assert "a phase that takes its time" in beats[0], beats

# 35. TWO CONCURRENT GATES DO NOT CLOBBER EACH OTHER'S RECORD, AND THEY TAKE NO LOCK.
#     The record lock, its takeover of a stale holder, and the merge a save ran under it
#     are gone: a save APPENDS one line, so two runs that overlap record two lines and
#     neither can drop the other's. This is the case that used to assert "the second
#     waits on the record lock", replaced rather than deleted -- both per-branch
#     baselines and both run entries must still survive. Each `yolo` answer sleeps, so
#     the two runs overlap the way two worktrees' `just done` runs do.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    stub = base / "stubs" / "yolo"
    stub.write_text(stub.read_text().replace("exit 0", "sleep 2\nexit 0"))
    # A verified run on the integration branch first, so the two lanes have a baseline of
    # their own to leave alone: the assertion below is that neither concurrent save moved
    # it, which needs it to exist.
    assert run(repo, env).returncode == 0
    main_baseline = record(repo)["baselines"]["refs/heads/main"]["commit"]
    lanes = {}
    for name in ("lane-a", "lane-b"):
        path = base / name
        sh(repo, "git", "worktree", "add", "--quiet", "-b", f"feat/{name}", str(path), "main")
        (path / "docs" / "guide.md").write_text(f"# Guide\n\n{name} edits this document.\n")
        commit(path, f"{name} edits the guide")
        lanes[name] = path
    # One check at a time so the overlap is the RUNS', not the scheduler's: the stub's
    # `sleep 2` must still be running when the second run starts.
    for name, (code, out, err) in run_parallel_gates(lanes, env).items():
        assert code == 0, (name, out, err)
        assert "VERIFIED" in out, (name, out)
    folded = record(repo)
    lane_heads = {name: sh(path, "git", "rev-parse", "HEAD").stdout.strip()
                  for name, path in lanes.items()}
    for name, head_sha in lane_heads.items():
        entry = folded["baselines"].get(f"refs/heads/feat/{name}")
        assert entry is not None and entry["commit"] == head_sha, (name, folded["baselines"])
    assert folded["baselines"]["refs/heads/main"]["commit"] == main_baseline, folded["baselines"]
    new = [entry for entry in folded["runs"] if entry.get("commit") in set(lane_heads.values())]
    assert len(new) == 2, json.dumps(folded["runs"][-3:], indent=2)
    assert all(entry.get("outcome") == "verified" for entry in new), json.dumps(new, indent=2)
    # The record really is a log, and really was appended to twice.
    lines = [json.loads(line) for line in record_log(repo).read_text().splitlines()]
    assert sum(len(line.get("runs") or []) for line in lines) >= 3, [
        (line.get("kind"), len(line.get("runs") or [])) for line in lines]
    # And no record lock exists to wait on, because none was ever taken.
    assert not (record_log(repo).parent / "state.lock").exists(), \
        list(record_log(repo).parent.iterdir())
    print("fork-check concurrent-gate records:")
    for entry in new:
        print(f"  at={entry['at']} commit={entry['commit'][:12]} outcome={entry['outcome']} "
              f"checks={len(entry.get('checks', []))}")

_shard_boundary("explain-and-triage", 11)

# 36. `--explain` is a read-only diagnostic: it writes no record and no lock. There is no
#     record lock left to take, so the case now pins the stronger property -- a planning
#     pass leaves the record directory exactly as it found it.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    state_dir = repo / ".git" / "yolo-fork-check"
    result = run(repo, env, "--explain", "--no-wait")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "REFUSED" not in result.stdout, result.stdout
    assert "would run" in result.stdout, result.stdout
    assert not (state_dir / "state.lock").exists(), list(state_dir.iterdir())
    assert not record_log(repo).exists(), list(state_dir.iterdir())

# 36a. THE APPEND IS THE WHOLE GUARANTEE. Two records both loaded before either saved --
#      the lost-update shape the old lock and its merge existed for -- both land, because
#      each save appends a line instead of rewriting the document. Nothing merges what it
#      did not read, and a save takes no lock.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    path = record_log(repo)
    path.parent.mkdir(parents=True, exist_ok=True)
    first = _RECORD_TOOL.Record(path, "refs/heads/main")
    second = _RECORD_TOOL.Record(path, "refs/heads/feat")
    for record_one, commit_sha in ((first, "a" * 40), (second, "b" * 40)):
        record_one.add_run({"at": "2026-11-15T00:00:00Z", "commit": commit_sha,
                            "outcome": "verified"})
        record_one.advance_baseline(commit_sha, "t" * 40)
    first.save()
    second.save()
    folded = record(repo)
    assert len(folded["runs"]) == 2, folded["runs"]
    assert folded["baselines"]["refs/heads/main"]["commit"] == "a" * 40, folded["baselines"]
    assert folded["baselines"]["refs/heads/feat"]["commit"] == "b" * 40, folded["baselines"]
    assert path.read_text().count("\n") == 2, path.read_text()
    assert not (path.parent / "state.lock").exists(), list(path.parent.iterdir())

# 36b. THE OLD RECORD IS MIGRATED ONCE, AND LEFT BEHIND. `state.json` is the document the
#      log replaced. A read folds it in memory and writes nothing, so a diagnostic cannot
#      mint a record; the first save writes it as one snapshot line, and the old file
#      stays where it was as the backup. A later save does not migrate again, so a stale
#      copy can never be replayed over newer lines.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    state_dir = repo / ".git" / "yolo-fork-check"
    state_dir.mkdir(parents=True, exist_ok=True)
    legacy = {
        "schema": 1, "producer": "tools/fork-check.py",
        "runs": [{"at": "2026-01-01T00:00:00Z", "commit": "a" * 40, "outcome": "verified"}],
        "baselines": {"refs/heads/main": {"commit": "a" * 40, "tree": "t" * 40,
                                          "at": "2026-01-01T00:00:00Z"}},
        "baseline": {"commit": "a" * 40, "tree": "t" * 40, "at": "2026-01-01T00:00:00Z"},
        "targets": {}, "inputs": {}, "triage": {},
    }
    (state_dir / "state.json").write_text(json.dumps(legacy))
    planned = _RECORD_TOOL.Record(record_log(repo), "refs/heads/main")
    assert planned.data["baseline"]["commit"] == "a" * 40, planned.data.get("baseline")
    assert not record_log(repo).exists(), list(state_dir.iterdir())
    planned.add_run({"at": "2026-11-15T00:00:00Z", "commit": "b" * 40, "outcome": "verified"})
    planned.save()
    assert (state_dir / "state.json").exists(), list(state_dir.iterdir())
    lines = [json.loads(line) for line in record_log(repo).read_text().splitlines()]
    assert [line["kind"] for line in lines] == ["snapshot", "run"], lines
    assert [run["commit"] for run in record(repo)["runs"]] == ["a" * 40, "b" * 40], record(repo)["runs"]
    again = _RECORD_TOOL.Record(record_log(repo), "refs/heads/main")
    again.add_run({"at": "2026-11-15T00:00:01Z", "commit": "c" * 40, "outcome": "verified"})
    again.save()
    kinds = [json.loads(line)["kind"] for line in record_log(repo).read_text().splitlines()]
    assert kinds.count("snapshot") == 1, kinds
    assert len(record(repo)["runs"]) == 3, record(repo)["runs"]

# 36c. THE TRIAGE LEDGER IS BOUNDED. `clear_triage` removes only a check that ran here and
#      passed, so the failure of a check nothing re-plans stays in the ledger forever: the
#      live record carried 160 entries on 2026-11-15 while the run and baseline maps were
#      already capped. `MAX_TRIAGE` is that cap, and the newest entries by `at` are the
#      ones a reader still has.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    path = record_log(repo)
    path.parent.mkdir(parents=True, exist_ok=True)
    seeded = _RECORD_TOOL.Record(path, "refs/heads/main")
    total = _RECORD_TOOL.MAX_TRIAGE + 20
    for index in range(total):
        seeded.set_triage(f"cmd {index:03d}", {"kind": "k", "at": f"2026-11-15T00:00:{index:03d}Z"})
    seeded.save()
    folded = record(repo)
    assert len(folded["triage"]) == _RECORD_TOOL.MAX_TRIAGE, len(folded["triage"])
    assert f"cmd {total - 1:03d}" in folded["triage"], sorted(folded["triage"])[-3:]
    assert "cmd 000" not in folded["triage"], sorted(folded["triage"])[:3]

# 36d. THE LOG IS COMPACTED, NOT UNBOUNDED. A save that would push the log past
#      `MAX_RECORD_LINES` rewrites it as one snapshot plus its run lines, so the file stays
#      within a bounded number of runs rather than growing a line per run forever. The
#      folded record is unchanged by a compaction: same runs, same baselines.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    path = record_log(repo)
    path.parent.mkdir(parents=True, exist_ok=True)
    saved = _RECORD_TOOL.MAX_RECORD_LINES
    # A compaction writes one snapshot line plus one line per kept run, so the bound has
    # to sit above `MAX_RUNS + 1` to leave anything between compactions. Two runs of
    # slack is enough to prove the rewrite happens and stays bounded.
    _RECORD_TOOL.MAX_RECORD_LINES = _RECORD_TOOL.MAX_RUNS + 2
    try:
        for index in range(20):
            one = _RECORD_TOOL.Record(path, "refs/heads/main")
            one.add_run({"at": f"2026-11-15T00:00:{index:02d}Z",
                         "commit": f"{index:040x}", "outcome": "verified"})
            one.advance_baseline(f"{index:040x}", "t" * 40)
            one.save()
        lines = [line for line in path.read_text().splitlines() if line.strip()]
        assert len(lines) <= _RECORD_TOOL.MAX_RECORD_LINES + 1, len(lines)
        folded = _RECORD_TOOL.Record(path, "refs/heads/main").data
        assert len(folded["runs"]) == _RECORD_TOOL.MAX_RUNS, len(folded["runs"])
        assert [run["commit"] for run in folded["runs"]] == [
            f"{index:040x}" for index in range(20 - _RECORD_TOOL.MAX_RUNS, 20)], folded["runs"]
        assert folded["baselines"]["refs/heads/main"]["commit"] == f"{19:040x}", folded["baselines"]
    finally:
        _RECORD_TOOL.MAX_RECORD_LINES = saved

# 38. The external inputs this gate cannot identify are DECLARED rather than pinned, each
#     with the command that covers it: `uvx`/vantage-check, `go` and `npm` in the footer,
#     and the HOST's own yolo build in its own `declared elsewhere` line. The installed yolo
#     build is advisory (25) and the installed node version is planned from (33), so this
#     footer is the whole coverage story for the rest -- and a reader told "not covered"
#     with no command has no way to cover it. The case reads the report a reader reads
#     rather than the constant behind it: dropping one tool's covering command from the
#     footer left every other case green.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert ("fork-check: not visible to this plan, declared rather than pinned, and the "
            "command that covers each:" in result.stdout), result.stdout
    for declared in (
        "`uvx`/vantage-check (`uvx vantage-check <file>`)",
        "`go` (`cd tools/<go-tool dir> && go test ./...`, in the directory whose step did not run)",
        "`npm` (`bin/pi-forks candidate --deps --fork <fork> -- npm run check`",
    ):
        assert declared in result.stdout, (declared, result.stdout)
    # The host's own yolo build is a DECLARED input in one line, with the command that
    # answers it -- but only from a jail: a host run reads that build itself, and the gate's
    # predicate (the container runtime's marker) is what decides it.
    spec = importlib.util.spec_from_file_location("fork_check_declared", repo / "tools" / "fork-check.py")
    tool = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tool)
    declared_line = ("fork-check: declared elsewhere -- the host's own yolo build: "
                     "a jail reads the yolo on its own PATH")
    assert (declared_line in result.stdout) == tool.in_jail(), (tool.in_jail(), result.stdout)
    if tool.in_jail():
        assert "checks with `yolo --version --full` on the host" in result.stdout, result.stdout

# 42. A failing check's own output is written beside the record. The run entry keeps per
#     check only kind, label, rc and seconds, so a host run that failed could be
#     diagnosed only from the terminal it ran in -- a reader on another machine had to
#     ask for a paste. A run with a failing check now writes that check's command, exit
#     code and last lines to `.git/yolo-fork-check/report.txt`, beside the record, and
#     points the entry's failed checks at it; a passing check gets no pointer, a run with
#     nothing failing writes nothing, and a later green run leaves the last failure's
#     excerpt in place so it cannot erase what a reader is diagnosing.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    excerpt = repo / ".git" / "yolo-fork-check" / "report.txt"

    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert not excerpt.exists(), "a run with nothing failing writes no excerpt"

    # A document finding is the case that needed this: the entry said "documents ... exited
    # 1", and the checker's own report -- the line that names the missing target -- existed
    # only on the terminal.
    waybar_fixture(repo)
    (repo / "tests/waybar-ai-modules.test.sh").write_text(
        '#!/bin/sh\n'
        f'if [ -f "{base}/uvx-rc" ]; then cat "{base}/uvx-out"; exit 1; fi\n'
        'exit 0\n')
    commit(repo, "break a document")
    (base / "uvx-out").write_text(
        "docs/guide.md\n"
        "  3:5  error  link/missing-target  `./gone.md` does not exist (looked for `gone.md`).\n"
    )
    (base / "uvx-rc").write_text("1")
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert excerpt.exists(), "a failing run writes the excerpt beside the record"
    text = excerpt.read_text()
    assert "command: sh tests/waybar-ai-modules.test.sh" in text, text
    assert "exit code: 1" in text, text
    assert "link/missing-target" in text, text
    # The report names the file, so the terminal says where to look without a paste.
    assert str(excerpt) in result.stdout, result.stdout
    # The record points the FAILED checks at it, and only those: a path on a passing check
    # would make the excerpt read as required evidence rather than as a failure's words.
    entry = record(repo)["runs"][-1]
    failed_entries = [one for one in entry["checks"] if one["rc"] != 0]
    passed_entries = [one for one in entry["checks"] if one["rc"] == 0]
    assert failed_entries, entry
    assert all(one["excerpt"] == str(excerpt) for one in failed_entries), entry
    assert all("excerpt" not in one for one in passed_entries), entry

    # A command that printed more than the excerpt keeps is cut to its LAST lines -- where a
    # finding is -- and the header says how many were dropped, so a truncated excerpt cannot
    # read as the whole transcript.
    spec = importlib.util.spec_from_file_location("fork_check_excerpt", repo / "tools" / "fork-check.py")
    tool = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tool)
    (base / "uvx-out").write_text(
        "".join(f"line {index} of a long report\n" for index in range(tool.EXCERPT_LINES + 40))
        + "LAST-KEPT\n"
    )
    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    text = excerpt.read_text()
    assert "line 0 of a long report" not in text, text
    assert "LAST-KEPT" in text, text
    assert "earlier line(s) dropped" in text, text
    # Rewritten, not appended: the file describes the most recent failing run, and its
    # header names that run because older entries carry the same path.
    assert text.count("=== waybar") == 1, text
    assert "link/missing-target" not in text, text

    # A green run afterwards leaves the last failure's excerpt alone: fixing the document
    # must not delete the thing a reader is diagnosing.
    (repo / "docs" / "guide.md").write_text("# Guide\n\nFixed.\n")
    commit(repo, "fix the document")
    clear(base, "uvx-rc", "uvx-out")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert "LAST-KEPT" in excerpt.read_text(), excerpt.read_text()

# 44. A failed check costs exactly ONE run, and what the gate produces instead of a retry is
#     a triage packet read out of that run's own output: the failing case, its file, the
#     assertion, the paths it names, the commits that touched them, and the one-file command
#     that reproduces it. No YELLOW, no FLAKY, no second run of the failing step -- and the
#     packet is what the repair path dispatches a debugger with.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    board_test = "yolo-packs/matt/tests/pi-subagents-rank-order.test.mjs"
    triage_path = repo / ".git" / "yolo-fork-check" / "triage.json"
    excerpt = repo / ".git" / "yolo-fork-check" / "report.txt"
    timings = repo / ".git" / "yolo-fork-check" / "timings.jsonl"
    # A verified baseline first: a path-gated step is planned from the diff since it, and a
    # first run checks the series alone.
    assert run(repo, env).returncode == 0

    # A real node case that fails, in the reporter node actually prints, and one that passes
    # beside it: the packet must name the failing case, not the file's first line or the
    # suite's exit code. The stub's own call log is appended from inside the file, so the
    # count below is runs of the step rather than runs of the gate.
    (repo / board_test).write_text(
        "import { appendFileSync } from 'node:fs';\n"
        "import { test } from 'node:test';\n"
        "import assert from 'node:assert';\n"
        f"appendFileSync('{base}/rank-order-calls', 'rank\\n');\n"
        "test('a board row keeps its column', () => {\n"
        f"  assert.strictEqual(1, 2, 'the row lost column {board_test}');\n"
        "});\n"
        "test('a second case still passes', () => {});\n"
    )
    commit(repo, "a rank-order case that fails")
    clear(base, "rank-order-calls")

    result = run(repo, env)
    assert result.returncode == 1, result.stdout + result.stderr
    assert "fork-check: FAILED" in result.stdout, result.stdout
    # The retry is gone, and with it the third state it invented: a failure is red, once.
    assert "YELLOW" not in result.stdout, result.stdout
    assert "FLAKY" not in result.stdout, result.stdout
    assert "re-running" not in result.stdout, result.stdout
    assert calls(base, "rank-order-calls") == ["rank"], calls(base, "rank-order-calls")
    # The one run is all the record keeps: no `attempts` array, no `flaky` entry.
    entry = record(repo)["runs"][-1]
    assert entry["outcome"] == "failed", entry
    failed_check = [one for one in entry["checks"] if one["kind"] == "rank-order"][0]
    assert "attempts" not in failed_check, failed_check
    assert "flaky" not in failed_check, failed_check
    assert "flaky" not in record(repo), record(repo)
    # The record keeps the triage record in its place: one entry per failing command, with
    # the case and the command a debugger starts from.
    triaged = record(repo)["triage"]
    assert len(triaged) == 1, triaged
    key, kept = list(triaged.items())[0]
    assert key.startswith("node --test"), key
    assert kept["kind"] == "rank-order", kept
    assert kept["cases"] == [{"name": "a board row keeps its column", "file": board_test}], kept
    assert kept["reproduce"] == f"node --test {board_test}", kept
    assert "--test-name-pattern" in kept["single_case"], kept
    assert failed_check["triage"] == str(triage_path), failed_check
    assert failed_check["excerpt"] == str(excerpt), failed_check

    # The packet itself, machine-readable beside the record.
    document = json.loads(triage_path.read_text())
    assert document["outcome"] == "failed", document
    assert len(document["checks"]) == 1, document
    packet = document["checks"][0]
    assert packet["check"] == "rank-order", packet
    assert packet["exit"] == 1, packet
    assert packet["scope"] == "file", packet
    assert len(packet["cases"]) == 1, packet
    case = packet["cases"][0]
    assert case["name"] == "a board row keeps its column", case
    assert case["file"] == board_test, case
    assert isinstance(case["line"], int) and case["line"] > 0, case
    assert "the row lost column" in case["assertion"], case
    # The assertion's own words, the path they name, and the commit that last touched it.
    assert case["paths"] == [board_test], case
    assert [one["path"] for one in case["commits"]] == [board_test], case
    assert case["commits"][0]["touched"][0]["sha"], case
    # One file, and one case inside it: what a debugger runs instead of the gate.
    assert case["reproduce"] == f"node --test {board_test}", case
    assert case["single_case"] == (
        f"node --test --test-name-pattern 'a board row keeps its column' {board_test}"), case
    assert packet["runs_allowed"] == 3, packet
    assert packet["may_not_run"] == ["the gate (`just done`)", "the whole suite the check runs"], \
        packet

    # The report prints the packet rather than asking a reader to open a JSON file: the case,
    # its file, the assertion, the reproducing command, and the two things the repair path
    # may not do.
    assert "fork-check: triage -- 1 failed check(s)" in result.stdout, result.stdout
    assert "case: a board row keeps its column" in result.stdout, result.stdout
    assert f"assertion: AssertionError" in result.stdout or "assertion: the row lost column" in result.stdout, result.stdout
    assert f"reproduce (one file): node --test {board_test}" in result.stdout, result.stdout
    assert "--test-name-pattern" in result.stdout, result.stdout
    assert "may not run the gate" in result.stdout, result.stdout
    # The one path a passing one-file re-run has to take: the report names the board command
    # that records the override AND files the flake item, with the check and the case already
    # filled in, so an overruled red is filed rather than remembered.
    assert ("board flake override --check rank-order --case 'a board row keeps its column'"
            in result.stdout), result.stdout
    assert f"the machine-readable packet is {triage_path}" in result.stdout, result.stdout
    # The transcript the packet was read from is still written beside it, once.
    text = excerpt.read_text()
    assert "status: FAIL" in text, text
    assert "the row lost column" in text, text
    # One run in the timings timeline, so the cost of a failure is one check, not two.
    timed = [one for one in json.loads(timings.read_text().splitlines()[-1])["checks"]
             if one["kind"] == "rank-order"]
    assert len(timed) == 1, timed
    assert "attempts" not in timed[0], timed[0]

    # The repair path's entry point: the packet is a read, so it runs nothing, and the
    # failing step's own call log does not move.
    clear(base, "rank-order-calls")
    result = run(repo, env, "--triage")
    assert result.returncode == 0, result.stdout + result.stderr
    shown = json.loads(result.stdout)
    assert shown["checks"][0]["cases"][0]["name"] == "a board row keeps its column", shown
    assert calls(base, "rank-order-calls") == [], calls(base, "rank-order-calls")
    # A selector narrows it, and a selector that matches nothing is a refusal, not a silence.
    result = run(repo, env, "--triage", "rank-order")
    assert result.returncode == 0, result.stdout + result.stderr
    assert json.loads(result.stdout)["checks"], result.stdout
    result = run(repo, env, "--triage", "no-such-check")
    assert result.returncode == 2, result.stdout + result.stderr
    assert "no recorded packet matches" in result.stdout, result.stdout

    # The failure still stands in the record, so a run that does not plan the check names it:
    # the diagnosis cannot go quiet just because this run does not exercise it. `--since HEAD`
    # is that run -- an empty range, so nothing path-gated is planned and nothing advances.
    result = run(repo, env, "--since", "HEAD")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "  run  rank-order" not in result.stdout, result.stdout
    assert "recorded failure(s) did not run here" in result.stdout, result.stdout
    assert "rank-order: a capacity-queued dispatch follows the pile's rank" in result.stdout, \
        result.stdout

    # The fix, verified: the check runs and passes, so its recorded failure is cleared -- but
    # the packet and the excerpt stay, because they are the evidence a reader returns to.
    (repo / board_test).write_text(
        "import { appendFileSync } from 'node:fs';\n"
        "import { test } from 'node:test';\n"
        f"appendFileSync('{base}/rank-order-calls', 'rank\\n');\n"
        "test('a board row keeps its column', () => {});\n"
    )
    commit(repo, "fix the rank-order case")
    clear(base, "rank-order-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "VERIFIED" in result.stdout, result.stdout
    assert "recorded failure(s) did not run here" not in result.stdout, result.stdout
    assert record(repo)["triage"] == {}, record(repo)["triage"]
    assert triage_path.exists(), "a green run must not erase the last packet"
    assert "the row lost column" in excerpt.read_text(), excerpt.read_text()

_shard_boundary("pi-forks-and-scheduler", 39)

# 45. The pi-forks driver's own suite. `tests/pi-forks.test.sh` pins `bin/pi-forks`, the
#     driver every patch-series step above execs, and no `Check(...)` named it: the cases
#     that pin the driver sat unexecuted, and the suite was itself broken on a clean tree --
#     on a lane worktree it aborted under `set -e` after 80 passing cases, because the
#     driver's linked-worktree refusal fired on the sourced call that reads the real
#     manifest. The step is planned by a change to the driver, its manifest, the suite, a
#     series directory or a pack's declaration, and by nothing else; it runs from the
#     checkout, and a failing case is a verdict about the change rather than an inability
#     to answer it.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, _ = build_repo(base)
    env = build_stubs(base)

    # The routing, without running anything: every input the driver reads plans the step,
    # and an unrelated path leaves it out of the plan (named, so the silence is visible).
    # The `moves_series` column is the structural boundary D5 asks for: the driver's own
    # cases live in `tests/pi-forks.test.sh`, a separable path, so a change confined to
    # them plans the cases and moves no series. Driver code and the manifest still move
    # every series, exactly as before -- the path rule narrows only the test path.
    spec = importlib.util.spec_from_file_location("fork_check_pi_forks", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    one_series = [module.Series(pack="matt", rel="patches/pi-subagents",
                                source=f"git+{releases}?ref=main", follow=None)]
    for changed, planned, moves_series in (
        (["bin/pi-forks"], True, True),
        (["bin/pi-forks.list"], True, True),
        (["tests/pi-forks.test.sh"], True, False),
        (["yolo-packs/matt/patches/pi-subagents/0001-x.patch"], True, True),
        (["yolo-packs/matt/pack.json"], True, True),
        (["docs/guide.md"], False, False),
    ):
        plan = module.build_plan(
            repo, one_series, module.Record(base / "plan-state.json"), changed, None, True,
            audit_upstreams=False, audit_yolo=False, audit_node=False,
        )
        kinds = [check.kind for check in plan.checks]
        assert ("pi-forks" in kinds) is planned, (changed, kinds)
        assert ("series" in kinds) is moves_series, (changed, kinds)
        if not planned:
            assert any(skip.label.startswith("pi-forks (bash") for skip in plan.skips), \
                [skip.label for skip in plan.skips]

    # A verified baseline first, then the step itself: the stub records that the gate ran
    # it, and from where.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    suite = repo / "tests" / "pi-forks.test.sh"
    suite.write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$(pwd)" > "{base}/pi-forks-suite-cwd"\n'
        "echo 'ok   stub pi-forks driver case'\n"
    )
    commit(repo, "the pi-forks suite")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "  run  pi-forks" in result.stdout, result.stdout
    assert "bin/pi-forks: the driver's own cases" in result.stdout, result.stdout
    assert (base / "pi-forks-suite-cwd").read_text().strip() == str(repo), \
        (base / "pi-forks-suite-cwd").read_text()
    # The step leaves the checkout clean: the suite builds its fixtures in scratch.
    assert sh(repo, "git", "status", "--porcelain").stdout.strip() == "", \
        sh(repo, "git", "status", "--porcelain").stdout

    # A failing case is a verdict about the change -- the driver no longer holds -- and not
    # an inability to answer it.
    suite.write_text("#!/bin/sh\necho 'FAIL a driver case'\nexit 1\n")
    commit(repo, "a failing driver case")
    result = run(repo, env)
    assert result.returncode != 0, result.stdout + result.stderr
    assert "fork-check: FAILED" in result.stdout, result.stdout
    assert "pi-forks: bin/pi-forks" in result.stdout, result.stdout


# 46. Independent checks run at the same time; two that write the same path do not.
#     The scheduler's two rules are unit cases below -- a write conflict is compared by
#     path COMPONENT (so two forks' checkouts, which merely share a prefix, are
#     independent) and the bound is respected -- and then measured on the same fixture
#     repository, whose pi-forks stand-in records when each call starts and ends, so
#     overlap is observed rather than inferred from a wall clock.
    spec = importlib.util.spec_from_file_location("fork_check_concurrency", SCRIPT)
    scheduler = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(scheduler)

    # Two forks' checkouts share a path prefix and nothing else; a checkout and anything
    # under it are one resource; a check that writes nothing conflicts with nothing.
    assert scheduler.write_conflict(("/w/.forks/pi",), ("/w/.forks/pi-subagents",)) is None
    assert scheduler.write_conflict(("/w/.forks/pi",), ("/w/.forks/pi/dist",)) == "/w/.forks/pi"
    assert scheduler.write_conflict(("/w/out/frames",), ("/w/out/frames",)) == "/w/out/frames"
    assert scheduler.write_conflict((), ("/w/.forks/pi",)) is None

    # Both formerly isolated suites are admitted beside an independent check. Use the
    # scheduler's submission and completion order, not a wall-clock overlap threshold.
    mixed = [scheduler.Check("t", "independent reader", [], "", 0, cost=60),
             scheduler.Check("fork-quality", "fork pi-subagents: npm run test:unit",
                             [], "", 0, cost=60),
             scheduler.Check("self-suite", "the gate's own audit suite still passes",
                             [], "", 0, cost=60)]
    transitions = []
    scheduler.run_checks(mixed, 3, lambda _: None,
                         on_start=lambda index: transitions.append(("start", index)),
                         on_complete=lambda index: transitions.append(("complete", index)))
    assert transitions[:3] == [("start", 0), ("start", 1), ("start", 2)], transitions
    assert all(not hasattr(check, "exclusive") for check in mixed), mixed

    # A fork-quality step whose failures are ALL quarantined cases -- real-time budgets the
    # candidate the gate builds meets on an idle run and misses beside the gate's other
    # checks -- is a step this machine cannot answer, named with its reason rather than
    # reported red. The declaration is a quarantine, not a claim that the cases pass, and it
    # is narrow on purpose: every failing case must be one of the declared names, so a
    # regression beside them stays a verdict. Otherwise one upstream timing budget would
    # hide every later regression in the same suite.
    quality_label = ("fork pi-subagents: npm run test:unit in the clean checkout at "
                     "/w/.forks/candidate/pi-subagents")
    case = ("preserves canonical contact_supervisor progress and structured-interview "
            "semantics")
    append_case = ("leaves the remote operator's APPEND_SYSTEM.md out of the child's prompt")

    # Install a declaration against the fixture candidate checkout, not the real fork.
    quarantine_checkout = base / "quarantine-candidate"
    quarantined_file = quarantine_fixture_file(quarantine_checkout)
    assert scheduler.file_blob_id(quarantined_file) == QUARANTINE_FIXTURE_BLOB == sh(
        base, "git", "hash-object", str(quarantined_file)).stdout.strip()
    assert scheduler.file_blob_id(quarantine_checkout / "absent.ts") is None
    live_declarations = scheduler.QUARANTINED_FORK_QUALITY
    assert live_declarations == (), live_declarations
    fixture_declaration = scheduler.Quarantine(**QUARANTINE_FIXTURE_ARGS)
    scheduler.QUARANTINED_FORK_QUALITY = (fixture_declaration,)

    def unit_step(output, rc=1, checkout=quarantine_checkout):
        step = scheduler.Check("fork-quality", quality_label, ["npm", "run", "test:unit"],
                              "its series changed", 0, fingerprint_dir=str(checkout))
        step.rc, step.output = rc, output
        return step

    def transcript(fails, listed, count=None):
        return ("ℹ tests 4671\nℹ pass 4651\nℹ fail %s\n\n✖ failing tests:\n\n"
                "%s\n" % (len(listed) if count is None else count, fails))

    at_file = "test at test/unit/herdr-native-placement.test.ts:186:2\n"
    known = transcript(at_file + f"✖ {case} (263.675482ms)", [case])
    skip = scheduler.sentinel_skip(unit_step(known))
    assert skip is not None, known
    assert skip == (quality_label, QUARANTINE_FIXTURE_ARGS["reason"]), skip
    # Either declared case alone is the same skip.
    other = transcript(at_file + f"✖ {append_case} (381.543693ms)", [append_case])
    assert scheduler.sentinel_skip(unit_step(other)) == (quality_label, skip[1]), other
    # Two quarantined cases failing together are still only quarantined cases.
    both = transcript(at_file + f"✖ {case} (1ms)\n"
                      "test at test/unit/herdr-native-placement.test.ts:252:2\n"
                      f"✖ {append_case} (2ms)", [case, append_case])
    assert scheduler.sentinel_skip(unit_step(both)) is not None, both
    # A quarantined case beside any other failure is a verdict: the declaration covers only
    # the names it lists.
    second = transcript(at_file + f"✖ {case} (1ms)\n"
                        "test at test/unit/other.test.ts:9:2\n✖ a real regression (2ms)",
                        [case, "a real regression"])
    assert scheduler.sentinel_skip(unit_step(second)) is None, second
    # A count that disagrees with the list is a verdict too -- the transcript is not a
    # complete list of this run's failures.
    assert scheduler.sentinel_skip(unit_step(transcript(
        at_file + f"✖ {case} (1ms)", [case], count=2))) is None
    # A case in the quarantined file that the fixture declaration does not name stays
    # a verdict.
    assert scheduler.sentinel_skip(unit_step(transcript(
        at_file + "✖ some other case (1ms)", ["some other case"]))) is None
    # A DIFFERENT file failing is somebody's change, not this machine, and stays a verdict.
    assert scheduler.sentinel_skip(unit_step(transcript(
        "test/unit/some-other-file.test.ts:1:1\n✖ some other case (1ms)", ["some other case"]))) is None
    # The same case name from another file is not the quarantined one.
    assert scheduler.sentinel_skip(unit_step(transcript(
        "test at test/unit/other.test.ts:9:2\n" + f"✖ {case} (1ms)", [case]))) is None
    # A step that passed answers, so it is never a skip; a step of another kind is not
    # read by this declaration at all.
    assert scheduler.sentinel_skip(unit_step(known, rc=0)) is None
    other_kind = unit_step(known)
    other_kind.kind = "rank-order"
    assert scheduler.sentinel_skip(other_kind) is None

    # Without a fixture declaration the same failure is not quarantined or annotated.
    scheduler.QUARANTINED_FORK_QUALITY = live_declarations
    undeclared = unit_step(known)
    assert scheduler.sentinel_skip(undeclared) is None
    assert undeclared.warnings == [], undeclared.warnings

    # A declaration that has lapsed suppresses nothing: the step is the red it already is,
    # and the lapse is recorded on the check's warnings so the red explains itself instead
    # of merely appearing. Two things lapse a declaration -- it names no item, or the file
    # in the candidate checkout no longer has the content it was written against -- and
    # neither is a date or a count (docs/reference/gate-check-rules.md).
    def declaration(**fields):
        stated = dict(check=quality_label, path=QUARANTINE_FIXTURE_PATH,
                      cases=None, classification=scheduler.QUARANTINE_UPSTREAM,
                      item="quarantine/herdr-native-placement", blob=QUARANTINE_FIXTURE_BLOB,
                      reason="a quarantined timing case")
        stated.update(fields)
        return scheduler.Quarantine(**stated)

    try:
        # Matching content: the declaration applies, and the check's status says so.
        scheduler.QUARANTINED_FORK_QUALITY = (declaration(),)
        matching = unit_step(known)
        assert scheduler.sentinel_skip(matching) == (quality_label, "a quarantined timing case")
        assert scheduler.applied_quarantine(matching) is scheduler.QUARANTINED_FORK_QUALITY[0]
        assert matching.warnings == [], matching.warnings

        # Changed content: the same declaration, the same failing case, a file with other
        # bytes. The declaration stops applying and names both blob ids.
        changed_checkout = base / "quarantine-candidate-changed"
        changed_file = quarantine_fixture_file(changed_checkout, QUARANTINE_FIXTURE_CONTENT + b"\n")
        changed = unit_step(known, checkout=changed_checkout)
        assert scheduler.sentinel_skip(changed) is None, "a declaration applied to a changed file"
        assert scheduler.applied_quarantine(changed) is None
        assert any(f"written against blob {QUARANTINE_FIXTURE_BLOB}" in warning
                   and f"is now blob {scheduler.file_blob_id(changed_file)}" in warning
                   for warning in changed.warnings), changed.warnings
        # The lapse is recorded once however often the step is read.
        scheduler.sentinel_skip(changed)
        assert len(changed.warnings) == 1, changed.warnings

        # A file that cannot be read is not a match, and neither is a step that recorded no
        # candidate checkout at all.
        for checkout in (base / "quarantine-candidate-absent", ""):
            unreadable = unit_step(known, checkout=checkout)
            assert scheduler.sentinel_skip(unreadable) is None, checkout
            assert any("could not be read" in warning for warning in unreadable.warnings), \
                unreadable.warnings

        scheduler.QUARANTINED_FORK_QUALITY = (declaration(item=""),)
        ticketless = unit_step(known)
        assert scheduler.sentinel_skip(ticketless) is None, "a declaration with no item still suppressed its case"
        assert any("no queue item" in warning for warning in ticketless.warnings), ticketless.warnings

        # No count refuses a declaration: eleven of them, four on this one check, and the
        # step's own declaration still applies.
        scheduler.QUARANTINED_FORK_QUALITY = tuple(declaration() for _ in range(4)) + tuple(
            declaration(check=f"another check #{n}") for n in range(7))
        assert scheduler.sentinel_skip(unit_step(known)) is not None, \
            "a declaration was refused for how many declarations there are"
    finally:
        scheduler.QUARANTINED_FORK_QUALITY = live_declarations

    # The retired mechanisms are gone by name, and nothing about a quarantine reads a
    # clock: no expiry, no budget, and no "unanswerable" classification for a case that
    # ran and failed.
    for gone in ("QUARANTINE_BUDGET_REPOSITORY", "QUARANTINE_BUDGET_PER_CHECK",
                 "quarantine_budget_lapse", "QUARANTINE_UNANSWERABLE",
                 "UNANSWERABLE_FORK_QUALITY"):
        assert not hasattr(scheduler, gone), gone
        assert gone not in SCRIPT.read_text(), gone
    gate_text = SCRIPT.read_text()
    quarantine_code = (
        gate_text[gate_text.index("def file_blob_id("):gate_text.index("\nQUARANTINED_FORK_QUALITY:")]
        + gate_text[gate_text.index("def declared_quarantine("):gate_text.index("def sentinel_skip(")])
    assert "class Quarantine:" in quarantine_code and "def applied_quarantine(" in quarantine_code
    for clock_read in ("datetime", "time.", "today", "expires"):
        assert clock_read not in quarantine_code, clock_read
    assert "agent-runtime" not in SCRIPT.read_text()

    # The bound: four readers, two at a time, never three. And with nothing to conflict
    # with they really do overlap, so the bound is the only thing limiting them.
    readers = [scheduler.Check("t", f"reader {n}", [], "", 0) for n in range(4)]
    active = []
    peak_seen = []

    def read(index):
        active.append(index)
        peak_seen.append(len(active))
        time.sleep(0.15)
        active.remove(index)

    wall, peak, halted = scheduler.run_checks(readers, 2, read)
    assert peak == 2, peak
    # The overlap IS the assertion, and `peak_seen` proves it directly: two readers were
    # in flight at once, which a serial scheduler cannot produce. The wall-clock bound that
    # used to sit here (`wall < 4 * 0.15`) asserted the same thing indirectly and broke
    # under the gate's own load -- four 0.15 s sleeps overlapped two at a time measured
    # 1.3548 s beside twelve other checks, and no bound tight enough to prove overlap
    # survives that. A timing bound that measures the machine instead of the scheduler is
    # the flake this suite exists to refuse, so it is gone rather than loosened.
    assert max(peak_seen) == 2, peak_seen

    # `after` orders a reader behind a writer it shares no path with. The prepare step is
    # the only writer of a checkout and a typecheck writes nothing, so a write set cannot
    # express that the typecheck must not read the tree while it is being materialized
    # (board item 405). The reader here writes nothing and names the writer, so it must
    # start only after the writer finished -- and the two readers, both ordered behind it,
    # still run at the same time.
    ordered = [scheduler.Check("t", "prepare", [], "", 0, writes=("/w/checkout",)),
               scheduler.Check("t", "reader a", [], "", 0, after=("prepare",)),
               scheduler.Check("t", "reader b", [], "", 0, after=("prepare",))]
    started, finished = [], []

    def ordered_run(index):
        started.append(index)
        time.sleep(0.1)
        finished.append(index)

    scheduler.run_checks(ordered, 3, ordered_run)
    assert started[0] == 0, started
    assert set(started[1:]) == {1, 2}, started
    assert finished[0] == 0, finished
    # A label the plan does not hold is satisfied, so a preflight that drops a check whose
    # working copy is absent does not leave its dependents waiting forever.
    orphan = [scheduler.Check("t", "reader", [], "", 0, after=("a check this plan dropped",))]
    scheduler.run_checks(orphan, 1, lambda index: None)

    # Two writers of one path plus a reader of another: the writers never overlap and the
    # reader still runs beside one of them.
    writers = [scheduler.Check("t", "writer a", [], "", 0, writes=("/w/artifacts",)),
               scheduler.Check("t", "writer b", [], "", 0, writes=("/w/artifacts",)),
               scheduler.Check("t", "reader", [], "", 0, writes=())]
    holding = set()
    collisions = []
    concurrent_with_reader = []

    def write_or_read(index):
        paths = writers[index].writes
        for path in paths:
            if path in holding:
                collisions.append((index, path))
        holding.update(paths)
        if not paths:
            # Sample after a moment: the reader starts beside the writer, and which thread
            # touches the shared set first is the race this is trying to observe.
            time.sleep(0.05)
            concurrent_with_reader.append(len(holding))
        time.sleep(0.15)
        holding.difference_update(paths)

    scheduler.run_checks(writers, 3, write_or_read)
    assert collisions == [], collisions
    assert max(concurrent_with_reader) >= 1, concurrent_with_reader
    try:
        scheduler.run_checks(readers, 0, read)
    except ValueError:
        pass
    else:
        raise AssertionError("jobs=0 must be refused rather than run unbounded")

    # Why a check waited is recorded, not only how long it ran. Each pending check's
    # interval is charged to the rule that held it, so a start delay is attributable from
    # the record. The waits below are guaranteed by the blocking event, not by machine
    # speed: a check cannot start until the event that frees it, so its wait is positive
    # however fast the box is. The first check in each case starts at once and is charged
    # nothing, which is the control that keeps the assertion from passing on a constant.
    def waited(check):
        return check.waited

    # The pool: one slot, three readers. The first starts at once; the other two wait on
    # the bound, and neither is charged to a write conflict or the cheap barrier.
    pool_checks = [scheduler.Check("t", f"pool {n}", [], "", 0) for n in range(3)]

    def pool_run(index):
        if index == 0:
            time.sleep(0.05)

    scheduler.run_checks(pool_checks, 1, pool_run)
    assert waited(pool_checks[0]) == {"pool": 0.0, "cheap": 0.0,
                                      "heavy": 0.0, "writes": 0.0,
                                      "after": 0.0}, waited(pool_checks[0])
    assert waited(pool_checks[1])["pool"] > 0, waited(pool_checks[1])
    assert waited(pool_checks[2])["pool"] > 0, waited(pool_checks[2])
    assert waited(pool_checks[1])["writes"] == 0, waited(pool_checks[1])
    assert waited(pool_checks[1])["cheap"] == 0, waited(pool_checks[1])

    # A write conflict: two writers of one path, two slots. The second waits on the path,
    # not on the bound, because a slot is free the whole time.
    write_checks = [scheduler.Check("t", "writer a", [], "", 0, writes=("/w/one",)),
                    scheduler.Check("t", "writer b", [], "", 0, writes=("/w/one",))]

    def write_run(index):
        if index == 0:
            time.sleep(0.05)

    scheduler.run_checks(write_checks, 2, write_run)
    assert waited(write_checks[0])["writes"] == 0, waited(write_checks[0])
    assert waited(write_checks[1])["writes"] > 0, waited(write_checks[1])
    assert waited(write_checks[1])["pool"] == 0, waited(write_checks[1])

    # The cheap_first barrier: a cheap check and a suite, two slots. The suite waits on
    # the barrier even though a slot is free, and the cheap check is charged nothing.
    cheap_check = scheduler.Check("t", "cheap", [], "", 0, cost=0.0)
    suite_check = scheduler.Check("t", "suite", [], "", 0,
                                  cost=scheduler.CHEAP_CHECK_SECONDS + 1)

    def cheap_run(index):
        if index == 0:
            time.sleep(0.05)

    scheduler.run_checks([cheap_check, suite_check], 2, cheap_run, cheap_first=True)
    assert waited(cheap_check)["cheap"] == 0, waited(cheap_check)
    assert waited(suite_check)["cheap"] > 0, waited(suite_check)
    assert waited(suite_check)["pool"] == 0, waited(suite_check)

    # The same rules on a fixture repository. The pi-forks stand-in sleeps and records a
    # start and an end stamp, so this case can say when two steps overlapped instead of
    # arguing from elapsed time.
    window = base / "pi-forks-window"
    (repo / "bin" / "pi-forks").write_text(
        "#!/bin/sh\n"
        # The candidate preflight is fast and runs inside the fork-quality step, so it is
        # answered without the sleep this stand-in uses to make overlap observable: it is
        # not a writer of its own (board item 194).
        'case "$*" in "candidate --check"*) exit 0 ;; esac\n'
        # A refusal, when a marker asks for one: INV-3's own words, so the check reads as
        # the bridge's verdict rather than as an inability to answer.
        f'if [ -f "{base}/pi-forks-refuse" ] && [ "$1" = "patch-branch" ] && [ "$2" = "verify" ]; then\n'
        '  echo "   FAILED: the series is not the export of refs/heads/main"\n'
        "  exit 1\n"
        "fi\n"
        f'printf "start %s|%s\\n" "$(date +%s%N)" "$*" >> "{window}"\n'
        "sleep 1\n"
        f'printf "end %s|%s\\n" "$(date +%s%N)" "$*" >> "{window}"\n'
        "exit 0\n"
    )
    (repo / "bin" / "pi-forks").chmod(0o755)
    # The driver's own suite is a step of this plan too, because `bin/pi-forks` changes, and
    # the case above leaves its stand-in FAILING -- that is what proves a failing case is a
    # verdict. This case is about the scheduler, so it restores a passing stand-in: a red
    # step would fail the very run the overlap is measured in.
    (repo / "tests" / "pi-forks.test.sh").write_text(
        "#!/bin/sh\necho 'ok   stub pi-forks driver'\n"
    )
    # Committed, not just written: `bin/pi-forks` is a path these checks read, so a dirty
    # one refuses the run before any of them starts -- which is the gate's own rule.
    commit(repo, "a pi-forks stand-in that records when each call runs")

    def windows():
        """Each call as (argv, start_ns, end_ns), matched by its argument string."""
        open_calls, spans = {}, []
        for line in window.read_text().splitlines():
            kind, rest = line.split(" ", 1)
            stamp, argv = rest.split("|", 1)
            if kind == "start":
                open_calls[argv] = int(stamp)
            else:
                spans.append((argv, open_calls.pop(argv), int(stamp)))
        return spans

    def overlapping(left, right):
        return left[1] < right[2] and right[1] < left[2]

    def any_overlap(spans):
        return any(overlapping(left, right)
                   for index, left in enumerate(spans) for right in spans[index + 1:])

    # `--all` plans the merge question for both forks on every run, and pi-subagents' one
    # prepare step and two source checks with them, so the two runs below judge the same
    # plan. The merge questions write different candidate checkouts, so they are allowed to
    # run at the same time; the prepare step writes pi-subagents' checkout, so it waits for
    # that fork's merge question, and the two source checks wait for the prepare step.
    serial = run(repo, env, "--all", "--jobs", "1")
    assert serial.returncode == 0, serial.stdout + serial.stderr
    serial_timings = json.loads((repo / ".git" / "yolo-fork-check" / "timings.jsonl")
                                .read_text().splitlines()[-1])
    assert serial_timings["jobs"] == 1, serial_timings
    assert serial_timings["peak_concurrency"] == 1, serial_timings
    # Every check's record names why it waited, and a serial run's later checks waited on
    # the bound: the first check is the control that started at once.
    assert all(set(one["waited"]) == {"pool", "cheap", "heavy", "writes", "after"}
               for one in serial_timings["checks"]), serial_timings
    assert serial_timings["checks"][0]["waited"] == {"pool": 0.0, "cheap": 0.0,
                                                      "heavy": 0.0,
                                                      "writes": 0.0, "after": 0.0}, \
        serial_timings["checks"][0]
    assert any(sum(one["waited"].values()) > 0 for one in serial_timings["checks"][1:]), \
        serial_timings
    # The plan's own drift probe is not a check: it runs while the plan is built, before
    # the check phase, and the stub records every call the gate makes.
    serial_spans = [span for span in windows() if not span[0].startswith("patch-branch target")]
    assert len(serial_spans) == 5, serial_spans
    assert not any_overlap(serial_spans), serial_spans
    window.unlink()

    concurrent = run(repo, env, "--all", "--jobs", "4")
    assert concurrent.returncode == 0, concurrent.stdout + concurrent.stderr
    concurrent_timings = json.loads((repo / ".git" / "yolo-fork-check" / "timings.jsonl")
                                    .read_text().splitlines()[-1])
    assert concurrent_timings["jobs"] == 4, concurrent_timings
    assert concurrent_timings["peak_concurrency"] >= 2, concurrent_timings
    # The concurrency actually achieved is the sum over the check phase's wall clock: the
    # same checks, the same seconds, less wall clock. That is the whole claim.
    serial_ratio = serial_timings["check_seconds"] / serial_timings["check_wall_seconds"]
    concurrent_ratio = (concurrent_timings["check_seconds"]
                        / concurrent_timings["check_wall_seconds"])
    assert serial_ratio < 1.1, serial_ratio
    # Assert overlap, not how much: write conflicts and prerequisites affect the ratio.
    assert concurrent_ratio > 1.0, concurrent_ratio
    concurrent_spans = [span for span in windows() if not span[0].startswith("patch-branch target")]
    assert len(concurrent_spans) == 5, concurrent_spans
    assert any_overlap(concurrent_spans), concurrent_spans
    # The prepare step is the only writer of pi-subagents' candidate checkout, and the two
    # source checks wait for it even with four slots free. Their admission alongside
    # independent checks is tested above from the scheduler's recorded start order.
    prepare = [span for span in concurrent_spans if span[0] == "candidate --fork pi-subagents --deps"]
    readers = [span for span in concurrent_spans if "--prepared" in span[0]]
    assert len(prepare) == 1 and len(readers) == 2, concurrent_spans
    assert not any(overlapping(prepare[0], reader) for reader in readers), concurrent_spans
    # Plan order, not completion order: both calls took the same second, so the order they
    # finished in is a race, and the run must still report the plan's order.
    assert concurrent.stderr.index("fork pi-background-tasks: the series merges the upstream") \
        < concurrent.stderr.index("fork pi-subagents: the series merges the upstream"), concurrent.stderr
    timings_line = [line for line in concurrent.stdout.splitlines()
                    if line.startswith("fork-check: timings --")][0]
    assert "wall clock" in timings_line and "x concurrent (peak" in timings_line, timings_line
    assert "4 at a time" in timings_line, timings_line
    window.unlink()

    # A FORK WHOSE TWO QUALITY SCRIPTS WRITE DISJOINT PATHS RUNS THEM AT THE SAME TIME,
    # after one prepare step. pi-session-search's `check` is `tsc --noEmit` (writes
    # nothing) and its `test` rebuilds the bundle into `dist/`; the two can overlap.
    search = upstream(base, "pi-session-search")
    manifest = repo / "yolo-packs/matt/pack.json"
    data = json.loads(manifest.read_text())
    data["contributes"].append({
        "kind": "files", "into": ".pi/agent/yolo-patched/pi-session-search",
        "source": f"git+{search}?ref=main", "patches": "patches/pi-session-search",
    })
    manifest.write_text(json.dumps(data) + "\n")
    search_patch = repo / "yolo-packs/matt/patches/pi-session-search/0001-x.patch"
    search_patch.parent.mkdir()
    search_patch.write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nx\n")
    (repo / ".forks/pi-session-search").mkdir()
    (repo / ".forks/pi-session-search/package.json").write_text(
        '{"scripts":{"check":"tsc","test":"node --test"}}\n')
    commit(repo, "add the session-search series")
    window.unlink(missing_ok=True)
    result = run(repo, env, "--jobs", "4")
    assert result.returncode == 0, result.stdout + result.stderr
    spans = windows()
    prepare = [span for span in spans if span[0] == "candidate --fork pi-session-search --build"]
    check = [span for span in spans
             if span[0] == "candidate --fork pi-session-search --prepared -- npm run check"]
    test = [span for span in spans
            if span[0] == "candidate --fork pi-session-search --prepared -- npm run test"]
    assert len(prepare) == 1 and len(check) == 1 and len(test) == 1, spans
    # The prepare step is the only writer, and both readers wait for it -- the typecheck
    # writes nothing, so `after` is what orders it, not a shared write.
    assert not overlapping(prepare[0], check[0]), spans
    assert not overlapping(prepare[0], test[0]), spans
    # The readers write disjoint paths, so they run at the same time.
    assert overlapping(check[0], test[0]), spans
    window.unlink()

    # A changed series plans the replay and the build of one fork, and both write that
    # fork's working copy: they must not overlap, whatever the bound.
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nw\n"
    )
    commit(repo, "change the series")
    result = run(repo, env, "--jobs", "4")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    spans = windows()
    replay = [span for span in spans if span[0] == "candidate --fork pi-subagents"]
    build = [span for span in spans if span[0] == "candidate --fork pi-subagents --build"]
    assert len(replay) == 1 and len(build) == 1, spans
    assert not overlapping(replay[0], build[0]), spans
    # And the report says which paths each writer holds, so a reader sees why it waited.
    assert "writes: " in result.stdout, result.stdout
    assert str(repo / ".forks" / "pi-subagents") in result.stdout, result.stdout

    # A failing check is reported the same way whether or not it overlapped another. INV-3
    # is a reader, so it runs beside the writers above; the same tree is judged at --jobs 1
    # and at --jobs 4, and the two reports must be identical apart from their durations.
    (base / "pi-forks-refuse").write_text("1\n")
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nv\n"
    )
    commit(repo, "the series is not the export")

    def report_body(out):
        """The report before its timings block, with every duration replaced.

        Durations are the one thing two runs of the same tree cannot share; everything
        else -- the headline, each check's block, the failing check's own output -- is what
        "reported the same way" means.
        """
        body = out.split("fork-check: timings --")[0]
        return re.sub(r"\d+m\d+s|\d+\.\ds", "<time>", body)

    failed_serial = run(repo, env, "--jobs", "1")
    assert failed_serial.returncode == 1, failed_serial.stdout + failed_serial.stderr
    failed_concurrent = run(repo, env, "--jobs", "4")
    assert failed_concurrent.returncode == 1, failed_concurrent.stdout + failed_concurrent.stderr
    assert "fork-check: FAILED -- 1 of" in failed_concurrent.stdout, failed_concurrent.stdout
    assert "is not the export of refs/heads/main" in failed_concurrent.stdout, failed_concurrent.stdout
    assert "FAIL patch-branch-inv3" in failed_concurrent.stderr, failed_concurrent.stderr
    # The verdict is the same in both, but the NOT-RUN tail is not: a red check stops the
    # pass (board item 337), so --jobs 1 has not yet started the checks that --jobs 4 had
    # in flight and terminated. What must not differ is the check blamed and the words of
    # the verdict; the stopped tail is the scheduling's own honest account.
    for failed in (failed_serial, failed_concurrent):
        assert "fork-check: FAILED -- 1 of" in failed.stdout, failed.stdout
        assert "patch-branch-inv3: fork pi-subagents: the series is exactly its working copy's export" in failed.stdout, failed.stdout
        assert "is not the export of refs/heads/main" in failed.stdout, failed.stdout
        assert "did not run because the verdict was already red" in failed.stdout, failed.stdout
    assert "not started: patch-branch-inv3" in failed_serial.stdout, failed_serial.stdout
    assert "terminated when the run stopped on a red check" in failed_concurrent.stdout, failed_concurrent.stdout
    # The failing run advanced nothing, so the same change is still open.
    assert record(repo)["runs"][-1]["outcome"] == "failed", record(repo)["runs"][-1]

_shard_boundary("concurrent-quality-and-hooks", 24)

# 46b. A SEEDED FAILURE IN EACH QUALITY SCRIPT IS THE SAME VERDICT CONCURRENTLY AS
#      SERIALLY. The split runs a fork's two quality scripts at the same time when their
#      writes are disjoint, so the property that matters is that concurrency changes no
#      verdict: the same tree, judged at --jobs 1 and at --jobs 4, must blame the same
#      check. Each script is seeded in turn, so neither can hide behind the other, and the
#      sibling is asserted NOT blamed (board item 405).
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    assert run(repo, env).returncode == 0
    search = upstream(base, "pi-session-search")
    manifest = repo / "yolo-packs/matt/pack.json"
    data = json.loads(manifest.read_text())
    data["contributes"].append({
        "kind": "files", "into": ".pi/agent/yolo-patched/pi-session-search",
        "source": f"git+{search}?ref=main", "patches": "patches/pi-session-search",
    })
    manifest.write_text(json.dumps(data) + "\n")
    patch = repo / "yolo-packs/matt/patches/pi-session-search/0001-x.patch"
    patch.parent.mkdir()
    patch.write_text("From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nx\n")
    (repo / ".forks/pi-session-search").mkdir()
    (repo / ".forks/pi-session-search/package.json").write_text(
        '{"scripts":{"check":"tsc","test":"node --test"}}\n')
    commit(repo, "add the session-search series")

    def blamed(result):
        """The checks the run blamed, in the streaming report's own words.

        The duration is the one thing two runs of the same tree cannot share, so it is
        replaced; the check, its label and its verdict are what "the same verdict" means.
        """
        return sorted(re.sub(r" \(\d+m\d+s|\d+\.\ds\)$", "", line)
                      for line in result.stderr.splitlines()
                      if line.startswith("fork-check: FAIL "))

    for script in ("check", "test"):
        seeded = f"candidate --fork pi-session-search --prepared -- npm run {script}"
        (base / "pi-forks-fail").write_text(seeded)
        (base / "pi-forks-rc").write_text("1")
        serial = run(repo, env, "--jobs", "1")
        concurrent = run(repo, env, "--jobs", "4")
        assert serial.returncode == 1, serial.stdout + serial.stderr
        assert concurrent.returncode == 1, concurrent.stdout + concurrent.stderr
        assert blamed(serial) == blamed(concurrent), (
            script, blamed(serial), blamed(concurrent))
        assert len(blamed(serial)) == 1, blamed(serial)
        assert f"fork pi-session-search: npm run {script} in the clean checkout" in blamed(serial)[0], \
            blamed(serial)
        # The sibling is not blamed: a seeded failure in one script is not a failure in both.
        other = "test" if script == "check" else "check"
        assert not any(f"npm run {other} in the clean checkout" in line for line in blamed(serial)), \
            blamed(serial)
    clear(base, "pi-forks-fail", "pi-forks-rc")

# 47. `just hooks-install` has a suite in the gate. The installer keeps the shared hooks
#     directory in step with hooks/, and nothing else asserts it: a copy carrying this
#     repository's marker replaced with drift reported on every run, a hook without the
#     marker left alone and chained, and one report line per hook. It is planned by a
#     change to the installer, to anything under hooks/, to the `.justfile` recipe that
#     calls it, or to the suite, and the suite builds its own repository under the system
#     temp directory so the step writes no shared hooks directory.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    suite = repo / "tests" / "hooks-install.test.sh"
    suite.write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$*" >> "{base}/hooks-install-calls"\n'
        "echo 'ok   stub hooks install'\n"
    )
    (repo / "bin" / "hooks-install").write_text("#!/bin/sh\nexit 0\n")
    commit(repo, "the hooks installer and the suite that pins it")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "  run  hooks-install" in result.stdout, result.stdout
    assert "bin/hooks-install: a marked copy is replaced" in result.stdout, result.stdout
    assert calls(base, "hooks-install-calls") == [""], calls(base, "hooks-install-calls")
    # A hook change plans the step too: `hooks/` is watched as a directory, so a new hook,
    # or an edit to one, is not silently outside the installer's coverage. The fixture adds
    # a hook whose name no other step watches, so this case is about the installer alone.
    clear(base, "hooks-install-calls")
    (repo / "hooks").mkdir()
    (repo / "hooks" / "post-commit").write_text("#!/bin/sh\nexit 0\n")
    commit(repo, "a hook the installer installs")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "  run  hooks-install" in result.stdout, result.stdout
    assert calls(base, "hooks-install-calls") == [""], calls(base, "hooks-install-calls")

# 47b. The observatory recipes' suite is checked only for its five inputs.
#      The fixture runs a stand-in suite; the real supervision suite has its own controls.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    # Tool availability must not depend on the machine running this routing suite.
    for tool in ("just", "hivemind"):
        stub = base / "stubs" / tool
        stub.write_text("#!/bin/sh\nexit 0\n")
        stub.chmod(0o755)
    dev_test = "tests/observatory-dev.test.py"
    inputs = (".justfile", "tools/observatory/run.sh", "tools/tillr-view/Procfile.dev",
              "mise.toml", dev_test)
    (repo / dev_test).write_text(
        "from pathlib import Path\n"
        f"Path({str(base / 'observatory-dev-calls')!r}).open('a').write('dev\\n')\n"
        "print('ok   stub observatory dev supervision')\n"
    )
    (repo / "tests/hooks-install.test.sh").write_text("echo 'ok   stub hooks install'\n")
    (repo / "tests/fork-check.test.py").write_text("print('ok   stub gate audit')\n")
    commit(repo, "the observatory dev suite")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    clear(base, "observatory-dev-calls")
    (repo / ".justfile").write_text("# the dev recipe changed\n")
    commit(repo, "edit the observatory dev recipe")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "  run  observatory-dev" in result.stdout, result.stdout
    assert calls(base, "observatory-dev-calls") == ["dev"], calls(base, "observatory-dev-calls")

    module = load_tool("fork_check_observatory_dev")
    original_which = module.shutil.which
    try:
        module.shutil.which = lambda name, *a, **k: (
            f"/stub/{name}" if name in ("just", "hivemind", "python3")
            else original_which(name, *a, **k)
        )
        def dev_plan(changed):
            planned = module.build_plan(
                repo, [], module.Record(base / "plan-state.json"), changed, None, True,
            )
            return [c for c in planned.checks if c.kind == "observatory-dev"], planned

        for path in inputs:
            checks, planned = dev_plan([path])
            assert len(checks) == 1, (path, [c.kind for c in planned.checks])
            check = checks[0]
            assert check.argv == ["python3", dev_test], check.argv
            assert check.watches == inputs, check.watches
            # Broken recipes consume the suite's bounded failure waits before naming
            # their assertions; a 120s limit cut them off as unanswerable, not red.
            assert check.timeout == module.OBSERVATORY_DEV_TIMEOUT == 600, check.timeout
            assert module.CHECK_TIER[check.kind] == module.TIER_LANDING
            assert check.cost == 1.4 < module.CHEAP_CHECK_SECONDS, check.cost
        for path in ("bashrc", ".justfile.bak", "mise.toml.bak", dev_test + ".bak",
                     "tools/tillr-view/Procfile.dev.bak", "tools/tillr-view/serve/server.go",
                     "tools/observatory/run.sh.bak"):
            checks, planned = dev_plan([path])
            assert checks == [], (path, checks)
            assert any(s.kind == "observatory-dev" and not s.planned
                       for s in planned.skips), planned.skips
        for missing in ("just", "hivemind", "python3"):
            module.shutil.which = lambda name, *a, **k: (
                None if name == missing else f"/stub/{name}"
            )
            checks, planned = dev_plan([".justfile"])
            assert checks == [], (missing, checks)
            assert any(s.kind == "observatory-dev" and s.planned and missing in s.why
                       for s in planned.skips), (missing, planned.skips)
    finally:
        module.shutil.which = original_which
print("ok   47b observatory-dev: recipe change runs the suite, exact inputs select it, missing tools cannot answer")

# 44. The shared pipeline-evidence format's conformance step. The gate is the producer that
#     still ships here, so a change to the format or to this checker has to run the suite
#     that validates a record against the one schema. This case proves the step is planned
#     by the format's own paths and by the producer, that it is executed rather than merely
#     listed, and that a repository where nothing moved names it as not run.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)

    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert calls(base, "pipeline-evidence-calls") == [], calls(base, "pipeline-evidence-calls")
    assert f"  {'not run':<16} pipeline-evidence (tests/pipeline-evidence.test.py)" in result.stdout, \
        result.stdout

    # The emitter is wired into the run, not only into a builder a unit test can call: a
    # real gate run leaves the shared records beside its own record, one per check and one
    # for the run, each of the format's shape.
    recorded = repo / ".git" / "yolo-fork-check" / "evidence.jsonl"
    assert recorded.is_file(), "a gate run recorded no pipeline evidence"
    lines = [json.loads(line) for line in recorded.read_text().splitlines() if line.strip()]
    assert lines, recorded
    assert all(line["version"] == 1 and line["component"] == "gate" for line in lines), lines
    assert all(line["stage"] == "gate" for line in lines), lines
    steps = [line["step"] for line in lines]
    assert "run" in steps, steps
    assert steps[-1] == "run", f"the run record closes the file: {steps}"
    # The wait split travels with the shared record too, so a consumer that reads only
    # the evidence file can attribute a start delay without the timings timeline.
    check_records = [line for line in lines if line["step"] != "run"]
    assert check_records, lines
    assert all(set(line["detail"]["waited"]) == {"pool", "cheap", "heavy",
                                                "writes", "after"}
               for line in check_records), check_records

    # A path only this step watches: the format's own directory.
    (repo / "tools" / "pipeline-evidence" / "schema").mkdir(parents=True)
    (repo / "tools" / "pipeline-evidence" / "schema" / "evidence-v1.json").write_text("{}\n")
    commit(repo, "move the evidence schema")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  pipeline-evidence" in result.stdout, result.stdout
    assert calls(base, "pipeline-evidence-calls") == ["pipeline-evidence"], \
        calls(base, "pipeline-evidence-calls")

    # And the gate, which is the producer that still ships here: an edit to this checker
    # runs the suite too. The fixture plans the step from the changed path alone -- here the
    # point is that the producer's path is in this step's watches at all.
    clear(base, "pipeline-evidence-calls")
    plan = importlib.util.spec_from_file_location("fork_check_evidence_plan",
                                                  repo / "tools" / "fork-check.py")
    module = importlib.util.module_from_spec(plan)
    plan.loader.exec_module(module)
    planned = module.build_plan(
        repo, [], module.Record(base / "plan-state.json"), ["tools/fork-check.py"], None, True,
    )
    steps = [check for check in planned.checks if check.kind == "pipeline-evidence"]
    assert len(steps) == 1, [(check.kind, check.label) for check in planned.checks]
    assert steps[0].argv == [sys.executable, "tests/pipeline-evidence.test.py"], steps[0].argv
    assert steps[0].watches == module.PIPELINE_EVIDENCE_WATCHES, steps[0].watches
    assert calls(base, "pipeline-evidence-calls") == [], calls(base, "pipeline-evidence-calls")

    # The reload-residue audit reads the registry document and every staged extension source,
    # so any of them plans it -- and it names the guard's own test as its argv, not the tool,
    # because the test audits the tree and then breaks one thing at a time in a fixture.
    planned = module.build_plan(
        repo, [], module.Record(base / "plan-state.json"),
        ["yolo-packs/matt/files/extensions/or-route.ts"], None, True,
    )
    steps = [check for check in planned.checks if check.kind == "extension-residue"]
    assert len(steps) == 1, [(check.kind, check.label) for check in planned.checks]
    assert steps[0].argv == [sys.executable, "tests/extension-residue.test.py"], steps[0].argv
    assert steps[0].watches == module.RESIDUE_WATCHES, steps[0].watches
    assert calls(base, "extension-residue-calls") == [], calls(base, "extension-residue-calls")

    # The active-steering skill's own text is a step of its own: nothing else in the gate
    # reads a pack skill. Editing the skill plans the test, which runs on the interpreter
    # running the gate and records that it executed rather than being merely listed.
    clear(base, "active-steering-skill-calls")
    planned = module.build_plan(
        repo, [], module.Record(base / "plan-state.json"),
        ["yolo-packs/matt-agentwork/skills/active-steering/SKILL.md"], None, True,
    )
    steps = [check for check in planned.checks if check.kind == "active-steering-skill"]
    assert len(steps) == 1, [(check.kind, check.label) for check in planned.checks]
    assert steps[0].argv == [sys.executable, "tests/active-steering-skill.test.py"], steps[0].argv
    assert steps[0].watches == module.ACTIVE_STEERING_WATCHES, steps[0].watches
    assert calls(base, "active-steering-skill-calls") == [], calls(base, "active-steering-skill-calls")
    skill = repo / "yolo-packs" / "matt-agentwork" / "skills" / "active-steering" / "SKILL.md"
    skill.write_text("# Active steering\n\nA checkpoint is every 15 minutes.\n")
    commit(repo, "write a cadence back into the active-steering skill")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "run  active-steering-skill" in result.stdout, result.stdout
    assert calls(base, "active-steering-skill-calls") == ["skill"], \
        calls(base, "active-steering-skill-calls")

    # 49. THE PLAN LINE, and the fixed integrity set under it. Every run ends with one line
    #     of keys -- the commit its diff was taken from, how many steps it ran and did not,
    #     what it cost against the full gate, and how much of the integrity set it carried --
    #     because a landing path that reuses a verdict, or an operator asking what a gate run
    #     cost, needs the selection as data rather than as prose. It is printed LAST so that
    #     the tail a consumer keeps cannot cut it off.
    clear(base, "yolo-calls", "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    lines = [line for line in result.stdout.splitlines() if line.strip()]
    assert lines[-1].startswith("fork-check: plan "), lines[-3:]
    plan_fields = dict(field.split("=", 1) for field in lines[-1].split()[2:])
    assert set(plan_fields) == {"base", "head", "ran", "notrun", "seconds", "full",
                                "saved", "priced", "integrity", "checks", "required",
                                "reused", "tier", "owed", "landing", "landing_class",
                                "runner_count", "runner_landing"}, plan_fields
    assert plan_fields["landing_class"] == "ordinary", plan_fields
    assert plan_fields["runner_count"] == "0", plan_fields
    assert float(plan_fields["runner_landing"]) == 0.0, plan_fields
    # A bare run is the full gate: no tier was requested, nothing is owed, and the
    # landing-wall model is the landing subset of the checks this run just measured.
    assert plan_fields["tier"] == "full", plan_fields
    assert plan_fields["owed"] == "-", plan_fields
    assert float(plan_fields["landing"]) >= 0.0, plan_fields
    # Nothing changed, so the names are the integrity set alone -- and every kind the plan
    # requires is one it ran: that is the pair a landing reads rather than guessing which
    # steps a diff selected.
    assert plan_fields["checks"] == "file-size", plan_fields
    assert plan_fields["required"] == plan_fields["checks"], plan_fields
    assert plan_fields["head"] == sh(repo, "git", "rev-parse", "HEAD").stdout.strip(), plan_fields
    # A run with nothing to do still carries the integrity set: that is the floor under the
    # selection, and the surviving file-size warning runs every time.
    assert plan_fields["integrity"] == "1", plan_fields
    assert "  run  file-size " in result.stdout, result.stdout

    # A change no step but the documents and the integrity set reaches: the plan is a
    # fraction of the gate, and the line says so against the record's own full-gate price.
    verified_before = record(repo)["baseline"]["commit"]
    (repo / "docs").mkdir(exist_ok=True)
    (repo / "docs" / "plan-line.md").write_text("# a document\n\nThe plan line names what ran.\n")
    commit(repo, "docs: a document for the plan line")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    lines = [line for line in result.stdout.splitlines() if line.strip()]
    plan_fields = dict(field.split("=", 1) for field in lines[-1].split()[2:])
    assert plan_fields["base"] == verified_before, (plan_fields, verified_before)
    # The doc change reaches the documents step, and the plan names it: a landing reads
    # this pair rather than reconstructing the selection from the tree.
    assert "waybar" not in plan_fields["checks"].split(","), plan_fields
    assert set(plan_fields["required"].split(",")) <= set(plan_fields["checks"].split(",")), plan_fields
    ran, notrun, priced = int(plan_fields["ran"]), int(plan_fields["notrun"]), int(plan_fields["priced"])
    seconds, full, saved = (float(plan_fields[key]) for key in ("seconds", "full", "saved"))
    assert notrun > 0, plan_fields
    assert priced > ran, plan_fields
    # The estimate is summed from the record's own per-step prices at 0.1 s resolution, and
    # a step that records 0.0 can still cost a few hundredths here, so allow one rounding
    # unit. The claim being pinned is that the figure is never SMALLER than the run that was
    # actually paid for -- not that it is exact. (The identity guard made this visible: it
    # is the one integrity step that shells out, so it is the first to cost >0.05 s.)
    assert full >= seconds - 0.1, plan_fields
    # `saved` is the implementation's own arithmetic, and it is CLAMPED: `full - spent`,
    # and never negative, because a run cannot save less than nothing. With the fixture's
    # stub prices an integrity step can cost more than the whole estimate (the rounding unit
    # allowed above), so state the formula the implementation uses rather than assuming the
    # estimate is the larger number.
    assert abs(saved - max(0.0, round(full - seconds, 1))) < 0.05, plan_fields
    # The selection is the point: a document change must not have run the whole gate.
    assert ran < priced, plan_fields

    # The full-gate figure is a floor under the run it describes, not only a sum of past
    # prices. Two recorded steps at 0.02s each round the estimate to 0.0, so a run whose
    # own documents step took 0.1s priced the gate BELOW the part of it that just ran and
    # the line above read as a flake (2026-10-29). A subset cannot cost more than the
    # whole gate it is part of, so the figure never reads below this run's own cost.
    floor_state = base / "plan-line-floor.json"
    floor_state.write_text(json.dumps({
        "schema": module.SCHEMA, "producer": module.PRODUCER,
        "targets": {}, "inputs": {}, "triage": {},
        "runs": [{"checks": [{"label": "a fast recorded step", "seconds": 0.02},
                             {"label": "another fast recorded step", "seconds": 0.02}]}],
    }))
    floor_plan = module.Plan()
    slow = module.Check("waybar", "a document was checked", [], "a document changed", 0)
    slow.seconds = 0.1
    floor_plan.checks = [slow]
    printed = io.StringIO()
    with contextlib.redirect_stdout(printed):
        module.print_plan(module.Record(floor_state), floor_plan, "base", "head")
    floor_fields = dict(field.split("=", 1) for field in printed.getvalue().split()[2:])
    assert floor_fields["seconds"] == "0.1", printed.getvalue()
    assert floor_fields["full"] == "0.1", printed.getvalue()
    assert floor_fields["saved"] == "0.0", printed.getvalue()

    # The landing path's reuse list (board item 338). A batch replayed onto a moved tip is
    # re-gated, and a check whose watches the landed paths cannot reach is a result the
    # caller already holds: `FORK_CHECK_REUSE` names those checks and the run buys only the
    # rest. The list is a REQUEST, so the plan's own `reused` field is the answer -- a check
    # the run was told not to buy is named there, is absent from `checks`, and is not a
    # skip, because nothing about it was unanswerable.
    reuse_plan = module.Plan()
    reuse_plan.checks = [
        module.Check("waybar", "a document was checked", [], "a document changed", 0,
                     watches=("docs/",)),
        module.Check("go-tool", "tools/tillr-land builds", [], "a path under tools/tillr-land/ changed", 0,
                     watches=("tools/tillr-land/",)),
        module.Check("file-size", "every series' shape", [], "always", 0),
        module.Check("series", "pack matt: 1 series", [], "the series changed", 0,
                     watches=("yolo-packs/matt/",), reusable=False),
    ]
    module.apply_reuse(reuse_plan, ["waybar", "file-size", "series"])
    assert [check.kind for check in reuse_plan.reused] == ["waybar"], reuse_plan.reused
    assert [check.kind for check in reuse_plan.checks] == ["go-tool", "file-size", "series"], \
        reuse_plan.checks
    # The integrity set is never reused: it is a property of the tree, and the tree is what
    # moved. A check that declares itself not reusable is never reused either: its verdict
    # depends on an input outside its watches, so a moved upstream between the two gates
    # would be invisible. Both are re-run even when the caller asked for them.
    assert "file-size" not in [check.kind for check in reuse_plan.reused], reuse_plan.reused
    assert "series" not in [check.kind for check in reuse_plan.reused], reuse_plan.reused
    # A `kind:label` selector names one check of a shared kind, and the label is matched
    # EXACTLY: the caller reads it from the selection line this gate printed, so a prefix
    # match would let one check's reuse silently cover a sibling of the same kind.
    exact = module.Plan()
    exact.checks = [module.Check("go-tool", "tools/tillr-land builds", [], "changed", 0,
                                 watches=("tools/tillr-land/",))]
    module.apply_reuse(exact, ["go-tool:tools/tillr-land"])
    assert exact.reused == [] and len(exact.checks) == 1, (exact.reused, exact.checks)
    module.apply_reuse(exact, ["go-tool:tools/tillr-land builds"])
    assert [check.kind for check in exact.reused] == ["go-tool"], exact.reused
    # The plan line names what the run did not buy, and `-` when it bought everything.
    printed = io.StringIO()
    with contextlib.redirect_stdout(printed):
        module.print_plan(module.Record(floor_state), reuse_plan, "base", "head")
    reuse_fields = dict(field.split("=", 1) for field in printed.getvalue().split()[2:])
    assert reuse_fields["reused"] == "waybar", printed.getvalue()
    assert "waybar" not in reuse_fields["checks"].split(","), printed.getvalue()
    # The selection line carries the reused check with the watches that licensed it, so a
    # reader of the record can see what the run did not buy and why.
    printed = io.StringIO()
    with contextlib.redirect_stdout(printed):
        module.print_selection(reuse_plan, ["docs/reuse.md"])
    reuse_selection = json.loads(printed.getvalue()[len(module.SELECTION_PREFIX):])
    assert [one["kind"] for one in reuse_selection["reused"]] == ["waybar"], \
        reuse_selection["reused"]
    assert reuse_selection["reused"][0]["watches"] == ["docs/"], reuse_selection["reused"]
    # A reuse list belongs to the gate invocation that was handed it, never to a check: a
    # check that spawns another gate (the self-suite does) must not hand its own reuse down,
    # or the inner gate would skip checks the outer one was told to skip.
    assert "FORK_CHECK_REUSE" not in module.environ({"FORK_CHECK_REUSE": "waybar"}), \
        module.environ({"FORK_CHECK_REUSE": "waybar"})
    # The gate is the one boundary every check's environment passes through. A repository
    # selection inherited by the gate (6f2d9d938: a fixture's `git commit -m initial` landed
    # on the real main) must not reach any check, whoever added it.
    selected = module.environ({"GIT_DIR": "/x/.git", "GIT_WORK_TREE": "/x", "GIT_INDEX_FILE": "/x/i"})
    assert not [k for k in selected if k in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE")], selected

    # The landing path names the base it is gating (board item 299), so the plan's diff is
    # the batch's own rather than whatever verified baseline this history offers. The
    # named base predates the last two commits, so both are in the diff the plan reports.
    forced_base = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "docs" / "second.md").write_text("# a second document\n\nAnother change.\n")
    commit(repo, "docs: a second document for the plan base")
    forced_head = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    forced = run(repo, {**env, "FORK_CHECK_PLAN_BASE": forced_base})
    assert forced.returncode == 0, forced.stdout + forced.stderr
    forced_fields = dict(field.split("=", 1) for field in
                         [line for line in forced.stdout.splitlines() if line.strip()][-1].split()[2:])
    assert forced_fields["base"] == forced_base, forced_fields
    assert forced_fields["head"] == forced_head, forced_fields
    assert "waybar" not in forced_fields["checks"].split(","), forced_fields
    # A base this history does not contain is refused rather than planned from, because a
    # plan about a tree this history lacks cannot be this batch's answer.
    unresolved = run(repo, {**env, "FORK_CHECK_PLAN_BASE": "0" * 40})
    assert unresolved.returncode == 2, unresolved.stdout + unresolved.stderr
    assert "FORK_CHECK_PLAN_BASE" in unresolved.stderr, unresolved.stderr

    # 50. THE SELECTION LINE: why every check ran, and why every step did not. The report
    #     prints the same facts as prose, but a consumer that keeps only the tail of a
    #     gate's output -- the board's stored pass -- needs them as data. The line is one
    #     JSON object, printed immediately above the plan line so the plan line stays last
    #     by contract and both survive the tail. A check selected by a watched path records
    #     that path; a check in the fixed integrity set records that it is unconditional;
    #     a step that did not run records the reason it did not.
    waybar_fixture(repo)
    (repo / "config/waybar/config").write_text("changed\n")
    commit(repo, "docs: a document for the selection line")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    lines = [line for line in result.stdout.splitlines() if line.strip()]
    assert lines[-1].startswith("fork-check: plan "), lines[-3:]
    assert lines[-2].startswith(module.SELECTION_PREFIX), lines[-3:]
    selection = json.loads(lines[-2][len(module.SELECTION_PREFIX):])
    by_kind = {one["kind"]: one for one in selection["checks"]}
    documents = by_kind["waybar"]
    assert "config/waybar/config" in documents["selected_by"], documents
    assert documents["unconditional"] is False, documents
    assert documents["why"], documents
    assert documents["watches"], documents
    structural = by_kind["file-size"]
    assert structural["unconditional"] is True, structural
    assert structural["selected_by"] == [], structural
    # A step the change gave no reason to run is named with its reason, and a step the
    # change reached that this machine could not answer says so.
    assert any(not one["planned"] and one["why"] for one in selection["skips"]), selection["skips"]
    # The run's own record carries the same facts, so a reader of the record does not have
    # to read the gate's source and guess.
    entry = record(repo)["runs"][-1]
    recorded = {one["kind"]: one for one in entry["checks"]}
    assert "config/waybar/config" in recorded["waybar"]["selected_by"], recorded["waybar"]
    assert recorded["waybar"]["why"], recorded["waybar"]
    assert recorded["file-size"]["unconditional"] is True, recorded["file-size"]
    assert recorded["file-size"]["selected_by"] == [], recorded["file-size"]
    assert entry["skips"], entry
    assert any(not one["planned"] and one["why"] for one in entry["skips"]), entry["skips"]
    # `--explain` prints the same line, so a planning pass and a run answer "why did this
    # check run" in one shape. It must be asked about the same diff the run judged: the
    # verified run above advanced the baseline to its own head, so an explain pass planned
    # from the record would see no changed path and plan only the integrity set. Naming the
    # run's own base makes the two passes answer the same question, which is the claim.
    run_base = dict(field.split("=", 1) for field in lines[-1].split()[2:])["base"]
    explained = run(repo, {**env, "FORK_CHECK_PLAN_BASE": run_base}, "--explain")
    assert explained.returncode == 0, explained.stdout + explained.stderr
    explain_lines = [line for line in explained.stdout.splitlines() if line.strip()]
    assert explain_lines[-1].startswith(module.SELECTION_PREFIX), explain_lines[-3:]
    explained_selection = json.loads(explain_lines[-1][len(module.SELECTION_PREFIX):])
    assert {one["kind"] for one in explained_selection["checks"]} == set(by_kind), \
        explained_selection["checks"]
    explained_documents = {one["kind"]: one for one in explained_selection["checks"]}["waybar"]
    assert explained_documents["selected_by"] == documents["selected_by"], explained_documents

    # ── the hostile-matrix preflight ────────────────────────────────────────────
    #
    # A gate check is judged in conditions a developer's shell does not have, so a check
    # that only holds in that shell is green until a landing asks the gate's question.
    # `--preflight` runs the SAME planned checks in a matrix of those conditions and
    # reports every failure in one pass. The cases below pin the matrix itself (each
    # named condition is imposed by a cell) and the declarations file. There is no cell
    # that adds CPU load: the burner cell was deleted on 2026-10-31, and case 53 below
    # fails the gate if one comes back.
    scheduler = load_tool("fork_check_preflight")
    matrix = {cell.name: cell for cell in scheduler.PREFLIGHT_CELLS}
    assert list(matrix) == ["baseline", "gate-serial"], list(matrix)
    scratch = Path("/w/durable/hostile-preflight/run-1")
    additions, drop = scheduler.cell_environment(matrix["gate-serial"], scratch)
    assert additions["HOME"] == str(scratch / "home"), additions
    assert additions["XDG_STATE_HOME"] == str(scratch / "state"), additions
    assert additions["BOARD_STORE_STRICT"] == "1", additions
    assert additions["FORK_CHECK_PREFLIGHT_CELL"] == "gate-serial", additions
    # The Go build cache is carried across the scrub rather than re-created under it: a
    # per-run GOCACHE is the worktree-local cache this repository forbids.
    assert not additions["GOCACHE"].startswith(str(scratch)), additions
    assert not additions["GOMODCACHE"].startswith(str(scratch)), additions
    # BOARD_DB and BOARD_CLI are REMOVED, not emptied: an empty variable is a different
    # question to a store resolver from an absent one.
    assert drop == ("BOARD_DB", "BOARD_CLI"), drop
    caller_additions, caller_drop = scheduler.cell_environment(matrix["baseline"], scratch)
    assert "HOME" not in caller_additions and "XDG_STATE_HOME" not in caller_additions, \
        caller_additions
    assert "BOARD_STORE_STRICT" not in caller_additions, caller_additions
    assert caller_drop == (), caller_drop
    # A cell carries an environment and nothing else: no batch size and no load, because
    # a cell that adds either is the burner cell this gate deleted.
    assert not hasattr(matrix["gate-serial"], "load"), matrix["gate-serial"].__dict__
    assert not hasattr(matrix["gate-serial"], "jobs"), matrix["gate-serial"].__dict__
    assert scheduler.select_preflight_cells(None) == list(scheduler.PREFLIGHT_CELLS)
    for bad in ("nope", "baseline,nope", ""):
        try:
            scheduler.select_preflight_cells(bad)
        except scheduler.PreflightError as error:
            assert "cell" in str(error), error
        else:
            raise AssertionError(f"--cells {bad!r} was accepted")
    assert [cell.name for cell in scheduler.select_preflight_cells("gate-serial,baseline")] == \
        ["baseline", "gate-serial"], scheduler.select_preflight_cells("gate-serial,baseline")
    # `--only` uses one vocabulary with the declarations: a kind, or kind:label-prefix.
    probe = scheduler.Check("rank-order", "a queued dispatch follows its rank", [], "", 0)
    assert scheduler.preflight_selected(probe, []) is True
    assert scheduler.preflight_selected(probe, ["rank-order"]) is True
    assert scheduler.preflight_selected(probe, ["rank-order:a queued"]) is True
    assert scheduler.preflight_selected(probe, ["rank-order:the board"]) is False
    assert scheduler.preflight_selected(probe, ["steering-timer,go-tool"]) is False

    # The declarations file: a check says which cell it cannot pass, WHY, and the repair
    # it waits on. A malformed file is refused whole -- a declaration that cannot be read
    # is not an empty one, or a known limitation would read as a failure and a real
    # failure as declared.
    good = base / "declarations.json"
    good.write_text(json.dumps({"declarations": [
        {"check": "rank-order", "cell": "*",
         "reason": "its timing cases cannot share the machine",
         "repair": "item 247 asserts recorded events instead"},
        {"check": "fork-quality:fork pi-subagents", "cell": "gate-serial",
         "reason": "a real 30 s timing case", "repair": "the suite's own timing budget"},
    ]}))
    declared = scheduler.load_declarations(good)
    assert len(declared) == 2, declared
    assert declared[0].covers("rank-order", "a queued dispatch follows its rank", "gate-serial")
    assert declared[0].covers("rank-order", "anything", "baseline")
    assert not declared[0].covers("steering-timer", "the timer", "gate-serial")
    assert declared[1].covers("fork-quality", "fork pi-subagents: npm run test:unit", "gate-serial")
    assert not declared[1].covers("fork-quality", "fork pi-subagents: npm run test:unit", "baseline")
    assert scheduler.load_declarations(base / "absent-declarations.json") == []

    def refuses_declaration(document, needle):
        path = base / "bad-declarations.json"
        path.write_text(json.dumps(document))
        try:
            scheduler.load_declarations(path)
        except scheduler.PreflightError as error:
            assert needle in str(error), (needle, error)
        else:
            raise AssertionError(f"{needle!r} was accepted: {document}")

    one = {"check": "x", "cell": "*", "reason": "r", "repair": "p"}
    refuses_declaration({"declarations": [{**one, "cell": "nope"}]}, "not one of")
    refuses_declaration({"declarations": [{**one, "reason": "  "}]}, "non-empty 'reason'")
    refuses_declaration({"declarations": [{**one, "repair": ""}]}, "non-empty 'repair'")
    refuses_declaration({"declarations": [{**one, "why": "q"}]}, "unknown key")
    refuses_declaration({"declarations": [one, one]}, "repeats the declaration")
    refuses_declaration({"entries": []}, "'declarations'")


_shard_boundary("verdict-table-and-pins", 25)

# 48. What a check's non-zero exit means is declared per kind, and EVERY kind is declared.
#     The table was an allowlist with a string fallback, so a kind nobody had added answered
#     "not a verdict" (exit 2) by default. An undeclared kind was one of those, and the red
#     it reported -- the pack's copy of the board CLI missing the two files its source had
#     just gained -- was printed `ERR` and exited 2 for a tree the gate had judged, so the
#     landing queue had no check name and keyed the flake on `exit:2` (board item 284).
#     Every kind the planner's own source names is classified; a verdict kind's failure is a
#     verdict -- `FAIL`, exit 1 -- and a kind nobody declared fails closed rather than
#     silently reaching no verdict.
spec = importlib.util.spec_from_file_location("fork_check_verdict", SCRIPT)
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)
# The planner's kind literals, read from its own source: a kind built from a variable would
# be missed, so the count is asserted too rather than trusting an empty or tiny scan.
planned_kinds = set(re.findall(r'Check\(\s*"([a-z0-9-]+)"', SCRIPT.read_text()))
assert len(planned_kinds) >= 30, sorted(planned_kinds)
undeclared = sorted(planned_kinds - set(verdict.CHECK_KIND_GROUP))
assert not undeclared, (
    f"these check kinds are planned and not classified, so their failure would fall back to "
    f"the default: {undeclared}"
)
# The candidate driver can refuse this machine before preparing or judging a tree.
# Its linked-worktree refusal exits 1, but is not a failing repository check.
refused_prepare = verdict.Check(
    "fork-quality", "prepare the clean checkout",
    ["/repo/bin/pi-forks", "candidate", "--fork", "pi-session-search", "--deps"],
    "why", 60,
)
refused_prepare.rc = 1
refused_prepare.output = (
    "pi-forks: refusing to run from a linked worktree: /repo/lane\n"
    "  That checkout shares one .forks with its main repository (/repo).\n"
)
assert not refused_prepare.conflict, "a machine-local driver refusal became red"
assert verdict.check_status(refused_prepare) == "ERR"
assert verdict.verdict_of([refused_prepare]) == ("unproven", 2)
# The rolling consumer reads these spans, not the human report. It must receive
# ERR rather than FAIL for a preparation this machine could not perform.
with tempfile.TemporaryDirectory(dir=os.environ.get("YOLO_DURABLE_DIR")) as directory:
    spans_path = Path(directory) / "gate.spans.json"
    refused_prepare.start_ns = 1
    prepare_plan = verdict.Plan()
    prepare_plan.checks = [refused_prepare]
    verdict.write_gate_spans(str(spans_path), prepare_plan, ROOT, 1,
                             "unproven", "head", "tree", [], 1, 1, 0)
    assert json.loads(spans_path.read_text())["check_spans"][0]["status"] == "ERR"
# The same text from a suite, or an actual prepare failure, must stay red.
for argv, output in (
    (["npm", "run", "test"], refused_prepare.output),
    (["/repo/bin/pi-forks", "candidate", "--fork", "x", "--prepared", "--", "npm", "run", "test"],
     refused_prepare.output),
    (refused_prepare.argv, "pi-forks: patch failed to apply\n"),
):
    genuine_red = verdict.Check("fork-quality", "label", argv, "why", 60)
    genuine_red.rc, genuine_red.output = 1, output
    assert genuine_red.conflict, "an actual quality failure was suppressed"

# A verdict kind that fails is this change's own red, not an inability to answer.
red = verdict.Check("pipeline-evidence",
                    "one record from each producer validates against the shared schema",
                    [sys.executable, "tests/pipeline-evidence.test.py"], "the format changed", 60)
red.rc = 1
assert red.conflict, "a failing pipeline-evidence check is not a verdict"
assert verdict.check_status(red) == "FAIL", verdict.check_status(red)
assert verdict.verdict_of([red]) == ("failed", 1), verdict.verdict_of([red])
# A kind the table does not name is a verdict, the failing side: the worst a missing entry
# can do is refuse a change loudly.
stranger = verdict.Check("a-kind-nobody-declared", "label", ["true"], "why", 1)
stranger.rc = 1
assert stranger.conflict, "an undeclared kind is not a verdict"
assert verdict.verdict_of([stranger]) == ("failed", 1), verdict.verdict_of([stranger])
# The two numbers stay disjoint: a check that reached no verdict is still exit 2, and the
# gate's own audit suite is the kind that says so when it names no case. A suite that ran
# to completion and named the assertion that failed is a verdict about the tree -- FAILED,
# exit 1 -- while a crash, a timeout or a failure that names nothing stays unproven.
unanswered = verdict.Check("self-suite", "the gate's own audit suite still passes",
                           [sys.executable, "tests/fork-check.test.py"], "why", 1)
unanswered.rc = 1
assert not unanswered.conflict
assert verdict.verdict_of([unanswered]) == ("unproven", 2), verdict.verdict_of([unanswered])
named = verdict.Check("self-suite", "the gate's own audit suite still passes",
                      [sys.executable, "tests/fork-check.test.py"], "why", 1)
named.rc = 1
named.output = (
    'Traceback (most recent call last):\n'
    '  File "tests/fork-check.test.py", line 2363, in <module>\n'
    '    assert "concurrent (peak 1, 1 at a time)" in timings[0], timings[0]\n'
    'AssertionError: the timings line still says concurrent (peak 1, 1 at a time)\n'
)
assert named.conflict, "a self-suite that names its failing assertion is not a verdict"
assert verdict.check_status(named) == "FAIL", verdict.check_status(named)
assert verdict.verdict_of([named]) == ("failed", 1), verdict.verdict_of([named])
# A timeout is the machine not answering, whatever the output says: the number is read
# before the kind's group, so a named case cannot turn a killed run into a verdict.
timed_out = verdict.Check("self-suite", "the gate's own audit suite still passes",
                          [sys.executable, "tests/fork-check.test.py"], "why", 1)
timed_out.rc = 124
timed_out.output = named.output
assert not timed_out.conflict
assert verdict.verdict_of([timed_out]) == ("unproven", 2), verdict.verdict_of([timed_out])
# An exit status that means the machine could not answer is not a red for ANY kind. The
# number has to mean the same thing everywhere, so it is read before the kind's group is
# consulted: `pipeline-evidence` and `go-tool` are verdict kinds, and a timeout or a missing
# runner on either verified nothing about the change and must not become its red. Exit 2
# and never a pass, so a candidate stays queued instead of being called wrong for what
# this machine could not do.
for kind, rc in (("pipeline-evidence", 124), ("go-tool", 127), ("file-size", 137),
                 ("fork-quality", 124)):
    could_not_run = verdict.Check(kind, "label", ["true"], "why", 1)
    could_not_run.rc = rc
    assert not could_not_run.conflict, f"{kind} exiting {rc} became a verdict"
    assert verdict.check_status(could_not_run) == "ERR", verdict.check_status(could_not_run)
    assert verdict.verdict_of([could_not_run]) == ("unproven", 2), verdict.verdict_of([could_not_run])
# A check ended by a signal the gate did not send (a worker's pattern kill) is not a red:
# it reports "ended by signal N from outside", is unproven, and is run again.
for kind in ("go-tool", "gate-runner", "pipeline-evidence"):
    signaled = verdict.Check(kind, "label", ["true"], "why", 1)
    signaled.rc = -15
    assert not signaled.conflict, f"{kind} killed by SIGTERM became a verdict"
    assert verdict.verdict_of([signaled]) == ("unproven", 2), verdict.verdict_of([signaled])
assert verdict.outside_signal(-15) == 15 and verdict.outside_signal(1) is None
assert verdict.outside_signal(verdict.CHECK_STALLED_RC) is None
import tempfile as _tf
with _tf.TemporaryDirectory() as _d:
    _root = Path(_d)
    _marker = _root / "count"
    _script = f"echo x >> {_marker}; if [ $(wc -l < {_marker}) -lt 2 ]; then kill -TERM $$; fi"
    retried = verdict.Check("go-tool", "label", ["sh", "-c", _script], "why", 30)
    verdict.execute_attempt(_root, retried, None)
    assert retried.rc == 0, (retried.rc, retried.output)
    assert any("ended by signal 15 from outside" in w for w in retried.warnings), retried.warnings
    _marker.unlink()
    always = verdict.Check("go-tool", "label", ["sh", "-c", "kill -TERM $$"], "why", 30)
    verdict.execute_attempt(_root, always, None)
    assert always.rc == -15 and not always.conflict, always.rc
    assert "ended by signal 15 from outside" in always.output, always.output
    assert verdict.check_status(always) == "ERR"

print(f"fork-check verdict table: {len(planned_kinds)} planned kind(s), every one classified; "
      "a failing pipeline-evidence is FAIL/exit 1, a self-suite that names its assertion is "
      "FAIL/exit 1 while one that names none is unproven, and an undeclared kind fails closed")


# 48b. THE TIER TABLE NAMES EVERY KIND (docs/design/landing-velocity.md section 4.3). The
#      landing tier is the proof a batch carries on the tree the trunk will publish; the
#      rolling tier follows on a clock. The table is a declaration with the same fail-safe
#      direction as CHECK_KIND_GROUP: every kind the planner can produce is named, and an
#      unnamed kind defaults to the LANDING tier, so a missing entry can only make a
#      landing slower, never quieter. The split Go kinds are the one placement this slice
#      makes: the build lands, the behavior suite rolls.
tiered = verdict.CHECK_TIER
assert set(tiered) == set(verdict.CHECK_KIND_GROUP), (
    "CHECK_TIER and CHECK_KIND_GROUP must name the same kinds: "
    f"tier-only {sorted(set(tiered) - set(verdict.CHECK_KIND_GROUP))}, "
    f"group-only {sorted(set(verdict.CHECK_KIND_GROUP) - set(tiered))}"
)
untiered = sorted(planned_kinds - set(tiered))
assert not untiered, (
    f"these planned check kinds are not in CHECK_TIER, so their tier is a guess: {untiered}"
)
bad_tiers = sorted(kind for kind, tier in tiered.items()
                   if tier not in (verdict.TIER_LANDING, verdict.TIER_ROLLING))
assert not bad_tiers, f"these kinds declare a tier that is not landing or rolling: {bad_tiers}"
assert verdict.check_tier("a-kind-nobody-declared") == verdict.TIER_LANDING, (
    "an unnamed kind must default to the landing tier, the safe side"
)
assert verdict.check_tier("unregistered-fixture") == verdict.TIER_LANDING, verdict.check_tier("unregistered-fixture")
assert verdict.check_tier("go-substrate") == verdict.TIER_LANDING, verdict.check_tier("go-substrate")
assert verdict.check_tier("go-tool") == verdict.TIER_ROLLING, verdict.check_tier("go-tool")
print(f"fork-check tier table: {len(tiered)} kind(s) declared, "
      f"{sum(1 for tier in tiered.values() if tier == verdict.TIER_ROLLING)} rolling; "
      "an unnamed kind lands")


# The invariant kinds keep their driver's own wording as the verdict: the named refusal is
# the failure, and the same non-zero exit without it is the driver saying it could not
# answer. This is the other half of the rule case 48 pins -- a kind's group says where to
# look, and the phrase it printed says whether the driver judged the change.
for kind, text, is_verdict in (
    ("patch-branch-inv2", "FAILED: main conflicts with refs/tags/v9 (INV-2)", True),
    ("patch-branch-inv2", "could not resolve the upstream a launch would build", False),
    ("series", "upstream v9 does not take the patch series", True),
    ("series", "could not fetch the upstream", False),
    # `build` runs `bin/pi-forks candidate --build`, which never prints the series phrase:
    # its refusals are a compile that failed and a lockfile that does not match the
    # series' own package.json. Both are facts of the tree, so both are verdicts. On
    # 2026-11-18 a candidate that pinned a fork commit with a stale typebox lock reached
    # "no verdict" here and was held for an operator instead of going back to its worker.
    ("build", "   npm run build\n   BUILD FAILED\n   FAILED: build\n", True),
    ("build", "   npm ci --ignore-scripts\nnpm error code EUSAGE\nnpm error `npm ci` can only "
              "install packages when your package.json and package-lock.json or "
              "npm-shrinkwrap.json are in sync.\n"
              "   INSTALL FAILED -- npm ci did not complete (an install problem, not a build)\n"
              "   FAILED: dependency install (npm ci), not the build\n", True),
    # An install the network broke says nothing about the tree.
    ("build", "   npm ci --ignore-scripts\nnpm error code ETIMEDOUT\n"
              "   FAILED: dependency install (npm ci), not the build\n", False),
    ("build", "   FAILED: no shared checkout at /x to build the candidate from\n", False),
):
    check = verdict.Check(kind, "label", ["true"], "why", 60)
    check.rc, check.output = 1, text
    assert check.conflict is is_verdict, (kind, text, check.conflict)

for kind in ("patch-branch-inv2", "patch-branch-inv3", "forks-check", "build", "fork-quality"):
    check = verdict.Check(kind, "label", ["true"], "why", 60)
    check.rc, check.output = 4, "could not judge: cannot fetch commit abc from remote"
    assert check.unanswered and not check.verdict, (kind, verdict.check_status(check))
    check.rc, check.output = 1, "FAILED: conflicts with upstream; is not the export of main; does not take the patch series\ncould not judge: cannot fetch commit abc from remote"
    assert check.verdict and not check.unanswered, (kind, verdict.check_status(check))

check = verdict.Check("fork-quality", "label", ["bin/pi-forks", "candidate", "--prepared", "--", "npm"], "why", 60)
check.rc, check.output = 4, "test command failed"
assert check.verdict, verdict.check_status(check)

print("fork-check routing: 51 audit cases, range diagnostics, approved-baseline and "
      "source-quality regressions pass; concurrency: independent checks run at the same "
      "time, writers of one path never do, and the run still reports one line per check "
      "in plan order; the gate runs its own audit suite, the pipeline-evidence "
      "conformance suite, the reload-residue audit, the rank-order suite, the "
      "session-forensics test, the pi-forks driver's own suite, "
      "the hooks installer's suite and the liveness harness; "
      "a yolo upgrade is reported and plans nothing, a node upgrade re-plans its own, "
      "and a machine-local input is declared elsewhere instead of blocking the verdict; "
      "what a check's non-zero exit means is declared per kind and every kind is declared, "
      "so a red cannot quietly become 'not a verdict'; "
      "the baseline is kept per branch, so two worktrees started together both verify, a "
      "candidate outside HEAD's history is skipped rather than refused, and an entry ahead "
      "of this branch plans from the fork point rather than reading as a first run; "
      "every changed Go tool directory gets its whole behavior suite, "
      "suite, the integration cases included; "
      "a long run is observable while it runs, and one PI_FORKS_ROOT reading serves both "
      "the gate and the driver, and it moves only the fork checkouts -- the fork steps "
      "verify the series of the tree being verified; a failing run writes its failing "
      "checks' own output beside the record and points the run entry at it; a failed check "
      "runs exactly once and is triaged from that output into a machine-readable packet -- "
      "case, file, assertion, named paths, the commits that touched them, the one-file "
      "command that reproduces it; a fork-quality suite whose only failing cases are "
      "covered by a fixture quarantine is named with its reason instead of reported red, "
      "and a run "
      "whose only non-passing step is that one still reaches a verdict while any other "
      "failure still reds the run; and every run ends with one plan line naming the commit "
      "its diff was taken from, the steps it ran and did not, its own cost against the "
      "record's full-gate price, and how much of the fixed integrity set it carried; "
      "and the hostile-matrix preflight runs the same planned checks under the gate's own "
      "conditions -- scrubbed HOME and XDG_STATE_HOME, a strict board store, the caller's "
      "uid, and one check at a time -- reporting every failure in one pass, classifying "
      "what only fails in the gate's cell as environment-conditional, and letting a check "
      "declare a cell it cannot pass instead of failing it")


# The upstream a launch would build, as the bridge resolves it. The gate must record that
# ref and not a tag list of its own, and it must say so when no checkout could answer --
# the failure this covers is a gate that blessed a pi-subagents series against v0.83.0
# while every launch had moved to v0.83.1 and would refuse the merge (2026-10-28).
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    release_sha = sh(releases, "git", "rev-parse", "HEAD").stdout.strip()
    head_sha = sh(heads, "git", "rev-parse", "HEAD").stdout.strip()

    # 1. The bridge answers: the record keeps the ref a launch would build, the kind it
    #    was chosen by, and the commit, beside the comparable fingerprint.
    (base / "target-lines").write_text(
        f"nicobailon/pi-subagents\trelease\trefs/tags/v1.2.3\t{release_sha}\n"
        f"Jawfish/pi-background-tasks\thead\trefs/remotes/upstream/main\t{head_sha}\n"
    )
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    targets = record(repo)["targets"]
    assert targets["matt/pi-subagents"]["fingerprint"] == f"release:v1.2.3@{release_sha}", targets
    assert targets["matt/pi-subagents"]["resolved"] == {
        "kind": "release", "ref": "refs/tags/v1.2.3", "commit": release_sha,
    }, targets
    assert targets["matt/pi-background-tasks"]["fingerprint"] == f"head:main:{head_sha}", targets
    assert targets["matt/pi-background-tasks"]["resolved"]["kind"] == "head", targets
    # The merge question was asked of the launch's ref, not of a ref this checkout had.
    assert "patch-branch merge-check --candidate --fetch --fork pi-subagents" in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")

    # 2. A release the tag list would have named differently: the bridge's answer wins,
    #    because it is the ref a launch builds.
    (base / "target-lines").write_text(
        f"nicobailon/pi-subagents\trelease\trefs/tags/v1.2.3\t{release_sha}\n"
        f"Jawfish/pi-background-tasks\thead\trefs/remotes/upstream/main\t{head_sha}\n"
    )
    sh(releases, "git", "tag", "v9.9.9")   # a tag no launch would build off the followed branch
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    targets = record(repo)["targets"]
    assert targets["matt/pi-subagents"]["fingerprint"] == f"release:v1.2.3@{release_sha}", targets

    # 3. No answer from the bridge: the tag-list probe keeps the run possible, and the
    #    plan says the launch's own resolution was not available.
    (base / "target-lines").unlink()
    (base / "pi-forks-fail").write_text("patch-branch target")
    (base / "pi-forks-rc").write_text("1")
    (repo / "yolo-packs" / "matt" / "patches" / "pi-subagents" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nq\n"
    )
    commit(repo, "a series edit that asks the fork's checks")
    result = run(repo, env, "--explain")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "the launch's resolution was not available" in result.stdout, result.stdout
    assert "bin/pi-forks patch-branch target --fetch could not resolve it" in result.stdout, result.stdout
    # And the run itself still reaches a verdict, with the probe's spelling recorded.
    (base / "pi-forks-fail").unlink()
    (base / "pi-forks-rc").unlink()
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    targets = record(repo)["targets"]
    # The probe's spelling, and the proof that it is only a probe: it names the newest tag
    # on the tag list, which is the one a launch does NOT build here, and nothing claims
    # the launch resolved it.
    probe = sh(releases, "git", "rev-parse", "v9.9.9^{commit}").stdout.strip()
    assert targets["matt/pi-subagents"]["fingerprint"] == f"release:v9.9.9@{probe}", targets
    assert "resolved" not in targets["matt/pi-subagents"], targets

print("fork-check upstream resolution: the bridge's ref is what a verdict records, and a "
      "probe answer is named rather than passed off as the launch's own")


_shard_boundary("candidate-tree-preflight", 42)

# 51. The gate only judges a candidate tree proven current against its series (board item
#     194). A 216-second landing run reported two failing pi-subagents cases the series did
#     not have, because the candidate tree the gate read was not the series' tree. The
#     preflight is `bin/pi-forks candidate --check`: it compares the candidate's tree, its
#     recorded series digest, its working-tree state and its dependency stamp against the
#     series, names the expected and actual value of each, and refuses in seconds without
#     running the fork's test command. This case drives the REAL driver through the gate,
#     so the preflight is the one a person runs, not a stand-in. The prepare step is the
#     suite's stand-in (it records its call and exits 0), so the candidate is still in the
#     state this case put it in when the reader's preflight runs -- which is the state the
#     preflight exists to judge: a tree that is not the series' tree, a checkout a writer
#     touched, or dependencies that are not the ones the lockfile names.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    # The real driver, beside the fixture rather than inside it, so the stand-in can exec
    # it for `candidate --check` alone. PI_FORKS_ROOT names the fixture repository, which
    # is also where the gate reads its `.forks/` from.
    real_driver = base / "pi-forks-real"
    shutil.copy(ROOT / "bin" / "pi-forks", real_driver)
    real_driver.chmod(0o755)
    env["PI_FORKS_ROOT"] = str(repo)
    # The upstream the manifest names is a local repository: the driver clones it over
    # https, so the fixture rewrites that URL the way tests/pi-forks.test.sh does. The
    # series and manifest are read from the fixture tree, not from where the copied
    # driver happens to live.
    real_env = {**env, "PI_FORKS_SERIES_ROOT": str(repo), "GIT_CONFIG_COUNT": "3",
                "GIT_CONFIG_KEY_2": f"url.{releases}.insteadOf",
                "GIT_CONFIG_VALUE_2": "https://github.com/nicobailon/pi-subagents.git"}

    # A real series that applies to the upstream, with a lockfile and an ignore rule so
    # the candidate's installed dependencies are not working-tree content.
    (releases / ".gitignore").write_text("node_modules/\n")
    (releases / "package-lock.json").write_text('{"name":"pi-subagents","lockfileVersion":3}\n')
    # The rank-order step imports this from the shared checkout, so the upstream carries
    # it: a series change plans that step, and a missing file would refuse the run.
    (releases / "src" / "runs" / "background").mkdir(parents=True)
    (releases / "src" / "runs" / "background" / "queue-rank.ts").write_text(
        "export const compareQueueEntries = () => 0;\n")
    commit(releases, "a lockfile, an ignore rule and the rank-order import")
    series_base = sh(releases, "git", "rev-parse", "HEAD").stdout.strip()
    work = base / "pi-subagents-work"
    sh(base, "git", "clone", "--quiet", str(releases), str(work))
    (work / "src" / "index.ts").write_text("export const x = 2;\n")
    commit(work, "ours: the series' own change")
    series_dir = repo / "yolo-packs" / "matt" / "patches" / "pi-subagents"
    for stale in series_dir.glob("*.patch"):
        stale.unlink()
    sh(work, "git", "format-patch", "-q", "--no-signature", "--suffix=.patch",
       f"--base={series_base}", "-o", str(series_dir), f"{series_base}..HEAD")
    commit(repo, "a real pi-subagents series")

    # The shared checkout and the candidate, built by the real driver from that series.
    # The fixture's plain stand-in directory is not a checkout, so it is removed first.
    shutil.rmtree(repo / ".forks" / "pi-subagents")
    sh(repo, str(real_driver), "setup", "--fork", "pi-subagents", env=real_env)
    sh(repo, str(real_driver), "candidate", "--fork", "pi-subagents", env=real_env)
    candidate = repo / ".forks" / "candidate" / "pi-subagents"
    candidate_head = sh(candidate, "git", "rev-parse", "HEAD").stdout.strip()
    # The dependency stamp `ensure_deps` writes, made by hand because the fixture has no
    # packages to install. `node_modules/` is ignored, so the tree stays clean.
    lock = sh(candidate, "git", "hash-object", "package-lock.json").stdout.strip()
    (candidate / "node_modules").mkdir()
    (candidate / "node_modules" / ".pi-forks-lock").write_text(lock + "\n")
    # The stand-in answers `candidate --check` with the real driver from here on.
    (base / "pi-forks-check-real").write_text("1\n")

    # A declaration change plans the fork-quality step without planning the producer
    # steps, so the candidate is exactly what this case put there.
    pack = repo / "yolo-packs" / "matt" / "pack.json"
    manifest = json.loads(pack.read_text())
    manifest["description"] = "fixture"
    pack.write_text(json.dumps(manifest, indent=2) + "\n")
    commit(repo, "a declaration change plans the source checks")

    def gate(*args):
        return run(repo, env, *args)

    def gate_all():
        # `--all` plans the fork-quality step on every run, so each stale state is judged
        # by the preflight rather than skipped because the baseline already covers it.
        return run(repo, env, "--all")

    def quality_calls():
        return [call for call in calls(base, "pi-forks-calls") if "-- npm run" in call]

    # A freshly built candidate passes the preflight and proceeds to the fork's tests.
    clear(base, "pi-forks-calls")
    result = gate_all()
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert quality_calls() == [
        "candidate --fork pi-subagents --prepared -- npm run typecheck",
        "candidate --fork pi-subagents --prepared -- npm run test:unit",
    ], calls(base, "pi-forks-calls")

    def stale_run(label, expected):
        """One stale state: the gate refuses, names it, and runs no test command."""
        clear(base, "pi-forks-calls")
        result = gate_all()
        assert result.returncode == 2, (label, result.stdout + result.stderr)
        assert "fork-check: NOT VERIFIED" in result.stdout, (label, result.stdout)
        assert "STALE" in result.stdout, (label, result.stdout)
        assert expected in result.stdout, (label, result.stdout)
        assert "expected" in result.stdout and "actual" in result.stdout, (label, result.stdout)
        assert "rebuild" in result.stdout and "candidate --fork pi-subagents" in result.stdout, \
            (label, result.stdout)
        assert quality_calls() == [], (label, calls(base, "pi-forks-calls"))
        # A stale candidate is a refusal, not a test failure: no triage packet is written
        # for it, because the step never ran its command.
        assert "triage fork-quality" not in result.stdout, (label, result.stdout)
        return result

    # (a) The tree is not the one the series produces.
    sh(candidate, "git", "reset", "--hard", "--quiet", series_base)
    stale_run("tree", "the candidate's tree is not the series' tree")
    sh(candidate, "git", "reset", "--hard", "--quiet", candidate_head)

    # (b) Tracked content is dirty.
    (candidate / "src" / "index.ts").write_text("export const x = 99;\n")
    stale_run("tracked dirt", "the candidate carries working-tree content")
    sh(candidate, "git", "checkout", "--quiet", "--", ".")

    # (c) Non-ignored untracked content is present.
    (candidate / "stray.txt").write_text("a writer's leftover\n")
    stale_run("untracked", "the candidate carries working-tree content")
    (candidate / "stray.txt").unlink()

    # (d) The dependency stamp is not the candidate's lockfile.
    (candidate / "node_modules" / ".pi-forks-lock").write_text("0" * 40 + "\n")
    stale_run("dependencies", "the candidate's dependencies are not the ones its lockfile names")
    (candidate / "node_modules" / ".pi-forks-lock").write_text(lock + "\n")

    # (e) The candidate was built from a different series. The subject line changes the
    #     digest without changing the tree, so this state is the series alone.
    patch = next(series_dir.glob("*.patch"))
    original = patch.read_text()
    patch.write_text(original.replace("Subject: [PATCH] ", "Subject: [PATCH] moved: ", 1))
    commit(repo, "the series moves without changing its tree")
    stale_run("series", "the candidate was built from a different series")
    patch.write_text(original)
    commit(repo, "the series returns")

    # The same candidate, rebuilt, passes again and the fork's tests run. The verdict
    # cache is cleared first: a cached verdict is a reused full run on a hash-identical
    # tree, and this case is about the preflight letting the command run at all.
    sh(repo, str(real_driver), "candidate", "--fork", "pi-subagents", env=real_env)
    (repo / ".git" / "yolo-fork-check" / "verdicts.json").unlink(missing_ok=True)
    clear(base, "pi-forks-calls")
    result = gate_all()
    assert result.returncode == 0, result.stdout + result.stderr
    assert quality_calls(), calls(base, "pi-forks-calls")

    # A triage packet is evidence about one candidate. A packet from an earlier run whose
    # check did not run here is HISTORICAL: it is named with its originating run and
    # candidate, never among this run's failures, and the summary's word for a check is
    # its recorded return code rather than a bare "ok".
    # The cache is cleared again: a cached green verdict would answer for the tree and the
    # failing command would never run, which is the opposite of what this block pins.
    (repo / ".git" / "yolo-fork-check" / "verdicts.json").unlink(missing_ok=True)
    (base / "pi-forks-fail").write_text("npm run test:unit")
    (base / "pi-forks-rc").write_text("1")
    (base / "pi-forks-out").write_text(
        "\u2139 tests 2\n\u2139 pass 1\n\u2139 fail 1\n\n\u2716 failing tests:\n\n"
        "test at test/unit/example.test.ts:1:1\n\u2716 a case the series does not have (1ms)\n"
    )
    result = gate_all()
    assert result.returncode == 1, result.stdout + result.stderr
    assert "fork-check: FAILED" in result.stdout, result.stdout
    assert "triage fork-quality" in result.stdout, result.stdout
    packet = record(repo)["triage"]
    assert len(packet) == 1, packet
    kept = list(packet.values())[0]
    assert kept["candidate"]["commit"] == candidate_head, kept
    # The summary names the recorded return code, so a reader of the tail cannot read a
    # red as "ok".
    assert "FAIL rc=1" in result.stdout, result.stdout
    assert "ok     fork pi-subagents: npm run test:unit" not in result.stdout, result.stdout

    # A run that does not plan the check names the packet as historical, with the
    # candidate it is about, and does not count it among this run's failures.
    clear(base, "pi-forks-fail", "pi-forks-rc", "pi-forks-out")
    result = gate("--since", "HEAD")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "recorded failure(s) did not run here" in result.stdout, result.stdout
    assert "historical, not this run's failures" in result.stdout, result.stdout
    assert f"candidate {candidate_head[:12]}" in result.stdout, result.stdout
    assert "fork-check: FAILED" not in result.stdout, result.stdout
    assert "triage fork-quality" not in result.stdout, result.stdout

    # A different candidate does not inherit the packet's identity: the historical line
    # still names the candidate that produced it.
    sh(candidate, "git", "commit", "--quiet", "--allow-empty", "-m", "a different candidate")
    moved = sh(candidate, "git", "rev-parse", "HEAD").stdout.strip()
    assert moved != candidate_head
    result = gate("--since", "HEAD")
    assert result.returncode == 0, result.stdout + result.stderr
    assert f"candidate {candidate_head[:12]}" in result.stdout, result.stdout
    assert f"candidate {moved[:12]}" not in result.stdout, result.stdout

# 51b. A PINNED fork does not follow its upstream (board item 397). A hex `?ref=` makes
#      `follows_branch` false, so the gate never asks the upstream which ref a launch would
#      build, the fingerprint is the pin, and an upstream push cannot carry the fork's
#      quality suite into an unrelated landing pass. The other half of the trade is the
#      detector: `--pin-drift` compares the pin with the upstream's default branch and
#      files one bump item per drifted pin, idempotently.
def build_pinned_repo(base: Path):
    """A minimal repository with ONE pinned series, its upstream, and a real checkout."""
    source = upstream(base, "pi-session-search")
    pin = sh(source, "git", "rev-parse", "HEAD").stdout.strip()

    repo = base / "pinned-repo"
    (repo / "yolo-packs" / "matt" / "patches" / "pi-session-search").mkdir(parents=True)
    (repo / "yolo-packs" / "matt" / "patches" / "pi-session-search" / "0001-x.patch").write_text(
        "From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001\n\nx\n")
    (repo / "bin").mkdir()
    (repo / "tests").mkdir()
    (repo / "tools").mkdir()
    shutil.copy(SCRIPT, repo / "tools" / "fork-check.py")
    (repo / "tools" / "gate").mkdir(exist_ok=True)
    shutil.copy(ROOT / "tools/gate/classify.py", repo / "tools/gate/classify.py")
    (repo / "yolo-packs" / "matt" / "pack.json").write_text(json.dumps({
        "name": "matt",
        "contributes": [
            {"kind": "files", "into": ".pi/agent/yolo-patched/pi-session-search",
             "source": f"git+{source}?ref={pin}", "patches": "patches/pi-session-search"},
        ],
    }, indent=2) + "\n")
    (repo / "bin" / "pi-forks.list").write_text(
        "# comment\n"
        "samfoy/pi-session-search          yolo-packs/matt/patches/pi-session-search\n")
    (repo / "tests" / "pi-patch-series.test.sh").write_text("#!/bin/sh\necho 'ok   stub structural'\n")
    copy_identity_check(repo)
    forks = repo / "bin" / "pi-forks"
    forks.write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$*" >> "{base}/pinned-pi-forks-calls"\n'
        "exit 0\n")
    forks.chmod(0o755)
    # A REAL clone, because the detector counts the commits between the pin and the tip
    # through the checkout's objects. The gate's own fork steps run through the stub above.
    sh(base, "git", "clone", "--quiet", str(source), str(repo / ".forks" / "pi-session-search"))
    (repo / ".gitignore").write_text(".forks/\n")
    git_init(repo, "--initial-branch=main")
    commit(repo, "initial")
    return repo, source, pin


with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, source, pin = build_pinned_repo(base)
    env = build_stubs(base)

    # 1. First run: the series is new, so the fork's quality suite is planned and runs.
    #    The gate never asks the upstream for a pinned fork -- the pin is the answer.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    first = calls(base, "pinned-pi-forks-calls")
    assert any("npm run check" in call for call in first), first
    assert any("npm run test" in call for call in first), first
    assert not any("patch-branch target" in call for call in first), first
    # The record tracks FOLLOWED forks, because their fingerprint can move. A pin is the
    # declaration's answer, so there is no target entry to drift.
    assert "matt/pi-session-search" not in record(repo)["targets"], record(repo)["targets"]

    # 2. Upstream moves. Nothing here changed, so the pinned fork is not planned at all:
    #    no series check, no merge question, no replay, no build, and no quality suite.
    (source / "src" / "index.ts").write_text("export const x = 2;\n")
    commit(source, "upstream moves past the pin")
    clear(base, "pinned-pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: VERIFIED" in result.stdout, result.stdout
    assert calls(base, "pinned-pi-forks-calls") == [], calls(base, "pinned-pi-forks-calls")
    # No fork-quality CHECK ran. The report's "declared elsewhere" line mentions the
    # fork-quality steps as a class, so the assertion names the check's own label.
    assert "fork-quality fork pi-session-search" not in result.stdout, result.stdout
    assert "fork-quality fork pi-subagents" not in result.stdout, result.stdout
    # The fork is named as not planned, with the reason, rather than silently dropped.
    assert "fork pi-session-search" in result.stdout, result.stdout
    assert "no series of it changed" in result.stdout, result.stdout
    # The record still has no target for the pinned fork: the pin is the declaration's
    # answer, and an upstream move cannot create one.
    assert "matt/pi-session-search" not in record(repo)["targets"], record(repo)["targets"]

    # 3. The detector surfaces the drift, and files one bump item per pin. The board is a
    #    stub: `item show` says not found, `item add` answers with an id.
    board = base / "board-stub"
    board.write_text(
        "#!/usr/bin/env python3\n"
        "import json, sys\n"
        f"open({str(base / 'board-calls')!r}, 'a').write(' '.join(sys.argv[1:]) + '\\n')\n"
        "if sys.argv[1:3] == ['item', 'show']:\n"
        "    sys.exit(5)\n"
        "if sys.argv[1:3] == ['item', 'add']:\n"
        "    print(json.dumps({'ok': True, 'item': {'id': 7}}))\n"
        "sys.exit(0)\n")
    board.chmod(0o755)
    result = subprocess.run(
        [sys.executable, str(repo / "tools" / "fork-check.py"), "--pin-drift",
         "--file", "--board", str(board)],
        cwd=str(repo), capture_output=True, text=True, env=env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "AHEAD by 1 commit(s)" in result.stdout, result.stdout
    assert "filed item 7" in result.stdout, result.stdout
    assert "1 bump(s) owed" in result.stdout, result.stdout
    recorded = (base / "board-calls").read_text()
    added = [line for line in recorded.splitlines() if line.startswith("item add")]
    assert len(added) == 1, added
    # The description carries newlines, so the external ref is on a later line of the
    # same recorded call; read the whole file rather than the first line.
    assert f"fork-pin-drift:pi-session-search:{pin}" in recorded, recorded

    # 4. A second run is idempotent: the same (fork, pin) pair is one item, not one per poll.
    board.write_text(
        "#!/usr/bin/env python3\n"
        "import json, sys\n"
        f"open({str(base / 'board-calls')!r}, 'a').write(' '.join(sys.argv[1:]) + '\\n')\n"
        "if sys.argv[1:3] == ['item', 'show']:\n"
        "    print(json.dumps({'ok': True, 'item': {'id': 7}}))\n"
        "sys.exit(0)\n")
    board.chmod(0o755)
    clear(base, "board-calls")
    result = subprocess.run(
        [sys.executable, str(repo / "tools" / "fork-check.py"), "--pin-drift",
         "--file", "--board", str(board)],
        cwd=str(repo), capture_output=True, text=True, env=env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "already tracks this pin" in result.stdout, result.stdout
    assert not [line for line in calls(base, "board-calls") if line.startswith("item add")], \
        calls(base, "board-calls")

    # 5. At the pin, the detector owes nothing and files nothing.
    (source / "src" / "index.ts").write_text("export const x = 1;\n")
    sh(source, "git", "reset", "--quiet", "--hard", pin)
    clear(base, "board-calls")
    result = subprocess.run(
        [sys.executable, str(repo / "tools" / "fork-check.py"), "--pin-drift",
         "--file", "--board", str(board)],
        cwd=str(repo), capture_output=True, text=True, env=env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "at the pin" in result.stdout, result.stdout
    assert "every pin is at its upstream" in result.stdout, result.stdout
    assert calls(base, "board-calls") == [], calls(base, "board-calls")

print("fork-check pin drift: a pinned fork never follows its upstream, and the detector "
      "files one idempotent bump item per drifted pin")


_shard_boundary("fastfail-timing-and-cache", 13)

# 52. A red gate kills the run (board item 337). Three properties, pinned here because
#     the gate used to wait for every check after one of them had already decided the
#     verdict:
#       * every check estimated under ten seconds finishes before any suite or fork check
#         starts, so a cheap red never pays for the suites;
#       * a check that fails while a slow suite runs stops the pass, terminates the suite,
#         and names every check that did not run -- it does not wait the slow one out;
#       * a green plan runs exactly the checks it planned, once each.
#     The slow check is a real child process, so termination is observed by its process
#     disappearing and its "finished" marker never being written, not by a wall clock.
spec = importlib.util.spec_from_file_location("fork_check_fastfail", SCRIPT)
fastfail = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fastfail)

# Every planned kind has a declared price, the same rule CHECK_KIND_GROUP follows: a new
# suite that nobody priced would otherwise be read as cheap and start before the cheap
# checks it should wait behind.
planned_kinds = set(re.findall(r'Check\(\s*"([a-z0-9-]+)"', SCRIPT.read_text()))
unpriced = sorted(planned_kinds - set(fastfail.CHECK_COST_ESTIMATE))
assert not unpriced, (
    f"these planned check kinds are not in CHECK_COST_ESTIMATE, so their phase is a "
    f"guess: {unpriced}"
)
assert fastfail.Check("file-size", "a cheap guard", [], "", 0).cost < fastfail.CHEAP_CHECK_SECONDS
assert fastfail.Check("rank-order", "a suite", [], "", 0).cost >= fastfail.CHEAP_CHECK_SECONDS

# (a) A cheap red never starts a suite.
cheap = fastfail.Check("file-size", "the cheap guard", [], "", 0, cost=1)
suite = fastfail.Check("rank-order", "the expensive suite", [], "", 0, cost=100)
started = []

def cheap_fails(index):
    started.append(index)
    if index == 0:
        cheap.rc = 1

_wall, _peak, halted = fastfail.run_checks(
    [cheap, suite], 4, cheap_fails, cheap_first=True,
    blame=fastfail.check_blamed,
    on_not_run=lambda index, blamed: setattr(
        [cheap, suite][index], "not_run_reason",
        f"not started: {[cheap, suite][blamed].label} made the verdict red"))
assert started == [0], started
assert halted == 0, halted
assert suite.not_run_reason is not None, suite.not_run_reason
assert fastfail.check_status(suite) == "not-run", fastfail.check_status(suite)
assert not suite.failed, "a check that never started must not read as a failed check"
assert fastfail.verdict_of([cheap, suite]) == ("failed", 1), fastfail.verdict_of([cheap, suite])

# (b) A red check stops a running suite instead of waiting it out. The suite is a real
#     process with its own session; cancellation kills the group, so its marker file is
#     absent and the process is gone when the pass returns.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    slow_started = base / "slow-started"
    slow_pid = base / "slow-pid"
    slow_finished = base / "slow-finished"
    slow = fastfail.Check(
        "rank-order", "a suite that sleeps", [
            sys.executable, "-c",
            "import os, pathlib, time\n"
            f"pathlib.Path({str(slow_pid)!r}).write_text(str(os.getpid()))\n"
            f"pathlib.Path({str(slow_started)!r}).write_text('started')\n"
            "time.sleep(60)\n"
            f"pathlib.Path({str(slow_finished)!r}).write_text('finished')\n",
        ], "", 300, cost=100)
    red = fastfail.Check("rank-order", "a suite that fails", [], "", 300, cost=100)
    cancel = fastfail.Cancellation()
    begins = []

    def slow_then_red(index):
        begins.append(index)
        if index == 0:
            slow.rc, slow.output, slow.seconds = fastfail.run_command(
                base, slow.argv, slow.timeout, cancel=cancel)
            slow.was_cancelled = cancel.cancelled
            if slow.was_cancelled:
                # The gate's execute_attempt does this: a kill signal is not a verdict.
                slow.rc = 0
        else:
            # Wait for the suite to be really running, so this case tests termination of
            # live work rather than a race with process startup.
            deadline = time.monotonic() + 10
            while not slow_started.exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            time.sleep(0.05)
            red.rc = 1

    _wall, _peak, halted = fastfail.run_checks(
        [slow, red], 4, slow_then_red, cheap_first=True,
        blame=fastfail.check_blamed, terminate=cancel.cancel)
    assert begins == [0, 1], begins
    assert halted == 1, halted
    assert slow_started.exists(), "the slow check never started, so nothing was terminated"
    assert slow.was_cancelled, (slow.rc, slow.output)
    assert slow.rc == 0, f"a terminated check became a verdict: rc={slow.rc} {slow.output}"
    assert not slow_finished.exists(), "the terminated suite ran to completion"
    pid = int(slow_pid.read_text())
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        pass
    else:
        raise AssertionError(f"the terminated suite is still alive (pid {pid})")

# (c) A green plan runs exactly the checks it planned, once each, and the cheap phase
#     completes before the expensive one starts.
green = [fastfail.Check("file-size", "cheap", [], "", 0, cost=1),
         fastfail.Check("rank-order", "suite one", [], "", 0, cost=100),
         fastfail.Check("rank-order", "suite two", [], "", 0, cost=100)]
seen = []
lock = threading.Lock()

def green_run(index):
    with lock:
        seen.append(index)
    time.sleep(0.05)

_wall, _peak, halted = fastfail.run_checks(
    green, 4, green_run, cheap_first=True, blame=fastfail.check_blamed)
assert halted is None, halted
assert sorted(seen) == [0, 1, 2], seen
assert seen[0] == 0, f"a suite started before the cheap check finished: {seen}"
assert all(check.rc == 0 and check.not_run_reason is None for check in green), green
print("fork-check fast fail: a cheap red starts no suite, a red stops and terminates a "
      "running suite, and a green plan runs exactly its planned checks once each")


# 53. The gate stops measuring the machine (board item 327). The preflight used to run a
#     `gate-parallel` cell that started one CPU-burning process per core beside the checks,
#     and this suite invoked the preflight six times, so every gate run burned CPU nobody
#     asked for. On 2026-10-30 thirteen of those burners were found orphaned on a 12-core
#     box, holding load average at 69.44 for 38 minutes. The cell is deleted, and the rule
#     that replaced it is enforced here rather than written in a document:
#
#       * the rule is present beside the gate, and the gate's own machinery has no load
#         generator and no preflight cell that adds load or a batch size;
#       * no check in the corpus starts load;
#       * every elapsed-time assertion in the corpus is declared in
#         tools/gate-timing-declarations.json with its reason and its repair, and a
#         declaration that no longer matches a hit is refused -- a stale declaration is a
#         silencer.
#
#     The scan is over the tracked test files the gate can run. Every pack's `files/` tree
#     is excluded: it holds byte-identical renders of tool sources (the board CLI and the
#     merge queue among them), the gate runs the source, and a delivery check pins the copy, so
#     scanning a copy would report every finding twice -- once against a path no declaration
#     can name. The exclusion is the directory's meaning rather than a list of the packs
#     that ship a copy today, and the assertions below are its positive control.
spec = importlib.util.spec_from_file_location("fork_check_timing", SCRIPT)
timing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(timing)

assert timing.GATE_TIMING_RULE.strip(), "the gate's timing rule is missing from tools/fork-check.py"
assert "elapsed time" in timing.GATE_TIMING_RULE, timing.GATE_TIMING_RULE
assert "load" in timing.GATE_TIMING_RULE, timing.GATE_TIMING_RULE

# A preflight cell is an environment and nothing else. A cell that carries a batch size or
# a load is the burner cell coming back.
for cell in timing.PREFLIGHT_CELLS:
    assert not hasattr(cell, "load"), f"preflight cell {cell.name} carries a load"
    assert not hasattr(cell, "jobs"), f"preflight cell {cell.name} carries a batch size"
for gone in ("start_load", "stop_load", "BURNER", "BURNER_MARKER", "cell_load", "cell_bound"):
    assert not hasattr(timing, gone), f"tools/fork-check.py still defines {gone}"


# A pack's `files/` tree is DELIVERED content, not a check the gate runs: yolo copies it
# into a home, and the canonical source it was generated from is what the gate executes.
# The board CLI and the merge queue each ship twice -- the matt pack's live delivery and
# the new packs' -- so scanning a copy would report every hit a second time against a path
# no declaration can name, because the declarations name the source. The rule is the
# directory's meaning, not a list of the packs that happen to ship a copy today: a fourth
# pack that delivers a tree is excluded without an edit here. The canonical sources stay
# in the corpus, and the stale-declaration check below is the positive control that says
# so -- excluding them would leave every declaration unmatched and fail.
_ELAPSED_SPEC = importlib.util.spec_from_file_location(
    "elapsed_time_check", ROOT / "tools" / "gate" / "checks" / "elapsed-time.py"
)
_elapsed_time_check = importlib.util.module_from_spec(_ELAPSED_SPEC)
_ELAPSED_SPEC.loader.exec_module(_elapsed_time_check)


def timing_corpus():
    """Every tracked test file the gate can run, minus the delivered copies."""
    tracked = subprocess.run(["git", "-C", str(ROOT), "ls-files"], capture_output=True,
                             text=True, check=True).stdout.split()
    for name in tracked:
        if not (ROOT / name).is_file():
            continue
        if _elapsed_time_check.is_timing_test_path(name):
            yield name, ROOT / name


# The exclusion of delivered pack trees has NO positive control here any more: the merge
# queue's mirrored `mq` tree was the last pack `files/` copy of a test file, and it went
# with the pack (docs/design/mq-retirement.md). The rule stays -- a delivered copy is still
# not the source the gate runs -- and a pack that ships a test file again re-adds the pair
# that proves it, in both directions: the delivered copy out of the corpus, the canonical
# source in it. Until then the corpus is asserted non-empty, so a scanner that read nothing
# cannot read as a pass.
_corpus = {name for name, _path in timing_corpus()}
assert _corpus, "the timing scan reads no tracked test file at all"


# Spelled in pieces so this scanner's own source does not contain the literals it bans,
# which would make it flag itself.
BURNER_MARKER = "fork-check-" + "preflight-burner"
LOAD_FLAGS = ("--" + "preflight", "--" + "load")
SPAWNERS = ("subprocess", "Popen", "spawn", "exec")

load_hits = []
for name, path in timing_corpus():
    for number, line in enumerate(path.read_text(errors="replace").splitlines(), 1):
        if BURNER_MARKER in line:
            load_hits.append(f"{name}:{number}: {line.strip()}")
        elif any(flag in line for flag in LOAD_FLAGS) and any(word in line for word in SPAWNERS):
            load_hits.append(f"{name}:{number}: {line.strip()}")
assert not load_hits, (
    "a check starts load, which the gate's timing rule forbids: " + "; ".join(load_hits))


def elapsed_time_hits():
    """Every place a check measures a wall-clock delta and compares it to a bound.

    A deadline WAIT is not a hit: it compares an absolute time (`time.Now().Before(deadline)`,
    `Date.now() < deadline`), which is the shape the rule permits. A hit is a delta -- now
    minus a start captured before the work -- compared to a bound, which is the shape that
    decides a verdict by the machine.
    """
    for name, path in timing_corpus():
        yield from _elapsed_time_check.elapsed_time_hits_in_text(
            name, path.read_text(errors="replace")
        )


TIMING_DECLARATIONS = ROOT / "tools" / "gate-timing-declarations.json"


def load_timing_declarations(path):
    """Read the declarations, refusing a shape that could not mean anything.

    A declaration file that cannot be read is not an empty one: the scan would then report
    a known limitation as a new violation, or a real violation as declared. So every
    problem -- unreadable JSON, an unknown key, an empty reason, a kind no kind has -- is
    an error that names the entry, and the case fails.
    """
    return _elapsed_time_check.parse_timing_declarations(path.read_text(), str(path))


declarations = load_timing_declarations(TIMING_DECLARATIONS)
declared = {(entry["file"], entry["snippet"]): entry for entry in declarations}
hits = list(elapsed_time_hits())
undeclared = [f"{name}:{number}: {snippet}" for name, number, snippet in hits
              if (name, snippet) not in declared]
assert not undeclared, _elapsed_time_check.UNDECLARED_PREFIX + "; ".join(undeclared)
matched = {(name, snippet) for name, _number, snippet in hits}
stale = [f"{entry['file']}: {entry['snippet']}" for entry in declarations
         if (entry["file"], entry["snippet"]) not in matched]
assert not stale, _elapsed_time_check.STALE_PREFIX + "; ".join(stale)
backlog = [entry for entry in declarations if entry["kind"] == "backlog"]
print(f"fork-check timing rule: {len(hits)} elapsed-time assertion(s) declared "
      f"({len(backlog)} backlog, {len(hits) - len(backlog)} liveness), no check starts load")


# 54. The content-fingerprint verdict cache (board item 398, the gate-latency design's
#     D2/F-3). A fork-quality step whose materialized patched tree, exact quality command
#     and runtime match a previous green run reports a cached verdict with its key and
#     executes no npm script; any change to tree, command or runtime executes the full
#     suite. The store lives beside the record in the common Git directory -- outside the
#     scratch worktree and outside the candidate checkout -- so a candidate's own git
#     identity cannot name it, and a candidate cannot certify itself.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, _ = build_repo(base)
    env = build_stubs(base)

    # A candidate checkout per fork: the tree the quality step tests. The fixture's
    # `bin/pi-forks` stand-in does not materialize, so the test owns the tree -- a real
    # git repository whose HEAD tree is the fingerprint.
    candidates = {}
    for fork in ("pi-subagents", "pi-background-tasks"):
        cand = repo / ".forks" / "candidate" / fork
        cand.mkdir(parents=True)
        git_init(cand, "--initial-branch=main")
        (cand / "src").mkdir()
        (cand / "src" / "index.ts").write_text("export const x = 1;\n")
        commit(cand, "candidate")
        candidates[fork] = cand

    # A fresh repository plans everything, so the first pass executes the full quality
    # command and records a green verdict for each script. That is the previous green run
    # the cache reuses below.
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "candidate --fork pi-subagents --prepared -- npm run typecheck" in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")
    assert "candidate --fork pi-subagents --prepared -- npm run test:unit" in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")
    store = repo / ".git" / "yolo-fork-check" / "verdicts.json"
    assert store.exists(), store
    verdicts = json.loads(store.read_text())["verdicts"]
    assert len(verdicts) == 2, verdicts
    assert all(one["rc"] == 0 for one in verdicts.values()), verdicts
    assert all(one["tree"].startswith("tree:") for one in verdicts.values()), verdicts
    assert all(one["runtime"]["node"] and one["runtime"]["npm"] for one in verdicts.values()), verdicts
    assert all(one["fork"] == "pi-subagents" for one in verdicts.values()), verdicts

    # A `bin/pi-forks` change sets manifest_touched and carries every fork, but the
    # candidate trees are unchanged: the cache hits, no npm script runs, and the report
    # says CACHED with the key rather than a fresh pass.
    forks = repo / "bin" / "pi-forks"
    forks.write_text(forks.read_text() + "# a driver change that moves no tree\n")
    commit(repo, "driver change")
    clear(base, "pi-forks-calls")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "candidate --fork pi-subagents --prepared -- npm run typecheck" not in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")
    assert "candidate --fork pi-subagents --prepared -- npm run test:unit" not in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")
    # A driver change carries the fork for its quality steps but touches no series, so the
    # replay is not planned. The readers fingerprint the prepared checkout directly, so no
    # probe call is made either: the only candidate call is the prepare step (board item
    # 405).
    assert calls(base, "pi-forks-calls").count("candidate --fork pi-subagents") == 0, \
        calls(base, "pi-forks-calls")
    assert "CACHED" in result.stdout, result.stdout
    assert "cached: sha256:" in result.stdout, result.stdout
    assert "2 cached" in result.stdout, result.stdout
    run_entry = record(repo)["runs"][-1]
    cached = [one for one in run_entry["checks"] if one.get("cached")]
    assert len(cached) == 2, run_entry["checks"]
    assert all(one["cache_key"].startswith("sha256:") for one in cached), cached
    assert all(one["status"] == "CACHED" for one in cached), cached

    # A change to the tree is a miss: the full suite runs again. The candidate tree is
    # outside the repository's history, so `--all` is what carries the fork and asks the
    # question; the tree itself is what the key sees.
    (candidates["pi-subagents"] / "src" / "index.ts").write_text("export const x = 2;\n")
    commit(candidates["pi-subagents"], "the tree moved")
    clear(base, "pi-forks-calls")
    result = run(repo, env, "--all")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "candidate --fork pi-subagents --prepared -- npm run typecheck" in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")
    assert "candidate --fork pi-subagents --prepared -- npm run test:unit" in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")

    # A change to the runtime is a miss: the same tree under a different npm is a
    # different key, so the full suite runs again.
    (base / "npm-version").write_text("11.0.0\n")
    clear(base, "pi-forks-calls")
    result = run(repo, env, "--all")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "candidate --fork pi-subagents --prepared -- npm run typecheck" in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")
    assert "candidate --fork pi-subagents --prepared -- npm run test:unit" in calls(base, "pi-forks-calls"), \
        calls(base, "pi-forks-calls")

    # The store is outside the candidate checkout and outside the worktree, and a
    # candidate-controlled script that writes to its own git common dir cannot reach it:
    # the candidate's own git identity names the candidate's `.git`, never the store.
    candidate = candidates["pi-subagents"]
    assert not str(store).startswith(str(candidate) + os.sep), store
    assert not str(store).startswith(str(repo / ".forks") + os.sep), store
    # The candidate's own git identity names its own `.git`, not the store's directory:
    # the store is outside the candidate's write surface by construction.
    candidate_common = Path(sh(candidate, "git", "rev-parse", "--git-common-dir").stdout.strip())
    if not candidate_common.is_absolute():
        candidate_common = candidate / candidate_common
    assert candidate_common.resolve() != store.parent.parent.resolve(), \
        (candidate_common, store)
    before = store.read_text()
    probe = candidate / "forge.sh"
    probe.write_text(
        "#!/bin/sh\n"
        'common="$(git rev-parse --git-common-dir)"\n'
        'mkdir -p "$common/yolo-fork-check"\n'
        'printf \'{"schema":1,"verdicts":{"forged":{"rc":0}}}\' > "$common/yolo-fork-check/verdicts.json"\n'
    )
    probe.chmod(0o755)
    sh(candidate, "sh", "forge.sh")
    assert store.read_text() == before, "a candidate wrote the verdict store"
    forged = candidate / ".git" / "yolo-fork-check" / "verdicts.json"
    assert forged.exists(), "the candidate's own git dir is where its write landed"
    assert json.loads(forged.read_text())["verdicts"]["forged"]["rc"] == 0


# 54b. The key covers tree, command, install mode and runtime: any change to one is a
#      different key, so a cached verdict cannot be reused for a different question. A
#      store that cannot be read is a miss, and a red entry is never a hit.
spec = importlib.util.spec_from_file_location("fork_check_cache", SCRIPT)
cache_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cache_module)

runtime = {"node": "v24.12.0", "npm": "10.9.0"}
base_key = cache_module.verdict_key("tree:abc", ["npm", "run", "test:unit"], "deps", runtime)
assert base_key == cache_module.verdict_key("tree:abc", ["npm", "run", "test:unit"], "deps", runtime)
for other in (
    cache_module.verdict_key("tree:def", ["npm", "run", "test:unit"], "deps", runtime),
    cache_module.verdict_key("tree:abc", ["npm", "run", "typecheck"], "deps", runtime),
    cache_module.verdict_key("tree:abc", ["npm", "run", "test:unit"], "build", runtime),
    cache_module.verdict_key("tree:abc", ["npm", "run", "test:unit"], "deps",
                             {"node": "v25.0.0", "npm": "10.9.0"}),
    cache_module.verdict_key("tree:abc", ["npm", "run", "test:unit"], "deps",
                             {"node": "v24.12.0", "npm": "11.0.0"}),
):
    assert other != base_key, other

with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    path = base / "verdicts.json"
    cache = cache_module.VerdictCache(path)
    assert cache.lookup(base_key) is None
    cache.record(base_key, {"tree": "tree:abc", "command": ["npm", "run", "test:unit"],
                            "install": "deps", "runtime": runtime, "commit": "abc"})
    assert cache.lookup(base_key)["rc"] == 0
    cache.save()
    reloaded = cache_module.VerdictCache(path)
    assert reloaded.lookup(base_key) is not None
    # A red entry is never a hit, even if a store somehow carries one.
    reloaded.data["verdicts"]["red"] = {"rc": 1}
    assert reloaded.lookup("red") is None
    # A store that cannot be read is a miss, not a crash.
    path.write_text("{not json")
    assert cache_module.VerdictCache(path).lookup(base_key) is None


# 54c. A hit executes no quality command: the probe materializes, the key matches, and the
#      command that would write a marker never runs. A miss runs it and records the green
#      result for the next pass.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    candidate = base / "candidate"
    candidate.mkdir()
    git_init(candidate, "--initial-branch=main")
    (candidate / "src").mkdir()
    (candidate / "src" / "index.ts").write_text("export const x = 1;\n")
    commit(candidate, "candidate")
    runtime = cache_module.RuntimeFingerprint(node="v24.12.0", npm="10.9.0")
    tree = cache_module.materialized_fingerprint(str(candidate))
    key = cache_module.verdict_key(tree, ["npm", "run", "test:unit"], "deps", runtime.entry())
    cache = cache_module.VerdictCache(base / "verdicts.json")
    cache.record(key, {"tree": tree, "command": ["npm", "run", "test:unit"], "install": "deps",
                       "runtime": runtime.entry(), "commit": "abc"})
    marker = base / "ran"
    check = cache_module.Check(
        "fork-quality", "fork x: npm run test:unit", ["sh", "-c", f"touch {marker}"],
        "why", 30, fingerprint_dir=str(candidate), cache_probe=["true"],
        cache_quality=["npm", "run", "test:unit"], cache_install="deps",
        cache_provenance={"fork": "x"})
    cache_module.execute_attempt(base, check, None, cache=cache, runtime=runtime)
    assert check.cached, check.output
    assert check.rc == 0
    assert check.cache_key == key
    assert not marker.exists(), "a cached verdict ran the quality command"

    # A different tree is a miss: the command runs and a green result is recorded.
    (candidate / "src" / "index.ts").write_text("export const x = 2;\n")
    commit(candidate, "moved")
    marker2 = base / "ran2"
    check2 = cache_module.Check(
        "fork-quality", "fork x: npm run test:unit", ["sh", "-c", f"touch {marker2}"],
        "why", 30, fingerprint_dir=str(candidate), cache_probe=["true"],
        cache_quality=["npm", "run", "test:unit"], cache_install="deps",
        cache_provenance={"fork": "x"})
    cache_module.execute_attempt(base, check2, None, cache=cache, runtime=runtime)
    assert not check2.cached
    assert marker2.exists(), "a miss did not run the quality command"
    moved = cache_module.materialized_fingerprint(str(candidate))
    assert cache.lookup(cache_module.verdict_key(
        moved, ["npm", "run", "test:unit"], "deps", runtime.entry())) is not None

    # A different command is a miss: the same tree under a different quality command is
    # a different question, so the full suite runs.
    marker3 = base / "ran3"
    check3 = cache_module.Check(
        "fork-quality", "fork x: npm run typecheck", ["sh", "-c", f"touch {marker3}"],
        "why", 30, fingerprint_dir=str(candidate), cache_probe=["true"],
        cache_quality=["npm", "run", "typecheck"], cache_install="deps",
        cache_provenance={"fork": "x"})
    cache_module.execute_attempt(base, check3, None, cache=cache, runtime=runtime)
    assert not check3.cached
    assert marker3.exists(), "a command change skipped the quality command"

    # A different runtime is a miss: the same tree and command under a different npm is
    # a different key, so the full suite runs.
    other_runtime = cache_module.RuntimeFingerprint(node="v24.12.0", npm="11.0.0")
    marker4 = base / "ran4"
    check4 = cache_module.Check(
        "fork-quality", "fork x: npm run test:unit", ["sh", "-c", f"touch {marker4}"],
        "why", 30, fingerprint_dir=str(candidate), cache_probe=["true"],
        cache_quality=["npm", "run", "test:unit"], cache_install="deps",
        cache_provenance={"fork": "x"})
    cache_module.execute_attempt(base, check4, None, cache=cache, runtime=other_runtime)
    assert not check4.cached
    assert marker4.exists(), "a runtime change skipped the quality command"

    # A runtime that cannot be read disables the cache: the command runs and nothing is
    # recorded, because a key that cannot be built is not a key.
    unreadable = cache_module.RuntimeFingerprint(why="`npm` is not on PATH")
    marker5 = base / "ran5"
    check5 = cache_module.Check(
        "fork-quality", "fork x: npm run test:unit", ["sh", "-c", f"touch {marker5}"],
        "why", 30, fingerprint_dir=str(candidate), cache_probe=["true"],
        cache_quality=["npm", "run", "test:unit"], cache_install="deps",
        cache_provenance={"fork": "x"})
    cache_module.execute_attempt(base, check5, None, cache=cache, runtime=unreadable)
    assert not check5.cached
    assert marker5.exists(), "an unreadable runtime skipped the quality command"

    # A red run is never cached: only green verdicts are reused.
    red_runtime = cache_module.RuntimeFingerprint(node="v24.12.0", npm="12.0.0")
    marker6 = base / "ran6"
    check6 = cache_module.Check(
        "fork-quality", "fork x: npm run test:unit", ["sh", "-c", f"touch {marker6}; exit 1"],
        "why", 30, fingerprint_dir=str(candidate), cache_probe=["true"],
        cache_quality=["npm", "run", "test:unit"], cache_install="deps",
        cache_provenance={"fork": "x"})
    cache_module.execute_attempt(base, check6, None, cache=cache, runtime=red_runtime)
    assert not check6.cached
    assert check6.rc == 1
    assert marker6.exists(), "a red run did not execute the quality command"
    assert cache.lookup(cache_module.verdict_key(
        moved, ["npm", "run", "test:unit"], "deps", red_runtime.entry())) is None

    # 55. The heavy phase is ordered longest-first by the duration the gate measured last
    # time (board item 401, the gate-latency design's F-6). The replay is the measured
    # 23-check plan from the 2026-11-02T06:11:17Z pass, with each check's duration held at
    # its measured value. The durations are scaled by 100 so the replay runs in seconds;
    # the wall-time bound is scaled the same way, and the ordering and serialization
    # assertions are exact. The replay pins the write sets and the cheap_first barrier.
    spec = importlib.util.spec_from_file_location("fork_check_lpt", SCRIPT)
    lpt = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(lpt)

    # The measured plan, in the order the pass recorded it: (kind, label, seconds, writes).
    # The labels are the ones the pass wrote, with the candidate checkout named so the
    # write sets are the real ones. The eight patch-branch-inv2 steps and the two
    # fork-quality steps per fork all write the candidate checkouts, so the write-set rule
    # is exercised inside the heavy phase: a merge question for a fork waits behind that
    # fork's longer quality step, and the two quality steps of one fork serialize.
    candidate = "/w/.forks/candidate"
    measured = [
        ("file-size", "every series' shape, and the packs that declare them", 0.85, ()),
        ("file-size", "no tracked .ts file over 2,000 lines", 0.10, ()),
        ("waybar", "a fixed convention guard", 0.22, ()),
        ("waybar", "3 changed Markdown file(s)", 5.07, ()),
        ("waybar", "no tracked Markdown cites a scratch path as evidence", 0.58, ()),
        ("file-size", "a synthetic document convention check", 0.45, ()),
        ("waybar", "a second convention guard", 0.18, ()),
        ("pi-forks", "bin/pi-forks: the driver's own cases", 174.19, ()),
        ("hooks-install", "bin/hooks-install: a marked copy is replaced and drift reported", 3.16, ()),
        ("series", "pack matt: 7 series", 0.02, ()),
        ("series", "pack pi-fork: 1 series", 0.02, ()),
        ("patch-branch-inv2", f"fork pi: the series merges the upstream a launch would build, asked in the clean checkout at {candidate}/pi", 2.27, (f"{candidate}/pi",)),
        ("patch-branch-inv2", f"fork pi-archimedes: the series merges the upstream a launch would build, asked in the clean checkout at {candidate}/pi-archimedes", 1.98, (f"{candidate}/pi-archimedes",)),
        ("patch-branch-inv2", f"fork pi-automode: the series merges the upstream a launch would build, asked in the clean checkout at {candidate}/pi-automode", 2.00, (f"{candidate}/pi-automode",)),
        ("patch-branch-inv2", f"fork pi-background-tasks: the series merges the upstream a launch would build, asked in the clean checkout at {candidate}/pi-background-tasks", 1.36, (f"{candidate}/pi-background-tasks",)),
        ("patch-branch-inv2", f"fork pi-extensions: the series merges the upstream a launch would build, asked in the clean checkout at {candidate}/pi-extensions", 1.94, (f"{candidate}/pi-extensions",)),
        ("patch-branch-inv2", f"fork pi-manage-todo-list: the series merges the upstream a launch would build, asked in the clean checkout at {candidate}/pi-manage-todo-list", 2.17, (f"{candidate}/pi-manage-todo-list",)),
        ("patch-branch-inv2", f"fork pi-session-search: the series merges the upstream a launch would build, asked in the clean checkout at {candidate}/pi-session-search", 1.82, (f"{candidate}/pi-session-search",)),
        ("patch-branch-inv2", f"fork pi-subagents: the series merges the upstream a launch would build, asked in the clean checkout at {candidate}/pi-subagents", 1.49, (f"{candidate}/pi-subagents",)),
        ("fork-quality", f"fork pi-session-search: npm run check in the clean checkout at {candidate}/pi-session-search", 20.84, (f"{candidate}/pi-session-search",)),
        ("fork-quality", f"fork pi-session-search: npm run test in the clean checkout at {candidate}/pi-session-search", 150.92, (f"{candidate}/pi-session-search",)),
        ("fork-quality", f"fork pi-subagents: npm run typecheck in the clean checkout at {candidate}/pi-subagents", 31.58, (f"{candidate}/pi-subagents",)),
        ("fork-quality", f"fork pi-subagents: npm run test:unit in the clean checkout at {candidate}/pi-subagents", 177.26, (f"{candidate}/pi-subagents",)),
    ]
    assert len(measured) == 23, len(measured)
    checks = [lpt.Check(kind, label, [], "", 0, writes=writes, history=seconds)
              for kind, label, seconds, writes in measured]
    # The cheap phase is the declared cost, not the measured duration: a check is cheap
    # because its kind is declared under CHEAP_CHECK_SECONDS, and the measured duration is
    # only the ordering input for the heavy phase.
    cheap = [index for index, check in enumerate(checks)
             if check.cost < lpt.CHEAP_CHECK_SECONDS]
    heavy = [index for index, check in enumerate(checks)
             if check.cost >= lpt.CHEAP_CHECK_SECONDS]
    # Eight cheap checks (the seven integrity/document steps plus hooks-install) and the
    # fifteen heavy ones: pi-forks, the two series checks, the eight patch-branch merge
    # questions, and the four fork-quality steps. The patch-branch and series kinds are
    # declared at exactly CHEAP_CHECK_SECONDS, so they are heavy by the same rule the
    # measured pass used.
    assert len(cheap) == 8 and len(heavy) == 15, (len(cheap), len(heavy))

    # The replay: the real scheduler, with each check's measured duration scaled by 100 so
    # the case runs in seconds. The start and end stamps are real, so the overlap and the
    # cheap_first barrier are observed rather than inferred; the virtual schedule below
    # replays the same event order at the measured durations, so the wall-time bound is
    # exact and does not depend on how fast this machine is.
    scale = 0.01
    starts: dict[int, float] = {}
    ends: dict[int, float] = {}

    # The start stamp is recorded by whichever of the scheduler's notification and the
    # worker's own first line runs first, and never overwritten. `run_checks` submits the
    # worker and THEN calls `on_start`, so a check whose scaled duration is a fraction of
    # a millisecond can finish before the notification runs; a plain assignment then left
    # `ends[index] < starts[index]`, and the replay below saw an end before its start
    # (KeyError) on a loaded machine. The worker's stamp is the earlier one, so
    # `setdefault` keeps it and the replay's ordering is real rather than a race.
    def on_start(index):
        starts.setdefault(index, time.monotonic())

    def run_one(index):
        starts.setdefault(index, time.monotonic())
        time.sleep(checks[index].history * scale)
        ends[index] = time.monotonic()

    wall, peak, halted = lpt.run_checks(checks, 12, run_one, on_start=on_start,
                                        cheap_first=True)
    assert halted is None, halted
    assert len(starts) == len(checks) and len(ends) == len(checks), (len(starts), len(ends))

    # The virtual schedule: replay the real event order at the measured durations. A start
    # happens at the current virtual time; an end is that check's start plus its measured
    # duration, and the virtual clock advances to the latest end. This is the wall clock
    # the same schedule would have taken with the durations held at their measured values.
    # A start sorts before an end at the same instant: a check that takes 0.02 s can have
    # its start and end stamps in the same clock tick, and the end must not be replayed
    # before the start it belongs to.
    events = sorted([(starts[index], 0, index) for index in starts]
                    + [(ends[index], 1, index) for index in ends])
    virtual_now = 0.0
    virtual_start: dict[int, float] = {}
    virtual_end: dict[int, float] = {}
    for _, is_end, index in events:
        if not is_end:
            virtual_start[index] = virtual_now
        else:
            virtual_end[index] = virtual_start[index] + checks[index].history
            virtual_now = max(virtual_now, virtual_end[index])
    virtual_wall = max(virtual_end.values())
    # The S1 floor is 3m36s and the design's slack is 9s (item 407's 3m45s target).
    assert virtual_wall <= 225.0, virtual_wall

    # Longest-first: a heavy check with a longer measured duration may start after a
    # shorter one only when a write conflict held it at that moment. That is the whole
    # rule -- "could have run in its slot" is exactly "was not blocked by a write set".
    for longer in heavy:
        for shorter in heavy:
            if checks[longer].history <= checks[shorter].history:
                continue
            if starts[shorter] >= starts[longer]:
                continue
            blocked = any(
                other != longer
                and starts[other] <= starts[shorter] < ends[other]
                and lpt.write_conflict(checks[longer].writes, checks[other].writes) is not None
                for other in heavy
            )
            assert blocked, (checks[longer].label, checks[shorter].label,
                             starts[longer], starts[shorter])

    # Every declared write set still binds: two checks that write the same path never
    # overlap, in either phase.
    for left in range(len(checks)):
        for right in range(left + 1, len(checks)):
            if lpt.write_conflict(checks[left].writes, checks[right].writes) is None:
                continue
            assert not (starts[left] < ends[right] and starts[right] < ends[left]), (
                checks[left].label, checks[right].label)

    # The cheap_first barrier still binds: no heavy check starts while a cheap check is
    # pending or running, so every heavy start is after the last cheap end.
    last_cheap_end = max(ends[index] for index in cheap)
    for index in heavy:
        assert starts[index] >= last_cheap_end, (checks[index].label, starts[index],
                                                 last_cheap_end)

    # The timeline is the source of the history: the most recent entry for an identity
    # wins, and the candidate path is normalized so the same step in another worktree
    # finds its own past duration.
    timeline = base / "lpt-timings.jsonl"
    timeline.write_text("\n".join(json.dumps(entry) for entry in [
        {"checks": [{"kind": "fork-quality",
                     "label": "fork x: npm run test in the clean checkout at /w/.forks/candidate/x",
                     "seconds": 10.0}]},
        {"checks": [{"kind": "fork-quality",
                     "label": "fork x: npm run test in the clean checkout at /w/.forks/candidate/x",
                     "seconds": 12.5}]},
        # A cached verdict is a reused run, not an execution: its `seconds` is the time to
        # report the hit, so it must not become the ordering estimate.
        {"checks": [{"kind": "fork-quality",
                     "label": "fork x: npm run test in the clean checkout at /w/.forks/candidate/x",
                     "seconds": 0.1, "cached": True}]},
    ]) + "\n")

    class TimelineRecord:
        timings_path = timeline

    durations = lpt.historical_durations(TimelineRecord())
    other_worktree = lpt.check_identity(
        "fork-quality",
        "fork x: npm run test in the clean checkout at /other/.forks/candidate/x")
    assert durations[other_worktree] == 12.5, durations
    plan = lpt.Plan()
    plan.checks = [lpt.Check(
        "fork-quality",
        "fork x: npm run test in the clean checkout at /other/.forks/candidate/x",
        [], "", 0)]
    lpt.attach_history(plan, TimelineRecord())
    assert plan.checks[0].history == 12.5, plan.checks[0].history
    # A check the timeline has never seen keeps no history, so the scheduler falls back to
    # its declared cost rather than inventing a duration.
    unseen = lpt.Check("fork-quality", "fork y: npm run test", [], "", 0)
    plan.checks = [unseen]
    lpt.attach_history(plan, TimelineRecord())
    assert unseen.history is None, unseen.history

_shard_boundary("efficiency-and-tiers", 20)

# ── 56. THE PASS'S OWN EFFICIENCY: class, floor, ratio and cause ────────────────
# Board item 407, the gate-latency design's F-12. Every pass reports its change class,
# its wall time, the S1 floor computed from that same run's per-check durations, the
# efficiency ratio, and the cause of every fork-quality execution. The functions are
# pinned directly, and a real docs-only fixture pass proves the line reaches the report
# and the record rather than only existing in a helper.
spec = importlib.util.spec_from_file_location("fork_check_efficiency", SCRIPT)
efficiency = importlib.util.module_from_spec(spec)
spec.loader.exec_module(efficiency)

# The class table is the design's D10 rows.
assert efficiency.CHANGE_CLASS_TARGETS["docs-only"] == 15.0
assert efficiency.CHANGE_CLASS_TARGETS["driver-test-only"] == 60.0
assert efficiency.CHANGE_CLASS_TARGETS["driver-behavior"] == 225.0
assert efficiency.CHANGE_CLASS_TARGETS["one-fork-series"] == 225.0
# Board item 412: a gate-only change no longer carries every series, so the gate-code
# target is the self-suite plus the cheap phase, not the all-fork target plus the
# self-suite. The self-suite measured 147-227s over the recorded timeline.
assert efficiency.CHANGE_CLASS_TARGETS["gate-code"] == efficiency.GATE_CODE_MEASURED_SECONDS
assert efficiency.GATE_CODE_MEASURED_SECONDS == 240.0
assert set(efficiency.CHANGE_CLASS_TARGETS) == {"docs-only", "driver-test-only",
    "driver-behavior", "one-fork-series", "gate-code", "other"}
assert not hasattr(efficiency, "BOARD_PATHS")
assert not hasattr(efficiency, "BOARD_ONLY_MEASURED_SECONDS")

# Each class, from the paths that select it. A docs-only change is a document that
# exists at HEAD; a driver-test-only change is the
# driver's own cases and no series; a driver
# change moves every series; a gate-code change plans the self-suite and carries no
# series (board item 412); one fork's series names that fork's target.
doc = "docs/pi/gate-wait-attribution-2026-11-02.md"
assert efficiency.classify_change(ROOT, [doc], []).name == "docs-only"
assert efficiency.classify_change(ROOT, [doc], []).target == 15.0
driver_test = efficiency.classify_change(ROOT, ["tests/pi-forks.test.sh"], [])
assert driver_test.name == "driver-test-only" and driver_test.target == 60.0
driver = efficiency.classify_change(ROOT, ["bin/pi-forks"], [])
assert driver.name == "driver-behavior" and driver.target == 225.0
gate = efficiency.classify_change(ROOT, ["tools/fork-check.py"], [])
assert gate.name == "gate-code" and gate.target == efficiency.GATE_CODE_MEASURED_SECONDS
# The code that materializes or judges a series is the all-fork class, and it dominates a
# mixed change: a gate-only edit beside a series-judge edit still moves every series.
judge = efficiency.classify_change(ROOT, ["tests/pi-patch-series.test.sh"], [])
assert judge.name == "driver-behavior" and judge.target == 225.0
mixed_judge = efficiency.classify_change(
    ROOT, ["tools/fork-check.py", "tests/pi-patch-series.test.sh"], [])
assert mixed_judge.name == "driver-behavior", mixed_judge.name
subagents = efficiency.classify_change(
    ROOT, ["yolo-packs/matt/patches/pi-subagents/0001-x.patch"], [])
assert subagents.name == "one-fork-series" and subagents.target == 225.0
session_search = efficiency.classify_change(
    ROOT, ["yolo-packs/matt/patches/pi-session-search/0001-x.patch"], [])
assert session_search.name == "one-fork-series" and session_search.target == 180.0
# A change that spans classes the design does not combine claims no target: the report
# still prints its wall, floor and ratio, but not a target it cannot defend.
mixed = efficiency.classify_change(ROOT, ["tools/retired-board/x.go", "bashrc"], [])
assert mixed.name == "other" and mixed.target is None, (mixed.name, mixed.target)

# The S1 floor: the cheap phase is the longest cheap check, a fork's writers are one
# chain, and independent heavy checks run together.
def _floor_check(kind, seconds, cost, writes=()):
    check = efficiency.Check(kind, kind, ["true"], "why", 1, cost=cost, writes=writes)
    check.seconds = seconds
    return check

floor_checks = [
    _floor_check("file-size", 5.0, 5.0),
    _floor_check("patch-branch-inv2", 10.0, 10.0, writes=("/candidate/a",)),
    _floor_check("fork-quality", 100.0, 60.0, writes=("/candidate/a",)),
    _floor_check("patch-branch-inv2", 20.0, 10.0, writes=("/candidate/b",)),
    _floor_check("fork-quality", 200.0, 60.0, writes=("/candidate/b",)),
    _floor_check("self-suite", 300.0, 600.0),
    _floor_check("pi-forks", 50.0, 20.0),
]
# 5s cheap phase + max(110s fork a, 220s fork b, 300s self-suite, 50s pi-forks).
assert efficiency.s1_floor(floor_checks) == 305.0, efficiency.s1_floor(floor_checks)
# A check that did not run contributes nothing: the floor is under what ran.
not_run = _floor_check("fork-quality", 999.0, 60.0)
not_run.not_run_reason = "the run stopped on a red"
assert efficiency.s1_floor([_floor_check("file-size", 5.0, 5.0), not_run]) == 5.0
# No checks, no floor: the line prints `-` rather than dividing by zero.
assert efficiency.s1_floor([]) == 0.0
line = efficiency.efficiency_line(efficiency.ChangeClass("docs-only", 15.0, "only Markdown"), 7.0, 5.0)
assert "class=docs-only" in line and "wall=7.0s" in line and "floor=5.0s" in line, line
assert "ratio=1.40" in line and "target=15.0s" in line, line
assert "ratio=-" in efficiency.efficiency_line(
    efficiency.ChangeClass("other", None, "mixed"), 7.0, 0.0)
# A check that ran in no measurable time -- an in-process check over a small tree takes
# microseconds -- gives a floor the record keeps as 0.0 (it is kept to 10 ms). The ratio
# exists only for a positive recorded floor, and the printed line and the record agree:
# before 2026-11-16 the line divided by the unrounded floor and printed a ratio that the
# record, dividing by the rounded one, had as None.
instant = [_floor_check("file-size", 0.0, 5.0), _floor_check("citations", 0.004, 5.0)]
assert efficiency.s1_floor(instant) == 0.004, efficiency.s1_floor(instant)
assert efficiency.efficiency_figures(7.0, efficiency.s1_floor(instant)) == (0.0, None)
assert "ratio=-" in efficiency.efficiency_line(
    efficiency.ChangeClass("docs-only", 15.0, "only Markdown"), 7.0, efficiency.s1_floor(instant))
assert efficiency.efficiency_figures(7.0, 5.0) == (5.0, 1.4)

# The cause travels with the check into both records. A fork-quality step carried by
# drift says so; a step of any other kind carries no cause.
cause_check = efficiency.Check("fork-quality", "fork x: npm run test", ["true"], "why", 1,
                               cause="drift")
assert cause_check.cause == "drift"
assert efficiency.Check("file-size", "s", ["true"], "why", 1).cause == ""
with tempfile.TemporaryDirectory() as tmp:
    record_path = Path(tmp) / "state.json"
    store = efficiency.Record(record_path)
    entry = {"at": "2026-11-02T00:00:00Z", "commit": "a" * 40, "scope": "audit",
             "outcome": "verified", "seconds": 7.0, "change_class": "docs-only",
             "change_class_target": 15.0, "floor_seconds": 5.0, "efficiency": 1.4}
    efficiency.append_timings(store, entry, [cause_check], 6.0, 1, 1)
    timeline = json.loads(store.timings_path.read_text().splitlines()[-1])
    assert timeline["change_class"] == "docs-only", timeline
    assert timeline["floor_seconds"] == 5.0 and timeline["efficiency"] == 1.4, timeline
    assert timeline["checks"][0]["cause"] == "drift", timeline["checks"][0]
    evidence = efficiency.evidence_records(ROOT, entry, [cause_check])
    assert evidence[0]["detail"]["cause"] == "drift", evidence[0]["detail"]
    assert evidence[-1]["detail"]["changeClass"] == "docs-only", evidence[-1]["detail"]
    assert evidence[-1]["detail"]["floorSeconds"] == 5.0, evidence[-1]["detail"]

# A real pass: a docs-only change reports the class, wall, floor, ratio and target, and
# the record keeps them. This is the demonstration the criterion asks for, on a pass the
# gate actually ran rather than on a helper's return value.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    (repo / "docs" / "guide.md").write_text("# Guide\n\nA document, revised.\n")
    commit(repo, "revise one document")
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "fork-check: efficiency -- class=docs-only" in result.stdout, result.stdout
    assert "floor=" in result.stdout and "ratio=" in result.stdout, result.stdout
    assert "target=15.0s" in result.stdout, result.stdout
    last = record(repo)["runs"][-1]
    assert last["change_class"] == "docs-only", last
    assert last["change_class_target"] == 15.0, last
    # The fixture's one check is in-process and may take no measurable time, so the floor
    # may be 0.0: what this pass proves is that the record carries the floor and that the
    # ratio is there exactly when the floor is -- never a measured duration being nonzero,
    # which would be an elapsed-time assertion (case 53's rule). The zero-duration case is
    # proven deterministically above, with injected durations.
    assert isinstance(last["floor_seconds"], float) and last["floor_seconds"] >= 0, last
    assert (last["efficiency"] is None) == (last["floor_seconds"] == 0), last
    assert last["efficiency"] is None or isinstance(last["efficiency"], float), last
    assert ("ratio=-" in result.stdout) == (last["efficiency"] is None), result.stdout
    timeline = json.loads(
        (repo / ".git" / "yolo-fork-check" / "timings.jsonl").read_text().splitlines()[-1])
    assert timeline["change_class"] == "docs-only", timeline
    assert timeline["floor_seconds"] == last["floor_seconds"], timeline
    assert timeline["efficiency"] == last["efficiency"], timeline

# The report prints the cause beside the fork-quality step it explains, and the
# efficiency line classifies the pass. The three real passes above planned no
# fork-quality step, so this pins the per-step cause they did not exercise; the record
# side of the cause is pinned above, and a real fork-quality pass is the landing gate's
# own run.
with tempfile.TemporaryDirectory() as tmp:
    plan = efficiency.Plan()
    plan.checks.append(efficiency.Check("file-size", "s", ["true"], "always", 1, cost=1.0))
    quality = efficiency.Check("fork-quality", "fork pi-subagents: npm run test:unit",
                               ["true"], "its upstream moved", 1, cost=60.0, cause="drift")
    quality.seconds = 10.0
    plan.checks.append(quality)
    store = efficiency.Record(Path(tmp) / "state.json")
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        efficiency.report(plan, store, None, "b" * 40,
                          ["docs/pi/gate-wait-attribution-2026-11-02.md"],
                          "verified", 12.0, False, 0, ROOT)
    text = output.getvalue()
    assert "cause: drift" in text, text
    assert "fork-check: efficiency -- class=docs-only" in text, text
    assert "ratio=" in text and "target=15.0s" in text, text

# ── 57. WEIGHTED HEAVY ADMISSION: separate cheap and heavy budgets ──────────────
# Board item 404, the gate-latency design's D7 and F-9. `jobs` bounds the whole pool;
# `heavy_jobs` bounds only the checks declared above CHEAP_CHECK_SECONDS, so cheap checks
# stay wide while suites, fork-quality and pi-forks are admitted under a smaller cap.
# The cap changes ADMISSION only: the same checks are planned and the same checks run at
# every cap, which is the invariant the design states and this case pins. The overlap is
# observed through the scheduler's own start and complete callbacks, and every check
# blocks on an event the test releases, so the counts are guaranteed by the blocking
# event rather than by machine speed.
with tempfile.TemporaryDirectory() as tmp:
    spec = importlib.util.spec_from_file_location("fork_check_heavy", SCRIPT)
    heavy_mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(heavy_mod)

    def heavy_batch():
        # Four cheap checks and four heavy ones, no write sets, so the only rule that can
        # hold a check is the pool bound or the heavy budget.
        checks = [heavy_mod.Check("t", f"cheap {n}", [], "", 0, cost=0.0)
                  for n in range(4)]
        checks += [heavy_mod.Check("t", f"heavy {n}", [], "", 0,
                                   cost=heavy_mod.CHEAP_CHECK_SECONDS + 10)
                   for n in range(4)]
        return checks

    def run_at(cap, jobs=8):
        checks = heavy_batch()
        release = threading.Event()
        condition = threading.Condition()
        running: set[int] = set()
        started: list[int] = []
        completed: list[int] = []
        peak_heavy = [0]
        peak_cheap = [0]

        def is_heavy(index):
            return checks[index].cost > heavy_mod.CHEAP_CHECK_SECONDS

        def on_start(index):
            with condition:
                running.add(index)
                started.append(index)
                heavy_now = sum(1 for one in running if is_heavy(one))
                peak_heavy[0] = max(peak_heavy[0], heavy_now)
                peak_cheap[0] = max(peak_cheap[0], len(running) - heavy_now)
                condition.notify_all()

        def on_complete(index):
            with condition:
                running.discard(index)
                completed.append(index)
                condition.notify_all()

        def run_one(index):
            release.wait(10)

        def drive():
            heavy_mod.run_checks(checks, jobs, run_one, on_start=on_start,
                                 on_complete=on_complete, heavy_jobs=cap)

        worker = threading.Thread(target=drive)
        worker.start()
        # The scheduler fills what it can in one scan and then waits for a completion;
        # every started check blocks, so the running set is stable at the cap.
        expected = 4 + min(cap, 4)
        with condition:
            deadline = time.monotonic() + 10
            while len(running) < expected and time.monotonic() < deadline:
                condition.wait(0.1)
            observed_heavy = sum(1 for one in running if is_heavy(one))
            observed_cheap = len(running) - observed_heavy
        release.set()
        worker.join(10)
        assert not worker.is_alive(), "the scheduler did not finish after the release"
        return {"started": set(started), "completed": set(completed),
                "heavy": observed_heavy, "cheap": observed_cheap,
                "peak_heavy": peak_heavy[0], "peak_cheap": peak_cheap[0]}

    planned = set(range(8))
    for cap in (1, 2, 3, 4, 8):
        result = run_at(cap)
        # The planned and executed sets are identical at every cap: the cap changes
        # admission, never selection.
        assert result["started"] == planned, (cap, result["started"])
        assert result["completed"] == planned, (cap, result["completed"])
        # The heavy budget binds: at most `cap` heavy checks run at once, and the cheap
        # checks are not limited by it.
        assert result["heavy"] == min(cap, 4), (cap, result["heavy"])
        assert result["cheap"] == 4, (cap, result["cheap"])
        assert result["peak_heavy"] <= min(cap, 4), (cap, result["peak_heavy"])

    # A cap at or above the pool is the old flat admission: every heavy check starts.
    flat = run_at(8)
    assert flat["heavy"] == 4, flat

    # The default is the whole pool, so a caller that does not name a cap keeps the old
    # behaviour.
    default_checks = heavy_batch()
    default_seen: list[int] = []
    heavy_mod.run_checks(default_checks, 8, lambda index: default_seen.append(index))
    assert set(default_seen) == planned, default_seen

    # The cheap budget is the whole pool, not the heavy cap: four cheap checks run at once
    # under a heavy cap of one.
    cheap_only = [heavy_mod.Check("t", f"cheap {n}", [], "", 0, cost=0.0)
                  for n in range(4)]
    cheap_release = threading.Event()
    cheap_condition = threading.Condition()
    cheap_running: set[int] = set()

    def cheap_start(index):
        with cheap_condition:
            cheap_running.add(index)
            cheap_condition.notify_all()

    def cheap_run(index):
        cheap_release.wait(10)

    def cheap_drive():
        heavy_mod.run_checks(cheap_only, 4, cheap_run, on_start=cheap_start,
                             heavy_jobs=1)

    cheap_worker = threading.Thread(target=cheap_drive)
    cheap_worker.start()
    with cheap_condition:
        deadline = time.monotonic() + 10
        while len(cheap_running) < 4 and time.monotonic() < deadline:
            cheap_condition.wait(0.1)
        cheap_observed = len(cheap_running)
    cheap_release.set()
    cheap_worker.join(10)
    assert cheap_observed == 4, cheap_observed

    # A cap below one is refused rather than run unbounded.
    try:
        heavy_mod.run_checks(heavy_batch(), 8, lambda index: None, heavy_jobs=0)
    except ValueError:
        pass
    else:
        raise AssertionError("heavy_jobs=0 must be refused rather than run unbounded")

# ── 58. THE EXECUTED SET IS PINNED FOR REPRESENTATIVE CHANGES ───────────────────
# Board item 403. The gate's routing is the one thing every other check depends on: a
# change that quietly widens it pays for suites the diff did not reach, and one that
# narrows it lands work nothing examined. The five representative combinations below are
# the design's change classes -- board-only, driver-test-only, driver-behavior, one fork's
# series, and a pin bump -- and each asserts the EXACT multiset of suites that execute, so
# a routing change that adds or drops a step fails here rather than in a landing. A
# repeated kind is a separate suite (a check), which is why the expected lists keep the
# duplicates: three fork-quality steps are a prepare and two readers, and two
# patch-branch-inv2 steps are two forks.
#
# The pin and fingerprint state is part of the combination: a first run records every
# followed upstream where the last check left it, so the diff alone decides. No verdict
# cache exists in this fixture, so every planned check executes; case 58b pins the one way
# a planned check does not.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr

    spec = importlib.util.spec_from_file_location("fork_check_executed", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    state = module.Record(module.record_log_path(repo / ".git"))

    def executed(plan):
        """The suites that execute: planned, not stopped, not a reused verdict."""
        return sorted(check.kind for check in plan.checks
                      if check.not_run_reason is None and not check.cached)

    representative = {
        "driver-test-only": (
            ["tests/pi-forks.test.sh"],
            ["file-size", "pi-forks"],
        ),
        "driver-behavior": (
            ["bin/pi-forks"],
            ["file-size", "fork-quality", "fork-quality",
             "fork-quality", "patch-branch-inv2", "patch-branch-inv2", "pi-forks",
             "series"],
        ),
        "one-fork-series": (
            ["yolo-packs/matt/patches/pi-subagents/0001-x.patch"],
            ["build", "file-size", "fork-quality", "fork-quality",
             "fork-quality", "forks-check", "patch-branch-inv2", "patch-branch-inv3",
             "pi-forks", "rank-order", "series", "storybook"],
        ),
    }
    for name, (changed, expected) in representative.items():
        plan = module.build_plan(repo, module.read_series(repo), state, changed, None, True)
        assert executed(plan) == expected, (name, executed(plan))

    # A pin bump is a change to a fork's `source` ref in its pack's declaration. It is a
    # declaration change, not a series change: the fork is carried for its merge question
    # and its quality scripts, and no replay, verify or build is planned, because the
    # series' own content did not move. The pack's declaration is also an input to the
    # pack's render, residue and config steps, so those execute too.
    manifest = repo / "yolo-packs" / "matt" / "pack.json"
    data = json.loads(manifest.read_text())
    pin = sh(releases, "git", "rev-parse", "HEAD").stdout.strip()
    for contribution in data["contributes"]:
        if contribution.get("patches") == "patches/pi-subagents":
            contribution["source"] = contribution["source"].replace("?ref=main", f"?ref={pin}")
    manifest.write_text(json.dumps(data, indent=2) + "\n")
    commit(repo, "a pin bump")
    plan = module.build_plan(repo, module.read_series(repo), state,
                             ["yolo-packs/matt/pack.json"], None, True)
    assert executed(plan) == [
        "extension-residue", "file-size",
        "fork-quality", "fork-quality", "fork-quality", "matt-render",
        "patch-branch-inv2", "patch-branch-inv2", "pi-forks", "series",
        "yolo-config",
    ], executed(plan)

# 58b. A CARRIED CHECK WITH A CACHE HIT IS NOT EXECUTED. A driver change carries every
#      fork, so its quality scripts are planned; a green verdict for the same tree,
#      command and runtime is reused, and the reused check is not an execution. The
#      prepare step of the same kind still executes, so the exclusion is the CHECK's, not
#      the kind's -- which is why the assertion is at check level.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, releases, heads = build_repo(base)
    env = build_stubs(base)
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr

    spec = importlib.util.spec_from_file_location("fork_check_cached", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    state = module.Record(module.record_log_path(repo / ".git"))
    plan = module.build_plan(repo, module.read_series(repo), state, ["bin/pi-forks"], None, True)
    quality = [check for check in plan.checks if check.kind == "fork-quality"]
    assert len(quality) == 3, [check.label for check in quality]

    candidate = repo / ".forks" / "candidate" / "pi-subagents"
    candidate.mkdir(parents=True)
    git_init(candidate, "--initial-branch=main")
    (candidate / "src").mkdir()
    (candidate / "src" / "index.ts").write_text("export const x = 1;\n")
    commit(candidate, "candidate")

    runtime = module.RuntimeFingerprint(node="v24.12.0", npm="10.9.0")
    tree = module.materialized_fingerprint(str(candidate))
    cache = module.VerdictCache(base / "verdicts.json")
    for check in quality:
        if check.cache_quality:
            key = module.verdict_key(tree, check.cache_quality, check.cache_install,
                                     runtime.entry())
            cache.record(key, {"tree": tree, "command": list(check.cache_quality),
                               "install": check.cache_install, "runtime": runtime.entry(),
                               "commit": "abc"})
    for check in quality:
        module.execute_attempt(repo, check, None, cache=cache, runtime=runtime)

    cached = [check for check in quality if check.cached]
    ran = [check for check in quality
           if check.not_run_reason is None and not check.cached]
    assert len(cached) == 2, [check.label for check in cached]
    assert all(check.rc == 0 for check in cached), [check.rc for check in cached]
    assert len(ran) == 1, [check.label for check in ran]
    assert ran[0].cache_quality is None, ran[0].label
    assert "prepare" in ran[0].label, ran[0].label
    # The kind is still executed by its prepare step: the cache removed the two readers,
    # not the kind.
    assert {check.kind for check in ran} == {"fork-quality"}

_shard_boundary("landing-tiers-and-shards", 24)

# 59. THE TIER FLAG AND THE OWED STATE (docs/design/landing-velocity-plan.md, S2). The
#     tier is a CALLER REQUEST: no flag is the full gate, byte for byte in behavior, and
#     that is what the merge queue runs. `--tier landing` keeps the pre-publish kinds and
#     records each rolling kind the diff selected as OWED to the rolling tier -- the third
#     state beside ran and not-planned (invariant I5), so a landing's coverage contract can
#     tell deferred proof from a step nothing selected. `--tier rolling` plans the deferred
#     kinds. A landing-tier verified run does not advance the recorded baseline; a full or
#     rolling run may.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    spec = importlib.util.spec_from_file_location("fork_check_tier", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    # A `go` on PATH that answers without building: these cases are the plan, not a build.
    go_stub = base / "go-stub"
    go_stub.mkdir()
    (go_stub / "go").write_text("#!/bin/sh\nexit 0\n")
    (go_stub / "go").chmod(0o755)
    env = {**env, "PATH": f"{go_stub}:{env['PATH']}"}

    # 59a. The tier filter itself, on hand-built plans: the full gate is untouched, the
    #      landing tier owes every rolling kind the diff selected, and a rolling kind the
    #      diff did NOT select stays not-planned rather than becoming debt.
    def tier_plan():
        plan = module.Plan()
        plan.checks = [
            module.Check("go-substrate", "a build", ["true"], "why", 1),
            module.Check("go-tool", "a suite", ["true"], "why", 1),
            module.Check("file-size", "the shape", ["true"], "why", 1),
        ]
        plan.skips = [module.Skip("go-tool (tools/tillr-land)", "no path under tools/tillr-land/ changed",
                                  planned=False, kind="go-tool")]
        return plan

    full = tier_plan()
    module.apply_tier(full, None)
    assert full.tier is None, full.tier
    assert [check.kind for check in full.checks] == ["go-substrate", "go-tool", "file-size"], \
        [check.kind for check in full.checks]
    assert not any(skip.owed for skip in full.skips), full.skips

    landing = tier_plan()
    module.apply_tier(landing, module.TIER_LANDING)
    assert [check.kind for check in landing.checks] == ["go-substrate", "file-size"], \
        [check.kind for check in landing.checks]
    owed = [skip for skip in landing.skips if skip.owed]
    assert [skip.kind for skip in owed] == ["go-tool"], \
        [(skip.kind, skip.owed) for skip in landing.skips]
    # Owed is not `planned`: a landing must not read deferred proof as unverified, and the
    # plan-coverage contract's `required` set must not name it.
    assert owed[0].planned is False, owed[0].planned
    planned, required = module.plan_kinds(landing)
    assert "go-tool" not in required, required
    # The go-tool step nothing selected stays not-planned, not owed.
    assert not any(skip.owed for skip in landing.skips
                   if skip.label == "go-tool (tools/tillr-land)"), landing.skips

    # A rolling kind the diff selected that this machine could not answer is owed too: the
    # landing tier defers it whatever the machine's answer would have been.
    unanswered = tier_plan()
    unanswered.checks = [check for check in unanswered.checks if check.kind != "go-tool"]
    unanswered.skips = [module.Skip("go-tool (tools/tillr-land)", "go is unavailable here",
                                    planned=True, kind="go-tool")]
    module.apply_tier(unanswered, module.TIER_LANDING)
    assert [skip.owed for skip in unanswered.skips] == [True], unanswered.skips
    assert unanswered.skips[0].planned is False, unanswered.skips[0].planned

    # The rolling tier is the after-merge proof: it judges the landing kinds the range
    # selected as well as its own, because the pointer it moves says every check guarding
    # the trunk passed there. A commit that reached the trunk without the lander carries no
    # landing verdict; on 2026-11-18 a red `tillr-docs` was left out as "the landing tier's
    # business" for five green advances. Only a landing step whose verdict reads the
    # upstream a launch would build (not reusable, and not a fast tree declaration) stays
    # the landing tier's business.
    rolling = tier_plan()
    rolling.checks.append(module.Check("series", "a series", ["true"], "why", 1, reusable=False))
    fast_tree = module.Check("scratch", "a tree declaration", ["true"], "why", 1, reusable=False)
    fast_tree.fast_tree = True
    rolling.checks.append(fast_tree)
    module.apply_tier(rolling, module.TIER_ROLLING)
    assert [check.kind for check in rolling.checks] == ["go-substrate", "go-tool", "file-size",
                                                        "scratch"], \
        [check.kind for check in rolling.checks]
    assert not any(skip.owed for skip in rolling.skips), rolling.skips
    # The go-tool step nothing selected stays not-planned; the upstream reader is named as
    # the landing tier's business, and nothing else is.
    assert {skip.kind for skip in rolling.skips} == {"go-tool", "series"}, \
        [skip.kind for skip in rolling.skips]
    assert any(skip.kind == "series" and "reads the upstream" in skip.why
               for skip in rolling.skips), rolling.skips
    # A landing kind the range selected that this machine could not answer stays a planned
    # skip under the proof: unavailable, never someone else's business.
    unanswered_rolling = tier_plan()
    unanswered_rolling.skips = [module.Skip("tillr-docs (python3 tests/tillr-docs.test.py)",
                                            "go is unavailable here", planned=True,
                                            kind="tillr-docs")]
    module.apply_tier(unanswered_rolling, module.TIER_ROLLING)
    assert [(skip.planned, skip.owed) for skip in unanswered_rolling.skips] == [(True, False)], \
        unanswered_rolling.skips

    # Declared reads further constrain a lane verdict, not the rolling proof. Fork
    # steps can declare tracked selection paths separately from their checkout watches.
    for changed, expected in ((["README.md"], ["file-size"]),
                              (["tools/tillr/main.go"], ["go-substrate", "file-size"]),
                              (["yolo-packs/fork-pins/pi"], ["file-size", "forks-check"])):
        scoped = tier_plan()
        scoped.checks[0].watches = ("tools/tillr/",)
        scoped.checks.append(module.Check("forks-check", "fork replay", ["true"], "why", 1,
                                         watches=(".forks/pi/",),
                                         selected_by=("yolo-packs/fork-pins/pi",)))
        module.apply_tier(scoped, module.TIER_LANDING, changed=changed)
        assert [check.kind for check in scoped.checks] == expected, scoped.checks
        skipped = [skip for skip in scoped.skips if skip.why == "skipped: no changed path is read"]
        assert {skip.kind for skip in skipped} == {"go-substrate", "forks-check"} - set(expected), skipped

    # 59b. The plan line names the tier, the owed kinds, and the landing-wall model. The
    #      model is `s1_floor` over the landing-tier checks, so a full run prices what the
    #      landing tier would have cost from the durations it just measured.
    def plan_line(plan):
        printed = io.StringIO()
        with contextlib.redirect_stdout(printed):
            module.print_plan(module.Record(base / "tier-state.json"), plan, "base", "head")
        return dict(field.split("=", 1) for field in printed.getvalue().split()[2:])

    full_fields = plan_line(full)
    assert full_fields["tier"] == "full", full_fields
    assert full_fields["owed"] == "-", full_fields
    landing_fields = plan_line(landing)
    assert landing_fields["tier"] == "landing", landing_fields
    assert landing_fields["owed"] == "go-tool", landing_fields
    assert "go-tool" not in landing_fields["checks"].split(","), landing_fields
    assert "go-substrate" in landing_fields["checks"].split(","), landing_fields
    # The model is over the landing subset, so it is at most the run's own cost.
    assert float(landing_fields["landing"]) <= float(landing_fields["seconds"]) + 0.1, \
        landing_fields

    # 59c. The selection line carries the owed state per skip, and only when it is owed:
    #      a full-gate run's selection line is byte-identical to the one it printed before
    #      the tier existed.
    def selection(plan):
        printed = io.StringIO()
        with contextlib.redirect_stdout(printed):
            module.print_selection(plan, ["tools/tillr-land/cmd/mq/main.go"])
        return json.loads(printed.getvalue()[len(module.SELECTION_PREFIX):])

    assert all("owed" not in skip for skip in selection(full)["skips"]), selection(full)["skips"]
    owed_entries = [skip for skip in selection(landing)["skips"] if skip.get("owed")]
    assert [skip["kind"] for skip in owed_entries] == ["go-tool"], \
        selection(landing)["skips"]

    # 59d. End to end on a Go tool directory's diff: the landing tier would run the build
    #      and not the suite, and the suite is named as owed rather than skipped. The
    #      specimen is `tools/tillr-land`, a registered Go tool directory; the retired board tool left
    #      GO_TOOL_DIRS when the board was retired, so it plans no Go step to defer.
    assert run(repo, env).returncode == 0
    (repo / "tools" / "tillr-land").mkdir(exist_ok=True)
    (repo / "tools" / "tillr-land" / "queue.go").write_text("package main\n")
    commit(repo, "change the lander")

    result = run(repo, env, "--tier", "landing", "--explain")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "would run  go-build" not in result.stdout, result.stdout
    assert "would run  go-tool" not in result.stdout, result.stdout
    assert "would owe" in result.stdout, result.stdout
    assert "tools/tillr-land's whole suite passes" in result.stdout, result.stdout
    explained = json.loads([line for line in result.stdout.splitlines()
                            if line.startswith(module.SELECTION_PREFIX)][-1]
                           [len(module.SELECTION_PREFIX):])
    # The Go suite is the only rolling kind that diff selects, so it is owed alone: the
    # landing tier defers it and runs the build.
    assert [skip["kind"] for skip in explained["skips"] if skip.get("owed")] == \
        ["go-tool"], explained["skips"]

    # 59e. The rolling tier plans the suite and not the build, and a gate-code diff plans
    #      no self-suite there: gate code never proves itself late (invariant I3).
    result = run(repo, env, "--tier", "rolling", "--explain")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "would run  go-tool" in result.stdout, result.stdout
    assert "would run  go-build" not in result.stdout, result.stdout
    assert "would owe" not in result.stdout, result.stdout

    # 59f. A landing-tier verified run does not advance the recorded baseline: the next
    #      full run must still plan the rolling kinds this run deferred.
    before = record(repo)["baseline"]["commit"]
    result = run(repo, env, "--tier", "landing")
    assert result.returncode == 0, result.stdout + result.stderr
    assert record(repo)["baseline"]["commit"] == before, record(repo)["baseline"]
    fields = dict(field.split("=", 1) for field in
                  [line for line in result.stdout.splitlines() if line.strip()][-1].split()[2:])
    assert fields["tier"] == "landing", fields
    assert fields["owed"] == "go-tool", fields
    assert "go-tool" not in fields["checks"].split(","), fields
    assert "go-substrate" not in fields["checks"].split(","), fields

    # 59g. A full run still advances the baseline, and its plan line names the full tier,
    #      no owed kinds, and a landing-wall model no larger than the run itself.
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert record(repo)["baseline"]["commit"] == \
        sh(repo, "git", "rev-parse", "HEAD").stdout.strip(), record(repo)["baseline"]
    fields = dict(field.split("=", 1) for field in
                  [line for line in result.stdout.splitlines() if line.strip()][-1].split()[2:])
    assert fields["tier"] == "full", fields
    assert fields["owed"] == "-", fields
    assert "go-tool" in fields["checks"].split(","), fields
    assert float(fields["landing"]) <= float(fields["seconds"]) + 0.1, fields

    # The lane adapter passes this exact range. A docs-only candidate records Go as
    # skipped, not passed, and names the unconditional check that actually ran.
    docs_base = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "README.md").write_text("# Docs only\n")
    commit(repo, "docs only")
    spans = base / "doc-spans.json"
    result = run(repo, env, "--tier", "landing", "--since", docs_base, "--spans-out", str(spans))
    assert result.returncode == 0, result.stdout + result.stderr
    assert "checks with no declared reads (always run): file-size" in result.stdout, result.stdout
    observations = json.loads(spans.read_text())
    assert [check["kind"] for check in observations["check_spans"]] == ["file-size"], observations
    assert any("go-tool (tools/tillr-land)" in skip["label"] and
               skip["reason"] == "skipped: no changed path is read" and not skip["planned"]
               for skip in observations["skipped_checks"]), observations

    # A first rolling audit asks --all even when the gate has already verified this tip.
    # The remembered baseline must not turn the Go suite into a no-change skip.
    tool = repo / "tools/tillr-drain"
    tool.mkdir(parents=True, exist_ok=True)
    (tool / "audit.go").write_text("package main\n")
    # The rolling tier now judges the landing kinds too (59a), so the fixture carries a
    # gate audit suite for its self-suite step to run.
    (repo / "tests").mkdir(exist_ok=True)
    (repo / "tests" / "fork-check.test.py").write_text("")
    commit(repo, "seed the drain suite for the first rolling audit")
    state = record(repo)
    baseline = {**state["baseline"],
                "commit": sh(repo, "git", "rev-parse", "HEAD").stdout.strip(),
                "tree": sh(repo, "git", "rev-parse", "HEAD^{tree}").stdout.strip()}
    state["baseline"] = baseline
    state["baselines"] = {sh(repo, "git", "symbolic-ref", "HEAD").stdout.strip(): baseline}
    write_record(repo, state)
    assert record(repo)["baseline"]["commit"] == \
        sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    spans_path = base / "first-rolling-spans.json"
    result = run(repo, env, "--tier", "rolling", "--all", "--spans-out", str(spans_path))
    assert result.returncode == 0, result.stdout + result.stderr
    spans = json.loads(spans_path.read_text())
    rolling_suites = [span for span in spans["check_spans"] if span["kind"] == "go-tool"]
    assert len(rolling_suites) == 2, spans
    assert any("tillr-drain" in span["label"] for span in rolling_suites), spans
    assert not any(module.CHECK_TIER.get(skip["kind"]) == module.TIER_ROLLING and "no " in skip["reason"]
                   and "changed" in skip["reason"] for skip in spans["skipped_checks"]), spans

    # 59h. Gate code never proves itself late (invariant I3). A gate-code diff plans the
    #      audit suite in the landing tier, so the landing never owes it. The rolling tier
    #      is the after-merge proof and judges it again on the trunk (59a): that is a
    #      re-proof, never a deferral, so it is never owed either.
    gate_state = module.Record(base / "tier-gate-state.json")
    gate_landing = module.build_plan(
        repo, module.read_series(repo), gate_state, ["tools/fork-check.py"], None, True,
        tier=module.TIER_LANDING)
    assert "self-suite" in [check.kind for check in gate_landing.checks], \
        [check.kind for check in gate_landing.checks]
    gate_rolling = module.build_plan(
        repo, module.read_series(repo), gate_state, ["tools/fork-check.py"], None, True,
        tier=module.TIER_ROLLING)
    assert "self-suite" in [check.kind for check in gate_rolling.checks], \
        [check.kind for check in gate_rolling.checks]
    assert "self-suite" not in [skip.kind for skip in gate_landing.skips if skip.owed], \
        [(skip.kind, skip.owed) for skip in gate_landing.skips]
    assert "self-suite" not in [skip.kind for skip in gate_rolling.skips if skip.owed], \
        [(skip.kind, skip.owed) for skip in gate_rolling.skips]

# 60. THE GATE'S PER-CHECK REPORT LINE KEEPS ITS SHAPE. It is what a human reads in
#     `just done`'s output, and it is the one part of the retired
#     tests/land-check-patterns.test.py that outlived `bin/land`: the shim's two
#     `--gate-check-*` patterns went with the merge queue's reader, and the line they
#     scraped did not. Every per-check line a green run prints is
#     `<status> <kind> -- <label> (<seconds>s)`, with the status one of the words
#     check_status answers -- so a new status word, or a shape that moved, is a red here
#     rather than in a reader that is already parsing it.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    result = run(repo, env)
    assert result.returncode == 0, result.stdout + result.stderr
    report_line = re.compile(r"^fork-check: (ok|FAIL|ERR|REFUSED|skip|not-run|CACHED|"
                             r"QUARANTINED|STALE) (\S+) -- (.+) \(\d+\.\d+s\)$")
    # The progress lines go to stderr and the plan to stdout, so both streams are read.
    progress = (result.stdout + result.stderr).splitlines()
    # Each planned check's own line, found by its kind and label: the run's summary lines
    # carry a timing too, so a scan over every line with one would be pinning the summary.
    selection_line = "fork-check: selection "
    selection = json.loads([line for line in result.stdout.splitlines()
                            if line.startswith(selection_line)][-1][len(selection_line):])
    assert selection["checks"], selection
    for check in selection["checks"]:
        wanted = f"{check['kind']} -- {check['label']} ("
        lines = [line for line in progress if wanted in line]
        assert len(lines) == 1, (wanted, progress)
        match = report_line.match(lines[0])
        assert match, lines[0]
        # The kind is one word and the label is the rest of the line before the timing, so
        # a label holding " -- " still reads whole.
        assert match.group(3), lines[0]
    # The plan line is NOT a check: it is the run's own summary, and it has its own shape.
    plan_lines = [line for line in result.stdout.splitlines()
                  if line.startswith("fork-check: plan ")]
    assert len(plan_lines) == 1, result.stdout

    # Diagnostics have no tier option, but --all must still plan the full gate.
    preflight = sh(repo, sys.executable, str(repo / "tools/fork-check.py"),
                   "--preflight", "--all", "--only", "file-size", "--explain", check=False, env=env)
    assert preflight.returncode == 0, preflight.stdout + preflight.stderr
    replay_green = run(repo, env, "--replay", "--only", "file-size")
    assert replay_green.returncode == 0, replay_green.stdout + replay_green.stderr
    assert "named check(s) green" in replay_green.stdout, replay_green.stdout

    # ── the base replay ─────────────────────────────────────────────────────
    #
    # A red gate is a suspect until the same checks are replayed on the base the candidate
    # was staged from. `--replay` runs only the checks `--only` names, writes no record
    # and advances no baseline, and answers 0 (all green), 1 (any red) or 2 (could not
    # answer). The 2 is the load-bearing one: a driver that read an unmatched or
    # unplannable replay as green would acquit a candidate nobody cleared.
    replay = run(repo, env, "--replay", "--only", "no-such-check")
    assert replay.returncode == 2, replay.stdout + replay.stderr
    assert "no planned check matches" in replay.stderr, replay.stderr

# 60a. A REPLAY PLANS THE CHECK IT NAMES, WHATEVER THE RECORD REMEMBERS. The heavy runner
#      attributes a red by replaying that one check on the green pointer, and the pointer
#      is a commit some earlier run already verified. The replay used to plan from the
#      record's baseline like a landing does, so on a verified commit it planned only the
#      checks no path selects: on 2026-11-18 attribution of the tillr-view suite at a
#      verified green pointer answered "no planned check matches --only" (exit 2), the
#      runner read that as "error, not a verdict", and the red got no fixer. A
#      path-selected check (Waybar's) replayed on a verified commit must run and answer.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    waybar_fixture(repo)
    commit(repo, "waybar inputs")
    verified = run(repo, env)
    assert verified.returncode == 0, verified.stdout + verified.stderr
    replay = run(repo, env, "--replay", "--only", "waybar")
    assert replay.returncode == 0, replay.stdout + replay.stderr
    assert "PASS waybar" in replay.stdout, replay.stdout
    # The same check red on a verified commit is a red, not "could not answer": a
    # replay that cannot see the check would acquit or misattribute by omission.
    (repo / "tests/waybar-ai-modules.test.sh").write_text("#!/bin/sh\necho FAIL fixture\nexit 1\n")
    commit(repo, "break the waybar test")
    red_replay = run(repo, env, "--replay", "--only", "waybar")
    assert red_replay.returncode == 1, red_replay.stdout + red_replay.stderr
    assert "FAIL waybar" in red_replay.stdout, red_replay.stdout

# 60b. THE SHARD RUNNER'S OWN REGRESSION SUITE LANDS WHEN THE RUNNER OR ITS SUITE CHANGES.
#      At 7:13 AM ET on 2026-11-22 a commit touching only `tests/go-test-shards.test.py`
#      (df41a37b07) passed the landing gate in 2s with `ran=1 notrun=55 ... checks=file-size
#      ... owed=-` because `tools/gate/checks/go-test-shards-test.json` is `scope: "tree"`,
#      `tier: "heavy"` (excluded from `tillr-gate checks --fast-tree --scope-to-change`) and
#      no check in `tools/fork-check.py` watched `tests/go-test-shards.test.py` or ran that
#      suite when `tools/go-test-shards.py` changed (9ac76b74e1). Touching either file must
#      select and execute `tests/go-test-shards.test.py` at the landing tier, and touching
#      neither file must leave it unplanned.
with tempfile.TemporaryDirectory() as tmp:
    base = Path(tmp)
    repo, _, _ = build_repo(base)
    env = build_stubs(base)
    go_stub = base / "go-stub"
    go_stub.mkdir()
    (go_stub / "go").write_text("#!/bin/sh\nexit 0\n")
    (go_stub / "go").chmod(0o755)
    env = {**env, "PATH": f"{go_stub}:{env['PATH']}"}
    spec = importlib.util.spec_from_file_location("fork_check_shards_suite", SCRIPT)
    shards_mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(shards_mod)
    assert shards_mod.CHECK_KIND_GROUP.get("go-test-shards-suite") == shards_mod.KIND_VERDICT
    assert shards_mod.CHECK_TIER.get("go-test-shards-suite") == shards_mod.TIER_LANDING
    assert "go-test-shards-suite" in shards_mod.CHECK_COST_ESTIMATE

    (repo / "tools").mkdir(exist_ok=True)
    (repo / "tools" / "go-test-shards.py").write_text("#!/usr/bin/env python3\n")
    (repo / "tools" / "go-test-durations.json").write_text("{}\n")
    # Editing tools/go-test-shards.py also selects gate-runner at the landing tier, so
    # give the synthetic repo gate-runner's two companion test stubs.
    (repo / "tools" / "gate").mkdir(parents=True, exist_ok=True)
    (repo / "tools" / "gate" / "change-gate.test.py").write_text("")
    (repo / "tools" / "gate" / "runner-first.test.py").write_text("")
    (repo / "tests").mkdir(exist_ok=True)
    suite_calls = base / "go-test-shards-suite-calls"
    (repo / "tests" / "go-test-shards.test.py").write_text(
        "from pathlib import Path\n"
        f"Path({str(suite_calls)!r}).open('a').write('ran\\n')\n"
    )
    commit(repo, "seed go-test-shards runner and suite")

    for trigger in ("tests/go-test-shards.test.py", "tools/go-test-shards.py"):
        planned = shards_mod.build_plan(
            repo, [], shards_mod.Record(base / "shards-suite-state.json"),
            [trigger], None, True, tier=shards_mod.TIER_LANDING,
        )
        matched = [c for c in planned.checks if c.kind == "go-test-shards-suite"]
        assert len(matched) == 1, (trigger, [(c.kind, c.label) for c in planned.checks])
        assert matched[0].argv == [sys.executable, "tests/go-test-shards.test.py"], matched[0].argv
        assert set(matched[0].watches) == {
            "tools/go-test-shards.py",
            "tests/go-test-shards.test.py",
        }, matched[0].watches

        # The heavy tree declaration go-test-shards-test already runs in JudgeHeavy, so
        # the rolling tier does not duplicate the suite.
        rolling_planned = shards_mod.build_plan(
            repo, [], shards_mod.Record(base / "shards-suite-rolling.json"),
            [trigger], None, True, tier=shards_mod.TIER_ROLLING,
        )
        assert not any(c.kind == "go-test-shards-suite" for c in rolling_planned.checks), (
            trigger, [c.kind for c in rolling_planned.checks]
        )

    for unrelated in ("README.md", "tools/tillr-land/main.go", "tools/go-test-durations.json"):
        planned = shards_mod.build_plan(
            repo, [], shards_mod.Record(base / "shards-suite-unrelated.json"),
            [unrelated], None, True, tier=shards_mod.TIER_LANDING,
        )
        assert not any(c.kind == "go-test-shards-suite" for c in planned.checks), (
            unrelated, [c.kind for c in planned.checks]
        )
        assert not any(s.kind == "go-test-shards-suite" and s.owed for s in planned.skips), (
            unrelated, planned.skips
        )

    # End-to-end landing runs: each watched file executes the suite, an unrelated edit does
    # not, and a failing suite fails the landing gate.
    for path, content in (
        ("tests/go-test-shards.test.py",
         "from pathlib import Path\n"
         f"Path({str(suite_calls)!r}).open('a').write('suite-edit\\n')\n"),
        ("tools/go-test-shards.py", "#!/usr/bin/env python3\n# runner edit\n"),
    ):
        suite_calls.unlink(missing_ok=True)
        prev = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
        (repo / path).write_text(content)
        commit(repo, f"edit {path}")
        res = run(repo, env, "--tier", "landing", "--since", prev)
        assert res.returncode == 0, res.stdout + res.stderr
        assert "go-test-shards-suite" in res.stdout, res.stdout
        assert suite_calls.exists() and suite_calls.read_text().strip(), path

    suite_calls.unlink(missing_ok=True)
    prev = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "README.md").write_text("# Unrelated docs edit\n")
    commit(repo, "unrelated docs edit")
    unrelated_res = run(repo, env, "--tier", "landing", "--since", prev)
    assert unrelated_res.returncode == 0, unrelated_res.stdout + unrelated_res.stderr
    assert not suite_calls.exists(), suite_calls.read_text()
    fields = dict(field.split("=", 1) for field in
                  [line for line in unrelated_res.stdout.splitlines() if line.strip()][-1].split()[2:])
    assert "go-test-shards-suite" not in fields["checks"].split(","), fields

    prev = sh(repo, "git", "rev-parse", "HEAD").stdout.strip()
    (repo / "tests" / "go-test-shards.test.py").write_text("import sys\nsys.exit(1)\n")
    commit(repo, "break go-test-shards suite")
    red_res = run(repo, env, "--tier", "landing", "--since", prev)
    assert red_res.returncode == 1, red_res.stdout + red_res.stderr
    assert "FAIL go-test-shards-suite" in (red_res.stdout + red_res.stderr), (
        red_res.stdout + red_res.stderr
    )

_shard_boundary("watchdog-and-subsuites", 24)

# 66. Wrapper targets never kill progress; only complete silent samples stall.
# The fake child supplies observations, not elapsed-time assertions.
from unittest.mock import patch
import types
spec = importlib.util.spec_from_file_location("fork_check_watchdog", SCRIPT)
watch = importlib.util.module_from_spec(spec)
spec.loader.exec_module(watch)
assert watch.GO_TEST_TIMEOUT == "0", "Go's nested test deadline still kills working suites"
go_watch = (ROOT / "tools/tillr-gate/watchdog.go").read_text()
assert f"const WatchdogSampleInterval = {int(watch.WATCHDOG_SAMPLE_INTERVAL * 1000)} * time.Millisecond" in go_watch
assert f"const WatchdogSilentSamples = {watch.WATCHDOG_SILENT_SAMPLES}" in go_watch

class SampledChild:
    pid = 12345
    returncode = 0
    def __init__(self, mode):
        self.n = 0
        self.mode = mode
    def communicate(self, timeout=None):
        self.n += 1
        if self.n <= 70 or self.mode == "silent":
            output = b"last line\n" + (b"x" * self.n if self.mode == "output" else b"")
            raise subprocess.TimeoutExpired("stub", timeout, output=output)
        return b"finished\n", b""
    def poll(self):
        return None

for mode in ("output", "cpu", "silent", "unavailable"):
    child = SampledChild(mode)
    def sample(pid):
        if mode == "unavailable":
            raise OSError("CPU unavailable")
        return child.n if mode == "cpu" else 12
    def kill(proc):
        proc.mode = "killed"
        proc.n = 71
        proc.returncode = -9
    warnings = []
    with patch.object(watch.subprocess, "Popen", return_value=child), \
         patch.object(watch, "progress", side_effect=warnings.append), \
         patch.object(watch.os, "write"), \
         patch.object(watch, "_kill_process_group", side_effect=kill), \
         patch.object(watch, "_start_cpu_sampler", return_value=(sample, lambda: None)), \
         patch.object(watch, "_start_process_sampler", return_value=(sample, lambda: None)):
        rc, out, err, _ = watch._run_cancellable(ROOT, ["stub"], 0, None, (), watch.Cancellation(), False)
    if mode == "silent":
        assert rc == watch.CHECK_STALLED_RC and "last line" in err and "60000 ms" in err, (rc, err)
    else:
        assert rc == 0, (mode, rc, err)
        if mode == "unavailable":
            assert any("watchdog unavailable" in line for line in warnings), warnings
stalled = watch.Check("documents", "blocked", ["stub"], "test", 1)
stalled.rc = watch.CHECK_STALLED_RC
stalled.output = "stalled: last line"
assert not stalled.conflict
assert watch.verdict_of([stalled]) == ("stalled", watch.EXIT_STALLED)
assert watch.check_status(stalled) == "STALLED"
progressed = watch.Check("documents", "working", ["stub"], "test", 1)
# Inject the measured duration; never compare a real elapsed time to a budget.
with patch.object(watch, "execute_attempt", side_effect=lambda root, check, *args, **kwargs: setattr(check, "seconds", 70)):
    watch.execute_check(progressed, ROOT, None)
assert progressed.rc == 0 and any("over target 1s" in line for line in progressed.warnings)
progressed.rc = watch.EXIT_STALLED  # an ordinary command may itself exit 6
assert progressed.conflict and watch.verdict_of([progressed]) == ("failed", 1)
# The real group is killed even if its leader exited and a child holds the pipe.
# Inject progress until its readiness marker exists; startup speed is never a verdict.
with tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"]) as directory:
    ready = Path(directory) / "ready"
    cpu = 0
    def ready_sample(pid):
        global cpu
        if not ready.exists():
            cpu += 1
        return cpu
    with patch.object(watch, "WATCHDOG_SAMPLE_INTERVAL", 0.001), \
         patch.object(watch, "_start_cpu_sampler", return_value=(ready_sample, lambda: None)):
        rc, out, err, _ = watch._run_cancellable(ROOT, [sys.executable, "-c",
            "import os,signal,sys\nfrom pathlib import Path\npid=os.fork()\nif pid: os._exit(0)\nprint('child holds pipe',flush=True)\nPath(sys.argv[1]).touch()\nsignal.pause()", str(ready)],
            0, None, (), watch.Cancellation(), False)
    assert rc == watch.CHECK_STALLED_RC and "child holds pipe" in err, (rc, out, err)
    # Named observations are exported even when no rolling span file was requested.
    path = Path(directory) / "stall.json"
    nonexecutable = Path(directory) / "not-executable"
    nonexecutable.write_text("#!/bin/sh\nexit 0\n")
    rc, _, _ = watch.run_command(ROOT, [str(nonexecutable)], 1)
    assert rc == 126, "an unexecutable runner became a content verdict"
    stalled.output = out + err
    with patch.dict(watch.os.environ, TILLR_FAST_VERDICT_OUT=str(path)):
        watch.write_gate_spans("", types.SimpleNamespace(checks=[stalled]), ROOT, 0,
                               "stalled", "head", "tree", [], 1, 1, 0)
    observation = json.loads(path.read_text())["payload"]["checks"][0]
    assert observation["check_id"] and observation["last_output_line"] == "child holds pipe", observation
    assert observation["silent_samples"] == watch.WATCHDOG_SILENT_SAMPLES
# Controlled snapshots prove totals, identities and progress below the total's
# high water without relying on a child being scheduled between real samples.
with tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"]) as directory:
    proc_root = Path(directory)
    def proc_member(pid, start, ns, children=0):
        task = proc_root / str(pid) / "task" / str(pid)
        task.mkdir(parents=True, exist_ok=True)
        fields = "S 1 123 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0".split()
        fields[13], fields[19] = str(children), str(start)
        (proc_root / str(pid) / "stat").write_text(f"{pid} (name with ) spaces) " + " ".join(fields))
        (task / "schedstat").write_text(f"{ns} 0 0")
    sampler, close = watch._start_process_sampler(OSError("counter denied"), proc_root)
    proc_member(123, 1, 1000, 20)
    proc_member(999, 1, 0)
    unrelated = proc_root / "999/stat"
    unrelated.write_bytes(unrelated.read_bytes().replace(b"name with ) spaces", b"x\xff").replace(b"S 1 123", b"S 1 456"))
    tick = 1_000_000_000 // os.sysconf("SC_CLK_TCK")
    assert sampler(123) == 1000 + 20*tick and not sampler.cpu_changed
    assert sampler(123) == 1000 + 20*tick and not sampler.cpu_changed
    proc_member(123, 1, 1001, 30)
    assert sampler(123) == 1001 + 30*tick and sampler.cpu_changed
    proc_member(124, 2, 900000000)
    high = sampler(123)
    assert sampler.cpu_changed
    shutil.rmtree(proc_root / "124")
    assert sampler(123) == high and sampler.cpu_changed
    proc_member(123, 1, 1002, 30)
    assert sampler(123) == high and sampler.cpu_changed
    proc_member(123, 99, 1002, 30)
    assert sampler(123) == high and sampler.cpu_changed
    (proc_root / "123/task/125").mkdir()
    (proc_root / "123/task/125/schedstat").write_text("77 0 0")
    assert sampler(123) == high and sampler.cpu_changed
    (proc_root / "123/task/125/schedstat").write_text("invalid")
    try:
        sampler(123)
        assert False, "unreadable runtime silently substituted"
    except OSError as error:
        assert "process accounting" in str(error)
    (proc_root / "123/task/125/schedstat").unlink()
    try:
        sampler(123)
        assert False, "missing runtime on a live task was treated as progress"
    except OSError as error:
        assert "process accounting" in str(error)

# Reaping between child and parent reads must not inflate the lower-bound total.
with tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"]) as directory:
    proc_root = Path(directory)
    def reap_member(pid, children):
        task = proc_root / str(pid) / "task" / str(pid)
        task.mkdir(parents=True, exist_ok=True)
        fields = "S 1 123 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1".split()
        fields[13] = str(children)
        (proc_root / str(pid) / "stat").write_text(f"{pid} (check) " + " ".join(fields))
        (task / "schedstat").write_text("1000000000 0 0")
    reap_member(123, 0)
    reap_member(124, 0)
    original_read, original_iter = Path.read_bytes, Path.iterdir
    reaped = False
    def read_with_reap(path):
        global reaped
        if path == proc_root / "124/stat" and not reaped:
            reaped = True
            reap_member(124, 100)
            shutil.rmtree(proc_root / "123")
        return original_read(path)
    def ordered_iter(path):
        entries = list(original_iter(path))
        return iter(sorted(entries) if path == proc_root else entries)
    sampler, _ = watch._start_process_sampler(OSError("counter denied"), proc_root)
    with patch.object(Path, "read_bytes", read_with_reap), patch.object(Path, "iterdir", ordered_iter):
        total = sampler(123)
    assert total == 2_000_000_000, f"reaped child counted twice: {total}"
    assert sampler.cpu_changed, "disappearance did not count as progress"

# Detached descendants started with setsid (such as go_affected.py's
# --invocation-guard) leave the check's process group; process accounting must
# follow ppid -> children across session boundaries without counting unrelated
# processes.
with tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"]) as directory:
    proc_root = Path(directory)
    def detached_member(pid, ppid, pgrp, start, ns):
        task = proc_root / str(pid) / "task" / str(pid)
        task.mkdir(parents=True, exist_ok=True)
        fields = "S 1 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0".split()
        fields[1], fields[2], fields[19] = str(ppid), str(pgrp), str(start)
        (proc_root / str(pid) / "stat").write_text(f"{pid} (proc) " + " ".join(fields))
        (task / "schedstat").write_text(f"{ns} 0 0")
    detached_member(123, 1, 123, 1, 1000)   # idle check leader
    detached_member(200, 123, 200, 2, 500)  # guard in a new session (pgrp 200 != 123)
    detached_member(201, 200, 200, 3, 2000) # worker child under guard
    detached_member(202, 201, 202, 4, 3000) # nested setsid grandchild
    detached_member(999, 1, 999, 5, 900000) # unrelated process outside the tree
    sampler, _ = watch._start_process_sampler(OSError("counter denied"), proc_root)
    assert sampler(123) == 6500 and not sampler.cpu_changed
    detached_member(999, 1, 999, 5, 999999999)
    assert sampler(123) == 6500 and not sampler.cpu_changed, "unrelated process counted as progress"
    detached_member(201, 200, 200, 3, 52000)
    assert sampler(123) == 56500 and sampler.cpu_changed, "detached worker CPU missed"
    detached_member(202, 201, 202, 4, 103000)
    assert sampler(123) == 156500 and sampler.cpu_changed, "nested setsid grandchild CPU missed"

# Mimic go_affected.py: the check leader blocks silently in wait() while a
# child started with start_new_session=True runs a CPU-burning grandchild.
guarded_code = '''import subprocess, sys
worker = "import time\\nend = time.monotonic() + 1.0\\nwhile time.monotonic() < end: pass\\n"
guard = "import subprocess, sys\\nsys.exit(subprocess.call([sys.executable, '-c', sys.argv[1]]))\\n"
p = subprocess.Popen([sys.executable, "-c", guard, worker], start_new_session=True)
sys.exit(p.wait())
'''
with patch.object(watch, "WATCHDOG_SAMPLE_INTERVAL", .01), \
     patch.object(watch, "_start_cpu_sampler", side_effect=OSError("counter denied")), \
     patch.object(watch, "progress"):
    rc, out, err, _ = watch._run_cancellable(ROOT, [sys.executable, "-c", guarded_code],
                                            0, None, (), watch.Cancellation(), False)
    assert rc == 0, f"active guarded-session check judged stalled: rc={rc} out={out!r} err={err!r}"

# Force the counter refused: read-only accounting must see progress even when
# the command auto-reaps children between watchdog samples.
for mode in ("silent", "cpu", "output", "children", "discarded", "neither"):
    code = '''import os,signal,sys,time
mode = sys.argv[1]
def burn():
    end = time.monotonic() + .04
    while time.monotonic() < end: pass
if mode == 'silent': signal.pause()
if mode == 'neither':
    time.sleep(.8)
    sys.exit(0)
if mode == 'discarded': signal.signal(signal.SIGCHLD, signal.SIG_IGN)
for n in range(35):
    if mode in ('children', 'discarded'):
        pid = os.fork()
        if pid == 0:
            burn()
            os._exit(0)
        if mode == 'children': os.waitpid(pid, 0)
        else: time.sleep(.05)
    elif mode in ('output', 'neither'):
        print(n, flush=True)
        time.sleep(.04)
    else: burn()
'''
    warnings = []
    process_sampler = watch._start_process_sampler
    # Keep the real function outside the patched attribute to avoid recursion.
    real_process_sampler = watch._start_process_sampler
    if mode == "neither":
        process_sampler = lambda error: real_process_sampler(error, ROOT / "nonexistent-proc-test")
    with patch.object(watch, "WATCHDOG_SAMPLE_INTERVAL", .01), \
         patch.object(watch, "_start_cpu_sampler", side_effect=OSError("counter denied")), \
         patch.object(watch, "_start_process_sampler", side_effect=process_sampler), \
         patch.object(watch, "progress", side_effect=warnings.append):
        rc, out, err, _ = watch._run_cancellable(ROOT, [sys.executable, "-c", code, mode],
                                                0, None, (), watch.Cancellation(), False)
    if mode == "silent":
        assert rc == watch.CHECK_STALLED_RC, (mode, rc, out, err, warnings)
        observation = json.loads(err.split("stall observations: ")[1])
        assert observation["silent_samples"] == 60 and observation["cpu_source"] == "process accounting", observation
    else:
        assert rc == 0, (mode, rc, out, err, warnings)
    if mode == "neither":
        assert any("watchdog unavailable" in line and "process accounting" in line for line in warnings), warnings
    else:
        assert any("watchdog CPU source: process accounting" in line for line in warnings), warnings
        assert not any("watchdog unavailable" in line for line in warnings), warnings
# A completed leader does not finish communicate while descendants hold output.
# Force the real fallback rather than injecting perf-shaped sample readings.
for idle in (False, True):
    code = '''import os,signal,time,sys
pid = os.fork()
if pid: os._exit(0)
print('child started', flush=True)
if sys.argv[1] == 'idle': signal.pause()
for n in range(35):
    print(n, flush=True)
    time.sleep(.04)
print('child completed', flush=True)
'''
    with patch.object(watch, "WATCHDOG_SAMPLE_INTERVAL", .01), \
         patch.object(watch, "_start_cpu_sampler", side_effect=OSError("counter denied")), \
         patch.object(watch, "progress"):
        rc, out, err, _ = watch._run_cancellable(ROOT, [sys.executable, "-c", code, "idle" if idle else "active"],
                                                0, None, (), watch.Cancellation(), False)
    if idle:
        assert rc == watch.CHECK_STALLED_RC and "child started" in err, (rc, out, err)
    else:
        assert rc == 0 and "child completed" in out, (rc, out, err)
# Nested watchdogs independently read their own process groups, without counting
# the inner watcher's CPU as progress by its idle child.
nested_code = '''import importlib.util,sys
from pathlib import Path
spec=importlib.util.spec_from_file_location('nested_watch', Path.cwd()/'tools/fork-check.py')
w=importlib.util.module_from_spec(spec); spec.loader.exec_module(w)
def denied(pid): raise OSError('counter denied')
w._start_cpu_sampler=denied
w.WATCHDOG_SAMPLE_INTERVAL=.01
warnings=[]
w.progress=warnings.append
mode=sys.argv[1]
code='import signal; signal.pause()' if mode=='silent' else ''' + repr('import os,time\nfor n in range(35):\n    pid=os.fork()\n    if pid == 0:\n        end=time.monotonic()+.02\n        while time.monotonic()<end: pass\n        os._exit(0)\n    os.waitpid(pid,0)\n    time.sleep(.02)\n') + '''
rc,out,err,_=w._run_cancellable(Path.cwd(),[sys.executable,'-c',code],0,None,(),w.Cancellation(),False)
assert rc==(w.CHECK_STALLED_RC if mode=='silent' else 0),(rc,out,err)
assert not any('watchdog unavailable' in note for note in warnings),warnings
print('nested '+mode+' passed',flush=True)
'''
with patch.object(watch, "_start_cpu_sampler", side_effect=OSError("counter denied")), \
     patch.object(watch, "progress"):
    for mode in ("silent", "children"):
        rc, out, err, _ = watch._run_cancellable(ROOT, [sys.executable, "-c", nested_code, mode],
                                                0, None, (), watch.Cancellation(), False)
        assert rc == 0 and f"nested {mode} passed" in out, (rc, out, err)
print("wrapper watchdog: cadence parity, output/CPU progress, refused-counter fallback, retained children, nested Go/Python accounting, unavailable accounting, group kill and named stall handoff pass")

_shard_boundary("watchdog-go-reader", 45)
# 66b. Exercise the Go reader under the Python watchdog as the real gate does.
# Deterministic shared scratch under GOCACHE lets Go reuse cached results when
# tools/tillr-gate is unchanged instead of re-running 100 Hz /proc loops on every gate.
_watch_go_spec = importlib.util.spec_from_file_location("fork_check_watchdog_go", SCRIPT)
_watch_go = importlib.util.module_from_spec(_watch_go_spec)
_watch_go_spec.loader.exec_module(_watch_go)
with patch.object(_watch_go, "_start_cpu_sampler", side_effect=OSError("counter denied")), \
     patch.object(_watch_go, "progress"):
    _go_scratch = Path(os.environ.get("GOCACHE") or (Path.home() / ".cache" / "go-build")) / "self-suite-go-scratch"
    _go_scratch.mkdir(parents=True, exist_ok=True)
    rc, out, err, _ = _watch_go._run_cancellable(ROOT, ["go", "test", "-C", "tools/tillr-gate", "-trimpath", "./...", "-run",
        "^(TestProcessAccounting|TestDeniedCounter|TestNeitherCPUSource|TestCPUSource)"],
        0, {"TMPDIR": str(_go_scratch), "YOLO_DURABLE_DIR": str(_go_scratch)}, (), _watch_go.Cancellation(), False)
    assert rc == 0, (rc, out, err)

_shard_boundary("runner-and-fast-tree-subsuites", 18)
# 67. The runner exception is part of the legacy judge's permanent audit too.
subprocess.run([sys.executable, str(ROOT / "tools/gate/runner-first.test.py")], check=True)
# 68. Fast tree checks run before fork-check in the landing gate.
subprocess.run([sys.executable, str(ROOT / "tests/fast-tree-in-done.test.py")], check=True)
# 69. A gate refusal whose only failure is a fork pin mismatch says so.
subprocess.run([sys.executable, str(ROOT / "tests/fork-pin-mismatch.test.py")], check=True)
