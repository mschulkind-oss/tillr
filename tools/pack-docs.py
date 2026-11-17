#!/usr/bin/env python3
"""Deliver tillr's design documentation through the tillr pack, as a dated snapshot.

The tillr pack already ships the Go source of every `tools/tillr*` module under
`yolo-packs/tillr/files/local/share/tillr/<module>/` (see `just pack-sync`). This program
adds the documents an agent in ANOTHER workspace needs to know what tillr does today and
what it is going to build: the status index `docs/tillr/design-status.md` and every
document that index marks `shipped`. They land in a jail at `~/.local/share/tillr/`,
under the same repository-relative path with a leading `tools/` dropped, so
`docs/design/tillr-item-graphs.md` arrives at
`~/.local/share/tillr/docs/design/tillr-item-graphs.md` and `tools/tillr-loop/README.md`
at `~/.local/share/tillr/tillr-loop/README.md`.

WHY A SNAPSHOT, AND WHY IT IS NOT REFRESHED BY THE GATE. The module mirror is held to its
source on every gate run, which is affordable because a change to `tools/tillr*` is
fenced to include its mirror. A design document is different: the running drain edits
them on every landing (the "retire landed document block" commits), and a lane's fence
does not include this delivered tree. A gate that required the copies to match HEAD would
turn every landing that touched a delivered document red for every lane after it. So the
copies are a snapshot of one commit, recorded in `snapshot.json`:

* the gate's check (tests/pack-sync.test.py) proves each copy is exactly this program's
  transform of the blob the snapshot names -- a hand edit, a broken transform, a missing
  or stray file, or a live directive fails -- and that judgment depends on nothing a lane
  can change;
* freshness against HEAD is the `--fresh` mode of the same suite, run by `just pack-docs`
  after a refresh and by whoever wants to know how far the snapshot lags.

THE ONE DECLARATION. The list of shipped documents is the index itself: every Markdown
table row whose first cell links a document and whose last cell reads exactly `shipped`.
A row whose last cell reads `not shipped` is listed for the reader and left out. The sync
reads the index; the suite reads the delivered copy of the index and compares.

THE TRANSFORM, which is the whole difference between a source and its copy:

1. A one-line banner after the first heading says which file and commit this is.
2. Every relative link, outside fenced blocks, is re-resolved: a target that was delivered
   (a shipped document, or a file of a mirrored module) gets a relative link that resolves
   in the delivered tree; any other target becomes a link to the repository on GitHub.
3. Every `<!-- tillr: ... -->` directive outside a fenced block is made inert, so no
   sync -- here, or in a repository an agent copies a delivered document into -- reads a
   copy as a second declaration of an item. Vantage question directives stay live:
   Vantage refuses an open question without one (`vantage/oq-missing`), tillr reads a
   question only from the document an item names as its design, so a copy cannot hold or
   release an item, and `.vantage.toml` excludes this tree from the planning index, so the
   owner's Needs-you list never shows a copy's question.

Usage:
  pack-docs.py sync [--commit <rev>]   rewrite the delivered tree from <rev> (default HEAD)
  pack-docs.py list                    print the documents the index at HEAD declares
"""

import json
import os
import posixpath
import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
INDEX = "docs/tillr/design-status.md"
DELIVERED_ROOT = ROOT / "yolo-packs" / "tillr" / "files" / "local" / "share" / "tillr"
# Where the pack's `files` contribution puts DELIVERED_ROOT in a jail's home.
JAIL_ROOT = "~/.local/share/tillr"
SNAPSHOT = "docs/snapshot.json"
REPOSITORY = "https://github.com/mschulkind-oss/tillr/blob/main/"

# The modules `just pack-sync` mirrors. A link into one of them is a link into the
# delivered tree, whether or not this program copied the file. tests/pack-sync.test.py
# asserts this tuple equals its own MODULES, so the two lists cannot drift apart.
MIRRORED_MODULES = (
    "tillr",
    "tillr-cli",
    "tillr-drain",
    "tillr-gate",
    "tillr-item",
    "tillr-land",
    "tillr-run",
    "tillr-store",
    "tillr-view",
    "tillr-trace",
    "tillr-roll",
)

FENCE = ("```", "~~~")
INLINE_LINK = re.compile(r"\]\((?P<target>[^)\s]+)\)")
REFERENCE_LINK = re.compile(r"^(?P<lead> {0,3}\[[^\]\n]+\]:\s*)(?P<target>\S+)")
ABSOLUTE = re.compile(r"^(?:[a-z][a-z0-9+.-]*:|/|#)")
DIRECTIVE = re.compile(r"<!--(\s*)tillr:(\s*)")
TABLE_LINK = re.compile(r"\[[^\]]*\]\((?P<target>[^)\s#]+\.md)(?:#[^)\s]*)?\)")


