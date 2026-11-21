# The preflight every adapter shares, sourced as
#
#     . "$(dirname -- "$0")/../preflight.sh"
#
# Reference: docs/reference/tillr-muse-backend.md (brief present and sectioned, the
# request's `backend` field naming this adapter, a model staging supplied). One copy,
# sourced by every adapter, is how that discipline stays uniform rather than drifting
# per harness. The refusal is exit 2 and nothing has run when it fires, so a refused
# dispatch spends no model tokens and says nothing about the item.
#
# $PROG is the adapter's own name, set by the adapter before it sources this file.

# The template's ten sections, by the text after `## `. The heading is matched as a
# prefix, so `## Settled decisions — do not redesign` satisfies `Settled decisions`.
REQUIRED_HEADINGS='Header
Task
Scope and boundaries
Seats
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
# so the refusal says the record is what must be fixed. Staging carries an item's own `{{`
# intact and states how many it carries in the lane's `brace-literals` file; only pairs
# beyond that count are refused, so this is the adapter's backstop for a brief that reached
# it some other way, and it must not send an operator to edit a template that is not at
# fault.
preflight_brief() {
    if [ ! -s "$1" ]; then
        refuse "the brief '$1' is missing or empty: staging writes it from backends/brief-template.md before launch"
    fi
    # Saved words, an idea's or a filed request's, are appended after the rendered
    # template. The heading states exactly how many bytes remain through EOF; even
    # a fake heading inside the
    # words cannot end this suffix early. Check sections only in the template.
    if ! template_text=$(LC_ALL=C awk -v total="$(wc -c < "$1")" '
        /^## Saved (idea|request) words \(verbatim; [0-9]+ bytes\)$/ {
            count=$0; sub(/^.*; /,"",count); sub(/ bytes\)$/,"",count)
            if (total - prefix - length($0) - 1 != count) exit 2
            exit
        }
        { prefix += length($0) + 1; print }
    ' "$1"); then
        refuse "the brief '$1' has a saved words byte count that does not match its suffix"
    fi
    checked_template=$(printf '%s\n' "$template_text" | awk '
        /^## (Answered owner questions|Retry reasons)$/ { in_recorded=1; print; next }
        /^## / { in_recorded=0 }
        in_recorded && /^>/ { next }
        { print }
    ')
    # Staging states in `brace-literals` how many `{{` pairs the brief carries on purpose,
    # because they are the item's own text and are carried intact. Without that file the
    # count is zero, and any pair is refused as before.
    allowed=0
    literals=$(lane_path "$1" brace-literals)
    if [ -r "$literals" ]; then
        allowed=$(tr -dc '0-9' < "$literals")
        allowed=${allowed:-0}
    fi
    found=$(printf '%s\n' "$checked_template" | grep -o '{{' | wc -l)
    if [ "$found" -gt "$allowed" ]; then
        if printf '%s\n' "$checked_template" | grep -q '{{[A-Z][A-Z0-9_]*}}'; then
            refuse "the brief '$1' still carries an unfilled template placeholder ('{{NAME}}'): the template backends/brief-template.md is the thing to fix, and an unfilled template is not a brief"
        fi
        refuse "the brief '$1' carries a literal '{{' that is not a template placeholder: it arrived in an item field (the title, the body reference, a criterion or an allowed path), so the item's record is the thing to fix and backends/brief-template.md is not"
    fi
    headings=$REQUIRED_HEADINGS
    contract="all ten of the template's sections"
    capability=$(lane_path "$1" ending-capability)
    if [ -r "$capability" ] && [ "$(cat "$capability")" = call-suspends ]; then
        contract="the common sections and Finishing"
        headings="${REQUIRED_HEADINGS%
Report shape
Stop conditions}
Finishing"
        if ! printf '%s\n' "$template_text" | grep -qx '## Finishing'; then
            refuse "the brief '$1' has no '## Finishing' section for its staged call-suspends ending"
        fi
    fi
    while IFS= read -r heading; do
        # Runs staged before the fence was retired may still resume with an old brief.
        if [ "$heading" = 'Scope and boundaries' ] && printf '%s\n' "$template_text" | grep -qx '## Fence'; then
            continue
        fi
        if ! printf '%s\n' "$template_text" | grep -q "^## $heading"; then
            refuse "the brief '$1' has no '## $heading' section: $contract are load-bearing, and one missing is a brief the driver must not dispatch"
        fi
    done <<EOF
$headings
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
