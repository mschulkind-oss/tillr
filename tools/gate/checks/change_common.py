"""Change-only checks. The caller supplies both commits; no gate record is read."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import posixpath
import re
import subprocess
import sys
import tempfile
import time
from urllib.parse import unquote
import roadmap_sections

# The checks are executable scripts in a subdirectory of the gate adapters.
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "tillr-gate"))

HEAD = "HEAD"
TS_SIZE_WARN_LINES = 2000
TS_SIZE_REFUSE_LINES = 3000
GENERATED_COPIES = ()
UNLANDED_MARKER = "[unlanded]"
UPSTREAM_REF = re.compile(r"^refs/(?:heads/patch/|published/patch/|remotes/[^/]+/patch/|remotes/upstream/|tags/)")
SHA_CITATION = re.compile(r"(?<![0-9A-Za-z])[0-9a-f]{7,40}(?![0-9A-Za-z])")


def git(root, *args, check=True):
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True, text=True)
    if check and result.returncode:
        raise RuntimeError(result.stderr.strip() or "git could not read the change")
    return result.stdout


def validate(base, head):
    for label, rev in (("base", base), ("head", head)):
        if not rev:
            raise RuntimeError(f"missing {label} argument")
        git(Path.cwd(), "rev-parse", "--verify", rev + "^{commit}")
    result = subprocess.run(["git", "merge-base", "--is-ancestor", base, head], capture_output=True)
    if result.returncode:
        raise RuntimeError("base is not an ancestor of head")


def added_markdown_lines(root: Path, base: str, paths: list[str]) -> dict[str, list[int]]:
    """The line numbers a `base..HEAD` diff adds to each of `paths`.

    `--unified=0` keeps the diff to changed lines, so no context line can be read as an
    added one; a hunk header's new-file start is the number of its first `+` line, and
    each added line increments it while a deleted line does not.
    """
    diff = git(root, "diff", "--unified=0", "--no-renames", "--no-color", base, HEAD,
               "--", *paths)
    added: dict[str, list[int]] = {}
    path: str | None = None
    number = 0
    for line in diff.splitlines():
        if line.startswith("+++ b/"):
            path = line[len("+++ b/"):]
            added.setdefault(path, [])
        elif line.startswith("@@"):
            match = re.search(r"\+(\d+)", line)
            number = int(match.group(1)) if match else 0
        elif path is not None and line.startswith("+"):
            added[path].append(number)
            number += 1
    return added


def unreachable_citations(root: Path, base: str | None, paths: list[str]) -> list[str]:
    """Every line this change adds to a tracked document that cites a commit not on HEAD.

    One `path:line: sha: reading -- text` per finding. The reading that failed is named:
    the token resolves to a commit in this clone, that commit is not an ancestor of HEAD,
    and it is not reachable from a ref that carries upstream's history (UPSTREAM_REF) --
    an upstream pin is a legitimate citation, not a claim about this repository. A token
    that resolves to nothing is not judged -- an upstream project's sha and a hex-looking
    session id both look like that, and refusing them would refuse a legitimate citation
    the guard cannot read. A fenced line is a specimen, not a citation, and is not read; a
    line carrying `UNLANDED_MARKER` is a deliberate unlanded citation and passes. `base`
    is the commit the change is diffed against, so
    only lines this change introduced are judged (see UNLANDED_MARKER for the backlog
    that keeps the guard off the whole tree), and a sha the same document already cited
    at `base` is not a new claim: a reflowed paragraph re-adds its lines, and the backlog
    those lines carry is filed rather than refused again on every edit.
    """
    if base is None or not paths:
        return []
    added = added_markdown_lines(root, base, paths)
    resolved: dict[str, str | None] = {}
    prior: dict[str, str] = {}
    findings: list[str] = []
    for path in sorted(added):
        if not git(root, "show", f"{HEAD}:{path}", check=False):
            continue
        lines = git(root, "show", f"{HEAD}:{path}").splitlines()
        if path not in prior:
            shown = subprocess.run(["git", "-C", str(root), "show", f"{base}:{path}"],
                                   capture_output=True, text=True)
            prior[path] = shown.stdout if shown.returncode == 0 else ""
        # Which lines sit inside a fence, read from the whole file: an added line's number
        # only says where it landed, not whether a specimen encloses it.
        inside: list[bool] = []
        fenced = False
        for line in lines:
            inside.append(fenced)
            if line.lstrip().startswith(("```", "~~~")):
                fenced = not fenced
        for number in added[path]:
            if not 1 <= number <= len(lines):
                continue
            line = lines[number - 1]
            if inside[number - 1] or UNLANDED_MARKER in line:
                continue
            for token in SHA_CITATION.findall(line):
                if token in prior[path]:
                    continue
                if token not in resolved:
                    found = subprocess.run(
                        ["git", "-C", str(root), "rev-parse", "--verify", "--quiet",
                         f"{token}^{{commit}}"],
                        capture_output=True, text=True,
                    )
                    resolved[token] = found.stdout.strip() if found.returncode == 0 else None
                commit = resolved[token]
                if commit is None:
                    continue
                ancestor = subprocess.run(
                    ["git", "-C", str(root), "merge-base", "--is-ancestor", commit, HEAD],
                    capture_output=True, text=True,
                )
                if ancestor.returncode == 0:
                    continue
                if reachable_from_upstream(root, commit):
                    continue
                findings.append(
                    f"{path}:{number}: {token}: resolves to a commit that is not an ancestor "
                    f"of HEAD and is not reachable from an upstream ref; mark the line "
                    f"{UNLANDED_MARKER} if that is intended -- {line.strip()}"
                )
    return findings


def reachable_from_upstream(root: Path, commit: str) -> bool:
    """Whether `commit` is reachable from a ref that carries upstream's history.

    One `git for-each-ref --contains` answers every ref at once, and the names are then
    matched against UPSTREAM_REF. A commit reachable only from a lane branch answers no,
    which is the unlanded case the marker exists for; a commit reachable from a patch
    branch, its published or remote-tracking copy, the upstream remote, or a release tag
    answers yes, which is an upstream pin.
    """
    refs = git(root, "for-each-ref", "--contains", commit, "--format=%(refname)",
               check=False)
    return any(UPSTREAM_REF.match(name) for name in refs.splitlines())


def tracked_typescript_lines(root: Path) -> list[tuple[str, int]]:
    """Every tracked `.ts` file with its line count at HEAD, largest first.

    Read from the commit, never the working tree: the budget judges what the change
    lands, and a count taken from the tree would judge bytes the commit does not
    contain -- which is why this check watches nothing and refuses no dirty path. One
    `git grep -c` counts every file's lines in a single process (`-c` counts the lines
    an empty pattern matches, which is every line; a file with no newline at its end
    counts its last line too, and an empty file is absent rather than counted as 0).
    """
    counted = git(root, "grep", "-c", "", HEAD, "--", "*.ts", check=False)
    sizes: list[tuple[str, int]] = []
    for line in counted.splitlines():
        path, _, count = line.rpartition(":")
        try:
            lines = int(count)
        except ValueError:
            continue
        if path.startswith(HEAD + ":"):
            path = path[len(HEAD) + 1:]
        sizes.append((path, lines))
    return sorted(sizes, key=lambda pair: (-pair[1], pair[0]))


def added_typescript(root: Path, base: str) -> list[str]:
    """The `.ts` files the `base..HEAD` diff ADDS, repo-relative.

    `--no-renames` is the diff's own reading everywhere else here, so a rename shows as
    an addition and a deletion: a file that appears at this size is judged, whatever it
    was called before.
    """
    diff = git(root, "diff", "--name-only", "--no-renames", "--diff-filter=A", "-z",
               base, HEAD, "--", "*.ts")
    return [path for path in diff.split("\0") if path]


def generated_copy_of(root: Path, path: str) -> bool:
    """Whether `path` is a byte-identical generated copy of a canonical source.

    Read from the commit, like the size itself: the claim is about what the change
    lands, not about a working tree that may hold an uncommitted edit.
    """
    for generated, canonical in GENERATED_COPIES:
        if not path.startswith(generated):
            continue
        source = canonical + path[len(generated):]
        blob = git(root, "rev-parse", f"{HEAD}:{path}", check=False).strip()
        origin = git(root, "rev-parse", f"{HEAD}:{source}", check=False).strip()
        return bool(blob) and blob == origin
    return False


def typescript_size_findings(root: Path, base: str | None) -> tuple[list[str], list[str]]:
    """The size budget's warnings and refusals, in the terms the report prints.

    A tracked file above `TS_SIZE_WARN_LINES` warns; a file this change ADDS above
    `TS_SIZE_REFUSE_LINES` refuses. The two are separate lists because the run is
    different: a warning is reported beside a VERIFIED headline (the tree's existing
    large files are debt, not this change's fault), while a refusal is a verdict about
    the change and does not advance the baseline. `base` is the commit the change is
    diffed against, and with none nothing can be called added -- that is said out loud
    rather than read as "no file was added".
    """
    sizes = tracked_typescript_lines(root)
    added = set(added_typescript(root, base)) if base is not None else set()
    warnings: list[str] = []
    refusals: list[str] = []
    for path, lines in sizes:
        if lines > TS_SIZE_REFUSE_LINES and path in added and not generated_copy_of(root, path):
            refusals.append(
                f"{path}: {lines:,} lines added by this change, over the "
                f"{TS_SIZE_REFUSE_LINES:,}-line budget for a new .ts file -- split it"
            )
        elif lines > TS_SIZE_WARN_LINES:
            warnings.append(
                f"{path}: {lines:,} lines, over the {TS_SIZE_WARN_LINES:,}-line budget "
                f"for a .ts file"
            )
    if base is None:
        warnings.append(
            "no diff base, so no .ts file can be called added by this change and the "
            f"{TS_SIZE_REFUSE_LINES:,}-line refusal was not judged"

        )
    return warnings, refusals


def identity_findings(root, base, head):
    # Configuration governs commit creation, not which identities history may carry.
    for key in ("user.name", "user.email"):
        configured = subprocess.run(["git", "config", "--show-scope", "--get-all", key],
                                    capture_output=True, text=True)
        if configured.returncode not in (0, 1):
            raise RuntimeError(configured.stderr.strip() or "cannot read deployed identity")
        entries = configured.stdout
        for line in entries.splitlines():
            scope, _, value = line.partition("\t")
            if scope in ("local", "worktree"):
                label = "repo-local" if scope == "local" else scope
                return [f"{label} {key} overrides the deployed identity; unset the {scope} key"]
        values = [line.partition("\t")[2].strip() for line in entries.splitlines()]
        if not values or not values[-1]:
            raise RuntimeError(f"landing identity is not configured ({key})")
    policy = Path(__file__).resolve().parents[1] / "allowed-commit-identities.json"
    allowed = json.loads(policy.read_text(encoding="utf-8"))
    findings = []
    for line in git(root, "log", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce", f"{base}..{head}").splitlines():
        sha, an, ae, cn, ce = line.split("\x1f")
        for role, name, email in (("author", an, ae), ("committer", cn, ce)):
            if [name, email] not in allowed:
                findings.append(f"{sha}: {role} {name} <{email}> is not an allowed repository identity")
    return findings


# --- documents: a remembered pass per document ------------------------------------
#
# A document's verdict is remembered in the gate's own result store -- the directory the
# runner keeps tree checks' remembered passes in, `<git common dir>/tillr-gate/results`,
# one `<key>.<pass|fail>.json` file per verdict, in the runner's own field names -- under
# a key over everything the renderer reads to judge that one document. A pass is reused
# only when no fail was ever recorded under the same key, so a remembered fail can never
# become a pass, and only when it was recorded by the renderer version that would judge
# the document now. The key's inputs, and why each is there:
#
# * the document's path, mode and blob;
# * the bytes of this check (its declaration and both Python files), so a change to how
#   documents are judged forgets every remembered pass;
# * every `.vantage.toml` and `.vantageignore`, which set rule severities;
# * the names in the document's own directory (`ref/unlinked-file` judges a filename in
#   prose against the files beside the document);
# * every tree entry the document's text names -- a link, reference definition, `href` or
#   `src`, resolved against the document's directory and the repository root, files and
#   directories alike, and any other word that resolves to a file. The target's object id
#   is the input: a moved heading changes the target blob, so `link/dead-section-anchor`
#   is judged again;
# * every `roadmap.md`, when a config turns on `planning/unrouted`, the one rule that
#   reads documents a page does not name.
#
# The key reads the commit, but the renderer reads the working tree, so the two agree only
# where the checkout matches the commit. A pass is therefore NOT remembered for a document
# whose verdict may have read something the commit does not hold: a link destination that
# is absent at HEAD but present on disk (an ignored or untracked file makes a link resolve
# that the commit leaves dead), a tracked input -- a named entry, a sibling, a config --
# that is modified or deleted on disk, or a `.vantage.toml` or `.vantageignore` on disk,
# in the document's directory or above it, that HEAD lacks. Such a pass may be the
# working tree's verdict rather than the commit's, and the store would carry it to every
# checkout that shares the git common dir. An untracked sibling is not a reason: it can
# only add a `ref/unlinked-file` finding, never remove one. The verdict itself is still
# the run's, exactly as it was before memory.
#
# A failing run is remembered only when it is attributable: every error finding names,
# after normalizing its path, a document the run was given. A finding on anything else --
# a spelling the check cannot match, or a file it did not ask about -- means the check
# cannot tell which documents passed, so that run remembers nothing.
#
# The renderer's version is not in the key, because asking for it costs a process start
# (about 0.28 s): a run that renders anything reads the version from its own JSON report,
# and only a run with every document remembered asks `vantage-check version`. A
# remembered pass from another version is judged again, in one run with the rest.
#
# Every document left to judge is rendered in ONE renderer run, as before. Measured on
# 2026-11-16 on the four-document candidate of the drain fault, one run per document in
# parallel was slower than one run over all four (2.0-2.2 s against 1.9-2.0 s alone, and
# 3.7-4.0 s against 2.7 s at ten concurrent gates): the renderer's start-up, not its
# per-file work, is the cost.
#
# Memory is an optimization of a verdict, never a verdict: a store that cannot be read or
# written loses a hit, not a result. A result file is renamed into place but not fsynced --
# a crash may lose one, and an unreadable file reads as absent, which renders the document.

DOCUMENT_MEMORY_FORMAT = "documents-1"
DOCUMENT_CHECK_FILES = ("tools/gate/checks/documents.json", "tools/gate/checks/documents.py",
                        "tools/gate/checks/change_common.py", "tools/gate/checks/roadmap_sections.py",
                        "tools/tillr-gate/classify.py")
DOCUMENT_CONFIG_NAMES = (".vantage.toml", ".vantageignore")
LINK_DESTINATIONS = (
    re.compile(r"\]\(\s*<([^>\n]*)>"),
    re.compile(r"\]\(\s*([^)\s]+)"),
    re.compile(r"^ {0,3}\[[^\]\n]+\]:\s*<?([^\s>]+)", re.M),
    re.compile(r"""(?:href|src)\s*=\s*["']([^"'\n]+)["']""", re.I),
)
WORD = re.compile(r"[^\s()<>\[\]{}\"'`|,;*]+")


def tree_entries(root):
    """Every entry at HEAD, trees included: path -> (mode, type, object)."""
    entries = {}
    for entry in git(root, "ls-tree", "-r", "-t", "-z", HEAD).split("\0"):
        if not entry:
            continue
        metadata, _, path = entry.partition("\t")
        mode, kind, obj = metadata.split()
        entries[path] = (mode, kind, obj)
    return entries


def named_inputs(text, path, entries):
    """The tree entries a document's text names, as `path -> object id`, and the link
    destinations it names that HEAD does not hold."""
    here = posixpath.dirname(path)
    named = {}
    absent = set()

    def resolve(token, linked):
        if "://" in token or token.startswith(("mailto:", "#")):
            return
        token = unquote(token.split("#", 1)[0].split("?", 1)[0]).rstrip(".:,").rstrip("/")
        if not token:
            return
        for candidate in (posixpath.normpath(posixpath.join(here, token)),
                          posixpath.normpath(token.lstrip("/"))):
            if candidate.startswith("..") or candidate == ".":
                continue
            entry = entries.get(candidate)
            if entry and (linked or entry[1] == "blob"):
                named[candidate] = entry[2]
            elif linked and not entry:
                absent.add(candidate)

    for pattern in LINK_DESTINATIONS:
        for token in pattern.findall(text):
            resolve(token.strip(), True)
    for token in WORD.findall(text):
        resolve(token, False)
    return named, absent


def document_keys(root, documents):
    """Each document's memory key -- a hash of every input its verdict reads -- and the
    documents whose inputs differ on disk from HEAD, whose pass must not be remembered."""
    entries = tree_entries(root)
    configs = {path: entry[2] for path, entry in entries.items()
               if posixpath.basename(path) in DOCUMENT_CONFIG_NAMES}
    roadmaps = {}
    for path in configs:
        if posixpath.basename(path) == ".vantage.toml" and "unrouted" in git(root, "show", f"{HEAD}:{path}"):
            roadmaps = {p: e[2] for p, e in entries.items() if posixpath.basename(p) == "roadmap.md"}
            break
    code = {path: entries.get(path, ("", "", "absent"))[2] for path in DOCUMENT_CHECK_FILES}
    keys = {}
    watched = {}
    unsettled = {}
    for path in documents:
        mode, _, blob = entries[path]
        here = posixpath.dirname(path)
        siblings = sorted(p for p in entries if posixpath.dirname(p) == here)
        text = git(root, "cat-file", "blob", blob)
        named, absent = named_inputs(text, path, entries)
        # A symlink's Git object binds the link text, not the contents its target
        # exposes to the renderer. That mutable target cannot certify a tree pass.
        symlinks = sorted(p for p in named if entries[p][0] == "120000")
        if symlinks:
            unsettled[path] = symlinks
        identity = {"format": DOCUMENT_MEMORY_FORMAT, "check": "documents", "code": code,
                    "configs": configs, "roadmaps": roadmaps, "path": path, "mode": mode,
                    "blob": blob, "siblings": siblings, "named": named}
        raw = json.dumps(identity, sort_keys=True).encode()
        keys[path] = hashlib.sha256(raw).hexdigest()
        watched[path] = set(named) | set(siblings) | set(configs) | set(roadmaps)
        ancestors, step = [], here
        while True:
            ancestors.append(step)
            if not step:
                break
            step = posixpath.dirname(step)
        on_disk = sorted(candidate for candidate in absent | {
            posixpath.join(directory, name) for directory in ancestors for name in DOCUMENT_CONFIG_NAMES}
            if candidate not in entries and os.path.lexists(root / candidate))
        if on_disk:
            unsettled.setdefault(path, []).extend(on_disk)
    changed = tracked_changes(root, set().union(*watched.values()))
    for path in documents:
        differ = sorted(p for p in changed if any(p == w or p.startswith(w + "/") for w in watched[path]))
        if differ:
            unsettled.setdefault(path, []).extend(differ)
    return keys, unsettled


def tracked_changes(root, paths):
    """The tracked paths, among `paths` and below them, that differ on disk from HEAD."""
    if not paths:
        return set()
    status = git(root, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=no",
                 "--ignore-submodules=all", "--", *(":(literal)" + p for p in sorted(paths)))
    return {entry[3:] for entry in status.split("\0") if len(entry) > 3}


def result_store(root):
    common = git(root, "rev-parse", "--path-format=absolute", "--git-common-dir").strip()
    return Path(common) / "tillr-gate" / "results"


def stored_result(store, key, verdict, check_id=None):
    try:
        data = json.loads((store / f"{key}.{verdict}.json").read_text())
    except (OSError, ValueError):
        return None
    if not isinstance(data, dict) or not isinstance(data.get("result"), str) or not data.get("time"):
        return None
    word = data["result"]
    try:
        # JSON can contain escaped lone surrogates. The Go decoder substitutes
        # replacement characters, so such a record cannot make a wire round trip.
        word.encode("utf-8")
    except UnicodeEncodeError:
        return None
    from classify import classify

    recorded_id = data.get("check_id")
    selected_id = check_id or (recorded_id if isinstance(recorded_id, str) and recorded_id else "documents")
    answer, = classify([{"check": selected_id, "source": "cached-record", "word": word}])
    # A record under this key's pass/fail suffix must actually have that verdict.
    return data if answer["outcome"] == verdict else None


def store_result(store, key, verdict, duration_ms, tree, tool, check_id="documents"):
    record = {"check_id": check_id, "result": verdict, "duration_ms": duration_ms,
              "time": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
              "tree": tree, "tool": tool}
    temporary = None
    try:
        store.mkdir(parents=True, exist_ok=True)
        handle, temporary = tempfile.mkstemp(dir=store, prefix=".result-")
        with os.fdopen(handle, "w") as out:
            json.dump(record, out)
        os.replace(temporary, store / f"{key}.{verdict}.json")
        temporary = None
    except OSError as error:
        print(f"documents: could not remember a {verdict} ({error}); the verdict stands", file=sys.stderr)
    finally:
        if temporary:
            try:
                os.unlink(temporary)
            except OSError:
                pass


def renderer_version():
    """`vantage-check version`'s number, or None when it cannot say."""
    asked = subprocess.run(["uvx", "vantage-check", "version"], capture_output=True, text=True)
    words = asked.stdout.split()
    return words[-1] if asked.returncode == 0 and words else None


def render(paths, locations=None):
    """One renderer run over `paths`: its exit, its JSON report (None when unreadable), its time."""
    started = time.monotonic()
    rendered = subprocess.run(["uvx", "vantage-check", "check", "--format", "json", *paths],
                              capture_output=True, text=True)
    duration_ms = int((time.monotonic() - started) * 1000)
    try:
        report = json.loads(rendered.stdout)
    except ValueError:
        report = None
    if not isinstance(report, dict) or not isinstance(report.get("findings"), list):
        report = None
    for finding in (report or {}).get("findings", []):
        name, line = finding.get("file"), finding.get("line")
        location = (locations or {}).get(reported(Path.cwd(), name))
        if location:
            name = location[0]
            if isinstance(line, int):
                line += location[1]
        print(f"{name}:{line}:{finding.get('column')}: "
              f"{finding.get('severity')} {finding.get('rule')} {finding.get('message')}")
    for failure in (report or {}).get("failures") or []:
        print(f"could not check: {json.dumps(failure, sort_keys=True)}")
    if report is None and rendered.stdout.strip():
        print(rendered.stdout, end="")
    print(rendered.stderr, end="", file=sys.stderr)
    return rendered.returncode, report, duration_ms


def judge_documents(root, documents):
    keys, unsettled = document_keys(root, documents)
    paths, locations, temporary = roadmap_sections.prepare(
        root, documents, sys.modules[__name__], keys, unsettled)
    try:
        return judge_document_set(root, paths, keys, unsettled, locations)
    finally:
        roadmap_sections.cleanup(temporary)


def judge_document_set(root, documents, keys, unsettled, locations):
    """Judge the changed documents in one renderer run, skipping remembered passes.

    The installed classifier judges the raw renderer exit and report facts. Memory only
    narrows which documents the run is given. From a passing run every document is
    remembered as a pass; from a failing one whose every error finding names a document it
    was given, a document with an error finding is remembered as a fail and one with no
    finding at all as a pass. Nothing is remembered from a run that could not judge or whose
    failure is not attributable, nor for a document the report says it could not check, and
    no pass is remembered for a document whose inputs differ on disk from the commit.
    """
    store = result_store(root)
    tree = git(root, "rev-parse", f"{HEAD}^{{tree}}").strip()
    remembered = {}
    for path in documents:
        passed = stored_result(store, keys[path], "pass")
        if passed and path not in unsettled and not stored_result(store, keys[path], "fail"):
            remembered[path] = passed
    pending = [path for path in documents if path not in remembered]
    if pending:
        code, report, duration_ms = render(pending, locations)
        version = report.get("version") if report else None
        # A run that produced no report could not judge, whatever is remembered.
        stale = [path for path, passed in remembered.items() if version and passed.get("tool") != version]
    else:
        version = renderer_version()
        stale = [path for path, passed in remembered.items() if passed.get("tool") != version]
    if stale:
        # Remembered under another renderer, or the renderer could not say which it is: the
        # pass is not this renderer's verdict, so every document is judged in one run.
        print(f"documents: {len(stale)} remembered pass(es) are not from renderer {version}; judging again",
              file=sys.stderr)
        pending = list(documents)
        code, report, duration_ms = render(pending, locations)
        version = report.get("version") if report else None
    for path in documents:
        if path not in pending:
            location = locations.get(path)
            label = f"{location[0]} (section at line {location[2]})" if location else path
            print(f"documents: {label}: remembered pass at {remembered[path]['time']}", file=sys.stderr)
    if len(documents) > 1 or len(pending) < len(documents):
        units = "render unit(s)" if locations else "document(s)"
        print(f"documents: {len(documents)} {units}: {len(pending)} rendered, "
              f"{len(documents) - len(pending)} remembered", file=sys.stderr)
    if not pending:
        return 0
    from classify import classify

    findings = report.get("findings", []) if report else []
    answer, = classify([{"check": "documents", "source": "document-render", "exit_code": code,
                         "report_read": report is not None, "has_finding": bool(findings)}])
    outcome = answer["outcome"]
    if outcome not in ("pass", "fail"):
        raise RuntimeError(f"uvx vantage-check exited {code} without a document finding")
    verdict = 1 if outcome == "fail" else 0
    if not version:
        return verdict
    unchecked = {reported(root, failure.get("file")) for failure in report.get("failures") or []
                 if isinstance(failure, dict)}
    flagged = {reported(root, finding.get("file")) for finding in findings if isinstance(finding, dict)}
    errors = {reported(root, finding.get("file")) for finding in findings
              if isinstance(finding, dict) and finding.get("severity") == "error"}
    if outcome == "fail" and (not errors or not errors <= set(pending)):
        print("documents: the run failed on findings it does not attribute to a document it was given; "
              "nothing is remembered from it", file=sys.stderr)
        return verdict
    for path in pending:
        if path in unchecked:
            continue
        if path in errors:
            store_result(store, keys[path], "fail", duration_ms, tree, version)
        elif path in flagged:
            continue
        elif path in unsettled:
            print(f"documents: {path}: pass not remembered; it read what differs on disk from the commit: "
                  f"{', '.join(unsettled[path])}", file=sys.stderr)
        else:
            store_result(store, keys[path], "pass", duration_ms, tree, version)
    return verdict


def reported(root, name):
    """A file the renderer names, as a repository-relative path; None for no name."""
    if not isinstance(name, str) or not name:
        return None
    if posixpath.isabs(name):
        name = os.path.relpath(name, root)
    return posixpath.normpath(name)


def run(kind, args):
    global HEAD
    try:
        if len(args) != 2:
            raise RuntimeError("missing base and head arguments; supply BASE HEAD")
        base, HEAD = args
        validate(base, HEAD)
        root = Path.cwd()
        if kind == "commit-identity":
            # Identity depends on commit metadata, not changed paths or Markdown.
            # Do not spend its fast-check budget discovering unrelated documents.
            findings = identity_findings(root, base, HEAD)
            if findings:
                print("\n".join(findings))
                return 1
            return 0
        changed = git(root, "diff", "--name-only", "--no-renames", "-z", base, HEAD).split("\0")
        documents = [path for path in changed if path.endswith(".md")
                     and not any(part in (".forks", "node_modules", ".yolo") for part in Path(path).parts)
                     and git(root, "ls-tree", "--name-only", HEAD, "--", path)]
        if kind == "citations":
            findings = unreachable_citations(root, base, documents)
        elif kind == "file-size":
            _, findings = typescript_size_findings(root, base)
        elif kind == "documents":
            if not documents:
                return 0
            if git(root, "rev-parse", "HEAD").strip() != git(root, "rev-parse", HEAD).strip():
                raise RuntimeError("documents require the named head to be checked out")
            if git(root, "status", "--porcelain", "--", *documents).strip():
                raise RuntimeError("changed Markdown files are dirty")
            return judge_documents(root, documents)
        else:
            raise RuntimeError(f"unknown change check {kind}")
        if findings:
            print("\n".join(findings))
            return 1
        return 0
    except (RuntimeError, OSError, ValueError) as error:
        print(f"{kind}: could not judge: {error}", file=sys.stderr)
        return 4
