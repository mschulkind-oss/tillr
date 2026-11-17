# Definition files

A **definition** is the data that turns tillr's core into one use: its item states,
allowed state moves, dispatch conditions, completion rule, and optional hook program.
The term and the name **git-landing** are placeholders coined in
[the direction document](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-core-and-git-landing.md#the-words-this-document-uses);
the owner has not chosen the final names.

This package reads and validates that data. **No utility consumes it yet.** It does
not open a store, dispatch work, check a prerequisite, run a program, or change the
existing queue's behavior. The consumer column below names the intended reader
when the later integration work lands, not something this change has wired in.

## Reading a file

`Read(io.Reader)` accepts exactly one JSON object; `Load(path)` opens a file and
uses the same reader. Both return a validated `Definition` or an error, never a
partial definition. `Definition.Validate()` checks an already constructed value;
it does not apply defaults. `GitLanding()` reads the embedded
[landing.json](./landing.json) afresh, so mutating one result cannot change later reads.
The sole Go spelling of the placeholder name is `GitLandingName`.

The reader rejects unknown keys, including keys inside transitions and hooks;
key spelling is case-sensitive, and duplicate object keys are refused rather than
merged. It rejects wrong JSON types, unsupported versions, duplicate or empty state names,
and state references that are not declared. An error names the offending key.
The package recognizes version 1 only.

## Keys, types, defaults, and readers

A required key has no default. Optional arrays default to no entries (a nil Go
slice); null optional arrays also mean no entries. A **finished state** ends an
item and satisfies a dependent's `blocked_by`
edge; this does not inherently forbid leaving it. A **prerequisite** is a named
condition checked before dispatch. A **hook program** is one executable called for
judgments the core cannot make. These terms come from
[the build plan's vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-definitions-plan.md#the-words-this-plan-uses).

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

Its transitions capture the item utility at the staged base `bfb02cb3a`:
raw apply permits every distinct move among the five nonclosed writable states.
Close allows the three unfinished states to enter `closed`; reopen restores one
of those three saved prior states. An unchanged state is not a move, so the file
has no self-transitions. The file cannot express command-specific requirements
such as a close reason, the saved reopen state, or block metadata.

This snapshot therefore includes moves out of `landed` and `resolved-elsewhere`.
That permissiveness is the defect addressed by
[the separate terminal-state item](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-definitions-plan.md#prevent-raw-writes-from-reopening-a-landed-or-resolved-item),
not an endorsement of reopening landed work. No consumer is wired to this file,
and adopting a stricter lifecycle must update its snapshot and literal-value test
together. Legacy read-only states (`abandoned`, `deleted`, `in-progress`) are not
writable vocabulary in the definition.
