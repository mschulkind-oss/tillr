#!/usr/bin/env python3
"""The documentation contract: every face a document names is a face the binary answers.

WHY THIS EXISTS. `--help` is the one home of a flag
(docs/design/tillr-distribution.md Section 3). A module `README.md` and the `tillr-operating`
skill say what a module owns, which faces compose, and when a face refuses; they never
restate a flag, because a copied flag list is the page that disagrees with the binary a month
later. That split only holds if the documents are checked against the binaries, so this suite
does the three checks the split needs:

  1. the eight module READMEs exist and each opens with the stamped Known Gaps callout, so a
     module ships its own judgment rather than source with nothing beside it;
  2. no document carries a flag table, in either spelling the design bans -- a Markdown table
     whose header names a flag, and a line that is an option list;
  3. every `<bin> <face>` (or `tillr <noun> <face>`) invocation a document names is run with
     `--help` against the binary built from this tree, and a face that does not exist -- or a
     face that answers something other than exit 0 and its own flag set -- fails the suite.

A face that does not exist is the failure this exists to catch: a document is the only place
that can name a face nobody built, and nothing else reads a document for executable claims.

Cases:
  1. the eight module READMEs open with the same stamped Known Gaps callout;
  2. no document carries a flag table;
  3. every documented face answers `--help` with exit 0 and its own flag set.

Run: python3 tests/tillr-docs.test.py
"""

import ast
import copy
import importlib.util
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# The tillr pack delivers copies of tracked files (the module mirror and the design
# documentation snapshot). A path-keyed allowance follows a copy to its source by the one
# rule, `source_of` in tools/pack-docs.py, so a copy is judged exactly as its source is.
_spec = importlib.util.spec_from_file_location("pack_docs", ROOT / "tools" / "pack-docs.py")
pack_docs = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(pack_docs)

# The eight binaries and the module each is built from. `tillr` is the dispatcher and is
# built from `tillr-cli`; the seven `tillr-<noun>` name their own module.
MODULES = (
    "tillr-cli",
    "tillr-drain",
    "tillr-gate",
    "tillr-item",
    "tillr-land",
    "tillr-run",
    "tillr-store",
    "tillr-view",
)
BINARY = {"tillr-cli": "tillr", **{module: module for module in MODULES if module != "tillr-cli"}}

# The seven nouns the dispatcher forwards, in the order its usage line prints them.
NOUNS = ("store", "item", "land", "drain", "run", "gate", "view")

SKILL = ROOT / "yolo-packs" / "tillr" / "skills" / "tillr-operating" / "SKILL.md"

# The documents the design's layer 2 owns: one README per binary module, and the skill. The
# skill is read, never edited here: its prose is the loop's home, not a module's.
DOCUMENTS = [ROOT / "tools" / module / "README.md" for module in MODULES] + [SKILL]

# A known-gaps callout, stamped with the date and commit it was verified at. The stamp is
# what makes the banner a claim instead of a decoration: a reader can tell a re-verified
# banner from one nobody has looked at since.
STAMP = re.compile(
    r"^> \*\*Known Gaps \(verified \d{4}-\d{2}-\d{2} at `[0-9a-f]{7,40}`\)\.\*\*"
)

FENCE = re.compile(r"^```[^\n]*\n(.*?)^```", re.S | re.M)

# A line that is an option list: the design's second spelling of a flag table.
OPTION_LINE = re.compile(r"^\s+--[A-Za-z]")
# A Markdown table's header cell that names a flag.
FLAG_HEADER = re.compile(r"\b(flag|option|switch)\b", re.I)

BUILD_TIMEOUT = 300
HELP_TIMEOUT = 60


def ok(name):
    print(f"ok   {name}")


def fail(message):
    print(f"FAIL {message}", file=sys.stderr)
    raise SystemExit(1)


def text_of(path):
    if not path.is_file():
        fail(f"missing document: {path.relative_to(ROOT)}")
    return path.read_text()


