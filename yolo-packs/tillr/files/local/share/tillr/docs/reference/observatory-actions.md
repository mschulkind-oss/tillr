---
title: "Observatory actions — queue placement, idea intake, and browser writes"
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - tools/tillr/settings/settings.go
  - tools/tillr-item/place.go
  - tools/tillr-item/idea.go
  - tools/tillr-item/item/place.go
  - tools/tillr-item/item/idea.go
  - tools/tillr-item/item/order_places.go
  - tools/tillr-item/item/sync.go
  - tools/tillr-item/item/sync_from.go
  - tools/tillr-drain/drain/idea.go
  - tools/tillr-view/serveplace.go
  - tools/tillr-view/serveidea.go
  - tools/tillr-view/order.go
  - tools/tillr-view/idea_words.go
  - tools/tillr-view/idea_tree.go
  - tools/tillr-view/web/src/components/ItemsPasses.tsx
  - tools/tillr-view/web/src/components/NewIdea.tsx
tags: [tillr, observatory, queue, intake, reference]
summary: "How tillr numbers and moves items in the queue (`tillr-item place` and first-import placement), saves the owner's words verbatim as a queued design item and checks the design turn that answers them (`tillr-item idea`), and exposes both writes over HTTP when `tillr-view serve` runs with `--write`."
---

# Observatory actions — queue placement, idea intake, and browser writes

> **Delivered copy.** This is `docs/reference/observatory-actions.md` from this repository at commit `06e63d0112` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-actions.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Verified 2026-11-24 against the tree. UNMEASURED: no use of the
page's writes on a live queue is recorded here; verification rests on reading
the code and on the Go and front-end suites.

The observatory is read-only by default. Started with `--write`, `tillr-view
serve` exposes two writes: moving an item in the queue, and saving the owner's
raw words as a queued design item (an **idea**). Both writes are `tillr-item`
commands. The HTTP server keeps its own store handle read-only and runs
`tillr-item` as a child process, one request at a time.

| Component | Lives in |
| :--- | :--- |
| Repository `[intake]` settings | `Intake` in `tools/tillr/settings` |
| The `place` and `idea` command faces | `tools/tillr-item` (`placeFace`, `ideaFace`) |
| Key allocation, source-mark placement, idea rows | `tools/tillr-item/item` (`Place`, `insertOrdered`, `placeFrom`, `Idea`) |
| The idea's brief and the check on its candidate | `tools/tillr-drain/drain` (`ideaTask`, `validateIdea`) |
| Place numbers on read faces, the idea words reader, the two HTTP write handlers | `tools/tillr-view` (`itemPlaces`, `readIdeaWords`, `placeHandler`, `ideaHandler`) |
| Move controls and the new-idea form | `tools/tillr-view/web/src/components` (`ItemsPasses.tsx`, `NewIdea.tsx`) |

**Reads with:** [the tillr observatory reference](observatory-redesign.md)
(the pages and the read-only server contract),
[tillr item states](tillr-item-states.md) (the projected states),
[queue persistence](tillr-queue-persistence.md) and
[queue persistence](tillr-queue-persistence.md) (document-backed item blocks
and private store state), [the idea pipeline](../design/tillr-idea-pipeline.md)
(lineage and downstream shaping), and
[the decision provenance review](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md) (the
owner ruling [`OQ-PR13`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR13) and
the open reviews [`OQ-PR11`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR11)
and [`OQ-PR16`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR16)).

---

## Queue order and places

An item's **place** is its 1-based position in the queue order. The store keeps
a sparse sortable string in `items.rank`; read faces show the place and never
the stored key.

- **Membership (`InOrder`).** A parked item (`not-started`) and a finished one
  (`landed`, `closed`, `resolved-elsewhere`) are outside the order and have no
  place: `null` in JSON, `—` in a table. Every other item has one.
- **Total order.** Items with a key come first, by key; items with no key
  follow, by item id. So an item has a place before sync has given it a key,
  and giving it one does not change any place.
- **Stable numbers under filters.** Places are counted over the full order
  before a face filters it, so a filtered Items page shows true places with
  gaps.
- **Round trip.** `tillr-item apply` accepts and ignores a `place` field, so
  `tillr-item show` output can be applied back, and keeps an item's stored key
  when the input names none.

### Moving an item

`tillr-item place` moves one in-order item and changes nothing else about it:

```sh
tillr-item place --store <db> --item <id> (--to first | --to last | --before <other> | --after <other>) [--by <actor>] [--via <surface>]
```

- **A destination names a neighbor, never a number.** A move is `first`,
  `last`, or before or after a named in-order item. A move sent from a stale or
  filtered table still lands beside the named neighbor, with no version check.
