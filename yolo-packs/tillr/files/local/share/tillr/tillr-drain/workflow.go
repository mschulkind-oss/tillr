package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func workflowFace(args []string) int {
	help := func() {
		fmt.Printf("Usage: tillr drain workflow check <file-or-directory>\nReports executable nodes, depth (longest dependency path in nodes), and fan-out (most direct successors).\nRules: %s\n", drain.WorkflowRules)
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		help()
		return 0
	}
	if len(args) != 2 || args[0] != "check" {
		help()
		return 2
	}
	if args[1] == "--help" || args[1] == "-h" {
		help()
		return 0
	}
	target, err := filepath.Abs(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// Find the target's root, not the process cwd (the gate runs the binary
	// from its module). Non-Git roots resolve by walking up to the nearest
	// command registry or tillr layout before falling back to Git or the file's directory.
	directory := filepath.Dir(target)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		directory = target
	}
	root := workflowRoot(directory)
	if drain.CheckWorkflows(root, target, os.Stdout) {
		return 0
	}
	return 1
}

func workflowRoot(directory string) string {
	for dir := directory; ; dir = filepath.Dir(dir) {
		if info, err := os.Stat(filepath.Join(dir, "config", "tillr", "commands.json")); err == nil && !info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	if raw, err := exec.Command("git", "-C", directory, "rev-parse", "--show-toplevel").Output(); err == nil {
		return strings.TrimSpace(string(raw))
	}
	for dir := directory; ; dir = filepath.Dir(dir) {
		for _, marker := range []string{filepath.Join("config", "tillr", "workflows"), ".tillr"} {
			if info, err := os.Stat(filepath.Join(dir, marker)); err == nil && info.IsDir() {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return directory
}
