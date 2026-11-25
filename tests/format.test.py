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
        self.cache = self.root / ".git" / "gofmt-cache"
        self.run_command("git", "init", "-q")
        shutil.copyfile(ROOT / ".justfile", self.root / ".justfile")
        self.run_command("git", "add", ".justfile")

    def run_command(self, *args, code=0, extra_env=None):
        env = {
            **os.environ,
            "TMPDIR": str(self.root),
            "JUST_FORMAT_CACHE_DIR": str(self.cache),
            "XDG_CONFIG_HOME": str(self.root / ".git" / "xdg-config"),
        }
        if extra_env:
            env.update(extra_env)
        result = subprocess.run(args, cwd=self.root, capture_output=True, text=True, env=env)
        self.assertEqual(result.returncode, code, result.stdout + result.stderr)
        return result

    def real_gofmt(self):
        for entry in os.environ.get("PATH", "").split(os.pathsep):
            cand = os.path.join(entry, "gofmt")
            if entry and os.path.isfile(cand) and os.access(cand, os.X_OK):
                if os.path.basename(os.path.realpath(cand)) != "mise":
                    return cand
        return shutil.which("gofmt")

    def install_counting_gofmt(self, version_tag="v1"):
        bin_dir = self.root / ".git" / "test-bin"
        bin_dir.mkdir(parents=True, exist_ok=True)
        log_path = self.root / ".git" / "gofmt-calls.jsonl"
        real = self.real_gofmt()
        wrapper = bin_dir / "gofmt"
        wrapper.write_text(
            "#!/usr/bin/env python3\n"
            f"# {version_tag}\n"
            "import json, os, sys\n"
            f"with open({str(log_path)!r}, 'a', encoding='utf-8') as f:\n"
            "    f.write(json.dumps(sys.argv[1:]) + '\\n')\n"
            f"os.execv({real!r}, [{real!r}, *sys.argv[1:]])\n",
            encoding="utf-8",
        )
        wrapper.chmod(0o755)
        path_env = f"{bin_dir}{os.pathsep}{os.environ.get('PATH', '')}"
        return log_path, {"PATH": path_env}

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
        self.plant("tools/tillr/go.mod", (ROOT / "tools/tillr/go.mod").read_bytes())
        for rel_dir in ("tools/tillr/packmirror", "tools/tillr/cmd/pack-mirror"):
            for src_file in sorted((ROOT / rel_dir).rglob("*")):
                if src_file.is_file():
                    self.plant(src_file.relative_to(ROOT).as_posix(), src_file.read_bytes())
        self.run_command("just", "format")
        self.run_command("just", "pack-sync")
        self.assertEqual(source.read_bytes(), (self.root / PACK / "tillr/example.go").read_bytes())

    def test_list_names_go_and_comment_explains_omissions(self):
        listing = self.run_command("just", "--list").stdout
        self.assertRegex(listing, r"format[^\n]*Go")
        recipe = (self.root / ".justfile").read_text()
        for phrase in ("Python", "shell", "TypeScript", "Lua", "84", "mise.toml", "own commit"):
            self.assertIn(phrase, recipe)

    def test_duplicate_digests_and_clean_blob_cache_preserve_all_verdicts(self):
        clean_src = b"package fixture\n"
        self.plant("tools/tillr/a.go", BAD)
        self.plant(PACK + "/tillr/a.go", BAD)
        self.plant("tools/tillr/clean.go", clean_src)
        self.plant(PACK + "/tillr/clean.go", clean_src)
        log_path, env = self.install_counting_gofmt("v1")

        def read_calls():
            if not log_path.exists():
                return []
            calls = [json.loads(line) for line in log_path.read_text().splitlines() if line]
            log_path.unlink()
            return calls

        first = self.run_command("just", "format", "--check", code=1, extra_env=env)
        self.assertEqual(set(first.stdout.splitlines()), {"tools/tillr/a.go", PACK + "/tillr/a.go"})
        calls = read_calls()
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0][0], "-l")
        # Identical mirror blobs are checked once per unique digest, not twice per file.
        self.assertEqual(len(calls[0][1:]), 2)

        second = self.run_command("just", "format", "--check", code=1, extra_env=env)
        self.assertEqual(set(second.stdout.splitlines()), {"tools/tillr/a.go", PACK + "/tillr/a.go"})
        calls = read_calls()
        self.assertEqual(len(calls), 1)
        # Clean blobs are remembered; unformatted blobs are never cached as clean.
        self.assertEqual(len(calls[0][1:]), 1)

        self.run_command("just", "format", extra_env=env)
        calls = read_calls()
        self.assertEqual([c[0] for c in calls], ["-l", "-w"])
        self.assertEqual(len(calls[0][1:]), 1)
        self.assertEqual(set(calls[1][1:]), {"tools/tillr/a.go", PACK + "/tillr/a.go"})

        # Fully clean tree hits the clean-digest cache without spawning gofmt.
        self.assertEqual(self.run_command("just", "format", "--check", extra_env=env).stdout, "")
        self.run_command("just", "format", extra_env=env)
        self.assertEqual(read_calls(), [])

        # An unstaged working-tree edit is hashed from disk and caught immediately.
        (self.root / "tools/tillr/clean.go").write_bytes(BAD)
        unstaged = self.run_command("just", "format", "--check", code=1, extra_env=env)
        self.assertEqual(unstaged.stdout.splitlines(), ["tools/tillr/clean.go"])
        self.run_command("just", "format", extra_env=env)
        read_calls()

        # Changing the gofmt binary invalidates the cache key and re-verifies blobs.
        log_path, env_v2 = self.install_counting_gofmt("v2")
        self.assertEqual(self.run_command("just", "format", "--check", extra_env=env_v2).stdout, "")
        calls_v2 = read_calls()
        self.assertEqual(len(calls_v2), 1)
        self.assertEqual(calls_v2[0][0], "-l")

    def test_duplicate_syntax_errors_and_corrupt_or_unwritable_cache(self):
        broken = b"package fixture\nfunc (\n"
        self.plant("tools/tillr/broken.go", broken)
        self.plant(PACK + "/tillr/broken.go", broken)
        for cmd in (("just", "format", "--check"), ("just", "format")):
            result = self.run_command(*cmd, code=2)
            self.assertIn("tools/tillr/broken.go:", result.stderr)
            self.assertIn(PACK + "/tillr/broken.go:", result.stderr)

        self.plant("tools/tillr/broken.go", BAD)
        self.plant(PACK + "/tillr/broken.go", BAD)
        self.cache.mkdir(parents=True, exist_ok=True)
        for existing in self.cache.glob("*.bin"):
            existing.write_bytes(b"truncated")
        blocked_cache = self.root / ".git" / "not-a-directory"
        blocked_cache.write_text("file blocking mkdir\n")
        for extra in (None, {"JUST_FORMAT_CACHE_DIR": str(blocked_cache)}):
            check = self.run_command("just", "format", "--check", code=1, extra_env=extra)
            self.assertEqual(set(check.stdout.splitlines()),
                             {"tools/tillr/broken.go", PACK + "/tillr/broken.go"})
        self.run_command("just", "format", extra_env={"JUST_FORMAT_CACHE_DIR": str(blocked_cache)})
        self.assertEqual(
            self.run_command("just", "format", "--check",
                             extra_env={"JUST_FORMAT_CACHE_DIR": str(blocked_cache)}).stdout,
            "",
        )

    def test_inactive_mise_shim_is_bypassed_until_mise_configures_go(self):
        self.plant("tools/example/a.go", b"package fixture\n")
        shim_dir = self.root / ".git" / "shim-bin"
        real_dir = self.root / ".git" / "real-bin"
        shim_dir.mkdir(parents=True)
        real_dir.mkdir(parents=True)
        fake_mise = shim_dir / "mise"
        fake_mise.write_text("#!/bin/sh\necho 'mise shim invoked' >&2\nexit 99\n")
        fake_mise.chmod(0o755)
        (shim_dir / "gofmt").symlink_to(fake_mise)
        (real_dir / "gofmt").symlink_to(self.real_gofmt())
        env = {"PATH": os.pathsep.join([str(shim_dir), str(real_dir), os.environ.get("PATH", "")])}

        # Substring 'go' inside 'cargo:viu' must not activate the mise shim.
        (self.root / "mise.toml").write_text('[tools]\njust = "1.58.0"\n"cargo:viu" = "latest"\n')
        self.run_command("just", "format", "--check", extra_env=env)

        # Explicitly pinning go in mise.toml routes through the mise shim.
        (self.root / "mise.toml").write_text('[tools]\ngo = "1.27.3"\n')
        res = self.run_command("just", "format", "--check", code=99, extra_env=env)
        self.assertIn("mise shim invoked", res.stderr)


if __name__ == "__main__":
    unittest.main()
