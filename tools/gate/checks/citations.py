#!/usr/bin/env python3
"""Judge citations against the base and head supplied by the caller."""
import sys
from change_common import run

if __name__ == "__main__":
    sys.exit(run("citations", sys.argv[1:]))
