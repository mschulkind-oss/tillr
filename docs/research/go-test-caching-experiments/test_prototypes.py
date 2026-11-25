#!/usr/bin/env python3
"""Deterministic stdlib unittest suite for Go test caching experiment prototypes."""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))

import baseline_timings  # noqa: E402
import proto_content_cache  # noqa: E402
import proto_fine_shards  # noqa: E402
import proto_sandbox_inputs  # noqa: E402


def _make_sample_repo(ws: Path) -> None:
    mod = ws / "tools" / "sample"
    mod.mkdir(parents=True, exist_ok=True)
    (mod / "go.mod").write_text("module example.com/sample\n\ngo 1.21\n", encoding="utf-8")
    (mod / "sample_test.go").write_text(
        'package sample\n\nimport (\n\t"os"\n\t"testing"\n)\n\n'
        'func TestOne(t *testing.T) {\n'
        '\tb, err := os.ReadFile("../../data/one.txt")\n'
        '\tif err != nil || string(b) != "one" {\n'
        '\t\tt.Fatalf("unexpected: %v %q", err, string(b))\n'
        '\t}\n'
        '}\n\n'
        'func TestTwo(t *testing.T) {\n'
        '\tb, err := os.ReadFile("../../data/two.txt")\n'
        '\tif err != nil || string(b) != "two" {\n'
        '\t\tt.Fatalf("unexpected: %v %q", err, string(b))\n'
        '\t}\n'
        '}\n',
        encoding="utf-8",
    )
    (ws / "data").mkdir(parents=True, exist_ok=True)
    (ws / "data" / "one.txt").write_text("one", encoding="utf-8")
    (ws / "data" / "two.txt").write_text("two", encoding="utf-8")
    (ws / "undeclared").mkdir(parents=True, exist_ok=True)
    (ws / "undeclared" / "secret.txt").write_text("secret", encoding="utf-8")
    gate = ws / "tools" / "gate"
    gate.mkdir(parents=True, exist_ok=True)
    (gate / "go-test-edges.json").write_text(
        json.dumps(
            {
                "example.com/sample": {
                    "builds": [],
                    "reads": ["data/one.txt", "data/two.txt"],
                    "read_tests": {
                        "data/one.txt": ["TestOne"],
                        "data/two.txt": ["TestTwo"],
                    },
                }
            }
        ),
        encoding="utf-8",
    )


