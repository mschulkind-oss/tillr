#!/usr/bin/env python3
"""Exercise the real format recipe and its gate declaration in scratch repositories."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
PACK = "yolo-packs/tillr/files/local/share/tillr"
BAD = b"package fixture\nfunc example(){println(1)}\n"


class FormatTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=os.environ["YOLO_DURABLE_DIR"], prefix="format-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.run_command("git", "init", "-q")
        shutil.copyfile(ROOT / ".justfile", self.root / ".justfile")
        self.run_command("git", "add", ".justfile")

    def run_command(self, *args, code=0):
        result = subprocess.run(args, cwd=self.root, capture_output=True, text=True,
                                env={**os.environ, "TMPDIR": str(self.root)})
        self.assertEqual(result.returncode, code, result.stdout + result.stderr)
        return result

    def plant(self, name, data=BAD, tracked=True):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        if tracked:
            self.run_command("git", "add", "-f", "--", name)
        return path

    def snapshot(self):
        return {str(p.relative_to(self.root)): (p.read_bytes(), p.stat().st_mtime_ns)
                for p in self.root.rglob("*") if p.is_file() and ".git" not in p.parts}

    def test_check_reports_every_file_without_writing_then_format_is_idempotent(self):
        names = ["tools/example/a.go", "tools/example/space name.go", PACK + "/tillr/a.go"]
        for name in names:
            self.plant(name)
        before = self.snapshot()
        result = self.run_command("just", "format", "--check", code=1)
        self.assertEqual(set(result.stdout.splitlines()), set(names))
        self.assertEqual(self.snapshot(), before)
        self.run_command("just", "format")
        for name in names:
            expected = subprocess.run(["gofmt"], input=BAD, capture_output=True, check=True).stdout
            self.assertEqual((self.root / name).read_bytes(), expected)
        formatted = self.snapshot()
        self.run_command("just", "format")
        self.assertEqual(self.snapshot(), formatted)
        self.assertEqual(self.run_command("just", "format", "--check").stdout, "")

    def test_excluded_tracked_files_and_untracked_go_are_untouched(self):
        names = ["tools/vendor/a.go", "tools/example/vendor/nested/a.go",
                 PACK + "/tillr/vendor/a.go", ".forks/example/a.go",
                 "tools/.forks/example/a.go", "vendor/a.go",
                 "yolo-packs/matt/briefing/prose.md", "yolo-packs/matt/skills/example/a.go",
                 "tools/example/change.patch", "yolo-packs/other/a.go", "elsewhere/a.go"]
        for name in names:
            self.plant(name)
        self.plant("tools/example/untracked.go", tracked=False)
        # A tracked symlink must not let a formatter write into excluded storage.
        link = self.root / "tools/example/link.go"
        link.symlink_to("../../vendor/a.go")
        self.run_command("git", "add", "tools/example/link.go")
        before = self.snapshot()
        self.run_command("just", "format")
        self.run_command("just", "format", "--check")
        self.assertEqual(self.snapshot(), before)

    def test_symlink_ancestor_cannot_reach_vendor(self):
        source = self.plant("tools/example/a.go")
        protected = self.plant("vendor/a.go")
        source.unlink()
        source.parent.rmdir()
        source.parent.symlink_to("../vendor", target_is_directory=True)
        # The index still lists tools/example/a.go, now reached through a symlink.
        before = self.snapshot()
        self.assertEqual(self.run_command("just", "format", "--check").stdout, "")
        self.run_command("just", "format")
        self.assertEqual(protected.read_bytes(), BAD)
        self.assertEqual(self.snapshot(), before)

    def test_empty_source_set_and_unknown_argument(self):
        self.run_command("just", "format")
        self.run_command("just", "format", "--check")
        self.run_command("just", "format", "--unknown", code=2)

    def test_gate_uses_the_recipe_and_is_red_until_formatted(self):
        declaration = json.loads((ROOT / "tools/gate/checks/format.json").read_text())
        self.assertEqual(declaration["command"], ["just", "format", "--check"])
        self.assertEqual(declaration["scope"], "tree")
        self.assertEqual(declaration["tier"], "fast")
        # gofmt has no --version, the gate's default tool identity probe.
        self.assertIs(declaration.get("remember"), False)
        regression = json.loads((ROOT / "tools/gate/checks/format-test.json").read_text())
        self.assertIs(regression.get("remember"), False)
        self.assertGreater(declaration["budget_ms"], 0)
        self.assertTrue({"just", "git", "python3", "gofmt"} <= set(declaration["tools"]))
        self.assertTrue({".justfile", "tools/", PACK + "/"} <= set(declaration["reads"]))
        self.plant("tools/example/a.go")
        before = self.snapshot()
        self.run_command(*declaration["command"], code=1)
        self.assertEqual(self.snapshot(), before)
        self.run_command("just", "format")
        self.run_command(*declaration["command"])

    def test_format_then_pack_sync_keeps_source_and_copy_identical(self):
        source = self.plant("tools/tillr/example.go")
        self.plant("tools/pack-mirror-links.py", (ROOT / "tools/pack-mirror-links.py").read_bytes())
        self.run_command("just", "format")
        self.run_command("just", "pack-sync")
        self.assertEqual(source.read_bytes(), (self.root / PACK / "tillr/example.go").read_bytes())

    def test_list_names_go_and_comment_explains_omissions(self):
        listing = self.run_command("just", "--list").stdout
        self.assertRegex(listing, r"format[^\n]*Go")
        recipe = (self.root / ".justfile").read_text()
        for phrase in ("Python", "shell", "TypeScript", "Lua", "84", "mise.toml", "own commit"):
            self.assertIn(phrase, recipe)


if __name__ == "__main__":
    unittest.main()
