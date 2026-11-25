#!/usr/bin/env python3
"""Judge added or changed test files against the gate's no-elapsed-time and short-deadline rules."""
import ast
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
SHORT_DEADLINE_PREFIX = (
    "a Go test waits with a short positive deadline (< 5m), which a loaded machine "
    "can miss; wait with hangGuard (5 minutes) instead of a short positive deadline, or declare "
    "it in tools/gate-timing-declarations.json with its reason and its repair: "
)
STALE_PREFIX = (
    "a timing declaration no longer matches a hit, so it is a silencer rather than a record; "
    "remove it or fix the snippet: "
)

HANG_GUARD_SECONDS = 5 * 60.0
ALLOWED_DURATION_NAMES = frozenset({"hangGuard", "heartbeatStallBound"})
GO_TIME_UNITS = {
    "Nanosecond": 1e-9,
    "Microsecond": 1e-6,
    "Millisecond": 1e-3,
    "Second": 1.0,
    "Minute": 60.0,
    "Hour": 3600.0,
}
GO_CONST_LINE = re.compile(r"^\s*const\s+(\w+)(?:\s+[\w.*\[\]]+)?\s*=\s*(.+)$")
GO_CONST_BLOCK_START = re.compile(r"^\s*const\s*\(\s*$")
GO_CONST_BLOCK_ENTRY = re.compile(r"^\s*(\w+)(?:\s+[\w.*\[\]]+)?\s*=\s*(.+)$")
GO_DIRECT_AFTER_CASE = re.compile(r"^\s*case\s*<-\s*time\.After\(")
GO_ASSIGN_AFTER = re.compile(r"^\s*(\w+)\s*:?=\s*time\.After\(")
GO_ASSIGN_NOW_ADD = re.compile(r"^\s*(?:for\s+)?(\w+)\s*:?=\s*time\.Now\(\)\.Add\(")
GO_ASSIGN_TIMER = re.compile(r"^\s*(\w+)\s*:?=\s*time\.NewTimer\(")
GO_FAIL_OR_KILL = re.compile(
    r"\b(?:t\.(?:Fatal|Fatalf|Error|Errorf|Fail|FailNow)|panic|Process\.Kill)\s*\("
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


def _strip_go_line_comment(line: str) -> str:
    in_double = False
    in_raw = False
    i = 0
    n = len(line)
    while i < n:
        ch = line[i]
        if in_raw:
            if ch == "`":
                in_raw = False
        elif in_double:
            if ch == "\\":
                i += 2
                continue
            if ch == '"':
                in_double = False
        else:
            if ch == "`":
                in_raw = True
            elif ch == '"':
                in_double = True
            elif ch == "/" and i + 1 < n and line[i + 1] == "/":
                return line[:i]
        i += 1
    return line


def _extract_balanced_parens(text: str, open_paren_idx: int) -> tuple[str, int] | None:
    """Given index of '(', return (inner_text, index_after_closing_paren) or None."""
    if open_paren_idx < 0 or open_paren_idx >= len(text) or text[open_paren_idx] != "(":
        return None
    depth = 0
    for idx in range(open_paren_idx, len(text)):
        ch = text[idx]
        if ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
            if depth == 0:
                return text[open_paren_idx + 1:idx], idx + 1
    return None


def _extract_go_consts(lines: list[str]) -> dict[str, str]:
    consts: dict[str, str] = {}
    in_block = False
    for raw in lines:
        code = _strip_go_line_comment(raw).rstrip()
        if in_block:
            if re.match(r"^\s*\)\s*$", code):
                in_block = False
                continue
            m = GO_CONST_BLOCK_ENTRY.match(code)
            if m:
                consts[m.group(1)] = m.group(2).strip()
        else:
            if GO_CONST_BLOCK_START.match(code):
                in_block = True
                continue
            m = GO_CONST_LINE.match(code)
            if m:
                consts[m.group(1)] = m.group(2).strip()
    return consts


def resolve_go_duration_seconds(
    expr: str, consts: dict[str, str], visiting: frozenset[str] = frozenset()
) -> float | None:
    """Resolve a Go duration expression to seconds, or None if unknown/allowed."""
    expr = expr.strip()
    if not expr or expr in ALLOWED_DURATION_NAMES:
        return None
    try:
        tree = ast.parse(expr, mode="eval")
    except SyntaxError:
        return None

    def eval_node(node: ast.AST) -> float | None:
        if (
            isinstance(node, ast.Constant)
            and isinstance(node.value, (int, float))
            and not isinstance(node.value, bool)
        ):
            return float(node.value)
        if isinstance(node, ast.Name):
            if node.id in ALLOWED_DURATION_NAMES or node.id in visiting:
                return None
            if node.id in consts:
                return resolve_go_duration_seconds(
                    consts[node.id], consts, visiting | {node.id}
                )
            return None
        if isinstance(node, ast.Attribute):
            if isinstance(node.value, ast.Name) and node.value.id == "time":
                return GO_TIME_UNITS.get(node.attr)
            return None
        if isinstance(node, ast.UnaryOp):
            val = eval_node(node.operand)
            if val is None:
                return None
            if isinstance(node.op, ast.UAdd):
                return val
            if isinstance(node.op, ast.USub):
                return -val
            return None
        if isinstance(node, ast.BinOp):
            left = eval_node(node.left)
            right = eval_node(node.right)
            if left is None or right is None:
                return None
            if isinstance(node.op, ast.Add):
                return left + right
            if isinstance(node.op, ast.Sub):
                return left - right
            if isinstance(node.op, ast.Mult):
                return left * right
            if isinstance(node.op, ast.Div):
                return left / right if right != 0 else None
            return None
        return None

    return eval_node(tree.body)


def _case_body_fails(lines: list[str], case_idx: int, colon_pos: int) -> bool:
    """Whether the select case starting at `case_idx` fails the test or kills a process."""
    case_line = lines[case_idx]
    case_indent = len(case_line.expandtabs(4)) - len(case_line.lstrip().expandtabs(4))
    inline_rest = _strip_go_line_comment(case_line[colon_pos + 1:]).strip()
    body_parts = [inline_rest] if inline_rest else []
    for j in range(case_idx + 1, len(lines)):
        code = _strip_go_line_comment(lines[j])
        stripped = code.lstrip()
        if not stripped:
            continue
        line_indent = len(code.expandtabs(4)) - len(stripped.expandtabs(4))
        if line_indent <= case_indent and (
            stripped.startswith(("case ", "case\t", "default:", "}"))
        ):
            break
        body_parts.append(stripped)
    if not body_parts:
        return False
    return bool(GO_FAIL_OR_KILL.search("\n".join(body_parts)))


def _var_passed_to_call(code: str, var: str) -> bool:
    """Whether `var` appears as an argument inside (...) in `code` (not `<-var`)."""
    pattern = re.compile(r"(?<!<-)\b" + re.escape(var) + r"\b")
    for match in pattern.finditer(code):
        prefix = code[:match.start()]
        if prefix.count("(") > prefix.count(")"):
            return True
    return False


def short_deadline_hits_in_text(name: str, text: str):
    """Yield (name, line_number, snippet) for short positive deadlines in *_test.go."""
    if not name.endswith("_test.go"):
        return
    lines = text.splitlines()
    consts = _extract_go_consts(lines)

    for idx, raw_line in enumerate(lines):
        code = _strip_go_line_comment(raw_line)
        snippet = " ".join(raw_line.split())

        m_direct = GO_DIRECT_AFTER_CASE.match(code)
        if m_direct:
            parsed = _extract_balanced_parens(code, m_direct.end() - 1)
            if parsed is not None:
                expr, after_idx = parsed
                rest = code[after_idx:].lstrip()
                if rest.startswith(":"):
                    colon_pos = code.find(":", after_idx)
                    dur = resolve_go_duration_seconds(expr, consts)
                    if dur is not None and 0 < dur < HANG_GUARD_SECONDS:
                        if _case_body_fails(lines, idx, colon_pos):
                            yield name, idx + 1, snippet
            continue

        m_assign = GO_ASSIGN_AFTER.match(code)
        if m_assign:
            var = m_assign.group(1)
            parsed = _extract_balanced_parens(code, m_assign.end() - 1)
            if parsed is None:
                continue
            expr, _ = parsed
            dur = resolve_go_duration_seconds(expr, consts)
            if dur is None or dur <= 0 or dur >= HANG_GUARD_SECONDS:
                continue
            case_re = re.compile(r"^\s*case\s*<-\s*" + re.escape(var) + r"\s*:")
            reassign_re = re.compile(r"\b" + re.escape(var) + r"\s*:?=")
            for k in range(idx + 1, len(lines)):
                code_k = _strip_go_line_comment(lines[k])
                if re.match(r"^\s*func\b", code_k) or reassign_re.search(code_k):
                    break
                if case_re.match(code_k):
                    colon_pos = code_k.find(":")
                    if _case_body_fails(lines, k, colon_pos):
                        yield name, idx + 1, snippet
                        break
                elif _var_passed_to_call(code_k, var):
                    yield name, idx + 1, snippet
                    break
            continue

        m_now = GO_ASSIGN_NOW_ADD.match(code)
        if m_now:
            var = m_now.group(1)
            parsed = _extract_balanced_parens(code, m_now.end() - 1)
            if parsed is None:
                continue
            expr, after_idx = parsed
            dur = resolve_go_duration_seconds(expr, consts)
            if dur is None or dur <= 0 or dur >= HANG_GUARD_SECONDS:
                continue
            poll_re = re.compile(
                r"\btime\.(?:Now\(\)\.(?:After|Before)|Until)\(\s*" + re.escape(var) + r"\s*\)"
            )
            if poll_re.search(code[after_idx:]):
                yield name, idx + 1, snippet
                continue
            reassign_re = re.compile(r"\b" + re.escape(var) + r"\s*:?=")
            for k in range(idx + 1, len(lines)):
                code_k = _strip_go_line_comment(lines[k])
                if re.match(r"^\s*func\b", code_k) or reassign_re.search(code_k):
                    break
                if poll_re.search(code_k):
                    yield name, idx + 1, snippet
                    break
            continue

        m_timer = GO_ASSIGN_TIMER.match(code)
        if m_timer:
            var = m_timer.group(1)
            parsed = _extract_balanced_parens(code, m_timer.end() - 1)
            if parsed is None:
                continue
            expr, _ = parsed
            dur = resolve_go_duration_seconds(expr, consts)
            if dur is None or dur <= 0 or dur >= HANG_GUARD_SECONDS:
                continue
            case_re = re.compile(r"^\s*case\s*<-\s*" + re.escape(var) + r"\.C\s*:")
            reassign_re = re.compile(r"\b" + re.escape(var) + r"\s*:?=")
            for k in range(idx + 1, len(lines)):
                code_k = _strip_go_line_comment(lines[k])
                if re.match(r"^\s*func\b", code_k) or reassign_re.search(code_k):
                    break
                if case_re.match(code_k):
                    colon_pos = code_k.find(":")
                    if _case_body_fails(lines, k, colon_pos):
                        yield name, idx + 1, snippet
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
    elapsed_hits = []
    deadline_hits = []
    for name in scan_paths:
        content = blob_bytes(root, head, name)
        if content is None:
            continue
        text = content.decode("utf-8", errors="replace")
        elapsed_hits.extend(elapsed_time_hits_in_text(name, text))
        deadline_hits.extend(short_deadline_hits_in_text(name, text))

    findings: list[str] = []
    undeclared_elapsed = [
        f"{name}:{number}: {snippet}"
        for name, number, snippet in elapsed_hits
        if (name, snippet) not in declared
    ]
    if undeclared_elapsed:
        findings.append(UNDECLARED_PREFIX + "; ".join(undeclared_elapsed))

    undeclared_deadlines = [
        f"{name}:{number}: {snippet}"
        for name, number, snippet in deadline_hits
        if (name, snippet) not in declared
    ]
    if undeclared_deadlines:
        findings.append(SHORT_DEADLINE_PREFIX + "; ".join(undeclared_deadlines))

    matched = {(name, snippet) for name, _number, snippet in elapsed_hits + deadline_hits}
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
