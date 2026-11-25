#!/usr/bin/env python3
"""Prototype 2: Restricted filesystem view blocking undeclared test inputs.

Probes Linux Landlock LSM, unshare -rm, bwrap, and strace in this container.
Under Landlock, allowlists only system runtime paths, Go toolchain/cache,
scratch TMPDIR, the target package's module, and declared go-test-edges.json
inputs, then runs real Go package tests and reports which fail and why.
"""

from __future__ import annotations

import argparse
import ctypes
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any

# Linux x86_64 / aarch64 syscall numbers for Landlock
SYS_LANDLOCK_CREATE_RULESET = 444
SYS_LANDLOCK_ADD_RULE = 445
SYS_LANDLOCK_RESTRICT_SELF = 446
PR_SET_NO_NEW_PRIVS = 38

LANDLOCK_CREATE_RULESET_VERSION = 1 << 0
LANDLOCK_RULE_PATH_BENEATH = 1

# Landlock FS access rights (ABI v1..v3)
FS_EXECUTE = 1 << 0
FS_WRITE_FILE = 1 << 1
FS_READ_FILE = 1 << 2
FS_READ_DIR = 1 << 3
FS_REMOVE_DIR = 1 << 4
FS_REMOVE_FILE = 1 << 5
FS_MAKE_CHAR = 1 << 6
FS_MAKE_DIR = 1 << 7
FS_MAKE_REG = 1 << 8
FS_MAKE_SOCK = 1 << 9
FS_MAKE_FIFO = 1 << 10
FS_MAKE_BLOCK = 1 << 11
FS_MAKE_SYM = 1 << 12
FS_REFER = 1 << 13
FS_TRUNCATE = 1 << 14

DIR_ONLY_FLAGS = (
    FS_READ_DIR
    | FS_REMOVE_DIR
    | FS_REMOVE_FILE
    | FS_MAKE_CHAR
    | FS_MAKE_DIR
    | FS_MAKE_REG
    | FS_MAKE_SOCK
    | FS_MAKE_FIFO
    | FS_MAKE_BLOCK
    | FS_MAKE_SYM
)

READ_EXEC_DIR = FS_READ_FILE | FS_READ_DIR | FS_EXECUTE
READ_EXEC_FILE = FS_READ_FILE | FS_EXECUTE
RW_DIR = (
    FS_EXECUTE
    | FS_WRITE_FILE
    | FS_READ_FILE
    | FS_READ_DIR
    | FS_REMOVE_DIR
    | FS_REMOVE_FILE
    | FS_MAKE_DIR
    | FS_MAKE_REG
    | FS_MAKE_SYM
    | FS_TRUNCATE
)
RW_FILE = FS_EXECUTE | FS_WRITE_FILE | FS_READ_FILE | FS_TRUNCATE


class _RulesetAttr(ctypes.Structure):
    _fields_ = [("handled_access_fs", ctypes.c_uint64)]


class _PathBeneathAttr(ctypes.Structure):
    _layout_ = "ms"
    _pack_ = 1
    _fields_ = [
        ("allowed_access", ctypes.c_uint64),
        ("parent_fd", ctypes.c_int32),
    ]


def _libc() -> ctypes.CDLL:
    return ctypes.CDLL(None, use_errno=True)


def probe_landlock() -> dict[str, Any]:
    libc = _libc()
    ver = libc.syscall(SYS_LANDLOCK_CREATE_RULESET, None, 0, LANDLOCK_CREATE_RULESET_VERSION)
    err = ctypes.get_errno()
    if ver < 0:
        return {
            "available": False,
            "abi_version": 0,
            "errno": err,
            "error": os.strerror(err) if err else "unknown",
        }
    return {"available": True, "abi_version": int(ver), "errno": 0, "error": ""}


def _supported_fs_mask(abi_version: int) -> int:
    mask = (1 << 13) - 1  # ABI v1: bits 0..12
    if abi_version >= 3:
        mask |= FS_TRUNCATE
    return mask


def probe_mechanisms() -> dict[str, Any]:
    """Probe all restriction / audit mechanisms in this container."""
    results: dict[str, Any] = {"landlock": probe_landlock()}

    for name, cmd in (
        ("unshare_rm", ["unshare", "-rm", "true"]),
        ("bwrap", ["bwrap", "--ro-bind", "/", "/", "true"]),
        ("strace", ["strace", "-f", "-e", "trace=file", "true"]),
    ):
        exe = shutil.which(cmd[0])
        if not exe:
            results[name] = {
                "available": False,
                "binary": None,
                "error": f"{cmd[0]} not found in PATH",
            }
            continue
        proc = subprocess.run(cmd, capture_output=True, text=True)
        results[name] = {
            "available": proc.returncode == 0,
            "binary": exe,
            "returncode": proc.returncode,
            "stderr": proc.stderr.strip()[:400],
        }
    return results