def opening_block(text):
    """The callout a document opens with: the first block, skipping headings and blanks."""
    lines = text.splitlines()
    index = 0
    while index < len(lines) and (not lines[index].strip() or lines[index].lstrip().startswith("#")):
        index += 1
    block = []
    while index < len(lines) and lines[index].startswith(">"):
        block.append(lines[index].strip())
        index += 1
    return block


def case_callout():
    """Case 1: the eight module READMEs open with the same stamped Known Gaps callout."""
    stamps = {}
    for module in MODULES:
        path = ROOT / "tools" / module / "README.md"
        block = opening_block(text_of(path))
        if not block or block[0] != "> [!WARNING]":
            fail(
                f"tools/{module}/README.md does not open with the Known Gaps callout: its "
                f"first block is {block[:1] or 'empty'}"
            )
        if len(block) < 2 or not STAMP.match(block[1]):
            fail(
                f"tools/{module}/README.md's callout carries no stamp: expected a line "
                f"' > **Known Gaps (verified <date> at `<sha>`).**', got "
                f"{block[1] if len(block) > 1 else 'nothing'}"
            )
        stamps[module] = "\n".join(block)
    distinct = set(stamps.values())
    if len(distinct) != 1:
        differing = sorted(module for module, block in stamps.items() if block != stamps[MODULES[0]])
        fail(
            "the eight module READMEs carry different Known Gaps banners, so a reader cannot "
            "tell which one is current: "
            + ", ".join(f"tools/{module}/README.md" for module in differing)
        )
    stamp = re.search(r"verified [^`]+`[0-9a-f]+`", stamps[MODULES[0]])
    ok(f"every module README opens with the same stamped Known Gaps callout "
       f"({stamp.group(0) if stamp else stamps[MODULES[0]]})")


def case_no_flag_table():
    """Case 2: no document carries a flag table, in either spelling the design bans."""
    for path in DOCUMENTS:
        relative = path.relative_to(ROOT)
        for number, line in enumerate(text_of(path).splitlines(), start=1):
            stripped = line.strip()
            if OPTION_LINE.match(line):
                fail(
                    f"{relative}:{number} is an option list, which is a flag table by "
                    f"another name: name the face and let `--help` carry the flags. "
                    f"Line: {stripped}"
                )
            if not stripped.startswith("|"):
                continue
            cells = [cell.strip() for cell in stripped.strip("|").split("|")]
            if any(cell.startswith("--") for cell in cells):
                fail(
                    f"{relative}:{number} lists a flag in a table, which is the page that "
                    f"disagrees with the binary a month later: name the face and let "
                    f"`--help` carry the flags. Line: {stripped}"
                )
            if any(FLAG_HEADER.search(cell) for cell in cells):
                fail(
                    f"{relative}:{number} is a flag table: name the face and let `--help` "
                    f"carry the flags. Line: {stripped}"
                )
    ok(f"no document carries a flag table ({len(DOCUMENTS)} documents)")


def inline_spans(text):
    """Every backtick span in a document, allowing a newline inside one and nothing else."""
    spans, index = [], 0
    while True:
        start = text.find("`", index)
        if start < 0:
            return spans
        end = text.find("`", start + 1)
        if end < 0:
            return spans
        body = text[start + 1 : end]
        if "\n\n" not in body:
            spans.append(body)
        index = end + 1


def code_blobs(text):
    """Every fenced block's body and every inline span: the places a command is written."""
    return [*FENCE.findall(text), *inline_spans(FENCE.sub("", text))]


def words_of(blob):
    """One token list per non-empty line of a blob, with punctuation trimmed."""
    for line in blob.splitlines():
        words = [token.strip("`*.,;:()[]") for token in line.split()]
        words = [word for word in words if word]
        if words:
            yield words


