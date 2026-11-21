#!/bin/sh
# The patch-branch pre-push guard (hooks/pre-push).
#
# A pack manifest may name a `patch_branch` locator that points back into this
# repository ("../..?ref=patch/<name>"). The branch it names is what yolo clones and
# builds on another machine, so a manifest push that reaches origin while origin lacks
# that branch leaves origin promising what it cannot deliver. The guard, per pushed
# commit:
#
#  1. reads the manifests that commit changes and the locators pointing here;
#  2. resolves each `?ref=` to this repository's OID;
#  3. asks origin whether it has that branch at that OID;
#  4. if not, PUSHES THE BRANCH ITSELF;
#  5. only if that push fails, refuses the manifest push and prints the exact remedy.
#
# It also publishes every patch branch a manifest in the tree names before any of that,
# and says on every push which hook file git ran and whether it is this repository's own.
#
# Cases:
#  1. a manifest push naming a branch origin lacks succeeds: the hook pushes the
#     branch first, and origin ends with the branch at the pusher's OID;
#  2. a manifest push whose branch ANOTHER clone moved is refused: origin's branch is not
#     fast-forwardable from here and it is not the value this clone published either, so
#     the publish step refuses the rewrite naming origin's commit, and the message names
#     the manifest, locator and OID without offering a force command;
#  2b. a verified rewrite with a matching published record succeeds and records the OID;
#     a direct refspec cannot substitute an unproved outgoing OID;
#  2c. a clone that has published nothing refuses its first rewrite even with the old flag;
#     a second required branch is still published in the same run;
#  3. a manifest push naming a branch this repository does not have is refused, with
#     the `git branch` remedy;
#  4. a push that changes no manifest is untouched;
#  5. a manifest push naming a branch origin already has at the right OID is untouched
#     (no second push, no output);
#  6. a hook already installed as `pre-push.local` runs first, with the remote name and
#     URL git passed, and its refusal stops the push before the guard's own work;
#  7. the internal branch push does not recurse: a run with PI_FORKS_PUSH_GUARD_ACTIVE
#     set exits at once;
#  8. a force-push whose old tip this clone never fetched is still guarded: the
#     remote's advertised OID is not an object here, and the commits the push adds are
#     still read;
#  9. every push the guard inspects says which hook file git ran and that it is this
#     repository's own hook, and the publish step says how many patch branches it saw;
# 10. a hook file that differs from the one the repository ships is refused: the copy
#     that ran is named, so is the shipped one, and so is the `just hooks-install`
#     remedy -- a stale copy running stale checks is not allowed to pass silently;
# 11. a `core.hooksPath` that replaces the repository's hooks directory is named, and a
#     copy there that is identical to the shipped hook is allowed;
# 12. a linked install reports both the link git ran and the file it points at, since
#     the resolved target alone hides where git looked;
# 13. a locator a historical commit named and a later commit retired does not refuse the
#     push: a fresh remote makes the guard read the whole history, and a renamed branch
#     is not a promise the pushed tip makes.
# 14. a series in bin/pi-forks.list is required too, not only the branches a manifest
#     names: its branch is published on the next push, a series whose branch exists
#     neither here nor on the remote refuses the push, naming the series that needs it,
#     and a verified transition publishes the rewrite without a manifest naming it.
# 15. a patch branch this clone is BEHIND on is not refused and no rewrite is
#     requested: the local tip is a strict ancestor of origin's, so there is nothing to
#     publish. Refusing it and naming `--force-rewritten` (the 2026-10-30 failure) would
#     have rewound origin over commits already published;
# 16. a patch branch equal on both sides is a no-op: nothing rewritten, nothing refused;
# 17. the same behind branch with `refs/published/<branch>` naming origin's tip is still
#     a no-op -- the record never licenses force-pushing an ancestor over origin;
# 18. a GENUINELY divergent branch (neither tip descends from the other) is still
#     refused without proof, and origin's branch is left untouched.
# 19. a push that NAMES the patch branch it changes is left to git: the ref is not
#     published by the hook on the way, so the push's own update is not refused as a
#     stale old-value claim (the 2026-11-08 `cannot lock ref ... but expected ...`), the
#     branch arrives at the tip the push delivered, and the publish step says it left the
#     ref to the push instead of pushing it.
#
# Every case runs with `bin/patch-branches-publish` installed beside the hook, as the
# repository has it: the guard publishes first on every push, and a branch only the
# remote holds is reported rather than treated as a failure.
#
# Run: sh tests/pre-push-guard.test.sh

set -eu

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
hook="$repo_root/hooks/pre-push"
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

origin="$tmp/origin.git"
work="$tmp/work"
git init -q --bare "$origin"
git init -q "$work"
git -C "$work" remote add origin "$origin"
mkdir -p "$work/yolo-packs/p"
git -C "$work" add -A 2>/dev/null || true

manifest() {   # $1: the locator the contribution declares
  cat > "$work/yolo-packs/p/pack.json" <<JSON
{"name": "p", "contributes": [
  {"kind": "files", "into": ".pi/agent/yolo-patched/ext",
   "source": "git+https://github.com/owner/ext?ref=main", "patch_branch": "$1"}
]}
JSON
}

install_hook() {
  mkdir -p "$work/.git/hooks"
  cp "$hook" "$work/.git/hooks/pre-push"
  chmod +x "$work/.git/hooks/pre-push"
  # The hook the repository ships, which the installed copy is compared with. The real
  # repository has both: `hooks/pre-push` in the tree and the installed copy git runs.
  mkdir -p "$work/hooks"
  cp "$hook" "$work/hooks/pre-push"
  # The publish step the guard runs first on every push, as the real repository has it.
  mkdir -p "$work/bin"
  cp "$repo_root/bin/patch-branches-publish" "$work/bin/patch-branches-publish"
  chmod +x "$work/bin/patch-branches-publish"
}
install_hook