def apply_landlock_rules(ro_paths: list[Path], rw_paths: list[Path]) -> None:
    """Apply Landlock filesystem restrictions to the current process."""
    info = probe_landlock()
    if not info["available"]:
        raise OSError(info["errno"], f"Landlock unavailable: {info['error']}")
    supported = _supported_fs_mask(info["abi_version"])
    libc = _libc()

    if libc.prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0:
        err = ctypes.get_errno()
        raise OSError(err, f"prctl(PR_SET_NO_NEW_PRIVS) failed: {os.strerror(err)}")

    attr = _RulesetAttr(handled_access_fs=supported)
    ruleset_fd = libc.syscall(
        SYS_LANDLOCK_CREATE_RULESET,
        ctypes.byref(attr),
        ctypes.sizeof(attr),
        0,
    )
    if ruleset_fd < 0:
        err = ctypes.get_errno()
        raise OSError(err, f"landlock_create_ruleset failed: {os.strerror(err)}")

    try:
        o_path = getattr(os, "O_PATH", 0x200000) | os.O_CLOEXEC
        entries: list[tuple[Path, int, int]] = []
        for p in ro_paths:
            entries.append((p, READ_EXEC_DIR, READ_EXEC_FILE))
        for p in rw_paths:
            entries.append((p, RW_DIR, RW_FILE))

        for path, dir_flags, file_flags in entries:
            if not path.exists():
                continue
            is_dir = path.is_dir()
            access = (dir_flags if is_dir else (file_flags & ~DIR_ONLY_FLAGS)) & supported
            fd = os.open(str(path), o_path)
            try:
                rule = _PathBeneathAttr(allowed_access=access, parent_fd=fd)
                rc = libc.syscall(
                    SYS_LANDLOCK_ADD_RULE,
                    ruleset_fd,
                    LANDLOCK_RULE_PATH_BENEATH,
                    ctypes.byref(rule),
                    0,
                )
                if rc < 0:
                    err = ctypes.get_errno()
                    raise OSError(
                        err,
                        f"landlock_add_rule({path}, is_dir={is_dir}) failed: {os.strerror(err)}",
                    )
            finally:
                os.close(fd)

        rc = libc.syscall(SYS_LANDLOCK_RESTRICT_SELF, ruleset_fd, 0)
        if rc < 0:
            err = ctypes.get_errno()
            raise OSError(err, f"landlock_restrict_self failed: {os.strerror(err)}")
    finally:
        os.close(ruleset_fd)


def _go_env_paths() -> dict[str, Path]:
    proc = subprocess.run(
        ["go", "env", "GOROOT", "GOCACHE", "GOMODCACHE", "GOPATH"],
        capture_output=True,
        text=True,
        check=True,
    )
    lines = proc.stdout.splitlines()
    keys = ("GOROOT", "GOCACHE", "GOMODCACHE", "GOPATH")
    out: dict[str, Path] = {}
    for k, v in zip(keys, lines):
        if v.strip():
            out[k] = Path(v.strip())
    return out


def _resolve_package(repo_root: Path, pkg_spec: str) -> dict[str, str]:
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    import proto_content_cache as pcc

    return pcc.resolve_package(repo_root, pkg_spec)


def _declared_allowlist(repo_root: Path, import_path: str) -> list[Path]:
    """Return paths declared in tools/gate/go-test-edges.json for import_path."""
    edges_path = repo_root / "tools" / "gate" / "go-test-edges.json"
    if not edges_path.is_file():
        return []
    edges = json.loads(edges_path.read_text(encoding="utf-8"))
    edge = edges.get(import_path, {})
    allowed: list[Path] = []
    for rule in edge.get("reads", []):
        p = repo_root / rule.rstrip("/")
        if p.exists():
            allowed.append(p)
    # Map builds import paths to their module directories under tools/
    for target in edge.get("builds", []):
        # e.g. github.com/mschulkind-oss/tillr-cli -> tools/tillr-cli
        suffix = target.split("/")[-1]
        if suffix.startswith("tillr"):
            mod_dir = repo_root / "tools" / suffix
            if mod_dir.is_dir():
                allowed.append(mod_dir)
    return allowed