def documented(words):
    """The invocation a token list names, or None when it names no face.

    A documented invocation is `tillr <noun> <face>` or `tillr-<noun> <face>`, and it ends
    at the first word that is a flag or a placeholder: this suite asks the binary what the
    rest means, so a document never has to spell a flag to be checked.
    """
    head = words[0]
    if head == "tillr":
        if len(words) < 3 or words[1] not in NOUNS:
            return None
        prefix, rest = [head, words[1]], words[2:]
    elif head in BINARY.values():
        if len(words) < 2:
            return None
        prefix, rest = [head], words[1:]
    else:
        return None
    kept = []
    for word in rest:
        if word.startswith(("-", "<", "$", "{")):
            break
        kept.append(word)
    if not kept:
        return None
    return [*prefix, *kept]


def invocations(path):
    """Every documented invocation in one document, as argv, in the order it appears."""
    found = []
    for blob in code_blobs(text_of(path)):
        for words in words_of(blob):
            argv = documented(words)
            if argv is not None:
                found.append(tuple(argv))
    return found


def build(directory):
    """Build the eight binaries into one directory, which is what `PATH` will hold."""
    for module, binary in sorted(BINARY.items(), key=lambda item: item[1]):
        built = subprocess.run(
            ["go", "build", "-o", str(directory / binary), "."],
            cwd=ROOT / "tools" / module,
            capture_output=True,
        )
        if built.returncode != 0:
            fail(
                f"building {binary} from tools/{module} failed:\n"
                + built.stderr.decode(errors="replace")
            )


def case_faces_answer_help(directory):
    """Case 3: every documented face answers `--help` with exit 0 and its own flag set."""
    environment = dict(os.environ)
    environment["PATH"] = str(directory) + os.pathsep + environment.get("PATH", "")
    # Nothing here reads a store, but a face that derived one anyway must not reach the
    # owner's board: the loop's own suites scrub the same two variables for the same reason.
    environment["BOARD_DB"] = ""
    environment["XDG_STATE_HOME"] = str(directory / "state")

    seen, checked = set(), 0
    for path in DOCUMENTS:
        relative = path.relative_to(ROOT)
        for argv in invocations(path):
            if argv in seen:
                continue
            seen.add(argv)
            command = [str(directory / argv[0]), *argv[1:], "--help"]
            ran = subprocess.run(
                command, cwd=directory, env=environment, capture_output=True, timeout=HELP_TIMEOUT
            )
            face = argv[-1]
            if ran.returncode != 0:
                fail(
                    f"{relative} documents `{' '.join(argv)}`, which does not answer its "
                    f"`--help`: exit {ran.returncode}\n"
                    f"stderr: {ran.stderr.decode(errors='replace').strip()}\n"
                    "A face a document names is a face the binary must have."
                )
            if face not in ran.stdout.decode(errors="replace"):
                fail(
                    f"{relative} documents `{' '.join(argv)}`, whose `--help` does not name "
                    f"the face:\n{ran.stdout.decode(errors='replace').strip()}"
                )
            checked += 1
    if not checked:
        fail(
            "no documented invocation was found in any module README or in the "
            "tillr-operating skill, so this suite asserted nothing"
        )
    ok(f"every documented face answers `--help` with its own flag set "
       f"({checked} invocations over {len(DOCUMENTS)} documents)")


# Only an already-executed historical record may keep a disabling invocation.
# Scripts, recipes, and skills are instructions and can never be exempted.
ROLLING_HISTORY = {
    "docs/design/tillr-claude-backend.md":
        "Records the 2026-11-16 scratch drain measurement with rolling checks off.",
}


