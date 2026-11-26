# Sourced by run-pi after repo_root is resolved.
# Selects a content-keyed trunk-built Pi agent directory for drain workers in a
# matt-pack repository, records the selected extension set, and unsets internal
# selection variables before launching pi.

PI_AGENT_DIR_BUILT=0
ext_record_line=

_pi_use_deployed=0
if [ "${TILLR_NON_LANDING:-}" = "1" ]; then
    _pi_use_deployed=1
elif [ "${TILLR_PI_AGENT_DIRS_DIR+set}" = set ] && [ -z "$TILLR_PI_AGENT_DIRS_DIR" ]; then
    _pi_use_deployed=1
fi

# A plain scratch directory under <repo>/.yolo/ or <repo>/.tillr/ has no .git of its
# own; git rev-parse walks up into the enclosing repo, which does not make the scratch
# directory a checkout of that repo (a real lane worktree has its own .git file, so its
# --show-toplevel is the worktree itself). Likewise, when TILLR_PI_AGENT_DIRS_DIR is
# unset and the caller explicitly passed a custom PI_CODING_AGENT_DIR outside the
# deployed/session/stopgap locations, keep the caller's directory.
_git_toplevel=
if [ "$_pi_use_deployed" -eq 0 ]; then
    _git_toplevel=$(git rev-parse --path-format=absolute --show-toplevel 2>/dev/null || true)
    case ${target:-} in
        "$_git_toplevel"/.yolo/*|"$_git_toplevel"/.tillr/*)
            _pi_use_deployed=1
            ;;
    esac
fi
if [ "$_pi_use_deployed" -eq 0 ] && [ "${TILLR_PI_AGENT_DIRS_DIR+set}" != set ] && [ -n "${PI_CODING_AGENT_DIR:-}" ]; then
    case $PI_CODING_AGENT_DIR in
        "$HOME/.pi/agent"|"$HOME"/.pi/.yolo-sessions/*/agent|*/.yolo/durable/pi-agent-overlay)
            ;;
        *)
            _pi_use_deployed=1
            ;;
    esac
fi

if [ "$_pi_use_deployed" -eq 0 ] && [ -n "${repo_root:-}" ] &&
    [ -f "$repo_root/yolo-packs/matt/pack.json" ] &&
    [ -d "$repo_root/yolo-packs/matt/files/extensions" ] &&
    [ -f "$repo_root/bin/pi-forks.list" ]; then

    if [ -n "${TILLR_PI_AGENT_DIRS_DIR:-}" ]; then
        _agent_dirs_root=$TILLR_PI_AGENT_DIRS_DIR
    elif [ -n "${YOLO_DURABLE_DIR:-}" ]; then
        _agent_dirs_root=$YOLO_DURABLE_DIR/pi-agent-dirs
    else
        _agent_dirs_root=$repo_root/.yolo/durable/pi-agent-dirs
    fi

    _key=$("${TILLR_RUN_BIN:-tillr-run}" pi-build-agent-dir --key 2>/dev/null || true)
    if [ -n "$_key" ]; then
        _verified=
        if [ -r "$_agent_dirs_root/$_key/.verified" ]; then
            _verified=$(tr -d ' \r\n' < "$_agent_dirs_root/$_key/.verified")
        fi
        if [ "$_verified" = "$_key" ] && [ -d "$_agent_dirs_root/$_key/agent" ]; then
            PI_CODING_AGENT_DIR=$_agent_dirs_root/$_key/agent
            export PI_CODING_AGENT_DIR
            PI_AGENT_DIR_BUILT=1
            ext_record_line="$PI_CODING_AGENT_DIR (set $_key)"
        else
            _already_held=0
            if [ -r "$_agent_dirs_root/status.json" ] && command -v node >/dev/null 2>&1; then
                if node -e '
const fs = require("node:fs");
try {
    const data = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    if (data && data.state === "held" && data.target_key === process.argv[2]) {
        process.exit(0);
    }
} catch {}
process.exit(1);
' "$_agent_dirs_root/status.json" "$_key"
                then
                    _already_held=1
                fi
            fi

            _built_dir=
            if [ "$_already_held" -eq 0 ]; then
                _built_dir=$(PI_HARNESS_BIN="${PI_HARNESS_BIN:-}" "${TILLR_RUN_BIN:-tillr-run}" pi-build-agent-dir 2>/dev/null || true)
            fi

            if [ -n "$_built_dir" ] && [ -d "$_built_dir" ]; then
                PI_CODING_AGENT_DIR=$_built_dir
                export PI_CODING_AGENT_DIR
                PI_AGENT_DIR_BUILT=1
                ext_record_line="$PI_CODING_AGENT_DIR (set $_key)"
            else
                _prev_out=
                if [ -r "$_agent_dirs_root/current.json" ] && command -v node >/dev/null 2>&1; then
                    _prev_out=$(node -e '
const fs = require("node:fs");
const path = require("node:path");
try {
    const data = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const key = data.key || "", agentDir = data.agent_dir || "", keyDir = data.dir || "";
    const vp = path.join(keyDir, ".verified");
    if (key && agentDir && keyDir && fs.statSync(agentDir).isDirectory() && fs.readFileSync(vp, "utf8").trim() === key) {
        process.stdout.write(key + "\n" + agentDir + "\n");
        process.exit(0);
    }
} catch {}
process.exit(1);
' "$_agent_dirs_root/current.json" || true)
                fi
                if [ -n "$_prev_out" ]; then
                    _prev_key=$(printf '%s\n' "$_prev_out" | sed -n '1p')
                    _prev_dir=$(printf '%s\n' "$_prev_out" | sed -n '2p')
                    PI_CODING_AGENT_DIR=$_prev_dir
                    export PI_CODING_AGENT_DIR
                    PI_AGENT_DIR_BUILT=1
                    ext_record_line="$_prev_dir (set $_prev_key; held pi-extensions-build-failed for $_key)"
                else
                    _deployed=${TILLR_DEPLOYED_PI_AGENT_DIR:-${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}}
                    PI_CODING_AGENT_DIR=$_deployed
                    export PI_CODING_AGENT_DIR
                    PI_AGENT_DIR_BUILT=0
                    ext_record_line="$_deployed (deployed; held pi-extensions-build-failed for $_key)"
                fi
                printf 'run-pi: pi-extensions-build-failed for %s; mover: land a fix to yolo-packs/matt or patch/<fork> on trunk, or rerun tillr-run pi-build-agent-dir\n' "$_key" >&2
            fi
        fi
    fi
fi

if [ -z "$ext_record_line" ]; then
    _deployed=${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}
    PI_AGENT_DIR_BUILT=0
    ext_record_line="$_deployed (deployed)"
fi

if [ -n "${TILLR_EXTENSION_SET_RECORD_FILE:-}" ]; then
    printf '%s\n' "$ext_record_line" > "$TILLR_EXTENSION_SET_RECORD_FILE"
fi
unset TILLR_PI_AGENT_DIRS_DIR TILLR_EXTENSION_SET_RECORD_FILE
unset _pi_use_deployed _git_toplevel _agent_dirs_root _key _verified _already_held _built_dir _prev_out _prev_key _prev_dir _deployed ext_record_line
