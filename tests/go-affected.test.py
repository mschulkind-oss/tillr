#!/usr/bin/env python3
"""Tests for tools/gate/go_affected.py, go-test-edges.json, and fork-check integration."""
from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
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

        # Rolling tier drops go-affected.
        rolling = fork_check.build_plan(
            ROOT, [], fork_check.Record(state), ["tools/tillr-drain/drain/loop.go"], None, True,
            tier=fork_check.TIER_ROLLING,
        )
        assert not any(c.kind == "go-affected" for c in rolling.checks), [c.kind for c in rolling.checks]

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
        explain_landing = subprocess.run(
            [sys.executable, "tools/fork-check.py", "--explain", "--tier", "landing", "--since", "HEAD~1"],
            cwd=repo,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        assert explain_landing.returncode == 0, explain_landing.stderr
        assert "would run  go-affected " in explain_landing.stdout, explain_landing.stdout

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


def main() -> int:
    root_graph = go_affected.load_tillr_graph(ROOT)
    test_discover_modules()
    test_build_env_strips_outer_gate_vars()
    test_selection_rules(root_graph)
    test_edges_file_honest_and_env_read()
    test_edge_cache_invalidation()
    test_failed_module_floor_fallback()
    test_failure_refusal_names_packages_and_tests_far_above_tail()
    test_fork_check_integration(root_graph)
    test_heavy_failures_replay(root_graph)
    print("PASS: all go-affected tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
