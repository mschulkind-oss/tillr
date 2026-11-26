#!/bin/sh
# Private pi harness resolution for the drain's pi adapter and compact step.
#
# Resolves the rebuilt pi privately (never placing anything on shared PATH):
#   1. Verified build for the repository's current pi series key, when present;
#   2. Else the previous verified current build in current.json, when present;
#   3. Else the installed pi on PATH.
#
# When sourced with PI_HARNESS_SOURCED=1, defines pi_apply_yolo_session_home,
# pi_resolve_harness, and pi_prepare_harness_env. When executed as
# `pi-harness.sh --exec -- <pi-args...>`, resolves and execs pi directly.
set -eu

pi_apply_yolo_session_home() {
    [ -z "${PI_CODING_AGENT_DIR:-}" ] || return 0
    [ -n "${YOLO_SESSION_HOMES:-}" ] || return 0
    _pi_tab=$(printf '\t')
    _pi_old_ifs=$IFS
    while IFS="$_pi_tab" read -r _pi_sh_bin _pi_sh_var _pi_sh_dir; do
        [ "$_pi_sh_bin" = pi ] || continue
        [ "$_pi_sh_var" = PI_CODING_AGENT_DIR ] || continue
        if [ -n "$_pi_sh_dir" ]; then
            PI_CODING_AGENT_DIR=$_pi_sh_dir
            export PI_CODING_AGENT_DIR
            break
        fi
    done <<EOF
$YOLO_SESSION_HOMES
EOF
    IFS=$_pi_old_ifs
    unset _pi_tab _pi_old_ifs _pi_sh_bin _pi_sh_var _pi_sh_dir
}

_pi_series_patch_body() {
    sed -e '1d' -e 's/^Subject: \[PATCH [0-9]*\/[0-9]*\]/Subject: [PATCH]/' "$1" | awk '
        { line[NR] = $0 }
        END {
            n = NR
            while (n > 0 && line[n] == "") n--
            if (n >= 2 && line[n - 1] == "-- ") n -= 2
            for (i = 1; i <= n; i++) print line[i]
        }'
}

_pi_series_digest_key() {
    _pi_dir=$1
    [ -d "$_pi_dir" ] || return 1
    _pi_digest=$(
        first=1
        count=0
        for name in $(LC_ALL=C ls -A1 -- "$_pi_dir" 2>/dev/null | LC_ALL=C sort); do
            case $name in *.patch) ;; *) continue ;; esac
            f=$_pi_dir/$name
            [ -f "$f" ] || return 1
            if ! grep -q '^diff --git ' "$f"; then
                case $first:$name in
                    1:*0000-cover-letter.patch) first=0; continue ;;
                    *) return 1 ;;
                esac
            fi
            first=0
            count=$((count + 1))
            printf '%s\n' "$name"
            _pi_series_patch_body "$f"
        done | git hash-object --stdin 2>/dev/null
    ) || return 1
    [ -n "$_pi_digest" ] || return 1
    printf '%.12s' "$_pi_digest"
}

