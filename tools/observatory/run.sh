#!/usr/bin/env bash
# `just observatory` and `just observatory-dev`: serve THIS checkout's board from THIS
# checkout's build, or refuse in one sentence that names the fix.
#
#   run.sh serve <port> [serve flags...]   one process, front end embedded
#   run.sh dev   <port> [serve flags...]   Vite on <port>, the API one above it
#
# What "this checkout" means, and how each part is pinned (2026-11-16):
#
#   - The root is this script's own checkout, as an absolute path, whatever directory the
#     recipe was started from and whatever path the checkout is mounted at (a container's
#     /workspace is the host's ~/code/tillr). Every file the server reads is passed to it from
#     that root: the store, the root, and the planner.
#   - Both programs the board needs, tillr-view and the tillr-drain it asks for Waiting, are
#     built here, from this tree, into one directory per checkout. Nothing is found on PATH:
#     a PATH whose stale copy came first, or that held none, is how the Waiting panel showed
#     another build's answer or none at all. The directory is keyed by the root's path, so two
#     checkouts never share (or overwrite) each other's binaries.
#   - The builds are static (CGO_ENABLED=0), as `just tillr-build` and the pack launcher's are,
#     so a binary built in the container also runs on the host and the other way round.
#   - node_modules is shared by a host and a container that may differ in platform, so it is
#     stamped with the platform that installed it and reinstalled when that differs.
#
# Toolchain programs (go, git, node, npm, hivemind) do come from PATH: they are the tools,
# not the board. A missing one is refused by name before anything is built.
set -euo pipefail

# The recipe's own clock, for the performance log's startup record: bash's EPOCHREALTIME is
# read without a subprocess, and a bash without it (before 5.0) reports no phases rather than
# wrong ones.
now_ms() {
    local t="${EPOCHREALTIME:-}"
    [ -n "$t" ] || return 1
    t="${t/,/.}"
    local s="${t%.*}" f="${t#*.}000"
    printf '%s%s' "$s" "${f:0:3}"
}
recipe_start="$(now_ms || true)"
phases=""
phase_start=""
begin_phase() { phase_start="$(now_ms || true)"; }
end_phase() {
    local end
    end="$(now_ms || true)"
    [ -n "$phase_start" ] && [ -n "$end" ] && phases="${phases:+$phases,}$1=$((end - phase_start))"
    phase_start=""
}

mode="${1:?usage: run.sh serve|dev <port> [args...]}"
port="${2:?usage: run.sh serve|dev <port> [args...]}"
shift 2
args=("$@")
name="observatory"
[ "$mode" = dev ] && name="observatory-dev"

refuse() {
    printf '%s: %s\n' "$name" "$*" >&2
    exit 1
}

case "$port" in
    '' | *[!0-9]*) refuse "the port must be a number, and \`$port\` is not: run \`just $name 4318\`" ;;
esac

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
view_src="$root/tools/tillr-view"
drain_src="$root/tools/tillr-drain"
web="$view_src/web"

has() {
    local want="$1" f
    for f in "${args[@]}"; do [ "${f%%=*}" = "$want" ] && return 0; done
    return 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || refuse "$2"
}

