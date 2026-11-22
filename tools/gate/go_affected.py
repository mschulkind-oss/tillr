#!/usr/bin/env python3
"""Select and run affected tillr Go test packages with Go's result cache on."""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import contextlib
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

EXCLUDED_PACK_MIRROR_PREFIX = "yolo-packs/tillr/files/local/share/tillr/"
SHARD_THRESHOLD = 100
MIN_SHARDS = 6
MAX_SHARDS = 12
TOP_LEVEL_TEST_RE = re.compile(
    r"^func\s+(Test(?:[^a-z]\w*)?)\s*\(\s*\w+\s+\*testing\.T\b",
    re.MULTILINE,
)

# Outer gate, drain, and git-override variables that child `go list` / `go test`
# processes must never inherit: inner fixtures spawn nested gates and git repos,
# and per-run IDs would pollute Go's testlog environment cache key if read.
STRIPPED_OUTER_ENV = (
    "LAND_BATCH_LIMIT",
    "TILLR_FORK_DRIFT",
    "PI_FORKS_ROOT",
    "PI_FORKS_SERIES_ROOT",
    "TILLR_LOOP_REGISTRY",
    "TILLR_LOOP_ADAPTER_BUILD",
    "TILLR_ORDINARY_GATE_TIMING",
    "TILLR_FAST_TREE_TIMING",
    "TILLR_GATE_SPANS_OUT",
    "TILLR_FAST_VERDICT_OUT",
    "TILLR_GO_AFFECTED_EVIDENCE_OUT",
    "TILLR_GO_SHARDS_EVIDENCE_OUT",
    "TILLR_GO_BUILD_EVIDENCE_OUT",
    "TILLR_SQLITE_TRACE",
    "GO_TEST_SHARDS_HOLDER",
    "GO_TEST_SHARDS_CLASS",
    "TILLR_RUN_ID",
    "TILLR_LOOP_STORE",
    "TILLR_ITEM_ID",
    "TILLR_LANE_ID",
    "TILLR_NODE_ID",
    "TILLR_PROGRESS_FILE",
    "TILLR_GATE_EDGE_INPUTS",
    "GIT_DIR",
    "GIT_WORK_TREE",
    "GIT_INDEX_FILE",
    "GIT_COMMON_DIR",
    "GIT_OBJECT_DIRECTORY",
    "GIT_ALTERNATE_OBJECT_DIRECTORIES",
)


def discover_tillr_modules(root: Path) -> list[str]:
    """Return tools/tillr and every tools/tillr-* directory holding a go.mod."""
    modules: list[str] = []
    if (root / "tools" / "tillr" / "go.mod").is_file():
        modules.append("tools/tillr")
    tools_dir = root / "tools"
    if tools_dir.is_dir():
        for go_mod in sorted(tools_dir.glob("tillr-*/go.mod")):
            modules.append(f"tools/{go_mod.parent.name}")
    return modules


def load_edges(root: Path) -> dict[str, dict]:
    path = root / "tools" / "gate" / "go-test-edges.json"
    if not path.is_file():
        return {}
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"{path}: expected JSON object")
    for pkg, entry in data.items():
        if not isinstance(pkg, str) or not isinstance(entry, dict):
            raise ValueError(f"{path}: invalid entry for {pkg!r}")
        for key in ("builds", "reads"):
            val = entry.get(key, [])
            if not isinstance(val, list) or not all(isinstance(x, str) for x in val):
                raise ValueError(f"{path}: {pkg}.{key} must be a list of strings")
    return data


def build_env() -> dict[str, str]:
    env = dict(os.environ, CGO_ENABLED="0")
    for key in STRIPPED_OUTER_ENV:
        env.pop(key, None)
    existing = env.get("GOFLAGS", "").split()
    if "-trimpath" not in existing:
        existing.append("-trimpath")
    env["GOFLAGS"] = " ".join(existing)
    return env