- **One row per ordinary move.** The moved item gets a key strictly between its
  new neighbors' keys, and one `item.update` event carrying `placed_by` and
  `placed_via`. When no key fits between the neighbors, or the key would pass
  the length cap, every in-order item is given an evenly spaced key in the same
  transaction, with one event for each item whose key changed. No item's
  relative order changes.
- **A move to where the item already is writes nothing.** It answers with the
  item's current place.
- **The answer** is an `item.place` record: `item_id`, `place`, `count` (items
  in the order), `event_seq` and `event_head_seq`.
- **Refusals.** An item or neighbor the store does not hold is refused as
  `unknown-item`; a parked or finished one as `not-in-order`; a store whose
  writer lock does not come free as `store-busy`.

### First-import placement

`tillr item sync` never changes a key an item already has. Opening or migrating
a store assigns no keys; sync does.

1. **Missing keys first (`keyMissingOrder`).** Before it imports anything, a
   writing sync gives a key to each stored in-order item that has none,
   appending them after the highest key in item-id order. That is the order
   they already read in, so no place moves.
2. **Append.** A newly imported in-order item, or a parked one that sync
   unparks with no key, is appended after the highest key, in the order the
   documents are walked.
3. **Source mark (`placeFrom`).** After every append, a newly created item whose
   block carries `from="<source-id>"` moves to just after its source, or after
   the last sibling already placed from that source, so one turn's items keep
   their block order. The source counts if the store holds it with a key, even
   when it has landed. The move's event records `placed_from`, with
   `placed_via` set to `sync`. A source that is missing or has no key leaves
   the item where the append put it.

Running the source marks after the appends means a source imported later in the
same sync is never moved away from its children.

<a id="the-order-record"></a>

### Where hand-set order lives

Hand-set order lives only in the store (owner ruling
[`OQ-PR13`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR13), 2026-11-20). It
survives a restart on the same database. A rebuilt store starts from
first-import placement and does not restore hand moves. Sync reads no
queue-order file, and reordering writes no repository file and no commit. The
event kind `drain.order-record` stays registered only so that a store holding
such events still opens.

---

## Idea intake and the design turn

`tillr-item idea` reads raw text from standard input, keeps an exact copy under
`<store-dir>/intake/`, and creates a `design`-seat queue item at the named
place. The owner supplies no id, title, fence or Markdown block:

```sh
tillr-item idea --store <db> --root <repo-root> (--to first | --to last | --before <id> | --after <id>) [--by <actor>] [--via <surface>]
```

### Words files and preservation

