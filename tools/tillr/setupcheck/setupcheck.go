package setupcheck

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const relBackendsPrefix = "tools/tillr-run/backends/"

var workflowRefShape = regexp.MustCompile(`^[a-z][a-z0-9-]*@v[1-9][0-9]*$`)

func resolveCheckoutDir(moduleRel, moduleLine string) (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	exe, _ := os.Executable()
	for _, dir := range []string{filepath.Dir(exe), cwd} {
		if dir == "" {
			continue
		}
		checkout := dir
		parent := filepath.Dir(dir)
		grandparent := filepath.Dir(parent)
		switch {
		case filepath.Base(parent) == "tools":
			checkout = grandparent
		case filepath.Base(grandparent) == "tools":
			checkout = filepath.Dir(grandparent)
		}
		module, err := os.ReadFile(filepath.Join(checkout, moduleRel))
		if err == nil && strings.HasPrefix(string(module), moduleLine) {
			return checkout, true
		}
	}
	return "", false
}

// ResolveInstallRoot returns TILLR_INSTALL_ROOT when set (requiring an absolute
// path), or the <checkout>/tools directory when invoked from a tillr source checkout.
func ResolveInstallRoot() (string, error) {
	if root := os.Getenv("TILLR_INSTALL_ROOT"); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("TILLR_INSTALL_ROOT must be absolute: %s", root)
		}
		return root, nil
	}
	if checkout, ok := resolveCheckoutDir(
		filepath.Join("tools", "tillr-run", "go.mod"),
		"module github.com/mschulkind-oss/tillr-run\n",
	); ok {
		return filepath.Join(checkout, "tools"), nil
	}
	return "", fmt.Errorf("cannot locate tillr installation root; set TILLR_INSTALL_ROOT")
}

// ReadInstallSupportRevision reads <installRoot>/tillr/buildstamp/support-revision.
func ReadInstallSupportRevision(installRoot string) (string, string, error) {
	path := filepath.Join(installRoot, "tillr", "buildstamp", "support-revision")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", path, err
	}
	return strings.TrimSpace(string(data)), path, nil
}

// CheckSupportFile verifies that path exists and is a regular file.
func CheckSupportFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// CheckGateRunner verifies that <installRoot>/tillr-gate/<name> exists as a regular file.
func CheckGateRunner(name string) (string, string, error) {
	installRoot, err := ResolveInstallRoot()
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(installRoot, "tillr-gate", name)
	if err := CheckSupportFile(path); err != nil {
		return installRoot, path, fmt.Errorf("installed gate runner %s: %w", path, err)
	}
	return installRoot, path, nil
}

func resolveRunModuleDir(target string) (string, error) {
	root := os.Getenv("TILLR_INSTALL_ROOT")
	if root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("TILLR_INSTALL_ROOT must be absolute: %s", root)
		}
		return filepath.Join(root, "tillr-run"), nil
	}
	if checkout, ok := resolveCheckoutDir(
		filepath.Join("tools", "tillr-run", "go.mod"),
		"module github.com/mschulkind-oss/tillr-run\n",
	); ok {
		return filepath.Join(checkout, "tools", "tillr-run"), nil
	}
	return "", fmt.Errorf("cannot locate tillr source checkout for %s; set TILLR_INSTALL_ROOT", target)
}

