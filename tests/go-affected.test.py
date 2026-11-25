#!/usr/bin/env python3
"""Tests for tools/gate/go_affected.py, go-test-edges.json, and the go-affected check declaration."""
from __future__ import annotations

import argparse
import atexit
from collections.abc import Callable
import contextlib
import fcntl
import functools
import importlib.util
import json
import os
from pathlib import Path
import re
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import traceback
from unittest.mock import patch
import uuid

ROOT = Path(__file__).resolve().parents[1]


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


go_affected = load_module("go_affected", ROOT / "tools" / "gate" / "go_affected.py")

# How many trailing output lines a failure report keeps when it shows only the end of a
# check's log.
EXCERPT_LINES = 60

GO_FAIL_LINE = re.compile(r"^\s*--- FAIL: (?P<name>\S+) \(")
GO_LOCATION_LINE = re.compile(r"^\s+(?P<file>[\w./-]+_test\.go):(?P<line>\d+): ")
GO_PACKAGE_FAIL_LINE = re.compile(r"^FAIL\t(?P<package>\S+)")


def go_failure_cases(output: str) -> list[dict]:
    """Return the failing Go tests a readable `go test` log names, in the order it names them.

    Each case carries the test's name, the first `_test.go` file and line reported for it,
    and the package whose `FAIL` line closes the block the test appears in.
    """
    cases: list[dict] = []
    open_cases: list[dict] = []
    current: dict | None = None
    for line in output.splitlines():
        failed = GO_FAIL_LINE.match(line)
        if failed:
            current = {"name": failed.group("name"), "file": "", "line": 0, "package": ""}
            cases.append(current)
            open_cases.append(current)
            continue
        located = GO_LOCATION_LINE.match(line)
        if located and current is not None and not current["file"]:
            current["file"] = located.group("file")
            current["line"] = int(located.group("line"))
            continue
        closed = GO_PACKAGE_FAIL_LINE.match(line)
        if closed:
            for case in open_cases:
                case["package"] = closed.group("package")
            open_cases = []
            current = None
    return cases


def scratch_root() -> Path:
    base = Path(os.environ.get("YOLO_DURABLE_DIR", tempfile.gettempdir()))
    base.mkdir(parents=True, exist_ok=True)
    return base


def test_discover_modules() -> None:
    mods = go_affected.discover_tillr_modules(ROOT)
    expected = [
        "tools/tillr",
        "tools/tillr-cli",
        "tools/tillr-drain",
        "tools/tillr-gate",
        "tools/tillr-item",
        "tools/tillr-land",
        "tools/tillr-run",
        "tools/tillr-store",
        "tools/tillr-trace",
        "tools/tillr-view",
        "tools/tillr-watch",
    ]
    assert mods == expected, mods


def test_build_env_strips_outer_gate_vars() -> None:
    polluted = {
        "TILLR_FORK_DRIFT": "1",
        "PI_FORKS_ROOT": "/workspace",
        "PI_FORKS_SERIES_ROOT": "/workspace",
        "LAND_BATCH_LIMIT": "4",
        "TILLR_RUN_ID": "run-outer-1",
        "TILLR_GATE_SPANS_OUT": "/workspace/spans.json",
        "TILLR_GO_VERDICTS_IN": "/workspace/verdicts-in.json",
        "TILLR_GO_VERDICTS_OUT": "/workspace/verdicts-out.json",
        "TILLR_SELF_SUITE_TIMING_OUT": "/workspace/self-suite.json",
        "GO_TEST_SHARDS_HOLDER": "gate:run-outer-1",
        "GIT_DIR": "/workspace/.git",
        "GIT_WORK_TREE": "/workspace",
    }
    with patch.dict(os.environ, polluted):
        env = go_affected.build_env()
    for key in polluted:
        assert key not in env, f"{key} leaked into build_env()"
    assert env["CGO_ENABLED"] == "0"
    assert "-trimpath" in env["GOFLAGS"].split()


def test_subreaper_reaps_orphans_while_command_runs() -> None:
    """Reparented grandchildren must be reaped while the guarded command is still running."""
    if not sys.platform.startswith("linux") or not Path("/proc").is_dir():
        return
    with tempfile.TemporaryDirectory(prefix="go-affected-subreaper-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        code = """import os, sys, time
from pathlib import Path

r, w = os.pipe()
mid = os.fork()
if mid == 0:
    os.close(r)
    grand = os.fork()
    if grand == 0:
        os.close(w)
        os._exit(0)
    os.write(w, f"{grand}\\n".encode())
    os.close(w)
    os._exit(0)
os.close(w)
os.waitpid(mid, 0)
grand_pid = int(os.read(r, 64).decode().strip())
os.close(r)
stat_path = Path(f"/proc/{grand_pid}/stat")
while True:
    if not stat_path.exists():
        print("reaped-while-running")
        sys.exit(0)
    try:
        raw = stat_path.read_text(encoding="utf-8", errors="replace")
    except (ProcessLookupError, FileNotFoundError):
        print("reaped-while-running")
        sys.exit(0)
    state = raw[raw.rfind(")") + 2:].split()[0]
    if state != "Z":
        time.sleep(0.01)
        continue
    time.sleep(0.01)
"""
        rc, stdout = go_affected._run_guarded_command(
            [sys.executable, "-c", code],
            cwd=tmp_path,
            env=go_affected.build_env(),
        )
        assert rc == 0 and "reaped-while-running" in stdout, (rc, stdout)


def test_selection_rules(root_graph: dict) -> None:
    # 1. Unrelated file selects nothing (exercises uncached entry path too).
    sel = go_affected.select_affected_packages(ROOT, ["README.md"])
    assert sel["selected"] == [], sel

    # 2. Pack-sync mirror and non-tillr Go tools are excluded.
    for excluded in (
        "yolo-packs/tillr/files/local/share/tillr/tillr-drain/main.go",
        "tools/dotmod/main.go",
        "tools/bedrock-spend/main.go",
    ):
        sel = go_affected.select_affected_packages(ROOT, [excluded], graph=root_graph)
        assert sel["selected"] == [], (excluded, sel)

    # 3. Drain package change selects drain packages and tillr-view (via import or builds edge),
    # exercising full end-to-end graph loading without preloaded graph.
    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-drain/drain/loop.go"])
    by_pkg = {item["import_path"]: item for item in sel["selected"]}
    assert by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["reason"] == "changed", by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["reason"] == "import", by_pkg
    assert "github.com/mschulkind-oss/tillr-view" in by_pkg, by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-view"]["reason"] in ("import", "edge"), by_pkg

    # 4. Item package change selects item packages and tillr-view (via import or builds edge).
    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-item/item/doc_triage.go"], graph=root_graph)
    by_pkg = {item["import_path"]: item for item in sel["selected"]}
    assert by_pkg["github.com/mschulkind-oss/tillr-item/item"]["reason"] == "changed", by_pkg
    assert "github.com/mschulkind-oss/tillr-view" in by_pkg, by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-view"]["reason"] in ("import", "edge"), by_pkg

    # 5. Unplaceable go.mod change triggers floor for module and replacing modules
    #    (cross-module source-scanning packages in tools/tillr also select via edge).
    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-drain/go.mod"], graph=root_graph)
    floor_mods = set(sel["floor_modules"])
    assert floor_mods >= {
        "tools/tillr-drain",
        "tools/tillr-land",
        "tools/tillr-store",
        "tools/tillr-view",
        "tools/tillr-watch",
    }, sel["floor_modules"]
    assert all(
        item["reason"] == "floor" for item in sel["selected"] if item["module"] in floor_mods
    ), sel["selected"]
    assert all(
        item["reason"] == "edge" for item in sel["selected"] if item["module"] not in floor_mods
    ), sel["selected"]

    # 6. Deleted package path triggers floor.
    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-drain/deletedpkg/foo.go"], graph=root_graph)
    assert "tools/tillr-drain" in sel["floor_modules"], sel

    # 7. drop_modules excludes tools/tillr when go-substrate covers it.
    sel = go_affected.select_affected_packages(
        ROOT, ["tools/tillr/event/event.go"], drop_modules={"tools/tillr"}, graph=root_graph
    )
    assert all(item["module"] != "tools/tillr" for item in sel["selected"]), sel["selected"]
    assert len(sel["selected"]) > 0, sel

    # 8. Sibling/cross-package builds edges (including drain/item main -> tillr-view).
    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-store/main.go"], graph=root_graph)
    pkgs = {item["import_path"] for item in sel["selected"]}
    assert {
        "github.com/mschulkind-oss/tillr-store",
        "github.com/mschulkind-oss/tillr-watch",
        "github.com/mschulkind-oss/tillr-item",
    } <= pkgs, pkgs

    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-view/main.go"], graph=root_graph)
    pkgs = {item["import_path"] for item in sel["selected"]}
    assert {
        "github.com/mschulkind-oss/tillr-view",
        "github.com/mschulkind-oss/tillr-store",
    } <= pkgs, pkgs

    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-drain/main.go"], graph=root_graph)
    by_pkg = {item["import_path"]: item for item in sel["selected"]}
    assert {
        "github.com/mschulkind-oss/tillr-drain",
        "github.com/mschulkind-oss/tillr-drain/drain",
        "github.com/mschulkind-oss/tillr-view",
    } <= set(by_pkg), by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-view"]["reason"] == "edge", by_pkg

    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-item/main.go"], graph=root_graph)
    by_pkg = {item["import_path"]: item for item in sel["selected"]}
    root_edges = go_affected.load_edges(ROOT)
    assert by_pkg["github.com/mschulkind-oss/tillr-view"]["reason"] == "edge", by_pkg
    assert set(by_pkg["github.com/mschulkind-oss/tillr-view"]["selected_tests"] or ()) == set(
        root_edges["github.com/mschulkind-oss/tillr-view"]["build_tests"]["github.com/mschulkind-oss/tillr-item"]
    ), by_pkg["github.com/mschulkind-oss/tillr-view"]

    # High-fanout builds edges in go-test-edges.json narrow to the tests that build each sibling.
    for changed_sib, consumer_pkg, sib_ip in [
        (
            "tools/tillr-view/main.go",
            "github.com/mschulkind-oss/tillr-drain/drain",
            "github.com/mschulkind-oss/tillr-view",
        ),
        (
            "tools/tillr-store/main.go",
            "github.com/mschulkind-oss/tillr-item",
            "github.com/mschulkind-oss/tillr-store",
        ),
        (
            "tools/tillr-gate/main.go",
            "github.com/mschulkind-oss/tillr-land",
            "github.com/mschulkind-oss/tillr-gate",
        ),
    ]:
        sel = go_affected.select_affected_packages(ROOT, [changed_sib], graph=root_graph)
        by_pkg = {item["import_path"]: item for item in sel["selected"]}
        assert by_pkg[consumer_pkg]["reason"] == "edge", (changed_sib, by_pkg[consumer_pkg])
        assert set(by_pkg[consumer_pkg]["selected_tests"] or ()) == set(
            root_edges[consumer_pkg]["build_tests"][sib_ip]
        ), (changed_sib, by_pkg[consumer_pkg])

    # 9. Non-Go reads edges.
    for changed_path, expected_pkgs in [
        ("tools/tillr-run/backends/pi/extract-report", {
            "github.com/mschulkind-oss/tillr-run",
            "github.com/mschulkind-oss/tillr-run/run",
        }),
        ("tools/go-test-shards.py", {
            "github.com/mschulkind-oss/tillr-land",
            "github.com/mschulkind-oss/tillr-drain/drain",
        }),
        ("config/tillr/backend-policy.json", {
            "github.com/mschulkind-oss/tillr-drain",
            "github.com/mschulkind-oss/tillr-drain/drain",
        }),
        ("tools/gate/change-gate.py", {"github.com/mschulkind-oss/tillr-drain/drain"}),
        ("tools/gate/classify.py", {"github.com/mschulkind-oss/tillr-drain/drain"}),
        ("tools/tillr-loop/drain-argv.json", {"github.com/mschulkind-oss/tillr-drain/drain"}),
        ("yolo-packs/tillr/pack.json", {"github.com/mschulkind-oss/tillr-drain/drain"}),
        ("tools/tillr/store/testdata/crash_writer.py", {
            "github.com/mschulkind-oss/tillr-store",
            "github.com/mschulkind-oss/tillr-view",
        }),
        ("tools/tillr/examples/recorded-verdict/definition.json", {
            "github.com/mschulkind-oss/tillr-drain/drain",
            "github.com/mschulkind-oss/tillr-watch",
        }),
        ("tools/gate/checks/go-build.py", {"github.com/mschulkind-oss/tillr-gate"}),
        ("tools/pack-mirror-links.py", {"github.com/mschulkind-oss/tillr-drain/drain"}),
    ]:
        sel = go_affected.select_affected_packages(ROOT, [changed_path], graph=root_graph)
        pkgs = {item["import_path"] for item in sel["selected"]}
        assert expected_pkgs <= pkgs, f"{changed_path}: missing {expected_pkgs - pkgs} in {pkgs}"
        for item in sel["selected"]:
            if item["import_path"] in expected_pkgs:
                assert item["selected_tests"] is None, f"unscoped read {changed_path} narrowed {item}"

    # 10. Scoped read_tests on one read path select only the reading packages and narrow
    # each to its reader tests, whether the path changes alone or beside an unrelated file.
    scoped_read = "tools/gate/change-gate.py"
    drain_pkg = "github.com/mschulkind-oss/tillr-drain/drain"
    gate_pkg = "github.com/mschulkind-oss/tillr-gate"
    drain_readers = go_affected.scan_package_tests(ROOT, {
        "import_path": drain_pkg, "module": "tools/tillr-drain", "dir_rel": "tools/tillr-drain/drain",
    })[:13]
    gate_readers = go_affected.scan_package_tests(ROOT, {
        "import_path": gate_pkg, "module": "tools/tillr-gate", "dir_rel": "tools/tillr-gate",
    })[:9]
    drain_readers = [t for t in drain_readers if t != "TestEdgeInputs"]
    gate_readers = [t for t in gate_readers if t != "TestEdgeInputs"]
    assert drain_readers and gate_readers, (drain_readers, gate_readers)
    scoped_edges = json.loads(json.dumps(go_affected.load_edges(ROOT)))
    for ip in list(scoped_edges):
        scoped_edges[ip]["reads"] = [r for r in scoped_edges[ip].get("reads", []) if r != scoped_read]
    scoped_edges[drain_pkg]["reads"].append(scoped_read)
    scoped_edges[drain_pkg]["read_tests"] = {scoped_read: drain_readers}
    scoped_edges[gate_pkg]["reads"].append(scoped_read)
    scoped_edges[gate_pkg]["read_tests"] = {scoped_read: gate_readers}
    expected_scoped_pkgs = {drain_pkg, gate_pkg}
    for scoped_changed in (
        [scoped_read],
        [scoped_read, "README.md"],
    ):
        sel = go_affected.select_affected_packages(
            ROOT, scoped_changed, edges=scoped_edges, graph=root_graph
        )
        by_pkg = {item["import_path"]: item for item in sel["selected"]}
        assert set(by_pkg) == expected_scoped_pkgs, (scoped_changed, set(by_pkg))
        for ip in expected_scoped_pkgs:
            assert by_pkg[ip]["reason"] == "edge", by_pkg[ip]
            assert "TestEdgeInputs" not in by_pkg[ip]["selected_tests"], by_pkg[ip]
        assert by_pkg[drain_pkg]["selected_tests"] == drain_readers, by_pkg[drain_pkg]
        assert by_pkg[gate_pkg]["selected_tests"] == gate_readers, by_pkg[gate_pkg]

    # 11. Pairing the scoped read path with a Go package edit, a builds-edge edit, an unscoped
    # reads edit, or a module floor trigger widens affected packages back to whole-package
    # execution (selected_tests=None) while unaffected reading packages stay narrowed.
    sel_go = go_affected.select_affected_packages(
        ROOT, [scoped_read, "tools/tillr-drain/drain/loop.go"], edges=scoped_edges, graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_go["selected"]}
    assert by_pkg[drain_pkg]["reason"] == "changed", by_pkg
    assert by_pkg[drain_pkg]["selected_tests"] is None, by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["reason"] == "import", by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["selected_tests"] is None, by_pkg
    assert by_pkg[gate_pkg]["selected_tests"] == gate_readers, by_pkg

    # Pairing a scoped read path with a scoped builds edge narrows to the union of both.
    sel_scoped_both = go_affected.select_affected_packages(
        ROOT, [scoped_read, "tools/tillr-cli/main.go"], edges=scoped_edges, graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_scoped_both["selected"]}
    cli_builders = scoped_edges[drain_pkg]["build_tests"]["github.com/mschulkind-oss/tillr-cli"]
    assert set(by_pkg[drain_pkg]["selected_tests"] or ()) == set(drain_readers) | set(cli_builders), (
        by_pkg[drain_pkg]
    )
    assert by_pkg[gate_pkg]["selected_tests"] == gate_readers, by_pkg

    # Pairing with an unscoped builds edge widens back to whole-package execution.
    unscoped_build_edges = json.loads(json.dumps(scoped_edges))
    unscoped_build_edges[drain_pkg].get("build_tests", {}).pop("github.com/mschulkind-oss/tillr-cli", None)
    sel_builds = go_affected.select_affected_packages(
        ROOT, [scoped_read, "tools/tillr-cli/main.go"], edges=unscoped_build_edges, graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_builds["selected"]}
    assert "github.com/mschulkind-oss/tillr-drain" not in by_pkg, by_pkg
    assert by_pkg[drain_pkg]["selected_tests"] is None, by_pkg
    assert by_pkg[gate_pkg]["selected_tests"] == gate_readers, by_pkg

    sel_unscoped = go_affected.select_affected_packages(
        ROOT, [scoped_read, "config/tillr/backend-policy.json"], edges=scoped_edges, graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_unscoped["selected"]}
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["selected_tests"] is None, by_pkg
    assert by_pkg[drain_pkg]["selected_tests"] is None, by_pkg
    assert by_pkg[gate_pkg]["selected_tests"] == gate_readers, by_pkg

    sel_floor = go_affected.select_affected_packages(
        ROOT, [scoped_read, "tools/tillr-drain/go.mod"], edges=scoped_edges, graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_floor["selected"]}
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["selected_tests"] is None, by_pkg
    assert by_pkg[drain_pkg]["selected_tests"] is None, by_pkg

    # 12. Alert rule changes under config/tillr/watch/ (alone or with tools/tillr-watch/)
    # select tillr-watch and no tillr-drain package, while every config/tillr/ path read by
    # tillr-drain tests still selects that package.
    for watch_changed in (
        ["config/tillr/watch/fast-gate-slow.toml", "tools/tillr-watch/main_test.go"],
        ["config/tillr/watch/new-alert-rule.toml", "tools/tillr-watch/new_alert_rule_test.go"],
        ["config/tillr/watch/fast-gate-slow.toml"],
    ):
        sel_watch = go_affected.select_affected_packages(ROOT, watch_changed, graph=root_graph)
        watch_pkgs = {item["import_path"] for item in sel_watch["selected"]}
        assert "github.com/mschulkind-oss/tillr-watch" in watch_pkgs, (watch_changed, watch_pkgs)
        drain_pkgs = {
            p for p in watch_pkgs
            if p == "github.com/mschulkind-oss/tillr-drain"
            or p.startswith("github.com/mschulkind-oss/tillr-drain/")
        }
        assert not drain_pkgs, f"{watch_changed} selected tillr-drain packages: {sorted(drain_pkgs)}"
        assert "tools/tillr-drain" not in sel_watch["floor_modules"], sel_watch

    policy_files = sorted(
        p.relative_to(ROOT).as_posix()
        for p in (ROOT / "config" / "tillr").glob("backend-policy*.json")
    )
    assert policy_files, "expected config/tillr/backend-policy*.json files"
    for policy_path in policy_files:
        sel_policy = go_affected.select_affected_packages(ROOT, [policy_path], graph=root_graph)
        pkgs = {item["import_path"] for item in sel_policy["selected"]}
        expected_policy_pkgs = {
            "github.com/mschulkind-oss/tillr-drain",
            "github.com/mschulkind-oss/tillr-drain/drain",
        }
        assert expected_policy_pkgs <= pkgs, f"{policy_path}: missing {expected_policy_pkgs - pkgs} in {pkgs}"

    workflow_files = sorted(
        p.relative_to(ROOT).as_posix()
        for p in (ROOT / "config" / "tillr" / "workflows").glob("*.json")
    )
    assert workflow_files, "expected config/tillr/workflows/*.json files"
    for drain_read_path in [
        *workflow_files,
        "config/tillr/workflows/new-workflow@v1.json",
        "config/tillr/commands.json",
        "config/tillr/deny-list.json",
        "config/tillr/gate-policy.json",
        "config/tillr/plan-guidance.md",
    ]:
        sel_drain_cfg = go_affected.select_affected_packages(ROOT, [drain_read_path], graph=root_graph)
        pkgs = {item["import_path"] for item in sel_drain_cfg["selected"]}
        assert "github.com/mschulkind-oss/tillr-drain/drain" in pkgs, f"{drain_read_path}: {pkgs}"


def test_edges_file_honest_and_env_read() -> None:
    edges = go_affected.load_edges(ROOT)
    assert edges, "go-test-edges.json must not be empty"

    # Scan *_test.go for sibling go build / go run invocations outside the test's own package.
    modules = go_affected.discover_tillr_modules(ROOT)
    sibling_dir_re = re.compile(r'"(?:\.\./)+(tillr-[a-z]+)"')
    sibling_pkg_re = re.compile(r'"github\.com/mschulkind-oss/(tillr-[a-z]+)"')
    parent_build_re = re.compile(r'exec\.Command\("go",\s*"build"[^)]*"\.\."\)')
    helper_join_re = re.compile(r'filepath\.Join\((?:"\.\.",\s*)+"tillr-"\s*\+')

    for mod in modules:
        mod_name = mod.split("/", 1)[1]
        mod_root = ROOT / mod
        for test_file in mod_root.rglob("*_test.go"):
            text = test_file.read_text(encoding="utf-8")
            if "go" not in text:
                continue
            if not re.search(r'"go",\s*"(?:build|run)"', text):
                continue
            pkg_rel = test_file.parent.relative_to(mod_root).as_posix()
            pkg_import = (
                f"github.com/mschulkind-oss/{mod_name}"
                if pkg_rel == "."
                else f"github.com/mschulkind-oss/{mod_name}/{pkg_rel}"
            )
            found_targets: set[str] = set()
            for m in sibling_dir_re.finditer(text):
                if m.group(1) != mod_name:
                    found_targets.add(f"github.com/mschulkind-oss/{m.group(1)}")
            for m in sibling_pkg_re.finditer(text):
                if m.group(1) != mod_name:
                    found_targets.add(f"github.com/mschulkind-oss/{m.group(1)}")
            if pkg_rel != "." and parent_build_re.search(text):
                parent_rel = Path(pkg_rel).parent.as_posix()
                parent_import = (
                    f"github.com/mschulkind-oss/{mod_name}"
                    if parent_rel == "."
                    else f"github.com/mschulkind-oss/{mod_name}/{parent_rel}"
                )
                found_targets.add(parent_import)
            if helper_join_re.search(text):
                for name in re.findall(r'buildUtility\([^)]*"([a-z]+)"\)', text):
                    if f"tillr-{name}" != mod_name and (ROOT / "tools" / f"tillr-{name}").is_dir():
                        found_targets.add(f"github.com/mschulkind-oss/tillr-{name}")
                for list_body in re.findall(r'\[\]string\{([^}]+)\}', text):
                    for name in re.findall(r'"([a-z]+)"', list_body):
                        if f"tillr-{name}" != mod_name and (ROOT / "tools" / f"tillr-{name}").is_dir():
                            found_targets.add(f"github.com/mschulkind-oss/tillr-{name}")
            if found_targets:
                assert pkg_import in edges, f"{test_file}: missing entry for {pkg_import} in go-test-edges.json"
                declared = set(edges[pkg_import].get("builds", []))
                missing = found_targets - declared
                assert not missing, f"{test_file}: {pkg_import} missing builds edges {sorted(missing)}"

    # Every package listed in go-test-edges.json must read TILLR_GATE_EDGE_INPUTS in its tests.
    for pkg_import in edges:
        prefix = "github.com/mschulkind-oss/"
        assert pkg_import.startswith(prefix), pkg_import
        rel = pkg_import[len(prefix):]
        pkg_dir = ROOT / "tools" / rel
        assert pkg_dir.is_dir(), f"{pkg_import}: directory {pkg_dir} not found"
        test_files = list(pkg_dir.glob("*_test.go"))
        assert test_files, f"{pkg_import}: no *_test.go files"
        assert any("TILLR_GATE_EDGE_INPUTS" in tf.read_text(encoding="utf-8") for tf in test_files), (
            f"{pkg_import}: no *_test.go reads TILLR_GATE_EDGE_INPUTS"
        )
        # Validate reads rules resolve on disk and read_tests keys/names match scan_package_tests.
        read_rules = set(edges[pkg_import].get("reads", []))
        for rule in sorted(read_rules):
            if rule.endswith("/"):
                assert (ROOT / rule.rstrip("/")).is_dir(), (
                    f"{pkg_import}: directory reads rule {rule!r} missing on disk"
                )
            else:
                assert (ROOT / rule).is_file(), (
                    f"{pkg_import}: file reads rule {rule!r} missing on disk"
                )
        pkg_test_names = set(go_affected.scan_package_tests(ROOT, {
            "import_path": pkg_import,
            "module": f"tools/{rel.split('/', 1)[0]}",
            "dir_rel": f"tools/{rel}",
        }))
        for map_key, allowed_keys in (
            ("build_tests", set(edges[pkg_import].get("builds", []))),
            ("read_tests", read_rules),
        ):
            scoped_tests = edges[pkg_import].get(map_key, {})
            assert isinstance(scoped_tests, dict), f"{pkg_import}: {map_key} must be a dict"
            for rule, names in scoped_tests.items():
                assert rule in allowed_keys, f"{pkg_import}: {map_key} key {rule!r} missing from declaration list"
                assert isinstance(names, list) and names, f"{pkg_import}: {map_key}[{rule!r}] must be non-empty"
                assert "TestEdgeInputs" not in names, (
                    f"{pkg_import}: omit TestEdgeInputs from {map_key}[{rule!r}]; go_affected appends it"
                )
                missing_tests = [n for n in names if n not in pkg_test_names]
                assert not missing_tests, (
                    f"{pkg_import}: {map_key}[{rule!r}] names tests absent from package: {missing_tests}"
                )

    # load_edges refuses invalid build_tests and read_tests declarations.
    with tempfile.TemporaryDirectory(prefix="go-affected-edges-val-", dir=scratch_root()) as tmp:
        bad_repo = Path(tmp)
        (bad_repo / "tools" / "gate").mkdir(parents=True)
        for bad_edges in (
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": ["bad"]}},
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"other.py": ["TestX"]}}},
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": []}}},
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": [123]}}},
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": [""]}}},
            {"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": ["bad"]}},
            {"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/b": ["TestX"]}}},
            {"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/a": []}}},
            {"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/a": [123]}}},
            {"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/a": [""]}}},
            {"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/a": [" TestX "]}}},
        ):
            (bad_repo / "tools" / "gate" / "go-test-edges.json").write_text(json.dumps(bad_edges))
            try:
                go_affected.load_edges(bad_repo)
            except ValueError:
                pass
            else:
                raise AssertionError(f"load_edges accepted invalid scoped tests: {bad_edges}")


def test_edge_cache_invalidation() -> None:
    """Prove Go's result cache hits on unchanged edge digest, misses on change, and records failing cases."""
    with tempfile.TemporaryDirectory(prefix="go-affected-cache-", dir=scratch_root()) as tmp:
        repo = Path(tmp) / "repo"
        (repo / "tools" / "tillr-sib").mkdir(parents=True)
        (repo / "tools" / "tillr-consumer").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "config" / "watch").mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "gate" / "go_affected.py", repo / "tools" / "gate" / "go_affected.py")

        (repo / "tools" / "tillr-sib" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-sib\n\ngo 1.22\n"
        )
        sib_main = repo / "tools" / "tillr-sib" / "main.go"
        sib_main.write_text('package main\nimport "fmt"\nfunc main() { fmt.Println("ok") }\n')

        nonce = _fresh_nonce()
        (repo / "tools" / "tillr-consumer" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-consumer\n\ngo 1.22\n"
        )
        (repo / "tools" / "tillr-consumer" / "consumer_test.go").write_text(
            'package main\n'
            'import (\n'
            '  "os"\n'
            '  "os/exec"\n'
            '  "path/filepath"\n'
            '  "strings"\n'
            '  "testing"\n'
            ')\n'
            'func TestEdgeInputs(t *testing.T) {\n'
            f'  if t.Name() == "{nonce}" {{ t.Fatal() }}\n'
            '  _ = os.Getenv("TILLR_GATE_EDGE_INPUTS")\n'
            '}\n'
            'func TestBuildsEdge(t *testing.T) {\n'
            '  bin := filepath.Join(t.TempDir(), "sib")\n'
            '  b := exec.Command("go", "build", "-o", bin, ".")\n'
            '  b.Dir = "../tillr-sib"\n'
            '  if out, err := b.CombinedOutput(); err != nil { t.Fatalf("build: %v: %s", err, out) }\n'
            '  out, err := exec.Command(bin).CombinedOutput()\n'
            '  if err != nil { t.Fatalf("run: %v: %s", err, out) }\n'
            '  if strings.TrimSpace(string(out)) != "ok" { t.Fatalf("got %q", out) }\n'
            '}\n'
            'func TestReadsEdge(t *testing.T) {\n'
            '  raw, err := os.ReadFile("../../config/watch/rule.txt")\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  if strings.TrimSpace(string(raw)) != "allow" { t.Fatalf("got %q", raw) }\n'
            '}\n'
        )
        rule_file = repo / "config" / "watch" / "rule.txt"
        rule_file.write_text("allow\n")
        (repo / "tools" / "gate" / "go-test-edges.json").write_text(json.dumps({
            "github.com/mschulkind-oss/tillr-consumer": {
                "builds": ["github.com/mschulkind-oss/tillr-sib"],
                "reads": ["config/watch/"],
            }
        }))

        ev_file = Path(tmp) / "evidence.json"
        durable = Path(tmp) / "durable"
        durable.mkdir()
        lock_dir = Path(tmp) / "locks"
        gocache = _shared_gocache()

        def run_once(changed: list[str]) -> tuple[int, str, dict]:
            with patch.dict(os.environ, {
                "GOCACHE": str(gocache),
                "YOLO_DURABLE_DIR": str(durable),
                "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
            }):
                sel = go_affected.select_affected_packages(repo, changed)
                rc, out, ev = go_affected.run_selected_tests(repo, sel, ev_path=str(ev_file))
                return rc, out, ev

        # 1. Warm run via builds edge.
        rc, _out, ev = run_once(["tools/tillr-sib/main.go"])
        assert rc == 0, ev
        assert len(ev["packages"]) == 1 and ev["packages"][0]["cached"] is False, ev
        assert set(ev["packages"][0]) == {"import_path", "module", "reason", "elapsed_ms", "cached"}, ev

        # 2. Second run with same inputs -> cached hit!
        rc, _out, ev = run_once(["tools/tillr-sib/main.go"])
        assert rc == 0, ev
        assert ev["packages"][0]["cached"] is True, ev

        # 3. Break sibling main -> digest changes -> cache miss, test fails, evidence + triage case recorded!
        sib_main.write_text('package main\nimport "fmt"\nfunc main() { fmt.Println("broken") }\n')
        rc, out, ev = run_once(["tools/tillr-sib/main.go"])
        assert rc != 0, ev
        assert ev["packages"][0]["cached"] is False, ev
        written_ev = json.loads(ev_file.read_text(encoding="utf-8"))
        assert written_ev == ev, written_ev
        cases = go_failure_cases(out)
        assert any(
            c["name"] == "TestBuildsEdge"
            and c["file"] == "consumer_test.go"
            and c.get("package") == "github.com/mschulkind-oss/tillr-consumer"
            for c in cases
        ), (out, cases)

        # 4. Restore sibling main -> passes, uncached first, then cached!
        sib_main.write_text('package main\nimport "fmt"\nfunc main() { fmt.Println("ok") }\n')
        rc, _out, ev = run_once(["tools/tillr-sib/main.go"])
        assert rc == 0, ev
        rc, _out, ev = run_once(["tools/tillr-sib/main.go"])
        assert rc == 0 and ev["packages"][0]["cached"] is True, ev

        # 5. Break reads edge input -> digest changes -> cache miss and test fails!
        rule_file.write_text("deny\n")
        rc, out, ev = run_once(["config/watch/rule.txt"])
        assert rc != 0, ev
        assert ev["packages"][0]["cached"] is False, ev
        cases = go_failure_cases(out)
        assert any(
            c["name"] == "TestReadsEdge"
            and c["file"] == "consumer_test.go"
            and c.get("package") == "github.com/mschulkind-oss/tillr-consumer"
            for c in cases
        ), (out, cases)