def module_replace_dependents(root: Path, modules: list[str]) -> dict[str, set[str]]:
    """Map each module -> set of modules that replace it (directly or transitively)."""
    direct: dict[str, set[str]] = {m: set() for m in modules}
    mod_set = set(modules)
    for mod in modules:
        gomod = root / mod / "go.mod"
        try:
            text = gomod.read_text(encoding="utf-8")
        except OSError:
            continue
        for line in text.splitlines():
            line = line.split("//", 1)[0].strip()
            if line.startswith("replace "):
                line = line[len("replace "):]
            if "=>" not in line:
                continue
            target = line.split("=>", 1)[1].split()
            if not target or not target[0].startswith(("./", "../")):
                continue
            rel = os.path.normpath(os.path.join(mod, target[0]))
            if rel in mod_set and rel != mod:
                direct[rel].add(mod)
    closure: dict[str, set[str]] = {}
    for target in modules:
        seen: set[str] = set()
        stack = list(direct.get(target, ()))
        while stack:
            cur = stack.pop()
            if cur in seen:
                continue
            seen.add(cur)
            stack.extend(direct.get(cur, ()))
        closure[target] = seen
    return closure


def parse_json_stream(text: str) -> list[dict]:
    dec = json.JSONDecoder()
    objs: list[dict] = []
    pos = 0
    n = len(text)
    while pos < n:
        while pos < n and text[pos].isspace():
            pos += 1
        if pos >= n:
            break
        obj, pos = dec.raw_decode(text, pos)
        objs.append(obj)
    return objs


def list_module_packages(root: Path, module: str, env: dict[str, str]) -> tuple[bool, list[dict]]:
    res = subprocess.run(
        ["go", "list", "-deps", "-test", "-json", "./..."],
        cwd=root / module,
        env=env,
        text=True,
        capture_output=True,
    )
    if res.returncode != 0:
        return False, []
    try:
        return True, parse_json_stream(res.stdout)
    except ValueError:
        return False, []


def path_matches_read(path: str, rule: str) -> bool:
    if path.startswith(EXCLUDED_PACK_MIRROR_PREFIX) and not rule.startswith(EXCLUDED_PACK_MIRROR_PREFIX):
        return False
    if rule.endswith("/"):
        return path.startswith(rule)
    return path == rule or path.startswith(rule + "/")


def compute_edge_digest(
    root: Path,
    edge: dict,
    pkg_info: dict[str, dict],
) -> str:
    hasher = hashlib.sha256()
    files: set[str] = set()
    mods: set[str] = set()
    for target in edge.get("builds", []):
        info = pkg_info.get(target)
        if not info:
            hasher.update(f"missing-build:{target}\n".encode("utf-8"))
            continue
        closure = {target} | info["deps"]
        for dep in closure:
            dinfo = pkg_info.get(dep)
            if not dinfo:
                continue
            mods.add(dinfo["module"])
            dpath = root / dinfo["dir_rel"]
            if dpath.is_dir():
                for child in dpath.iterdir():
                    if child.is_file():
                        files.add(child.relative_to(root).as_posix())
    for mod in mods:
        for mf in ("go.mod", "go.sum"):
            mp = root / mod / mf
            if mp.is_file():
                files.add(f"{mod}/{mf}")
    for rule in edge.get("reads", []):
        rp = root / rule.rstrip("/")
        if rp.is_file():
            files.add(rp.relative_to(root).as_posix())
        elif rp.is_dir():
            for dirpath, dirnames, filenames in os.walk(rp):
                dirnames[:] = [d for d in dirnames if d not in (".git", "node_modules", "__pycache__")]
                for fn in filenames:
                    fp = Path(dirpath) / fn
                    rel = fp.relative_to(root).as_posix()
                    if rel.startswith(EXCLUDED_PACK_MIRROR_PREFIX) and not rule.startswith(EXCLUDED_PACK_MIRROR_PREFIX):
                        continue
                    files.add(rel)
        else:
            hasher.update(f"missing-read:{rule}\n".encode("utf-8"))
    for rel in sorted(files):
        hasher.update(rel.encode("utf-8") + b"\0")
        try:
            hasher.update((root / rel).read_bytes())
        except OSError:
            hasher.update(b"<unreadable>")
        hasher.update(b"\0")
    return hasher.hexdigest()


