#!/usr/bin/env python3
"""Tests for tools/gate/go_affected.py, go-test-edges.json, and fork-check integration."""
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
fork_check = load_module("fork_check", ROOT / "tools" / "fork-check.py")


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
for _ in range(100):
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
print(f"orphan {grand_pid} remained zombie while guarded child ran", file=sys.stderr)
sys.exit(1)
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

    # 5. Unplaceable go.mod change triggers floor for module and replacing modules.
    sel = go_affected.select_affected_packages(ROOT, ["tools/tillr-drain/go.mod"], graph=root_graph)
    assert set(sel["floor_modules"]) >= {
        "tools/tillr-drain",
        "tools/tillr-land",
        "tools/tillr-store",
        "tools/tillr-view",
        "tools/tillr-watch",
    }, sel["floor_modules"]
    assert all(item["reason"] == "floor" for item in sel["selected"]), sel["selected"]

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
    assert by_pkg["github.com/mschulkind-oss/tillr-view"]["reason"] == "edge", by_pkg

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
        ("bin/pi-forks", {"github.com/mschulkind-oss/tillr-drain/drain"}),
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
        ("tests/pi-patch-series.test.sh", {"github.com/mschulkind-oss/tillr-gate"}),
        ("yolo-packs/fork-pins/pin.json", {"github.com/mschulkind-oss/tillr-gate"}),
    ]:
        sel = go_affected.select_affected_packages(ROOT, [changed_path], graph=root_graph)
        pkgs = {item["import_path"] for item in sel["selected"]}
        assert expected_pkgs <= pkgs, f"{changed_path}: missing {expected_pkgs - pkgs} in {pkgs}"
        for item in sel["selected"]:
            assert item["selected_tests"] is None, f"unscoped read {changed_path} narrowed {item}"

    # 10. Scoped read_tests on tools/fork-check.py (alone or with tests/fork-check.test.py)
    # selects only the reading packages and narrows each to its reader tests.
    expected_fc_pkgs = {
        "github.com/mschulkind-oss/tillr-drain/drain",
        "github.com/mschulkind-oss/tillr-gate",
    }
    for fc_changed in (
        ["tools/fork-check.py"],
        ["tools/fork-check.py", "tests/fork-check.test.py"],
    ):
        sel = go_affected.select_affected_packages(ROOT, fc_changed, graph=root_graph)
        by_pkg = {item["import_path"]: item for item in sel["selected"]}
        assert set(by_pkg) == expected_fc_pkgs, (fc_changed, set(by_pkg))
        for ip in expected_fc_pkgs:
            assert by_pkg[ip]["reason"] == "edge", by_pkg[ip]
            assert isinstance(by_pkg[ip]["selected_tests"], list) and by_pkg[ip]["selected_tests"], by_pkg[ip]
            assert "TestEdgeInputs" not in by_pkg[ip]["selected_tests"], by_pkg[ip]
        assert "TestTheComposedPipelineRunsEndToEndOnAFixture" in by_pkg[
            "github.com/mschulkind-oss/tillr-drain/drain"
        ]["selected_tests"], by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]
        assert len(by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["selected_tests"]) == 12, (
            by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]
        )
        assert len(by_pkg["github.com/mschulkind-oss/tillr-gate"]["selected_tests"]) == 9, (
            by_pkg["github.com/mschulkind-oss/tillr-gate"]
        )

    # 11. Pairing tools/fork-check.py with a Go package edit, a builds-edge edit, an unscoped
    # reads edit, or a module floor trigger widens affected packages back to whole-package
    # execution (selected_tests=None) while unaffected reading packages stay narrowed.
    sel_go = go_affected.select_affected_packages(
        ROOT, ["tools/fork-check.py", "tools/tillr-drain/drain/loop.go"], graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_go["selected"]}
    assert by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["reason"] == "changed", by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["selected_tests"] is None, by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["reason"] == "import", by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["selected_tests"] is None, by_pkg
    assert len(by_pkg["github.com/mschulkind-oss/tillr-gate"]["selected_tests"]) == 9, by_pkg

    sel_builds = go_affected.select_affected_packages(
        ROOT, ["tools/fork-check.py", "tools/tillr-cli/main.go"], graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_builds["selected"]}
    assert "github.com/mschulkind-oss/tillr-drain" not in by_pkg, by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["selected_tests"] is None, by_pkg
    assert len(by_pkg["github.com/mschulkind-oss/tillr-gate"]["selected_tests"]) == 9, by_pkg

    sel_unscoped = go_affected.select_affected_packages(
        ROOT, ["tools/fork-check.py", "config/tillr/backend-policy.json"], graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_unscoped["selected"]}
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["selected_tests"] is None, by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["selected_tests"] is None, by_pkg
    assert len(by_pkg["github.com/mschulkind-oss/tillr-gate"]["selected_tests"]) == 9, by_pkg

    sel_floor = go_affected.select_affected_packages(
        ROOT, ["tools/fork-check.py", "tools/tillr-drain/go.mod"], graph=root_graph
    )
    by_pkg = {item["import_path"]: item for item in sel_floor["selected"]}
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["selected_tests"] is None, by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["selected_tests"] is None, by_pkg

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


def _strip_go_comments(src: str) -> str:
    out: list[str] = []
    i, n = 0, len(src)
    while i < n:
        c = src[i]
        if c == "/" and i + 1 < n and src[i + 1] == "/":
            j = src.find("\n", i + 2)
            if j < 0:
                break
            out.append("\n")
            i = j + 1
        elif c == "/" and i + 1 < n and src[i + 1] == "*":
            j = src.find("*/", i + 2)
            if j < 0:
                break
            out.append(" " * (j + 2 - i))
            i = j + 2
        elif c == "`":
            j = src.find("`", i + 1)
            if j < 0:
                out.append(src[i:])
                break
            out.append(src[i:j + 1])
            i = j + 1
        elif c in ('"', "'"):
            quote = c
            j = i + 1
            while j < n:
                if src[j] == "\\":
                    j += 2
                    continue
                if src[j] == quote:
                    j += 1
                    break
                j += 1
            out.append(src[i:j])
            i = j
        else:
            out.append(c)
            i += 1
    return "".join(out)


