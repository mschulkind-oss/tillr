package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type goEnvCacheKey struct {
	root string
	env  string
}

type cachedModuleIdentity struct {
	LocalReplaces []string `json:"local_replaces,omitempty"`
}

var (
	goEnvMu    sync.Mutex
	goEnvCache = map[goEnvCacheKey]map[string]string{}

	goModMu    sync.Mutex
	goModCache = map[string]cachedModuleIdentity{}
)

func cachedGoEnv(root string, env []string) (map[string]string, error) {
	key := goEnvCacheKey{root: root, env: strings.Join(env, "\x00")}
	goEnvMu.Lock()
	defer goEnvMu.Unlock()

	if cached, ok := goEnvCache[key]; ok {
		copied := make(map[string]string, len(cached))
		for k, v := range cached {
			copied[k] = v
		}
		return copied, nil
	}

	cmd := exec.Command("go", "env", "-json")
	cmd.Dir, cmd.Env = root, env
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Go build environment: %w", err)
	}
	var settings map[string]string
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, err
	}
	goEnvCache[key] = settings
	copied := make(map[string]string, len(settings))
	for k, v := range settings {
		copied[k] = v
	}
	return copied, nil
}

func hasNestedWorkspace(root, dir string) bool {
	cur := filepath.ToSlash(filepath.Clean(dir))
	for {
		if _, err := os.Stat(filepath.Join(root, cur, "go.work")); err == nil {
			return true
		}
		if cur == "." || cur == "/" || cur == "" {
			break
		}
		parent := filepath.ToSlash(filepath.Dir(cur))
		if parent == cur {
			break
		}
		cur = parent
	}
	return false
}

func goModuleStoreDir(root string) string {
	store, err := openResultStore(root)
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(store.dir), "go-mod-identity")
}

func moduleContentKey(modBytes, sumBytes []byte, sumExists bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "v1\x00%d\x00", len(modBytes))
	h.Write(modBytes)
	if sumExists {
		fmt.Fprintf(h, "\x001\x00%d\x00", len(sumBytes))
		h.Write(sumBytes)
	} else {
		h.Write([]byte("\x000"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func readCachedModuleIdentity(storeDir, key string) (cachedModuleIdentity, bool) {
	goModMu.Lock()
	cached, ok := goModCache[key]
	goModMu.Unlock()
	if ok {
		return cached, true
	}
	if storeDir == "" {
		return cachedModuleIdentity{}, false
	}
	raw, err := os.ReadFile(filepath.Join(storeDir, key+".json"))
	if err != nil {
		return cachedModuleIdentity{}, false
	}
	if err := json.Unmarshal(raw, &cached); err != nil {
		return cachedModuleIdentity{}, false
	}
	goModMu.Lock()
	goModCache[key] = cached
	goModMu.Unlock()
	return cached, true
}

func writeCachedModuleIdentity(storeDir, key string, id cachedModuleIdentity) {
	goModMu.Lock()
	goModCache[key] = id
	goModMu.Unlock()
	if storeDir == "" {
		return
	}
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return
	}
	raw, err := json.Marshal(id)
	if err != nil {
		return
	}
	file, err := os.CreateTemp(storeDir, ".mod-")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return
	}
	if err := file.Close(); err != nil {
		return
	}
	_ = os.Rename(file.Name(), filepath.Join(storeDir, key+".json"))
}

func goModuleIdentity(modDir string, env []string, storeDir string) (cachedModuleIdentity, bool, error) {
	modBytes, err := os.ReadFile(filepath.Join(modDir, "go.mod"))
	if err == nil {
		sumBytes, sumErr := os.ReadFile(filepath.Join(modDir, "go.sum"))
		sumExists := sumErr == nil
		if sumErr == nil || os.IsNotExist(sumErr) {
			key := moduleContentKey(modBytes, sumBytes, sumExists)
			if cached, ok := readCachedModuleIdentity(storeDir, key); ok {
				return cached, true, nil
			}
			cmd := exec.Command("go", "mod", "edit", "-json")
			cmd.Dir, cmd.Env = modDir, env
			raw, editErr := cmd.Output()
			if editErr != nil {
				return cachedModuleIdentity{}, false, nil
			}
			var metadata struct {
				Replace []struct {
					New struct{ Path, Version string }
				}
			}
			if err := json.Unmarshal(raw, &metadata); err != nil {
				return cachedModuleIdentity{}, false, err
			}
			var id cachedModuleIdentity
			for _, replacement := range metadata.Replace {
				if replacement.New.Version != "" {
					continue
				}
				id.LocalReplaces = append(id.LocalReplaces, replacement.New.Path)
			}
			writeCachedModuleIdentity(storeDir, key, id)
			return id, true, nil
		}
	}

	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir, cmd.Env = modDir, env
	raw, err := cmd.Output()
	if err != nil {
		return cachedModuleIdentity{}, false, nil
	}
	var metadata struct {
		Replace []struct {
			New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return cachedModuleIdentity{}, false, err
	}
	var id cachedModuleIdentity
	for _, replacement := range metadata.Replace {
		if replacement.New.Version != "" {
			continue
		}
		id.LocalReplaces = append(id.LocalReplaces, replacement.New.Path)
	}
	return id, true, nil
}

// goBuildIdentity binds inherited build options, not just `go version`. Inputs
// selected by workspaces, external local replacements or file-valued flags cannot
// be proved by the static declaration: execute normally, but remember no verdict.
func goBuildIdentity(check PlanCheck, root string, inputs []treeInput) (string, string, error) {
	env := checkEnv(check)
	settings, err := cachedGoEnv(root, env)
	if err != nil {
		return "", "", err
	}
	reason := ""
	if work := settings["GOWORK"]; work != "" && work != "off" {
		reason = "Go workspace inputs"
	}
	for _, flag := range []string{"-overlay", "-modfile", "-toolexec", "-pkgdir", "-asmflags", "-gcflags", "-ldflags", "-gccgoflags", "-compiler", "-C"} {
		if strings.Contains(settings["GOFLAGS"], flag) {
			reason = "Go build flags with undeclared inputs"
		}
	}
	storeDir := goModuleStoreDir(root)
	// Go's parser, rather than a second module-file grammar. This is metadata only,
	// never a compile, and catches local inputs outside the declaration's fence.
	for _, input := range inputs {
		if input.Mode == "120000" && coversPath(check.Reads, input.Path) {
			reason = "symlinked Go inputs"
		}
		if !strings.HasSuffix(input.Path, "/go.mod") || !coversPath(check.Reads, input.Path) {
			continue
		}
		dir := filepath.Dir(input.Path)
		if settings["GOWORK"] == "" && hasNestedWorkspace(root, dir) {
			reason = "Go workspace inputs in " + dir
		}
		id, ok, err := goModuleIdentity(filepath.Join(root, dir), env, storeDir)
		if err != nil {
			return "", "", err
		}
		if !ok {
			reason = "unreadable Go module metadata"
			continue // the real build retains its ordinary compile verdict
		}
		for _, target := range id.LocalReplaces {
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, dir, target)
			}
			relative, err := filepath.Rel(root, target)
			if err != nil || !coversPath(check.Reads, filepath.ToSlash(relative)) {
				reason = "local Go replacement outside declared inputs"
			}
		}
	}
	// Build cache locations do not change compiled semantics. GOGCCFLAGS carries
	// a new temporary path each query; this check turns cgo off in its program.
	for _, name := range []string{"GOMOD", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOENV", "GOROOT", "GOTOOLDIR", "GOGCCFLAGS"} {
		delete(settings, name)
	}
	raw, err := json.Marshal(settings)
	return string(raw), reason, err
}
