# =============================================================================
# TILLR BUILD SYSTEM
# =============================================================================
# Modern build system for repository management

# Default command - show help
default:
    @just help

# Python is excluded: adopting its formatter would rewrite 84 files at once.
# shell, TypeScript, and Lua are excluded because they have no pinned formatter.
# Adding a language means pinning its formatter in mise.toml and landing the reformat
# as its own commit. Only tracked source is ours; vendor and fork trees are not.
# Format Go with gofmt; --check lists changes without writing and exits 1 on drift.
[positional-arguments]
format *args:
    #!/usr/bin/env python3
    import os
    from pathlib import Path
    import subprocess
    import sys

    if sys.argv[1:] not in ([], ["--check"]):
        print("usage: just format [--check]", file=sys.stderr)
        sys.exit(2)
    listing = subprocess.check_output([
        "git", "ls-files", "-z", "--", "tools/", "yolo-packs/tillr/files/local/share/tillr/",
    ])
    files = []
    for entry in listing.split(b"\0"):
        path = Path(os.fsdecode(entry))
        if (entry and path.suffix == ".go" and "vendor" not in path.parts
                and ".forks" not in path.parts and path.is_file()
                and not any(part.is_symlink() for part in (path, *path.parents))):
            files.append(str(path))
    if files:
        if sys.argv[1:]:
            result = subprocess.run(["gofmt", "-l", *files], capture_output=True)
            sys.stdout.buffer.write(result.stdout)
            sys.stderr.buffer.write(result.stderr)
            sys.exit(result.returncode or int(bool(result.stdout)))
        subprocess.run(["gofmt", "-w", *files], check=True)

# Create portable nvim config archive
nvim_dist:
    #!/usr/bin/env bash
    TIMESTAMP=$(date +%Y%m%d_%H%M%S)
    FILENAME="matt-nvim-config-${TIMESTAMP}.tar.bz2"
    tar -cjf "$FILENAME" --transform 's,^config/nvim,.config/nvim,' config/nvim
    echo "✓ Created $FILENAME"
    # Create/update symlink to latest
    ln -sf "$FILENAME" matt-nvim-config.tar.bz2
    echo "🔗 Updated symlink: matt-nvim-config.tar.bz2 -> $FILENAME"
    echo "📦 Extract with: tar -xjf matt-nvim-config.tar.bz2"
    # Clean up old versions (keep only the most recent)
    ls -t matt-nvim-config-*.tar.bz2 2>/dev/null | tail -n +2 | xargs -r rm -f
    echo "🧹 Cleaned up old archives (kept only the most recent)"

# Create nvim config archive with exclusions
nvim_dist_clean:
    #!/usr/bin/env bash
    TIMESTAMP=$(date +%Y%m%d_%H%M%S)
    FILENAME="matt-nvim-config-${TIMESTAMP}.tar.bz2"
    tar -cjf "$FILENAME" --transform 's,^config/nvim,.config/nvim,' --exclude='config/nvim/.git' --exclude='config/nvim/tmp' --exclude='config/nvim/*.log' config/nvim
    echo "✓ Created clean $FILENAME"
    # Create/update symlink to latest
    ln -sf "$FILENAME" matt-nvim-config.tar.bz2
    echo "🔗 Updated symlink: matt-nvim-config.tar.bz2 -> $FILENAME"
    echo "📦 Extract with: tar -xjf matt-nvim-config.tar.bz2"
    # Clean up old versions (keep only the most recent)
    ls -t matt-nvim-config-*.tar.bz2 2>/dev/null | tail -n +2 | xargs -r rm -f
    echo "🧹 Cleaned up old archives (kept only the most recent)"

# Show archive contents (latest file)
nvim_dist_list:
    #!/usr/bin/env bash
    LATEST_FILE=$(ls -t matt-nvim-config-*.tar.bz2 2>/dev/null | head -n1)
    if [ -n "$LATEST_FILE" ]; then
        tar -tjf "$LATEST_FILE"
    else
        echo "✗ No nvim config archives found"
    fi