push() { if out="$(git -C "$work" push origin "$@" 2>&1)"; then rc=0; else rc=$?; fi; }
origin_ref() { git -C "$origin" rev-parse --verify --quiet "refs/heads/$1" || true; }
says() { printf '%s' "$out" | grep -q -- "$1"; }
landing() {
  mkdir -p "$work/.tillr"
  python3 - "$work/.tillr/state.db" "$@" <<'PYTHON'
import json, sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    db.execute('CREATE TABLE IF NOT EXISTS events (seq INTEGER PRIMARY KEY, kind TEXT, payload TEXT)')
    db.execute('INSERT INTO events(kind,payload) VALUES (?,?)', ('land.landing', json.dumps(dict(
        result='landed', base_ref=sys.argv[2], trunk_before=sys.argv[3], trunk_after=sys.argv[4]))))
PYTHON
}

# ── 1. The hook pushes the branch itself, then lets the manifest through ────────
printf 'upstream\n' > "$work/base.txt"
git -C "$work" add base.txt
git -C "$work" commit -qm "base"
git -C "$work" branch patch/ext
patch_oid="$(git -C "$work" rev-parse patch/ext)"
manifest '../..?ref=patch/ext'
git -C "$work" add yolo-packs/p/pack.json
git -C "$work" commit -qm "declare the patch branch"
push main
check "a manifest push whose patch branch origin lacks succeeds" "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "the hook pushed the branch itself, at the pusher's OID" \
  "$([ "$(origin_ref patch/ext)" = "$patch_oid" ] && echo 1 || echo 0)"
check "the manifest landed" \
  "$([ "$(git -C "$origin" show main:yolo-packs/p/pack.json 2>/dev/null | grep -c 'patch/ext')" = "1" ] && echo 1 || echo 0)"
check "and the publish step recorded the value this clone published" \
  "$([ "$(git -C "$work" rev-parse --verify --quiet refs/published/patch/ext)" = "$patch_oid" ] && echo 1 || echo 0)"

# ── 2b. A rewrite needs a receipt for the actual outgoing OID ──────────────────
# The publish step recorded patch_oid in case 1, and origin still holds it.
git -C "$work" checkout -q patch/ext
printf 're-exported\n' > "$work/re-exported.txt"
git -C "$work" add re-exported.txt
git -C "$work" commit -q --amend -m "base, re-exported"
rewritten_oid="$(git -C "$work" rev-parse patch/ext)"
git -C "$work" checkout -q main
[ "$rewritten_oid" != "$patch_oid" ] || { echo "fixture: the amend did not rewrite the branch" >&2; exit 1; }
printf 'after\n' > "$work/after.txt"
git -C "$work" add after.txt
git -C "$work" commit -qm "a push after the re-export"
push "+$rewritten_oid:refs/heads/patch/ext"
check "an explicit refspec cannot push an unproved rewrite" \
  "$([ "$rc" != 0 ] && says 'no verified landing chain' && [ "$(origin_ref patch/ext)" = "$patch_oid" ] && echo 1 || echo 0)"
landing refs/heads/patch/ext "$patch_oid" "$rewritten_oid"
unproved_oid="$(printf 'unlanded\n' | git -C "$work" commit-tree "$rewritten_oid^{tree}" -p "$rewritten_oid")"
PI_FORKS_PUSH_GUARD_ACTIVE=1 push "+$unproved_oid:refs/heads/patch/ext"
check "even with recursion disabled the outgoing OID must match the proof" \
  "$([ "$rc" != 0 ] && says 'no verified landing chain' && [ "$(origin_ref patch/ext)" = "$patch_oid" ] && echo 1 || echo 0)"
PI_FORKS_PUSH_GUARD_ACTIVE=1 push "--force-with-lease=refs/heads/patch/ext:$patch_oid" "$rewritten_oid:refs/heads/patch/ext"
check "the recursion flag cannot turn a direct rewrite into recorded publication" \
  "$([ "$rc" != 0 ] && says 'publish the verified rewrite with' && [ "$(origin_ref patch/ext)" = "$patch_oid" ] && [ "$(git -C "$work" rev-parse refs/published/patch/ext)" = "$patch_oid" ] && echo 1 || echo 0)"
push "--force-with-lease=refs/heads/patch/ext:$patch_oid" "$rewritten_oid:refs/heads/patch/ext"
check "a proved rewrite must use the publisher so it can record the result" \
  "$([ "$rc" != 0 ] && says 'publish the verified rewrite with' && [ "$(origin_ref patch/ext)" = "$patch_oid" ] && [ "$(git -C "$work" rev-parse refs/published/patch/ext)" = "$patch_oid" ] && echo 1 || echo 0)"
cat > "$work/.git/hooks/pre-push.local" <<'SH'
#!/bin/sh
if grep -q ' refs/heads/patch/ext '; then
    echo 'chained hook refused publication' >&2
    exit 1
fi
SH
chmod +x "$work/.git/hooks/pre-push.local"
push main
check "a chained hook can reject the publisher's rewrite, not just the outer push" \
  "$([ "$rc" != 0 ] && says 'chained hook refused publication' && [ "$(origin_ref patch/ext)" = "$patch_oid" ] && [ "$(git -C "$work" rev-parse refs/published/patch/ext)" = "$patch_oid" ] && echo 1 || echo 0)"
