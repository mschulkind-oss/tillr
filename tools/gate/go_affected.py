#!/usr/bin/env python3
"""Select and run affected tillr Go test packages with Go's result cache on."""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import contextlib
import ctypes
import errno
import fcntl
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time

EXCLUDED_PACK_MIRROR_PREFIX = "yolo-packs/tillr/files/local/share/tillr/"
# Packages whose subprocess-heavy integration suites are proved after the merge
# (go-test-tillr-drain) rather than in pre-merge go-affected (D-GR53, D-GR54).
DEFERRED_PROOF_PACKAGES: frozenset[str] = frozenset({
    "github.com/mschulkind-oss/tillr-drain/drain",
})
SHARD_THRESHOLD = 100
MIN_SHARDS = 6
MAX_SHARDS = 2 * _cpu_count if (_cpu_count := os.cpu_count()) and _cpu_count > 0 else 12
NARROWED_SHARD_DURATION_THRESHOLD = 25.0
INVOCATION_GUARD_FLAG = "--invocation-guard"
PR_SET_CHILD_SUBREAPER = 36
_live_guards: dict[int, int] = {}
_live_guards_lock = threading.Lock()
TOP_LEVEL_TEST_RE = re.compile(
    r"^func\s+((?:Test(?:[^a-z\W]\w*)?(?=\s*\(\s*\w+\s+\*testing\.T\b)|Fuzz(?:[^a-z\W]\w*)?(?=\s*\(\s*\w+\s+\*testing\.F\b)|Example(?:[^a-z\W]\w*)?(?=\s*\(\s*\))))",
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
    "TILLR_GO_VERDICTS_IN",
    "TILLR_GO_VERDICTS_OUT",
    "TILLR_SELF_SUITE_TIMING_OUT",
    "TILLR_SQLITE_TRACE",
    "GO_TEST_SHARDS_HOLDER",
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
        for map_key, list_key in (("build_tests", "builds"), ("read_tests", "reads")):
            if map_key not in entry:
                continue
            scoped_tests = entry[map_key]
            if not isinstance(scoped_tests, dict):
                raise ValueError(f"{path}: {pkg}.{map_key} must be an object")
            allowed_keys = set(entry.get(list_key, []))
            for rule, names in scoped_tests.items():
                if not isinstance(rule, str) or rule not in allowed_keys:
                    raise ValueError(
                        f"{path}: {pkg}.{map_key} key {rule!r} must appear in {pkg}.{list_key}"
                    )
                if (
                    not isinstance(names, list)
                    or not names
                    or not all(isinstance(t, str) and bool(t.strip()) and t == t.strip() for t in names)
                ):
                    raise ValueError(
                        f"{path}: {pkg}.{map_key}[{rule!r}] must be a non-empty list of test names"
                    )
    return data


SOURCE_SCANNING_EDGES: tuple[tuple[str, str, bool], ...] = (
    (
        "github.com/mschulkind-oss/tillr/definition",
        "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers",
        True,
    ),
    (
        "github.com/mschulkind-oss/tillr/store",
        "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit",
        False,
    ),
)


def missing_source_scanning_edges(root: Path) -> list[str]:
    """Return refusal messages for any tillr Go module absent from source-scanning edges."""
    edges = load_edges(root)
    modules = discover_tillr_modules(root)
    findings: list[str] = []
    for pkg, test_name, include_root_module in SOURCE_SCANNING_EDGES:
        entry = edges.get(pkg)
        if entry is None:
            findings.append(f"{pkg}: missing entry in tools/gate/go-test-edges.json")
            continue
        reads = set(entry.get("reads", []))
        read_tests = entry.get("read_tests", {})
        for mod in modules:
            if not include_root_module and mod == "tools/tillr":
                continue
            rule = f"{mod}/"
            if rule not in reads:
                findings.append(
                    f"{pkg}: missing {rule!r} in tools/gate/go-test-edges.json reads"
                )
            elif test_name not in read_tests.get(rule, []):
                findings.append(
                    f"{pkg}: missing {test_name!r} in tools/gate/go-test-edges.json read_tests[{rule!r}]"
                )
    return findings


_GO_WALK_RE = re.compile(r"\b(?:filepath\.Walk(?:Dir)?|fs\.WalkDir)\s*\(")
_GO_EXT_RE = re.compile(r'(?:HasSuffix|Ext)\([^)]*"\.go"|==\s*"\.go"')
_GO_PARSE_RE = re.compile(r"\b\w+\.Parse(?:File|Dir)\s*\(")
_REPO_REL_MOD_RE = re.compile(r'"tools/(tillr(?:-[a-z0-9-]+)?)(?:/([^"]*))?"')
_PARENT_REL_MOD_RE = re.compile(r'"(?:\.\./)+(tillr(?:-[a-z0-9-]+)?)(?:/([^"]*))?"')
_FILEPATH_JOIN_RE = re.compile(r"filepath\.Join\(([^)]*)\)")
_MOD_GLOB_RE = re.compile(r'"(?:tools/)?(tillr-\*|tillr\*)"')
_STR_LIT_RE = re.compile(r'"([^"]+)"')
_TILLR_IMPORT_RE = re.compile(r'"(github\.com/mschulkind-oss/[^"]+)"')


def _package_tillr_imports(pkg_dir: Path) -> set[str]:
    imports: set[str] = set()
    for go_file in pkg_dir.glob("*.go"):
        try:
            text = go_file.read_text(encoding="utf-8")
        except OSError:
            continue
        for match in _TILLR_IMPORT_RE.finditer(text):
            imports.add(match.group(1))
    return imports


def undeclared_cross_module_go_reads(
    root: Path,
    edges: dict[str, dict] | None = None,
) -> list[str]:
    """Return findings for *_test.go files that walk or parse Go source outside their own module
    without a declared reads edge in tools/gate/go-test-edges.json."""
    if edges is None:
        edges = load_edges(root)
    modules = discover_tillr_modules(root)
    mod_names = {mod.split("/", 1)[1] for mod in modules}
    findings: list[str] = []

    for mod in modules:
        mod_name = mod.split("/", 1)[1]
        mod_root = root / mod
        for test_file in sorted(mod_root.rglob("*_test.go")):
            text = test_file.read_text(encoding="utf-8")
            is_go_parse = bool(_GO_PARSE_RE.search(text))
            is_go_walk = bool(_GO_WALK_RE.search(text) and _GO_EXT_RE.search(text))
            if not (is_go_parse or is_go_walk):
                continue

            pkg_rel = test_file.parent.relative_to(mod_root).as_posix()
            pkg_import = (
                f"github.com/mschulkind-oss/{mod_name}"
                if pkg_rel == "."
                else f"github.com/mschulkind-oss/{mod_name}/{pkg_rel}"
            )

            targets: set[tuple[str, str]] = set()
            for match in _REPO_REL_MOD_RE.finditer(text):
                if match.group(1) in mod_names and match.group(1) != mod_name:
                    targets.add((match.group(1), (match.group(2) or "").rstrip("/")))
            for match in _PARENT_REL_MOD_RE.finditer(text):
                if match.group(1) in mod_names and match.group(1) != mod_name:
                    targets.add((match.group(1), (match.group(2) or "").rstrip("/")))
            for match in _FILEPATH_JOIN_RE.finditer(text):
                args = _STR_LIT_RE.findall(match.group(1))
                if ".." not in args and "tools" not in args:
                    continue
                for idx, arg in enumerate(args):
                    if arg in mod_names and arg != mod_name:
                        sub = "/".join(a for a in args[idx + 1 :] if "*" not in a)
                        targets.add((arg, sub.rstrip("/")))
                        break
            for match in _MOD_GLOB_RE.finditer(text):
                glob_pat = match.group(1)
                for other in mod_names:
                    if other == mod_name:
                        continue
                    if glob_pat == "tillr-*" and not other.startswith("tillr-"):
                        continue
                    targets.add((other, ""))

            if not targets:
                continue

            # If a whole module is already in targets, drop more specific subpaths under it.
            whole_mods = {t_mod for t_mod, sub in targets if not sub}
            deduped = sorted(
                (t_mod, sub)
                for t_mod, sub in targets
                if not sub or t_mod not in whole_mods
            )

            read_rules = edges.get(pkg_import, {}).get("reads", [])
            pkg_imports = _package_tillr_imports(test_file.parent)
            rel_test = test_file.relative_to(root).as_posix()

            for target_mod_name, sub in deduped:
                if sub and "." in Path(sub).name and not sub.endswith(".go"):
                    continue
                target_path = f"tools/{target_mod_name}" + (f"/{sub}" if sub else "")
                probe = target_path if target_path.endswith(".go") else f"{target_path}/synthetic.go"
                if any(path_matches_read(probe, rule) for rule in read_rules):
                    continue
                target_pkg_sub = Path(sub).parent.as_posix() if sub.endswith(".go") else sub
                if target_pkg_sub == ".":
                    target_pkg_sub = ""
                target_imp = f"github.com/mschulkind-oss/{target_mod_name}" + (
                    f"/{target_pkg_sub}" if target_pkg_sub else ""
                )
                if not is_go_walk and target_imp in pkg_imports:
                    continue
                findings.append(
                    f"{rel_test}: {pkg_import} reads Go source in {target_path} "
                    f"without a declared read in tools/gate/go-test-edges.json"
                )
    return findings


def build_env() -> dict[str, str]:
    env = dict(os.environ, CGO_ENABLED="0")
    for key in STRIPPED_OUTER_ENV:
        env.pop(key, None)
    existing = env.get("GOFLAGS", "").split()
    if "-trimpath" not in existing:
        existing.append("-trimpath")
    env["GOFLAGS"] = " ".join(existing)
    durable = env.get("YOLO_DURABLE_DIR") or env.get("TMPDIR")
    return _bypass_git_shim_env(
        env,
        _shared_cache_dir(env),
        Path(durable) / ".go-affected-scratch" if durable else None,
    )


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


def _resolve_edge_builds(
    root: Path,
    edge: dict,
    pkg_info: dict[str, dict],
) -> dict[str, list[str] | None]:
    per_target: dict[str, list[str] | None] = {}
    for target in edge.get("builds", []):
        info = pkg_info.get(target)
        if not info:
            per_target[target] = None
            continue
        files: set[str] = set()
        mods: set[str] = set()
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
        per_target[target] = sorted(files)
    return per_target


def compute_edge_digest(
    root: Path,
    edge: dict,
    pkg_info: dict[str, dict],
    *,
    active_tests: set[str] | list[str] | tuple[str, ...] | None = None,
    resolved_builds: dict[str, list[str] | None] | tuple[list[str], list[str]] | None = None,
) -> str:
    hasher = hashlib.sha256()
    build_tests = edge.get("build_tests", {})
    read_tests = edge.get("read_tests", {})
    active_set = set(active_tests) if active_tests is not None else None
    files: set[str] = set()
    if resolved_builds is None:
        resolved_by_target: dict[str, list[str] | None] | None = _resolve_edge_builds(
            root, edge, pkg_info
        )
    elif isinstance(resolved_builds, dict):
        resolved_by_target = resolved_builds
    else:
        legacy_files, legacy_missing = resolved_builds
        for target in legacy_missing:
            hasher.update(f"missing-build:{target}\n".encode("utf-8"))
        files.update(legacy_files)
        resolved_by_target = None
    if resolved_by_target is not None:
        for target in edge.get("builds", []):
            if active_set is not None and target in build_tests:
                if not (set(build_tests[target]) & active_set):
                    continue
            target_files = resolved_by_target.get(target)
            if target_files is None:
                hasher.update(f"missing-build:{target}\n".encode("utf-8"))
            else:
                files.update(target_files)
    for rule in edge.get("reads", []):
        if active_set is not None and rule in read_tests:
            if not (set(read_tests[rule]) & active_set):
                continue
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


GO_PACKAGE_VERDICTS_FILE = "gate.go-packages.json"
GO_VERDICTS_IN_ENV = "TILLR_GO_VERDICTS_IN"
GO_VERDICTS_OUT_ENV = "TILLR_GO_VERDICTS_OUT"
_RUN_ID_MARKER_RE = re.compile(r"(?:-([0-9]{3,12}))?-(failover|restart|fresh|interrupted)$")
_BRIEF_FIRST_RUN_RE = re.compile(r"\bIts first run was ([^\s,]+)")
_BRIEF_ITEM_RE = re.compile(r"^\s*-\s*\*\*Item:\*\*\s*(\S+)", re.MULTILINE)


def split_run_id_markers(run_id: str) -> str:
    """Strip trailing continuation markers (-restart/-fresh/-interrupted/-failover) from run_id."""
    cur = run_id
    while True:
        m = _RUN_ID_MARKER_RE.search(cur)
        if m is None:
            return cur
        cur = cur[: len(cur) - len(m.group(0))]


def is_cfix_run_id(run_id: str) -> bool:
    """Return True iff run_id denotes a conflict-repair (-cfix) run."""
    return split_run_id_markers(run_id).endswith("-cfix")


def read_run_item_id(run_dir: Path) -> str | None:
    """Read item_id from gate.face.json, run.record.json, or brief.md when present."""
    for fname in ("gate.face.json", "run.record.json"):
        p = run_dir / fname
        if p.is_file():
            with contextlib.suppress(OSError, ValueError):
                obj = json.loads(p.read_text(encoding="utf-8"))
                if isinstance(obj, dict):
                    item_id = obj.get("item_id")
                    if not isinstance(item_id, str) or not item_id.strip():
                        payload = obj.get("payload")
                        if isinstance(payload, dict):
                            item_id = payload.get("item_id")
                    if isinstance(item_id, str) and item_id.strip():
                        return item_id.strip()
    brief = run_dir / "brief.md"
    if brief.is_file():
        with contextlib.suppress(OSError):
            m = _BRIEF_ITEM_RE.search(brief.read_text(encoding="utf-8", errors="replace"))
            if m:
                return m.group(1).strip()
    return None


def resolve_cfix_prior_run_dirs(run_dir: Path) -> list[Path]:
    """Return pre-conflict run directories for the same item when run_dir is a -cfix run."""
    run_dir = run_dir.resolve()
    run_id = run_dir.name
    if not is_cfix_run_id(run_id):
        return []
    loop_dir = run_dir.parent
    expected_item = read_run_item_id(run_dir)
    queue: list[Path] = [run_dir]
    visited: set[str] = {run_id}
    prior_dirs: list[Path] = []
    while queue:
        cur = queue.pop(0)
        stem = split_run_id_markers(cur.name)
        candidates: list[str] = []
        if cur.name != stem:
            candidates.append(stem)
        if stem.endswith("-cfix"):
            pred = stem[: -len("-cfix")]
            candidates.append(pred)
            pred_stem = split_run_id_markers(pred)
            if pred_stem != pred:
                candidates.append(pred_stem)
        brief = cur / "brief.md"
        if brief.is_file():
            with contextlib.suppress(OSError):
                m = _BRIEF_FIRST_RUN_RE.search(brief.read_text(encoding="utf-8", errors="replace"))
                if m:
                    candidates.append(m.group(1).strip())
        for cand_id in candidates:
            if not cand_id or cand_id in visited:
                continue
            visited.add(cand_id)
            cand_dir = loop_dir / cand_id
            if not cand_dir.is_dir():
                continue
            cand_item = read_run_item_id(cand_dir)
            if expected_item and cand_item and cand_item != expected_item:
                continue
            prior_dirs.append(cand_dir)
            if is_cfix_run_id(cand_id):
                queue.append(cand_dir)
    return prior_dirs


def merge_package_verdicts_into(target: dict, source: dict) -> bool:
    """Merge passing package verdict entries from source into target; return True if any added."""
    src_pkgs = source.get("packages") if isinstance(source, dict) else None
    if not isinstance(src_pkgs, dict):
        return False
    dst_pkgs = target.setdefault("packages", {})
    added = False
    for ip, rec in src_pkgs.items():
        if not isinstance(ip, str) or not isinstance(rec, dict):
            continue
        dst = dst_pkgs.setdefault(ip, {"whole_digests": [], "narrowed": {}})
        whole_list = dst.setdefault("whole_digests", [])
        for d in rec.get("whole_digests") or ():
            if isinstance(d, str) and d:
                added = True
                if d not in whole_list:
                    whole_list.append(d)
        single = rec.get("whole_digest")
        if isinstance(single, str) and single:
            added = True
            if single not in whole_list:
                whole_list.append(single)
        narrow_src = rec.get("narrowed")
        if isinstance(narrow_src, dict):
            narrow_dst = dst.setdefault("narrowed", {})
            for k, nrec in narrow_src.items():
                if not isinstance(nrec, dict):
                    continue
                ntests = nrec.get("tests")
                ndig = nrec.get("digest")
                if isinstance(ntests, list) and isinstance(ndig, str) and ndig:
                    added = True
                    narrow_dst[k] = {"tests": list(ntests), "digest": ndig}
    return added


def load_package_verdicts_file(path: Path) -> dict | None:
    if not path.is_file():
        return None
    with contextlib.suppress(OSError, ValueError):
        raw = json.loads(path.read_text(encoding="utf-8"))
        if isinstance(raw, dict) and isinstance(raw.get("packages"), dict):
            merged: dict = {"schema": 1, "packages": {}}
            merge_package_verdicts_into(merged, raw)
            return merged
    return None


def load_prior_cfix_verdicts(run_dir: Path) -> dict | None:
    prior_dirs = resolve_cfix_prior_run_dirs(run_dir)
    if not prior_dirs:
        return None
    merged: dict = {"schema": 1, "packages": {}}
    any_found = False
    for cand_dir in prior_dirs:
        loaded = load_package_verdicts_file(cand_dir / GO_PACKAGE_VERDICTS_FILE)
        if loaded and merge_package_verdicts_into(merged, loaded):
            any_found = True
    return merged if any_found else None


def save_package_verdicts_atomic(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=".go-packages-", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, sort_keys=True)
            f.write("\n")
        os.replace(tmp_name, path)
    except Exception:
        with contextlib.suppress(OSError):
            os.unlink(tmp_name)
        raise


def compute_package_verdict_digest(
    root: Path,
    import_path: str,
    graph: dict,
    edges: dict[str, dict],
    *,
    active_tests: list[str] | None = None,
    cache: dict | None = None,
) -> str | None:
    """Compute SHA-256 over a Go test package's files, transitive in-repo Go dependencies,
    enclosing go.mod/go.sum files, Go toolchain version, and declared builds/reads edges.
    Returns None if any module or input cannot be resolved cleanly.
    """
    root = root.resolve()
    pkg_info = graph["pkg_info"]
    test_deps = graph["test_deps"]
    dir_to_pkg = graph["dir_to_pkg"]
    failed_modules = graph["failed_modules"]
    if import_path == "./..." or import_path not in pkg_info:
        return None
    info = pkg_info[import_path]
    if info["module"] in failed_modules:
        return None
    dep_closure = {import_path} | set(test_deps.get(import_path, ()))
    in_repo_deps = sorted(d for d in dep_closure if d in pkg_info)
    for dep in in_repo_deps:
        if pkg_info[dep]["module"] in failed_modules:
            return None

    if cache is None:
        cache = {}
    dir_hashes: dict[tuple[str, bool], str | None] = cache.setdefault("dir_hashes", {})
    mod_hashes: dict[str, str | None] = cache.setdefault("mod_hashes", {})
    if "go_version" not in cache:
        try:
            ver_proc = subprocess.run(
                ["go", "env", "GOVERSION"],
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                env=build_env(),
                timeout=15,
            )
            cache["go_version"] = ver_proc.stdout.strip() if ver_proc.returncode == 0 else ""
        except (OSError, subprocess.SubprocessError):
            cache["go_version"] = ""

    def pkg_dir_digest(dir_rel: str, include_test_go: bool) -> str | None:
        key = (dir_rel, include_test_go)
        if key in dir_hashes:
            return dir_hashes[key]
        base_dir = root / dir_rel
        if not base_dir.is_dir():
            dir_hashes[key] = None
            return None
        h = hashlib.sha256()
        for dirpath, dirnames, filenames in os.walk(base_dir, topdown=True):
            rel_dir = Path(dirpath).relative_to(root).as_posix()
            kept_dirs: list[str] = []
            for dname in sorted(dirnames):
                if dname in {".git", "node_modules", "__pycache__"}:
                    continue
                sub_rel = f"{rel_dir}/{dname}" if rel_dir != "." else dname
                if sub_rel != dir_rel and sub_rel in dir_to_pkg:
                    continue
                if sub_rel == EXCLUDED_PACK_MIRROR_PREFIX.rstrip("/") or sub_rel.startswith(
                    EXCLUDED_PACK_MIRROR_PREFIX
                ):
                    continue
                kept_dirs.append(dname)
            dirnames[:] = kept_dirs
            for fn in sorted(filenames):
                if not include_test_go and fn.endswith("_test.go"):
                    continue
                fpath = Path(dirpath) / fn
                frel = fpath.relative_to(root).as_posix()
                try:
                    raw = fpath.read_bytes()
                except OSError:
                    dir_hashes[key] = None
                    return None
                h.update(frel.encode("utf-8") + b"\0" + str(len(raw)).encode("ascii") + b"\0")
                h.update(raw)
                h.update(b"\0")
        res_hex = h.hexdigest()
        dir_hashes[key] = res_hex
        return res_hex

    def mod_files_digest(mod: str) -> str | None:
        if mod in mod_hashes:
            return mod_hashes[mod]
        h = hashlib.sha256()
        for fn in ("go.mod", "go.sum"):
            rel = f"{mod}/{fn}"
            fpath = root / rel
            h.update(rel.encode("utf-8") + b"\0")
            if fpath.exists():
                try:
                    raw = fpath.read_bytes()
                except OSError:
                    mod_hashes[mod] = None
                    return None
                h.update(b"present\0" + str(len(raw)).encode("ascii") + b"\0")
                h.update(raw)
            else:
                h.update(b"absent")
            h.update(b"\0")
        res_hex = h.hexdigest()
        mod_hashes[mod] = res_hex
        return res_hex

    scope_tag = (
        "whole"
        if active_tests is None
        else "tests:" + ",".join(sorted(dict.fromkeys(active_tests)))
    )
    hasher = hashlib.sha256()
    hasher.update(
        f"v1\0pkg={import_path}\0goversion={cache['go_version']}\0scope={scope_tag}\n".encode(
            "utf-8"
        )
    )
    for dep in in_repo_deps:
        include_tests = dep == import_path
        dd = pkg_dir_digest(pkg_info[dep]["dir_rel"], include_tests)
        if dd is None:
            return None
        hasher.update(f"dep:{dep}:{int(include_tests)}:{dd}\n".encode("utf-8"))
    for mod in sorted({pkg_info[dep]["module"] for dep in in_repo_deps}):
        md = mod_files_digest(mod)
        if md is None:
            return None
        hasher.update(f"mod:{mod}:{md}\n".encode("utf-8"))
    if import_path in edges:
        edge = edges[import_path]
        for btarget in edge.get("builds", ()):
            if btarget not in pkg_info or pkg_info[btarget]["module"] in failed_modules:
                return None
        edge_spec = json.dumps(edge, sort_keys=True, separators=(",", ":"))
        edge_dig = compute_edge_digest(root, edge, pkg_info, active_tests=active_tests)
        hasher.update(f"edge:{edge_spec}:{edge_dig}\n".encode("utf-8"))
    return hasher.hexdigest()


def package_verdict_reusable(
    root: Path,
    import_path: str,
    graph: dict,
    edges: dict[str, dict],
    prior_verdicts: dict | None,
    *,
    selected_tests: list[str] | None = None,
    cache: dict | None = None,
) -> bool:
    """Return True if prior_verdicts carries a passing verdict for import_path whose
    transitive input + edge digest matches the current tree.
    """
    if not prior_verdicts:
        return False
    pkgs = prior_verdicts.get("packages")
    if not isinstance(pkgs, dict):
        return False
    entry = pkgs.get(import_path)
    if not isinstance(entry, dict):
        return False
    whole_digests = entry.get("whole_digests") or []
    if whole_digests:
        whole_cur = compute_package_verdict_digest(
            root, import_path, graph, edges, active_tests=None, cache=cache
        )
        if whole_cur is not None and whole_cur in whole_digests:
            return True
    if selected_tests is not None:
        req_set = set(selected_tests)
        narrowed_map = entry.get("narrowed") or {}
        if isinstance(narrowed_map, dict):
            for rec in narrowed_map.values():
                if not isinstance(rec, dict):
                    continue
                rec_tests = rec.get("tests")
                rec_digest = rec.get("digest")
                if not isinstance(rec_tests, list) or not isinstance(rec_digest, str):
                    continue
                if req_set <= set(rec_tests):
                    cur = compute_package_verdict_digest(
                        root, import_path, graph, edges, active_tests=rec_tests, cache=cache
                    )
                    if cur is not None and cur == rec_digest:
                        return True
    return False


def record_package_verdict(
    root: Path,
    import_path: str,
    graph: dict,
    edges: dict[str, dict],
    verdicts_store: dict,
    *,
    selected_tests: list[str] | None = None,
    cache: dict | None = None,
) -> bool:
    """Record a passing package verdict into verdicts_store; return True if recorded."""
    digest = compute_package_verdict_digest(
        root, import_path, graph, edges, active_tests=selected_tests, cache=cache
    )
    if digest is None:
        return False
    pkgs = verdicts_store.setdefault("packages", {})
    entry = pkgs.setdefault(import_path, {"whole_digests": [], "narrowed": {}})
    if selected_tests is None:
        whole_list = entry.setdefault("whole_digests", [])
        if digest not in whole_list:
            whole_list.append(digest)
    else:
        ordered = sorted(dict.fromkeys(selected_tests))
        key = "\0".join(ordered)
        entry.setdefault("narrowed", {})[key] = {"tests": ordered, "digest": digest}
    return True


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
    drop_packages: set[str] | frozenset[str] | None = None,
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
    drop_pkg_set = set(DEFERRED_PROOF_PACKAGES if drop_packages is None else drop_packages)
    norm_changed = [os.path.normpath(p).lstrip("./") for p in changed if p and p.strip()]

    all_reads = [
        r
        for ip, e in edges.items()
        if ip not in drop_pkg_set
        for r in e.get("reads", [])
    ]
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
    whole_pkg_selected: set[str] = set()
    matched_scoped_builds: dict[str, list[str]] = {}
    matched_scoped_reads: dict[str, list[str]] = {}

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
            # A *_test.go file is only compiled when testing its own package; it cannot
            # affect reverse importers or sibling `builds` targets.
            if not path.endswith("_test.go"):
                changed_pkgs.add(placed)
            if pkg_info[placed]["has_tests"]:
                mark(placed, "changed")
                whole_pkg_selected.add(placed)

    # Step 2: Importers (reverse import graph across all tillr modules).
    if changed_pkgs:
        for ip, info in pkg_info.items():
            if not info["has_tests"]:
                continue
            if test_deps.get(ip, set()) & changed_pkgs:
                mark(ip, "import")
                whole_pkg_selected.add(ip)

    # Step 3: Declared edges (builds + reads).
    for test_pkg, edge in edges.items():
        if test_pkg in drop_pkg_set:
            continue
        info = pkg_info.get(test_pkg)
        if not info or not info["has_tests"]:
            continue
        matched_builds: list[str] = []
        for target in edge.get("builds", []):
            if target in changed_pkgs:
                matched_builds.append(target)
                continue
            tinfo = pkg_info.get(target)
            if tinfo and (tinfo["deps"] & changed_pkgs):
                matched_builds.append(target)
        matched_reads = [
            rule
            for rule in edge.get("reads", [])
            if any(path_matches_read(p, rule) for p in norm_changed)
        ]
        if matched_builds or matched_reads:
            mark(test_pkg, "edge")
            build_tests_map = edge.get("build_tests", {})
            read_tests_map = edge.get("read_tests", {})
            if any(t not in build_tests_map for t in matched_builds) or any(
                r not in read_tests_map for r in matched_reads
            ):
                whole_pkg_selected.add(test_pkg)
            else:
                if matched_builds:
                    matched_scoped_builds[test_pkg] = matched_builds
                if matched_reads:
                    matched_scoped_reads[test_pkg] = matched_reads

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
                whole_pkg_selected.add(ip)

    selected: list[dict] = []
    for ip in sorted(reasons):
        info = pkg_info[ip]
        if info["module"] in drop_set or ip in drop_pkg_set:
            continue
        has_edge = ip in edges
        selected_tests: list[str] | None = None
        if ip not in whole_pkg_selected and (ip in matched_scoped_builds or ip in matched_scoped_reads):
            build_tests_map = edges[ip].get("build_tests", {})
            read_tests_map = edges[ip].get("read_tests", {})
            wanted_list: list[str] = []
            wanted_set: set[str] = set()
            for target in matched_scoped_builds.get(ip, ()):
                for tname in build_tests_map.get(target, ()):
                    if tname != "TestEdgeInputs" and tname not in wanted_set:
                        wanted_set.add(tname)
                        wanted_list.append(tname)
            for rule in matched_scoped_reads.get(ip, ()):
                for tname in read_tests_map.get(rule, ()):
                    if tname != "TestEdgeInputs" and tname not in wanted_set:
                        wanted_set.add(tname)
                        wanted_list.append(tname)
            pkg_tests = scan_package_tests(root, {
                "import_path": ip,
                "module": info["module"],
                "dir_rel": info["dir_rel"],
                "test_files": info["test_files"],
            })
            ordered = [t for t in pkg_tests if t in wanted_set]
            ordered_set = set(ordered)
            for t in wanted_list:
                if t not in ordered_set:
                    ordered.append(t)
            selected_tests = ordered
        resolved_builds = _resolve_edge_builds(root, edges[ip], pkg_info) if has_edge else None
        digest = (
            compute_edge_digest(
                root,
                edges[ip],
                pkg_info,
                active_tests=selected_tests,
                resolved_builds=resolved_builds,
            )
            if has_edge
            else None
        )
        entry_item = {
            "import_path": ip,
            "module": info["module"],
            "dir_rel": info["dir_rel"],
            "test_files": info["test_files"],
            "reason": reasons[ip],
            "selected_tests": selected_tests,
            "has_edge": has_edge,
            "edge_digest": digest,
        }
        if resolved_builds is not None:
            entry_item["_resolved_builds"] = resolved_builds
        selected.append(entry_item)
    for mod in failed_floor_mods:
        selected.append({
            "import_path": "./...",
            "module": mod,
            "dir_rel": mod,
            "test_files": [],
            "reason": "floor",
            "selected_tests": None,
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
            if name and name != "TestMain" and name not in seen_set:
                seen_set.add(name)
                seen.append(name)
    return seen


def _leaf_failed_tests(names: list[str]) -> list[str]:
    """Drop parent test names when a subtest of that parent also failed."""
    deduped = list(dict.fromkeys(names))
    return [
        name for name in deduped
        if not any(other != name and other.startswith(name + "/") for other in deduped)
    ]


TRIAGE_CASES = 10
SIGQUIT_ASSERTION = "blocked: the test binary was ended by SIGQUIT while this test was running"
_SIGQUIT_FRAME_RE = re.compile(
    r"^(?P<pkg>(?:[^\s()]+/)?[^\s./()]+)\.(?P<name>Test(?:[^a-z\W]\w*)?)\("
)
_GO_FAIL_HEADER_RE = re.compile(r"^\s*--- FAIL: (?P<name>\S+) \(")
_GO_FAIL_LOC_RE = re.compile(r"^\s+(?P<file>[\w./-]+\.go):(?P<line>\d+):\s*(?P<msg>.*)$")
_GO_PKG_FAIL_RE = re.compile(r"^FAIL\t(?P<package>\S+)")


def _reconstruct_go_test_output(stdout: str) -> str:
    """Reconstruct raw test binary output in event order from `go test -json` or plain lines."""
    chunks: list[str] = []
    for line in stdout.splitlines(keepends=True):
        stripped = line.lstrip()
        if not stripped.startswith("{"):
            chunks.append(line)
            continue
        try:
            ev = json.loads(stripped)
        except ValueError:
            chunks.append(line)
            continue
        if not isinstance(ev, dict):
            chunks.append(line)
            continue
        if ev.get("Action") == "output":
            out_chunk = ev.get("Output")
            if isinstance(out_chunk, str):
                chunks.append(out_chunk)
    return "".join(chunks)


def extract_sigquit_cases(
    stdout: str,
    known_packages: set[str] | None = None,
) -> list[dict]:
    """Return one failing case per unique top-level Test* frame in a SIGQUIT goroutine dump."""
    lines = _reconstruct_go_test_output(stdout).splitlines()
    sigquit_idx: int | None = None
    for idx, line in enumerate(lines):
        if line.strip() == "SIGQUIT: quit":
            sigquit_idx = idx
            break
    if sigquit_idx is None:
        return []

    cases: list[dict] = []
    seen: set[tuple[str, str]] = set()
    known = known_packages or set()
    for idx in range(sigquit_idx + 1, len(lines)):
        m = _SIGQUIT_FRAME_RE.match(lines[idx])
        if not m:
            continue
        name = m.group("name")
        if name == "TestMain":
            continue
        raw_pkg = m.group("pkg")
        pkg = raw_pkg[:-5] if raw_pkg.endswith("_test") and raw_pkg not in known else raw_pkg
        key = (pkg, name)
        if key in seen:
            continue
        seen.add(key)
        where = lines[idx + 1].strip() if idx + 1 < len(lines) else ""
        cases.append({
            "package": pkg,
            "name": name,
            "where": where,
            "assertion": SIGQUIT_ASSERTION,
            "sigquit": True,
        })
    return cases


def _extract_fail_cases(readable: str, parsed: dict[str, dict]) -> list[dict]:
    """Extract ordinary failing test cases from `--- FAIL:` blocks in readable `go test` output."""
    raw_cases: list[dict] = []
    open_cases: list[dict] = []
    current: dict | None = None
    for line in readable.splitlines():
        m_fail = _GO_FAIL_HEADER_RE.match(line)
        if m_fail:
            current = {
                "package": "",
                "name": m_fail.group("name"),
                "file": "",
                "line": "",
                "assertion": "",
            }
            raw_cases.append(current)
            open_cases.append(current)
            continue
        m_loc = _GO_FAIL_LOC_RE.match(line)
        if m_loc and current is not None and not current["file"]:
            current["file"] = m_loc.group("file")
            current["line"] = m_loc.group("line")
            current["assertion"] = m_loc.group("msg").strip()
            continue
        m_pkg = _GO_PKG_FAIL_RE.match(line)
        if m_pkg:
            pkg_name = m_pkg.group("package")
            for case in open_cases:
                case["package"] = pkg_name
            open_cases = []
            current = None

    if open_cases and parsed:
        sole_pkg = next(iter(parsed)) if len(parsed) == 1 else ""
        for case in open_cases:
            if case["package"]:
                continue
            if sole_pkg:
                case["package"] = sole_pkg
                continue
            matches = [
                p for p, rec in parsed.items()
                if case["name"] in (rec.get("failed_tests") or ())
            ]
            if len(matches) == 1:
                case["package"] = matches[0]

    names_by_pkg: dict[str, list[str]] = {}
    for case in raw_cases:
        names_by_pkg.setdefault(case["package"], []).append(case["name"])
    leaves_by_pkg = {pkg: set(_leaf_failed_tests(names)) for pkg, names in names_by_pkg.items()}

    cases: list[dict] = []
    seen: set[tuple[str, str]] = set()
    for case in raw_cases:
        pkg = case["package"]
        name = case["name"]
        if name not in leaves_by_pkg.get(pkg, ()):
            continue
        key = (pkg, name)
        if key in seen:
            continue
        seen.add(key)
        loc = f"{case['file']}:{case['line']}" if case["file"] and case["line"] else ""
        if pkg and loc:
            where = f"{pkg} ({loc})"
        elif pkg:
            where = pkg
        elif loc:
            where = loc
        else:
            where = ""
        cases.append({
            "package": pkg,
            "name": name,
            "where": where,
            "assertion": case["assertion"],
            "sigquit": False,
        })
    return cases


def format_triage_block(
    cases: list[dict],
    pkg_locations: dict[str, tuple[str, str]],
) -> str:
    """Render a bounded `fork-check: triage --` block for a failing `go-affected` run."""
    lines = [
        "fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:\n",
        "  triage go-affected: Go test packages affected by the change pass\n",
    ]
    if not cases:
        lines.append("       no case named in the output; reproduce with: python3 tools/gate/go_affected.py\n")
        return "".join(lines)
    kept = cases[:TRIAGE_CASES]
    for case in kept:
        where = case.get("where") or "(no file named)"
        lines.append(f"       case: {case['name']} -- {where}\n")
        assertion = case.get("assertion", "")
        if assertion:
            lines.append(f"       assertion: {assertion.splitlines()[0]}\n")
        loc = pkg_locations.get(case.get("package", ""))
        if loc is not None:
            mod, pkg_rel = loc
            lines.append(f"       one case: go -C {mod} test {pkg_rel} -run '^{case['name']}$'\n")
    dropped = len(cases) - len(kept)
    if dropped > 0:
        lines.append(f"       ({dropped} more failing case(s) not listed; the packet keeps {len(kept)})\n")
    return "".join(lines)


def format_failure_summary(failed_packages: list[tuple[str, list[str]]]) -> str:
    """Render the failing packages and tests block appended to failed check output."""
    if not failed_packages:
        return ""
    lines = ["go-affected: failing packages and tests:\n"]
    for pkg_name, tests in failed_packages:
        if tests:
            lines.append(f"  {pkg_name}: {', '.join(tests)}\n")
        else:
            lines.append(f"  {pkg_name}: (no failing test named)\n")
    return "".join(lines)


def parse_go_test_json(stdout: str) -> tuple[dict[str, dict], str]:
    """Return (per-package {elapsed_ms, cached, passed, failed_tests}, plain `go test` failure text)."""
    results: dict[str, dict] = {}
    non_pkg_chunks: list[str] = []
    pkg_chunks: dict[str, list[str]] = {}
    raw_failed: dict[str, list[str]] = {}
    test_headers: dict[tuple[str, str], str] = {}
    test_bodies: dict[tuple[str, str], list[str]] = {}
    for line in stdout.splitlines(keepends=True):
        stripped = line.lstrip()
        if not stripped.startswith("{"):
            non_pkg_chunks.append(line)
            continue
        try:
            ev = json.loads(stripped)
        except ValueError:
            non_pkg_chunks.append(line)
            continue
        if not isinstance(ev, dict):
            non_pkg_chunks.append(line)
            continue
        out_chunk = ev.get("Output")
        pkg = ev.get("Package")
        test_name = ev.get("Test")
        action = ev.get("Action")
        if not isinstance(pkg, str) or not pkg:
            if isinstance(out_chunk, str):
                non_pkg_chunks.append(out_chunk)
            continue
        rec = results.setdefault(
            pkg, {"elapsed_ms": 0, "cached": False, "passed": None, "failed_tests": []}
        )
        chunks = pkg_chunks.setdefault(pkg, [])
        if isinstance(test_name, str) and test_name:
            key = (pkg, test_name)
            if action == "output" and isinstance(out_chunk, str):
                s = out_chunk.lstrip()
                if s.startswith("--- FAIL:"):
                    test_headers[key] = out_chunk
                elif not s.startswith(("=== RUN", "=== PAUSE", "=== CONT", "--- PASS:", "--- SKIP:")):
                    test_bodies.setdefault(key, []).append(out_chunk)
            elif action == "fail":
                raw_failed.setdefault(pkg, []).append(test_name)
                elapsed = ev.get("Elapsed")
                dur = f"{elapsed:.2f}s" if isinstance(elapsed, (int, float)) else "0.00s"
                header = test_headers.pop(key, f"--- FAIL: {test_name} ({dur})\n")
                chunks.append(header)
                chunks.extend(test_bodies.pop(key, ()))
            elif action in ("pass", "skip"):
                test_headers.pop(key, None)
                test_bodies.pop(key, None)
        else:
            if action == "output" and isinstance(out_chunk, str):
                if "(cached)" in out_chunk:
                    rec["cached"] = True
                chunks.append(out_chunk)
            elif action in ("pass", "fail", "skip"):
                elapsed = ev.get("Elapsed")
                if isinstance(elapsed, (int, float)):
                    rec["elapsed_ms"] = int(round(elapsed * 1000))
                rec["passed"] = action != "fail"

    sigquit_by_pkg: dict[str, list[str]] = {}
    for case in extract_sigquit_cases(stdout, known_packages=set(results)):
        sigquit_by_pkg.setdefault(case["package"], []).append(case["name"])

    for pkg, sq_names in sigquit_by_pkg.items():
        rec = results.setdefault(
            pkg, {"elapsed_ms": 0, "cached": False, "passed": False, "failed_tests": []}
        )
        if rec["passed"] is not False:
            rec["passed"] = False

    for pkg, rec in results.items():
        normal_failed = _leaf_failed_tests(raw_failed.get(pkg, []))
        sq_failed = sigquit_by_pkg.get(pkg, [])
        rec["failed_tests"] = list(dict.fromkeys(sq_failed + normal_failed))
        if rec["passed"] is False:
            chunks = pkg_chunks.setdefault(pkg, [])
            for key in [k for k in list(test_headers) + list(test_bodies) if k[0] == pkg]:
                header = test_headers.pop(key, None)
                body = test_bodies.pop(key, ())
                if header:
                    chunks.append(header)
                if body:
                    chunks.extend(body)

    text_chunks = list(non_pkg_chunks)
    for pkg in results:
        text_chunks.extend(pkg_chunks.get(pkg, ()))
    return results, "".join(text_chunks)


def format_progress_event(line: str) -> str | None:
    """Return a short progress line for a completed test or package JSON event, else None."""
    stripped = line.lstrip()
    if not stripped.startswith("{"):
        return None
    try:
        ev = json.loads(stripped)
    except ValueError:
        return None
    if not isinstance(ev, dict):
        return None
    action = ev.get("Action")
    if action not in ("pass", "fail", "skip"):
        return None
    pkg = ev.get("Package")
    if not isinstance(pkg, str) or not pkg:
        return None
    test_name = ev.get("Test")
    elapsed = ev.get("Elapsed")
    dur = f" ({elapsed:.2f}s)" if isinstance(elapsed, (int, float)) else ""
    if isinstance(test_name, str) and test_name:
        return f"go-affected: {action} {pkg} {test_name}{dur}\n"
    return f"go-affected: {action} {pkg}{dur}\n"


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


def _set_child_subreaper() -> None:
    if not sys.platform.startswith("linux"):
        return
    try:
        libc = ctypes.CDLL("libc.so.6", use_errno=True)
        libc.prctl(PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
    except OSError:
        pass


def _proc_children_and_pgrp(root_pid: int) -> tuple[set[int], set[int]]:
    """Return (all descendant/pgrp PIDs of root_pid, live non-zombie PIDs to kill)."""
    children: dict[int, list[int]] = {}
    states: dict[int, str] = {}
    same_pgrp: set[int] = set()
    proc = Path("/proc")
    if not proc.is_dir():
        return set(), set()
    for entry in proc.iterdir():
        name = entry.name
        if not name.isdigit():
            continue
        pid = int(name)
        if pid == root_pid:
            continue
        try:
            raw = (entry / "stat").read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        rparen = raw.rfind(")")
        if rparen < 0:
            continue
        fields = raw[rparen + 2:].split()
        if len(fields) < 3:
            continue
        state = fields[0]
        try:
            ppid = int(fields[1])
            pgrp = int(fields[2])
        except ValueError:
            continue
        states[pid] = state
        children.setdefault(ppid, []).append(pid)
        if pgrp == root_pid:
            same_pgrp.add(pid)
    descendants: set[int] = set()
    stack = list(children.get(root_pid, ()))
    while stack:
        cur = stack.pop()
        if cur in descendants:
            continue
        descendants.add(cur)
        stack.extend(children.get(cur, ()))
    all_targets = descendants | same_pgrp
    live = {p for p in all_targets if states.get(p) != "Z"}
    return all_targets, live


def _reap_nonblocking() -> bool:
    """Reap exited direct children without blocking. Return True when ECHILD."""
    while True:
        try:
            pid, _ = os.waitpid(-1, os.WNOHANG)
        except ChildProcessError:
            return True
        except OSError:
            return True
        if pid == 0:
            return False


def _kill_and_reap_descendants(root_pid: int, max_wait: float = 5.0) -> None:
    deadline = time.monotonic() + max_wait
    while True:
        all_targets, live = _proc_children_and_pgrp(root_pid)
        for pid in live:
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.kill(pid, signal.SIGKILL)
        no_children = _reap_nonblocking()
        if not live and not all_targets and no_children:
            return
        if time.monotonic() >= deadline:
            return
        time.sleep(0.01)


_WATCHDOG_INTERVAL_RE = re.compile(
    r"^\s*const\s+WatchdogSampleInterval\s*=\s*(?:(\d+)\s*\*\s*)?time\.(Nanosecond|Microsecond|Millisecond|Second|Minute|Hour)\b",
    re.MULTILINE,
)
_WATCHDOG_SILENT_RE = re.compile(
    r"^\s*const\s+WatchdogSilentSamples\s*=\s*(\d+)\b",
    re.MULTILINE,
)
_GO_TIME_UNIT_SECONDS = {
    "Nanosecond": 1e-9,
    "Microsecond": 1e-6,
    "Millisecond": 1e-3,
    "Second": 1.0,
    "Minute": 60.0,
    "Hour": 3600.0,
}


def _load_watchdog_constants(watchdog_path: Path | None = None) -> tuple[float, int] | None:
    """Read WatchdogSampleInterval (seconds) and WatchdogSilentSamples from tools/tillr-gate/watchdog.go."""
    path = (
        watchdog_path
        if watchdog_path is not None
        else Path(__file__).resolve().parents[1] / "tillr-gate" / "watchdog.go"
    )
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return None
    m_int = _WATCHDOG_INTERVAL_RE.search(text)
    m_sil = _WATCHDOG_SILENT_RE.search(text)
    if m_int is None or m_sil is None:
        return None
    mult = int(m_int.group(1)) if m_int.group(1) is not None else 1
    interval_s = mult * _GO_TIME_UNIT_SECONDS[m_int.group(2)]
    silent_samples = int(m_sil.group(1))
    if interval_s <= 0 or silent_samples <= 0:
        return None
    return interval_s, silent_samples


class _BinaryStallWatchdog:
    """Apply tools/tillr-gate's watchdog sample rule to one test binary's process-tree CPU."""

    def __init__(self, silent_threshold: int) -> None:
        self.silent_threshold = silent_threshold
        self.previous_members: dict[tuple[int, str], int] | None = None
        self.high_cpu_ns = 0
        self.previous_cpu_ns = 0
        self.initialized = False
        self.silent_samples = 0
        self.unavailable = ""

    def observe(self, cpu_ns: int, cpu_changed: bool, err: str = "") -> bool:
        if self.unavailable:
            return False
        if err:
            self.unavailable = f"watchdog unavailable: {err}"
            self.silent_samples = 0
            return False
        if not self.initialized:
            self.previous_cpu_ns = cpu_ns
            self.initialized = True
            return False
        if cpu_changed or cpu_ns > self.previous_cpu_ns:
            self.silent_samples = 0
        else:
            self.silent_samples += 1
        self.previous_cpu_ns = cpu_ns
        return self.silent_samples >= self.silent_threshold

    def observe_tree(
        self,
        members: dict[tuple[int, str], int],
        total_ns: int,
        changed: bool,
        err: str = "",
    ) -> bool:
        if err:
            return self.observe(0, False, err)
        if self.previous_members is not None:
            if len(self.previous_members) != len(members):
                changed = True
            else:
                for member_id, ns in members.items():
                    if self.previous_members.get(member_id) != ns:
                        changed = True
                        break
        self.previous_members = members
        if total_ns > self.high_cpu_ns:
            self.high_cpu_ns = total_ns
        return self.observe(self.high_cpu_ns, changed)


def _is_proc_gone(exc: OSError) -> bool:
    return exc.errno in (errno.ENOENT, errno.ESRCH)


def _parse_proc_stat_fields(raw: str) -> list[str] | None:
    end = raw.rfind(")")
    if end < 0:
        return None
    fields = raw[end + 1:].split()
    return fields if len(fields) >= 20 else None


def _scan_proc_stats(
    proc_root: Path = Path("/proc"),
) -> tuple[dict[int, list[str]], dict[int, list[int]], dict[int, list[int]]] | None:
    by_pid: dict[int, list[str]] = {}
    children_of: dict[int, list[int]] = {}
    by_pgrp: dict[int, list[int]] = {}
    try:
        entries = list(proc_root.iterdir())
    except OSError:
        return None
    for entry in entries:
        name = entry.name
        if not name.isdigit():
            continue
        pid = int(name)
        try:
            raw = (entry / "stat").read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        fields = _parse_proc_stat_fields(raw)
        if fields is None:
            continue
        try:
            ppid = int(fields[1])
            pgrp = int(fields[2])
        except ValueError:
            continue
        by_pid[pid] = fields
        children_of.setdefault(ppid, []).append(pid)
        by_pgrp.setdefault(pgrp, []).append(pid)
    return by_pid, children_of, by_pgrp


def _is_go_test_binary(proc_root: Path, pid: int) -> bool:
    try:
        raw = (proc_root / str(pid) / "cmdline").read_bytes()
    except OSError:
        return False
    args = [part.decode("utf-8", errors="replace") for part in raw.split(b"\0") if part]
    return bool(args) and args[0].endswith(".test") and any(a.startswith("-test.") for a in args[1:])


def _sample_process_tree_cpu(
    proc_root: Path,
    root_pid: int,
    by_pid: dict[int, list[str]],
    children_of: dict[int, list[int]],
    by_pgrp: dict[int, list[int]],
) -> tuple[dict[tuple[int, str], int], int, bool, str]:
    if root_pid not in by_pid:
        return {}, 0, True, ""
    queue: list[int] = [root_pid]
    seen: set[int] = {root_pid}
    for p in by_pgrp.get(root_pid, ()):
        if p in by_pid and p not in seen:
            seen.add(p)
            queue.append(p)
    snapshots: list[tuple[int, list[str]]] = []
    idx = 0
    while idx < len(queue):
        cur = queue[idx]
        idx += 1
        snapshots.append((cur, by_pid[cur]))
        for child in children_of.get(cur, ()):
            if child in by_pid and child not in seen:
                seen.add(child)
                queue.append(child)

    try:
        tick_ns = 1_000_000_000 // os.sysconf("SC_CLK_TCK")
    except (ValueError, OSError):
        tick_ns = 10_000_000

    members: dict[tuple[int, str], int] = {}
    total = 0
    changed = False
    for pid, fields in snapshots:
        member_id = (pid, fields[19])
        pid_dir = proc_root / str(pid)
        task_dir = pid_dir / "task"
        try:
            tasks = list(task_dir.iterdir())
        except OSError as exc:
            if _is_proc_gone(exc):
                if pid_dir.exists():
                    return {}, 0, False, str(exc)
                changed = True
                continue
            return {}, 0, False, str(exc)
        ns = 0
        for task in tasks:
            try:
                data = (task / "schedstat").read_text(encoding="utf-8", errors="replace")
            except OSError as exc:
                if _is_proc_gone(exc):
                    if task.exists():
                        return {}, 0, False, str(exc)
                    changed = True
                    continue
                return {}, 0, False, str(exc)
            values = data.split()
            if not values:
                return {}, 0, False, f"empty schedstat for {pid}"
            try:
                ns += int(values[0])
            except ValueError as exc:
                return {}, 0, False, str(exc)
        try:
            after_raw = (pid_dir / "stat").read_text(encoding="utf-8", errors="replace")
        except OSError as exc:
            if _is_proc_gone(exc):
                changed = True
                continue
            return {}, 0, False, str(exc)
        latest = _parse_proc_stat_fields(after_raw)
        if latest is None:
            return {}, 0, False, f"malformed process stat for {pid}"
        if latest[19] != fields[19] or latest[2] != fields[2]:
            changed = True
            continue
        try:
            cutime = int(fields[13])
            cstime = int(fields[14])
        except ValueError as exc:
            return {}, 0, False, str(exc)
        if cutime < 0 or cstime < 0:
            return {}, 0, False, f"invalid child CPU for {pid}"
        children_ns = (cutime + cstime) * tick_ns
        members[member_id] = ns
        total += ns + children_ns
    return members, total, changed, ""


def _signal_if_same_process(proc_root: Path, pid: int, expected_start: str, sig: int) -> bool:
    try:
        raw = (proc_root / str(pid) / "stat").read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    fields = _parse_proc_stat_fields(raw)
    if fields is None or fields[0] == "Z" or fields[19] != expected_start:
        return False
    try:
        os.kill(pid, sig)
        return True
    except (ProcessLookupError, PermissionError):
        return False


def _step_go_test_watchdog(
    go_pid: int,
    silent_threshold: int,
    watchers: dict[tuple[int, str], _BinaryStallWatchdog],
    signalled: set[tuple[int, str]],
    *,
    proc_root: Path = Path("/proc"),
    on_signal=None,
) -> None:
    scan = _scan_proc_stats(proc_root)
    if scan is None:
        return
    by_pid, children_of, by_pgrp = scan
    go_fields = by_pid.get(go_pid)
    if go_fields is None or go_fields[0] == "Z":
        return

    live_children: set[tuple[int, str]] = set()
    for pid in children_of.get(go_pid, ()):
        fields = by_pid.get(pid)
        if fields is None or fields[0] == "Z":
            continue
        identity = (pid, fields[19])
        live_children.add(identity)
        if identity not in watchers and identity not in signalled:
            if _is_go_test_binary(proc_root, pid):
                watchers[identity] = _BinaryStallWatchdog(silent_threshold)

    for identity in list(watchers):
        if identity not in live_children:
            watchers.pop(identity, None)

    for identity, watcher in list(watchers.items()):
        if identity in signalled:
            continue
        pid, start_time = identity
        members, total_ns, changed, err = _sample_process_tree_cpu(
            proc_root, pid, by_pid, children_of, by_pgrp
        )
        if watcher.observe_tree(members, total_ns, changed, err):
            signalled.add(identity)
            watchers.pop(identity, None)
            if on_signal is not None:
                on_signal(pid)
            else:
                _signal_if_same_process(proc_root, pid, start_time, signal.SIGQUIT)


def _watch_go_test_binaries(
    go_pid: int,
    sample_interval: float,
    silent_threshold: int,
    stop_event: threading.Event,
    *,
    proc_root: Path = Path("/proc"),
    on_signal=None,
) -> None:
    watchers: dict[tuple[int, str], _BinaryStallWatchdog] = {}
    signalled: set[tuple[int, str]] = set()
    next_tick = time.monotonic()
    while not stop_event.is_set():
        _step_go_test_watchdog(
            go_pid,
            silent_threshold,
            watchers,
            signalled,
            proc_root=proc_root,
            on_signal=on_signal,
        )
        next_tick += sample_interval
        wait = next_tick - time.monotonic()
        if wait <= 0:
            next_tick = time.monotonic()
            continue
        if stop_event.wait(wait):
            return


def _invocation_guard_main(lifeline_fd: int, command: list[str]) -> int:
    """Run command as a Linux subreaper and kill/reap every descendant on exit."""
    _set_child_subreaper()
    me = os.getpid()
    cleanup_lock = threading.Lock()
    stop_watcher = threading.Event()

    def cleanup_all() -> None:
        with cleanup_lock:
            _kill_and_reap_descendants(me)

    def watch_lifeline() -> None:
        try:
            while os.read(lifeline_fd, 4096):
                pass
        except OSError:
            pass
        cleanup_all()
        os._exit(1)

    def on_signal(signum: int, _frame) -> None:
        cleanup_all()
        os._exit(128 + signum)

    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, on_signal)

    threading.Thread(target=watch_lifeline, daemon=True).start()
    child = subprocess.Popen(command)
    watchdog_spec = _load_watchdog_constants()
    if watchdog_spec is not None and sys.platform.startswith("linux"):
        interval_s, silent_samples = watchdog_spec
        threading.Thread(
            target=_watch_go_test_binaries,
            args=(child.pid, interval_s, silent_samples, stop_watcher),
            daemon=True,
        ).start()
    code = 0
    try:
        # As a Linux subreaper, wait on any child (-1) rather than child.pid
        # alone so reparented grandchildren are reaped as they exit instead of
        # accumulating thousands of zombie entries in /proc across long suites.
        while True:
            try:
                wpid, status = os.waitpid(-1, 0)
            except InterruptedError:
                continue
            except ChildProcessError:
                polled = child.poll()
                code = 0 if polled is None else polled
                break
            if wpid == child.pid:
                code = os.waitstatus_to_exitcode(status)
                child.returncode = code
                break
    finally:
        stop_watcher.set()
        cleanup_all()
    return 128 - code if code < 0 else code


def _stop_live_guards() -> None:
    with _live_guards_lock:
        items = list(_live_guards.items())
    for pid, _fd in items:
        with contextlib.suppress(ProcessLookupError, PermissionError):
            os.kill(pid, signal.SIGTERM)


def _end_with_runner(signum: int, _frame) -> None:
    _stop_live_guards()
    raise SystemExit(128 + signum)


def _install_signal_handlers() -> None:
    for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
        signal.signal(sig, _end_with_runner)


def _clear_directory_contents(path: Path) -> None:
    if not path.is_dir():
        return
    for child in list(path.iterdir()):
        with contextlib.suppress(OSError):
            if child.is_dir() and not child.is_symlink():
                shutil.rmtree(child, ignore_errors=True)
            else:
                child.unlink(missing_ok=True)


@contextlib.contextmanager
def acquire_invocation_scratch(durable: str | None, scratch_key: str):
    """Acquire an exclusive per-invocation scratch directory without losing cache hits.

    Each package/shard key owns a locked pool of numbered slots (`slot-0`, `slot-1`, ...).
    Non-concurrent runs always acquire `slot-0` (stable path -> Go test cache hits);
    concurrent runs acquire distinct slots (`slot-0`, `slot-1`, ...) so they never share
    a scratch directory.
    """
    if not durable:
        with tempfile.TemporaryDirectory(prefix="go-affected-scratch-") as tmp:
            p = Path(tmp)
            yield p, p
        return
    slug = hashlib.sha256(scratch_key.encode("utf-8")).hexdigest()[:12]
    pool_dir = Path(durable) / ".go-affected-scratch" / slug
    pool_dir.mkdir(parents=True, exist_ok=True)
    slot = 0
    fd = None
    while True:
        lock_path = pool_dir / f"slot-{slot}.lock"
        cand = os.open(lock_path, os.O_CREAT | os.O_RDWR, 0o666)
        try:
            fcntl.flock(cand, fcntl.LOCK_EX | fcntl.LOCK_NB)
            fd = cand
            break
        except BlockingIOError:
            os.close(cand)
            slot += 1
        except BaseException:
            os.close(cand)
            raise
    scratch_dir = pool_dir / f"slot-{slot}"
    try:
        scratch_dir.mkdir(parents=True, exist_ok=True)
        _clear_directory_contents(scratch_dir)
        yield scratch_dir, pool_dir
    finally:
        _clear_directory_contents(scratch_dir)
        try:
            fcntl.flock(fd, fcntl.LOCK_UN)
        finally:
            os.close(fd)


def _shared_cache_dir(env: dict[str, str] | None = None) -> Path:
    source = os.environ if env is None else env
    gocache = source.get("GOCACHE", "").strip()
    if gocache and gocache != "off":
        return Path(gocache).resolve()
    if sys.platform == "darwin":
        return (Path.home() / "Library" / "Caches" / "go-build").resolve()
    xdg = source.get("XDG_CACHE_HOME", "").strip()
    base = Path(xdg) if xdg else Path.home() / ".cache"
    return (base / "go-build").resolve()


def _is_git_shim(path: Path) -> bool:
    if path.parent.parts[-3:] == (".yolo", "bin", "block"):
        return True
    try:
        with path.open("rb") as handle:
            head = handle.read(256)
    except OSError:
        return False
    return head.startswith(b"#!") and (
        b"_yg_tool=git" in head or b"# tillr-git-guard" in head
    )


def _is_tillr_git_guard(path: Path) -> bool:
    try:
        with path.open("rb") as handle:
            return handle.read(64).startswith(b"#!/bin/sh\n# tillr-git-guard")
    except OSError:
        return False


def _resolve_real_git(path_value: str | None = None) -> tuple[Path | None, bool]:
    raw_path = os.environ.get("PATH", "") if path_value is None else path_value
    saw_shim = False
    for entry in raw_path.split(os.pathsep):
        if not entry:
            continue
        candidate = Path(entry) / "git"
        if not (candidate.is_file() and os.access(candidate, os.X_OK)):
            continue
        if _is_git_shim(candidate):
            saw_shim = True
            continue
        return candidate, saw_shim
    if saw_shim:
        for fallback in (Path("/bin/git"), Path("/usr/bin/git")):
            if fallback.is_file() and os.access(fallback, os.X_OK) and not _is_git_shim(fallback):
                return fallback, True
    return None, saw_shim


def _ensure_git_bin_dir(base_dir: Path, real_git: Path) -> Path:
    digest = hashlib.sha256(str(real_git).encode("utf-8")).hexdigest()[:12]
    bin_dir = base_dir / "go-test-git-bin" / digest
    link = bin_dir / "git"
    try:
        if link.is_symlink() and os.readlink(link) == str(real_git):
            return bin_dir
    except OSError:
        pass
    bin_dir.mkdir(parents=True, exist_ok=True)
    tmp_link = bin_dir / f".git.{os.getpid()}.{threading.get_ident()}"
    with contextlib.suppress(OSError):
        tmp_link.unlink()
    tmp_link.symlink_to(real_git)
    try:
        os.replace(tmp_link, link)
    except OSError:
        with contextlib.suppress(OSError):
            tmp_link.unlink()
        if not (link.is_symlink() and os.readlink(link) == str(real_git)):
            raise
    return bin_dir


def _bypass_git_shim_env(
    base_env: dict[str, str], *candidate_roots: Path | None
) -> dict[str, str]:
    env = dict(base_env)
    path_val = env.get("PATH", "")
    real_git, saw_shim = _resolve_real_git(path_val)
    if not saw_shim:
        return env
    kept = [
        entry
        for entry in path_val.split(os.pathsep)
        if not (entry and _is_tillr_git_guard(Path(entry) / "git"))
    ]
    cleaned_path = os.pathsep.join(kept)
    real_after, still_shimmed = _resolve_real_git(cleaned_path)
    if not still_shimmed and real_after is not None:
        env["PATH"] = cleaned_path
        return env
    if real_git is not None:
        for root_dir in candidate_roots:
            if root_dir is None:
                continue
            try:
                git_bin = _ensure_git_bin_dir(root_dir, real_git)
                env["PATH"] = f"{git_bin}{os.pathsep}{cleaned_path}" if cleaned_path else str(git_bin)
                return env
            except OSError:
                continue
    env["PATH"] = cleaned_path
    env["YOLO_BYPASS_SHIMS"] = "1"
    return env


def _global_git_config(env: dict[str, str] | None = None) -> list[tuple[str, str]]:
    git_env = _bypass_git_shim_env(
        os.environ if env is None else env,
        _shared_cache_dir(env),
    )
    raw = subprocess.check_output(
        ["git", "config", "--list", "--includes", "--show-scope", "--null"],
        text=True,
        env=git_env,
    )
    values: list[tuple[str, str]] = []
    fields = raw.rstrip("\0").split("\0") if raw else []
    for scope, entry in zip(fields[::2], fields[1::2]):
        if scope != "global":
            continue
        key, separator, value = entry.partition("\n")
        if key.lower() == "include.path" or key.lower().startswith("includeif."):
            continue
        values.append((key, value if separator else "true"))
    return values


def _write_private_gitconfig(
    scratch_dir: Path,
    git_values: list[tuple[str, str]],
    *,
    env: dict[str, str] | None = None,
) -> Path:
    config = scratch_dir / "gitconfig"
    config.write_text("", encoding="utf-8")
    git_env = _bypass_git_shim_env(
        os.environ if env is None else env,
        _shared_cache_dir(env),
        scratch_dir,
    )
    for key, value in git_values:
        if key.lower() in ("maintenance.auto", "gc.auto"):
            continue
        subprocess.run(
            ["git", "config", "--file", str(config), "--add", key, value],
            check=True,
            capture_output=True,
            text=True,
            env=git_env,
        )
    with config.open("a", encoding="utf-8") as handle:
        handle.write("[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n")
    return config


def _run_guarded_command(
    cmd: list[str],
    *,
    cwd: Path,
    env: dict[str, str],
    out_dir: Path | None = None,
    on_line=None,
) -> tuple[int, str]:
    """Run cmd under the subreaper guard, streaming stdout lines while it runs."""
    script = str(Path(__file__).resolve())
    lifeline_read, lifeline_write = os.pipe()
    proc = None
    chunks: list[str] = []
    try:
        proc = subprocess.Popen(
            [sys.executable, script, INVOCATION_GUARD_FLAG, str(lifeline_read), "--", *cmd],
            cwd=cwd,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            start_new_session=True,
            pass_fds=(lifeline_read,),
        )
    except BaseException:
        os.close(lifeline_write)
        raise
    finally:
        os.close(lifeline_read)
    with _live_guards_lock:
        _live_guards[proc.pid] = lifeline_write
    try:
        assert proc.stdout is not None
        try:
            for raw_line in proc.stdout:
                line = raw_line.decode("utf-8", errors="replace")
                chunks.append(line)
                if on_line is not None:
                    on_line(line)
        finally:
            proc.stdout.close()
        rc = proc.wait()
    finally:
        with _live_guards_lock:
            _live_guards.pop(proc.pid, None)
        with contextlib.suppress(OSError):
            os.close(lifeline_write)
        if proc.poll() is None:
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.kill(proc.pid, signal.SIGTERM)
            proc.wait()
    return rc, "".join(chunks)


def _format_evidence_package(
    item: dict, *, elapsed_ms: int = 0, cached: bool = False, reused: bool = False
) -> dict:
    rec = {
        "import_path": item["import_path"],
        "module": item["module"],
        "reason": item["reason"],
        "elapsed_ms": elapsed_ms,
        "cached": cached,
    }
    if reused:
        rec["reused"] = True
    if item.get("selected_tests") is not None:
        rec["tests"] = list(item["selected_tests"])
    return rec


def run_selected_tests(
    root: Path,
    selection: dict,
    *,
    ev_path: str = "",
    progress_stream=None,
    graph: dict | None = None,
    verdicts_in: str = "",
    verdicts_out: str = "",
) -> tuple[int, str, dict]:
    root = root.resolve()
    selected = selection["selected"]
    initial_evidence = {
        "mode": "go-affected",
        "selection_ms": selection["selection_ms"],
        "slot_wait_ms": 0,
        "packages": [_format_evidence_package(item) for item in selected],
    }
    write_evidence_atomic(ev_path, initial_evidence)
    if not selected:
        if verdicts_out:
            save_package_verdicts_atomic(Path(verdicts_out), {"schema": 1, "packages": {}})
        return 0, "go-affected: no affected tillr Go test packages\n", initial_evidence

    shards_mod = _load_shards_module(root)
    durations = _load_durations(root)
    edges = load_edges(root)
    fallback_graph: dict | None = graph
    if (verdicts_in or verdicts_out) and fallback_graph is None:
        fallback_graph = load_tillr_graph(root)
    prior_verdicts = load_package_verdicts_file(Path(verdicts_in)) if verdicts_in else None
    digest_cache: dict = {}
    reused_ips: set[str] = set()
    active_selected: list[dict] = []
    for item in selected:
        if (
            prior_verdicts is not None
            and fallback_graph is not None
            and package_verdict_reusable(
                root,
                item["import_path"],
                fallback_graph,
                edges,
                prior_verdicts,
                selected_tests=item.get("selected_tests"),
                cache=digest_cache,
            )
        ):
            reused_ips.add(item["import_path"])
        else:
            active_selected.append(item)

    def invocation_digest(item: dict, active_tests: list[str] | None) -> str | None:
        if not item.get("has_edge"):
            return None
        edge = edges.get(item["import_path"])
        if edge is None:
            return item.get("edge_digest")
        resolved = item.get("_resolved_builds")
        if resolved is None:
            if edge.get("builds"):
                nonlocal fallback_graph
                if fallback_graph is None:
                    fallback_graph = load_tillr_graph(root)
                resolved = _resolve_edge_builds(root, edge, fallback_graph["pkg_info"])
            else:
                resolved = {}
        return compute_edge_digest(
            root, edge, {}, active_tests=active_tests, resolved_builds=resolved
        )

    base_env = build_env()
    git_values: list[tuple[str, str]] = []
    with contextlib.suppress(OSError, subprocess.CalledProcessError):
        git_values = _global_git_config(base_env)
    durable = base_env.get("YOLO_DURABLE_DIR") or base_env.get("TMPDIR")

    # Group small non-edge packages by module; narrowed, edge, or large packages run with their own invocations.
    # Each invocation is (module, items, digest, run_regex_or_None, scratch_key).
    invocations: list[tuple[str, list[dict], str | None, str | None, str]] = []
    by_mod: dict[str, list[dict]] = {}
    for item in active_selected:
        tests = scan_package_tests(root, item)
        has_edge_test = "TestEdgeInputs" in tests
        narrowed = item.get("selected_tests")
        if narrowed is not None:
            reader_tests = [t for t in narrowed if t != "TestEdgeInputs"]
            pkg_durations = durations.get(item["import_path"], {})
            subset_durations = {t: float(pkg_durations.get(t, 1.0)) for t in reader_tests}
            est_total = sum(subset_durations.values())
            subset_sig = hashlib.sha256("\0".join(reader_tests).encode("utf-8")).hexdigest()[:12]
            if len(reader_tests) >= 2 and est_total >= NARROWED_SHARD_DURATION_THRESHOLD:
                shard_count = min(
                    MAX_SHARDS,
                    max(2, math.ceil(est_total / NARROWED_SHARD_DURATION_THRESHOLD)),
                )
                if shards_mod is not None and hasattr(shards_mod, "assign"):
                    buckets = shards_mod.assign(reader_tests, subset_durations, shard_count)
                else:
                    buckets = [[] for _ in range(shard_count)]
                    for idx, name in enumerate(reader_tests):
                        buckets[idx % shard_count].append(name)
                for shard_idx, bucket in enumerate(buckets):
                    if not bucket:
                        continue
                    shard_names = list(bucket)
                    if has_edge_test:
                        shard_names.append("TestEdgeInputs")
                    pattern = "^(" + "|".join(re.escape(t) for t in shard_names) + ")$"
                    bucket_sig = hashlib.sha256("\0".join(bucket).encode("utf-8")).hexdigest()[:12]
                    invocations.append((
                        item["module"],
                        [item],
                        invocation_digest(item, bucket),
                        pattern,
                        f"{item['import_path']}#tests-{subset_sig}-shard-{shard_idx}-{bucket_sig}",
                    ))
            else:
                run_names = list(reader_tests)
                if has_edge_test:
                    run_names.append("TestEdgeInputs")
                pattern = "^(" + "|".join(re.escape(t) for t in run_names) + ")$"
                invocations.append((
                    item["module"],
                    [item],
                    invocation_digest(item, reader_tests),
                    pattern,
                    f"{item['import_path']}#tests-{subset_sig}",
                ))
        elif len(tests) >= SHARD_THRESHOLD:
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
                    invocation_digest(item, bucket),
                    pattern,
                    f"{item['import_path']}#shard-{shard_idx}",
                ))
        elif item["import_path"] != "./...":
            invocations.append((item["module"], [item], item["edge_digest"], None, item["import_path"]))
        else:
            by_mod.setdefault(item["module"], []).append(item)
    for mod in sorted(by_mod):
        invocations.append((mod, by_mod[mod], None, None, f"module:{mod}"))

    outputs: list[str] = []
    pkg_shards: dict[str, list[dict]] = {}
    pkg_sigquit_tests: dict[str, list[str]] = {}
    pkg_failed_tests: dict[str, list[str]] = {}
    failed_pkgs_order: list[str] = []
    failed_pkgs_set: set[str] = set()
    all_sigquit_cases: list[dict] = []
    all_fail_cases: list[dict] = []
    pkg_locations: dict[str, tuple[str, str]] = {}
    for item in selected:
        ip = item["import_path"]
        mod = item.get("module") or ""
        if ip == "./..." or not mod:
            continue
        pkg_rel: str | None = None
        dir_rel = item.get("dir_rel")
        if isinstance(dir_rel, str) and dir_rel:
            rel = os.path.relpath(dir_rel, mod).replace(os.sep, "/")
            pkg_rel = "." if rel == "." else f"./{rel}"
        else:
            raw_dir = item.get("dir")
            if isinstance(raw_dir, str) and raw_dir:
                try:
                    rel = Path(raw_dir).resolve().relative_to((root / mod).resolve()).as_posix()
                    pkg_rel = "." if rel == "." else f"./{rel}"
                except ValueError:
                    pkg_rel = None
        if pkg_rel is not None:
            pkg_locations[ip] = (mod, pkg_rel)
    max_slot_wait_ms = 0
    rc_all = 0
    stream = sys.stdout if progress_stream is None else progress_stream
    stream_lock = threading.Lock()

    def emit_progress(line: str) -> None:
        progress = format_progress_event(line)
        if progress is None or stream is False:
            return
        with stream_lock:
            stream.write(progress)
            stream.flush()

    def record_failed_pkg(pkg_name: str, tests: list[str], *, sigquit_tests: list[str] | None = None) -> None:
        if pkg_name not in failed_pkgs_set:
            failed_pkgs_set.add(pkg_name)
            failed_pkgs_order.append(pkg_name)
        if sigquit_tests:
            sq_bucket = pkg_sigquit_tests.setdefault(pkg_name, [])
            for name in sigquit_tests:
                if name not in sq_bucket:
                    sq_bucket.append(name)
        bucket = pkg_failed_tests.setdefault(pkg_name, [])
        for name in tests:
            if name not in bucket:
                bucket.append(name)

    def run_one(
        inv: tuple[str, list[dict], str | None, str | None, str],
    ) -> tuple[int, str, int, dict[str, dict], list[dict], list[dict]]:
        mod, items, digest, run_pattern, scratch_key = inv
        cmd = ["go", "test", "-json", "-timeout", "0", "-p", "1", "-parallel", "1"]
        if run_pattern is not None:
            cmd.extend(["-run", run_pattern])
        cmd.extend(item["import_path"] for item in items)

        wait_ms = 0
        with acquire_invocation_scratch(durable, scratch_key) as (scratch_dir, out_dir):
            env = _bypass_git_shim_env(base_env, scratch_dir)
            env["GOMAXPROCS"] = "1"
            gotmp_dir = Path(os.path.abspath(scratch_dir / "gotmp"))
            gotmp_dir.mkdir(parents=True, exist_ok=True)
            env["GOTMPDIR"] = str(gotmp_dir)
            env["YOLO_DURABLE_DIR"] = str(scratch_dir)
            with contextlib.suppress(OSError, subprocess.CalledProcessError):
                gitconfig = _write_private_gitconfig(scratch_dir, git_values, env=env)
                env["GIT_CONFIG_GLOBAL"] = str(gitconfig)
            if digest is not None:
                env["TILLR_GATE_EDGE_INPUTS"] = digest
            else:
                env.pop("TILLR_GATE_EDGE_INPUTS", None)

            rc, stdout = _run_guarded_command(
                cmd, cwd=root / mod, env=env, out_dir=out_dir, on_line=emit_progress
            )
        parsed, readable = parse_go_test_json(stdout)
        sigquit_cases = extract_sigquit_cases(stdout, known_packages=set(parsed))
        fail_cases = _extract_fail_cases(readable, parsed)
        return rc, readable, wait_ms, parsed, sigquit_cases, fail_cases

    if invocations:
        with ThreadPoolExecutor(max_workers=min(16, len(invocations))) as pool:
            for inv, (rc, readable_out, wait_ms, parsed, sigquit_cases, fail_cases) in zip(
                invocations, pool.map(run_one, invocations)
            ):
                mod, items, _digest, _run_pattern, _scratch_key = inv
                if wait_ms > max_slot_wait_ms:
                    max_slot_wait_ms = wait_ms
                inv_recorded_failure = False
                inv_sigquit_by_pkg: dict[str, list[str]] = {}
                for case in sigquit_cases:
                    inv_sigquit_by_pkg.setdefault(case["package"], []).append(case["name"])
                for pkg_name, rec in parsed.items():
                    pkg_shards.setdefault(pkg_name, []).append(rec)
                    failed_tests = rec.get("failed_tests") or []
                    if rec.get("passed") is False or failed_tests:
                        record_failed_pkg(
                            pkg_name,
                            failed_tests,
                            sigquit_tests=inv_sigquit_by_pkg.get(pkg_name),
                        )
                        inv_recorded_failure = True
                if rc != 0:
                    rc_all = rc
                    outputs.append(readable_out)
                    all_sigquit_cases.extend(sigquit_cases)
                    all_fail_cases.extend(fail_cases)
                    if not inv_recorded_failure:
                        for item in items:
                            pkg_label = mod if item["import_path"] == "./..." else item["import_path"]
                            record_failed_pkg(pkg_label, [])

    packages_evidence: list[dict] = []
    for item in selected:
        ip = item["import_path"]
        if ip in reused_ips:
            packages_evidence.append(
                _format_evidence_package(item, elapsed_ms=0, cached=True, reused=True)
            )
            continue
        shards_list = pkg_shards.get(ip, [])
        packages_evidence.append(
            _format_evidence_package(
                item,
                elapsed_ms=max((s.get("elapsed_ms", 0) for s in shards_list), default=0),
                cached=bool(shards_list) and all(bool(s.get("cached", False)) for s in shards_list),
            )
        )

    if verdicts_out and fallback_graph is not None:
        out_verdicts: dict = {"schema": 1, "packages": {}}
        for item in selected:
            ip = item["import_path"]
            if ip == "./..." or ip in failed_pkgs_set:
                continue
            if ip not in reused_ips:
                shards_list = pkg_shards.get(ip, [])
                if not shards_list or not all(
                    s.get("passed") is True and not s.get("failed_tests") for s in shards_list
                ):
                    continue
            record_package_verdict(
                root,
                ip,
                fallback_graph,
                edges,
                out_verdicts,
                selected_tests=item.get("selected_tests"),
                cache=digest_cache,
            )
        save_package_verdicts_atomic(Path(verdicts_out), out_verdicts)

    evidence = {
        "mode": "go-affected",
        "selection_ms": selection["selection_ms"],
        "slot_wait_ms": max_slot_wait_ms,
        "packages": packages_evidence,
    }
    write_evidence_atomic(ev_path, evidence)
    reused_note = f", {len(reused_ips)} reused" if reused_ips else ""
    summary = (
        f"go-affected: {len(selected)} package(s) "
        f"({sum(1 for p in packages_evidence if p['cached'])} cached{reused_note}, "
        f"selection {selection['selection_ms']}ms, slot_wait {max_slot_wait_ms}ms)\n"
    )
    triage_block = ""
    failure_summary = ""
    if rc_all != 0:
        ordered_pkg_tests: list[tuple[str, list[str]]] = []
        for pkg_name in failed_pkgs_order:
            combined = list(dict.fromkeys(
                pkg_sigquit_tests.get(pkg_name, []) + pkg_failed_tests.get(pkg_name, [])
            ))
            ordered_pkg_tests.append((pkg_name, combined))

        triage_cases: list[dict] = []
        seen_cases: set[tuple[str, str]] = set()
        for case in all_sigquit_cases:
            key = (case["package"], case["name"])
            if key not in seen_cases:
                seen_cases.add(key)
                triage_cases.append(case)
        for case in all_fail_cases:
            key = (case["package"], case["name"])
            if key not in seen_cases:
                seen_cases.add(key)
                triage_cases.append(case)
        for pkg_name, tests in ordered_pkg_tests:
            for name in tests:
                key = (pkg_name, name)
                if key not in seen_cases:
                    seen_cases.add(key)
                    triage_cases.append({
                        "package": pkg_name,
                        "name": name,
                        "where": pkg_name,
                        "assertion": "",
                        "sigquit": False,
                    })
        triage_block = format_triage_block(triage_cases, pkg_locations)
        failure_summary = format_failure_summary(ordered_pkg_tests)

    body = "".join(outputs)
    if body and triage_block and not body.endswith("\n"):
        body += "\n"
    return rc_all, summary + body + triage_block + failure_summary, evidence


def git_changed_paths(root: Path, base: str, head: str) -> list[str]:
    out = subprocess.check_output(
        ["git", "-C", str(root), "diff", "--name-only", "--no-renames", "-z", base, head, "--"],
    )
    return [os.fsdecode(p) for p in out.split(b"\0") if p]


def main(argv: list[str] | None = None) -> int:
    raw_argv = sys.argv[1:] if argv is None else argv
    if raw_argv[:1] == [INVOCATION_GUARD_FLAG]:
        return _invocation_guard_main(int(raw_argv[1]), raw_argv[3:])
    _install_signal_handlers()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".", help="Repository root")
    parser.add_argument("--base", help="Base commit")
    parser.add_argument("--head", default="HEAD", help="Head commit")
    parser.add_argument("--changed", nargs="*", help="Explicit changed paths (relative to root)")
    parser.add_argument("--drop-module", action="append", default=[], help="Module to drop from selection")
    parser.add_argument("--select-only", action="store_true", help="Print selection JSON and exit 0")
    parser.add_argument("--evidence-out", help="Write evidence JSON to this path")
    parser.add_argument("--verdicts-in", help="Read prior passing package verdicts JSON from this path")
    parser.add_argument("--verdicts-out", help="Write passing package verdicts JSON to this path")
    args = parser.parse_args(argv)

    root = Path(args.root).resolve()
    if args.changed is not None:
        changed = args.changed
    elif args.base:
        changed = git_changed_paths(root, args.base, args.head)
    else:
        parser.error("either --base or --changed is required")

    verdicts_in = args.verdicts_in or os.environ.get(GO_VERDICTS_IN_ENV, "")
    verdicts_out = args.verdicts_out or os.environ.get(GO_VERDICTS_OUT_ENV, "")
    graph = load_tillr_graph(root) if (verdicts_in or verdicts_out) and not args.select_only else None
    selection = select_affected_packages(
        root, changed, drop_modules=set(args.drop_module), graph=graph
    )
    if args.select_only:
        printable = {
            **selection,
            "selected": [
                {k: v for k, v in item.items() if not k.startswith("_")}
                for item in selection["selected"]
            ],
        }
        print(json.dumps(printable, indent=2, sort_keys=True))
        return 0

    ev_path = args.evidence_out or os.environ.get("TILLR_GO_AFFECTED_EVIDENCE_OUT", "")
    rc, output, _evidence = run_selected_tests(
        root,
        selection,
        ev_path=ev_path,
        graph=graph,
        verdicts_in=verdicts_in,
        verdicts_out=verdicts_out,
    )
    sys.stdout.write(output)
    sys.stdout.flush()
    return rc


if __name__ == "__main__":
    sys.exit(main())