def load_tillr_graph(root: Path, modules: list[str] | None = None) -> dict:
    root = root.resolve()
    modules = discover_tillr_modules(root) if modules is None else list(modules)
    env = build_env()
    with ThreadPoolExecutor(max_workers=max(1, len(modules))) as pool:
        listed = list(pool.map(lambda m: (m, *list_module_packages(root, m, env)), modules))

    failed_modules: set[str] = set()
    pkg_info: dict[str, dict] = {}
    test_deps: dict[str, set[str]] = {}
    dir_to_pkg: dict[str, str] = {}
    module_test_pkgs: dict[str, list[str]] = {m: [] for m in modules}

    for mod, ok, objs in listed:
        if not ok:
            failed_modules.add(mod)
            continue
        mod_root = (root / mod).resolve()
        for obj in objs:
            if obj.get("Standard"):
                continue
            d = obj.get("Dir")
            if not d:
                continue
            d_path = Path(d).resolve()
            if not d_path.is_relative_to(root):
                continue
            dir_rel = d_path.relative_to(root).as_posix()
            ip = obj.get("ImportPath", "")
            for_test = obj.get("ForTest")
            deps = set(obj.get("Deps", []))
            if for_test:
                test_deps.setdefault(for_test, set()).update(deps)
                continue
            if ip.endswith(".test") and obj.get("Name") == "main":
                continue
            owner_mod = None
            if d_path.is_relative_to(mod_root):
                owner_mod = mod
            else:
                for cand in modules:
                    if dir_rel == cand or dir_rel.startswith(cand + "/"):
                        owner_mod = cand
                        break
            if not owner_mod:
                continue
            test_files = sorted(set((obj.get("TestGoFiles") or []) + (obj.get("XTestGoFiles") or [])))
            has_tests = bool(test_files)
            if ip not in pkg_info:
                pkg_info[ip] = {
                    "import_path": ip,
                    "module": owner_mod,
                    "dir_rel": dir_rel,
                    "has_tests": has_tests,
                    "test_files": test_files,
                    "deps": deps,
                    "name": obj.get("Name", ""),
                }
                dir_to_pkg[dir_rel] = ip
                test_deps.setdefault(ip, set()).update(deps)
                if has_tests and ip not in module_test_pkgs[owner_mod]:
                    module_test_pkgs[owner_mod].append(ip)

    return {
        "modules": modules,
        "failed_modules": failed_modules,
        "pkg_info": pkg_info,
        "test_deps": test_deps,
        "dir_to_pkg": dir_to_pkg,
        "module_test_pkgs": module_test_pkgs,
    }


