#!/usr/bin/env python3
"""Rewrite the README links in a mirrored pack tree that escape the pack.

`just pack-sync` copies a module's source into a pack, where the repository's own
`docs/` tree is not present. A link that resolves outside the module root therefore
points at nothing, and the gate reads it as a broken link inside the pack. Those links
are rewritten to the repository on GitHub, which is where the file actually lives.

A link that stays inside the module is left exactly as it is. That distinction is the
whole point of this program: a sed that rewrites every `../` turns `../brief-template.md`
— a sibling in the same pack — into a URL naming a different file, and a reference that
names one file and opens another is worse than no link at all.

Usage: pack-mirror-links.py <module-root>

The module root is the directory the module was mirrored to, which stands in for
`tools/<module>/` in the repository.
"""

import os
import re
import sys

REPOSITORY = "https://github.com/mschulkind-oss/tillr/blob/main/"

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


def main(argv):
    if len(argv) != 2:
        print("usage: pack-mirror-links.py <module-root>", file=sys.stderr)
        return 2
    root = argv[1]
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
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