_pi_series_root() {
    [ "${TILLR_NON_LANDING:-}" != "1" ] || return 1
    _pi_pwd=$(pwd -P 2>/dev/null || true)
    _pi_top=$(git rev-parse --path-format=absolute --show-toplevel 2>/dev/null || true)
    _pi_common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)
    [ -n "$_pi_top" ] && [ -n "$_pi_common" ] || return 1
    # Uninitialized scratch directories inside <repo>/.yolo/ inherit <repo> from git's
    # upward walk; they are not checkouts of <repo>.
    case $_pi_pwd in
        "$_pi_top/.yolo"/*) return 1 ;;
    esac
    _pi_root=$(dirname -- "$_pi_common")
    if [ -f "$_pi_root/bin/pi-forks.list" ]; then
        printf '%s' "$_pi_root"
        return 0
    fi
    case $_pi_top in
        */.forks/*)
            _pi_outer=${_pi_top%%/.forks/*}
            if [ -f "$_pi_outer/bin/pi-forks.list" ]; then
                printf '%s' "$_pi_outer"
                return 0
            fi
            ;;
    esac
    return 1
}

_pi_current_series_key() {
    _pi_root=$1
    _pi_loc=$(awk '!/^[[:space:]]*($|#)/ && $1 == "earendil-works/pi" { print $2; exit }' "$_pi_root/bin/pi-forks.list" 2>/dev/null || true)
    [ -n "$_pi_loc" ] || return 1
    case $_pi_loc in
        patch/*)
            _pi_tip=$(git -C "$_pi_root" rev-parse --verify --quiet "refs/heads/$_pi_loc^{commit}" 2>/dev/null || true)
            if [ -z "$_pi_tip" ] && [ -d "$_pi_root/.forks/pi" ]; then
                _pi_tip=$(git -C "$_pi_root/.forks/pi" rev-parse --verify --quiet "refs/heads/$_pi_loc^{commit}" 2>/dev/null || true)
            fi
            [ -n "$_pi_tip" ] || return 1
            printf '%.12s' "$_pi_tip"
            ;;
        *)
            _pi_series_digest_key "$_pi_root/$_pi_loc"
            ;;
    esac
}

_pi_read_current_json() {
    if command -v python3 >/dev/null 2>&1; then
        python3 - "$1" <<'PY' 2>/dev/null
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    d = json.load(f)
k, p, r = d.get("key", ""), d.get("pi", ""), d.get("dir", "")
if all(isinstance(x, str) and x and "\n" not in x and "\t" not in x for x in (k, p, r)):
    print(f"{k}\t{p}\t{r}")
PY
    elif command -v node >/dev/null 2>&1; then
        node -e '
const fs = require("node:fs");
const d = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
const [k, p, r] = [d.key, d.pi, d.dir];
if ([k, p, r].every(x => typeof x === "string" && x && !x.includes("\n") && !x.includes("\t"))) {
    process.stdout.write(`${k}\t${p}\t${r}\n`);
}
' "$1" 2>/dev/null
    else
        return 1
    fi
}

_pi_installed_version() {
    _pi_probe=$1
    if [ -f "$_pi_probe" ] && grep -q '^PROGRAM=\.local/bin/pi$' "$_pi_probe" 2>/dev/null && [ -e "${HOME:-}/.local/bin/pi" ]; then
        _pi_probe="$HOME/.local/bin/pi"
    fi
    _pi_real=$(readlink -f "$_pi_probe" 2>/dev/null || printf '%s' "$_pi_probe")
    _pi_walk=$(dirname -- "$_pi_real")
    for _pi_step in 1 2 3 4; do
        if [ -f "$_pi_walk/package.json" ] && grep -q 'pi-coding-agent' "$_pi_walk/package.json" 2>/dev/null; then
            _pi_ver=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$_pi_walk/package.json" | head -n 1)
            if [ -n "$_pi_ver" ]; then
                printf '%s' "$_pi_ver"
                return 0
            fi
        fi
        _pi_parent=$(dirname -- "$_pi_walk")
        [ "$_pi_parent" != "$_pi_walk" ] || break
        _pi_walk=$_pi_parent
    done
    # Only execute `--version` on compiled binaries or scripts that explicitly handle it,
    # never on minimal test stand-ins that ignore argv and block.
    _pi_head=$(dd if="$1" bs=2 count=1 2>/dev/null || true)
    if [ "$_pi_head" != "#!" ] || grep -Eq -- '["'"'"'( ]--version' "$1" 2>/dev/null; then
        _pi_ver=$(PI_OFFLINE=1 PI_TELEMETRY=0 "$1" --version </dev/null 2>/dev/null | head -n 1 | tr -d '\r' || true)
        if [ -n "$_pi_ver" ]; then
            printf '%s' "$_pi_ver"
            return 0
        fi
    fi
    return 0
}

pi_resolve_harness() {
    PI_HARNESS_BIN=
    PI_HARNESS_RECORD=
    PI_HARNESS_REBUILT=0

    _pi_root=$(_pi_series_root || true)
    _pi_builds=
    if [ -n "$_pi_root" ]; then
        if [ "${TILLR_PI_BUILDS_DIR+set}" = set ]; then
            _pi_builds=$TILLR_PI_BUILDS_DIR
        elif [ -n "${YOLO_DURABLE_DIR:-}" ]; then
            _pi_builds=$YOLO_DURABLE_DIR/pi-builds
        else
            _pi_builds=$_pi_root/.yolo/durable/pi-builds
        fi
    fi

    if [ -n "$_pi_builds" ] && [ -n "$_pi_root" ]; then
        _pi_key=$(_pi_current_series_key "$_pi_root" || true)
        if [ -n "$_pi_key" ]; then
            _pi_dir=$_pi_builds/$_pi_key
            _pi_bin=$_pi_dir/home/.local/bin/pi
            if [ -f "$_pi_dir/.verified" ] && [ -x "$_pi_bin" ]; then
                PI_HARNESS_BIN=$_pi_bin
                PI_HARNESS_RECORD="$_pi_bin (series $_pi_key)"
                PI_HARNESS_REBUILT=1
            fi
        fi
        if [ -z "$PI_HARNESS_BIN" ] && [ -r "$_pi_builds/current.json" ]; then
            _pi_cur=$(_pi_read_current_json "$_pi_builds/current.json" || true)
            if [ -n "$_pi_cur" ]; then
                _pi_tab=$(printf '\t')
                _pi_old_ifs=$IFS
                IFS=$_pi_tab
                set -- $_pi_cur
                IFS=$_pi_old_ifs
                if [ $# -eq 3 ] && [ -f "$3/.verified" ] && [ -x "$2" ]; then
                    PI_HARNESS_BIN=$2
                    PI_HARNESS_RECORD="$2 (series $1)"
                    PI_HARNESS_REBUILT=1
                fi
            fi
        fi
    fi

    if [ -z "$PI_HARNESS_BIN" ]; then
        _pi_installed=$(command -v pi 2>/dev/null || true)
        [ -n "$_pi_installed" ] || refuse "required harness 'pi' is not on PATH"
        case $_pi_installed in
            */*)
                _pi_canon=$(cd "$(dirname -- "$_pi_installed")" 2>/dev/null && pwd -P)/$(basename -- "$_pi_installed") || _pi_canon=$_pi_installed
                ;;
            *)
                _pi_canon=$_pi_installed
                ;;
        esac
        _pi_ver=$(_pi_installed_version "$_pi_installed" || true)
        PI_HARNESS_BIN=$_pi_installed
        if [ -n "$_pi_ver" ]; then
            PI_HARNESS_RECORD="$_pi_canon (installed $_pi_ver)"
        else
            PI_HARNESS_RECORD="$_pi_canon (installed)"
        fi
    fi
}

