#!/usr/bin/env python3
"""Check author and committer against this repository's two allowed identity pairs.

This repository's history filter is not tillr's rule for creating commits. The filter
shares the tracked list used by the live gate and does not derive it from Git config.
It reads only BASE..HEAD: patch branches carry upstream authors, and existing history
is not judged. BASE is `FORK_CHECK_PLAN_BASE` when set, otherwise the merge base with
`origin/main` (falling back to `origin/HEAD`). With no usable base, no range is judged.

Still require a configured identity for commit creation and reject local or worktree
identity keys, even if they match an allowed pair. Lanes can write those scopes, and
repo-local config is shared by all worktrees.

Exit status: 0 clean or no diff base; 1 disallowed identity in the range, missing
configured identity, or a writable config override; 2 could not read config, policy,
or the range.
"""

import json
import os
from pathlib import Path
import subprocess
import sys

PLAN_BASE_ENV = "FORK_CHECK_PLAN_BASE"
MAX_REPORTED = 10
# The identity keys, which is also the whole set of keys a repo-local override may use to
# steer a commit's author or committer.
IDENTITY_KEYS = ("user.name", "user.email")
LANE_WRITABLE_SCOPES = ("local", "worktree")
POLICY = Path(__file__).resolve().parents[1] / "tools/gate/allowed-commit-identities.json"


def git(*args: str) -> subprocess.CompletedProcess:
    return subprocess.run(["git", *args], capture_output=True, text=True)


def configured(key: str) -> tuple[str, list[str], str]:
    """Read the deployed value and detect any local or worktree override of the key."""
    listed = git("config", "--show-scope", "--get-all", key)
    if listed.returncode == 1:
        return "", [], ""
    if listed.returncode != 0:
        detail = listed.stderr.strip().splitlines()
        return "", [], detail[0] if detail else f"git config exited {listed.returncode}"
    value = ""
    overrides = []
    for line in listed.stdout.splitlines():
        scope, _, entry = line.partition("\t")
        if scope in LANE_WRITABLE_SCOPES:
            overrides.append(scope)
        else:
            value = entry.strip()
    return value, overrides, ""


def refuse(message: str) -> int:
    """The question could not be asked. Never a pass, and never a verdict about content."""
    print(f"git-identity: {message}")
    return 2


def base_of(env=os.environ) -> str:
    """The commit this run judges from, or "" when this checkout cannot say."""
    named = env.get(PLAN_BASE_ENV, "").strip()
    if named:
        resolved = git("rev-parse", "--verify", "--quiet", named + "^{commit}")
        return resolved.stdout.strip() if resolved.returncode == 0 else ""
    # No lander base: the branch's fork point from the published trunk. `origin/main` and
    # not a hard-coded `main`, because a lane worktree's local `main` is the trunk as that
    # worktree last read it.
    for upstream in ("origin/main", "origin/HEAD"):
        merged = git("merge-base", "HEAD", upstream)
        if merged.returncode == 0 and merged.stdout.strip():
            return merged.stdout.strip()
    return ""


def main() -> int:
    values = []
    overrides = []
    for key in IDENTITY_KEYS:
        value, scopes, problem = configured(key)
        if problem:
            return refuse(f"the configured git identity could not be read: {problem}")
        values.append(value)
        overrides.extend((scope, key) for scope in scopes)

    if overrides:
        print("git-identity: FAIL: this repository's own config sets the landing identity")
        for scope, key in dict.fromkeys(overrides):
            print(f"  {scope} {key} overrides the deployed identity; "
                  f"unset it with git config --{scope} --unset-all {key}")
        return 1

    if not all(values):
        print("git-identity: FAIL: the landing identity is not configured "
              "(git user.name and user.email)")
        print("  remedy: configure the deployed Git identity outside local and worktree scope.")
        return 1

    base = base_of()
    if not base:
        # NOT a refusal. With no diff base there are no added commits to judge -- the same
        # position the `.ts` size budget takes ("no diff base, so no .ts file can be called
        # added by this change and the 3,000-line refusal was not judged"), and a refusal
        # here made a whole gate run reach no verdict in every checkout without an
        # `origin/main` (measured: the gate's own self-suite fixtures, plan `base=-`).
        # A reader still sees it: the plan line prints `base=-`.
        print(f"git-identity: no diff base, so no commit can be called added by this change "
              f"and its identity was not judged (no usable {PLAN_BASE_ENV}, and no origin/main "
              f"to take a merge base from)")
        return 0

    try:
        allowed = json.loads(POLICY.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        return refuse(f"cannot read the repository identity policy: {error}")

    listed = git("log", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce", f"{base}..HEAD")
    if listed.returncode != 0:
        return refuse(f"cannot read commits in {base[:10]}..HEAD: {listed.stderr.strip()}")

    findings = []
    judged = 0
    for line in listed.stdout.splitlines():
        if not line.strip():
            continue
        sha, author_name, author_email, committer_name, committer_email = line.split("\x1f")
        judged += 1
        for role, name, email in (("author", author_name, author_email),
                                  ("committer", committer_name, committer_email)):
            if [name, email] not in allowed:
                findings.append(f"{sha}: {role} {name} <{email}> is not an allowed repository identity")

    if findings:
        print(f"git-identity: FAIL: {len(findings)} disallowed author/committer pair(s) "
              f"in {base[:10]}..HEAD:")
        for finding in findings[:MAX_REPORTED]:
            print(f"  {finding}")
        if len(findings) > MAX_REPORTED:
            print(f"  ... and {len(findings) - MAX_REPORTED} more")
        print("  remedy: drop the offending commits or recreate them with an approved identity "
              "read from Git config.")
        return 1

    print(f"git-identity: {judged} commit(s) in {base[:10]}..HEAD carry only approved identities")
    return 0


if __name__ == "__main__":
    sys.exit(main())
