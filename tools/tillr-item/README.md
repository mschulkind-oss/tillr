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
repository's tracked documents — the only intake from documents. A fence entry
under `.forks/lanes/<lane>/<fork>/` names a checkout staging materializes for the
assigned lane, so `sync` does not warn that it is missing even on a fresh repository.
Other absent paths still receive the ordinary `path-missing` warning.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md).

## State invariants

- **An item's state and its criteria's states are different writes.** An item's state says
  where the work stands; a criterion's state says what a judge found. One face moving both
  would make a criterion's freeze depend on a caller's care.
- **Landed and resolved items stay finished.** Once an item is `landed` or
  `resolved-elsewhere`, raw `state` writes cannot move it elsewhere, and `apply`
  cannot change its stored definition, including its title, criteria, blockers or
  fence. These changes are refused as `state-terminal`, naming the item and its
  current state. The existing `criterion-state` and `criterion-frozen` rules are
  checked first and keep their names even when a record changes other fields too.
  Writing the same state or applying the same definition succeeds without changing
  the item or appending an event. Verdicts followed by the move to `landed` still
  finish the item; the explicit close and reopen commands are unchanged. Seed
  retains its existing authority to change rank alone, preserving the finished
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
- **A conflict is recorded once per transition.** A transition is a changed record of
  the same conflict: its routing, answer, report, origin run, document, or blocked
  criteria may change. A record equal in every field to that conflict's last record
  is not written and does not change the item's disposition. A longer report from a
  second failed run is new evidence, even when the status and design run stay the same.
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
  [the queue-persistence ruling's Section 1](../../docs/design/queue-persistence-ruling.md#1-shared-item-block-shape).
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
  rather than leaving half a document applied. A rank-only seed changes the rank alone,
  apart from `updated_at` and `event_seq` bookkeeping. It preserves every other stored
  field, including `workflow` (the item's chosen sequence of work steps), criteria and
  dependency edges. Items with a workflow do not need to be skipped when ranking.
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
nothing. The sync report also carries `document_fed`: true when at least one
stored item, in any state, has a document block. The drain uses this same comparison
for its [document-fed workflow prerequisite](../tillr-drain/README.md#document-fed-workflow-prerequisite);
sync itself neither dispatches triage nor changes the queue’s readiness rules.

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
[the judge output contract](../../docs/design/tillr-judge-and-lifecycle.md#21-topology-relative-to-gate-and-land-d-jl1).
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
steps in [the judge build order](../../docs/design/tillr-judge-and-lifecycle.md#8-build-order).

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

The [designer-session design](../../docs/design/tillr-designer-sessions.md#the-words-this-design-uses)
defines batches and desks. Automatic quiet-period intake is a later slice; this command is
the hand-driven path.

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