rm "$work/.git/hooks/pre-push.local"
push main
check "the publisher delivers the verified tip" \
  "$([ "$rc" = 0 ] && [ "$(origin_ref patch/ext)" = "$rewritten_oid" ] && echo 1 || echo 0)"
check "and publication itself records the exact OID" \
  "$([ "$(git -C "$work" rev-parse --verify --quiet refs/published/patch/ext)" = "$rewritten_oid" ] && echo 1 || echo 0)"

# ── 2c. A clone that has published nothing refuses its first rewrite ───────────
# The old force flag cannot bypass the check.
fresh_clone="$tmp/fresh-clone"
git clone -q "$origin" "$fresh_clone"
mkdir -p "$fresh_clone/bin" "$fresh_clone/hooks"
cp "$repo_root/bin/patch-branches-publish" "$fresh_clone/bin/patch-branches-publish"
chmod +x "$fresh_clone/bin/patch-branches-publish"
# The publisher reuses the hook's `pack.json` comment stripper, so the hook ships with it.
cp "$hook" "$fresh_clone/hooks/pre-push"
# The publish step reads the manifests from the working tree, so it runs from the clone's
# main checkout, as it does in the repository.
publish_in() { if out="$(cd "$fresh_clone" && python3 bin/patch-branches-publish --remote origin "$@" 2>&1)"; then rc=0; else rc=$?; fi; }
# A clone gets no local branch for a remote one until it checks it out, and the publish
# step publishes local branches, so this is the clone's copy of the branch.
git -C "$fresh_clone" branch patch/ext origin/patch/ext
# First run, while this clone agrees with origin: that is how a branch published by an
# older version of this step -- one with no record -- gets one.
publish_in
check "a branch already in sync records what this clone has published" \
  "$([ "$rc" = "0" ] && [ "$(git -C "$fresh_clone" rev-parse --verify --quiet refs/published/patch/ext)" = "$rewritten_oid" ] && echo 1 || echo 0)"
# Fixture plumbing: forget the record, so the next case is a clone that has published
# nothing for the branch -- the state every clone is in before its first publish.
git -C "$fresh_clone" update-ref -d refs/published/patch/ext
git -C "$fresh_clone" checkout -q patch/ext
printf 'fresh\n' > "$fresh_clone/fresh.txt"
git -C "$fresh_clone" add fresh.txt
git -C "$fresh_clone" commit -q --amend -m "base, rewritten in a clone that never published"
fresh_oid="$(git -C "$fresh_clone" rev-parse patch/ext)"
git -C "$fresh_clone" checkout -q main
# A second required branch, new here: a refusal is about the branch it names, so this one
# still has to be published in the same run. `bin/pi-forks.list` is read from the working
# tree, so it need not be committed in this fixture.
printf 'owner/other  yolo-packs/p/patches/other\n' > "$fresh_clone/bin/pi-forks.list"
git -C "$fresh_clone" branch patch/other main
other_oid="$(git -C "$fresh_clone" rev-parse patch/other)"
publish_in
check "a clone with no published value refuses its first rewrite" "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "and a refused rewrite does not stop the branch beside it publishing" \
  "$([ "$(git -C "$origin" rev-parse --verify --quiet refs/heads/patch/other)" = "$other_oid" ] && says '1 pushed to origin' && echo 1 || echo 0)"
check "and the refusal names the remote commit it would overwrite" \
  "$(says "REFUSED $(printf '%s' "$rewritten_oid" | cut -c1-12) -> $(printf '%s' "$fresh_oid" | cut -c1-12)" && echo 1 || echo 0)"
check "and says the clone has published nothing for it" \
  "$(says 'this clone has published nothing for it' && echo 1 || echo 0)"
check "and origin's branch is untouched" "$([ "$(origin_ref patch/ext)" = "$rewritten_oid" ] && echo 1 || echo 0)"
publish_in --force-rewritten
check "--force-rewritten still refuses and leaves the remote and record unchanged" \
  "$([ "$rc" != 0 ] && [ "$(origin_ref patch/ext)" = "$rewritten_oid" ] &&
     ! git -C "$fresh_clone" show-ref --verify --quiet refs/published/patch/ext && echo 1 || echo 0)"

# ── 5. A branch origin already has at the right OID is untouched ────────────────
printf 'another\n' > "$work/another.txt"
git -C "$work" add another.txt
git -C "$work" commit -qm "unrelated change"
push main
check "a push that names a branch origin already has is untouched" "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and it refuses nothing" "$(says 'pre-push: refusing' && echo 0 || echo 1)"

# ── 4. A push that changes no manifest is untouched ────────────────────────────
printf 'third\n' > "$work/third.txt"
git -C "$work" add third.txt
git -C "$work" commit -qm "no manifest here"
push main
check "a push that changes no manifest is untouched" "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and it refuses nothing either" "$(says 'pre-push: refusing' && echo 0 || echo 1)"

