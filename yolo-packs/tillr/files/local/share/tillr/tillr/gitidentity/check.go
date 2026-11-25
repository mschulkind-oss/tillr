package gitidentity

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	planBaseEnv = "FORK_CHECK_PLAN_BASE"
	policyEnv   = "TILLR_IDENTITY_POLICY"
	maxReported = 10
)

var identityKeys = []string{"user.name", "user.email"}

// MainCheck runs the git-identity check for the current working directory.
func MainCheck(args []string, stdout, stderr io.Writer) int {
	return CheckIdentity("", nil, stdout)
}

// CheckIdentity checks author and committer identities in base..HEAD against the
// repository's allowed-commit-identities policy, and verifies the local/global config.
// dir and env may be empty/nil to use the current process working directory and environment.
func CheckIdentity(dir string, env []string, stdout io.Writer) int {
	var values [2]string
	type scopeKey struct {
		scope string
		key   string
	}
	var overrides []scopeKey
	seen := make(map[scopeKey]bool)

	for i, key := range identityKeys {
		value, scopes, problem := configured(dir, env, key)
		if problem != "" {
			return refuse(stdout, fmt.Sprintf("the configured git identity could not be read: %s", problem))
		}
		values[i] = value
		for _, scope := range scopes {
			sk := scopeKey{scope: scope, key: key}
			if !seen[sk] {
				seen[sk] = true
				overrides = append(overrides, sk)
			}
		}
	}

	if len(overrides) > 0 {
		fmt.Fprintln(stdout, "git-identity: FAIL: this repository's own config sets the landing identity")
		for _, sk := range overrides {
			fmt.Fprintf(stdout, "  %s %s overrides the deployed identity; unset it with git config --%s --unset-all %s\n",
				sk.scope, sk.key, sk.scope, sk.key)
		}
		return 1
	}

	if values[0] == "" || values[1] == "" {
		fmt.Fprintln(stdout, "git-identity: FAIL: the landing identity is not configured (git user.name and user.email)")
		fmt.Fprintln(stdout, "  remedy: configure the deployed Git identity outside local and worktree scope.")
		return 1
	}

	base := baseOf(dir, env)
	if base == "" {
		fmt.Fprintf(stdout, "git-identity: no diff base, so no commit can be called added by this change "+
			"and its identity was not judged (no usable %s, and no origin/main "+
			"to take a merge base from)\n", planBaseEnv)
		return 0
	}

	policyPath := resolvePolicyPath(dir, env)
	rawPolicy, err := os.ReadFile(policyPath)
	if err != nil {
		return refuse(stdout, fmt.Sprintf("cannot read the repository identity policy: %s", err))
	}
	var allowed [][]string
	if err := json.Unmarshal(rawPolicy, &allowed); err != nil {
		return refuse(stdout, fmt.Sprintf("cannot read the repository identity policy: %s", err))
	}

	shortBase := base
	if len(shortBase) > 10 {
		shortBase = shortBase[:10]
	}

	out, errStr, code, err := runGit(dir, env, "log", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce", base+"..HEAD")
	if err != nil || code != 0 {
		detail := errStr
		if detail == "" && err != nil {
			detail = err.Error()
		}
		return refuse(stdout, fmt.Sprintf("cannot read commits in %s..HEAD: %s", shortBase, detail))
	}

	type identityPair struct {
		name  string
		email string
	}
	allowedSet := make(map[identityPair]bool, len(allowed))
	for _, entry := range allowed {
		if len(entry) >= 2 {
			allowedSet[identityPair{name: entry[0], email: entry[1]}] = true
		}
	}

	var findings []string
	judged := 0
	for _, line := range splitLines(out) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) != 5 {
			continue
		}
		sha, authorName, authorEmail, committerName, committerEmail := parts[0], parts[1], parts[2], parts[3], parts[4]
		judged++
		roles := []struct {
			role  string
			name  string
			email string
		}{
			{"author", authorName, authorEmail},
			{"committer", committerName, committerEmail},
		}
		for _, r := range roles {
			if !allowedSet[identityPair{name: r.name, email: r.email}] {
				findings = append(findings, fmt.Sprintf("%s: %s %s <%s> is not an allowed repository identity",
					sha, r.role, r.name, r.email))
			}
		}
	}

	if len(findings) > 0 {
		fmt.Fprintf(stdout, "git-identity: FAIL: %d disallowed author/committer pair(s) in %s..HEAD:\n",
			len(findings), shortBase)
		limit := len(findings)
		if limit > maxReported {
			limit = maxReported
		}
		for _, finding := range findings[:limit] {
			fmt.Fprintf(stdout, "  %s\n", finding)
		}
		if len(findings) > maxReported {
			fmt.Fprintf(stdout, "  ... and %d more\n", len(findings)-maxReported)
		}
		fmt.Fprintln(stdout, "  remedy: drop the offending commits or recreate them with an approved identity read from Git config.")
		return 1
	}

	fmt.Fprintf(stdout, "git-identity: %d commit(s) in %s..HEAD carry only approved identities\n", judged, shortBase)
	return 0
}

func configured(dir string, env []string, key string) (value string, overrides []string, problem string) {
	out, errStr, code, err := runGit(dir, env, "config", "--show-scope", "--get-all", key)
	if code == 1 {
		return "", nil, ""
	}
	if err != nil || code != 0 {
		if errStr != "" {
			lines := splitLines(errStr)
			if len(lines) > 0 {
				return "", nil, lines[0]
			}
		}
		if code > 0 {
			return "", nil, fmt.Sprintf("git config exited %d", code)
		}
		if err != nil {
			return "", nil, err.Error()
		}
		return "", nil, fmt.Sprintf("git config exited %d", code)
	}
	for _, line := range splitLines(out) {
		scope, entry, _ := strings.Cut(line, "\t")
		if scope == "local" || scope == "worktree" {
			overrides = append(overrides, scope)
		} else {
			value = strings.TrimSpace(entry)
		}
	}
	return value, overrides, ""
}

func refuse(stdout io.Writer, message string) int {
	fmt.Fprintf(stdout, "git-identity: %s\n", message)
	return 2
}

func baseOf(dir string, env []string) string {
	named := strings.TrimSpace(lookupEnv(env, planBaseEnv))
	if named != "" {
		out, _, code, err := runGit(dir, env, "rev-parse", "--verify", "--quiet", named+"^{commit}")
		if err == nil && code == 0 {
			return strings.TrimSpace(out)
		}
		return ""
	}
	for _, upstream := range []string{"origin/main", "origin/HEAD"} {
		out, _, code, err := runGit(dir, env, "merge-base", "HEAD", upstream)
		if err == nil && code == 0 && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
	}
	return ""
}

func resolvePolicyPath(dir string, env []string) string {
	if p := strings.TrimSpace(lookupEnv(env, policyEnv)); p != "" {
		return p
	}
	rel := filepath.Join("tools", "gate", "allowed-commit-identities.json")
	if dir != "" {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	} else {
		if _, err := os.Stat(rel); err == nil {
			return rel
		}
	}
	if top, _, code, err := runGit(dir, env, "rev-parse", "--show-toplevel"); err == nil && code == 0 {
		candidate := filepath.Join(strings.TrimSpace(top), rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		return filepath.Join(filepath.Dir(file), "..", "..", "gate", "allowed-commit-identities.json")
	}
	return rel
}

func lookupEnv(env []string, key string) string {
	if env == nil {
		return os.Getenv(key)
	}
	prefix := key + "="
	val := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			val = strings.TrimPrefix(entry, prefix)
		}
	}
	return val
}
