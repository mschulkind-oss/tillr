package item

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var workflowRefShape = regexp.MustCompile(`^[a-z][a-z0-9-]*@v[1-9][0-9]*$`)

// WorkflowRefValid reports whether a reference names a versioned workflow file.
func WorkflowRefValid(ref string) bool { return workflowRefShape.MatchString(ref) }

// ResolveWorkflowsDir resolves the installed workflows directory ($TILLR_INSTALL_ROOT/workflows),
// or the source checkout's config/tillr/workflows when invoked directly from tillr's source
// checkout. It never falls back to an operated repository's config/tillr/workflows.
func ResolveWorkflowsDir() (string, error) {
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
		module, err := os.ReadFile(filepath.Join(checkout, "tools", "tillr-item", "go.mod"))
		if err == nil && strings.HasPrefix(string(module), "module github.com/mschulkind-oss/tillr-item\n") {
			wfDir := filepath.Join(checkout, "config", "tillr", "workflows")
			if info, err := os.Stat(wfDir); err == nil && info.IsDir() {
				return wfDir, nil
			}
		}
	}
	return "", fmt.Errorf("cannot locate tillr installation for workflows; set TILLR_INSTALL_ROOT")
}

// ResolveWorkflowFile resolves ref (<name>@v<N>) against root/config/tillr/workflows first,
// falling back to the tillr installation when no file exists in the operated repository.
// It returns the resolved path and whether it came from root.
func ResolveWorkflowFile(root, ref string) (string, bool, error) {
	if !WorkflowRefValid(ref) {
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
	dir, err := ResolveWorkflowsDir()
	if err != nil {
		return "", false, err
	}
	installedPath := filepath.Join(dir, ref+".json")
	if _, err := os.Stat(installedPath); err != nil {
		return "", false, err
	}
	return installedPath, false, nil
}