# The features of pi that tillr's adapter cannot run without. `parked-tool-calls`: the
# `land` and `obstacle` tools answer `park: true`, pi prints a `{"type":"parked"}` event
# and exits 0 without settling, and a promptless `--session` resume replays the call.
PI_REQUIRED_FEATURES="parked-tool-calls"

# pi_require_features refuses a run on a pi that lacks a required feature. It asks the
# resolved binary itself, `pi --features` (a JSON array of names), so no fork is assumed:
# any pi that lists the feature runs. `TILLR_PI_FEATURES`, a space-separated list, states
# the features instead and skips the probe, for a caller that has already established them.
pi_require_features() {
    if [ "${TILLR_PI_FEATURES+set}" = set ]; then
        _pi_have=$TILLR_PI_FEATURES
        _pi_source="TILLR_PI_FEATURES states"
    else
        _pi_errfile=$(mktemp "${TMPDIR:-/tmp}/pi-probe.XXXXXX")
        _pi_rc=0
        _pi_attempt=0
        while [ "$_pi_attempt" -lt 2 ]; do
            _pi_attempt=$((_pi_attempt + 1))
            _pi_rc=0
            : > "$_pi_errfile"
            _pi_probe=$(PI_OFFLINE=1 PI_TELEMETRY=0 \
                "$PI_HARNESS_BIN" --features </dev/null 2>"$_pi_errfile") || _pi_rc=$?
            [ "$_pi_rc" -eq 0 ] && break
        done
        _pi_err_line=$(head -n 1 "$_pi_errfile" 2>/dev/null | tr -d '\r' || true)
        rm -f "$_pi_errfile"
        if [ "$_pi_rc" -ne 0 ]; then
            if [ -n "$_pi_err_line" ]; then
                _pi_how="exited with status $_pi_rc: $_pi_err_line"
            else
                _pi_how="exited with status $_pi_rc (no stderr)"
            fi
            refuse "the \`pi --features\` probe for $PI_HARNESS_BIN failed ($_pi_how), so whether it has the feature '$PI_REQUIRED_FEATURES' is unknown."
        fi
        _pi_have=$(printf '%s' "$_pi_probe" | python3 -c '
import json, sys
try:
    names = json.loads(sys.stdin.read())
except ValueError:
    names = []
if isinstance(names, list):
    print(" ".join(name for name in names if isinstance(name, str)))
' 2>/dev/null || true)
        _pi_source="\`pi --features\` lists"
    fi
    for _pi_need in $PI_REQUIRED_FEATURES; do
        case " $_pi_have " in
            *" $_pi_need "*) ;;
            *)
                refuse "the pi at $PI_HARNESS_BIN lacks the feature '$_pi_need', which this adapter requires: $_pi_source '${_pi_have:-nothing}'. Upstream pi does not have it yet; the fork mschulkind-oss/tillr-pi does. Install a pi whose \`pi --features\` output names '$_pi_need'."
                ;;
        esac
    done
    unset _pi_have _pi_probe _pi_source _pi_need _pi_errfile _pi_rc _pi_attempt _pi_err_line _pi_how
}

