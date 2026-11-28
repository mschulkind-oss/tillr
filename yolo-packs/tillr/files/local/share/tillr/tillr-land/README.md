# tillr-land — the only path the trunk moves through

> [!WARNING]
> **Known gaps.** Two things about `tillr` are not what a first reading assumes:
> 1. **The observatory front end is a separate build** — a `tillr-view` built by a plain
>    `go build` answers 404 for the UI. The front end under `tools/tillr-view/web` is a
>    Node build, embedded only under the `observatory` build tag (`just observatory` does
>    both) or served from a directory named with `--web-dist`.
> 2. **A `tillr` installed from the pack is not this working tree** — the pack under
>    `yolo-packs/tillr` builds from its own mirrored copy of the source, which
>    `just pack-sync` regenerates. To run an edit under `tools/tillr*`, build the working
>    tree with `just tillr-build`.

## What this module owns

`tillr-land` performs the landing transaction. It takes a `gate.verdict` that already binds
a head and a base, squashes the change onto the trunk, and moves the trunk ref. It has no
queue: there is no candidate to enqueue, list, show or cancel between the gate and the
trunk, and no state is carried across the landing except the `landings` row it writes and
the head it pins when nothing lands.

The design that splits this surface into `--help`, this README and the composition skill is
[`docs/reference/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-distribution.md).

## The red cap

There is none. A landing is never held because of how many landings, or how many minutes,
separate the trunk from `refs/tillr/green`:
[a red proof](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/gate.md#a-red-proof) is answered by a revert or a fixer
item instead. `Check` in [`redcap/cap.go`](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/redcap/cap.go) always answers no
hold, and the `red_cap` field of a landing refusal is never set. The package, the field and
the `red-cap` reason remain in the code and can be removed.

## State invariants

- **The trunk moves only through a landing, and only for a verdict that passed.** A landing
  with no verdict behind it is refused; the record is the authority for the decision, not
  the caller's word for it.
- **The owner's disposition is checked immediately before landing.** An item stored as
  `closed` or `resolved-elsewhere` is refused as `item-not-landable`, even if its gate
  already passed. The check is made in the short write that claims the landing (below),
  and the claim row it writes in `landing_claims` refuses `tillr-item close` and a move to
  `resolved-elsewhere` with `landing-in-progress` until the receipt write removes it.
  A close before the claim prevents the landing. A close during publication is refused,
  but can be retried after the receipt if the item is still open. Closing it then does
  not undo the landing. Standalone stores with no item table remain usable.
- **One ordinary trunk landing, one squash.** `Trunk.Squash` writes exactly one commit —
  `git merge-tree --write-tree` to compute the tree, `git commit-tree` to write the commit —
  with the repository's scope-checked identity and the candidate's message: a single-commit
  change keeps its message unchanged, while a multi-commit change uses the first commit's
  subject and lists the remaining commits' subjects in the body. The commit
  names no queue item: there is no `Tillr-Item:` trailer (ruling `D-RS3` in
  [queue persistence](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-queue-persistence.md#landing-receipts-without-commit-trailers)), and
  `Trunk.Move` fast-forwards the trunk onto it. The landed tree is the tree the verdict
  judged unless the trunk moved, and the row records both so a reader can tell.
  The drain routes gated remote-sync repairs around the ordinary squash. It
  records a completed linear replay with the same landing row and event:
  `trunk_after` identifies the gated repair, and `replay_tip` identifies the landing branch
  after replaying the remaining commits. The standalone landing command does not
  run this repair path.
- **No store write is open while git runs or a file is touched.** Under `land.lock`,
  ancestry, squash, clash checks and range preservation happen outside SQLite
  transactions. The publication is then three steps: a short write that rechecks
  disposition, an unrecorded receipt and the history row, and claims the landing in
  `landing_claims`; the trunk reread, the Move, the fork-cache refresh and the
  history-retention refs, with nothing held; and a short write that records the receipt and
  the history row and deletes the claim. A changed preparation input is an explicit error,
  not automatic replay. Every git command the lander starts passes the store's I/O guard
  (`store.CheckIO`, [`ioguard.go`](https://github.com/mschulkind-oss/tillr/blob/main/tillr/store/ioguard.go)), which refuses it when the
  calling goroutine holds an open store write, so a git command inside a write fails where it
  is written rather than stalling every other writer.
- **A kill between the steps is finished by the next attempt.** After the claim and before
  the Move, the marker names a commit the trunk does not carry: the same run's retry squashes
  again and its claim replaces the old one. After the Move and before the receipt, the trunk
  carries the marker's commit: the retry recovers it, moves nothing, records the landing once
  and deletes the claim in that receipt. A claim whose commit no trunk carries is cleared by
  the next landing of any run, because `land.lock` proves no landing is in flight; one whose
  commit is on the trunk stays, and keeps the item unclosable, until its run's retry records
  it. [`contention_test.go`](contention_test.go) kills a lander at both points.
- **A retry of a run does not squash or publish its change a second time.** Git's Move and
  the SQLite receipt are two writes, and a crash can keep the first and lose the second.
  Before Move, the lander points `refs/tillr/landed/<run_id>` at the squash commit. That ref
  is the lander's own marker: it is local, it is never pushed, and the commit itself names
  no item. A run id must be a valid ref name component, and one that is not is refused
  (exit 2) before any Git read. A retry of the same run is answered in one of three ways:
  - The store already holds a `landed` row for this item, run and head at the marker's
    commit. The retry answers that row with exit 0 and writes nothing: no row, no event, no
    ref.
  - No row names the marker's commit, the commit is in the trunk's history, and it is
    exactly this head's squash onto its own parent. The retry records that commit as the
    landing and does not move the trunk. Neither an unrelated commit with the same tree nor
    a different head for the same run passes that check.
  - Otherwise the retry squashes as a first landing would. A squash that would only
    reproduce the trunk's tree is refused as `already-on-trunk` and is never published as
    an empty commit.

  A `trunk_history` tip whose object Git no longer has is an external failure that names
  the commit, and nothing moves. A Git failure while reading that tip is reported as the
  Git failure, not as a lost commit. The cases are pinned in
  [`idempotent_test.go`](idempotent_test.go), and the writer-side recheck in
  [`contention_test.go`](contention_test.go). The [known cases](#known-cases-the-marker-does-not-settle)
  below are where this answer is not what an operator might expect.
- **The lease on the trunk excludes other landers, never the gate.** `land.lock` spans
  preparation and the whole publication, claim to receipt. The gate runs in another process before
  `pass` is called.
- **A non-landing pins its head.** A squash conflict or a refusal, `already-on-trunk` included, answers
  `refs/tillr/runs/<run_id>` at the head before the lock is released, so a head the gate saw
  cannot be garbage-collected before a conflict-fixer or a person reads it.
- **Authorship follows the trunk landing.** Tillr stamps the author and committer of its own
  trunk landings with the identity from Git config in a scope a lane cannot write, not the
  worker's identity. It enforces no identity policy on anyone else's commits. Repositories
  that want additional identity checks should implement them in their own gate.

## Fork delivery

A fork item delivers to `refs/heads/patch/<fork>`, not the repository's trunk. Its
passing verdict must bind the candidate's base and tree and carry a passing
`fork-build` check, which runs the pack's build command on that exact head. The
lander publishes the built commit without squashing. An ordinary delivery
fast-forwards and writes its landed marker before the move. The marker names that
same head, so sibling run boundaries recognize the delivery.

A non-fast-forward rewrite is allowed only from the staged delivery tip,
with the retained verdict, change record and fork registry for that run, in two
cases: a same-tree rewrite whose tree equals the delivered tip's tree and whose
history above the fork's upstream base is linear, or a release rebase whose head
is linear above the new release the fork's patch-branch target selects, not just
above an arbitrary tag. Under the landing lock the lander compares and
swaps the patch ref to the exact built head and, in the same Git transaction,
writes the landed marker and pins the old tip at
`refs/tillr/patch-history/<fork>/<old>`. A concurrent delivery or conflicting pin
prevents the transaction. The successful `land.landing` event names the delivery
ref and both tips; the pin alone is not proof of a successful landing.

The shared cache must be clean and on `main`; before a release rebase it must be
at the staged tip. A missing cache is prepared privately from the gated fork's
upstream metadata and published only when complete, at the current delivery tip.
The lander refreshes both cache `main` and its copy of the patch ref before the
receipt. A cache-local refresh ref records the source and target before the lander
detaches `HEAD`. On retry, only that clean, journaled checkout can reattach; an
unrelated detached checkout still refuses. Recovery finishes the cache refresh
without a second delivery, retaining a later delivered descendant when present.
A passing verdict without the build check is `verdict-mismatch`, not permission
to deliver. See
[fork_safety_test.go](fork_safety_test.go) and
[fork_rebase_test.go](fork_rebase_test.go).

## Known cases the marker does not settle

The landed marker (`refs/tillr/landed/<run_id>`, above) is evidence that this run published
a commit. It is not evidence that the trunk still carries the change, or that the run id
belongs to only one store. Two cases follow from that.

- **An owner reverts a landing the store never recorded.** The marker's commit is still in
  the trunk's history under the revert. A retry of that run records it as `landed`, although
  the trunk no longer carries the change. *Operator:* a revert means the item's change is not
  on the trunk. Re-open or re-dispatch the item under a new run, and correct the recorded
  `landed` row by hand if a reader needs it gone.
- **Two stores share one repository and reuse a run id.** Markers are per repository and
  receipts are per store. A second store that lands a candidate with the same content under
  the same run id finds the first store's marker. It records the first store's commit as its
  own landing. *Operator:* do not reuse run ids across stores over one repository. If it
  happened, correct the second store's row by hand.

The lander writes markers before publication and does not delete them itself. When the drain
absorbs a trunk rewrite, it moves live markers to their replacement commits without changing
historical landing receipts. The drain removes an ordinary marker in settlement only after
that run has a durable
`landed` row naming the marker's commit. A fork delivery marker stays until all pending
examinations and leased lanes have finished: a sibling's boundary comparison needs the
marker to recognize the patch branch's legitimate move. A landing event carries
`base_ref` to distinguish the delivery; a receipt without that field is treated
conservatively as a fork delivery when the published commit equals the candidate head.

The drain removes all of a finished item's run pins, including earlier attempts and
conflict repairs. Operator-owned held and recovery refs are never removed, even when
they name the same commit as a deleted pin. The
[drain's namespace lifetimes and sweep](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#ref-lifetimes-and-backlog-cleanup)
cover retryable attempts and backlog cleanup. Standalone landings leave their markers
until the drain or its sweep observes the receipt. An unrecorded landing keeps its
marker: deleting it by hand loses crash recovery, so a retry squashes again and is
refused as `already-on-trunk` when the trunk still carries the change.

## Standalone invocation

```sh
tillr-land pass --store .tillr/state.db --repo . --item item-042 --run run-1 --base <base-sha> --head <head-sha> --verdict gate.verdict.json
tillr-land one --store .tillr/state.db --repo . --branch feat/thing --trunk main --gate '<gate command>'
```

`pass` is the landing on its own; an external caller that already has a verdict composes it
without the loop. `one` gates a branch and then lands it, so a manual landing a person runs
by hand produces the same row as an unattended one.

## What it refuses

- **A trunk that no longer contains the loop's observed tip** — `pass --observed-trunk`
  checks ancestry under the landing lock and records `trunk-rewritten`. For a drain
  store it also reconciles the durable observation and lost tip, then revalidates that
  snapshot in the claim's write, so a stale request cannot bypass a newly
  recorded hold. A history row another observer rewrites between the claim and the receipt
  is that observer's and is left as it is. Successful
  landings advance the stored observation and its Git object-retention ref. This is not a
  comparison with the candidate's base: a reset to that base can still remove history
  the loop observed later. The drain retains the candidate and its verdict until history
  is restored; ordinary forward movement is reapplied without another gate. Manual
  callers that omit the observation do not claim to detect a loop's lost history.
- **A verdict that does not bind the head's tree and the base sha** — there is no judgment
  about this change to act on.
- **A head that does not squash onto the trunk** — the row records `squash-conflict` and the
  paths git reported; the head is pinned and the verdict stands.
- **A squash whose tree does not build** — when the squash differs from the tree the gate judged
  (the trunk moved), the lander extracts the squashed tree with `git archive` and runs `go build`
  and `go vet` (which compiles test packages) in every Go module the candidate's `base..head`
  touches, plus the sibling modules their `replace` lines name, before the trunk moves. Two changes
  that merge cleanly as text and do not compile together are recorded as `squash-conflict`, with
  one entry per failing module in place of paths, and returned to their author. The cost is
  bounded by the candidate's own modules, not by the landings since the gate ran; a landing whose
  squash is the gated tree is not checked, because the gate built that tree.
- **A predicted conflict between the owner's content and the landed result, or a main checkout
  in the middle of a merge, rebase, cherry-pick or revert** — see
  [the owner's working tree](#the-owners-working-tree). Uncommitted content that restores
  cleanly is not a refusal.
- **A repository that states no landing identity, or states one a lane can write** — the
  identity a commit carries is not a value a lane may choose.
- **A patch branch that is already at the head, or an unverified rewrite** — a
  non-fast-forward delivery requires the [verified release rebase](#fork-delivery)
  protocol.

## The owner's working tree

The main checkout is the owner's: an editor may be open on any file in it, and the landing
lock does not hold the editor. A landing behaves as `git pull --autostash` does, and never waits
for the owner. The rule, in the order the lander applies it:

1. **Paths the landing does not change are not touched.** No rewrite, no index change, no stash:
   the same bytes, inode and modification time, whether the file is modified, staged, untracked,
   ignored or merely open. The checkout changes only the paths the landing changed.
2. **Everything is prepared before the landing is claimed**, under the landing lock,
   after the verdict, the squash and the observed-trunk check, and none of it changes the working
   tree or the index: the prediction, the final disk content and index entry of each touched path,
   and the recovery copy of the owner's content on them. The recovery copy is a ref under
   `refs/tillr/owner-recovery/<run>` (never `refs/stash`, which is the owner's list). It copies;
   nothing leaves the disk. A landing that records no fault removes it.
3. **The publication, with no store write open, does the comparison and the final move only**: the checkout still
   has the trunk branch, the trunk is at the tip the prediction read, and every touched path (disk
   and index) still holds what the prediction read. Then it replaces each file once, writes the
   touched index entries, and moves the trunk ref with a guarded `update-ref` (carrying
   `-c tillr.move=final`, the marker the tests' Git wrappers stop on). `tillr item close` is
   refused by the landing's claim until the receipt.
4. **A path whose final content equals what is on disk is not rewritten.** A touched file is written
   to a temporary file beside it and renamed over the path: one replacement, a new inode. The
   intermediate states that exist are a temporary file and the half-installed set of paths, inside
   the lock and the transaction; the number of touched paths is the minimum.

**Staged and unstaged edits keep their split.** The final index entry is the three-way merge of the
base, the landed blob and the owner's staged blob; the final file is the same merge over the
owner's file. A staged edit and an unstaged edit on the same file stay staged and unstaged.

### The prediction cases

| Case | Outcome |
| :--- | :--- |
| owner's edit and the landed change on different hunks | lands; both are in the file (`git merge-file`) |
| both change the same hunk | **conflict**: nothing is written, no recovery ref, no markers |
| owner deleted or renamed away a path the landing modifies, or edited one the landing deletes | conflict; identical results (both deleted) land |
| mode (executable bit) changed on one side | merged; on both sides differently, conflict |
| binary (an attribute says `binary` or `-diff`, or Git finds a NUL in the first 8000 bytes) | any overlap is a conflict unless the bytes are identical |
| owner's untracked or ignored file on a path the landing adds | conflict unless identical, and an identical file is not rewritten |
| a file becomes a directory, or the reverse, and the owner has no content there | lands, including when the directory held tracked files in subdirectories the landing deletes |
| a file becomes a directory and the owner has content at the file path | conflict |
| a directory becomes a file | every file still under the directory after the landing's own deletions is the owner's content, whether modified, untracked, ignored or an editor side file: conflict naming the landed path and each file in the way. The lander never deletes a file it does not track |
| a path that attributes convert (line endings, a clean or smudge filter) | compared in Git's stored form, so a clean checked-out file is never a false conflict; written in its checked-out form |
| a touched path whose parent directory in the checkout is a symlink | conflict: the lander never writes through a symlink, and does not claim Git's own checkout handles it safely |
| submodule entries | conflict |
| a merge, rebase, cherry-pick or revert in progress in the checkout | pause, the reason names the state |

A predicted conflict is the only pause the owner's tree causes. The landing is refused as
`trunk-worktree-uncommitted` with the conflicting paths, the drain holds the gated candidate as
`trunk-path-dirty` and re-evaluates it as the owner's content or the trunk changes.

**Editor side files** (Vim `.<name>.swp`, `.swo`, `<name>~`, `4913`; Emacs `.#<name>`, `#<name>#`;
anything ignored) are never touched and never counted as the owner's content on a landed path.
The two exceptions are a path the landing adds itself, and a directory the landing replaces with a
file. A file the owner has open with nothing saved is simply updated on disk, as `git pull` does;
the lander logs nothing about editor side files, because a touched path is judged by its own
content and an informational line would be noise on every landing.

