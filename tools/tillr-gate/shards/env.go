package shards

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// EnvSliceToMap converts a KEY=VALUE slice into a map.
func EnvSliceToMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, entry := range environ {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}

// EnvMapToSlice converts a map into a KEY=VALUE slice.
func EnvMapToSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func cloneEnv(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func isGitShim(path string) bool {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "block" {
		parent := filepath.Dir(dir)
		if filepath.Base(parent) == "bin" && filepath.Base(filepath.Dir(parent)) == ".yolo" {
			return true
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	head := buf[:n]
	return bytes.HasPrefix(head, []byte("#!")) &&
		(bytes.Contains(head, []byte("_yg_tool=git")) || bytes.Contains(head, []byte("# tillr-git-guard")))
}

func isTillrGitGuard(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	return bytes.HasPrefix(buf[:n], []byte("#!/bin/sh\n# tillr-git-guard"))
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// ResolveRealGit finds the real git executable behind block/git or tillr-git-guard shims.
func ResolveRealGit(pathValue string) (string, bool) {
	sawShim := false
	for _, entry := range filepath.SplitList(pathValue) {
		if entry == "" {
			continue
		}
		candidate := filepath.Join(entry, "git")
		if !isExecutableFile(candidate) {
			continue
		}
		if isGitShim(candidate) {
			sawShim = true
			continue
		}
		return candidate, sawShim
	}
	if sawShim {
		for _, fallback := range []string{"/bin/git", "/usr/bin/git"} {
			if isExecutableFile(fallback) && !isGitShim(fallback) {
				return fallback, true
			}
		}
	}
	return "", sawShim
}

func ensureGitBinDir(baseDir, realGit string) (string, error) {
	sum := sha256.Sum256([]byte(realGit))
	digest := hex.EncodeToString(sum[:])[:12]
	binDir := filepath.Join(baseDir, "go-test-git-bin", digest)
	link := filepath.Join(binDir, "git")
	if target, err := os.Readlink(link); err == nil && target == realGit {
		return binDir, nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	tmpLink := filepath.Join(binDir, ".git."+strconv.Itoa(os.Getpid())+"."+strconv.FormatInt(int64(len(realGit)), 10))
	_ = os.Remove(tmpLink)
	if err := os.Symlink(realGit, tmpLink); err != nil {
		return "", err
	}
	if err := os.Rename(tmpLink, link); err != nil {
		_ = os.Remove(tmpLink)
		if target, rerr := os.Readlink(link); rerr == nil && target == realGit {
			return binDir, nil
		}
		return "", err
	}
	return binDir, nil
}

// BypassGitShimEnv returns a copy of baseEnv with git shims removed or shadowed.
func BypassGitShimEnv(baseEnv map[string]string, candidateRoots ...string) map[string]string {
	env := cloneEnv(baseEnv)
	pathVal := env["PATH"]
	realGit, sawShim := ResolveRealGit(pathVal)
	if !sawShim {
		return env
	}
	var kept []string
	for _, entry := range filepath.SplitList(pathVal) {
		if entry != "" && isTillrGitGuard(filepath.Join(entry, "git")) {
			continue
		}
		kept = append(kept, entry)
	}
	cleanedPath := strings.Join(kept, string(os.PathListSeparator))
	realAfter, stillShimmed := ResolveRealGit(cleanedPath)
	if !stillShimmed && realAfter != "" {
		env["PATH"] = cleanedPath
		return env
	}
	if realGit != "" {
		for _, rootDir := range candidateRoots {
			if rootDir == "" {
				continue
			}
			gitBin, err := ensureGitBinDir(rootDir, realGit)
			if err != nil {
				continue
			}
			if cleanedPath != "" {
				env["PATH"] = gitBin + string(os.PathListSeparator) + cleanedPath
			} else {
				env["PATH"] = gitBin
			}
			return env
		}
	}
	env["PATH"] = cleanedPath
	env["YOLO_BYPASS_SHIMS"] = "1"
	return env
}

// SharedCacheDir returns Go's shared build cache directory.
func SharedCacheDir(env map[string]string) string {
	gocache := strings.TrimSpace(env["GOCACHE"])
	if gocache != "" && gocache != "off" {
		if abs, err := filepath.Abs(gocache); err == nil {
			return abs
		}
		return gocache
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		p := filepath.Join(home, "Library", "Caches", "go-build")
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	xdg := strings.TrimSpace(env["XDG_CACHE_HOME"])
	base := xdg
	if base == "" {
		base = filepath.Join(home, ".cache")
	}
	p := filepath.Join(base, "go-build")
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func lookPathInPath(name, pathVal string) string {
	for _, entry := range filepath.SplitList(pathVal) {
		if entry == "" {
			continue
		}
		cand := filepath.Join(entry, name)
		if isExecutableFile(cand) {
			return cand
		}
	}
	return name
}

// GlobalGitConfig snapshots inherited global git config values, flattening includes.
func GlobalGitConfig(env map[string]string) ([][2]string, error) {
	if env == nil {
		env = EnvSliceToMap(os.Environ())
	}
	fallback := env["TMPDIR"]
	if fallback == "" {
		fallback = env["YOLO_DURABLE_DIR"]
	}
	var fallbackAbs string
	if fallback != "" {
		if abs, err := filepath.Abs(fallback); err == nil {
			fallbackAbs = abs
		}
	}
	gitEnv := BypassGitShimEnv(env, SharedCacheDir(env), fallbackAbs)
	gitExe := lookPathInPath("git", gitEnv["PATH"])
	cmd := exec.Command(gitExe, "config", "--list", "--includes", "--show-scope", "--null")
	cmd.Env = EnvMapToSlice(gitEnv)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimRight(string(out), "\x00")
	if raw == "" {
		return nil, nil
	}
	fields := strings.Split(raw, "\x00")
	var values [][2]string
	for i := 0; i+1 < len(fields); i += 2 {
		scope := fields[i]
		entry := fields[i+1]
		if scope != "global" {
			continue
		}
		key, val, hasSep := strings.Cut(entry, "\n")
		lower := strings.ToLower(key)
		if lower == "include.path" || strings.HasPrefix(lower, "includeif.") {
			continue
		}
		if !hasSep {
			val = "true"
		}
		values = append(values, [2]string{key, val})
	}
	return values, nil
}

// WritePrivateGitConfig writes a private gitconfig file disabling auto maintenance/gc.
func WritePrivateGitConfig(scratchDir string, gitValues [][2]string, env map[string]string) (string, error) {
	configPath := filepath.Join(scratchDir, "gitconfig")
	if err := os.WriteFile(configPath, nil, 0o644); err != nil {
		return "", err
	}
	if env == nil {
		env = EnvSliceToMap(os.Environ())
	}
	gitEnv := BypassGitShimEnv(env, SharedCacheDir(env), scratchDir)
	gitExe := lookPathInPath("git", gitEnv["PATH"])
	envSlice := EnvMapToSlice(gitEnv)
	for _, pair := range gitValues {
		key, val := pair[0], pair[1]
		lower := strings.ToLower(key)
		if lower == "maintenance.auto" || lower == "gc.auto" {
			continue
		}
		cmd := exec.Command(gitExe, "config", "--file", configPath, "--add", key, val)
		cmd.Env = envSlice
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git config --add %s: %w: %s", key, err, strings.TrimSpace(string(out)))
		}
	}
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString("[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n")
	cerr := f.Close()
	if werr != nil {
		return "", werr
	}
	if cerr != nil {
		return "", cerr
	}
	return configPath, nil
}

// PrivateEnvironment isolates a test or discovery process in scratch with a private gitconfig.
func PrivateEnvironment(label, scratch string, gitValues [][2]string) (map[string]string, error) {
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot prepare private git config: %v", label, err)}
	}
	base := EnvSliceToMap(os.Environ())
	configPath := filepath.Join(scratch, "gitconfig")
	base["TMPDIR"] = scratch
	base["YOLO_DURABLE_DIR"] = scratch
	base["GIT_CONFIG_GLOBAL"] = configPath
	env := BypassGitShimEnv(base, SharedCacheDir(base), scratch)

	values := gitValues
	if values == nil {
		var err error
		values, err = GlobalGitConfig(EnvSliceToMap(os.Environ()))
		if err != nil {
			return nil, &ShardError{Message: fmt.Sprintf("%s cannot prepare private git config: %v", label, err)}
		}
	}
	if _, err := WritePrivateGitConfig(scratch, values, env); err != nil {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot prepare private git config: %v", label, err)}
	}
	return env, nil
}