def _parse_go_funcs(src: str) -> dict[str, str]:
    clean = _strip_go_comments(src)
    funcs: dict[str, str] = {}
    for m in re.finditer(r"^func\s+(?:\([^)]*\)\s*)?(\w+)\s*\(", clean, re.MULTILINE):
        name = m.group(1)
        brace = clean.find("{", m.end())
        if brace < 0:
            continue
        depth = 0
        i = brace
        n = len(clean)
        while i < n:
            c = clean[i]
            if c == "`":
                j = clean.find("`", i + 1)
                i = n if j < 0 else j + 1
                continue
            if c in ('"', "'"):
                q = c
                i += 1
                while i < n:
                    if clean[i] == "\\":
                        i += 2
                        continue
                    if clean[i] == q:
                        i += 1
                        break
                    i += 1
                continue
            if c == "{":
                depth += 1
            elif c == "}":
                depth -= 1
                if depth == 0:
                    funcs[name] = funcs.get(name, "") + "\n" + clean[brace:i + 1]
                    break
            i += 1
    return funcs


def _scan_fork_check_reader_tests(root: Path, modules: list[str]) -> dict[str, list[str]]:
    """Return {pkg_import: [Test* names]} for every Go test reading/executing checkout tools/fork-check.py."""
    readers_by_pkg: dict[str, list[str]] = {}
    for mod in modules:
        mod_name = mod.split("/", 1)[1]
        mod_root = root / mod
        by_pkg: dict[str, list[Path]] = {}
        for tf in sorted(mod_root.rglob("*_test.go")):
            rel = tf.parent.relative_to(mod_root).as_posix()
            pkg = (
                f"github.com/mschulkind-oss/{mod_name}"
                if rel == "."
                else f"github.com/mschulkind-oss/{mod_name}/{rel}"
            )
            by_pkg.setdefault(pkg, []).append(tf)
        for pkg, files in sorted(by_pkg.items()):
            funcs: dict[str, str] = {}
            top_tests: list[str] = []
            for tf in files:
                raw = tf.read_text(encoding="utf-8")
                for tname in go_affected.TOP_LEVEL_TEST_RE.findall(raw):
                    if tname not in top_tests:
                        top_tests.append(tname)
                for fname, fbody in _parse_go_funcs(raw).items():
                    funcs[fname] = funcs.get(fname, "") + "\n" + fbody

            direct: set[str] = set()
            clones_repo: set[str] = set()
            execs_fc: set[str] = set()
            for fn, fb in funcs.items():
                if "repoRoot(" in fb and '"clone"' in fb:
                    clones_repo.add(fn)
                if (
                    '"tools/fork-check.py"' in fb
                    and ("exec.Command" in fb or '"--gate-arg"' in fb)
                    and "writeForkFile" not in fb
                    and "os.WriteFile" not in fb
                ):
                    execs_fc.add(fn)
                if "fork-check.py" not in fb:
                    continue
                if (
                    re.search(r'repositoryFile\([^)]*"tools/fork-check\.py"', fb)
                    or re.search(r'filepath\.Join\([^)]*"\.\."[^)]*"fork-check\.py"', fb)
                    or (
                        ("repositoryFile(" in fb or "repoRoot(" in fb)
                        and "os.ReadFile(" in fb
                        and '"tools/fork-check.py"' in fb
                    )
                    or re.search(r'(?:os\.ReadFile|exec\.Command)\([^;\n]*"\.\./[^"\n]*fork-check\.py"', fb)
                ):
                    direct.add(fn)

            def callers_of(seed: set[str]) -> set[str]:
                reach = set(seed)
                progressed = True
                while progressed:
                    progressed = False
                    for fn, fb in funcs.items():
                        if fn in reach:
                            continue
                        if any(re.search(rf"\b{re.escape(cal)}\s*\(", fb) for cal in reach):
                            reach.add(fn)
                            progressed = True
                return reach

            readers = callers_of(direct) | (callers_of(clones_repo) & callers_of(execs_fc))
            found = [t for t in top_tests if t in readers]
            if found:
                readers_by_pkg[pkg] = found
    return readers_by_pkg


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
        read_tests = edges[pkg_import].get("read_tests", {})
        assert isinstance(read_tests, dict), f"{pkg_import}: read_tests must be a dict"
        pkg_test_names = set(go_affected.scan_package_tests(ROOT, {
            "import_path": pkg_import,
            "module": f"tools/{rel.split('/', 1)[0]}",
            "dir_rel": f"tools/{rel}",
        }))
        for rule, names in read_tests.items():
            assert rule in read_rules, f"{pkg_import}: read_tests key {rule!r} missing from reads"
            assert isinstance(names, list) and names, f"{pkg_import}: read_tests[{rule!r}] must be non-empty"
            assert "TestEdgeInputs" not in names, (
                f"{pkg_import}: omit TestEdgeInputs from read_tests[{rule!r}]; go_affected appends it"
            )
            missing_tests = [n for n in names if n not in pkg_test_names]
            assert not missing_tests, (
                f"{pkg_import}: read_tests[{rule!r}] names tests absent from package: {missing_tests}"
            )

    # Every Go test across all tillr modules that reads or executes the checkout's
    # tools/fork-check.py must be covered by reads and read_tests["tools/fork-check.py"].
    actual_fc_readers = _scan_fork_check_reader_tests(ROOT, modules)
    assert set(actual_fc_readers) == {
        "github.com/mschulkind-oss/tillr-drain/drain",
        "github.com/mschulkind-oss/tillr-gate",
    }, actual_fc_readers
    for pkg_import, actual_tests in actual_fc_readers.items():
        assert pkg_import in edges, f"{pkg_import} missing from go-test-edges.json"
        assert "tools/fork-check.py" in edges[pkg_import].get("reads", []), (
            f"{pkg_import}: tools/fork-check.py missing from reads"
        )
        declared_rt = edges[pkg_import].get("read_tests", {}).get("tools/fork-check.py")
        assert declared_rt is not None, (
            f"{pkg_import}: tools/fork-check.py missing from read_tests"
        )
        missing_readers = set(actual_tests) - set(declared_rt)
        extra_readers = set(declared_rt) - set(actual_tests)
        assert not missing_readers, (
            f"{pkg_import}: read_tests['tools/fork-check.py'] missing reader tests {sorted(missing_readers)}"
        )
        assert not extra_readers, (
            f"{pkg_import}: read_tests['tools/fork-check.py'] lists non-reader tests {sorted(extra_readers)}"
        )

    # load_edges refuses invalid read_tests declarations.
    with tempfile.TemporaryDirectory(prefix="go-affected-edges-val-", dir=scratch_root()) as tmp:
        bad_repo = Path(tmp)
        (bad_repo / "tools" / "gate").mkdir(parents=True)
        for bad_edges in (
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": ["bad"]}},
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"other.py": ["TestX"]}}},
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": []}}},
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": [123]}}},
            {"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": [""]}}},
        ):
            (bad_repo / "tools" / "gate" / "go-test-edges.json").write_text(json.dumps(bad_edges))
            try:
                go_affected.load_edges(bad_repo)
            except ValueError:
                pass
            else:
                raise AssertionError(f"load_edges accepted invalid read_tests: {bad_edges}")


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
        cases = fork_check.failure_cases(out)
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
        cases = fork_check.failure_cases(out)
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


