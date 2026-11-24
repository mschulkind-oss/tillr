---
title: "Drain Seat Profiles and One-Command Switching"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags:
  - tillr
  - drain
  - backend-policy
  - design
summary: "A seat profile is a named, complete backend policy for the drain, tracked one file per name, listed and switched with one command and no restart, with a hold that keeps a defined profile from being selected. Nothing here is built: the drain takes a policy file path at startup. Three questions on the held Claude profile and on which policy files carry forward are open."
next: "The owner rules OQ-DSP1 through OQ-DSP3. The claude-sub profile stays held until the Claude backend is proven and the owner asks to switch."
depends-on:
  - ./tillr-live-drain-config.md
  - ../reference/tillr-claude-backend.md
vantage:
  status-chip: true
---

# Drain Seat Profiles and One-Command Switching

**Status:** 2026-11-24. Proposed; nothing here is built. `config/tillr/profiles/` and `config/tillr/seats.json` do not exist, `tillr-drain` has no `profile` face, and `rg -n 'seat_profile' tools` finds nothing. The drain takes one `--policy <path>` flag at startup and reloads that file's contents at each pass (`refreshPolicy`).

> **In short.** Give each complete assignment of seats to backends a name, keep one tracked file per name, and let one command switch a running drain between them. A profile can be defined and held, so it is checked on every test run but cannot be selected.

**Why it matters.** A path on a command line names nothing an operator can read back, a defined-but-unproven assignment can be selected by accident, and every policy file repeats the same seat list.

**The shape.** A seat catalog, a directory of profile files, one key in the drain's runtime configuration table, and a `tillr drain profile` face.

**Cost.** One loader change, one face, and one field on the policy snapshot the read faces already show. It forecloses a tracked policy that names no model.

