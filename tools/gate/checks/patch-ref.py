#!/usr/bin/env python3
"""Judge the optional local delivery branch; structural checks judge its locator.

The declaration's ref need prevents this command from running without the branch.
The pi branch also carries the build script, which must use the offline catalog.
"""

import subprocess
import sys


def main():
    branch = sys.argv[1]
    ref = "refs/heads/" + branch
    subprocess.run(["git", "rev-parse", "--verify", ref + "^{commit}"], check=True,
                   stdout=subprocess.DEVNULL)
    print(f"ok   patch branch {branch} exists in this clone")
    if branch == "patch/pi":
        script = subprocess.check_output(["git", "show", ref + ":scripts/fork-build.mjs"], text=True)
        print("ok   the pi patch branch adds scripts/fork-build.mjs")
        if '"build:offline"' not in script:
            print("FAIL fork-build.mjs runs upstream's build:offline, not the live build", file=sys.stderr)
            return 1
        print("ok   fork-build.mjs runs upstream's build:offline, not the live build")
    return 0


if __name__ == "__main__":
    sys.exit(main())
