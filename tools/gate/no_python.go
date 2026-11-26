package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Temporary bootstrap exemption: trunk's tillr-loop invokes python3 on
// tools/tillr-gate/change-gate.py (which imports classify.py) from the candidate's own
// tree until follow-up item-request-629db3e857 deletes these files and this exemption
// after the loop rolls onto the Go adapter.
var noPythonBootstrapExemption = map[string]bool{
	"tools/tillr-gate/change-gate.py":                                    true,
	"tools/tillr-gate/tree-check.py":                                     true,
	"tools/tillr-gate/classify.py":                                       true,
	"yolo-packs/tillr/files/local/share/tillr/tillr-gate/change-gate.py": true,
	"yolo-packs/tillr/files/local/share/tillr/tillr-gate/tree-check.py":  true,
	"yolo-packs/tillr/files/local/share/tillr/tillr-gate/classify.py":    true,
}

var pythonShebangRe = regexp.MustCompile(`(?m)^[ \t]*#![^\n]*python`)

func isExtensionless(path string) bool {
	base := filepath.Base(filepath.FromSlash(path))
	trimmed := strings.TrimPrefix(base, ".")
	return !strings.Contains(trimmed, ".")
}

func noPythonFindings(root string) ([]string, error) {
	pyOut, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.py").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files '*.py': %w", err)
	}
	var findings []string
	for _, raw := range bytes.Split(pyOut, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p := string(raw)
		if noPythonBootstrapExemption[p] {
			continue
		}
		findings = append(findings, fmt.Sprintf("tracked Python file: %s", p))
	}

	allOut, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	for _, raw := range bytes.Split(allOut, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p := string(raw)
		if !isExtensionless(p) {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(p))
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		if pythonShebangRe.Match(data) {
			findings = append(findings, fmt.Sprintf("extensionless Python shebang: %s", p))
		}
	}
	sort.Strings(findings)
	return findings, nil
}

func cmdNoPython(args []string) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "no-python: could not judge: %v\n", err)
		return 4
	}
	findings, err := noPythonFindings(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no-python: could not judge: %v\n", err)
		return 4
	}
	for _, f := range findings {
		fmt.Printf("no-python: %s\n", f)
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}
