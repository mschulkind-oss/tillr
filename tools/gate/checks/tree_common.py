"""Whole-tree Markdown convention guards, migrated from fork-check."""
import os
from pathlib import Path
import re
import subprocess

def git(root, *args):
    return subprocess.run(["git", "-C", str(root), *args], check=True, capture_output=True, text=True).stdout.strip()

DOCUMENT_EXCLUDED = frozenset({".forks", "node_modules", ".yolo"})


SCRATCH = re.compile(r"\.yolo/durable|YOLO_DURABLE_DIR")


SCRATCH_EXCEPTIONS = (
    # A tool's intended output directory (the storybook's `just` recipe names it).
    ("tools/footer-storybook/README.md", "$YOLO_DURABLE_DIR/footer-storybook"),
    # pi-profiler's documented output directory: the `-o` default and the two prose lines
    # that say where an artifact lands (never `/tmp`). The three citing lines read
    # differently, so each keeps its own marker -- its own line's text -- and the
    # exception cannot grow to cover a different line in that file.
    ("tools/pi-profiler/README.md", "$YOLO_DURABLE_DIR/pi-profiler/<stamp>-<pid>"),
    ("tools/pi-profiler/README.md", "Artifacts go to `-o`, or under `$YOLO_DURABLE_DIR`"),
    ("tools/pi-profiler/README.md", "Artifacts default to a stamped directory under `$YOLO_DURABLE_DIR`"),
    # The configured PI_SUBAGENTS_WORKTREE_DIR value, at its own reference and in the
    # shipped pi rule that repeats it (so the edit has one home).
    ("docs/research/agent-worktree-lifecycle.md", "/workspace/.yolo/durable/worktrees/pi-subagents"),
    ("docs/pi/agent-work-control.md", "PI_SUBAGENTS_WORKTREE_DIR=$YOLO_DURABLE_DIR/worktrees"),
    ("yolo-packs/matt/briefing/work-rules.md", "PI_SUBAGENTS_WORKTREE_DIR=$YOLO_DURABLE_DIR/worktrees"),
    # `just tillr-build`'s documented output directory: the section states where a source
    # build of the tillr programs lands, which is a location by design rather than evidence
    # of a past claim. The marker is that line's own text.
    ("docs/design/tillr-distribution.md", "$YOLO_DURABLE_DIR/bin"),
    # The evidence-location rule: it names where an agent PUTS evidence, not what proved a
    # past claim. Its marker is that line's own text, so the exception cannot grow to cover
    # a different line in the file.
    ("yolo-packs/matt/briefing/work-rules.md", "repository or under `$YOLO_DURABLE_DIR`; agent scratch and working files never go in"),
    ("yolo-packs/matt/files/pi-rules.md", 'git worktree add --lock "$YOLO_DURABLE_DIR/worktrees/<task>"'),
    ("docs/pi/system-prompts.md", 'git worktree add --lock "$YOLO_DURABLE_DIR/worktrees/<task>"'),
    # The coordinator spill directory its reference documents (that file is a separate
    # lane's work, so this gate must not refuse it).
    ("docs/reference/work-control.md", "$YOLO_DURABLE_DIR/work-control/bash-output"),
    # The owner-answer journal's directory, documented the same way in the same file as a
    # designed location (one per whale session, read by the owner), not evidence of a past
    # claim.
    ("docs/reference/work-control.md", "$YOLO_DURABLE_DIR/journals"),
    # The scratch-clone directory a fork rebase is made in: the skill names where to
    # clone by design (never `/tmp`), so this is a location, not evidence of a past
    # claim. The skill is repo-owned at `.pi/skills/`, and this gate's exceptions live
    # with the gate. Each marker is
    # the text of one line, not the bare token, so the exception can never grow to
    # cover a different line in that file that cites scratch.
    (".agents/skills/fork-series-evergreen/SKILL.md", "Fresh scratch clone under `$YOLO_DURABLE_DIR`"),
    (".agents/skills/fork-series-evergreen/SKILL.md", "$YOLO_DURABLE_DIR/rebase-"),
    # The same skill's lane-checkout line: it names where a lane puts its own clone and
    # where a `patches/` directory goes by design, so it is a location too. It cites
    # scratch on a line of its own, which is why the two markers above do not cover it.
    (".agents/skills/fork-series-evergreen/SKILL.md",
     "under `$YOLO_DURABLE_DIR`, and leave any `patches/` directory to `export`"),
    # The land-queue suite's scratch base: the doc tells a lane running beside others to
    # give the suite its own, because `tests/land-queue.test.sh` derives the base from
    # `${YOLO_DURABLE_DIR:-$PWD/.land-queue-tests}`. That names the variable to set, not
    # an artifact that proved a past claim, so the prose keeps it. The marker is the
    # phrase as that sentence wraps in the document, not the whole sentence: the
    # document is wrapped, and a marker that spans the wrap matches no line at all.
    ("docs/pi/landing-queue.md", "`YOLO_DURABLE_DIR` when lanes run beside each other"),
    # Where a Go test's scratch lives by design -- the harness convention that every
    # suite under tools/ follows -- and the reference's directory table, whose worktree
    # row names the helper that returns that directory. Each is a location a reader is
    # being told about, never an artifact that proved a past claim, and each marker is
    # that line's own text so the exception cannot grow to cover a neighbouring line.
    ("docs/design/board-state-machine-plan.md",
     "Test scratch lives under `$YOLO_DURABLE_DIR` via `scratch(t, prefix)`"),
    ("docs/reference/landing-system.md",
     "| Lane worktrees | `durableWorktreesDir()`, under `$YOLO_DURABLE_DIR` |"),
    # The operator's own binary: the hand-drain card and the runbook each pin the path of
    # the board built from the trunk, because a stale binary refuses the store rather than
    # misreporting. That is where the command a reader types lives, not an artifact that
    # proved a past claim, and each marker is its own line's text so the exception cannot
    # grow to cover a neighbouring line.
    ("docs/design/hand-drain-tactics-2026-10-31.md",
     "Every `board` below means `/workspace/.yolo/durable/bin/board`"),
    ("docs/reference/landing-runbook.md",
     "`/workspace/.yolo/durable/bin/board`. A stale binary does not misreport: it refuses"),
    # The tillr lane plan and implementation plan name where a lane's scratch and a
    # fixture's store go by design (never `/tmp`), and the plan's digest finding quotes
    # the environment value that made a digest irreproducible. Each is a location or a
    # quoted value a reader is being told about, never an artifact that proved a past
    # claim, and each marker is that line's own text so the exception cannot grow to
    # cover a neighbouring line.
    ("docs/design/board-utilities-lanes.md",
     "`$YOLO_DURABLE_DIR`, never `/tmp`, and the report names the scratch paths it used"),
    ("docs/design/board-utilities-plan.md",
     "`$YOLO_DURABLE_DIR`, `storeFixture(t, prefix)` returns"),
    ("docs/design/board-utilities-plan.md",
     "`land-queue`'s `YOLO_DURABLE_DIR` differs per process"),
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
            if any(path == allowed and marker in line for allowed, marker in SCRATCH_EXCEPTIONS):
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

