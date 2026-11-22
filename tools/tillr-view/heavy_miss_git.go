package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
)

func fullHeavySHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// heavyMissPaths reads only the two recorded commits, never the worktree's modules.
func heavyMissPaths(ctx context.Context, root, before, after string) (paths, modules []string, err error) {
	for _, sha := range []string{before, after} {
		if !fullHeavySHA(sha) {
			return nil, nil, fmt.Errorf("heavy miss: invalid full commit SHA %q", sha)
		}
	}
	objects, err := heavyMissGit(ctx, root, before+"\n"+after+"\n", "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, nil, err
	}
	if string(objects) != before+" commit\n"+after+" commit\n" {
		return nil, nil, fmt.Errorf("heavy miss: endpoints must be existing exact full commit SHAs: %s", strings.TrimSpace(string(objects)))
	}
	changed, err := heavyMissGit(ctx, root, "", "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-relative", "--no-color", "--ignore-submodules=none", before, after, "--")
	if err != nil {
		return nil, nil, err
	}
	paths, err = heavyMissNames(changed)
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	modules = make([]string, 0)
	if len(paths) == 0 {
		return paths, modules, nil
	}

	// Two tree inventories avoid spawning a Git process for each changed path or ancestor.
	moduleDirs := make(map[string]bool)
	for _, sha := range []string{before, after} {
		tree, err := heavyMissGit(ctx, root, "", "ls-tree", "-r", "--name-only", "-z", "--full-tree", sha)
		if err != nil {
			return nil, nil, err
		}
		names, err := heavyMissNames(tree)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range names {
			name = heavyMissSourcePath(name)
			if path.Base(name) == "go.mod" {
				moduleDirs[path.Dir(name)] = true
			}
		}
	}
	seen := make(map[string]bool)
	for _, name := range paths {
		module := "(non-module)"
		for dir := path.Dir(heavyMissSourcePath(name)); ; dir = path.Dir(dir) {
			if moduleDirs[dir] {
				module = dir
				break
			}
			if dir == "." {
				break
			}
		}
		seen[module] = true
	}
	for module := range seen {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	return paths, modules, nil
}

func heavyMissSourcePath(name string) string {
	const generated = "yolo-packs/tillr/files/local/share/tillr/"
	if strings.HasPrefix(name, generated) {
		return "tools/" + strings.TrimPrefix(name, generated)
	}
	return name
}

func heavyMissNames(data []byte) ([]string, error) {
	if len(data) == 0 {
		return make([]string, 0), nil
	}
	if data[len(data)-1] != 0 {
		return nil, fmt.Errorf("heavy miss: Git returned a path list without a terminating NUL")
	}
	return strings.Split(string(data[:len(data)-1]), "\x00"), nil
}

func heavyMissGit(ctx context.Context, root, input string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	// Do not let replacement objects or a partial clone turn this report into a
	// different history or a network operation. Optional locks are unnecessary for reads.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_NO_REPLACE_OBJECTS", "GIT_NO_LAZY_FETCH", "GIT_OPTIONAL_LOCKS":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("heavy miss: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
