package goaffected

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// CheckDrainTestParity verifies that deleting or renaming *_test.go files under
// tools/tillr-drain/ does not drop any top-level Test* function compared to base.
func CheckDrainTestParity(root, base, head string, changedPaths []string) error {
	if base == "" {
		return nil
	}
	if !hasDeletedOrMovedDrainTest(root, base, head, changedPaths) {
		return nil
	}

	baseCounts, err := countDrainTestsAtRev(root, base)
	if err != nil {
		return fmt.Errorf("scanning base %s drain tests: %w", base, err)
	}
	headCounts, err := countDrainTestsAtHead(root, head)
	if err != nil {
		return fmt.Errorf("scanning head drain tests: %w", err)
	}

	var dropped []string
	for name, baseN := range baseCounts {
		if headCounts[name] < baseN {
			dropped = append(dropped, fmt.Sprintf("%s (base=%d, head=%d)", name, baseN, headCounts[name]))
		}
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		return fmt.Errorf("drain test parity failed: dropped or renamed Test* functions: %s", strings.Join(dropped, ", "))
	}
	return nil
}

func hasDeletedOrMovedDrainTest(root, base, head string, changedPaths []string) bool {
	for _, p := range changedPaths {
		if !strings.HasPrefix(p, "tools/tillr-drain/") || !strings.HasSuffix(p, "_test.go") {
			continue
		}
		if head == "" || head == "HEAD" {
			if !isFile(filepath.Join(root, filepath.FromSlash(p))) {
				return true
			}
		}
	}
	if head == "" {
		return false
	}
	env := BuildEnv()
	cmd := exec.Command("git", "-C", root, "diff", "--name-status", "--no-renames", "-z", base, head, "--", "tools/tillr-drain")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	fields := bytes.Split(out, []byte{0})
	for i := 0; i+1 < len(fields); i += 2 {
		status := string(fields[i])
		path := string(fields[i+1])
		if strings.HasPrefix(status, "D") && strings.HasSuffix(path, "_test.go") {
			return true
		}
	}
	return false
}

func countDrainTestsAtRev(root, rev string) (map[string]int, error) {
	env := BuildEnv()
	lsCmd := exec.Command("git", "-C", root, "ls-tree", "-r", "--name-only", "-z", rev, "--", "tools/tillr-drain")
	lsCmd.Env = env
	out, err := lsCmd.Output()
	if err != nil {
		return nil, err
	}
	var specs []string
	for _, entry := range bytes.Split(out, []byte{0}) {
		p := string(entry)
		if strings.HasSuffix(p, "_test.go") {
			specs = append(specs, rev+":"+p)
		}
	}
	counts := make(map[string]int)
	if len(specs) == 0 {
		return counts, nil
	}

	catCmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	catCmd.Env = env
	catCmd.Stdin = strings.NewReader(strings.Join(specs, "\n") + "\n")
	var stdout bytes.Buffer
	catCmd.Stdout = &stdout
	if err := catCmd.Run(); err != nil {
		return nil, err
	}

	r := bufio.NewReader(&stdout)
	fset := token.NewFileSet()
	for _, spec := range specs {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading cat-file header for %s: %w", spec, err)
		}
		header = strings.TrimSuffix(header, "\n")
		parts := strings.Split(header, " ")
		if len(parts) < 3 {
			return nil, fmt.Errorf("unexpected cat-file header for %s: %q", spec, header)
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("invalid cat-file size for %s: %w", spec, err)
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("reading cat-file body for %s: %w", spec, err)
		}
		// Consume trailing newline after object body.
		if _, err := r.ReadByte(); err != nil {
			return nil, err
		}
		addTestDeclsFromSource(fset, spec, buf, counts)
	}
	return counts, nil
}

func countDrainTestsAtHead(root, head string) (map[string]int, error) {
	if head != "" && head != "HEAD" {
		return countDrainTestsAtRev(root, head)
	}
	drainRoot := filepath.Join(root, "tools", "tillr-drain")
	counts := make(map[string]int)
	fset := token.NewFileSet()
	err := filepath.WalkDir(drainRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		addTestDeclsFromSource(fset, path, raw, counts)
		return nil
	})
	return counts, err
}

func addTestDeclsFromSource(fset *token.FileSet, filename string, src []byte, counts map[string]int) {
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		if !isExecutableTestDecl(name) {
			continue
		}
		counts[name]++
	}
}

func isExecutableTestDecl(name string) bool {
	if name == "TestMain" || name == "TestEdgeInputs" {
		return false
	}
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	if len(name) > 4 && unicode.IsLower(rune(name[4])) {
		return false
	}
	return true
}
