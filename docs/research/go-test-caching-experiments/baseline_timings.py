#!/usr/bin/env python3
"""Analyze recorded gate.timing.jsonl and go-affected logs for Go test cache hits."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

DEFAULT_LOOP_DIR = "/workspace/.tillr/loop"

_GO_AFFECTED_PKG_RE = re.compile(
    r"^go-affected:\s+(pass|fail)\s+(\S+)\s+\((cached|[\d.]+s)\)\s*$"
)
_GO_AFFECTED_SUMMARY_RE = re.compile(
    r"^go-affected:\s+(\d+)\s+package\(s\)\s+\((\d+)\s+cached"
)


def _new_bucket() -> dict[str, int]:
    return {"total": 0, "hit": 0, "miss": 0, "reused": 0, "not_applicable": 0}


def _record_status(bucket: dict[str, int], status: str, *, reused: bool = False) -> None:
    if status == "not-applicable":
        bucket["not_applicable"] += 1
        return
    bucket["total"] += 1
    if status == "hit":
        bucket["hit"] += 1
    else:
        bucket["miss"] += 1
    if reused:
        bucket["reused"] += 1


def _finalize_bucket(b: dict[str, int]) -> dict[str, Any]:
    total = b["total"]
    hit = b["hit"]
    return {
        "total": total,
        "hit": hit,
        "miss": b["miss"],
        "reused": b["reused"],
        "not_applicable": b["not_applicable"],
        "hit_rate": round(hit / total, 4) if total else 0.0,
    }


def analyze_timings(
    root: Path,
    *,
    include_logs: bool = True,
) -> dict[str, Any]:
    """Scan gate.timing.jsonl (and optional go-affected logs) under root."""
    timing_files: list[Path] = []
    if root.is_dir():
        for dirpath, _, filenames in os.walk(root):
            if "gate.timing.jsonl" in filenames:
                timing_files.append(Path(dirpath) / "gate.timing.jsonl")
    elif root.is_file():
        timing_files.append(root)
    timing_files.sort()

    overall_go_build = _new_bucket()
    by_pkg_go_build: dict[str, dict[str, int]] = defaultdict(_new_bucket)

    overall_go_affected = _new_bucket()
    by_pkg_go_affected: dict[str, dict[str, int]] = defaultdict(_new_bucket)
    by_reason_go_affected: dict[str, dict[str, int]] = defaultdict(_new_bucket)

    overall_shards = _new_bucket()
    by_pkg_shards: dict[str, dict[str, int]] = defaultdict(_new_bucket)

    overall_logs = _new_bucket()
    by_pkg_logs: dict[str, dict[str, int]] = defaultdict(_new_bucket)
    log_summaries = {"runs_with_log": 0, "selected_packages": 0, "cached_packages": 0}

    records_read = 0
    malformed_lines = 0
    go_affected_structured_records = 0

    for tf in timing_files:
        try:
            text = tf.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        for raw_line in text.splitlines():
            line = raw_line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                malformed_lines += 1
                continue
            if not isinstance(rec, dict):
                malformed_lines += 1
                continue
            records_read += 1

            checks = rec.get("checks", {})
            if not isinstance(checks, dict):
                checks = {}

            # 1. go-build packages (test_cache: hit / miss / not-applicable)
            gb = checks.get("go-build", {})
            if isinstance(gb, dict):
                for mod_entry in gb.get("go_build", []) or []:
                    if not isinstance(mod_entry, dict):
                        continue
                    for pkg in mod_entry.get("packages", []) or []:
                        if not isinstance(pkg, dict):
                            continue
                        ip = str(pkg.get("import_path", "<unknown>"))
                        tc = str(pkg.get("test_cache", "miss"))
                        status = (
                            tc
                            if tc in ("hit", "miss", "not-applicable")
                            else ("hit" if pkg.get("cached") else "miss")
                        )
                        _record_status(overall_go_build, status)
                        _record_status(by_pkg_go_build[ip], status)

            # 2. go-affected structured packages/shards (if present in timing JSONL)
            ga = checks.get("go-affected", {})
            ga_pkgs = None
            if isinstance(ga, dict) and isinstance(ga.get("packages"), list):
                ga_pkgs = ga["packages"]
            elif isinstance(rec.get("packages"), list):
                ga_pkgs = rec["packages"]

            if ga_pkgs is not None:
                go_affected_structured_records += 1
                for pkg in ga_pkgs:
                    if not isinstance(pkg, dict):
                        continue
                    ip = str(pkg.get("import_path", "<unknown>"))
                    reason = str(pkg.get("reason") or "<unspecified>")
                    reused = bool(pkg.get("reused", False))
                    if "test_cache" in pkg:
                        status = str(pkg["test_cache"])
                    else:
                        status = "hit" if pkg.get("cached") else "miss"
                    _record_status(overall_go_affected, status, reused=reused)
                    _record_status(by_pkg_go_affected[ip], status, reused=reused)
                    _record_status(by_reason_go_affected[reason], status, reused=reused)

                    shards = pkg.get("shards")
                    if isinstance(shards, list):
                        for sh in shards:
                            if not isinstance(sh, dict):
                                continue
                            sh_status = (
                                str(sh["test_cache"])
                                if "test_cache" in sh
                                else ("hit" if sh.get("cached") else "miss")
                            )
                            _record_status(overall_shards, sh_status)
                            _record_status(by_pkg_shards[ip], sh_status)

        # 3. Optional companion go-affected change-logs when timing JSONL lacks packages
        if include_logs:
            change_logs = tf.parent / "change-logs"
            if change_logs.is_dir():
                for log_file in sorted(change_logs.glob("*go-affected*.log")):
                    try:
                        log_text = log_file.read_text(encoding="utf-8", errors="replace")
                    except OSError:
                        continue
                    saw_summary = False
                    for log_line in log_text.splitlines():
                        m = _GO_AFFECTED_PKG_RE.match(log_line.strip())
                        if m:
                            ip = m.group(2)
                            detail = m.group(3)
                            status = "hit" if detail == "cached" else "miss"
                            _record_status(overall_logs, status)
                            _record_status(by_pkg_logs[ip], status)
                            continue
                        ms = _GO_AFFECTED_SUMMARY_RE.match(log_line.strip())
                        if ms:
                            saw_summary = True
                            log_summaries["selected_packages"] += int(ms.group(1))
                            log_summaries["cached_packages"] += int(ms.group(2))
                    if saw_summary:
                        log_summaries["runs_with_log"] += 1

    return {
        "root": str(root),
        "timing_files": len(timing_files),
        "records_read": records_read,
        "malformed_lines": malformed_lines,
        "go_affected_structured_records": go_affected_structured_records,
        "go_build": {
            "overall": _finalize_bucket(overall_go_build),
            "by_package": {
                k: _finalize_bucket(v) for k, v in sorted(by_pkg_go_build.items())
            },
        },
        "go_affected": {
            "overall": _finalize_bucket(overall_go_affected),
            "by_package": {
                k: _finalize_bucket(v) for k, v in sorted(by_pkg_go_affected.items())
            },
            "by_reason": {
                k: _finalize_bucket(v) for k, v in sorted(by_reason_go_affected.items())
            },
            "shards_overall": _finalize_bucket(overall_shards),
            "shards_by_package": {
                k: _finalize_bucket(v) for k, v in sorted(by_pkg_shards.items())
            },
        },
        "go_affected_logs": {
            "overall": _finalize_bucket(overall_logs),
            "by_package": {
                k: _finalize_bucket(v) for k, v in sorted(by_pkg_logs.items())
            },
            "summary_lines": log_summaries,
        },
    }


def format_human(report: dict[str, Any]) -> str:
    lines: list[str] = []
    lines.append(f"Root: {report['root']}")
    lines.append(
        f"Files: {report['timing_files']} gate.timing.jsonl, "
        f"{report['records_read']} records ({report['malformed_lines']} malformed)"
    )

    gb = report["go_build"]["overall"]
    lines.append("")
    lines.append(
        f"go-build (gate.timing.jsonl test_cache): "
        f"{gb['hit']}/{gb['total']} hit ({gb['hit_rate']*100:.2f}%), "
        f"{gb['miss']} miss, {gb['not_applicable']} not-applicable"
    )

    ga = report["go_affected"]["overall"]
    lines.append(
        f"go-affected (structured in gate.timing.jsonl, "
        f"{report['go_affected_structured_records']} records): "
        f"{ga['hit']}/{ga['total']} cached ({ga['hit_rate']*100:.2f}%), "
        f"{ga['reused']} reused"
    )
    if report["go_affected"]["by_reason"]:
        lines.append("  By selection reason:")
        for reason, b in report["go_affected"]["by_reason"].items():
            lines.append(
                f"    {reason}: {b['hit']}/{b['total']} cached ({b['hit_rate']*100:.2f}%)"
            )

    logs = report["go_affected_logs"]
    lo = logs["overall"]
    ls = logs["summary_lines"]
    lines.append("")
    lines.append(
        f"go-affected (companion change-logs/*go-affected*.log): "
        f"{lo['hit']}/{lo['total']} package runs cached ({lo['hit_rate']*100:.2f}%)"
    )
    if ls["runs_with_log"]:
        rate = (
            ls["cached_packages"] / ls["selected_packages"] * 100.0
            if ls["selected_packages"]
            else 0.0
        )
        lines.append(
            f"  Summary lines across {ls['runs_with_log']} runs: "
            f"{ls['cached_packages']}/{ls['selected_packages']} selected packages cached ({rate:.2f}%)"
        )
    if logs["by_package"]:
        lines.append("  Top packages by total runs (logs):")
        ranked = sorted(
            logs["by_package"].items(),
            key=lambda kv: (-kv[1]["total"], kv[0]),
        )
        for ip, b in ranked[:20]:
            lines.append(
                f"    {ip}: {b['hit']}/{b['total']} cached ({b['hit_rate']*100:.2f}%)"
            )
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root",
        default=DEFAULT_LOOP_DIR,
        help=f"Directory or file to scan (default: {DEFAULT_LOOP_DIR})",
    )
    parser.add_argument("--json", action="store_true", help="Output JSON instead of text")
    parser.add_argument(
        "--no-logs",
        action="store_true",
        help="Do not inspect companion change-logs/*go-affected*.log files",
    )
    args = parser.parse_args(argv)
    report = analyze_timings(Path(args.root), include_logs=not args.no_logs)
    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print(format_human(report))
    return 0


if __name__ == "__main__":
    sys.exit(main())
