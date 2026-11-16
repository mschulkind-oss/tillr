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
delegation runs under, and the verdicts a judge records. It also writes a whole seed
document's items in one transaction, and `sync` materializes the item blocks of the
repository's tracked documents — the only intake from documents.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## State invariants

- **An item's state and its criteria's states are different writes.** An item's state says
  where the work stands; a criterion's state says what a judge found. One face moving both
  would make a criterion's freeze depend on a caller's care.
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
- **A decision is superseded, never edited.** The superseded record stays and the new one
  names it, so the ledger reads in order.
- **A seat is a role, not a model.** An item names at most one of `design`, `build`,
  `review` or `scout`; the backend policy maps that seat to a backend, and the backend names
  the model it runs. Swapping which model fills a seat, or moving the seat to another
  backend, is a policy edit that touches no item.
- **Sync is the only intake from documents, and it never destroys a local fact.** `sync`
  reads every `<!-- tillr: item ... -->` directive that starts a line outside a fenced block
  in the Markdown git tracks under `docs/`, and writes the items they declare through the
  same record→row mapping `apply` uses. The directive grammar and the field mapping are
  [the queue-persistence ruling's Section 1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/queue-persistence-ruling.md#1-shared-item-block-shape).
  A new item is `in-queue`, or `not-started` when a roadmap's `## Icebox` or
  `## Exploration` section links to its document; an item the store holds keeps its state,
  rank, runs and criterion states. A changed block updates the item's definition while no
  criterion carries a state, and is reported `definition-frozen` once one does. A second run
  over unchanged documents writes nothing and appends no event.
- **A document with a finding writes nothing; the others are written.** A missing or
  malformed id, a duplicate id, no paths, no `c1`, an unknown attribute or seat, or a
  `blocked_by` naming nothing a block declares or the store holds is reported with its file
  and heading, and the face exits `1` after the other documents are applied. A path that
  does not exist yet is a warning, since the slice may create it.
- **A seed is one transaction.** An item that cannot be written takes the batch with it,
  rather than leaving half a document applied.
- **A mail delivery is acknowledged once, by a named session.** A second acknowledgement is
  a refusal and not a second row.

## Standalone invocation

```sh
tillr-item show
tillr-item decision list
tillr-item sync
tillr item close --store .tillr/state.db --item item-042 --reason "not doing this" --by owner
tillr item reopen --store .tillr/state.db --item item-042 --reason "wanted again" --by owner
```

`show` and the read verbs are what an external reviewer needs: they answer from one store,
name what is not there rather than guessing, and write nothing. `sync --dry-run` is the same
for the documents: it prints what a sync would create, update and refuse, with the two
residue counters — `unshared_open`, the open store items no block declares, and
`stale_doc_blocks`, the blocks whose item this store already holds as `landed` — and writes
nothing.

## What it refuses

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