# Extract and test the archive (latest file)
nvim_dist_test:
    #!/usr/bin/env bash
    LATEST_FILE=$(ls -t matt-nvim-config-*.tar.bz2 2>/dev/null | head -n1)
    if [ -n "$LATEST_FILE" ]; then
        mkdir -p /tmp/nvim-test
        tar -xjf "$LATEST_FILE" -C /tmp/nvim-test
        echo "✓ Archive $LATEST_FILE extracted to /tmp/nvim-test"
        echo "📁 Contents:"
        ls -la /tmp/nvim-test/.config/nvim/
    else
        echo "✗ No nvim config archives found"
    fi

# Clean up test directory
nvim_dist_cleanup:
    rm -rf /tmp/nvim-test
    @echo "🧹 Cleaned up test directory"

# Release nvim config to public repo
nvim_release:
    #!/usr/bin/env bash
    set -e
    
    # Check if .nvim-config-public exists
    if [ ! -d ".nvim-config-public" ]; then
        echo "✗ Error: .nvim-config-public directory not found"
        echo "Please clone your public nvim config repo:"
        echo "  git clone <your-repo-url> .nvim-config-public"
        exit 1
    fi
    
    # Check if it's a git repository
    if [ ! -d ".nvim-config-public/.git" ]; then
        echo "✗ Error: .nvim-config-public is not a git repository"
        echo "Please clone your public nvim config repo:"
        echo "  git clone <your-repo-url> .nvim-config-public"
        exit 1
    fi
    
    echo "✓ Found .nvim-config-public git repository"
    
    # Clean the target directory (remove everything except .git)
    echo "🧹 Cleaning target directory..."
    cd .nvim-config-public
    find . -not -path './.git*' -not -name '.' -not -name '..' -exec rm -rf {} + 2>/dev/null || true
    cd ..
    
    # Copy nvim config with proper structure for $HOME/.config/nvim
    echo "📦 Copying nvim config..."
    cp -r config/nvim/* .nvim-config-public/
    
    echo "✓ Nvim config copied to .nvim-config-public"
    echo ""
    echo "Next steps:"
    echo "  1. cd .nvim-config-public"
    echo "  2. git add ."
    echo "  3. git commit -m 'Update nvim config'"
    echo "  4. git push"
    echo ""
    echo "Users can then install with:"
    echo "  git clone <your-repo-url> $HOME/.config/nvim"

# Build Docker image for testing
nvim_docker_build:
    #!/usr/bin/env bash
    docker buildx build -t nvim-config-test -f .docker/Dockerfile .

# Build and run Neovim in Docker
nvim_docker_test:
    #!/usr/bin/env bash
    set -e
    just nvim_docker_build
    docker run --rm -it nvim-config-test nvim


# =============================================================================
# PI FORKS
# =============================================================================
# Our patches to pi and its extensions deploy as PATCH SERIES (directories of
# `git format-patch --base` files) under yolo-packs/; yolo applies each to its
# upstream and builds it. .forks/<name> (gitignored, rcm-excluded) is a working
# copy materialized from a series. The set is bin/pi-forks.list. Everything here
# works in a jail: no gh, no push, https only.
#
# The loop: forks_setup --lane <lane> --fork <name>; commit on main in
# .forks/lanes/<lane>/<name>; forks_export --lane <lane> --fork <name>; forks_check;
# yolo pack lint <pack>; commit. .forks/<name> itself is the shared cache: read it, do
# not edit it (docs/pi/forks.md#a-lane-gets-its-own-checkout).

# Status of every fork checkout against its series
forks:
    @bin/pi-forks status

# Materialize each checkout from its series (--fork <name>; --lane <lane> for a lane's own
# copy; --force to replace a divergent main)
forks_setup *args:
    @bin/pi-forks setup {{args}}

# Write each checkout's main back as the delivery: the series, or a patch-branch fork's
# patch/<name> moved to the lane tip (--fork <name>)
forks_export *args:
    @bin/pi-forks export {{args}}

# Fail when a series is unreadable or does not replay to its checkout's main
forks_check *args:
    @bin/pi-forks check {{args}}

# One command for upstream drift: rebase a series onto the upstream a launch would
# build, re-export it at the new base, and verify the replay. A conflict is aborted
# and the exact git commands to resolve it by hand are printed.
forks_rebase *args:
    @bin/pi-forks rebase {{args}}

# The launch's series verdict, run per PACK. Never on the workspace root: that form
# stages the whole tree, so a broken symlink under .forks/ fails every pack.
forks_series_check *args:
    @bin/pi-forks series-check {{args}}

# Fail when a series' patch branch is not on the remote, or is not that series: the
# published tip's tree against the series' replay tree at the same base, and the patch
# ids of each. Read-only; asks the remote, so it needs network and a read credential.
forks_published *args:
    @bin/pi-forks patch-branch published {{args}}

# Install this repository's git hooks (hooks/*) into the shared hooks directory, so the
# patch-branch pre-push guard runs from the main checkout and every linked worktree.
# A copy git already runs is replaced when it carries this repository's marker, and a
# hook without the marker is left alone -- kept as <name>.local, which our hook runs
# first -- so every run reports installed, replaced, left alone or drifted (see
# hooks/README.md and bin/hooks-install).
hooks-install *args:
    @bin/hooks-install {{args}}

# The patch branch's checks, for a timer, read-only and offline: how many releases behind
# the upstream a launch would build each series is, and whether the patch branch still
# merges it.
# Exits non-zero on a stale series or a merge conflict; `just done` runs both invariants
# itself, so this recipe is the shout a timer makes.
patch-branch-check *args:
    #!/usr/bin/env bash
    rc=0
    bin/pi-forks patch-branch lag {{args}} || rc=$?
    bin/pi-forks patch-branch merge-check {{args}} || rc=$?
    exit "$rc"

# What each checkout's main changes over its series' base
forks_diff *args:
    @bin/pi-forks diff {{args}}

# Local development build: npm ci when node_modules is missing, then the checkout's build -- hydrating upstream's catalog pin and building offline when the checkout carries one
forks_build *args:
    @bin/pi-forks build {{args}}

# Run the forked pi straight from its checkout (the patched-pi testing loop)
forks_run *args:
    @bin/pi-forks run {{args}}

# Build the self-profiler's static Go binary for its launcher
# (tools/pi-profiler/pi-profiler-go): gitignored, built on demand, never deployed.
profiler-build:
    cd tools/pi-profiler && CGO_ENABLED=0 go build -o ./pi-profiler-go ./cmd/pi-profiler

# Drive pi's queued-message states headlessly (tools/queued-message-states);
# needs the .forks/pi checkout and its installed dependencies.
queued-message-states *args:
    @bash tools/queued-message-states/run {{args}}

# Measure pi's session liveness headlessly (tools/session-liveness): expected GREEN. The stuck
# compaction, the unresolved idle wait and the wedged send-now were fixed by patch 0149; the abort
# of a compaction or branch summary with no run is bounded, and abandoning it clears its
# registration instead of only releasing the wait. The open-run spec pins the unbounded contract on
# purpose. Needs the .forks/pi checkout.
session-liveness *args:
    @bash tools/session-liveness/run {{args}}

# Mirror the tillr pack's copy of every `tools/tillr*` module into
# yolo-packs/tillr/files/local/share/tillr/<module>. The copy is committed because the
# pack -- not this checkout -- is what puts `tillr` on PATH in a workspace that has no
# tools/. `tests/pack-sync.test.py` fails when the copy diverges from its source.
# With module arguments, render only those modules. The drain embeds this same
# renderer so it can work on candidates with earlier versions of this recipe.
[positional-arguments]
pack-sync *modules:
    #!/usr/bin/env bash
    set -euo pipefail
    root="$(cd "$(dirname "{{justfile()}}")" && pwd)"
    exec python3 "$root/tools/pack-mirror-links.py" --sync "$root" "$@"

# Build the eight tillr programs from refs/tillr/green (the last trunk commit proved by
# the heavy runner), never from the newer tip or working tree. The pack's rendered copy
# is a separate owner-run deployment and is not changed here.
#
# The output directory must be prepended to PATH for the loop AND its children, not just used by
# name: `tillr` resolves `tillr-<noun>` through PATH and `ProcessSteps` resolves `tillr-run` and
# `tillr-item` through PATH, so a directory holding only `tillr` hands the loop the launched
# siblings. Build all eight together and run:
#
#   PATH="$YOLO_DURABLE_DIR/bin:$PATH" tillr drain loop ...
#
# Run it against a SCRATCH store, never the launched binaries' store: a newer binary migrates a
# store, and the launched binaries then refuse it. docs/design/tillr-distribution.md Section 9
# states the rule and OQ-TD4 is the ruling that would remove the constraint.
#
# Bootstrap, the one exception: when the deployed loop itself cannot prove the trunk (it
# crashes, or predates the fix the heavy run needs), the pointer can never reach the fix
# that would move it. An operator then names the commit explicitly:
#
#   TILLR_BUILD_UNPROVED=<commit> just tillr-build
#
# That build says UNPROVED on every run, never moves or reads the pointer as its proof, and
# stamps no green commit, so the loop's `loop: build` line reads `green unknown` until the
# next ordinary build from refs/tillr/green replaces it.
tillr-build:
    #!/usr/bin/env bash
    set -euo pipefail
    proof="-X github.com/mschulkind-oss/tillr-drain/drain.greenRevision="
    if [ -n "${TILLR_BUILD_UNPROVED:-}" ]; then
        if ! green=$(git rev-parse --verify --quiet "${TILLR_BUILD_UNPROVED}^{commit}"); then
            printf 'tillr-build: TILLR_BUILD_UNPROVED=%s names no commit\n' "$TILLR_BUILD_UNPROVED" >&2
            exit 1
        fi
        proved=$(git rev-parse --verify --quiet 'refs/tillr/green^{commit}' || printf 'absent')
        printf 'tillr-build: UNPROVED build of %s (TILLR_BUILD_UNPROVED); refs/tillr/green is %s and stays unchanged\n' "$green" "$proved" >&2
        proof=""
    elif ! green=$(git rev-parse --verify 'refs/tillr/green^{commit}'); then
        printf 'tillr-build: no valid refs/tillr/green; the heavy run at `tillr drain loop` startup (with after-merge checks enabled) creates it after proving the trunk; TILLR_BUILD_UNPROVED=<commit> is the documented bootstrap\n' >&2
        exit 1
    else
        proof="$proof$green"
    fi
    durable="${YOLO_DURABLE_DIR:?tillr-build needs YOLO_DURABLE_DIR for build output and scratch}"
    mkdir -p "$durable"
    durable=$(cd "$durable" && pwd)
    out="$durable/bin"
    work=$(mktemp -d "$durable/tillr-build-XXXXXXXX")
    trap 'rm -rf "$work"' EXIT
    # This independent, sparse clone borrows read-only objects, not the source's refs
    # or index. Its detached HEAD pins one green commit even if the proof ref moves.
    # Keeping Git metadata preserves native build stamps in older source trees and
    # in every sibling program; the view uses that revision to reject stale planners.
    git clone --shared --no-checkout --quiet . "$work/source"
    git -C "$work/source" sparse-checkout set --cone tools
    git -C "$work/source" checkout --detach --quiet "$green"
    mkdir -p "$work/bin"
    if [ -n "$proof" ]; then
        printf 'tillr-build: green %s\n' "$green"
    else
        printf 'tillr-build: UNPROVED %s\n' "$green"
    fi
    # `tillr-cli` builds the dispatcher named `tillr`; the shared library module of that name is
    # not a program, so it is built through its dependents rather than emitted here.
    for module in tillr-cli tillr-drain tillr-gate tillr-item tillr-land tillr-run tillr-store tillr-view; do
        name="${module}"
        [ "$module" = "tillr-cli" ] && name="tillr"
        tags=""
        if [ "$module" = "tillr-view" ]; then
            # A view built without the tag serves the API and answers 404 for the UI with
            # "this binary carries no built front end". Embed the front end when there is one
            # to embed, and say which line to run when there is not, so the message arrives
            # here rather than from a browser a day later.
            if [ -f "$work/source/tools/tillr-view/web/dist/index.html" ]; then
                tags="-tags observatory"
            else
                printf 'tillr-build: %s carries no front end in green %s; pass --web-dist to its serve face for external assets\n' "$name" "$green"
            fi
        fi
        ( cd "$work/source/tools/$module" && CGO_ENABLED=0 go build -buildvcs=true $tags -ldflags "$proof" -o "$work/bin/$name" . )
    done
    # Keep the existing install untouched until all eight builds succeed.
    mkdir -p "$out"
    for program in "$work/bin/"*; do
        python3 -c 'import os, sys; os.replace(*sys.argv[1:])' "$program" "$out/${program##*/}"
        printf 'tillr-build: %s/%s\n' "$out" "${program##*/}"
    done
    printf 'tillr-build: prepend %s to PATH for the loop and its children: PATH=%s:$PATH\n' "$out" "$out"