def python_command_blobs(text):
    """Read literal command values and their local append/extend operations, never execute."""
    try:
        tree = ast.parse(text)
    except (SyntaxError, ValueError):
        return []
    blobs = []

    class Commands(ast.NodeVisitor):
        def __init__(self):
            self.values = {}

        def value(self, node):
            if isinstance(node, ast.Constant) and isinstance(node.value, str):
                return node.value
            if isinstance(node, ast.Name):
                return self.values.get(node.id)
            if isinstance(node, (ast.List, ast.Tuple)):
                variants = [[]]
                for part in node.elts:
                    value = self.value(part)
                    choices = value if isinstance(value, tuple) else (value,)
                    variants = [prefix + [choice if isinstance(choice, str) else "<dynamic>"]
                                for prefix in variants for choice in choices]
                return variants[0] if len(variants) == 1 else tuple(variants)
            if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Add):
                left, right = self.value(node.left), self.value(node.right)
                lefts = left if isinstance(left, tuple) else (left,)
                rights = right if isinstance(right, tuple) else (right,)
                variants = []
                for one in lefts:
                    for two in rights:
                        if isinstance(one, list):
                            variants.append(one + (two if isinstance(two, list) else ["<dynamic>"]))
                        elif isinstance(one, str) and isinstance(two, str):
                            variants.append(one + two)
                if variants:
                    return variants[0] if len(variants) == 1 else tuple(variants)
            return None

        def emit(self, value):
            if isinstance(value, tuple):
                for alternative in value:
                    self.emit(alternative)
            elif isinstance(value, list):
                blobs.append(shlex.join(value))
            elif isinstance(value, str):
                blobs.append(value)

        def visit_Assign(self, node):
            self.generic_visit(node)
            value = self.value(node.value)
            for target in node.targets:
                if isinstance(target, ast.Name):
                    self.values[target.id] = value

        def visit_AnnAssign(self, node):
            self.generic_visit(node)
            if isinstance(node.target, ast.Name):
                self.values[node.target.id] = self.value(node.value)

        def visit_AugAssign(self, node):
            self.generic_visit(node)
            if isinstance(node.target, ast.Name) and isinstance(node.op, ast.Add):
                left, right = self.values.get(node.target.id), self.value(node.value)
                if isinstance(left, list) and isinstance(right, list):
                    left.extend(right)
                elif isinstance(left, str) and isinstance(right, str):
                    self.values[node.target.id] = left + right

        def visit_If(self, node):
            self.visit(node.test)
            if isinstance(node.test, ast.Constant):
                selected = node.body if node.test.value else node.orelse
                for statement in selected:
                    self.visit(statement)
                return
            # Both unknown branches are possible, but neither may overwrite the
            # other's command value. Tuples here hold alternatives, not argv.
            incoming = copy.deepcopy(self.values)
            for statement in node.body:
                self.visit(statement)
            yes = self.values
            self.values = incoming
            for statement in node.orelse:
                self.visit(statement)
            no = self.values
            self.values = {}
            for name in yes.keys() | no.keys():
                left, right = yes.get(name), no.get(name)
                if left == right:
                    self.values[name] = left
                else:
                    self.values[name] = ((left if isinstance(left, tuple) else (left,))
                                         + (right if isinstance(right, tuple) else (right,)))

        def visit_FunctionDef(self, node):
            outer = self.values
            self.values = copy.deepcopy(outer)
            for argument in [*node.args.posonlyargs, *node.args.args, *node.args.kwonlyargs]:
                self.values.pop(argument.arg, None)
            for statement in node.body:
                self.visit(statement)
            self.values = outer

        visit_AsyncFunctionDef = visit_FunctionDef

        def visit_Return(self, node):
            # Keepers return the composed argv to a launch helper.
            self.generic_visit(node)
            self.emit(self.value(node.value))

        def visit_Call(self, node):
            self.generic_visit(node)
            if not isinstance(node.func, ast.Attribute):
                return
            command = (node.args[0] if node.args else
                       next((keyword.value for keyword in node.keywords
                             if keyword.arg == "args"), None))
            if command is None:
                return
            argument = self.value(command)
            if isinstance(node.func.value, ast.Name):
                value = self.values.get(node.func.value.id)
                alternatives = value if isinstance(value, tuple) else (value,)
                for argv in alternatives:
                    if isinstance(argv, list):
                        if node.func.attr == "append" and isinstance(argument, str):
                            argv.append(argument)
                        elif node.func.attr == "extend" and isinstance(argument, list):
                            argv.extend(argument)
            if node.func.attr in ("run", "Popen", "call", "check_call", "check_output", "system"):
                self.emit(argument)

    Commands().visit(tree)
    return blobs


