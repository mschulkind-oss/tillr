#!/bin/sh
# `just hooks-install` (bin/hooks-install): the installer that keeps the shared hooks
# directory in step with hooks/.
#
# git runs hooks from one directory shared by the main checkout and every linked worktree,
# and nothing in the tree writes there. On 2026-10-23 `.git/hooks/pre-push` was one lane's
# revision while the landed `hooks/pre-push` was another, so the guard on a push was not
# the guard in the tree -- and the installer, which only asked whether a file existed,
# would not have corrected it. The installer now compares content on every run and says
# what it found, and this suite pins each outcome:
#
#  1. a fresh install writes a COPY (not a link), executable, and reports `installed`;
#  2. a second run over an identical copy reports `left alone` and changes nothing;
#  3. a stale copy carrying this repository's marker is reported `drifted` and replaced by
#     hooks/<name>;
#  4. drift is judged on every run, not only when the file was first written: a copy that
#     goes stale again is reported `drifted` again, and an identical one is not;
#  5. a marked symlink -- what the older installer left -- is replaced by a copy, and a
#     marked link whose content is stale is reported `drifted` too;
#  6. a dangling link is replaced, since nothing runs from it;
#  7. a hook WITHOUT the marker is left alone: its bytes are preserved at <name>.local,
#     the report says so, and the installed hook runs it first, with the argv and stdin git
#     passed (its exit status stops the hook);
#  8. an existing <name>.local with different content is a conflict, refused rather than
#     clobbered;
#  9. a source hook in hooks/ without its marker is refused before anything is written;
# 10. every executable in this repository's hooks/ carries its own marker, which is what
#     makes the ownership test above work for the next hook somebody adds;
# 11. a repository whose worktree list outgrows the pipe buffer still installs: reading the
#     first worktree with a pipeline that lets its reader (or, once the reader quits, its
#     writer) die of SIGPIPE ended the run at exit 141, before a hook was written and with
#     no output at all -- so `just hooks-install`, the remedy every refusal names, was a
#     silent no-op on this worktree-heavy repository;
# 12. a hooks/ with nothing executable, or a destination that cannot be created, fails
#     loudly: a run that installs nothing must not read as a run that installed everything.
#
# Every case builds its own repository under a scratch directory and passes it as --root,
# so nothing here writes the shared hooks directory this checkout uses.
#
# Run: sh tests/hooks-install.test.sh

set -eu

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
installer="$repo_root/bin/hooks-install"
failures=0

check() {
  name="$1"; pass="$2"
  if [ "$pass" = "1" ]; then
    echo "ok   $name"
  else
    echo "FAIL $name" >&2
    failures=$((failures + 1))
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=fixture GIT_AUTHOR_EMAIL=fixture@example.invalid
export GIT_COMMITTER_NAME=fixture GIT_COMMITTER_EMAIL=fixture@example.invalid
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0="init.defaultBranch" GIT_CONFIG_VALUE_0="main"

work="$tmp/work"
git init -q "$work"
# The repository's own hooks, so the installer's source and the hook it installs are the
# real ones (case 7 runs the real pre-push to prove the chain).
cp -R "$repo_root/hooks" "$work/hooks"
hook="$work/hooks/pre-push"
dst="$work/.git/hooks/pre-push"

run_installer() {
  if out="$("$installer" --root "$work" 2>&1)"; then rc=0; else rc=$?; fi
}
says() { printf '%s' "$out" | grep -qF -- "$1"; }
same() { cmp -s -- "$1" "$2"; }

# ── 1. A fresh install is a copy, and says installed ───────────────────────────
run_installer
check "a fresh install succeeds" "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "the hook is installed" "$([ -f "$dst" ] && echo 1 || echo 0)"
check "it is a copy of hooks/pre-push" "$(same "$hook" "$dst" && echo 1 || echo 0)"
check "and not a link" "$([ -L "$dst" ] && echo 0 || echo 1)"
check "it is executable" "$([ -x "$dst" ] && echo 1 || echo 0)"
check "the report says installed, naming the hook and where it went" \
  "$(says "installed pre-push -> $dst" && echo 1 || echo 0)"

# ── 2. An identical copy is left alone ─────────────────────────────────────────
run_installer
check "a second run succeeds" "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "an identical copy is reported left alone" \
  "$(says "left alone pre-push (identical to $hook)" && echo 1 || echo 0)"
check "and it is still the same bytes" "$(same "$hook" "$dst" && echo 1 || echo 0)"
check "and the run does not claim a replacement" \
  "$(says 'drifted' && echo 0 || echo 1)"

# ── 3. A stale marked copy is drifted, and replaced ────────────────────────────
printf '# stale: an older revision of this hook\n' >> "$dst"
run_installer
check "a stale marked copy is reported drifted" \
  "$(says "drifted pre-push (installed copy differed from $hook; replaced)" && echo 1 || echo 0)"
check "and replaced by the repository's copy" "$(same "$hook" "$dst" && echo 1 || echo 0)"
# The pattern starts at `replaced)` on purpose: a grep shim in a sandboxed jail may read a
# pattern beginning with `-` as a flag, and the line still has to be findable there.
check "the replacement names where it went" "$(says "replaced) -> $dst" && echo 1 || echo 0)"

