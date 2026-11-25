#!/usr/bin/env python3
"""Prototype 3: Finer shards backed by per-shard content-addressed result records.

Partitions a Go package's top-level Test* functions (weighted by
tools/go-test-durations.json) into configurable shard sizes, executes uncached
shards with bounded concurrency, and supports partway interruption/restart.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import proto_content_cache as pcc  # noqa: E402

PRESET_TESTS_PER_SHARD = {
    "coarse": 40,
    "medium": 10,
    "fine": 2,
}


def discover_tests(module_dir: Path, import_path: str) -> list[str]:
    """List top-level Test* functions in import_path."""
    proc = subprocess.run(
        ["go", "test", "-list", "^Test", import_path],
        cwd=module_dir,
        capture_output=True,
        text=True,
        check=True,
    )
    tests: list[str] = []
    for line in proc.stdout.splitlines():
        s = line.strip()
        if s.startswith("Test") and not s.startswith("ok ") and " " not in s:
            tests.append(s)
    return tests


def load_durations(repo_root: Path) -> dict[str, dict[str, float]]:
    path = repo_root / "tools" / "go-test-durations.json"
    if not path.is_file():
        return {}
    data = json.loads(path.read_text(encoding="utf-8"))
    return data if isinstance(data, dict) else {}


def partition_tests(
    tests: list[str],
    *,
    durations: dict[str, float] | None = None,
    shard_count: int | None = None,
    tests_per_shard: int | None = None,
) -> list[dict[str, Any]]:
    """Partition tests into balanced shards using LPT bin-packing."""
    if not tests:
        return []
    dur_map = durations or {}
    if shard_count is None:
        tps = max(1, tests_per_shard or 10)
        shard_count = max(1, (len(tests) + tps - 1) // tps)
    shard_count = max(1, min(shard_count, len(tests)))

    # Longest processing time first, tie-break alphabetically
    weighted = sorted(
        tests,
        key=lambda t: (-float(dur_map.get(t, 0.1)), t),
    )
    bins: list[list[str]] = [[] for _ in range(shard_count)]
    loads: list[float] = [0.0 for _ in range(shard_count)]

    for t in weighted:
        idx = min(range(shard_count), key=lambda i: (loads[i], len(bins[i]), i))
        bins[idx].append(t)
        loads[idx] += float(dur_map.get(t, 0.1))

    shards: list[dict[str, Any]] = []
    for idx, group in enumerate(bins):
        if not group:
            continue
        ordered = sorted(group)
        pattern = "^(" + "|".join(ordered) + ")$"
        shards.append(
            {
                "shard_index": idx,
                "tests": ordered,
                "test_count": len(ordered),
                "run_pattern": pattern,
                "estimated_seconds": round(loads[idx], 3),
            }
        )
    return shards


def _run_one_shard(
    repo_root: Path,
    import_path: str,
    pkg_dir: Path,
    bin_path: Path,
    bin_sha: str,
    cache_dir: Path,
    shard: dict[str, Any],
    rel_env: dict[str, str],
    scratch_dir: Path,
) -> dict[str, Any]:
    args = [f"-test.run={shard['run_pattern']}"]
    edge_digest = pcc.compute_declared_edge_digest(
        repo_root,
        import_path,
        active_tests=shard["tests"],
    )
    key = pcc.compute_cache_key(
        binary_sha256=bin_sha,
        args=args,
        edge_digest=edge_digest,
        env=rel_env,
    )
    record_path = cache_dir / f"{key}.json"
    if record_path.is_file():
        rec = json.loads(record_path.read_text(encoding="utf-8"))
        if rec.get("exit_code") == 0 and rec.get("key") == key:
            return {
                "shard_index": shard["shard_index"],
                "test_count": shard["test_count"],
                "cache_hit": True,
                "exit_code": 0,
                "run_ms": 0,
                "key": key,
            }

    run_tmp = scratch_dir / f"shard-{shard['shard_index']}"
    run_tmp.mkdir(parents=True, exist_ok=True)
    run_env = os.environ.copy()
    run_env.update(rel_env)
    run_env["TMPDIR"] = str(run_tmp)

    t0 = time.monotonic()
    proc = subprocess.run(
        [str(bin_path), *pcc.normalize_test_args(args)],
        cwd=pkg_dir,
        env=run_env,
        capture_output=True,
        text=True,
    )
    run_ms = int((time.monotonic() - t0) * 1000)

    if proc.returncode == 0:
        record = {
            "schema": 1,
            "key": key,
            "import_path": import_path,
            "args": pcc.normalize_test_args(args),
            "binary_sha256": bin_sha,
            "edge_digest": edge_digest,
            "env": rel_env,
            "exit_code": 0,
            "run_ms": run_ms,
        }
        tmp_rec = cache_dir / f".tmp-{key}-{os.getpid()}-{shard['shard_index']}.json"
        tmp_rec.write_text(json.dumps(record, indent=2), encoding="utf-8")
        tmp_rec.replace(record_path)

    return {
        "shard_index": shard["shard_index"],
        "test_count": shard["test_count"],
        "cache_hit": False,
        "exit_code": proc.returncode,
        "run_ms": run_ms,
        "key": key,
        "output_tail": (proc.stdout + proc.stderr)[-800:] if proc.returncode != 0 else "",
    }


def filter_tests(
    tests: list[str],
    *,
    run_pattern: str | None = None,
    max_tests: int | None = None,
) -> list[str]:
    """Filter discovered tests by regex and optional deterministic cap."""
    selected = list(tests)
    if run_pattern:
        rx = re.compile(run_pattern)
        selected = [t for t in selected if rx.search(t)]
    if max_tests is not None:
        selected = selected[: max(0, max_tests)]
    return selected


def run_sharded_package(
    repo_root: Path,
    pkg_spec: str,
    cache_dir: Path,
    *,
    preset: str = "medium",
    shard_count: int | None = None,
    tests_per_shard: int | None = None,
    run_pattern: str | None = None,
    max_tests: int | None = None,
    workers: int = 4,
    stop_after_shards: int | None = None,
    scratch_dir: Path | None = None,
) -> dict[str, Any]:
    t0 = time.monotonic()
    info = pcc.resolve_package(repo_root, pkg_spec)
    import_path = info["import_path"]
    pkg_dir = Path(info["pkg_dir"])
    module_dir = Path(info["module_dir"])

    tests = filter_tests(
        discover_tests(module_dir, import_path),
        run_pattern=run_pattern,
        max_tests=max_tests,
    )
    all_durations = load_durations(repo_root)
    pkg_durations = all_durations.get(import_path, {})

    tps = tests_per_shard or PRESET_TESTS_PER_SHARD.get(preset, 10)
    shards = partition_tests(
        tests,
        durations=pkg_durations,
        shard_count=shard_count,
        tests_per_shard=tps,
    )

    cache_dir.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(
        prefix="proto3-shards-",
        dir=str(scratch_dir) if scratch_dir else None,
    ) as tmp:
        tmp_path = Path(tmp)
        bin_path = tmp_path / "pkg.test"
        t_comp = time.monotonic()
        has_tests, _ = pcc.compile_test_binary(module_dir, import_path, bin_path)
        compile_ms = int((time.monotonic() - t_comp) * 1000)

        if not has_tests or not shards:
            return {
                "import_path": import_path,
                "total_tests": len(tests),
                "total_shards": len(shards),
                "processed_shards": 0,
                "shard_hits": 0,
                "shard_misses": 0,
                "tests_skipped": 0,
                "tests_executed": 0,
                "stopped_early": False,
                "compile_ms": compile_ms,
                "wall_ms": int((time.monotonic() - t0) * 1000),
                "shards": [],
            }

        bin_sha = pcc.sha256_file(bin_path)
        rel_env = pcc.collect_relevant_env()

        active_shards = shards[:stop_after_shards] if stop_after_shards is not None else shards
        stopped_early = stop_after_shards is not None and stop_after_shards < len(shards)

        shard_results: list[dict[str, Any]] = []
        with ThreadPoolExecutor(max_workers=max(1, workers)) as pool:
            futs = [
                pool.submit(
                    _run_one_shard,
                    repo_root,
                    import_path,
                    pkg_dir,
                    bin_path,
                    bin_sha,
                    cache_dir,
                    sh,
                    rel_env,
                    tmp_path,
                )
                for sh in active_shards
            ]
            for fut in as_completed(futs):
                shard_results.append(fut.result())

        shard_results.sort(key=lambda r: r["shard_index"])
        hits = sum(1 for r in shard_results if r["cache_hit"])
        misses = sum(1 for r in shard_results if not r["cache_hit"])
        tests_skipped = sum(r["test_count"] for r in shard_results if r["cache_hit"])
        tests_executed = sum(r["test_count"] for r in shard_results if not r["cache_hit"])

        return {
            "import_path": import_path,
            "total_tests": len(tests),
            "total_shards": len(shards),
            "processed_shards": len(shard_results),
            "shard_hits": hits,
            "shard_misses": misses,
            "tests_skipped": tests_skipped,
            "tests_executed": tests_executed,
            "stopped_early": stopped_early,
            "compile_ms": compile_ms,
            "wall_ms": int((time.monotonic() - t0) * 1000),
            "shards": shard_results,
        }


def verify_wrong_skip(scratch_dir: Path) -> dict[str, Any]:
    """Verify that changing a test or declared input invalidates only/all affected shards."""
    with tempfile.TemporaryDirectory(prefix="proto3-wrongskip-", dir=str(scratch_dir)) as td:
        ws = Path(td)
        mod = ws / "tools" / "sample"
        mod.mkdir(parents=True)
        (mod / "go.mod").write_text("module example.com/sample\n\ngo 1.21\n", encoding="utf-8")
        (mod / "sample_test.go").write_text(
            'package sample\n\nimport (\n\t"os"\n\t"testing"\n)\n\n'
            'func TestAlpha(t *testing.T) {\n'
            '\tb, err := os.ReadFile("../../data/a.txt")\n'
            '\tif err != nil || string(b) != "a" { t.Fatalf("bad a") }\n'
            '}\n\n'
            'func TestBeta(t *testing.T) {\n'
            '\tb, err := os.ReadFile("../../data/b.txt")\n'
            '\tif err != nil || string(b) != "b" { t.Fatalf("bad b") }\n'
            '}\n',
            encoding="utf-8",
        )
        (ws / "data").mkdir()
        (ws / "data" / "a.txt").write_text("a", encoding="utf-8")
        (ws / "data" / "b.txt").write_text("b", encoding="utf-8")
        gate_dir = ws / "tools" / "gate"
        gate_dir.mkdir(parents=True)
        (gate_dir / "go-test-edges.json").write_text(
            json.dumps(
                {
                    "example.com/sample": {
                        "builds": [],
                        "reads": ["data/a.txt", "data/b.txt"],
                        "read_tests": {
                            "data/a.txt": ["TestAlpha"],
                            "data/b.txt": ["TestBeta"],
                        },
                    }
                }
            ),
            encoding="utf-8",
        )
        cache_dir = ws / "cache"
        r1 = run_sharded_package(ws, "tools/sample", cache_dir, tests_per_shard=1, scratch_dir=ws)
        # Mutate only data/a.txt -> only TestAlpha shard should miss and fail!
        (ws / "data" / "a.txt").write_text("changed", encoding="utf-8")
        r2 = run_sharded_package(ws, "tools/sample", cache_dir, tests_per_shard=1, scratch_dir=ws)
        return {
            "cold_misses": r1["shard_misses"],
            "mutated_hits": r2["shard_hits"],
            "mutated_misses": r2["shard_misses"],
            "mutated_failed_shards": sum(1 for s in r2["shards"] if s["exit_code"] != 0),
        }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", default=".", help="Repository root")
    parser.add_argument("--cache-dir", required=True, help="Shard cache directory")
    parser.add_argument("--pkg", default="", help="Go package to shard and run")
    parser.add_argument("--preset", choices=["coarse", "medium", "fine"], default="medium")
    parser.add_argument("--shard-count", type=int, default=None)
    parser.add_argument("--tests-per-shard", type=int, default=None)
    parser.add_argument("--run", dest="run_pattern", default=None, help="Regex filter on Test* names")
    parser.add_argument("--max-tests", type=int, default=None, help="Limit to first N selected tests")
    parser.add_argument("--workers", type=int, default=4)
    parser.add_argument("--stop-after-shards", type=int, default=None)
    parser.add_argument("--scratch-dir", default=None)
    parser.add_argument("--test-wrong-skip", action="store_true")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)

    scratch = Path(args.scratch_dir) if args.scratch_dir else None
    if scratch:
        scratch.mkdir(parents=True, exist_ok=True)

    if args.test_wrong_skip:
        out = verify_wrong_skip(scratch or Path(tempfile.gettempdir()))
        print(json.dumps(out, indent=2))
        return 0 if (out["cold_misses"] == 2 and out["mutated_hits"] == 1 and out["mutated_misses"] == 1 and out["mutated_failed_shards"] == 1) else 1

    if not args.pkg:
        parser.error("--pkg is required unless --test-wrong-skip is set")

    res = run_sharded_package(
        Path(args.repo_root).resolve(),
        args.pkg,
        Path(args.cache_dir).resolve(),
        preset=args.preset,
        shard_count=args.shard_count,
        tests_per_shard=args.tests_per_shard,
        run_pattern=args.run_pattern,
        max_tests=args.max_tests,
        workers=args.workers,
        stop_after_shards=args.stop_after_shards,
        scratch_dir=scratch,
    )
    if args.json:
        print(json.dumps(res, indent=2))
    else:
        print(
            f"proto_fine_shards: {res['import_path']} tests={res['total_tests']} "
            f"shards={res['processed_shards']}/{res['total_shards']} "
            f"(hits={res['shard_hits']}, misses={res['shard_misses']}, "
            f"tests_skipped={res['tests_skipped']}, tests_executed={res['tests_executed']}, "
            f"compile_ms={res['compile_ms']}, wall_ms={res['wall_ms']})"
        )
    any_failed = any(s["exit_code"] != 0 for s in res["shards"])
    return 1 if any_failed else 0


if __name__ == "__main__":
    sys.exit(main())
