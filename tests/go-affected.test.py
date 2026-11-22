#!/usr/bin/env python3
"""Tests for tools/gate/go_affected.py, go-test-edges.json, and fork-check integration."""
from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import re
import select
import shutil
import subprocess
import sys
import tempfile
import time
from unittest.mock import patch

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
        ("config/tillr/policy.json", {"github.com/mschulkind-oss/tillr-drain"}),
        ("bin/pi-forks", {"github.com/mschulkind-oss/tillr-drain"}),
        ("tools/gate/change-gate.py", {"github.com/mschulkind-oss/tillr-drain/drain"}),
        ("tools/gate/classify.py", {"github.com/mschulkind-oss/tillr-drain/drain"}),
        ("tools/tillr-loop/drain-argv.json", {"github.com/mschulkind-oss/tillr-drain/drain"}),
        ("yolo-packs/tillr/pack.json", {"github.com/mschulkind-oss/tillr-drain/drain"}),
        ("tools/tillr/store/testdata/crash_writer.py", {
            "github.com/mschulkind-oss/tillr-store",
            "github.com/mschulkind-oss/tillr-view",
        }),
        ("tools/tillr/examples/recorded-verdict/definition.json", {
            "github.com/mschulkind-oss/tillr-drain",
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
    # selects only the three reading packages and narrows each to its reader tests.
    expected_fc_pkgs = {
        "github.com/mschulkind-oss/tillr-drain",
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
        assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["selected_tests"] == [
            "TestTheComposedPipelineRunsEndToEndOnAFixture"
        ], by_pkg["github.com/mschulkind-oss/tillr-drain"]
        assert len(by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["selected_tests"]) == 11, (
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
    assert by_pkg["github.com/mschulkind-oss/tillr-drain"]["selected_tests"] is None, by_pkg
    assert by_pkg["github.com/mschulkind-oss/tillr-drain/drain"]["selected_tests"] is None, by_pkg
    assert len(by_pkg["github.com/mschulkind-oss/tillr-gate"]["selected_tests"]) == 9, by_pkg

    sel_unscoped = go_affected.select_affected_packages(
        ROOT, ["tools/fork-check.py", "config/tillr/policy.json"], graph=root_graph
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
    sibling_dir_re = re.compile(r'"\.\./(tillr-[a-z]+)"')
    sibling_pkg_re = re.compile(r'"github\.com/mschulkind-oss/(tillr-[a-z]+)"')
    parent_build_re = re.compile(r'exec\.Command\("go",\s*"build"[^)]*"\.\."\)')
    helper_join_re = re.compile(r'filepath\.Join\("\.\.",\s*"tillr-"\s*\+')

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
        # Validate read_tests keys and test names against scan_package_tests.
        read_rules = set(edges[pkg_import].get("reads", []))
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
        "github.com/mschulkind-oss/tillr-drain",
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
        lock_dir = Path(tmp) / "locks"

        def run_once(changed: list[str]) -> tuple[int, str, dict]:
            with patch.dict(os.environ, {
                "GOCACHE": str(Path(tmp) / "gocache"),
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
        for via_build_plan in (True, False):
            rolling = fork_check.build_plan(
                ROOT, fork_check.read_series(ROOT), fork_check.Record(state),
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
        (repo / "tools" / "tillr-watch" / "main.go").write_text("package main\nfunc main() {}\n")
        (repo / "tools" / "tillr-watch" / "main_test.go").write_text(
            'package main\n\nimport "testing"\n\nfunc TestWatch(t *testing.T) {}\n'
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

        explain_full = subprocess.run(
            [sys.executable, "tools/fork-check.py", "--explain", "--since", "HEAD~1"],
            cwd=repo,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        assert explain_full.returncode == 0, explain_full.stderr
        assert "would run  go-affected " not in explain_full.stdout, explain_full.stdout
        assert "would not run          go-affected (affected tillr Go test packages pass)" in explain_full.stdout, (
            explain_full.stdout
        )
        explain_rolling = subprocess.run(
            [sys.executable, "tools/fork-check.py", "--explain", "--all", "--tier", "rolling"],
            cwd=repo,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        assert explain_rolling.returncode == 0, explain_rolling.stderr
        assert "would run  go-affected " not in explain_rolling.stdout, explain_rolling.stdout
        assert "would skip" not in explain_rolling.stdout, explain_rolling.stdout
        assert "would not run          go-affected (affected tillr Go test packages pass)" in explain_rolling.stdout, (
            explain_rolling.stdout
        )
        assert "does not apply on the rolling tier because whole Go module suites run fresh" in explain_rolling.stdout, (
            explain_rolling.stdout
        )
        explain_landing = subprocess.run(
            [sys.executable, "tools/fork-check.py", "--explain", "--tier", "landing", "--since", "HEAD~1"],
            cwd=repo,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        assert explain_landing.returncode == 0, explain_landing.stderr
        assert "would run  go-affected " in explain_landing.stdout, explain_landing.stdout
        landing_repo = fork_check.build_plan(
            repo, [], fork_check.Record(state), ["tools/tillr-watch/main.go"], None, True,
            tier=fork_check.TIER_LANDING,
        )
        landing_ga = next(c for c in landing_repo.checks if c.kind == "go-affected")
        rc, out, _ = fork_check.run_landing_go_check(
            repo, landing_ga, dict(os.environ), landing_ga.drop, None
        )
        assert rc == 0, out
        assert "go-affected: 1 package(s)" in out, out

    # registry-json exports go-affected cleanly.
    reg_res = subprocess.run(
        [sys.executable, str(ROOT / "tools" / "fork-check.py"), "--registry-json"],
        cwd=ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    assert reg_res.returncode == 0, reg_res.stderr
    reg = json.loads(reg_res.stdout)
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
        gocache = tmp_path / "gocache"
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
                "GOCACHE": str(tmp_path / "gocache"),
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
            env["YOLO_DURABLE_DIR"] = str(sdir)
            env["TMPDIR"] = str(sdir)
            rc, stdout = go_affected._run_guarded_command(
                [
                    "go", "test", "-count=1", "-json", "-timeout", "0", "-parallel", "1",
                    "-run", "^TestRestartReplacesTheLoopInPlaceWithoutSignalingARun$",
                    "github.com/mschulkind-oss/tillr-drain",
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
                "GOCACHE": str(tmp_path / "gocache"),
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
                "GOCACHE": str(tmp_path / "gocache"),
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
        gocache = tmp_path / "gocache"

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


def main() -> int:
    root_graph = go_affected.load_tillr_graph(ROOT)
    test_discover_modules()
    test_build_env_strips_outer_gate_vars()
    test_selection_rules(root_graph)
    test_edges_file_honest_and_env_read()
    test_edge_cache_invalidation()
    test_scoped_read_tests_execution_cache_and_sharding()
    test_failed_module_floor_fallback()
    test_failure_refusal_names_packages_and_tests_far_above_tail()
    test_fork_check_integration(root_graph)
    test_heavy_failures_replay(root_graph)
    test_concurrent_runs_do_not_share_scratch_and_preserve_cache()
    test_no_leftover_processes_after_check_finishes()
    test_streams_progress_lines_before_run_ends()
    test_stalled_run_prints_nothing_without_test_events()
    print("PASS: all go-affected tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