### What a save is, and the limit

The owner can save at any moment, so the prediction can go stale. A **save is the bytes readable
at a touched path**. The lander writes only through directory handles it opens one component at a
time without following a symlink, and confirms before and after each replacement that the handle
still is the directory the checkout path names; a parent the owner turns into a link or renames away
is never written through, and an undo goes back through the handle it wrote in, not through the path.
This needs Linux (`renameat2` with `RENAME_EXCHANGE`) on a filesystem that supports it; elsewhere
the landing fails with the system's error and changes nothing.

**The index lock.** From before the first replacement until the trunk ref has moved (and through
any rollback), the lander holds `<git dir>/index.lock`, the lock Git's own checkout, commit and add
take. A branch switch or an `add` cannot happen in the middle: it fails with Git's usual lock message
and can be run again a moment later, and a rollback never overwrites something the owner did. The
lander checks the branch after taking the lock, so a switch that won leaves the other branch's files
unwritten. It never waits inside the publication: a lock someone else holds is ordinary Git
work in progress, which is contention and not a fact about the owner's content. The attempt is
rolled back, nothing is held, refused or recorded, and the landing tries again after a short pause
(25 ms doubling to 1 s; the request's context ends it). The lock is created by linking a file that
already carries the lander's ownership record (kind, host, process id, token), so record and lock
appear together. A lock whose record names this host and a process that no longer exists is the
lander's own leftover and the next landing removes it; any other lock is never touched. A lander
killed during the final move leaves this lock, as a killed Git command leaves its own. Git then
refuses the owner's commands with its usual `index.lock: File exists` message. The file is JSON
whose `kind` is `tillr-land-index-lock` and whose `host` and `pid` name the lander, and removing it
is safe once that process is gone. The index is
replaced once, from a copy with only the touched entries changed, so untouched entries keep their
stat data. Because a file is replaced by an exchange with a temporary, the displaced inode exists
under a temporary name beside the path from the replacement until the trunk ref has moved; that is
the one intermediate on-disk state, inside the lock, and the names are removed right after.

**A directory that becomes a file is exchanged whole.** The lander does not empty the directory
file by file. One exchange puts the prepared file at the path and leaves the directory, with
everything in it, under a temporary name beside the path; the deletions the landing makes under it
appear only in the index. The lander reads the whole displaced directory after the exchange and
lists it again once every path is installed. A file the prediction did not read, or one whose
content differs, is the owner's: before the trunk moves the directory is exchanged back and the
landing predicts again, which is then a conflict. After the trunk has moved the lander unlinks the
files it read, by name, and the directories that are then empty, never a tree recursively. Anything
else found there stays, the directory keeps its temporary name (`.tillr-<random>.tmp` beside the
path), and the landing records a `land.fault` event naming it. If the owner saves a file at the
path while an install is being undone, that file stays at the path, the directory stays under its
temporary name, and the fault names both.

**A landing predicts again only when something was read differently.** When the comparison or the
install answers that the owner changed a path, the next prediction's readings of the touched paths,
their index entries and the branch are compared with the previous prediction's. If all are the
same, the owner changed nothing: the landing ends as a failed install does, with the owner's
content put back and a `land.fault` event, so a landing never repeats without a save in between.

- A change found **before the trunk moves** puts the owner's files back (symlinks included), saved
  bytes included, and predicts again under the same lock. The comparison, the branch check before
  every mutation and a parent that changed do the same.
- A change found **after the trunk moves** undoes nothing: the landed checkout and index stay. The
  last read happens after the receipt is durable, or after a receipt that failed (the published
  checkout is never undone, and the descriptors are read and closed on that path too). The bytes go to
  a ref under `refs/tillr/owner-recovery/saved/` and the landing records a `land.fault` event, in a
  transaction of its own, naming the path and the ref. A receipt that was rolled back leaves the
  ref and the event; the recovery that later records the landing reports the ref.
- A failed install puts the owner's content back, records a `land.fault` event naming the failure
  and the recovery copy, and fails the landing; no conflict markers are written. While undoing, the
  lander reads each file before it replaces it; bytes that are not what it installed are an owner's
  save, and every newer version found is kept (the newest at the path, the rest in the saved ref).
  An undo step that fails is reported with the path, the error and the ref holding the owner's
  original bytes; the landing never says "exactly" unless every restoration was confirmed, and a
  failed index restoration keeps the original entries of the touched paths in the saved ref.
- The last read and the fault event run with a context the request cannot cancel, and every answer
  that does not install (a stored receipt, a recovered landing) first reports and records what an
  earlier attempt of the run left: stale copies, saved refs, and a fault event that never reached
  the store.
- The recovery copy is `refs/tillr/owner-recovery/copy/<run>-<random>`, new for each preparation. A
  copy a crashed landing left stays until a later landing finds the disk matching it (and removes
  it) or reports its ref and the differing paths; it is never overwritten, not even by the same run.
  A preparation that installs nothing (a conflict, a closed item, a switched branch) removes its own
  copy.
- The checkout's rendering of each file uses the **landed** `.gitattributes`; the comparison with
  what is on disk uses the attributes the checkout has now.

**The limit.** No save is promised past the lander's last read of the displaced file. Bytes written
through a descriptor opened before the replacement, after that last read and before the lander
closes it, are outside the promise. `git pull` protects none of these either. The lander does not
wait for a writer and does not keep the replaced file after the landing, so a clean landing leaves
nothing behind. A save made through the path after the replacement goes into the new file and stays.

### The owner moving the trunk

The owner pulling, committing or rebasing forward on the landing branch while candidates are gated or in
flight needs no intervention: the squash is recomputed onto the tip read under the lock and the
verdict still binds the head's tree. A rewritten history is refused with `trunk-rewritten`.

## Test

`go test ./...` in this directory builds the binary and drives the faces against scratch
repositories. The land package's own suite pins the squash, the landing identity, the pin,
and the lock: a gate that probes the lock with a non-blocking `flock` finds it free, and a
second writer is refused while a landing holds it.