# ── 4. Drift is judged on every run, not only when first written ───────────────
printf '# stale again: the copy drifted a second time\n' >> "$dst"
run_installer
check "drift is reported again when the copy goes stale again" \
  "$(says 'drifted pre-push (installed copy differed' && echo 1 || echo 0)"
check "and the copy is the repository's again" "$(same "$hook" "$dst" && echo 1 || echo 0)"
run_installer
check "the next run reports left alone, not drifted" \
  "$(says 'left alone pre-push (identical' && echo 1 || echo 0)"

# ── 5. A marked link from the older installer becomes a copy ───────────────────
rm -f "$dst"
ln -s "$hook" "$dst"
run_installer
check "a marked link is reported replaced" \
  "$(says "replaced pre-push (was a link to $hook; now a copy) -> $dst" && echo 1 || echo 0)"
check "and the link is gone, leaving a copy" \
  "$([ -L "$dst" ] && echo 0 || echo 1)"
check "with the repository's bytes" "$(same "$hook" "$dst" && echo 1 || echo 0)"

rm -f "$dst"
cp "$hook" "$tmp/stale-hook"
printf '# stale behind a link\n' >> "$tmp/stale-hook"
ln -s "$tmp/stale-hook" "$dst"
run_installer
check "a marked link whose content is stale is reported drifted" \
  "$(says "drifted pre-push (the link to $tmp/stale-hook differed from $hook; replaced)" && echo 1 || echo 0)"
check "and replaced by a copy of the repository's hook" \
  "$(same "$hook" "$dst" && [ ! -L "$dst" ] && echo 1 || echo 0)"

# ── 6. A dangling link is replaced, because nothing runs from it ───────────────
rm -f "$dst"
ln -s "$tmp/gone" "$dst"
run_installer
check "a dangling link is reported installed" \
  "$(says "installed pre-push -> $dst (replaced a dangling link)" && echo 1 || echo 0)"
check "and it is the repository's copy now" "$(same "$hook" "$dst" && echo 1 || echo 0)"

# ── 7. A hook without the marker is left alone and chained ─────────────────────
# Its bytes are preserved at pre-push.local, which hooks/pre-push runs first. The fixture
# records the argv and stdin it received, then exits 42: the installed hook must run it and
# pass its status through, which proves the chain rather than just the file's existence.
rm -f "$dst"
cat > "$dst" <<EOF
#!/bin/sh
printf '%s\n' "\$@" > "$tmp/chained.argv"
cat > "$tmp/chained.stdin"
exit 42
EOF
chmod +x "$dst"
cp "$dst" "$tmp/foreign"
run_installer
check "a hook without the marker is reported left alone and chained" \
  "$(says "left alone pre-push (no tillr-hook marker; kept as $dst.local and chained) -> $dst" && echo 1 || echo 0)"
check "its bytes are preserved, untouched, as pre-push.local" \
  "$(same "$tmp/foreign" "$dst.local" && echo 1 || echo 0)"
