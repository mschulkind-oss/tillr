package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
	root := os.Getenv("TILLR_INSTALL_ROOT")
	var path string
	if root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("TILLR_INSTALL_ROOT must be absolute: %s", root)
		}
		path = filepath.Join(root, "tillr-run", "backends", kind, adapter)
	} else {
		// A direct source invocation runs a binary in the checkout or starts
		// from its root or module root (as with go run). Never walk consumer
		// ancestors or search PATH to infer an installation.
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		exe, _ := os.Executable()
		for _, dir := range []string{filepath.Dir(exe), cwd} {
			checkout := dir
			if filepath.Base(filepath.Dir(dir)) == "tools" && (filepath.Base(dir) == "tillr-run" || filepath.Base(dir) == "tillr-drain") {
				checkout = filepath.Dir(filepath.Dir(dir))
			}
			module, err := os.ReadFile(filepath.Join(checkout, "tools", "tillr-run", "go.mod"))
			if err == nil && strings.HasPrefix(string(module), "module github.com/mschulkind-oss/tillr-run\n") {
				path = filepath.Join(checkout, "tools", "tillr-run", "backends", kind, adapter)
				break
			}
		}
		if path == "" {
			return "", fmt.Errorf("cannot locate tillr source checkout for adapter %s; set TILLR_INSTALL_ROOT", adapter)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("installed adapter %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("installed adapter %s is not an executable file", path)
	}
	return path, nil
}
