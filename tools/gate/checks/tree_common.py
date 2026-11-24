"""Whole-tree Markdown convention guards."""
import os
from pathlib import Path
import re
import subprocess

def git(root, *args):
    return subprocess.run(["git", "-C", str(root), *args], check=True, capture_output=True, text=True).stdout.strip()

DOCUMENT_EXCLUDED = frozenset({".forks", "node_modules", ".yolo"})


SCRATCH = re.compile(r"\.yolo/durable|YOLO_DURABLE_DIR")


SCRATCH_EXCEPTIONS = (
    # `just tillr-build`'s documented output directory: the section states where a source
    # build of the tillr programs lands, which is a location by design rather than evidence
    # of a past claim. The marker is that line's own text.
    ("docs/reference/tillr-distribution.md", "$YOLO_DURABLE_DIR/bin"),
    # The observatory recipe's build directory: tools/observatory/run.sh builds the two
    # programs into that directory by design, and the README says where, so a reader can
    # find the binaries. It is a location, not an artifact that proved a past claim. The
    # pack's mirror of the README carries the same line and is excused by `excused_as`. The
    # marker is that line's own text so the exception cannot grow to cover a neighbouring line.
    ("tools/tillr-view/README.md",
     "tillr-observatory/<key>/`, where `<key>`"),
    # The observatory's performance log directory: tools/observatory/run.sh hands serve that
    # directory by design and the README says where, so a reader can find the log. It is a
    # location, not an artifact that proved a past claim; the pack's mirror is excused by
    # `excused_as`, and the marker is that line's own text.
    ("tools/tillr-view/README.md",
     "`<checkout>/.yolo/durable/tillr-observatory/logs/<hostname>-<port>/` and prints it at"),
)


DESIGN_CITATION_FIELDS = ("depends-on", "superseded-by", "supersedes")


DESIGN_CITATION_DOC = re.compile(r"\.(?:md|markdown)(?:#.*)?$", re.IGNORECASE)


DESIGN_CITATION_ROOTS = ("", "docs", "docs/design")


MARKDOWN_HEADING = re.compile(r"^#{1,6}\s+(.*)$")


MARKDOWN_HTML_ANCHOR = re.compile(r'<a\s+[^>]*(?:id|name)="([^"]+)"')


MARKDOWN_VANTAGE_QUESTION = re.compile(r"<!--\s*vantage:\s*question\s+id=([^\s]+)")


def tracked_markdown(root: Path) -> list[str]:
    """Every tracked Markdown file, repo-relative (which excludes `.forks/`, node_modules
    and `.yolo/` for free -- none are tracked).
    """
    return [path for path in git(root, "ls-files", "*.md").split("\n") if path]


# The tillr pack delivers copies under this prefix: the module mirror (`just pack-sync`,
# `tools/<module>/x` at `<module>/x`) and the design documentation snapshot (`just
# pack-docs`, `docs/x` at `docs/x`). A copy carries its source's lines, so it is excused
# exactly where its source is. This is `source_of` in tools/pack-docs.py, restated because a
# gate check reads nothing outside its own directory; tests/pack-sync.test.py asserts the
# two agree.
DELIVERED_PREFIX = "yolo-packs/tillr/files/local/share/tillr/"
DELIVERED_DOCS_PREFIX = DELIVERED_PREFIX + "docs/"


def excused_as(path: str) -> str:
    """The path whose SCRATCH_EXCEPTIONS apply to `path`: its source, for a delivered copy."""
    if not path.startswith(DELIVERED_PREFIX):
        return path
    rest = path[len(DELIVERED_PREFIX):]
    return rest if rest.startswith("docs/") else "tools/" + rest


def scratch_citations(root: Path) -> list[str]:
    """Every tracked Markdown line that cites scratch, outside a fenced specimen.

    One `path:line: text` per finding. Fenced blocks are skipped whole -- a command or
    a transcript in a fence is a specimen, not a reference -- and `SCRATCH_EXCEPTIONS`
    allows the documented locations. There is no `--all`: the guard is repository-wide,
    because a convention the tree already violates is not one a change can be trusted
    to leave alone.
    """
    findings: list[str] = []
    for path in tracked_markdown(root):
        text = (root / path).read_text(encoding="utf-8", errors="replace")
        in_fence = False
        for number, line in enumerate(text.splitlines(), 1):
            if line.lstrip().startswith(("```", "~~~")):
                in_fence = not in_fence
                continue
            if in_fence or not SCRATCH.search(line):
                continue
            if any(excused_as(path) == allowed and marker in line
                   for allowed, marker in SCRATCH_EXCEPTIONS):
                continue
            findings.append(f"{path}:{number}: {line.strip()}")
    return findings


def frontmatter_entries(text: str) -> list[tuple[int, str]]:
    """A document's YAML frontmatter as `(line number, line)`, or nothing.

    Frontmatter is the block between the first `---` and the next one, and this reads it
    the way the corpus writes it: a header of one-line fields, with block lists under a
    field that has no value of its own. A document with no frontmatter cites nothing
    this guard can read, and one whose block never closes has no frontmatter either --
    `uvx vantage-check` judges the malformed document, not this guard.
    """
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return []
    for index in range(1, len(lines)):
        if lines[index].strip() == "---":
            return [(number, lines[number - 1]) for number in range(2, index + 1)]
    return []


