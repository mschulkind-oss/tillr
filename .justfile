# =============================================================================
# TILLR BUILD SYSTEM
# =============================================================================
# Modern build system for repository management

# Default command - show help
default:
    @just help

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
pack-sync:
    #!/usr/bin/env bash
    set -euo pipefail
    root="$(cd "$(dirname "{{justfile()}}")" && pwd)"

    # A copied README must not link out of the pack: `../../docs/...` is correct beside
    # tools/ and dead here, where it resolves to files/local/docs/... and the repo gate
    # reads that as a broken link. Point the same prose at the repository on GitHub
    # instead; the source README keeps the relative links that work where it lives.
    mirror() {
        local module="$1" dst="$2" rel file
        rm -rf "$dst"
        mkdir -p "$dst"
        # Every file git would commit -- tracked, plus untracked-but-not-ignored, because
        # a module can be mid-work and still be the source the launcher builds. An IGNORED
        # file is a build output (tools/tillr-store/tillr-store), and the pack ships
        # source, never a compiled artifact.
        {
            git -C "$root" ls-files -z "tools/$module"
            git -C "$root" ls-files -z --others --exclude-standard "tools/$module"
        } | while IFS= read -r -d '' file; do
            rel="${file#tools/$module/}"
            mkdir -p "$dst/$(dirname "$rel")"
            cp -p "$root/$file" "$dst/$rel"
        done
        # A link that resolves outside the module points at nothing in the pack, because the
        # repository's docs/ tree is not there; it is rewritten to the repository on GitHub.
        # A link that stays inside the module is left alone -- a sed that rewrites every
        # `../` turns `../brief-template.md`, a sibling in the same pack, into a URL naming a
        # different file, and a reference that names one file and opens another is worse than
        # no link at all. The resolver is a program because that distinction needs one.
        python3 "$root/tools/pack-mirror-links.py" "$dst"
        echo "pack-sync: mirrored tools/$module -> ${dst#"$root"/}"
    }

    # Every module that ships, which is not the same set as the modules that ship with a
    # launcher. Nine Go modules build the eight names on PATH: the shared library `tillr`
    # is part of the tree because the binaries resolve it through relative `replace`
    # paths in their go.mod files, `tillr-cli` builds the `tillr` dispatcher, and
    # `tillr-drain` replaces two sibling modules as well. `tillr-trace` is the tenth and
    # ships as source only: `tillr-run`'s pi adapter sources its `bash-shim.sh` through
    # `BASH_ENV`, and nothing on PATH is built from it.
    # tillr-roll is source-only: the repository's keeper is run by the recipe below.
    for module in tillr tillr-cli tillr-drain tillr-gate tillr-item tillr-land tillr-run tillr-store tillr-view tillr-trace tillr-roll; do
        mirror "$module" "$root/yolo-packs/tillr/files/local/share/tillr/$module"
    done

# Build the tillr programs from THIS tree into $YOLO_DURABLE_DIR/bin, so a change to tools/tillr*
# can be run in the launch you are in instead of waiting for the next render. The launcher the pack
# puts on PATH rebuilds from `~/.local/share/tillr` on first call after a change, but that tree is
# the pack's RENDERED copy, written at launch: `just pack-sync` only rewrites the repository's
# yolo-packs copy, and a host render is what puts it in the launch's home. This recipe is the
# working tree's own build.
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
tillr-build:
    #!/usr/bin/env bash
    set -euo pipefail
    out="${YOLO_DURABLE_DIR:-${TMPDIR:-/tmp}}/bin"
    mkdir -p "$out"
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
            if [ -f "tools/tillr-view/web/dist/index.html" ]; then
                tags="-tags observatory"
            else
                printf 'tillr-build: %s carries no front end: run `npm --prefix tools/tillr-view/web ci && npm --prefix tools/tillr-view/web run build`, then this recipe again (or pass --web-dist to its serve face)\n' "$name"
            fi
        fi
        ( cd "tools/$module" && CGO_ENABLED=0 go build $tags -o "$out/$name" . )
        printf 'tillr-build: %s\n' "$out/$name"
    done
    printf 'tillr-build: prepend %s to PATH for the loop and its children: PATH=%s:$PATH\n' "$out" "$out"

