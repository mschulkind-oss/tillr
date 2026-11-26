---
title: "Installing tillr without a yolo jail"
status: draft
stage: DESIGN
owners: [owner]
summary: "Install tillr on a plain Linux or macOS host via prebuilt GitHub Release archives consumed by a Homebrew formula and a POSIX install script, backed by a single <prefix>/bin and <prefix>/share/tillr layout with executable-relative discovery, embedded default workflows and watch rules, and zero Python or Node build requirements at install time."
---

# Installing tillr without a yolo jail

**Status:** Draft (`DESIGN`) — 2026-11-26.
**Needs your ruling:** [OQ-HI1](#OQ-HI1), [OQ-HI2](#OQ-HI2), and [OQ-HI3](#OQ-HI3) below, plus the designer's decisions `D-HI1` through `D-HI9` in the [Decision Ledger](#decision-ledger) that you may overturn.

---

## 1. Problem, Standing Direction, and Current Delivery

### 1.1 Standing direction

On 2026-11-25 (`R-HI1`), the owner asked for an installation path outside a `yolo` jail, ranked below open defects:

> "Just a way to install it somehow. This may just be homebrew and or other things."

### 1.2 Why using `tillr` outside a jail hurts today

Today the only packaged delivery of `tillr` is [`yolo-packs/tillr/`](../../yolo-packs/tillr/README.md). Inside a `yolo` jail, [`pack.json`](../../yolo-packs/tillr/pack.json) places source snapshots under `/usr/local/share/tillr/` and installs eleven shell launchers (`tillr`, `tillr-drain`, `tillr-gate`, `tillr-item`, `tillr-land`, `tillr-loop`, `tillr-run`, `tillr-store`, `tillr-trace`, `tillr-view`, `tillr-watch`) that hash the mirrored Go source, compile each binary on first call into a durable cache, and export `TILLR_INSTALL_ROOT=/usr/local/share/tillr` and `TILLR_LAUNCHER` before `exec` ([`yolo-packs/tillr/programs/tillr/run`](../../yolo-packs/tillr/programs/tillr/run); see [`tillr-distribution.md`](../reference/tillr-distribution.md#build-and-delivery)).

Outside a jail, none of that machinery exists (`fd 'goreleaser|release\.yml|Formula|install\.sh' .github tools` returns nothing at `bc431c2541fd`; [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) runs only `just check-ci`). A person on a laptop or server must follow the manual source bootstrap in [`docs/guide.md` §6](../guide.md#6-walkthrough-from-a-fresh-clone-to-your-first-landed-item):

1. Install Go (`1.26.0`), `just`, Python 3, Git, and — if building the web observatory or `tillr-pi` — Node.js `>= 22.19` and `npm`.
2. Clone `mschulkind-oss/tillr`, build [`tools/tillr-view/web`](../../tools/tillr-view/web/) with `npm ci && npm run build`, and run `TILLR_BUILD_UNPROVED=HEAD just tillr-build` with the build scratch directory variable set ([`.justfile`](../../.justfile) `tillr-build`, which also omits `tillr-watch`).
3. Keep the `tillr` source checkout on disk and point backend policy JSON files at clone paths (`adapter`, `brief_template`), or pass `--workflow single-worker@v1` because `planned-build@v1` and its five sibling workflow graphs are read from `<operated-repo>/config/tillr/workflows/` rather than from `tillr`'s installation.
4. If using the `pi` backend, separately clone and build `mschulkind-oss/tillr-pi` (`main` branch; [`D-HI9`](#D-HI9), designer's decision from a filed operator request, which the owner may overturn) so `pi --features` reports `parked-tool-calls`.

An installation strategy for plain hosts has one job: let a person install prebuilt `tillr` programs and their runtime files onto `PATH` in one command, run `tillr` inside any repository without keeping a `tillr` source clone or Go/Node/Python build toolchains around, and upgrade cleanly in place.

---

## 2. Comparing the Installation Routes

All candidate routes have to deliver **more than one Go binary**: `tillr` comprises eleven compiled Go executables (one with compiled web assets embedded), shell and TypeScript backend adapter sidecars under `tillr-run/backends/`, `tillr-trace/bash-shim.sh`, default workflows and watch rules, and specimen configuration ([§3](#3-what-an-installed-tillr-needs-beside-its-eleven-programs)). That shape decides which channels fit and which fight the tool.

### 2.1 Candidate routes compared

| Route | End-user prerequisites | Delivers all 11 binaries + sidecars + web UI? | Upgrade path | Verdict (`D-HI2`, designer's decision) |
| :--- | :--- | :--- | :--- | :--- |
| **1. GitHub Release archives** (`tillr_<ver>_<os>_<arch>.tar.gz` + `checksums.txt`) | `tar` + `curl` (or browser) | **Yes** — CI builds web UI once and compiles static `CGO_ENABLED=0` Go binaries alongside `share/tillr/` | Re-extract or let Homebrew / `install.sh` consume the new release | **Build first as the shared binary substrate** ([OQ-HI1](#OQ-HI1)). Every binary installer below consumes these archives instead of rebuilding from source. |
| **2. Homebrew formula / tap** (`brew install`) | Homebrew (macOS or Linuxbrew) | **Yes** — installs prebuilt archive into `Cellar/tillr/<ver>/{bin,share/tillr}` and symlinks `bin/tillr*` | `brew upgrade tillr` | **Primary route for macOS and Homebrew-on-Linux users** ([OQ-HI1](#OQ-HI1)). Best upgrade, uninstall, and dependency UX where `brew` is installed. |
| **3. POSIX install script** (`curl -fsSL …/install.sh \| sh`) | POSIX `sh`, `curl` (or `wget`), `tar`, `sha256sum` / `shasum` | **Yes** — verifies `checksums.txt` and installs the release archive into `~/.local/{bin,share/tillr}` (or `$TILLR_PREFIX`) | Re-run `install.sh` (or `install.sh --version <ver>`) | **Primary route for plain Linux servers, WSL2, containers, and machines without Homebrew or root.** Zero language-toolchain requirement. |
| **4. Local checkout prefix build** (`just install PREFIX=~/.local`) | Local clone + Go `1.26` + `just` (+ Node if rebuilding `web/dist`) | **Yes** — installs into the exact same `<prefix>/{bin,share/tillr}` layout | `git pull && just install` | **Build first as the local foundation and testbed.** Proves prefix layout and executable-relative discovery before CI release plumbing exists. |
| **5. `go install pkg@version`** | Go `1.26+` on every target host | **No** — three structural blockers ([§2.2](#22-why-go-install-is-rejected-as-a-primary-route)) | 11 separate `go install` invocations | **Rejected as a primary user route.** |
| **6. `mise` / `ubi` / `aqua`** | `mise`, `ubi`, or `aqua` | **Partial unless archive layout matches** — single-binary extractors miss `share/tillr/` unless configured for full prefix extraction | `mise upgrade` | **Free secondary compatibility** once Release archives use standard `<os>_<arch>.tar.gz` naming; not a replacement for Homebrew or `install.sh`. |
| **7. Nix flake (`nix profile install`)** | Nix with flakes enabled | **Yes** | `nix profile upgrade` | **Defer.** High maintenance (`vendorHash` + `npmDepsHash` churn across 13 Go modules and `tillr-view/web`) for a small audience; easy to add externally once `just install` exists. |
| **8. `.deb` / `.rpm` / `apt` / `dnf` repositories** | Root (`sudo`) on Debian/Fedora | **Yes** (`/usr/bin` + `/usr/share/tillr`) | `apt upgrade` / `dnf upgrade` | **Defer.** Requiring `sudo` hurts per-user server and laptop setups, and `install.sh` into `~/.local` covers every Linux distribution at once. |
| **9. `yolo-packs/tillr`** | `yolo` jail | **Yes** (source-mirror + lazy build) | `just pack-sync` + jail rebuild | **Keep unchanged for jails.** |

### 2.2 Why `go install` is rejected as a primary route

**Designer's decision (`D-HI2`); the owner may overturn this.** `go install github.com/mschulkind-oss/...@vX.Y.Z` fails for `tillr` on four independent counts:

1. **Thirteen cross-linked `go.mod` files with local `replace` directives.** Every module under [`tools/`](../../tools/) declares `module github.com/mschulkind-oss/<name>` (a module path that does not match its `tools/<name>` subdirectory in `mschulkind-oss/tillr`) and uses `replace github.com/mschulkind-oss/... => ../...` against `v0.0.0` ([`tools/tillr-drain/go.mod`](../../tools/tillr-drain/go.mod), [`tools/tillr-cli/go.mod`](../../tools/tillr-cli/go.mod)). The Go command unconditionally refuses `go install pkg@version` whenever the target `go.mod` contains `replace` directives. Supporting remote `go install` would require either collapsing all thirteen modules into one root `go.mod` or publishing tagged pseudo-versions across thirteen subdirectories in lockstep on every commit.
2. **Eleven binaries instead of one.** `go install` compiles one `main` package at a time into `$GOBIN`; a user would either run eleven `go install` commands or `tillr` would have to be refactored into a single multicall binary with post-install link creation.
3. **Unshipped runtime sidecars.** `go install` copies only compiled binaries into `$GOBIN`. It has no way to place `tillr-run/backends/*`, `pi/*.ts` extensions, or `tillr-trace/bash-shim.sh` onto disk, forcing runtime extraction of executable scripts into `/tmp` (which fails on `noexec` `/tmp` mounts).
4. **Unbuilt observatory web UI.** [`tools/tillr-view`](../../tools/tillr-view/webassets_on.go) embeds `web/dist/*` only when `web/dist` has already been built by `npm run build` and `-tags observatory` is passed ([`webassets_off.go`](../../tools/tillr-view/webassets_off.go)); `web/dist/` is gitignored, so `go install` would ship a `tillr-view` binary whose browser UI returns 404.

Prebuilt release archives consumed by Homebrew and `install.sh` avoid every one of those costs: CI runs `npm ci && npm run build` and `CGO_ENABLED=0 go build` once per release, and users need neither Go nor Node to install `tillr`.

---

## 3. What an Installed `tillr` Needs Beside Its Eleven Programs

At `bc431c2541fd`, a complete `tillr` installation consists of eleven Go binaries (`tillr` built from [`tools/tillr-cli`](../../tools/tillr-cli/), `tillr-drain`, `tillr-gate`, `tillr-item`, `tillr-land`, `tillr-loop`, `tillr-run`, `tillr-store`, `tillr-trace`, `tillr-view` compiled with `-tags observatory`, and `tillr-watch`) plus five categories of supporting files and external tools. All eleven Go binaries compile with `CGO_ENABLED=0` because SQLite uses pure-Go `modernc.org/sqlite` ([`tools/tillr/go.mod`](../../tools/tillr/go.mod)).

| Need beside the 11 binaries | Where it lives in the repo today | How an outside-jail install gets it (`D-HI1`, `D-HI3`, `D-HI4`) |
| :--- | :--- | :--- |
| **Observatory web assets** | [`tools/tillr-view/web/`](../../tools/tillr-view/web/) (`npm run build` -> `web/dist/`) | Built once in release CI and embedded inside the `tillr-view` binary via `-tags observatory` ([`webassets_on.go`](../../tools/tillr-view/webassets_on.go)). |
| **Backend adapters & helper files** | [`tools/tillr-run/backends/`](../../tools/tillr-run/backends/) + [`tools/tillr-trace/bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh) | Shipped read-only under `<prefix>/share/tillr/tillr-run/backends/` and `<prefix>/share/tillr/tillr-trace/bash-shim.sh`. |
| **Default workflows (`*@v1`)** | [`config/tillr/workflows/`](../../config/tillr/workflows/) (6 JSON graphs) + built-in `single-worker@v1` | Embedded into `tillr` via `//go:embed` (`D-HI3`), layered beneath optional `<repo>/config/tillr/workflows/<ref>.json` overrides. |
| **Default watch rules** | [`config/tillr/watch/`](../../tools/tillr-watch/rules/) (32 core/charter `.toml` files) | Embedded into `tillr-watch` via `//go:embed` under [`tillr-watch.md` `D-TW83`–`D-TW88`](./tillr-watch.md#43-shipped-rules-pack-delivery-and-repository-overrides-d-tw83-to-d-tw87), layered beneath `<repo>/config/tillr/watch/`. |
| **User seat profiles & specimen policy** | `~/.config/tillr/backend-policy.<name>.json` ([`tillr-seat-profiles.md`](./tillr-seat-profiles.md)) + [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) | Specimen shipped at `<prefix>/share/tillr/examples/backend-policy.json`; user keeps real profiles in `~/.config/tillr/backend-policy.<name>.json`. |
| **Operator / design agent skills** | [`yolo-packs/tillr/skills/`](../../yolo-packs/tillr/skills/) (`tillr-operating`, `tillr-designs`) | Shipped at `<prefix>/share/tillr/skills/` so a host user can symlink them into `~/.claude/skills/` or `~/.agents/skills/`. |
| **Agent harness (`claude` or `pi`)** | External to `tillr` (`claude` CLI or `mschulkind-oss/tillr-pi` / `pi`) | Installed and authenticated by the user (`claude` for `run-claude`; `tillr-pi` or compatibility-mode `pi` for `run-pi`, [§3.3](#33-the-patched-pi-fork-and-coding-agent-harnesses) & [OQ-HI2](#OQ-HI2)). |
| **OS tools (`git`, `/bin/sh`, optional `bash`)** | Host OS | Present on standard Linux and macOS hosts (`timeout` and `python3` removed as runtime needs; [§3.5](#35-remaining-external-interpreters-and-os-tools) & [§4](#4-how-this-design-depends-on-queued-python-removal-and-embedded-watch-rules)). |

### 3.1 Backend adapters and their helper files

**Designer's decision (`D-HI4`); the owner may overturn this.** `tillr-run` executes worker turns through POSIX shell adapters and helper files under [`tools/tillr-run/backends/`](../../tools/tillr-run/backends/), plus the command-tracing shim in [`tools/tillr-trace/bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh):

- **Shared files:** [`brief-template.md`](../../tools/tillr-run/backends/brief-template.md) (rendered by `tillr-run` when staging a worker's brief) and [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh) (sourced by every built-in `run-*` adapter).
- **Per-harness directories (`claude/`, `pi/`, `agy/`, `muse/`):** each holds `run-<backend>`, `ending-capability`, `pause-kind`, and `extract-report` (thin `exec tillr-run extract-report --format <fmt>` shims).
- **`pi/` helpers and extensions:** `pi-harness.sh`, `pi-agent-dir.sh`, and the four TypeScript extension files loaded directly by `pi` (`keep-going.ts`, `land.ts`, `no_change.ts`, `obstacle.ts`).
- **Bash tracing shim:** [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) and [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) resolve `bash-shim.sh` at `$SELF_DIR/../../../tillr-trace/bash-shim.sh`.

Shipping those files verbatim under `<prefix>/share/tillr/tillr-run/backends/` and `<prefix>/share/tillr/tillr-trace/bash-shim.sh` preserves the exact three-parent relative path between `tillr-run/backends/<name>/run-<name>` and `tillr-trace/bash-shim.sh` that both the source tree and [`yolo-packs/tillr`](../../yolo-packs/tillr/pack.json) already use. Unlike embedding scripts into Go binaries and extracting them into `/tmp` on launch, keeping them under `<prefix>/share/tillr/` works on `noexec` `/tmp` filesystems, gives `pi` stable on-disk `.ts` extension paths, and lets operators read the exact adapter scripts running on their machine.

Two jail-era details in these adapters already work or have queued fixes outside a jail:

- `run-claude` exports `YOLO_NO_LAUNCH_FLAGS=1` before invoking `claude -p` ([`run-claude`](../../tools/tillr-run/backends/claude/run-claude)), which stock `claude` ignores when no jail wrapper is present; for `call-suspends` runs, `tillr-run exec` (`Exec` in [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go)) already creates `<lane-dir>/durable/<run_id>` and passes the run scratch environment variable to the child process regardless of whether a jail exists.
- [`pi-build-agent-dir`](../../tools/tillr-run/pihelper/build_agent_dir.go) still references `yolo-packs/matt/pack.json` when assembling a `pi` agent directory (`TILLR_BUILD_AGENT_DIR` mode); removing that foreign-repo reach-in is already queued in [`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) (entry 6).

### 3.2 Default workflows and watch rules

**Default workflows (`D-HI3`, designer's decision; the owner may overturn this).** Today only `single-worker@v1` is compiled into Go (`SingleWorkerWorkflow` in [`tools/tillr-item/item/request.go`](../../tools/tillr-item/item/request.go) and [`tools/tillr-drain/drain/workflow_defaults.go`](../../tools/tillr-drain/drain/workflow_defaults.go)). The six standard multi-node workflows — `planned-build@v1`, `build-pieces@v1`, `scout-then-build@v1`, `scouted-design@v1`, `design-from-scouts@v1`, and `doc-triage@v1` — live only in [`config/tillr/workflows/`](../../config/tillr/workflows/). Yet `tillr item request` defaults to `planned-build@v1` or `scouted-design@v1` (`resolveRequestWorkflow` in [`tools/tillr-item/item/request.go`](../../tools/tillr-item/item/request.go)), `tillr item doc-triage` defaults to `doc-triage@v1` ([`doc_triage.go`](../../tools/tillr-item/item/doc_triage.go)), and both `tillr-item` and `tillr-drain` (`loadWorkflowDef` in [`tools/tillr-drain/drain/pass.go`](../../tools/tillr-drain/drain/pass.go)) look up `<repo-root>/config/tillr/workflows/<ref>.json` inside the *operated* repository. In any fresh project outside `tillr`'s own repo, default `tillr item request` therefore fails unless the user either copies those six JSON files into their repository or passes `--workflow single-worker@v1`.

Under `D-HI3`, those six JSON definitions embed into [`tools/tillr/`](../../tools/tillr/) via `//go:embed` (with a gate check or single source of truth so [`config/tillr/workflows/`](../../config/tillr/workflows/) cannot drift). Resolution checks `<operated-repo>/config/tillr/workflows/<ref>.json` first so any project can override or add custom workflows, and falls back to the embedded catalog when the operated repository carries none. That satisfies [`tillr-utility-contracts.md` §8.1](./tillr-utility-contracts.md#81-defaults-come-from-tillrs-installation) (`D-UC16`: *"Defaults come from tillr's installation"*).

**Default watch rules.** [`tillr-watch.md` §4.3](./tillr-watch.md#43-shipped-rules-pack-delivery-and-repository-overrides-d-tw83-to-d-tw87) (`D-TW83` through `D-TW88`) already settles the identical layered model for watch rules: 32 of the 33 rule files in [`config/tillr/watch/`](../../tools/tillr-watch/rules/) (35 core and `git-landing` charter rules; only `repo-tmp-usage-high.toml` stays repo-local) move into `tools/tillr-watch/rules/` and embed into the `tillr-watch` binary via `//go:embed`, layered beneath `<operated-repo>/config/tillr/watch/*.toml`. Once that slice lands, an installed `tillr-watch` needs zero `.toml` files copied onto disk or into consumer repositories.

### 3.3 The patched `pi` fork and coding-agent harnesses

`tillr` does not bundle proprietary LLM SDKs or credentials; its `run-*` adapters invoke external coding-agent CLIs installed and authenticated in the user's own environment:

- **Users who choose `claude` (`run-claude`):** need only the `claude` CLI on `PATH` ([`tillr-claude-backend.md`](../reference/tillr-claude-backend.md)). They do **not** need `pi`, `tillr-pi`, or Node.js. `run-claude` supports full `call-suspends` (`land` and `obstacle` MCP tools via `tillr-run claude-mcp`) against stock `claude -p`.
- **Users who choose `pi` (`run-pi`):** hit a hard gate in [`tools/tillr-run/backends/pi/run-pi`](../../tools/tillr-run/backends/pi/run-pi) (`pi_require_features`), which runs `$PI_BIN --features` and refuses unless the output lists `parked-tool-calls`. Stock upstream `pi` (`@mariozechner/pi-coding-agent` / `earendil-works/pi`) does not have `--features` or `parked-tool-calls`; today only the `main` branch of `mschulkind-oss/tillr-pi` provides it ([`D-HI9`](#D-HI9), designer's decision from a filed operator request quoting the owner on 2026-11-26: *"let's just put stuff on main in tillr-pi"*, which the owner may overturn), built from source with Node.js `>= 22.19` ([`docs/guide.md` §6](../guide.md#6-walkthrough-from-a-fresh-clone-to-your-first-landed-item)).

Two paths make `run-pi` usable outside a jail:

1. **Already queued on the roadmap:** [`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) (entry 6, *Give the pi run adapter a compatibility mode*) adds an opt-in mode that runs against stock upstream `pi` using `RESULT:` text footers when `parked-tool-calls` is absent, while keeping `parked-tool-calls` as the default mode.
2. **Owner question ([OQ-HI2](#OQ-HI2)):** whether `mschulkind-oss/tillr-pi` should *also* publish prebuilt release tarballs and a `tillr-pi` Homebrew formula (declaring `depends_on "node"`) so outside-jail `pi` users get full `parked-tool-calls` (`land` and `obstacle` in-session suspension) in one command without cloning and compiling `tillr-pi`.

Regardless of [OQ-HI2](#OQ-HI2), `tillr` itself never declares `tillr-pi` or `node` as a hard install dependency, because `claude`-only users need neither. Instead, `run-pi` names both remedies (`brew install mschulkind-oss/tap/tillr-pi` if [OQ-HI2](#OQ-HI2) chooses Option A, or enabling compatibility mode / setting `PI_BIN`) when `pi` is missing or lacks `parked-tool-calls`.

### 3.4 Seat profiles in the person's home directory

Outside a jail, model names, seats, and adapter choices belong in the user's home directory rather than in environment variables or tracked repository files. [`tillr-seat-profiles.md`](./tillr-seat-profiles.md#32-per-user-profile-directory-configtillrbackend-policynamejson) (`D-DSP10`–`D-DSP18`) already specifies that home-directory layout:

- **Location:** `$TILLR_CONFIG_DIR` when set, else `$XDG_CONFIG_HOME/tillr`, else `$HOME/.config/tillr/`, holding named profiles as `backend-policy.<name>.json` (`D-DSP18`, the designer's decision from an operator request, which the owner may overturn) and an optional user default in `drain.json`.
- **Built-in adapter names:** profiles name built-in adapters (`"adapter": "run-pi"` or `"adapter": "run-claude"`) and omit `brief_template` unless overriding it (`D-DSP12` in [`tillr-seat-profiles.md`](./tillr-seat-profiles.md#34-the-repositorys-example-file-and-what-a-fresh-clone-can-do-with-it)). `tillr-run` resolves `"run-pi"` and the default `brief-template.md` against `tillr`'s own `<prefix>/share/tillr/tillr-run/backends/` directory ([§5.2](#52-how-an-installed-copy-finds-its-files-and-sibling-binaries)), so upgrading `tillr` from `0.1.0` to `0.2.0` never invalidates paths stored inside `~/.config/tillr/backend-policy.*.json`.
- **Specimen configuration on a host with no repo clone:** [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) ships inside the archive at `<prefix>/share/tillr/examples/backend-policy.json` (updated once `D-DSP12` lands to use built-in adapter names rather than `./tools/tillr-run/...` clone paths). A new user copies that file to `~/.config/tillr/backend-policy.default.json` and passes `--profile default` (or sets `[drain] profile = "default"` in `.tillr.toml`, or passes `--policy ~/.config/tillr/policy.json`), fills in real model identifiers, and runs `tillr`.

### 3.5 Remaining external interpreters and OS tools

Once the queued Python-removal items in [§4](#4-how-this-design-depends-on-queued-python-removal-and-embedded-watch-rules) land, an installed `tillr` calls only:

- **`git`:** required at runtime for `git-landing` charters (worktrees, `git merge-tree` / squash merges in [`tools/tillr-land`](../../tools/tillr-land/), rev-parse, diff). Optional for `recorded-verdict` charters that complete via hooks. Homebrew formulas declare `depends_on "git"`, and `install.sh` warns if `git` is absent from `PATH`.
- **POSIX `/bin/sh`:** required to execute `tillr-run/backends/*/run-*` and `preflight.sh`. Present on every POSIX host.
- **`bash` (optional):** [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) and [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) set `BASH_ENV` to `bash-shim.sh` so any Bash subshell spawned by the coding agent records command spans. `run-*` themselves are `/bin/sh` scripts.
- **No GNU `timeout` requirement (`D-HI6`, designer's decision; the owner may overturn this):** today [`run-pi`](../../tools/tillr-run/backends/pi/run-pi), [`run-claude`](../../tools/tillr-run/backends/claude/run-claude), [`run-agy`](../../tools/tillr-run/backends/agy/run-agy), and [`run-muse`](../../tools/tillr-run/backends/muse/run-muse) prepend `timeout "$wall"` when `--timeout` is greater than zero. Stock macOS has no `timeout` utility unless GNU coreutils is installed. `D-HI6` moves non-zero `--timeout` wall enforcement into `tillr-run exec` ([`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go)) via Go timer + child-group termination, leaving `--timeout` accepted on `run-*` as a no-op compatibility flag when `tillr-run exec` already supervises the deadline.
- **No Go, Node, `just`, or Python runtime requirement for `tillr` itself:** consumer repositories still install whatever compilers or test runners their *own* `.tillr.toml` gate checks invoke, which is independent of `tillr`.

---

## 4. How This Design Depends on Queued Python Removal and Embedded Watch Rules

### 4.1 What has already moved to Go vs. what still calls `python3`

The Python-to-Go migration in [`tillr-utility-contracts.md` §8](./tillr-utility-contracts.md#8-the-thirteen-contracts) has now landed across `tillr-loop`, `tillr-trace`, `tillr-roll`, `tillr-drain keep`, `tillr-gate shards`, `tillr-run extract-report`, the `tillr-run pi-*` helper faces (`pi-build-agent-dir`, `pi-extension-manifest`, `pi-preflight`, `pi-resolve-fork`, `pi-select-fork`), and the three runtime paths that still called `python3` at `bc431c2541fd` (`fd '\.py$' tools/` returns nothing at `3eb70e5891e0`, enforced by [`tools/gate/checks/no-python.json`](../../tools/gate/checks/no-python.json)):

1. **Gate runners invoked by `tillr-loop`:** ported to Go in `ff91d16e` with the Python scripts deleted in `1eeef862`. [`tools/tillr-loop/loop/loop.go`](../../tools/tillr-loop/loop/loop.go) now invokes the compiled `tillr-gate` binary's [`change_gate.go`](../../tools/tillr-gate/change_gate.go) (`change-gate`), [`tree_check.go`](../../tools/tillr-gate/tree_check.go) (`tree-check`), and [`classify.go`](../../tools/tillr-gate/classify.go) directly, leaving zero `.py` files needed under `<prefix>/share/tillr/`.
2. **Base-replay Go affected-package and legacy-registry helpers:** `8231d777` ported `go_affected.py` into [`tools/tillr-gate/goaffected/`](../../tools/tillr-gate/goaffected/) (`tillr-gate go-affected`, [`tillr-utility-contracts.md` §8.4](./tillr-utility-contracts.md#84-the-affected-packages-calculator-moves-to-go) `D-UC19`), and `2527c9ca` extracted the base-replay helpers to [`tools/tillr-drain/internal/gatespans/replay_go_affected.go`](../../tools/tillr-drain/internal/gatespans/replay_go_affected.go) (`ReplayGoCheck`, which runs `tools/tillr-gate/goaffected` when present and falls back to `python3 tools/gate/go_affected.py` only when replaying an older base commit) and [`tools/tillr-drain/internal/gatespans/legacy_watches.go`](../../tools/tillr-drain/internal/gatespans/legacy_watches.go) (`LegacyCheckDeclarations`, which invokes `python3` only if `tools/fork-check.py` exists in a replayed legacy tree; deleting that legacy path is [`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) entry 5).
3. **Inline `python3 -c` JSON/path snippets in `pi` shell scripts:** removed in `ff91d16e` across [`run-pi`](../../tools/tillr-run/backends/pi/run-pi), [`pi-agent-dir.sh`](../../tools/tillr-run/backends/pi/pi-agent-dir.sh), and [`pi-harness.sh`](../../tools/tillr-run/backends/pi/pi-harness.sh) (`rg -n 'python3' tools/tillr-run/backends/ -g '!**/testdata/**'` returns nothing at `3eb70e5891e0`; only dev-time [`.justfile`](../../.justfile) `ensure-durable` and `testdata/land.test.mjs` still invoke `python3`).

### 4.2 Why public installers wait on Python removal and embedded watch rules (`D-HI5`)

**Designer's decision (`D-HI5`); the owner may overturn this.** Sequencing splits cleanly into two halves:

- **Does NOT wait (Slice 1 in [§7](#7-recommended-build-order)):** adding `<realExeDir>/../share/tillr` and sibling-binary discovery, embedding the six default workflows (`D-HI3`), moving `--timeout` into `tillr-run exec` (`D-HI6`), and adding `just install` can land independently of Python removal or watch-rule embedding.
- **DOES wait before cutting public release archives, Homebrew formulas, or `install.sh` (Slices 2–3 in [§7](#7-recommended-build-order)):**
  1. Waiting on **Python removal** avoids declaring `depends_on "python@3.12"` in Homebrew, checking for `python3` in `install.sh`, or shipping `change-gate.py` / `tree-check.py` under `<prefix>/share/tillr/tillr-gate/` only to delete them a week later.
  2. Waiting on **embedded watch rules** ([`tillr-watch.md` `D-TW83`–`D-TW88`](./tillr-watch.md#43-shipped-rules-pack-delivery-and-repository-overrides-d-tw83-to-d-tw87)) avoids inventing a second watch-rule lookup path under `<prefix>/share/tillr/watches/` that `D-TW84`'s `//go:embed` catalog immediately supersedes.

---

## 5. Prefix Layout, File Discovery, and Updates

### 5.1 One standard `<prefix>` tree across every route (`D-HI1`)

**Designer's decision (`D-HI1`); the owner may overturn this.** Release archives, Homebrew (`Cellar/tillr/<version>`), `install.sh` (`~/.local` or `$TILLR_PREFIX`), and `just install` all populate the exact same relative directory tree:

```text
<prefix>/
  bin/
    tillr
    tillr-drain
    tillr-gate
    tillr-item
    tillr-land
    tillr-loop
    tillr-run
    tillr-store
    tillr-trace
    tillr-view                  # compiled with -tags observatory (web/dist embedded)
    tillr-watch
  share/
    tillr/
      INSTALL_MANIFEST          # newline-terminated list of files installed under share/tillr and bin
      tillr-run/
        backends/
          brief-template.md
          preflight.sh
          claude/{run-claude,ending-capability,pause-kind,extract-report}
          pi/{run-pi,ending-capability,pause-kind,extract-report,pi-harness.sh,pi-agent-dir.sh}
          pi/{keep-going.ts,land.ts,no_change.ts,obstacle.ts}
          agy/{run-agy,ending-capability,pause-kind,extract-report}
          muse/{run-muse,ending-capability,pause-kind,extract-report}
      tillr-trace/
        bash-shim.sh
      examples/
        backend-policy.json
      skills/
        tillr-operating/SKILL.md
        tillr-designs/SKILL.md
```

### 5.2 How an installed copy finds its files and sibling binaries

Today `resolveRunModuleDir` in [`tools/tillr-run/run/adapter.go`](../../tools/tillr-run/run/adapter.go) and `resolveGateRunner` in [`tools/tillr-loop/loop/loop.go`](../../tools/tillr-loop/loop/loop.go) check only `$TILLR_INSTALL_ROOT` and a `tillr` source checkout (`go.mod` check), while `resolve` in [`tools/tillr-cli/main.go`](../../tools/tillr-cli/main.go) calls `exec.LookPath("tillr-" + noun)` without checking its own binary directory first (only `siblingBinary` in [`tools/tillr-view/siblings.go`](../../tools/tillr-view/siblings.go) checks its own directory first today).

Under `D-HI1`, a shared helper in [`tools/tillr`](../../tools/tillr/) resolves both sidecars and sibling programs with **zero wrapper scripts and zero required environment variables**:

1. **Sidecar install root (`tillr.ResolveShareDir`):**
   - **Rung 1 — `$TILLR_INSTALL_ROOT`:** when set (as `yolo-packs/tillr` launchers set `/usr/local/share/tillr`), require `<TILLR_INSTALL_ROOT>/<module>` to exist; if missing, fail loudly with no fallback.
   - **Rung 2 — Executable-relative `<realExeDir>/../share/tillr`:** resolve `os.Executable()`, strip Linux's `" (deleted)"` suffix if present, resolve symlinks via `filepath.EvalSymlinks`, and check `<realExeDir>/../share/tillr/<module>`. Because `EvalSymlinks` resolves `/opt/homebrew/bin/tillr-run` to `/opt/homebrew/Cellar/tillr/0.2.0/bin/tillr-run`, `<realExeDir>/../share/tillr` lands on `/opt/homebrew/Cellar/tillr/0.2.0/share/tillr` — always version-matched to the running binary, with no Homebrew wrapper scripts.
   - **Rung 3 — `tillr` source checkout:** walk upward from `<realExeDir>` (and `cwd` only when `go.mod` declares `module github.com/mschulkind-oss/tillr`), preserving `go run` and `go test` in development while still refusing consumer repositories that happen to have an unrelated `tools/` directory.
2. **Sibling binaries (`tillr.ResolveSiblingBinary`):** `tillr` (`tools/tillr-cli`), `tillr-drain`, `tillr-loop`, `tillr-run`, and `tillr-view` check `<realExeDir>/tillr-<name>` before falling back to `PATH` (completing `D-SH1` in [`tillr-selfhosted-build.md` §3](./tillr-selfhosted-build.md#3-mechanism-and-one-writer-rules)). Installing into `~/.local/bin` therefore works even when a non-interactive service invokes `/home/user/.local/bin/tillr` without `~/.local/bin` on `PATH`.

### 5.3 How an installed copy is updated (`D-HI8`)

**Designer's decision (`D-HI8`); the owner may overturn this.** Updates stay with the channel that installed `tillr` (no self-mutating `tillr self-update` command that would fight Homebrew's Cellar or read-only prefixes):

- **Homebrew:** `brew update && brew upgrade tillr` unpacks the new `Cellar/tillr/<new-ver>` directory and atomically repoints `/opt/homebrew/bin/tillr*` (or `/home/linuxbrew/.linuxbrew/bin/tillr*`).
- **`install.sh`:** re-running `install.sh` (or `install.sh --version vX.Y.Z`) downloads and verifies `checksums.txt`, unpacks into `<prefix>/share/tillr/.staging-$$/`, installs each binary into `<prefix>/bin/.tillr-tmp-$$-<name>` and `rename(2)`s it over `<prefix>/bin/<name>`, swaps `<prefix>/share/tillr/` entries from the manifest, and removes any retired file listed in the previous `INSTALL_MANIFEST` without touching non-`tillr` files in `~/.local/bin` or `~/.local/share`. `install.sh --uninstall` removes every path recorded in `INSTALL_MANIFEST` and leaves `~/.config/tillr/` untouched.
- **How running `tillr-drain` and `tillr-view` daemons pick up an update:** on POSIX filesystems, unlinking or `rename`-replacing `<prefix>/bin/tillr-drain` leaves an already-running `tillr-drain` or `tillr-view` process undisturbed on its open inode; the UI and `tillr drain status` surface the disk-vs-running digest mismatch (`drain.build` / `view.build`; [`tillr-restart.md`](../reference/tillr-restart.md)), and `tillr drain restart` / `tillr view restart` hands off cleanly between passes.
  - **Symlink-prefix subtlety to handle in Slice 1:** on Linux, `os.Executable()` reads `/proc/self/exe`, which resolves through symlinks into `.../Cellar/tillr/0.1.0/bin/tillr-drain`. When `TILLR_LAUNCHER` is unset, `LauncherCandidate` in [`tools/tillr-drain/restart/restart.go`](../../tools/tillr-drain/restart/restart.go) currently returns `rc.Executable` (`os.Executable()`), which after `brew upgrade` still points at `0.1.0` instead of the repointed `/opt/homebrew/.../bin/tillr-drain` symlink! Recording the invoked symlink path at startup (`os.Args[0]` resolved via `exec.LookPath` when `EvalSymlinks(invoked) == EvalSymlinks(exe)`) and passing that stable entry path to `LauncherCandidate` lets `tillr drain restart` and `tillr view restart` discover `0.2.0` after `brew upgrade` just as they do after `install.sh` atomic `rename`.

---

## 6. Operating Systems Covered First

### 6.1 Measured cross-compilation baseline (`2026-11-26` at `bc431c2541fd`)

Cross-compiling all eleven executable modules (`for mod in tillr-cli tillr-drain tillr-gate tillr-item tillr-land tillr-loop tillr-run tillr-store tillr-trace tillr-view tillr-watch; do (cd tools/$mod && GOOS=<os> GOARCH=<arch> CGO_ENABLED=0 go build -trimpath .); done`) on `2026-11-26` at `bc431c2541fd` establishes the exact platform gap:

| Target | Result at `bc431c2541fd` | Measured size / blockers |
| :--- | :--- | :--- |
| **`linux/amd64`** | **11 / 11 binaries compile cleanly** | `158 MiB` uncompressed (`107.1 MiB` with `-ldflags="-s -w"`, `45.2 MiB` `.tar.gz` across all 11 binaries) |
| **`linux/arm64`** | **11 / 11 binaries compile cleanly** | `149 MiB` uncompressed across all 11 binaries |
| **`darwin/arm64` & `darwin/amd64`** | **5 / 11 compile** (`tillr`, `tillr-gate`, `tillr-item`, `tillr-loop`, `tillr-trace`); **6 fail to compile** (`tillr-drain`, `tillr-land`, `tillr-run`, `tillr-store`, `tillr-view`, `tillr-watch`) | Four unsplit Linux syscall files ([§6.2](#62-rollout-order-and-the-bounded-macos-port-d-hi7-oq-hi3)), pulled into `tillr-store`, `tillr-view`, and `tillr-watch` through package imports |

### 6.2 Rollout order and the bounded macOS port (`D-HI7`, [OQ-HI3](#OQ-HI3))

**Designer's decision (`D-HI7`); the owner may overturn this.** Operating systems are covered in two tiers, with native Windows outside WSL2 explicitly out of scope:

1. **First: Linux (`linux/amd64` and `linux/arm64`).** Covers Linux workstations and servers, Windows hosts running WSL2, and Linux VMs or containers on macOS. Every Go package, pidfd/child-subreaper supervisor, `/proc` lock inspector, and shell adapter works on Linux today without platform stubs.
2. **Second: Native macOS (`darwin/arm64` Apple Silicon first, `darwin/amd64` Intel second).** Six binaries fail to compile on Darwin today because four files call Linux-only syscalls without `//go:build linux` / `//go:build darwin` splits, and four already-split `*_other.go` files return runtime refusals on `!linux`:

| Site | Linux mechanism used today | Native macOS (`darwin`) replacement |
| :--- | :--- | :--- |
| [`tools/tillr-land/land/owner_fs.go`](../../tools/tillr-land/land/owner_fs.go) (compile blocker) | `unix.Renameat2` with `RENAME_NOREPLACE` and `RENAME_EXCHANGE` | `unix.RenameatxNp` with `unix.RENAME_EXCL` and `unix.RENAME_SWAP` (native on APFS / macOS) |
| [`tools/tillr-drain/presence/presence.go`](../../tools/tillr-drain/presence/presence.go) (compile blocker in `drain`, `store`, `view`, `watch`) | `syscall.F_SETLEASE` kernel lease check | `unix.Flock` non-blocking probe / kqueue `EVFILT_VNODE` on Darwin |
| [`tools/tillr-run/run/children.go`](../../tools/tillr-run/run/children.go) & [`tools/tillr-drain/keep/restart.go`](../../tools/tillr-drain/keep/restart.go) (compile blockers) | `unix.PidfdOpen` + `unix.PidfdSendSignal` | Process-group signaling (`Setpgid` + `kill(-pgid)`) guarded by `(pid, start_time)` check via `sysctl` `KERN_PROC_PID` or `kqueue` `EVFILT_PROC` |
| [`tools/tillr-drain/heavyrun/heavy_tree_other.go`](../../tools/tillr-drain/heavyrun/heavy_tree_other.go) & [`tools/tillr-drain/keep/keep.go`](../../tools/tillr-drain/keep/keep.go) (runtime `!linux` refusals) | `PR_SET_CHILD_SUBREAPER`, pidfd, `/proc/self/comm` | Dedicated process group + pipe-EOF lifeline watcher in child + `KERN_PROC_PID` identity check |
| [`tools/tillr/store/holders_other.go`](../../tools/tillr/store/holders_other.go) & [`tools/tillr-gate/cpu_other.go`](../../tools/tillr-gate/cpu_other.go) (diagnostic `!linux` stubs) | `/proc/locks` and `/proc/<pid>/stat` | `lsof -F` best-effort holder diagnostic; Darwin `proc_pidinfo` (`PROC_PIDTASKINFO`) for CPU time |

Whether the first public release ships Linux archives immediately and adds `darwin/{arm64,amd64}` once full macOS loop supervision (`keep` and `heavyrun`) lands, or ships `darwin` archives as soon as the compile blockers are split while documenting `keep` / rolling-proof limits on macOS, is left to the owner in [OQ-HI3](#OQ-HI3).

---

## 7. Recommended Build Order

Per `R-HI1`, all slices below sit **behind open defects** on [`roadmap.md`](../tillr/roadmap.md#9-packaging-and-contracts). Slices 2 and 3 also sit behind the queued Python-removal and embedded-watch-rule items ([§4.2](#42-why-public-installers-wait-on-python-removal-and-embedded-watch-rules-d-hi5)).

1. **Slice 1 — Relocatable prefix layout, embedded default workflows, Go `--timeout`, and `just install` (waits on no open question):**
   - Add shared `ResolveShareDir` (`$TILLR_INSTALL_ROOT` -> `<EvalSymlinks(exe)>/../share/tillr` -> `tillr` repo checkout) and `ResolveSiblingBinary` (`<EvalSymlinks(exe)>` -> `PATH`) in [`tools/tillr`](../../tools/tillr/), wiring `tillr-cli`, `tillr-run`, `tillr-loop`, `tillr-drain`, and `tillr-view`.
   - Record the invoked symlink path for `LauncherCandidate` in [`tools/tillr-drain/restart/restart.go`](../../tools/tillr-drain/restart/restart.go) when `TILLR_LAUNCHER` is unset so symlinked prefixes (`brew upgrade`) restart into the newly linked version.
   - Embed the six [`config/tillr/workflows/*@v1.json`](../../config/tillr/workflows/) graphs as fallbacks beneath `<repo>/config/tillr/workflows/` (`D-HI3`), and move `--timeout` enforcement into `tillr-run exec` (`D-HI6`).
   - Add `just install PREFIX=~/.local` (building all **eleven** binaries including `tillr-watch`, embedding `tillr-view/web/dist` when present, and populating `<prefix>/share/tillr/` + `INSTALL_MANIFEST`) plus an end-to-end test that copies the installed prefix to a temporary directory, deletes access to the source tree, and runs `tillr store`, `tillr item request`, `tillr gate`, and `tillr drain loop --once` in a fresh temporary Git repository.
2. **Prerequisite check (already queued elsewhere on the roadmap):** complete [`tillr-watch.md` `D-TW83`–`D-TW88`](./tillr-watch.md#10-build-slices-for-shipped-and-repository-watch-rules) (embedded watch rules), [`tillr-utility-contracts.md` `D-UC19`](./tillr-utility-contracts.md#84-the-affected-packages-calculator-moves-to-go) (`go_affected.py` -> Go), porting `change-gate.py` / `tree-check.py` into `tillr-gate`, and removing the inline `python3` snippets in `tools/tillr-run/backends/pi/*.sh`. Verify `rg 'python3' <prefix>/share/tillr` returns nothing.
3. **Slice 2 — Public release workflow and checksummed archives (waits on [OQ-HI1](#OQ-HI1); macOS matrix timing per [OQ-HI3](#OQ-HI3)):**
   - Add `.github/workflows/release.yml` building `tools/tillr-view/web/dist`, cross-compiling all eleven binaries with `CGO_ENABLED=0 -buildvcs=true -trimpath` and `-tags observatory` on `tillr-view`, packaging `tillr_<ver>_linux_{amd64,arm64}.tar.gz` plus `checksums.txt`, and smoke-testing the unpacked tarball in a clean container with neither Go, Node, nor Python installed.
4. **Slice 3 — Homebrew formula and POSIX `install.sh` (waits on Slice 2 and [OQ-HI1](#OQ-HI1)):**
   - Ship `install.sh` (checksum verification, atomic `~/.local` or `$TILLR_PREFIX` install/upgrade, `--uninstall` via `INSTALL_MANIFEST`, PATH hint when `<prefix>/bin` is missing from `PATH`).
   - Publish the Homebrew `tillr` formula consuming the prebuilt GitHub Release archives (`depends_on "git"`, `test do system "#{bin}/tillr", "store", "init", ... end`) and automate formula version/SHA256 bumps in `release.yml`.
   - Update [`README.md`](../../README.md) and [`docs/guide.md`](../guide.md) with the outside-jail install section once live (honoring the documentation honesty rule in [`tillr-distribution.md`](../reference/tillr-distribution.md#documentation-split-and-the-honesty-rule)).
5. **Slice 4 — Native macOS port (`darwin/arm64` and `darwin/amd64`) (sequenced relative to Slice 2 per [OQ-HI3](#OQ-HI3)):**
   - Split the four Darwin compile-time Linux syscall files (`owner_fs`, `presence`, `run/children`, `keep/restart`) and implement Darwin supervision for `heavyrun` and `keep`, adding a `macos-14` (`arm64`) smoke job in CI and adding `darwin_{arm64,amd64}` archives to `release.yml` and the Homebrew formula.
6. **Slice 5 — Optional `tillr-pi` packaging (if [OQ-HI2](#OQ-HI2) chooses Option A):**
   - Publish prebuilt `tillr-pi` release tarballs on `mschulkind-oss/tillr-pi` and a companion `tillr-pi` Homebrew formula (`depends_on "node"`).

---

## 8. Non-Goals and Risks

- **Non-goals:** replacing [`yolo-packs/tillr`](../../yolo-packs/tillr/README.md) inside `yolo` jails; providing sandboxing or filesystem isolation (that remains `yolo`'s job); bundling proprietary agent CLIs (`claude`) or API keys; supporting native Windows outside WSL2; or shipping a self-mutating `tillr self-update` subcommand.
- **Risk — partial upgrade across eleven binaries:** interrupted extraction directly into `bin/` could leave mixed binary versions. Mitigation: `install.sh` stages the full archive in `.staging-$$/` after verifying `checksums.txt` and `rename(2)`s binaries into place only after staging succeeds; `ResolveSiblingBinary` and `tillr-view`'s build-revision check catch mixed revisions loudly.
- **Risk — embedded workflows drifting from `config/tillr/workflows/`:** mitigated by storing the canonical JSON files inside `tools/tillr/workflow/builtin/` and verifying parity (or reading from one canonical location) in `just check-ci`.

---

## Open Questions

<!-- vantage: question id=OQ-HI1 leaning="Option A — Cut GitHub Releases from explicit v* tags and publish formulas in mschulkind-oss/homebrew-tap" -->
💬 **OQ-HI1: How should public `tillr` releases and the Homebrew formula be published?**

Slice 1 (`just install` and executable-relative `<prefix>/share/tillr` discovery) works locally with no public release, while Slices 2 and 3 publish binaries and formulas under `mschulkind-oss` ([`authority-not-approval.md`](../reference/authority-not-approval.md#what-owner-only-is-for): external publication). Imagine you have merged a batch of fixes on `main` and want a laptop user outside a jail to get them via `brew install` or `install.sh`: should releases happen only when you push an explicit `v*` version tag, or automatically on every proved commit, and should Homebrew use a standard `mschulkind-oss/homebrew-tap` repository?

- **Option A — Cut GitHub Releases from explicit `v*` tags on `mschulkind-oss/tillr`, and publish the formula in a dedicated `mschulkind-oss/homebrew-tap` repository (`brew install mschulkind-oss/tap/tillr`).** Nothing publishes externally until you push a version tag, and Homebrew users get the standard two-part tap syntax; requires creating `mschulkind-oss/homebrew-tap` and one workflow token to update the formula on release.
- **Option B — Cut rolling date/commit-stamped releases automatically whenever `refs/tillr/green` advances on `main`, updating `mschulkind-oss/homebrew-tap` automatically.** Every proved commit on `main` becomes installable immediately with zero manual tagging, at the cost that every green push publishes a public release asset and advances `brew upgrade` immediately.
- **Option C — Cut GitHub Releases from explicit `v*` tags on `mschulkind-oss/tillr`, and keep `Formula/tillr.rb` inside `mschulkind-oss/tillr` with no separate tap repository.** Avoids creating a second repository or cross-repo token, at the cost of a clumsier two-step Homebrew command (`brew tap mschulkind-oss/tillr https://github.com/mschulkind-oss/tillr && brew install tillr`).

_Leaning:_ **Option A** — explicit `v*` tags keep public releases deliberate while `tillr` is changing rapidly, and `mschulkind-oss/homebrew-tap` gives the standard one-line `brew install mschulkind-oss/tap/tillr` experience.

**Answer:**

<!-- vantage: question id=OQ-HI2 leaning="Option A — Publish release tarballs on mschulkind-oss/tillr-pi and a companion tillr-pi formula alongside upstream pi compatibility mode" -->
💬 **OQ-HI2: Should `mschulkind-oss/tillr-pi` also publish prebuilt release bundles and a Homebrew formula for outside-jail users who choose the `pi` backend?**

Someone installing `tillr` on a laptop who uses `claude` (`run-claude`) needs only `claude` on `PATH`. Someone who chooses `pi` (`run-pi`) hits `run-pi`'s check for `pi --features` containing `parked-tool-calls`, which today requires cloning `mschulkind-oss/tillr-pi` (`main` branch; [`D-HI9`](#D-HI9)) and running `npm ci && npm run build`. Entry 6 of [`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) already plans a compatibility mode so `run-pi` can drive stock upstream `pi` via `RESULT:` footers, without `parked-tool-calls`. Should we also package `tillr-pi` itself so outside-jail `pi` users get full `parked-tool-calls` (`land` and `obstacle` suspension) without a source build?

- **Option A — Publish release tarballs on `mschulkind-oss/tillr-pi` and a companion `tillr-pi` formula (`brew install mschulkind-oss/tap/tillr-pi`, plus an optional `--with-tillr-pi` flag in `install.sh`), alongside upstream `pi` compatibility mode.** Gives outside-jail `pi` users the real `land`/`obstacle` tool-suspension path in one command, at the cost of maintaining release builds on `mschulkind-oss/tillr-pi` until upstream `pi` adopts `parked-tool-calls`.
- **Option B — Do not publish `tillr-pi` packages; let outside-jail `pi` users run stock upstream `pi` via roadmap entry 6's compatibility mode (or `claude -p`), and keep `tillr-pi` as a source build documented in `docs/guide.md`.** Zero extra release surface on the fork repository, at the cost that a packaged `pi` setup runs on text `RESULT:` footers instead of `parked-tool-calls`.

_Leaning:_ **Option A** — `parked-tool-calls` is how workers receive gate and land refusals inside the same session without losing context, and packaging the built `tillr-pi` bundle from a fork that already exists is one small workflow.

**Answer:**

<!-- vantage: question id=OQ-HI3 leaning="Option A — Ship Linux archives first and publish macOS archives once Slice 4 completes Darwin supervision" -->
💬 **OQ-HI3: Should native macOS (`darwin/arm64` and `darwin/amd64`) release archives wait until `tillr drain keep` and supervised rolling trunk proofs work on macOS, or ship as soon as the eleven binaries compile on Darwin?**

At `bc431c2541fd`, all eleven binaries compile cleanly on `linux/{amd64,arm64}`, whereas six fail to compile on `darwin/{arm64,amd64}` due to four Linux syscall files (`land/owner_fs.go`, `presence/presence.go`, `run/children.go`, `keep/restart.go`), and two supervision paths (`keep` and `heavyrun` rolling trunk proofs) explicitly refuse `!linux` kernels ([§6.2](#62-rollout-order-and-the-bounded-macos-port-d-hi7-oq-hi3)). Imagine a developer on a MacBook runs `brew install` on day one: should we hold `darwin` release bottles until the full macOS supervision slice (`Slice 4`) is done, or publish `darwin` binaries earlier once the compile blockers are split and let `keep` / supervised rolling proofs refuse on macOS until `Slice 4` lands?

- **Option A — Ship `linux/{amd64,arm64}` archives first, and publish `darwin/{arm64,amd64}` archives and Homebrew macOS support only once `Slice 4` completes both compilation and Darwin supervision (`keep` + `heavyrun`).** Every advertised OS runs the full user-guide workflow (`tillr drain keep` and default after-merge trunk proofs included) from its first release, at the cost that native macOS installs wait for `Slice 4` (macOS users can still use a Linux VM or container in the meantime).
- **Option B — Split the four Darwin compile-time syscall sites inside `Slice 2` and ship `darwin/{arm64,amd64}` archives immediately, documenting and refusing `tillr drain keep` and supervised rolling trunk proofs (`--rolling ''` works) on macOS until `Slice 4` finishes.** Mac laptop users get `tillr` CLI commands, the web observatory, and unrolled drain loops right away, at the cost of a split feature set on Homebrew's most common OS until `Slice 4` lands.

_Leaning:_ **Option A** — most `brew install` users are on macOS, and meeting a Linux-only refusal on `tillr drain keep` or the default after-merge rolling proof right after installing makes the first impression feel broken when `Slice 4` is bounded and concrete.

**Answer:**

---

## Decision Ledger

Per [`decision-provenance-review.md` §6](../tillr/decision-provenance-review.md#6-how-this-stops-happening) (`D-PR1`, `D-PR2`), only `R-HI1` and the owner quotation carried in `D-HI9` record the owner's words; `D-HI1` through `D-HI9` are the designer's decisions (with `D-HI9` from a filed operator request) and remain subject to owner override.

| ID | Provenance & Status | Decision |
| :--- | :--- | :--- |
| <a id="R-HI1"></a>`R-HI1` | **Owner direction** (2026-11-25, verbatim from filed request) | Provide a way to install and use `tillr` without a `yolo` jail (*"Just a way to install it somehow. This may just be homebrew and or other things."*), ranked lower priority than open defects. |
| <a id="D-HI1"></a>`D-HI1` | **Designer's decision; the owner may overturn this.** | Standardize all non-jail installs on `<prefix>/bin/` (all eleven compiled `tillr*` binaries, with `web/dist` embedded in `tillr-view`) and `<prefix>/share/tillr/` (sidecars, specimen policy, skills, `INSTALL_MANIFEST`), discovered via `$TILLR_INSTALL_ROOT` -> `<EvalSymlinks(exe)>/../share/tillr` -> `tillr` source checkout, with sibling `tillr-*` binaries resolved in `<EvalSymlinks(exe)>` before `PATH`. |
| <a id="D-HI2"></a>`D-HI2` | **Designer's decision; the owner may overturn this.** | Build checksummed GitHub Release archives (`CGO_ENABLED=0`, web UI pre-embedded) as the single binary substrate, consumed by a Homebrew formula and a POSIX `install.sh` script (`~/.local` default), plus `just install` for source checkouts; reject `go install` as a primary end-user route and keep `yolo-packs/tillr` unchanged for jails. |
| <a id="D-HI3"></a>`D-HI3` | **Designer's decision; the owner may overturn this.** | Embed the six standard workflow JSON definitions (`planned-build@v1`, `build-pieces@v1`, `scout-then-build@v1`, `scouted-design@v1`, `design-from-scouts@v1`, `doc-triage@v1`) in `tillr` via `//go:embed` as built-in fallbacks layered beneath `<operated-repo>/config/tillr/workflows/<ref>.json`. |
| <a id="D-HI4"></a>`D-HI4` | **Designer's decision; the owner may overturn this.** | Keep `tillr-run/backends/*`, `pi/*.ts` extensions, `tillr-trace/bash-shim.sh`, `examples/backend-policy.json`, and `skills/{tillr-operating,tillr-designs}` as ordinary read-only files under `<prefix>/share/tillr/` rather than extracting embedded scripts into `/tmp` at runtime. |
| <a id="D-HI5"></a>`D-HI5` | **Designer's decision; the owner may overturn this.** | Gate public release archives, the Homebrew formula, and `install.sh` (Slices 2–3) behind completing the queued Python-removal items (`go_affected.py`, `change-gate.py` / `tree-check.py`, and `pi` shell `python3` snippets) and embedded default watch rules (`D-TW83`–`D-TW88`), while letting Slice 1 (`just install` + prefix discovery + embedded workflows) land without waiting on them. |
| <a id="D-HI6"></a>`D-HI6` | **Designer's decision; the owner may overturn this.** | Enforce non-zero `--timeout` run walls inside `tillr-run exec` in Go instead of calling GNU `timeout` inside `run-{pi,claude,agy,muse}`, removing the hidden GNU coreutils dependency on macOS and minimal hosts. |
| <a id="D-HI7"></a>`D-HI7` | **Designer's decision; the owner may overturn this.** | Cover Linux (`linux/amd64` and `linux/arm64`, including WSL2 and Linux VMs/containers) first, followed by native macOS (`darwin/arm64` then `darwin/amd64`) after splitting the four Linux syscall compile blockers and `/proc`+pidfd supervision paths ([OQ-HI3](#OQ-HI3)); leave native Windows outside WSL2 out of scope. |
| <a id="D-HI8"></a>`D-HI8` | **Designer's decision; the owner may overturn this.** | Update installed copies through the installing channel (`brew upgrade` or re-running `install.sh` with atomic staging and `INSTALL_MANIFEST` cleanup, never touching `~/.config/tillr`), and record the invoked symlink path at startup when `TILLR_LAUNCHER` is unset so `tillr drain restart` and `tillr view restart` follow symlinked prefixes (`brew upgrade`) cleanly. |
| <a id="D-HI9"></a>`D-HI9` | **Designer's decision (from a filed operator request, 2026-11-26); the owner may overturn this.** | Carry out the filed operator request quoting the owner on 2026-11-26 (*"let's just put stuff on main in tillr-pi"*) by naming the `main` branch of `mschulkind-oss/tillr-pi` rather than the retired feature branch wherever this document references `mschulkind-oss/tillr-pi` ([§1.2](#12-why-using-tillr-outside-a-jail-hurts-today), [§3.3](#33-the-patched-pi-fork-and-coding-agent-harnesses), and [OQ-HI2](#OQ-HI2)), and update [§4.1](#41-what-has-already-moved-to-go-vs-what-still-calls-python3) for the Python-to-Go ports (`8231d777`, `ff91d16e`, `1eeef862`) and `internal/gatespans` extraction (`2527c9ca`) that landed since `bc431c2541fd`. |