def select_affected_packages(
    root: Path,
    changed: list[str],
    *,
    modules: list[str] | None = None,
    edges: dict[str, dict] | None = None,
    drop_modules: set[str] | None = None,
    graph: dict | None = None,
) -> dict:
    started = time.monotonic()
    root = root.resolve()
    modules = (
        list(graph["modules"])
        if graph is not None and modules is None
        else (discover_tillr_modules(root) if modules is None else list(modules))
    )
    edges = load_edges(root) if edges is None else edges
    drop_set = set(drop_modules or ())
    norm_changed = [os.path.normpath(p).lstrip("./") for p in changed if p and p.strip()]

    all_reads = [r for e in edges.values() for r in e.get("reads", [])]
    relevant = [
        p for p in norm_changed
        if any(p == m or p.startswith(m + "/") for m in modules)
        or any(path_matches_read(p, r) for r in all_reads)
    ]
    if not relevant:
        return {
            "selection_ms": int(round((time.monotonic() - started) * 1000)),
            "modules": modules,
            "selected": [],
            "floor_modules": [],
        }

    if graph is None:
        graph = load_tillr_graph(root, modules)
    failed_modules = graph["failed_modules"]
    pkg_info = graph["pkg_info"]
    test_deps = graph["test_deps"]
    dir_to_pkg = graph["dir_to_pkg"]
    module_test_pkgs = graph["module_test_pkgs"]

    reasons: dict[str, str] = {}
    priority = {"changed": 4, "import": 3, "edge": 2, "floor": 1}

    def mark(pkg: str, reason: str) -> None:
        prev = reasons.get(pkg)
        if prev is None or priority[reason] > priority[prev]:
            reasons[pkg] = reason

    changed_pkgs: set[str] = set()
    floor_triggers: set[str] = set()

    # Step 1: Changed paths -> packages or floor triggers.
    for path in relevant:
        owner_mod = None
        for m in modules:
            if path == m or path.startswith(m + "/"):
                owner_mod = m
                break
        if owner_mod is None:
            continue
        if owner_mod in failed_modules:
            floor_triggers.add(owner_mod)
            continue
        if path in (f"{owner_mod}/go.mod", f"{owner_mod}/go.sum"):
            floor_triggers.add(owner_mod)
            continue
        imm_dir = posix_dirname(path)
        if not (root / imm_dir).is_dir():
            # Deleted directory/package or deleted file in a removed directory.
            floor_triggers.add(owner_mod)
            continue
        cur = imm_dir
        placed = None
        while True:
            if cur in dir_to_pkg:
                placed = dir_to_pkg[cur]
                break
            if cur == owner_mod or "/" not in cur:
                break
            cur = posix_dirname(cur)
        if placed is None:
            floor_triggers.add(owner_mod)
        else:
            changed_pkgs.add(placed)
            if pkg_info[placed]["has_tests"]:
                mark(placed, "changed")

    # Step 2: Importers (reverse import graph across all tillr modules).
    if changed_pkgs:
        for ip, info in pkg_info.items():
            if not info["has_tests"]:
                continue
            if test_deps.get(ip, set()) & changed_pkgs:
                mark(ip, "import")

    # Step 3: Declared edges (builds + reads).
    for test_pkg, edge in edges.items():
        info = pkg_info.get(test_pkg)
        if not info or not info["has_tests"]:
            continue
        hit = False
        for target in edge.get("builds", []):
            if target in changed_pkgs:
                hit = True
                break
            tinfo = pkg_info.get(target)
            if tinfo and (tinfo["deps"] & changed_pkgs):
                hit = True
                break
        if not hit:
            for rule in edge.get("reads", []):
                if any(path_matches_read(p, rule) for p in norm_changed):
                    hit = True
                    break
        if hit:
            mark(test_pkg, "edge")

    # Step 4: Floor (unplaceable changes -> module + modules replacing it).
    floor_modules: list[str] = []
    failed_floor_mods: list[str] = []
    if floor_triggers:
        dependents = module_replace_dependents(root, modules)
        expanded = set(floor_triggers)
        for trig in floor_triggers:
            expanded.update(dependents.get(trig, ()))
        floor_modules = sorted(expanded)
        for mod in floor_modules:
            if mod in failed_modules:
                if mod not in drop_set:
                    failed_floor_mods.append(mod)
                continue
            for ip in module_test_pkgs.get(mod, []):
                mark(ip, "floor")

    selected: list[dict] = []
    for ip in sorted(reasons):
        info = pkg_info[ip]
        if info["module"] in drop_set:
            continue
        has_edge = ip in edges
        digest = compute_edge_digest(root, edges[ip], pkg_info) if has_edge else None
        selected.append({
            "import_path": ip,
            "module": info["module"],
            "dir_rel": info["dir_rel"],
            "test_files": info["test_files"],
            "reason": reasons[ip],
            "has_edge": has_edge,
            "edge_digest": digest,
        })
    for mod in failed_floor_mods:
        selected.append({
            "import_path": "./...",
            "module": mod,
            "dir_rel": mod,
            "test_files": [],
            "reason": "floor",
            "has_edge": False,
            "edge_digest": None,
        })

    return {
        "selection_ms": int(round((time.monotonic() - started) * 1000)),
        "modules": modules,
        "selected": selected,
        "floor_modules": floor_modules,
    }


def posix_dirname(path: str) -> str:
    idx = path.rfind("/")
    return path[:idx] if idx >= 0 else ""


