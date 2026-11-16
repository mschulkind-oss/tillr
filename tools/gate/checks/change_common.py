"""Change-only checks. The caller supplies both commits; no gate record is read."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys

HEAD = "HEAD"
LEGACY_SEED = "config/tillr/seed-items.json"
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


def legacy_seed_findings(root: Path, base: str | None) -> list[str]:
    """What the intake freeze refuses about a change to the legacy seed.

    `config/tillr/seed-items.json` is intake-frozen under `D-QP1`
    (docs/design/tillr-queue-persistence.md Section 3.1): it may only shrink. A surviving
    entry may not change a field, and a net-new `item_id` may not appear. Removal is the
    one permitted edit, because the freeze exists so terminal rows leave as they land or
    resolve. `base` is the commit the change is diffed against; with none nothing can be
    called added, and the plan says so rather than reading silence as a pass.
    """
    if base is None:
        return []
    path = root / LEGACY_SEED
    if not git(root, "ls-tree", "--name-only", HEAD, "--", LEGACY_SEED):
        # The file is gone: every entry was removed, which is the one permitted edit.
        return []
    try:
        current = json.loads(git(root, "show", f"{HEAD}:{LEGACY_SEED}"))
    except OSError as error:
        return [f"{LEGACY_SEED}: cannot read the intake-frozen seed: {error}"]
    except json.JSONDecodeError as error:
        return [f"{LEGACY_SEED}: the intake-frozen seed is not JSON: {error}"]
    before_raw = git(root, "show", f"{base}:{LEGACY_SEED}", check=False)
    if not before_raw.strip():
        # The file did not exist at the base, so every entry it holds now is net-new.
        before: dict = {"items": []}
    else:
        try:
            before = json.loads(before_raw)
        except json.JSONDecodeError as error:
            return [f"{LEGACY_SEED}: the seed at {base[:12]} is not JSON: {error}"]

    def entries(document: dict) -> dict[str, dict]:
        items = document.get("items")
        if not isinstance(items, list):
            return {}
        return {item["item_id"]: item for item in items
                if isinstance(item, dict) and isinstance(item.get("item_id"), str)}

    old = entries(before)
    new = entries(current)
    remedy = ("file the item through the store and the owning document, not the legacy "
              "seed")
    findings: list[str] = []
    for item_id in sorted(set(new) - set(old)):
        findings.append(
            f"{LEGACY_SEED}: {item_id} is a net-new item_id, and the file is "
            f"intake-frozen under D-QP1 -- {remedy}")
    for item_id in sorted(set(old) & set(new)):
        if old[item_id] == new[item_id]:
            continue
        fields = sorted(
            key for key in set(old[item_id]) | set(new[item_id])
            if old[item_id].get(key) != new[item_id].get(key))
        changed = ", ".join(fields) if fields else "fields"
        findings.append(
            f"{LEGACY_SEED}: {item_id} is a surviving entry and its {changed} changed; "
            f"the file is intake-frozen under D-QP1 -- {remedy}")
    return findings




def identity_findings(root, base, head):
    expected = []
    for key in ("user.name", "user.email"):
        local = subprocess.run(["git", "config", "--local", "--get", key], capture_output=True, text=True)
        if local.returncode not in (0, 1):
            raise RuntimeError(local.stderr.strip() or "cannot read local identity")
        if local.returncode == 0 and local.stdout.strip():
            return [f"repo-local {key} overrides the deployed identity; unset the local key"]
        configured = subprocess.run(["git", "config", "--show-scope", "--get-all", key],
                                    capture_output=True, text=True)
        if configured.returncode not in (0, 1):
            raise RuntimeError(configured.stderr.strip() or "cannot read deployed identity")
        entries = configured.stdout
        for line in entries.splitlines():
            scope, _, value = line.partition("\t")
            if scope in ("local", "worktree"):
                return [f"{scope} {key} overrides the deployed identity; unset the {scope} key"]
        values = [line.partition("\t")[2].strip() for line in entries.splitlines()]
        if not values or not values[-1]:
            raise RuntimeError(f"landing identity is not configured ({key})")
        expected.append(values[-1])
    findings = []
    for line in git(root, "log", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce", f"{base}..{head}").splitlines():
        sha, an, ae, cn, ce = line.split("\x1f")
        if [an, ae, cn, ce] != expected * 2:
            findings.append(f"{sha}: author or committer differs from deployed identity")
    return findings


def run(kind, args):
    global HEAD
    try:
        if len(args) != 2:
            raise RuntimeError("missing base and head arguments; supply BASE HEAD")
        base, HEAD = args
        validate(base, HEAD)
        root = Path.cwd()
        changed = git(root, "diff", "--name-only", "--no-renames", "-z", base, HEAD).split("\0")
        documents = [path for path in changed if path.endswith(".md")
                     and not any(part in (".forks", "node_modules", ".yolo") for part in Path(path).parts)
                     and git(root, "ls-tree", "--name-only", HEAD, "--", path)]
        if kind == "commit-identity":
            findings = identity_findings(root, base, HEAD)
        elif kind == "seed-freeze":
            findings = legacy_seed_findings(root, base) if LEGACY_SEED in changed else []
        elif kind == "citations":
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
            rendered = subprocess.run(["uvx", "vantage-check", *documents], capture_output=True, text=True)
            print(rendered.stdout + rendered.stderr, end="")
            if rendered.returncode == 0:
                return 0
            if rendered.returncode == 1 and rendered.stdout.strip():
                return 1
            raise RuntimeError(f"uvx vantage-check exited {rendered.returncode} without a document finding")
        else:
            raise RuntimeError(f"unknown change check {kind}")
        if findings:
            print("\n".join(findings))
            return 1
        return 0
    except (RuntimeError, OSError, ValueError) as error:
        print(f"{kind}: could not judge: {error}", file=sys.stderr)
        return 4
