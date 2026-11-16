#!/usr/bin/env bash
#
# bash-shim.sh — the fail-open `proc.exec` recorder for a run's child shells.
#
# `run-pi` names this file in `BASH_ENV`, so every non-interactive `bash` a run spawns
# sources it before the command it was asked to run, and each shell command appends one
# line to `$TILLR_TRACE_DIR/proc-exec.jsonl` — the schema
# `tools/tillr-trace/tillr_trace.py` parses (session-recording §4.3 `proc.exec`).
#
# Fail-open, by the four guarantees of session-recording §7.1:
#   1. No daemon, no pipe, no lock: one builtin `printf` appends to a regular file
#      opened O_APPEND, and the command text is capped at `__tillr_proc_cap` bytes, so
#      the line stays under the 4 KiB atomic-append size. Every write is wrapped in
#      `{ ...; } 2>/dev/null || true`: a full, read-only or vanished directory costs one
#      failed `printf` and nothing else.
#   2. Exit-code preservation: `$?` is the trap's first instruction and is what the EXIT
#      trap returns, so nothing here can change the shell's status. Neither trap writes
#      to stdout or to the command's stderr: each one runs as `{ ...; } 2>/dev/null`, so
#      a `bash -x` shell does not gain a line per shim statement and the command's own
#      stderr is untouched. Two limits are stated rather than implied: the statements
#      that run while the shim itself is sourced are traced once per shell (nothing can
#      redirect them from inside), and a shell that points `BASH_XTRACEFD` at a
#      descriptor of its own sends its trace past the redirect.
#   3. Zero lock contention on `.tillr/state.db`: nothing here opens it.
#   4. Bounded memory: only the command text is kept, capped, and no command output is
#      captured, hashed or buffered.
# The §7.2 overhead budget (≤ 1.5 ms per command) is held by using shell builtins only:
# no `date`, no `jq`, and no fork per command.
#
# A shell with no `TILLR_TRACE_DIR` is the "emitter absent" case, not an error: the shim
# installs no trap and the shell behaves exactly as it would without it.
#
# What this record cannot prove: it attests that a shell command ran, not who caused it.
# A run's own shell can append a well-formed `proc.exec` line to the same file, and
# `tillr-trace verify-claims` will read it, so a `claim.verdict` is evidence about an
# honest lane and never evidence about a hostile one. No nonce would help — the run's
# environment is where a nonce would have to live.

if [ -z "${TILLR_TRACE_DIR:-}" ]; then
    # `return` when sourced (the normal case), `exit` when someone runs this file.
    return 0 2>/dev/null || exit 0
fi

__tillr_proc_cap=512
__tillr_proc_pending=0
__tillr_proc_seq=0
# Set once a shell `exit` has been armed; see `__tillr_proc_begin`.
__tillr_proc_armed_exit=0

