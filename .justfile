# =============================================================================
# TILLR BUILD SYSTEM
# =============================================================================
# Recipes for building, testing and operating tillr; see README.md

# Default command - show help
default:
    @just --list

# The standard recipe set. `check` is what to run while working; `check-ci` is what CI
# runs and never rewrites a file. `done` (below) is the full local gate.
check: format lint test

check-ci: lint-ci test

# Fetch every Go module's dependencies and the observatory front end's packages.
setup:
    #!/usr/bin/env bash
    set -euo pipefail
    for mod in tools/*/go.mod; do (cd "$(dirname "$mod")" && go mod download); done
    (cd tools/tillr-view/web && npm ci)

# Vet every Go module.
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    for mod in tools/*/go.mod; do (cd "$(dirname "$mod")" && go vet ./...); done

# The same, after refusing unformatted Go instead of rewriting it.
lint-ci:
    @just format --check
    @just lint

# Run every Go module's tests; arguments go to `go test`. With no arguments the drain
# runs in parallel shards, because its whole suite exceeds `go test`'s ten-minute limit.
[positional-arguments]
test *args:
    #!/usr/bin/env bash
    set -euo pipefail
    for mod in tools/*/go.mod; do
        dir="$(dirname "$mod")"
        if [ "$#" -eq 0 ] && [ "$dir" = "tools/tillr-drain" ]; then
            go run -C tools/tillr-gate . shards "$dir" -k 6
        else
            (cd "$dir" && go test ./... "$@")
        fi
    done

# Python is excluded: adopting its formatter would rewrite 84 files at once.
# shell, TypeScript, and Lua are excluded because they have no pinned formatter.
# Adding a language means pinning its formatter in mise.toml and landing the reformat
# as its own commit. Only tracked source is ours; vendor trees are not.
# Format Go with gofmt; --check lists changes without writing and exits 1 on drift.
[positional-arguments]
format *args:
    #!/bin/sh
    set -eu
    if [ -x tools/gate/repo-gate ]; then
        exec tools/gate/repo-gate format "$@"
    fi
    check=0
    case "$#" in
        0) ;;
        1)
            if [ "$1" != "--check" ]; then
                printf 'usage: just format [--check]\n' >&2
                exit 2
            fi
            check=1
            ;;
        *)
            printf 'usage: just format [--check]\n' >&2
            exit 2
            ;;
    esac
    files=$(git ls-files -- 'tools/' 'yolo-packs/tillr/files/local/share/tillr/' | grep -E '(^|/)[^/]+\.go$' || true)
    if [ -z "$files" ]; then
        exit 0
    fi
    if [ "$check" -eq 1 ]; then
        dirty=$(printf '%s\n' "$files" | xargs gofmt -l)
        if [ -n "$dirty" ]; then
            printf '%s\n' "$dirty"
            exit 1
        fi
        exit 0
    fi
    printf '%s\n' "$files" | xargs gofmt -w

# Install this repository's git hooks (hooks/*) into the shared hooks directory, so they
# run from the main checkout and every linked worktree.
# A copy git already runs is replaced when it carries this repository's marker, and a
# hook without the marker is left alone -- kept as <name>.local, which our hook runs
# first -- so every run reports installed, replaced, left alone or drifted (see
# hooks/README.md and bin/hooks-install).
hooks-install *args:
    @bin/hooks-install {{args}}

# Mirror the tillr pack's copy of every `tools/tillr*` module into
# yolo-packs/tillr/files/local/share/tillr/<module>. The copy is committed because the
# pack -- not this checkout -- is what puts `tillr` on PATH in a workspace that has no
# tools/. `go run -C tools/tillr ./cmd/pack-sync-check` fails when the copy diverges from its source.
# With module arguments, render only those modules. The drain calls the same
# `packmirror.Sync` implementation so it can work on candidates with earlier versions of this recipe.
[positional-arguments]
pack-sync *modules:
    #!/usr/bin/env bash
    set -euo pipefail
    root="$(cd "$(dirname "{{justfile()}}")" && pwd)"
    exec go run -C "$root/tools/tillr" ./cmd/pack-mirror --sync "$root" "$@"

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
# store, and the launched binaries then refuse it. docs/reference/tillr-distribution.md,
# "Running a tree the pack has not rendered", states the rule.
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
        proof="-X github.com/mschulkind-oss/tillr-drain/drain.greenRevision=$green -X github.com/mschulkind-oss/tillr/buildstamp.greenRevision=$green"
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
    for module in tillr-cli tillr-drain tillr-gate tillr-item tillr-land tillr-loop tillr-run tillr-store tillr-trace tillr-view; do
        [ -f "$work/source/tools/$module/go.mod" ] || continue
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
        mv -f "$program" "$out/${program##*/}"
        printf 'tillr-build: %s/%s\n' "$out" "${program##*/}"
    done
    printf 'tillr-build: prepend %s to PATH for the loop and its children: PATH=%s:$PATH\n' "$out" "$out"