def git(*args, binary=False):
    result = subprocess.run(["git", "-C", str(ROOT), *args], capture_output=True, check=True)
    return result.stdout if binary else result.stdout.decode("utf-8")


# --- the declaration ----------------------------------------------------------------


def declared(index_text, index_path=INDEX):
    """The repository paths the index marks `shipped`, in the order it lists them.

    A table row counts when its first cell links a Markdown document and its last cell is
    exactly `shipped` or `not shipped`; the first is declared. The index itself is always
    delivered and is not repeated here.
    """
    here = posixpath.dirname(index_path)
    found = []
    fenced = False
    for line in index_text.splitlines():
        if line.lstrip().startswith(FENCE):
            fenced = not fenced
            continue
        if fenced or not line.lstrip().startswith("|"):
            continue
        cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
        if len(cells) < 2 or cells[-1] != "shipped":
            continue
        link = TABLE_LINK.search(cells[0])
        if not link:
            continue
        path = posixpath.normpath(posixpath.join(here, link.group("target")))
        if path not in found and path != index_path:
            found.append(path)
    return found


def undecided_rows(index_text):
    """Table rows that link a document in their first cell but say neither `shipped` nor
    `not shipped` last, so a reader cannot tell whether the document was delivered."""
    rows = []
    fenced = False
    for number, line in enumerate(index_text.splitlines(), 1):
        if line.lstrip().startswith(FENCE):
            fenced = not fenced
            continue
        if fenced or not line.lstrip().startswith("|"):
            continue
        cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
        if len(cells) < 2 or not TABLE_LINK.search(cells[0]):
            continue
        if cells[-1] not in ("shipped", "not shipped"):
            rows.append(f"{number}: {line.strip()[:120]}")
    return rows


# --- where a file lands ------------------------------------------------------------


def mirrored(path):
    """Whether a repository path lies inside a module `just pack-sync` mirrors."""
    parts = path.split("/")
    return len(parts) >= 2 and parts[0] == "tools" and parts[1] in MIRRORED_MODULES


def destination(path):
    """Where a repository path lands under the delivered root: `tools/` is dropped, as the
    module mirror drops it, and everything else keeps its repository path."""
    return path[len("tools/"):] if path.startswith("tools/") else path


# The delivered root as a repository-relative prefix.
DELIVERED_PREFIX = "yolo-packs/tillr/files/local/share/tillr/"


def source_of(path):
    """THE RULE every check that reads tracked files applies to a delivered copy: a file
    under the delivered root is its source, so an allowance a check keys by path follows
    a copy to the source it was copied from. `destination` inverted: a copy under `docs/`
    is the same `docs/` path, and anything else (a module mirror file, the loop README) is
    under `tools/`. A path outside the delivered root is its own source.

    tools/gate/checks/tree_common.py carries the same rule as `excused_as`, because a gate
    check reads nothing outside its own directory; tests/pack-sync.test.py asserts the two
    agree."""
    if not path.startswith(DELIVERED_PREFIX):
        return path
    rest = path[len(DELIVERED_PREFIX):]
    return rest if rest.startswith("docs/") else "tools/" + rest


# --- the transform ------------------------------------------------------------------


def banner(source, commit, date):
    return (f"> **Delivered copy.** This is `{source}` from this repository at commit "
            f"`{commit[:10]}` ({date}), copied into the tillr pack by `just pack-docs`. The "
            f"[source]({REPOSITORY}{source}) is authoritative and may be newer. Links to "
            f"documents that were not delivered point at the repository, and the queue item "
            f"directives in this copy are inert.")


def rewrite_target(target, source, delivered):
    """One relative link target, re-resolved for the delivered tree."""
    if ABSOLUTE.match(target):
        return target
    path, hashmark, anchor = target.partition("#")
    if not path:
        return target
    trailing = "/" if path.endswith("/") else ""
    resolved = posixpath.normpath(posixpath.join(posixpath.dirname(source), path))
    if resolved.startswith("..") or resolved == ".":
        return target
    suffix = hashmark + anchor
    if resolved in delivered or mirrored(resolved):
        relative = posixpath.relpath(destination(resolved),
                                     posixpath.dirname(destination(source)) or ".")
        return relative + trailing + suffix
    return REPOSITORY + resolved + trailing + suffix


def inert(line):
    """A line with its tillr directives made inert: the comment survives, readable, and
    no longer starts with the `tillr:` keyword any parser matches."""
    return DIRECTIVE.sub(lambda m: f"<!--{m.group(1)}inert copy of a tillr directive:{m.group(2)}", line)