# Keep this checkout's loop alive; --stop stops only the keeper. Pass loop options after --.
tillr-keep *args:
    @python3 tools/tillr-roll/tillr-keep {{args}}

# Check the one generated pack copy: the tillr pack's tree against tools/tillr*,
# module by module, byte for byte, with the README rewrite, the exec bits, and the
# eight launchers' shared body.
pack-sync-test:
    @python3 tests/pack-sync.test.py

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

# Build the drain observatory's front end and serve it with the API from one process on
# http://127.0.0.1:<port> (`just observatory 5000`, or `port=5000`; the default is 4318).
# `--store`, `--root` and the rest pass through to `tillr-view serve`, and a passthrough
# flag beats the default this recipe supplies for the same option. The binary is built with
# the `observatory` tag so the built assets are embedded: no Node runtime is needed to look
# at a drain.
observatory port="4318" *args:
    #!/usr/bin/env bash
    set -euo pipefail
    web=tools/tillr-view/web
    # Reinstall when the tree has no modules AND when the lockfile has moved past them. A
    # jail and its host share this directory, so "node_modules exists" is not the same
    # question as "node_modules is current": a package.json that gains a dependency (as this
    # one did when the board began rendering Markdown) would otherwise build against the old
    # set and fail on an import that is plainly in the lockfile.
    if [ ! -d "$web/node_modules" ] || [ "$web/package-lock.json" -nt "$web/node_modules/.package-lock.json" ]; then
        npm --prefix "$web" ci
    fi
    npm --prefix "$web" run build
    bin="${YOLO_DURABLE_DIR:-${TMPDIR:-/tmp}}/tillr-view-observatory"
    mkdir -p "$(dirname "$bin")"
    (cd tools/tillr-view && go build -tags observatory -o "$bin" .)
    args=({{args}})
    has() { local want="$1" f; for f in "${args[@]}"; do [ "${f%%=*}" = "$want" ] && return 0; done; return 1; }
    cmd=(serve)
    has --store || cmd+=(--store .tillr/state.db)
    has --root || cmd+=(--root .)
    has --addr || cmd+=(--addr "127.0.0.1:{{port}}")
    exec "$bin" "${cmd[@]}" "${args[@]}"