# Test, build and smoke the committed checkout before updating the live bin directory.
[positional-arguments]
tillr-roll *args:
    @python3 tools/tillr-roll/tillr-roll.py "$@"

# Keep this checkout's loop alive; --stop stops only the keeper. Pass loop options after --.
# Positional arguments keep each one intact, including an empty `--rolling ''`.
[positional-arguments]
tillr-keep *args:
    @python3 tools/tillr-roll/tillr-keep "$@"

# Stand the keeper down, stop the loop, wait for exit, build from HEAD, and restart the keeper
# with the running loop's (or recorded launch's) policy and lane count. Safe when no loop runs.
[positional-arguments]
tillr-restart *args:
    @python3 tools/tillr-roll/tillr-keep --restart "$@"

# Check the one generated pack copy: the tillr pack's tree against tools/tillr*,
# module by module, byte for byte, with the README rewrite, the exec bits, and the
# eight launchers' shared body.
pack-sync-test:
    @python3 tests/pack-sync.test.py

# Refresh the tillr pack's dated snapshot of tillr's design documentation: the status
# index docs/tillr/design-status.md and every document its tables mark `shipped`, copied
# from HEAD into yolo-packs/tillr/files/local/share/tillr/docs/ (tools/pack-docs.py), then
# judged current against HEAD. Commit the result. Deliberately NOT part of `pack-sync`:
# lanes run pack-sync, the docs tree is outside a lane's fence, and the drain edits design
# documents on every landing, so the snapshot is refreshed by hand, never per landing.
pack-docs:
    @python3 tools/pack-docs.py sync
    @python3 tests/pack-sync.test.py --fresh

