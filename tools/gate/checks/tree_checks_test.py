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


def missing_go_reads(reads, root):
    """For each `tools/<module>/` read with a go.mod, the in-repo modules it replaces
    with a relative path that `reads` does not cover."""
    def covered(path):
        return any(path == read or (read.endswith("/") and path.startswith(read)) for read in reads)
    missing = {}
    for read in reads:
        if not read.endswith("/") or not (root / read / "go.mod").is_file():
            continue
        for line in (root / read / "go.mod").read_text().splitlines():
            line = line.split("//", 1)[0]
            if "=>" not in line:
                continue
            target = line.split("=>", 1)[1].split()
            if not target or not target[0].startswith(("./", "../")):
                continue
            dependency = os.path.normpath(os.path.join(read, target[0])) + "/"
            if not dependency.startswith("..") and not covered(dependency):
                missing.setdefault(read, []).append(dependency)
    return missing


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

    def test_scratch_exception_follows_a_delivered_copy_to_its_source(self):
        # The tillr pack delivers dated copies of design documents (tools/pack-docs.py).
        # A copy carries its source's lines, so it is excused exactly where its source is.
        line = "build into `$YOLO_DURABLE_DIR/bin` and prepend it to PATH.\n"
        copy = "yolo-packs/tillr/files/local/share/tillr/docs/design/tillr-distribution.md"
        self.track(copy, line)
        self.assertEqual(tree_common.scratch_citations(self.root), [])
        other = "yolo-packs/tillr/files/local/share/tillr/docs/design/other.md"
        self.track(other, line)
        self.assertIn(other + ":1", tree_common.scratch_citations(self.root)[0])
        (self.root / other).unlink()
        subprocess.run(["git", "-C", str(self.root), "rm", "-q", "--cached", other], check=True)
        # The module mirror is a copy too: `tools/<module>/x` lands at `<module>/x`.
        mirror = "yolo-packs/tillr/files/local/share/tillr/tillr-view/README.md"
        self.track(mirror, "under `tillr-observatory/<key>/`, where `<key>` names it. $YOLO_DURABLE_DIR\n")
        self.assertEqual(tree_common.scratch_citations(self.root), [])
        self.assertEqual(tree_common.excused_as(mirror), "tools/tillr-view/README.md")

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

    def test_design_citations_skip_delivered_pack_docs_snapshot(self):
        # Delivered copies under yolo-packs/tillr/files/local/share/tillr/docs/ are a
        # commit-pinned snapshot verified by tests/pack-sync.test.py against snapshot.json
        # (and excluded in .vantage.toml). Graduating and deleting an unshipped design on
        # HEAD must not convict a snapshot copy's historical frontmatter citation.
        snapshot_copy = (
            "yolo-packs/tillr/files/local/share/tillr/docs/design/shipped.md"
        )
        self.track(snapshot_copy, "---\ndepends-on:\n  - graduated-design.md\n---\n# Copy\n")
        self.assertEqual(tree_common.design_citation_findings(self.root), [])
        # Negative control: a live document outside the snapshot with the same missing
        # target is still refused.
        self.track("docs/design/live.md", "---\ndepends-on:\n  - graduated-design.md\n---\n")
        findings = tree_common.design_citation_findings(self.root)
        self.assertEqual(len(findings), 1)
        self.assertIn("docs/design/live.md:3", findings[0])
        self.assertIn("names no tracked document", findings[0])

    def test_missing_git_is_could_not_judge(self):
        for name in ["scratch", "design-citations"]:
            with patch.dict(os.environ, PATH="/not-a-tool-directory"):
                result = subprocess.run([os.sys.executable, str(HERE / (name + ".py"))], cwd=self.root, text=True, capture_output=True)
            self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
            self.assertIn("could not judge", result.stderr)

    def test_eleven_fast_checks_fit_and_extra_declaration_fails(self):
        declarations = [json.loads(path.read_text()) for path in HERE.glob("*.json")]
        fast = [check for check in declarations if check["tier"] == "fast"]
        structural = next(check for check in declarations if check["id"] == "structural")
        self.assertFalse(structural["remember"], "Git refs are not tree-memory inputs")
        # The budget check itself is admission, not one of the eleven admitted checks.
        self.assertEqual(len([check for check in fast if check["id"] != "gate-budget"]), 11)
        result = subprocess.run(["python3", str(HERE / "gate-budget.py")], text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for check in declarations:
            (self.root / (check["id"] + ".json")).write_text(json.dumps(check))
        total = sum(check["budget_ms"] for check in fast)
        (self.root / "extra.json").write_text(json.dumps({"tier": "fast", "budget_ms": 9001-total}))
        result = subprocess.run(["python3", str(HERE / "gate-budget.py"), str(self.root)], text=True, capture_output=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("9001", result.stdout)

    def test_every_go_tool_is_gated_or_explicitly_excluded(self):
        repo = HERE.parents[2]
        source = repo / "tools/fork-check.py"
        # Read the declared inventory without importing the legacy gate or probing tools.
        declarations = ast.parse(source.read_text()).body

        def declared(name):
            return next(ast.literal_eval(node.value) for node in declarations
                        if isinstance(node, ast.Assign)
                        and any(isinstance(target, ast.Name) and target.id == name
                                for target in node.targets))

        dirs = declared("GO_TOOL_DIRS")
        exclusions = declared("GO_TOOL_EXCLUSIONS")
        tracked = subprocess.check_output(
            ["git", "ls-files", "-z", "--", "tools"], cwd=repo).split(b"\0")
        modules = {Path(os.fsdecode(path)).parent.as_posix()
                   for path in tracked if path.endswith(b"/go.mod")}
        self.assertEqual(modules, set(dirs) | exclusions.keys())
        self.assertFalse(set(dirs) & exclusions.keys())
        self.assertTrue(all(exclusions.values()))
        self.assertEqual(go_build.GO_TOOL_DIRS, dirs)
        build = json.loads((HERE / "go-build.json").read_text())
        self.assertEqual(build["tier"], "fast")
        self.assertEqual({path for path in build["reads"] if path.endswith("/")},
                         {directory + "/" for directory in dirs})

    def test_watch_suite_rolls_for_own_source_and_each_replacement(self):
        repo = HERE.parents[2]
        spec = importlib.util.spec_from_file_location("fork_check_watch", repo / "tools/fork-check.py")
        planner = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(planner)
        directory = "tools/tillr-watch"
        parsed = subprocess.check_output(["go", "mod", "edit", "-json"], cwd=repo / directory)
        replacements = {(repo / directory / replacement["New"]["Path"]).resolve()
                        .relative_to(repo).as_posix() + "/"
                        for replacement in json.loads(parsed)["Replace"]
                        if not replacement["New"].get("Version")}
        self.assertTrue(replacements)
        watched = {directory + "/", *replacements}
        record = planner.Record(self.root / "plan.json")
        for prefix in sorted(watched | {"tools/pi-profiler/"}):
            changed = prefix + "main.go"
            with self.subTest(changed=changed):
                plan = planner.build_plan(repo, [], record, [changed], None, True)
                planner.apply_tier(plan, planner.TIER_ROLLING)
                suites = [check for check in plan.checks
                          if check.label == f"{directory}'s whole suite passes"]
                if prefix in watched:
                    self.assertEqual(len(suites), 1)
                    self.assertEqual(suites[0].kind, "go-tool")
                    self.assertEqual(planner.check_tier(suites[0].kind), planner.TIER_ROLLING)
                    self.assertEqual(set(suites[0].watches), watched)
                    self.assertEqual(suites[0].selected_by, (changed,))
                else:
                    self.assertEqual(suites, [])

    def test_a_declaration_reading_a_go_module_reads_its_in_repo_dependencies(self):
        # A declaration that builds or runs a Go module is red when that module's in-repo
        # dependency breaks it, so `reads` that name the module and not its dependencies
        # select nothing for the change that broke it. On 2026-11-18 the tillr-view suite
        # read only its own directory and a drain-only landing turned the trunk red. The
        # dependency set is each module's relative `replace` directives (an unpublished
        # v0.0.0 module builds against a sibling only through one).
        fixture = self.root / "tools"
        (fixture / "app").mkdir(parents=True)
        (fixture / "lib").mkdir()
        (fixture / "app/go.mod").write_text("module x/app\n\nreplace x/lib => ../lib\n")
        self.assertEqual(missing_go_reads(["tools/app/"], self.root), {"tools/app/": ["tools/lib/"]})
        self.assertEqual(missing_go_reads(["tools/app/", "tools/lib/"], self.root), {})
        repo = HERE.parents[2]
        for path in sorted(HERE.glob("*.json")):
            reads = json.loads(path.read_text()).get("reads", [])
            with self.subTest(declaration=path.name):
                self.assertEqual(missing_go_reads(reads, repo), {})
        # The inventory test must run for a newly added go.mod too, not just the known ones.
        own = json.loads((HERE / "tree-checks-test.json").read_text())["reads"]
        self.assertIn("tools/", own)

    def module_fixture(self):
        module = self.root / "tool"
        module.mkdir()
        (module / "go.mod").write_text("module fixture/tool\n\ngo 1.24\n")
        (module / "main.go").write_text("package main\nfunc main() {}\n")
        subprocess.run(["git", "-C", str(self.root), "add", "."], check=True)
        subprocess.run(["git", "-C", str(self.root), "-c", "user.name=Fixture", "-c",
                        "user.email=fixture@example.invalid", "commit", "-qm", "fixture"], check=True)
        return module

    def test_module_memory_detects_nested_auto_workspace(self):
        module = self.module_fixture()
        (module / "go.work").write_text("go 1.24\nuse .\n")
        subprocess.run(["git", "-C", str(self.root), "add", "."], check=True)
        subprocess.run(["git", "-C", str(self.root), "-c", "user.name=Fixture", "-c",
                        "user.email=fixture@example.invalid", "commit", "-qm", "workspace"], check=True)
        with patch.dict(os.environ, GOWORK=""):
            keys = go_build.module_keys(self.root, [module])
        self.assertFalse(keys[module][1], "a nested auto-discovered workspace was remembered")

    def test_module_memory_rechecks_binding_before_publication(self):
        module = self.module_fixture()
        original = (module / "main.go").read_text()
        calls = []
        def build(module, scratch):
            calls.append(module)
            (module / "main.go").write_text(original + "// changed during compilation\n")
            return 0, ""
        with patch.object(go_build, "GO_TOOL_DIRS", (str(module),)), \
                patch.object(go_build.Path, "cwd", return_value=self.root), \
                patch.object(go_build, "build_module", build):
            self.assertEqual(go_build.main(), 0)
            (module / "main.go").write_text(original)
            self.assertEqual(go_build.main(), 0)
        self.assertEqual(len(calls), 2, "a build of dirty sources certified the committed tree")
        store = self.root / ".git/tillr-gate/results"
        self.assertEqual(list(store.glob("*.json")), [])

    def test_external_replacements_and_file_options_build_without_memory(self):
        module = self.module_fixture()
        external = tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"])
        self.addCleanup(external.cleanup)
        overlay = Path(external.name) / "overlay.json"
        overlay.write_text('{"Replace":{}}')
        for flags, workspace in [("-overlay=" + str(overlay), "off"), ("-gcflags=all=-N", "off"),
                                 ("", str(self.root / "custom.work"))]:
            (self.root / "custom.work").write_text("go 1.24\nuse ./tool\n")
            with patch.dict(os.environ, GOFLAGS=flags, GOWORK=workspace):
                keys = go_build.module_keys(self.root, [module])
                self.assertFalse(keys[module][1], keys)
        (module / "go.mod").write_text("module fixture/tool\n\ngo 1.24\nreplace fixture/external => " + external.name + "\n")
        with patch.object(go_build, "GO_TOOL_DIRS", (str(module),)), \
                patch.object(go_build.Path, "cwd", return_value=self.root), \
                patch.object(go_build, "build_module", return_value=(0, "")) as build:
            self.assertEqual(go_build.main(), 0)
            self.assertEqual(build.call_count, 1)

    def test_module_memory_rebuilds_local_dependents_but_not_unrelated_modules(self):
        modules = [self.root / name for name in ("a", "b", "c")]
        for module in modules:
            module.mkdir()
            (module / "go.mod").write_text("module fixture/" + module.name + "\n\ngo 1.24\n")
            (module / "main.go").write_text("package main\nfunc main() {}\n")
        (modules[1] / "go.mod").write_text("module fixture/b\n\ngo 1.24\nrequire fixture/a v0.0.0\nreplace fixture/a => ../a\n")
        def commit():
            subprocess.run(["git", "-C", str(self.root), "add", "."], check=True)
            subprocess.run(["git", "-C", str(self.root), "-c", "user.name=Fixture", "-c",
                            "user.email=fixture@example.invalid", "commit", "-qm", "fixture"], check=True)
        commit()
        seen = []
        def build(module, scratch):
            seen.append(module.name)
            return 0, ""
        with patch.object(go_build, "GO_TOOL_DIRS", tuple(map(str, modules))), \
                patch.object(go_build.Path, "cwd", return_value=self.root), \
                patch.object(go_build, "build_module", build):
            self.assertEqual(go_build.main(), 0)
            self.assertEqual(set(seen), {"a", "b", "c"})
            seen.clear()
            (modules[0] / "main.go").write_text("package main\nfunc main() { println(1) }\n")
            commit()
            self.assertEqual(go_build.main(), 0)
            self.assertEqual(set(seen), {"a", "b"})
            seen.clear()
            self.track("docs/guide.md", "# Markdown only\n")
            commit()
            self.assertEqual(go_build.main(), 0)
            self.assertEqual(seen, [])
            # Uncommitted source cannot consume a remembered pass from HEAD.
            (modules[2] / "main.go").write_text("package main\nfunc main() { println(2) }\n")
            self.assertEqual(go_build.main(), 0)
            self.assertEqual(seen, ["c"])

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

    def test_go_build_does_not_execute_testmain(self):
        module = self.root / "tool"
        module.mkdir()
        marker = self.root / "testmain-ran"
        (module / "go.mod").write_text("module fixture/tool\n\ngo 1.24\n")
        (module / "main.go").write_text("package main\nfunc main() {}\n")
        (module / "main_test.go").write_text(
            'package main\nimport ("os"; "testing")\n'
            f'func TestMain(m *testing.M) {{ os.WriteFile({json.dumps(str(marker))}, []byte("ran"), 0600); os.Exit(m.Run()) }}\n')
        code, output = go_build.build_module(module, self.root)
        self.assertEqual(code, 0, output)
        self.assertFalse(marker.exists(), "a compile check ran TestMain")

    def test_go_build_catches_a_source_compile_error(self):
        module = self.root / "tool"
        module.mkdir()
        (module / "go.mod").write_text("module fixture/tool\n\ngo 1.24\n")
        (module / "main.go").write_text("package main\nfunc main() { undefinedSourceSymbol() }\n")
        code, output = go_build.build_module(module, self.root)
        self.assertNotEqual(code, 0)
        self.assertIn("undefinedSourceSymbol", output)

    def test_go_build_trims_paths_in_every_go_command_it_starts(self):
        # Every go command the check starts trims source paths, so a new checkout
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
        self.assertIn("-exec", calls[2][0])
        self.assertEqual(calls[2][0][calls[2][0].index("-exec") + 1], "true")
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
