---
title: "Drain Seat Profiles and One-Command Switching"
status: in-review
stage: DESIGN
updated: 2026-11-26
tags:
  - tillr
  - drain
  - backend-policy
  - design
summary: "A seat profile is a named, complete backend policy for the drain. Profiles are a person's own files: they live one file per name (backend-policy.<name>.json) in a per-user directory outside any repository, reach a machine by a personal pack or by hand, can be selected at startup by --profile <name> or optional [drain] profile in .tillr.toml (with --policy <file> winning when given), and are listed and switched with one command and no restart, with a hold that keeps a defined profile from being selected. The loop refuses a missing profile name with the list of names that exist, and once the full resolver lands, reports at startup which file it took and why. Nothing here is built: the drain takes a policy file path at startup. Two questions on the held Claude profile are open."
next: "Build the slices at the end of the document, starting with --profile <name> and [drain] profile selection (D-DSP18). The owner rules OQ-DSP1 and OQ-DSP2. The claude-sub profile stays held until the Claude backend is proven and the owner asks to switch."
depends-on:
  - ./tillr-live-drain-config.md
  - ../reference/tillr-claude-backend.md
vantage:
  status-chip: true
---

# Drain Seat Profiles and One-Command Switching

**Status:** 2026-11-26. Proposed; nothing here is built. No per-user directory is read, `config/tillr/seats.json` does not exist, `tillr-drain` has no `--profile` flag or `profile` face, `.tillr.toml` has no `[drain] profile` key, and `rg -n 'seat_profile|TILLR_CONFIG_DIR' tools config` finds nothing. The drain takes one `--policy <path>` flag at startup, falls back to `<root>/config/tillr/backend-policy.json` when the flag is absent, and reloads that file's contents at each pass (`refreshPolicy`).

> **In short.** Give each complete assignment of seats to backends a name, keep one file per name in the person's own configuration directory outside any repository, and let one command switch a running drain between them. A profile can be defined and held, so it is checked whenever it is read but cannot be selected.

**Why it matters.** Which model fills which seat is a personal, per-machine choice, so it does not belong in a repository. A path on a command line names nothing an operator can read back, a defined-but-unproven assignment can be selected by accident, and every policy file repeats the same seat list.

**The shape.** A seat catalog in the repository, a per-user directory of profile files that a personal pack or a person fills, one lookup order with a startup report, one key in the drain's runtime configuration table, and a `tillr drain profile` face.

**Cost.** One resolver shared by the loop and the recovery command, one face, and two fields on the policy snapshot the read faces already show. It forecloses a tracked policy that names no model, and it takes profiles out of the gate's reach: a profile is checked when tillr reads it, not on every test run.

