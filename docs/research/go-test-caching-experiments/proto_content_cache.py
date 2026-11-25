#!/usr/bin/env python3
"""Prototype 1: Content-addressed Go test result cache.

Compiles a Go package test binary with `go test -trimpath -c`, computes a
SHA-256 cache key from (test binary bytes, normalized test arguments,
declared edge input digest from tools/gate/go-test-edges.json, relevant env),
stores passing result records in a cache directory, and skips execution on hit.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any

DEFAULT_ENV_KEYS = ("GOOS", "GOARCH", "CGO_ENABLED")


def _try_import_go_affected(repo_root: Path):
    gate_dir = repo_root / "tools" / "gate"
    if not (gate_dir / "go_affected.py").is_file():
        return None
    gate_str = str(gate_dir)
    if gate_str not in sys.path:
        sys.path.insert(0, gate_str)
    try:
        import go_affected  # type: ignore

        return go_affected
    except Exception:
        return None


def resolve_package(repo_root: Path, pkg_spec: str) -> dict[str, str]:
    """Resolve a Go package import path or module-relative path."""
    # If pkg_spec looks like tools/tillr-land/land or ./land, find its module
    candidate_dir = repo_root / pkg_spec
    if candidate_dir.is_dir():
        mod_dir = candidate_dir
        while mod_dir != repo_root.parent and not (mod_dir / "go.mod").is_file():
            mod_dir = mod_dir.parent
        if (mod_dir / "go.mod").is_file():
            rel = candidate_dir.relative_to(mod_dir).as_posix()
            target = "." if rel == "." else f"./{rel}"
            proc = subprocess.run(
                ["go", "list", "-json", target],
                cwd=mod_dir,
                capture_output=True,
                text=True,
                check=True,
            )
            info = json.loads(proc.stdout)
            return {
                "import_path": info["ImportPath"],
                "pkg_dir": info["Dir"],
                "module_dir": info.get("Module", {}).get("Dir", str(mod_dir)),
            }

    # Otherwise search modules under tools/tillr* and repo_root
    search_dirs = [repo_root]
    tools_dir = repo_root / "tools"
    if tools_dir.is_dir():
        for gm in sorted(tools_dir.glob("tillr*/go.mod")):
            search_dirs.append(gm.parent)
    for mod_dir in search_dirs:
        if not (mod_dir / "go.mod").is_file():
            continue
        proc = subprocess.run(
            ["go", "list", "-json", pkg_spec],
            cwd=mod_dir,
            capture_output=True,
            text=True,
        )
        if proc.returncode == 0 and proc.stdout.strip():
            info = json.loads(proc.stdout)
            return {
                "import_path": info["ImportPath"],
                "pkg_dir": info["Dir"],
                "module_dir": info.get("Module", {}).get("Dir", str(mod_dir)),
            }
    raise RuntimeError(f"Could not resolve Go package: {pkg_spec}")


def compile_test_binary(
    module_dir: Path,
    import_path: str,
    out_bin: Path,
) -> tuple[bool, str]:
    """Compile a Go test binary with -trimpath -c. Returns (has_tests, stderr)."""
    out_bin.parent.mkdir(parents=True, exist_ok=True)
    proc = subprocess.run(
        ["go", "test", "-trimpath", "-c", "-o", str(out_bin), import_path],
        cwd=module_dir,
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"go test -trimpath -c failed for {import_path}:\n{proc.stderr}"
        )
    if not out_bin.is_file():
        # Package has no test files
        return False, proc.stderr
    return True, proc.stderr


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        while True:
            chunk = fh.read(65536)
            if not chunk:
                break
            h.update(chunk)
    return h.hexdigest()


def compute_declared_edge_digest(
    repo_root: Path,
    import_path: str,
    *,
    active_tests: list[str] | None = None,
) -> str:
    """Compute SHA-256 of declared edge inputs from tools/gate/go-test-edges.json."""
    edges_path = repo_root / "tools" / "gate" / "go-test-edges.json"
    if not edges_path.is_file():
        return "no-edges-file"

    ga = _try_import_go_affected(repo_root)
    if ga is not None:
        edges = ga.load_edges(repo_root)
        edge = edges.get(import_path)
        if edge is None:
            return "no-edge-entry"
        graph = ga.load_tillr_graph(repo_root)
        return ga.compute_edge_digest(
            repo_root,
            edge,
            graph,
            active_tests=active_tests,
        )

    # Fallback direct hashing if go_affected is unavailable
    edges = json.loads(edges_path.read_text(encoding="utf-8"))
    edge = edges.get(import_path)
    if not isinstance(edge, dict):
        return "no-edge-entry"
    h = hashlib.sha256()
    files: set[str] = set()
    read_tests = edge.get("read_tests", {})
    active_set = set(active_tests) if active_tests is not None else None
    for rule in edge.get("reads", []):
        if active_set is not None and rule in read_tests:
            if not (set(read_tests[rule]) & active_set):
                continue
        rp = repo_root / rule.rstrip("/")
        if rp.is_file():
            files.add(rp.relative_to(repo_root).as_posix())
        elif rp.is_dir():
            for dp, dns, fns in os.walk(rp):
                dns[:] = [d for d in dns if d not in (".git", "node_modules", "__pycache__")]
                for fn in fns:
                    files.add((Path(dp) / fn).relative_to(repo_root).as_posix())
        else:
            h.update(f"missing-read:{rule}\n".encode("utf-8"))
    for rel in sorted(files):
        h.update(rel.encode("utf-8") + b"\0")
        h.update((repo_root / rel).read_bytes())
        h.update(b"\0")
    return h.hexdigest()


def normalize_test_args(args: list[str]) -> list[str]:
    """Normalize test binary flags into a canonical sorted list."""
    return sorted(a.strip() for a in args if a.strip())


def compute_cache_key(
    *,
    binary_sha256: str,
    args: list[str],
    edge_digest: str,
    env: dict[str, str],
) -> str:
    payload = {
        "schema": 1,
        "binary_sha256": binary_sha256,
        "args": normalize_test_args(args),
        "edge_digest": edge_digest,
        "env": sorted((k, v) for k, v in env.items()),
    }
    encoded = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def collect_relevant_env(extra_env: dict[str, str] | None = None) -> dict[str, str]:
    env: dict[str, str] = {}
    for k in DEFAULT_ENV_KEYS:
        if k in os.environ:
            env[k] = os.environ[k]
    if extra_env:
        env.update(extra_env)
    return env


def run_package_cached(
    repo_root: Path,
    pkg_spec: str,
    cache_dir: Path,
    *,
    run_pattern: str = "",
    extra_args: list[str] | None = None,
    extra_env: dict[str, str] | None = None,
    scratch_dir: Path | None = None,
) -> dict[str, Any]:
    """Compile and run (or cache-skip) a single Go package test binary."""
    info = resolve_package(repo_root, pkg_spec)
    import_path = info["import_path"]
    pkg_dir = Path(info["pkg_dir"])
    module_dir = Path(info["module_dir"])

    args = list(extra_args or [])
    if run_pattern:
        args.append(f"-test.run={run_pattern}")

    active_tests = None
    if run_pattern.startswith("^(") and run_pattern.endswith(")$"):
        active_tests = run_pattern[2:-2].split("|")
    elif run_pattern.startswith("^") and run_pattern.endswith("$"):
        active_tests = [run_pattern[1:-1]]

    with tempfile.TemporaryDirectory(
        prefix="proto1-bin-",
        dir=str(scratch_dir) if scratch_dir else None,
    ) as tmp:
        tmp_path = Path(tmp)
        bin_path = tmp_path / "pkg.test"
        t_compile0 = time.monotonic()
        has_tests, _ = compile_test_binary(module_dir, import_path, bin_path)
        compile_ms = int((time.monotonic() - t_compile0) * 1000)

        if not has_tests:
            return {
                "import_path": import_path,
                "has_tests": False,
                "cache_hit": True,
                "exit_code": 0,
                "compile_ms": compile_ms,
                "run_ms": 0,
                "key": "",
                "binary_sha256": "",
                "edge_digest": "",
            }

        bin_sha = sha256_file(bin_path)
        edge_digest = compute_declared_edge_digest(
            repo_root, import_path, active_tests=active_tests
        )
        rel_env = collect_relevant_env(extra_env)
        key = compute_cache_key(
            binary_sha256=bin_sha,
            args=args,
            edge_digest=edge_digest,
            env=rel_env,
        )

        cache_dir.mkdir(parents=True, exist_ok=True)
        record_path = cache_dir / f"{key}.json"
        if record_path.is_file():
            rec = json.loads(record_path.read_text(encoding="utf-8"))
            if rec.get("exit_code") == 0 and rec.get("key") == key:
                return {
                    "import_path": import_path,
                    "has_tests": True,
                    "cache_hit": True,
                    "exit_code": 0,
                    "compile_ms": compile_ms,
                    "run_ms": 0,
                    "key": key,
                    "binary_sha256": bin_sha,
                    "edge_digest": edge_digest,
                }

        run_env = os.environ.copy()
        run_env.update(rel_env)
        run_tmp = tmp_path / "run-tmp"
        run_tmp.mkdir(parents=True, exist_ok=True)
        run_env["TMPDIR"] = str(run_tmp)

        t_run0 = time.monotonic()
        proc = subprocess.run(
            [str(bin_path), *normalize_test_args(args)],
            cwd=pkg_dir,
            env=run_env,
            capture_output=True,
            text=True,
        )
        run_ms = int((time.monotonic() - t_run0) * 1000)

        if proc.returncode == 0:
            record = {
                "schema": 1,
                "key": key,
                "import_path": import_path,
                "args": normalize_test_args(args),
                "binary_sha256": bin_sha,
                "edge_digest": edge_digest,
                "env": rel_env,
                "exit_code": 0,
                "run_ms": run_ms,
            }
            tmp_rec = cache_dir / f".tmp-{key}-{os.getpid()}.json"
            tmp_rec.write_text(json.dumps(record, indent=2), encoding="utf-8")
            tmp_rec.replace(record_path)

        return {
            "import_path": import_path,
            "has_tests": True,
            "cache_hit": False,
            "exit_code": proc.returncode,
            "compile_ms": compile_ms,
            "run_ms": run_ms,
            "key": key,
            "binary_sha256": bin_sha,
            "edge_digest": edge_digest,
            "output_tail": (proc.stdout + proc.stderr)[-1000:] if proc.returncode != 0 else "",
        }


def run_batch(
    repo_root: Path,
    packages: list[str],
    cache_dir: Path,
    *,
    run_pattern: str = "",
    stop_after: int | None = None,
    scratch_dir: Path | None = None,
) -> dict[str, Any]:
    t0 = time.monotonic()
    results: list[dict[str, Any]] = []
    stopped_early = False
    for idx, pkg in enumerate(packages):
        if stop_after is not None and idx >= stop_after:
            stopped_early = True
            break
        res = run_package_cached(
            repo_root,
            pkg,
            cache_dir,
            run_pattern=run_pattern,
            scratch_dir=scratch_dir,
        )
        results.append(res)
        if res["exit_code"] != 0:
            break
    wall_ms = int((time.monotonic() - t0) * 1000)
    hits = sum(1 for r in results if r["cache_hit"])
    misses = sum(1 for r in results if not r["cache_hit"])
    return {
        "repo_root": str(repo_root),
        "cache_dir": str(cache_dir),
        "requested": len(packages),
        "processed": len(results),
        "skipped_due_to_stop": len(packages) - len(results) if stopped_early else 0,
        "stopped_early": stopped_early,
        "hits": hits,
        "misses": misses,
        "wall_ms": wall_ms,
        "results": results,
    }


def verify_wrong_skip(repo_root: Path, scratch_dir: Path) -> dict[str, Any]:
    """Verify in a scratch Go module that mutating a declared input or arg forces a rerun."""
    with tempfile.TemporaryDirectory(prefix="proto1-wrongskip-", dir=str(scratch_dir)) as td:
        ws = Path(td)
        mod = ws / "tools" / "sample"
        mod.mkdir(parents=True)
        (mod / "go.mod").write_text("module example.com/sample\n\ngo 1.21\n", encoding="utf-8")
        (mod / "sample_test.go").write_text(
            'package sample\n\nimport (\n\t"os"\n\t"testing"\n)\n\n'
            'func TestDeclaredRead(t *testing.T) {\n'
            '\tb, err := os.ReadFile("../../data/input.txt")\n'
            '\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n'
            '\tif string(b) != "ok" {\n\t\tt.Fatalf("got %q", string(b))\n\t}\n'
            '}\n',
            encoding="utf-8",
        )
        data_dir = ws / "data"
        data_dir.mkdir(parents=True)
        input_file = data_dir / "input.txt"
        input_file.write_text("ok", encoding="utf-8")

        gate_dir = ws / "tools" / "gate"
        gate_dir.mkdir(parents=True)
        (gate_dir / "go-test-edges.json").write_text(
            json.dumps({"example.com/sample": {"builds": [], "reads": ["data/input.txt"]}}),
            encoding="utf-8",
        )
        cache_dir = ws / "cache"

        r1 = run_package_cached(ws, "tools/sample", cache_dir, scratch_dir=ws)
        r2 = run_package_cached(ws, "tools/sample", cache_dir, scratch_dir=ws)
        # Mutate declared external input -> key must change and test must rerun and fail
        input_file.write_text("changed", encoding="utf-8")
        r3 = run_package_cached(ws, "tools/sample", cache_dir, scratch_dir=ws)
        return {
            "cold_hit": r1["cache_hit"],
            "repeat_hit": r2["cache_hit"],
            "mutated_hit": r3["cache_hit"],
            "mutated_exit_code": r3["exit_code"],
            "key_changed": r1["key"] != r3["key"],
        }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", default=".", help="Repository root")
    parser.add_argument("--cache-dir", required=True, help="Content cache directory")
    parser.add_argument("--pkg", action="append", default=[], help="Package to run (repeatable)")
    parser.add_argument("--run", default="", help="Optional -test.run pattern")
    parser.add_argument("--stop-after", type=int, default=None, help="Stop after N packages")
    parser.add_argument("--scratch-dir", default=None, help="Scratch directory for temp files")
    parser.add_argument("--test-wrong-skip", action="store_true", help="Run wrong-skip check")
    parser.add_argument("--json", action="store_true", help="Output JSON")
    args = parser.parse_args(argv)

    scratch = Path(args.scratch_dir) if args.scratch_dir else None
    if scratch:
        scratch.mkdir(parents=True, exist_ok=True)

    if args.test_wrong_skip:
        out = verify_wrong_skip(Path(args.repo_root).resolve(), scratch or Path(tempfile.gettempdir()))
        print(json.dumps(out, indent=2))
        return 0 if (not out["cold_hit"] and out["repeat_hit"] and not out["mutated_hit"] and out["key_changed"] and out["mutated_exit_code"] != 0) else 1

    if not args.pkg:
        parser.error("at least one --pkg is required unless --test-wrong-skip is set")

    summary = run_batch(
        Path(args.repo_root).resolve(),
        args.pkg,
        Path(args.cache_dir).resolve(),
        run_pattern=args.run,
        stop_after=args.stop_after,
        scratch_dir=scratch,
    )
    if args.json:
        print(json.dumps(summary, indent=2))
    else:
        print(
            f"proto_content_cache: processed {summary['processed']}/{summary['requested']} "
            f"(hits={summary['hits']}, misses={summary['misses']}, "
            f"stopped_early={summary['stopped_early']}, wall_ms={summary['wall_ms']})"
        )
        for r in summary["results"]:
            status = "HIT" if r["cache_hit"] else f"MISS(exit={r['exit_code']})"
            print(
                f"  {r['import_path']}: {status} compile={r['compile_ms']}ms run={r['run_ms']}ms key={r['key'][:12]}"
            )
    any_failed = any(r["exit_code"] != 0 for r in summary["results"])
    return 1 if any_failed else 0


if __name__ == "__main__":
    sys.exit(main())