def rolling_disabled(text, markdown=False, python_script=False):
    """Return disabling loop commands, not mentions of the flag or quoted fixtures."""
    hits = []
    blobs = [text, *code_blobs(text)] if markdown else [text]
    if not markdown and (python_script or re.match(r"^#![^\n]*python", text)):
        blobs.extend(python_command_blobs(text))
    for blob in blobs:
        # Shell continuations belong to the same command, including in recipes.
        for line in re.sub(r"\\\r?\n", " ", blob).splitlines():
            try:
                lexer = shlex.shlex(line, posix=True, punctuation_chars=";&|()")
                lexer.whitespace_split = True
                words = list(lexer)
            except ValueError:
                # A prose/source line with unmatched quotes is not a shell command.
                continue
            for index, word in enumerate(words):
                if word.rsplit("/", 1)[-1] == "tillr-drain" and words[index + 1:index + 2] == ["loop"]:
                    start = index + 2
                elif word == "tillr" and words[index + 1:index + 3] == ["drain", "loop"]:
                    start = index + 3
                else:
                    continue
                for position in range(start, len(words)):
                    token = words[position]
                    if token in (";", "&&", "||", "|", ")"):
                        break
                    if token == "--rolling=" or (
                        token == "--rolling" and words[position + 1:position + 2] == [""]
                    ):
                        hits.append(line.strip())
    return hits


def rolling_policy_errors(files, history=ROLLING_HISTORY):
    """Check tracked working bytes, including staged additions, and stale exceptions."""
    hits = {name: rolling_disabled(body, name.endswith(".md"), name.endswith(".py"))
            for name, body in files.items()}
    errors = []
    for name, commands in hits.items():
        if commands and pack_docs.source_of(name) not in history:
            errors.append(f"{name}: disables the after-merge check: {commands[0]}")
    for name, reason in history.items():
        if not name.startswith("docs/") or not name.endswith(".md") or "/skills/" in name:
            errors.append(f"{name}: only historical documents may be exempted")
        if not reason.strip():
            errors.append(f"{name}: historical exception has no reason")
        if not hits.get(name):
            errors.append(f"{name}: stale historical exception (no disabling loop command)")
    return errors


