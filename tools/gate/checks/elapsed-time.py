#!/usr/bin/env python3
"""Judge added or changed test files against the gate's no-elapsed-time rule."""
import json
from pathlib import Path
import re
import subprocess
import sys

HERE = Path(__file__).resolve().parent
if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))

from change_common import git, validate

TIMING_DECLARATIONS = "tools/gate-timing-declarations.json"
DELIVERED_PACK_TREES = re.compile(r"^yolo-packs/[^/]+/files/")
EXCLUDED_PARTS = (".forks", "node_modules", ".yolo")

GO_DELTA_DIRECT = re.compile(r"time\.Since\([^)]*\)\s*(?:[<>]=?)")
GO_DELTA_INLINE = re.compile(r"(\w+)\s*:=\s*time\.Since\([^)]*\)\s*;\s*\1\s*(?:[<>]=?)")
GO_DELTA_ASSIGN = re.compile(r"(\w+)\s*:?=\s*time\.Since\(")
JS_DELTA_DIRECT = re.compile(r"(?:performance\.now\(\)|Date\.now\(\))\s*-\s*[\w.$]+\s*(?:[<>]=?)")
JS_DELTA_ASSIGN = re.compile(
    r"(?:const|let|var)\s+(\w+)\s*=\s*(?:performance\.now\(\)|Date\.now\(\))\s*-\s*[\w.$]+"
)
PY_DELTA_DIRECT = re.compile(
    r"time\.(?:monotonic|time|perf_counter)\(\)\s*-\s*[\w.]+\s*(?:[<>]=?)"
)
PY_DELTA_ASSIGN = re.compile(
    r"(\w+)\s*=\s*time\.(?:monotonic|time|perf_counter)\(\)\s*-\s*[\w.]+"
)

UNDECLARED_PREFIX = (
    "a check asserts elapsed time, which the gate's timing rule forbids; assert a count, an "
    "order or an equality over what the product recorded, or declare it in "
    "tools/gate-timing-declarations.json with its reason and its repair: "
)
STALE_PREFIX = (
    "a timing declaration no longer matches a hit, so it is a silencer rather than a record; "
    "remove it or fix the snippet: "
)


class TimingDeclarationError(Exception):
    """A malformed or incomplete timing declaration entry."""


def is_timing_test_path(name: str) -> bool:
    """Whether a repo-relative path is a test file in the timing scan corpus."""
    if DELIVERED_PACK_TREES.match(name):
        return False
    if any(part in EXCLUDED_PARTS for part in Path(name).parts):
        return False
    base = Path(name).name
    return (base.endswith("_test.go") or ".test." in base
            or base.startswith("test_") or base.endswith("_test.py"))


def elapsed_time_hits_in_text(name: str, text: str):
    """Every line in `text` that measures a wall-clock delta and compares it to a bound."""
    if name.endswith(".go"):
        direct, inline, assign = GO_DELTA_DIRECT, GO_DELTA_INLINE, GO_DELTA_ASSIGN
    elif name.endswith((".mjs", ".ts", ".js")):
        direct, inline, assign = JS_DELTA_DIRECT, None, JS_DELTA_ASSIGN
    elif name.endswith(".py"):
        direct, inline, assign = PY_DELTA_DIRECT, None, PY_DELTA_ASSIGN
    else:
        return
    deltas = {}
    for number, line in enumerate(text.splitlines(), 1):
        if direct.search(line) or (inline is not None and inline.search(line)):
            yield name, number, " ".join(line.split())
            continue
        assigned = assign.search(line)
        if assigned:
            deltas[assigned.group(1)] = number
            continue
        for variable in deltas:
            if re.search(r"\b" + re.escape(variable) + r"\b\s*(?:[<>]=?)", line):
                yield name, number, " ".join(line.split())
                break


def parse_timing_declarations(raw: str, label: str = TIMING_DECLARATIONS) -> list[dict]:
    """Read and validate timing declarations, refusing malformed or empty entries."""
    try:
        document = json.loads(raw)
    except (json.JSONDecodeError, UnicodeDecodeError) as error:
        raise TimingDeclarationError(f"{label}: {error}") from error
    if not isinstance(document, dict) or set(document) != {"declarations"}:
        raise TimingDeclarationError(f"{label}: expected a single 'declarations' key")
    entries = document["declarations"]
    if not isinstance(entries, list):
        raise TimingDeclarationError(f"{label}: 'declarations' must be a list")
    seen = set()
    for index, entry in enumerate(entries):
        where = f"{label}: declarations[{index}]"
        if not isinstance(entry, dict):
            raise TimingDeclarationError(f"{where}: expected an object")
        if set(entry) != {"file", "snippet", "kind", "reason", "repair"}:
            raise TimingDeclarationError(
                f"{where}: expected keys file, snippet, kind, reason, repair; got {sorted(entry)}"
            )
        for key in ("file", "snippet", "kind", "reason", "repair"):
            if not isinstance(entry[key], str) or not entry[key].strip():
                raise TimingDeclarationError(f"{where} needs a non-empty '{key}'")
        if entry["kind"] not in ("liveness", "backlog"):
            raise TimingDeclarationError(
                f"{where} names kind {entry['kind']!r}, which is not liveness or backlog"
            )
        key = (entry["file"], entry["snippet"])
        if key in seen:
            raise TimingDeclarationError(f"{where} repeats the declaration for {key}")
        seen.add(key)
    return entries


