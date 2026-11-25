#!/usr/bin/env python3
"""Verify every tillr Go module is declared in cross-module source-scanning edges."""
from __future__ import annotations

import importlib.util
from pathlib import Path
import sys


def _load_go_affected(root: Path):
    path = root / "tools" / "gate" / "go_affected.py"
    if not path.is_file():
        path = Path(__file__).resolve().parents[1] / "go_affected.py"
    spec = importlib.util.spec_from_file_location("go_affected", path)
    if spec is None or spec.loader is None:
        raise OSError(f"cannot load {path}")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def main() -> int:
    root = Path.cwd()
    try:
        go_affected = _load_go_affected(root)
        findings = go_affected.missing_source_scanning_edges(root)
    except ValueError as error:
        print(f"go-edges: {error}")
        return 1
    except OSError as error:
        print(f"go-edges: could not judge: {error}", file=sys.stderr)
        return 4
    for finding in findings:
        print(f"go-edges: {finding}")
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main())