pi_prepare_harness_env() {
    if [ "${PI_HARNESS_REBUILT:-0}" = 1 ]; then
        pi_apply_yolo_session_home
        if [ -n "${HOME:-}" ]; then
            _yolo_entry_dir="$HOME/.config/yolo-agent-env"
            case ${YOLO_SESSION_ID:-} in
                ''|*[!0-9a-f]*) ;;
                *)
                    if [ -d "$_yolo_entry_dir/sessions/$YOLO_SESSION_ID" ]; then
                        _yolo_entry_dir="$_yolo_entry_dir/sessions/$YOLO_SESSION_ID"
                    fi
                    ;;
            esac
            if [ -r "$_yolo_entry_dir/pi.sh" ]; then
                . "$_yolo_entry_dir/pi.sh"
            fi
            if [ -r "$_yolo_entry_dir/provider-keys/pi.sh" ]; then
                . "$_yolo_entry_dir/provider-keys/pi.sh"
            fi
            unset _yolo_entry_dir
            if [ -z "${NODE_COMPILE_CACHE:-}" ] && mkdir -p "$HOME/.local/state/yolo/compile-cache/node" 2>/dev/null; then
                NODE_COMPILE_CACHE="$HOME/.local/state/yolo/compile-cache/node"
                export NODE_COMPILE_CACHE
            fi
        fi
        PI_SUBAGENT_PI_BINARY=$PI_HARNESS_BIN
        export PI_SUBAGENT_PI_BINARY
    fi
    if [ -n "${TILLR_HARNESS_RECORD_FILE:-}" ]; then
        printf '%s\n' "$PI_HARNESS_RECORD" > "$TILLR_HARNESS_RECORD_FILE" 2>/dev/null || true
    fi
    unset TILLR_PI_BUILDS_DIR TILLR_HARNESS_RECORD_FILE
}

case ${PI_HARNESS_SOURCED:-0} in
    1) ;;
    *)
        PROG=${PROG:-pi-harness}
        _pi_self_dir=$(dirname -- "$0")
        . "$_pi_self_dir/../preflight.sh"
        if [ "${1:-}" = "--exec" ]; then
            shift
            [ "${1:-}" = "--" ] && shift
            pi_apply_yolo_session_home
            pi_resolve_harness
            pi_prepare_harness_env
            exec "$PI_HARNESS_BIN" "$@"
        fi
        refuse "expected --exec -- <args...>"
        ;;
esac