def run_package_sandboxed(
    repo_root: Path,
    pkg_spec: str,
    *,
    scratch_dir: Path | None = None,
    compile_first: bool = True,
) -> dict[str, Any]:
    """Run a Go package test under Landlock allowlisting only declared inputs."""
    info = _resolve_package(repo_root, pkg_spec)
    import_path = info["import_path"]
    pkg_dir = Path(info["pkg_dir"])
    module_dir = Path(info["module_dir"])
    go_paths = _go_env_paths()

    system_ro = [
        Path("/usr"),
        Path("/lib"),
        Path("/lib64"),
        Path("/bin"),
        Path("/sbin"),
        Path("/etc"),
        Path("/dev"),
        Path("/proc"),
        Path("/run"),
        Path("/sys"),
        Path("/nix/store"),
    ]
    for path_entry in os.environ.get("PATH", "").split(os.pathsep):
        if not path_entry:
            continue
        pe = Path(path_entry)
        if pe.exists() and not pe.is_relative_to(repo_root):
            system_ro.append(pe)
    declared_ro = _declared_allowlist(repo_root, import_path)
    ro_paths = system_ro + [module_dir] + declared_ro
    for k in ("GOROOT", "GOMODCACHE", "GOPATH"):
        if k in go_paths:
            ro_paths.append(go_paths[k])

    with tempfile.TemporaryDirectory(
        prefix="proto2-sbx-",
        dir=str(scratch_dir) if scratch_dir else None,
    ) as tmp:
        tmp_path = Path(tmp)
        run_tmp = tmp_path / "run-tmp"
        run_tmp.mkdir(parents=True, exist_ok=True)
        bin_path = tmp_path / "pkg.test"

        if compile_first:
            cproc = subprocess.run(
                ["go", "test", "-trimpath", "-c", "-o", str(bin_path), import_path],
                cwd=module_dir,
                capture_output=True,
                text=True,
            )
            if cproc.returncode != 0:
                return {
                    "import_path": import_path,
                    "passed": False,
                    "exit_code": cproc.returncode,
                    "failure_kind": "compile_error",
                    "output": cproc.stderr[-1500:],
                }
            if not bin_path.is_file():
                return {
                    "import_path": import_path,
                    "passed": True,
                    "has_tests": False,
                    "exit_code": 0,
                    "failure_kind": "none",
                    "output": "",
                }
            cmd = [str(bin_path)]
            ro_paths.append(tmp_path)
        else:
            cmd = ["go", "test", "-count=1", import_path]

        rw_paths = [run_tmp, Path("/dev/null")]
        if "GOCACHE" in go_paths:
            rw_paths.append(go_paths["GOCACHE"])

        env = os.environ.copy()
        env["TMPDIR"] = str(run_tmp)
        env["YOLO_DURABLE_DIR"] = str(run_tmp)

        def _preexec() -> None:
            apply_landlock_rules(ro_paths, rw_paths)

        t0 = time.monotonic()
        proc = subprocess.run(
            cmd,
            cwd=pkg_dir if compile_first else module_dir,
            env=env,
            preexec_fn=_preexec,
            capture_output=True,
            text=True,
        )
        elapsed_ms = int((time.monotonic() - t0) * 1000)
        combined = (proc.stdout or "") + (proc.stderr or "")
        eacces = "permission denied" in combined.lower() or "eacces" in combined.lower()
        return {
            "import_path": import_path,
            "passed": proc.returncode == 0,
            "has_tests": True,
            "exit_code": proc.returncode,
            "elapsed_ms": elapsed_ms,
            "eacces_detected": eacces,
            "declared_inputs": [str(p.relative_to(repo_root)) for p in declared_ro if p.is_relative_to(repo_root)],
            "output_tail": combined[-1500:] if proc.returncode != 0 else "",
        }


