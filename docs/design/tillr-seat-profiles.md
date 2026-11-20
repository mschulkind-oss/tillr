---
status: in-review
stage: DESIGN
next: "Rule on OQ-DSP1 through OQ-DSP3; keep claude-sub held until item-request-e074800ade proves the Claude backend and the owner asks to switch"
depends-on:
  - ./tillr-live-drain-config.md
  - ./tillr-claude-backend.md
vantage:
  status-chip: true
---

# Drain Seat Profiles and One-Command Switching

**Status:** Proposed design — 2026-11-19.
**Needs your ruling:** [`OQ-DSP1`](#open-questions), [`OQ-DSP2`](#open-questions), [`OQ-DSP3`](#open-questions).
**Reads with:** [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) (live runtime configuration table, precedence ladder, and pass-boundary reload that this design slots into), [`tillr-claude-backend.md`](./tillr-claude-backend.md) (`run-claude` adapter and subscription quota windows), [`tillr-convergence.md`](./tillr-convergence.md) (audit whose [`OQ-TC2`](./tillr-convergence.md#decision-ledger) stopgap framing this design revises), [`seat-profiles.md`](./seat-profiles.md) (pi's interactive sub-agent picker sets, distinct from drain seat profiles). No implementation plan is seeded yet.

---

## 1. Context and Goal

Today a running `tillr-drain` loop takes one `--policy <path>` flag at startup pointing at one of five JSON files under `config/tillr/` (`backend-policy.copilot.json`, `backend-policy.claude.json`, `backend-policy.mixed.json`, `backend-policy.bedrock.json`, and `backend-policy.json`). That file maps the drain's ten workflow seats (`design`, `plan`, `build`, `edit`, `test-write`, `docs`, `review`, `verify`, `scout`, and `test-run`) to backend rows naming an adapter executable, model route, effort level, fallback order, optional proportional shares, and escalation hops. The backend policy currently in force is [`config/tillr/backend-policy.copilot.json`](../../config/tillr/backend-policy.copilot.json): Gemini 4 Carbon on `design` and judgement seats (`plan`, `review`, `verify`), Gemini 4 Hydrogen on implementation seats (`build`, `edit`, `test-write`, `docs`), and Gemini 3.8 Flash on read-only seats (`scout`, `test-run`).

Three friction points make raw `--policy` paths the wrong operator and owner surface:

1. **Paths instead of names.** Nothing in `tillr status`, the Observatory header, or loop events names the active workload assignment. Operators pass `config/tillr/backend-policy.copilot.json` on the command line, and `tillr drain policy` prints a SHA-256 hash and file path. Pointing a running drain at a different file today requires restarting the loop with a new `--policy` flag (while [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) [§4.1](./tillr-live-drain-config.md#41-live-seat-to-backend-and-model-routing) sketched path-based `tillr drain policy set <path>`, still without named profiles).
2. **No way to define and hold a future profile safely.** On 2026-11-19 the owner asked to define a Claude subscription assignment — *"as much work as possible to haiku and sonnet on the subscription plan"* with *"fable 5.2 on claude -p for the design seat"* — and *"hold that as a profile to switch to in the future"* without shifting workloads yet (*"don't shift workloads yet"*), while *"switch[ing] between these seat profiles easily and nam[ing] them."* A plain backend-policy file on disk carries neither a human name nor a mechanical hold preventing accidental selection before its backend dependency (`item-request-e074800ade`) is proven.
3. **Duplicated seat boilerplate across files.** Every `config/tillr/backend-policy*.json` file repeats identical `read_only_seats` and ten-entry `seat_purposes` blocks so worker briefs can render the repository's seat list. Adding an eleventh seat forces five identical edits.

This document settles what a named drain seat profile is, where profiles and shared seat definitions live, how profiles are listed, inspected, and switched with one command inside [`tillr-live-drain-config.md`](./tillr-live-drain-config.md)'s runtime store without restarting the drain, how in-flight runs and warm sessions behave across a switch, how the operator and owner see the active profile, the exact shape of the first two named profiles (`copilot-gemini` active; `claude-sub` held and **not selected**), and how named profiles revise [`OQ-TC2`](./tillr-convergence.md#decision-ledger) in [`tillr-convergence.md`](./tillr-convergence.md).

---

## 2. Boundary and Vocabulary: What a Seat Profile Is (and Is Not)

A **seat profile** *(coined here)* is a named, complete **backend policy** for `tillr-drain`: a tracked document under `config/tillr/profiles/<name>.json` that binds a stable slug (`copilot-gemini`, `claude-sub`) to a full map from every repository seat — plus `default_backend` and `preference` for seatless items — to backend adapter rows (harness adapter, model route, effort level, fallback list, optional proportional `seat_shares`, `escalate` ladders, quota probe settings, and baseline concurrency caps).

Three boundaries separate a drain seat profile from adjacent concepts in this repository:

- **Not a pi session seat set ([`seat-profiles.md`](./seat-profiles.md)).** Pi's `yolo-packs/seat-profiles` pack defines interactive picker sets (`META`, `OR`, `CODEX`, `BEDROCK`) inside one `pi` session to route child sub-agents (`coordinator`, `planner`, `worker`, `scout`). Those sets live inside a single harness and never govern `tillr-drain` dispatch or switch seats between `run-pi` and `run-claude`.
- **Not a yolo host profile (`config.toml`, `personal.toml`, `work.toml`).** Yolo profiles configure interactive agent defaults on a host or jail and require `yolo host apply --assert` plus jail relaunch to change. Switching a drain seat profile changes only what `tillr-drain` dispatches on subsequent passes; it never rewrites `~/.pi/agent/settings.json` or `~/.claude/` on the host.
- **Never a partial overlay.** Following the completeness rule established for pi sets in [`seat-profiles.md`](./seat-profiles.md#terms), every seat profile maps every seat declared by the repository. Switching profiles replaces the entire routing table at once, so no unmentioned seat can silently keep running the previous profile's provider or model.

---

## 3. Where Named Profiles Live and How They Are Checked

### 3.1 One Canonical Seat Catalog (`config/tillr/seats.json`)

Which seats exist in this repository, which of them are read-only, and how each seat is described in worker briefs are properties of the repository's workflows, not of Copilot, Claude, or Bedrock. Those declarations move once into `config/tillr/seats.json` ([`D-DSP1`](#decision-ledger)):

```json
{
  "read_only_seats": ["scout", "test-run"],
  "seat_purposes": {
    "design": "Settle design decisions and specify the intended change.",
    "plan": "Read the item and tree and write the piece list.",
    "build": "Implement the change described by the item or piece list.",
    "edit": "Apply a settled edit named in the piece list.",
    "test-write": "Write tests from the criteria.",
    "test-run": "Run a named check and report its result without fixing it.",
    "docs": "Write documentation and README changes.",
    "review": "Review the candidate against the criteria.",
    "verify": "Join the pieces and verify the combined result.",
    "scout": "Search the tree and report findings without writing."
  }
}
```

When loading a named profile from `config/tillr/profiles/<name>.json`, the loader supplies `read_only_seats` and `seat_purposes` from `config/tillr/seats.json` and requires the profile's `seats` table to map **every** key in `seat_purposes`. Standalone `--policy <path>` files used by unit tests or external repositories continue to accept inline `read_only_seats` and `seat_purposes` unchanged, preserving `tillr-drain`'s separable CLI contract ([`D-DSP2`](#decision-ledger)).

### 3.2 Tracked Profile Directory (`config/tillr/profiles/<name>.json`)

Named seat profiles live in `config/tillr/profiles/<name>.json`, one file per profile, where `<name>` is a lowercase ASCII slug matching `[a-z0-9][a-z0-9-]*` ([`D-DSP2`](#decision-ledger)). Keeping one file per profile lets Git diffs, code reviews, and gate checks inspect each profile independently and lets `Policy` validation check every profile on disk — active or held — on every test run.

Each profile file uses the existing `Policy` JSON structure with two profile metadata fields:

- `summary` *(required string on named profiles)*: one concise line naming the harness and model tiering for `list` and Observatory display.
- `hold` *(optional object `{ "reason": string, "depends_on": [string] }`)*: marks a profile as **defined and held** ([`D-DSP3`](#decision-ledger)). A held profile must still pass every structural, adapter-path, read-only, escalation, and full-seat check in gate tests so it never rots while waiting, but `tillr drain profile use <name>` and `tillr-drain loop --profile <name>` refuse to activate it until `hold` is removed in Git.

Baseline concurrency caps (`backend_caps`, `seat_caps`, `work_caps` from [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) [§4.2](./tillr-live-drain-config.md#42-concurrency-limits-per-backend-per-seat-and-per-kind-of-work)) may also appear inside a profile file so provider-specific concurrency ceilings travel with the profile they protect.

---

## 4. Listing, Showing, and Selecting Profiles Inside Live Drain Config

### 4.1 Slotting Into `drain_runtime_config` Rather Than Beside It

[`tillr-live-drain-config.md`](./tillr-live-drain-config.md) [§5.1](./tillr-live-drain-config.md#51-canonical-storage-in-tillrstatedb) (`D-LD6`) establishes `drain_runtime_config` in `.tillr/state.db`, paired with `drain.config-changed` events, as the single runtime configuration store across live loop passes, `tillr-drain restart` (`syscall.Exec`), and `tillr-keep` relaunches.

Rather than adding a second store or keeping [`tillr-live-drain-config.md`](./tillr-live-drain-config.md)'s unbuilt path-only `policy_path` key alongside a separate profile key, named seat profiles **fill that exact slot** under key `seat_profile` ([`D-DSP4`](#decision-ledger)):

```json
{
  "profile": "copilot-gemini",
  "path": "config/tillr/profiles/copilot-gemini.json"
}
```

When an operator or external harness passes an explicit custom file outside `config/tillr/profiles/`, the same `seat_profile` row stores `{"profile": "", "path": "<resolved-path>"}`. One key owns active backend-policy selection, so profile name and file path can never disagree.

Startup and reload follow [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) [§5.2](./tillr-live-drain-config.md#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep)'s four-level precedence ladder:

1. **`drain_runtime_config` (`source = 'operator'`) in `.tillr/state.db`** — written by `tillr drain profile use`; survives `tillr-drain restart` and `tillr-keep` supervisor relaunches until cleared by `tillr drain config --reset`.
2. **Startup CLI flags on `tillr-drain loop`** — `--profile <name>` (resolving `config/tillr/profiles/<name>.json`) or `--policy <path>` (mutually exclusive on the same command line; passing both exits 2).
3. **`.tillr.toml` repository setting** — optional `[drain] profile = "<name>"` when a workspace wants a checked-in fallback; omitted when machines in the same repo run different providers.
4. **Conventional default** — `copilot-gemini` when `config/tillr/profiles/copilot-gemini.json` exists and carries no `hold`; otherwise startup refuses with a message listing available ready profiles.

### 4.2 Operator Command Surface (`tillr drain profile`)

`tillr drain profile` replaces [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) [§4.1](./tillr-live-drain-config.md#41-live-seat-to-backend-and-model-routing)'s unbuilt `tillr drain policy set <path>` sketch before implementation begins, while keeping `tillr drain policy [--json]` working as a low-level reader of `.tillr/loop/backend-policy.json` ([`D-DSP4`](#decision-ledger)):

```text
tillr drain profile                      # same as 'list': list named profiles and mark active (*)
tillr drain profile list [--json]        # list every profile, status (active/ready/held), summary
tillr drain profile show [<name>] [--json] # show active profile (or <name>, even if held) in full
tillr drain profile use <name>           # validate <name>, refuse if held, switch live in state.db
tillr drain profile use --file <path>    # escape hatch for ad-hoc untracked backend-policy files
```

Example `tillr drain profile` output with the initial two profiles:

```text
  NAME             STATE    SUMMARY
* copilot-gemini   active   pi on GitHub Copilot: Carbon 4 design/judge, Hydrogen 4 build, Flash 3.8 scout
  claude-sub       held     claude -p subscription: Fable 5.2 design, Sonnet 5.5 + Haiku 5.5 work [depends on item-request-e074800ade]
```

Running `tillr drain profile use claude-sub` while `hold` is present fails immediately with exit code 2, prints `hold.reason` and `hold.depends_on`, writes nothing to `.tillr/state.db`, and leaves the running drain untouched:

```text
tillr drain profile use: profile "claude-sub" is held and cannot be selected:
  reason: Held per owner instruction (2026-11-19: do not shift workloads yet) until the Claude backend is proven and selectable.
  depends_on: item-request-e074800ade
active profile remains: copilot-gemini
```

Running `tillr drain profile use <ready-name>` loads `config/tillr/seats.json` and `config/tillr/profiles/<ready-name>.json`, runs full `Policy.Check()` validation, verifies adapter executables exist, and — in one `.tillr/state.db` write transaction — upserts `drain_runtime_config['seat_profile']` and appends `drain.config-changed`. Whether the loop is running or stopped, the command finishes in milliseconds and prints the old and new profile names plus whether a live loop acknowledged or will pick up the switch on next pass.

`tillr drain config [--json]` and `tillr drain config --reset [profile]` from [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) [§5.3](./tillr-live-drain-config.md#53-how-an-operator-reads-the-current-value) display and clear `seat_profile` alongside `lanes` and concurrency caps.

---

## 5. Switching on a Running Drain: Pass Boundaries and Runs in Flight

### 5.1 Zero-Restart Reload at the Next Pass Boundary

Switching seat profiles **never requires restarting `tillr-drain loop` or `tillr-keep`** ([`D-DSP5`](#decision-ledger)):

1. `tillr drain profile use <name>` commits `seat_profile` and appends `drain.config-changed` to `.tillr/state.db`.
2. The loop's store trigger (`waitForTrigger`, polling every 100 ms) wakes immediately if idle between passes, or sees the new event as soon as the current planning pass finishes.
3. At the top of the pass, `refreshPolicy` reads the effective `seat_profile` from `.tillr/state.db`, loads and validates the profile, updates the in-memory `Policy`, writes the accepted snapshot atomically to `.tillr/loop/backend-policy.json` (`PolicyState`, which is loop output read by `tillr drain why`, `tillr drain policy`, and `tillr view waiting`) and `.tillr/loop/backend-policy-changes.jsonl`, and resets `seat_share_epochs` (`D-LD7` in [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) [§4.1](./tillr-live-drain-config.md#41-live-seat-to-backend-and-model-routing)) for any seat whose proportional shares changed.
4. If someone later hand-edits the active profile's JSON file on disk into an invalid state, `refreshPolicy` keeps the previously accepted `Policy` snapshot running in memory, records `refusal` and `disk_hash` in `.tillr/loop/backend-policy.json`, and surfaces the existing `backend-policy` `"needs you"` waiting row without dropping runs in flight.

### 5.2 Exact Behavior of Work and Sessions in Flight

Every class of active or retained state in `tillr-drain` transitions across a profile switch without killing processes or corrupting harness transcripts ([`D-DSP6`](#decision-ledger)):

| State at the moment of `profile use` | Behavior after the switch | Mechanism |
|---|---|---|
| **Active worker, scout, judge, gate, or landing subprocess** | Finishes undisturbed on the backend, model, and effort it started with; commits, gates, and lands normally. | Dispatched `run.Request` values are immutable once handed to `tillr-run`; `refreshPolicy` mutates only future pass planning. |
| **Multi-node workflow in flight** (`planned-build@v1`, `scouted-design@v1`, `design-from-scouts@v1`, `doc-triage@v1`) | Running nodes finish on the old profile; downstream nodes not yet claimed resolve their seat against the **new** profile when claimed. | Workflow graphs store seat names (`plan`, `scout`, `build`, `verify`, `design`), never backend IDs, and every named profile maps all ten seats. |
| **Warm worker session after gate, fence, or land return** (`WorkerSession`) | Starts a **fresh** session on the new profile's backend/model in the same lane on top of the retained candidate commit. | `workerSessionReason` compares `Backend` and `Route()` against the new `run.Request` and records `"seat-changed"`, never passing a `pi` session UUID to `claude -p` or vice versa. |
| **Warm designer session at a document desk** (`last_designers`) | Keeps the document's desk worktree, retires the old transcript binding (`"seat-changed"`), and opens a fresh designer session on the new profile's `design` backend. | `chooseDesigner` checks `Backend` and `Model` before resuming; transcript files remain in durable history. |
| **Prior seat escalation on an item** (`drain.seat-escalated`) | Scoped to the profile under which the obstacle occurred; under the new profile the item starts at the new profile's primary backend for its seat. | Recording the profile name on `drain.seat-escalated` prevents backend-name collisions across profiles (for example, `"pi"` meaning primary judgement in one profile and fallback in another). |
| **Quota-paused single-worker run** (`quota_pauses`) | If the paused backend is absent or no longer primary for that seat in the new profile, continues immediately on the new profile's open backend for that seat with `FreshSession = true`, preserving uncommitted files in the lane. | Updating `availableBackend` to walk the new profile's `seatList(seat)` from index 0 when `start` leaves the profile prevents stranding a lane on an inactive profile's quota window. |

If the operator holds temporary runtime concurrency overrides in `drain_runtime_config` (`tillr drain alloc` from [`tillr-live-drain-config.md`](./tillr-live-drain-config.md) [§4.2](./tillr-live-drain-config.md#42-concurrency-limits-per-backend-per-seat-and-per-kind-of-work)), `seat_caps` and `work_caps` continue to apply across profiles, while `backend_caps` entries naming backends absent from the newly selected profile remain stored as inactive and are noted by `tillr drain profile use` and `tillr drain config`.

---

## 6. How the Operator and Owner See Which Profile Is Active

Active profile identity is propagated from `PolicyState` (`.tillr/loop/backend-policy.json`, extended with `profile` and `source`) to every CLI and web surface where the operator or owner checks the drain ([`D-DSP7`](#decision-ledger)):

1. **`tillr status`** — adds `profile <name>` (for example, `profile copilot-gemini`, or `profile custom (path)` for `--policy`, plus `[reload refused]` if `PolicyState.Refusal` is non-empty) directly to the drain summary line so routine status checks always display which seat profile is spending quota.
2. **`tillr drain profile` and `tillr drain profile show`** — `profile` marks the active profile with `*` in the profile table; `profile show` prints the active profile's provenance (`operator`, `flag`, `toml`, or `default`), accepted SHA-256, full ten-seat routing table, escalation paths, and effective caps.
3. **`tillr drain config` and `tillr drain policy`** — both include `profile`, `path`, `source`, `hash`, and `refusal` in human and `--json` output (`tillr-keep` reads `tillr drain config --json` on relaunch).
4. **Observatory header bar and `"needs you"` cards** — `tillr-view`'s snapshot endpoint exposes `policy.profile`, `policy.source`, `policy.summary`, and `policy.refusal` so `ObservatoryHeaderBar` renders a persistent profile chip (`copilot-gemini`) beside the loop state dot and lane counts. Hovering or opening the chip shows the active tier summary (`design: gemini-4-carbon · build: gemini-4-hydrogen · scout: gemini-3.8-flash`). When an on-disk edit to the active profile fails validation, the existing `backend-policy` waiting card in `"needs you"` names the profile explicitly.

---

## 7. The First Two Named Profiles

**Workload invariant:** Landing this design and its implementation **does not change which profile the drain runs**. `copilot-gemini` is the active profile; `claude-sub` is defined, validated by repository tests, and **held — explicitly not selected** ([`D-DSP8`](#decision-ledger)). Selecting `claude-sub` requires both completion of `item-request-e074800ade` (proving and making selectable the Claude backend) and a subsequent owner decision removing `hold` and running `tillr drain profile use claude-sub`.

### 7.1 `copilot-gemini` — Active Copilot Gemini Profile (Selected)

`config/tillr/profiles/copilot-gemini.json` carries forward the exact assignment currently running in [`config/tillr/backend-policy.copilot.json`](../../config/tillr/backend-policy.copilot.json), minus the duplicated `read_only_seats` and `seat_purposes` blocks moved to `config/tillr/seats.json`:

- **Summary:** `"pi on GitHub Copilot: Carbon 4 on design and judgement, Hydrogen 4 on build, Flash 3.8 on scout"`
- **State:** Ready and **active** (`default_backend: "pi-build"`, `preference: ["pi-build", "pi"]`, `escalate: { "build": ["pi-build", "pi"] }`).

| Seat(s) | Backend mapping | Adapter | Model route | Effort |
|---|---|---|---|---|
| `design` | `["pi-designer", "pi"]` | `tools/tillr-run/backends/pi/run-pi` | `github-copilot/gemini-4-carbon` | `medium` |
| `plan`, `review`, `verify` | `"pi"` | `tools/tillr-run/backends/pi/run-pi` | `github-copilot/gemini-4-carbon` | `medium` |
| `build`, `edit`, `test-write`, `docs` *(+ `default_backend`)* | `"pi-build"` | `tools/tillr-run/backends/pi/run-pi` | `github-copilot/gemini-4-hydrogen` | `medium` |
| `scout`, `test-run` *(read-only)* | `"pi-scout"` | `tools/tillr-run/backends/pi/run-pi` | `github-copilot/gemini-3.8-flash` | `medium` |

### 7.2 `claude-sub` — Claude Subscription Profile (Defined and Held; NOT Selected)

`config/tillr/profiles/claude-sub.json` encodes the subscription profile requested by the owner on 2026-11-19 (*"as much work as possible to haiku and sonnet on the subscription plan"* with *"fable 5.2 on claude -p for the design seat"*, *"don't shift workloads yet"*).

Unlike the earlier [`config/tillr/backend-policy.claude.json`](../../config/tillr/backend-policy.claude.json) — which routed all nine non-design seats to Opus 5.5 (`model: "opus"`, `effort: "high"`) and tied fallbacks to work-only AWS Bedrock — `claude-sub` tiers non-design work across **Sonnet 5.5** (`sonnet`) and **Haiku 5.5** (`haiku`) while reserving **Fable 5.2** (`fable`) on `claude -p` exclusively for `design`:

- **Summary:** `"claude -p subscription: Fable 5.2 on design, Sonnet 5.5 and Haiku 5.5 across work and scout seats"`
- **State:** **Held — not selected.** Carries:
  ```json
  "hold": {
    "reason": "Held per owner instruction (2026-11-19: do not shift workloads yet) until the Claude backend is proven and selectable.",
    "depends_on": ["item-request-e074800ade"]
  }
  ```
- **Adapter across all rows:** `tools/tillr-run/backends/claude/run-claude` (`claude -p`, documented in [`tillr-claude-backend.md`](./tillr-claude-backend.md), with 300k auto-compaction on Fable 5.2 landed in `1d986978c`).

| Seat(s) | Backend mapping (under [`OQ-DSP1`](#open-questions) & [`OQ-DSP2`](#open-questions) leanings) | Adapter | Model route | Effort |
|---|---|---|---|---|
| `design` | `"claude-designer"` | `tools/tillr-run/backends/claude/run-claude` | `fable` (Claude Fable 5.2) | `high` |
| `plan`, `review`, `verify` | `"claude-sonnet"` | `tools/tillr-run/backends/claude/run-claude` | `sonnet` (Claude Sonnet 5.5) | `medium` |
| `build` *(+ `default_backend`)* | `"claude-sonnet"` | `tools/tillr-run/backends/claude/run-claude` | `sonnet` (Claude Sonnet 5.5) | `medium` |
| `edit`, `docs`, `test-write` | `"claude-haiku"` *(escalating to `"claude-sonnet"` on seat obstacle; or `"claude-sonnet"` if [`OQ-DSP1`](#open-questions) rules B)* | `tools/tillr-run/backends/claude/run-claude` | `haiku` (Claude Haiku 5.5) | `medium` |
| `scout`, `test-run` *(read-only)* | `"claude-haiku"` | `tools/tillr-run/backends/claude/run-claude` | `haiku` (Claude Haiku 5.5) | `medium` |

Two owner choices inside `claude-sub` are surfaced for ruling before `hold` is ever lifted:

- [`OQ-DSP1`](#open-questions) rules whether the three bounded piece-writing seats (`edit`, `docs`, `test-write`) start on Haiku 5.5 with automatic seat escalation to Sonnet 5.5 (leaning A, maximizing Haiku share on planned builds) or start directly on Sonnet 5.5 keeping Haiku strictly read-only (Option B).
- [`OQ-DSP2`](#open-questions) rules whether `claude-sub` pauses cleanly when the Claude subscription hits its quota reserve (leaning A, pure subscription profile portable across work and personal hosts) or automatically spills over to Copilot Gemini (Option B).

---

## 8. Relation to the Per-Machine Backend-Policy Stopgap

In [`tillr-convergence.md`](./tillr-convergence.md) [§3.5](./tillr-convergence.md#35-a-seat-names-its-harness-and-the-harness-names-the-model), designer-settled [`OQ-TC2`](./tillr-convergence.md#decision-ledger) treated the five `config/tillr/backend-policy*.json` files as a temporary per-machine-group stopgap toward a single tracked `seat -> harness` map in which tillr's files would name **no model IDs at all**, delegating model choice to each harness's host-level yolo profile once a harness profile could answer for the `design` seat.

The owner's 2026-11-19 requirement revises that target while preserving both underlying engineering invariants from [`tillr-convergence.md`](./tillr-convergence.md) ([`D-DSP9`](#decision-ledger)):

1. **Why a single model-free `seat -> harness` map is no longer the target.** A host's yolo profile picks one default model for an interactive user session; an unattended drain routes ten seats across **three distinct model tiers within the same harness** (`gemini-4-carbon` / `gemini-4-hydrogen` / `gemini-3.8-flash` inside `run-pi`, or `fable` / `sonnet` / `haiku` inside `run-claude`) and switches **both harness and model tiering together as one named unit** on the same machine (`copilot-gemini` today, `claude-sub` later) without rewriting host repository or restarting jails. Named `(harness + model-tier)` bundles are therefore a permanent operator and owner control, not a stopgap waiting to disappear.
2. **What [`OQ-TC2`](./tillr-convergence.md#decision-ledger) got right — and how this design enforces it cleanly.**
   - *No model IDs in code:* `tools/tillr*` binaries and adapter scripts still name zero model IDs; every model route lives in data under `config/tillr/profiles/`.
   - *No duplicated seat definitions ([`tillr-convergence.md` §2.1](./tillr-convergence.md#21-a-derived-copy-is-checked-instead-of-derived)):* Extracting `config/tillr/seats.json` ([§3.1](#31-one-canonical-seat-catalog-configtillrseatsjson)) eliminates the five-way copy-paste of `read_only_seats` and `seat_purposes` immediately.
   - *Machine-local selection stays out of tracked files:* Just as `tag-work` and `tag-personal` define named yolo profiles in Git while each machine's untracked `overrides.toml` selects which one runs, `config/tillr/profiles/` defines named drain seat profiles in Git while each machine's untracked `.tillr/state.db` (`drain_runtime_config['seat_profile']`) selects which profile that machine's drain runs. A work host running AWS Bedrock and a personal host running Copilot Gemini never clobber each other's active selection through `git pull`.

Disposition of the four remaining `config/tillr/backend-policy*.json` files alongside `copilot-gemini` and `claude-sub` is raised in [`OQ-DSP3`](#open-questions). Once [`OQ-DSP3`](#open-questions) is ruled and implemented, [`OQ-TC2`](./tillr-convergence.md#decision-ledger) in [`tillr-convergence.md`](./tillr-convergence.md) will be marked superseded by [`D-DSP9`](#decision-ledger).

---

## Open Questions

1. 💬 **OQ-DSP1: In the held Claude subscription profile, should bounded piece-writing seats start on Haiku 5.5 or Sonnet 5.5?**

   Today all non-design implementation seats share one model tier, while planned builds split work into `plan`, `build`, `edit`, `docs`, `test-write`, and `verify`. When you later switch to the Claude subscription profile, `design` runs Fable 5.2 and read-only `scout` and `test-run` run Haiku 5.5, leaving a cost-versus-rework trade-off on bounded writing seats. Starting `edit`, `docs`, and `test-write` on Haiku 5.5 stretches subscription allowance much further, whereas starting them on Sonnet 5.5 avoids occasional first-pass gate bounces on nuanced tests or prose.

   **A — Start `edit`, `docs`, and `test-write` on Haiku 5.5, climbing to Sonnet 5.5 if a worker objects that the piece exceeds its model.** `build`, `plan`, `review`, and `verify` run Sonnet 5.5; `scout` and `test-run` run Haiku 5.5; `design` runs Fable 5.2.

   **B — Keep Haiku 5.5 on read-only `scout` and `test-run` only, and run every writing and judgement seat (`build`, `edit`, `docs`, `test-write`, `plan`, `review`, `verify`) on Sonnet 5.5.** Higher Sonnet subscription spend per planned item, with fewer first-pass bounces on tests and docs.

   <!-- vantage: question id=OQ-DSP1 leaning="A — Start edit, docs, and test-write on Haiku 5.5 with escalation to Sonnet 5.5" -->

   _Leaning:_ **A**, because `edit`, `docs`, and `test-write` execute already-planned pieces, fulfilling *"as much work as possible to haiku and sonnet"* while escalating automatically to Sonnet 5.5 when a piece proves harder than expected.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-DSP2: When the Claude subscription profile reaches its quota reserve, should the drain pause or spill over to Copilot Gemini?**

   Earlier Claude configurations listed a second provider on every seat because switching files used to require restarting the drain. Once switching profiles is a single live command, hitting the 5-hour or 7-day subscription reserve while running the Claude profile forces a choice between strict provider boundaries and unattended spillover. Automatic spillover keeps items landing without waiting for you, whereas pausing keeps spend strictly inside the subscription you selected and keeps the profile usable on machines without Copilot credentials.

   **A — Pause cleanly on subscription reserve (pure Claude profile).** Every seat names only `claude -p` models; when quota closes, runs pause and wait for the window to reopen unless you switch profiles with one command.

   **B — Spill over automatically to Copilot Gemini when Claude subscription windows close.** Every seat lists its Copilot Gemini counterpart as a fallback so draining continues automatically on Copilot during Claude cooldowns.

   <!-- vantage: question id=OQ-DSP2 leaning="A — Pause cleanly on subscription reserve (pure Claude profile)" -->

   _Leaning:_ **A**, because one-command live switching removes the restart cost that motivated mixed-provider fallbacks, and a pure profile never spends a second provider's quota silently or breaks on work machines without personal Copilot access.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-DSP3: Which of the older provider files should carry forward as named profiles alongside `copilot-gemini` and `claude-sub`?**

   `config/tillr/` currently tracks five provider files: Copilot Gemini, an older Opus-everywhere Claude file with Bedrock fallbacks, a Claude-designer plus Bedrock-worker split, pure AWS Bedrock for work machines, and an older personal OpenRouter DeepSeek file. When migrating into named profiles, retiring unused setups keeps `tillr drain profile` short and clear, while dropping a setup still used on a work or personal host would force recreating it later.

   **A — Carry forward `bedrock` and `openrouter` alongside `copilot-gemini` and held `claude-sub` (four profiles total), retiring the Opus-everywhere Claude draft and the mixed Bedrock split.** Keeps one profile per distinct provider setup across work and personal machines.

   **B — Carry forward all four remaining files as named profiles alongside `copilot-gemini` and held `claude-sub` (six profiles total).** Preserves every historical setup verbatim at the cost of clutter in the profile list.

   **C — Keep only `copilot-gemini`, held `claude-sub`, and `bedrock` (three profiles total), retiring OpenRouter, mixed Bedrock, and the Opus-everywhere Claude draft.** Smallest list if OpenRouter is no longer used as a personal fallback.

   <!-- vantage: question id=OQ-DSP3 leaning="A — Carry forward bedrock and openrouter alongside copilot-gemini and held claude-sub" -->

   _Leaning:_ **A**, because `bedrock` remains the work-host AWS profile, `openrouter` preserves the non-Copilot personal floor, and `claude-sub` directly supersedes the earlier Opus-everywhere Claude subscription draft.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

### Settled by the Owner's Request

| ID | Decision | Date | Where |
|---|---|---|---|
| `D-DSP8` | Define two initial named seat profiles: `copilot-gemini` (the current Carbon 4 / Hydrogen 4 / Flash 3.8 assignment, kept active) and `claude-sub` (Fable 5.2 on `claude -p` for `design`, Sonnet 5.5 and Haiku 5.5 across remaining seats on the subscription plan, **defined and held — not selected**, gated on `item-request-e074800ade` and an explicit future owner switch). | 2026-11-19 | [§7](#7-the-first-two-named-profiles) |

### Designer's Decisions (Owner May Overturn)

| ID | Decision | Date | Where |
|---|---|---|---|
| `D-DSP1` | Extract repository-invariant `read_only_seats` and `seat_purposes` once into `config/tillr/seats.json`, eliminating five-way duplication across profiles while requiring every named profile to map every declared seat. | 2026-11-19 | [§3.1](#31-one-canonical-seat-catalog-configtillrseatsjson) |
| `D-DSP2` | Store named seat profiles at `config/tillr/profiles/<name>.json` (`[a-z0-9][a-z0-9-]*`) with required `summary`, validated in gate tests whether active or held; preserve standalone `--policy <path>` for tests and external callers. | 2026-11-19 | [§3.2](#32-tracked-profile-directory-configtillrprofilesnamejson) |
| `D-DSP3` | Support an optional `hold` block (`{reason, depends_on}`) on named profiles; `tillr drain profile use` and `tillr-drain loop --profile` refuse held profiles with exit code 2 while gate checks still validate them completely. | 2026-11-19 | [§3.2](#32-tracked-profile-directory-configtillrprofilesnamejson), [§4.2](#42-operator-command-surface-tillr-drain-profile) |
| `D-DSP4` | Unify profile selection into [`tillr-live-drain-config.md`](./tillr-live-drain-config.md)'s `drain_runtime_config` table under `seat_profile` (`{profile, path}`) with the same four-level precedence ladder (`operator > CLI flag > .tillr.toml > default`), replacing the unbuilt path-only `tillr drain policy set <path>` sketch with `tillr drain profile {list,show,use}`. | 2026-11-19 | [§4.1](#41-slotting-into-drain_runtime_config-rather-than-beside-it), [§4.2](#42-operator-command-surface-tillr-drain-profile) |
| `D-DSP5` | Switch profiles live with zero loop or keeper restart: `drain.config-changed` wakes the loop within 100 ms, `refreshPolicy` swaps the profile at the next pass boundary, resets changed `seat_share_epochs`, and keeps the previous accepted snapshot running if a later on-disk edit is invalid. | 2026-11-19 | [§5.1](#51-zero-restart-reload-at-the-next-pass-boundary) |
| `D-DSP6` | Leave active subprocesses untouched on switch; evaluate unclaimed downstream workflow nodes against the new profile; start fresh worker/designer sessions (`"seat-changed"`) rather than resuming across harnesses/models; scope `drain.seat-escalated` by profile; and advance quota-paused runs onto the new profile's open seat backend when their paused backend is no longer primary. | 2026-11-19 | [§5.2](#52-exact-behavior-of-work-and-sessions-in-flight) |
| `D-DSP7` | Record `profile` and `source` in `.tillr/loop/backend-policy.json` (`PolicyState`) and surface the active profile in `tillr status`, `tillr drain profile`, `tillr drain config`, `tillr drain policy`, the Observatory header bar, and `backend-policy` `"needs you"` refusal cards. | 2026-11-19 | [§6](#6-how-the-operator-and-owner-see-which-profile-is-active) |
| `D-DSP9` | Revise [`OQ-TC2`](./tillr-convergence.md#decision-ledger) in [`tillr-convergence.md`](./tillr-convergence.md): named `(harness + model-tier)` seat profiles in `config/tillr/profiles/` are a permanent control rather than a stopgap toward model-free seat maps, while `config/tillr/seats.json` removes boilerplate duplication and `.tillr/state.db` keeps active selection machine-local. | 2026-11-19 | [§8](#8-relation-to-the-per-machine-backend-policy-stopgap) |
