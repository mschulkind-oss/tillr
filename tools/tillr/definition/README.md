# Definition files

A **definition** is the data that turns tillr's core into one use: its item states,
allowed state moves, dispatch conditions, completion rule, and optional hook program.
The term and the name **git-landing** are placeholders coined in
[the direction document](../../../docs/design/tillr-core-and-git-landing.md#the-words-this-document-uses);
the owner has not chosen the final names.

This package reads and validates that data and answers an open store's declaration.
The store utility records and reports it. Item state, apply, close, reopen, and
sync read its lifecycle vocabulary; drain integration remains separate work.
This package does not open a store, dispatch work, check a prerequisite, or run a
program. The consumer column below includes both current and intended readers.

## Reading a file

`Read(io.Reader)` accepts exactly one JSON object; `Load(path)` opens a file and
uses the same reader. Both return a validated `Definition` or an error, never a
partial definition. `Definition.Validate()` checks an already constructed value;
it does not apply defaults. `GitLanding()` reads the embedded
[landing.json](./landing.json) afresh, so mutating one result cannot change later reads.
The sole Go spelling of the placeholder name is `GitLandingName`.

The reader rejects unknown keys, including keys inside transitions and hooks;
key spelling is case-sensitive, and duplicate object keys are refused rather than
merged. It rejects invalid UTF-8 instead of accepting a string whose bytes would
change when stored. It also rejects wrong JSON types, unsupported versions, duplicate
or empty state names, and state references that are not declared. An error names the
offending key or invalid encoding.
The package recognizes version 1 only.

## A store's definition

`ForStore(ctx, store)` is the shared reader for an already open store. It returns a
`Record`: name, version, exact source content as a string, lowercase SHA-256 of
that content, and a `Default` flag. Its `Parsed` field carries the validated
`Definition` for Go consumers and is omitted from JSON. The store utility prints
this same record as its `store.definition` payload.

`ForReader(reader)` reads the same declaration inside a caller's existing transaction.
Item validation and writes therefore use one consistent declaration even when another
writer replaces it. A nil reader uses the embedded default for a not-yet-created store.
`HasState`, `IsFinished`, and `Allows` answer vocabulary membership and listed moves;
a same-state write is not a move and need not have a listed self-transition.

The declaration is one JSON record at key `store.definition` in the existing `meta`
table. An absent row means the embedded Git-landing definition (`Default: true`);
there is no new table or schema version. Reading a legacy store requires no migration.
An invalid declaration or metadata that disagrees with its content is an error,
never a reason to use the default.

`Set(ctx, store, content)` validates bytes before starting a write transaction, then
checks all current item states, replaces the declaration, and appends
`store.definition.set` atomically. The event names `old_sha256` and `new_sha256`;
the old hash names the embedded content when there was no declaration.
`OccupiedStatesError` names every item and current state the replacement would
drop. A store predating the item table has no items to check. No file, Git, or
process work runs under SQLite's write lock. See the
[store utility's usage and refusal behavior](../../tillr-store/README.md#the-stores-definition).

## Keys, types, defaults, and readers

A required key has no default. Optional arrays default to no entries (a nil Go
slice); null optional arrays also mean no entries. A **finished state** ends an
item and satisfies a dependent's `blocked_by`
edge; this does not inherently forbid leaving it. A **prerequisite** is a named
condition checked before dispatch. A **hook program** is one executable called for
judgments the core cannot make. These terms come from
[the build plan's vocabulary](../../../docs/design/tillr-definitions-plan.md#the-words-this-plan-uses).

| Key | JSON type | Default | Meaning and intended reader |
| :--- | :--- | :--- | :--- |
| `definition` | String | Required | Nonblank name; store declaration and definition selection. |
| `version` | Integer | Required | Must be 1; this package's format validator. |
| `states` | Array of strings | Required, nonempty | Unique, nonblank writable state names; item validation and lifecycle commands. |
| `ready` | String | Required | Declared state the planner dispatches from; reopening restores an item's saved prior state rather than always writing this state. |
| `blocked` | String | Required | Declared state the block command writes; planner checks the blocked disposition. |
| `closed` | String | Required | Declared state the close command writes and the reopen command leaves. |
| `finished` | Array of strings | No entries | Declared states that end an item; planner dependency checks and item completion. |
| `transitions` | Array of objects | No entries | Allowed moves; lifecycle validation. Each object has required `from` and `to` strings naming declared states, with no defaults. Moves out of finished states must be explicit. |
| `prerequisites` | Array of strings | No entries | Names from the closed list below; planner dispatch checks. |
| `completes` | String | Required | `landing` or `hook`; loop completion routing. `hook` requires a hook program. |
| `retention` | String | `keep` | Only `keep` is supported; retained store events and run artifacts. No cleanup is implemented. |
| `brief_template` | String | Empty string | Template for staging's lane brief; empty means no template is supplied. This package does not resolve a file or render it. |
| `hooks` | Object or null | Null (no program) | Hook dispatch. An object has required `argv` and `names` arrays of strings, with no defaults. `argv` is one program's argument vector, starting with a nonblank executable; `names` is a nonempty list of nonblank hook names it answers. |

A hook's argument vector goes directly to a process, not a shell. Arguments after
the executable may be empty strings. Hook names are declarations, not a closed
vocabulary in this package; matching them to a program's capabilities and defining
its records and exit codes belongs to the later hook-contract integration.

### Closed prerequisite vocabulary

| Name | Condition the planner will check |
| :--- | :--- |
| `fence` | The item names paths it is allowed to change, not an unrestricted repository root. |
| `design-document` | The item's referenced design document exists. |
| `design-questions` | The question index is readable and the document has no unanswered question. |
| `owner-clean` | The owner checkout has no dirty paths that prevent landing the candidate. |

An unknown name is refused as `prerequisites`, not silently skipped. These names
select checks only; the package does not perform filesystem or Git operations.

## The embedded Git-landing snapshot

The embedded file states the six writable states, `in-queue` as ready,
`blocked` and `closed` as their command roles, and `landed`, `resolved-elsewhere`,
and `closed` as finished. It completes through landing, keeps artifacts, supplies
no brief template, and names no hook program.

Its transitions preserve Git-landing's finished-item policy: neither `landed` nor
`resolved-elsewhere` has an outgoing move. Close allows the three unfinished states
to enter `closed`; reopen restores one of those three saved prior states. An unchanged
state is not a move, so the file has no self-transitions. Command-specific requirements
such as a close reason, the saved reopen state, and block metadata are enforced by
the item utility in addition to the listed moves.

Other definitions may list outgoing moves from selected finished states. The item
utility requires a reason and actor and records them in the event when such a move
is made. Legacy read-only states (`abandoned`, `deleted`, `in-progress`) are not
writable vocabulary in the definition.
