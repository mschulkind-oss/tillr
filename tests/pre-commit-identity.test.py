#!/usr/bin/env python3
"""Exercise installed hooks in private repositories, never the owner's git directory."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

class IdentityHookTests(unittest.TestCase):
    def setUp(self):
        scratch = Path(os.environ.get("TMPDIR") or os.environ.get("YOLO_DURABLE_DIR") or ROOT / ".yolo" / "durable")
        scratch.mkdir(parents=True, exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(dir=scratch)
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        config = self.root / "gitconfig"
        config.write_text('[user]\n name = Owner\n email = owner@example.invalid\n')
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        self.env.update(GIT_CONFIG_GLOBAL=str(config), GIT_CONFIG_NOSYSTEM="1")
        self.git("init", "-q", "-b", "main")
        self.hooks = self.root / ".git" / "hooks"
        install = subprocess.run([str(ROOT / "bin/hooks-install"), "--root", str(ROOT), "--hooks-dir", str(self.hooks)], env=self.env, capture_output=True, text=True)
        self.assertEqual(install.returncode, 0, install.stderr)
        self.git("commit", "--allow-empty", "-qm", "base")

    def git(self, *args, extra=None, ok=True):
        p = subprocess.run(["git", "-C", str(self.root), *args], env=self.env | (extra or {}), capture_output=True, text=True)
        if ok:
            self.assertEqual(p.returncode, 0, p.stderr)
        return p

    def test_author_and_committer_overrides_are_refused(self):
        for extra in [{"GIT_AUTHOR_NAME": "Other"}, {"GIT_COMMITTER_EMAIL": "other@example.invalid"}]:
            p = self.git("commit", "--allow-empty", "-qm", "foreign", extra=extra, ok=False)
            self.assertNotEqual(p.returncode, 0)
            self.assertIn("pre-commit: foreign identity on main", p.stderr)
            self.assertIn("Owner <owner@example.invalid>", p.stderr)
        p = self.git("commit", "--allow-empty", "--author=Other <other@example.invalid>", "-qm", "foreign", ok=False)
        self.assertNotEqual(p.returncode, 0)

    def test_lane_and_detached_commits_are_not_guarded(self):
        self.git("checkout", "-qb", "lane")
        self.git("commit", "--allow-empty", "-qm", "candidate", extra={"GIT_AUTHOR_NAME": "Other"})
        self.git("checkout", "--detach", "-q")
        self.git("commit", "--allow-empty", "-qm", "detached", extra={"GIT_AUTHOR_NAME": "Other"})

    def test_local_identity_cannot_replace_the_owner(self):
        self.git("config", "user.name", "Other")
        p = self.git("commit", "--allow-empty", "-qm", "local", ok=False)
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("local config", p.stderr)

    def test_local_hook_is_chained(self):
        local = self.hooks / "pre-commit.local"
        local.write_text('#!/bin/sh\necho local-hook >&2\nexit 7\n')
        local.chmod(0o755)
        p = self.git("commit", "--allow-empty", "-qm", "local guard", ok=False)
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("local-hook", p.stderr)

if __name__ == "__main__":
    unittest.main()