# Render and screenshot the pi-subagents Background work roster at many widths and
# content mixes (tools/roster-storybook); writes $YOLO_DURABLE_DIR/roster-storybook.
roster_storybook *args:
    @bash tools/roster-storybook/run.sh {{args}}

# Render the pi-archimedes footer status line at several widths and assert the WHALE
# badge is visible (tools/footer-storybook); writes $YOLO_DURABLE_DIR/footer-storybook.
footer_storybook *args:
    @bash tools/footer-storybook/run.sh {{args}}

# Live-reload dev server for the roster storybook: `just dev [port]` (default 5173).
dev *args:
    @bash tools/roster-storybook/dev.sh {{args}}

# Serve the drain observatory for THIS checkout on http://127.0.0.1:<port> (`just observatory
# 5000`; the default is 4318), from one process with the front end embedded. It builds
# tillr-view and the tillr-drain it asks for Waiting from this tree (static, so a host and a
# container can run each other's builds) into one directory per checkout, passes the store,
# root and planner as absolute paths, and runs with any working directory, mount path or
# PATH that holds the toolchain. `--store`, `--root`, `--addr` and the rest pass through to
# `tillr-view serve` and beat the defaults. No Node is needed once the front end is built.
# A port held by this same build serving this store answers with its URL; one held by an
# older build or anything else is refused with the command that frees it. In a yolo jail the
# recipe binds 0.0.0.0 only for a port published in yolo-jail.toml, and otherwise prints the
# line to add. tools/tillr-view/README.md ("Starting the observatory") states every refusal.
[doc("Serve this checkout's observatory, built from this checkout, on 127.0.0.1:<port> (default 4318)")]
[no-exit-message]
observatory port="4318" *args:
    @exec bash "{{justfile_directory()}}/tools/observatory/run.sh" serve "{{port}}" {{args}}