def test_failed_module_floor_fallback() -> None:
    """When go list fails on a changed module, go-affected selects ./... and fails nonzero."""
    with tempfile.TemporaryDirectory(prefix="go-affected-fail-", dir=scratch_root()) as tmp:
        repo = Path(tmp) / "repo"
        (repo / "tools" / "tillr-broken").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n")
        (repo / "tools" / "tillr-broken" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-broken\n\nbad_directive\n"
        )
        (repo / "tools" / "tillr-broken" / "main.go").write_text(
            "package main\nfunc main() {}\n"
        )
        sel = go_affected.select_affected_packages(repo, ["tools/tillr-broken/main.go"])
        assert "tools/tillr-broken" in sel["floor_modules"], sel
        assert any(
            p["module"] == "tools/tillr-broken"
            and p["import_path"] == "./..."
            and p["reason"] == "floor"
            for p in sel["selected"]
        ), sel
        with patch.dict(os.environ, {
            "GOCACHE": str(_shared_gocache()),
            "YOLO_DURABLE_DIR": str(Path(tmp) / "durable"),
            "GO_TEST_SHARDS_LOCK_DIR": str(Path(tmp) / "locks"),
        }):
            rc, out, _ev = go_affected.run_selected_tests(repo, sel)
        assert rc != 0, f"expected nonzero rc on broken module, got 0:\n{out}"