# __tillr_proc_iso <EPOCHREALTIME> — sets __tillr_proc_ts to an ISO-8601 UTC stamp with
# milliseconds, from the builtin clock and integer arithmetic alone. Empty when the shell
# has no EPOCHREALTIME (bash < 5): a missing clock drops the time, never the row.
#
# Arithmetic rather than `printf '%(...)T'`, for two measured reasons: that conversion
# formats in the shell's LOCAL zone, so a ran-on-a-`TZ=America/New_York`-host stamp read
# 01:01:19Z for a command `date -u` printed 06:01:19Z and the §8.3 edit/test join ordered
# a later suite before the edit that preceded it; and the obvious repair — `TZ=UTC0
# printf ...` — costs bash a `tzset` save/restore per call, measured at +0.4 ms per
# command on a 400-command loop, a third of the whole §7.2 overhead budget. The civil-date
# decomposition is Howard Hinnant's `civil_from_days`, valid for epoch seconds ≥ 0.
__tillr_proc_iso() {
    local t=${1:-} whole frac days secs z era doe yoe y doy mp d mon
    __tillr_proc_ts=
    if [ -z "$t" ]; then
        return 0
    fi
    t=${t/[.,]/.}
    whole=${t%%.*}
    frac=${t#*.}
    if [ "$whole" = "$t" ]; then
        return 0
    fi
    case $whole in
        ''|*[!0-9]*) return 0 ;;
    esac
    days=$(( whole / 86400 ))
    secs=$(( whole - days * 86400 ))
    z=$(( days + 719468 ))
    era=$(( (z >= 0 ? z : z - 146096) / 146097 ))
    doe=$(( z - era * 146097 ))
    yoe=$(( (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365 ))
    y=$(( yoe + era * 400 ))
    doy=$(( doe - (365 * yoe + yoe / 4 - yoe / 100) ))
    mp=$(( (5 * doy + 2) / 153 ))
    d=$(( doy - (153 * mp + 2) / 5 + 1 ))
    mon=$(( mp < 10 ? mp + 3 : mp - 9 ))
    if [ "$mon" -le 2 ]; then
        y=$(( y + 1 ))
    fi
    printf -v __tillr_proc_ts '%04d-%02d-%02dT%02d:%02d:%02d' \
        "$y" "$mon" "$d" $(( secs / 3600 )) $(( secs % 3600 / 60 )) $(( secs % 60 ))
    __tillr_proc_ts="${__tillr_proc_ts}.${frac:0:3}Z"
    return 0
}

# __tillr_proc_elapsed <start> <end> — sets __tillr_proc_ms to whole milliseconds, or
# empty when either stamp is unusable. Both stamps are EPOCHREALTIME strings, whose
# digits are microseconds, so the difference divided by 1000 is the duration.
__tillr_proc_elapsed() {
    local s=${1:-} e=${2:-}
    __tillr_proc_ms=
    s=${s/[.,]/}
    e=${e/[.,]/}
    case $s$e in
        *[!0-9]*|'') return 0 ;;
    esac
    if [ "${#s}" -ne "${#e}" ]; then
        return 0
    fi
    __tillr_proc_ms=$(( (e - s) / 1000 ))
    return 0
}

