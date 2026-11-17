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
IDS = {"commit-identity", "seed-freeze", "citations", "documents", "file-size"}


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
        (self.repo / "config/tillr").mkdir(parents=True)
        self.seed = self.repo / "config/tillr/seed-items.json"
        self.seed.write_text(json.dumps({"items": [{"item_id": "item-a", "title": "A"},
                                                  {"item_id": "item-b", "title": "B"}]}))
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
findings = [{"rule": "stub/finding", "severity": "error", "message": env("RENDER_FINDING"),
             "file": name, "line": 1, "column": 1} for name in failing if env("RENDER_FINDING")]
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
        self.git("add", "config", "README.md", "tools")
        self.git("commit", "-qm", "chore: fixture")
        return self.git("rev-parse", "HEAD")

    def check(self, kind, base=None, head="HEAD"):
        return subprocess.run(["python3", "tools/gate/checks/" + kind + ".py", base or self.base, head],
                              cwd=self.repo, env=self.env, capture_output=True, text=True)

    def runner_check(self, *args):
        return subprocess.run([str(self.runner), "checks", "--logs", str(self.repo / "logs"), *args],
                              cwd=self.repo, env=self.env, capture_output=True, text=True)

    def test_recorded_baseline_predating_seed_is_not_a_change_base(self):
        result = subprocess.run(["python3", str(ROOT / "tools/gate/checks/change-checks.test.py")],
                                cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

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

    def test_markdown_change_runs_all_five_against_caller_base(self):
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
        for kind in IDS:
            self.assertEqual(next((self.repo / "logs").glob("*-" + kind + ".log")).read_text(), "")

    def test_seed_only_shrinks(self):
        for entries, rc, needle in (([{"item_id": "item-a", "title": "A"}], 0, ""),
                                   ([{"item_id": "item-c"}], 1, "net-new"),
                                   ([{"item_id": "item-a", "title": "changed"}], 1, "title changed")):
            self.seed.write_text(json.dumps({"items": entries}))
            self.commit()
            result = self.check("seed-freeze")
            self.assertEqual(result.returncode, rc, result.stdout + result.stderr)
            self.assertIn(needle, result.stdout)
        self.seed.unlink()
        self.commit()
        self.assertEqual(self.check("seed-freeze").returncode, 0)

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
        (self.repo / "config/tillr/unrelated.json").write_text("{}\n")
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
        # Change checks remain disjoint from the legacy program. The two Markdown
        # conventions and structural proof now belong to the tree declarations.
        before = {"file-size", "commit-identity", "seed-freeze", "citations",
                  "documents", "pipeline-evidence", "self-suite"}
        self.assertEqual(migrated | remaining, before)
        self.assertFalse((IDS - {"file-size"}) & remaining)
        tree_ids = {"scratch", "design-citations", "structural", "pack-sync", "go-build"}
        self.assertFalse(tree_ids & remaining)
        exported = subprocess.run(["python3", "tools/fork-check.py", "--registry-json"],
                                  cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(exported.returncode, 0, exported.stdout + exported.stderr)
        legacy_registry = json.loads(exported.stdout)
        legacy_ids = {check["id"] for check in legacy_registry["declarations"]}
        legacy_ids.update(check["id"] for check in legacy_registry["in_process"])
        declaration_ids = {json.loads(path.read_text())["id"]
                           for path in (self.repo / "tools/gate/checks").glob("*.json")}
        self.assertFalse(legacy_ids & declaration_ids, legacy_ids & declaration_ids)
        self.assertEqual(migrated | remaining | tree_ids,
                         before | {"scratch", "design-citations", "structural", "pack-sync", "go-build"})
        for kind in tree_ids:
            declaration = json.loads((self.repo / "tools/gate/checks" / (kind + ".json")).read_text())
            self.assertEqual(declaration["scope"], "tree")
            self.assertEqual(declaration["tier"], "fast")

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
        expected = IDS | {"gate-budget", "scratch", "design-citations", "structural", "pack-sync", "go-build"}
        self.assertEqual({check["check_id"] for check in checks}, expected)
        self.assertEqual(len(checks), len(expected))
        self.assertTrue(all(check["result"] == "pass" for check in checks))
        self.assertEqual(legacy, "legacy inventory: file-size warning")

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
        for extra, want in (([], f"--since {self.base} --tier landing --lock-timeout 30"),
                            (["--since", self.base], f"--since {self.base} --lock-timeout 30"),
                            (["--all"], "--all --lock-timeout 30")):
            result = subprocess.run(common + extra, cwd=self.repo, env=self.env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.splitlines()[-1], want)
        result = subprocess.run(common + ["--all", "--since", self.base], cwd=self.repo, env=self.env,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 2, result.stderr)


if __name__ == "__main__":
    unittest.main()
