package drain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func resolveDrainModuleDir(target string) (string, error) {
	root := os.Getenv("TILLR_INSTALL_ROOT")
	if root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("TILLR_INSTALL_ROOT must be absolute: %s", root)
		}
		return filepath.Join(root, "tillr-drain"), nil
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
		module, err := os.ReadFile(filepath.Join(checkout, "tools", "tillr-drain", "go.mod"))
		if err == nil && strings.HasPrefix(string(module), "module github.com/mschulkind-oss/tillr-drain\n") {
			return filepath.Join(checkout, "tools", "tillr-drain"), nil
		}
	}
	return "", fmt.Errorf("cannot locate tillr source checkout for %s; set TILLR_INSTALL_ROOT", target)
}

// ResolveForkGate resolves the installed tillr-drain fork-gate.py, or the
// source checkout's tools/tillr-drain/fork-gate.py when invoked directly from
// tillr's source checkout. It never falls back to an operated repository's tools/.
func ResolveForkGate() (string, error) {
	modDir, err := resolveDrainModuleDir("fork-gate.py")
	if err != nil {
		return "", err
	}
	path := filepath.Join(modDir, "fork-gate.py")
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("installed fork-gate.py %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("installed fork-gate.py %s is not a regular file", path)
	}
	return path, nil
}
