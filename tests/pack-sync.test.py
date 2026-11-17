#!/usr/bin/env python3
"""The one generated pack tree: the tillr pack's copy of every `tools/tillr*` module.

`tools/tillr`, its eight binaries and the `tillr-trace` shim are the canonical source.
`yolo-packs/tillr/files/local/share/tillr/<module>` is a committed copy, because the pack
-- not this checkout -- is what puts `tillr` on PATH in a workspace that has no `tools/`.
`just pack-sync` mirrors each module; this suite fails when a copy diverges from its
source.

The copy is committed rather than built because a pack ships source and builds it lazily
through `yolo-packs/tillr/programs/<bin>/run`, never a compiled artifact. A module's build
output (`tools/tillr-store/tillr-store`) is IGNORED by git and is the one kind of file the
mirror leaves out; an untracked-but-not-ignored `.go` file is source the launcher must
build, so it is copied.

Shipping and launching are two different sets. Nine Go modules ship and build the eight
names on PATH; `tillr-trace` ships and launches nothing, because it is the recording shim
`tillr-run`'s pi adapter sources through `BASH_ENV`, and a pack that left it out silently
recorded no subprocess at all.

A copied README must not link out of the pack: `../../docs/...` is correct beside
`tools/` and dead inside a pack, where it resolves to `files/local/docs/...` and the
repository gate reads that as a broken link. The recipe rewrites those links to the
repository on GitHub, and `mirrored()` below spells the same rule.

The pack also delivers tillr's design documentation: the status index
`docs/tillr/design-status.md` and every document it marks `shipped`, copied by
`just pack-docs` (tools/pack-docs.py) into `files/local/share/tillr/docs/`. Those copies
are a SNAPSHOT of one commit, recorded in `docs/snapshot.json`, and they are judged
against that snapshot rather than against HEAD: the running drain edits design documents
on every landing, a lane's fence does not include this tree, and a check that required
the copies to match HEAD would turn every such landing red for every lane after it. How
far the snapshot lags HEAD is the `--fresh` mode, which fails on any lag and names the
refresh command; `just pack-docs` runs it.

Cases:
  1. every module's copy is present and carries exactly the source's committable files;
  2. every copied file is byte-identical to its source, with the README rewrite applied;
  3. every copied file has the source's exec bit, and none is a symlink;
  4. a Python bytecode cache is a build output and never enters the source set, so a
     developer's global gitignore cannot decide what this check reads;
  5. the eight launchers are one body, each naming the module it builds, one per declared
     `program` entry;
  6. the documentation transform: links re-resolved for the delivered tree, item
     directives inert outside fences, fences and frontmatter untouched, one banner;
  7. the index's declaration: a `shipped` row is declared, a `not shipped` row is not,
     and a row that says neither is named;
  8. the delivered documents: each copy is exactly the transform of the blob the snapshot
     names, nothing is missing or extra, every document the delivered index declares is
     delivered, and no copy carries a live item directive;
  9. (--fresh only) the snapshot is current: no delivered source changed at HEAD, and the
     index at HEAD declares the documents the snapshot delivered.

Run: python3 tests/pack-sync.test.py [--candidate | --fresh]
"""

import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PACK = ROOT / "yolo-packs" / "tillr"
COPY = PACK / "files" / "local" / "share" / "tillr"
PROGRAMS = PACK / "programs"