# ── 2. Origin's branch moved: the branch push fails, the manifest is refused ────
# Another machine advances the patch branch. This repository's different child is not a
# fast-forward, and origin's tip is not the value this clone published. The publisher
# refuses the rewrite and the hook does not try a fallback push.
other="$tmp/other"
git clone -q "$origin" "$other"
git -C "$other" checkout -q patch/ext
printf 'rewritten elsewhere\n' > "$other/elsewhere.txt"
git -C "$other" add elsewhere.txt
git -C "$other" commit -qm "elsewhere: rewrite the branch"
git -C "$other" push -q origin patch/ext
origin_oid="$(git -C "$origin" rev-parse patch/ext)"
[ "$origin_oid" != "$patch_oid" ] || { echo "fixture: the branches did not diverge" >&2; exit 1; }
printf 'local\n' > "$work/local.txt"
git -C "$work" add local.txt
git -C "$work" commit -qm "a local commit on the patch branch"
git -C "$work" checkout -q patch/ext
printf 'ours\n' > "$work/ours.txt"
git -C "$work" add ours.txt
git -C "$work" commit -qm "ours: a different tip"
git -C "$work" checkout -q main
new_oid="$(git -C "$work" rev-parse patch/ext)"
cat > "$work/yolo-packs/p/pack.json" <<JSON
{"name": "p", "contributes": [
  {"kind": "files", "into": ".pi/agent/yolo-patched/ext",
   "source": "git+https://github.com/owner/ext?ref=main", "follow": "head",
   "patch_branch": "../..?ref=patch/ext"}
]}
JSON
printf 'more\n' > "$work/more.txt"
git -C "$work" add more.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "name the rewritten patch branch"
before_manifest="$(git -C "$origin" show main:yolo-packs/p/pack.json 2>/dev/null || true)"
push main
check "a manifest push whose branch origin has at another OID is refused" "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "the refusal names the manifest" "$(says 'yolo-packs/p/pack.json' && echo 1 || echo 0)"
check "the refusal names the locator and the OID origin lacks" \
  "$(says '../..?ref=patch/ext' && says "$(printf '%s' "$new_oid" | cut -c1-12)" && echo 1 || echo 0)"
check "the publish step refuses the rewrite, naming origin's commit and the value it published" \
  "$(says "REFUSED $(printf '%s' "$origin_oid" | cut -c1-12) -> $(printf '%s' "$new_oid" | cut -c1-12)" && says "origin moved to $(printf '%s' "$origin_oid" | cut -c1-12) since this clone published" && echo 1 || echo 0)"
check "the refusal does not suggest forcing a rewrite or retrying the push" \
  "$(if says 'force-with-lease' || says 'force-rewritten' || says "git push origin $new_oid:refs/heads/patch/ext"; then echo 0; else echo 1; fi)"
check "the refused manifest did not land" \
  "$([ "$(git -C "$origin" show main:yolo-packs/p/pack.json 2>/dev/null || true)" = "$before_manifest" ] && echo 1 || echo 0)"
check "and origin's branch was not rewritten" "$([ "$(origin_ref patch/ext)" = "$origin_oid" ] && echo 1 || echo 0)"

# ── 3. A branch this repository does not have is refused with its remedy ───────
manifest '../..?ref=patch/absent'
printf 'absent\n' > "$work/absent.txt"
git -C "$work" add absent.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "name a branch that does not exist here"
push main
check "a manifest naming a branch this repository lacks is refused" "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "the refusal names the missing branch and both sides that lack it" \
  "$(says 'neither this repository nor origin has branch patch/absent' && echo 1 || echo 0)"
check "the refusal prints the exact push remedy" \
  "$(says "git push origin <commit>:refs/heads/patch/absent" && echo 1 || echo 0)"
check "the missing-branch manifest did not land" \
  "$([ "$(git -C "$origin" show main:yolo-packs/p/pack.json 2>/dev/null | grep -c 'patch/absent')" = "0" ] && echo 1 || echo 0)"

# ── 3b. A branch origin already has is enough, even when this clone lacks it ─────
# What the manifest promises is that origin holds the branch; a pusher's clone that
# never fetched it is not a defect. Give origin both branches the earlier manifests
# name, then push a manifest naming one this clone does not have.
# Reconcile the branch case 2 left diverged, so this case's push is about the
# origin-only branch alone. This is fixture plumbing, not a guarded push: the tree still
# names the branch case 3 proved is missing, and the publish step refuses any push while
# a manifest in the tree names a branch this clone cannot supply.
git -C "$work" push -q --no-verify --force origin patch/ext
git -C "$origin" update-ref refs/heads/patch/absent "$(git -C "$origin" rev-parse main)"
git -C "$origin" update-ref refs/heads/patch/remoteonly "$(git -C "$origin" rev-parse main)"
manifest '../..?ref=patch/remoteonly'
printf 'remote-only\n' > "$work/remote-only.txt"
git -C "$work" add remote-only.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "name a branch only origin holds"
push main
check "a branch only origin holds is enough" "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and the publish step says it is on origin only, not missing" \
  "$(says 'on origin only, nothing to push here' && echo 1 || echo 0)"
check "and that manifest lands" \
  "$([ "$(git -C "$origin" show main:yolo-packs/p/pack.json 2>/dev/null | grep -c 'patch/remoteonly')" = "1" ] && echo 1 || echo 0)"

# ── 6. A hook already installed as pre-push.local runs first ────────────────────
# Restore a manifest that resolves, so this case is about the chained hook alone.
manifest '../..?ref=patch/ext'
git -C "$work" add yolo-packs/p/pack.json
git -C "$work" commit -qm "restore the good manifest"
git -C "$work" push -q origin main
chained_args="$tmp/chained-args"
cat > "$work/.git/hooks/pre-push.local" <<'SH'
#!/bin/sh
printf '%s %s\n' "$1" "$2" > "$CHAINED_ARGS"
echo "the host gate refused" >&2
exit 1
SH
chmod +x "$work/.git/hooks/pre-push.local"
printf 'local-gate\n' > "$work/local-gate.txt"
git -C "$work" add local-gate.txt
git -C "$work" commit -qm "a change with no manifest"
export CHAINED_ARGS="$chained_args"
push main
unset CHAINED_ARGS
check "a pre-push.local that refuses stops the push" "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "and its own message is what the pusher sees" "$(says 'the host gate refused' && echo 1 || echo 0)"
check "and it receives the remote name and URL git passed" \
  "$([ "$(cat "$chained_args" 2>/dev/null)" = "origin $origin" ] && echo 1 || echo 0)"