def _load_shards_module(root: Path):
    shards_path = root / "tools" / "go-test-shards.py"
    if not shards_path.is_file():
        return None
    spec = importlib.util.spec_from_file_location("go_test_shards", shards_path)
    if spec is None or spec.loader is None:
        return None
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def _load_durations(root: Path) -> dict[str, dict[str, float]]:
    path = root / "tools" / "go-test-durations.json"
    if not path.is_file():
        return {}
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
        return raw if isinstance(raw, dict) else {}
    except (OSError, ValueError):
        return {}


def scan_package_tests(root: Path, item: dict) -> list[str]:
    """Scan top-level Test* functions from Go test source files without invoking `go test -list`."""
    if item["import_path"] == "./...":
        return []
    dir_rel = item.get("dir_rel")
    if not dir_rel:
        prefix = "github.com/mschulkind-oss/"
        ip = item["import_path"]
        dir_rel = f"tools/{ip[len(prefix):]}" if ip.startswith(prefix) else item["module"]
    pkg_dir = root / dir_rel
    if not pkg_dir.is_dir():
        return []
    test_files = item.get("test_files")
    if not test_files:
        test_files = [p.name for p in sorted(pkg_dir.glob("*_test.go"))]
    seen: list[str] = []
    seen_set: set[str] = set()
    for fname in test_files:
        fpath = pkg_dir / fname
        try:
            source = fpath.read_text(encoding="utf-8")
        except OSError:
            continue
        for name in TOP_LEVEL_TEST_RE.findall(source):
            if name not in seen_set:
                seen_set.add(name)
                seen.append(name)
    return seen


def parse_go_test_json(stdout: str) -> tuple[dict[str, dict], str]:
    """Return (per-package {elapsed_ms, cached, passed}, plain `go test` failure text)."""
    results: dict[str, dict] = {}
    text_chunks: list[str] = []
    test_headers: dict[tuple[str, str], str] = {}
    test_bodies: dict[tuple[str, str], list[str]] = {}
    for line in stdout.splitlines(keepends=True):
        stripped = line.lstrip()
        if not stripped.startswith("{"):
            text_chunks.append(line)
            continue
        try:
            ev = json.loads(stripped)
        except ValueError:
            text_chunks.append(line)
            continue
        if not isinstance(ev, dict):
            text_chunks.append(line)
            continue
        out_chunk = ev.get("Output")
        pkg = ev.get("Package")
        test_name = ev.get("Test")
        action = ev.get("Action")
        if not isinstance(pkg, str) or not pkg:
            if isinstance(out_chunk, str):
                text_chunks.append(out_chunk)
            continue
        rec = results.setdefault(pkg, {"elapsed_ms": 0, "cached": False, "passed": None})
        if isinstance(test_name, str) and test_name:
            key = (pkg, test_name)
            if action == "output" and isinstance(out_chunk, str):
                s = out_chunk.lstrip()
                if s.startswith("--- FAIL:"):
                    test_headers[key] = out_chunk
                elif not s.startswith(("=== RUN", "=== PAUSE", "=== CONT", "--- PASS:", "--- SKIP:")):
                    test_bodies.setdefault(key, []).append(out_chunk)
            elif action == "fail":
                elapsed = ev.get("Elapsed")
                dur = f"{elapsed:.2f}s" if isinstance(elapsed, (int, float)) else "0.00s"
                header = test_headers.pop(key, f"--- FAIL: {test_name} ({dur})\n")
                text_chunks.append(header)
                text_chunks.extend(test_bodies.pop(key, ()))
            elif action in ("pass", "skip"):
                test_headers.pop(key, None)
                test_bodies.pop(key, None)
        else:
            if action == "output" and isinstance(out_chunk, str):
                if "(cached)" in out_chunk:
                    rec["cached"] = True
                text_chunks.append(out_chunk)
            elif action in ("pass", "fail", "skip"):
                elapsed = ev.get("Elapsed")
                if isinstance(elapsed, (int, float)):
                    rec["elapsed_ms"] = int(round(elapsed * 1000))
                rec["passed"] = action != "fail"
    return results, "".join(text_chunks)