# Every module that ships, in the order the pack's `files` tree carries them.
MODULES = (
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

# The modules that ship without a launcher, so the pack must claim no name on PATH for
# them: `tillr` is the shared library the eight binaries resolve through relative
# `replace` paths (the dispatcher named `tillr` is built from `tillr-cli`), and
# `tillr-trace` is sourced as a shim rather than run as a program.
# `tillr-roll` is the repository's keeper, invoked by its just recipe, not a PATH binary.
SOURCE_ONLY_MODULES = ("tillr", "tillr-cli", "tillr-trace", "tillr-roll")

# The modules that ship WITH a launcher, keyed by the name on PATH. The name is not always
# the module directory: `tillr` is the umbrella dispatcher, built from `tillr-cli`.
PROGRAM_MODULES = {
    "tillr": "tillr-cli",
    **{module: module for module in MODULES if module not in SOURCE_ONLY_MODULES},
}

OUT_OF_TREE = re.compile(rb"\]\((?:\.\./)+")
REPOSITORY = b"](https://github.com/mschulkind-oss/tillr/blob/main/"


def ok(name):
    print(f"ok   {name}")


# A Python bytecode cache is a build output, like the Go binary a module's own .gitignore
# names, and the mirror must leave it out. It is filtered here rather than left to
# `--exclude-standard` because whether that pattern applies depends on who runs the suite:
# the `.gitignore` that covers `*.pyc` on a developer's machine lives in their global
# excludes file, so under the gate's scrubbed HOME the same directory reads as
# untracked-but-not-ignored source and the check fails in one cell and passes in the other
# (measured 2026-11-14). A rule the tree does not carry is not a rule the gate can rely on.
BUILD_OUTPUT = re.compile(r"(?:^|/)__pycache__/|\.pyc$")


def committable(module):
    """Every file git would commit under tools/<module>, by POSIX relative path.

    Tracked plus untracked-but-not-ignored: a module can be mid-work and still be the
    source the launcher builds. An ignored file is a build output, and so is a Python
    bytecode cache (see BUILD_OUTPUT).
    """
    listing = subprocess.run(
        ["git", "-C", str(ROOT), "ls-files", "-z", "--cached", "--others",
         "--exclude-standard", f"tools/{module}"],
        check=True, capture_output=True,
    ).stdout
    found = {}
    for entry in listing.split(b"\0"):
        if not entry:
            continue
        relative = entry.decode().split("/", 2)[2]
        if BUILD_OUTPUT.search(relative):
            continue
        found[relative] = ROOT / "tools" / module / relative
    return found


def case_build_outputs():
    """A build output is not source, whatever the caller's gitignore happens to say."""
    for relative in ("__pycache__/tillr_trace.cpython-314.pyc", "tillr_trace.pyc"):
        assert BUILD_OUTPUT.search(relative), (
            f"{relative} is a build output and the source set must leave it out"
        )
    for relative in ("tillr_trace.py", "README.md", "testdata/canary-run.log",
                     "pycache-notes.md"):
        assert not BUILD_OUTPUT.search(relative), (
            f"{relative} is source, and the source set must keep it"
        )
    ok("a Python bytecode cache is a build output, and a source file is not")


def files(root):
    """Every regular file under root, by POSIX relative path, build outputs left out.

    The same filter as the source side: an interpreter that runs a copied script writes
    its bytecode cache beside it, and that cache is not drift (the gate failed a candidate
    on 2026-11-17 over one `__pycache__/report.cpython-314.pyc` in the pack's copy).
    """
    found = {}
    for path in sorted(root.rglob("*")):
        if path.is_dir():
            continue
        relative = path.relative_to(root).as_posix()
        if BUILD_OUTPUT.search(relative):
            continue
        found[relative] = path
    return found


def case_copy_build_outputs():
    """A bytecode cache on the pack side is not drift; a stray source file still is."""
    import tempfile
    with tempfile.TemporaryDirectory() as raw:
        copy = Path(raw)
        (copy / "__pycache__").mkdir()
        (copy / "__pycache__" / "x.cpython-314.pyc").write_bytes(b"\0")
        (copy / "kept.py").write_text("x = 1\n")
        assert set(files(copy)) == {"kept.py"}, (
            "a __pycache__ file in the pack's copy must not be listed as a copied file"
        )
        (copy / "stray.py").write_text("y = 2\n")
        assert set(files(copy)) - {"kept.py"} == {"stray.py"}, (
            "a stray source file in the pack's copy must still be listed, so it reads as drift"
        )
    ok("a bytecode cache on the pack side is ignored, and a stray source file is not")


def executable(path):
    return bool(path.stat().st_mode & 0o111)


# The mirror's link rewrite is a program, not a pattern; the suite imports the same one.
import importlib.util

_resolver_path = Path(__file__).resolve().parent.parent / "tools" / "pack-mirror-links.py"
_spec = importlib.util.spec_from_file_location("pack_mirror_links", _resolver_path)
pack_mirror_links = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(pack_mirror_links)

_docs_path = Path(__file__).resolve().parent.parent / "tools" / "pack-docs.py"
_docs_spec = importlib.util.spec_from_file_location("pack_docs", _docs_path)
pack_docs = importlib.util.module_from_spec(_docs_spec)
_docs_spec.loader.exec_module(pack_docs)


def mirrored(relative, data):
    """The bytes `just pack-sync` should have written for one source file.

    The link rewrite is a program, not a pattern: a link that resolves outside the module
    is rewritten to the repository on GitHub, and a link that stays inside it is left
    alone. A sed that rewrites every `../` turns `../brief-template.md`, a sibling in the
    same pack, into a URL naming a different file. The suite imports the same resolver the
    mirror runs, so the two cannot disagree about what the copy should contain.
    """
    if not relative.endswith("README.md"):
        return data
    return pack_mirror_links.rewrite(
        data.decode("utf-8"), str(Path(relative).parent)
    ).encode("utf-8")


def case_tree_matches_source(candidate=False):
    """Each module's copy against its source: present, complete, and nothing extra."""
    if candidate:
        print("n/a  the tillr pack's tree: a candidate does not carry the generated "
              "tree; the landing runs this case strictly")
        return
    assert COPY.is_dir(), f"missing the pack's copy: {COPY}\nrun `just pack-sync` to generate it"
    checked = 0
    for module in MODULES:
        source = ROOT / "tools" / module
        copy = COPY / module
        assert source.is_dir(), f"missing the canonical source tree: {source}"
        assert copy.is_dir(), (
            f"missing the pack's copy of {module}: {copy}\nrun `just pack-sync`"
        )
        source_files, copy_files = committable(module), files(copy)
        missing = sorted(set(source_files) - set(copy_files))
        assert not missing, (
            f"the pack's copy of {module} is missing {len(missing)} file(s): "
            f"{', '.join(missing[:5])}{' ...' if len(missing) > 5 else ''}\n"
            "run `just pack-sync`"
        )
        extra = sorted(set(copy_files) - set(source_files))
        assert not extra, (
            f"the pack's copy of {module} carries {len(extra)} file(s) the source does "
            f"not have: {', '.join(extra[:5])}{' ...' if len(extra) > 5 else ''}\n"
            "run `just pack-sync` (it mirrors the tree, so a stray file is a stale copy)"
        )
        checked += len(source_files)
    ok(f"every module's copy matches its source's file set ({len(MODULES)} modules, "
       f"{checked} files)")


def case_bytes(candidate=False):
    """Every copied file against its source byte for byte, with the README rewrite."""
    if candidate:
        print("n/a  the tillr pack's bytes: a candidate does not carry the generated tree")
        return
    for module in MODULES:
        copy = COPY / module
        for relative, source_path in committable(module).items():
            expected = mirrored(relative, source_path.read_bytes())
            assert expected == (copy / relative).read_bytes(), (
                f"the pack's copy of {module}/{relative} diverges from its source: "
                "run `just pack-sync`"
            )
    ok("every copied file is byte-identical to its source, with the README rewrite")


def case_modes(candidate=False):
    """Exec bits survive the mirror, and no copied file is a symlink."""
    if candidate:
        print("n/a  the tillr pack's modes: a candidate does not carry the generated tree")
        return
    for module in MODULES:
        copy = COPY / module
        for relative, source_path in committable(module).items():
            copy_path = copy / relative
            assert not copy_path.is_symlink(), (
                f"the pack's copy of {module}/{relative} is a symlink; the committed tree "
                "must be self-contained"
            )
            assert executable(source_path) == executable(copy_path), (
                f"the pack's copy of {module}/{relative} has a different exec bit from "
                "the source: run `just pack-sync`"
            )
    ok("every copied file keeps its source's exec bit and is a regular file")


def launcher_body(text):
    """A launcher's text with the three lines that legitimately differ removed."""
    kept = []
    for line in text.splitlines():
        if re.match(r"^# \S+ -- the launcher the tillr pack", line):
            continue
        if re.match(r"^(bin|mod)=", line):
            continue
        kept.append(line)
    return "\n".join(kept)


def declared_programs():
    """The `bin` of every `program` contribution in the pack manifest."""
    text = (PACK / "pack.json").read_text()
    return re.findall(r'"kind":\s*"program",\s*"bin":\s*"([^"]+)"', text)


def case_launchers():
    """The eight launchers are one body, each naming the module that builds it."""
    bodies = {}
    for bin_name, module in sorted(PROGRAM_MODULES.items()):
        path = PROGRAMS / bin_name / "run"
        assert path.is_file(), f"missing launcher: {path}"
        text = path.read_text()
        assert f"\nbin={bin_name}\n" in text, (
            f"{path} does not set `bin={bin_name}`; the delivered file is named for the "
            "binary it must run"
        )
        assert f"\nmod={module}\n" in text, (
            f"{path} does not set `mod={module}`: {bin_name} is built from module {module}"
        )
        assert executable(path), f"{path} is not executable; yolo delivers the exec bit"
        bodies[bin_name] = launcher_body(text)
    canonical = bodies["tillr"]
    for bin_name, body in bodies.items():
        assert body == canonical, (
            f"the {bin_name} launcher's body has drifted from the tillr launcher's; the "
            "eight are one script and only the header line and the two assignments differ"
        )
    declared = sorted(declared_programs())
    assert declared == sorted(PROGRAM_MODULES), (
        "the manifest's `program` entries and the launchers do not match: declared "
        f"{declared}, launchers {sorted(PROGRAM_MODULES)}"
    )
    ok(f"the eight launchers are one body, each naming its module, one per declared "
       f"program ({len(declared)} entries)")


# --- the delivered design documentation ------------------------------------------------


def case_docs_transform():
    """The transform, on a specimen that exercises every rule it has."""
    source = "docs/design/a.md"
    delivered = {"docs/design/a.md", "docs/design/b.md", "docs/tillr/design-status.md"}
    text = "\n".join([
        "---",
        "depends-on: [b.md]",
        "---",
        "",
        "# A",
        "",
        "See [b](./b.md#2-the-part), [index](../tillr/design-status.md), [gone](./c.md#x),",
        "[stage](../../tools/tillr-drain/drain/stage.go), [loop](../../tools/tillr-loop/README.md),",
        "[here](#local), [web](https://example.com/x.md) and [dir](../../tools/tillr-gate/).",
        "[ref]: ../roadmap.md",
        '<!-- tillr: item id="item-x" paths="docs/" blocked_by="" -->',
        "   <!-- vantage: question id=OQ-A1 leaning=\"A\" -->",
        "```text",
        '<!-- tillr: item id="item-specimen" paths="docs/" blocked_by="" -->',
        "[kept](./c.md)",
        "```",
        "",
    ])
    out = pack_docs.transform(text, source, delivered, "0123456789abcdef", "2026-11-16")
    lines = out.split("\n")
    assert lines[:3] == ["---", "depends-on: [b.md]", "---"], "frontmatter must be untouched"
    assert lines[5] == "" and lines[6].startswith("> **Delivered copy.** This is `docs/design/a.md`"), (
        "the banner follows the first heading")
    assert out.count("> **Delivered copy.**") == 1, "exactly one banner"
    repo = pack_mirror_links.REPOSITORY
    for expected in ("[b](./b.md#2-the-part)".replace("./", ""),
                     "[index](../tillr/design-status.md)",
                     f"[gone]({repo}docs/design/c.md#x)",
                     "[stage](../../tillr-drain/drain/stage.go)",
                     f"[loop]({repo}tools/tillr-loop/README.md)",
                     "[here](#local)", "[web](https://example.com/x.md)",
                     "[dir](../../tillr-gate/)",
                     f"[ref]: {repo}docs/roadmap.md"):
        assert expected in out, f"the transform should have written {expected!r}:\n{out}"
    assert '<!-- inert copy of a tillr directive: item id="item-x"' in out
    assert '<!-- tillr: item id="item-specimen"' in out, "a fenced specimen is left alone"
    assert "[kept](./c.md)" in out, "a fenced link is left alone"
    assert '   <!-- vantage: question id=OQ-A1 leaning="A" -->' in out, (
        "a question directive stays live, because Vantage refuses an open question without one")
    assert pack_docs.live_directives(out) == [], pack_docs.live_directives(out)
    assert pack_docs.live_directives(text), "the specimen's own directive is live"
    # A document delivered OUTSIDE docs/ links back with the path the tree gives it.
    back = pack_docs.transform("# L\n[a](../../docs/design/a.md)\n", "tools/tillr-loop/README.md",
                               delivered | {"tools/tillr-loop/README.md"}, "0" * 40, "2026-11-16")
    assert "[a](../docs/design/a.md)" in back, back
    ok("the documentation transform re-resolves links, makes item directives inert outside "
       "fences, leaves fences, frontmatter and questions alone, and adds one banner")


def case_docs_declaration():
    """The index is the declaration, read from its table rows."""
    index = "\n".join([
        "| Document | Status | Pack |",
        "| :--- | :--- | :--- |",
        "| [Item graphs](../design/tillr-item-graphs.md) | built | shipped |",
        "| [Seams](../design/autoqa-tillr-seams.md#top) | superseded | not shipped |",
        "| [Gate](../../tools/tillr-gate/README.md) | built | shipped |",
        "| [Unsure](../design/unsure.md) | ? | maybe |",
        "| not a link | built | shipped |",
        "```",
        "| [Specimen](../design/specimen.md) | x | shipped |",
        "```",
    ])
    assert pack_docs.declared(index) == ["docs/design/tillr-item-graphs.md",
                                         "tools/tillr-gate/README.md"], pack_docs.declared(index)
    assert len(pack_docs.undecided_rows(index)) == 1, pack_docs.undecided_rows(index)
    assert pack_docs.MIRRORED_MODULES == MODULES, (
        "tools/pack-docs.py's MIRRORED_MODULES and this suite's MODULES disagree, so a link "
        "into a module would be rewritten as if it were (or were not) delivered")
    # The copy-to-source rule every path-keyed allowance follows: it inverts `destination`,
    # and the gate's restatement of it (tree_common.excused_as) agrees on every shape.
    _common_spec = importlib.util.spec_from_file_location(
        "tree_common", ROOT / "tools" / "gate" / "checks" / "tree_common.py")
    tree_common = importlib.util.module_from_spec(_common_spec)
    _common_spec.loader.exec_module(tree_common)
    assert tree_common.DELIVERED_PREFIX == pack_docs.DELIVERED_PREFIX
    assert pack_docs.DELIVERED_PREFIX == COPY.relative_to(ROOT).as_posix() + "/"
    for source in ("docs/design/a.md", "docs/tillr/design-status.md", "tools/tillr-loop/README.md",
                   "tools/tillr-view/README.md", "tools/tillr-run/backends/pi/run-pi"):
        copy = pack_docs.DELIVERED_PREFIX + pack_docs.destination(source)
        assert pack_docs.source_of(copy) == source, (copy, pack_docs.source_of(copy))
        assert tree_common.excused_as(copy) == source, (copy, tree_common.excused_as(copy))
    for outside in ("docs/design/a.md", "tools/tillr-view/README.md", "yolo-packs/matt/README.md"):
        assert pack_docs.source_of(outside) == outside == tree_common.excused_as(outside)
    ok("the index declares exactly its `shipped` rows, names an undecided row, and agrees "
       "with this suite about which modules are mirrored; a delivered copy maps back to its "
       "source by one rule, which the gate's checks restate identically")


def read_blobs(blobs):
    """Each blob's bytes, from one `git cat-file --batch`."""
    if not blobs:
        return {}
    batch = subprocess.run(["git", "-C", str(ROOT), "cat-file", "--batch"],
                           input="".join(f"{blob}\n" for blob in blobs).encode(),
                           capture_output=True, check=True).stdout
    found, at = {}, 0
    for blob in blobs:
        end = batch.index(b"\n", at)
        header = batch[at:end].split()
        assert len(header) == 3 and header[1] == b"blob", (
            f"the snapshot names {blob}, which this repository does not hold as a blob: "
            "run `just pack-docs`")
        size = int(header[2])
        found[blob] = batch[end + 1:end + 1 + size]
        at = end + 1 + size + 1
    return found


def load_snapshot():
    path = COPY / pack_docs.SNAPSHOT
    assert path.is_file(), (
        f"missing the delivered documentation's snapshot: {path.relative_to(ROOT)}\n"
        "run `just pack-docs`")
    return json.loads(path.read_text())


def case_docs_delivered(candidate=False):
    """Every delivered document against the blob its snapshot names."""
    if candidate:
        print("n/a  the delivered documentation: a candidate does not carry the generated tree")
        return
    snapshot = load_snapshot()
    entries = pack_docs.snapshot_entries(snapshot)
    delivered = pack_docs.delivered_set(snapshot)
    blobs = read_blobs([blob for _, blob, _ in entries])
    size = 0
    for source, blob, dest in entries:
        copy = COPY / dest
        assert copy.is_file() and not copy.is_symlink(), (
            f"the delivered copy of {source} is missing or a symlink: {dest}; run `just pack-docs`")
        expected = pack_docs.transform(blobs[blob].decode("utf-8"), source, delivered,
                                       snapshot["commit"], snapshot["date"]).encode("utf-8")
        actual = copy.read_bytes()
        assert actual == expected, (
            f"the delivered copy of {source} is not the transform of the blob its snapshot "
            f"names ({blob[:10]}); it was edited by hand or the transform changed: run "
            "`just pack-docs`")
        live = pack_docs.live_directives(actual.decode("utf-8"))
        assert not live, f"the delivered copy of {source} carries a live item directive: {live[0]}"
        size += len(actual)
    on_disk = {path.relative_to(COPY).as_posix() for path in (COPY / "docs").rglob("*")
               if not path.is_dir()}
    expected_files = {dest for _, _, dest in entries if dest.startswith("docs/")} | {pack_docs.SNAPSHOT}
    stray = sorted(on_disk - expected_files)
    assert not stray, (
        f"the delivered docs tree carries {len(stray)} file(s) its snapshot does not name: "
        f"{', '.join(stray[:5])}; run `just pack-docs`")
    index = COPY / pack_docs.destination(snapshot["index"])
    assert snapshot["index"] in delivered and index.is_file(), "the status index is not delivered"
    # The delivered index's links were re-resolved for the delivered tree, so what it
    # declares is in DESTINATION space (`tillr-store/README.md`, no `tools/`): compare there.
    declared = pack_docs.declared(index.read_text(encoding="utf-8"), snapshot["index"])
    copied = {dest for _, _, dest in entries}
    undelivered = [path for path in declared if path not in copied
                   and not (path in mirror_destinations(snapshot) and (COPY / path).is_file())]
    assert not undelivered, (
        f"the delivered index lists {len(undelivered)} shipped document(s) the pack does not "
        f"deliver: {', '.join(undelivered[:5])}; run `just pack-docs`")
    unlisted = sorted(copied - set(declared) - {pack_docs.destination(snapshot["index"])})
    assert not unlisted, (
        f"the pack delivers {len(unlisted)} document(s) its index does not list as shipped: "
        f"{', '.join(unlisted[:5])}; run `just pack-docs`")
    # How far the snapshot lags HEAD is said, never judged here (see the module docstring).
    at_head = head_blobs([source for source, _, _ in entries])
    lagging = [source for source, blob, _ in entries if at_head.get(source) != blob]
    ok(f"the delivered documentation is the snapshot of {snapshot['commit'][:10]} "
       f"({snapshot['date']}): {len(entries)} document(s), {size:,} bytes, plus "
       f"{len(snapshot.get('in_module_mirror', []))} delivered by the module mirror; "
       f"{len(lagging)} changed at HEAD since (`--fresh` judges that)")


def mirror_destinations(snapshot):
    """Where the documents the module mirror delivers land under the delivered root."""
    return {pack_docs.destination(path) for path in snapshot.get("in_module_mirror", [])}


def head_blobs(paths):
    """Each path's blob at HEAD, from one `git ls-tree`; a path HEAD lacks is absent."""
    if not paths:
        return {}
    listing = subprocess.run(["git", "-C", str(ROOT), "ls-tree", "-z", "HEAD", "--", *paths],
                             capture_output=True, check=True).stdout.decode()
    found = {}
    for entry in listing.split("\0"):
        if entry:
            meta, _, path = entry.partition("\t")
            found[path] = meta.split()[2]
    return found


def stale_documents(entries, at_head):
    """The delivered sources whose blob at HEAD is not the one the snapshot copied."""
    return [source for source, blob, _ in entries if at_head.get(source) != blob]


def case_docs_stale_rule():
    """The freshness rule, on data: a changed or deleted source is stale."""
    entries = [("docs/a.md", "1" * 40, "docs/a.md"), ("docs/b.md", "2" * 40, "docs/b.md"),
               ("docs/c.md", "3" * 40, "docs/c.md")]
    at_head = {"docs/a.md": "1" * 40, "docs/b.md": "9" * 40}
    assert stale_documents(entries, at_head) == ["docs/b.md", "docs/c.md"]
    assert stale_documents(entries[:1], at_head) == []
    ok("a delivered source changed or deleted at HEAD reads as stale, and an unchanged one does not")


def case_docs_fresh():
    """--fresh: the snapshot is the documentation as HEAD holds it."""
    snapshot = load_snapshot()
    entries = pack_docs.snapshot_entries(snapshot)
    stale = stale_documents(entries, head_blobs([source for source, _, _ in entries]))
    assert not stale, (
        f"{len(stale)} delivered document(s) changed at HEAD since the snapshot of "
        f"{snapshot['commit'][:10]} ({snapshot['date']}): {', '.join(stale[:8])}"
        f"{' ...' if len(stale) > 8 else ''}\nrun `just pack-docs` and commit the result")
    index = subprocess.run(["git", "-C", str(ROOT), "show", f"HEAD:{pack_docs.INDEX}"],
                           capture_output=True, check=True).stdout.decode("utf-8")
    declared = set(pack_docs.declared(index))
    shipped = (pack_docs.delivered_set(snapshot) - {pack_docs.INDEX}) | set(
        snapshot.get("in_module_mirror", []))
    assert declared == shipped, (
        "the index at HEAD declares a different set of shipped documents from the snapshot's: "
        f"added {sorted(declared - shipped)[:5]}, removed {sorted(shipped - declared)[:5]}\n"
        "run `just pack-docs` and commit the result")
    ok(f"the delivered documentation is current: {len(entries)} document(s) match HEAD")


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    if "--fresh" in argv:
        case_docs_fresh()
        return 0
    candidate = "--candidate" in argv
    case_tree_matches_source(candidate=candidate)
    case_bytes(candidate=candidate)
    case_modes(candidate=candidate)
    case_build_outputs()
    case_copy_build_outputs()
    case_launchers()
    case_docs_transform()
    case_docs_declaration()
    case_docs_stale_rule()
    case_docs_delivered(candidate=candidate)
    return 0


if __name__ == "__main__":
    sys.exit(main())