def frontmatter_citations(entries: list[tuple[int, str]]) -> list[tuple[int, str, str]]:
    """Every document citation a frontmatter block declares, as `(line, field, value)`.

    A field is one scalar (`depends-on: a.md`), one inline list (`depends-on: [a.md,
    b.md]`) or a block list (the field with no value, then `- a.md` lines), and a value
    keeps its quotes stripped and its trailing comment dropped. A block list belongs to
    the field that opened it and ends at the next field, so an indented continuation of
    some other field is never read as a citation.
    """
    citations: list[tuple[int, str, str]] = []
    field: str | None = None
    for number, line in entries:
        stripped = line.strip()
        name, separator, value = stripped.partition(":")
        if separator and not stripped.startswith("-"):
            field = name.strip() if name.strip() in DESIGN_CITATION_FIELDS else None
            if field is None:
                continue
            value = value.strip()
            if value.startswith("[") and value.endswith("]"):
                for part in value[1:-1].split(","):
                    candidate = part.strip().strip("\"'")
                    if candidate:
                        citations.append((number, field, candidate))
            elif value:
                citations.append((number, field, strip_yaml_comment(value)))
            continue
        if field is None:
            continue
        item = re.match(r"^-\s+(.*)$", stripped)
        if item:
            candidate = strip_yaml_comment(item.group(1).strip())
            if candidate:
                citations.append((number, field, candidate))
            continue
        # Any other line at this level closes the block list; a deeper indent is a
        # continuation of the item above, which is prose in a field this guard reads as
        # one token and therefore does not resolve.
        if stripped and not line.startswith((" ", "\t")):
            field = None
    return citations


def strip_yaml_comment(value: str) -> str:
    """A frontmatter scalar without its trailing ` # comment` and without quotes."""
    comment = value.find(" #")
    if comment >= 0:
        value = value[:comment]
    return value.strip().strip("\"'")


def markdown_anchors(text: str) -> set[str]:
    """Every anchor a document declares: heading slugs, HTML ids and vantage questions.

    The three forms are the ones the corpus uses, and the same three the retired
    board CLI's `anchorPresent` accepts -- a citation
    that resolves for the gate must resolve for the doctor, or the two halves of one
    mechanism would disagree about what "the section exists" means.
    """
    anchors: set[str] = set()
    for line in text.splitlines():
        heading = MARKDOWN_HEADING.match(line)
        if heading:
            anchors.add(markdown_slug(heading.group(1)))
        for match in MARKDOWN_HTML_ANCHOR.findall(line):
            anchors.add(match.lower())
        for match in MARKDOWN_VANTAGE_QUESTION.findall(line):
            anchors.add(match.lower())
    return anchors


def markdown_slug(heading: str) -> str:
    """A heading's GitHub-style anchor: lowercase, punctuation dropped, spaces joined.

    It is the same reduction the board CLI applies (`slugifyHeading`), so a `#section`
    that resolves here resolves there.
    """
    kept = "".join(
        character for character in heading.strip().lower()
        if character.isalnum() or character in "- "
    )
    return "-".join(kept.split())


def design_citation_findings(root: Path) -> list[str]:
    """Every frontmatter document citation that resolves to nothing.

    One `path:line: field: value: reason` per finding, over every tracked Markdown file:
    the guard is a repository convention rather than a diff, because a citation that
    already dangles is not one a change can be trusted to leave alone (the scratch guard
    reads the whole tree for the same reason). A citation is read as a `doc_path` and an
    optional `#anchor`; the path is resolved beside the citing document, at the
    repository root, then under `docs/` and `docs/design/`, and the anchor must be one of
    the target's headings, HTML ids or vantage questions.
    """
    findings: list[str] = []
    anchor_cache: dict[str, set[str] | None] = {}

    def anchors_of(relative: str) -> set[str] | None:
        if relative not in anchor_cache:
            try:
                anchor_cache[relative] = markdown_anchors(
                    (root / relative).read_text(encoding="utf-8", errors="replace"))
            except OSError:
                anchor_cache[relative] = None
        return anchor_cache[relative]

    for path in tracked_markdown(root):
        if path.startswith(DELIVERED_DOCS_PREFIX):
            # Delivered design copies (just pack-docs) are a commit-pinned snapshot
            # verified by tests/pack-sync.test.py against snapshot.json and excluded
            # in .vantage.toml; graduating an unshipped design on HEAD does not
            # rewrite historical snapshot frontmatter.
            continue
        try:
            text = (root / path).read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        for number, field, value in frontmatter_citations(frontmatter_entries(text)):
            if not DESIGN_CITATION_DOC.search(value):
                continue
            target, _, anchor = value.partition("#")
            resolved = None
            if target:
                for base in (os.path.dirname(path), *DESIGN_CITATION_ROOTS):
                    candidate = os.path.normpath(os.path.join(base, target))
                    if candidate.startswith("..") or candidate.startswith("/"):
                        continue
                    if (root / candidate).is_file():
                        resolved = candidate
                        break
            else:
                resolved = path
            if resolved is None:
                findings.append(
                    f"{path}:{number}: {field}: {value}: names no tracked document "
                    f"(looked beside the document, at the repository root, and under "
                    f"docs/ and docs/design/)"
                )
                continue
            if not anchor:
                continue
            present = anchors_of(resolved)
            if present is not None and anchor.lower() in present:
                continue
            findings.append(
                f"{path}:{number}: {field}: {value}: {resolved} has no such section "
                f"(no heading, HTML anchor or vantage question matches #{anchor})"
            )
    return findings