func resolveBackendsDir() (string, error) {
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

func checkExecutableFile(adapter, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("adapter %q (%s) is missing: %w", adapter, path, err)
		}
		return "", fmt.Errorf("adapter %q (%s): %w", adapter, path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("adapter %q (%s) is a directory, not an executable file", adapter, path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("adapter %q (%s) is not a regular file", adapter, path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("adapter %q (%s) is not executable", adapter, path)
	}
	return path, nil
}

// CheckAdapter resolves adapter against root (when relative) or tillr's
// installation/PATH, and verifies that the resolved path exists, is a regular
// file (not a directory), and has at least one executable bit set.
func CheckAdapter(root, adapter string) (string, error) {
	path, _, err := CheckAdapterWithOrigin(root, adapter)
	return path, err
}

// CheckAdapterWithOrigin behaves like CheckAdapter and also reports whether the
// adapter resolved from tillr's installation rather than root, an absolute path,
// or PATH.
func CheckAdapterWithOrigin(root, adapter string) (string, bool, error) {
	if adapter == "" {
		return "", false, fmt.Errorf("adapter must not be empty")
	}
	switch adapter {
	case "run-pi", "run-claude", "run-agy", "run-muse":
		kind := strings.TrimPrefix(adapter, "run-")
		modDir, err := resolveRunModuleDir("adapter " + adapter)
		if err != nil {
			return "", true, err
		}
		path := filepath.Join(modDir, "backends", kind, adapter)
		checked, err := checkExecutableFile(adapter, path)
		return checked, true, err
	}
	if filepath.IsAbs(adapter) {
		checked, err := checkExecutableFile(adapter, adapter)
		return checked, false, err
	}
	if strings.ContainsRune(adapter, filepath.Separator) {
		firstPath := adapter
		if root != "" {
			base := root
			if absRoot, err := filepath.Abs(root); err == nil {
				base = absRoot
			}
			firstPath = filepath.Join(base, adapter)
		} else if absAdapter, err := filepath.Abs(adapter); err == nil {
			firstPath = absAdapter
		}
		if _, err := os.Stat(firstPath); err == nil {
			checked, err := checkExecutableFile(adapter, firstPath)
			return checked, false, err
		}
		clean := filepath.ToSlash(filepath.Clean(adapter))
		if rest, ok := strings.CutPrefix(clean, relBackendsPrefix); ok && rest != "" {
			backendsDir, err := resolveBackendsDir()
			if err != nil {
				return "", true, fmt.Errorf("adapter %q is missing (tried %s; installed backends fallback failed: %w)", adapter, firstPath, err)
			}
			installPath := filepath.Join(backendsDir, filepath.FromSlash(rest))
			checked, err := checkExecutableFile(adapter, installPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return "", true, fmt.Errorf("adapter %q is missing (tried %s and %s): %w", adapter, firstPath, installPath, err)
				}
				return "", true, fmt.Errorf("adapter %q (tried %s, found %s): %w", adapter, firstPath, installPath, err)
			}
			return checked, true, nil
		}
		checked, err := checkExecutableFile(adapter, firstPath)
		return checked, false, err
	}
	resolved, err := exec.LookPath(adapter)
	if err != nil {
		return "", false, fmt.Errorf("adapter %q is missing from PATH: %w", adapter, err)
	}
	checked, err := checkExecutableFile(adapter, resolved)
	return checked, false, err
}

// ResolveBriefTemplate resolves the installed tillr-run brief template, or the
// source checkout's tools/tillr-run/backends/brief-template.md when invoked
// directly from tillr's source checkout.
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

func resolveWorkflowsDir() (string, error) {
	if root := os.Getenv("TILLR_INSTALL_ROOT"); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("TILLR_INSTALL_ROOT must be absolute: %s", root)
		}
		dir := filepath.Join(root, "workflows")
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("installed workflows directory %s is not a directory", dir)
			}
			return dir, nil
		}
		if errors.Is(err, os.ErrNotExist) && filepath.Base(root) == "tools" {
			checkout := filepath.Dir(root)
			mod, modErr := os.ReadFile(filepath.Join(checkout, "tools", "tillr-item", "go.mod"))
			if modErr == nil && strings.HasPrefix(string(mod), "module github.com/mschulkind-oss/tillr-item\n") {
				wfDir := filepath.Join(checkout, "config", "tillr", "workflows")
				if wfInfo, wfErr := os.Stat(wfDir); wfErr == nil && wfInfo.IsDir() {
					return wfDir, nil
				}
			}
		}
		return "", fmt.Errorf("installed workflows directory %s: %w", dir, err)
	}
	if checkout, ok := resolveCheckoutDir(
		filepath.Join("tools", "tillr-item", "go.mod"),
		"module github.com/mschulkind-oss/tillr-item\n",
	); ok {
		wfDir := filepath.Join(checkout, "config", "tillr", "workflows")
		if info, err := os.Stat(wfDir); err == nil && info.IsDir() {
			return wfDir, nil
		}
	}
	return "", fmt.Errorf("cannot locate tillr installation for workflows; set TILLR_INSTALL_ROOT")
}

// ResolveWorkflowFile resolves ref (<name>@v<N>) against root/config/tillr/workflows first,
// falling back to the tillr installation when no file exists in the operated repository.
// It returns the resolved path and whether it came from root.
func ResolveWorkflowFile(root, ref string) (string, bool, error) {
	if !workflowRefShape.MatchString(ref) {
		return "", false, fmt.Errorf("workflow-reference: %q", ref)
	}
	if root != "" {
		repoPath := filepath.Join(root, "config", "tillr", "workflows", ref+".json")
		if _, err := os.Stat(repoPath); err == nil {
			return repoPath, true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
	}
	dir, err := resolveWorkflowsDir()
	if err != nil {
		return "", false, err
	}
	installedPath := filepath.Join(dir, ref+".json")
	if _, err := os.Stat(installedPath); err != nil {
		return "", false, err
	}
	return installedPath, false, nil
}