def test_fork_check_integration(root_graph: dict) -> None:
    assert "go-affected" in fork_check.CHECK_KIND_GROUP
    assert fork_check.CHECK_TIER["go-affected"] == fork_check.TIER_LANDING
    assert "go-affected" in fork_check.CHECK_COST_ESTIMATE
    assert "go-affected-suite" in fork_check.CHECK_KIND_GROUP
    assert fork_check.CHECK_TIER["go-affected-suite"] == fork_check.TIER_LANDING

    # Non-Go read edges for tillr-gate and tillr-drain/drain select those packages.
    gate_sel = go_affected.select_affected_packages(ROOT, ["tools/fork-check.py"], graph=root_graph)
    assert any(p["import_path"] == "github.com/mschulkind-oss/tillr-gate" for p in gate_sel["selected"]), gate_sel
    checks_sel = go_affected.select_affected_packages(ROOT, ["tools/gate/checks/go-build.py"], graph=root_graph)
    assert any(p["import_path"] == "github.com/mschulkind-oss/tillr-gate" for p in checks_sel["selected"]), checks_sel
    mirror_sel = go_affected.select_affected_packages(ROOT, ["tools/pack-mirror-links.py"], graph=root_graph)
    assert any(p["import_path"] == "github.com/mschulkind-oss/tillr-drain/drain" for p in mirror_sel["selected"]), mirror_sel

    with tempfile.TemporaryDirectory(prefix="fc-go-affected-", dir=scratch_root()) as tmp:
        state = Path(tmp) / "state.json"
        # build_plan(tier=None) keeps go-affected without --drop-module so replay/preflight/registry see it.
        full = fork_check.build_plan(
            ROOT, [], fork_check.Record(state), ["tools/tillr-drain/drain/loop.go"], None, True
        )
        assert any(c.kind == "go-affected" for c in full.checks), [c.kind for c in full.checks]
        sub_full = fork_check.build_plan(
            ROOT, [], fork_check.Record(state), ["tools/tillr/event/event.go"], None, True
        )
        ga_full = next(c for c in sub_full.checks if c.kind == "go-affected")
        assert "--drop-module" not in ga_full.argv, ga_full.argv

        # Landing tier keeps go-affected.
        landing = fork_check.build_plan(
            ROOT, [], fork_check.Record(state), ["tools/tillr-drain/drain/loop.go"], None, True,
            tier=fork_check.TIER_LANDING,
        )
        assert any(c.kind == "go-affected" for c in landing.checks), [c.kind for c in landing.checks]

        # Rolling tier (trunk proof) drops go-affected and states plainly that it does not
        # apply there without stamping a check_id into skipped_checks (which runHeavyLegacy
        # turns into unavailable), while leaving fork patch-branch entries untouched.
        trunk_changed = subprocess.check_output(
            ["git", "ls-files"], cwd=ROOT, text=True
        ).splitlines()
        series = fork_check.read_series(ROOT)
        for via_build_plan in (True, False):
            rolling = fork_check.build_plan(
                ROOT, series, fork_check.Record(state),
                trunk_changed, "--all", True, audit_upstreams=False,
                tier=fork_check.TIER_ROLLING if via_build_plan else None,
            )
            if not via_build_plan:
                fork_check.apply_tier(rolling, fork_check.TIER_ROLLING)
            assert not any(c.kind == "go-affected" for c in rolling.checks), [c.kind for c in rolling.checks]
            rolling_spans_path = Path(tmp) / f"rolling-spans-{via_build_plan}.json"
            fork_check.write_gate_spans(
                str(rolling_spans_path), rolling, ROOT, 1, "PASS",
                "0" * 40, "1" * 40, trunk_changed, 1, 1, 0.1,
            )
            rolling_spans = json.loads(rolling_spans_path.read_text(encoding="utf-8"))
            heavy_unavailable = [
                skip["check_id"] for skip in rolling_spans["skipped_checks"]
                if not skip["owed"] and skip["check_id"]
            ]
            assert "go-affected/affected-tillr-go-test-packages-pass" not in heavy_unavailable, (
                heavy_unavailable
            )
            assert any(cid.startswith("patch-branch-inv2/") for cid in heavy_unavailable), heavy_unavailable
            assert any(cid.startswith("patch-branch-inv3/") for cid in heavy_unavailable), heavy_unavailable
            go_affected_skips = [
                skip for skip in rolling_spans["skipped_checks"]
                if "go-affected" in skip["label"]
            ]
            assert len(go_affected_skips) == 1, rolling_spans["skipped_checks"]
            assert go_affected_skips[0]["check_id"] == "", go_affected_skips[0]
            assert go_affected_skips[0]["planned"] is False and go_affected_skips[0]["owed"] is False, (
                go_affected_skips[0]
            )
            assert "does not apply" in go_affected_skips[0]["reason"], go_affected_skips[0]
            assert "whole Go module suites" in go_affected_skips[0]["reason"], go_affected_skips[0]

        # Even if `go` is absent on the host, the rolling tier still records go-affected as
        # not applicable rather than unavailable.
        orig_which = fork_check.shutil.which
        try:
            fork_check.shutil.which = lambda name: None if name == "go" else orig_which(name)
            rolling_no_go = fork_check.build_plan(
                ROOT, [], fork_check.Record(state), ["tools/tillr-drain/drain/loop.go"],
                "--all", True, audit_upstreams=False, tier=fork_check.TIER_ROLLING,
            )
        finally:
            fork_check.shutil.which = orig_which
        assert not any(s.kind == "go-affected" for s in rolling_no_go.skips), [
            (s.kind, s.label, s.planned) for s in rolling_no_go.skips
        ]

        # Substrate change passes --drop-module tools/tillr to go-affected under landing tier.
        sub = fork_check.build_plan(
            ROOT, [], fork_check.Record(state), ["tools/tillr/event/event.go"], None, True,
            tier=fork_check.TIER_LANDING,
        )
        ga = next(c for c in sub.checks if c.kind == "go-affected")
        assert "--drop-module" in ga.argv and "tools/tillr" in ga.argv, ga.argv

        # Changing go_affected.py, go-test-edges.json, or tests/go-affected.test.py plans go-affected-suite.
        for trigger in (
            "tools/gate/go_affected.py",
            "tools/gate/go-test-edges.json",
            "tests/go-affected.test.py",
        ):
            suite_plan = fork_check.build_plan(
                ROOT, [], fork_check.Record(state), [trigger], None, True,
                tier=fork_check.TIER_LANDING,
            )
            assert any(c.kind == "go-affected-suite" for c in suite_plan.checks), trigger

        # run_gate(--explain) with tier=None skips go-affected, whereas --tier landing plans it.
        repo = Path(tmp) / "explain-repo"
        (repo / "tools" / "tillr-watch").mkdir(parents=True)
        (repo / "tools" / "gate").mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "fork-check.py", repo / "tools" / "fork-check.py")
        shutil.copy2(ROOT / "tools" / "gate" / "classify.py", repo / "tools" / "gate" / "classify.py")
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

        p_full = subprocess.Popen(
            [sys.executable, "tools/fork-check.py", "--explain", "--since", "HEAD~1"],
            cwd=repo, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        p_rolling = subprocess.Popen(
            [sys.executable, "tools/fork-check.py", "--explain", "--all", "--tier", "rolling"],
            cwd=repo, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        p_landing = subprocess.Popen(
            [sys.executable, "tools/fork-check.py", "--explain", "--tier", "landing", "--since", "HEAD~1"],
            cwd=repo, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        p_reg = subprocess.Popen(
            [sys.executable, str(ROOT / "tools" / "fork-check.py"), "--registry-json"],
            cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        full_out, full_err = p_full.communicate(timeout=120)
        roll_out, roll_err = p_rolling.communicate(timeout=120)
        land_out, land_err = p_landing.communicate(timeout=120)
        reg_out, reg_err = p_reg.communicate(timeout=120)

        assert p_full.returncode == 0, full_err
        assert "would run  go-affected " not in full_out, full_out
        assert "would not run          go-affected (affected tillr Go test packages pass)" in full_out, full_out

        assert p_rolling.returncode == 0, roll_err
        assert "would run  go-affected " not in roll_out, roll_out
        assert "would skip" not in roll_out, roll_out
        assert "would not run          go-affected (affected tillr Go test packages pass)" in roll_out, roll_out
        assert "does not apply on the rolling tier because whole Go module suites run fresh" in roll_out, roll_out

        assert p_landing.returncode == 0, land_err
        assert "would run  go-affected " in land_out, land_out
        landing_repo = fork_check.build_plan(
            repo, [], fork_check.Record(state), ["tools/tillr-watch/main.go"], None, True,
            tier=fork_check.TIER_LANDING,
        )
        landing_ga = next(c for c in landing_repo.checks if c.kind == "go-affected")
        landing_env = {
            **os.environ,
            "GOCACHE": str(_shared_gocache()),
            "YOLO_DURABLE_DIR": str(Path(tmp) / "durable"),
            "GO_TEST_SHARDS_LOCK_DIR": str(Path(tmp) / "locks"),
            "GO_TEST_SHARDS_CLASS": "ordinary",
        }
        rc, out, _ = fork_check.run_landing_go_check(
            repo, landing_ga, landing_env, landing_ga.drop, None
        )
        assert rc == 0, out
        assert "go-affected: 1 package(s)" in out, out

    # registry-json exports go-affected cleanly.
    assert p_reg.returncode == 0, reg_err
    reg = json.loads(reg_out)
    assert any(d["id"] == "go-affected/affected-tillr-go-test-packages-pass" for d in reg["declarations"]), (
        [d["id"] for d in reg["declarations"]]
    )

    # run_replay matches go-affected/affected-tillr-go-test-packages-pass when a tillr Go file changed.
    replay_plan = fork_check.build_plan(
        ROOT, [], fork_check.Record(scratch_root() / "nonexistent-replay-state.json"),
        ["tools/tillr-watch/main.go"], None, True,
    )
    matched = [
        c for c in replay_plan.checks
        if fork_check.declaration_id(c.kind, c.label, ROOT) == "go-affected/affected-tillr-go-test-packages-pass"
    ]
    assert len(matched) == 1, [c.kind for c in replay_plan.checks]


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
        drop: set[str] = set()
        if any(p == "tools/tillr" or p.startswith("tools/tillr/") for p in changed):
            drop.add("tools/tillr")
        if any(p == "tools/tillr-gate" or p.startswith("tools/tillr-gate/") for p in changed):
            drop.add("tools/tillr-gate")
        sel = go_affected.select_affected_packages(ROOT, changed, drop_modules=drop, graph=root_graph)
        selected_pkgs = {p["import_path"]: p["reason"] for p in sel["selected"]}
        if expected_pkg is not None:
            assert expected_pkg in selected_pkgs, (sha, expected_pkg, selected_pkgs)
            go_hits += 1
    assert go_hits == 9, go_hits


def test_failure_refusal_names_packages_and_tests_far_above_tail() -> None:
    """Failures far above the 60-line tail still name every failing package and test in the gate refusal."""
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
    assert len(all_lines) > fork_check.EXCERPT_LINES * 3, len(all_lines)
    # Confirm the actual --- FAIL: lines are far above the tail.
    early_idx = next(i for i, ln in enumerate(all_lines) if "TestEarlyMainFailure" in ln)
    assert len(all_lines) - early_idx > fork_check.EXCERPT_LINES * 2, (early_idx, len(all_lines))

    # Even the 60-line tail excerpt carries the failing packages and tests summary.
    tail_excerpt = "\n".join(all_lines[-fork_check.EXCERPT_LINES:])
    for needle in (
        "go-affected: failing packages and tests:",
        f"  {pkg_main}: TestEarlyMainFailure, TestEdgeInputs",
        f"  {pkg_drain}: TestEarlyDrainFailure, TestEdgeInputs, TestSubParent/leaf",
        f"  {pkg_broken}: (no failing test named)",
    ):
        assert needle in tail_excerpt, (needle, tail_excerpt)

    # Gate refusal (triage_packet + triage_lines) names every failing package and test.
    check = fork_check.Check(
        "go-affected",
        "affected tillr Go test packages pass",
        [sys.executable, "tools/gate/go_affected.py", "--changed", "tools/tillr-drain/drain/loop.go"],
        "tillr Go module changed",
        600,
    )
    check.rc = 1
    check.output = full_output
    packet = fork_check.triage_packet(check, ROOT)
    triage_text = "\n".join(fork_check.triage_lines(packet))
    assert "no case named in the output" not in triage_text, triage_text

    # Every failing package and test appears in the refusal, including duplicate test names across packages.
    for expected in (
        f"case: TestEarlyMainFailure -- {pkg_main} (hook_test.go:42)",
        f"case: TestEdgeInputs -- {pkg_main} (edge_inputs_test.go:10)",
        f"case: TestEarlyDrainFailure -- {pkg_drain} (fixer_test.go:99)",
        f"case: TestEdgeInputs -- {pkg_drain} (edge_inputs_test.go:10)",
        f"case: TestSubParent/leaf -- {pkg_drain} (sub_test.go:15)",
        f"case: (package failed) -- {pkg_broken}",
    ):
        assert expected in triage_text, (expected, triage_text)


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
            '    deadline := time.Now().Add(30 * time.Second)\n'
            '    for time.Now().Before(deadline) {\n'
            '      if _, err := os.Stat(peer); err == nil { break }\n'
            '      time.Sleep(20 * time.Millisecond)\n'
            '    }\n'
            '    if _, err := os.Stat(peer); err != nil {\n'
            '      t.Fatalf("peer run never reached barrier %s", peer)\n'
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
        o1, _ = p1.communicate(timeout=60)
        o2, _ = p2.communicate(timeout=60)
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
            env=seq_env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=60,
        )
        assert res1.returncode == 0, res1.stdout
        res2 = subprocess.run(
            [*cmd, "--evidence-out", str(ev_file)],
            env=seq_env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=60,
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
            '  deadline := time.Now().Add(10 * time.Second)\n'
            '  for time.Now().Before(deadline) {\n'
            '    if raw, err := os.ReadFile(pidsFile); err == nil && len(raw) > 0 { break }\n'
            '    time.Sleep(20 * time.Millisecond)\n'
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
            timeout=60,
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
            '  deadline := time.Now().Add(30 * time.Second)\n'
            '  for time.Now().Before(deadline) {\n'
            '    if _, err := os.Stat(release); err == nil { return }\n'
            '    time.Sleep(20 * time.Millisecond)\n'
            '  }\n'
            '  t.Fatalf("release file %s never appeared", release)\n'
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
            deadline = time.monotonic() + 30.0
            while time.monotonic() < deadline:
                remaining = max(0.0, deadline - time.monotonic())
                ready, _, _ = select.select([proc.stdout], [], [], remaining)
                if not ready:
                    break
                raw = proc.stdout.readline()
                if not raw:
                    break
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
            rc = proc.wait(timeout=30)
        finally:
            if not release_file.exists():
                release_file.write_text("cleanup\n", encoding="utf-8")
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=10)
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
            '  deadline := time.Now().Add(30 * time.Second)\n'
            '  for time.Now().Before(deadline) {\n'
            '    if _, err := os.Stat(release); err == nil { return }\n'
            '    time.Sleep(20 * time.Millisecond)\n'
            '  }\n'
            '  t.Fatalf("release file %s never appeared", release)\n'
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
            deadline = time.monotonic() + 30.0
            while not started_file.exists() and time.monotonic() < deadline:
                assert proc.poll() is None, f"go_affected exited prematurely: {proc.stdout.read()!r}"
                time.sleep(0.02)
            assert started_file.exists(), "TestStalls never started"
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
            rc = proc.wait(timeout=30)
        finally:
            if not release_file.exists():
                release_file.write_text("cleanup\n", encoding="utf-8")
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=10)
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

        planner_file = repo / "tools" / "fork-check.py"
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
            '  raw, err := os.ReadFile("../../tools/fork-check.py")\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  if !strings.Contains(string(raw), "PLANNER_V") { t.Fatalf("bad planner: %q", raw) }\n'
            '}\n'
            'func TestReadsPlannerB(t *testing.T) {\n'
            '  recordPID(t, "B")\n'
            '  raw, err := os.ReadFile("../../tools/fork-check.py")\n'
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
                "reads": ["config/app.txt", "tools/fork-check.py"],
                "read_tests": {
                    "tools/fork-check.py": ["TestReadsPlannerA", "TestReadsPlannerB"],
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
        rc, out, ev = run_scoped(["tools/fork-check.py", "tests/fork-check.test.py"])
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
        rc, out, ev = run_scoped(["tools/fork-check.py"])
        assert rc == 0, out
        assert ev["packages"][0]["cached"] is True, ev
        assert not (pids_dir / "A").exists() and not (pids_dir / "B").exists()
        assert ev["packages"][0]["tests"] == ["TestReadsPlannerA", "TestReadsPlannerB"], ev

        # 3. Editing tools/fork-check.py invalidates the digest and catches TestReadsPlannerB's failure.
        planner_file.write_text("PLANNER_V = 'v2'\nBROKEN_B = True\n")
        rc, out, ev = run_scoped(["tools/fork-check.py"])
        assert rc != 0, out
        assert ev["packages"][0]["cached"] is False, ev
        assert "TestReadsPlannerB" in out, out

        # 4. Per-invocation active_tests digest scoping: non-reader shard digests ignore
        # tools/fork-check.py edits while still tracking builds and unscoped reads.
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
            ["tools/fork-check.py", "config/app.txt"],
            {"ALLOW_UNRELATED": "1", "SCOPED_PIDS_DIR": str(pids_dir)},
        )
        assert rc == 0, out
        assert (pids_dir / "CFG").is_file() and (pids_dir / "UNREL").is_file()
        assert set(ev["packages"][0]) == {"import_path", "module", "reason", "elapsed_ms", "cached"}, ev


def test_go_affected_slot_wait_order_and_trunk_repair_priority() -> None:
    """Real go_affected.py gates competing for a shared ordinary slot run in arrival order,
    with revert and trunk-fixer gates served ahead of waiting ordinary candidates."""
    shards = load_module("go_test_shards_for_affected", ROOT / "tools" / "go-test-shards.py")
    with tempfile.TemporaryDirectory(prefix="go-affected-slotorder-", dir=scratch_root()) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        mod = repo / "tools" / "tillr-slotorder"
        mod.mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "go-test-shards.py", repo / "tools" / "go-test-shards.py")
        copied_shards = repo / "tools" / "go-test-shards.py"
        copied_text = copied_shards.read_text(encoding="utf-8")
        assert "ENFORCE_SLOT_LIMITS = False" in copied_text
        copied_shards.write_text(
            copied_text.replace("ENFORCE_SLOT_LIMITS = False", "ENFORCE_SLOT_LIMITS = True", 1),
            encoding="utf-8",
        )
        (mod / "go.mod").write_text("module example.com/tillr-slotorder\n\ngo 1.23\n")
        (mod / "slotorder_test.go").write_text(
            'package slotorder\n'
            'import (\n'
            '  "os"\n'
            '  "path/filepath"\n'
            '  "testing"\n'
            '  "time"\n'
            ')\n'
            'func TestOrder(t *testing.T) {\n'
            '  tag := os.Getenv("GATE_TAG")\n'
            '  orderFile := os.Getenv("ORDER_FILE")\n'
            '  f, err := os.OpenFile(orderFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)\n'
            '  if err != nil { t.Fatal(err) }\n'
            '  _, _ = f.WriteString(tag + "\\n")\n'
            '  _ = f.Close()\n'
            '  rel := filepath.Join(os.Getenv("RELEASE_DIR"), tag)\n'
            '  deadline := time.Now().Add(30 * time.Second)\n'
            '  for time.Now().Before(deadline) {\n'
            '    if _, err := os.Stat(rel); err == nil { return }\n'
            '    time.Sleep(10 * time.Millisecond)\n'
            '  }\n'
            '  t.Fatalf("timed out waiting for release %s", rel)\n'
            '}\n'
        )

        lock_dir = tmp_path / "locks"
        lock_dir.mkdir()
        wait_dir = shards.wait_queue_dir(lock_dir)
        order_file = tmp_path / "order.log"
        release_dir = tmp_path / "release"
        release_dir.mkdir()
        durable = tmp_path / "durable"
        durable.mkdir()
        gocache = _shared_gocache()
        _precompile_go_test(mod, gocache)

        ordinary_limit = shards.default_shard_limit()
        gate_limit = shards.default_gate_shard_limit(ordinary_limit)
        proof_limit = shards.default_proof_shard_limit(ordinary_limit, gate_limit)

        held_fds: list[int] = []
        slot0_fd: int | None = None

        def lock_all() -> None:
            nonlocal slot0_fd
            for prefix, count in (("slot", ordinary_limit), ("proof", proof_limit), ("gate", gate_limit)):
                for idx in range(count):
                    fd = os.open(lock_dir / f"{prefix}-{idx}.lock", os.O_CREAT | os.O_RDWR, 0o666)
                    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    if prefix == "slot" and idx == 0:
                        slot0_fd = fd
                    else:
                        held_fds.append(fd)

        def wait_for_tickets(expected_count: int) -> None:
            deadline = time.monotonic() + 30.0
            while time.monotonic() < deadline:
                if wait_dir.is_dir():
                    live = [n for n in os.listdir(wait_dir) if n.endswith(".wait") and not n.startswith(".")]
                    if len(live) >= expected_count:
                        return
                time.sleep(0.02)
            raise AssertionError(f"timed out waiting for {expected_count} wait tickets in {wait_dir}")

        procs: list[tuple[str, subprocess.Popen]] = []
        try:
            lock_all()
            cmd = [
                sys.executable,
                str(ROOT / "tools" / "gate" / "go_affected.py"),
                "--root", str(repo),
                "--changed", "tools/tillr-slotorder/slotorder_test.go",
            ]
            # Start ordinary candidates first, then revert and trunk-fixer gates while all slots are busy.
            specs = [
                ("item-1", "gate:run-item-1"),
                ("item-2", "gate:run-item-2"),
                ("revert", "gate:run-revert-dd17e3283fd1-d5f5f41a7e57"),
                ("fixer", "gate:run-fixer-main-dd17e3283fd1-a1-n1"),
            ]
            for idx, (tag, holder) in enumerate(specs, start=1):
                env = {
                    **os.environ,
                    "YOLO_DURABLE_DIR": str(durable),
                    "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
                    "GO_TEST_SHARDS_HOLDER": holder,
                    "GOCACHE": str(gocache),
                    "GATE_TAG": tag,
                    "ORDER_FILE": str(order_file),
                    "RELEASE_DIR": str(release_dir),
                }
                env.pop("GO_TEST_SHARDS_CLASS", None)
                proc = subprocess.Popen(cmd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
                procs.append((tag, proc))
                wait_for_tickets(idx)

            # Release only ordinary slot-0; all four gates compete for that single shared ordinary slot.
            assert slot0_fd is not None
            fcntl.flock(slot0_fd, fcntl.LOCK_UN)
            os.close(slot0_fd)
            slot0_fd = None

            expected_order = ["revert", "fixer", "item-1", "item-2"]
            for idx, want in enumerate(expected_order):
                deadline = time.monotonic() + 30.0
                observed: list[str] = []
                while time.monotonic() < deadline:
                    if order_file.is_file():
                        observed = [line.strip() for line in order_file.read_text(encoding="utf-8").splitlines() if line.strip()]
                        if len(observed) >= idx + 1:
                            break
                    time.sleep(0.02)
                assert len(observed) >= idx + 1, f"timed out waiting for gate {idx} ({want}); observed={observed}"
                assert observed[idx] == want, f"wrong gate served at turn {idx}: got {observed}, want prefix {expected_order[:idx + 1]}"
                (release_dir / want).write_text("go\n")

            for tag, proc in procs:
                out, _ = proc.communicate(timeout=30)
                assert proc.returncode == 0, f"{tag} failed: {out}"
        finally:
            for tag, _ in procs:
                (release_dir / tag).write_text("go\n")
            if slot0_fd is not None:
                fcntl.flock(slot0_fd, fcntl.LOCK_UN)
                os.close(slot0_fd)
            for fd in held_fds:
                fcntl.flock(fd, fcntl.LOCK_UN)
                os.close(fd)
            for _, proc in procs:
                if proc.poll() is None:
                    proc.kill()
                    proc.wait()


def test_candidate_gates_use_gate_class_and_leave_proof_slots_free() -> None:
    """Candidate gates running go_affected.py use slot class 'gate', record class 'gate'
    in holder files, never take proof-* slots, and yield freed ordinary slots to waiting
    trunk proofs."""
    shards = load_module("go_test_shards_for_proof_slots", ROOT / "tools" / "go-test-shards.py")
    shards.ENFORCE_SLOT_LIMITS = True
    with tempfile.TemporaryDirectory(dir=str(scratch_root())) as tmp:
        tmp_path = Path(tmp)
        repo = tmp_path / "repo"
        mod = repo / "tools" / "tillr-slotclass"
        mod.mkdir(parents=True)
        shutil.copy2(ROOT / "tools" / "go-test-shards.py", repo / "tools" / "go-test-shards.py")
        copied_shards = repo / "tools" / "go-test-shards.py"
        copied_text = copied_shards.read_text(encoding="utf-8")
        assert "ENFORCE_SLOT_LIMITS = False" in copied_text
        copied_shards.write_text(
            copied_text.replace("ENFORCE_SLOT_LIMITS = False", "ENFORCE_SLOT_LIMITS = True", 1),
            encoding="utf-8",
        )
        (mod / "go.mod").write_text("module example.com/tillr-slotclass\n\ngo 1.23\n")
        (mod / "slotclass_test.go").write_text(
            'package slotclass\n'
            'import (\n'
            '  "os"\n'
            '  "path/filepath"\n'
            '  "testing"\n'
            '  "time"\n'
            ')\n'
            'func TestHold(t *testing.T) {\n'
            '  tag := os.Getenv("GATE_TAG")\n'
            '  entered := filepath.Join(os.Getenv("ENTERED_DIR"), tag)\n'
            '  if err := os.WriteFile(entered, []byte("in\\n"), 0o644); err != nil {\n'
            '    t.Fatal(err)\n'
            '  }\n'
            '  rel := filepath.Join(os.Getenv("RELEASE_DIR"), tag)\n'
            '  deadline := time.Now().Add(30 * time.Second)\n'
            '  for time.Now().Before(deadline) {\n'
            '    if _, err := os.Stat(rel); err == nil { return }\n'
            '    time.Sleep(10 * time.Millisecond)\n'
            '  }\n'
            '  t.Fatalf("timed out waiting for release %s", rel)\n'
            '}\n'
        )

        lock_dir = tmp_path / "locks"
        lock_dir.mkdir()
        entered_dir = tmp_path / "entered"
        entered_dir.mkdir()
        release_dir = tmp_path / "release"
        release_dir.mkdir()
        durable = tmp_path / "durable"
        durable.mkdir()
        gocache = _shared_gocache()
        _precompile_go_test(mod, gocache)
        trace_dir = tmp_path / "trace"
        trace_dir.mkdir()

        # Run go_affected.py in real child processes with cpu_count patched to 4
        # (1 ordinary slot-0, 1 gate slot gate-0, 1 proof slot proof-0) and
        # GO_TEST_SHARDS_CLASS unset so the real candidate-gate default is exercised.
        wrapper = (
            "import importlib.util, os, sys; "
            "os.cpu_count = lambda: 4; "
            "spec = importlib.util.spec_from_file_location('go_affected', sys.argv[1]); "
            "mod = importlib.util.module_from_spec(spec); "
            "spec.loader.exec_module(mod); "
            "sys.exit(mod.main(sys.argv[2:]))"
        )
        base_cmd = [
            sys.executable,
            "-c",
            wrapper,
            str(ROOT / "tools" / "gate" / "go_affected.py"),
            "--root", str(repo),
            "--changed", "tools/tillr-slotclass/slotclass_test.go",
        ]

        def start_gate(tag: str, holder: str) -> subprocess.Popen:
            env = {
                **os.environ,
                "YOLO_DURABLE_DIR": str(durable),
                "GO_TEST_SHARDS_LOCK_DIR": str(lock_dir),
                "GO_TEST_SHARDS_HOLDER": holder,
                "GOCACHE": str(gocache),
                "GATE_TAG": tag,
                "ENTERED_DIR": str(entered_dir),
                "RELEASE_DIR": str(release_dir),
            }
            env.pop("GO_TEST_SHARDS_CLASS", None)
            return subprocess.Popen(
                base_cmd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
            )

        def wait_file(path: Path, desc: str) -> None:
            deadline = time.monotonic() + 30.0
            while time.monotonic() < deadline:
                if path.is_file():
                    return
                time.sleep(0.02)
            raise AssertionError(f"timed out waiting for {desc}: {path}")

        procs: list[tuple[str, subprocess.Popen]] = []
        proof1_release = threading.Event()
        proof2_release = threading.Event()
        threads: list[threading.Thread] = []
        errors: list[BaseException] = []
        try:
            # 1. Two candidate gates occupy the single ordinary slot (slot-0) and gate-0.
            for tag, holder in (("cand-1", "gate:run-cand-1"), ("cand-2", "gate:run-cand-2")):
                proc = start_gate(tag, holder)
                procs.append((tag, proc))
                wait_file(entered_dir / tag, f"{tag} to enter TestHold")

            holders = shards.read_slot_holders(lock_dir)
            assert len(holders) == 2, f"expected 2 live holders, got {holders}"
            for h in holders:
                assert h.get("class") == shards.SLOT_CLASS_GATE, (
                    f"candidate gate holder must record class 'gate', got {h}"
                )
                assert not str(h.get("slot", "")).startswith("proof-"), (
                    f"candidate gate must not hold a proof-* slot, got {h}"
                )
            held_slots = {h["slot"] for h in holders}
            assert held_slots == {"slot-0", "gate-0"}, f"expected slot-0 and gate-0 held, got {held_slots}"

            # 2. A third candidate gate arrives and waits because slot-0 and gate-0 are full.
            proc3 = start_gate("cand-3", "gate:run-cand-3")
            procs.append(("cand-3", proc3))
            deadline = time.monotonic() + 30.0
            while time.monotonic() < deadline and not shards.gate_caller_waiting(lock_dir):
                time.sleep(0.02)
            assert shards.gate_caller_waiting(lock_dir), "cand-3 did not enter gate wait"
            assert not (entered_dir / "cand-3").is_file()

            # 3. A trunk proof arrives later while candidate gates hold every ordinary slot
            #    and still gets its reserved proof-0 slot immediately without waiting.
            proof1_notices: list[str] = []
            proof1_acquired = threading.Event()

            def run_proof1() -> None:
                try:
                    with patch.dict(os.environ, {"TILLR_TRACE_DIR": str(trace_dir)}):
                        with shards.acquire_shard_slot(
                            lock_dir, 1, notify=proof1_notices.append,
                            slot_class="proof", proof_limit=1, gate_limit=1,
                            holder="trunk-proof", label="proof-imm",
                        ):
                            proof1_acquired.set()
                            assert proof1_release.wait(30)
                except BaseException as exc:
                    errors.append(exc)
                    proof1_acquired.set()

            t1 = threading.Thread(target=run_proof1)
            t1.start()
            threads.append(t1)
            assert proof1_acquired.wait(10), "trunk proof did not acquire reserved proof-0 slot"
            assert proof1_notices == [], f"trunk proof waited when proof-0 should be free: {proof1_notices}"

            # 4. A second trunk proof shard arrives after cand-3 is already waiting, waits on
            #    proof-waiting.lock, and gets slot-0 ahead of cand-3 when cand-1 releases it.
            proof2_waiting = threading.Event()
            proof2_acquired = threading.Event()

            def run_proof2() -> None:
                try:
                    with shards.acquire_shard_slot(
                        lock_dir, 1, notify=lambda _m: proof2_waiting.set(),
                        slot_class="proof", proof_limit=1, gate_limit=1,
                        holder="trunk-proof", label="proof-wait",
                    ):
                        proof2_acquired.set()
                        assert proof2_release.wait(30)
                except BaseException as exc:
                    errors.append(exc)
                    proof2_waiting.set()
                    proof2_acquired.set()

            t2 = threading.Thread(target=run_proof2)
            t2.start()
            threads.append(t2)
            assert proof2_waiting.wait(10), "proof2 did not enter wait"
            assert shards.proof_caller_waiting(lock_dir)
            assert not proof2_acquired.is_set()

            # Release whichever candidate gate holds slot-0.
            slot0_holder = next(h["run"] for h in holders if h["slot"] == "slot-0")
            slot0_tag = "cand-1" if slot0_holder == "gate:run-cand-1" else "cand-2"
            (release_dir / slot0_tag).write_text("go\n")

            assert proof2_acquired.wait(15), "waiting trunk proof did not acquire freed slot-0"
            assert not (entered_dir / "cand-3").is_file(), (
                "waiting candidate gate acquired freed slot-0 ahead of waiting trunk proof"
            )

            # Release the proofs and remaining candidate gates so everything exits cleanly.
            proof2_release.set()
            proof1_release.set()
            for tag in ("cand-1", "cand-2", "cand-3"):
                (release_dir / tag).write_text("go\n")
            wait_file(entered_dir / "cand-3", "cand-3 to run after proof finished")

            for t in threads:
                t.join(15)
            if errors:
                raise errors[0]

            for tag, proc in procs:
                out, _ = proc.communicate(timeout=30)
                assert proc.returncode == 0, f"{tag} failed: {out}"

            rows = [
                json.loads(line)
                for line in (trace_dir / "slot-waits.jsonl").read_text(encoding="utf-8").splitlines()
                if line.strip()
            ]
            imm = next(r for r in rows if r.get("label") == "proof-imm")
            assert imm["slot"] == "proof-0", imm
            assert imm["waited_ms"] == 0, imm
            assert imm["class"] == "proof", imm
        finally:
            proof1_release.set()
            proof2_release.set()
            for tag in ("cand-1", "cand-2", "cand-3"):
                (release_dir / tag).write_text("go\n")
            for t in threads:
                t.join(10)
            for _, proc in procs:
                if proc.poll() is None:
                    proc.kill()
                    proc.wait()


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
        res1 = subprocess.run(cmd, env=env1, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=60)
        assert res1.returncode == 0, res1.stdout
        assert not shim_hits.exists(), shim_hits.read_text(encoding="utf-8")
        assert inherited_cfg.read_text(encoding="utf-8") == inherited_text
        ev1 = json.loads(ev_file.read_text(encoding="utf-8"))
        assert ev1["packages"][0]["cached"] is False, ev1

        env2 = {**env1, "PATH": gate_path}
        res2 = subprocess.run(cmd, env=env2, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=60)
        assert res2.returncode == 0, res2.stdout
        assert not shim_hits.exists(), shim_hits.read_text(encoding="utf-8")
        ev2 = json.loads(ev_file.read_text(encoding="utf-8"))
        assert ev2["packages"][0]["cached"] is True, ev2


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
        ("test_fork_check_integration", lambda: test_fork_check_integration(root_graph)),
        ("test_go_affected_slot_wait_order_and_trunk_repair_priority", test_go_affected_slot_wait_order_and_trunk_repair_priority),
        ("test_heavy_failures_replay", lambda: test_heavy_failures_replay(root_graph)),
        ("test_selection_rules", lambda: test_selection_rules(root_graph)),
        ("test_candidate_gates_use_gate_class_and_leave_proof_slots_free", test_candidate_gates_use_gate_class_and_leave_proof_slots_free),
        ("test_edge_cache_invalidation", test_edge_cache_invalidation),
        ("test_edges_file_honest_and_env_read", test_edges_file_honest_and_env_read),
        ("test_stalled_run_prints_nothing_without_test_events", test_stalled_run_prints_nothing_without_test_events),
        ("test_scoped_read_tests_execution_cache_and_sharding", test_scoped_read_tests_execution_cache_and_sharding),
        ("test_concurrent_runs_do_not_share_scratch_and_preserve_cache", test_concurrent_runs_do_not_share_scratch_and_preserve_cache),
        ("test_streams_progress_lines_before_run_ends", test_streams_progress_lines_before_run_ends),
        ("test_suite_gocache_and_runner_helpers", test_suite_gocache_and_runner_helpers),
        ("test_failed_module_floor_fallback", test_failed_module_floor_fallback),
        ("test_subreaper_reaps_orphans_while_command_runs", test_subreaper_reaps_orphans_while_command_runs),
        ("test_discover_modules", test_discover_modules),
        ("test_build_env_strips_outer_gate_vars", test_build_env_strips_outer_gate_vars),
        ("test_failure_refusal_names_packages_and_tests_far_above_tail", test_failure_refusal_names_packages_and_tests_far_above_tail),
        ("test_private_gitconfig_disables_auto_maintenance_and_bypasses_block_git_shim", test_private_gitconfig_disables_auto_maintenance_and_bypasses_block_git_shim),
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
