package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var workflowExactTriggers = map[string]bool{
	"definition.json":                  true,
	"tools/gate/checks/workflows.json": true,
	"tools/gate/workflows.go":          true,
	"tools/gate/change_common.go":      true,
	"tools/gate/main.go":               true,
	"tools/gate/repo-gate":             true,
}

var workflowPrefixTriggers = []string{
	"config/tillr/",
	"config/tillr/workflows/",
	"tools/tillr-drain/",
	"tools/tillr-watch/",
	"tools/tillr-watch/rules/",
	"tools/tillr/",
	"tools/tillr-item/",
	"tools/tillr-run/",
	"tools/tillr-land/",
}

func isWorkflowSelected(p string) bool {
	if workflowExactTriggers[p] {
		return true
	}
	for _, prefix := range workflowPrefixTriggers {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func workflowGoEnv() []string {
	var out []string
	goflags := ""
	for _, e := range os.Environ() {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if k == "CGO_ENABLED" {
			continue
		}
		if k == "GOFLAGS" {
			goflags = v
			continue
		}
		out = append(out, e)
	}
	out = append(out, "CGO_ENABLED=0")
	if goflags != "" {
		out = append(out, "GOFLAGS="+goflags+" -trimpath")
	} else {
		out = append(out, "GOFLAGS=-trimpath")
	}
	return out
}

func resolveToolModule(root, name string) string {
	candidate := filepath.Join(root, "tools", name)
	if info, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil && !info.IsDir() {
		return candidate
	}
	return filepath.Join("tools", name)
}

func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

func cmdWorkflows(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "workflows: supply base and head arguments")
		return 4
	}
	base, head := args[0], args[1]
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflows: %v\n", err)
		return 4
	}
	if err := validateBaseHead(root, base, head); err != nil {
		fmt.Fprintf(os.Stderr, "workflows: %v\n", err)
		return 4
	}
	changedOut, err := gitRun(root, true, "diff", "--name-only", "--no-renames", base, head)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflows: %v\n", err)
		return 4
	}
	selected := false
	for _, p := range splitLines(changedOut) {
		if isWorkflowSelected(p) {
			selected = true
			break
		}
	}
	if !selected {
		return 0
	}

	env := workflowGoEnv()
	drainModule := resolveToolModule(root, "tillr-drain")
	watchModule := resolveToolModule(root, "tillr-watch")

	workflowsDir := filepath.Join(root, "config", "tillr", "workflows")
	if info, err := os.Stat(workflowsDir); err == nil && info.IsDir() {
		cmd := exec.Command("go", "run", ".", "workflow", "check", workflowsDir)
		cmd.Dir = drainModule
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return 1
		}
	}

	configDir := filepath.Join(root, "config", "tillr")
	policies, _ := filepath.Glob(filepath.Join(configDir, "backend-policy*.json"))
	sort.Strings(policies)
	defInfo, defErr := os.Stat(filepath.Join(root, "definition.json"))
	watchInfo, watchErr := os.Stat(filepath.Join(configDir, "watch"))
	runRootLint := (defErr == nil && !defInfo.IsDir()) || len(policies) > 0 || (watchErr == nil && watchInfo.IsDir())

	recordedVerdict := filepath.Join(root, "tools", "tillr", "examples", "recorded-verdict")
	rvDef, rvDefErr := os.Stat(filepath.Join(recordedVerdict, "definition.json"))
	rvPrep, rvPrepErr := os.Stat(filepath.Join(recordedVerdict, "prepare.sh"))
	runExampleLint := (rvDefErr == nil && !rvDef.IsDir()) && (rvPrepErr == nil && !rvPrep.IsDir())

	if runRootLint || runExampleLint {
		scratchDir := os.Getenv("YOLO_DURABLE_DIR")
		if scratchDir != "" {
			if err := os.MkdirAll(scratchDir, 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "workflows: %v\n", err)
				return 4
			}
		}
		tmp, err := os.MkdirTemp(scratchDir, "workflows-gate-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "workflows: %v\n", err)
			return 4
		}
		defer os.RemoveAll(tmp)

		watchBin := filepath.Join(tmp, "tillr-watch")
		build := exec.Command("go", "build", "-o", watchBin, ".")
		build.Dir = watchModule
		build.Env = env
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			return 1
		}

		if runRootLint {
			lintArgs := []string{"lint", root}
			for _, pol := range policies {
				lintArgs = append(lintArgs, "--policy", pol)
			}
			lintRoot := exec.Command(watchBin, lintArgs...)
			lintRoot.Dir = root
			lintRoot.Env = env
			lintRoot.Stdout = os.Stdout
			lintRoot.Stderr = os.Stderr
			if err := lintRoot.Run(); err != nil {
				return 1
			}
		}

		if runExampleLint {
			exampleCopy := filepath.Join(tmp, "recorded-verdict")
			if err := copyDirRecursive(recordedVerdict, exampleCopy); err != nil {
				fmt.Fprintf(os.Stderr, "workflows: %v\n", err)
				return 4
			}
			prep := exec.Command("sh", "prepare.sh")
			prep.Dir = exampleCopy
			prep.Stdout = os.Stdout
			prep.Stderr = os.Stderr
			if err := prep.Run(); err != nil {
				return 1
			}
			lintExample := exec.Command(watchBin, "lint", exampleCopy)
			lintExample.Dir = exampleCopy
			lintExample.Env = env
			lintExample.Stdout = os.Stdout
			lintExample.Stderr = os.Stderr
			if err := lintExample.Run(); err != nil {
				return 1
			}
		}
	}

	return 0
}