def test_check_declarations_cover_selection_inputs() -> None:
    """The go-affected declaration runs go_affected.py over a change and reads every selection input."""
    checks_dir = ROOT / "tools" / "gate" / "checks"
    decl = json.loads((checks_dir / "go-affected.json").read_text(encoding="utf-8"))
    assert decl["id"] == "go-affected", decl["id"]
    assert decl["scope"] == "change", decl["scope"]
    assert decl["tier"] == "heavy", decl["tier"]
    assert {"python3", "go", "git"} <= set(decl["tools"]), decl["tools"]
    command = decl["command"]
    assert command[:2] == ["sh", "-c"], command
    assert "python3 tools/gate/go_affected.py " in command[2], command
    assert '--base "$1"' in command[2] and '--head "$2"' in command[2], command
    assert "--drop-module" not in command[2], command

    reads = decl["reads"]
    assert reads == sorted(set(reads)), "go-affected.json reads must be sorted and unique"
    for rule in reads:
        if rule.endswith("/"):
            assert (ROOT / rule.rstrip("/")).is_dir(), f"go-affected.json reads {rule!r} missing on disk"
        else:
            assert (ROOT / rule).is_file(), f"go-affected.json reads {rule!r} missing on disk"

    def covered(path: str, rules: list[str]) -> bool:
        return any(go_affected.path_matches_read(path, rule) for rule in rules)

    # Everything selection consults: the selector, its edges file, the declaration itself,
    # every tillr Go module, and every non-Go path a Go test package declares it reads.
    edges = go_affected.load_edges(ROOT)
    modules = go_affected.discover_tillr_modules(ROOT)
    selection_inputs = [
        "tools/gate/go_affected.py",
        "tools/gate/go-test-edges.json",
        "tools/gate/checks/go-affected.json",
        *(f"{mod}/go.mod" for mod in modules),
        *(f"{mod}/synthetic_trigger_test.go" for mod in modules),
        *(rule for entry in edges.values() for rule in entry.get("reads", [])),
    ]
    for path in selection_inputs:
        assert covered(path, reads), f"go-affected.json reads do not cover {path}"
    for outside in (
        "README.md",
        "tools/dotmod/main_test.go",
        "yolo-packs/tillr/files/local/share/tillr/tillr-drain/drain/gate_timing_test.go",
    ):
        assert not covered(outside, reads), f"go-affected.json reads cover {outside}"

    # The suite's own declaration is a tree check that reads the selector, the edges file,
    # this suite, and every tillr Go module the edges file describes.
    suite_decl = json.loads((checks_dir / "go-affected-test.json").read_text(encoding="utf-8"))
    assert suite_decl["scope"] == "tree" and suite_decl["tier"] == "heavy", suite_decl
    assert suite_decl["command"] == ["python3", "tests/go-affected.test.py"], suite_decl["command"]
    described_mods = [
        mod for mod in modules
        if any(
            k == f"github.com/mschulkind-oss/{Path(mod).name}"
            or k.startswith(f"github.com/mschulkind-oss/{Path(mod).name}/")
            for k in edges
        )
    ]
    assert "tools/tillr-drain" in described_mods, described_mods
    for path in (
        "tools/gate/go_affected.py",
        "tools/gate/go-test-edges.json",
        "tests/go-affected.test.py",
        "tools/tillr-drain/drain/gate_timing_test.go",
        "tools/tillr-drain/workflow/workflow_test.go",
        *(f"{mod}/synthetic_edge_trigger_test.go" for mod in described_mods),
    ):
        assert covered(path, suite_decl["reads"]), f"go-affected-test.json reads do not cover {path}"
    for outside in ("README.md", "tools/dotmod/main_test.go", "tests/tillr-testmain_test.go"):
        assert not covered(outside, suite_decl["reads"]), f"go-affected-test.json reads cover {outside}"

    # The declared command, given a base and a head, tests the one package the change affects.
    with tempfile.TemporaryDirectory(prefix="go-affected-decl-", dir=scratch_root()) as tmp:
        repo = Path(tmp) / "repo"
        (repo / "tools" / "tillr-watch").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "gate" / "go_affected.py", repo / "tools" / "gate" / "go_affected.py")
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n")
        (repo / "tools" / "tillr-watch" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-watch\n\ngo 1.22\n"
        )
        nonce = _fresh_nonce()
        (repo / "tools" / "tillr-watch" / "main.go").write_text("package main\nfunc main() {}\n")
        (repo / "tools" / "tillr-watch" / "main_test.go").write_text(
            f'package main\n\nimport "testing"\n\nfunc TestWatch(t *testing.T) {{ if t.Name() == "{nonce}" {{ t.Fatal() }} }}\n'
        )
        git_env = {
            **os.environ,
            "GIT_AUTHOR_NAME": "Fixture",
            "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
            "GIT_COMMITTER_NAME": "Fixture",
            "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
        }
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "add", "."], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "commit", "-qm", "base"], cwd=repo, env=git_env, check=True)
        (repo / "tools" / "tillr-watch" / "main.go").write_text("package main\nfunc main() { println(1) }\n")
        subprocess.run(["git", "add", "."], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "commit", "-qm", "change"], cwd=repo, env=git_env, check=True)

        res = subprocess.run(
            [*command, "HEAD~1", "HEAD"],
            cwd=repo,
            env={
                **os.environ,
                "GOCACHE": str(_shared_gocache()),
                "YOLO_DURABLE_DIR": str(Path(tmp) / "durable"),
                "GO_TEST_SHARDS_LOCK_DIR": str(Path(tmp) / "locks"),
                "GO_TEST_SHARDS_CLASS": "ordinary",
            },
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        assert res.returncode == 0, res.stdout
        assert "go-affected: 1 package(s)" in res.stdout, res.stdout


def test_heavy_failures_replay(root_graph: dict) -> None:
    """Replay selection over the 13 attributed heavy failures of 2026-11-19 to 2026-11-21."""
    cases = [
        ("9628978cfc", "github.com/mschulkind-oss/tillr-view"),
        ("9cadb24d06", None),
        ("129509992c", "github.com/mschulkind-oss/tillr-drain"),
        ("68fb25f77d", "github.com/mschulkind-oss/tillr-view"),
        ("315a213fe4", "github.com/mschulkind-oss/tillr-drain"),
        ("a0b415fc3d", "github.com/mschulkind-oss/tillr-view"),
        ("9298e19241", "github.com/mschulkind-oss/tillr-drain/drain"),
        ("cc31e10d8b", None),
        ("2838315c85", None),
        ("dcd11d187a", "github.com/mschulkind-oss/tillr-drain"),
        ("9434198857", None),
        ("5514f6eda3", "github.com/mschulkind-oss/tillr-run/run"),
        ("a427c165a3", "github.com/mschulkind-oss/tillr-item"),
    ]
    probe = subprocess.run(
        ["git", "-C", str(ROOT), "cat-file", "-e", f"{cases[0][0]}^{{commit}}"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    if probe.returncode != 0:
        return  # Shallow or synthetic fixture clone without historical trunk commits.

    go_hits = 0
    for sha, expected_pkg in cases:
        changed = go_affected.git_changed_paths(ROOT, f"{sha}~1", sha)
        sel = go_affected.select_affected_packages(ROOT, changed, graph=root_graph)
        selected_pkgs = {p["import_path"]: p["reason"] for p in sel["selected"]}
        if expected_pkg is not None:
            assert expected_pkg in selected_pkgs, (sha, expected_pkg, selected_pkgs)
            go_hits += 1
    assert go_hits == 9, go_hits


def test_failure_refusal_names_packages_and_tests_far_above_tail() -> None:
    """Failures far above the 60-line tail still name every failing package and test in the tail summary."""
    pkg_main = "github.com/mschulkind-oss/tillr-drain"
    pkg_drain = "github.com/mschulkind-oss/tillr-drain/drain"
    pkg_broken = "github.com/mschulkind-oss/tillr-broken"

    events = [
        {"Action": "run", "Package": pkg_main, "Test": "TestEarlyMainFailure"},
        {"Action": "output", "Package": pkg_main, "Test": "TestEarlyMainFailure",
         "Output": "=== RUN   TestEarlyMainFailure\n"},
        {"Action": "output", "Package": pkg_main, "Test": "TestEarlyMainFailure",
         "Output": "    hook_test.go:42: expected directory match\n"},
        {"Action": "output", "Package": pkg_main, "Test": "TestEarlyMainFailure",
         "Output": "--- FAIL: TestEarlyMainFailure (0.12s)\n"},
        {"Action": "fail", "Package": pkg_main, "Test": "TestEarlyMainFailure", "Elapsed": 0.12},
        {"Action": "run", "Package": pkg_main, "Test": "TestEdgeInputs"},
        {"Action": "output", "Package": pkg_main, "Test": "TestEdgeInputs",
         "Output": "    edge_inputs_test.go:10: main edge mismatch\n"},
        {"Action": "output", "Package": pkg_main, "Test": "TestEdgeInputs",
         "Output": "--- FAIL: TestEdgeInputs (0.01s)\n"},
        {"Action": "fail", "Package": pkg_main, "Test": "TestEdgeInputs", "Elapsed": 0.01},
        # Interleaved second package failing far above the tail.
        {"Action": "run", "Package": pkg_drain, "Test": "TestEarlyDrainFailure"},
        {"Action": "output", "Package": pkg_drain, "Test": "TestEarlyDrainFailure",
         "Output": "    fixer_test.go:99: expected named check\n"},
        {"Action": "output", "Package": pkg_drain, "Test": "TestEarlyDrainFailure",
         "Output": "--- FAIL: TestEarlyDrainFailure (0.25s)\n"},
        {"Action": "fail", "Package": pkg_drain, "Test": "TestEarlyDrainFailure", "Elapsed": 0.25},
        {"Action": "run", "Package": pkg_drain, "Test": "TestEdgeInputs"},
        {"Action": "output", "Package": pkg_drain, "Test": "TestEdgeInputs",
         "Output": "    edge_inputs_test.go:10: drain edge mismatch\n"},
        {"Action": "output", "Package": pkg_drain, "Test": "TestEdgeInputs",
         "Output": "--- FAIL: TestEdgeInputs (0.01s)\n"},
        {"Action": "fail", "Package": pkg_drain, "Test": "TestEdgeInputs", "Elapsed": 0.01},
        # Subtest failure: parent + child both emit fail, leaf stays in failed_tests.
        {"Action": "run", "Package": pkg_drain, "Test": "TestSubParent"},
        {"Action": "run", "Package": pkg_drain, "Test": "TestSubParent/leaf"},
        {"Action": "output", "Package": pkg_drain, "Test": "TestSubParent/leaf",
         "Output": "        sub_test.go:15: leaf failed\n"},
        {"Action": "output", "Package": pkg_drain, "Test": "TestSubParent/leaf",
         "Output": "    --- FAIL: TestSubParent/leaf (0.01s)\n"},
        {"Action": "fail", "Package": pkg_drain, "Test": "TestSubParent/leaf", "Elapsed": 0.01},
        {"Action": "output", "Package": pkg_drain, "Test": "TestSubParent",
         "Output": "--- FAIL: TestSubParent (0.02s)\n"},
        {"Action": "fail", "Package": pkg_drain, "Test": "TestSubParent", "Elapsed": 0.02},
    ]
    # Push all failures far above the 60-line tail excerpt with 200+ trailing output lines.
    for i in range(120):
        events.append({
            "Action": "output",
            "Package": pkg_main,
            "Output": f"main trailing log line {i}\n",
        })
        events.append({
            "Action": "output",
            "Package": pkg_drain,
            "Output": f"drain trailing log line {i}\n",
        })
    events.extend([
        {"Action": "output", "Package": pkg_main, "Output": f"FAIL\t{pkg_main}\t1.23s\n"},
        {"Action": "fail", "Package": pkg_main, "Elapsed": 1.23},
        {"Action": "output", "Package": pkg_drain, "Output": f"FAIL\t{pkg_drain}\t2.34s\n"},
        {"Action": "fail", "Package": pkg_drain, "Elapsed": 2.34},
        # A third package that failed with no test-level fail event (e.g. TestMain exit).
        {"Action": "output", "Package": pkg_broken, "Output": f"FAIL\t{pkg_broken} [setup failed]\n"},
        {"Action": "fail", "Package": pkg_broken, "Elapsed": 0.01},
    ])
    raw_stream = "".join(json.dumps(ev) + "\n" for ev in events)
    parsed, readable = go_affected.parse_go_test_json(raw_stream)

    assert parsed[pkg_main]["failed_tests"] == ["TestEarlyMainFailure", "TestEdgeInputs"], parsed[pkg_main]
    assert parsed[pkg_drain]["failed_tests"] == [
        "TestEarlyDrainFailure", "TestEdgeInputs", "TestSubParent/leaf"
    ], parsed[pkg_drain]
    assert parsed[pkg_broken]["failed_tests"] == [] and parsed[pkg_broken]["passed"] is False, parsed[pkg_broken]

    summary = go_affected.format_failure_summary([
        (pkg_main, parsed[pkg_main]["failed_tests"]),
        (pkg_drain, parsed[pkg_drain]["failed_tests"]),
        (pkg_broken, parsed[pkg_broken]["failed_tests"]),
    ])
    full_output = "go-affected: 3 package(s) (0 cached, selection 10ms, slot_wait 0ms)\n" + readable + summary
    all_lines = full_output.splitlines()
    assert len(all_lines) > EXCERPT_LINES * 3, len(all_lines)
    # Confirm the actual --- FAIL: lines are far above the tail.
    early_idx = next(i for i, ln in enumerate(all_lines) if "TestEarlyMainFailure" in ln)
    assert len(all_lines) - early_idx > EXCERPT_LINES * 2, (early_idx, len(all_lines))

    # Even the 60-line tail excerpt carries the failing packages and tests summary.
    tail_excerpt = "\n".join(all_lines[-EXCERPT_LINES:])
    for needle in (
        "go-affected: failing packages and tests:",
        f"  {pkg_main}: TestEarlyMainFailure, TestEdgeInputs",
        f"  {pkg_drain}: TestEarlyDrainFailure, TestEdgeInputs, TestSubParent/leaf",
        f"  {pkg_broken}: (no failing test named)",
    ):
        assert needle in tail_excerpt, (needle, tail_excerpt)

    # The readable log still names every failing test with the file and line it failed at.
    located = {(c["name"], c["file"], c["line"]) for c in go_failure_cases(full_output)}
    for expected in (
        ("TestEarlyMainFailure", "hook_test.go", 42),
        ("TestEdgeInputs", "edge_inputs_test.go", 10),
        ("TestEarlyDrainFailure", "fixer_test.go", 99),
        ("TestSubParent/leaf", "sub_test.go", 15),
    ):
        assert expected in located, (expected, located)


def test_concurrent_runs_do_not_share_scratch_and_preserve_cache() -> None:
    """Two concurrent go-affected runs get distinct scratch dirs; sequential runs still hit Go cache."""
    with tempfile.TemporaryDirectory(prefix="go-affected-concurrent-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        (repo / "tools" / "tillr-conc").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n")
        nonce = _fresh_nonce()
        (repo / "tools" / "tillr-conc" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-conc\n\ngo 1.22\n"
        )
        (repo / "tools" / "tillr-conc" / "conc_test.go").write_text(
            'package main\n'
            'import (\n'
            '  "encoding/json"\n'
            '  "os"\n'
            '  "path/filepath"\n'
            '  "testing"\n'
            '  "time"\n'
            ')\n'
            'func TestScratchIsolation(t *testing.T) {\n'
            f'  if t.Name() == "{nonce}" {{ t.Fatal() }}\n'
            '  rec := map[string]string{\n'
            '    "durable": os.Getenv("YOLO_DURABLE_DIR"),\n'
            '    "tmpdir": os.Getenv("TMPDIR"),\n'
            '    "gitconfig": os.Getenv("GIT_CONFIG_GLOBAL"),\n'
            '  }\n'
            '  if outPath := os.Getenv("CONC_OUT_FILE"); outPath != "" {\n'
            '    raw, _ := json.Marshal(rec)\n'
            '    if err := os.WriteFile(outPath, raw, 0o644); err != nil { t.Fatal(err) }\n'
            '  }\n'
            '  marker := filepath.Join(rec["durable"], "marker")\n'
            '  if _, err := os.Stat(marker); err == nil {\n'
            '    t.Fatal("scratch directory already had marker from another run")\n'
            '  }\n'
            '  if err := os.WriteFile(marker, []byte("mine"), 0o644); err != nil {\n'
            '    t.Fatal(err)\n'
            '  }\n'
            '  if ready := os.Getenv("CONC_READY_FILE"); ready != "" {\n'
            '    if err := os.WriteFile(ready, []byte("ok"), 0o644); err != nil { t.Fatal(err) }\n'
            '  }\n'
            '  if peer := os.Getenv("CONC_PEER_READY_FILE"); peer != "" {\n'
            '    for {\n'
            '      if _, err := os.Stat(peer); err == nil { break }\n'
            '      time.Sleep(20 * time.Millisecond)\n'
            '    }\n'
            '  }\n'
            '}\n'
        )

        shared_durable = tmp_path / "shared-durable"
        shared_durable.mkdir()
        lock_dir = tmp_path / "locks"
        gocache = _shared_gocache()
        _precompile_go_test(repo / "tools" / "tillr-conc", gocache)
        out1, out2 = tmp_path / "out1.json", tmp_path / "out2.json"
        ready1, ready2 = tmp_path / "ready1", tmp_path / "ready2"

        base = {
            **os.environ,
            "YOLO_DURABLE_DIR": str(shared_durable),
            "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
            "GO_TEST_SHARDS_CLASS": "ordinary",
            "GOCACHE": str(gocache),
        }
        cmd = [
            sys.executable,
            str(ROOT / "tools" / "gate" / "go_affected.py"),
            "--root", str(repo),
            "--changed", "tools/tillr-conc/conc_test.go",
        ]
        p1 = subprocess.Popen(
            cmd,
            env={**base, "CONC_OUT_FILE": str(out1), "CONC_READY_FILE": str(ready1), "CONC_PEER_READY_FILE": str(ready2)},
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        p2 = subprocess.Popen(
            cmd,
            env={**base, "CONC_OUT_FILE": str(out2), "CONC_READY_FILE": str(ready2), "CONC_PEER_READY_FILE": str(ready1)},
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        try:
            while not (ready1.exists() and ready2.exists()):
                for tag, proc in (("p1", p1), ("p2", p2)):
                    if proc.poll() is not None:
                        if ready1.exists() and ready2.exists():
                            break
                        raise AssertionError(
                            f"{tag} exited before both concurrent runs reached barrier: "
                            f"{proc.stdout.read() if proc.stdout else ''}"
                        )
                time.sleep(0.02)
            o1, _ = p1.communicate()
            o2, _ = p2.communicate()
        finally:
            for ready in (ready1, ready2):
                if not ready.exists():
                    ready.write_text("cleanup\n", encoding="utf-8")
            for proc in (p1, p2):
                if proc.poll() is None:
                    proc.terminate()
                    proc.wait()
        assert p1.returncode == 0, o1
        assert p2.returncode == 0, o2

        r1 = json.loads(out1.read_text(encoding="utf-8"))
        r2 = json.loads(out2.read_text(encoding="utf-8"))
        assert r1["durable"] and r2["durable"] and r1["durable"] != r2["durable"], (r1, r2)
        assert r1["gitconfig"] and r2["gitconfig"] and r1["gitconfig"] != r2["gitconfig"], (r1, r2)

        # Sequential runs after both finish reuse slot-0 and hit Go's test result cache.
        ev_file = tmp_path / "seq-ev.json"
        seq_env = {**base, "CONC_OUT_FILE": "", "CONC_READY_FILE": "", "CONC_PEER_READY_FILE": ""}
        res1 = subprocess.run(
            [*cmd, "--evidence-out", str(ev_file)],
            env=seq_env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        assert res1.returncode == 0, res1.stdout
        res2 = subprocess.run(
            [*cmd, "--evidence-out", str(ev_file)],
            env=seq_env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        assert res2.returncode == 0, res2.stdout
        ev2 = json.loads(ev_file.read_text(encoding="utf-8"))
        assert ev2["packages"][0]["cached"] is True, ev2


def _fresh_nonce() -> str:
    return uuid.uuid4().hex


@functools.lru_cache(maxsize=1)
def _default_go_env_gocache() -> str:
    try:
        return subprocess.check_output(["go", "env", "GOCACHE"], text=True).strip()
    except (OSError, subprocess.CalledProcessError):
        return ""


def _resolve_writable_gocache(raw: str | None = None) -> Path | None:
    cand = raw.strip() if raw is not None else os.environ.get("GOCACHE", "").strip()
    if not cand:
        cand = _default_go_env_gocache()
    if not cand or cand == "off":
        return None
    path = Path(cand)
    if not path.exists():
        with contextlib.suppress(OSError):
            path.mkdir(parents=True, exist_ok=True)
    if path.is_dir() and os.access(path, os.W_OK):
        return path
    return None


@functools.lru_cache(maxsize=1)
def _fallback_gocache_dir() -> Path:
    owner_pid = os.getpid()
    tmp = tempfile.TemporaryDirectory(prefix="go-affected-gocache-", dir=scratch_root())
    atexit.register(lambda: tmp.cleanup() if os.getpid() == owner_pid else None)
    return Path(tmp.name)


_fallback_warmed = False


def _shared_gocache(*, warm_fallback: bool = True) -> Path:
    """Return a writable GOCACHE, reusing the configured cache or warming one scratch fallback."""
    resolved = _resolve_writable_gocache()
    if resolved is not None:
        return resolved
    fb = _fallback_gocache_dir()
    global _fallback_warmed
    if warm_fallback and not _fallback_warmed:
        with tempfile.TemporaryDirectory(prefix="go-affected-warm-", dir=scratch_root()) as tmp:
            pkg = Path(tmp)
            (pkg / "go.mod").write_text("module example.com/warm\n\ngo 1.22\n")
            (pkg / "warm_test.go").write_text(
                'package warm\n'
                'import (\n'
                '  _ "encoding/json"\n'
                '  _ "fmt"\n'
                '  _ "os"\n'
                '  _ "os/exec"\n'
                '  _ "path/filepath"\n'
                '  _ "strings"\n'
                '  _ "syscall"\n'
                '  "testing"\n'
                '  _ "time"\n'
                ')\n'
                'func TestWarm(t *testing.T) {}\n'
            )
            _precompile_go_test(pkg, fb)
        _fallback_warmed = True
    return fb


def _precompile_go_test(pkg_dir: Path, gocache: Path) -> None:
    """Warm GOCACHE with the compiled test binary under go_affected.build_env() flags."""
    env = go_affected.build_env()
    env["GOCACHE"] = str(gocache)
    subprocess.run(
        ["go", "test", "-c", "-o", os.devnull, "."],
        cwd=pkg_dir,
        env=env,
        check=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def test_no_leftover_processes_after_check_finishes() -> None:
    """After go-affected finishes, no descendant survives — including setsid orphans and drain's stub pi/stop-at-gap/tillr-run."""
    with tempfile.TemporaryDirectory(prefix="go-affected-reap-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        (repo / "tools" / "tillr-orphan").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n")
        (repo / "tools" / "tillr-orphan" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-orphan\n\ngo 1.22\n"
        )
        pids_file = tmp_path / "orphan-pids.txt"
        (repo / "tools" / "tillr-orphan" / "orphan_test.go").write_text(
            'package main\n'
            'import (\n'
            '  "os"\n'
            '  "os/exec"\n'
            '  "syscall"\n'
            '  "testing"\n'
            '  "time"\n'
            ')\n'
            'func TestSpawnsDetachedOrphan(t *testing.T) {\n'
            '  pidsFile := os.Getenv("ORPHAN_PIDS_FILE")\n'
            '  cmd := exec.Command("/bin/sh", "-c", "sleep 600 & child=$!; printf \'%d\\n%d\\n\' $$ $child > \\"$1\\"; wait $child", "sh", pidsFile)\n'
            '  cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}\n'
            '  if err := cmd.Start(); err != nil { t.Fatal(err) }\n'
            '  done := make(chan error, 1)\n'
            '  go func() { done <- cmd.Wait() }()\n'
            '  for {\n'
            '    if raw, err := os.ReadFile(pidsFile); err == nil && len(raw) > 0 { break }\n'
            '    select {\n'
            '    case err := <-done:\n'
            '      t.Fatalf("detached shell exited before writing %s: %v", pidsFile, err)\n'
            '    default:\n'
            '      time.Sleep(20 * time.Millisecond)\n'
            '    }\n'
            '  }\n'
            '}\n'
        )
        durable = tmp_path / "durable"
        durable.mkdir()
        gocache = _shared_gocache()
        _precompile_go_test(repo / "tools" / "tillr-orphan", gocache)
        res = subprocess.run(
            [
                sys.executable,
                str(ROOT / "tools" / "gate" / "go_affected.py"),
                "--root", str(repo),
                "--changed", "tools/tillr-orphan/orphan_test.go",
            ],
            env={
                **os.environ,
                "YOLO_DURABLE_DIR": str(durable),
                "GO_TEST_SHARDS_LOCK_DIR": str(tmp_path / "locks"),
                "GO_TEST_SHARDS_CLASS": "ordinary",
                "GOCACHE": str(gocache),
                "ORPHAN_PIDS_FILE": str(pids_file),
            },
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        assert res.returncode == 0, res.stdout
        recorded = [int(line) for line in pids_file.read_text(encoding="utf-8").splitlines() if line.strip()]
        assert len(recorded) == 2, recorded
        for pid in recorded:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                continue
            raise AssertionError(f"detached orphan pid {pid} survived after go_affected.py finished")

        # Real drain package check: TestRestartReplacesTheLoopInPlaceWithoutSignalingARun spawns
        # tillr-run exec (setsid) -> stop-at-gap -> stub pi -> sleep 600 and leaves them behind
        # without a subreaper guard. Prove none remain after go_affected's guarded invocation exits.
        drain_durable = tmp_path / "drain-durable"
        drain_durable.mkdir()
        with go_affected.acquire_invocation_scratch(str(drain_durable), "drain-reap-test") as (sdir, out_dir):
            env = go_affected.build_env()
            env["GOCACHE"] = str(gocache)
            env["YOLO_DURABLE_DIR"] = str(sdir)
            env["TMPDIR"] = str(sdir)
            rc, stdout = go_affected._run_guarded_command(
                [
                    "go", "test", "-count=1", "-json", "-timeout", "0", "-parallel", "1",
                    "-run", "^TestRestartReplacesTheLoopInPlaceWithoutSignalingARun$",
                    "github.com/mschulkind-oss/tillr-drain/drain",
                ],
                cwd=ROOT / "tools" / "tillr-drain",
                env=env,
                out_dir=out_dir,
            )
            assert rc == 0, stdout
        ps_out = subprocess.check_output(["ps", "-eo", "pid,ppid,args"], text=True)
        me = str(os.getpid())
        leftovers = [
            line for line in ps_out.splitlines()[1:]
            if str(drain_durable) in line and line.strip().split(None, 1)[0] != me
        ]
        assert not leftovers, f"leftover drain processes survived: {leftovers}"


def test_streams_progress_lines_before_run_ends() -> None:
    """go_affected.py prints one line per completed test or package while go test is still running."""
    pkg = "github.com/mschulkind-oss/tillr-stream"
    assert go_affected.format_progress_event("not json\n") is None
    assert go_affected.format_progress_event(json.dumps({"Action": "start", "Package": pkg})) is None
    assert go_affected.format_progress_event(json.dumps({"Action": "run", "Package": pkg, "Test": "TestA"})) is None
    assert go_affected.format_progress_event(json.dumps({"Action": "output", "Package": pkg, "Output": "hi\n"})) is None
    assert go_affected.format_progress_event(
        json.dumps({"Action": "pass", "Package": pkg, "Test": "TestA", "Elapsed": 0.12})
    ) == f"go-affected: pass {pkg} TestA (0.12s)\n"
    assert go_affected.format_progress_event(
        json.dumps({"Action": "fail", "Package": pkg, "Test": "TestB", "Elapsed": 1.5})
    ) == f"go-affected: fail {pkg} TestB (1.50s)\n"
    assert go_affected.format_progress_event(
        json.dumps({"Action": "skip", "Package": pkg, "Test": "TestC", "Elapsed": 0.0})
    ) == f"go-affected: skip {pkg} TestC (0.00s)\n"
    assert go_affected.format_progress_event(
        json.dumps({"Action": "pass", "Package": pkg, "Elapsed": 2.34})
    ) == f"go-affected: pass {pkg} (2.34s)\n"

    with tempfile.TemporaryDirectory(prefix="go-affected-stream-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        (repo / "tools" / "tillr-stream").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n")
        (repo / "tools" / "tillr-stream" / "go.mod").write_text(
            f"module {pkg}\n\ngo 1.22\n"
        )
        release_file = tmp_path / "release"
        (repo / "tools" / "tillr-stream" / "stream_test.go").write_text(
            'package main\n'
            'import (\n'
            '  "os"\n'
            '  "testing"\n'
            '  "time"\n'
            ')\n'
            'func TestFirst(t *testing.T) {}\n'
            'func TestSecond(t *testing.T) {\n'
            '  release := os.Getenv("STREAM_RELEASE_FILE")\n'
            '  for {\n'
            '    if _, err := os.Stat(release); err == nil { return }\n'
            '    time.Sleep(20 * time.Millisecond)\n'
            '  }\n'
            '}\n'
        )
        durable = tmp_path / "durable"
        durable.mkdir()
        gocache = _shared_gocache()
        _precompile_go_test(repo / "tools" / "tillr-stream", gocache)
        proc = subprocess.Popen(
            [
                sys.executable,
                str(ROOT / "tools" / "gate" / "go_affected.py"),
                "--root", str(repo),
                "--changed", "tools/tillr-stream/stream_test.go",
            ],
            env={
                **os.environ,
                "YOLO_DURABLE_DIR": str(durable),
                "GO_TEST_SHARDS_LOCK_DIR": str(tmp_path / "locks"),
                "GO_TEST_SHARDS_CLASS": "ordinary",
                "GOCACHE": str(gocache),
                "STREAM_RELEASE_FILE": str(release_file),
            },
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        seen_lines: list[str] = []
        saw_first_while_running = False
        try:
            assert proc.stdout is not None
            for raw in proc.stdout:
                line = raw.decode("utf-8", errors="replace")
                seen_lines.append(line)
                if f"go-affected: pass {pkg} TestFirst " in line:
                    assert proc.poll() is None, f"process already exited before streaming TestFirst: {seen_lines}"
                    assert not release_file.exists(), "TestSecond was already released"
                    saw_first_while_running = True
                    release_file.write_text("go\n", encoding="utf-8")
                    break
            assert saw_first_while_running, f"did not receive TestFirst progress line while running: {seen_lines}"
            for raw in proc.stdout:
                seen_lines.append(raw.decode("utf-8", errors="replace"))
            rc = proc.wait()
        finally:
            if not release_file.exists():
                release_file.write_text("cleanup\n", encoding="utf-8")
            if proc.poll() is None:
                proc.terminate()
                proc.wait()
        assert rc == 0, "".join(seen_lines)
        assert any(f"go-affected: pass {pkg} TestSecond " in ln for ln in seen_lines), seen_lines
        assert any(ln.startswith(f"go-affected: pass {pkg} (") for ln in seen_lines), seen_lines
        assert any(ln.startswith("go-affected: 1 package(s)") for ln in seen_lines), seen_lines


def test_stalled_run_prints_nothing_without_test_events() -> None:
    """A stalled go test prints no heartbeat/timer lines and burns no wrapper CPU while blocked."""
    pkg = "github.com/mschulkind-oss/tillr-stall"

    def proc_cpu_ticks(pid: int) -> int:
        raw = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8")
        fields = raw[raw.rfind(")") + 2:].split()
        # utime (field 14, index 11 after state) + stime (field 15, index 12 after state).
        return int(fields[11]) + int(fields[12])

    with tempfile.TemporaryDirectory(prefix="go-affected-stall-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        (repo / "tools" / "tillr-stall").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n")
        (repo / "tools" / "tillr-stall" / "go.mod").write_text(
            f"module {pkg}\n\ngo 1.22\n"
        )
        started_file = tmp_path / "started"
        release_file = tmp_path / "release"
        (repo / "tools" / "tillr-stall" / "stall_test.go").write_text(
            'package main\n'
            'import (\n'
            '  "os"\n'
            '  "testing"\n'
            '  "time"\n'
            ')\n'
            'func TestStalls(t *testing.T) {\n'
            '  if err := os.WriteFile(os.Getenv("STALL_STARTED_FILE"), []byte("1"), 0o644); err != nil {\n'
            '    t.Fatal(err)\n'
            '  }\n'
            '  release := os.Getenv("STALL_RELEASE_FILE")\n'
            '  for {\n'
            '    if _, err := os.Stat(release); err == nil { return }\n'
            '    time.Sleep(20 * time.Millisecond)\n'
            '  }\n'
            '}\n'
        )
        durable = tmp_path / "durable"
        durable.mkdir()
        gocache = _shared_gocache()
        _precompile_go_test(repo / "tools" / "tillr-stall", gocache)
        proc = subprocess.Popen(
            [
                sys.executable,
                str(ROOT / "tools" / "gate" / "go_affected.py"),
                "--root", str(repo),
                "--changed", "tools/tillr-stall/stall_test.go",
            ],
            env={
                **os.environ,
                "YOLO_DURABLE_DIR": str(durable),
                "GO_TEST_SHARDS_LOCK_DIR": str(tmp_path / "locks"),
                "GO_TEST_SHARDS_CLASS": "ordinary",
                "GOCACHE": str(gocache),
                "STALL_STARTED_FILE": str(started_file),
                "STALL_RELEASE_FILE": str(release_file),
            },
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        try:
            assert proc.stdout is not None
            while not started_file.exists():
                assert proc.poll() is None, f"go_affected exited prematurely: {proc.stdout.read()!r}"
                time.sleep(0.02)
            # Allow go_affected.py to finish consuming the start/run events and block on the pipe.
            time.sleep(0.2)
            cpu_before = proc_cpu_ticks(proc.pid) if Path(f"/proc/{proc.pid}/stat").is_file() else None
            ready, _, _ = select.select([proc.stdout], [], [], 2.0)
            assert not ready, f"stalled run unexpectedly produced output: {proc.stdout.readline()!r}"
            assert proc.poll() is None, "process exited during stall window"
            if cpu_before is not None:
                cpu_after = proc_cpu_ticks(proc.pid)
                assert cpu_after == cpu_before, (
                    f"wrapper consumed CPU while test stalled: before={cpu_before} after={cpu_after}"
                )
            release_file.write_text("go\n", encoding="utf-8")
            tail = proc.stdout.read().decode("utf-8", errors="replace")
            rc = proc.wait()
        finally:
            if not release_file.exists():
                release_file.write_text("cleanup\n", encoding="utf-8")
            if proc.poll() is None:
                proc.terminate()
                proc.wait()
        assert rc == 0, tail
        assert f"go-affected: pass {pkg} TestStalls " in tail, tail
        assert f"go-affected: pass {pkg} (" in tail, tail


def test_scoped_read_tests_execution_cache_and_sharding() -> None:
    """Prove read_tests narrows -run, LPT-shards >=25s reader subsets, scopes digests, and hits Go's cache."""
    pkg = "github.com/mschulkind-oss/tillr-scoped"
    sib_pkg = "github.com/mschulkind-oss/tillr-sib"
    with tempfile.TemporaryDirectory(prefix="go-affected-scoped-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        (repo / "tools" / "tillr-sib").mkdir(parents=True)
        (repo / "tools" / "tillr-scoped").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "config").mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "gate" / "go_affected.py", repo / "tools" / "gate" / "go_affected.py")
        shutil.copy2(ROOT / "tools" / "go-test-shards.py", repo / "tools" / "go-test-shards.py")

        (repo / "tools" / "tillr-sib" / "go.mod").write_text(
            f"module {sib_pkg}\n\ngo 1.22\n"
        )
        sib_main = repo / "tools" / "tillr-sib" / "main.go"
        sib_main.write_text('package main\nfunc main() {}\n')

        planner_file = repo / "tools" / "planner.py"
        planner_file.write_text("PLANNER_V = 'v1'\n")
        config_file = repo / "config" / "app.txt"
        config_file.write_text("cfg-ok\n")
        pids_dir = tmp_path / "pids"
        pids_dir.mkdir()

        nonce = _fresh_nonce()
        (repo / "tools" / "tillr-scoped" / "go.mod").write_text(
            f"module {pkg}\n\ngo 1.22\n"
        )
        (repo / "tools" / "tillr-scoped" / "scoped_test.go").write_text(
            'package main\n'
            'import (\n'
            '  "fmt"\n'
            '  "os"\n'
            '  "path/filepath"\n'
            '  "strings"\n'
            '  "testing"\n'
            ')\n'
            'func TestEdgeInputs(t *testing.T) {\n'
            f'  if t.Name() == "{nonce}" {{ t.Fatal() }}\n'
            '  if os.Getenv("TILLR_GATE_EDGE_INPUTS") == "" {\n'
            '    t.Fatal("missing TILLR_GATE_EDGE_INPUTS")\n'
            '  }\n'
            '}\n'
            'func recordPID(t *testing.T, name string) {\n'
            '  if dir := os.Getenv("SCOPED_PIDS_DIR"); dir != "" {\n'
            '    _ = os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)\n'
            '  }\n'
            '}\n'
            'func TestReadsPlannerA(t *testing.T) {\n'
            '  recordPID(t, "A")\n'
            '  raw, err := os.ReadFile("../../tools/planner.py")\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  if !strings.Contains(string(raw), "PLANNER_V") { t.Fatalf("bad planner: %q", raw) }\n'
            '}\n'
            'func TestReadsPlannerB(t *testing.T) {\n'
            '  recordPID(t, "B")\n'
            '  raw, err := os.ReadFile("../../tools/planner.py")\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  if strings.Contains(string(raw), "BROKEN_B") { t.Fatalf("planner broke B: %q", raw) }\n'
            '}\n'
            'func TestReadsConfig(t *testing.T) {\n'
            '  recordPID(t, "CFG")\n'
            '  raw, err := os.ReadFile("../../config/app.txt")\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  if strings.TrimSpace(string(raw)) != "cfg-ok" { t.Fatalf("bad cfg: %q", raw) }\n'
            '}\n'
            'func TestUnrelatedMustNotRunWhenNarrowed(t *testing.T) {\n'
            '  recordPID(t, "UNREL")\n'
            '  if os.Getenv("ALLOW_UNRELATED") != "1" {\n'
            '    t.Fatal("unrelated test ran during narrowed reads execution")\n'
            '  }\n'
            '}\n'
        )
        edges_data = {
            pkg: {
                "builds": [sib_pkg],
                "reads": ["config/app.txt", "tools/planner.py"],
                "read_tests": {
                    "tools/planner.py": ["TestReadsPlannerA", "TestReadsPlannerB"],
                },
            }
        }
        (repo / "tools" / "gate" / "go-test-edges.json").write_text(json.dumps(edges_data))
        # Estimated total = 20.0 + 15.0 = 35.0s >= 25.0s across 2 reader tests -> 2 LPT shards.
        (repo / "tools" / "go-test-durations.json").write_text(json.dumps({
            pkg: {
                "TestReadsPlannerA": 20.0,
                "TestReadsPlannerB": 15.0,
            }
        }))

        ev_file = tmp_path / "ev.json"
        durable = tmp_path / "durable"
        durable.mkdir()
        lock_dir = tmp_path / "locks"
        gocache = _shared_gocache()
        _precompile_go_test(repo / "tools" / "tillr-scoped", gocache)

        def run_scoped(changed: list[str], extra_env: dict[str, str] | None = None) -> tuple[int, str, dict]:
            env_patch = {
                "GOCACHE": str(gocache),
                "YOLO_DURABLE_DIR": str(durable),
                "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
                "GO_TEST_SHARDS_CLASS": "ordinary",
                "SCOPED_PIDS_DIR": str(pids_dir),
                **(extra_env or {}),
            }
            with patch.dict(os.environ, env_patch):
                sel = go_affected.select_affected_packages(repo, changed)
                rc, out, ev = go_affected.run_selected_tests(repo, sel, ev_path=str(ev_file))
                return rc, out, ev

        # 1. Scoped change runs only TestReadsPlannerA + TestReadsPlannerB (+ TestEdgeInputs),
        # splits into 2 LPT shard processes, and records "tests" in evidence.
        rc, out, ev = run_scoped(["tools/planner.py", "tests/planner.test.py"])
        assert rc == 0, out
        assert (pids_dir / "A").is_file() and (pids_dir / "B").is_file()
        assert not (pids_dir / "CFG").exists() and not (pids_dir / "UNREL").exists()
        pid_a = (pids_dir / "A").read_text(encoding="utf-8")
        pid_b = (pids_dir / "B").read_text(encoding="utf-8")
        assert pid_a != pid_b, f"expected 2 LPT shards for 35s reader subset, got same pid {pid_a}"
        assert len(ev["packages"]) == 1, ev
        assert ev["packages"][0]["cached"] is False, ev
        assert ev["packages"][0]["tests"] == ["TestReadsPlannerA", "TestReadsPlannerB"], ev
        assert set(ev["packages"][0]) == {
            "import_path", "module", "reason", "elapsed_ms", "cached", "tests"
        }, ev

        # 2. Second run with unchanged inputs hits Go's result cache across both shards
        # without re-executing either test binary.
        (pids_dir / "A").unlink()
        (pids_dir / "B").unlink()
        rc, out, ev = run_scoped(["tools/planner.py"])
        assert rc == 0, out
        assert ev["packages"][0]["cached"] is True, ev
        assert not (pids_dir / "A").exists() and not (pids_dir / "B").exists()
        assert ev["packages"][0]["tests"] == ["TestReadsPlannerA", "TestReadsPlannerB"], ev

        # 3. Editing tools/planner.py invalidates the digest and catches TestReadsPlannerB's failure.
        planner_file.write_text("PLANNER_V = 'v2'\nBROKEN_B = True\n")
        rc, out, ev = run_scoped(["tools/planner.py"])
        assert rc != 0, out
        assert ev["packages"][0]["cached"] is False, ev
        assert "TestReadsPlannerB" in out, out

        # 4. Per-invocation active_tests digest scoping: non-reader shard digests ignore
        # tools/planner.py edits while still tracking builds and unscoped reads.
        graph = go_affected.load_tillr_graph(repo)
        edge_entry = edges_data[pkg]
        d_unrel_before = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestUnrelatedMustNotRunWhenNarrowed"]
        )
        d_reader_before = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestReadsPlannerA"]
        )
        planner_file.write_text("PLANNER_V = 'v3'\n")
        d_unrel_after_fc = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestUnrelatedMustNotRunWhenNarrowed"]
        )
        d_reader_after_fc = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestReadsPlannerA"]
        )
        assert d_unrel_after_fc == d_unrel_before, "non-reader shard digest changed on scoped read edit"
        assert d_reader_after_fc != d_reader_before, "reader shard digest did not change on scoped read edit"

        config_file.write_text("cfg-ok-2\n")
        d_unrel_after_cfg = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestUnrelatedMustNotRunWhenNarrowed"]
        )
        assert d_unrel_after_cfg != d_unrel_after_fc, "non-reader shard digest missed unscoped read edit"
        config_file.write_text("cfg-ok\n")

        sib_main.write_text('package main\nfunc main() { println("v2") }\n')
        d_unrel_after_sib = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestUnrelatedMustNotRunWhenNarrowed"]
        )
        assert d_unrel_after_sib != d_unrel_after_fc, "non-reader shard digest missed builds edit"

        # 5. Pairing with an unscoped reads change widens back to whole-package (5-field evidence).
        rc, out, ev = run_scoped(
            ["tools/planner.py", "config/app.txt"],
            {"ALLOW_UNRELATED": "1", "SCOPED_PIDS_DIR": str(pids_dir)},
        )
        assert rc == 0, out
        assert (pids_dir / "CFG").is_file() and (pids_dir / "UNREL").is_file()
        assert set(ev["packages"][0]) == {"import_path", "module", "reason", "elapsed_ms", "cached"}, ev


def test_scoped_build_tests_execution_cache_and_sharding() -> None:
    """Prove build_tests narrows -run, LPT-shards >=25s builder subsets, scopes digests, and hits Go's cache."""
    pkg = "github.com/mschulkind-oss/tillr-scoped"
    sib_a_pkg = "github.com/mschulkind-oss/tillr-siba"
    sib_b_pkg = "github.com/mschulkind-oss/tillr-sibb"
    with tempfile.TemporaryDirectory(prefix="go-affected-build-scoped-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        (repo / "tools" / "tillr-siba").mkdir(parents=True)
        (repo / "tools" / "tillr-sibb").mkdir(parents=True)
        (repo / "tools" / "tillr-scoped").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "gate" / "go_affected.py", repo / "tools" / "gate" / "go_affected.py")
        shutil.copy2(ROOT / "tools" / "go-test-shards.py", repo / "tools" / "go-test-shards.py")

        (repo / "tools" / "tillr-siba" / "go.mod").write_text(f"module {sib_a_pkg}\n\ngo 1.22\n")
        sib_a_main = repo / "tools" / "tillr-siba" / "main.go"
        sib_a_main.write_text('package main\nfunc main() { println("sibA-v1") }\n')

        (repo / "tools" / "tillr-sibb" / "go.mod").write_text(f"module {sib_b_pkg}\n\ngo 1.22\n")
        sib_b_main = repo / "tools" / "tillr-sibb" / "main.go"
        sib_b_main.write_text('package main\nfunc main() { println("sibB-v1") }\n')

        pids_dir = tmp_path / "pids"
        pids_dir.mkdir()

        nonce = _fresh_nonce()
        (repo / "tools" / "tillr-scoped" / "go.mod").write_text(f"module {pkg}\n\ngo 1.22\n")
        (repo / "tools" / "tillr-scoped" / "scoped_test.go").write_text(
            'package main\n'
            'import (\n'
            '  "fmt"\n'
            '  "os"\n'
            '  "path/filepath"\n'
            '  "strings"\n'
            '  "testing"\n'
            ')\n'
            'func TestEdgeInputs(t *testing.T) {\n'
            f'  if t.Name() == "{nonce}" {{ t.Fatal() }}\n'
            '  if os.Getenv("TILLR_GATE_EDGE_INPUTS") == "" {\n'
            '    t.Fatal("missing TILLR_GATE_EDGE_INPUTS")\n'
            '  }\n'
            '}\n'
            'func recordPID(t *testing.T, name string) {\n'
            '  if dir := os.Getenv("SCOPED_PIDS_DIR"); dir != "" {\n'
            '    _ = os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)\n'
            '  }\n'
            '}\n'
            'func TestBuildsSibA1(t *testing.T) {\n'
            '  recordPID(t, "A1")\n'
            '  raw, err := os.ReadFile("../tillr-siba/main.go")\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  if !strings.Contains(string(raw), "sibA-") { t.Fatalf("bad sibA: %q", raw) }\n'
            '}\n'
            'func TestBuildsSibA2(t *testing.T) {\n'
            '  recordPID(t, "A2")\n'
            '  raw, err := os.ReadFile("../tillr-siba/main.go")\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  if strings.Contains(string(raw), "BROKEN_A2") { t.Fatalf("sibA broke A2: %q", raw) }\n'
            '}\n'
            'func TestBuildsSibB(t *testing.T) {\n'
            '  recordPID(t, "B")\n'
            '  raw, err := os.ReadFile("../tillr-sibb/main.go")\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  if !strings.Contains(string(raw), "sibB-") { t.Fatalf("bad sibB: %q", raw) }\n'
            '}\n'
            'func TestUnrelatedMustNotRunWhenNarrowed(t *testing.T) {\n'
            '  recordPID(t, "UNREL")\n'
            '  if os.Getenv("ALLOW_UNRELATED") != "1" {\n'
            '    t.Fatal("unrelated test ran during narrowed builds execution")\n'
            '  }\n'
            '}\n'
        )
        edges_data = {
            pkg: {
                "builds": [sib_a_pkg, sib_b_pkg],
                "build_tests": {
                    sib_a_pkg: ["TestBuildsSibA1", "TestBuildsSibA2"],
                },
                "reads": [],
            }
        }
        (repo / "tools" / "gate" / "go-test-edges.json").write_text(json.dumps(edges_data))
        (repo / "tools" / "go-test-durations.json").write_text(json.dumps({
            pkg: {
                "TestBuildsSibA1": 20.0,
                "TestBuildsSibA2": 15.0,
            }
        }))

        ev_file = tmp_path / "ev.json"
        durable = tmp_path / "durable"
        durable.mkdir()
        lock_dir = tmp_path / "locks"
        gocache = _shared_gocache()
        _precompile_go_test(repo / "tools" / "tillr-scoped", gocache)

        def run_scoped(changed: list[str], extra_env: dict[str, str] | None = None) -> tuple[int, str, dict]:
            env_patch = {
                "GOCACHE": str(gocache),
                "YOLO_DURABLE_DIR": str(durable),
                "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
                "GO_TEST_SHARDS_CLASS": "ordinary",
                "SCOPED_PIDS_DIR": str(pids_dir),
                **(extra_env or {}),
            }
            with patch.dict(os.environ, env_patch):
                sel = go_affected.select_affected_packages(repo, changed)
                rc, out, ev = go_affected.run_selected_tests(repo, sel, ev_path=str(ev_file))
                return rc, out, ev

        # 1. Scoped builds change runs only TestBuildsSibA1 + TestBuildsSibA2 (+ TestEdgeInputs),
        # splits into 2 LPT shard processes, and records "tests" in evidence.
        rc, out, ev = run_scoped(["tools/tillr-siba/main.go"])
        assert rc == 0, out
        assert (pids_dir / "A1").is_file() and (pids_dir / "A2").is_file()
        assert not (pids_dir / "B").exists() and not (pids_dir / "UNREL").exists()
        pid_a1 = (pids_dir / "A1").read_text(encoding="utf-8")
        pid_a2 = (pids_dir / "A2").read_text(encoding="utf-8")
        assert pid_a1 != pid_a2, f"expected 2 LPT shards for 35s builder subset, got same pid {pid_a1}"
        assert len(ev["packages"]) == 1, ev
        assert ev["packages"][0]["cached"] is False, ev
        assert ev["packages"][0]["tests"] == ["TestBuildsSibA1", "TestBuildsSibA2"], ev
        assert set(ev["packages"][0]) == {
            "import_path", "module", "reason", "elapsed_ms", "cached", "tests"
        }, ev

        # 2. Second run with unchanged inputs hits Go's result cache across both shards.
        (pids_dir / "A1").unlink()
        (pids_dir / "A2").unlink()
        rc, out, ev = run_scoped(["tools/tillr-siba/main.go"])
        assert rc == 0, out
        assert ev["packages"][0]["cached"] is True, ev
        assert not (pids_dir / "A1").exists() and not (pids_dir / "A2").exists()

        # 3. Editing tillr-siba/main.go invalidates the digest and catches TestBuildsSibA2's failure.
        sib_a_main.write_text('package main\nfunc main() { println("sibA-v2 BROKEN_A2") }\n')
        rc, out, ev = run_scoped(["tools/tillr-siba/main.go"])
        assert rc != 0, out
        assert ev["packages"][0]["cached"] is False, ev
        assert "TestBuildsSibA2" in out, out
        sib_a_main.write_text('package main\nfunc main() { println("sibA-v3") }\n')

        # 4. Subset-scoped edge digest: non-builder shard ignores scoped sib_a edits while
        # tracking unscoped sib_b edits; builder shard tracks both sib_a and unscoped sib_b.
        graph = go_affected.load_tillr_graph(repo)
        edge_entry = edges_data[pkg]
        d_unrel_before = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestUnrelatedMustNotRunWhenNarrowed"]
        )
        d_builder_before = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestBuildsSibA1"]
        )
        sib_a_main.write_text('package main\nfunc main() { println("sibA-v4") }\n')
        d_unrel_after_a = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestUnrelatedMustNotRunWhenNarrowed"]
        )
        d_builder_after_a = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestBuildsSibA1"]
        )
        assert d_unrel_after_a == d_unrel_before, "non-builder shard digest changed on scoped builds edit"
        assert d_builder_after_a != d_builder_before, "builder shard digest missed scoped builds edit"

        sib_b_main.write_text('package main\nfunc main() { println("sibB-v2") }\n')
        d_unrel_after_b = go_affected.compute_edge_digest(
            repo, edge_entry, graph["pkg_info"], active_tests=["TestUnrelatedMustNotRunWhenNarrowed"]
        )
        assert d_unrel_after_b != d_unrel_after_a, "non-builder shard digest missed unscoped builds edit"

        # 5. Pairing with an unscoped builds change widens back to whole-package (5-field evidence).
        rc, out, ev = run_scoped(
            ["tools/tillr-siba/main.go", "tools/tillr-sibb/main.go"],
            {"ALLOW_UNRELATED": "1", "SCOPED_PIDS_DIR": str(pids_dir)},
        )
        assert rc == 0, out
        assert (pids_dir / "B").is_file() and (pids_dir / "UNREL").is_file()
        assert set(ev["packages"][0]) == {"import_path", "module", "reason", "elapsed_ms", "cached"}, ev


