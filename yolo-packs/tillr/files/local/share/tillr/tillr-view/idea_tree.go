package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// committedIdeaWords reads file bytes from this root's current commit, not
// the working copy.
// A folder inside some other checkout, or a new repository without a commit,
// holds no committed words. Tool/permission failures are not absence.
func committedIdeaWords(root, path string) ([]byte, bool, error) {
	run := func(args ...string) ([]byte, string, error) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		out, err := cmd.Output()
		return out, diagnostic.String(), err
	}
	top, diagnostic, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(diagnostic, "not a git repository") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cannot inspect committed idea words: %v; %s", err, diagnostic)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, false, err
	}
	if canonical != strings.TrimSpace(string(top)) {
		return nil, false, nil
	}
	if _, diagnostic, err = run("rev-parse", "--verify", "--quiet", "HEAD^{tree}"); err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cannot inspect committed idea tree: %v; %s", err, diagnostic)
	}
	entry, diagnostic, err := run("ls-tree", "-z", "--full-tree", "HEAD", "--", ":(literal)"+path)
	if err != nil {
		return nil, false, fmt.Errorf("cannot locate committed idea words: %v; %s", err, diagnostic)
	}
	if len(entry) == 0 {
		return nil, false, nil
	}
	parts := strings.SplitN(string(entry), "\t", 2)
	fields := strings.Fields(parts[0])
	if len(parts) != 2 || len(fields) != 3 || fields[1] != "blob" || fields[0] != "100644" && fields[0] != "100755" {
		return nil, false, fmt.Errorf("committed idea words path %s is not a regular file", path)
	}
	raw, diagnostic, err := run("cat-file", "blob", fields[2])
	if err != nil {
		return nil, false, fmt.Errorf("cannot read committed idea words: %v; %s", err, diagnostic)
	}
	return raw, true, nil
}
