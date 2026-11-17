#!/usr/bin/env python3
"""Permanent coverage for the migrated whole-tree checks."""
from concurrent.futures import ThreadPoolExecutor
import ast
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch
import tree_common

HERE = Path(__file__).parent
spec = importlib.util.spec_from_file_location("go_build", HERE / "go-build.py")
go_build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(go_build)


class TreeChecks(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"])
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)

    def track(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        subprocess.run(["git", "-C", str(self.root), "add", name], check=True)

    def test_scratch_prose_fences_and_file_specific_exceptions(self):
        self.track("guide.md", "# Guide\nEvidence `.yolo/durable/report.md`.\n")
        self.assertIn("guide.md:2", tree_common.scratch_citations(self.root)[0])
        self.track("guide.md", "```sh\ncat .yolo/durable/report.md\n```\n")
        self.assertEqual(tree_common.scratch_citations(self.root), [])
        location = "Artifacts land under $YOLO_DURABLE_DIR/footer-storybook by design.\n"
        self.track("tools/footer-storybook/README.md", location)
        self.assertEqual(tree_common.scratch_citations(self.root), [])
        self.track("guide.md", location)
        self.assertTrue(tree_common.scratch_citations(self.root))

    def test_design_document_and_anchor_resolution(self):
        self.track("docs/design/target.md", '# Target\n## 3. The section\n<a id="html"></a>\n<!-- vantage: question id=Q1 -->\n')
        for citation in ["target.md", "target.md#3-the-section", "target.md#html", "target.md#q1"]:
            self.track("docs/design/citing.md", "---\ndepends-on: " + citation + "\nsupersedes: a framing\n---\n")
            self.assertEqual(tree_common.design_citation_findings(self.root), [])
        for citation, needle in [("target.md#gone", "no such section"), ("gone.md", "names no tracked document")]:
            self.track("docs/design/citing.md", "---\ndepends-on:\n - " + citation + "\n---\n")
            self.assertIn(needle, tree_common.design_citation_findings(self.root)[0])
        self.track("docs/design/citing.md", "---\ndepends-on: [target.md#html, 'target.md#q1']\n---\n")
        self.assertEqual(tree_common.design_citation_findings(self.root), [])

    def test_missing_git_is_could_not_judge(self):
        for name in ["scratch", "design-citations"]:
            with patch.dict(os.environ, PATH="/not-a-tool-directory"):
                result = subprocess.run([os.sys.executable, str(HERE / (name + ".py"))], cwd=self.root, text=True, capture_output=True)
            self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
            self.assertIn("could not judge", result.stderr)

    def test_ten_fast_checks_fit_and_extra_declaration_fails(self):
        declarations = [json.loads(path.read_text()) for path in HERE.glob("*.json")]
        fast = [check for check in declarations if check["tier"] == "fast"]
        structural = next(check for check in declarations if check["id"] == "structural")
        self.assertFalse(structural["remember"], "Git refs are not tree-memory inputs")
        # The budget check itself is admission, not one of the ten admitted checks.
        self.assertEqual(len([check for check in fast if check["id"] != "gate-budget"]), 10)
        result = subprocess.run(["python3", str(HERE / "gate-budget.py")], text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for check in declarations:
            (self.root / (check["id"] + ".json")).write_text(json.dumps(check))
        total = sum(check["budget_ms"] for check in fast)
        (self.root / "extra.json").write_text(json.dumps({"tier": "fast", "budget_ms": 9001-total}))
        result = subprocess.run(["python3", str(HERE / "gate-budget.py"), str(self.root)], text=True, capture_output=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("9001", result.stdout)

    def test_parallel_builder_covers_every_legacy_go_tool_directory(self):
        source = HERE.parents[1] / "fork-check.py"
        # Read the declared inventory without importing the legacy gate or probing tools.
        declarations = ast.parse(source.read_text()).body
        dirs = next(ast.literal_eval(node.value) for node in declarations
                    if isinstance(node, ast.Assign)
                    and any(isinstance(target, ast.Name) and target.id == "GO_TOOL_DIRS"
                            for target in node.targets))
        self.assertEqual(go_build.GO_TOOL_DIRS, dirs)

    def test_go_modules_start_in_parallel_including_nested_module(self):
        roots = [self.root / str(i) for i in range(2)]
        nested = roots[0] / "nested"
        for root in roots + [nested]:
            root.mkdir(parents=True, exist_ok=True)
            (root / "go.mod").write_text("module fixture\n")
        barrier = threading.Barrier(3)
        seen = []
        def build(module, scratch):
            seen.append(module)
            barrier.wait(timeout=10)  # deadlock guard, not a duration assertion
            return 0, ""
        with patch.object(go_build, "GO_TOOL_DIRS", tuple(map(str, roots))), patch.object(go_build, "build_module", build):
            self.assertEqual(go_build.main(), 0)
        self.assertEqual(set(seen), set(roots + [nested]))

    def test_go_build_compiles_test_files_and_reports_missing_modules(self):
        module = self.root / "tool"
        module.mkdir()
        (module / "go.mod").write_text("module fixture/tool\n\ngo 1.24\n")
        (module / "main.go").write_text("package main\nfunc main() {}\n")
        (module / "main_test.go").write_text("package main\nvar _ = undefinedTestSymbol\n")
        code, output = go_build.build_module(module, self.root)
        self.assertNotEqual(code, 0)
        self.assertIn("undefinedTestSymbol", output)
        self.assertEqual(sorted(p.name for p in module.iterdir()), ["go.mod", "main.go", "main_test.go"])
        with patch.object(go_build, "GO_TOOL_DIRS", (str(self.root / "missing"),)):
            self.assertEqual(go_build.main(), 4)

    def test_go_build_trims_paths_in_every_go_command_it_starts(self):
        # Every go command the check starts -- and every one those start, such as a test
        # binary's TestMain building its tool -- trims source paths, so a new checkout
        # reuses the shared cache's entries. The flag rides in GOFLAGS, added to the
        # caller's own GOFLAGS, and the cache location is left exactly as the caller set it.
        module = self.root / "tool"
        module.mkdir()
        calls = []
        def recorded(args, **kwargs):
            calls.append((args, kwargs["env"]))
            return subprocess.CompletedProcess(args, 0, stdout="M", stderr="")
        with patch.dict(os.environ, {"GOFLAGS": "-mod=mod"}), patch.object(go_build.subprocess, "run", recorded):
            self.assertEqual(go_build.build_module(module, self.root)[0], 0)
            expected_cache = os.environ.get("GOCACHE")
        self.assertEqual([args[:2] for args, _ in calls], [["go", "list"], ["go", "build"], ["go", "test"]])
        for args, env in calls:
            self.assertEqual(env["GOFLAGS"].split(), ["-mod=mod", "-trimpath"], args)
            self.assertEqual(env.get("GOCACHE"), expected_cache, args)

    def test_go_build_handles_library_missing_main_and_healthy_main(self):
        for name, source, expected in [("library", "package library\nfunc F() {}\n", 0),
                                       ("missing", "package main\nfunc helper() {}\n", 1),
                                       ("healthy", "package main\nfunc main() {}\n", 0)]:
            module = self.root / name
            module.mkdir()
            (module / "go.mod").write_text("module fixture/" + name + "\n\ngo 1.24\n")
            (module / "main.go").write_text(source)
            code, output = go_build.build_module(module, self.root)
            self.assertEqual(code, expected, output)
            self.assertEqual(sorted(p.name for p in module.iterdir()), ["go.mod", "main.go"])
        self.assertEqual(sorted(p.name for p in self.root.iterdir()), [".git", "healthy", "library", "missing"])


if __name__ == "__main__":
    unittest.main()
