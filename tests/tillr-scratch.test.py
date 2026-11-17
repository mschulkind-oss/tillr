#!/usr/bin/env python3
"""Check TestMain cleanup; --suites also measures scratch growth around uncached suites.

The default check is part of tillr-docs.test.py, so the repository gate runs it.
Only this check's own scratch is removed. Existing leftovers are never deleted.
"""

import argparse
import os
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def entries(root):
    return {
        path.name
        for pattern in ("tillr-*-bin-*", "tillr-drain-trace-*")
        for path in root.glob(pattern)
        if path.is_dir()
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--suites", action="store_true")
    args = parser.parse_args()
    root = Path(os.environ.get("YOLO_DURABLE_DIR", ROOT / ".yolo/durable")).resolve()
    root.mkdir(parents=True, exist_ok=True)
    before = entries(root)
    print(f"scratch before: {len(before)}", flush=True)
    result = 0
    with tempfile.TemporaryDirectory(prefix="tillr-cleanup-check-", dir=root) as scratch:
        env = {**os.environ, "YOLO_DURABLE_DIR": str(root), "TMPDIR": scratch}
        command = ["go", "test", "-count=1", "tests/tillr-testmain_test.go"]
        print("+ " + " ".join(command), flush=True)
        result = subprocess.run(command, cwd=ROOT, env=env).returncode
        if args.suites and not result:
            for module in sorted((ROOT / "tools").glob("tillr*")):
                if not (module / "go.mod").is_file():
                    continue
                command = ["go", "test", "-count=1", "./..."]
                print(f"+ (cd tools/{module.name} && {' '.join(command)})", flush=True)
                code = subprocess.run(command, cwd=module, env=env).returncode
                if code:
                    result = code
                    break
    after = entries(root)
    added = after - before
    print(f"scratch after: {len(after)}; new entries: {len(added)}", flush=True)
    for name in sorted(added):
        print(f"FAIL: suite left {name}", flush=True)
    return result or bool(added)


if __name__ == "__main__":
    raise SystemExit(main())
