#!/usr/bin/env python3
"""Commit the standard step adapter with the drain sources that embed it."""
from pathlib import Path

source = Path(__file__).resolve().parent / "tillr-loop"
target = source.parent.parent / "tillr-drain" / "drain" / "tillr-loop.py"
target.write_bytes(source.read_bytes())
print("synced tools/tillr-loop/tillr-loop -> tools/tillr-drain/drain/tillr-loop.py")
