#!/usr/bin/env python3
"""Run the drain's fork-driver consumer test on changes to its inputs."""
import os
from pathlib import Path
import subprocess
import sys

from change_common import git, validate


TEST_NEEDS = {
    "TestForkBuildDeliveryAndSuccessiveLaneBases": frozenset({
        "bin/pi-forks",
        "tools/fork-check.py",
        "tools/gate/checks/pi-forks-drain.json",
        "tools/gate/checks/pi-forks-drain.py",
    }),
}


def main(base, head):
    validate(base, head)
    root = Path.cwd()
    changed = set(git(root, "diff", "--name-only", "--no-renames", base, head).splitlines())
    for test, needs in TEST_NEEDS.items():
        if changed.isdisjoint(needs):
            continue
        result = subprocess.run(["go", "test", "-count=1", ".", "-run", f"^{test}$"],
                                cwd=root / "tools/tillr-drain", env={**os.environ, "CGO_ENABLED": "0"})
        if result.returncode:
            return 1 if result.returncode == 1 else 4
    return 0


if __name__ == "__main__":
    try:
        if len(sys.argv) != 3:
            raise RuntimeError("supply base and head arguments")
        sys.exit(main(*sys.argv[1:]))
    except (RuntimeError, OSError) as error:
        print(f"pi-forks-drain: {error}", file=sys.stderr)
        sys.exit(4)
