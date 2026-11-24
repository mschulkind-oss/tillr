---
title: "Observatory actions — queue placement, idea intake, and browser writes"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: 351b4186c
covers:
  - tools/tillr/settings/settings.go
  - tools/tillr-item/place.go
  - tools/tillr-item/idea.go
  - tools/tillr-item/item/place.go
  - tools/tillr-item/item/idea.go
  - tools/tillr-item/item/order.go
  - tools/tillr-item/item/order_ids.go
  - tools/tillr-item/item/order_places.go
  - tools/tillr-item/item/sync_from.go
  - tools/tillr-drain/drain/idea.go
  - tools/tillr-view/serveplace.go
  - tools/tillr-view/serveidea.go
  - tools/tillr-view/order.go
  - tools/tillr-view/idea_words.go
  - tools/tillr-view/idea_tree.go
  - tools/tillr-view/web/src/components/ItemsPasses.tsx
  - tools/tillr-view/web/src/components/NewIdea.tsx
  - tools/tillr-view/web/src/models/api.ts
tags: [tillr, observatory, queue, intake, reference]
summary: "How tillr numbers and moves items in the queue (`place`, `tillr-item place`, and first-import placement), saves verbatim owner ideas and routes their one-turn design run (`tillr-item idea`, `idea_intake`, `from` outcomes), and exposes both writes over HTTP when `tillr-view serve` runs with `--write`."
---

# Observatory actions — queue placement, idea intake, and browser writes

> **Delivered copy.** This is `docs/reference/observatory-actions.md` from this repository at commit `e613f54fde` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/observatory-actions.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Verified 2026-11-20 against `351b4186c`. UNMEASURED: no live use of
the page's writes by the owner is recorded here; verification rests on the Go
and front-end suites.

The observatory is read-only by default and exposes two opt-in mutations when
`tillr-view serve` starts with `--write`: moving an item in the queue (`place`)
and saving raw owner words as a queued design item (`idea`). Both mutations live
in `tillr-item`; the HTTP server keeps its SQLite handle read-only and invokes
`tillr-item` as a serialized child process.

| Component | Lives in |
| :--- | :--- |
| Repository `[intake]` settings | `tools/tillr/settings/settings.go` (`Intake`) |
| `tillr-item place` and `idea` CLI faces | `tools/tillr-item/place.go`, `idea.go` |
| Sparse key math, `from` chaining, idea rows | `tools/tillr-item/item/place.go`, `order*.go`, `sync_from.go`, `idea.go` |
| Idea staging and candidate checks | `tools/tillr-drain/drain/idea.go` |
| Read-face place projection, idea file/commit reader, `--write` HTTP handlers | `tools/tillr-view/order.go`, `idea_words.go`, `idea_tree.go`, `serveplace.go`, `serveidea.go` |
| Items table move controls and header `New idea` form | `tools/tillr-view/web/src/components/ItemsPasses.tsx`, `NewIdea.tsx`, `src/models/api.ts` |

**Reads with:** [the tillr observatory reference](observatory-redesign.md)
(the linked-page UI and read-only server contract),
[tillr item states](tillr-item-states.md) (the seven projected states and
frozen criteria), [the queue persistence ruling](../design/queue-persistence-ruling.md)
and [queue persistence](tillr-queue-persistence.md) (document-backed
item blocks and private store state),
[the idea pipeline](../design/tillr-idea-pipeline.md) (lineage and downstream
shaping), and [the decision provenance review](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md)
(the 2026-11-20 owner ruling [`OQ-PR13`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR13)
and open reviews [`OQ-PR11`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR11)
and [`OQ-PR16`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR16)).

---

## Queue order and places

An item's **place** is its 1-based position across the active queue order. The
database stores a sparse lexicographic string in `items.rank` and never exposes
that token on CLI or HTTP read faces (`tillr-item show`, `tillr-view waiting`,
`GET /api/v1/snapshot`, `GET /api/v1/waiting`, `GET /api/v1/items/{id}`).

- **Membership (`item.InOrderState`):** Terminal items (`landed`, `closed`,
  `resolved-elsewhere`), parked items (`not-started`), and frozen legacy states
  (`abandoned`, `deleted`) are outside the order and report no place (`null` in
  JSON, `—` in tables). Active items (`in-queue`, `blocked`, `conflict`, and
  run-projected `running`) have a place.