**Start at [§2](#2-boundary-and-vocabulary-what-a-seat-profile-is-and-is-not)** for what a profile is, [§3](#3-where-named-profiles-live-and-how-they-are-checked) for where one lives and how it gets there, then [§4](#4-listing-showing-and-selecting-profiles-inside-live-drain-config) for the lookup order and the command.

**Needs your ruling:** [OQ-DSP1](#OQ-DSP1), [OQ-DSP2](#OQ-DSP2).

**Reads with:** [tillr-live-drain-config.md](./tillr-live-drain-config.md) (the runtime configuration table, its precedence and the reload at a pass boundary, which this design uses), [tillr-claude-backend.md](../reference/tillr-claude-backend.md) (the `run-claude` adapter and the subscription's quota windows), and [tillr-convergence.md](./tillr-convergence.md) (whose [OQ-TC2](./tillr-convergence.md#OQ-TC2) this design revises). No implementation plan exists.

## 1. Context and Goal

A running `tillr-drain` loop takes one `--policy <path>` flag naming a backend policy file; with none it reads `config/tillr/backend-policy.json` in the repository it operates, which this repository ships as an example with placeholder models, so a loop started here with no flag has nothing it can run. The owner's own policies (an OpenRouter one, a Bedrock one, a Copilot Gemini one and two Claude ones) were tracked beside it until 2026-11-24 and now live outside the repository. Each file maps the drain's eleven workflow seats (`design`, `plan`, `build`, `edit`, `test-write`, `docs`, `review`, `verify`, `scout`, `classify` and `test-run`) to backend rows, and a backend row names an adapter program, a model route and an effort level. The file also holds the default backend and the escalation order.

Four things make a raw path the wrong surface for an operator:

1. **A personal choice with no personal place.** The owner said on 2026-11-24 that these seat profiles are "personal preference" and belong with a person's own configuration, and "we also need to have a way to ship profiles from a personal pack into jails or with manual staging somewhere for other setups" (`D-DSP10`, `D-DSP11`). Today the only place tillr looks unasked is inside the repository.
2. **Paths, not names.** Nothing in `tillr status`, the observatory header or the loop's events names the active assignment. `tillr drain policy` prints a hash and a file path, and pointing a running drain at a different file means restarting the loop with a new flag.
3. **No way to define a profile and hold it.** A policy file on disk carries neither a name nor anything that stops it from being selected before the backend it depends on is proven.
4. **The seat list is repeated.** Every policy file repeats the same `read_only_seats` and `seat_purposes` blocks so that worker briefs can render the repository's seat list. Adding a seat means the same edit in every file.

This design settles what a named seat profile is, where profiles and the shared seat definitions live, how a profile reaches a machine, in what order the loop looks for a policy and what it says about the one it took, what the repository's example file is for, how profiles are listed, shown and switched inside [the live drain configuration](./tillr-live-drain-config.md) without restarting the drain, what happens to runs and sessions in flight, how the active profile is shown, the first two profiles, and how named profiles revise [OQ-TC2](./tillr-convergence.md#OQ-TC2).

## 2. Boundary and Vocabulary: What a Seat Profile Is (and Is Not)

A **seat profile** *(coined here)* is a named, complete **backend policy** for `tillr-drain`: a file `backend-policy.<name>.json` in the person's own tillr configuration directory ([§3.2](#32-per-user-profile-directory-configtillrbackend-policynamejson); `D-DSP18`, the designer's decision from an operator request, which the owner may overturn) that binds a name such as `copilot-gemini` to a full map from every seat the operated repository declares, plus the default backend for an item with no seat, to backend rows. A backend row names the adapter, the model route, the effort level, the fallback list, optional proportional shares, the escalation order, the quota probe settings and the baseline concurrency caps.

Four boundaries separate it from its neighbors:

- **Not part of a repository.** A profile names models, and which models a person's credentials reach is a fact about that person's machines. This repository tracks the seat catalog and one example, and tillr never looks inside a repository for a named profile (`D-DSP10`).
- **Not a model set inside one harness.** A harness may define sets of models for the sub-agents of one interactive session (pi reads its own from `~/.pi/agent/seat-profiles.json`, a different file with a similar name). Those sets never govern what `tillr-drain` dispatches, and cannot move a seat between `run-pi` and `run-claude`.
- **Not a host's agent profile.** A host's agent profile sets the defaults of interactive sessions, and changing it needs those sessions relaunched. Switching a seat profile changes only what `tillr-drain` dispatches on later passes; it writes nothing under the harnesses' own configuration directories.
- **Never a partial overlay.** Every profile maps every seat. A switch replaces the whole routing table at once, so no unmentioned seat keeps running the earlier profile's backend.

## 3. Where Named Profiles Live and How They Are Checked

### 3.1 One Canonical Seat Catalog (`config/tillr/seats.json`)

Which seats exist, which are read-only and how each is described in a worker's brief are properties of the repository's workflows, not of a provider or a person. They are declared once, in the operated repository's `config/tillr/seats.json` (`D-DSP1`):

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

The purposes are the sentences the policy files carry today. When the loader reads a policy for a repository that has a catalog, it takes `read_only_seats` and `seat_purposes` from the catalog, ignores the same blocks in the policy file, and requires the file's `seats` table to map **every** key of `seat_purposes`. A profile may map a seat the repository does not declare; the extra row is unused there and `tillr drain profile show` says so, which lets one person's profile serve several repositories whose catalogs differ. For a repository with no catalog, the policy file carries both blocks inline, as every policy file does today, so a policy written before this design keeps loading (`D-DSP1`).

### 3.2 Per-User Profile Directory (`~/.config/tillr/backend-policy.<name>.json`)

Profiles live in the person's own tillr configuration directory, outside every repository (`D-DSP10`, the owner's ruling; the directory lookup and file naming below are the designer's decisions in `D-DSP12` and `D-DSP18` from an operator request, which the owner may overturn). The directory is the first of these that applies:

1. **`$TILLR_CONFIG_DIR`**, when set and not empty. It must be absolute; a relative value is refused at startup, the way `TILLR_INSTALL_ROOT` is today (`ResolvePolicyAdapter`).
2. **`$XDG_CONFIG_HOME/tillr`**, when `XDG_CONFIG_HOME` is set to an absolute path.
3. **`$HOME/.config/tillr`**, on every operating system. A dotfiles repository and a pack both address a home-relative path, so the path does not vary by platform.

Inside it:

```text
~/.config/tillr/
  backend-policy.<name>.json   one seat profile per file
  drain.json                   optional: { "profile": "<name>" }, this person's default
```

`<name>` matches `[a-z0-9][a-z0-9_-]*` (non-empty lowercase ASCII letters, digits, hyphens and underscores, starting with a letter or digit, with no dots or path separators so `backend-policy.json` is not a named profile and path traversal is refused). Any other file in the directory is ignored when scanning for profile names (`list` reports malformed `backend-policy.*.json` filenames as ignored, never loaded). One file per profile lets a pack, a dotfiles diff and a hand edit each address one profile, matching the `backend-policy.<name>.json` convention operators already stage in `~/.config/tillr/` (`D-DSP18`). Tillr only reads this directory: it creates nothing in it, writes no lock or cache beside the files, and follows a symbolic link, so a read-only mount and a linked dotfile both work. Which profile a drain runs is stored elsewhere ([§4.1](#41-slotting-into-drain_runtime_config-rather-than-beside-it)).

A profile file has the policy file's structure and two optional metadata fields:

- `summary` *(optional, string)*: one line naming the harness and the model tiers, shown by `list` and by the observatory (`about` serves as the fallback summary when `summary` is omitted, so existing `backend-policy.<name>.json` files load unchanged without adding `summary`; `D-DSP2`, revised by `D-DSP18`).
- `hold` *(optional, `{ "reason": string, "depends_on": [string] }`)*: marks the profile **defined and held** (`D-DSP3`). A held profile still passes every structural, adapter, read-only, escalation and full-seat check whenever it is listed or shown. `tillr drain profile use <name>` and `tillr-drain loop --profile <name>` refuse it until its owner removes `hold` from the file.

A profile names each adapter by its built-in name (`run-pi`, `run-claude`, `run-agy` or `run-muse`), which `ResolvePolicyAdapter` resolves against the tillr installation, so the same file works on the host, in a jail and for any repository. A repository-relative adapter path still resolves against the operated repository's root (`resolveAdapterPath`) and so works only where that repository holds the adapter.

No gate reaches a per-user file, so a profile is validated in full at the moments tillr reads it (`D-DSP2`): when `tillr drain profile list`, `show` or `use` runs, when the loop starts, and at each pass for the profile in force. The repository's tests check the example file instead ([§3.4](#34-the-repositorys-example-file-and-what-a-fresh-clone-can-do-with-it)).

The baseline concurrency caps of [the live drain configuration](./tillr-live-drain-config.md#42-concurrency-limits-per-backend-per-seat-and-per-kind-of-work) (`backend_caps`, `seat_caps`, `work_caps`) may appear in a profile file, so a provider's ceilings travel with the profile they protect.

### 3.3 How a Profile Reaches a Machine: A Personal Pack or By Hand

The owner ruled that profiles need "a way to ship profiles from a personal pack into jails or with manual staging somewhere for other setups" (`D-DSP11`). Both paths end at the same directory, and tillr cannot tell them apart. What each needs from tillr is the designer's reading (`D-DSP13`).

**By a personal yolo pack.** A person's own pack, selected in their user-scope yolo configuration, carries the files and declares one contribution:

```json
{ "kind": "files", "from": "tillr", "into": ".config/tillr" }
```

with `tillr/backend-policy.<name>.json`, and optionally `tillr/drain.json`, in the pack. A pack selected at user scope applies to every workspace that person launches, so it reaches the jail that runs this repository's own loop the same way it reaches any other, and nothing in this repository names it. The tillr pack this repository ships (`yolo-packs/tillr`) delivers the programs and carries no profile.

Measured in this repository's jail on 2026-11-25 (yolo 0.19.0+237): `yolo pack --help` describes a `files` entry as "own a file tree, bind-mounted :ro in the jail"; a selected pack that declares `"into": ".config/tok-stats/config.json"` produced that file under `~/.config/tok-stats/`, readable by the jail's user; and the jail's storage listing gives `~/.config` as a per-workspace directory, so nothing placed there by hand in one jail appears in another. The same help text says a `files` entry is also placed at the host; whether the host copy is a link or a regular file was not measured, and tillr does not depend on it.

What the pack path needs from tillr:

- **A home-relative default path**, because a pack's `into` is relative to the home directory and the host's and the jail's home directories differ. `$HOME/.config/tillr` is one. `TILLR_CONFIG_DIR` is not needed by a pack and stays available to a test or an unusual setup.
- **Read-only use of the directory**, as [§3.2](#32-per-user-profile-directory-configtillrbackend-policynamejson) rules. A read-only mount is the reason the selection cannot be a file beside the profiles.
- **No absolute path inside a profile.** Built-in adapter names give that.
- **The selection stored by name, not by path.** A workspace's store is shared between the host and its jail, and the same profile sits at a different absolute path in each.
- **A reload that reads content, not a change notice.** `refreshPolicy` already hashes the file at each pass, so a pack update is taken at the next pass after the files change, whenever yolo applies it.
- Nothing else: no flag, no environment variable and no step in the loop's launch.

**By hand.** A setup without yolo creates the directory and puts files in it, by copying, by a dotfiles manager, or by a link:

```sh
mkdir -p ~/.config/tillr
cp config/tillr/backend-policy.json ~/.config/tillr/backend-policy.mine.json
# edit backend-policy.mine.json: remove "example" once D-DSP14 lands, put in models your credentials reach, optionally add a "summary"
tillr drain loop --profile mine --check
```

This path needs nothing from tillr beyond the lookup itself. Tillr asks nothing of the files' owner or mode beyond being readable, and `tillr drain profile list` prints the directory it read and how it arrived at it, so a person who put files in the wrong place sees that at once. A jail whose pack delivers `.config/tillr` has that directory read-only, so in such a jail the pack is the way to add a profile.

### 3.4 The Repository's Example File and What a Fresh Clone Can Do With It

`config/tillr/backend-policy.json` stays where it is, and its role is the specimen: a complete, valid policy with placeholder model names that shows every field and every adapter's row. It gains one field, `"example": true` (`D-DSP14`, the designer's), which makes its role a fact the loader can read instead of a sentence in its `about` text.

- **The repository's tests load it** with the real loader, so the specimen cannot fall behind the schema.
- **A fresh clone copies it** into the per-user directory as a first profile, as [§3.3](#33-how-a-profile-reaches-a-machine-a-personal-pack-or-by-hand) shows.
- **A read-only face accepts it when it is named.** `tillr drain why --policy config/tillr/backend-policy.json` plans against it and dispatches nothing, so a newcomer can see what the loop would do with a queue before choosing a model.
- **Nothing that dispatches runs on it.** The loop and the recovery command refuse a policy marked `example`, whether it was found by the lookup or named with `--policy`, and say that it is an example. A fresh clone with no per-user profile therefore gets the refusal of [§4.3](#43-what-the-loop-says-at-startup-and-when-it-refuses), which names the copy command, not a loop whose every run fails at its provider.

A repository that wants one policy for everyone who operates it may track a real one at the same path, without the `example` field. The lookup takes it last ([§4.1](#41-slotting-into-drain_runtime_config-rather-than-beside-it)).

## 4. Listing, Showing, and Selecting Profiles Inside Live Drain Config

### 4.1 Slotting Into `drain_runtime_config` Rather Than Beside It

[The live drain configuration](./tillr-live-drain-config.md#51-canonical-storage-in-tillrstatedb) (`D-LD6`) makes the table `drain_runtime_config` in `.tillr/state.db`, with its `drain.config-changed` events, the one store of runtime configuration across passes, restarts and relaunches by `tillr-keep`. That design sketches a path-only `policy_path` key, which is not built. Named profiles take that slot under the key `seat_profile` (`D-DSP4`):

```json
{ "profile": "copilot-gemini" }
```

The row holds the name and no path: the loader resolves the name in the per-user directory of the process that reads it, at startup and at every pass, because one workspace's store is read from the host and from its jail and the directory's absolute path differs between them. A file selected by path is stored in the same row as `{"profile": "", "path": "<resolved path>"}`. One key owns the selection, so a name and a path cannot disagree.

The loop and the recovery command share one resolver, and it takes the first rung that names something (`D-DSP15`, the designer's, amended by `D-DSP18` from an operator request, which the owner may overturn). `D-LD10` of the live drain configuration, which lets an explicit `--lanes` on a fresh start replace a saved lane count, [applies to the lane count only](./tillr-live-drain-config.md#54-startup-lane-count-reporting-and-fresh-start-flag-replacement).

1. **The `seat_profile` row written by an operator** (source `operator`, built in Slice 5). `tillr drain profile use` writes it; it survives `tillr-drain restart` and a relaunch by `tillr-keep` until `tillr drain config --reset` clears it. This rung follows [the live drain configuration's precedence](./tillr-live-drain-config.md#52-precedence-across-startups-tillr-drain-restart-and-tillr-keep), where a stored operator choice outranks a startup flag.
2. **The `--policy <path>` startup flag** (source `flag`). Names any policy file, as today, and **wins over `--profile <name>` and `[drain] profile` when both are given** (`D-DSP18`, from an operator request). When `--policy` is non-empty, the per-user profile directory is not consulted.
3. **The `--profile <name>` startup flag** (source `flag`). On `tillr-drain loop` (and `tillr drain loop`), selects `<config-dir>/backend-policy.<name>.json` from the per-user tillr configuration directory (`D-DSP18`).
4. **The repository's `[drain] profile` setting** (source `toml`). Optional `profile = "<name>"` under `[drain]` in `.tillr.toml` (`Settings.Drain.Profile` in `tools/tillr/settings`, validated against `[a-z0-9][a-z0-9_-]*` when `.tillr.toml` is read and appended as `drain.profile="<name>"` in `StartupLine()` when set), selecting `<config-dir>/backend-policy.<name>.json` when neither `--policy` nor `--profile` is given (`D-DSP18`).
5. **The person's default** (source `user-default`, Slice 2). The `profile` key of `drain.json` in the per-user directory.
6. **The only ready profile** (source `sole-profile`, Slice 2). When the per-user directory holds exactly one `backend-policy.<name>.json` that is valid and not held, it is taken, so a first profile placed by hand needs no second file. With two or more ready, none is taken unasked.
7. **The repository's own file** (source `repository`). `<root>/config/tillr/backend-policy.json`, when it exists (and, once Slice 2 lands, is not marked `example`).

Two rules govern the walk. A rung that names nothing (no row, no flag, no `.tillr.toml` key, no file) is skipped. A rung that names something unusable (an invalid profile name, a missing `backend-policy.<name>.json` file, or an invalid, held or example policy) **refuses** with exit 2; the resolver never falls through to a lower rung, because running a different policy from the one named is worse than not starting. In particular, when `--profile <name>` or `[drain] profile` names a valid `<name>` whose `backend-policy.<name>.json` file is absent from the resolved per-user directory, startup (and `--check`) refuses with exit 2, naming `<name>`, the directory searched, and the sorted list of `<name>` values whose `backend-policy.<name>.json` files exist in that directory (or stating that none exist when the directory is missing or holds none; `D-DSP18`).

No default profile name is compiled into the code. Optional `[drain] profile = "<name>"` in `.tillr.toml` (`D-DSP18`, the designer's decision from an operator request, which the owner may overturn) tracks no policy file or model id — the profile files themselves stay in the operator's per-user configuration directory under `D-DSP10`. Repositories shared across machines with different profile names (including this repository's `.tillr.toml`) leave `[drain] profile` unset; a consumer repository whose operators keep a shared profile name in their per-user directories sets `[drain] profile = "<name>"` instead of passing `--profile` on every launch.

### 4.2 Operator Command Surface (`tillr drain profile`)

`tillr drain profile` takes the place of the `tillr drain policy set <path>` face that [the live drain configuration](./tillr-live-drain-config.md#41-live-seat-to-backend-and-model-routing) sketches, and `tillr drain policy [--json]` stays as the reader of the accepted policy snapshot (`D-DSP4`):

```text
tillr drain profile                         # same as 'list'
tillr drain profile list [--json]           # the directory read, then every profile: active, ready, held or invalid, and its summary
tillr drain profile show [<name>] [--json]  # the active profile, or <name> even if held, in full
tillr drain profile use <name>              # validate, refuse if held, switch in state.db
tillr drain profile use --file <path>       # a policy file outside the per-user directory
```

```text
profiles in /home/agent/.config/tillr (from $HOME)
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

### 4.3 What the Loop Says at Startup and When It Refuses

In Slice 1 (`D-DSP18`, from an operator request), when `--profile <name>` or `[drain] profile` selects a `<name>` with no `backend-policy.<name>.json` in the per-user configuration directory, `tillr-drain loop` (and `--check`) exits 2 naming `<name>`, the directory searched, and the sorted profile names that exist there (or stating that none exist when the directory is missing or contains no `backend-policy.<name>.json` files).

In Slice 2 (`D-DSP16`, the designer's), the loop adds a startup line on standard error beside the repository settings and gate policy lines (`StartupLine`), naming the file it took, the profile's name when it has one, the rung that chose it, and the rungs above that named nothing:

```text
tillr-drain loop: backend policy: profile "copilot-gemini" (/home/agent/.config/tillr/backend-policy.copilot-gemini.json)
  chosen by: the person's default, /home/agent/.config/tillr/drain.json
  skipped:   no stored selection; no --policy; no --profile; no [drain] profile
```

The same facts go into the policy snapshot ([§6](#6-how-the-operator-and-owner-see-which-profile-is-active)), so they can be read back after the terminal is gone. `tillr-drain loop --check` runs the resolver and prints the line without starting a pass.

When no rung yields a usable policy once Slice 2 lands, the loop does not start. It exits 2 with every rung, what the rung found and why that was not taken, then the ways out:

```text
tillr-drain loop: no usable backend policy. Looked, in order:
  1. stored selection (seat_profile):  none
  2. --policy:                         not given
  3. --profile:                        not given
  4. .tillr.toml ([drain] profile):    not set
  5. the person's default:             /home/agent/.config/tillr/drain.json: no such file
  6. per-user profiles:                /home/agent/.config/tillr (from $HOME; TILLR_CONFIG_DIR and XDG_CONFIG_HOME unset)
       bedrock     ready
       openrouter  ready
       claude-sub  held: Held until the Claude backend is proven and the owner asks to switch.
       scratch     invalid: seats: no backend named "pi-large"
     2 ready, so none is taken unasked
  7. the repository's file:            /workspace/config/tillr/backend-policy.json: an example ("example": true)
To start: pass --profile <name> or --policy <path>, set [drain] profile in .tillr.toml, run 'tillr drain profile use <name>',
or copy the example to /home/agent/.config/tillr/backend-policy.<name>.json and put in your models.
```

A refusal caused by a rung that named something unusable prints the same list, with that rung marked as the one that stopped the walk and the rungs below it shown as not consulted. A per-user directory that does not exist is reported as such with its path; it is not an error on its own.

## 5. Switching on a Running Drain: Pass Boundaries and Runs in Flight

### 5.1 Zero-Restart Reload at the Next Pass Boundary

A switch never needs `tillr-drain loop` or `tillr-keep` restarted (`D-DSP5`):

1. `tillr drain profile use <name>` commits `seat_profile` and appends `drain.config-changed`.
2. The loop's store trigger (`waitForTrigger`) wakes an idle loop, or the loop sees the event when its current pass ends.
3. At the top of the pass, `refreshPolicy` reads the effective `seat_profile`, resolves the name in the per-user directory, loads and validates the profile, replaces the policy in memory, and writes the accepted snapshot to `.tillr/loop/backend-policy.json` and a line to `.tillr/loop/backend-policy-changes.jsonl`. It resets the share epoch (`D-LD7` of [the live drain configuration](./tillr-live-drain-config.md#41-live-seat-to-backend-and-model-routing)) of any seat whose proportional shares changed.
4. If the active profile's file is later edited into an invalid state, or removed (a pack that no longer ships it), `refreshPolicy` keeps the accepted policy running, records the refusal and the file's hash in the snapshot, and the `backend-policy` row that needs a person appears. No run in flight is dropped.

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
2. **`tillr drain profile` and `tillr drain profile show`** mark the active profile and print where the selection came from (`operator`, `flag`, `toml`, `user-default`, `sole-profile` or `repository`), the accepted hash, the full routing table, the escalation order and the effective caps.
3. **`tillr drain config` and `tillr drain policy`** include `profile`, `path`, `source`, `hash` and `refusal` in both outputs. `tillr-keep` reads `tillr drain config --json` on a relaunch.
4. **The observatory header and the cards that need a person.** The view's snapshot endpoint exposes `policy.profile`, `policy.source`, `policy.summary` and `policy.refusal`; `ObservatoryHeaderBar` shows the profile name beside the loop's state and lane counts, with the summary on opening it. When an edit to the active profile fails validation, the existing `backend-policy` card names the profile.

## 7. The First Two Named Profiles

Both are the owner's own profiles and belong in the owner's own configuration, not in this repository (`D-DSP10`); this section records what they hold so the build can be checked against them. The repository's code and tests name neither.

**Invariant.** Building this design does not change which backends the owner's drain runs. `copilot-gemini` is selected; `claude-sub` is defined and **held** (`D-DSP8`). Selecting `claude-sub` needs the Claude backend proven and then the owner removing `hold` and running `tillr drain profile use claude-sub`.

### 7.1 `copilot-gemini` — Active Copilot Gemini Profile (Selected)

`backend-policy.copilot-gemini.json` carries the assignment of the owner's Copilot Gemini policy, without the `read_only_seats` and `seat_purposes` blocks once this repository has a catalog. Every row runs the adapter `run-pi` at effort `medium`. The default backend is `pi-build`, and `build` escalates from `pi-build` to `pi`.

| Seats | Backend | Model route |
| :--- | :--- | :--- |
| `design` | `pi-designer` | `github-copilot/gemini-4-carbon` |
| `plan`, `review`, `verify`, `tricky` | `pi` | `github-copilot/gemini-4-neon` |
| `build`, `edit`, `test-write`, `docs` | `pi-build` | `github-copilot/gemini-4-hydrogen` |
| `scout`, `classify`, `test-run` (read-only) | `pi-scout` | `github-copilot/gemini-3.8-flash` |

### 7.2 `claude-sub` — Claude Subscription Profile (Defined and Held; NOT Selected)

`backend-policy.claude-sub.json` puts the strongest model of the Claude subscription on `design` alone and spreads every other seat across the two cheaper ones. The owner's earlier Claude policy (no longer tracked here), by contrast, runs every seat but `design` on one model. Every row runs the adapter `run-claude`, which [the Claude backend reference](../reference/tillr-claude-backend.md) describes. The profile carries:

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

[The convergence design](./tillr-convergence.md#35-a-seat-names-its-harness-and-the-harness-names-the-model) ([OQ-TC2](./tillr-convergence.md#OQ-TC2), a designer's decision) treats the policy files as a stopgap on the way to one tracked map from seat to harness in which tillr's files name no model, each harness's own profile on the machine choosing it. It rejects an untracked per-machine file. This design revises that target, takes the per-user file the owner's ruling of 2026-11-24 asks for (`D-DSP10`), and keeps the target's two invariants (`D-DSP9`).

**Why a model-free map is not the target.** A host's agent profile picks one default model for an interactive session. A drain routes eleven seats across three model tiers inside one harness, and switches harness and tiers together, as one named unit, on one machine, without rewriting host configuration or relaunching a session. A named bundle of harness and model tiers is therefore a lasting control.

**What stays true.**

- **No model in code or in the repository.** The `tools/tillr*` programs and the adapters name no model, and the one tracked policy is an example with placeholders; every real model route is data in a person's profile.
- **No repeated seat definitions.** The catalog of [§3.1](#31-one-canonical-seat-catalog-configtillrseatsjson) removes the repeated blocks, which is the rule of [the convergence design](./tillr-convergence.md#21-a-derived-copy-is-checked-instead-of-derived).
- **A machine's selection stays out of tracked files.** A person's configuration holds the profiles; each workspace's untracked store holds which one its drain runs. Two machines with different credentials never change each other's selection by pulling.

Once the lookup of [§4.1](#41-slotting-into-drain_runtime_config-rather-than-beside-it) is built, [OQ-TC2](./tillr-convergence.md#OQ-TC2) names `D-DSP9` as the decision that replaces it.

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

## Decision Ledger

`D-DSP8`, `D-DSP10` and `D-DSP11` are the owner's; `D-DSP10` and `D-DSP11` quote the owner's dated words verbatim. Every other row (including `D-DSP18`, recorded from an operator request) is the designer's decision, not an owner ruling; the owner has not ruled on it and may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-DSP1` | `read_only_seats` and `seat_purposes` are declared once, in the operated repository's `config/tillr/seats.json`. A policy read for a repository with a catalog takes both from it and must map every declared seat; it may map seats the repository does not declare. With no catalog, the policy carries both blocks inline, as today. | 2026-11-19, designer; revised 2026-11-25 | [§3.1](#31-one-canonical-seat-catalog-configtillrseatsjson) | — |
| `D-DSP2` | A named profile is one file `backend-policy.<name>.json` in the per-user tillr configuration directory, with an optional `summary` (`about` serves as fallback when `summary` is omitted; amended 2026-11-26 by `D-DSP18` from an operator request). It is validated in full when tillr reads it (by `list`, `show`, `use`, at startup and at each pass), not by a repository's gate, which cannot reach it. `--policy <path>` keeps working for any file. | 2026-11-19, designer; revised 2026-11-25, 2026-11-26 | [§3.2](#32-per-user-profile-directory-configtillrbackend-policynamejson) | — |
| `D-DSP3` | A profile may carry a `hold` block. `tillr drain profile use` and `tillr-drain loop --profile` refuse a held profile with exit 2, while the checks still validate it in full. | 2026-11-19, designer | [§3.2](#32-per-user-profile-directory-configtillrbackend-policynamejson), [§4.2](#42-operator-command-surface-tillr-drain-profile) | — |
| `D-DSP4` | The selection is the `seat_profile` row of `drain_runtime_config`, holding the profile's name and no path. `tillr drain profile` with `list`, `show` and `use` takes the place of the path-only `tillr drain policy set <path>`. | 2026-11-19, designer; revised 2026-11-25 | [§4.1](#41-slotting-into-drain_runtime_config-rather-than-beside-it), [§4.2](#42-operator-command-surface-tillr-drain-profile) | — |
| `D-DSP5` | A switch needs no restart of the loop or its keeper: `drain.config-changed` wakes the loop, `refreshPolicy` replaces the profile at the next pass boundary and resets changed share epochs, and an invalid later edit leaves the accepted policy running. | 2026-11-19, designer | [§5.1](#51-zero-restart-reload-at-the-next-pass-boundary) | — |
| `D-DSP6` | A switch leaves running processes alone, resolves unclaimed workflow nodes against the new profile, starts fresh worker and designer sessions and never resumes one across harnesses or models, scopes `drain.seat-escalated` by profile, and moves a quota-paused run onto the new profile's open backend when its own is absent or not first. | 2026-11-19, designer | [§5.2](#52-exact-behavior-of-work-and-sessions-in-flight) | — |
| `D-DSP7` | The policy snapshot records `profile` and `source`, and `tillr status`, `tillr drain profile`, `tillr drain config`, `tillr drain policy`, the observatory header and the `backend-policy` refusal card show the active profile. | 2026-11-19, designer | [§6](#6-how-the-operator-and-owner-see-which-profile-is-active) | — |
| `D-DSP8` | **Owner direction.** Two first profiles: `copilot-gemini`, the assignment of the owner's Copilot Gemini policy, kept selected; and `claude-sub`, the subscription's strongest model on `design` and its two cheaper models on every other seat, defined and held until the Claude backend is proven and the owner asks to switch. Where the two files live follows `D-DSP10`. | 2026-11-19, owner | [§7](#7-the-first-two-named-profiles) | — |
| `D-DSP9` | Revises [OQ-TC2](./tillr-convergence.md#OQ-TC2): a named profile of harness and model tiers is a lasting control, not a stopgap on the way to a model-free seat map, and it is a per-user file, which that decision had rejected. The seat catalog removes the repeated blocks, and the untracked store keeps each machine's selection local. | 2026-11-19, designer; revised 2026-11-25 | [§8](#8-relation-to-the-per-machine-backend-policy-stopgap) | — |
| `D-DSP10` | **Owner ruling.** Owner, 2026-11-24: these seat profiles are "personal preference" and belong with a person's own configuration, not in this repository. The designer reads it as: profiles are a person's own files and no repository tracks one. | 2026-11-24, owner | [§2](#2-boundary-and-vocabulary-what-a-seat-profile-is-and-is-not), [§3.2](#32-per-user-profile-directory-configtillrbackend-policynamejson) | Partly: the owner's policies left this repository on 2026-11-24; no per-user location is read |
| `D-DSP11` | **Owner ruling.** Owner, 2026-11-24: "we also need to have a way to ship profiles from a personal pack into jails or with manual staging somewhere for other setups". | 2026-11-24, owner | [§3.3](#33-how-a-profile-reaches-a-machine-a-personal-pack-or-by-hand) | — |
| `D-DSP12` | The per-user directory is `$TILLR_CONFIG_DIR` (absolute, else refused), else `$XDG_CONFIG_HOME/tillr` (when `XDG_CONFIG_HOME` is absolute), else `$HOME/.config/tillr` on every platform. It holds `backend-policy.<name>.json` (amended 2026-11-26 by `D-DSP18` from an operator request) and an optional `drain.json` naming the person's default. Tillr only reads it. | 2026-11-25, designer; revised 2026-11-26 | [§3.2](#32-per-user-profile-directory-configtillrbackend-policynamejson) | — |
| `D-DSP13` | A personal pack delivers profiles with one `files` contribution into `.config/tillr`; a setup without yolo places the files by hand. From tillr the pack path needs a home-relative default, read-only use, built-in adapter names, a selection stored by name and a reload by content; the hand path needs nothing more. The tillr pack carries no profile. | 2026-11-25, designer | [§3.3](#33-how-a-profile-reaches-a-machine-a-personal-pack-or-by-hand) | — |
| `D-DSP14` | `config/tillr/backend-policy.json` is the specimen and carries `"example": true`. Tests load it, a fresh clone copies it, a read-only face accepts it when named, and the loop and the recovery command refuse to dispatch on it. A repository may track a real policy at that path. | 2026-11-25, designer | [§3.4](#34-the-repositorys-example-file-and-what-a-fresh-clone-can-do-with-it) | — |
| `D-DSP15` | Lookup order (amended 2026-11-26 by `D-DSP18` from an operator request): the stored selection (`seat_profile`), then `--policy <file>` (which wins when `--profile` or `[drain] profile` is also given), then `--profile <name>`, then optional `[drain] profile` in `.tillr.toml`, then the person's `drain.json`, then the only ready per-user profile, then the repository's own file. A rung that names something unusable refuses; the walk never falls through it. No default profile name is compiled into the code. | 2026-11-25, designer; revised 2026-11-26 | [§4.1](#41-slotting-into-drain_runtime_config-rather-than-beside-it) | — |
| `D-DSP16` | At startup (Slice 2) the loop prints the file it took, the profile's name, the rung that chose it and the rungs skipped. With no usable policy it exits 2 and lists every rung with what it found. | 2026-11-25, designer | [§4.3](#43-what-the-loop-says-at-startup-and-when-it-refuses) | — |
| `D-DSP17` | The former third question, on which of the owner's other policies this repository would carry as tracked profiles, is withdrawn by the designer: after `D-DSP10` the repository carries none, and which policies a person keeps as profiles is that person's own. | 2026-11-25, designer | [§7](#7-the-first-two-named-profiles) | n/a |
| `D-DSP18` | **From an operator request; the designer's decision, not an owner ruling (the owner may overturn it).** Amends `D-DSP2`, `D-DSP12` and `D-DSP15`: named profile files live directly in the per-user tillr configuration directory (`$TILLR_CONFIG_DIR` when set and non-empty — absolute, else refused; else `$XDG_CONFIG_HOME/tillr` when absolute; else `$HOME/.config/tillr`) as `backend-policy.<name>.json` (`<name>` matching `[a-z0-9][a-z0-9_-]*`, with `summary` optional so existing policy files load unchanged). `tillr-drain loop` (and `tillr drain loop`) gains `--profile <name>`, and `.tillr.toml` gains optional `[drain] profile = "<name>"` (`Settings.Drain.Profile` in `tools/tillr/settings`, validated against `[a-z0-9][a-z0-9_-]*` and appended as `drain.profile="<name>"` in `StartupLine()` when set). Precedence: non-empty `--policy <file>` wins when given (including when `--profile <name>` and/or `[drain] profile` is also given, without consulting the per-user directory); else `--profile <name>` selects `<config-dir>/backend-policy.<name>.json`; else `[drain] profile` in `.tillr.toml` selects `<config-dir>/backend-policy.<name>.json`; else `<root>/config/tillr/backend-policy.json` (with `drain.json`, `sole-profile` and `"example": true` added in Slice 2). When `--profile <name>` or `[drain] profile` names a valid `<name>` whose `backend-policy.<name>.json` does not exist in `<config-dir>`, startup (and `--check`) exits 2 naming `<name>`, `<config-dir>`, and the sorted profile names that exist there (or that none exist). **Rejected:** `profiles/<name>.json` subdirectory; requiring `summary` to load a profile; exiting 2 when both `--policy` and `--profile` are passed; forbidding `[drain] profile` in `.tillr.toml`. | 2026-11-26, designer, from an operator request | [§3.2](#32-per-user-profile-directory-configtillrbackend-policynamejson), [§4.1](#41-slotting-into-drain_runtime_config-rather-than-beside-it), [§4.3](#43-what-the-loop-says-at-startup-and-when-it-refuses) | — |

## Build Slices

Each slice is one landing with its tests. Slice 1 answers the filed operator request (`D-DSP18`) and is useful alone; the rest follow in order, and Slice 3 may land beside any of them.

1. **Choosing a profile by name (`--profile <name>` and `[drain] profile`, `D-DSP18`).** Resolve the per-user tillr configuration directory (`D-DSP12`), add `[drain] profile = "<name>"` to `tools/tillr/settings` and `--profile <name>` to `tillr-drain loop`, keep `--policy <file>` winning when both are given, select `<config-dir>/backend-policy.<name>.json` when `--profile` or `[drain] profile` is set, keep `<root>/config/tillr/backend-policy.json` when none is set, and refuse a missing `<name>` with exit 2 listing the sorted `backend-policy.<name>.json` names that exist in `<config-dir>`. Profiles are complete policy files as today, so nothing waits on the catalog. Tests: `--profile <name>`, `[drain] profile` in `.tillr.toml`, `--profile` overriding `[drain] profile`, `--policy <file>` winning when given alongside `--profile` and/or `[drain] profile`, directory override order (`$TILLR_CONFIG_DIR`, `$XDG_CONFIG_HOME/tillr`, `$HOME/.config/tillr`, and relative `$TILLR_CONFIG_DIR` refusal), invalid `<name>` refusal, and missing-profile refusal listing existing profile names.
2. **Unasked fallback rungs, startup report, and `"example": true` (`D-DSP14`–`D-DSP16`).** Share the resolver with `recovery` / `examine`, add `drain.json` (`user-default`) and `sole-profile` rungs between `[drain] profile` and `<root>/config/tillr/backend-policy.json`, add `"example": true` with the refusal to dispatch on it (`D-DSP14`) and mark the tracked example, print the startup policy line and multi-rung refusal list (`D-DSP16`), and add `profile` and `source` on `PolicyState`. Tests: each fallback rung, the example refused by the loop and accepted by `tillr drain why`, and a read-only per-user directory.
3. **The documents that state the old default.** `README.md` (the `--policy ~/.config/tillr/my-policy.json` walkthrough becomes `backend-policy.<name>.json` with `--profile`), [the live drain configuration](./tillr-live-drain-config.md), the `tillr-drain` utility contract, the adapters' READMEs under `tools/tillr-run/backends/`, [OQ-TC2](./tillr-convergence.md#OQ-TC2) (`D-DSP9`), and the flag help in `tools/tillr-drain`. The example's backend rows change to built-in adapter names so a copy works outside this repository.
4. **`tillr drain profile list` and `show`.** Read-only: the directory and how it was arrived at, every profile's state (`active`, `ready`, `held`, `invalid`) and `summary` (falling back to `about` when omitted), with `hold` parsed (`D-DSP2`, `D-DSP3`). `--profile` and `[drain] profile` refuse a held profile from this slice on.
5. **The stored selection.** The `seat_profile` row, `tillr drain profile use` and `use --file`, rung 1 of the lookup, the reload at a pass boundary and the behavior of work in flight (`D-DSP4` to `D-DSP6`). This slice depends on the runtime configuration table of [the live drain configuration](./tillr-live-drain-config.md#51-canonical-storage-in-tillrstatedb).
6. **The seat catalog.** `config/tillr/seats.json`, the loader taking both blocks from it, and the full-seat check against it (`D-DSP1`).
7. **The read surfaces.** `tillr status`, `tillr drain config`, `tillr drain policy`, the observatory header and the `backend-policy` card show the profile and its source (`D-DSP7`).

Not a slice of this repository: the owner's personal pack with `copilot-gemini` and the held `claude-sub` (`D-DSP8`), which the owner keeps where `D-DSP10` puts it. It can be written as soon as slice 1 lands.
