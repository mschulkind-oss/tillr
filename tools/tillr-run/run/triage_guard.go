package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	triageSeatName    = "triage"
	triageBinDirName  = "triage-bin"
	triageBashEnvName = "triage-bash-env.sh"
)

var triageAlwaysWrappedTillr = []string{
	"tillr",
	"tillr-item",
	"tillr-watch",
	"tillr-drain",
	"tillr-run",
	"tillr-store",
	"tillr-trace",
	"tillr-view",
	"tillr-land",
	"tillr-gate",
	"tillr-keep",
}

var triageForbiddenBins = []string{
	"just",
	"sqlite3",
	"kill",
	"pkill",
	"killall",
	"systemctl",
	"service",
}

var triageFileMutatingBins = []string{
	"rm",
	"mv",
	"cp",
	"chmod",
	"chown",
	"ln",
	"touch",
	"truncate",
	"tee",
}

func installTriageGuard(durableDir string, env []string) ([]string, error) {
	for _, key := range gitRepoEnvKeys {
		env = withoutEnv(env, key)
	}
	absDurable, err := filepath.Abs(durableDir)
	if err != nil {
		return nil, Internalf("resolving triage durable dir %q: %v", durableDir, err)
	}
	if err := os.MkdirAll(absDurable, 0o755); err != nil {
		return nil, Internalf("creating triage durable dir %q: %v", absDurable, err)
	}
	binDir := filepath.Join(absDurable, triageBinDirName)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, Internalf("creating triage bin dir %q: %v", binDir, err)
	}

	bashEnvPath := filepath.Join(absDurable, triageBashEnvName)
	bashEnvScript := `#!/usr/bin/env bash
# tillr-triage-bash-env: overrides bash builtins forbidden to the triage seat.
kill() {
    printf 'triage boundary: kill builtin is forbidden on the triage seat\n' >&2
    return 2
}
`
	if err := os.WriteFile(bashEnvPath, []byte(bashEnvScript), 0o644); err != nil {
		return nil, Internalf("writing triage bash env %q: %v", bashEnvPath, err)
	}

	pathVal := lookupEnvValue(env, "PATH")

	// Wrap tillr and every tillr-* binary.
	tillrNames := map[string]bool{}
	for _, name := range triageAlwaysWrappedTillr {
		tillrNames[name] = true
	}
	for _, dir := range filepath.SplitList(pathVal) {
		if dir == "" || filepath.Clean(dir) == filepath.Clean(binDir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == "tillr" || strings.HasPrefix(name, "tillr-") {
				tillrNames[name] = true
			}
		}
	}
	for name := range tillrNames {
		realBin := findRealBinary(pathVal, name, binDir)
		script := renderTriageTillrWrapper(name, realBin)
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
			return nil, Internalf("writing triage wrapper for %q: %v", name, err)
		}
	}

	// Wrap git for read-only inspection only.
	realGit := findRealBinary(pathVal, "git", binDir)
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(renderTriageGitWrapper(realGit)), 0o755); err != nil {
		return nil, Internalf("writing triage git wrapper: %v", err)
	}

	// Wrap forbidden binaries.
	for _, name := range triageForbiddenBins {
		script := fmt.Sprintf("#!/bin/sh\nprintf 'triage boundary: %s is forbidden on the triage seat\\n' >&2\nexit 2\n", name)
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
			return nil, Internalf("writing triage forbidden wrapper for %q: %v", name, err)
		}
	}

	// Wrap file-mutating utilities so targets outside durableDir / TMPDIR are refused.
	for _, name := range triageFileMutatingBins {
		realBin := findRealBinary(pathVal, name, binDir)
		script := renderTriageFileMutatorWrapper(name, realBin, absDurable)
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
			return nil, Internalf("writing triage file wrapper for %q: %v", name, err)
		}
	}

	env = withoutEnv(env, "GIT_OPTIONAL_LOCKS")
	env = append(env, "GIT_OPTIONAL_LOCKS=0")
	env = withoutEnv(env, "TILLR_PI_AGENT_DIRS_DIR")
	env = append(env, "TILLR_PI_AGENT_DIRS_DIR=")
	env = withoutEnv(env, "BASH_ENV")
	env = append(env, "BASH_ENV="+bashEnvPath)
	env = withoutEnv(env, "TILLR_SEAT_BASH_ENV")
	env = append(env, "TILLR_SEAT_BASH_ENV="+bashEnvPath)

	env = withoutEnv(env, "PATH")
	if pathVal == "" {
		env = append(env, "PATH="+binDir)
	} else if !hasPathPrefix(pathVal, binDir) {
		env = append(env, "PATH="+binDir+string(os.PathListSeparator)+pathVal)
	} else {
		env = append(env, "PATH="+pathVal)
	}
	return env, nil
}