def _run_cases(
    cases: list[tuple[str, Callable[[], None]]],
    *,
    max_workers: int,
    emit=print,
) -> int:
    if not cases:
        return 0
    if max_workers <= 1 or not hasattr(os, "fork"):
        rc_all = 0
        for name, fn in cases:
            t0 = time.monotonic()
            try:
                fn()
            except BaseException:
                rc_all = 1
                emit(f"FAIL {name} ({time.monotonic() - t0:.2f}s)\n{traceback.format_exc()}")
            else:
                emit(f"PASS {name} ({time.monotonic() - t0:.2f}s)")
        return rc_all

    with tempfile.TemporaryDirectory(prefix="go-affected-cases-", dir=scratch_root()) as log_root:
        log_dir = Path(log_root)
        active: dict[int, tuple[str, float, Path, int]] = {}
        queue = list(enumerate(cases))
        rc_all = 0

        def terminate_children(signum: int, _frame) -> None:
            for pid, (_n, _s, _lp, wfd) in list(active.items()):
                with contextlib.suppress(OSError):
                    os.close(wfd)
                with contextlib.suppress(ProcessLookupError, PermissionError):
                    os.kill(pid, signal.SIGTERM)
            for pid in list(active):
                with contextlib.suppress(ChildProcessError, OSError):
                    os.waitpid(pid, 0)
            raise SystemExit(128 + signum)

        prev_handlers = {
            sig: signal.signal(sig, terminate_children)
            for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP)
        }

        def launch_next() -> None:
            idx, (name, fn) = queue.pop(0)
            log_path = log_dir / f"{idx:02d}-{name}.log"
            child_r, child_w = os.pipe()
            started = time.monotonic()
            sys.stdout.flush()
            sys.stderr.flush()
            pid = os.fork()
            if pid == 0:
                try:
                    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
                        signal.signal(sig, signal.SIG_DFL)
                    os.close(child_w)
                    for _n, _s, _lp, wfd in active.values():
                        with contextlib.suppress(OSError):
                            os.close(wfd)

                    def watch_parent() -> None:
                        with contextlib.suppress(OSError):
                            while os.read(child_r, 64):
                                pass
                        os._exit(1)

                    threading.Thread(target=watch_parent, daemon=True).start()
                    fd = os.open(log_path, os.O_CREAT | os.O_WRONLY | os.O_TRUNC, 0o644)
                    os.dup2(fd, 1)
                    os.dup2(fd, 2)
                    os.close(fd)
                    fn()
                    sys.stdout.flush()
                    sys.stderr.flush()
                    os._exit(0)
                except SystemExit as exc:
                    code = exc.code if isinstance(exc.code, int) else (1 if exc.code else 0)
                    sys.stdout.flush()
                    sys.stderr.flush()
                    os._exit(code)
                except BaseException:
                    traceback.print_exc()
                    sys.stdout.flush()
                    sys.stderr.flush()
                    os._exit(1)
            os.close(child_r)
            active[pid] = (name, started, log_path, child_w)

        try:
            while queue and len(active) < max_workers:
                launch_next()
            while active:
                try:
                    wpid, status = os.waitpid(-1, 0)
                except InterruptedError:
                    continue
                entry = active.pop(wpid, None)
                if entry is None:
                    continue
                name, started, log_path, child_w = entry
                with contextlib.suppress(OSError):
                    os.close(child_w)
                elapsed = time.monotonic() - started
                code = os.waitstatus_to_exitcode(status)
                if code == 0:
                    emit(f"PASS {name} ({elapsed:.2f}s)")
                else:
                    rc_all = 1
                    body = ""
                    with contextlib.suppress(OSError):
                        body = log_path.read_text(encoding="utf-8", errors="replace")
                    emit(f"FAIL {name} ({elapsed:.2f}s)\n{body}".rstrip())
                while queue and len(active) < max_workers:
                    launch_next()
        finally:
            for pid, (_n, _s, _lp, wfd) in list(active.items()):
                with contextlib.suppress(OSError):
                    os.close(wfd)
                with contextlib.suppress(ProcessLookupError, PermissionError):
                    os.kill(pid, signal.SIGTERM)
                with contextlib.suppress(ChildProcessError, OSError):
                    os.waitpid(pid, 0)
            for sig, handler in prev_handlers.items():
                signal.signal(sig, handler)
        return rc_all


