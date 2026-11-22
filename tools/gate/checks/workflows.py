#!/usr/bin/env python3
"""Check tracked workflows, charters, policies, watch rules, and recorded-verdict example."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from change_common import git, validate

EXACT_TRIGGERS = (
    "definition.json",
    "tools/gate/checks/workflows.py",
    "tools/gate/checks/workflows.json",
    "tools/gate/checks/change_common.py",
)

PREFIX_TRIGGERS = (
    "config/tillr/",
    "config/tillr/workflows/",
    "config/tillr/watch/",
    "tools/tillr-drain/",
    "tools/tillr-watch/",
    "tools/tillr/",
    "tools/tillr-item/",
    "tools/tillr-run/",
    "tools/tillr-land/",
)


def is_selected(path):
    return path in EXACT_TRIGGERS or path.startswith(PREFIX_TRIGGERS)


def go_env():
    env = dict(os.environ, CGO_ENABLED="0")
    env["GOFLAGS"] = " ".join(filter(None, [os.environ.get("GOFLAGS", ""), "-trimpath"]))
    return env


def resolve_module(root, name):
    candidate = root / "tools" / name
    if (candidate / "go.mod").is_file():
        return candidate
    return Path(__file__).resolve().parents[2] / name


def main(args):
    if len(args) != 2:
        print("workflows: supply base and head arguments", file=sys.stderr)
        return 4
    base, head = args
    validate(base, head)
    root = Path.cwd()
    changed = git(root, "diff", "--name-only", "--no-renames", base, head).splitlines()
    if not any(is_selected(p) for p in changed):
        return 0

    env = go_env()
    drain_module = resolve_module(root, "tillr-drain")
    watch_module = resolve_module(root, "tillr-watch")

    # Step 1: tillr-drain workflow check
    workflows_dir = root / "config/tillr/workflows"
    if workflows_dir.exists():
        res = subprocess.run(
            ["go", "run", ".", "workflow", "check", str(workflows_dir)],
            cwd=drain_module,
            env=env,
        )
        if res.returncode != 0:
            return 1

    # Step 2 & Step 3: tillr-watch lint on root and recorded-verdict example
    config_dir = root / "config/tillr"
    policies = sorted(config_dir.glob("backend-policy*.json")) if config_dir.is_dir() else []
    run_root_lint = (root / "definition.json").is_file() or bool(policies) or (config_dir / "watch").is_dir()
    recorded_verdict = root / "tools/tillr/examples/recorded-verdict"
    run_example_lint = (recorded_verdict / "definition.json").is_file() and (recorded_verdict / "prepare.py").is_file()

    if run_root_lint or run_example_lint:
        scratch_dir = os.environ.get("YOLO_DURABLE_DIR") or None
        if scratch_dir is not None:
            Path(scratch_dir).mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=scratch_dir, prefix="workflows-gate-") as tmp:
            watch_bin = Path(tmp) / "tillr-watch"
            build = subprocess.run(
                ["go", "build", "-o", str(watch_bin), "."],
                cwd=watch_module,
                env=env,
            )
            if build.returncode != 0:
                return 1

            if run_root_lint:
                cmd = [str(watch_bin), "lint", str(root)]
                for policy in policies:
                    cmd.extend(["--policy", str(policy)])
                lint_root = subprocess.run(cmd, cwd=root, env=env)
                if lint_root.returncode != 0:
                    return 1

            if run_example_lint:
                example_copy = Path(tmp) / "recorded-verdict"
                shutil.copytree(recorded_verdict, example_copy)
                prep = subprocess.run([sys.executable, "prepare.py"], cwd=example_copy)
                if prep.returncode != 0:
                    return 1
                lint_example = subprocess.run(
                    [str(watch_bin), "lint", str(example_copy)],
                    cwd=example_copy,
                    env=env,
                )
                if lint_example.returncode != 0:
                    return 1

    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except (RuntimeError, OSError) as error:
        print(f"workflows: {error}", file=sys.stderr)
        sys.exit(4)
