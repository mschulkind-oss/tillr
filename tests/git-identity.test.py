#!/usr/bin/env python3
"""Every commit this range adds is authored and committed by the configured identity.

WHY THIS EXISTS. On 2026-10-28 an agent hand-landing a lane passed
`-c user.name="Matthew Schulkind" -c user.email="matt@mschulkind.com"` on every commit,
because AGENTS.md told it to "amend the landed commit to the landing identity" without
saying where that identity comes from. Twenty-one commits carried the invented address,
fourteen of them published, and the repair was a full history rewrite. Prose cannot
prevent that -- the instruction was the prose -- so the act is what gets checked, and this
is the act: the author and committer of every commit the change ADDS must be the identity
`git config` states.

That is not a new rule imposed from outside. It is already what the lander does:
`tools/tillr-land` reads `user.name`/`user.email`, refuses to land when the repository
states none or states one in a scope a lane can write, and re-authors every commit it
publishes to them (`Trunk.NormalizeHead`). This check keeps
a hand landing honest with the same identity those paths use, so inventing one is a gate
refusal at the first commit instead of a rewrite later.

THE RANGE, AND WHY IT IS A RANGE. This repository already holds work-identity commits
(`owner@work.example.invalid`, `owner@work.example.invalid`) from machines whose
global identity differs, and the patch branches carry upstream authors. A whole-history
check would refuse every run over history nobody can change, so this reads BASE..HEAD: the
commits the change brings.

THE BASE is `FORK_CHECK_PLAN_BASE` when the lander set it -- the batch base the gate is
judging -- and otherwise `merge-base HEAD origin/main`. A checkout with no `origin/main`
cannot answer the question, and says so instead of passing: "could not ask" and "asked and
fine" must never look alike.

THE IDENTITY IS THE DEPLOYED ONE, AND THE REPOSITORY'S OWN CONFIG MAY NOT SET IT. A
worktree shares `.git/config` with every other worktree of this repository, so a
`user.name`/`user.email` there replaces the deployed identity for every commit and every
landing in every worktree, and any lane can write it. On 2026-11-02 a lane fixture wrote
`t <t@e.invalid>` into it at 15:02:50Z; the 16:19 gate then refused the 33 correctly
authored commits in its range because they did not match the fixture (breakage 3), and the
same value would have made the lander re-author landed commits to it.

So the repo-local layer is asked about separately and refused before any range is judged,
and the identity the commits are judged against is read from the scopes a lane cannot
write: the deployed global config (rcm deploys `gitconfig` and the tag's
`gitconfig.local` there), the system config, and the caller's command scope (a harness
declaring its own identity, as this repository's own suites do). The per-worktree scope is
ignored too, so a lane that enables `extensions.worktreeConfig` cannot steer the answer
either. A lane's write therefore cannot change the identity this check reads: it is
refused by name, and the value it would have supplied is never the one judged against.

Exit status: 0 clean, or nothing to judge because the run has no diff base; 1 a finding
(a commit carries an identity that is not the deployed one, no identity is configured at
all, or the repository's own config overrides it); 2 the identity or the range could not
be read.
"""

import os
import subprocess
import sys

PLAN_BASE_ENV = "FORK_CHECK_PLAN_BASE"
MAX_REPORTED = 10
# The identity keys, which is also the whole set of keys a repo-local override may use to
# steer a commit's author or committer.
IDENTITY_KEYS = ("user.name", "user.email")
# The scopes a lane can write. `local` is the repository's own config, shared by every
# worktree of the checkout, so a lane's `git config user.name` lands there and would
# otherwise be what the trunk gate expects. `worktree` is per-worktree: a lane's write
# there cannot reach the trunk, but it is not the deployed identity either.
LANE_WRITABLE_SCOPES = ("local", "worktree")


def git(*args: str) -> subprocess.CompletedProcess:
    return subprocess.run(["git", *args], capture_output=True, text=True)