# Summarize the observatory's performance log: per-route and per-page p50/p95/max, the slowest
# requests with their phase breakdown, the browser-versus-server gap, the live channel's
# traffic and each start's phases. `just observatory` and `observatory-dev` write that log
# under this checkout's durable scratch directory (tools/tillr-view/README.md, "The
# performance log"), which the host and its jails share, so this reads what the HOST's server recorded
# too. Arguments pass through to `tillr-view perf-log` (`--since 1h`, `--top 20`, `--json`, or
# a log file or directory instead of every server's).
[doc("Summarize the observatory's performance log (what the host's or this jail's server recorded)")]
[no-exit-message]
observatory-perf *args:
    @cd "{{justfile_directory()}}/tools/tillr-view" && go run . perf-log --logs "{{justfile_directory()}}/.yolo/durable/tillr-observatory/logs" --from "{{invocation_directory()}}" {{args}}

# Develop against the observatory with hot reload. The port you name is the one you OPEN --
# Vite serves the UI there and proxies `/api` to a Go server one port above it (so two dev
# runs do not collide); a caller-named `--addr` moves the API and the proxy follows it.
# Live reload is for code, not data (design Section 5.3). The same guarantees as
# `observatory`: both programs built from this tree, every path absolute, nothing found on
# PATH but the toolchain. No `npm run build` and no `observatory` tag: Vite serves the UI
# from source, so embedded assets would be a stale snapshot of it.
#
# ONE SUPERVISED TREE, NOT TWO BACKGROUND JOBS. The processes are declared in
# `tools/tillr-view/Procfile.dev` and run under hivemind (pinned in `mise.toml`): a child that
# dies takes the other with it, and SIGINT or SIGTERM tears down both process groups. A port
# that is already answering is refused before any build, and Vite runs with `--strictPort`,
# because the last way to serve the wrong board is to serve an old process's.
[doc("Hot-reload observatory for this checkout: Vite on <port>, the API on <port>+1")]
[no-exit-message]
observatory-dev port="4318" *args:
    @exec bash "{{justfile_directory()}}/tools/observatory/run.sh" dev "{{port}}" {{args}}

