#!/usr/bin/env python3
"""Permanent tests for caller-owned change checks and the loop's default judge."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[3]
SCRATCH = Path(os.environ.get("YOLO_DURABLE_DIR", ROOT / "tools/gate/.scratch"))
SCRATCH.mkdir(parents=True, exist_ok=True)
IDS = {"commit-identity", "citations", "documents", "file-size", "workflows"}


class Changes(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory(dir=SCRATCH)
        cls.runner = Path(cls.build.name) / "tillr-gate"
        subprocess.run(["go", "build", "-o", str(cls.runner), "."], cwd=ROOT / "tools/tillr-gate", check=True)

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=SCRATCH)
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name)
        self.env = dict(os.environ)
        # Command scope, not shared repository config, declares the fixture identity.
        self.env.update(GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="user.name", GIT_CONFIG_VALUE_0="Fixture",
                        GIT_CONFIG_KEY_1="user.email", GIT_CONFIG_VALUE_1="fixture@example.invalid")
        self.git("init", "-q")
        (self.repo / "README.md").write_text("# Fixture\n")
        shutil.copytree(ROOT / "tools/gate", self.repo / "tools/gate", ignore=shutil.ignore_patterns("__pycache__"))
        # These cases judge routing and findings, not whether a subprocess fits a
        # production fast budget. Deadline semantics have signal-blocking Go cases.
        for path in (self.repo / "tools/gate/checks").glob("*.json"):
            declaration = json.loads(path.read_text())
            if declaration["scope"] == "change":
                declaration.update(tier="heavy", timeout_ms=60000)
                declaration.pop("budget_ms", None)
                path.write_text(json.dumps(declaration))
        self.base = self.commit()
        self.bin = self.repo / "stubs"
        self.bin.mkdir()
        uvx = self.bin / "uvx"
        # The renderer stub answers `vantage-check version` and `vantage-check check
        # --format json FILE...`, logging one line per file it was given. RENDER_FINDING is
        # an error finding on each file in RENDER_FAIL (every file when unset),
        # RENDER_FINDING_FILE spells each finding's file (`./{}` adds a leading `./`),
        # RENDER_UNCHECKED names files the report could not check, and exit 127 prints no
        # report at all, as a missing tool does.
        uvx.write_text(f"#!{sys.executable}\n" + '''import json, os, sys
env = os.environ.get
args = sys.argv[2:]
if args == ["version"]:
    print("vantage-check " + env("RENDER_VERSION", "0.0.0"))
    sys.exit(int(env("RENDER_VERSION_RC", "0")))
files = [arg for arg in args if arg not in ("check", "--format", "json")]
with open(env("RENDER_LOG"), "a") as log:
    log.writelines("vantage-check " + name + "\\n" for name in files)
rc = int(env("RENDER_RC", "0"))
sys.stderr.write(env("RENDER_ERR", ""))
if rc == 127:
    sys.exit(rc)
failing = env("RENDER_FAIL", "").split(",") if env("RENDER_FAIL") else files
spell = env("RENDER_FINDING_FILE", "{}")
findings = [{"rule": "stub/finding", "severity": "error", "message": env("RENDER_FINDING"),
             "file": spell.format(name), "line": 1, "column": 1} for name in failing if env("RENDER_FINDING")]
failures = [{"file": name, "message": "stub"} for name in env("RENDER_UNCHECKED", "").split(",") if name]
print(json.dumps({"tool": "vantage-check", "version": env("RENDER_VERSION", "0.0.0"),
                  "findings": findings, "failures": failures}))
sys.exit(rc)
''')
        uvx.chmod(0o755)
        self.env["PATH"] = str(self.bin) + os.pathsep + self.env["PATH"]
        self.env["RENDER_LOG"] = str(self.repo / "render.log")

    def git(self, *args):
        result = subprocess.run(["git", *args], cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def commit(self):
        self.git("add", "README.md", "tools")
        self.git("commit", "-qm", "chore: fixture")
        return self.git("rev-parse", "HEAD")

    def check(self, kind, base=None, head="HEAD"):
        return subprocess.run(["python3", "tools/gate/checks/" + kind + ".py", base or self.base, head],
                              cwd=self.repo, env=self.env, capture_output=True, text=True)

    def runner_check(self, *args):
        return subprocess.run([str(self.runner), "checks", "--logs", str(self.repo / "logs"), *args],
                              cwd=self.repo, env=self.env, capture_output=True, text=True)

    def roadmap(self, text):
        path = self.repo / "docs/tillr/roadmap.md"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        self.git("add", "docs")
        return self.commit()

    def test_roadmap_remembers_unchanged_sections_but_not_changed_headings(self):
        text = "# Roadmap\n\n## First\n\nOne.\n\n## Second\n\nTwo.\n"
        self.roadmap(text)
        self.assertEqual(self.check("documents").returncode, 0)
        log = self.repo / "render.log"
        self.assertEqual(len(log.read_text().splitlines()), 3)
        self.roadmap(text.replace("Two.", "Changed."))
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(len(log.read_text().splitlines()), 4)
        self.roadmap(text.replace("## Second", "## Renamed"))
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(len(log.read_text().splitlines()), 7)
        self.assertEqual(list((self.repo / "docs/tillr").glob(".gate-roadmap-*")), [])

    def test_roadmap_cross_section_markup_gets_the_full_renderer(self):
        self.roadmap("# Roadmap\n\n## First\n\n[shared][ref]\n\n## Second\n\n[ref]: target.md\n")
        result = self.check("documents")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("checking the full file", result.stderr)
        self.assertEqual((self.repo / "render.log").read_text().splitlines(),
                         ["vantage-check docs/tillr/roadmap.md"])

    def test_real_renderer_catches_a_broken_roadmap_link_and_changed_target(self):
        self.env["PATH"] = os.environ["PATH"]
        target = self.repo / "docs/tillr/target.md"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text("# Target\n\n## Live\n")
        text = "# Roadmap\n\n## First\n\n[Target](target.md#live).\n\n## Second\n\nTwo.\n"
        self.roadmap(text)
        first = self.check("documents")
        self.assertEqual(first.returncode, 0, first.stdout + first.stderr)
        self.roadmap(text.replace("Two.", "[Broken](missing-roadmap-target.md)."))
        broken = self.check("documents")
        self.assertEqual(broken.returncode, 1, broken.stdout + broken.stderr)
        self.assertIn("link/missing-target", broken.stdout)
        self.assertIn("docs/tillr/roadmap.md:9:", broken.stdout)
        self.roadmap(text)
        target.write_text("# Target\n\n## Renamed\n")
        self.roadmap(text)
        changed = self.check("documents")
        self.assertEqual(changed.returncode, 1, changed.stdout + changed.stderr)
        self.assertIn("link/dead-section-anchor", changed.stdout)

    def test_roadmap_does_not_remember_a_mutable_symlink_target(self):
        self.env["PATH"] = os.environ["PATH"]
        target = Path(self.build.name) / (self.repo.name + "-target.md")
        target.write_text("# Target\n\n## Live\n")
        linked = self.repo / "docs/tillr/linked.md"
        linked.parent.mkdir(parents=True, exist_ok=True)
        linked.symlink_to(target)
        self.git("add", "docs")
        base = self.commit()
        text = "# Roadmap\n\n## First\n\n[Target](linked.md#live).\n\n## Second\n\nTwo.\n"
        self.roadmap(text)
        result = self.check("documents", base)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        target.write_text("# Target\n\n## Removed\n")
        self.roadmap(text.replace("Two.", "Changed."))
        result = self.check("documents", base)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("link/dead-section-anchor", result.stdout)

    def test_real_renderer_catches_header_only_invalid_frontmatter(self):
        self.env["PATH"] = os.environ["PATH"]
        self.roadmap("---\ntitle: [\n---\n")
        result = self.check("documents")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("frontmatter/parse", result.stdout)

    def test_real_renderer_invalidates_indented_and_tabbed_heading_links(self):
        self.env["PATH"] = os.environ["PATH"]
        for prefix in ("  ### ", "###\t"):
            text = "# Roadmap\n\n## First\n\n[Hidden](#hidden).\n\n## Second\n\n" + prefix + "Hidden\n"
            self.roadmap(text)
            result = self.check("documents")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.roadmap(text.replace(prefix + "Hidden", prefix + "Renamed"))
            result = self.check("documents")
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertIn("link/dead-section-anchor", result.stdout)

    def test_real_renderer_does_not_split_cross_section_math(self):
        self.env["PATH"] = os.environ["PATH"]
        self.roadmap("# Roadmap\n\n## First\n\n$$\nx\n## Second\ny\n$$\n")
        result = self.check("documents")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("katex/parse", result.stdout)
        self.assertIn("checking the full file", result.stderr)

    def test_real_renderer_invalidates_self_line_links_when_page_length_changes(self):
        self.env["PATH"] = os.environ["PATH"]
        text = "# Roadmap\n\n## First\n\n[Tail](#L25).\n\n## Second\n\n" + "Padding.\n\n" * 15
        self.roadmap(text)
        result = self.check("documents")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.roadmap(text.replace("Padding.\n\n" * 15, "Short.\n"))
        result = self.check("documents")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("link/line-anchor-range", result.stdout)

    def test_real_renderer_invalidates_header_only_dependency_targets(self):
        self.env["PATH"] = os.environ["PATH"]
        target = self.repo / "docs/design/target.md"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text("# Target\n\n<!-- vantage: question id=OQ-X1 -->\n\n## Choice\n")
        self.git("add", "docs")
        base = self.commit()
        text = "---\nstatus: draft\nstage: DESIGN\ndepends-on: [../design/target.md#OQ-X1]\n---\n\n# Roadmap\n\n## First\n\nOne.\n"
        self.roadmap(text)
        result = self.check("documents", base)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        target.write_text("# Target\n\n## Removed\n")
        self.roadmap(text)
        result = self.check("documents", base)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("planning/depends-on-missing", result.stdout)

    def test_markdown_only_change_remembers_go_build_without_starting_a_compile(self):
        declaration = json.loads((ROOT / "tools/gate/checks/go-build.json").read_text())
        registry = self.repo / "build-check"
        registry.mkdir()
        (registry / "go-build.json").write_text(json.dumps(declaration))
        spec = importlib.util.spec_from_file_location("build_check", ROOT / "tools/gate/checks/go-build.py")
        build = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(build)
        for directory in build.GO_TOOL_DIRS:
            module = self.repo / directory
            module.mkdir(parents=True, exist_ok=True)
            (module / "go.mod").write_text("module fixture/" + module.name + "\n\ngo 1.24\n")
            (module / "main.go").write_text("package main\nfunc main() {}\n")
        marker = Path(self.build.name) / (self.repo.name + "-compile.log")
        goroot = subprocess.run(["go", "env", "GOROOT"], capture_output=True, text=True, check=True).stdout.strip()
        real_go = str(Path(goroot) / "bin/go")
        go = self.bin / "go"
        go.write_text(f"#!{sys.executable}\n" +
                      "import os, sys\n" +
                      f"if sys.argv[1] in ('list', 'build', 'test'):\n    with open({str(marker)!r}, 'a') as log: log.write('compile\\n')\n" +
                      f"os.execv({real_go!r}, [{real_go!r}, *sys.argv[1:]])\n")
        go.chmod(0o755)
        self.git("add", ".")
        self.git("commit", "-qm", "chore: build inputs")
        base = self.git("rev-parse", "HEAD")
        logs = Path(self.build.name) / (self.repo.name + "-logs")
        def run_build():
            result = subprocess.run([str(self.runner), "checks", "--base", base, "--head", "HEAD",
                                     "--fast-tree", "--checks", str(registry), "--logs", str(logs)],
                                    cwd=self.repo, env=self.env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            return json.loads(result.stdout)["payload"]["checks"]
        first = run_build()
        self.assertEqual(first[0]["memory"], "passed", first)
        compiles = marker.read_text()
        self.assertTrue(compiles)
        (self.repo / "README.md").write_text("# Markdown-only change\n")
        self.git("add", "README.md")
        self.git("commit", "-qm", "docs: fixture")
        second = run_build()
        self.assertEqual(len(second), 1, second)
        self.assertEqual(second[0]["memory"], "remembered", second)
        self.assertEqual(marker.read_text(), compiles, "remembered build started a compiler")

    def test_runner_requires_base_and_ancestor(self):
        result = self.runner_check("--head", "HEAD")
        self.assertEqual(result.returncode, 4)
        self.assertIn("could not judge", result.stderr)
        self.assertIn("--base", result.stderr)
        (self.repo / "README.md").write_text("# Future\n")
        future = self.commit()
        result = self.runner_check("--base", future, "--head", self.base)
        self.assertEqual(result.returncode, 4)
        self.assertIn("ancestor", result.stderr)
        for kind in IDS:
            result = self.check(kind, future, self.base)
            self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
            self.assertIn("ancestor", result.stderr)
            result = subprocess.run(["python3", "tools/gate/checks/" + kind + ".py"], cwd=self.repo,
                                    env=self.env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 4)
            self.assertIn("base", result.stderr)

    def test_worktree_selects_its_own_default_declarations(self):
        path = self.repo / "tools/gate/checks/citations.json"
        declaration = json.loads(path.read_text())
        declaration["id"] = "fixture-citations"
        path.write_text(json.dumps(declaration))
        result = subprocess.run([str(self.runner), "checks", "--worktree", str(self.repo),
                                 "--base", self.base, "--head", "HEAD", "--logs", str(self.repo / "logs")],
                                cwd=ROOT, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        ids = {check["check_id"] for check in json.loads(result.stdout)["payload"]["checks"]}
        self.assertIn("fixture-citations", ids)

    def test_markdown_change_runs_all_declared_checks_against_caller_base(self):
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        head = self.commit()
        result = self.runner_check("--base", self.base, "--head", head)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        payload = json.loads(result.stdout)["payload"]
        judged = {check["check_id"]: check["result"] for check in payload["checks"]}
        self.assertTrue(IDS <= judged.keys(), judged)
        self.assertTrue(all(judged[kind] == "pass" for kind in IDS), judged)
        self.assertEqual(payload["base_sha"], self.base)
        self.assertEqual((self.repo / "render.log").read_text(), "vantage-check README.md\n")
        checks = {check["check_id"]: check for check in payload["checks"]}
        for kind in IDS:
            # The command stays silent; an unavailable watchdog is separately
            # reported in both the verdict and the log, never silently ignored.
            diagnostic = checks[kind].get("watchdog_unavailable", "")
            if diagnostic:
                self.assertTrue(diagnostic.startswith("watchdog unavailable: "), diagnostic)
            expected_log = diagnostic + "\n" if diagnostic else ""
            self.assertEqual(next((self.repo / "logs").glob("*-" + kind + ".log")).read_text(), expected_log)

    def test_added_size_refusal_and_tree_debt(self):
        giant = self.repo / "tools/giant.ts"
        giant.write_text("line\n" * 3001)
        head = self.commit()
        result = self.check("file-size")
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("3,001 lines added", result.stdout)
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        self.commit()
        self.assertEqual(self.check("file-size", head).returncode, 0)

    def test_citations_markers_fences_and_upstream_pins(self):
        self.git("checkout", "-qb", "lane-only")
        (self.repo / "README.md").write_text("# Side\n")
        side = self.commit()
        self.git("checkout", "-q", "-")
        for text, rc in ((f"Evidence {side}\n", 1), (f"[unlanded] Evidence {side}\n", 0),
                         (f"```\nEvidence {side}\n```\n", 0), (f"Evidence {self.base}\n", 0)):
            (self.repo / "README.md").write_text("# Fixture\n\n" + text)
            self.commit()
            result = self.check("citations")
            self.assertEqual(result.returncode, rc, result.stdout + result.stderr)
        self.git("update-ref", "refs/remotes/upstream/main", side)
        (self.repo / "README.md").write_text("# Fixture\n\nEvidence " + side + "\n")
        self.commit()
        self.assertEqual(self.check("citations").returncode, 0)

    def test_identity_does_not_discover_changed_files_or_documents(self):
        spec = importlib.util.spec_from_file_location("identity_fast_common", ROOT / "tools/gate/checks/change_common.py")
        common = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(common)
        with mock.patch.object(common, "validate") as validate, \
                mock.patch.object(common, "identity_findings", return_value=[]) as identity, \
                mock.patch.object(common, "git", side_effect=AssertionError("identity inspected unrelated changed files")):
            self.assertEqual(common.run("commit-identity", [self.base, "HEAD"]), 0)
        validate.assert_called_once_with(self.base, "HEAD")
        identity.assert_called_once_with(Path.cwd(), self.base, "HEAD")

    def test_identity_mismatch_and_local_override(self):
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        self.commit()
        self.assertEqual(self.check("commit-identity").returncode, 0)
        self.git("config", "--local", "user.name", "Wrong")
        result = self.check("commit-identity")
        self.assertEqual(result.returncode, 1)
        self.assertIn("repo-local", result.stdout)
        self.git("config", "--local", "--unset-all", "user.name")
        self.env["GIT_AUTHOR_NAME"] = "Wrong"
        (self.repo / "README.md").write_text("# Wrong\n")
        self.commit()
        self.assertEqual(self.check("commit-identity").returncode, 1)

    def test_worktree_identity_override_is_refused(self):
        self.git("config", "extensions.worktreeConfig", "true")
        self.git("config", "--worktree", "user.name", "Wrong")
        result = self.check("commit-identity")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("worktree", result.stdout)

    def test_missing_deployed_identity_is_not_rejection(self):
        self.env.update(GIT_CONFIG_COUNT="0", GIT_CONFIG_GLOBAL="/dev/null",
                        GIT_CONFIG_SYSTEM="/dev/null", GIT_CONFIG_NOSYSTEM="1")
        result = self.runner_check("--base", self.base, "--head", "HEAD")
        self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
        self.assertEqual(json.loads(result.stdout)["payload"]["result"], "error")

    def test_unreadable_identity_is_not_a_rejection(self):
        actual_git = shutil.which("git")
        stub = self.bin / "git"
        stub.write_text('#!/bin/sh\nif [ "$1" = "config" ] || [ "$3" = "config" ]; then echo "cannot read config" >&2; exit 2; fi\n'
                        + 'exec ' + actual_git + ' "$@"\n')
        stub.chmod(0o755)
        result = self.check("commit-identity")
        self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
        self.assertIn("could not judge", result.stderr)

    def test_missing_judgment_outranks_an_earlier_rejection(self):
        self.env["GIT_AUTHOR_NAME"] = "Wrong"
        (self.repo / "README.md").write_text("# Changed\n")
        self.commit()
        self.env["RENDER_RC"] = "3"
        result = self.runner_check("--base", self.base, "--head", "HEAD")
        self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
        self.assertEqual(json.loads(result.stdout)["payload"]["result"], "error")

    def test_renderer_findings_and_unavailable_tool_are_distinct(self):
        (self.repo / "README.md").write_text("# Changed\n")
        self.commit()
        for rc, finding, stderr, expected in (("1", "missing document target", "", 1),
                                             ("1", "", "resolver failed\n", 4),
                                             ("3", "diagnostic", "", 4),
                                             ("127", "", "uvx missing\n", 4)):
            with self.subTest(rc=rc, expected=expected):
                self.env.update(RENDER_RC=rc, RENDER_FINDING=finding, RENDER_ERR=stderr)
                result = self.check("documents")
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)

    def test_unavailable_renderer_is_not_rejection(self):
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        self.commit()
        self.env["RENDER_RC"] = "3"
        result = self.runner_check("--base", self.base, "--head", "HEAD")
        self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
        self.assertEqual(json.loads(result.stdout)["payload"]["result"], "error")
        self.env["RENDER_RC"] = "0"
        (self.repo / "README.md").write_text("# Dirty\n")
        self.assertEqual(self.check("documents").returncode, 4)

    def renders(self):
        log = self.repo / "render.log"
        return log.read_text().splitlines() if log.exists() else []

    def test_an_unchanged_document_pass_is_remembered(self):
        (self.repo / "other.md").write_text("# Other\n\n## Target\n")
        (self.repo / "README.md").write_text("# Fixture\n\nSee [other](other.md#target).\n")
        self.git("add", "other.md")
        head = self.commit()
        first = self.check("documents", head="HEAD")
        self.assertEqual(first.returncode, 0, first.stdout + first.stderr)
        self.assertEqual(sorted(self.renders()), ["vantage-check README.md", "vantage-check other.md"])
        again = self.check("documents")
        self.assertEqual(again.returncode, 0, again.stdout + again.stderr)
        self.assertEqual(len(self.renders()), 2, "a remembered pass was rendered again")
        self.assertIn("documents: README.md: remembered pass at ", again.stderr)
        self.assertIn("2 document(s): 0 rendered, 2 remembered", again.stderr)
        # A later commit that touches neither document nor anything they name keeps both
        # remembered: the memory is per document, not per tree.
        (self.repo / "tools/unrelated.json").write_text("{}\n")
        self.commit()
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(len(self.renders()), 2, "an unrelated commit forgot a remembered pass")

    def test_a_changed_document_or_a_changed_target_is_rendered_again(self):
        (self.repo / "other.md").write_text("# Other\n\n## Target\n")
        (self.repo / "README.md").write_text("# Fixture\n\nSee [other](other.md#target).\n")
        self.git("add", "other.md")
        self.commit()
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(len(self.renders()), 2)
        # The document's own blob changed.
        (self.repo / "README.md").write_text("# Fixture\n\nSee [other](other.md#target), again.\n")
        self.commit()
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(self.renders()[2:], ["vantage-check README.md"])
        # Only the link target changed: README's blob is the same, but the heading it
        # names is gone, so its remembered pass is not its verdict any more.
        (self.repo / "other.md").write_text("# Other\n\n## Renamed\n")
        self.git("add", "other.md")
        self.commit()
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(sorted(self.renders()[3:]), ["vantage-check README.md", "vantage-check other.md"])

    def test_a_remembered_fail_is_never_reused_as_a_pass(self):
        (self.repo / "README.md").write_text("# Fixture\n\nBroken.\n")
        self.commit()
        self.env.update(RENDER_RC="1", RENDER_FINDING="missing target")
        self.assertEqual(self.check("documents").returncode, 1)
        # The same blob, now rendered green: that run's pass is reported, but a key that
        # ever failed is rendered every time, so neither verdict is reused.
        self.env.update(RENDER_RC="0", RENDER_FINDING="")
        for _ in range(2):
            result = self.check("documents")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn("remembered", result.stderr)
        self.assertEqual(len(self.renders()), 3)
        self.env.update(RENDER_RC="1", RENDER_FINDING="missing target")
        self.assertEqual(self.check("documents").returncode, 1)

    def test_a_pass_from_another_renderer_version_is_judged_again(self):
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        self.commit()
        self.env["RENDER_VERSION"] = "1.0.0"
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(len(self.renders()), 1, "the same renderer's pass was not remembered")
        self.env["RENDER_VERSION"] = "2.0.0"
        result = self.check("documents")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("not from renderer 2.0.0", result.stderr)
        self.assertEqual(len(self.renders()), 2)
        # A renderer that cannot say its version cannot confirm a remembered pass either.
        self.env["RENDER_VERSION_RC"] = "3"
        self.assertEqual(self.check("documents").returncode, 0)
        self.assertEqual(len(self.renders()), 3)

    def test_a_report_without_a_version_remembers_nothing(self):
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        self.commit()
        self.env["RENDER_VERSION"] = ""
        for _ in range(2):
            result = self.check("documents")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.renders()), 2)

    def test_a_failing_run_remembers_only_what_it_judged(self):
        (self.repo / "other.md").write_text("# Other\n")
        (self.repo / "third.md").write_text("# Third\n")
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        self.git("add", "other.md", "third.md")
        self.commit()
        # README fails, other.md could not be checked, third.md has no finding.
        self.env.update(RENDER_RC="1", RENDER_FINDING="finding", RENDER_FAIL="README.md",
                        RENDER_UNCHECKED="other.md")
        result = self.check("documents")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("could not check", result.stdout)
        self.assertEqual(len(self.renders()), 3)
        # Next run: only third.md was a judged pass, so README (a fail) and other.md (not
        # checked) are rendered again and third.md is remembered.
        self.env.update(RENDER_RC="0", RENDER_FINDING="", RENDER_FAIL="", RENDER_UNCHECKED="")
        result = self.check("documents")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(sorted(self.renders()[3:]), ["vantage-check README.md", "vantage-check other.md"])
        self.assertIn("documents: third.md: remembered pass at ", result.stderr)

    def test_a_failing_run_attributes_its_findings_before_remembering(self):
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        self.commit()
        # The reviewer's stub: the renderer exits 1 and spells the failing file `./README.md`.
        # A different spelling of a pending document is still that document's finding;
        # a finding on a file the run was not given makes the run's verdict unattributable.
        for spelling, why in (("./{}", "a pending document spelled with ./"),
                              ("elsewhere.md", "a file the run was not given")):
            with self.subTest(why):
                shutil.rmtree(self.repo / ".git/tillr-gate", ignore_errors=True)
                self.env.update(RENDER_RC="1", RENDER_FINDING="finding", RENDER_FINDING_FILE=spelling)
                before = len(self.renders())
                for attempt in (1, 2):
                    result = self.check("documents")
                    self.assertEqual(result.returncode, 1, f"run {attempt}: " + result.stdout + result.stderr)
                    self.assertNotIn("remembered pass", result.stderr)
                self.assertEqual(self.renders()[before:], ["vantage-check README.md"] * 2)

    def test_a_pass_that_rests_on_the_working_tree_is_not_remembered(self):
        # The renderer reads the working tree; the memory key reads the commit. A pass
        # whose input exists only on disk, or differs on disk from the commit, is not the
        # commit's verdict, so it must not reach another checkout through the store.
        (self.repo / ".gitignore").write_text("scratch/\n")
        (self.repo / "docs").mkdir()
        (self.repo / "docs/b.md").write_text("# B\n\n## Yes\n")
        self.git("add", ".gitignore", "docs")
        base = self.commit()
        (self.repo / "docs/a.md").write_text("# A\n\nSee [b](b.md#yes) and [n](../scratch/n.md).\n")
        self.git("add", "docs")
        self.commit()
        cases = (
            ("a linked file that is ignored but on disk",
             lambda: ((self.repo / "scratch").mkdir(), (self.repo / "scratch/n.md").write_text("# N\n")),
             lambda: shutil.rmtree(self.repo / "scratch")),
            ("a linked tracked file edited on disk",
             lambda: (self.repo / "docs/b.md").write_text("# B\n"),
             lambda: self.git("checkout", "--", "docs/b.md")),
            ("a renderer config on disk that the commit lacks",
             lambda: (self.repo / "docs/.vantage.toml").write_text("[rules]\n"),
             lambda: (self.repo / "docs/.vantage.toml").unlink()),
        )
        for why, make, undo in cases:
            with self.subTest(why):
                shutil.rmtree(self.repo / '.git/tillr-gate', ignore_errors=True)
                make()
                before = len(self.renders())
                result = self.check("documents", base)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                undo()
                result = self.check("documents", base)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertNotIn("remembered pass", result.stderr)
                self.assertEqual(self.renders()[before:], ["vantage-check docs/a.md"] * 2)
                # The clean checkout's own pass is remembered as before.
                self.assertIn("remembered pass", self.check("documents", base).stderr)

    def test_plan_union_preserves_the_old_fixture_kinds(self):
        shutil.copy(ROOT / "tools/fork-check.py", self.repo / "tools/fork-check.py")
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        self.commit()
        result = subprocess.run(["python3", "tools/fork-check.py", "--explain", "--since", self.base],
                                cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        selection = next(line.removeprefix("fork-check: selection ") for line in result.stdout.splitlines()
                         if line.startswith("fork-check: selection "))
        remaining = {check["kind"] for check in json.loads(selection)["checks"]}
        declarations = self.repo / "migrated-checks"
        declarations.mkdir()
        for kind in IDS:
            shutil.copy(self.repo / "tools/gate/checks" / (kind + ".json"), declarations)
        ran = self.runner_check("--base", self.base, "--head", "HEAD", "--checks", str(declarations))
        self.assertEqual(ran.returncode, 0, ran.stdout + ran.stderr)
        migrated = {check["check_id"] for check in json.loads(ran.stdout)["payload"]["checks"]}
        # Migrated change checks remain disjoint. The planner also schedules fast
        # whole-tree declarations, so its execution inventory includes those kinds
        # without reintroducing them as legacy implementations in the registry.
        before = {"file-size", "commit-identity", "citations",
                  "documents", "pipeline-evidence", "self-suite", "workflows"}
        tree_ids = {"scratch", "design-citations", "structural", "pack-sync", "go-build", "format"}
        self.assertEqual(migrated | remaining, before | tree_ids)
        self.assertFalse((IDS - {"file-size"}) & remaining)
        self.assertEqual(tree_ids & remaining, tree_ids)
        exported = subprocess.run(["python3", "tools/fork-check.py", "--registry-json"],
                                  cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(exported.returncode, 0, exported.stdout + exported.stderr)
        legacy_registry = json.loads(exported.stdout)
        legacy_ids = {check["id"] for check in legacy_registry["declarations"]}
        legacy_ids.update(check["id"] for check in legacy_registry["in_process"])
        declaration_ids = {json.loads(path.read_text())["id"]
                           for path in (self.repo / "tools/gate/checks").glob("*.json")}
        self.assertFalse(legacy_ids & declaration_ids, legacy_ids & declaration_ids)
        for kind in tree_ids:
            declaration = json.loads((self.repo / "tools/gate/checks" / (kind + ".json")).read_text())
            self.assertEqual(declaration["scope"], "tree")
            self.assertEqual(declaration["tier"], "fast")

    def test_added_cyclic_workflow_fails_real_check(self):
        result = subprocess.run(["python3", str(ROOT / "tools/gate/checks/workflows.test.py")],
                                cwd=ROOT, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("added cycle fails workflow gate", result.stdout)

    def stub_tree_commands(self):
        # Only the fixture commands change: the real scope/tier/id declarations
        # determine the adapter's executed inventory, independently of the legacy gate.
        for path in (self.repo / "tools/gate/checks").glob("*.json"):
            declaration = json.loads(path.read_text())
            if declaration["scope"] == "tree":
                declaration.update(command=["true"], remember=False)
                path.write_text(json.dumps(declaration))

    def test_adapter_executes_the_complete_disjoint_fast_inventory(self):
        self.stub_tree_commands()
        (self.repo / "tools/fork-check.py").write_text('print("legacy inventory: file-size warning")\n')
        (self.repo / "README.md").write_text("# Changed\n")
        head = self.commit()
        result = subprocess.run(["python3", "tools/gate/change-gate.py", "--base", self.base,
                                 "--head", head, "--runner", str(self.runner), "--logs", str(self.repo / "logs")],
                                cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        verdict, legacy = result.stdout.splitlines()
        checks = json.loads(verdict)["payload"]["checks"]
        expected = IDS | {"gate-budget", "scratch", "design-citations", "structural", "pack-sync", "go-build", "format"}
        self.assertEqual({check["check_id"] for check in checks}, expected)
        self.assertEqual(len(checks), len(expected))
        self.assertTrue(all(check["result"] == "pass" for check in checks))
        self.assertEqual(legacy, "legacy inventory: file-size warning")

    def test_real_format_declarations_execute_on_clean_tree(self):
        # Unlike the routing fixtures, use the actual declarations and tools. A
        # clean tree exercises result caching's tool probes unless explicitly disabled.
        shutil.copy(ROOT / ".justfile", self.repo / ".justfile")
        (self.repo / "tools/example").mkdir()
        (self.repo / "tools/example/a.go").write_text("package example\n")
        (self.repo / "tests").mkdir()
        # Stand in for the suite body to avoid recursively running the suites here;
        # the heavy declaration's command, tools, and result-caching policy stay real.
        (self.repo / "tests/format.test.py").write_text('print("format fixture ran")\n')
        self.git("add", ".justfile", "tests")
        head = self.commit()
        declarations = self.repo / "format-checks"
        declarations.mkdir()
        for name in ("format", "format-test"):
            shutil.copy(ROOT / "tools/gate/checks" / (name + ".json"), declarations)
        for flag, name in (("--fast-tree", "format"), ("--heavy-tree", "format-test")):
            with self.subTest(check=name):
                result = self.runner_check(flag, "--base", self.base, "--head", head,
                                           "--checks", str(declarations))
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                checks = json.loads(result.stdout)["payload"]["checks"]
                self.assertEqual([(check["check_id"], check["result"]) for check in checks],
                                 [(name, "pass")])

    def test_loop_program_runs_change_runner_before_remaining_program(self):
        self.stub_tree_commands()
        (self.repo / "tools/fork-check.py").write_text('print("remaining program ran")\n')
        (self.repo / "README.md").write_text("# Fixture\n\nEdited.\n")
        head = self.commit()
        result = subprocess.run(["python3", "tools/gate/change-gate.py", "--base", self.base,
                                 "--head", head, "--runner", str(self.runner), "--logs", str(self.repo / "logs")],
                                cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        verdict, remaining = result.stdout.splitlines()
        self.assertEqual(json.loads(verdict)["payload"]["base_sha"], self.base)
        self.assertEqual(remaining, "remaining program ran")
        # The renderer is unavailable outright: it cannot check, and it cannot say which
        # version it is, so the pass the first run remembered cannot be confirmed either.
        self.env.update(RENDER_RC="3", RENDER_VERSION_RC="3")
        result = subprocess.run(["python3", "tools/gate/change-gate.py", "--base", self.base,
                                 "--head", head, "--runner", str(self.runner), "--logs", str(self.repo / "logs")],
                                cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 4)
        self.assertNotIn("remaining program ran", result.stdout)

    def test_a_fixer_range_runs_the_remaining_program_untiered(self):
        # A trunk fixer's gate is `tillr-loop gate --since <green>` or `--all`, which the
        # adapter hands to this judge. The change checks still take the candidate's range;
        # the remaining program takes the fixer's, with no landing tier.
        self.stub_tree_commands()
        (self.repo / "tools/fork-check.py").write_text('import sys\nprint(" ".join(sys.argv[1:]))\n')
        (self.repo / "README.md").write_text("# Fixture\n\nFixed.\n")
        head = self.commit()
        common = ["python3", "tools/gate/change-gate.py", "--base", self.base, "--head", head,
                  "--runner", str(self.runner), "--logs", str(self.repo / "logs")]
        # The append-only gate record has no landing-lock wait. Pin the exact
        # forwarded arguments so neither a stale lock flag nor a tier returns.
        for extra, want in (([], f"--since {self.base} --tier landing"),
                            (["--since", self.base], f"--since {self.base}"),
                            (["--all"], "--all")):
            result = subprocess.run(common + extra, cwd=self.repo, env=self.env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.splitlines()[-1], want)
        result = subprocess.run(common + ["--all", "--since", self.base], cwd=self.repo, env=self.env,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 2, result.stderr)

    def test_loop_program_keeps_the_verdict_of_a_slow_tree_check(self):
        # The loop's gate is where carrying a fast tree check is switched: until a heavy
        # runner takes what is carried (item-gate-heavy-runner), carrying would drop the
        # check's judgment, so the program asks the runner to keep it. The stub runner
        # records its arguments and fails, so nothing after it runs.
        (self.repo / "README.md").write_text("# Changed\n")
        head = self.commit()
        runner = self.bin / "recording-runner"
        runner.write_text(f"#!{sys.executable}\nimport json, sys\n"
                          f"open({str(self.repo / 'runner-args.json')!r}, 'w').write(json.dumps(sys.argv[1:]))\n"
                          "sys.exit(1)\n")
        runner.chmod(0o755)
        result = subprocess.run(["python3", "tools/gate/change-gate.py", "--base", self.base,
                                 "--head", head, "--runner", str(runner), "--logs", str(self.repo / "logs")],
                                cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        args = json.loads((self.repo / "runner-args.json").read_text())
        self.assertEqual(args[:2], ["checks", "--fast-tree"])
        self.assertEqual(args[args.index("--tree-overrun") + 1], "keep")

    def test_loop_program_warms_the_trimmed_go_cache_once_outside_the_ceilings(self):
        # go-build trims source paths, and the first trimmed build on a machine compiles the
        # standard library and every module from cold (19.9 s measured 2026-11-16), past
        # the 9 s ceiling. The gate program pays that once per Go version, before the fast
        # tier and outside its ceilings, so no item is held for a cold cache. A failed warm
        # is not a verdict: the check itself still judges the candidate.
        log = self.repo / "warm.log"
        (self.repo / "tools/gate/checks/go-build.py").write_text(
            f"import sys\nopen({str(log)!r}, 'a').write('warm\\n')\nsys.exit(int({str(self.repo / 'warm-rc')!r} and "
            f"open({str(self.repo / 'warm-rc')!r}).read() or 0))\n")
        (self.repo / "warm-rc").write_text("4")
        (self.repo / "README.md").write_text("# Changed\n")
        head = self.commit()
        runner = self.bin / "recording-runner"
        runner.write_text(f"#!{sys.executable}\nimport sys\nopen({str(log)!r}, 'a').write('runner\\n')\nsys.exit(1)\n")
        runner.chmod(0o755)
        gate = ["python3", "tools/gate/change-gate.py", "--base", self.base, "--head", head,
                "--runner", str(runner), "--logs", str(self.repo / "logs")]
        for rc in ("4", "0", "0"):
            (self.repo / "warm-rc").write_text(rc)
            result = subprocess.run(gate, cwd=self.repo, env=self.env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        # The failed warm is tried again; the completed one is not repeated.
        self.assertEqual(log.read_text().split(), ["warm", "runner", "warm", "runner", "runner"])


if __name__ == "__main__":
    unittest.main()
