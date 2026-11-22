package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// gitGuardDir is the per-run directory prepended to PATH when the run's working
// directory belongs to a Git repository. It contains a thin `git` wrapper that
// refuses `git init` and local `git config` writes targeting the trunk's common
// Git directory via GIT_DIR / --git-dir / GIT_COMMON_DIR, while forwarding all
// ordinary lane commands and unrelated test repositories straight to the real
// `git` binary.
const gitGuardDirName = "git-guard"

var gitRepoEnvKeys = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_COMMON_DIR",
	"GIT_INDEX_FILE",
}

func installGitGuard(workDir, durableDir string, env []string) ([]string, error) {
	// A run owns its working directory; inherited repository-redirecting Git
	// variables from the caller's shell would point child git commands at the
	// wrong repository.
	for _, key := range gitRepoEnvKeys {
		env = withoutEnv(env, key)
	}
	guardDir := filepath.Join(durableDir, gitGuardDirName)
	guardPath := filepath.Join(guardDir, "git")
	pathVal := lookupEnvValue(env, "PATH")
	realGit := findRealGit(pathVal, guardPath)
	if realGit == "" {
		return env, nil
	}
	commonDir := discoverGitCommonDir(workDir, env)
	if commonDir == "" {
		return env, nil
	}
	if err := os.MkdirAll(guardDir, 0o755); err != nil {
		return nil, Internalf("creating git guard directory %q: %v", guardDir, err)
	}
	script := renderGitGuardScript(realGit, commonDir)
	if err := os.WriteFile(guardPath, []byte(script), 0o755); err != nil {
		return nil, Internalf("writing git guard script %q: %v", guardPath, err)
	}
	env = withoutEnv(env, "PATH")
	if pathVal == "" {
		env = append(env, "PATH="+guardDir)
	} else if !hasPathPrefix(pathVal, guardDir) {
		env = append(env, "PATH="+guardDir+string(os.PathListSeparator)+pathVal)
	} else {
		env = append(env, "PATH="+pathVal)
	}
	return env, nil
}

func lookupEnvValue(env []string, key string) string {
	prefix := key + "="
	val := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			val = entry[len(prefix):]
		}
	}
	return val
}

func hasPathPrefix(pathVal, dir string) bool {
	parts := filepath.SplitList(pathVal)
	return len(parts) > 0 && filepath.Clean(parts[0]) == filepath.Clean(dir)
}

func findRealGit(pathVal, excludePath string) string {
	excludeClean := filepath.Clean(excludePath)
	excludeReal, _ := filepath.EvalSymlinks(excludeClean)
	for _, dir := range filepath.SplitList(pathVal) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "git")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		candClean := filepath.Clean(candidate)
		if candClean == excludeClean {
			continue
		}
		if excludeReal != "" {
			if candReal, err := filepath.EvalSymlinks(candClean); err == nil && candReal == excludeReal {
				continue
			}
		}
		return candClean
	}
	return ""
}

func parseCeilingDirs(env []string) map[string]bool {
	raw := lookupEnvValue(env, "GIT_CEILING_DIRECTORIES")
	if raw == "" {
		return nil
	}
	ceilings := make(map[string]bool)
	for _, entry := range filepath.SplitList(raw) {
		if entry == "" {
			continue
		}
		clean := filepath.Clean(entry)
		ceilings[clean] = true
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			ceilings[filepath.Clean(resolved)] = true
		}
	}
	return ceilings
}

