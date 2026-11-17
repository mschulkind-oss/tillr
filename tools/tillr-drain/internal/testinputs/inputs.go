// Package testinputs declares the sources used by the tests' child Go builds to
// Go's result cache. Child processes do not contribute file reads to the test log.
package testinputs

import (
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed siblings.txt
var siblingText string

// Siblings is the single list of sibling programs built by the drain tests.
func Siblings() []string { return strings.Fields(siblingText) }

// Track opens the sibling sources through a stable, module-local alias. Go ignores
// logged opens outside the test's module, but accepts paths lexically inside it.
// The alias must persist after the process exits: Go reads the log after TestMain.
// moduleDir is relative to the test package's working directory.
func Track(moduleDir string) error {
	alias := filepath.Join(moduleDir, ".sibling-inputs")
	if _, err := os.Readlink(alias); os.IsNotExist(err) {
		// The root and drain test binaries may start together in a fresh tree.
		if err := os.Symlink("..", alias); err != nil && !os.IsExist(err) {
			return fmt.Errorf("create sibling input alias: %w", err)
		}
	}
	if target, err := os.Readlink(alias); err != nil || target != ".." {
		return fmt.Errorf("sibling input alias %s must be a symlink to .. (got %q, %v)", alias, target, err)
	}
	for _, sibling := range Siblings() {
		root := filepath.Join(alias, sibling)
		// WalkDir starts with Lstat, which does not log a test input. ReadDir inside
		// the walk does: additions and removals invalidate the old directory listing.
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			name := entry.Name()
			if (!strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go")) && name != "go.mod" && name != "go.sum" {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			return file.Close()
		}); err != nil {
			return fmt.Errorf("declare %s build inputs: %w", sibling, err)
		}
	}
	return nil
}