def verify_undeclared_vs_declared(scratch_dir: Path) -> dict[str, Any]:
    """Verify Landlock blocks undeclared reads with EACCES while allowing declared reads."""
    with tempfile.TemporaryDirectory(prefix="proto2-verify-", dir=str(scratch_dir)) as td:
        ws = Path(td)
        mod = ws / "tools" / "sample"
        mod.mkdir(parents=True)
        (mod / "go.mod").write_text("module example.com/sample\n\ngo 1.21\n", encoding="utf-8")
        (mod / "sample_test.go").write_text(
            'package sample\n\nimport (\n\t"os"\n\t"testing"\n)\n\n'
            'func TestDeclaredRead(t *testing.T) {\n'
            '\tb, err := os.ReadFile("../../declared/ok.txt")\n'
            '\tif err != nil { t.Fatal(err) }\n'
            '\tif string(b) != "ok" { t.Fatalf("got %q", string(b)) }\n'
            '}\n\n'
            'func TestUndeclaredRead(t *testing.T) {\n'
            '\t_, err := os.ReadFile("../../undeclared/secret.txt")\n'
            '\tif err != nil { t.Fatal(err) }\n'
            '}\n',
            encoding="utf-8",
        )
        (ws / "declared").mkdir()
        (ws / "declared" / "ok.txt").write_text("ok", encoding="utf-8")
        (ws / "undeclared").mkdir()
        (ws / "undeclared" / "secret.txt").write_text("hidden", encoding="utf-8")
        gate_dir = ws / "tools" / "gate"
        gate_dir.mkdir(parents=True)
        (gate_dir / "go-test-edges.json").write_text(
            json.dumps({"example.com/sample": {"builds": [], "reads": ["declared/ok.txt"]}}),
            encoding="utf-8",
        )

        # Run only TestDeclaredRead -> should pass
        bin_path = ws / "sample.test"
        subprocess.run(
            ["go", "test", "-trimpath", "-c", "-o", str(bin_path), "."],
            cwd=mod,
            check=True,
            capture_output=True,
        )
        go_paths = _go_env_paths()
        run_tmp = ws / "tmp"
        run_tmp.mkdir()
        ro_base = [
            Path("/usr"), Path("/lib"), Path("/lib64"), Path("/bin"), Path("/sbin"),
            Path("/etc"), Path("/dev"), Path("/proc"), Path("/run"), Path("/sys"),
            Path("/nix/store"),
            mod, ws / "declared" / "ok.txt", bin_path,
        ]
        for k in ("GOROOT", "GOMODCACHE", "GOPATH"):
            if k in go_paths:
                ro_base.append(go_paths[k])
        rw_base = [run_tmp, Path("/dev/null")]

        def _run_one(pattern: str) -> subprocess.CompletedProcess[str]:
            env = os.environ.copy()
            env["TMPDIR"] = str(run_tmp)
            return subprocess.run(
                [str(bin_path), f"-test.run={pattern}"],
                cwd=mod,
                env=env,
                preexec_fn=lambda: apply_landlock_rules(ro_base, rw_base),
                capture_output=True,
                text=True,
            )

        dec = _run_one("^TestDeclaredRead$")
        undec = _run_one("^TestUndeclaredRead$")
        return {
            "declared_exit": dec.returncode,
            "undeclared_exit": undec.returncode,
            "undeclared_eacces": "permission denied" in (undec.stdout + undec.stderr).lower(),
            "undeclared_output": (undec.stdout + undec.stderr).strip(),
        }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--probe", action="store_true", help="Probe available restriction mechanisms")
    parser.add_argument("--repo-root", default=".", help="Repository root")
    parser.add_argument("--pkg", action="append", default=[], help="Package to run under Landlock")
    parser.add_argument("--scratch-dir", default=None, help="Scratch directory")
    parser.add_argument(
        "--test-undeclared-read",
        action="store_true",
        help="Verify Landlock blocks undeclared read with EACCES",
    )
    parser.add_argument("--json", action="store_true", help="Output JSON")
    args = parser.parse_args(argv)

    scratch = Path(args.scratch_dir) if args.scratch_dir else None
    if scratch:
        scratch.mkdir(parents=True, exist_ok=True)

    if args.probe:
        out = probe_mechanisms()
        print(json.dumps(out, indent=2))
        return 0

    if args.test_undeclared_read:
        out = verify_undeclared_vs_declared(scratch or Path(tempfile.gettempdir()))
        print(json.dumps(out, indent=2))
        return 0 if (out["declared_exit"] == 0 and out["undeclared_exit"] != 0 and out["undeclared_eacces"]) else 1

    if not args.pkg:
        parser.error("specify --probe, --test-undeclared-read, or at least one --pkg")

    results = [
        run_package_sandboxed(Path(args.repo_root).resolve(), p, scratch_dir=scratch)
        for p in args.pkg
    ]
    if args.json:
        print(json.dumps({"results": results}, indent=2))
    else:
        for r in results:
            status = "PASS" if r["passed"] else f"FAIL(exit={r['exit_code']}, eacces={r.get('eacces_detected')})"
            print(f"{r['import_path']}: {status}")
            if not r["passed"] and r.get("output_tail"):
                print(r["output_tail"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
