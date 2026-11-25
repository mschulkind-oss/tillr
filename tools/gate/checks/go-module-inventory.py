#!/usr/bin/env python3
"""Run every per-module Go inventory check when a candidate adds or removes a tools/ go.mod."""
from __future__ import annotations

import os
from pathlib import Path
import subprocess
import sys
import tempfile


def validate(base: str, head: str) -> None:
    for label, rev in (("base", base), ("head", head)):
        if not rev:
            raise RuntimeError(f"missing {label} argument")
        check = subprocess.run(
            ["git", "rev-parse", "--verify", rev + "^{commit}"],
            capture_output=True,
            text=True,
        )
        if check.returncode != 0:
            raise RuntimeError(check.stderr.strip() or f"invalid {label} revision: {rev}")
    result = subprocess.run(["git", "merge-base", "--is-ancestor", base, head], capture_output=True)
    if result.returncode:
        raise RuntimeError("base is not an ancestor of head")

SELF_TRIGGERS = (
    "tools/gate/checks/go-module-inventory.py",
    "tools/gate/checks/go-module-inventory.json",
    "tests/tillr-testmain_test.go",
    "tests/tillr-scratch.test.py",
)

DISCOVER_MODULES_SNIPPET = """
import importlib.util
from pathlib import Path
import sys

path = Path(sys.argv[1])
spec = importlib.util.spec_from_file_location("go_affected_test", path)
if spec is None or spec.loader is None:
    raise RuntimeError(f"cannot load {path}")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
mod.test_discover_modules()
"""


def added_or_removed_go_mods(root: Path, base: str, head: str) -> list[str]:
    """Return tools/.../go.mod paths added or removed in base..head."""
    raw = subprocess.run(
        ["git", "-C", str(root), "diff", "--name-status", "--no-renames", "-z", base, head, "--", "tools"],
        capture_output=True,
        check=False,
    )
    if raw.returncode != 0:
        raise RuntimeError(raw.stderr.decode("utf-8", errors="replace").strip() or "git diff failed")
    tokens = [os.fsdecode(part) for part in raw.stdout.split(b"\0") if part]
    matched: list[str] = []
    for index in range(0, len(tokens) - 1, 2):
        status, path = tokens[index], tokens[index + 1]
        if status in ("A", "D") and path.startswith("tools/") and path.endswith("/go.mod"):
            matched.append(path)
    return matched


def self_triggers_changed(root: Path, base: str, head: str) -> list[str]:
    raw = subprocess.run(
        ["git", "-C", str(root), "diff", "--name-only", "--no-renames", "-z", base, head, "--", *SELF_TRIGGERS],
        capture_output=True,
        check=False,
    )
    if raw.returncode != 0:
        raise RuntimeError(raw.stderr.decode("utf-8", errors="replace").strip() or "git diff failed")
    return [os.fsdecode(part) for part in raw.stdout.split(b"\0") if part]


def _run_step(name: str, cmd: list[str], root: Path, env: dict[str, str]) -> int:
    print(f"go-module-inventory: running {name}", flush=True)
    result = subprocess.run(cmd, cwd=root, env=env)
    if result.returncode == 0:
        print(f"go-module-inventory: ok {name}", flush=True)
        return 0
    if result.returncode == 4:
        print(f"go-module-inventory: could not judge {name} (exit 4)", file=sys.stderr, flush=True)
        return 4
    print(f"go-module-inventory: FAIL {name} (exit {result.returncode})", flush=True)
    return 1


def run_inventory_checks(root: Path) -> int:
    scratch_script = root / "tests" / "tillr-scratch.test.py"
    tree_test = root / "tools" / "gate" / "tree_checks_test.go"
    edges_script = root / "tools" / "gate" / "checks" / "go-edges.py"
    affected_test = root / "tests" / "go-affected.test.py"
    for required in (scratch_script, tree_test, edges_script, affected_test):
        if not required.is_file():
            print(f"go-module-inventory: could not judge: missing {required.relative_to(root)}", file=sys.stderr)
            return 4

    with tempfile.TemporaryDirectory(prefix="go-module-inventory-") as fallback_scratch:
        env = dict(os.environ)
        durable = env.get("YOLO_DURABLE_DIR")
        if durable:
            Path(durable).mkdir(parents=True, exist_ok=True)
        else:
            env["YOLO_DURABLE_DIR"] = fallback_scratch

        steps = [
            (
                "testmain-cleanup",
                [sys.executable, str(scratch_script)],
                root,
                env,
            ),
            (
                "declared-suites",
                [
                    "go",
                    "test",
                    "-count=1",
                    "-run",
                    "^(TestTreeEveryGoModuleIsBuiltAndHasDeclaredSuite|TestTreeDeclarationReadingGoModuleReadsInRepoDependencies)$",
                    ".",
                ],
                root / "tools" / "gate",
                env,
            ),
            (
                "go-edges",
                [sys.executable, str(edges_script)],
                root,
                env,
            ),
            (
                "go-affected-modules",
                [sys.executable, "-c", DISCOVER_MODULES_SNIPPET, str(affected_test)],
                root,
                env,
            ),
        ]

        worst = 0
        for name, cmd, step_cwd, step_env in steps:
            code = _run_step(name, cmd, step_cwd, step_env)
            if code == 4:
                return 4
            if code != 0:
                worst = 1
        return worst


def main(args: list[str]) -> int:
    if len(args) != 2:
        print("go-module-inventory: supply base and head arguments", file=sys.stderr)
        return 4
    base, head = args
    validate(base, head)
    root = Path.cwd()
    changed_mods = added_or_removed_go_mods(root, base, head)
    changed_self = self_triggers_changed(root, base, head)
    if not changed_mods and not changed_self:
        return 0
    if changed_mods:
        print(f"go-module-inventory: triggered by added/removed go.mod: {', '.join(changed_mods)}", flush=True)
    else:
        print(f"go-module-inventory: triggered by inventory check edit: {', '.join(changed_self)}", flush=True)
    return run_inventory_checks(root)


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except (RuntimeError, OSError) as error:
        print(f"go-module-inventory: could not judge: {error}", file=sys.stderr)
        sys.exit(4)
