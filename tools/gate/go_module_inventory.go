package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var goModInventorySelfTriggers = []string{
	"tools/gate/go_module_inventory.go",
	"tools/gate/checks/go-module-inventory.json",
	"tests/tillr-testmain_test.go",
	"tests/tillr_scratch_test.go",
}

func validateInventoryBaseHead(base, head string) error {
	for _, pair := range [][2]string{{"base", base}, {"head", head}} {
		label, rev := pair[0], pair[1]
		if rev == "" {
			return fmt.Errorf("missing %s argument", label)
		}
		cmd := exec.Command("git", "rev-parse", "--verify", rev+"^{commit}")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = fmt.Sprintf("invalid %s revision: %s", label, rev)
			}
			return errors.New(msg)
		}
	}
	if err := exec.Command("git", "merge-base", "--is-ancestor", base, head).Run(); err != nil {
		return errors.New("base is not an ancestor of head")
	}
	return nil
}

func addedOrRemovedGoMods(root, base, head string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "diff", "--name-status", "--no-renames", "-z", base, head, "--", "tools")
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
	var tokens []string
	for _, part := range bytes.Split(stdout.Bytes(), []byte{0}) {
		if len(part) > 0 {
			tokens = append(tokens, string(part))
		}
	}
	var matched []string
	for i := 0; i+1 < len(tokens); i += 2 {
		status, path := tokens[i], tokens[i+1]
		if (status == "A" || status == "D") && strings.HasPrefix(path, "tools/") && strings.HasSuffix(path, "/go.mod") {
			matched = append(matched, path)
		}
	}
	return matched, nil
}

func selfTriggersChanged(root, base, head string) ([]string, error) {
	args := []string{"-C", root, "diff", "--name-only", "--no-renames", "-z", base, head, "--"}
	args = append(args, goModInventorySelfTriggers...)
	cmd := exec.Command("git", args...)
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
	var changed []string
	for _, part := range bytes.Split(stdout.Bytes(), []byte{0}) {
		if len(part) > 0 {
			changed = append(changed, string(part))
		}
	}
	return changed, nil
}

func runInventoryStep(name string, cmdArgs []string, cwd string, env []string) int {
	fmt.Printf("go-module-inventory: running %s\n", name)
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		fmt.Printf("go-module-inventory: ok %s\n", name)
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code == 4 {
			fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge %s (exit 4)\n", name)
			return 4
		}
		fmt.Printf("go-module-inventory: FAIL %s (exit %d)\n", name, code)
		return 1
	}
	fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge %s: %v\n", name, err)
	return 4
}

func runInventoryChecks(root string) int {
	scratchTest := filepath.Join(root, "tests", "tillr_scratch_test.go")
	treeTest := filepath.Join(root, "tools", "gate", "tree_checks_test.go")
	tillrGateDir := filepath.Join(root, "tools", "tillr-gate")
	goaffectedDir := filepath.Join(tillrGateDir, "goaffected")
	for _, req := range []string{scratchTest, treeTest} {
		if info, err := os.Stat(req); err != nil || info.IsDir() {
			rel, _ := filepath.Rel(root, req)
			fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge: missing %s\n", filepath.ToSlash(rel))
			return 4
		}
	}
	if info, err := os.Stat(goaffectedDir); err != nil || !info.IsDir() {
		fmt.Fprintln(os.Stderr, "go-module-inventory: could not judge: missing tools/tillr-gate/goaffected")
		return 4
	}

	fallbackScratch, err := os.MkdirTemp("", "go-module-inventory-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge: %v\n", err)
		return 4
	}
	defer os.RemoveAll(fallbackScratch)

	durable := os.Getenv("YOLO_DURABLE_DIR")
	if durable != "" {
		if err := os.MkdirAll(durable, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge: %v\n", err)
			return 4
		}
	} else {
		durable = fallbackScratch
	}
	env := append(os.Environ(), "YOLO_DURABLE_DIR="+durable)

	repoGateBin, err := os.Executable()
	if err != nil || repoGateBin == "" {
		repoGateBin = filepath.Join(root, "tools", "gate", "repo-gate")
	}

	steps := []struct {
		name string
		cmd  []string
		cwd  string
	}{
		{
			name: "testmain-cleanup",
			cmd:  []string{"go", "test", "-count=1", "tests/tillr_scratch_test.go"},
			cwd:  root,
		},
		{
			name: "declared-suites",
			cmd: []string{
				"go", "test", "-count=1", "-run",
				"^(TestTreeEveryGoModuleIsBuiltAndHasDeclaredSuite|TestTreeDeclarationReadingGoModuleReadsInRepoDependencies)$",
				".",
			},
			cwd: filepath.Join(root, "tools", "gate"),
		},
		{
			name: "go-edges",
			cmd:  []string{repoGateBin, "go-edges"},
			cwd:  root,
		},
		{
			name: "go-affected-modules",
			cmd:  []string{"go", "test", "-C", tillrGateDir, "-count=1", "./goaffected", "-run", "^TestDiscoverModules$"},
			cwd:  root,
		},
	}

	worst := 0
	for _, step := range steps {
		code := runInventoryStep(step.name, step.cmd, step.cwd, env)
		if code == 4 {
			return 4
		}
		if code != 0 {
			worst = 1
		}
	}
	return worst
}

func cmdGoModuleInventory(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "go-module-inventory: supply base and head arguments")
		return 4
	}
	base, head := args[0], args[1]
	if err := validateInventoryBaseHead(base, head); err != nil {
		fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge: %v\n", err)
		return 4
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge: %v\n", err)
		return 4
	}
	changedMods, err := addedOrRemovedGoMods(root, base, head)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge: %v\n", err)
		return 4
	}
	changedSelf, err := selfTriggersChanged(root, base, head)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-module-inventory: could not judge: %v\n", err)
		return 4
	}
	if len(changedMods) == 0 && len(changedSelf) == 0 {
		return 0
	}
	if len(changedMods) > 0 {
		fmt.Printf("go-module-inventory: triggered by added/removed go.mod: %s\n", strings.Join(changedMods, ", "))
	} else {
		fmt.Printf("go-module-inventory: triggered by inventory check edit: %s\n", strings.Join(changedSelf, ", "))
	}
	return runInventoryChecks(root)
}
