#!/usr/bin/env python3
"""Judge every tracked Markdown document's design-citations convention."""
from pathlib import Path
import subprocess
import sys
from tree_common import design_citation_findings

def main():
    try:
        findings = design_citation_findings(Path.cwd())
    except (OSError, subprocess.SubprocessError) as error:
        print("design-citations: could not judge: " + str(error), file=sys.stderr)
        return 4
    print("\n".join(findings), end="\n" if findings else "")
    return 1 if findings else 0

if __name__ == "__main__":
    sys.exit(main())
