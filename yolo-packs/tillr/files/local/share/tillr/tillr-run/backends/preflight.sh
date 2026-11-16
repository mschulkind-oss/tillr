# The preflight every adapter shares, sourced as
#
#     . "$(dirname -- "$0")/../preflight.sh"
#
# Design §6 gives the muse adapter this discipline (brief present and sectioned, the
# request's `backend` field naming this adapter, a model staging supplied) and §9 gives
# the Pi and Agy Flash proof adapters "the same preflight discipline". One copy, sourced
# three times, is how that stays true: three copies would be three disciplines within a
# month. The refusal is exit 2 and nothing has run when it fires, so a refused dispatch
# spends no model tokens and says nothing about the item.
#
# $PROG is the adapter's own name, set by the adapter before it sources this file.

# The template's nine sections, by the text after `## `. The heading is matched as a
# prefix, so `## Settled decisions — do not redesign` satisfies `Settled decisions`.
REQUIRED_HEADINGS='Header
Task
Fence
Settled decisions
Facts carried
Method
Verification commands
Report shape
Stop conditions'

refuse() {
    printf '%s: refused: %s\n' "$PROG" "$1" >&2
    exit 2
}

# require_opt <flag> <value> <why staging must supply it>
require_opt() {
    if [ -z "${2:-}" ]; then
        refuse "$1 is required: $3"
    fi
}

# require_backend <the backend value staging supplied> <this adapter's name>
#
# A backend name is this adapter's name or one of its seat aliases, `<adapter>-<role>`:
# `pi-designer` is the pi adapter on the design seat's model, so it is a name for the same
# program rather than a second adapter. The alias is a directory-layout fact — the adapter's
# directory is `backends/<adapter>` — so the check is a prefix and never a policy read: an
# adapter that read the policy would carry a default the policy did not supply, and the
# tillr tree ships without one. A name that is neither is still refused, because a request
# whose provenance disagrees with the program that ran is the one this check exists for.
require_backend() {
    if [ -z "${1:-}" ]; then
        refuse "no --backend: the request's backend field names the adapter, and a run whose provenance has to be guessed from argv is exactly what that field exists to prevent"
    fi
    if [ "$1" != "$2" ] && [ "${1#"$2"-}" = "$1" ]; then
        refuse "the backend field names '$1' but this adapter is '$2' (or a seat alias '$2-<role>'): staging and the adapter disagree, so nothing has run"
    fi
}

# check_wall <the --timeout value, possibly empty>
#
# The wall is optional: an empty value and `0` both mean the run is bounded by nothing but
# the operator's own stop, and the lane lease carries no expiry (design D-RW1). Anything
# else must be a whole number of seconds, refused here rather than left to `timeout` failing
# to parse it after the harness has already been resolved.
check_wall() {
    case ${1:-} in
        '') ;;
        *[!0-9]*) refuse "--timeout must be a whole number of seconds, got '$1'" ;;
    esac
}

# preflight_brief <brief path>
#
# A brace pair is refused, and the two ways one can arrive are told apart because they are
# different things to fix. A well-formed `{{NAME}}` is a template placeholder staging never
# filled: the template (backends/brief-template.md) is what must be filled, and the refusal
# names it. A literal `{{` that is not a placeholder came in through an item's own field —
# the title, the body reference, a criterion or an allowed path — and the template is fine,
# so the refusal says the record is what must be fixed. Staging refuses that case before a
# brief is written, with the field named (drain's renderBrief); this is the adapter's own
# backstop for a brief that reached it some other way, and it must not send an operator to
# edit a template that is not at fault.
preflight_brief() {
    if [ ! -s "$1" ]; then
        refuse "the brief '$1' is missing or empty: staging writes it from backends/brief-template.md before launch"
    fi
    if grep -q '{{[A-Z][A-Z0-9_]*}}' "$1"; then
        refuse "the brief '$1' still carries an unfilled template placeholder ('{{NAME}}'): the template backends/brief-template.md is the thing to fix, and an unfilled template is not a brief"
    fi
    if grep -q '{{' "$1"; then
        refuse "the brief '$1' carries a literal '{{' that is not a template placeholder: it arrived in an item field (the title, the body reference, a criterion or an allowed path), so the item's record is the thing to fix and backends/brief-template.md is not"
    fi
    while IFS= read -r heading; do
        if ! grep -q "^## $heading" "$1"; then
            refuse "the brief '$1' has no '## $heading' section: all nine of the template's sections are load-bearing, and one missing is a brief the driver must not dispatch"
        fi
    done <<EOF
$REQUIRED_HEADINGS
EOF
}

# require_harness <command name> -- the harness must be on PATH, which is a preflight
# answer rather than a failed-to-start one: an absent binary is an environment defect, not
# an attempt outcome, and nothing about the item has been learned when it fires.
require_harness() {
    if ! command -v "$1" >/dev/null 2>&1; then
        refuse "the harness '$1' is not on PATH: fix the environment, do not re-dispatch the item"
    fi
}

# resolve_target <the --target value, possibly empty> -- answers the real path of the
# target, which must be the directory this adapter is running in.
#
# The check is not ceremony: the target is the harness's write root, so a target that is
# not the directory the run sits in would root the sandbox somewhere the fence does not
# describe. The lane worktree is the default (the adapter's cwd); the main-checkout form
# runs the adapter in the main checkout, so the same check holds there.
resolve_target() {
    here=$(pwd -P)
    if [ -z "${1:-}" ]; then
        printf '%s' "$here"
        return 0
    fi
    if ! there=$(cd "$1" 2>/dev/null && pwd -P); then
        refuse "the target '$1' does not exist"
    fi
    if [ "$there" != "$here" ]; then
        refuse "the target '$1' is '$there' but this adapter runs in '$here': the target is the harness's write root, so it must be the directory the run is in"
    fi
    printf '%s' "$there"
}

# lane_path <brief path> <file name> -- a sibling of the brief, inside the lane directory.
lane_path() {
    printf '%s/%s' "$(dirname -- "$1")" "$2"
}