class BaselineTimingsTests(unittest.TestCase):
    def test_parses_go_build_go_affected_and_logs(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            run1 = root / "run-1"
            (run1 / "change-logs").mkdir(parents=True)
            (run1 / "gate.timing.jsonl").write_text(
                json.dumps(
                    {
                        "run_id": "run-1",
                        "checks": {
                            "go-build": {
                                "go_build": [
                                    {
                                        "module": "tools/a",
                                        "packages": [
                                            {"import_path": "pkg/a", "test_cache": "hit"},
                                            {"import_path": "pkg/b", "test_cache": "miss"},
                                            {"import_path": "pkg/c", "test_cache": "not-applicable"},
                                        ],
                                    }
                                ]
                            },
                            "go-affected": {
                                "packages": [
                                    {
                                        "import_path": "pkg/a",
                                        "reason": "direct",
                                        "cached": True,
                                        "reused": False,
                                        "shards": [{"cached": True}, {"cached": False}],
                                    },
                                    {
                                        "import_path": "pkg/b",
                                        "reason": "rdep",
                                        "cached": False,
                                        "reused": False,
                                    },
                                ]
                            },
                        },
                    }
                )
                + "\n",
                encoding="utf-8",
            )
            (run1 / "change-logs" / "07-go-affected.log").write_text(
                "go-affected: pass pkg/a (cached)\n"
                "go-affected: pass pkg/b (0.25s)\n"
                "go-affected: 2 package(s) (1 cached, selection 10ms, slot_wait 0ms)\n",
                encoding="utf-8",
            )

            rep = baseline_timings.analyze_timings(root)
            self.assertEqual(rep["timing_files"], 1)
            self.assertEqual(rep["records_read"], 1)
            self.assertEqual(rep["go_build"]["overall"]["hit"], 1)
            self.assertEqual(rep["go_build"]["overall"]["miss"], 1)
            self.assertEqual(rep["go_build"]["overall"]["not_applicable"], 1)
            self.assertEqual(rep["go_affected"]["overall"]["hit"], 1)
            self.assertEqual(rep["go_affected"]["overall"]["total"], 2)
            self.assertEqual(rep["go_affected"]["by_reason"]["direct"]["hit"], 1)
            self.assertEqual(rep["go_affected"]["by_reason"]["rdep"]["miss"], 1)
            self.assertEqual(rep["go_affected"]["shards_overall"]["hit"], 1)
            self.assertEqual(rep["go_affected"]["shards_overall"]["total"], 2)
            self.assertEqual(rep["go_affected_logs"]["overall"]["hit"], 1)
            self.assertEqual(rep["go_affected_logs"]["summary_lines"]["cached_packages"], 1)


class ContentCacheTests(unittest.TestCase):
    def test_key_stability_across_worktrees_and_invalidation(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            base = Path(td)
            wt1 = base / "wt-alpha"
            wt2 = base / "wt-beta-longer-path"
            _make_sample_repo(wt1)
            _make_sample_repo(wt2)
            cache = base / "cache"

            r1 = proto_content_cache.run_package_cached(
                wt1, "tools/sample", cache, scratch_dir=base
            )
            self.assertFalse(r1["cache_hit"])
            self.assertEqual(r1["exit_code"], 0)

            # Second worktree with byte-identical inputs must hit the same key
            r2 = proto_content_cache.run_package_cached(
                wt2, "tools/sample", cache, scratch_dir=base
            )
            self.assertTrue(r2["cache_hit"])
            self.assertEqual(r1["key"], r2["key"])

            # Changing -run arg invalidates key
            r_arg = proto_content_cache.run_package_cached(
                wt1, "tools/sample", cache, run_pattern="^TestOne$", scratch_dir=base
            )
            self.assertFalse(r_arg["cache_hit"])
            self.assertNotEqual(r1["key"], r_arg["key"])

            # Mutating declared input invalidates key and fails on rerun
            (wt1 / "data" / "one.txt").write_text("corrupted", encoding="utf-8")
            r_mut = proto_content_cache.run_package_cached(
                wt1, "tools/sample", cache, scratch_dir=base
            )
            self.assertFalse(r_mut["cache_hit"])
            self.assertNotEqual(r1["key"], r_mut["key"])
            self.assertNotEqual(r_mut["exit_code"], 0)

    def test_stop_after_and_restart(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            base = Path(td)
            ws = base / "ws"
            _make_sample_repo(ws)
            # Add a second package in the same module
            pkg2 = ws / "tools" / "sample" / "sub"
            pkg2.mkdir(parents=True)
            (pkg2 / "sub_test.go").write_text(
                'package sub\n\nimport "testing"\n\nfunc TestSub(t *testing.T) {}\n',
                encoding="utf-8",
            )
            cache = base / "cache"

            part = proto_content_cache.run_batch(
                ws, ["tools/sample", "tools/sample/sub"], cache, stop_after=1, scratch_dir=base
            )
            self.assertTrue(part["stopped_early"])
            self.assertEqual(part["processed"], 1)
            self.assertEqual(part["misses"], 1)

            resumed = proto_content_cache.run_batch(
                ws, ["tools/sample", "tools/sample/sub"], cache, scratch_dir=base
            )
            self.assertFalse(resumed["stopped_early"])
            self.assertEqual(resumed["hits"], 1)
            self.assertEqual(resumed["misses"], 1)

    def test_wrong_skip_helper(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            out = proto_content_cache.verify_wrong_skip(Path("."), Path(td))
            self.assertFalse(out["cold_hit"])
            self.assertTrue(out["repeat_hit"])
            self.assertFalse(out["mutated_hit"])
            self.assertTrue(out["key_changed"])
            self.assertNotEqual(out["mutated_exit_code"], 0)


class SandboxInputsTests(unittest.TestCase):
    def test_probe_reports_mechanisms(self) -> None:
        res = proto_sandbox_inputs.probe_mechanisms()
        self.assertIn("landlock", res)
        self.assertIn("unshare_rm", res)
        self.assertIn("bwrap", res)
        self.assertIn("strace", res)

    def test_landlock_blocks_undeclared_read(self) -> None:
        ll = proto_sandbox_inputs.probe_landlock()
        if not ll["available"]:
            self.skipTest(f"Landlock unavailable: {ll['error']}")
        with tempfile.TemporaryDirectory() as td:
            out = proto_sandbox_inputs.verify_undeclared_vs_declared(Path(td))
            self.assertEqual(out["declared_exit"], 0)
            self.assertNotEqual(out["undeclared_exit"], 0)
            self.assertTrue(out["undeclared_eacces"])


class FineShardsTests(unittest.TestCase):
    def test_partition_and_partial_restart(self) -> None:
        tests = ["TestA", "TestB", "TestC", "TestD", "TestE"]
        shards = proto_fine_shards.partition_tests(
            tests,
            durations={"TestA": 5.0, "TestB": 4.0, "TestC": 1.0, "TestD": 1.0, "TestE": 1.0},
            tests_per_shard=2,
        )
        self.assertEqual(len(shards), 3)
        all_names = sorted(t for s in shards for t in s["tests"])
        self.assertEqual(all_names, sorted(tests))

        with tempfile.TemporaryDirectory() as td:
            base = Path(td)
            ws = base / "ws"
            _make_sample_repo(ws)
            cache = base / "cache"

            part = proto_fine_shards.run_sharded_package(
                ws, "tools/sample", cache, tests_per_shard=1, stop_after_shards=1, scratch_dir=base
            )
            self.assertTrue(part["stopped_early"])
            self.assertEqual(part["processed_shards"], 1)
            self.assertEqual(part["tests_executed"], 1)

            resumed = proto_fine_shards.run_sharded_package(
                ws, "tools/sample", cache, tests_per_shard=1, scratch_dir=base
            )
            self.assertFalse(resumed["stopped_early"])
            self.assertEqual(resumed["shard_hits"], 1)
            self.assertEqual(resumed["shard_misses"], 1)
            self.assertEqual(resumed["tests_skipped"], 1)
            self.assertEqual(resumed["tests_executed"], 1)

    def test_wrong_skip_scoped_invalidation(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            out = proto_fine_shards.verify_wrong_skip(Path(td))
            self.assertEqual(out["cold_misses"], 2)
            self.assertEqual(out["mutated_hits"], 1)
            self.assertEqual(out["mutated_misses"], 1)
            self.assertEqual(out["mutated_failed_shards"], 1)

    def test_filter_tests_and_cli_selection(self) -> None:
        tests = ["TestAlpha", "TestBeta", "TestGamma", "TestDelta"]
        self.assertEqual(
            proto_fine_shards.filter_tests(tests, run_pattern=r"^Test(Alpha|Gamma)$"),
            ["TestAlpha", "TestGamma"],
        )
        self.assertEqual(
            proto_fine_shards.filter_tests(tests, max_tests=2),
            ["TestAlpha", "TestBeta"],
        )
        self.assertEqual(
            proto_fine_shards.filter_tests(tests, run_pattern=r"a$", max_tests=2),
            ["TestAlpha", "TestBeta"],
        )

        with tempfile.TemporaryDirectory() as td:
            base = Path(td)
            ws = base / "ws"
            _make_sample_repo(ws)
            cache = base / "cache"
            rc = proto_fine_shards.main(
                [
                    "--repo-root",
                    str(ws),
                    "--cache-dir",
                    str(cache),
                    "--pkg",
                    "tools/sample",
                    "--run",
                    "^Test",
                    "--max-tests",
                    "1",
                    "--shard-count",
                    "1",
                    "--scratch-dir",
                    str(base),
                ]
            )
            self.assertEqual(rc, 0)
            records = list(cache.glob("*.json"))
            self.assertEqual(len(records), 1)
            rec = json.loads(records[0].read_text(encoding="utf-8"))
            self.assertEqual(rec["args"], ["-test.run=^(TestOne)$"])


if __name__ == "__main__":
    unittest.main()