rm "$work/.git/hooks/pre-push.local"
push main
check "with the local hook gone the same push succeeds" "$([ "$rc" = "0" ] && echo 1 || echo 0)"

# ── 7. The internal push does not recurse ──────────────────────────────────────
manifest '../..?ref=patch/newbranch'
git -C "$work" branch patch/newbranch
git -C "$work" add yolo-packs/p/pack.json
git -C "$work" commit -qm "name a fresh branch"
printf 'refs/heads/main %s refs/heads/main %s\n' \
  "$(git -C "$work" rev-parse main)" "$(git -C "$origin" rev-parse main)" > "$tmp/stdin"
if PI_FORKS_PUSH_GUARD_ACTIVE=1 "$work/.git/hooks/pre-push" origin "$origin" < "$tmp/stdin" > "$tmp/active.out" 2>&1; then rc=0; else rc=$?; fi
check "an active guard exits at once (no recursion)" "$([ "$rc" = "0" ] && [ ! -s "$tmp/active.out" ] && echo 1 || echo 0)"

# ── 8. A force-push whose old tip was never fetched is still guarded ──────────
# Another machine pushes a commit to main, and this clone never fetches it, so the
# remote OID git reports is not an object here. A force-push must still be read: the
# commit it carries names a branch neither side has, and must be refused.
other2="$tmp/other2"
git clone -q "$origin" "$other2"
git -C "$other2" checkout -q main
printf 'their commit\n' > "$other2/theirs.txt"
git -C "$other2" add theirs.txt
git -C "$other2" commit -qm "their commit on main"
git -C "$other2" push -q origin main
[ "$(git -C "$work" rev-parse main)" != "$(git -C "$origin" rev-parse main)" ] || {
  echo "fixture: main did not move on origin" >&2; exit 1; }
manifest '../..?ref=patch/nobodyhas'
printf 'force\n' > "$work/force.txt"
git -C "$work" add force.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "name a branch nobody has"
push --force main
check "a force-push whose old tip is unfetched is still guarded" \
  "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "and its refusal names the branch neither side has" \
  "$(says 'neither this repository nor origin has branch patch/nobodyhas' && echo 1 || echo 0)"

# ── 9. The guard says which hook ran, and that it is this repository's own ────
# Reconcile what cases 7 and 8 left behind -- origin holds every branch those
# manifests named -- and push a manifest that resolves. The first push is fixture
# plumbing, for the reason given in case 3b.
git -C "$work" push -q --no-verify --force origin patch/ext
git -C "$origin" update-ref refs/heads/patch/nobodyhas "$(git -C "$origin" rev-parse main)"
manifest '../..?ref=patch/ext'
printf 'identity\n' > "$work/identity.txt"
git -C "$work" add identity.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "a push the guard inspects"
push --force main
check "a healthy push succeeds" "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "the guard names the hook file git ran" \
  "$(says "guard: $work/.git/hooks/pre-push" && echo 1 || echo 0)"
check "and says it is this repository's own hook" \
  "$(says "(this repository's own hook)" && echo 1 || echo 0)"
check "and the publish step says what it saw, and where each requirement came from" \
  "$(says 'patch branches: 1 required (1 named by a manifest, 0 declared by a series)' && echo 1 || echo 0)"

# ── 10. A hook that differs from the one the repository ships is refused ───────
# The installed copy is a snapshot, not a link, so an edit to the repository's hook can
# leave stale logic running the pushes. That is refused rather than reported quietly:
# the checks that ran would not be the ones in the tree.
printf '# stale: an older copy of this guard\n' >> "$work/.git/hooks/pre-push"
printf 'stale\n' > "$work/stale.txt"
git -C "$work" add stale.txt
git -C "$work" commit -qm "a push through a stale copy"
push main
check "a hook that differs from the shipped one is refused" "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "the refusal says the running hook is not this repository's own" \
  "$(says "guard: $work/.git/hooks/pre-push (NOT this repository's own hook)" && echo 1 || echo 0)"
check "and names the hook the repository ships" \
  "$(says "is not $work/hooks/pre-push" && echo 1 || echo 0)"
check "and prints the hooks-install remedy" \
  "$(says 'just hooks-install' && echo 1 || echo 0)"
cp "$hook" "$work/.git/hooks/pre-push"
chmod +x "$work/.git/hooks/pre-push"
push main
check "reinstalling the shipped hook lets the same push through" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"

# ── 11. A replacing core.hooksPath is named, not guessed at ────────────────────
# The failure this makes visible: `core.hooksPath` replaces the repository's hooks
# directory wholesale, so `just hooks-install` writes a hook git never reads. Git still
# runs this guard here because the replacing directory holds the same hook; the report
# names that path, so the setting is on the push instead of in someone's guess.
global_hooks="$tmp/global-hooks"
mkdir -p "$global_hooks"
cp "$hook" "$global_hooks/pre-push"
chmod +x "$global_hooks/pre-push"
printf 'hooked\n' > "$work/hooked.txt"
git -C "$work" add hooked.txt
git -C "$work" commit -qm "a push through core.hooksPath"
if out="$(GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_1=core.hooksPath \
    GIT_CONFIG_VALUE_1="$global_hooks" git -C "$work" push origin main 2>&1)"; then rc=0; else rc=$?; fi
check "a push through a replacing core.hooksPath succeeds when the hook is identical" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "the report names the hooks path git actually read" \
  "$(says "hooks path: $global_hooks/pre-push" && echo 1 || echo 0)"