func triageRepoRoot(laneRoot string, env []string) string {
	if v := lookupEnvValue(env, "TILLR_REPO_ROOT"); v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			return abs
		}
		return v
	}
	if filepath.Base(laneRoot) == "lanes" && filepath.Base(filepath.Dir(laneRoot)) == ".tillr" {
		return filepath.Dir(filepath.Dir(laneRoot))
	}
	return filepath.Dir(laneRoot)
}

func findRealBinary(pathVal, name, excludeDir string) string {
	excludeClean := filepath.Clean(excludeDir)
	for _, dir := range filepath.SplitList(pathVal) {
		if dir == "" || filepath.Clean(dir) == excludeClean {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		return filepath.Clean(candidate)
	}
	return ""
}

func prepareTriageSymlinkView(root, workDir string) error {
	if _, err := os.Stat(workDir); err == nil {
		if err := os.Chmod(workDir, 0o755); err != nil {
			return Internalf("chmod triage work dir %q: %v", workDir, err)
		}
		existing, err := os.ReadDir(workDir)
		if err != nil {
			return Internalf("reading triage work dir %q: %v", workDir, err)
		}
		for _, entry := range existing {
			if err := os.RemoveAll(filepath.Join(workDir, entry.Name())); err != nil {
				return Internalf("clearing triage work entry %q: %v", entry.Name(), err)
			}
		}
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		return Internalf("creating triage work dir %q: %v", workDir, err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return Internalf("reading repo root %q: %v", root, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".tillr" || name == ".yolo" {
			continue
		}
		if err := os.Symlink(filepath.Join(root, name), filepath.Join(workDir, name)); err != nil {
			return Internalf("symlinking %q into triage work dir: %v", name, err)
		}
	}
	if err := os.Chmod(workDir, 0o555); err != nil {
		return Internalf("chmod 0555 triage work dir %q: %v", workDir, err)
	}
	return nil
}

func renderTriageTillrWrapper(binName, realBin string) string {
	fixedGroup := ""
	if strings.HasPrefix(binName, "tillr-") {
		fixedGroup = strings.TrimPrefix(binName, "tillr-")
	}
	return fmt.Sprintf(`#!/bin/sh
# tillr-triage-guard (%s)
BIN_NAME=%s
REAL_BIN=%s
FIXED_GROUP=%s

refuse() {
    printf 'triage boundary: %%s\n' "$1" >&2
    exit 2
}

is_help_flag() {
    case "$1" in
        -h|--help|-v|--version|help|version) return 0 ;;
        *) return 1 ;;
    esac
}

run_real() {
    if [ -z "$REAL_BIN" ]; then
        refuse "$BIN_NAME binary not found in PATH"
    fi
    exec "$REAL_BIN" "$@"
}

# Skip leading global flags to find the first positional token.
skip_global_flags() {
    _expect_val=0
    _idx=0
    for _arg in "$@"; do
        _idx=$((_idx + 1))
        if [ "$_expect_val" = 1 ]; then
            _expect_val=0
            continue
        fi
        case "$_arg" in
            --)
                _idx=$((_idx + 1))
                break
                ;;
            -h|--help|--version)
                echo "HELP_OR_VERSION"
                return 0
                ;;
            --store|--root|--format|--config|--policy|-C)
                _expect_val=1
                ;;
            -*)
                ;;
            *)
                echo "$_idx"
                return 0
                ;;
        esac
    done
    echo "NONE"
}

check_item_request() {
    _filed_by=""
    _alert=""
    _dedup=""
    _expect=""
    for _arg in "$@"; do
        if [ -n "$_expect" ]; then
            case "$_expect" in
                filed-by) _filed_by="$_arg" ;;
                alert) _alert="$_arg" ;;
                dedup-key) _dedup="$_arg" ;;
            esac
            _expect=""
            continue
        fi
        case "$_arg" in
            -h|--help) return 0 ;;
            --filed-by) _expect="filed-by" ;;
            --filed-by=*) _filed_by="${_arg#--filed-by=}" ;;
            --alert) _expect="alert" ;;
            --alert=*) _alert="${_arg#--alert=}" ;;
            --dedup-key) _expect="dedup-key" ;;
            --dedup-key=*) _dedup="${_arg#--dedup-key=}" ;;
        esac
    done
    if [ "$_filed_by" != "tillr-triage" ] || [ -z "$_alert" ] || [ -z "$_dedup" ]; then
        refuse "tillr item request on triage seat requires --filed-by tillr-triage, --alert, and --dedup-key"
    fi
    return 0
}

check_item_dep() {
    _has_add=0
    for _arg in "$@"; do
        case "$_arg" in
            -h|--help) return 0 ;;
            --remove|--remove=*) refuse "tillr item dep --remove is forbidden on the triage seat" ;;
            --add) _has_add=1 ;;
        esac
    done
    if [ "$_has_add" != 1 ]; then
        refuse "tillr item dep on triage seat requires --add"
    fi
    return 0
}

first_pos_after() {
    _skip="$1"
    shift
    _i=0
    _expect_val=0
    for _a in "$@"; do
        _i=$((_i + 1))
        if [ "$_i" -le "$_skip" ]; then
            continue
        fi
        if [ "$_expect_val" = 1 ]; then
            _expect_val=0
            continue
        fi
        case "$_a" in
            -h|--help|--version) echo "$_a"; return 0 ;;
            --store|--root|--format|--config|--policy|-C) _expect_val=1 ;;
            -*) ;;
            *) echo "$_a"; return 0 ;;
        esac
    done
    echo ""
}

pos="$(skip_global_flags "$@")"
if [ "$pos" = "HELP_OR_VERSION" ]; then
    run_real "$@"
fi

if [ -n "$FIXED_GROUP" ]; then
    GROUP="$FIXED_GROUP"
    VERB_OFFSET=0
else
    if [ "$pos" = "NONE" ]; then
        run_real "$@"
    fi
    _i=0
    GROUP=""
    for _a in "$@"; do
        _i=$((_i + 1))
        if [ "$_i" = "$pos" ]; then
            GROUP="$_a"
            break
        fi
    done
    VERB_OFFSET="$pos"
fi

if is_help_flag "$GROUP"; then
    run_real "$@"
fi

VERB="$(first_pos_after "$VERB_OFFSET" "$@")"

case "$GROUP" in
    view)
        if [ "$VERB" = "serve" ]; then
            refuse "tillr view serve is forbidden on the triage seat"
        fi
        run_real "$@"
        ;;
    item)
        case "$VERB" in
            list|show|-h|--help|--version) run_real "$@" ;;
            request)
                check_item_request "$@"
                run_real "$@"
                ;;
            dep)
                check_item_dep "$@"
                run_real "$@"
                ;;
            *)
                refuse "tillr item $VERB is forbidden on the triage seat"
                ;;
        esac
        ;;
    run)
        case "$VERB" in
            status|lanes|pi-build-agent-dir|pi-stop-at-gap|pi-compact|extract-report|-h|--help|--version)
                run_real "$@"
                ;;
            *)
                refuse "tillr run $VERB is forbidden on the triage seat"
                ;;
        esac
        ;;
    drain)
        case "$VERB" in
            why|workers|policy|workflow|-h|--help|--version) run_real "$@" ;;
            *) refuse "tillr drain $VERB is forbidden on the triage seat" ;;
        esac
        ;;
    watch)
        case "$VERB" in
            status|lint|record-triage|-h|--help|--version) run_real "$@" ;;
            *) refuse "tillr watch $VERB is forbidden on the triage seat" ;;
        esac
        ;;
    store)
        case "$VERB" in
            inventory|definition|check|events|-h|--help|--version) run_real "$@" ;;
            *) refuse "tillr store $VERB is forbidden on the triage seat" ;;
        esac
        ;;
    trace)
        case "$VERB" in
            tick|audit|run|cost|discrepancies|-h|--help|--version) run_real "$@" ;;
            *) refuse "tillr trace $VERB is forbidden on the triage seat" ;;
        esac
        ;;
    *)
        refuse "$BIN_NAME $GROUP is forbidden on the triage seat"
        ;;
esac
`, shSingleQuote(binName), shSingleQuote(binName), shSingleQuote(realBin), shSingleQuote(fixedGroup))
}

func renderTriageGitWrapper(realGit string) string {
	return fmt.Sprintf(`#!/bin/sh
# tillr-triage-git-guard
REAL_GIT=%s

refuse() {
    printf 'triage boundary: %%s\n' "$1" >&2
    exit 2
}

if [ -z "$REAL_GIT" ]; then
    refuse "real git binary not found in PATH"
fi

subcmd=""
expect_val=0
sub_idx=0
idx=0
for arg in "$@"; do
    idx=$((idx + 1))
    if [ "$expect_val" = 1 ]; then
        expect_val=0
        continue
    fi
    case "$arg" in
        --)
            break
            ;;
        -h|--help|--version)
            exec "$REAL_GIT" "$@"
            ;;
        -C|-c|--git-dir|--work-tree|--namespace|--super-prefix|--config-env)
            expect_val=1
            ;;
        -*)
            ;;
        *)
            subcmd="$arg"
            sub_idx="$idx"
            break
            ;;
    esac
done

if [ -z "$subcmd" ]; then
    exec "$REAL_GIT" "$@"
fi

case "$subcmd" in
    status|log|show|diff|rev-parse|rev-list|cat-file|ls-files|ls-tree|merge-base|describe|shortlog|blame|grep|for-each-ref|show-ref|name-rev|check-ignore|check-attr|count-objects|fsck|verify-commit|verify-tag|version|help)
        exec "$REAL_GIT" "$@"
        ;;
    config)
        has_read=0
        i=0
        for arg in "$@"; do
            i=$((i + 1))
            [ "$i" -le "$sub_idx" ] && continue
            case "$arg" in
                -h|--help) exec "$REAL_GIT" "$@" ;;
                --add|--replace-all|--unset|--unset-all|--rename-section|--remove-section|--edit|-e)
                    refuse "git config write ($arg) is forbidden on the triage seat"
                    ;;
                -l|--list|--get|--get-all|--get-regexp)
                    has_read=1
                    ;;
            esac
        done
        if [ "$has_read" != 1 ]; then
            refuse "git config on triage seat is restricted to read-only queries (--list/--get)"
        fi
        exec "$REAL_GIT" "$@"
        ;;
    branch)
        has_ro_flag=0
        pos_count=0
        i=0
        for arg in "$@"; do
            i=$((i + 1))
            [ "$i" -le "$sub_idx" ] && continue
            case "$arg" in
                -h|--help) exec "$REAL_GIT" "$@" ;;
                -d|-D|-m|-M|-c|-C|--delete|--move|--copy|--set-upstream-to|--unset-upstream|--edit-description)
                    refuse "git branch mutation ($arg) is forbidden on the triage seat"
                    ;;
                -l|--list|--show-current|--contains|--no-contains|--merged|--no-merged|-a|--all|-r|--remotes)
                    has_ro_flag=1
                    ;;
                -*)
                    ;;
                *)
                    pos_count=$((pos_count + 1))
                    ;;
            esac
        done
        if [ "$pos_count" -gt 0 ] && [ "$has_ro_flag" != 1 ]; then
            refuse "git branch creation is forbidden on the triage seat"
        fi
        exec "$REAL_GIT" "$@"
        ;;
    tag)
        i=0
        ro=1
        for arg in "$@"; do
            i=$((i + 1))
            [ "$i" -le "$sub_idx" ] && continue
            case "$arg" in
                -h|--help|-l|--list|-v|--verify|-n*) ;;
                -*) ro=0 ;;
                *) ro=0 ;;
            esac
        done
        if [ "$ro" != 1 ]; then
            refuse "git tag mutation is forbidden on the triage seat"
        fi
        exec "$REAL_GIT" "$@"
        ;;
    worktree)
        i=0
        wt_sub=""
        for arg in "$@"; do
            i=$((i + 1))
            [ "$i" -le "$sub_idx" ] && continue
            case "$arg" in
                -h|--help) exec "$REAL_GIT" "$@" ;;
                -*) ;;
                *) wt_sub="$arg"; break ;;
            esac
        done
        if [ "$wt_sub" != "list" ]; then
            refuse "git worktree $wt_sub is forbidden on the triage seat"
        fi
        exec "$REAL_GIT" "$@"
        ;;
    remote)
        i=0
        rem_sub=""
        for arg in "$@"; do
            i=$((i + 1))
            [ "$i" -le "$sub_idx" ] && continue
            case "$arg" in
                -h|--help|-v|--verbose) ;;
                -*) ;;
                *) rem_sub="$arg"; break ;;
            esac
        done
        case "$rem_sub" in
            ""|show|get-url) exec "$REAL_GIT" "$@" ;;
            *) refuse "git remote $rem_sub is forbidden on the triage seat" ;;
        esac
        ;;
    *)
        refuse "git $subcmd is forbidden on the triage seat"
        ;;
esac
`, shSingleQuote(realGit))
}

func renderTriageFileMutatorWrapper(binName, realBin, durableDir string) string {
	return fmt.Sprintf(`#!/bin/sh
# tillr-triage-file-guard (%s)
BIN_NAME=%s
REAL_BIN=%s
DURABLE_DIR=%s

refuse() {
    printf 'triage boundary: %%s\n' "$1" >&2
    exit 2
}

if [ -z "$REAL_BIN" ]; then
    refuse "$BIN_NAME binary not found in PATH"
fi

canon_dir() {
    if [ -d "$1" ]; then
        (cd -P -- "$1" 2>/dev/null && pwd -P)
    fi
}

ALLOW_DURABLE="$(canon_dir "$DURABLE_DIR")"
ALLOW_TMP=""
if [ -n "${TMPDIR:-}" ] && [ -d "$TMPDIR" ]; then
    ALLOW_TMP="$(canon_dir "$TMPDIR")"
fi

resolve_path() {
    _p="$1"
    case "$_p" in
        /*) ;;
        *) _p="$PWD/$_p" ;;
    esac
    # Strip trailing slashes (except root).
    while [ "$_p" != "/" ]; do
        case "$_p" in
            */) _p="${_p%%/}" ;;
            *) break ;;
        esac
    done
    _cur="$_p"
    _tail=""
    while [ ! -e "$_cur" ] && [ ! -L "$_cur" ]; do
        [ "$_cur" = "/" ] && break
        _base="${_cur##*/}"
        if [ -z "$_tail" ]; then
            _tail="$_base"
        else
            _tail="$_base/$_tail"
        fi
        _parent="${_cur%%/*}"
        [ -z "$_parent" ] && _parent="/"
        _cur="$_parent"
    done
    if [ -d "$_cur" ]; then
        _resolved="$(cd -P -- "$_cur" 2>/dev/null && pwd -P)"
    elif [ -L "$_cur" ] || [ -e "$_cur" ]; then
        _pdir="${_cur%%/*}"
        [ -z "$_pdir" ] && _pdir="/"
        _pbase="${_cur##*/}"
        _rdir="$(cd -P -- "$_pdir" 2>/dev/null && pwd -P)"
        if [ -L "$_cur" ] && [ "$BIN_NAME" != "rm" ] && [ "$BIN_NAME" != "mv" ] && [ "$BIN_NAME" != "ln" ]; then
            if command -v realpath >/dev/null 2>&1; then
                _resolved="$(realpath -m -- "$_cur" 2>/dev/null)"
            else
                _resolved="$_rdir/$_pbase"
            fi
        else
            _resolved="$_rdir/$_pbase"
        fi
    else
        _resolved="$_cur"
    fi
    if [ -n "$_tail" ]; then
        case "$_tail" in
            *..*|*/.*|*./*)
                # Reject unnormalized traversal in non-existent tail.
                if command -v realpath >/dev/null 2>&1; then
                    _resolved="$(realpath -m -- "$_resolved/$_tail" 2>/dev/null)"
                else
                    return 1
                fi
                ;;
            *)
                _resolved="${_resolved%%/}/$_tail"
                ;;
        esac
    fi
    printf '%%s\n' "$_resolved"
}

in_scratch() {
    _r="$(resolve_path "$1")" || return 1
    [ -n "$_r" ] || return 1
    if [ -n "$ALLOW_DURABLE" ]; then
        case "$_r" in
            "$ALLOW_DURABLE"|"$ALLOW_DURABLE"/*) return 0 ;;
        esac
    fi
    if [ -n "$ALLOW_TMP" ]; then
        case "$_r" in
            "$ALLOW_TMP"|"$ALLOW_TMP"/*) return 0 ;;
        esac
    fi
    return 1
}

check_target() {
    if ! in_scratch "$1"; then
        refuse "$BIN_NAME target '$1' resolves outside run scratch"
    fi
}

# Parse options and positional args per utility.
_pos_list=""
_target_dir=""
_expect_val=0
_end_opts=0
_skip_first_pos=0
if [ "$BIN_NAME" = "chmod" ] || [ "$BIN_NAME" = "chown" ]; then
    _skip_first_pos=1
fi

for _arg in "$@"; do
    if [ "$_expect_val" = "target-dir" ]; then
        _target_dir="$_arg"
        _expect_val=0
        continue
    elif [ "$_expect_val" = "ignore" ]; then
        _expect_val=0
        continue
    fi
    if [ "$_end_opts" = 0 ]; then
        case "$_arg" in
            --) _end_opts=1; continue ;;
            -t|--target-directory)
                if [ "$BIN_NAME" = "cp" ] || [ "$BIN_NAME" = "mv" ] || [ "$BIN_NAME" = "ln" ]; then
                    _expect_val="target-dir"
                    continue
                fi
                ;;
            --target-directory=*)
                if [ "$BIN_NAME" = "cp" ] || [ "$BIN_NAME" = "mv" ] || [ "$BIN_NAME" = "ln" ]; then
                    _target_dir="${_arg#--target-directory=}"
                    continue
                fi
                ;;
            --reference=*)
                if [ "$BIN_NAME" = "chmod" ] || [ "$BIN_NAME" = "chown" ]; then
                    _skip_first_pos=0
                    continue
                fi
                ;;
            -s|--size)
                if [ "$BIN_NAME" = "truncate" ]; then
                    _expect_val="ignore"
                    continue
                fi
                ;;
            -S|-d|-r|--suffix|--date|--reference)
                _expect_val="ignore"
                continue
                ;;
            -*)
                continue
                ;;
        esac
    fi
    if [ "$_skip_first_pos" = 1 ]; then
        _skip_first_pos=0
        continue
    fi
    if [ -z "$_pos_list" ]; then
        _pos_list="$_arg"
    else
        _pos_list="$_pos_list
$_arg"
    fi
done

if [ -n "$_target_dir" ]; then
    check_target "$_target_dir"
fi

case "$BIN_NAME" in
    cp|ln)
        if [ -z "$_target_dir" ]; then
            _last=""
            _count=0
            _old_ifs="$IFS"
            IFS='
'
            for _p in $_pos_list; do
                _last="$_p"
                _count=$((_count + 1))
            done
            IFS="$_old_ifs"
            if [ "$BIN_NAME" = "ln" ] && [ "$_count" -eq 1 ]; then
                check_target "."
            elif [ -n "$_last" ]; then
                check_target "$_last"
            fi
        fi
        ;;
    tee)
        if [ -n "$_pos_list" ]; then
            _old_ifs="$IFS"
            IFS='
'
            for _p in $_pos_list; do
                check_target "$_p"
            done
            IFS="$_old_ifs"
        fi
        ;;
    *)
        _old_ifs="$IFS"
        IFS='
'
        for _p in $_pos_list; do
            check_target "$_p"
        done
        IFS="$_old_ifs"
        ;;
esac

exec "$REAL_BIN" "$@"
`, shSingleQuote(binName), shSingleQuote(binName), shSingleQuote(realBin), shSingleQuote(durableDir))
}
