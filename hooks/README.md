# This repository's git hooks

`just hooks-install` (`bin/hooks-install`) copies every executable in this directory into
the repository's **shared** hooks directory (`git rev-parse --git-common-dir`/hooks), so
one install covers the main checkout and every linked worktree — a lane's worktree pushes
through the same guard as `main`.

**It installs a copy, not a link, and it says what it found on every run.** A link into a
checkout follows whatever revision that checkout holds, and a link into a lane's worktree
dangles once the lane is removed; a copy is the revision git actually runs. So the two can
drift, and on 2026-10-23 they had: `.git/hooks/pre-push` was one lane's revision while the
landed `hooks/pre-push` was another, so the guard that ran on a push was not the guard in
the tree. The installer compares the installed copy with `hooks/<name>` every time it runs
and reports the comparison, one line per hook:

```console
$ just hooks-install
hooks-install: installed pre-push -> /workspace/.git/hooks/pre-push
$ just hooks-install
hooks-install: left alone pre-push (identical to /workspace/hooks/pre-push)
$ just hooks-install          # after hooks/pre-push changed
hooks-install: drifted pre-push (installed copy differed from /workspace/hooks/pre-push; replaced) -> /workspace/.git/hooks/pre-push
```

Every executable here carries a `tillr-hook: <name>` line, and that line is the
ownership test: a marked copy is ours to replace, and a copy without it belongs to
somebody else — it is neither overwritten nor discarded, but moved to `<name>.local`,
which this repository's hook of that name runs first, with the same arguments and stdin
git gave it. So a host's own gate (an attribution check, say) keeps running in
front of ours, and the installer reports `left alone <name> (no tillr-hook marker;
kept as <name>.local and chained)`. An existing `<name>.local` with different content is a
conflict: the installer refuses rather than clobber either hook. A hook here without its
marker is refused at install time, before anything is written.

**A replacing `core.hooksPath` makes the install invisible.** git reads `core.hooksPath`
*instead of* the repository's hooks directory, so a global setting means these copies are
written where git never looks and every hook here is off. The installer checks for it and
says so; the guard itself repeats the setting on every push it inspects, because a push is
where it matters.

## `pre-push` — the patch branch guard

A pack manifest may name a `patch_branch` locator instead of a `patches` series. When
that locator points back into this repository (`"../..?ref=patch/<name>"`), the branch
it names is what yolo clones and builds on another machine. A push that lands such a
manifest while origin lacks the branch at the pusher's OID would leave origin promising
a branch it cannot deliver.

**Every push is an opportunity to publish, and the hook takes it.** Before it looks at
what the push carries, it runs `bin/patch-branches-publish`, which pushes every patch
branch this repository requires: the ones a manifest in the tree names, and one per
series in [`bin/pi-forks.list`](../bin/pi-forks.list), because the migration gives each
pack a locator and a locator whose branch the remote does not hold is a migration that
cannot proceed. A branch is the delivery only once the remote has it, and a branch
created or moved with no manifest change is otherwise invisible: on 2026-10-24
`patch/pi-manage-todo-list` lived in one clone only, and the first thing to notice was a
launch on another machine. A push is the one moment credentials and the network are
certainly present. A branch only the remote has is not a failure — that is what the
manifest or the series promises — so the publish step says so and moves on; only a
branch neither side has refuses, naming the manifest or the series that requires it.

**A ref the push itself carries is not published by the hook.** git reads the remote's
OIDs before it runs `pre-push` and sends `<old-oid> <new-oid>` afterwards, so a ref the
hook moves in between is refused as a stale old-value claim — `cannot lock ref ... but
expected ...` — even though it ends up holding the commit the push delivered. That is
what a push naming two patch branches did on 2026-11-08: one of them was rejected and
arrived anyway. The hook passes every ref git handed it to the publish step as
`--skip-ref`, and its own branch push skips the same refs; the push is the writer for
the refs it names, and the branch lands with the manifest or not at all.

Then, for each commit a push adds — including a force-push whose old tip this clone
never fetched, where the commits are read over the remote-tracking refs — it:

1. reads every `yolo-packs/*/pack.json` that commit changes and every `patch_branch`
   locator in it that points into this repository;
2. resolves the locator's `?ref=` to the OID this repository holds — a clone that
   never fetched the branch is not a defect, since what the manifest promises is that
   the remote holds it;
3. asks the remote whether it has that branch at that OID;
4. if it does not, pushes the branch itself;
5. only if that push fails, refuses the manifest push and prints the exact command that
   fixes it.

A push that names no patch branch, or one the remote already has, is untouched. So is a
locator a historical commit named and a later commit in the same push retired: a fresh
remote — or a clone that never fetched, where the range is the whole history — made the
guard demand `carry/<name>` of every push after the 2026-10-23 rename, so the branch a
locator names must still be named by the ref being pushed.

## What the guard says about itself

Every push the guard inspects starts with one line naming the hook file git ran and
whether that file is this repository's own `hooks/pre-push`, then what the publish step
saw:

```console
pre-push: guard: /workspace/.git/hooks/pre-push -> /workspace/hooks/pre-push (this repository's own hook)
pre-push: patch branches: 8 required (1 named by a manifest, 7 declared by a series), 0 pushed to origin
```

The 2026-10-24 failure was not a wrong answer but a missing one: a push carrying a
manifest that named `patch/pi-manage-todo-list` reached origin with the guard never
invoked — the pusher's `gps` alias was `git push --no-verify` — and nothing on the push
said so. So the report is the evidence. **No report means the guard did not run**: a
`--no-verify`, an uninstalled hook, or a `core.hooksPath` that replaced the repository's
hooks directory. A report that names a hook path and says it is *not* this repository's
own means an older copy ran instead, and that is refused rather than allowed to run
stale checks — the remedy is `just hooks-install`, and the message names it.

With `core.hooksPath` set, the report adds the path git actually reads and the setting
that chose it, so a replacing global config is visible on the push rather than guessed
at afterwards:

```console
pre-push: hooks path: /home/user/.config/git/hooks/pre-push (core.hooksPath=/home/user/.config/git/hooks)
pre-push: guard: /home/user/.config/git/hooks/pre-push (this repository's own hook)
```

## Why the refusal is narrow

A pre-push hook cannot abort one ref of a multi-ref push, so the refusal stops the push
that carried the manifest — after the branch push above has already done its part. The
manifest and the locator are named in the message, and the remedy is the exact command,
not a description of one.

The internal branch push sets `PI_FORKS_PUSH_GUARD_ACTIVE`, so the hook does not
recurse into itself; a hook that receives that variable exits at once.