check "and names core.hooksPath as the reason" \
  "$(says "(core.hooksPath=$global_hooks)" && echo 1 || echo 0)"
check "and says the hook that ran is this repository's own" \
  "$(says "guard: $global_hooks/pre-push (this repository's own hook)" && echo 1 || echo 0)"

# ── 12. A linked install names the link and the file it points at ───────────────
# `just hooks-install` installs a copy, but an older run left a link (and a replacing
# `core.hooksPath` can hold one), so the resolved target alone would hide where git looked.
rm "$work/.git/hooks/pre-push"
ln -s "$work/hooks/pre-push" "$work/.git/hooks/pre-push"
printf 'linked\n' > "$work/linked.txt"
git -C "$work" add linked.txt
git -C "$work" commit -qm "a push through a linked hook"
push main
check "a linked install still runs the repository's hook" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and the report names the link and the file it points at" \
  "$(says "guard: $work/.git/hooks/pre-push -> $work/hooks/pre-push (this repository's own hook)" && echo 1 || echo 0)"

# ── 13. A retired locator in history does not refuse a push ─────────────────────
# The 2026-10-23 rename left commits whose manifest named `carry/<name>`. A fresh remote,
# or a clone that never fetched, makes the guard read the whole history, so a branch a
# LATER commit renamed must not be demanded of the pushed tip.
fresh="$tmp/fresh.git"
git init -q --bare "$fresh"
manifest '../..?ref=patch/retired'
printf 'retired\n' > "$work/retired.txt"
git -C "$work" add retired.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "a historical manifest naming a branch that was later renamed"
manifest '../..?ref=patch/ext'
printf 'renamed\n' > "$work/renamed.txt"
git -C "$work" add renamed.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "rename the locator"
git -C "$work" remote add fresh "$fresh"
if out="$(git -C "$work" push fresh main 2>&1)"; then rc=0; else rc=$?; fi
check "a retired locator in history does not refuse a push to a fresh remote" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and the retired branch is not demanded" "$(says 'patch/retired' && echo 0 || echo 1)"
check "and the branch the tip names was published there" \
  "$([ "$(git -C "$fresh" rev-parse --verify --quiet refs/heads/patch/ext)" = "$(git -C "$work" rev-parse patch/ext)" ] && echo 1 || echo 0)"

# ── 14. A series is required too, not only the branches a manifest names ────────
# The migration gives each pack a `patch_branch` locator, and a locator whose branch the
# remote does not hold is a migration that cannot proceed, so every series in
# bin/pi-forks.list needs its branch published whether or not a manifest names it yet.
mkdir -p "$work/yolo-packs/p/patches/ext" "$work/yolo-packs/p/patches/gone"
cat > "$work/bin/pi-forks.list" <<'LIST'
owner/ext  yolo-packs/p/patches/ext
owner/gone  yolo-packs/p/patches/gone
LIST
printf 'series\n' > "$work/series.txt"
git -C "$work" add series.txt bin/pi-forks.list
git -C "$work" commit -qm "declare two series"
push main
check "a series whose branch is nowhere refuses the push" "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "and names the series that requires the branch" \
  "$(says 'MISSING locally and on origin (the series yolo-packs/p/patches/gone)' && echo 1 || echo 0)"
check "and that push lands nothing" \
  "$([ -z "$(git -C "$origin" rev-parse --verify --quiet refs/heads/main:bin/pi-forks.list || true)" ] && echo 1 || echo 0)"
git -C "$work" branch patch/gone main
gone_oid="$(git -C "$work" rev-parse patch/gone)"
push main
check "with the series' branch created, the push publishes it" \
  "$([ "$rc" = "0" ] && [ "$(origin_ref patch/gone)" = "$gone_oid" ] && echo 1 || echo 0)"
check "and the publish summary counts both kinds of requirement" \
  "$(says 'patch branches: 2 required (1 named by a manifest, 1 declared by a series)' && echo 1 || echo 0)"
# The rewrite rule covers a series-only branch too: the push above recorded patch/gone.
# A successful landing receipt lets the publisher move it without a manifest naming it.
git -C "$work" checkout -q patch/gone
printf 'series re-exported\n' > "$work/series-reexported.txt"
git -C "$work" add series-reexported.txt
git -C "$work" commit -q --amend -m "declare two series, re-exported"
reexported_oid="$(git -C "$work" rev-parse patch/gone)"
git -C "$work" checkout -q main
# A push with nothing to send runs no hook at all (`Everything up-to-date`), so this push
# carries a commit of its own.
printf 'after the series re-export\n' > "$work/after-series.txt"
git -C "$work" add after-series.txt
git -C "$work" commit -qm "a push after the series re-export"
landing refs/heads/patch/gone "$gone_oid" "$reexported_oid"
push main
check "a verified series-only rewrite publishes without the flag" \
  "$([ "$rc" = "0" ] && [ "$(origin_ref patch/gone)" = "$reexported_oid" ] && echo 1 || echo 0)"
check "and the publish step reports the series' rewrite" \
  "$(says "patch/gone  rewritten $(printf '%s' "$gone_oid" | cut -c1-12) -> $(printf '%s' "$reexported_oid" | cut -c1-12)" && echo 1 || echo 0)"

# ── 15. A clone that is BEHIND origin publishes nothing, and is not refused ─────
# The 2026-10-30 failure. The publish step asked one question of the two tips -- is the
# remote tip an ancestor of the local one -- and never the reverse. When the local ref was
# a strict ANCESTOR of origin's (this clone was simply behind), the answer fell through to
# "rewrite", so the step refused the whole push and named `--force-rewritten` as the
# remedy; taking that remedy would have rewound origin over commits already published.
# Behind means there is nothing for this clone to publish, and it must never read as a
# rewrite. The pushed commit changes the manifest, so the guard's own per-commit branch
# push runs over the same branch as well as the publish step.
peer="$tmp/peer"
git clone -q "$origin" "$peer"
git -C "$peer" checkout -q patch/ext
printf 'peer\n' > "$peer/peer.txt"
git -C "$peer" add peer.txt
git -C "$peer" commit -qm "a commit another clone published"
git -C "$peer" push -q origin patch/ext
# Fetch the objects without moving the local branch: the relationship between the two
# tips is what this case is about, and both OIDs must be objects here for it to be read.
git -C "$work" fetch -q origin
peer_oid="$(origin_ref patch/ext)"
[ "$peer_oid" != "$(git -C "$work" rev-parse patch/ext)" ] || {
  echo "fixture: origin's patch/ext did not move" >&2; exit 1; }
git -C "$work" merge-base --is-ancestor patch/ext "$peer_oid" || {
  echo "fixture: this clone's patch/ext is not an ancestor of origin's" >&2; exit 1; }
manifest '../..?ref=patch/ext'
printf '// a push while origin is ahead on a patch branch\n' >> "$work/yolo-packs/p/pack.json"
printf 'behind\n' > "$work/behind.txt"
git -C "$work" add behind.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "a push while origin is ahead on a patch branch"
push main
check "a patch branch this clone is behind on does not refuse the push" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and the publish step says the branch is behind, with nothing to publish" \
  "$(says 'behind origin at' && \
     says 'which is an ancestor, so there is nothing to publish here' && echo 1 || echo 0)"
check "and it does not suggest --force-rewritten" "$(says 'force-rewritten' && echo 0 || echo 1)"
check "and it does not suggest --force-with-lease" "$(says 'force-with-lease' && echo 0 || echo 1)"
check "and origin's branch is not rewound to this clone's older tip" \
  "$([ "$(origin_ref patch/ext)" = "$peer_oid" ] && echo 1 || echo 0)"

# ── 16. A branch equal on both sides is a no-op ────────────────────────────────
# Nothing to publish, nothing to rewrite, and no refusal -- the third possible
# relationship between the two tips, asserted on its own rather than incidentally.
git -C "$work" fetch -q origin patch/ext:patch/ext
fresh_oid="$(git -C "$work" rev-parse patch/ext)"
printf 'equal\n' > "$work/equal.txt"
git -C "$work" add equal.txt
git -C "$work" commit -qm "a push while the patch branch is in sync"
push main
check "a patch branch equal on both sides does not refuse the push" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and it is neither reported as rewritten nor refused" \
  "$(says 'rewritten' && echo 0 || echo 1)"
check "and origin's branch is unchanged" "$([ "$(origin_ref patch/ext)" = "$fresh_oid" ] && echo 1 || echo 0)"

# ── 17. A record naming origin's tip does not license rewinding origin ─────────
# The same behind case with the record at its most dangerous: this clone's last published
# value IS origin's current tip, and its local ref was then moved BACK to an ancestor (a
# reset, a stale export). A rule that consults the record before it asks which tip descends
# from which force-pushes that ancestor over origin and deletes the commits in between.
old_oid="$(git -C "$work" rev-parse patch/ext~1)"
git -C "$work" update-ref refs/heads/patch/ext "$old_oid"
git -C "$work" update-ref refs/published/patch/ext "$fresh_oid"
printf 'record\n' > "$work/record.txt"
git -C "$work" add record.txt
git -C "$work" commit -qm "a push while the record names origin's tip and the ref is behind"
push main
check "a record naming origin's tip does not turn a behind branch into a rewrite" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and origin keeps the commits this clone does not have" \
  "$([ "$(origin_ref patch/ext)" = "$fresh_oid" ] && echo 1 || echo 0)"

# ── 18. A genuinely divergent branch still needs proof ────────────────────────
git -C "$work" branch patch/div main
manifest '../..?ref=patch/div'
git -C "$work" add yolo-packs/p/pack.json
git -C "$work" commit -qm "name a branch to diverge"
push main
check "the branch a divergence case starts from publishes" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
git -C "$peer" fetch -q origin patch/div:patch/div
git -C "$peer" checkout -q patch/div
printf 'their divergence\n' > "$peer/div.txt"
git -C "$peer" add div.txt
git -C "$peer" commit -qm "another clone's commit on the branch"
git -C "$peer" push -q origin patch/div
git -C "$peer" checkout -q main
git -C "$work" fetch -q origin
git -C "$work" checkout -q patch/div
printf 'our divergence\n' > "$work/div.txt"
git -C "$work" add div.txt
git -C "$work" commit -q --amend -m "this clone's rewrite of the branch"
div_local="$(git -C "$work" rev-parse patch/div)"
git -C "$work" checkout -q main
printf '// divergent\n' >> "$work/yolo-packs/p/pack.json"
printf 'divergent\n' > "$work/divergent.txt"
git -C "$work" add divergent.txt yolo-packs/p/pack.json
git -C "$work" commit -qm "a push while the patch branch diverged"
push main
check "a genuinely divergent branch is refused without proof" \
  "$([ "$rc" != "0" ] && echo 1 || echo 0)"
check "and the refusal does not offer --force-rewritten" \
  "$(says 'force-rewritten' && echo 0 || echo 1)"
check "and it says neither tip descends from the other" \
  "$(says 'neither tip descends from the other' && echo 1 || echo 0)"
check "and origin's branch is untouched by the refusal" \
  "$([ "$(origin_ref patch/div)" != "$div_local" ] && [ -n "$(origin_ref patch/div)" ] && echo 1 || echo 0)"

# ── 19. A ref this push is delivering is left to git ───────────────────────────
# git reads origin's OIDs BEFORE it runs the hook, and the update it then sends carries
# those OIDs. Publishing a ref that is itself in the push moves it in between, so the
# remote refuses that push's own update as a stale old-value claim -- `cannot lock ref
# 'refs/heads/patch/ext': is at X but expected Y` -- even though the branch ends up
# holding the pushed commit. A patch branch must therefore not be published by the hook
# on the same push that carries it: the push is the writer for the refs it names.
# Clear case 18's divergent branch first: its branch is local-only now that origin holds
# it, and the guard pushes a branch a HISTORICAL commit names even when the pushed tip no
# longer does, so leaving it here would refuse this push for case 18's reason.
git -C "$work" branch -q -D patch/div
manifest '../..?ref=patch/ext'
printf '// a push that names the patch branch\n' >> "$work/yolo-packs/p/pack.json"
git -C "$work" add yolo-packs/p/pack.json
git -C "$work" commit -qm "a push that names the patch branch it changes"
push main
[ "$rc" = "0" ] || { echo "fixture: the manifest push before case 19 failed:" >&2; printf '%s\n' "$out" >&2; exit 1; }
git -C "$work" fetch -q origin patch/ext:patch/ext
git -C "$work" checkout -q patch/ext
printf 'in flight\n' > "$work/inflight.txt"
git -C "$work" add inflight.txt
git -C "$work" commit -qm "the tip this push delivers"
inflight_oid="$(git -C "$work" rev-parse patch/ext)"
git -C "$work" checkout -q main
push main patch/ext
check "a push that names the patch branch it changes succeeds" \
  "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "and origin holds the tip the push delivered" \
  "$([ "$(origin_ref patch/ext)" = "$inflight_oid" ] && echo 1 || echo 0)"
check "and the push was not refused as a stale old-value claim" \
  "$(says 'cannot lock ref' && echo 0 || echo 1)"
check "and the publish step left the ref to the push" \
  "$(says 'in this push, left to git' && echo 1 || echo 0)"
check "and it reports nothing it pushed for that ref" \
  "$(says '0 pushed to origin' && echo 1 || echo 0)"

# Exercise all three entry points before and after acknowledging the moves.
mkdir -p "$work/.tillr"
git -C "$work" branch patch/unrequired main
printf '// held publication
' >> "$work/yolo-packs/p/pack.json"
git -C "$work" commit -qam "manifest names the held patch branch"
git -C "$work" checkout -q patch/ext
git -C "$work" commit --allow-empty -qm "advance held patch branch"
git -C "$work" checkout -q main
python3 - "$work/.tillr/state.db" <<'PYTHON'
import json, sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    db.execute('INSERT INTO events VALUES (100, ?, ?)', ('boundary.check', json.dumps({
        'patch_moves': [{'ref': 'refs/heads/patch/' + name} for name in ('ext', 'unrequired')],
    })))
PYTHON
if out="$(python3 "$work/bin/patch-branches-publish" --force-rewritten 2>&1)"; then rc=0; else rc=$?; fi
check "publisher signals held publication with status 3 even when forced" \
  "$([ "$rc" = 3 ] && says acknowledge-patch-move && echo 1 || echo 0)"
for dest in main patch/ext patch/unrequired; do
  push "$dest"
  check "hold refuses $dest without publishing either branch" \
    "$([ "$rc" != 0 ] && says acknowledge-patch-move &&
        [ "$(origin_ref patch/ext)" = "$inflight_oid" ] &&
        [ -z "$(origin_ref patch/unrequired)" ] && echo 1 || echo 0)"
