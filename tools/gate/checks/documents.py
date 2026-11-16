#!/usr/bin/env python3
"""Judge documents against the base and head supplied by the caller."""
import sys
from change_common import run

if __name__ == "__main__":
    sys.exit(run("documents", sys.argv[1:]))
