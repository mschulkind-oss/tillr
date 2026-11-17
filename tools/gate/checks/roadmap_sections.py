"""Remember independent sections of the large tillr roadmap, not whole revisions.

This is deliberately not a general Markdown slicer. The roadmap has ATX headings
(blank-separated # headings), inline links and single-line HTML anchors/comments.
Cross-section constructs opt out, with a diagnostic, and get the full-file check.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import tempfile

ROADMAP = "docs/tillr/roadmap.md"


def split(text):
    # These constructs can make a section's interpretation depend on another section.
    # In particular, question/section directives and reference definitions must never
    # be judged as separate documents. The normal full-file renderer handles them.
    if "$$" in text:
        return None  # display math can consume headings in another section
    if re.search(r"^ {1,3}#{1,6}(?:[ \t]|$)|^#{1,6}\t", text, re.M):
        return None  # noncanonical ATX headings must participate in full-page anchors
    if re.search(r"^\s*(?:```|~~~|\[[^\]]+\]:|<(?!(?:a id=|!-- tillr:)))|<!-- vantage:|^\s*={3,}\s*$", text, re.M):
        # The two frontmatter delimiters are permitted, unlike setext headings/rules.
        without_header = re.sub(r"\A---\n.*?\n---\n", "", text, count=1, flags=re.S)
        if re.search(r"^\s*(?:```|~~~|\[[^\]]+\]:|<(?!(?:a id=|!-- tillr:)))|<!-- vantage:|^\s*={3,}\s*$", without_header, re.M):
            return None
    front = re.match(r"\A---\n.*?\n---\n", text, re.S)
    header = front.group(0) if front else ""
    if "planning-actions" in header or "planning_actions" in header:
        return None  # a rule whose meaning depends on the roadmap's filename
    body_lines = text[len(header):].splitlines()
    for index, line in enumerate(body_lines):
        if re.fullmatch(r'\s*-{3,}\s*', line) and index and body_lines[index - 1].strip():
            return None  # setext heading, not a blank-separated thematic break
        if line.lstrip().startswith("<") and not (
                re.fullmatch(r'<a id="[^"]+"></a>', line.strip()) or
                re.fullmatch(r'<!-- tillr:.* -->', line.strip())):
            return None
    starts = [len(header) + m.start() for m in re.finditer(r"^## ", text[len(header):], re.M)]
    # Headings inside a multiline HTML comment would not be boundaries.
    if any("<!--" in line and "-->" not in line for line in text.splitlines()):
        return None
    starts = [len(header), *starts, len(text)]
    sections = [(text[start:end], text[:start].count("\n") + 1)
                for start, end in zip(starts, starts[1:]) if start < end]
    return (header, sections) if sections else None


def prepare(root, documents, common, keys, unsettled):
    """Return fragment paths and cleanup handles, or leave the full-file check intact.

    Keys bind section bytes, configuration, checker bytes, linked inputs and every
    original heading/explicit anchor. Self links are checked against the real full
    roadmap; changing a heading forgets all sections. Working-tree differences never
    yield a reusable pass. Nothing changes the roadmap or its item source references.
    """
    paths = list(documents)
    locations = {}
    temporary = []
    if ROADMAP not in paths:
        return paths, locations, temporary
    text = (root / ROADMAP).read_text()
    partition = split(text)
    entries = common.tree_entries(root)
    configs = {p: e[2] for p, e in entries.items() if Path(p).name in common.DOCUMENT_CONFIG_NAMES}
    unrouted = any("unrouted" in common.git(root, "show", f"{common.HEAD}:{p}") for p in configs if p.endswith(".toml"))
    if partition is None or unrouted:
        print("documents: roadmap uses cross-section markup or unrouted planning; checking the full file", file=common.sys.stderr)
        return paths, locations, temporary
    header, sections = partition
    code = {p: entries.get(p, ("", "", "absent"))[2] for p in common.DOCUMENT_CHECK_FILES}
    anchors = re.findall(r"^#{1,6} .*$|<a id=\"[^\"]+\"></a>", text, re.M)
    siblings = sorted(p for p in entries if Path(p).parent == Path(ROADMAP).parent)
    base_identity = {"format": "roadmap-sections-1", "path": ROADMAP, "mode": entries[ROADMAP][0],
                     "header": header, "anchors": anchors, "configs": configs, "code": code, "siblings": siblings}
    paths.remove(ROADMAP)
    try:
        for body, line in sections:
            named, absent = common.named_inputs(header + body, ROADMAP, entries)
            named.pop(ROADMAP, None)
            identity = dict(base_identity, body=body, named=named, absent=sorted(absent))
            if re.search(r"#L\d", header + body):
                identity["line_count"] = len(text.splitlines())
            key = hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()
            handle, filename = tempfile.mkstemp(dir=root / Path(ROADMAP).parent,
                                                prefix=".gate-roadmap-", suffix=".md")
            temporary.append(filename)
            # Fragment-only links would go dead; keep their original full-page target.
            rendered_body = re.sub(r'(\]\(\s*<?)#', r'\1roadmap.md#', body)
            with os.fdopen(handle, "w") as output:
                output.write(header + rendered_body)
            path = os.path.relpath(filename, root)
            keys[path] = key
            if ROADMAP in unsettled:
                unsettled[path] = unsettled[ROADMAP]
            locations[path] = (ROADMAP, line - 1 - header.count("\n"), line)
            paths.append(path)
    except BaseException:
        cleanup(temporary)
        raise
    return paths, locations, temporary


def cleanup(temporary):
    for filename in temporary:
        Path(filename).unlink(missing_ok=True)