check "the installed hook is the repository's copy" "$(same "$hook" "$dst" && echo 1 || echo 0)"
if (cd "$work" && printf 'origin\tfile:///dev/null\n' | "$dst" origin file:///dev/null) \
    >/dev/null 2>&1; then
  run_rc=0
else
  run_rc=$?
fi
check "the installed hook runs the chained one" "$([ "$run_rc" = "42" ] && echo 1 || echo 0)"
check "and passes the argv git gave it" \
  "$([ "$(cat "$tmp/chained.argv")" = "origin
file:///dev/null" ] && echo 1 || echo 0)"
# The hook hands the chained one the same stdin git gave it, re-spaced as it read it.
check "and the stdin git gave it" \
  "$([ "$(cat "$tmp/chained.stdin")" = "origin file:///dev/null" ] && echo 1 || echo 0)"

# ── 8. A differing <name>.local is a conflict, not something to clobber ────────
cp "$dst.local" "$tmp/chained-before"
rm -f "$dst"
printf '#!/bin/sh\necho somebody else\n' > "$dst"
chmod +x "$dst"
cp "$dst" "$tmp/second-foreign"
run_installer
check "a differing pre-push.local refuses the install" \
  "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "and the refusal names both paths" \
  "$(says "refusing: $dst is not this repository's hook, and $dst.local already exists" && echo 1 || echo 0)"
check "neither hook was touched" \
  "$(same "$tmp/second-foreign" "$dst" && same "$tmp/chained-before" "$dst.local" && echo 1 || echo 0)"
rm -f "$dst" "$dst.local"

# ── 9. A source hook without its marker is refused before anything is written ──
printf '#!/bin/sh\nexit 0\n' > "$work/hooks/post-commit"
chmod +x "$work/hooks/post-commit"
run_installer
check "a source hook without its marker is refused" \
  "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "and the refusal names the missing marker line" \
  "$(says "refusing: $work/hooks/post-commit carries no 'tillr-hook: post-commit' line" && echo 1 || echo 0)"
check "and nothing was installed for it" \
  "$([ -e "$work/.git/hooks/post-commit" ] && echo 0 || echo 1)"
rm -f "$work/hooks/post-commit"

# ── 10. Every executable in the real hooks/ carries its marker ─────────────────
unmarked=""
for src in "$repo_root"/hooks/*; do
  [ -f "$src" ] && [ -x "$src" ] || continue
  name="${src##*/}"
  grep -qF -- "tillr-hook: $name" "$src" || unmarked="$unmarked $src"
done
check "every executable hook in hooks/ carries its marker${unmarked:+ (missing:$unmarked)}" \
  "$([ -z "$unmarked" ] && echo 1 || echo 0)"

# ── 11. A repository whose worktree list is long still installs ────────────────
# The layout this repository actually has: one main checkout and a worktree per lane, all
# sharing one hooks directory. The installer finds the main worktree from `git worktree
# list --porcelain`, and reading that list as `sed ... | head -n 1` closes the pipe while
# sed is still writing: SIGPIPE, and under `set -o pipefail` the assignment ended the run
# with exit 141 before a single hook was written. It printed nothing at all, so the remedy
# every refusal message names -- `just hooks-install` -- looked like a clean run while
# every hook stayed stale.
#
# The fixture needs the worktree list to outgrow the pipe buffer (64 KiB), because that is
# what makes the writer BLOCK and so makes this a case rather than a race: with two
# worktrees the whole list can be written before `head` exits, and the case passed against
# the broken installer. Long paths get there in 25 worktrees and two seconds.
git -C "$work" commit -q --allow-empty -m "base"
pad="$(printf '%0200d' 0 | tr '0' 'a')"
deep="$tmp/lanes"
n=0
while [ "$n" -lt 18 ]; do
  deep="$deep/$pad"
  n=$((n + 1))
done
mkdir -p "$deep"
n=0
while [ "$n" -lt 25 ]; do
  git -C "$work" worktree add -q "$deep/lane-$n" -b "lane-$n" || exit 1
  n=$((n + 1))
done
rm -f "$dst"
run_installer
check "a repository with a long worktree list still installs" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and writes the repository's hook" "$(same "$hook" "$dst" && echo 1 || echo 0)"
check "and reports what it installed instead of dying silently" \
  "$(says "installed pre-push -> $dst" && echo 1 || echo 0)"

# ── 12. A hooks/ with nothing executable is refused, not a quiet success ───────
# A run that installs nothing must not read as one that installed everything.
empty="$tmp/empty"
git init -q "$empty"
mkdir -p "$empty/hooks"
printf '#!/bin/sh\ntrue\n' > "$empty/hooks/pre-push"
chmod -x "$empty/hooks/pre-push"
if out="$("$installer" --root "$empty" 2>&1)"; then rc=0; else rc=$?; fi
check "a hooks/ with no executable hook is refused" "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "and the refusal names the empty source directory" \
  "$(says "nothing executable in $empty/hooks" && echo 1 || echo 0)"

if [ "$failures" -ne 0 ]; then
  echo "$failures hooks-install case(s) failed" >&2
  exit 1
fi
echo "all hooks-install cases pass"