def case_rolling_guard_fixtures():
    unrelated = ('#!/usr/bin/env python3\n'
                 'subprocess.run(["tillr-drain", "loop"])\n'
                 'subprocess.run(["other-tool", "--rolling", ""])\n')
    if rolling_policy_errors({"bin/keeper": unrelated}, {}):
        fail("rolling guard associated another tool's options with the drain")
    scoped = ('#!/usr/bin/env python3\ncommand = ["tillr-drain", "loop"]\n'
              'def unused():\n    command.append("--rolling=")\n'
              'subprocess.run(command)\n')
    if rolling_policy_errors({"bin/keeper": scoped}, {}):
        fail("rolling guard leaked mutations from another function scope")
    for command in (
        "tillr-drain loop --rolling ''",
        'exec /opt/bin/tillr-drain loop --rolling=""',
        'tillr-drain loop \\\n  --rolling ""',
        "tillr drain loop --rolling=",
        '#!/usr/bin/env python3\ndef loop_command():\n    return ["tillr-drain", "loop", "--rolling", ""]',
        '#!/usr/bin/env python3\ncommand = ["tillr-drain", "loop"]\ncommand.extend(["--rolling", ""])\nsubprocess.run(command)',
        '#!/usr/bin/env python3\nsubprocess.run("tillr-drain loop --rolling \'\'", shell=True)',
        '#!/usr/bin/env python3\ncommand = ["tillr-drain", "loop"]\ncommand.append("--rolling=")\nsubprocess.run(command)',
        '#!/usr/bin/env python3\ncommand = "tillr-drain loop --rolling \'\'"\nsubprocess.run(command, shell=True)',
        '#!/usr/bin/env python3\nsubprocess.run(args=["tillr-drain", "loop", "--rolling", ""])',
        '#!/usr/bin/env python3\nif True:\n    command = ["tillr-drain", "loop", "--rolling", ""]\nelse:\n    command = ["tillr-drain", "loop"]\nsubprocess.run(command)',
        '#!/usr/bin/env python3\nif unknown:\n    command = ["tillr-drain", "loop", "--rolling", ""]\nelse:\n    command = ["tillr-drain", "loop"]\nsubprocess.run(command)',
        '#!/usr/bin/env python3\nif unknown:\n    rolling = ""\nelse:\n    rolling = "python3 gate.py"\nsubprocess.run(["tillr-drain", "loop", "--rolling", rolling])',
    ):
        errors = rolling_policy_errors({"bin/keeper": command}, {})
        if not errors or "bin/keeper" not in errors[0]:
            fail(f"rolling guard missed the disabling keeper command: {command!r}")
    overwritten = ('#!/usr/bin/env python3\n'
                   'command = ["tillr-drain", "loop", "--rolling", ""]\n'
                   'command = ["tillr-drain", "loop"]\nsubprocess.run(command)\n')
    if rolling_policy_errors({"bin/keeper": overwritten}, {}):
        fail("rolling guard treated a superseded assignment as an invocation")
    if not rolling_policy_errors({"docs/example.md": "tillr-drain loop --rolling ''"}, {}):
        fail("rolling guard missed an unfenced document command")
    if rolling_policy_errors({"bin/keeper": "tillr-drain loop"}, {}):
        fail("rolling guard rejected the default enabled check")
    if rolling_policy_errors({"bin/keeper": "tillr-drain loop --rolling 'python3 gate.py'"}, {}):
        fail("rolling guard rejected an explicit nonempty check")
    historical = "docs/design/past.md"
    body = "```sh\ntillr-drain loop --rolling ''\n```"
    if rolling_policy_errors({historical: body}, {historical: "An already executed run."}):
        fail("rolling guard rejected a named historical run")
    if not rolling_policy_errors({historical: "tillr-drain loop"}, {historical: "Past run."}):
        fail("rolling guard accepted a stale historical exception")
    # A delivered copy is judged as its source: excused where the source is, and only there.
    copy_of = "yolo-packs/tillr/files/local/share/tillr/" + historical
    if rolling_policy_errors({historical: body, copy_of: body}, {historical: "An already executed run."}):
        fail("rolling guard refused the delivered copy of a named historical run")
    other_copy = "yolo-packs/tillr/files/local/share/tillr/docs/design/other.md"
    if not rolling_policy_errors({historical: body, other_copy: body}, {historical: "An already executed run."}):
        fail("rolling guard excused the delivered copy of a document with no exception")
    for name in ("bin/keeper", "yolo-packs/tillr/skills/keeper/SKILL.md", ".justfile"):
        if not rolling_policy_errors({name: body}, {name: "Not a historical record."}):
            fail(f"rolling guard allowed an instruction exception: {name}")
    ok("rolling guard catches disabled commands and stale or instructional exceptions")


def case_rolling_enabled():
    tracked = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT,
                             check=True, capture_output=True).stdout
    files = {}
    for raw in tracked.split(b"\0"):
        if not raw:
            continue
        name = os.fsdecode(raw)
        path = ROOT / name
        if not path.is_file():
            continue
        try:
            files[name] = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
    errors = rolling_policy_errors(files)
    if errors:
        fail("\n".join(errors))
    ok(f"tracked loop invocations keep the after-merge check on ({len(files)} text files; "
       f"{len(ROLLING_HISTORY)} named historical exception)")


