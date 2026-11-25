package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func resolveRunModuleDir(target string) (string, error) {
	root := os.Getenv("TILLR_INSTALL_ROOT")
	if root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("TILLR_INSTALL_ROOT must be absolute: %s", root)
		}
		return filepath.Join(root, "tillr-run"), nil
	}
	// A direct source invocation runs a binary in the checkout or starts
	// from its root, module root, or package subdirectory (as with go run
	// or go test). Never walk arbitrary consumer ancestors or search PATH
	// to infer an installation.
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	exe, _ := os.Executable()
	for _, dir := range []string{filepath.Dir(exe), cwd} {
		checkout := dir
		parent := filepath.Dir(dir)
		grandparent := filepath.Dir(parent)
		switch {
		case filepath.Base(parent) == "tools":
			checkout = grandparent
		case filepath.Base(grandparent) == "tools":
			checkout = filepath.Dir(grandparent)
		}
		module, err := os.ReadFile(filepath.Join(checkout, "tools", "tillr-run", "go.mod"))
		if err == nil && strings.HasPrefix(string(module), "module github.com/mschulkind-oss/tillr-run\n") {
			return filepath.Join(checkout, "tools", "tillr-run"), nil
		}
	}
	return "", fmt.Errorf("cannot locate tillr source checkout for %s; set TILLR_INSTALL_ROOT", target)
}

// ResolveBackendsDir resolves the installed tillr-run backends directory, or the
// source checkout's tools/tillr-run/backends when invoked directly from tillr's
// source checkout. It never falls back to an operated repository's tools/.
func ResolveBackendsDir() (string, error) {
	modDir, err := resolveRunModuleDir("backends directory")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(modDir, "backends")
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("installed backends directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("installed backends directory %s is not a directory", dir)
	}
	return dir, nil
}

// ResolveBriefTemplate resolves the installed tillr-run brief template, or the
// source checkout's tools/tillr-run/backends/brief-template.md when invoked
// directly from tillr's source checkout. It never falls back to an operated
// repository's tools/.
func ResolveBriefTemplate() (string, error) {
	modDir, err := resolveRunModuleDir("brief template")
	if err != nil {
		return "", err
	}
	path := filepath.Join(modDir, "backends", "brief-template.md")
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("installed brief template %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("installed brief template %s is not a regular file", path)
	}
	return path, nil
}

// ResolvePolicyAdapter resolves only tillr's built-in policy names. Other command
// names and explicit paths keep their existing semantics.
func ResolvePolicyAdapter(adapter string) (string, error) {
	var kind string
	switch adapter {
	case "run-pi", "run-claude", "run-agy", "run-muse":
		kind = strings.TrimPrefix(adapter, "run-")
	default:
		return adapter, nil
	}
	modDir, err := resolveRunModuleDir("adapter " + adapter)
	if err != nil {
		return "", err
	}
	path := filepath.Join(modDir, "backends", kind, adapter)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("installed adapter %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("installed adapter %s is not an executable file", path)
	}
	return path, nil
}