def configured(key: str) -> tuple[str, str]:
    """The deployed value of one identity key, and why it could not be read.

    `--show-scope` lists entries in increasing priority order with the scope each came
    from, so the last one outside `LANE_WRITABLE_SCOPES` is the value the deployed config
    states. A value that exists only in the repository's own config is not the deployed
    identity: `local_identity_overrides` refuses it by name rather than letting it answer.
    """
    listed = git("config", "--show-scope", "--get-all", key)
    if listed.returncode == 1:
        return "", ""
    if listed.returncode != 0:
        detail = listed.stderr.strip().splitlines()
        return "", detail[0] if detail else f"git config exited {listed.returncode}"
    value = ""
    for line in listed.stdout.splitlines():
        scope, _, entry = line.partition("\t")
        if scope in LANE_WRITABLE_SCOPES:
            continue
        value = entry.strip()
    return value, ""


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


def local_identity_overrides() -> list[tuple[str, str]]:
    """The identity keys this repository's own config sets, with the values it sets.

    `--local` is the repository config. In a linked worktree that is the file every
    worktree of this repository SHARES (the common dir's `config`), which is exactly why
    it is read here: a lane's write is the main checkout's identity too. `--worktree`
    would be the worktree-private file instead, and it needs `extensions.worktreeConfig`,
    which this repository does not enable -- so a value a lane writes lands in `--local`
    whether it meant to or not.
    """
    found = []
    for key in IDENTITY_KEYS:
        got = git("config", "--local", "--get", key)
        if got.returncode == 0 and got.stdout.strip():
            found.append((key, got.stdout.strip()))
    return found


def main() -> int:
    # The deployed identity is read first, so the refusal below can name the value the
    # repository's own config is overriding. Reading it is not judging a range.
    name, problem = configured("user.name")
    if problem:
        return refuse(f"the configured git identity could not be read: {problem}")
    email, problem = configured("user.email")
    if problem:
        return refuse(f"the configured git identity could not be read: {problem}")

    overrides = local_identity_overrides()
    if overrides:
        set_keys = ", ".join(f"{key} = {value!r}" for key, value in overrides)
        print("git-identity: FAIL: this repository's own config sets the landing identity "
              f"({set_keys}), so the identity a commit carries is a value a lane can write "
              "rather than the one this machine deploys:")
        print("  why: `.git/config` is shared by every worktree of this repository, so a "
              "value there replaces the deployed identity for every commit and every "
              "landing in every worktree.")
        if name and email:
            print(f"  deployed: {name} <{email}> -- the identity this check reads, and the "
                  "one the override above is not allowed to replace")
        else:
            print("  deployed: no identity is configured outside the repository's own "
                  "config, so unsetting the override leaves nothing to judge against")
        print("  remedy: unset it, so the deployed global identity answers: "
              "git config --local --unset-all user.name && "
              "git config --local --unset-all user.email")
        return 1

    if not name or not email:
        # The wording `tools/tillr-land` prints, so one incident has one sentence.
        print("git-identity: FAIL: the landing identity is not configured "
              "(git user.name and user.email)")
        print("  remedy: declare it where the repository deploys it (`gitconfig` for the "
              "name, tag-personal/ or tag-work/gitconfig.local for the email), then rcup. "
              "The deployed identity is the global scope: a repository-local identity is "
              "refused because every worktree shares `.git/config`, so a lane can rewrite "
              "it. Never pass -c user.* to work around this: that is the act this check "
              "refuses.")
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
        for role, actual, wanted in (
            ("author name", author_name, name),
            ("author email", author_email, email),
            ("committer name", committer_name, name),
            ("committer email", committer_email, email),
        ):
            if actual != wanted:
                findings.append(f"{sha[:10]}  {role} is {actual!r}, want {wanted!r}")

    if findings:
        print(f"git-identity: FAIL: {len(findings)} commit field(s) in {base[:10]}..HEAD "
              f"carry an identity that is not the configured one ({name} <{email}>):")
        for finding in findings[:MAX_REPORTED]:
            print(f"  {finding}")
        if len(findings) > MAX_REPORTED:
            print(f"  ... and {len(findings) - MAX_REPORTED} more")
        print("  remedy: re-author those commits to the configured identity (git rebase "
              "--exec 'git commit --amend --no-edit --reset-author'), or drop them. Do not "
              "widen this check or pass a second identity: the point is that there is one.")
        return 1

    print(f"git-identity: {judged} commit(s) in {base[:10]}..HEAD carry {name} <{email}>")
    return 0


if __name__ == "__main__":
    sys.exit(main())
