# tillr-item — the board's items and the ledgers that hang off them

> [!WARNING]
> **Known Gaps (verified 2026-11-14 at `04957a8a83`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **The AutoQA handoff skill is not shipped** — `yolo-packs/tillr/skills/` holds
>    `tillr-operating` only, so the design's `tillr-autoqa` handoff has no skill to dispatch.
> 2. **The observatory front end is not built** — `tillr view serve` answers 404 for the UI
>    until the front end under `tools/tillr-view/web` is built and embedded, which is a
>    separate Node step and not part of a Go build.
> 3. **The `tillr` on `PATH` is the pack's rendered tree, not this working tree** — an edit
>    under `tools/tillr*` reaches the launcher only after the pack is mirrored and rendered,
>    so a session that must run its own change builds the working tree first.
>
> **Do not wire `tillr` into another repository's gate while these stand.** The toolset
> ships early so it can be reviewed, not so it can be depended on.

## What this module owns

`tillr-item` writes and reads work items, and the four ledgers that attach to them: the
decisions a board records, the mail deliveries a lane is sent, the sign-off grants a
delegation runs under, and the verdicts a judge records. `sync` materializes the item
blocks of the repository's tracked documents — the only intake from documents.
The retired `seed` command refuses with a pointer to `tillr item sync`. A fence entry
under `.forks/lanes/<lane>/<fork>/` names a checkout staging materializes for the
assigned lane, so `sync` does not warn that it is missing even on a fresh repository.
Other absent paths still receive the ordinary `path-missing` warning.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## Reporting documents that owe the queue work

`reconcile` reads Vantage's planning index and reports every indexed document in
one of six cases: unstaged, terminal, built, accepted with item blocks, accepted
without item blocks, or held for a ruling or design work. The ordered rules and
path-derived item ids are defined in the
[queue-persistence reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-queue-persistence.md#document-reconciliation-tillr-item-reconcile).

```sh
tillr-item reconcile --root /path/to/repo
tillr-item reconcile --root /path/to/repo --store /path/to/state.db
```

This is a read-only report, not intake. It prints all six counts, each document's
case, and tracked Markdown paths under `docs/` absent from the index. Built
findings distinguish a saved `stage: BUILT` claim from completion inferred from
landed items. Missing graduation or decomposition work comes with a fenced
Markdown item block to paste into a document; `sync` imports that authored block.
Documents with unanswered questions list their ids, including blocked questions.

Without a store, only authored blocks count as carrying work. With an explicit
store, both its document references and authored blocks count; a missing store
refuses and is never created. An unreadable, empty, refused, or incomplete index,
invalid item block, or path-derived id collision refuses before any report is
printed. Run `tillr-item reconcile --help` for the optional index-command override.

## Reading an order record

`tillr-item order --store /path/to/state.db` prints a plain text
[order record](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-actions.md#the-order-record):
`# last_hand_move_event_seq: N`, then one item id per line in place order.
The sequence is the last hand placement event (including a saved idea), or zero
when none exists. Sync placements do not advance it. Parked and finished items
are excluded. The command is read-only and does not write a file.

A repository opts in through its settings:

```toml
[order]
record = "docs/order.txt"
settle_minutes = 10
```

`record` is a clean repository-relative file path and is unset by default;
`settle_minutes` is a nonnegative integer, default 10, reserved for the loop's
record landing step. Sync reads no record without the opt-in. A missing file
means the first record has not landed; other read errors, malformed item ids,
and duplicate ids refuse sync. Blank lines and comments starting with `#` are ignored.

Sync places only successful new imports using the record. It places named items
first, in record order: after the nearest item above already in the order, or
before the nearest below when none is above, or last when neither exists.
Remaining imports use their `from` mark or append in roadmap order. Unknown record
ids never create items, and editing the record never moves items the store holds.

## Moving an item in the order

`place` moves one item first, last, before another item, or after another item.
The [observatory actions reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-actions.md#queue-order-and-places)
defines the order: terminal and parked items are excluded. The command emits an
`item.place` record with the item's position counted from 1, the count of items
in the order, and the event sequence. A normal move updates only that item's
order key and update receipt; an exhausted gap rewrites keys in the same
transaction without changing the other items' relative order. Self-placement
and a move to the position already occupied append no event.

```sh
tillr-item place --store /path/to/state.db --item item-example --to first
tillr-item place --store /path/to/state.db --item item-example --to last
tillr-item place --store /path/to/state.db --item item-example --before item-other
tillr-item place --store /path/to/state.db --item item-example --after item-other
```

Unknown items or neighbors are refused as `unknown-item`; terminal or parked
ones as `not-in-order`. Update events record `placed_by` and `placed_via`,
defaulting to `owner` and `cli`. Run `tillr-item place --help` for all flags.

## Adding and removing dependencies

`dep` adds or removes `blocked_by` edges on an existing item without rewriting
the rest of its record and without changing its place in the queue.

```sh
tillr-item dep --store /path/to/state.db --item item-example --add item-other
tillr-item dep --store /path/to/state.db --item item-example --remove item-other
```

Unchanged dependencies append no event. It refuses an unknown item
(`not-found`), a blank or unknown dependency target (`edge-target-missing`),
a self-dependency (`edge-self`), and any addition that would form a cycle
(`edge-cycle`), and prints the resulting `item.record` envelope. Run
`tillr-item dep --help` for all flags.

## Saving an idea

`idea` reads the owner's words from standard input and accepts the same destinations
as `place`. The [idea intake reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-actions.md#idea-intake-and-the-design-turn)
defines the item: design seat, first-line title, three fixed criteria, and the tracked
words path plus the configured paths as its fence.

```sh
printf 'Make the queue easier to read\n' | tillr-item idea --store .tillr/state.db --root . --to first
```

Configure the repository's `.tillr.toml` before saving:

```toml
[intake]
dir = "docs/ideas"
paths = ["docs/design/", "docs/tillr/roadmap.md", "docs/pi/roadmap.md", "docs/roadmap.md"]
```

The command preserves valid UTF-8 words of 1 byte through 256 KiB, not only
whitespace. It flushes `intake/<item-id>.txt` beside the store before creating
both the item and `idea_intake` row in one transaction. The row records SHA-256,
byte count, and the tracked words path. A failed store write leaves the file and
names it in the refusal; missing settings refuse `intake-not-configured` with
only the words file saved. Opening a missing, incompatible, or busy store does
not write an idea.

The id is `item-idea-` plus ten hash digits, lengthened on a collision. Identical
words answer the first item without changing its place or appending an event.
An `item.idea` result carries `item_id`, `place`, `count`, `event_seq`, and
`event_head_seq`; a retry of an item no longer in the order has place 0.
`words-empty`, `words-not-text`, and `words-too-large` name input refusals.
No tracked file is written by saving; the words reach the tree through the
idea's own landing. There is no reserved priority prefix.

## Filing a document triage

`doc-triage` files one item that checks a set of documents: is each still relevant,
and is it still accurate. The item runs the
[`doc-triage@v1`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/doc-triage@v1.json) workflow, described
in the [drain README](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#document-triage), and its only
deliverable is one report. Name the documents as arguments after the flags, or name a
roadmap with `--roadmap`, or both:

```sh
tillr-item doc-triage --store .tillr/state.db --title "Two designs" docs/design/a.md docs/design/b.md
tillr-item doc-triage --store .tillr/state.db --title "Tillr roadmap designs" --roadmap docs/tillr/roadmap.md
```

A roadmap expands to every Markdown document under `docs/design/` that it links, in
first-link order and each once. Anchors, links outside `docs/design/` and links to
missing files are skipped. A named document must exist, or the face refuses. Named
documents come first, then the roadmap's. Run `tillr-item doc-triage --help` for the flags.

The item is written straight to the store. No document carries a block or a marker for
it, and nothing tracked is written when it is filed. Its fields:

- id `item-doc-triage-<date>-<slug>`, where the slug comes from the title. A second
  triage with the same id is refused as `item-exists`.
- the design seat and the `doc-triage@v1` workflow.
- `workflow_args` holding `{"docs": [...]}`, the document list.
- a fence of exactly one path, the report. It defaults to
  `docs/triage/<date>-<slug>.md`, and `--report` names another Markdown path under `docs/`.
- three fixed criteria: the report has one row per named document; every verdict cites
  its evidence; and no other file is changed.

It is placed last unless `--to`, `--before` or `--after` says otherwise. Every refusal
comes before the first write: `no-documents`, `too-many-documents` (above the `most`
the workflow file declares for `docs`), `title-required`, `report-path` (outside
`docs/`, or one of the triaged documents), `document-missing`, `document-not-markdown`
and `item-exists`. The answer is an `item.doc-triage` record carrying `item_id`,
`place`, `count`, `report` and `documents`.

The report recommends and edits nothing else. Acting on a recommendation is separate
work that the owner approves.

### Workflow arguments and store-declared items

An item record may carry `workflow_args`: the values for the top-level `args` of the
workflow it names. Its `workflow` field must name that workflow, or the record is refused
as `workflow-args-without-workflow`. Show answers the arguments. The drain binds them
when it loads the item's workflow, and refuses a missing, unknown, mistyped or oversized
argument there, as it does for a containing stage's `args`.

A **store-declared item** is an item whose record is its only declaration. The term is
coined here. It is either an item whose record carries `workflow_args`, for which no
document block has an attribute, or a [filed request](#filing-a-request), whose words
only the store holds. Sync therefore never counts a store-declared item in
`unshared_open`, and the drain never sends it to document triage. The `doc-triage` and
`request` faces file items of this kind.

## Filing a request

`request` files one piece of work straight into the queue. The request's words, read
from standard input, are its whole statement. The item runs an existing workflow inside
a fence you name, lands through the normal gate, and never has a document block. The
face's name, the `item-request-` id prefix and the four fixed criteria below were chosen
by the implementer on 2026-11-18, and the owner may rename them. "Request" is the
[vocabulary's word](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#51-the-work-and-its-statement)
for an item that has no block.

```sh
printf '%s\n' 'Add two rows to yolo-packs/copilot-models/pack.toml: ...' |
  tillr-item request --store .tillr/state.db --root . --title "List two more Copilot models" --workflow single-worker@v1 --path yolo-packs/copilot-models/pack.toml
```

The flags are `--title`, `--path`, `--seat`, `--workflow`, `--criterion`, `--blocked-by`, `--ordinary`, `--to`, `--before` and `--after`; run `tillr-item request --help` for their details. `--title` and `--path` (repeatable, one repo-relative prefix of the fence; a directory ends in `/`, and wildcards, absolute paths and paths that leave the repository are refused) are required. `--seat` is `build` by default. `--workflow` names a file under `config/tillr/workflows/` or the built-in `single-worker@v1` (one run on the item's seat); by default `design` runs `scouted-design@v1` and `build` runs `planned-build@v1`, other seats must name one, and a workflow that declares top-level `args` is refused as `workflow-needs-args` (`doc-triage@v1` has its own face). `--criterion` (repeatable, at most four) adds criteria after the fixed four. `--blocked-by` (repeatable) names an existing item this request waits on and stores the dependency when the item is filed so it never looks ready. `--ordinary` files the request for the ordinary lanes only; by default a filed request is marked `express` when the item is written so it is eligible for the express lanes. `--to`, `--before` and `--after` place the item (last by default).

The face reads the workflow file before the store's write transaction opens, so the
transaction does no I/O but the store's own. It writes the item (with `express` set unless
`--ordinary` is given) and a `request_words` row in that one transaction: the words byte
for byte, their SHA-256, and a hash of the title, seat, workflow, fence, criteria,
express choice and dependencies. Nothing tracked is written.

The id is `item-request-` plus ten digits of the words' hash, lengthened on a collision.
Filing the same words with the same choices again answers the first item's place
without writing anything. The same words with any other choice (including the express
choice) are refused as `request-differs`, naming the existing item. The four fixed criteria are:

1. The change does what the words ask, within the fence, and nothing else.
2. The change is verified the way this repository's rules require: a test for new or
   changed behavior, `uvx vantage-check` for each changed Markdown document, and every
   command and its result in the report.
3. No `<!-- tillr: item … -->` comment is added, edited or relied on.
4. Nothing only the owner can decide is guessed. Such a decision is reported as a
   question for the owner.

The refusals are `words-empty`, `words-not-text`, `words-too-large`, `title-required`,
`title-braces`, `fence-required`, `fence-wildcard`, `workflow-reference`,
`workflow-missing`, `workflow-needs-args`, `workflow-required`, `criterion-empty`,
`criterion-braces`, `too-many-criteria`, `edge-target-missing`, `request-differs`, `unknown-item` and
`not-in-order`. A `{{` in the title or a criterion is refused because the brief template
reads it as a placeholder; the words may hold anything. Every refusal comes before the
first write. Filing a `build`-seat request whose fence admits `docs/`, `docs/design/`, or
a file named like a design document prints a notice on standard error naming those paths
and suggesting a separate `design`-seat item; it is a notice, not a refusal. The answer
is an `item.request` record carrying `item_id`, `place`,
`count`, `seat`, `workflow`, `allowed_paths`, `blocked_by` and `words_size`. An identical refile
answers `item_id`, `place`, `count`, `event_seq` and `event_head_seq`. The
[drain README](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#filed-requests) says how the drain runs one.

## State invariants

Item states, finished states, the ready/blocked/closed roles, and allowed moves come
from [the store's definition](https://github.com/mschulkind-oss/tillr/blob/main/tillr/definition/README.md), not from the item utility.
Print the declaration with `tillr store definition --store <path>`. Stores without a
recorded declaration use the embedded Git-landing definition, without a migration.

A distinct move must be listed in that definition; an unlisted move is refused as
`transition-not-allowed`, naming the source, destination, and definition. Leaving a
finished state additionally requires `--reason` and `--by` on `state` or `apply`;
the update event records `lifecycle_reason` and `lifecycle_by`. The closed role still
uses `close` and `reopen`, and a blocked-role state write still needs waiting metadata.

On `state`, entering any finished state accepts three optional flags:

- `--reason <text>` records why the item is finished as `lifecycle_reason`.
- `--by <actor>` records who decided as `lifecycle_by`.
- `--resolves-via <pointer>` records the evidence as `resolves_via`. The pointer is
  `item:<id>` for an item this store holds, `commit:<sha>` with a hexadecimal SHA,
  or `doc:<path>` with a nonblank path. Commit and document pointers do not require
  the referenced object to exist locally. An unknown item is refused as `pointer-unknown`.

The item row retains the most recent finished-state entry's values, `show` prints
those values, and the same `item.update` event carries them with `lifecycle_action: state`.
A subsequent finished-state entry replaces all three; omitted flags record empty values.
Leaving a finished state records its reason and actor in the event without replacing
that entry's stored evidence. Retrying the same state neither appends an event nor
replaces the stored evidence. Old finished rows remain readable with no invented receipt.
On Git-landing, `resolved-elsewhere` requires `--resolves-via` on `state` or
`apply`; the reason and actor remain optional for entry. An ordinary move to
`landed` requires completed criteria and current landing evidence, not these
flags. Leaving `landed` for `in-queue` requires reason and actor.

### Finish an item that already landed

If an open Git-landing item already has an unreverted landing and its remaining
work is covered by another commit or item, the operator can finish it without
recording criterion verdicts:

Use `tillr item state --state landed --finish-existing-landing` with the
operator's name, reason, and a `commit:<sha>` or `item:<id>` pointer to the
remaining work. See `tillr item state --help` for the required arguments. This writes an `item.update`
with `lifecycle_action: finish-existing-landing`, leaves the criteria and landing
history unchanged, and refuses a missing or reverted landing. A graph completion
alone is not a landing. The flag cannot be combined with `--require-verdicts`.
An ordinary move to `landed` still requires completed criteria, and `close`
still refuses an unreverted landing.

### Other state-writing checks

`state --require-verdicts` refuses a move as `criteria-unjudged` unless every
criterion has an `item.verdict` event bound to the hash of its stored text. This
check runs in the state move's own write transaction, so appending a criterion
after earlier verdicts cannot finish unjudged work. Any recorded verdict counts;
it need not be `done`. A verdict recorded before the item's latest withdrawal (below)
does not count. The flag is opt-in: ordinary state moves keep their existing
behavior. Hook completion always supplies it.

A state flag that cannot be stored on the requested move is refused as `flag-not-stored`,
naming the flag, even if its value is explicitly empty. `--reason` is supported for
entering or leaving a finished state or entering the blocked role; `--by` for entering
or leaving a finished state; `--resolves-via` only for entering a finished state;
`--waiter` only for the blocked role. A same-state finished retry accepts its entry
flags but leaves the original evidence intact.

Legacy `abandoned`, `deleted`, and `in-progress` rows stay readable but cannot be
written as destinations. Sync treats finished and legacy frozen rows as finished
for document-sharing counts; legacy `in-progress` rows still protect held work from
being copied into a new version-suffixed item.

The following rules describe Git-landing, including its established refusal names
(`state-terminal` and the close/reopen guidance) for compatibility:

- **An item's state and its criteria's states are different writes.** An item's state says
  where the work stands; a criterion's state says what a judge found. One face moving both
  would make a criterion's freeze depend on a caller's care.
- **Finished claims need evidence.** Entering `landed` requires at least one
  criterion, all `done` (`landed-criteria-unmet`), and a current `landed` landing
  row or `drain.graph-completed` event naming the item (`landed-receipt-missing`).
  A receipt is current only after the latest return from `landed` to `in-queue`,
  or when no return occurred. Entering `resolved-elsewhere` requires a pointer to
  another stored item (`item:<id>`), a commit (`commit:<sha>`) or a document
  (`doc:<path>`), otherwise `resolved-pointer-missing`; a current receipt instead
  refuses it as `already-landed`. `show` prints the pointer. Existing finished
  rows remain readable and new finished-item imports remain accepted.
- **Landed and resolved items stay finished.** Once an item is `landed` or
  `resolved-elsewhere`, raw `state` writes cannot move it elsewhere except that
  a reverted `landed` item may return to `in-queue` through `state` with `--reason`
  and `--by`; `apply` cannot make that return. `apply` also
  cannot change its stored definition, including its title, criteria, blockers or
  fence. These changes are refused as `state-terminal`, naming the item and its
  current state. The existing `criterion-state` and `criterion-frozen` rules are
  checked first and keep their names even when a record changes other fields too.
  Writing the same state or applying the same definition succeeds without changing
  the item or appending an event. Verdicts followed by the move to `landed` still
  finish the item; the explicit close and reopen commands are unchanged. `rank`
  changes rank alone, preserving the finished
  item's state and definition; raw `apply` cannot change that rank. Sync
  reports a landed item's remaining document block as stale and a resolved item's
  changed definition as frozen. It writes neither change and still syncs the
  file's other blocks.
- **A new blocked state move names who must act and why.** `state --state blocked`
  requires `--waiter` and `--reason`; either missing or blank is refused as
  `blocked-waiter-missing`. The waiter is `owner`, `design`, `plan`, or `item:<id>` for
  an item this store holds. Only `not-started`, `in-queue`, and `blocked` may move to
  `blocked`. The item stores `waiting_on` with `waiter` and `reason`, plus
  `underlying_state`, the state before the first blocked move. Updating a blocked
  item's waiter preserves that original state. Moving to `not-started` or `in-queue`
  clears all three values. `show` prints the stored metadata; an older blocked row
  with none remains readable and says `waiter not recorded`.
- **Closing and reopening are explicit commands.** `close` accepts only
  `not-started`, `in-queue`, or `blocked`, records
  `close_reason`, `closed_at`, `closed_by`, and `closed_from`, and refuses a successful
  landing receipt as `already-landed`. Blank reasons are `close-reason-missing`.
  `reopen` also requires a reason and actor and restores `closed_from`, preserving a blocked
  item's waiter, reason, and underlying state. Its reason and actor are recorded in
  the update event. Raw `state` and `apply` writes cannot close or reopen an item;
  their refusals name the command to use. Sync may update a closed item's definition
  but never reopens it or clears its blocked metadata. Old closed rows without a
  saved prior state remain readable; reopening them refuses as `closed-from-missing`.
  Both commands accept an optional `--now` clock, defaulting to the wall clock.
- **A verdict is the only writer of a criterion's state.** No other face moves one, which
  is what makes a recorded judgment the answer to "why is this criterion where it is".
  The one inverse is a **withdrawal**: `item.WithdrawVerdicts`, which the drain calls when
  it automatically reverts a landing, clears every criterion state the item carries and
  appends an `item.verdicts-withdrawn` event naming the cleared states, the reason and
  the actor. A reverted landing's verdicts judged a change the trunk no longer holds;
  withdrawn, they no longer freeze the definition (`definition-frozen`), so a design
  answer that amends the block syncs. An item with no state to clear records nothing.
- **A conflict is recorded once per transition.** A transition is a changed record of
  the same conflict: its routing, answer, report, origin run, document, or blocked
  criteria may change. A record equal in every field to that conflict's last record
  is not written and does not change the item's disposition. A longer report from a
  second failed run is new evidence, even when the status and design run stay the same.
- **A decision is superseded, never edited.** The superseded record stays and the new one
  names it, so the ledger reads in order.
- **A seat is a role, not a model.** An item names at most one seat. Its name starts with a
  lowercase letter and contains only lowercase letters, digits and hyphens. The backend
  policy declares which seats exist and maps each to a backend, and the backend names
  the model it runs. Swapping which model fills a seat, or moving the seat to another
  backend, is a policy edit that touches no item.
- **Sync is the only intake from documents, and it never destroys a local fact.** `sync`
  reads every `<!-- tillr: item ... -->` directive that starts a line outside a fenced block
  in the Markdown git tracks under `docs/`, and writes the items they declare through the
  same record→row mapping `apply` uses. The directive grammar and the field mapping are
  [the queue-persistence ruling's Section 1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/queue-persistence-ruling.md#1-shared-item-block-shape).
  A new item is `in-queue`, or `not-started` when a roadmap's `## Icebox` or
  `## Exploration` section contains its block or links to its document. Parking also
  moves a stored `in-queue` item to `not-started` and clears its rank, through one
  `item.update` event. Only a whole sync can observe an item leaving every parked
  section and return a stored `not-started` item to `in-queue`, after every rank
  the store holds, even if the parked item retained an old rank. This also queues
  an item manually set to `not-started` whose document is not parked. Other states,
  runs and criterion states are preserved. The `item.sync` record lists these moves
  under `parked` and `unparked`; text output names them too. A dry run reports the
  same planned moves without writing. A new block may name its source with
  `from="<item id>"`: a keyed source, including a landed one, places it immediately
  after that source and earlier items placed by the same mark, in block order.
  A malformed source id is a finding; an absent or unkeyed source falls back to
  placing the item last. This mark never moves an existing item. Placement events retain
  `placed_from`, so retiring a sibling's block does not lose its placement history.
  Sync and `place` share key choice and respacing; crowded gaps can change other
  keys while preserving their relative order. A changed block updates the item's definition while no
  criterion carries a state, and is reported `definition-frozen` once one does.
  Parking and unparking still apply when the definition is frozen, using the stored
  definition rather than the changed block. A second run
  over unchanged documents changes no item and appends no event; it refreshes the
  saved sync observation.
- **Choose the documents with repeated `--roadmap <path>` flags.** Without the flag,
  sync reads every tracked Markdown document under `docs/`. With it, sync reads only
  the named roadmaps and the tracked Markdown documents under `docs/` they link
  directly, including parked sections. Links in those documents are not followed.
  Paths are repository-relative, must name existing tracked Markdown files, and
  need not have the filename `roadmap.md`. Invalid selections refuse with exit `2`
  before opening a store writer. Scoped reads refuse symlink traversal, including
  linked symlinks, rather than import their untracked targets. Repeated copies of
  one path are read once.
  First-import entries interleave in flag order instead of roadmap path order.
  Items whose blocks were not read retain their definitions, states and rank keys;
  missing keys are filled only for declared items. If source or saved-order placement
  would rewrite an unread item's crowded rank key, the entire narrowed sync refuses
  without writing; run a sync of every document for that placement.
  The `item.sync` payload's `scope` is `"all-documents"` or the ordered list of selected
  roadmap paths. Narrowed reports encode `unshared_open` and `stale_doc_blocks` as
  JSON `null` (not computed), and text says they need a sync of every document.
  They do not replace the last whole-sync residue observation or erase warnings
  for documents outside the selected set. `--dry-run` works with either scope.

  ```bash
  tillr item sync --store .tillr/state.db --root . --roadmap docs/tillr/roadmap.md --roadmap docs/pi/roadmap.md
  ```

- **Read faces show places, not stored keys.** A [place](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-actions.md#queue-order-and-places)
  counts from 1 across the full order; parked and finished items have no place.
  Empty keys count after keyed items, in item id order. A writing sync fills those
  empty keys in that same order, one item update each in its own transaction;
  opening or migrating a store never does. New and newly unparked items with no
  key are added after every key the store held when sync began. Existing records
  still accept the legacy `rank` field through apply and fixture seeding; `apply`
  also accepts `place` (ignoring it on write) and keeps an existing item's stored
  rank when `rank` is omitted, so a record read with `show` can be passed back to
  `apply` unchanged without moving in the queue.
- **First-import order follows the roadmaps.** Every tracked `docs/**/roadmap.md`
  is a roadmap, taken in path order. Its entries, read top to bottom outside
  `Icebox` and `Exploration`, are its own item blocks and the first links to
  tracked documents holding item blocks. A linked document's blocks stay in
  document order within that entry. Links to other roadmaps are not entries;
  a document reached by two roadmaps belongs to the first roadmap that reaches it.
  Sync takes one entry from each roadmap in turn, then the next entry from each;
  items in documents no roadmap reaches follow in path and document order.
  A new item without a usable `from` gets its first place last, in that order.
  Stored items in the order without a key go last before new imports, in item id
  order. Sync never changes the relative order of items already placed; only
  insertion by `from` into an exhausted gap may rewrite their keys, without
  changing that order. Appended keys are allocated as a batch, not extended once
  per item. The `item.sync` record's `placed` list and text output name each item
  given a place, ordered by its final place number, with its source when placed
  by `from`, and never its key. A dry run reports the same planned places and
  writes nothing.
- **A whole writing sync saves its residue counts in the same transaction.** The
  item-owned `item_sync_residue` table holds one row: the sync time, ids of open store
  items no document declares (`unshared_open`), the oldest one's last write, and ids
  of landed items whose blocks remain (`stale_doc_blocks`). Each whole sync replaces
  that row even when no item changes. A dry run leaves it alone; a caller supplying
  only some documents must set `SyncInput.Partial` and also leaves it alone. Status
  reads this observation, not documents, so its timestamp names when counts were computed.
- **A document with a finding writes no definition; the others are written.** A missing or
  malformed id, a duplicate id, no paths, no `c1`, an unknown attribute, a malformed seat name, or a
  `blocked_by` naming nothing a block declares or the store holds is reported with its file
  and heading, and the face exits `1` after the other documents are applied. A path that
  does not exist yet is a warning, since the slice may create it. Missing keys on
  items already in the order are still filled independently of document findings.
- **Rank changes priority, not an item's definition.** `tillr item rank` requires an
  existing item and an explicit `--rank` token. It changes only rank, `updated_at` and
  `event_seq` bookkeeping, appending one `item.update` event in the same transaction.
  State, criteria, fence, dependency edges, workflow and runs remain unchanged, including
  on finished or closed items. Repeating the stored rank changes nothing and appends
  no event. An empty token clears priority; an omitted token is refused. Tokens use
  `0123456789abcdefghijklmnopqrstuvwxyz`, compared as strings;
  an empty rank sorts after every recorded priority. `--now` is optional and defaults
  to the wall clock.
- **A mail delivery is acknowledged once, by a named session.** A second acknowledgement is
  a refusal and not a second row.

## Automatic prose rewrites

Sync keeps prose findings advisory by default. When the shared settings reader finds
`prose.rewrite = true` in the repository settings, each non-landed item with a
`prose-unmarked` or `prose-shape` finding gets one stored `rewrite-<item id>` item.
A repeated sync leaves that rewrite's identity, state and criteria alone. The rewrite
uses the build seat, fences only the source document and carries four fixed criteria:
rewrite its title and section prose to the guide; preserve the protected directive
fields; preserve every criterion's meaning and number; and pass the prose check.
These generated items are not missing document declarations in sync's residue count.

```toml
# .tillr.toml; absent file or key means false
[prose]
rewrite = true
```

The common reader refuses non-boolean values and unknown keys in this table. Sync's
dry run reports proposed rewrites without writing them. The
[drain's rewrite lifecycle](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#prose-rewrites-before-build)
holds the original and checks the candidate before the gate.

## Standalone invocation

```sh
tillr-item show
tillr-item decision list
tillr-item sync
tillr item rank --store .tillr/state.db --item item-042 --rank am
tillr item close --store .tillr/state.db --item item-042 --reason "not doing this" --by owner
tillr item reopen --store .tillr/state.db --item item-042 --reason "wanted again" --by owner
```

`show` and the read verbs are what an external reviewer needs: they answer from one store,
name what is not there rather than guessing, and write nothing. `sync --dry-run` is the same
for the documents: it prints what a sync would create, update and refuse, with the two
residue counters — `unshared_open`, the open store items no block declares, and
`stale_doc_blocks`, the blocks whose item this store already holds as `landed` — and writes
nothing. The sync report also carries `document_fed`: true when at least one
stored item, in any state, has a document block. The drain uses this same comparison
for its [document-fed workflow prerequisite](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#document-fed-workflow-prerequisite);
sync itself neither dispatches triage nor changes the queue’s readiness rules.

## Showing a design's questions

```sh
tillr item show --store .tillr/state.db --item item-042 --root .
tillr item show --store .tillr/state.db --item item-042 --root . --questions-cmd "my-index index --format json"
```

When an item names `design_doc`, `show` reads its unanswered questions through the
shared Vantage index reader in [`tools/tillr/vantage`](https://github.com/mschulkind-oss/tillr/blob/main/tillr/vantage), the same
reader the loop and view use. The default command is
`uvx vantage-check index --format json`; `--questions-cmd` overrides it with a
whitespace-separated command and arguments, just as in the loop. It runs at `--root`.
Questions are read anew at show time and never stored.

`questions_source` names the design document. `questions_status: "known"` means the
index answered; each unanswered question carries `question_id`, `text` (the index's
full title) and `state`. Only `answered` is settled: blocked and unfamiliar states
remain in the list. A successful read with no unanswered questions is known, not
unknown. With no design reference, all these question fields are omitted.

If the command is missing, fails, or returns a refused or invalid index, `show`
still prints the item with exit 0, `questions_status: "unknown"` and
`questions_reason` explaining why. The omitted question list in that case does not
mean there are none. A missing design document remains a `design-doc-missing`
rejection, distinct from an index failure.

## Listing a store's items

```sh
tillr item list --store .tillr/state.db
tillr item list --store .tillr/state.db --state landed --state resolved-elsewhere
```

`list` reads one store snapshot without writing or migrating it and prints one
`item.list` record. Its `payload.items` array contains every item, including finished
items, in item-id order. Each entry contains `item_id`, `title`, `state`, `blocked_by`
and `updated_at`. Blockers are the stored dependency ids in id order, not just those
that currently hold work; `updated_at` is the item's last update in UTC, in RFC 3339
format. No body or design document needs to exist for an item to be listed.

With no state filter, every state is included. Repeating the state filter selects
items matching any of its exact values, without duplicates. Values with no matches
produce no entries. An empty store or a filter matching nothing returns
`payload.items: []` with exit 0. The command requires an existing store; a missing
store is a usage error, not an empty queue. Run `tillr item list --help` for its flags.

## Written judge verdicts

`judge` is the shared intake for a judge's written verdict file, following
[the judge output contract](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md#21-topology-relative-to-gate-and-land-d-jl1).
It checks every stored criterion before recording any, then writes one `item.verdict`
per criterion in a single transaction. It never moves the item's own state; the caller
routes the outcome. A refused file changes no criterion and appends no verdict event.

```sh
tillr item judge --store .tillr/state.db --item item-example --input eval.output --head <full-candidate-sha> --actor 'run=review-run;session=review-session' --judged-actor 'run=writer-run;session=writer-session' --grant <grant-id> --dry-run
```

The file is a JSON object, not a record envelope:

```json
{
  "evaluated_head": "<full-candidate-sha>",
  "evaluated_tree": "<candidate-tree-sha>",
  "outcome": "pass",
  "verdicts": [
    {
      "criterion_id": "c1",
      "criterion_hash": "<sha256-of-stored-criterion-text>",
      "verdict": "done",
      "claim_class": "verification-only",
      "reason": "The cited test proves the required behavior.",
      "evidence": ["tests/example.test.go", "commit:<candidate-sha>"]
    }
  ]
}
```

Include exactly one entry for every criterion. Each entry needs its stored text's
SHA-256 hash, its stored claim class, a nonblank reason, and a nonempty list of
single-token evidence locators (paths, commit references, URLs or record ids, not prose).
Entries may also carry `waiting_on` for the caller's blocked-state routing; intake
retains that payload in its checked result but does not route it. Unknown fields and
duplicate JSON keys are refused. Any `actor` or `judged_actor` field, including an
empty or null field anywhere in the file, is refused as `actor-in-output`.

The caller supplies the identities and grant. Run and session identities can be
written as `run=<id>;session=<id>` (optionally with `;route=<route>`). The compact
forms `node:run_ids:route` and `run_ids:routes` locate comma-separated run ids by
position, whatever their spelling. A compact identity can also use
`node:run_ids:session:route` when the session is `session-` prefixed or a UUID.
Use tagged identities for opaque session ids or routes containing colons. Ambiguous
identities are refused as `actor-provenance`. Shared run or session handles are
refused as `self-judging`; a shared model route is not. The
candidate head must be a full commit SHA in the current Git repository; intake
resolves its tree independently and compares both with the file. The recorded grant
must exist, remain unexpired, and cover every entry's claim class. An agent cannot
judge an `owner-only` criterion.

`outcome` is `pass` only when every entry is `done`; otherwise write `fail`.
The command prints `pass`, or the first applicable outcome in the order `defective`,
`blocked`, `not-done`, with exit codes 0, 10, 11 and 12 respectively. A refused file
prints an `item.rejection` record and exits 1. `--dry-run` performs the same checks
through a read-only store transaction, without migration or writes. Without it,
valid failing verdicts are recorded too. `--landed <full-sha>` adds that commit to
both `landed` and `evidence` on each `done` verdict only. The optional `--now` clock
lets tests inject a timestamp; otherwise the current UTC time is used.

An item directive can opt in with `judge="agent"`. Sync stores this as `judge_mode`,
and show prints it (empty means no agent judge requested). Any other directive value,
including an explicitly empty one, produces `invalid-judge`, naming its block.
This request alone does not start a judge: dispatch and outcome routing are separate
steps in [the judge build order](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md#8-build-order).

## Hand-driven review comments

```sh
tillr item comments take --store .tillr/state.db --doc docs/design/example.md --vantage http://localhost:8001 --repo tillr
```

Intake reads Vantage's public review interface and prints one generated item id, or nothing
when no comment is pending. It writes the batch and its design-seat item in one transaction;
that item's fence includes the document, its existing companion plan, and roadmaps linking
to it. Generated ids end in a numeric batch counter, unlike the build items implementing
this feature, and `sync` excludes them from its missing-definition count. A document with an
open batch does not get another one.

The [designer-session reference](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-designer-sessions.md#the-words-this-design-uses)
defines batches and desks. When `[comments]` is configured in `.tillr.toml`, the drain polls
Vantage automatically after each document's quiet window; this command is the hand-driven path.

## What it refuses

- **A record from another system** — `producer.system` must be `tillr`. Any other
  value is refused as a record from another system, with exit 2, before opening the
  store. The producer's utility need not be `item`: tillr's other programs also
  produce item records.
- **An unknown item payload field** — `apply` refuses fields outside the item record's
  supported payload shape, including fields inside criteria, with exit 1 and an
  `item.rejection` record whose rule is `unknown-field` and whose reason names the
  field. For example, `"kind": "finding"` inside the payload is refused, not silently
  discarded. The refusal changes no item, criterion, dependency, request or event,
  even when the record repeats an already applied request's idempotency key (the key
  that makes a repeated request return its original result without another write).
- **A state move the item's own state machine does not allow** — refused, with the item
  unchanged and no event.
- **A verdict with no criteria to move** — refused: an item with no criterion retires
  nothing.
- **An edge to an item that is not in the store** — refused, in the same transaction that
  would have written the item.
- **A noun whose verb it does not have** — refused, naming the verbs it does.

## Test

`go test ./...` in this directory builds the binary and drives every face against a scratch
store, plus the item package's own suite for the schema and the record-to-row mapping.