- **Configuration.** Saving an idea needs both `[intake]` keys in
  `.tillr.toml`: `dir`, the repository-relative directory where landed words
  files live, and a non-empty `paths`, the path prefixes the design turn may
  edit beside its words file. Without them the command keeps the words file and
  refuses with `intake-not-configured`, naming the file. This repository's
  [`.tillr.toml`](https://github.com/mschulkind-oss/tillr/blob/main/.tillr.toml) sets no `[intake]` table.
- **Validation (`IdeaHash`).** The bytes must be within the size cap in
  [Current values](#current-values) (`words-too-large`), valid UTF-8
  (`words-not-text`), and not empty or only whitespace (`words-empty`). They
  are never normalized.
- **Words before rows.** The store is opened and its write transaction taken
  before any file is written, so a store that cannot be opened or is busy
  writes nothing and says no idea was saved. Inside the transaction,
  `PreserveIdea` writes the exact bytes to `<store-dir>/intake/<id>.txt` with
  exclusive creation and flushes the file, its directory and the directory
  above before any row is written. If the store write then fails, the file
  stays and the refusal (`intake-store-failed`) names it.
- **The id comes from the words.** It is `item-idea-` plus a prefix of the
  SHA-256 of the bytes, lengthened only when a shorter prefix is taken by other
  words.
- **A repeat is read-only.** Saving bytes the store already holds as an idea
  answers with that item's id and current place. It writes nothing and does not
  move the item to the repeat's destination.
- **Not yet in the tree.** Until the words are a regular file at the trunk's
  current commit, the item's answer says `in_tree: false` and the Item page
  says so. The bound on that gap proposed as `D-QP4` in
  [queue persistence](tillr-queue-persistence.md#why-its-this-way) is not
  built.

### The saved idea item

`Idea` creates one `items` row and one `idea_intake` row in the caller's
transaction, then places the item.

- **The item** is `in-queue` on the `design` seat, titled with the first line
  of the words (cut at a word boundary past the length in
  [Current values](#current-values)). Its fence is the words path
  `<intake.dir>/<id>.txt` followed by `intake.paths`. It carries three fixed
  criteria: the words are in the tree byte for byte; every piece of work they
  ask for is an item block that carries `from` this idea, or the design
  document says why it is not done; and anything only the owner can decide is
  an open question, not a guess.
- **The `idea_intake` row** holds the item id, the words' hash and size, the
  tracked words path, and the path of the copy under the store directory. The
  hash is unique, which makes concurrent repeats one item.
- **Dispatch.** A saved idea has no document block, so it is exempt from the
  document prerequisite that holds an item with no block. It is
  express-eligible by its id prefix.

### Staging, gate refusals, and outcomes

- **The brief (`ideaTask`).** Before the design run starts, the stager reads
  the store's copy of the words and checks its size and hash against
  `idea_intake`. The brief's task tells the designer to copy the words to the
  words path unchanged, write what they ask for as item blocks carrying
  `from="<idea-id>"`, and file every owner decision as an open question. It
  includes the whole prose guide. The words themselves follow at the end of the
  brief in a byte-counted section, and the brief's title is fixed text, so
  nothing in the owner's words is read as template syntax.
- **Two valid outcomes.** A clear idea lands its words file and one or more new
  item blocks carrying `from`. An idea that needs the owner lands its words
  file, a design document with open questions, and one design-seat item block
  carrying `from` that names the document, which then waits on those
  questions. The idea's own item is never renamed: it lands once, and its
  outcomes are new items.
- **The check before the gate (`validateIdea`).** The drain reads the
  candidate commit, never the working files, and refuses it on three
  conditions: `words-missing` (the words path is absent), `words-altered` (it
  is not a regular file, or its bytes differ from the saved size and hash),
  and `idea-no-outcome` (no item block carrying `from` this idea has an id
  that is new: absent from the base commit's documents and from the store).
- **After landing.** Sync imports the new blocks and `placeFrom` puts them
  just after the idea. `readIdeaWords` takes the words from the trunk's
  current commit when the path is a regular file there, and otherwise from the
  store's copy; either way it checks them against the saved size and hash. An
  idea's outcomes are the items whose placement event records `placed_from`
  the idea. That receipt outlives retired blocks and later hand moves.

---

## Browser writes

`tillr-view serve` accepts writes only when started with `--write`. Its own
store handle stays read-only. The two routes take a server-wide lock and run
`tillr-item` with `--by owner --via observatory`, bounded by the request's own
lifetime. The program is the one `--tillr-item` names, else a `tillr-item`
beside the server's executable, else the one on `PATH`.

> [!WARNING]
> **`--write` adds no authentication (`D-OA8`).** A request that carries an
> `Origin` header is accepted only from a loopback origin. A request with no
> `Origin` (a script, `curl`) is accepted, since no cross-site page can send
> one. Never combine `--write` with a non-loopback `--addr` unless
> every host that can reach the socket is trusted to reorder items and queue
> design runs.

- `POST /api/v1/items/{id}/place` takes a JSON object with exactly one member:
  `{"to":"first"}`, `{"to":"last"}`, `{"before":"<id>"}` or `{"after":"<id>"}`.
- `POST /api/v1/ideas` takes `{"words":"<text>","place":<destination>}` with
  the same destination forms, and passes the words to `tillr-item idea` on
  standard input.
- Both answer `200` with the standard `{snapshot, data}` envelope. `data` is
  the command's answer (`item_id`, `place`, `count`, `event_seq`,
  `event_head_seq`), and the snapshot's event head is the one the command
  reported, so a client can wait for its own write.
- Every snapshot carries `writes_enabled`, which is how the page knows whether
  to draw the controls.

### HTTP refusals

A refused write answers `{snapshot, error: {code, message}}`. The codes and
their statuses are one table in `tools/tillr-view` (`apierror.go`); the ones
with meaning for a client:

| Code | Status | When |
| :--- | :--- | :--- |
| `read-only` | 405 | Any `POST` under `/api/v1/` except the client-timing route, on a server started without `--write` |
| `origin-refused` | 403 | An `Origin` header that is not loopback |
| `content-type` | 415 | The body is not declared `application/json` |
| `too-large` | 413 | The body passes the cap in [Current values](#current-values) |
| `bad-request` | 400 | Malformed JSON, an unknown or repeated member, trailing JSON, or anything but exactly one valid destination |
| `unknown-item` | 404 | The moved item or the named neighbor is not in the store |
| `not-in-order` | 409 | The moved item or the named neighbor is parked or finished |
| `words-empty`, `words-not-text`, `words-too-large` | 422 | The words fail validation |
| `intake-not-configured` | 409 | `[intake]` is unset; the message names the kept words file |
| `intake-store-failed` | 500 | The store write failed after the words were kept; the message names the file |
| `store-busy` | 503 | `tillr-item` could not take the store's writer lock |
| `writer-unavailable` | 503 | `tillr-item` could not be found or started, or its answer was not a valid result |

### Browser controls

- **Items page.** Each row shows its place, and an idea's row is marked as one.
  With writes off the page says the server is read-only. With writes on, each
  movable row has a move-to-top button; sorted by place, rows can also be
  dragged, dropped below the last row, and moved one visible row with `J` and
  `K` (lowercase `j` and `k` move focus). One move is in flight at a time. The
  page shows the new order at once, restores the old one and says why if the
  write is refused, and keeps the new one with a warning if only the refresh
  after the write fails.
- **New idea form.** Drawn only when writes are on; opened by its button or
  `n` outside a text field. It takes the words and a place (top, bottom, or
  after a chosen item) and saves with its button or `Ctrl+Enter`. The draft is
  kept in the browser's `localStorage` per store path, survives closing the
  form or a refused save, and is cleared after a save only if storage still
  holds the draft that was sent, so a newer edit in another tab is not erased.
- **Item page.** For an idea it shows the words verbatim, whether they are in
  the tree yet, and the items that came from the idea.

---

## Current values

The prose above says how these behave; this table is the one place their
literal values are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Key alphabet | `0`–`9`, `a`–`z` | `RankAlphabet` in `tools/tillr-item/item` |
| Longest key before the order is respaced | 64 characters | `betweenKeys` in `tools/tillr-item/item` |
| Respaced keys | 8 characters, evenly spaced over `36^8` | `insertionKeys` in `tools/tillr-item/item` |
| Default receipt on a move | `placed_by = "owner"`, `placed_via = "cli"`; `"observatory"` from HTTP; `"sync"` from a source mark | `placeFace`, `ideaFace`; `placeHandler`, `ideaHandler`; `placeFrom` |
| Idea id | `item-idea-` + the first 10 hex digits of the words' SHA-256, lengthened on collision | `Idea` in `tools/tillr-item/item` |
| Largest idea | 256 KiB | `MaxIdeaWords` in `tools/tillr-item/item` |
| Idea title | The first line, cut at a word boundary when longer than 80 characters | `ideaTitle` in `tools/tillr-item/item` |
| Store copy of the words | `<store-dir>/intake/<id>.txt`, mode `0600` in a `0700` directory | `PreserveIdea` in `tools/tillr-item/item` |
| Intake settings | `[intake] dir`, `paths` | `Intake` in `tools/tillr/settings` |
| Server write switch and writer program | `--write` (off by default), `--tillr-item` | `tillr-view serve` |
| HTTP write body cap | 1 MiB | `readWriteRequest` in `tools/tillr-view` |
| Browser draft key | `tillr.idea:<store path>` | `NewIdea.tsx` |

---

## Why it's this way

Each row is a decision a maintainer reading only the behavior above might
otherwise undo.

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| `D-OA1` | Show 1-based places on every face and keep sparse keys hidden in `items.rank` | Dense integers would rewrite every row below a move and append an event for each; a sparse key keeps an ordinary move to one row and one event. |
| `D-OA2` | A move names `first`, `last`, or a `before`/`after` neighbor, never a place number | "Move to place 4" lands in the wrong spot once another writer moves a row above it; a named neighbor stays exact from a stale or filtered view with no version check. |
| `D-OA3` | Existing `items.rank` strings are opaque keys, unkeyed in-order items are keyed once by sync, and no key range is reserved | Rewriting keys on upgrade is needless churn, and a reserved prefix inverts priority when the owner moves another item above an idea. |
| `D-OA4` | Hand-set order lives only in the store: no tracked order file, no commit (owner ruling [`OQ-PR13`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR13), 2026-11-20: "database only") | Rejected: a `rank` attribute in Markdown item blocks, commits made by the server outside `tillr-land`, and an order file written by a loop landing. |
| `D-OA5` | The owner's raw words are a hash-checked `.txt` file, first under the store directory and then in `<intake.dir>/` when the idea lands | A `.md` file would put an unedited dump through the Markdown checks, and words kept only in SQLite are lost when a store is rebuilt. |
| `D-OA6` | An idea's design turn lands its words file plus new `from`-marked item blocks (or one design document with open questions and one `from`-marked design item); the idea item is never retitled *(designer's decision; open for owner review as [`OQ-PR11`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR11))* | An item lands once. New blocks carrying `from` let one idea become several items at the idea's place, and keep an unclear idea visible as an item waiting on its questions. |
| `D-OA7` | `tillr-view serve --write` keeps its store handle read-only and runs `tillr-item` under a lock for both writes | A server that opened its own writer would be a second item-mutation path, with its own copy of the validation, the event receipts and the lock contention. |
| `D-OA8` | Browser writes are gated by `--write` and an `Origin` check, with no login *(designer's decision; open for owner review as [`OQ-PR16`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR16))* | Both writes are reversible and the server binds loopback by default; a login would break the observatory's rule that it needs none. |