- **Total order (`orderSQL` in `order_places.go`, `tools/tillr-view/order.go`):**
  Items sort by non-empty `items.rank` first, then `items.rank` ascending, then
  `item_id` ascending. An unkeyed item therefore counts after every keyed item,
  ordered by `item_id`, even before `sync` assigns it a stored key.
- **Stable numbers under filters:** Filtering the Items page by state, class, or
  reason keeps each row's true place across the full order, leaving numeric gaps
  where rows were hidden.
- **Compatibility on write (`item.Apply`):** `apply` accepts and ignores a
  `place` field on input payloads so `tillr-item show` output round-trips
  cleanly, and preserves an existing item's stored `rank` whenever `rank` is
  omitted.

### Moving an item

`tillr-item place` (`item.Place` in `tools/tillr-item/item/place.go`) moves one
in-order item relative to the current order without changing its state,
workflow, criteria, `blocked_by` edges, fence, or run history:

```sh
tillr-item place --store <db> --item <id> (--to first | --to last | --before <other> | --after <other>) [--by <actor>] [--via <surface>]
```

- **Neighbour-anchored destinations:** Every move names `first`, `last`, or an
  existing in-order neighbour (`before` or `after`), never a numeric index. A
  move issued from a stale browser table still lands beside the named neighbour
  without a version check.