func discoverGitCommonDir(workDir string, cleanEnv []string) string {
	absWork, err := filepath.Abs(workDir)
	if err != nil {
		return ""
	}
	ceilings := parseCeilingDirs(cleanEnv)
	cur := filepath.Clean(absWork)
	for {
		dotGit := filepath.Join(cur, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			var gitDir string
			if info.IsDir() {
				gitDir = dotGit
			} else if info.Mode().IsRegular() {
				if raw, err := os.ReadFile(dotGit); err == nil {
					line := strings.TrimSpace(string(raw))
					if strings.HasPrefix(line, "gitdir:") {
						target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
						if target != "" {
							if !filepath.IsAbs(target) {
								target = filepath.Join(cur, target)
							}
							gitDir = filepath.Clean(target)
						}
					}
				}
			}
			if gitDir != "" {
				if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err == nil {
					commonDir := gitDir
					if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
						if rel := strings.TrimSpace(string(raw)); rel != "" {
							if !filepath.IsAbs(rel) {
								rel = filepath.Join(gitDir, rel)
							}
							commonDir = filepath.Clean(rel)
						}
					}
					if resolved, err := filepath.EvalSymlinks(commonDir); err == nil {
						return filepath.Clean(resolved)
					}
					return filepath.Clean(commonDir)
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur || ceilings[parent] {
			break
		}
		if resolved, err := filepath.EvalSymlinks(parent); err == nil && ceilings[filepath.Clean(resolved)] {
			break
		}
		cur = parent
	}
	return ""
}

func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func renderGitGuardScript(realGit, protectedCommonDir string) string {
	return fmt.Sprintf(`#!/bin/sh
# tillr-git-guard
REAL_GIT=%s
PROTECTED_COMMON_DIR=%s

resolve_dir() {
    _d="$1"
    case "$_d" in
        /*) ;;
        *) _d="${_base_dir}/$_d" ;;
    esac
    if [ -d "$_d" ]; then
        (cd -P -- "$_d" 2>/dev/null && pwd -P)
    fi
}

targets_protected() {
    [ -n "$1" ] || return 1
    _p="$(resolve_dir "$1")"
    [ -n "$_p" ] || return 1
    case "$_p" in
        "$PROTECTED_COMMON_DIR"|"$PROTECTED_COMMON_DIR"/*) return 0 ;;
    esac
    if [ -f "$_p/commondir" ]; then
        _c="$(cat "$_p/commondir" 2>/dev/null)"
        if [ -n "$_c" ]; then
            case "$_c" in
                /*) _cp="$_c" ;;
                *) _cp="$_p/$_c" ;;
            esac
            _cp="$(cd -P -- "$_cp" 2>/dev/null && pwd -P)"
            case "$_cp" in
                "$PROTECTED_COMMON_DIR"|"$PROTECTED_COMMON_DIR"/*) return 0 ;;
            esac
        fi
    fi
    return 1
}

check_args() {
    _base_dir="$PWD"
    _opt_git_dir=""
    _subcmd=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --)
                shift
                break
                ;;
            -C)
                [ "$#" -ge 2 ] || break
                case "$2" in
                    /*) _base_dir="$2" ;;
                    *) _base_dir="${_base_dir}/$2" ;;
                esac
                shift 2
                ;;
            -C*)
                _cdir="${1#-C}"
                case "$_cdir" in
                    /*) _base_dir="$_cdir" ;;
                    *) _base_dir="${_base_dir}/$_cdir" ;;
                esac
                shift
                ;;
            -c|--namespace|--super-prefix|--config-env)
                [ "$#" -ge 2 ] || break
                shift 2
                ;;
            --git-dir)
                [ "$#" -ge 2 ] || break
                _opt_git_dir="$2"
                shift 2
                ;;
            --git-dir=*)
                _opt_git_dir="${1#--git-dir=}"
                shift
                ;;
            --work-tree)
                [ "$#" -ge 2 ] || break
                shift 2
                ;;
            --work-tree=*)
                shift
                ;;
            -*)
                shift
                ;;
            *)
                _subcmd="$1"
                shift
                break
                ;;
        esac
    done

    case "$_subcmd" in
        init|config) ;;
        *) return 0 ;;
    esac

    _eff_git_dir="${_opt_git_dir:-${GIT_DIR:-}}"
    _eff_common_dir="${GIT_COMMON_DIR:-}"
    if ! targets_protected "$_eff_git_dir" && ! targets_protected "$_eff_common_dir"; then
        return 0
    fi

    if [ "$_subcmd" = "init" ]; then
        printf '%%s\n' "tillr-git-guard: refusing 'git init' targeting protected trunk git directory ($PROTECTED_COMMON_DIR); do not export GIT_DIR or GIT_WORK_TREE" >&2
        exit 1
    fi

    # For 'git config', allow read-only queries and non-local scopes.
    _pos=0
    _write_flag=0
    _end_opts=0
    for _arg in "$@"; do
        if [ "$_end_opts" -eq 0 ]; then
            case "$_arg" in
                --)
                    _end_opts=1
                    continue
                    ;;
                --global|--system|--file|-f|--file=*|--blob|--blob=*|--get|--get-all|--get-regexp|--get-urlmatch|--list|-l|--help|-h)
                    return 0
                    ;;
                --add|--replace-all|--unset|--unset-all|--rename-section|--remove-section)
                    _write_flag=1
                    continue
                    ;;
                -*)
                    continue
                    ;;
            esac
        fi
        if [ "$_pos" -eq 0 ]; then
            case "$_arg" in
                get|list) return 0 ;;
                set|unset) _write_flag=1 ;;
            esac
        fi
        _pos=$((_pos + 1))
    done
    if [ "$_write_flag" -eq 0 ] && [ "$_pos" -le 1 ]; then
        return 0
    fi
    printf '%%s\n' "tillr-git-guard: refusing 'git config' write targeting protected trunk git directory ($PROTECTED_COMMON_DIR); do not export GIT_DIR or GIT_WORK_TREE" >&2
    exit 1
}

check_args "$@"
exec "$REAL_GIT" "$@"
`, shSingleQuote(realGit), shSingleQuote(protectedCommonDir))
}
