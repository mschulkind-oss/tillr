#!/usr/bin/env python3
"""Check the tracked workflow directory when a workflow or its loader changes."""
from pathlib import Path
import subprocess
import sys
from change_common import git, validate


def main(args):
    if len(args) != 2:
        print("workflows: supply base and head arguments", file=sys.stderr)
        return 4
    base, head = args
    validate(base, head)
    root = Path.cwd()
    changed = git(root, "diff", "--name-only", "--no-renames", base, head).splitlines()
    if not any(p.startswith(("config/tillr/workflows/", "tools/tillr-drain/", "tools/tillr-item/item/"))
               or p in ("config/tillr/commands.json", "tools/gate/checks/workflows.py")
               for p in changed):
        return 0
    target = root / "config/tillr/workflows"
    if not target.exists():
        # Removing the last definition is valid; no files remain to check.
        if not git(root, "ls-tree", "--name-only", head, "--", "config/tillr/workflows").strip():
            return 0
    module = Path(__file__).resolve().parents[2] / "tillr-drain"
    return subprocess.run(["go", "run", ".", "workflow", "check", str(target)], cwd=module).returncode


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except (RuntimeError, OSError) as error:
        print(f"workflows: {error}", file=sys.stderr)
        sys.exit(4)