- **Sparse key allocation (`betweenKeys`, `rekeyOrder`):** Moving between two
  neighbours computes a base-36 midpoint string (`0`–`9`, `a`–`z`), moving to
  `first` steps before the first key (extending with trailing `m` when the head
  is `0...0`), and moving to `last` increments or appends to the last key. When
  no key fits strictly between two neighbours or the generated key would exceed
  the length cap in [Current values](#current-values), `rekeyOrder` rewrites
  every in-order item's `rank` in the same `WriteTx` at uniform stride without
  changing any other item's relative order; only the moved item emits an
  `item.update` event.
- **No-op moves and receipts:** Placing an item `before` or `after` itself, or
  moving an item to the position it already holds, writes no row and appends no
  event (`EventSeq: 0`). Actual moves append one `item.update` event carrying
  `placed_by` and `placed_via` (defaulting to `owner` and `cli`) and return an
  `item.place` envelope (`item_id`, `place`, `in_order`, `placed_by`,
  `placed_via`, `event_seq`).
- **Refusals:** Missing items or neighbours fail with rule `unknown-item`;
  parked or terminal items or neighbours fail with rule `not-in-order`.

### First-import placement

`tillr item sync` never modifies a non-empty `items.rank` on an item the store
already holds. Before importing document blocks, a writing sync assigns keys to
any already-stored in-order items whose `rank` is empty (`keyMissingOrder`,
`placed_via: "sync"`, one transaction per item in `item_id` order so readers
observe identical places before and after). Opening or migrating a store never
assigns keys.

When sync imports a new in-order item — or unparks an existing `not-started`
item that still has no key — sync assigns an append position. New imports with
an existing source mark then move beside their source (`tools/tillr-item/item/sync.go`):

1. **Source mark (`placeFrom` in `sync_from.go`):** If the item's
   HTML block carries `from="<source-id>"` and `<source-id>` is in the store
   with a non-empty `rank` (even if `<source-id>` has already `landed`), sync
   places the item immediately after `<source-id>` — or after the last sibling
   already placed with `placed_from = "<source-id>"`, preserving block order
   when one turn emits multiple child items. `insertOrdered` records
   `"placed_from": "<source-id>"` in the `item.update` event payload.
2. **Append (`nextKeyAfter`):** Otherwise sync appends the item after the
   highest existing `items.rank` in roadmap/document walk order. Sync finishes
   all append placements before running `placeFrom`, so a source imported later
   in the same sync is never moved away from its children.

<a id="the-order-record"></a>

### Where hand-set order lives

Hand-set order lives only in `.tillr/state.db`, as ruled by
[`OQ-PR13`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR13) on 2026-11-20.
It survives a restart with the same database. A rebuilt store starts with the
planner's order, using the normal first-import placement rules rather than
restoring hand moves. Sync does not read a queue-order file, and reordering
writes no repository file or commit. Historical `drain.order-record` events
remain readable; the loop retires abandoned recording work without landing it.

---

## Idea intake and the design turn

`tillr-item idea` captures raw text from standard input, preserves an exact copy
under `<store-dir>/intake/`, and inserts a `design`-seat queue item in one place
without requiring the owner to invent an ID, title, fence, or Markdown block:

```sh
tillr-item idea --store <db> --root <repo-root> [--to first|last | --before <id> | --after <id>] [--by <actor>] [--via <surface>]
```

### Words files and preservation

- **Configuration (`[intake]` in `.tillr.toml`):** Saving an idea requires
  `intake.dir` (clean repository-relative directory where landed words files
  live) and a non-empty `intake.paths` list (fenced path prefixes the design
  turn may edit alongside its words file). In this repository's
  [`.tillr.toml`](https://github.com/mschulkind-oss/tillr/blob/main/.tillr.toml) at `351b4186c`, `[intake]` is unset, so
  `tillr-item idea` refuses with `intake-not-configured` after preserving the
  words file.
- **Input validation (`validateWords` in `tools/tillr-item/item/idea.go`):**
  Input bytes must be valid UTF-8 with no NUL byte (`words-not-text`), non-empty
  after whitespace trimming (`words-empty`), and at most `MaxIdeaWordsBytes`
  (see [Current values](#current-values); `words-too-large`).
- **Pre-store preservation (`ideaFace` in `tools/tillr-item/idea.go`):** Opening
  the store and acquiring `WriteTx` happen before any file is written, so an
  unopenable store or `store-busy` timeout writes nothing (`no idea was saved`).
  Once inside `WriteTx`, `PreserveIdea` writes the exact input bytes to
  `<store-dir>/intake/item-idea-<sha256[:12]>.txt` (`0600` inside `0700`
  `intake/`) and `fsync`s the file, `intake/`, and `<store-dir>` before
  mutating SQLite. If `[intake]` is unconfigured or a non-busy store failure
  interrupts the write (`PreserveUnassignedIdea` falls back to a random suffix
  when needed), the preserved file remains on disk and the error names its path.
- **Idempotent retry (`item.SaveIdea`):** Because the ID is derived from the
  SHA-256 of the exact input bytes (`item-idea-<12-hex>`), submitting identical
  bytes when that item already exists in `idea_intake` returns the existing
  item's ID and current `queuePosition` (`EventSeq: 0`, `Created: false`)
  without moving the item to the retry's destination.
- **Bootstrap gap visibility:** While the words exist only in
  `<store-dir>/intake/<id>.txt` and not as a regular file on trunk `HEAD`,
  `GET /api/v1/items/{id}` sets `data.idea.in_tree = false` and the Item page
  displays `not yet in the tree`. The `retire-epoch` deletion lock and overdue
  header badge proposed in `D-QP4` of
  [queue persistence](tillr-queue-persistence.md#why-its-this-way)
  are not built in `tools/tillr*`.

### The saved idea item

`SaveIdea` creates one row in `items` and one row in `idea_intake`:

- **Item fields:** `item_id = "item-idea-<12-hex>"`, `state = "in-queue"`,
  `seat = "design"`, `body_ref = ""`, `title` set to the whitespace-collapsed
  first non-empty line truncated to `maxIdeaTitleRunes` with `…`, and
  `allowed_paths` set to `[<intake.dir>/<id>.txt, ...intake.paths]`. Three fixed
  `mechanical` criteria (`c1`–`c3`) require the verbatim words file at
  `<intake.dir>/<id>.txt`, at least one new `from`-marked item block, and all
  edits staying inside the fence.
- **`idea_intake` row:** Stores `item_id` (primary key), `words_hash` (full
  hex SHA-256), `words_size`, `words_path` (`<intake.dir>/<id>.txt`), and
  `intake_path` (`<store-dir>/intake/<id>.txt`).
- **Placement and dispatch:** Default destination is `--to first`. Saved ideas
  are exempt from the document-fed no-block triage prerequisite
  (`document_prerequisite.go`) and are automatically
  express-eligible (`strings.HasPrefix(item.ItemID, "item-idea-")` in
  `tools/tillr-drain/drain/plan.go`), preferring an idle `express-*` lane when
  `[lanes] express` is configured and falling back to an ordinary lane otherwise.

### Staging, gate refusals, and outcomes

- **Staging (`LaneStager.ideaTask` in `tools/tillr-drain/drain/idea.go`, `stage.go`):**
  Before launching the `design`-seat run, the stager verifies the intake copy's
  size and SHA-256 against `idea_intake` (falling back to the worktree copy if
  already present), writes the verbatim bytes to `<worktree>/<words_path>`,
  replaces the brief's `TASK` placeholder with instructions quoting those exact
  words alongside the prose guide, and clears template expansion on `TITLE` so
  template tokens in the owner's first line are never expanded.
- **Two valid turn shapes (`D-OA6`):** If the idea is clear, the designer commits
  `<words_path>` plus one or more new item blocks carrying `prose="2"` and
  `from="<idea-id>"`. If the idea needs owner input, the designer commits
  `<words_path>`, a `stage: DESIGN` document with open `question` directives,
  and one `seat="design"` item block naming that document with
  `from="<idea-id>"` (which the planner then holds on `question-open`).
- **Pre-gate candidate checks (`checkIdeaCandidate` in `tools/tillr-drain/drain/idea.go`):**
  Before running the landing gate, the drain checks the candidate commit directly
  and refuses with a `needs a cause` / `fence` hold (`holdrules.go`) on three
  conditions:
  - `words-missing`: `<words_path>` is absent or not a regular file in the
    candidate tree.
  - `words-altered`: `<words_path>` differs in byte length or SHA-256 from
    `idea_intake`.
  - `idea-no-outcome`: the diff adds no unfenced Markdown item block carrying
    `from="<idea-id>"` whose `id` is absent from both `base_sha` documents and
    stored `items`.
- **Landing and lineage (`readIdeaWords` in `tools/tillr-view/idea_words.go`, `idea_tree.go`):**
  When the idea lands, post-landing sync imports the new `from`-marked blocks
  right after the idea's key via `placeFrom`, while the idea item itself moves
  to `landed` (leaving no item block of its own to retire). `readIdeaWords`
  checks `HEAD:<words_path>` via `git cat-file` — rejecting symlinks, submodules,
  and uncommitted worktree edits — verifies size and SHA-256 against
  `idea_intake`, and populates `data.idea.outcomes` by querying `events` for
  `item.update` payloads where `$.placed_from` equals the idea's ID (no
  `derived_from` table exists yet).

---

## Browser writes

`tillr-view serve` accepts writes only when started with `--write` (`serve.go`).
Its own `*store.Store` stays read-only (`ReadOnly: true`); `POST /api/v1/items/{id}/place`
(`serveplace.go`) and `POST /api/v1/ideas` (`serveidea.go`) acquire the in-process
`server.writeMu` lock and execute `tillr-item` (`--tillr-item`, an executable
beside `tillr-view` including `yolo-packs/tillr` cache layouts, or `PATH`) with
`--by owner --via web` under `placeCommandTimeout`.

> [!WARNING]
> **Write-mode network boundary (`D-OA8`):** `--write` adds no authentication.
> Browser requests carry `Origin` and are checked against loopback or `req.Host`,
> while requests without an `Origin` header (such as `curl`) are accepted so
> local scripts work against the default `127.0.0.1` listener. Never pass
> `--write` together with a non-loopback `--listen` address unless every host
> that can reach that socket is trusted to reorder items and queue design runs.

- **Routes and payloads:**
  - `POST /api/v1/items/{id}/place` accepts a JSON object with exactly one of
    `{"to":"first"}`, `{"to":"last"}`, `{"before":"<id>"}`, or
    `{"after":"<id>"}`.
  - `POST /api/v1/ideas` accepts `{"words":"<text>","place":<destination>}`,
    passing `words` on stdin to `tillr-item idea --root <repo-root>`.
  - Both return `200 OK` with the standard `{snapshot, data}` envelope (`item_id`,
    `place`, `in_order`, `placed_by`, `placed_via`, `event_seq`), reading a fresh
    `serverSnapshot` after the child process exits so `snapshot.event_head_seq`
    is at least `data.event_seq`.
- **Capabilities and store health:** Every response's `snapshot.writes_enabled`
  reflects `--write`. While the server's store has an open/schema problem
  (`s.problem != nil`), `storeProblem` answers `503` before either write handler
  runs.

### HTTP refusals

Every failed write returns `{snapshot, error: {code, message}}` (`apierror.go`,
`serveplace.go`, `serveidea.go`):

| Code | HTTP status | When returned |
| :--- | :--- | :--- |
| `read-only` | `405 Method Not Allowed` | Any `POST /api/v1/*` (other than `client-timing`) when `serve` started without `--write` |
| `origin-refused` | `403 Forbidden` | `Origin` header is present and neither loopback (`localhost`, `127.0.0.1`, `::1`) nor equal to `req.Host` |
| `content-type` | `415 Unsupported Media Type` | `Content-Type` media type is not `application/json` |
| `too-large` | `413 Request Entity Too Large` | Request body exceeds `placeBodyLimit` (`1 MiB`) |
| `bad-request` | `400 Bad Request` | Malformed JSON, unknown fields, trailing tokens, missing/multiple placement keys, invalid `to`, or invalid path `{id}` |
| `unknown-item` | `404 Not Found` | Moved item or named `before`/`after` neighbour does not exist |
| `not-in-order` | `409 Conflict` | Moved item or named neighbour is parked (`not-started`) or terminal |
| `words-empty` | `422 Unprocessable Entity` | `words` is empty or whitespace-only |
| `words-not-text` | `422 Unprocessable Entity` | `words` is invalid UTF-8 or contains a NUL byte |
| `words-too-large` | `422 Unprocessable Entity` | `words` exceeds `MaxIdeaWordsBytes` (`64 KiB`) |
| `intake-not-configured` | `409 Conflict` | `[intake]` (`dir` / `paths`) is unset in `.tillr.toml`; preserved words path is named in `message` |
| `intake-store-failed` | `500 Internal Server Error` | Store write failed after words were preserved under `<store-dir>/intake/`; preserved path is named |
| `store-busy` | `503 Service Unavailable` | `tillr-item` could not acquire the SQLite writer within its busy timeout, or `placeCommandTimeout` elapsed |
| `writer-unavailable` | `503 Service Unavailable` | `tillr-item` binary could not be found or started |

### Browser controls

- **Items page (`ItemsPasses.tsx`):** Displays `#` (`place`), renders an `idea`
  chip plus title on idea rows (`item.kind === "idea"`), and prints `This server
  is read-only.` when `writes_enabled` is false. When writes are on, each movable
  row renders a `Move to top` button
  (`{"to":"first"}`); when sorted by `place` (`sort=place`), rows also offer a
  `↕` drag handle, a table-footer drop zone (`Drop here to move below the last
  row`, sending `after` the last movable item), and `J` / `K` keyboard moves
  one visible row down or up (while lowercase `j` / `k` move focus). `moveLock`
  allows one in-flight move at a time, applies the new order optimistically,
  restores the prior order with `Move refused: ...` on a failed `POST`, keeps
  the new order with `Order saved, but refresh failed: ...` if only the
  post-write refresh fails, and surfaces an `order changed ↑` banner when
  another writer reorders rows while the list is scroll- or focus-pinned.
- **Header `New idea` form (`ObservatoryShell.tsx`, `NewIdea.tsx`):** Rendered
  only when `snapshot.writes_enabled` is true; opened by the `New idea` button
  or `n` outside text inputs. Offers one `Idea words` `<textarea>`, placement
  radios (`Top`, `Bottom`, `After an item` over in-order items), and `Save idea`
  (`Ctrl+Enter` / `Cmd+Enter`). Draft state persists in `localStorage` under
  `tillr.idea:<store_path>` (warning visibly if `localStorage` throws), survives
  closing the form or a refused save, clears on confirm only if `localStorage`
  still holds the submitted draft (so another tab's newer edit is not erased),
  and navigates to `/item/<item_id>` on success.
- **Item page (`ItemRunPages.tsx`):** When `data.idea` is present, renders the
  verbatim `<pre className="idea-words">` block with `in the tree` or `not yet
  in the tree` beside `words_path`, followed by `Items from this idea`
  (`data.idea.outcomes`).

---

## Current values

Verified at `351b4186c`. The prose above explains how these settings and limits
behave; this table is the single place their literal values are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Sparse key alphabet and midpoint base | Base-36 (`0`–`9`, `a`–`z`), initial key `"m"` | `digits`, `betweenKeys` in `tools/tillr-item/item/place.go` |
| Max sparse key length before respacing | `64` characters | `betweenKeys` in `tools/tillr-item/item/place.go` |
| Respacing stride and width | `36^8` step, `8`-character base-36 keys | `rekeyOrder` in `tools/tillr-item/item/place.go` |
| Default placement receipt | `placed_by = "owner"`, `placed_via = "cli"` (`"web"` from HTTP) | `Place` in `tools/tillr-item/item/place.go`; `serveplace.go`, `serveidea.go` |
| Idea ID format | `"item-idea-" + sha256(words)[:12]` | `IdeaID` in `tools/tillr-item/item/idea.go` |
| Max idea words size | `65536` bytes (`64 KiB`) | `MaxIdeaWordsBytes` in `tools/tillr-item/item/idea.go` |
| Max derived idea title length | `120` runes (truncated at `119` + `"…"`) | `maxIdeaTitleRunes` in `tools/tillr-item/item/idea.go` |
| Intake directory and file modes | `<store-dir>/intake` `0700`, `<id>.txt` `0600` | `writePreservedFile` in `tools/tillr-item/item/idea.go` |
| Intake settings | `[intake] dir` (repo-relative), `paths` (non-empty path list) | `Intake` in `tools/tillr/settings/settings.go` |
| Server write gate and writer flags | `--write` (default `false`), `--tillr-item` (default `""`) | `cmdServe` in `tools/tillr-view/serve.go` |
| HTTP write body cap | `1048576` bytes (`1 MiB`) | `placeBodyLimit` in `tools/tillr-view/serveplace.go` |
| Child `tillr-item` execution timeout | `10s` | `placeCommandTimeout` in `tools/tillr-view/serveplace.go` |
| Browser draft `localStorage` key | `tillr.idea:<snapshot.store_path>` | `NewIdea` in `tools/tillr-view/web/src/components/NewIdea.tsx` |

---

## Why it's this way

Each row preserves a ruling (`D-OA1`–`D-OA8`) that a maintainer reading only the
normative behaviour above might otherwise undo, together with its provenance
status.

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| `D-OA1` | Expose 1-based places on every face while keeping hidden sparse keys in `items.rank` | Storing dense integers rewrites every row below a move and appends `N` update events per drag; sparse keys keep a normal move to one row and one event. |
| `D-OA2` | Anchor every move to `first`, `last`, or a named `before`/`after` neighbour, never a place number | "Move to place 4" lands in the wrong spot once another writer moves a row above it; naming the neighbour stays exact from a stale or filtered view without optimistic version checks. |
| `D-OA3` | Keep legacy `items.rank` strings as opaque keys, key unranked in-order items once on sync, and reserve no prefix range | Rewriting existing keys on upgrade creates needless churn, and reserved `00..` prefixes invert priority when the owner moves another item above an idea. |
| `D-OA4` | **Overturned 2026-11-20 by owner ruling [`OQ-PR13`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR13) (*"database only"*).** Originally persisted hand order across rebuilds via an opt-in tracked text file (`order.record`) written only by an idle gated loop landing | Originally rejected `rank` attributes in Markdown item blocks (`D-RS1`) and server-made git commits outside `tillr-land`; the owner subsequently ruled that dragged order belongs exclusively in `.tillr/state.db` with no repository file or commit. |
| `D-OA5` | Store raw owner words as hash-verified `.txt` files — first in `<store-dir>/intake/`, then in `<intake.dir>/` on landing | Saving `.md` subjects unedited owner dumps to Markdown lint/check refusals, and keeping words only in SQLite loses them on a store rebuild. |
| `D-OA6` | Every idea's design turn lands `<words_path>` plus new `from`-marked item blocks (or one `stage: DESIGN` doc with open questions and one `from`-marked design item), rather than retitling the idea item *(designer's decision; open for owner review as [`OQ-PR11`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR11))* | An item in tillr lands once and retires its block; emitting child blocks carrying `from` lets one idea fan out into multiple items at the idea's place while keeping ambiguous ideas visible on `question-open` after committing the words into git. |
| `D-OA7` | `tillr-view serve --write` keeps its SQLite handle read-only and executes `tillr-item` under a mutex for both writes | Having the HTTP server open a SQLite writer creates a second item-mutation code path with duplicate validation, event-receipt, and WAL lock-contention rules. |
| `D-OA8` | Gate browser writes behind `--write` and `Origin` validation with no login token, partially reversing `D-OBS2` *(designer's decision; open for owner review as [`OQ-PR16`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR16))* | Both writes are non-destructive and reversible on a loopback-bound operator server, whereas adding auth violates the observatory's zero-login operating model. |