# --- the jail's ports -----------------------------------------------------------------------
# In a yolo jail (bridged networking) the container's localhost is its own: a server bound to
# 127.0.0.1 there answers only inside the container. The host reaches a jail port only when
# the WORKSPACE's config publishes it -- the jail's /workspace/yolo-jail.toml, which is
# $YOLO_HOST_DIR/yolo-jail.toml on the host, whichever checkout this is -- and then the
# server must bind 0.0.0.0, the container's side of that publish. That file is the only
# record of a publish the jail can read; it cannot see whether the running jail was relaunched
# since the line was added, so the note says that too. OBSERVATORY_JAIL_WORKSPACE moves the
# workspace for this recipe's own tests.
jail_ws="${OBSERVATORY_JAIL_WORKSPACE:-/workspace}"
host_ws="${YOLO_HOST_DIR:-the workspace directory on the host}"
published() {
    local want="$1" file
    for file in "$jail_ws/yolo-jail.toml" "$jail_ws/yolo-jail.jsonc"; do
        [ -f "$file" ] || continue
        # TOML `jail = N` or JSONC `"jail": N`, inside a ports entry.
        if [[ "$(<"$file")" =~ (^|[^0-9a-z_])\"?jail\"?[[:space:]]*[=:][[:space:]]*${want}([^0-9]|$) ]]; then
            return 0
        fi
    done
    return 1
}

bind_host=127.0.0.1
jail_note=""
if [ "${YOLO_JAIL:-}" = 1 ]; then
    if published "$port"; then
        bind_host=0.0.0.0
        jail_note="port $port is published in $jail_ws/yolo-jail.toml, so the host opens http://localhost:$port (if it does not answer there, this jail predates that line: run \`yolo config apply\` and \`yolo stop\` on the host, then relaunch)"
    else
        jail_note="this is a yolo jail and port $port is not published, so only this container can open http://127.0.0.1:$port; to open it from the host, add
  [machine]
  ports = [{ host = $port, jail = $port }]
to $host_ws/yolo-jail.toml on the host ($jail_ws/yolo-jail.toml in here), run \`yolo config apply\` and \`yolo stop\` on the host, relaunch the jail, and run this again"
    fi
fi

# --- dev: refuse a held API port before any build --------------------------------------------
# The usual holder is a previous face that outlived its terminal, and letting the API discover
# it is the worst way to find out: the API dies on `bind: address already in use`, Vite stays
# up, and the board on screen is that older process's. Checked before the toolchain so a held
# port costs a second, not a `go build`.
api=""
if [ "$mode" = dev ]; then
    api="127.0.0.1:$((port + 1))"
    kept=()
    for ((i = 0; i < ${#args[@]}; i++)); do
        case "${args[i]}" in
            --addr) api="${args[i + 1]:-$api}"; i=$((i + 1)) ;;
            --addr=*) api="${args[i]#--addr=}" ;;
            *) kept+=("${args[i]}") ;;
        esac
    done
    args=("${kept[@]+"${kept[@]}"}")
    apihost="${api%:*}"; apiport="${api##*:}"
    case "$apihost" in '' | 0.0.0.0 | '[::]') apihost=127.0.0.1 ;; esac
    if (exec 3<>"/dev/tcp/${apihost}/${apiport}") 2>/dev/null; then
        printf '%s: %s is already answering, so this run would not be the face you see.\n' "$name" "$api" >&2
        if command -v ss >/dev/null 2>&1; then ss -ltnp "sport = :$apiport" >&2 || true; fi
        printf '%s: stop it and run this again -- a previous observatory that outlived its terminal is the usual cause:\n' "$name" >&2
        printf '  fuser -k %s/tcp    # or kill the pid ss named above\n' "$apiport" >&2
        exit 1
    fi
fi

# --- toolchain -------------------------------------------------------------------------------
need go "go is not on PATH, and both programs are built from this checkout with it: install Go (the version in $root/mise.toml) or run from a shell where \`go version\` works"
need git "git is not on PATH, and go stamps each build with the checkout's revision through it (the stamp is how the board tells its own build from an older one): install git or run from a shell that has it"

# --- the front end ---------------------------------------------------------------------------
# The platform node_modules was installed for. A host and a container share this directory,
# and esbuild and rollup ship one native binary per platform, so modules installed by one side
# fail on the other with an error about a missing @esbuild/<platform> package.
node_platform() {
    node -p 'process.platform + "-" + process.arch + (process.platform === "linux" ? (process.report.getReport().header.glibcVersionRuntime ? "-gnu" : "-musl") : "")'
}

ensure_modules() {
    local stamp="$web/node_modules/.tillr-platform" platform reason=""
    platform="$(node_platform)"
    if [ ! -d "$web/node_modules" ]; then
        reason="there are no node_modules"
    elif [ "$web/package-lock.json" -nt "$web/node_modules/.package-lock.json" ]; then
        reason="package-lock.json is newer than node_modules"
    elif [ ! -f "$stamp" ]; then
        reason="node_modules names no platform"
    elif [ "$(<"$stamp")" != "$platform" ]; then
        reason="node_modules was installed for $(<"$stamp"), and this is $platform"
    fi
    if [ -n "$reason" ]; then
        printf '%s: npm ci (%s)\n' "$name" "$reason" >&2
        begin_phase
        npm --prefix "$web" ci --no-audit --no-fund ||
            refuse "npm ci in $web failed (its output is above), so the front end has no modules to build or serve with: fix what npm names (a cache directory it cannot write is set with NPM_CONFIG_CACHE) and run this again"
        printf '%s\n' "$platform" >"$stamp"
        end_phase npm_ci
    fi
}

# dist is current when it exists and no source it is built from is newer than it.
dist_current() {
    local built="$web/dist/index.html" f
    [ -f "$built" ] || return 1
    for f in "$web/index.html" "$web/package-lock.json" "$web/package.json" "$web/vite.config.ts" "$web/tsconfig.json"; do
        [ -e "$f" ] && [ "$f" -nt "$built" ] && return 1
    done
    shopt -s globstar nullglob
    for f in "$web"/src/**; do
        [ -f "$f" ] && [ "$f" -nt "$built" ] && return 1
    done
    return 0
}

if [ "$mode" = dev ]; then
    need node "node is not on PATH, and the dev face serves its UI through Vite: install Node (the version in $root/mise.toml), or run \`just observatory\`, which needs no Node once its front end is built"
    need npm "npm is not on PATH, and the dev face installs and runs Vite with it: install Node with npm (the version in $root/mise.toml)"
    need hivemind "hivemind is not on PATH, and it supervises the API and Vite together: install it (pinned in $root/mise.toml) or run \`just observatory\`, which is one process"
    ensure_modules
elif ! has --web-dist; then
    if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
        ensure_modules
        begin_phase
        if ! dist_current; then
            npm --prefix "$web" run build ||
                refuse "npm run build in $web failed (its output is above), so there is no current front end to embed: fix the build and run this again"
            end_phase web_build
        else
            end_phase web_current_check
        fi
    elif ! dist_current; then
        refuse "the front end in $web/dist is missing or older than its sources, and node is not on PATH to rebuild it: install Node (the version in $root/mise.toml), or run this once from a shell that has it, after which no Node is needed"
    fi
fi

# --- the two programs, built from this tree ------------------------------------------------
# One directory per checkout, keyed by the root's path: two checkouts must not run each
# other's binaries. Each is built to a temporary name and moved into place, so a server
# already running keeps the file it started from.
digest_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        printf '%s' "$1" | sha256sum
    else
        printf '%s' "$1" | shasum -a 256
    fi
}
key="$(digest_of "$root")"
key="${key:0:16}"
out="${YOLO_DURABLE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}}/tillr-observatory/$key"
mkdir -p "$out"
printf '%s\n' "$root" >"$out/checkout"

build() {
    local dest="$1" src="$2" tmp
    shift 2
    tmp="$dest.building.$$"
    if ! (cd "$src" && CGO_ENABLED=0 go build "$@" -o "$tmp" .); then
        rm -f "$tmp"
        refuse "go build in $src failed (its output is above): fix the build and run this again"
    fi
    mv -f "$tmp" "$dest"
}

view="$out/tillr-view"
[ "$mode" = dev ] && view="$out/tillr-view-dev"
drain="$out/tillr-drain"
begin_phase
if [ "$mode" = dev ]; then
    build "$view" "$view_src"
else
    build "$view" "$view_src" -tags observatory
fi
end_phase go_build_view
begin_phase
build "$drain" "$drain_src"
end_phase go_build_drain

# --- the performance log ---------------------------------------------------------------------
# Every request, WebSocket window and browser report is a JSON line (tools/tillr-view
# perflog.go), written under THIS checkout's durable scratch directory -- never under the build
# directory's ~/.cache fallback above. A host and its jail mount the same checkout, so a log the
# host writes at <root>/.yolo/durable/... is the jail's /workspace/.yolo/durable/..., and an
# agent in the jail reads what the owner's browser met. One directory per host and port, so two
# servers never interleave one file. OBSERVATORY_PERF_LOG moves it; `off` turns it off.
perf_log="${OBSERVATORY_PERF_LOG:-$root/.yolo/durable/tillr-observatory/logs/${HOSTNAME:-host}-$port}"
export TILLR_VIEW_PERF_LOG="$perf_log"
export TILLR_VIEW_PERF_STARTUP="${phases}${recipe_start:+${phases:+,}recipe_start_ms=$recipe_start}"
if [ "$perf_log" = off ]; then
    printf '%s: performance log: off (OBSERVATORY_PERF_LOG=off)\n' "$name" >&2
else
    printf '%s: performance log: %s (summarize it with `just observatory-perf`)\n' "$name" "$perf_log" >&2
fi

if [ -n "$jail_note" ] && { [ "$mode" = dev ] || ! has --addr; }; then
    printf '%s: %s\n' "$name" "$jail_note" >&2
fi

if [ "$mode" = serve ]; then
    cmd=(serve)
    has --store || cmd+=(--store "$root/.tillr/state.db")
    has --root || cmd+=(--root "$root")
    has --addr || cmd+=(--addr "$bind_host:$port")
    has --tillr-drain || cmd+=(--tillr-drain "$drain")
    printf '%s: starting %s for %s on http://127.0.0.1:%s\n' "$name" "$view" "$root" "$port" >&2
    exec "$view" "${cmd[@]}" "${args[@]+"${args[@]}"}"
fi

# dev: the processes are declared in Procfile.dev and run under hivemind, which is what makes
# this safe to interrupt: a child that dies takes the other with it, and SIGINT or SIGTERM
# tears down both process groups. Every value the Procfile reads comes from here.
export TILLR_VIEW_BIN="$view"
export TILLR_VIEW_DRAIN="$drain"
export TILLR_VIEW_STORE="$root/.tillr/state.db"
export TILLR_VIEW_ROOT="$root"
export TILLR_VIEW_ADDR="$api"
export TILLR_VIEW_PORT="$port"
export TILLR_VIEW_UI_HOST="$bind_host"
export TILLR_VIEW_WEB="$web"
export TILLR_VIEW_API_ARGS="${args[*]+"${args[*]}"}"
export TILLR_VIEW_API="http://$api"
printf '%s: open the UI at http://127.0.0.1:%s  (the API on %s is JSON only, never the UI)\n' "$name" "$port" "$api"
exec hivemind --root "$root" --timeout 10 "$view_src/Procfile.dev"
