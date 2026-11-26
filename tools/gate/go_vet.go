package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func discoverGoModules(root string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(root, "tools", "*", "go.mod"))
	if err != nil {
		return nil, err
	}
	var modules []string
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		rel, err := filepath.Rel(root, filepath.Dir(m))
		if err != nil {
			return nil, err
		}
		modules = append(modules, filepath.ToSlash(rel))
	}
	sort.Strings(modules)
	return modules, nil
}

func goModuleReplaceDependents(root string, modules []string) map[string]map[string]bool {
	direct := make(map[string]map[string]bool, len(modules))
	modSet := make(map[string]bool, len(modules))
	for _, m := range modules {
		direct[m] = make(map[string]bool)
		modSet[m] = true
	}
	for _, mod := range modules {
		gomod := filepath.Join(root, filepath.FromSlash(mod), "go.mod")
		raw, err := os.ReadFile(gomod)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "replace ") {
				line = strings.TrimPrefix(line, "replace ")
			}
			arrow := strings.Index(line, "=>")
			if arrow < 0 {
				continue
			}
			targetFields := strings.Fields(line[arrow+2:])
			if len(targetFields) == 0 {
				continue
			}
			t0 := targetFields[0]
			if !strings.HasPrefix(t0, "./") && !strings.HasPrefix(t0, "../") {
				continue
			}
			rel := path.Clean(path.Join(mod, t0))
			if modSet[rel] && rel != mod {
				direct[rel][mod] = true
			}
		}
	}
	closure := make(map[string]map[string]bool, len(modules))
	for _, target := range modules {
		seen := make(map[string]bool)
		var stack []string
		for cur := range direct[target] {
			stack = append(stack, cur)
		}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			for next := range direct[cur] {
				stack = append(stack, next)
			}
		}
		closure[target] = seen
	}
	return closure
}

func reachedGoModules(root string, modules []string, changedPaths []string) []string {
	dependents := goModuleReplaceDependents(root, modules)
	reached := make(map[string]bool)
	for _, rawPath := range changedPaths {
		p := path.Clean(filepath.ToSlash(rawPath))
		if p == "." || p == "" {
			continue
		}
		for _, mod := range modules {
			if p == mod || strings.HasPrefix(p, mod+"/") {
				reached[mod] = true
				for dep := range dependents[mod] {
					reached[dep] = true
				}
			}
		}
	}
	var out []string
	for _, mod := range modules {
		if reached[mod] {
			out = append(out, mod)
		}
	}
	return out
}

func gitChangedPathsNull(root, base, head string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "diff", "--name-only", "--no-renames", "-z", base, head, "--")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "git diff failed"
		}
		return nil, errors.New(msg)
	}
	var paths []string
	for _, part := range bytes.Split(stdout.Bytes(), []byte{0}) {
		if len(part) > 0 {
			paths = append(paths, string(part))
		}
	}
	return paths, nil
}

func runGoVet(root, base, head string, stdout, stderr io.Writer) int {
	if err := validateBaseHead(root, base, head); err != nil {
		fmt.Fprintf(stderr, "go-vet: %v\n", err)
		return 4
	}
	modules, err := discoverGoModules(root)
	if err != nil {
		fmt.Fprintf(stderr, "go-vet: %v\n", err)
		return 4
	}
	if len(modules) == 0 {
		fmt.Fprintln(stdout, "go-vet: no Go modules found")
		return 0
	}
	changed, err := gitChangedPathsNull(root, base, head)
	if err != nil {
		fmt.Fprintf(stderr, "go-vet: %v\n", err)
		return 4
	}
	targets := reachedGoModules(root, modules, changed)
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "go-vet: no affected Go modules")
		return 0
	}

	failed := 0
	for _, mod := range targets {
		fmt.Fprintf(stdout, "go-vet: vetting %s\n", mod)
		cmd := exec.Command("go", "vet", "./...")
		cmd.Dir = filepath.Join(root, filepath.FromSlash(mod))
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				fmt.Fprintf(stdout, "go-vet: FAIL %s (exit %d)\n", mod, ee.ExitCode())
				failed++
				continue
			}
			fmt.Fprintf(stderr, "go-vet: could not judge %s: %v\n", mod, err)
			return 4
		}
	}
	if failed > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "go-vet: ok (%d modules)\n", len(targets))
	return 0
}

func cmdGoVet(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "go-vet: supply base and head arguments")
		return 4
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-vet: %v\n", err)
		return 4
	}
	return runGoVet(root, args[0], args[1], os.Stdout, os.Stderr)
}