def transform(text, source, delivered, commit, date):
    """The delivered bytes of one source document. A pure function of its arguments, so
    the suite can recompute it from the snapshot alone."""
    lines = text.split("\n")
    out = []
    fenced = False
    frontmatter = bool(lines) and lines[0].strip() == "---"
    banner_done = False
    for number, line in enumerate(lines):
        if frontmatter:
            out.append(line)
            if number > 0 and line.strip() == "---":
                frontmatter = False
            continue
        if line.lstrip().startswith(FENCE):
            fenced = not fenced
            out.append(line)
            continue
        if fenced:
            out.append(line)
            continue
        line = INLINE_LINK.sub(
            lambda m: "](" + rewrite_target(m.group("target"), source, delivered) + ")", line)
        line = REFERENCE_LINK.sub(
            lambda m: m.group("lead") + rewrite_target(m.group("target"), source, delivered), line)
        line = inert(line)
        out.append(line)
        if not banner_done and re.match(r"^# \S", line):
            out.extend(["", banner(source, commit, date)])
            banner_done = True
    return "\n".join(out)


def live_directives(text):
    """`line: text` for every directive outside a fenced block that a tool would read."""
    found = []
    fenced = False
    for number, line in enumerate(text.splitlines(), 1):
        if line.lstrip().startswith(FENCE):
            fenced = not fenced
            continue
        if not fenced and DIRECTIVE.search(line):
            found.append(f"{number}: {line.strip()[:100]}")
    return found


# --- the snapshot -------------------------------------------------------------------


def snapshot_entries(snapshot):
    """(source, blob, destination) for every document the snapshot's own tree carries."""
    return [(entry["source"], entry["blob"], entry["delivered"]) for entry in snapshot["documents"]]


def delivered_set(snapshot):
    return {entry["source"] for entry in snapshot["documents"]}


def sync(commit="HEAD"):
    sha = git("rev-parse", "--verify", f"{commit}^{{commit}}").strip()
    date = git("show", "-s", "--format=%cd", "--date=short", sha).strip()
    index_text = git("show", f"{sha}:{INDEX}")
    sources = [INDEX] + [path for path in declared(index_text) if not mirrored(path)]
    in_mirror = [path for path in declared(index_text) if mirrored(path)]
    missing = [path for path in sources + in_mirror
               if not git("ls-tree", "--name-only", sha, "--", path).strip()]
    if missing:
        print(f"pack-docs: the index at {sha[:10]} declares documents that commit does not "
              f"hold: {', '.join(missing)}", file=sys.stderr)
        return 1
    undecided = undecided_rows(index_text)
    if undecided:
        print("pack-docs: index rows that say neither `shipped` nor `not shipped`:\n  "
              + "\n  ".join(undecided), file=sys.stderr)
        return 1

    # Remove what the previous snapshot wrote, then write this one. Only the paths a
    # snapshot recorded are removed, so the module mirror beside them is never touched.
    previous = DELIVERED_ROOT / SNAPSHOT
    if previous.is_file():
        for _, _, dest in snapshot_entries(json.loads(previous.read_text())):
            (DELIVERED_ROOT / dest).unlink(missing_ok=True)
    docs_root = DELIVERED_ROOT / "docs"
    if docs_root.is_dir():
        shutil.rmtree(docs_root)

    delivered = set(sources)
    entries = []
    for source in sources:
        blob = git("rev-parse", f"{sha}:{source}").strip()
        text = git("cat-file", "blob", blob)
        dest = destination(source)
        target = DELIVERED_ROOT / dest
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(transform(text, source, delivered, sha, date), encoding="utf-8")
        entries.append({"source": source, "blob": blob, "delivered": dest})
    snapshot = {
        "format": "tillr-pack-docs-1",
        "commit": sha,
        "date": date,
        "index": INDEX,
        "jail_root": JAIL_ROOT,
        "documents": entries,
        "in_module_mirror": in_mirror,
    }
    previous.parent.mkdir(parents=True, exist_ok=True)
    previous.write_text(json.dumps(snapshot, indent=2) + "\n", encoding="utf-8")
    size = sum((DELIVERED_ROOT / e["delivered"]).stat().st_size for e in entries)
    print(f"pack-docs: delivered {len(entries)} document(s), {size:,} bytes, from {sha[:10]} "
          f"({date}); {len(in_mirror)} more are delivered by the module mirror")
    return 0


def main(argv):
    if len(argv) >= 2 and argv[1] == "sync":
        commit = "HEAD"
        if len(argv) == 4 and argv[2] == "--commit":
            commit = argv[3]
        elif len(argv) != 2:
            print(__doc__, file=sys.stderr)
            return 2
        return sync(commit)
    if len(argv) == 2 and argv[1] == "list":
        for path in declared(git("show", f"HEAD:{INDEX}")):
            print(path)
        return 0
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