# End of task: check every change since the last successfully verified commit,
# including earlier tasks and sessions. Advance that approved point only when all
# selected checks pass; failures and missing verdicts keep the pending work open.
# Always runs the fast offline series-shape check and probes upstream drift, then
# routes changed inputs to relevant document, render, Waybar, and fork checks.
#
# Commit watched inputs first. --explain prints the plan; --all forces every series;
# --jobs N runs at most N independent checks at a time (default: the core count).
# --since <base> is an explicit range diagnostic, NOT completion approval: it never
# advances the approved point. Records live in the common Git directory, shared by
# worktrees, and the approved point is kept per branch: a verified run advances only the
# branch it ran on, and plans from the newest verified commit its history contains. A
# small change is quick once all earlier pending work is verified.
# This program also imports every fast whole-tree declaration from tools/gate/checks/,
# using tillr-gate checks over the same last-verified..HEAD range as the remaining suites.
# The drain and measured faster tillr modules use go-test-shards; drain stays rolling,
# and this complete gate includes that tier.
done *args:
    @python3 tools/fork-check.py {{args}}

# Whole-module execution; prefer targeted `go test -run` while editing.
go-test-shards module="tools/tillr-drain" *args:
    @python3 tools/go-test-shards.py {{module}} {{args}}

# Run every check this change plans under the gate's own conditions -- HOME and
# XDG_STATE_HOME scrubbed, BOARD_STORE_STRICT, this process's uid, one check at a time --
# and report every failure in one pass. A diagnostic, not a verdict: it writes no record,
# advances no verified baseline and takes no lock, so it runs beside a real gate. Read its
# findings BEFORE attempting a fix; `--cells`, `--only`, `--report PATH` and `--json`
# narrow it, and a check that cannot pass a cell declares it in
# tools/preflight-declarations.json with the reason and the repair it waits on. There is
# no cell that adds CPU load: the burner cell was deleted on 2026-10-31, and
# tests/fork-check.test.py fails the gate if a check asserts elapsed time or starts load.
preflight *args:
    @python3 tools/fork-check.py --preflight {{args}}

