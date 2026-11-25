#!/usr/bin/env python3
"""Render the tillr pack mirror and rewrite README links that escape it.

`just pack-sync` copies a module's source into a pack, where the repository's own
`docs/` tree is not present. A link that resolves outside the module root therefore
points at nothing, and the gate reads it as a broken link inside the pack. Those links
are rewritten to the repository on GitHub, which is where the file actually lives.

A link that stays inside the module is left exactly as it is. That distinction is the
whole point of this program: a sed that rewrites every `../` turns `../brief-template.md`
— a sibling in the same pack — into a URL naming a different file, and a reference that
names one file and opens another is worse than no link at all.

Usage: pack-mirror.py --sync <repo-root> [<module> ...]
       pack-mirror-links.py <module-root>

The module root is the directory the module was mirrored to, which stands in for
`tools/<module>/` in the repository.
"""

import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

REPOSITORY = "https://github.com/mschulkind-oss/tillr/blob/main/"
MIRROR_ROOT = Path("yolo-packs/tillr/files/local/share/tillr")
# Includes shared libraries and source-only modules, not just programs on PATH.
SHIPPED = (
    "tillr", "tillr-cli", "tillr-drain", "tillr-gate", "tillr-item", "tillr-land",
    "tillr-run", "tillr-store", "tillr-view", "tillr-watch", "tillr-trace", "tillr-roll",
)

# The README beside this executable belongs to the documentation snapshot, not the mirror.
LOOP_MODULE = "tillr-loop"
LOOP_ADAPTER = Path(LOOP_MODULE) / LOOP_MODULE

# `[label](target)` and `[label](target#anchor)`. A target with a scheme, an absolute
# path, or a bare fragment is not a relative link and is never rewritten.
LINK = re.compile(r"\]\((?P<target>[^)\s]+)\)")
ABSOLUTE = re.compile(r"^(?:[a-z][a-z0-9+.-]*:|/|#)")


def rewrite(text, directory):
    """One README's text, with only its escaping links rewritten."""

    def one(match):
        target = match.group("target")
        if ABSOLUTE.match(target):
            return match.group(0)
        path, _, anchor = target.partition("#")
        resolved = os.path.normpath(os.path.join(directory, path))
        if not resolved.startswith(".."):
            return match.group(0)
        # Strip the `../` segments that leave the module: what remains is the path the
        # file has in the repository, which is what the URL needs.
        while resolved.startswith("../"):
            resolved = resolved[3:]
        return "](%s%s%s)" % (REPOSITORY, resolved, "#" + anchor if anchor else "")

    return LINK.sub(one, text)


def rewrite_readmes(root):
    rewritten = 0
    for base, _, files in os.walk(root):
        for name in files:
            if name != "README.md":
                continue
            path = os.path.join(base, name)
            with open(path, encoding="utf-8") as handle:
                before = handle.read()
            after = rewrite(before, os.path.relpath(base, root))
            if after != before:
                with open(path, "w", encoding="utf-8") as handle:
                    handle.write(after)
                rewritten += 1
    print("pack-mirror-links: rewrote %d README(s) under %s" % (rewritten, root))


def check_destination(root, dest):
    for parent in dest.parents:
        if parent == root:
            break
        if parent.is_symlink():
            raise ValueError(f"refusing symlinked destination ancestor: {parent}")


def mirror_loop_adapter(root):
    source = root / "tools" / LOOP_ADAPTER
    dest = root / MIRROR_ROOT / LOOP_ADAPTER
    check_destination(root, dest)
    dest.unlink(missing_ok=True)
    if source.is_file():
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, dest)
        print(f"pack-sync: mirrored {source.relative_to(root)} -> {dest.relative_to(root)}")


def mirror_module(root, module):
    source = Path("tools") / module
    dest = root / MIRROR_ROOT / module
    check_destination(root, dest)

    # Include tracked and unignored in-progress source, never ignored build outputs.
    # Read the index before clearing the destination in case git cannot list the source.
    paths = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", str(source)],
        cwd=root,
    ).split(b"\0")
    live = []
    for raw in paths:
        if not raw:
            continue
        path = Path(os.fsdecode(raw))
        src = root / path
        if src.exists():  # A tracked file can already be deleted in the worktree.
            live.append((path, src))
    if dest.is_symlink() or dest.is_file():
        dest.unlink()
    elif dest.exists():
        shutil.rmtree(dest)
    if not live:
        return
    dest.mkdir(parents=True)
    for path, src in live:
        dst = dest / path.relative_to(source)
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
    rewrite_readmes(dest)
    print(f"pack-sync: mirrored {source} -> {dest.relative_to(root)}")


def main(argv):
    if len(argv) >= 3 and argv[1] == "--sync":
        root = Path(argv[2]).resolve()
        shipped = (*SHIPPED, LOOP_MODULE)
        modules = argv[3:] or shipped
        for module in modules:
            if module not in shipped:
                print(f"pack-sync: unknown module: {module}", file=sys.stderr)
                return 2
        try:
            for module in modules:
                if module == LOOP_MODULE:
                    mirror_loop_adapter(root)
                else:
                    mirror_module(root, module)
        except (OSError, ValueError, subprocess.CalledProcessError) as exc:
            print(f"pack-sync: {exc}", file=sys.stderr)
            return 1
        return 0
    if len(argv) != 2 or argv[1] == "--sync":
        print(__doc__, file=sys.stderr)
        return 2
    rewrite_readmes(argv[1])
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
