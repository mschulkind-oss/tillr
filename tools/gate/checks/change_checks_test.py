#!/usr/bin/env python3
"""Permanent tests for caller-owned change checks and the loop's default judge."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

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
        self.base = self.commit()
        self.bin = self.repo / "stubs"
        self.bin.mkdir()
        uvx = self.bin / "uvx"
        uvx.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$RENDER_LOG"\n'
                       'printf "%s" "${RENDER_OUT:-}"\nprintf "%s" "${RENDER_ERR:-}" >&2\n'
                       'exit "${RENDER_RC:-0}"\n')
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
        for rc, stdout, stderr, expected in (("1", "missing document target\n", "", 1),
                                             ("1", "", "resolver failed\n", 4),
                                             ("3", "diagnostic\n", "", 4),
                                             ("127", "", "uvx missing\n", 4)):
            with self.subTest(rc=rc, expected=expected):
                self.env.update(RENDER_RC=rc, RENDER_OUT=stdout, RENDER_ERR=stderr)
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
        # The old plan for this seed/Markdown/gate-source fixture had these ten kinds.
        before = {"structural", "file-size", "commit-identity", "seed-freeze", "citations",
                  "documents", "scratch", "design-citations", "pipeline-evidence", "self-suite"}
        self.assertEqual(migrated | remaining, before)
        self.assertFalse((IDS - {"file-size"}) & remaining)

    def test_loop_program_runs_change_runner_before_remaining_program(self):
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
        self.env["RENDER_RC"] = "3"
        result = subprocess.run(["python3", "tools/gate/change-gate.py", "--base", self.base,
                                 "--head", head, "--runner", str(self.runner), "--logs", str(self.repo / "logs")],
                                cwd=self.repo, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 4)
        self.assertNotIn("remaining program ran", result.stdout)


if __name__ == "__main__":
    unittest.main()