# Put every patch branch this repository requires on the remote: one per series in
# bin/pi-forks.list, plus every branch a pack manifest names. A patch branch is the
# delivery only once the remote has it, and nothing else here pushes one: bin/pi-forks
# never pushes, and hooks/pre-push runs this same publish step first on every push it
# inspects, so an ordinary push is what keeps origin current. Needs push credentials, so
# run it on the host; `bin/pi-forks patch-branch published` is what proves, read-only,
# that a published branch replays to its series.
forks_publish *args:
    @bin/patch-branches-publish {{args}}

# Deploy any patched Arch package fork this checkout carries: `pkg-deploy sway`,
# `pkg-deploy ironbar`, `pkg-deploy ai-usagebar`. One command covers both build
# kinds (chroot + the signed `patched-local` repo, and AUR recipe + makepkg); an
# unchanged run is read-only. The ai-usagebar deploy regenerates its patch
# against the current AUR recipe and checks the delta before it builds. It is
# unrelated to the pi patch series
# (`bin/pi-forks`), which yolo builds and ships.
pkg-deploy *args:
    @python3 tools/arch-deploy.py {{args}}

# Test deployment orchestration, the shared repo strategy and Sway inputs
# without sudo or host changes.
sway-check:
    python3 -m pytest tests/test_sway_fork.py tests/test_sway_deploy.py tests/test_repo_forks.py
    sh tests/sway-floating-modifier.test.sh
    sh tests/sway-focus-tab.test.sh
    sh tests/sway-workspace-numbers.test.sh
    sh tests/media-toggle.test.sh
    sh tests/media-toggle-setup.test.sh
    sh tests/tv-volume.test.sh

# The ai-usagebar package patch is generated from the local delta, never
# hand-edited: it embeds that delta in the AUR recipe, carries the revision,
# pins the manifest at the recipe version, and dry-runs the delta against the
# upstream tag the recipe names. `--check` reports that drift without writing.
ai-usagebar-fork-patch *args:
    @python3 tools/ai-usagebar-fork-patch.py {{args}}

