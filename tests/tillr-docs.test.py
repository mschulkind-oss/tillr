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

import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

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


def main():
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
