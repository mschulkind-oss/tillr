# This repository's git hooks

`just hooks-install` (`bin/hooks-install`) copies every executable in this directory into
the repository's **shared** hooks directory (`git rev-parse --git-common-dir`/hooks), so
one install covers the main checkout and every linked worktree.

**It installs a copy, not a link, and it says what it found on every run.** A link into a
checkout follows whatever revision that checkout holds, and a link into a lane's worktree
dangles once the lane is removed; a copy is the revision git actually runs. So the two can
drift. The installer compares the installed copy with `hooks/<name>` every time it runs
and reports the comparison, one line per hook:

```console
$ just hooks-install
hooks-install: installed pre-commit -> /workspace/.git/hooks/pre-commit
$ just hooks-install
hooks-install: left alone pre-commit (identical to /workspace/hooks/pre-commit)
$ just hooks-install          # after hooks/pre-commit changed
hooks-install: drifted pre-commit (installed copy differed from /workspace/hooks/pre-commit; replaced) -> /workspace/.git/hooks/pre-commit
```

Every executable here carries a `tillr-hook: <name>` line, and that line is the
ownership test: a marked copy is ours to replace, and a copy without it belongs to
somebody else — it is neither overwritten nor discarded, but moved to `<name>.local`,
which this repository's hook of that name runs first, with the same arguments and stdin
git gave it. So a hook the machine already had (an attribution check, say) keeps running in
front of ours, and the installer reports `left alone <name> (no tillr-hook marker;
kept as <name>.local and chained)`. An existing `<name>.local` with different content is a
conflict: the installer refuses rather than clobber either hook. A hook here without its
marker is refused at install time, before anything is written.

**A replacing `core.hooksPath` makes the install invisible.** git reads `core.hooksPath`
*instead of* the repository's hooks directory, so a global setting means these copies are
written where git never looks and every hook here is off. The installer checks for it and
says so.

## `pre-commit` — the main identity guard

The [pre-commit hook](./pre-commit) refuses an ordinary commit on `main` when its
actual author or committer differs from the configured global Git name and email.
Repository-local and worktree-local identity overrides are refused, just as they are
by the lander. A hook that was already installed runs first, through the preserved
`pre-commit.local`. Install the hook with `just hooks-install`.

This hook cannot cover detached commits, commits on other branches, merges or fetched
history that skips this hook, direct ref updates, or commits created with Git's
`commit-tree` command or a private index without invoking hooks. This hook and the
[repository's identity gate](../tools/gate/checks/commit-identity.json) are repository policy. Tillr
stamps its own trunk landings but does not hold landings over anyone else's identity.