def blob_bytes(root: Path, rev: str, path: str) -> bytes | None:
    """Return regular-file blob bytes at `rev:path`, or None when absent or non-regular."""
    listing = git(root, "ls-tree", "-z", rev, "--", path)
    if not listing:
        return None
    entry = listing.split("\0", 1)[0]
    metadata, _, found = entry.partition("\t")
    if found != path:
        return None
    mode, kind, obj = metadata.split()
    if kind != "blob" or mode not in ("100644", "100755"):
        return None
    shown = subprocess.run(["git", "-C", str(root), "cat-file", "blob", obj], capture_output=True)
    if shown.returncode != 0:
        raise RuntimeError(shown.stderr.decode(errors="replace").strip() or f"cannot read blob {obj}")
    return shown.stdout


def elapsed_time_findings(root: Path, base: str, head: str) -> list[str]:
    """Judge test files added or changed in `base..head` and declaration edits."""
    changed = [
        path for path in git(root, "diff", "--name-only", "--no-renames", "-z", base, head).split("\0")
        if path
    ]
    changed_tests = {path for path in changed if is_timing_test_path(path)}
    decl_changed = TIMING_DECLARATIONS in changed
    if not changed_tests and not decl_changed:
        return []

    raw_head = blob_bytes(root, head, TIMING_DECLARATIONS)
    if raw_head is not None:
        try:
            declarations = parse_timing_declarations(raw_head.decode("utf-8"), TIMING_DECLARATIONS)
        except (UnicodeDecodeError, TimingDeclarationError) as error:
            return [str(error)]
    else:
        declarations = []

    added_decl_keys: set[tuple[str, str]] = set()
    removed_decl_files: set[str] = set()
    if decl_changed:
        raw_base = blob_bytes(root, base, TIMING_DECLARATIONS)
        base_declarations: list[dict] = []
        if raw_base is not None:
            try:
                base_declarations = parse_timing_declarations(raw_base.decode("utf-8"), TIMING_DECLARATIONS)
            except (UnicodeDecodeError, TimingDeclarationError):
                base_declarations = []
        base_keys = {(entry["file"], entry["snippet"]) for entry in base_declarations}
        head_keys = {(entry["file"], entry["snippet"]) for entry in declarations}
        added_decl_keys = head_keys - base_keys
        removed_decl_files = {
            file for file, _snippet in (base_keys - head_keys) if is_timing_test_path(file)
        }

    scan_paths = sorted(
        changed_tests
        | removed_decl_files
        | {file for file, _snippet in added_decl_keys if is_timing_test_path(file)}
    )
    declared = {(entry["file"], entry["snippet"]): entry for entry in declarations}
    hits = []
    for name in scan_paths:
        content = blob_bytes(root, head, name)
        if content is None:
            continue
        hits.extend(elapsed_time_hits_in_text(name, content.decode("utf-8", errors="replace")))

    findings: list[str] = []
    undeclared = [
        f"{name}:{number}: {snippet}"
        for name, number, snippet in hits
        if (name, snippet) not in declared
    ]
    if undeclared:
        findings.append(UNDECLARED_PREFIX + "; ".join(undeclared))

    matched = {(name, snippet) for name, _number, snippet in hits}
    must_match = [
        entry for entry in declarations
        if entry["file"] in changed_tests or (entry["file"], entry["snippet"]) in added_decl_keys
    ]
    stale = [
        f"{entry['file']}: {entry['snippet']}"
        for entry in must_match
        if (entry["file"], entry["snippet"]) not in matched
    ]
    if stale:
        findings.append(STALE_PREFIX + "; ".join(stale))
    return findings


def main(args: list[str]) -> int:
    if len(args) != 2:
        print("elapsed-time: could not judge: missing base and head arguments; supply BASE HEAD",
              file=sys.stderr)
        return 4
    base, head = args
    validate(base, head)
    findings = elapsed_time_findings(Path.cwd(), base, head)
    if findings:
        print("\n".join(findings))
        return 1
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except (RuntimeError, OSError, ValueError) as error:
        print(f"elapsed-time: could not judge: {error}", file=sys.stderr)
        sys.exit(4)