def test_suite_gocache_and_runner_helpers() -> None:
    """Suite GOCACHE reuses writable cache, falls back under scratch_root, precompiles with build_env, and reports failures."""
    with tempfile.TemporaryDirectory(prefix="go-affected-helper-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        writable_cache = tmp_path / "writable-gocache"
        writable_cache.mkdir()
        with patch.dict(os.environ, {"GOCACHE": str(writable_cache)}):
            resolved = _shared_gocache()
        assert resolved == writable_cache, f"expected writable GOCACHE reuse {writable_cache}, got {resolved}"

        with patch.dict(os.environ, {"GOCACHE": "off"}):
            fb_off = _shared_gocache(warm_fallback=False)
        assert fb_off.is_dir() and fb_off.is_relative_to(scratch_root()), fb_off
        with patch.dict(os.environ, {"GOCACHE": "/proc/unwritable-gocache"}):
            fb_ro = _shared_gocache(warm_fallback=False)
        assert fb_ro == fb_off, (fb_ro, fb_off)

        pkg_dir = tmp_path / "probe"
        pkg_dir.mkdir()
        nonce = _fresh_nonce()
        (pkg_dir / "go.mod").write_text("module example.com/precompile-probe\n\ngo 1.22\n")
        (pkg_dir / "probe_test.go").write_text(
            f'package probe\nimport "testing"\nfunc TestProbe(t *testing.T) {{ if t.Name() == "{nonce}" {{ t.Fatal() }} }}\n'
        )
        gocache = _shared_gocache()
        _precompile_go_test(pkg_dir, gocache)
        check_res = subprocess.run(
            ["go", "test", "-x", "-c", "-o", os.devnull, "."],
            cwd=pkg_dir,
            env={**go_affected.build_env(), "GOCACHE": str(gocache)},
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            check=True,
        )
        compile_lines = [
            ln for ln in check_res.stdout.splitlines()
            if "/compile " in ln or ln.startswith("compile ")
        ]
        assert not compile_lines, (
            "_precompile_go_test missed go_affected.build_env() cache:\n" + "\n".join(compile_lines[:10])
        )

        lines: list[str] = []

        def ok_case() -> None:
            return None

        def bad_case() -> None:
            raise AssertionError("boom-from-worker")

        rc_fail = _run_cases(
            [("ok_case", ok_case), ("bad_case", bad_case)],
            max_workers=2,
            emit=lines.append,
        )
        assert rc_fail == 1, (rc_fail, lines)
        joined = "\n".join(lines)
        assert "PASS ok_case" in joined, joined
        assert "FAIL bad_case" in joined and "boom-from-worker" in joined, joined

        # scan_package_tests matches Test*, Example*, Fuzz* (excluding TestMain and lowercase suffixes).
        scan_dir = tmp_path / "scanpkg"
        scan_dir.mkdir()
        (scan_dir / "scan_test.go").write_text(
            "package scanpkg\n"
            'import "testing"\n'
            "func TestOne(t *testing.T) {}\n"
            "func Testhelper(t *testing.T) {}\n"
            "func TestMain(m *testing.M) {}\n"
            "func Example() {}\n"
            "func ExampleFoo() {}\n"
            "func FuzzBar(f *testing.F) {}\n"
        )
        scanned = go_affected.scan_package_tests(
            tmp_path,
            {"import_path": "example.com/scanpkg", "dir_rel": "scanpkg", "test_files": ["scan_test.go"]},
        )
        assert scanned == ["TestOne", "Example", "ExampleFoo", "FuzzBar"], scanned

        # Non-edge < 100-test packages use per-package scratch_key = import_path (not module:<mod>).
        seen_scratch_keys: list[str] = []
        orig_acquire = go_affected.acquire_invocation_scratch

        @contextlib.contextmanager
        def spy_acquire(durable, key):
            seen_scratch_keys.append(key)
            with orig_acquire(durable, key) as pair:
                yield pair

        fake_root = tmp_path / "perpkg-root"
        (fake_root / "tools" / "tillr-x" / "a").mkdir(parents=True)
        (fake_root / "tools" / "tillr-x" / "b").mkdir(parents=True)
        (fake_root / "tools" / "tillr-x" / "go.mod").write_text("module example.com/tx\n\ngo 1.22\n")
        (fake_root / "tools" / "tillr-x" / "a" / "a_test.go").write_text(
            'package a\nimport "testing"\nfunc TestA(t *testing.T) {}\n'
        )
        (fake_root / "tools" / "tillr-x" / "b" / "b_test.go").write_text(
            'package b\nimport "testing"\nfunc TestB(t *testing.T) {}\n'
        )
        sel = {
            "selection_ms": 1,
            "modules": ["tools/tillr-x"],
            "floor_modules": [],
            "selected": [
                {
                    "import_path": "example.com/tx/a",
                    "module": "tools/tillr-x",
                    "dir_rel": "tools/tillr-x/a",
                    "test_files": ["a_test.go"],
                    "reason": "changed",
                    "selected_tests": None,
                    "has_edge": False,
                    "edge_digest": None,
                },
                {
                    "import_path": "example.com/tx/b",
                    "module": "tools/tillr-x",
                    "dir_rel": "tools/tillr-x/b",
                    "test_files": ["b_test.go"],
                    "reason": "changed",
                    "selected_tests": None,
                    "has_edge": False,
                    "edge_digest": None,
                },
            ],
        }
        with patch.object(go_affected, "acquire_invocation_scratch", side_effect=spy_acquire):
            rc_sel, out_sel, _ = go_affected.run_selected_tests(fake_root, sel, progress_stream=False)
        assert rc_sel == 0, out_sel
        assert sorted(seen_scratch_keys) == ["example.com/tx/a", "example.com/tx/b"], seen_scratch_keys


def _find_non_shim_git() -> Path:
    for entry in os.environ.get("PATH", "").split(os.pathsep):
        if not entry:
            continue
        cand = Path(entry) / "git"
        if not (cand.is_file() and os.access(cand, os.X_OK)):
            continue
        if cand.parent.parts[-3:] == (".yolo", "bin", "block"):
            continue
        try:
            head = cand.read_bytes()[:256]
        except OSError:
            continue
        if head.startswith(b"#!") and (b"_yg_tool=git" in head or b"# tillr-git-guard" in head):
            continue
        return cand
    raise AssertionError("could not find real git binary on PATH")


def test_private_gitconfig_disables_auto_maintenance_and_bypasses_block_git_shim() -> None:
    """Isolated scratch test runs bypass block/git and git-guard shims, disable maintenance.auto and gc.auto, and keep cache hits."""
    real_git = _find_non_shim_git()
    with tempfile.TemporaryDirectory(prefix="go-affected-gitshim-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)

        # 1. Direct _write_private_gitconfig check: overrides inherited maintenance.auto / gc.auto while keeping owner keys.
        direct_scratch = tmp_path / "direct-scratch"
        direct_scratch.mkdir()
        direct_cfg = go_affected._write_private_gitconfig(
            direct_scratch,
            [
                ("user.name", "Inherited Owner"),
                ("maintenance.auto", "true"),
                ("gc.auto", "6700"),
            ],
        )
        maint_vals = subprocess.check_output(
            [str(real_git), "config", "--file", str(direct_cfg), "--get-all", "maintenance.auto"],
            text=True,
        ).splitlines()
        gc_vals = subprocess.check_output(
            [str(real_git), "config", "--file", str(direct_cfg), "--get-all", "gc.auto"],
            text=True,
        ).splitlines()
        owner_val = subprocess.check_output(
            [str(real_git), "config", "--file", str(direct_cfg), "--get", "user.name"],
            text=True,
        ).strip()
        assert maint_vals == ["false"], maint_vals
        assert gc_vals == ["0"], gc_vals
        assert owner_val == "Inherited Owner", owner_val

        # 2. End-to-end isolated scratch test run with block/git and tillr-git-guard ahead on PATH.
        shim_hits = tmp_path / "shim-hits.log"
        fake_block = tmp_path / ".yolo" / "bin" / "block"
        fake_block.mkdir(parents=True)
        fake_block_git = fake_block / "git"
        fake_block_git.write_text(
            "#!/bin/sh\n"
            "# A guard: runs the real git unless an argument names this workspace's .yolo.\n"
            'if [ -z "$YOLO_BYPASS_SHIMS" ]; then\n'
            "  _yg_tool=git\n"
            f'  printf "block-hit\\n" >> "{shim_hits}"\n'
            "  exit 97\n"
            "fi\n"
            f'exec "{real_git}" "$@"\n',
            encoding="utf-8",
        )
        fake_block_git.chmod(0o755)

        fake_guard_dir = tmp_path / "git-guard"
        fake_guard_dir.mkdir()
        fake_guard_git = fake_guard_dir / "git"
        fake_guard_git.write_text(
            "#!/bin/sh\n"
            "# tillr-git-guard\n"
            f'printf "guard-hit\\n" >> "{shim_hits}"\n'
            "exit 98\n",
            encoding="utf-8",
        )
        fake_guard_git.chmod(0o755)

        fallback_env = go_affected._bypass_git_shim_env(
            {"PATH": os.pathsep.join([str(fake_guard_dir), str(fake_block)])},
            Path("/proc/unwritable-git-bin"),
        )
        assert fallback_env["YOLO_BYPASS_SHIMS"] == "1", fallback_env
        assert str(fake_guard_dir) not in fallback_env["PATH"].split(os.pathsep), fallback_env

        inherited_cfg = tmp_path / "inherited.gitconfig"
        inherited_text = (
            "[user]\n"
            "\tname = Inherited Owner\n"
            "\temail = owner@example.invalid\n"
            "[maintenance]\n"
            "\tauto = true\n"
            "[gc]\n"
            "\tauto = 6700\n"
        )
        inherited_cfg.write_text(inherited_text, encoding="utf-8")

        repo = tmp_path / "repo"
        mod = repo / "tools" / "tillr-gitshim"
        mod.mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n", encoding="utf-8")
        shutil.copy2(ROOT / "tools" / "go-test-shards.py", repo / "tools" / "go-test-shards.py")
        nonce = _fresh_nonce()
        (mod / "go.mod").write_text("module github.com/mschulkind-oss/tillr-gitshim\n\ngo 1.22\n")
        (mod / "gitshim_test.go").write_text(
            "package main\n"
            "import (\n"
            '  "os/exec"\n'
            '  "strings"\n'
            '  "testing"\n'
            ")\n"
            "func TestGitShimBypassAndAutoMaintenanceOff(t *testing.T) {\n"
            f'  if t.Name() == "{nonce}" {{ t.Fatal() }}\n'
            '  resolved, err := exec.LookPath("git")\n'
            "  if err != nil { t.Fatal(err) }\n"
            f'  if resolved == "{fake_block_git}" || resolved == "{fake_guard_git}" {{\n'
            '    t.Fatalf("git resolved to shell shim: %s", resolved)\n'
            "  }\n"
            "  dir := t.TempDir()\n"
            '  if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {\n'
            '    t.Fatalf("git init: %v: %s", err, out)\n'
            "  }\n"
            "  for _, check := range [][2]string{\n"
            '    {"maintenance.auto", "false"},\n'
            '    {"gc.auto", "0"},\n'
            '    {"user.name", "Inherited Owner"},\n'
            "  } {\n"
            '    out, err := exec.Command("git", "-C", dir, "config", "--get-all", check[0]).CombinedOutput()\n'
            '    if err != nil { t.Fatalf("git config %s: %v: %s", check[0], err, out) }\n'
            "    got := strings.TrimSpace(string(out))\n"
            '    if got != check[1] { t.Fatalf("%s = %q, want %q", check[0], got, check[1]) }\n'
            "  }\n"
            '  if out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-qm", "ok").CombinedOutput(); err != nil {\n'
            '    t.Fatalf("git commit: %v: %s", err, out)\n'
            "  }\n"
            "}\n",
            encoding="utf-8",
        )

        durable = tmp_path / "durable"
        durable.mkdir()
        lock_dir = tmp_path / "locks"
        ev_file = tmp_path / "ev.json"
        gocache = _shared_gocache()
        _precompile_go_test(mod, gocache)

        worker_path = os.pathsep.join([str(fake_guard_dir), str(fake_block), os.environ.get("PATH", "")])
        gate_path = os.pathsep.join([str(fake_block), os.environ.get("PATH", "")])
        env1 = {
            **os.environ,
            "PATH": worker_path,
            "GIT_CONFIG_GLOBAL": str(inherited_cfg),
            "YOLO_DURABLE_DIR": str(durable),
            "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
            "GO_TEST_SHARDS_CLASS": "ordinary",
            "GOCACHE": str(gocache),
        }
        env1.pop("YOLO_BYPASS_SHIMS", None)
        cmd = [
            sys.executable,
            str(ROOT / "tools" / "gate" / "go_affected.py"),
            "--root", str(repo),
            "--changed", "tools/tillr-gitshim/gitshim_test.go",
            "--evidence-out", str(ev_file),
        ]
        res1 = subprocess.run(cmd, env=env1, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        assert res1.returncode == 0, res1.stdout
        assert not shim_hits.exists(), shim_hits.read_text(encoding="utf-8")
        assert inherited_cfg.read_text(encoding="utf-8") == inherited_text
        ev1 = json.loads(ev_file.read_text(encoding="utf-8"))
        assert ev1["packages"][0]["cached"] is False, ev1

        env2 = {**env1, "PATH": gate_path}
        res2 = subprocess.run(cmd, env=env2, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        assert res2.returncode == 0, res2.stdout
        assert not shim_hits.exists(), shim_hits.read_text(encoding="utf-8")
        ev2 = json.loads(ev_file.read_text(encoding="utf-8"))
        assert ev2["packages"][0]["cached"] is True, ev2


def test_drain_subpackage_isolation(root_graph: dict) -> None:
    subpkgs = ("workflow", "remotesync", "fixer", "heavyrun")
    subpkg_ips = {f"github.com/mschulkind-oss/tillr-drain/{sub}" for sub in subpkgs}

    # 1. Changing drain production or test files selects none of the four extracted subpackages.
    for drain_path in ("tools/tillr-drain/drain/loop.go", "tools/tillr-drain/drain/loop_test.go"):
        sel = go_affected.select_affected_packages(ROOT, [drain_path], graph=root_graph)
        selected_ips = {item["import_path"] for item in sel["selected"]}
        assert not (selected_ips & subpkg_ips), (drain_path, selected_ips & subpkg_ips)

    # 2. Changing a *_test.go file in any extracted subpackage selects ONLY that subpackage
    #    within tillr-drain (cross-module source-scanning tests in tools/tillr also run narrowed).
    subpkg_tests = {
        "workflow": "tools/tillr-drain/workflow/workflow_test.go",
        "remotesync": "tools/tillr-drain/remotesync/remote_sync_test.go",
        "fixer": "tools/tillr-drain/fixer/fixer_test.go",
        "heavyrun": "tools/tillr-drain/heavyrun/heavy_ack_test.go",
    }
    for sub, test_path in subpkg_tests.items():
        sel = go_affected.select_affected_packages(ROOT, [test_path], graph=root_graph)
        selected_ips = {item["import_path"] for item in sel["selected"]}
        drain_ips = {ip for ip in selected_ips if ip.startswith("github.com/mschulkind-oss/tillr-drain")}
        assert drain_ips == {f"github.com/mschulkind-oss/tillr-drain/{sub}"}, (sub, selected_ips)

    # 3. Changing workflow.go selects workflow, drain, and root tillr-drain,
    #    and does NOT select remotesync, fixer, heavyrun, or heavy.
    sel = go_affected.select_affected_packages(
        ROOT, ["tools/tillr-drain/workflow/workflow.go"], graph=root_graph
    )
    selected_ips = {item["import_path"] for item in sel["selected"]}
    assert {
        "github.com/mschulkind-oss/tillr-drain/workflow",
        "github.com/mschulkind-oss/tillr-drain/drain",
        "github.com/mschulkind-oss/tillr-drain",
    } <= selected_ips, selected_ips
    forbidden_ips = {
        "github.com/mschulkind-oss/tillr-drain/remotesync",
        "github.com/mschulkind-oss/tillr-drain/fixer",
        "github.com/mschulkind-oss/tillr-drain/heavyrun",
        "github.com/mschulkind-oss/tillr-drain/heavy",
    }
    assert not (selected_ips & forbidden_ips), (selected_ips & forbidden_ips)


def test_cross_module_source_scanning_edges(root_graph: dict) -> None:
    """Every scanned Go module selects the tools/tillr source-scanning tests that walk it."""
    edges = go_affected.load_edges(ROOT)
    modules = go_affected.discover_tillr_modules(ROOT)
    def_pkg = "github.com/mschulkind-oss/tillr/definition"
    def_test = "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers"
    store_pkg = "github.com/mschulkind-oss/tillr/store"
    store_test = "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit"

    assert def_pkg in edges, edges
    assert store_pkg in edges, edges

    for mod in modules:
        rule = f"{mod}/"
        assert rule in edges[def_pkg]["reads"], (mod, edges[def_pkg]["reads"])
        assert def_test in edges[def_pkg]["read_tests"][rule], (mod, edges[def_pkg]["read_tests"])
        # Pick a Go package directory inside the module (for tools/tillr, a sibling package
        # that definition does not import) and test both *_test.go and *.go edits.
        sample_dir = "tools/tillr/vantage" if mod == "tools/tillr" else mod
        for sample_path in (
            f"{sample_dir}/synthetic_charter_probe_test.go",
            f"{sample_dir}/synthetic_charter_probe.go",
        ):
            sel = go_affected.select_affected_packages(ROOT, [sample_path], graph=root_graph)
            by_pkg = {item["import_path"]: item for item in sel["selected"]}
            assert def_pkg in by_pkg, (sample_path, sorted(by_pkg))
            assert by_pkg[def_pkg]["reason"] == "edge", (sample_path, by_pkg[def_pkg])
            assert by_pkg[def_pkg]["selected_tests"] == [def_test], (sample_path, by_pkg[def_pkg])
            if mod != "tools/tillr":
                assert rule in edges[store_pkg]["reads"], (mod, edges[store_pkg]["reads"])
                assert store_test in edges[store_pkg]["read_tests"][rule], (mod, edges[store_pkg]["read_tests"])
                assert store_pkg in by_pkg, (sample_path, sorted(by_pkg))
                assert by_pkg[store_pkg]["reason"] == "edge", (sample_path, by_pkg[store_pkg])
                assert by_pkg[store_pkg]["selected_tests"] == [store_test], (sample_path, by_pkg[store_pkg])


def test_candidate_gate_fails_on_undeclared_go_module() -> None:
    """Adding a Go module under tools/ without declaring it in source-scanning reads fails
    the go-edges check in the candidate's gate (--fast-tree --scope-to-change) before merge."""
    decl_path = ROOT / "tools" / "gate" / "checks" / "go-edges.json"
    decl = json.loads(decl_path.read_text(encoding="utf-8"))
    assert decl["id"] == "go-edges", decl
    assert decl["scope"] == "tree" and decl["tier"] == "fast", decl

    with tempfile.TemporaryDirectory(dir=scratch_root(), prefix="ga-cand-edges-") as tmp:
        tmp_path = Path(tmp)
        runner = tmp_path / "tillr-gate"
        env = go_affected.build_env()
        subprocess.run(
            ["go", "build", "-o", str(runner), "."],
            cwd=ROOT / "tools" / "tillr-gate",
            env=env,
            check=True,
        )

        repo = tmp_path / "repo"
        checks_dir = repo / "tools" / "gate" / "checks"
        checks_dir.mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "gate" / "go_affected.py", repo / "tools" / "gate" / "go_affected.py")
        shutil.copy2(decl_path, checks_dir / "go-edges.json")
        shutil.copy2(ROOT / "tools" / "gate" / "checks" / "go-edges.py", checks_dir / "go-edges.py")

        (repo / "tools" / "tillr").mkdir(parents=True)
        (repo / "tools" / "tillr" / "go.mod").write_text("module github.com/mschulkind-oss/tillr\n\ngo 1.22\n")
        (repo / "tools" / "tillr-existing").mkdir(parents=True)
        (repo / "tools" / "tillr-existing" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-existing\n\ngo 1.22\n"
        )

        def_pkg = "github.com/mschulkind-oss/tillr/definition"
        def_test = "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers"
        store_pkg = "github.com/mschulkind-oss/tillr/store"
        store_test = "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit"
        edges = {
            def_pkg: {
                "builds": [],
                "reads": ["tools/tillr-existing/", "tools/tillr/"],
                "read_tests": {
                    "tools/tillr-existing/": [def_test],
                    "tools/tillr/": [def_test],
                },
            },
            store_pkg: {
                "builds": [],
                "reads": ["tools/tillr-existing/"],
                "read_tests": {
                    "tools/tillr-existing/": [store_test],
                },
            },
        }
        edges_path = repo / "tools" / "gate" / "go-test-edges.json"
        edges_path.write_text(json.dumps(edges, indent=2) + "\n", encoding="utf-8")

        git_env = dict(
            env,
            GIT_AUTHOR_NAME="Test User",
            GIT_AUTHOR_EMAIL="test@example.com",
            GIT_COMMITTER_NAME="Test User",
            GIT_COMMITTER_EMAIL="test@example.com",
        )

        def git(*args: str) -> str:
            res = subprocess.run(
                ["git", *args],
                cwd=repo,
                env=git_env,
                text=True,
                capture_output=True,
                check=True,
            )
            return res.stdout.strip()

        git("init")
        git("add", ".")
        git("commit", "-m", "base")
        base = git("rev-parse", "HEAD")

        # Add a new Go module under tools/ without declaring it in go-test-edges.json.
        (repo / "tools" / "tillr-new").mkdir(parents=True)
        (repo / "tools" / "tillr-new" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-new\n\ngo 1.22\n"
        )
        git("add", "tools/tillr-new/go.mod")
        git("commit", "-m", "add undeclared module")
        head_missing = git("rev-parse", "HEAD")

        logs_fail = tmp_path / "logs-fail"
        res_fail = subprocess.run(
            [
                str(runner),
                "checks",
                "--fast-tree",
                "--scope-to-change",
                "--base",
                base,
                "--head",
                head_missing,
                "--worktree",
                str(repo),
                "--checks",
                str(checks_dir),
                "--logs",
                str(logs_fail),
            ],
            cwd=repo,
            env=env,
            text=True,
            capture_output=True,
        )
        assert res_fail.returncode != 0, (res_fail.returncode, res_fail.stdout, res_fail.stderr)
        verdict_fail = json.loads(res_fail.stdout.splitlines()[0])
        checks_fail = {c["check_id"]: c for c in verdict_fail["payload"]["checks"]}
        assert "go-edges" in checks_fail, checks_fail
        assert checks_fail["go-edges"]["result"] == "fail", checks_fail["go-edges"]
        log_texts = "\n".join(p.read_text(encoding="utf-8") for p in logs_fail.glob("*.log"))
        assert "tools/tillr-new/" in log_texts, log_texts
        assert "tools/gate/go-test-edges.json" in log_texts, log_texts

        # Now declare tools/tillr-new/ in both source-scanning packages; the candidate gate passes.
        edges[def_pkg]["reads"].append("tools/tillr-new/")
        edges[def_pkg]["read_tests"]["tools/tillr-new/"] = [def_test]
        edges[store_pkg]["reads"].append("tools/tillr-new/")
        edges[store_pkg]["read_tests"]["tools/tillr-new/"] = [store_test]
        edges_path.write_text(json.dumps(edges, indent=2) + "\n", encoding="utf-8")
        git("add", "tools/gate/go-test-edges.json")
        git("commit", "-m", "declare new module in edges")
        head_declared = git("rev-parse", "HEAD")

        logs_pass = tmp_path / "logs-pass"
        res_pass = subprocess.run(
            [
                str(runner),
                "checks",
                "--fast-tree",
                "--scope-to-change",
                "--base",
                base,
                "--head",
                head_declared,
                "--worktree",
                str(repo),
                "--checks",
                str(checks_dir),
                "--logs",
                str(logs_pass),
            ],
            cwd=repo,
            env=env,
            text=True,
            capture_output=True,
        )
        assert res_pass.returncode == 0, (res_pass.returncode, res_pass.stdout, res_pass.stderr)
        verdict_pass = json.loads(res_pass.stdout.splitlines()[0])
        checks_pass = {c["check_id"]: c for c in verdict_pass["payload"]["checks"]}
        assert checks_pass["go-edges"]["result"] == "pass", checks_pass["go-edges"]


def test_cfix_regate_reuses_unchanged_package_verdicts() -> None:
    """Conflict-repair (-cfix) regates reuse passing per-package verdicts from the item's
    pre-conflict go-affected run when package transitive inputs and declared edge digests
    are unchanged, and re-run only packages whose inputs changed during conflict repair.
    """
    with tempfile.TemporaryDirectory(dir=scratch_root(), prefix="ga-cfix-") as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        loop_dir = tmp_path / "loop"
        durable = tmp_path / "durable"
        lock_dir = tmp_path / "locks"
        loop_dir.mkdir()
        durable.mkdir()

        for mod_rel in (
            "tools/tillr/core",
            "tools/tillr/extra",
            "tools/tillr-gate",
            "tools/tillr-watch",
            "tools/tillr-view",
            "tools/gate",
            "config",
        ):
            (repo / mod_rel).mkdir(parents=True, exist_ok=True)

        shutil.copy2(ROOT / "tools" / "gate" / "go_affected.py", repo / "tools" / "gate" / "go_affected.py")
        shutil.copy2(ROOT / "tools" / "go-test-shards.py", repo / "tools" / "go-test-shards.py")
        (repo / "tools" / "go-test-durations.json").write_text("{}\n", encoding="utf-8")

        (repo / "config" / "view.txt").write_text("v1\n", encoding="utf-8")
        (repo / "notes.txt").write_text("base-line\n", encoding="utf-8")
        (repo / "tools" / "gate" / "go-test-edges.json").write_text(
            json.dumps(
                {
                    "github.com/mschulkind-oss/tillr-view": {
                        "builds": ["github.com/mschulkind-oss/tillr-watch"],
                        "reads": ["config/view.txt"],
                    }
                },
                indent=2,
            )
            + "\n",
            encoding="utf-8",
        )

        (repo / "tools" / "tillr" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr\n\ngo 1.22\n", encoding="utf-8"
        )
        (repo / "tools" / "tillr" / "core" / "core.go").write_text(
            "package core\n\nfunc Value() int { return 1 }\n", encoding="utf-8"
        )
        (repo / "tools" / "tillr" / "extra" / "extra.go").write_text(
            "package extra\n\nfunc Extra() int { return 10 }\n", encoding="utf-8"
        )
        (repo / "tools" / "tillr-gate" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-gate\n\ngo 1.22\n\n"
            "require github.com/mschulkind-oss/tillr v0.0.0\n"
            "replace github.com/mschulkind-oss/tillr => ../tillr\n",
            encoding="utf-8",
        )
        (repo / "tools" / "tillr-gate" / "gate.go").write_text(
            'package gate\n\nimport "github.com/mschulkind-oss/tillr/core"\n\n'
            "func GateVal() int { return core.Value() }\n",
            encoding="utf-8",
        )
        (repo / "tools" / "tillr-watch" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-watch\n\ngo 1.22\n\n"
            "require github.com/mschulkind-oss/tillr v0.0.0\n"
            "replace github.com/mschulkind-oss/tillr => ../tillr\n",
            encoding="utf-8",
        )
        (repo / "tools" / "tillr-watch" / "main.go").write_text(
            "package main\n\n"
            'import "github.com/mschulkind-oss/tillr/core"\n\n'
            "func WatchVal() int { return core.Value() }\n"
            "func main() {}\n",
            encoding="utf-8",
        )
        (repo / "tools" / "tillr-view" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-view\n\ngo 1.22\n", encoding="utf-8"
        )
        (repo / "tools" / "tillr-view" / "view.go").write_text(
            "package view\n\nfunc ViewVal() int { return 7 }\n", encoding="utf-8"
        )

        def write_logging_test(rel_path: str, pkg_name: str, marker: str, fn_name: str) -> None:
            (repo / rel_path).write_text(
                f"package {pkg_name}\n\n"
                "import (\n"
                '\t"os"\n'
                '\t"path/filepath"\n'
                '\t"testing"\n'
                ")\n\n"
                f"func {fn_name}(t *testing.T) {{\n"
                '\tif dir := os.Getenv("CFIX_EXEC_LOG_DIR"); dir != "" {\n'
                f'\t\t_ = os.WriteFile(filepath.Join(dir, "{marker}"), []byte("ran\\n"), 0o644)\n'
                "\t}\n"
                "}\n",
                encoding="utf-8",
            )

        write_logging_test("tools/tillr/core/core_test.go", "core", "tillr-core", "TestCore")
        write_logging_test("tools/tillr/extra/extra_test.go", "extra", "tillr-extra", "TestExtra")
        write_logging_test("tools/tillr-gate/gate_test.go", "gate", "tillr-gate", "TestGate")
        write_logging_test("tools/tillr-watch/main_test.go", "main", "tillr-watch", "TestWatch")
        write_logging_test("tools/tillr-view/view_test.go", "view", "tillr-view", "TestView")

        git_env = {
            **os.environ,
            "GIT_AUTHOR_NAME": "Fixture",
            "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
            "GIT_COMMITTER_NAME": "Fixture",
            "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
        }
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "add", "."], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "commit", "-qm", "base-v1"], cwd=repo, env=git_env, check=True)
        base_v1 = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()

        # Candidate pre-conflict commit modifies tillr/core/core.go and notes.txt.
        subprocess.run(["git", "checkout", "-qb", "cand"], cwd=repo, env=git_env, check=True)
        (repo / "tools" / "tillr" / "core" / "core.go").write_text(
            "package core\n\nfunc Value() int { return 2 }\n", encoding="utf-8"
        )
        (repo / "notes.txt").write_text("candidate-notes\n", encoding="utf-8")
        subprocess.run(["git", "add", "."], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "commit", "-qm", "cand-v1"], cwd=repo, env=git_env, check=True)

        gocache = _shared_gocache()
        step_counter = 0

        def run_landing_gate(run_id: str, since_sha: str, item_id: str = "item-request-demo") -> tuple[dict, set[str]]:
            """Run go_affected.py for one run directory the way a gate run does.

            The run's passing verdicts are written to its own directory, and a -cfix run is
            handed the verdicts its pre-conflict runs recorded for the same item.
            """
            nonlocal step_counter
            step_counter += 1
            exec_dir = tmp_path / f"exec-{step_counter}"
            exec_dir.mkdir()
            run_dir = loop_dir / run_id
            run_dir.mkdir(parents=True, exist_ok=True)
            (run_dir / "run.record.json").write_text(
                json.dumps({"payload": {"run_id": run_id, "item_id": item_id}}) + "\n",
                encoding="utf-8",
            )
            evidence_path = run_dir / "go-affected.evidence.json"
            verdicts_out = tmp_path / f"verdicts-out-{step_counter}.json"
            argv = [
                sys.executable, "tools/gate/go_affected.py",
                "--base", since_sha, "--head", "HEAD",
                "--evidence-out", str(evidence_path),
                "--verdicts-out", str(verdicts_out),
            ]
            prior = (
                go_affected.load_prior_cfix_verdicts(run_dir)
                if go_affected.is_cfix_run_id(run_id)
                else None
            )
            if prior and prior.get("packages"):
                verdicts_in = tmp_path / f"verdicts-in-{step_counter}.json"
                go_affected.save_package_verdicts_atomic(verdicts_in, prior)
                argv += ["--verdicts-in", str(verdicts_in)]
            env = {
                **os.environ,
                "TILLR_RUN_ID": "",
                "YOLO_DURABLE_DIR": str(durable),
                "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
                "GOCACHE": str(gocache),
                "CFIX_EXEC_LOG_DIR": str(exec_dir),
            }
            for name in (go_affected.GO_VERDICTS_IN_ENV, go_affected.GO_VERDICTS_OUT_ENV):
                env.pop(name, None)
            res = subprocess.run(
                argv,
                cwd=repo,
                env=env,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )
            assert res.returncode == 0, f"run_id={run_id} rc={res.returncode}\n{res.stdout}"
            ran_markers = {p.name for p in exec_dir.iterdir()}
            recorded = go_affected.load_package_verdicts_file(verdicts_out)
            if recorded is not None:
                go_affected.save_package_verdicts_atomic(
                    run_dir / go_affected.GO_PACKAGE_VERDICTS_FILE,
                    {**recorded, "item_id": item_id, "run_id": run_id},
                )
            evidence = json.loads(evidence_path.read_text(encoding="utf-8"))
            return evidence, ran_markers

        # 1. Pre-conflict gate runs only the affected Go packages via go-affected (skipping untouched sibling tillr/extra).
        orig_run = "run-item-request-demo-a1-n2"
        _, ran_orig = run_landing_gate(orig_run, base_v1)
        affected_go_markers = {"tillr-core", "tillr-gate", "tillr-watch", "tillr-view"}
        assert ran_orig == affected_go_markers, ran_orig
        assert "tillr-extra" not in ran_orig, ran_orig
        verdicts_path = loop_dir / orig_run / "gate.go-packages.json"
        assert verdicts_path.is_file(), f"expected {verdicts_path} to be recorded"

        # 2. Trunk advances on notes.txt; conflict repair resolves ONLY notes.txt (no Go or edge file changed).
        subprocess.run(["git", "checkout", "-q", "main"], cwd=repo, env=git_env, check=True)
        (repo / "notes.txt").write_text("trunk-notes\n", encoding="utf-8")
        subprocess.run(["git", "add", "notes.txt"], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "commit", "-qm", "trunk-notes"], cwd=repo, env=git_env, check=True)
        base_v2 = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()

        subprocess.run(["git", "checkout", "-qb", "cand-cfix", base_v2], cwd=repo, env=git_env, check=True)
        (repo / "tools" / "tillr" / "core" / "core.go").write_text(
            "package core\n\nfunc Value() int { return 2 }\n", encoding="utf-8"
        )
        (repo / "notes.txt").write_text("resolved-trunk-and-candidate-notes\n", encoding="utf-8")
        subprocess.run(["git", "add", "."], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "commit", "-qm", "cand-cfix-unrelated"], cwd=repo, env=git_env, check=True)

        evidence_cfix, ran_cfix = run_landing_gate(f"{orig_run}-cfix", base_v2)
        assert ran_cfix == set(), (
            f"expected zero Go packages to re-run on unrelated-file -cfix, got {ran_cfix}"
        )
        ga_pkgs = {p["import_path"]: p for p in evidence_cfix["packages"]}
        assert set(ga_pkgs) == {
            "github.com/mschulkind-oss/tillr/core",
            "github.com/mschulkind-oss/tillr-gate",
            "github.com/mschulkind-oss/tillr-watch",
            "github.com/mschulkind-oss/tillr-view",
        }, ga_pkgs
        assert all(p.get("reused") is True and p["elapsed_ms"] == 0 for p in ga_pkgs.values()), ga_pkgs

        # 3. Ordinary (non-cfix) run — even when the item slug contains "cfix" — never reuses -cfix verdicts.
        assert not go_affected.is_cfix_run_id("run-item-cfix-slug-a1-n1")
        _, ran_ordinary = run_landing_gate("run-item-cfix-slug-a1-n1", base_v2, item_id="item-cfix-slug")
        assert ran_ordinary == affected_go_markers, ran_ordinary

        # 4. Cross-item mismatch never reuses another item's verdicts.
        _, ran_wrong_item = run_landing_gate(f"{orig_run}-cfix", base_v2, item_id="item-request-other")
        assert ran_wrong_item == affected_go_markers, ran_wrong_item

        # 5. Selective invalidation: conflict repair modifies config/view.txt (declared edge of tillr-view)
        #    and tools/tillr/extra/extra.go (sibling package in tools/tillr not imported by core/gate/watch).
        #    Only tillr-extra and tillr-view re-run; tillr-core, tillr-gate, and tillr-watch stay reused!
        (repo / "config" / "view.txt").write_text("v2-from-conflict-fix\n", encoding="utf-8")
        (repo / "tools" / "tillr" / "extra" / "extra.go").write_text(
            "package extra\n\nfunc Extra() int { return 20 }\n", encoding="utf-8"
        )
        subprocess.run(["git", "add", "."], cwd=repo, env=git_env, check=True)
        subprocess.run(["git", "commit", "-qm", "cand-cfix-partial"], cwd=repo, env=git_env, check=True)

        _, ran_partial = run_landing_gate(f"{orig_run}-cfix-restart", base_v2)
        assert ran_partial == {"tillr-extra", "tillr-view"}, ran_partial

        # 6. Hashed boundRunIDBase -cfix run resolves pre-conflict run via brief.md's "Its first run was <orig>,".
        hashed_cfix = "run-item-request-demo-long-stem-0123456789abcdef-cfix"
        hashed_dir = loop_dir / hashed_cfix
        hashed_dir.mkdir(parents=True, exist_ok=True)
        (hashed_dir / "brief.md").write_text(
            f"# Conflict Fix\n- **Item:** item-request-demo\nIts first run was {orig_run}, which passed.\n",
            encoding="utf-8",
        )
        _, ran_hashed = run_landing_gate(hashed_cfix, base_v2)
        assert ran_hashed == {"tillr-extra", "tillr-view"}, ran_hashed

        # 7. Narrowed read_tests verdict does NOT satisfy a whole-package selection, while matching narrowed reuses.
        narrow_prior = {
            "packages": {
                "github.com/mschulkind-oss/tillr-view": {
                    "whole_digests": [],
                    "narrowed": {},
                }
            }
        }
        repo_graph = go_affected.load_tillr_graph(repo)
        repo_edges = go_affected.load_edges(repo)
        narrow_digest = go_affected.compute_package_verdict_digest(
            repo,
            "github.com/mschulkind-oss/tillr-view",
            repo_graph,
            repo_edges,
            active_tests=["TestView"],
        )
        assert narrow_digest is not None
        narrow_prior["packages"]["github.com/mschulkind-oss/tillr-view"]["narrowed"]["TestView"] = {
            "tests": ["TestView"],
            "digest": narrow_digest,
        }
        assert go_affected.package_verdict_reusable(
            repo,
            "github.com/mschulkind-oss/tillr-view",
            repo_graph,
            repo_edges,
            narrow_prior,
            selected_tests=["TestView"],
        )
        assert not go_affected.package_verdict_reusable(
            repo,
            "github.com/mschulkind-oss/tillr-view",
            repo_graph,
            repo_edges,
            narrow_prior,
            selected_tests=None,
        )


