#!/usr/bin/env python3
"""Judge every tracked Markdown document's scratch convention."""
from pathlib import Path
import subprocess
import sys
from tree_common import scratch_citations

def main():
    try:
        findings = scratch_citations(Path.cwd())
    except (OSError, subprocess.SubprocessError) as error:
        print("scratch: could not judge: " + str(error), file=sys.stderr)
        return 4
    print("\n".join(findings), end="\n" if findings else "")
    return 1 if findings else 0

if __name__ == "__main__":
    sys.exit(main())