# Test the ai-usagebar fork inputs, its deployment orchestration, and the waybar
# modules that consume them, without sudo or a package build.
ai-usagebar-check:
    python3 -m pytest tests/test_ai_usagebar_fork.py tests/test_ai_usagebar_deploy.py
    sh tests/waybar-ai-modules.test.sh

# The ironbar package patch is generated from the local delta, never hand-edited:
# it embeds that delta in Arch's recipe and carries the revision.
ironbar-fork-patch *args:
    @python3 tools/ironbar-fork-patch.py {{args}}

# Test the ironbar fork inputs and the shared repo strategy, without sudo or a
# package build.
ironbar-check:
    python3 -m pytest tests/test_ironbar_fork.py tests/test_repo_forks.py

# Provision and verify the Google TV control on this host (docs/media-toggle.md).
# Needs adb (android-tools); it never installs a package, and never starts a
# daemon - the runtime is just bin/media-toggle over adb.
media-toggle-setup *args:
    rcup -v bin/media-toggle bin/media-toggle-setup config/sway/config
    ~/.bin/media-toggle-setup {{args}}

# Show help
help:
    @echo "Available commands:"
    @echo "  nvim_dist        - Create timestamped nvim config archive (matt-nvim-config-<timestamp>.tar.bz2)"
    @echo "  nvim_dist_clean  - Create timestamped archive excluding temp files"
    @echo "  nvim_dist_list   - List contents of latest archive"
    @echo "  nvim_dist_test   - Extract and test the latest archive"
    @echo "  nvim_dist_cleanup- Clean up test directory"
    @echo "  nvim_release     - Release nvim config to .nvim-config-public repo"
    @echo "  nvim_docker_build      - Build Docker image for testing"
    @echo "  nvim_docker_test       - Build and run nvim in Docker container"
    @echo "  forks            - Status of every pi fork checkout against its patch series"
    @echo "  forks_setup      - Materialize .forks/<name> from its series (--force saves a divergent main)"
    @echo "  forks_export     - Write a checkout's main back as the delivery (series or patch branch)"
    @echo "  forks_check      - Fail when a series is unreadable or does not replay to main"
    @echo "  forks_rebase     - Rebase a series onto the followed upstream, re-export, verify"
    @echo "  forks_series_check - The launch's series verdict, per pack (never the workspace root)"
    @echo "  forks_publish    - Push every required patch branch to the remote (needs credentials; host)"
    @echo "  forks_published  - Fail when a series' published branch is missing or is not that series"
    @echo "  patch-branch-check - Patch branch: releases behind and the launch's merge question (read-only)"
    @echo "  hooks-install    - Install this repository's git hooks into the shared hooks directory"
    @echo "  forks_diff       - What each checkout's main changes over its series' base"
    @echo "  forks_build      - npm ci + the checkout's build for each checkout (local dev build)"
    @echo "  forks_run        - Run the forked pi from its checkout (testing loop)"
    @echo "  done             - Check since last verified commit; --explain for plan, --all for every series"
    @echo "  pkg-deploy       - Build/install a patched Arch package fork (pkg-deploy <name>)"
    @echo "  sway-check       - Test Sway inputs and deployment without host changes"
    @echo "  ai-usagebar-fork-patch - Regenerate the package patch from the local delta"
    @echo "  ai-usagebar-check  - Test ai-usagebar fork inputs, deploy logic, waybar modules"
    @echo "  ironbar-fork-patch - Regenerate the ironbar package patch from the local delta"
    @echo "  ironbar-check      - Test ironbar fork inputs and the shared repo strategy"
    @echo "  profiler-build   - Build the self-profiler binary (tools/pi-profiler/pi-profiler-go)"
    @echo "  queued-message-states - Drive pi's queued-message states headlessly (tools/queued-message-states)"
    @echo "  session-liveness - Measure pi's session liveness headlessly; expected GREEN (tools/session-liveness)"
    @echo "  help             - Show this help"
    @echo ""
    @echo "Note: Archives are automatically cleaned up (keeps only the most recent version)"

