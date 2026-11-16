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

Cases:
  1. every module's copy is present and carries exactly the source's committable files;
  2. every copied file is byte-identical to its source, with the README rewrite applied;
  3. every copied file has the source's exec bit, and none is a symlink;
  4. a Python bytecode cache is a build output and never enters the source set, so a
     developer's global gitignore cannot decide what this check reads;
  5. the eight launchers are one body, each naming the module it builds, one per declared
     `program` entry.

Run: python3 tests/pack-sync.test.py [--candidate]
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
    """Every regular file under root, by POSIX relative path."""
    found = {}
    for path in sorted(root.rglob("*")):
        if path.is_dir():
            continue
        found[path.relative_to(root).as_posix()] = path
    return found


def executable(path):
    return bool(path.stat().st_mode & 0o111)


# The mirror's link rewrite is a program, not a pattern; the suite imports the same one.
import importlib.util

_resolver_path = Path(__file__).resolve().parent.parent / "tools" / "pack-mirror-links.py"
_spec = importlib.util.spec_from_file_location("pack_mirror_links", _resolver_path)
pack_mirror_links = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(pack_mirror_links)


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


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    candidate = "--candidate" in argv
    case_tree_matches_source(candidate=candidate)
    case_bytes(candidate=candidate)
    case_modes(candidate=candidate)
    case_build_outputs()
    case_launchers()
    return 0


if __name__ == "__main__":
    sys.exit(main())