def test_max_shards_scales_with_cpu_count() -> None:
    """MAX_SHARDS is 2 * os.cpu_count() (12 when undetermined) and shard sizing scales past 12 up to MAX_SHARDS."""
    go_affected_path = ROOT / "tools" / "gate" / "go_affected.py"
    host_cpus = os.cpu_count()
    expected_host = 2 * host_cpus if host_cpus and host_cpus > 0 else 12
    assert go_affected.MAX_SHARDS == expected_host, (go_affected.MAX_SHARDS, host_cpus)

    loaded_by_cpus: dict[int, object] = {}
    for cpus in (1, 4, 8, 16, 32):
        with patch.object(os, "cpu_count", return_value=cpus):
            mod = load_module(f"go_affected_cpus_{cpus}", go_affected_path)
        assert mod.MAX_SHARDS == 2 * cpus, (cpus, mod.MAX_SHARDS)
        loaded_by_cpus[cpus] = mod

    for missing in (None, 0, -1):
        with patch.object(os, "cpu_count", return_value=missing):
            mod = load_module(f"go_affected_missing_{missing}", go_affected_path)
        assert mod.MAX_SHARDS == 12, (missing, mod.MAX_SHARDS)

    pkg = "example.com/tx/sharded"
    with tempfile.TemporaryDirectory(prefix="go-affected-max-shards-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        pkg_dir = repo / "tools" / "tillr-x" / "sharded"
        pkg_dir.mkdir(parents=True)
        (repo / "tools" / "tillr-x" / "go.mod").write_text("module example.com/tx\n\ngo 1.22\n")
        durable = tmp_path / "durable"
        durable.mkdir()

        def count_invocations(mod, total_tests: int, selected_tests: list[str] | None) -> int:
            lines = ['package sharded\nimport "testing"']
            lines.extend(f"func TestCase{i:04d}(t *testing.T) {{}}" for i in range(total_tests))
            (pkg_dir / "sharded_test.go").write_text("\n".join(lines) + "\n")
            seen_cmds: list[list[str]] = []
            seen_lock = threading.Lock()

            def fake_guarded(cmd, **_kwargs):
                with seen_lock:
                    seen_cmds.append(list(cmd))
                return 0, json.dumps({"Action": "pass", "Package": pkg}) + "\n"

            sel = {
                "selection_ms": 1,
                "modules": ["tools/tillr-x"],
                "floor_modules": [],
                "selected": [
                    {
                        "import_path": pkg,
                        "module": "tools/tillr-x",
                        "dir_rel": "tools/tillr-x/sharded",
                        "test_files": ["sharded_test.go"],
                        "reason": "edge" if selected_tests is not None else "changed",
                        "selected_tests": selected_tests,
                        "has_edge": False,
                        "edge_digest": None,
                    }
                ],
            }
            with (
                patch.dict(os.environ, {"YOLO_DURABLE_DIR": str(durable)}),
                patch.object(mod, "_run_guarded_command", side_effect=fake_guarded),
            ):
                rc, out, _ev = mod.run_selected_tests(repo, sel, progress_stream=False)
            assert rc == 0, out
            return len(seen_cmds)

        # Unnarrowed >= SHARD_THRESHOLD: 2,500 tests -> 2,500 // 125 = 20 uncapped shards.
        # On 8 processors (MAX_SHARDS = 16), scales past 12 and caps at 16; on 16 processors
        # (MAX_SHARDS = 32), runs all 20 shards and caps 5,000 tests (40 uncapped) at 32.
        assert count_invocations(loaded_by_cpus[8], 2500, None) == 16
        assert count_invocations(loaded_by_cpus[16], 2500, None) == 20
        assert count_invocations(loaded_by_cpus[16], 5000, None) == 32

        # Narrowed selected_tests: 20 reader tests * 25.0s = 500.0s -> 20 uncapped duration shards.
        narrowed = [f"TestCase{i:04d}" for i in range(20)]
        (repo / "tools" / "go-test-durations.json").write_text(
            json.dumps({pkg: {name: 25.0 for name in narrowed}})
        )
        assert count_invocations(loaded_by_cpus[8], 30, narrowed) == 16
        assert count_invocations(loaded_by_cpus[16], 30, narrowed) == 20


def test_gotmpdir_inside_slot_scratch_and_cleaned_on_signal() -> None:
    """Each go test invocation gets GOTMPDIR inside its slot scratch, and signaling leaves no go-build dir in /tmp."""
    with tempfile.TemporaryDirectory(prefix="go-affected-gotmpdir-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n")
        pkg_a = repo / "tools" / "tillr-gotmp" / "pkga"
        pkg_b = repo / "tools" / "tillr-gotmp" / "pkgb"
        pkg_a.mkdir(parents=True)
        pkg_b.mkdir(parents=True)
        (repo / "tools" / "tillr-gotmp" / "go.mod").write_text(
            "module github.com/mschulkind-oss/tillr-gotmp\n\ngo 1.22\n"
        )
        nonce = _fresh_nonce()
        test_body_template = (
            "package {pkg}\n"
            "import (\n"
            '  "encoding/json"\n'
            '  "os"\n'
            '  "path/filepath"\n'
            '  "testing"\n'
            '  "time"\n'
            ")\n"
            "func TestSlotGoTmpDir(t *testing.T) {{\n"
            '  if t.Name() == "{nonce}" {{ t.Fatal() }}\n'
            "  td := t.TempDir()\n"
            "  exe, err := os.Executable()\n"
            "  if err != nil {{ t.Fatal(err) }}\n"
            "  rec := map[string]any{{\n"
            '    "pkg": "{pkg}",\n'
            '    "gotmpdir": os.Getenv("GOTMPDIR"),\n'
            '    "durable": os.Getenv("YOLO_DURABLE_DIR"),\n'
            '    "tempdir": td,\n'
            '    "exe": exe,\n'
            '    "go_test_pid": os.Getppid(),\n'
            "  }}\n"
            '  if outDir := os.Getenv("GOTMP_REC_DIR"); outDir != "" {{\n'
            "    raw, err := json.Marshal(rec)\n"
            "    if err != nil {{ t.Fatal(err) }}\n"
            '    dest := filepath.Join(outDir, "{pkg}.json")\n'
            '    tmpFile := dest + ".tmp"\n'
            "    if err := os.WriteFile(tmpFile, raw, 0o644); err != nil {{ t.Fatal(err) }}\n"
            "    if err := os.Rename(tmpFile, dest); err != nil {{ t.Fatal(err) }}\n"
            "  }}\n"
            '  if os.Getenv("GOTMP_BLOCK") == "1" {{\n'
            "    for {{\n"
            "      time.Sleep(time.Hour)\n"
            "    }}\n"
            "  }}\n"
            "}}\n"
        )
        (pkg_a / "a_test.go").write_text(test_body_template.format(pkg="pkga", nonce=nonce))
        (pkg_b / "b_test.go").write_text(test_body_template.format(pkg="pkgb", nonce=nonce))

        durable = tmp_path / "durable"
        durable.mkdir()
        lock_dir = tmp_path / "locks"
        gocache = _shared_gocache()
        _precompile_go_test(pkg_a, gocache)
        _precompile_go_test(pkg_b, gocache)

        # Strip TMPDIR and GOTMPDIR to match the production gate environment (where unset
        # GOTMPDIR + TMPDIR previously caused go test to create /tmp/go-build<number>).
        base_env = {k: v for k, v in os.environ.items() if k not in ("TMPDIR", "GOTMPDIR")}
        base_env.update({
            "YOLO_DURABLE_DIR": str(durable),
            "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
            "GO_TEST_SHARDS_CLASS": "ordinary",
            "GOCACHE": str(gocache),
        })

        def cleanup_leaked_tmp(go_build_dir: Path | None) -> None:
            if go_build_dir is not None and go_build_dir.parent == Path("/tmp"):
                shutil.rmtree(go_build_dir, ignore_errors=True)

        # 1. Multi-package invocation check: every go test invocation gets its own GOTMPDIR
        # strictly inside its slot scratch directory, and sequential runs still hit Go's cache.
        rec_dir = tmp_path / "rec-multi"
        rec_dir.mkdir()
        ev_file = tmp_path / "multi-ev.json"
        cmd_both = [
            sys.executable,
            str(ROOT / "tools" / "gate" / "go_affected.py"),
            "--root", str(repo),
            "--changed",
            "tools/tillr-gotmp/pkga/a_test.go",
            "tools/tillr-gotmp/pkgb/b_test.go",
            "--evidence-out", str(ev_file),
        ]
        res1 = subprocess.run(
            cmd_both,
            env={**base_env, "GOTMPDIR": "/tmp", "GOTMP_REC_DIR": str(rec_dir)},
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        assert res1.returncode == 0, res1.stdout
        seen_gotmpdirs: set[str] = set()
        for pkg_name in ("pkga", "pkgb"):
            rec = json.loads((rec_dir / f"{pkg_name}.json").read_text(encoding="utf-8"))
            slot_scratch = Path(rec["durable"])
            gotmpdir_raw = rec["gotmpdir"]
            go_build_dir = Path(rec["exe"]).parents[1]
            try:
                assert gotmpdir_raw, f"{pkg_name}: GOTMPDIR was empty: {rec}"
                gotmpdir = Path(gotmpdir_raw)
                assert gotmpdir != slot_scratch and gotmpdir.is_relative_to(slot_scratch), (
                    f"{pkg_name}: GOTMPDIR {gotmpdir} is not a directory inside slot scratch {slot_scratch}"
                )
                assert Path(rec["tempdir"]).is_relative_to(gotmpdir), rec
                assert go_build_dir.name.startswith("go-build"), go_build_dir
                assert go_build_dir.parent == gotmpdir, (go_build_dir, gotmpdir)
                assert not (Path("/tmp") / go_build_dir.name).exists(), go_build_dir
                assert not gotmpdir.exists(), f"slot scratch {gotmpdir} was not cleared when slot ended"
                seen_gotmpdirs.add(str(gotmpdir))
            finally:
                cleanup_leaked_tmp(go_build_dir)
        assert len(seen_gotmpdirs) == 2, seen_gotmpdirs

        res2 = subprocess.run(
            cmd_both,
            env={**base_env, "GOTMP_REC_DIR": str(rec_dir)},
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        assert res2.returncode == 0, res2.stdout
        ev2 = json.loads(ev_file.read_text(encoding="utf-8"))
        assert all(p["cached"] is True for p in ev2["packages"]), ev2

        # 2. Signal checks: ending a slot's go test by signal (SIGTERM to go_affected.py,
        # SIGKILL directly to go test, and SIGKILL to go_affected.py) leaves no go-build dir in /tmp.
        cmd_one = [
            sys.executable,
            str(ROOT / "tools" / "gate" / "go_affected.py"),
            "--root", str(repo),
            "--changed", "tools/tillr-gotmp/pkga/a_test.go",
        ]

        def start_blocking_slot(tag: str) -> subprocess.Popen[str]:
            s_rec_dir = tmp_path / f"rec-{tag}"
            s_rec_dir.mkdir()
            s_durable = tmp_path / f"durable-{tag}"
            s_durable.mkdir()
            return subprocess.Popen(
                cmd_one,
                env={
                    **base_env,
                    "YOLO_DURABLE_DIR": str(s_durable),
                    "GOTMP_REC_DIR": str(s_rec_dir),
                    "GOTMP_BLOCK": "1",
                },
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )

        def wait_slot_ready(tag: str, proc: subprocess.Popen[str]) -> tuple[dict, Path]:
            rec_file = tmp_path / f"rec-{tag}" / "pkga.json"
            while not rec_file.is_file():
                assert proc.poll() is None, (
                    f"go_affected ({tag}) exited early: {proc.stdout.read() if proc.stdout else ''}"
                )
                time.sleep(0.02)
            rec = json.loads(rec_file.read_text(encoding="utf-8"))
            return rec, Path(rec["exe"]).parents[1]

        def wait_pid_gone(pid: int) -> None:
            while True:
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    return
                time.sleep(0.02)

        # Launch all three signal cases concurrently so go list runs in parallel across them.
        proc_term = start_blocking_slot("sigterm-runner")
        proc_child_kill = start_blocking_slot("sigkill-gotest")
        proc_kill = start_blocking_slot("sigkill-runner")
        build_dir_term = build_dir_child = build_dir_kill = None
        try:
            rec_term, build_dir_term = wait_slot_ready("sigterm-runner", proc_term)
            rec_child_kill, build_dir_child = wait_slot_ready("sigkill-gotest", proc_child_kill)
            rec_kill, build_dir_kill = wait_slot_ready("sigkill-runner", proc_kill)

            # 2a. SIGTERM to go_affected.py while go test is active.
            proc_term.send_signal(signal.SIGTERM)
            out_term, _ = proc_term.communicate()
            assert proc_term.returncode != 0, out_term
            wait_pid_gone(int(rec_term["go_test_pid"]))
            assert build_dir_term.name.startswith("go-build"), build_dir_term
            assert build_dir_term.parent != Path("/tmp"), build_dir_term
            assert not (Path("/tmp") / build_dir_term.name).exists(), build_dir_term
            assert not build_dir_term.exists(), f"go-build dir survived slot end: {build_dir_term}"
            assert not Path(rec_term["gotmpdir"]).exists(), rec_term

            # 2b. SIGKILL directly to the slot's go test process.
            os.kill(int(rec_child_kill["go_test_pid"]), signal.SIGKILL)
            out_child, _ = proc_child_kill.communicate()
            assert proc_child_kill.returncode != 0, out_child
            assert build_dir_child.name.startswith("go-build"), build_dir_child
            assert build_dir_child.parent != Path("/tmp"), build_dir_child
            assert not (Path("/tmp") / build_dir_child.name).exists(), build_dir_child
            assert not build_dir_child.exists(), f"go-build dir survived slot end: {build_dir_child}"
            assert not Path(rec_child_kill["gotmpdir"]).exists(), rec_child_kill

            # 2c. SIGKILL to go_affected.py (lifeline subreaper ends go test by SIGKILL).
            proc_kill.kill()
            proc_kill.communicate()
            wait_pid_gone(int(rec_kill["go_test_pid"]))
            assert build_dir_kill.name.startswith("go-build"), build_dir_kill
            assert build_dir_kill.parent != Path("/tmp"), build_dir_kill
            assert not (Path("/tmp") / build_dir_kill.name).exists(), build_dir_kill
        finally:
            for proc in (proc_term, proc_child_kill, proc_kill):
                if proc.poll() is None:
                    proc.kill()
                    proc.wait()
            for bdir in (build_dir_term, build_dir_child, build_dir_kill):
                cleanup_leaked_tmp(bdir)


def test_binary_watchdog_constants_and_sample_rule() -> None:
    """go_affected reads watchdog constants from tools/tillr-gate/watchdog.go and applies its sample rule."""
    spec = go_affected._load_watchdog_constants()
    assert spec == (1.0, 60), spec
    assert go_affected._load_watchdog_constants(ROOT / "tools" / "tillr-gate" / "missing.go") is None

    _interval_s, silent_samples = spec

    # 59 silent samples after initialization do not stall; the 60th does.
    w = go_affected._BinaryStallWatchdog(silent_samples)
    assert w.observe_tree({(100, "1"): 500}, 500, False) is False
    for _ in range(silent_samples - 1):
        assert w.observe_tree({(100, "1"): 500}, 500, False) is False
    assert w.silent_samples == silent_samples - 1
    assert w.observe_tree({(100, "1"): 500}, 500, False) is True
    assert w.silent_samples == silent_samples

    # CPU growth resets silent_samples to 0.
    w_cpu = go_affected._BinaryStallWatchdog(silent_samples)
    assert w_cpu.observe_tree({(100, "1"): 500}, 500, False) is False
    for _ in range(silent_samples - 1):
        assert w_cpu.observe_tree({(100, "1"): 500}, 500, False) is False
    assert w_cpu.observe_tree({(100, "1"): 501}, 501, False) is False
    assert w_cpu.silent_samples == 0

    # Child membership/runtime change resets silent_samples even below the high-water total.
    w_mem = go_affected._BinaryStallWatchdog(silent_samples)
    assert w_mem.observe_tree({(100, "1"): 1000}, 1000, False) is False
    for n in range(silent_samples * 2):
        assert w_mem.observe_tree({(100, "1"): 500, (200 + n, str(n)): 10}, 510, False) is False
    assert w_mem.silent_samples == 0

    # Unavailable accounting disables detection permanently without signalling.
    w_err = go_affected._BinaryStallWatchdog(silent_samples)
    assert w_err.observe_tree({}, 0, False, "permission denied") is False
    assert w_err.unavailable != ""
    for _ in range(silent_samples * 2):
        assert w_err.observe_tree({(100, "1"): 500}, 500, False) is False


def test_stalled_go_test_binary_receives_sigquit_and_busy_binary_is_not_signalled() -> None:
    """A stalled go test binary receives SIGQUIT with stacks and fails; CPU-active binaries pass."""
    stall_pkg = "github.com/mschulkind-oss/tillr-stall-sigquit"
    busy_pkg = "github.com/mschulkind-oss/tillr-busy-cpu"
    child_pkg = "github.com/mschulkind-oss/tillr-busy-child"
    with tempfile.TemporaryDirectory(prefix="go-affected-sigquit-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        (repo / "tools" / "gate").mkdir(parents=True)
        (repo / "tools" / "tillr-gate").mkdir(parents=True)
        (repo / "tools" / "gate" / "go-test-edges.json").write_text("{}\n")
        shutil.copy2(ROOT / "tools" / "gate" / "go_affected.py", repo / "tools" / "gate" / "go_affected.py")
        # Keep the 60-sample threshold and accelerate the sample interval so 60 samples take ~1.5s.
        (repo / "tools" / "tillr-gate" / "watchdog.go").write_text(
            "package main\n"
            'import "time"\n'
            "const WatchdogSampleInterval = 25 * time.Millisecond\n"
            "const WatchdogSilentSamples = 60\n",
            encoding="utf-8",
        )

        # 1. Stalled binary: blocks on a channel receive with zero CPU usage.
        stall_dir = repo / "tools" / "tillr-stall-sigquit"
        stall_dir.mkdir(parents=True)
        (stall_dir / "go.mod").write_text(f"module {stall_pkg}\n\ngo 1.22\n")
        (stall_dir / "stall_test.go").write_text(
            "package main\n"
            "import (\n"
            '  "testing"\n'
            '  "time"\n'
            ")\n"
            "func TestStalledBinaryBlocksForever(t *testing.T) {\n"
            "  ch := make(chan struct{})\n"
            "  go func() {\n"
            "    time.Sleep(time.Hour)\n"
            "    close(ch)\n"
            "  }()\n"
            "  <-ch\n"
            "}\n",
            encoding="utf-8",
        )

        # 2. Busy binary: burns CPU past the 60-sample window (~2.5s > ~1.5s) and exits 0.
        busy_dir = repo / "tools" / "tillr-busy-cpu"
        busy_dir.mkdir(parents=True)
        (busy_dir / "go.mod").write_text(f"module {busy_pkg}\n\ngo 1.22\n")
        (busy_dir / "busy_test.go").write_text(
            "package main\n"
            "import (\n"
            '  "testing"\n'
            '  "time"\n'
            ")\n"
            "func TestBusyBinaryKeepsRunning(t *testing.T) {\n"
            "  deadline := time.Now().Add(2500 * time.Millisecond)\n"
            "  var sink uint64\n"
            "  for time.Now().Before(deadline) {\n"
            "    for i := 0; i < 10000; i++ {\n"
            "      sink += uint64(i)\n"
            "    }\n"
            "  }\n"
            "  if sink == 0 {\n"
            '    t.Fatal("unreachable")\n'
            "  }\n"
            "}\n",
            encoding="utf-8",
        )

        # 3. Busy child: Go test binary itself blocks in Wait(), while its child burns CPU.
        child_dir = repo / "tools" / "tillr-busy-child"
        child_dir.mkdir(parents=True)
        (child_dir / "go.mod").write_text(f"module {child_pkg}\n\ngo 1.22\n")
        (child_dir / "child_test.go").write_text(
            "package main\n"
            "import (\n"
            '  "os/exec"\n'
            '  "testing"\n'
            ")\n"
            "func TestBusyChildKeepsParentAlive(t *testing.T) {\n"
            '  cmd := exec.Command("python3", "-c", "import time; end = time.monotonic() + 2.5\\nwhile time.monotonic() < end: pass")\n'
            "  if out, err := cmd.CombinedOutput(); err != nil {\n"
            '    t.Fatalf("child failed: %v: %s", err, out)\n'
            "  }\n"
            "}\n",
            encoding="utf-8",
        )

        gocache = _shared_gocache()
        _precompile_go_test(stall_dir, gocache)
        _precompile_go_test(busy_dir, gocache)
        _precompile_go_test(child_dir, gocache)

        durable = tmp_path / "durable"
        durable.mkdir()
        env = {
            **os.environ,
            "YOLO_DURABLE_DIR": str(durable),
            "GO_TEST_SHARDS_LOCK_DIR": str(tmp_path / "locks"),
            "GO_TEST_SHARDS_CLASS": "ordinary",
            "GOCACHE": str(gocache),
        }
        runner = str(repo / "tools" / "gate" / "go_affected.py")

        stall_res = subprocess.run(
            [sys.executable, runner, "--root", str(repo), "--changed", "tools/tillr-stall-sigquit/stall_test.go"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        assert stall_res.returncode != 0, stall_res.stdout
        assert "SIGQUIT: quit" in stall_res.stdout, stall_res.stdout
        assert "goroutine " in stall_res.stdout, stall_res.stdout
        assert "TestStalledBinaryBlocksForever" in stall_res.stdout, stall_res.stdout

        busy_res = subprocess.run(
            [sys.executable, runner, "--root", str(repo), "--changed", "tools/tillr-busy-cpu/busy_test.go"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        assert busy_res.returncode == 0, busy_res.stdout
        assert "SIGQUIT" not in busy_res.stdout, busy_res.stdout
        assert f"go-affected: pass {busy_pkg} TestBusyBinaryKeepsRunning " in busy_res.stdout, busy_res.stdout

        child_res = subprocess.run(
            [sys.executable, runner, "--root", str(repo), "--changed", "tools/tillr-busy-child/child_test.go"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        assert child_res.returncode == 0, child_res.stdout
        assert "SIGQUIT" not in child_res.stdout, child_res.stdout
        assert f"go-affected: pass {child_pkg} TestBusyChildKeepsParentAlive " in child_res.stdout, child_res.stdout


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", action="append", default=[], help="Run only named test case(s)")
    parser.add_argument("--jobs", type=int, default=0, help="Worker process count (default: auto)")
    args = parser.parse_args(argv)

    # Warm/resolve GOCACHE and load ROOT graph once before forking workers.
    _shared_gocache()
    root_graph = go_affected.load_tillr_graph(ROOT)
    # Ordered longest-first so critical-path cases start immediately in the worker pool.
    all_cases: list[tuple[str, Callable[[], None]]] = [
        ("test_no_leftover_processes_after_check_finishes", test_no_leftover_processes_after_check_finishes),
        ("test_cfix_regate_reuses_unchanged_package_verdicts", test_cfix_regate_reuses_unchanged_package_verdicts),
        ("test_gotmpdir_inside_slot_scratch_and_cleaned_on_signal", test_gotmpdir_inside_slot_scratch_and_cleaned_on_signal),
        ("test_check_declarations_cover_selection_inputs", test_check_declarations_cover_selection_inputs),
        ("test_heavy_failures_replay", lambda: test_heavy_failures_replay(root_graph)),
        ("test_selection_rules", lambda: test_selection_rules(root_graph)),
        ("test_drain_subpackage_isolation", lambda: test_drain_subpackage_isolation(root_graph)),
        ("test_cross_module_source_scanning_edges", lambda: test_cross_module_source_scanning_edges(root_graph)),
        ("test_candidate_gate_fails_on_undeclared_go_module", test_candidate_gate_fails_on_undeclared_go_module),
        ("test_edge_cache_invalidation", test_edge_cache_invalidation),
        ("test_edges_file_honest_and_env_read", test_edges_file_honest_and_env_read),
        ("test_stalled_run_prints_nothing_without_test_events", test_stalled_run_prints_nothing_without_test_events),
        ("test_scoped_read_tests_execution_cache_and_sharding", test_scoped_read_tests_execution_cache_and_sharding),
        ("test_scoped_build_tests_execution_cache_and_sharding", test_scoped_build_tests_execution_cache_and_sharding),
        ("test_concurrent_runs_do_not_share_scratch_and_preserve_cache", test_concurrent_runs_do_not_share_scratch_and_preserve_cache),
        ("test_streams_progress_lines_before_run_ends", test_streams_progress_lines_before_run_ends),
        ("test_suite_gocache_and_runner_helpers", test_suite_gocache_and_runner_helpers),
        ("test_failed_module_floor_fallback", test_failed_module_floor_fallback),
        ("test_subreaper_reaps_orphans_while_command_runs", test_subreaper_reaps_orphans_while_command_runs),
        ("test_discover_modules", test_discover_modules),
        ("test_build_env_strips_outer_gate_vars", test_build_env_strips_outer_gate_vars),
        ("test_failure_refusal_names_packages_and_tests_far_above_tail", test_failure_refusal_names_packages_and_tests_far_above_tail),
        ("test_private_gitconfig_disables_auto_maintenance_and_bypasses_block_git_shim", test_private_gitconfig_disables_auto_maintenance_and_bypasses_block_git_shim),
        ("test_max_shards_scales_with_cpu_count", test_max_shards_scales_with_cpu_count),
        ("test_binary_watchdog_constants_and_sample_rule", test_binary_watchdog_constants_and_sample_rule),
        ("test_stalled_go_test_binary_receives_sigquit_and_busy_binary_is_not_signalled", test_stalled_go_test_binary_receives_sigquit_and_busy_binary_is_not_signalled),
    ]
    if args.case:
        wanted = set(args.case)
        unknown = sorted(wanted - {name for name, _ in all_cases})
        if unknown:
            parser.error(f"unknown --case: {', '.join(unknown)}")
        cases = [(name, fn) for name, fn in all_cases if name in wanted]
    else:
        cases = all_cases

    default_jobs = max(4, min(8, (os.cpu_count() or 8) // 4))
    workers = args.jobs if args.jobs > 0 else min(len(cases), default_jobs)
    rc = _run_cases(cases, max_workers=workers, emit=lambda msg: print(msg, flush=True))
    if rc != 0:
        return rc
    print("PASS: all go-affected tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