def recovery_errors(text):
    """Recovery keeps the original item's identity and never names a replacement id."""
    section = re.search(r"^## Recovery\s*\n(.*?)(?=^## |\Z)", text, re.M | re.S)
    if not section:
        return ["operating skill has no Recovery section"]
    body = section.group(1)
    errors = []
    if "tillr drain retry" not in body or "own id" not in body:
        errors.append("Recovery must retry the failed item under its own id")
    if re.search(r"\bitem-[a-z0-9-]+-v2\b", body):
        errors.append("Recovery names a cloned -v2 item id")
    forbidden = (
        (r"\bscript\w*\b|\b(?:python\d*|bash|sh)\s+[^\n`]+|\S+\.(?:py|sh)\b", "a recovery script"),
        (r"\bsqlite\w*\b|\b(?:edit|update|modify|write)\b[^\n.]*\bstore\b|\b(?:UPDATE|DELETE FROM|INSERT INTO)\b", "a direct store edit"),
        (r"\bgit\s+reset\b", "git reset in a lane"),
    )
    for pattern, operation in forbidden:
        if re.search(pattern, body, re.I):
            errors.append(f"Recovery names {operation}; use a supported tillr face")
    return errors


def case_recovery():
    specimen = "## Recovery\nRetry under its own id with `tillr drain retry`.\n"
    if recovery_errors(specimen):
        fail("same-id recovery fixture was refused")
    if not recovery_errors(specimen + "Use `item-failed-v2`.\n"):
        fail("recovery guard accepted a cloned -v2 item id")
    for instruction in (
        "Run a recovery script.",
        "Use `python3 recover.py`.",
        "Edit the store directly.",
        "Run `sqlite3 state.db 'UPDATE lanes SET state=idle'`.",
        "Run `git reset --hard main` in the lane.",
    ):
        if not recovery_errors(specimen + instruction + "\n"):
            fail(f"recovery guard accepted an unsupported hand operation: {instruction}")
    errors = recovery_errors(text_of(SKILL))
    if errors:
        fail("\n".join(errors))
    ok("Recovery uses supported commands under the same item id; cloned ids, scripts, store edits and git reset fixtures refused")


def case_comment_intake_documented():
    expected = ("tillr", "item", "comments", "take")
    for path in (ROOT / "tools/tillr-item/README.md", SKILL):
        if expected not in invocations(path):
            fail(f"{path.relative_to(ROOT)} omits the hand-driven comment intake command")
    ok("hand-driven comment intake is documented in the item README and operating skill")


def case_designer_session_selection_documented():
    for path in (ROOT / "tools/tillr-drain/README.md", SKILL):
        text = text_of(path)
        if "#choosing-at-dispatch" not in text or "40,000" not in text:
            fail(f"{path.relative_to(ROOT)} omits the session selection policy")
    adapter = ROOT / "tools/tillr-run/backends/pi/compact"
    result = subprocess.run([str(adapter), "--help"], capture_output=True, text=True)
    if result.returncode or "--session-id" not in result.stdout:
        fail("the documented Pi compact step must answer --help and accept a session id")
    ok("session selection and the Pi compact step are documented")


def main():
    cleanup_tests = subprocess.run(
        [sys.executable, str(ROOT / "tests/tillr-scratch.test.py")],
        env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
    )
    if cleanup_tests.returncode:
        fail("the TestMain scratch cleanup regression tests failed")
    case_designer_session_selection_documented()
    case_comment_intake_documented()
    case_recovery()
    case_rolling_guard_fixtures()
    case_rolling_enabled()
    # The operating skill documents the repository keeper too. Keep its behavior
    # in this permanent gate suite without invoking a real loop or store.
    keeper_tests = subprocess.run(
        [sys.executable, str(ROOT / "tests/tillr-keep.test.py")],
        env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
    )
    if keeper_tests.returncode:
        fail("the repository keeper's regression tests failed")
    case_callout()
    case_no_flag_table()
    scratch = Path(tempfile.mkdtemp(dir=os.environ.get("YOLO_DURABLE_DIR")))
    try:
        build(scratch)
        case_faces_answer_help(scratch)
    finally:
        shutil.rmtree(scratch, ignore_errors=True)
    print("the eight module READMEs state their judgment, and every face they name exists")
    return 0


if __name__ == "__main__":
    sys.exit(main())