# Develop against the observatory with hot reload. The port you name is the one you OPEN --
# Vite serves the UI there and proxies `/api` to a Go server one port above it (so two dev
# runs do not collide); a caller-named `--addr` moves the API and the proxy follows it.
# Live reload is for code, not data (design §5.3).
#
# TWO PORTS, AND THE UI IS THE LOWER ONE. Opening the API port answers a bare JSON service,
# and before 2026-11-14 it answered "this binary carries no built front end" for `/`, which
# reads exactly like a broken install and is not: the UI was on the other port. The line
# this recipe prints names both, and it prints the API's build stamp, so "which program am
# I looking at" is answered by both panes.
#
# NO `npm run build` HERE, deliberately. Vite serves the UI from source and proxies every
# `/api` call to the Go server, so the Go server's own `/` handler is never reached: building
# `dist/` first cost this recipe 28 s per restart to produce assets nothing requested. The
# dev binary is built without the `observatory` tag for the same reason -- embedded assets
# would be a stale snapshot of the code Vite is serving -- so it has no UI to serve and does
# not pretend to. A caller who wants `--web-dist` anyway still gets it passed through.
#
# ONE SUPERVISED TREE, NOT TWO BACKGROUND JOBS. The processes are declared in
# `tools/tillr-view/Procfile.dev` and run under hivemind (pinned in `mise.toml`), which is
# what makes this recipe safe to interrupt: a child that dies takes the other with it, and
# SIGINT or SIGTERM tears down both process groups, so `npm`'s `vite` cannot outlive the
# API or the terminal. The recipe this replaces backgrounded the API and then `exec`'d Vite,
# which discarded the `EXIT` trap that was supposed to kill it; an API left holding the port
# then made the NEXT run answer `bind: address already in use` while Vite proxied to the
# previous process's stale board. A port that is already answering is refused up front for
# the same reason: the last way to serve the wrong board is to serve an old process's.
observatory-dev port="4318" *args:
    #!/usr/bin/env bash
    set -euo pipefail
    web=tools/tillr-view/web
    args=({{args}})
    # The API's address: one above the UI port unless the caller names one, in which case
    # that address is used for the server AND for the proxy -- a proxy pinned to a port the
    # server is not on is the same silent freeze a missing `ws: true` produces.
    api="127.0.0.1:$(( {{port}} + 1 ))"
    for ((i = 0; i < ${#args[@]}; i++)); do
        case "${args[i]}" in
            --addr) api="${args[i + 1]:-$api}"; unset 'args[i]' 'args[i + 1]' ;;
            --addr=*) api="${args[i]#--addr=}"; unset 'args[i]' ;;
        esac
    done
    # Refuse a port that is already answering, before any build. The usual holder is a
    # previous face that outlived its terminal, and letting the API discover it is the worst
    # way to find out: the API dies on `bind: address already in use`, Vite stays up, and the
    # board on screen is that older process's -- a stale snapshot, or a verdict it cached at
    # its own start. This check runs before the toolchain so a held port costs a second, not
    # a `go build`.
    apihost="${api%:*}"; apiport="${api##*:}"
    [ "$apihost" = "0.0.0.0" ] && apihost=127.0.0.1
    if (exec 3<>"/dev/tcp/${apihost}/${apiport}") 2>/dev/null; then
        printf 'observatory-dev: %s is already answering, so this run would not be the face you see.\n' "$api" >&2
        if command -v ss >/dev/null; then ss -ltnp "sport = :$apiport" >&2 || true; fi
        printf 'observatory-dev: kill it and run this again -- a previous observatory that outlived its terminal is the usual cause:\n' >&2
        printf '  fuser -k %s/tcp    # or kill the pid ss named above\n' "$apiport" >&2
        exit 1
    fi
    # Reinstall when the tree has no modules AND when the lockfile has moved past them. A
    # jail and its host share this directory, so "node_modules exists" is not the same
    # question as "node_modules is current": a package.json that gains a dependency (as this
    # one did when the board began rendering Markdown) would otherwise build against the old
    # set and fail on an import that is plainly in the lockfile.
    if [ ! -d "$web/node_modules" ] || [ "$web/package-lock.json" -nt "$web/node_modules/.package-lock.json" ]; then
        npm --prefix "$web" ci
    fi
    bin="${YOLO_DURABLE_DIR:-${TMPDIR:-/tmp}}/tillr-view-observatory-dev"
    mkdir -p "$(dirname "$bin")"
    (cd tools/tillr-view && go build -o "$bin" .)
    # No `--web-dist` default: this recipe builds none, and a `dist/` left over from an
    # earlier `just observatory` is a stale snapshot nobody asked for. A caller who names
    # one gets it (their args pass through).
    #
    # `--ui-at` is what makes the API's own account of itself true. Without it this process
    # has no front end and says `front-end none`, which the board drew beside its build —
    # reading as "this board has no UI" while the board was on screen, served by Vite. The
    # stamp names where the UI is instead, so `build <rev> · vite` is the honest line.
    # Both are defaults the Procfile supplies; a caller's own flag comes after them and wins.
    export TILLR_VIEW_BIN="$bin"
    export TILLR_VIEW_STORE=".tillr/state.db"
    export TILLR_VIEW_ROOT="."
    export TILLR_VIEW_ADDR="$api"
    export TILLR_VIEW_PORT="{{port}}"
    export TILLR_VIEW_WEB="$web"
    export TILLR_VIEW_API_ARGS="${args[*]}"
    export TILLR_VIEW_API="http://$api"
    printf 'observatory-dev: open the UI at http://localhost:%s  (the API on %s is JSON only, never the UI)\n' "{{port}}" "$api"
    # `exec`, deliberately: hivemind is the supervisor now, and it traps the signals itself.
    # The trap this recipe used to set never ran, because `exec` replaces the shell that set
    # it -- which is exactly the orphan this hands to a process manager that starts each
    # child in its own process group and interrupts the group.
    exec hivemind --root . --timeout 10 tools/tillr-view/Procfile.dev

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
done *args:
    @python3 tools/fork-check.py {{args}}

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