**Start at [§2](#2-boundary-and-vocabulary-what-a-seat-profile-is-and-is-not)** for what a profile is, then [§4](#4-listing-showing-and-selecting-profiles-inside-live-drain-config) for the command.

**Needs your ruling:** [OQ-DSP1](#OQ-DSP1), [OQ-DSP2](#OQ-DSP2), [OQ-DSP3](#OQ-DSP3).

**Reads with:** [tillr-live-drain-config.md](./tillr-live-drain-config.md) (the runtime configuration table, its precedence and the reload at a pass boundary, which this design uses), [tillr-claude-backend.md](../reference/tillr-claude-backend.md) (the `run-claude` adapter and the subscription's quota windows), and [tillr-convergence.md](./tillr-convergence.md) (whose [OQ-TC2](./tillr-convergence.md#OQ-TC2) this design revises). No implementation plan exists.

## 1. Context and Goal

A running `tillr-drain` loop takes one `--policy <path>` flag naming one of seven JSON files under `config/tillr/`: `backend-policy.json` and its `.bedrock`, `.claude`, `.claude-copilot`, `.copilot`, `.gemini` and `.mixed` variants. Each file maps the drain's eleven workflow seats (`design`, `plan`, `build`, `edit`, `test-write`, `docs`, `review`, `verify`, `scout`, `classify` and `test-run`) to backend rows, and a backend row names an adapter program, a model route and an effort level. The file also holds the default backend and the escalation order.

Three things make a raw path the wrong surface for an operator:

1. **Paths, not names.** Nothing in `tillr status`, the observatory header or the loop's events names the active assignment. `tillr drain policy` prints a hash and a file path, and pointing a running drain at a different file means restarting the loop with a new flag.
2. **No way to define a profile and hold it.** A policy file on disk carries neither a name nor anything that stops it from being selected before the backend it depends on is proven.
3. **The seat list is repeated.** Every policy file repeats the same `read_only_seats` and `seat_purposes` blocks so that worker briefs can render the repository's seat list. Adding a seat means the same edit in seven files.

This design settles what a named seat profile is, where profiles and the shared seat definitions live, how profiles are listed, shown and switched inside [the live drain configuration](./tillr-live-drain-config.md) without restarting the drain, what happens to runs and sessions in flight, how the active profile is shown, the first two profiles, and how named profiles revise [OQ-TC2](./tillr-convergence.md#OQ-TC2).

## 2. Boundary and Vocabulary: What a Seat Profile Is (and Is Not)

A **seat profile** *(coined here)* is a named, complete **backend policy** for `tillr-drain`: a tracked file `config/tillr/profiles/<name>.json` that binds a name such as `copilot-gemini` to a full map from every seat the repository declares, plus the default backend for an item with no seat, to backend rows. A backend row names the adapter, the model route, the effort level, the fallback list, optional proportional shares, the escalation order, the quota probe settings and the baseline concurrency caps.

Three boundaries separate it from its neighbors:

- **Not a model set inside one harness.** A harness may define sets of models for the sub-agents of one interactive session. Those sets never govern what `tillr-drain` dispatches, and cannot move a seat between `run-pi` and `run-claude`.
- **Not a host's agent profile.** A host's agent profile sets the defaults of interactive sessions, and changing it needs those sessions relaunched. Switching a seat profile changes only what `tillr-drain` dispatches on later passes; it writes nothing under the harnesses' own configuration directories.
- **Never a partial overlay.** Every profile maps every seat. A switch replaces the whole routing table at once, so no unmentioned seat keeps running the earlier profile's backend.

## 3. Where Named Profiles Live and How They Are Checked

### 3.1 One Canonical Seat Catalog (`config/tillr/seats.json`)

Which seats exist, which are read-only and how each is described in a worker's brief are properties of the repository's workflows, not of a provider. They are declared once, in `config/tillr/seats.json` (`D-DSP1`):

```json
{
  "read_only_seats": ["scout", "classify", "test-run"],
  "seat_purposes": {
    "design": "…",
    "plan": "…",
    "build": "…",
    "edit": "…",
    "test-write": "…",
    "test-run": "…",
    "docs": "…",
    "review": "…",
    "verify": "…",
    "scout": "…",
    "classify": "…"
  }
}
```

The purposes are the sentences the policy files carry today. When the loader reads a named profile it takes `read_only_seats` and `seat_purposes` from the catalog and requires the profile's `seats` table to map **every** key of `seat_purposes`. A standalone `--policy <path>` file, as a test or another repository uses, still carries both blocks inline, so the drain's command-line surface is unchanged (`D-DSP2`).

### 3.2 Tracked Profile Directory (`config/tillr/profiles/<name>.json`)

One file per profile, where `<name>` matches `[a-z0-9][a-z0-9-]*` (`D-DSP2`). One file per profile lets a diff, a review and a gate check read each profile on its own, and lets the policy validation check every profile on disk, active or held, on every test run.

A profile file has the policy file's structure and two more fields:

- `summary` *(required)*: one line naming the harness and the model tiers, shown by `list` and by the observatory.
- `hold` *(optional, `{ "reason": string, "depends_on": [string] }`)*: marks the profile **defined and held** (`D-DSP3`). A held profile still passes every structural, adapter-path, read-only, escalation and full-seat check, so it cannot rot while it waits. `tillr drain profile use <name>` and `tillr-drain loop --profile <name>` refuse it until `hold` is removed in Git.

The baseline concurrency caps of [the live drain configuration](./tillr-live-drain-config.md#42-concurrency-limits-per-backend-per-seat-and-per-kind-of-work) (`backend_caps`, `seat_caps`, `work_caps`) may appear in a profile file, so a provider's ceilings travel with the profile they protect.

## 4. Listing, Showing, and Selecting Profiles Inside Live Drain Config

### 4.1 Slotting Into `drain_runtime_config` Rather Than Beside It

[The live drain configuration](./tillr-live-drain-config.md#51-canonical-storage-in-tillrstatedb) (`D-LD6`) makes the table `drain_runtime_config` in `.tillr/state.db`, with its `drain.config-changed` events, the one store of runtime configuration across passes, restarts and relaunches by `tillr-keep`. That design sketches a path-only `policy_path` key, which is not built. Named profiles take that slot under the key `seat_profile` (`D-DSP4`):

```json
{
  "profile": "copilot-gemini",
  "path": "config/tillr/profiles/copilot-gemini.json"
}
```

A file outside `config/tillr/profiles/` is stored in the same row as `{"profile": "", "path": "<resolved path>"}`. One key owns the selection, so a name and a path cannot disagree.

Startup and reload follow [that design's precedence](./tillr-live-drain-config.md#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep), highest first. Its `D-LD10`, which lets an explicit `--lanes` on a fresh start replace a saved lane count, [applies to the lane count only](./tillr-live-drain-config.md#54-startup-lane-count-reporting-and-fresh-start-flag-replacement).

1. **The `seat_profile` row written by an operator.** `tillr drain profile use` writes it; it survives `tillr-drain restart` and a relaunch by `tillr-keep` until `tillr drain config --reset` clears it.
2. **A startup flag on `tillr-drain loop`.** `--profile <name>` or `--policy <path>`; passing both exits 2.
3. **The repository setting.** An optional `[drain] profile = "<name>"` in `.tillr.toml`, left out when machines sharing the repository run different providers.
4. **The conventional default.** `copilot-gemini`, when that file exists and carries no `hold`; otherwise startup refuses and lists the profiles that are ready.

### 4.2 Operator Command Surface (`tillr drain profile`)

`tillr drain profile` takes the place of the `tillr drain policy set <path>` face that [the live drain configuration](./tillr-live-drain-config.md#41-live-seat-to-backend-and-model-routing) sketches, and `tillr drain policy [--json]` stays as the reader of the accepted policy snapshot (`D-DSP4`):

```text
tillr drain profile                         # same as 'list'
tillr drain profile list [--json]           # every profile: active, ready or held, and its summary
tillr drain profile show [<name>] [--json]  # the active profile, or <name> even if held, in full
tillr drain profile use <name>              # validate, refuse if held, switch in state.db
tillr drain profile use --file <path>       # an untracked policy file
```

```text
  NAME             STATE    SUMMARY
* copilot-gemini   active   pi on GitHub Copilot: one model for design and judgment, one for build, one for scout
  claude-sub       held     claude -p on the subscription: one model for design, two across the other seats
```

`tillr drain profile use claude-sub` while `hold` is present exits 2, prints `hold.reason` and `hold.depends_on`, writes nothing to the store and leaves a running drain untouched:

```text
tillr drain profile use: profile "claude-sub" is held and cannot be selected:
  reason: Held until the Claude backend is proven and the owner asks to switch.
active profile remains: copilot-gemini
```

`tillr drain profile use <name>` on a ready profile loads the catalog and the profile, runs the full policy validation, checks that the adapter programs exist, and in one write transaction upserts the `seat_profile` row and appends `drain.config-changed`. It works whether the loop is running or stopped, and prints the old and new names and whether a running loop will pick the switch up on its next pass.

`tillr drain config [--json]` and `tillr drain config --reset`, from [the live drain configuration](./tillr-live-drain-config.md#53-how-an-operator-reads-the-current-value), show and clear `seat_profile` beside the lane count and the caps.

## 5. Switching on a Running Drain: Pass Boundaries and Runs in Flight

### 5.1 Zero-Restart Reload at the Next Pass Boundary

A switch never needs `tillr-drain loop` or `tillr-keep` restarted (`D-DSP5`):

1. `tillr drain profile use <name>` commits `seat_profile` and appends `drain.config-changed`.
2. The loop's store trigger (`waitForTrigger`) wakes an idle loop, or the loop sees the event when its current pass ends.
3. At the top of the pass, `refreshPolicy` reads the effective `seat_profile`, loads and validates the profile, replaces the policy in memory, and writes the accepted snapshot to `.tillr/loop/backend-policy.json` and a line to `.tillr/loop/backend-policy-changes.jsonl`. It resets the share epoch (`D-LD7` of [the live drain configuration](./tillr-live-drain-config.md#41-live-seat-to-backend-and-model-routing)) of any seat whose proportional shares changed.
4. If the active profile's file is later edited into an invalid state, `refreshPolicy` keeps the accepted policy running, records the refusal and the file's hash in the snapshot, and the `backend-policy` row that needs a person appears. No run in flight is dropped.

### 5.2 Exact Behavior of Work and Sessions in Flight

No process is killed and no harness transcript is handed to another harness (`D-DSP6`):

| State when `profile use` runs | After the switch | How |
| :--- | :--- | :--- |
| **A running worker, scout, judge, gate or landing** | Finishes on the backend, model and effort it started with, and gates and lands as usual. | A dispatched request does not change once `tillr-run` has it; `refreshPolicy` changes only later planning. |
| **A workflow with several nodes in flight** | Running nodes finish on the old profile; a node not yet claimed resolves its seat against the **new** profile when it is claimed. | A workflow graph stores seat names, never backend names, and every profile maps every seat. |
| **A warm worker session after a gate, fence or landing returns** | A **fresh** session starts on the new profile's backend in the same lane, on the retained candidate commit. | `workerSessionReason` compares backend and route with the new request and records `seat-changed`, so one harness's session is never passed to another. |
| **A warm designer session at a document** | The document's worktree is kept, the old transcript binding is retired with `seat-changed`, and a fresh designer session opens on the new `design` backend. | `chooseDesigner` compares backend and model before it resumes. |
| **An item's earlier seat escalation** (`drain.seat-escalated`) | Scoped to the profile it happened under; under the new profile the item starts at that profile's first backend for the seat. | The event records the profile name, because one backend name can mean different things in two profiles. |
| **A run paused on quota** | If the paused backend is absent from the new profile, or not first for that seat, the run continues on the new profile's open backend for the seat in a fresh session, keeping the lane's uncommitted files. | Backend choice walks the new profile's list for the seat from its start when the paused backend is not in it. |

Runtime cap overrides in `drain_runtime_config` carry across a switch: `seat_caps` and `work_caps` keep applying, and a `backend_caps` entry naming a backend the new profile lacks stays stored, inactive, and is reported by `tillr drain profile use` and `tillr drain config`.

## 6. How the Operator and Owner See Which Profile Is Active

The policy snapshot (`PolicyState`, `.tillr/loop/backend-policy.json`) gains `profile` and `source`, and every surface that reports the drain reads them (`D-DSP7`):

1. **`tillr status`** adds `profile <name>` to the drain's summary line, `profile custom (<path>)` for a `--policy` file, and `[reload refused]` when the snapshot holds a refusal.
2. **`tillr drain profile` and `tillr drain profile show`** mark the active profile and print where the selection came from (`operator`, `flag`, `toml` or `default`), the accepted hash, the full routing table, the escalation order and the effective caps.
3. **`tillr drain config` and `tillr drain policy`** include `profile`, `path`, `source`, `hash` and `refusal` in both outputs. `tillr-keep` reads `tillr drain config --json` on a relaunch.
4. **The observatory header and the cards that need a person.** The view's snapshot endpoint exposes `policy.profile`, `policy.source`, `policy.summary` and `policy.refusal`; `ObservatoryHeaderBar` shows the profile name beside the loop's state and lane counts, with the summary on opening it. When an edit to the active profile fails validation, the existing `backend-policy` card names the profile.

## 7. The First Two Named Profiles

**Invariant.** Building this design does not change which backends the drain runs. `copilot-gemini` is selected; `claude-sub` is defined, checked by the repository's tests and **held** (`D-DSP8`). Selecting `claude-sub` needs the Claude backend proven and then the owner removing `hold` and running `tillr drain profile use claude-sub`.

### 7.1 `copilot-gemini` — Active Copilot Gemini Profile (Selected)

`config/tillr/profiles/copilot-gemini.json` carries the assignment of [`config/tillr/backend-policy.copilot.json`](../../config/tillr/backend-policy.copilot.json), without the `read_only_seats` and `seat_purposes` blocks the catalog now holds. Every row runs the adapter `tools/tillr-run/backends/pi/run-pi` at effort `medium`. The default backend is `pi-build`, and `build` escalates from `pi-build` to `pi`.

| Seats | Backend | Model route |
| :--- | :--- | :--- |
| `design` | `pi-designer` | `github-copilot/gemini-4-carbon` |
| `plan`, `review`, `verify` | `pi` | `github-copilot/gemini-4-carbon` |
| `build`, `edit`, `test-write`, `docs` | `pi-build` | `github-copilot/gemini-4-hydrogen` |
| `scout`, `classify`, `test-run` (read-only) | `pi-scout` | `github-copilot/gemini-3.8-flash` |

### 7.2 `claude-sub` — Claude Subscription Profile (Defined and Held; NOT Selected)

`config/tillr/profiles/claude-sub.json` puts the strongest model of the Claude subscription on `design` alone and spreads every other seat across the two cheaper ones. [`config/tillr/backend-policy.claude.json`](../../config/tillr/backend-policy.claude.json), by contrast, runs every seat but `design` on one model. Every row runs the adapter `tools/tillr-run/backends/claude/run-claude`, which [the Claude backend reference](../reference/tillr-claude-backend.md) describes. The profile carries:

```json
"hold": {
  "reason": "Held until the Claude backend is proven and the owner asks to switch.",
  "depends_on": ["<the item that proves the Claude backend>"]
}
```

The table assumes the leanings of [OQ-DSP1](#OQ-DSP1) and [OQ-DSP2](#OQ-DSP2):

| Seats | Backend | Model route | Effort |
| :--- | :--- | :--- | :--- |
| `design` | `claude-designer` | `fable` | `high` |
| `plan`, `review`, `verify`, `build` (and the default) | `claude-sonnet` | `sonnet` | `medium` |
| `edit`, `docs`, `test-write` | `claude-haiku`, escalating to `claude-sonnet` | `haiku` | `medium` |
| `scout`, `classify`, `test-run` (read-only) | `claude-haiku` | `haiku` | `medium` |

## 8. Relation to the Per-Machine Backend-Policy Stopgap

[The convergence design](./tillr-convergence.md#35-a-seat-names-its-harness-and-the-harness-names-the-model) ([OQ-TC2](./tillr-convergence.md#OQ-TC2), a designer's decision) treats the policy files as a stopgap on the way to one tracked map from seat to harness in which tillr's files name no model, each harness's own profile on the machine choosing it. This design revises that target and keeps its two invariants (`D-DSP9`).

**Why a model-free map is not the target.** A host's agent profile picks one default model for an interactive session. A drain routes eleven seats across three model tiers inside one harness, and switches harness and tiers together, as one named unit, on one machine, without rewriting host configuration or relaunching a session. A named bundle of harness and model tiers is therefore a lasting control.

**What stays true.**

- **No model in code.** The `tools/tillr*` programs and the adapters name no model; every model route is data under `config/tillr/profiles/`.
- **No repeated seat definitions.** The catalog of [§3.1](#31-one-canonical-seat-catalog-configtillrseatsjson) removes the repeated blocks, which is the rule of [the convergence design](./tillr-convergence.md#21-a-derived-copy-is-checked-instead-of-derived).
- **A machine's selection stays out of tracked files.** Git holds the profiles; each machine's untracked store holds which one its drain runs. Two machines with different credentials never change each other's selection by pulling.

What happens to the other policy files is [OQ-DSP3](#OQ-DSP3). Once it is ruled and built, [OQ-TC2](./tillr-convergence.md#OQ-TC2) names `D-DSP9` as the decision that replaces it.

## Open Questions

1. 💬 **OQ-DSP1: In the held Claude profile, should the bounded writing seats start on the cheapest model or the middle one?**

   A planned build divides work into `plan`, `build`, `edit`, `docs`, `test-write` and `verify`. The last three writing seats carry out pieces that are already planned. Starting them on the cheapest model stretches the subscription further; starting them on the middle one avoids an occasional first-pass gate failure on a subtle test or paragraph.

   - **A — Start `edit`, `docs` and `test-write` on `haiku`, escalating to `sonnet` when a worker objects that the piece exceeds its model.** The judgment seats and `build` run `sonnet`.
   - **B — Keep `haiku` on the read-only seats only, and run every writing and judgment seat on `sonnet`.** More subscription spend per planned item, fewer first-pass failures.

   <!-- vantage: question id=OQ-DSP1 leaning="A — Start edit, docs, and test-write on Haiku 5.5 with escalation to Sonnet 5.5" -->

   _Leaning:_ **A**, because those three seats carry out pieces that are already planned, and escalation covers a piece that proves harder than expected.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-DSP2: When the Claude profile reaches its quota reserve, should the drain pause or spill over to another provider?**

   A policy file lists a second provider on a seat because switching files costs a restart. Once a switch is one command, reaching the subscription's reserve forces a choice. Spilling over keeps items landing unattended; pausing keeps spend inside the subscription you selected and keeps the profile usable on a machine that has only that credential.

   - **A — Pause.** Every seat names only `claude -p` models; when the quota window closes, runs pause until it reopens or you switch profiles.
   - **B — Spill over to the Copilot backends.** Every seat lists its `copilot-gemini` counterpart as a fallback.

   <!-- vantage: question id=OQ-DSP2 leaning="A — Pause cleanly on subscription reserve (pure Claude profile)" -->

   _Leaning:_ **A**, because one-command switching removes the restart cost that mixed-provider fallbacks answer, and a pure profile never spends a second provider's quota unasked.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-DSP3: Which of the other policy files should carry forward as named profiles beside `copilot-gemini` and `claude-sub`?**

   `config/tillr/` tracks six other policy files: the OpenRouter default (`backend-policy.json`), `.bedrock`, `.claude`, `.claude-copilot`, `.gemini` and `.mixed`. Dropping an unused one keeps the list short; dropping one a machine still runs means writing it again. The `.gemini` file declares a twelfth seat, `tricky`, which the catalog would have to hold for that file to become a profile.

   - **A — Carry forward `bedrock` and `openrouter`, four profiles in all.** One profile per distinct provider setup.
   - **B — Carry forward all six, eight profiles in all.** Keeps every setup as it is, at the cost of a long list.
   - **C — Carry forward `bedrock` only, three profiles in all.** The shortest list, if the OpenRouter setup is unused.

   <!-- vantage: question id=OQ-DSP3 leaning="A — Carry forward bedrock and openrouter alongside copilot-gemini and held claude-sub" -->

   _Leaning:_ **A**, because `bedrock` and `openrouter` are the two provider setups the first two profiles do not cover, and `claude-sub` replaces the single-model Claude file.

   **Answer:**

   > _(empty — fill in when decided)_

## Decision Ledger

`D-DSP8` is the owner's. Every other row is the designer's decision; the owner has not ruled on it and may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-DSP1` | `read_only_seats` and `seat_purposes` are declared once, in `config/tillr/seats.json`, and every named profile maps every declared seat. | 2026-11-19, designer | [§3.1](#31-one-canonical-seat-catalog-configtillrseatsjson) | — |
| `D-DSP2` | Named profiles are files `config/tillr/profiles/<name>.json` with a required `summary`, validated in the gate's tests whether active or held. A standalone `--policy <path>` keeps working for tests and other callers. | 2026-11-19, designer | [§3.2](#32-tracked-profile-directory-configtillrprofilesnamejson) | — |
| `D-DSP3` | A profile may carry a `hold` block. `tillr drain profile use` and `tillr-drain loop --profile` refuse a held profile with exit 2, while the checks still validate it in full. | 2026-11-19, designer | [§3.2](#32-tracked-profile-directory-configtillrprofilesnamejson), [§4.2](#42-operator-command-surface-tillr-drain-profile) | — |
| `D-DSP4` | The selection is the `seat_profile` row of `drain_runtime_config`, under [the live drain configuration's precedence](./tillr-live-drain-config.md#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep): operator row, then flag, then `.tillr.toml`, then default. `tillr drain profile` with `list`, `show` and `use` takes the place of the path-only `tillr drain policy set <path>`. | 2026-11-19, designer | [§4.1](#41-slotting-into-drain_runtime_config-rather-than-beside-it), [§4.2](#42-operator-command-surface-tillr-drain-profile) | — |
| `D-DSP5` | A switch needs no restart of the loop or its keeper: `drain.config-changed` wakes the loop, `refreshPolicy` replaces the profile at the next pass boundary and resets changed share epochs, and an invalid later edit leaves the accepted policy running. | 2026-11-19, designer | [§5.1](#51-zero-restart-reload-at-the-next-pass-boundary) | — |
| `D-DSP6` | A switch leaves running processes alone, resolves unclaimed workflow nodes against the new profile, starts fresh worker and designer sessions and never resumes one across harnesses or models, scopes `drain.seat-escalated` by profile, and moves a quota-paused run onto the new profile's open backend when its own is absent or not first. | 2026-11-19, designer | [§5.2](#52-exact-behavior-of-work-and-sessions-in-flight) | — |
| `D-DSP7` | The policy snapshot records `profile` and `source`, and `tillr status`, `tillr drain profile`, `tillr drain config`, `tillr drain policy`, the observatory header and the `backend-policy` refusal card show the active profile. | 2026-11-19, designer | [§6](#6-how-the-operator-and-owner-see-which-profile-is-active) | — |
| `D-DSP8` | **Owner direction.** Two first profiles: `copilot-gemini`, the assignment of `backend-policy.copilot.json`, kept selected; and `claude-sub`, the subscription's strongest model on `design` and its two cheaper models on every other seat, defined and held until the Claude backend is proven and the owner asks to switch. | 2026-11-19, owner | [§7](#7-the-first-two-named-profiles) | — |
| `D-DSP9` | Revises [OQ-TC2](./tillr-convergence.md#OQ-TC2): a named profile of harness and model tiers is a lasting control, not a stopgap on the way to a model-free seat map. The seat catalog removes the repeated blocks, and the untracked store keeps each machine's selection local. | 2026-11-19, designer | [§8](#8-relation-to-the-per-machine-backend-policy-stopgap) | — |
