package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr/watch"
)

//go:embed rules/*.toml
var shippedRulesFS embed.FS

func inferRepoRoot(root, storePath string) string {
	if root != "" {
		return root
	}
	if storePath == "" {
		return ""
	}
	absStore, err := filepath.Abs(storePath)
	if err != nil {
		absStore = storePath
	}
	parent := filepath.Dir(absStore)
	if filepath.Base(parent) == ".tillr" {
		return filepath.Dir(parent)
	}
	return ""
}

func loadCLIRules(rulesPath, root, storePath string, noShipped bool) ([]watch.Rule, error) {
	var shipped fs.FS
	if !noShipped {
		shipped = shippedRulesFS
	}
	repoDir := rulesPath
	if repoDir != "" {
		info, err := os.Stat(repoDir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s: rules path must be a directory", repoDir)
		}
	} else {
		if inferred := inferRepoRoot(root, storePath); inferred != "" {
			candidate := filepath.Join(inferred, "config", "tillr", "watch")
			info, err := os.Stat(candidate)
			if err == nil {
				if !info.IsDir() {
					return nil, fmt.Errorf("%s: rules path must be a directory", candidate)
				}
				repoDir = candidate
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		if noShipped && repoDir == "" {
			return nil, fmt.Errorf("--no-shipped-rules requires --rules or an existing <root>/config/tillr/watch directory")
		}
	}
	return watch.LoadRuleLayers(shipped, repoDir)
}