def write_evidence_atomic(ev_path: str, evidence: dict) -> None:
    if not ev_path:
        return
    try:
        dest = Path(ev_path)
        tmp = dest.with_name(f".{dest.name}.tmp.{os.getpid()}")
        tmp.write_text(json.dumps(evidence) + "\n", encoding="utf-8")
        tmp.replace(dest)
    except OSError as error:
        print(f"go-affected: could not write evidence: {error}", file=sys.stderr)


def run_selected_tests(root: Path, selection: dict, *, ev_path: str = "") -> tuple[int, str, dict]:
    selected = selection["selected"]
    initial_evidence = {
        "mode": "go-affected",
        "selection_ms": selection["selection_ms"],
        "slot_wait_ms": 0,
        "packages": [
            {
                "import_path": item["import_path"],
                "module": item["module"],
                "reason": item["reason"],
                "elapsed_ms": 0,
                "cached": False,
            }
            for item in selected
        ],
    }
    write_evidence_atomic(ev_path, initial_evidence)
    if not selected:
        return 0, "go-affected: no affected tillr Go test packages\n", initial_evidence

    shards_mod = _load_shards_module(root)
    durations = _load_durations(root)
    base_env = build_env()
    holder = (
        shards_mod.resolve_holder_run(None, shards_mod.SLOT_CLASS_PROOF)
        if shards_mod is not None
        else None
    )

    def invocation_scratch_dir(scratch_key: str) -> str | None:
        durable = base_env.get("YOLO_DURABLE_DIR") or base_env.get("TMPDIR")
        if not durable:
            return None
        slug = hashlib.sha256(scratch_key.encode("utf-8")).hexdigest()[:12]
        target = Path(durable) / ".go-affected-scratch" / slug
        target.mkdir(parents=True, exist_ok=True)
        return str(target)

    # Group small non-edge packages by module; edge or large packages run with their own invocations.
    # Each invocation is (module, items, digest, run_regex_or_None, scratch_key).
    invocations: list[tuple[str, list[dict], str | None, str | None, str]] = []
    by_mod: dict[str, list[dict]] = {}
    for item in selected:
        tests = scan_package_tests(root, item)
        if len(tests) >= SHARD_THRESHOLD:
            has_edge_test = "TestEdgeInputs" in tests
            non_edge = [t for t in tests if t != "TestEdgeInputs"]
            shard_count = min(MAX_SHARDS, max(MIN_SHARDS, len(non_edge) // 125))
            pkg_durations = durations.get(item["import_path"], {})
            if shards_mod is not None and hasattr(shards_mod, "assign"):
                buckets = shards_mod.assign(non_edge, pkg_durations, shard_count)
            else:
                buckets = [[] for _ in range(shard_count)]
                for idx, name in enumerate(non_edge):
                    buckets[idx % shard_count].append(name)
            for shard_idx, bucket in enumerate(buckets):
                if not bucket:
                    continue
                shard_names = list(bucket)
                if has_edge_test:
                    shard_names.append("TestEdgeInputs")
                pattern = "^(" + "|".join(re.escape(t) for t in shard_names) + ")$"
                invocations.append((
                    item["module"],
                    [item],
                    item["edge_digest"] if item["has_edge"] else None,
                    pattern,
                    f"{item['import_path']}#shard-{shard_idx}",
                ))
        elif item["has_edge"]:
            invocations.append((item["module"], [item], item["edge_digest"], None, item["import_path"]))
        else:
            by_mod.setdefault(item["module"], []).append(item)
    for mod in sorted(by_mod):
        invocations.append((mod, by_mod[mod], None, None, f"module:{mod}"))

    outputs: list[str] = []
    pkg_shards: dict[str, list[dict]] = {}
    max_slot_wait_ms = 0
    rc_all = 0

    def run_one(inv: tuple[str, list[dict], str | None, str | None, str]) -> tuple[int, str, int, dict[str, dict]]:
        mod, items, digest, run_pattern, scratch_key = inv
        env = dict(base_env)
        scratch_dir = invocation_scratch_dir(scratch_key)
        if scratch_dir is not None:
            env["YOLO_DURABLE_DIR"] = scratch_dir
        if digest is not None:
            env["TILLR_GATE_EDGE_INPUTS"] = digest
        else:
            env.pop("TILLR_GATE_EDGE_INPUTS", None)
        cmd = ["go", "test", "-json", "-timeout", "0"]
        if run_pattern is not None:
            cmd.extend(["-run", run_pattern])
        cmd.extend(item["import_path"] for item in items)

        wait_ms = 0
        if shards_mod is not None:
            wait_start = time.monotonic()
            with shards_mod.acquire_shard_slot(
                shards_mod.default_lock_dir(),
                shards_mod.default_shard_limit(),
                slot_class=shards_mod.SLOT_CLASS_PROOF,
                holder=holder,
                label=f"go-affected:{mod}",
            ):
                wait_ms = int(round((time.monotonic() - wait_start) * 1000))
                res = subprocess.run(
                    cmd,
                    cwd=root / mod,
                    env=env,
                    text=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                )
        else:
            res = subprocess.run(
                cmd,
                cwd=root / mod,
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
            )
        parsed, readable = parse_go_test_json(res.stdout)
        return res.returncode, readable, wait_ms, parsed

    with ThreadPoolExecutor(max_workers=min(16, len(invocations))) as pool:
        for rc, readable_out, wait_ms, parsed in pool.map(run_one, invocations):
            if wait_ms > max_slot_wait_ms:
                max_slot_wait_ms = wait_ms
            for pkg_name, rec in parsed.items():
                pkg_shards.setdefault(pkg_name, []).append(rec)
            if rc != 0:
                rc_all = rc
                outputs.append(readable_out)

    packages_evidence: list[dict] = []
    for item in selected:
        ip = item["import_path"]
        shards_list = pkg_shards.get(ip, [])
        packages_evidence.append({
            "import_path": ip,
            "module": item["module"],
            "reason": item["reason"],
            "elapsed_ms": max((s.get("elapsed_ms", 0) for s in shards_list), default=0),
            "cached": bool(shards_list) and all(bool(s.get("cached", False)) for s in shards_list),
        })

    evidence = {
        "mode": "go-affected",
        "selection_ms": selection["selection_ms"],
        "slot_wait_ms": max_slot_wait_ms,
        "packages": packages_evidence,
    }
    write_evidence_atomic(ev_path, evidence)
    summary = (
        f"go-affected: {len(selected)} package(s) "
        f"({sum(1 for p in packages_evidence if p['cached'])} cached, "
        f"selection {selection['selection_ms']}ms, slot_wait {max_slot_wait_ms}ms)\n"
    )
    return rc_all, summary + "".join(outputs), evidence


def git_changed_paths(root: Path, base: str, head: str) -> list[str]:
    out = subprocess.check_output(
        ["git", "-C", str(root), "diff", "--name-only", "--no-renames", "-z", base, head, "--"],
    )
    return [os.fsdecode(p) for p in out.split(b"\0") if p]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".", help="Repository root")
    parser.add_argument("--base", help="Base commit")
    parser.add_argument("--head", default="HEAD", help="Head commit")
    parser.add_argument("--changed", nargs="*", help="Explicit changed paths (relative to root)")
    parser.add_argument("--drop-module", action="append", default=[], help="Module to drop from selection")
    parser.add_argument("--select-only", action="store_true", help="Print selection JSON and exit 0")
    parser.add_argument("--evidence-out", help="Write evidence JSON to this path")
    args = parser.parse_args(argv)

    root = Path(args.root).resolve()
    if args.changed is not None:
        changed = args.changed
    elif args.base:
        changed = git_changed_paths(root, args.base, args.head)
    else:
        parser.error("either --base or --changed is required")

    selection = select_affected_packages(root, changed, drop_modules=set(args.drop_module))
    if args.select_only:
        print(json.dumps(selection, indent=2, sort_keys=True))
        return 0

    ev_path = args.evidence_out or os.environ.get("TILLR_GO_AFFECTED_EVIDENCE_OUT", "")
    rc, output, _evidence = run_selected_tests(root, selection, ev_path=ev_path)
    sys.stdout.write(output)
    return rc


if __name__ == "__main__":
    sys.exit(main())