# Test, build and smoke the committed checkout before updating the live bin directory.
[positional-arguments]
tillr-roll *args:
    @CGO_ENABLED=0 go run -C tools/tillr-roll . "$@"

# Keep this checkout's loop alive; --stop stops only the keeper. Pass loop options after --.
# Positional arguments keep each one intact, including an empty `--rolling ''`.
[positional-arguments]
tillr-keep *args:
    @tillr-drain keep "$@"

# Stand the keeper down, stop the loop, wait for exit, build from HEAD, and restart the keeper
# with the running loop's (or recorded launch's) policy and lane count. Safe when no loop runs.
[positional-arguments]
tillr-restart *args:
    @tillr-drain keep --restart "$@"

# Check the one generated pack copy: the tillr pack's tree against tools/tillr*,
# module by module, byte for byte, with the README rewrite, the exec bits, and the
# launchers' shared body.
pack-sync-test:
    @go run -C tools/tillr ./cmd/pack-sync-check

# Refresh the tillr pack's dated snapshot of tillr's design documentation: the status
# index docs/tillr/design-status.md and every document its tables mark `shipped`, copied
# from HEAD into yolo-packs/tillr/files/local/share/tillr/docs/ (`tools/tillr/cmd/pack-docs`), then
# judged current against HEAD. Commit the result. Deliberately NOT part of `pack-sync`:
# lanes run pack-sync, the docs tree is outside a lane's fence, and the drain edits design
# documents on every landing, so the snapshot is refreshed by hand, never per landing.
pack-docs:
    @go run -C tools/tillr ./cmd/pack-docs sync
    @go run -C tools/tillr ./cmd/pack-sync-check --fresh

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
# WHAT RELOADS: the UI through Vite, and the API through `air` (pinned in `mise.toml`, configured
# in `tools/tillr-view/.air.toml`): a change to a Go source tillr-view is built from, including
# commits landing on this checkout and the local modules it replaces, is rebuilt once per burst
# and replaces the API on its port. A failed build prints its error here and leaves the old API
# answering.
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

# End of task: judge the committed tree with every declared check
# (tools/gate/checks/, one JSON file each), using tillr-gate built from this checkout.
# First the change checks and the fast whole-tree checks over BASE..HEAD, then the
# heavy whole-tree checks (the Go suites and the script suites) on HEAD. BASE is
# refs/tillr/green, else the upstream branch, else HEAD's first parent. Commit first:
# the checks judge HEAD. --since <base> names the range; --fast runs the first pass
# only; --only <id> runs one heavy check. It writes no verdict record and moves no ref.
done *args:
    @tools/gate/repo-gate done {{args}}

# Whole-module execution; prefer targeted `go test -run` while editing.
go-test-shards module="tools/tillr-drain" *args:
    @go run -C tools/tillr-gate . shards {{module}} {{args}}