# __tillr_proc_flush <exit status> — writes the pending command's line, if any.
#
# The guard `__tillr_proc_pending` is cleared first: this function runs inside a trap,
# and a line must never describe the write that writes it.
__tillr_proc_flush() {
    local rc=${1:-0}
    if [ "${__tillr_proc_pending:-0}" != 1 ]; then
        return 0
    fi
    __tillr_proc_pending=0
    local dir=${TILLR_TRACE_DIR:-}
    local cmd=${__tillr_proc_cmd:-}
    local cwd=${PWD:-}
    local start=${__tillr_proc_start:-}
    local end=${EPOCHREALTIME:-}
    local rel=${cwd}
    local rel_json=null
    local ts= ts_end= ms= span=

    __tillr_proc_iso "$start"
    ts=$__tillr_proc_ts
    __tillr_proc_iso "$end"
    ts_end=$__tillr_proc_ts
    __tillr_proc_elapsed "$start" "$end"
    ms=$__tillr_proc_ms

    __tillr_proc_seq=$(( ${__tillr_proc_seq:-0} + 1 ))
    span="sh-${BASHPID:-$$}-${__tillr_proc_seq}"

    # JSON string escaping with parameter expansion only: no `jq`, no subshell per field.
    cmd=${cmd//\\/\\\\}
    cmd=${cmd//\"/\\\"}
    cmd=${cmd//$'\n'/\\n}
    cmd=${cmd//$'\t'/\\t}
    cmd=${cmd//$'\r'/\\r}
    cmd=${cmd//[$'\001'-$'\037']/}
    cmd=${cmd:0:$__tillr_proc_cap}
    cwd=${cwd//\\/\\\\}
    cwd=${cwd//\"/\\\"}

    # A repo-relative cwd is what the §8.3 owed-suite join reads: the cwd with the
    # repository root stripped when the run named one, and `null` otherwise — the indexer
    # then falls back to finding the module inside the absolute path.
    if [ -n "${TILLR_REPO_ROOT:-}" ]; then
        rel=${cwd#"${TILLR_REPO_ROOT%/}"/}
        if [ "$rel" != "$cwd" ]; then
            rel=${rel//\\/\\\\}
            rel=${rel//\"/\\\"}
            rel_json="\"$rel\""
        fi
    fi

    if [ -n "$dir" ] && [ -d "$dir" ] && [ -w "$dir" ]; then
        {
            printf '{"kind":"proc.exec","seam":"bash-shim","ts":"%s","ts_end":"%s","duration_ms":%s,"run_id":"%s","lane_id":"%s","pid":%d,"ppid":%d,"cwd":"%s","repo_rel_cwd":%s,"exit_code":%d,"span_id":"%s","cmd":"%s"}\n' \
                "$ts" "$ts_end" "${ms:-null}" "${TILLR_RUN_ID:-}" "${TILLR_LANE_ID:-}" \
                "${BASHPID:-$$}" "$PPID" "$cwd" "$rel_json" "$rc" "$span" "$cmd" \
                >>"$dir/proc-exec.jsonl"
        } 2>/dev/null || true
    fi
    return 0
}

# The DEBUG trap fires before every simple command of the shell, which is the only
# per-command hook a non-interactive bash offers. It records the command that just
# finished (its status is the `$?` on entry) and remembers the one about to start, and
# the EXIT trap writes the last one. Returning 0 matters: a DEBUG trap's status is the
# shell's, and a non-zero one can skip the command it fired for.
__tillr_proc_begin() {
    local rc=$?
    __tillr_proc_flush "$rc" || true
    # bash fires the DEBUG trap once for the `trap` command that installed it, inside the
    # sourced file. That command is the shim's own plumbing, so it is dropped rather than
    # recorded as work the run did.
    case ${BASH_COMMAND:-} in
        *__tillr_proc_begin*) __tillr_proc_pending=0; return 0 ;;
    esac
    # An `exit` reaches the shell's own teardown, which fires this trap one more time
    # with `BASH_COMMAND` still naming the `exit`. That fire is not a command the run
    # made, so it arms nothing: the row for the `exit` was already written by the fire
    # that preceded it, and arming again wrote the same row twice. The `exit` spelling is
    # the only one a fire can be recognised by: a shell that aborts under `set -e` (or on
    # a fatal error) reaches teardown the same way, with the failed command's text in
    # `BASH_COMMAND`, and no signal in the trap separates that fire from a repeated
    # command — so such a shell can still write its final row twice. A duplicate row
    # over-states one command; suppressing it by text alone could drop a real one.
    if [ "${__tillr_proc_armed_exit:-0}" = 1 ]; then
        __tillr_proc_pending=0
        return 0
    fi
    __tillr_proc_cmd=${BASH_COMMAND:-}
    __tillr_proc_start=${EPOCHREALTIME:-}
    __tillr_proc_pending=1
    case ${__tillr_proc_cmd:-} in
        exit|exit\ *|builtin\ exit|builtin\ exit\ *)
            __tillr_proc_armed_exit=1 ;;
    esac
    return 0
}

# The EXIT trap writes the final command and returns the shell's own status unchanged.
__tillr_proc_exit() {
    local rc=$?
    __tillr_proc_flush "$rc" || true
    return "$rc"
}

# The EXIT trap goes in first, and the DEBUG trap is installed as the file's last
# statement with no `|| true` after it: a list would leave one more command to fire for,
# and that command is the shim's own plumbing, not the shell's work. Both bodies run
# inside `{ ...; } 2>/dev/null` so that even the trap invocation's own `-x` line lands in
# the redirect — a run's shell keeps its stderr byte for byte.
trap '{ __tillr_proc_exit; } 2>/dev/null' EXIT
trap '{ __tillr_proc_begin; } 2>/dev/null' DEBUG
