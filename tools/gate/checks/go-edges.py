#!/usr/bin/env python3
"""Verify every tillr Go module is declared in cross-module source-scanning edges."""
from __future__ import annotations

import json
from pathlib import Path
import sys


def discover_tillr_modules(root: Path) -> list[str]:
    """Return tools/tillr and every tools/tillr-* directory holding a go.mod."""
    modules: list[str] = []
    if (root / "tools" / "tillr" / "go.mod").is_file():
        modules.append("tools/tillr")
    tools_dir = root / "tools"
    if tools_dir.is_dir():
        for go_mod in sorted(tools_dir.glob("tillr-*/go.mod")):
            modules.append(f"tools/{go_mod.parent.name}")
    return modules


def load_edges(root: Path) -> dict[str, dict]:
    path = root / "tools" / "gate" / "go-test-edges.json"
    if not path.is_file():
        return {}
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"{path}: expected JSON object")
    for pkg, entry in data.items():
        if not isinstance(pkg, str) or not isinstance(entry, dict):
            raise ValueError(f"{path}: invalid entry for {pkg!r}")
        for key in ("builds", "reads"):
            val = entry.get(key, [])
            if not isinstance(val, list) or not all(isinstance(x, str) for x in val):
                raise ValueError(f"{path}: {pkg}.{key} must be a list of strings")
        for map_key, list_key in (("build_tests", "builds"), ("read_tests", "reads")):
            if map_key not in entry:
                continue
            scoped_tests = entry[map_key]
            if not isinstance(scoped_tests, dict):
                raise ValueError(f"{path}: {pkg}.{map_key} must be an object")
            allowed_keys = set(entry.get(list_key, []))
            for rule, names in scoped_tests.items():
                if not isinstance(rule, str) or rule not in allowed_keys:
                    raise ValueError(
                        f"{path}: {pkg}.{map_key} key {rule!r} must appear in {pkg}.{list_key}"
                    )
                if (
                    not isinstance(names, list)
                    or not names
                    or not all(isinstance(t, str) and bool(t.strip()) and t == t.strip() for t in names)
                ):
                    raise ValueError(
                        f"{path}: {pkg}.{map_key}[{rule!r}] must be a non-empty list of test names"
                    )
    return data


SOURCE_SCANNING_EDGES: tuple[tuple[str, str, bool], ...] = (
    (
        "github.com/mschulkind-oss/tillr/definition",
        "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers",
        True,
    ),
    (
        "github.com/mschulkind-oss/tillr/store",
        "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit",
        False,
    ),
)


def missing_source_scanning_edges(root: Path) -> list[str]:
    """Return refusal messages for any tillr Go module absent from source-scanning edges."""
    edges = load_edges(root)
    modules = discover_tillr_modules(root)
    findings: list[str] = []
    for pkg, test_name, include_root_module in SOURCE_SCANNING_EDGES:
        entry = edges.get(pkg)
        if entry is None:
            findings.append(f"{pkg}: missing entry in tools/gate/go-test-edges.json")
            continue
        reads = set(entry.get("reads", []))
        read_tests = entry.get("read_tests", {})
        for mod in modules:
            if not include_root_module and mod == "tools/tillr":
                continue
            rule = f"{mod}/"
            if rule not in reads:
                findings.append(
                    f"{pkg}: missing {rule!r} in tools/gate/go-test-edges.json reads"
                )
            elif test_name not in read_tests.get(rule, []):
                findings.append(
                    f"{pkg}: missing {test_name!r} in tools/gate/go-test-edges.json read_tests[{rule!r}]"
                )
    return findings


def main() -> int:
    root = Path.cwd()
    try:
        findings = missing_source_scanning_edges(root)
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
