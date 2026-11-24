# Definition files

A **definition** is the data that turns tillr's core into one use: its item states,
allowed state moves, dispatch conditions, completion rule, and optional hook program.
The owner's word for this layer is **charter**
([OQ-VO4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#OQ-VO4)), and the first one is
**git-landing** ([OQ-VO5](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#OQ-VO5)); the code and
its flags say "definition", as
[the direction document](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-core-and-git-landing.md#the-words-this-document-uses)
does.

This package reads and validates that data and answers an open store's declaration.
The store utility records and reports it. Item state, apply, close, reopen, and
sync read its lifecycle vocabulary; drain reads it for planning and hook startup
validation.
This package does not open a store, dispatch work, check a prerequisite, or run a
program. The consumer column below includes both current and intended readers.

## Reading a file

`Read(io.Reader)` accepts exactly one JSON object; `Load(path)` opens a file and
uses the same reader. Both return a validated `Definition` or an error, never a
partial definition. `Definition.Validate()` checks an already constructed value;
it does not apply defaults. `GitLanding()` reads the embedded
[landing.json](./landing.json) afresh, so mutating one result cannot change later reads.
The sole Go spelling of that name is `GitLandingName`.

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
[store utility's usage and refusal behavior](https://github.com/mschulkind-oss/tillr/blob/main/tillr-store/README.md#the-stores-definition).

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
| `observability` | Object or null | Null (no bindings) | Optional charter watch bindings. Allowed keys are `progress_event` (nonblank trimmed string naming the forward-throughput event kind) and `rule_pack` (clean relative `.toml` path under the watch rules directory). |

A hook's argument vector goes directly to a process, not a shell. Arguments after
the executable may be empty strings. Hook names are declarations, not a closed
vocabulary in this package. The drain checks them against the program's capabilities
using the [hook program contract](#hook-program-contract).

## Hook program contract

A hook is called as the exact `hooks.argv` vector followed by its name and the
flags below. No shell parses that vector; spaces and empty arguments are retained.
The process runs outside every SQLite write transaction. Diagnostics go to stderr;
stdout is reserved for the hook's answer. JSON answers are single objects, not
log lines mixed with JSON.

The drain's `loop` and `loop --check` read the store's declaration without migration,
close that reader, and run `describe` before any pass starts. Only names in
`hooks.names` are checked and selected. An explicit step flag (even an empty value)
wins over the definition. With a hook program, an unlisted step defaults to empty,
not the Git-landing step default; an empty gate, land, or verdict cannot judge a
candidate, an empty replay cannot acquit it, an empty rolling does no rolling work,
and an empty fixer gate leaves fixers on the ordinary gate. With no hook program,
the existing command-line requirements and defaults remain unchanged.

### Describe

`<argv> describe` takes no flags and prints this capability record (example):

```json
{"hooks":{"gate":["--item","--lane","--run","--worktree","--base","--head","--root"],"replay":["--only"]}}
```

The `hooks` object maps each answered name to its accepted flag spellings, including
leading `--`. It may advertise names the definition does not select; they are not
called. Every selected name must appear, and every flag in its contract below must
be accepted, including flags used only in optional branches. This also checks
selected names whose step is overridden on the command line. Unknown names may be
advertised for other consumers, but the drain assigns them no flags or calls.
Exit 0 means a valid description; any other exit, malformed record, missing selected
hook, or missing flag refuses loop startup with exit 2. Missing-hook and missing-flag
diagnostics name the hook. Describe does not judge a candidate or mutate the store.
Definition hooks need not implement the `--help` / `--check-argv` negotiation;
explicit command-line step programs use that startup check.

### Candidate steps

**Candidate flags** means `--item`, `--lane`, `--run`, `--worktree`, `--base`,
`--head`, and `--root`: the item, lane, run, working directory, base commit, candidate
commit, and owning repository. The drain hands these to every candidate step.
The record envelope mentioned below is the suite's
[versioned record format](../record/envelope.go). Gate artifacts are written under the
run's loop directory, not returned as mixed stdout logging.

| Hook | Flags passed | Answer record and exit meaning |
| :--- | :--- | :--- |
| `gate` | Candidate flags | No stdout record required. The gate writes its `gate.verdict` artifact and measured check spans. Exit 0 passes, 1 fails, 2 refuses arguments (configuration fault), 6 is a stalled gate, and any other exit could not judge. |
| `land` | Candidate flags; optional `--observed-trunk`, `--base-ref`, `--message-from` | Prints a `land.landing` envelope with payload `result`, `reason`, `trunk_after`, and optional `conflict_paths`, `lock_observation`, `red_cap`. Exit 0 lands; 1 refuses or conflicts (the record distinguishes them); 2 refuses arguments; other exits fail the step. An empty stdout is read as an exit-only answer. |
| `verdict` | Candidate flags; `--store`, `--verdict` (`pass` or `fail`) | Records criterion verdicts in the named store; stdout is not consumed. Exit 0 records the requested verdict, 2 refuses arguments, any other exit fails the step. |
| `fixer-gate` | Candidate flags; `--only` with one check named in the trunk fixer's criterion | No stdout record consumed. Runs once per named check in the candidate's worktree, after `gate` has accepted the change. Exit 0 means that check passes, 1 means it fails, 77 means the program dropped the check (neither a pass nor a failure), and any other exit could not judge. |

`--observed-trunk` is the loop's last observed trunk tip; `--base-ref` names a
retained base reference when present; `--message-from` names the worker commit
whose message is kept when present. These optional flags must still be advertised.
Gate receives `TILLR_GATE_SPANS_OUT` and `TILLR_FAST_VERDICT_OUT`;
land receives `TILLR_LAND_OBSERVE=1`. The drain keeps these existing artifact and
observation conventions. The gate may also write failing-check details used for
base replay. See [the drain reference](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md).

### Revision checks

| Hook | Flags passed | Answer record and exit meaning |
| :--- | :--- | :--- |
| `replay` | `--only` with one failing check identifier | No stdout record consumed. Runs once per check in a detached worktree at the revision being judged. Exit 0 means that check passes, 1 means red, any other exit means unknown (never evidence for acquittal). |
| `rolling` | Either `--all` on the first run or `--since` with the proved watermark | No stdout record consumed. Runs in a detached worktree at the trunk tip and writes check spans to `TILLR_GATE_SPANS_OUT`. Exit 0 proves green; 1 proves red only if spans name failing checks; 6 reports stalled checks; all other exits are unknown. Unknown advances no watermark and files no fixer. |

For definition hooks the hook name selects the operation: replay receives only
`--only`, and rolling receives only its range flag. Revision checks and fixer gates run
with `PI_FORKS_ROOT` set to the repository the loop runs in, so a program in a detached
worktree can name the owning repository.

### Completion and detail hooks

These contracts are reserved for the
[completion integration](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-definitions-plan.md#add-completion-through-the-definitions-hook-for-items-that-never-land).
This change validates selected hooks at startup but does not yet call them.

| Hook | Flags | Answer record and exit meaning |
| :--- | :--- | :--- |
| `complete-item` | `--item`, `--run`, `--store`, `--root` | Prints one JSON object with `state`, `reason`, and optional `resolves_via` pointer. It first records criterion verdicts in the store. Exit 0 requests the named finished state; 1 leaves the item unfinished; any other exit holds it as a hook fault. The core must validate the state and recorded verdicts before moving the item, with actor `definition:<name>`. |
| `after-completion` | `--item`, `--store`, `--root` | No stdout record required. Exit 0 completes follow-up work; 1 is a negative answer; any other exit is a hook fault. It does not undo the already completed item. Invocation and negative-answer routing are future integration work. |
| `detail` | `--body-ref`, `--root` | Prints one JSON record envelope of kind `item.detail`, with a definition-specific payload for the item's referenced details. Exit 0 supplies details; 1 means no detail is available; any other exit is a hook fault. Display rendering is future integration work. |

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

Its transitions preserve Git-landing's finished-item policy with one exception:
`resolved-elsewhere` has no outgoing move, and `landed` has exactly one, back to
`in-queue`. That move is how an automatically reverted landing's item returns to its
worker (graph design [`OQ-GP1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md#OQ-GP1), ruled 2026-11-17): the full suite proved the landing broke
the trunk, the drain reverted it, and the item's work is no longer on the trunk. Like
any move out of a finished state it needs a reason and an actor. Close allows the three unfinished states
to enter `closed`; reopen restores one of those three saved prior states. An unchanged
state is not a move, so the file has no self-transitions. Command-specific requirements
such as a close reason, the saved reopen state, and block metadata are enforced by
the item utility in addition to the listed moves.

Other definitions may list outgoing moves from selected finished states. The item
utility requires a reason and actor and records them in the event when such a move
is made. Legacy read-only states (`abandoned`, `deleted`, `in-progress`) are not
writable vocabulary in the definition.