done
PI_FORKS_PUSH_GUARD_ACTIVE=1 push patch/unrequired
check "recursion flag does not bypass a direct patch hold" \
  "$([ "$rc" != 0 ] && says acknowledge-patch-move &&
      [ -z "$(origin_ref patch/unrequired)" ] && echo 1 || echo 0)"
mv "$work/.tillr/state.db" "$work/.tillr/saved.db"
mkdir "$work/.tillr/state.db"
push patch/unrequired
check "hook refuses unreadable store" \
  "$([ "$rc" != 0 ] && says 'cannot read patch move acknowledgments at' &&
      [ -z "$(origin_ref patch/unrequired)" ] && echo 1 || echo 0)"
rmdir "$work/.tillr/state.db"
mv "$work/.tillr/saved.db" "$work/.tillr/state.db"
python3 - "$work/.tillr/state.db" <<'PYTHON'
import json, sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    for name in ('ext', 'unrequired'):
        db.execute('INSERT INTO events(kind,payload) VALUES (?,?)', ('drain.patch-move-acknowledged', json.dumps({
            'observation': 100, 'ref': 'refs/heads/patch/' + name,
            'by': 'owner', 'acknowledged_at': '2026-11-18T00:00:00Z',
        })))
PYTHON
for dest in patch/ext patch/unrequired main; do
  push "$dest"
  check "acknowledgment allows $dest" \
    "$([ "$rc" = 0 ] && [ "$(origin_ref "$dest")" = "$(git -C "$work" rev-parse "$dest")" ] && echo 1 || echo 0)"
done

# Publication holds use SQLite observations and owner acknowledgments. Keep their
# isolated Python cases in this permanent suite, which the landing gate already runs.
if python3 "$repo_root/tests/patch-move-publish.test.py"; then
  check "observed patch moves hold publication until acknowledged" 1
else
  check "observed patch moves hold publication until acknowledged" 0
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures pre-push guard case(s) failed" >&2
  exit 1
fi
echo "all pre-push guard cases pass"
