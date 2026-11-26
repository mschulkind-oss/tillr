// Package packmirror renders the tillr pack mirror and rewrites README links that escape it.
package packmirror

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	Repository     = "https://github.com/mschulkind-oss/tillr/blob/main/"
	MirrorRoot     = "yolo-packs/tillr/files/local/share/tillr"
	LoopModule     = "tillr-loop"
	WorkflowsDir   = "config/tillr/workflows"
	WorkflowsMount = "workflows"
)

var (
	// LoopAdapter is the relative path to the standalone loop adapter executable.
	LoopAdapter = filepath.Join(LoopModule, LoopModule)

	// Shipped lists all shipped modules, including shared libraries and source-only modules.
	Shipped = []string{
		"tillr", "tillr-cli", "tillr-drain", "tillr-gate", "tillr-item", "tillr-land",
		"tillr-run", "tillr-store", "tillr-view", "tillr-watch", "tillr-trace", "tillr-roll",
	}

	linkRe     = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	absoluteRe = regexp.MustCompile(`^(?:[a-z][a-z0-9+.-]*:|/|#)`)
)

const usageText = `Render the tillr pack mirror and rewrite README links that escape it.

Usage: pack-mirror.py --sync <repo-root> [<module> ...]
       pack-mirror-links.py <module-root>
`

// Rewrite rewrites relative links in one README's text that escape the module root.
func Rewrite(text, directory string) string {
	dirSlash := filepath.ToSlash(directory)
	return linkRe.ReplaceAllStringFunc(text, func(match string) string {
		// match is "](target)"
		target := match[2 : len(match)-1]
		if absoluteRe.MatchString(target) {
			return match
		}
		p, anchor, hasHash := strings.Cut(target, "#")
		resolved := pathpkg.Clean(pathpkg.Join(dirSlash, p))
		if !strings.HasPrefix(resolved, "..") {
			return match
		}
		for strings.HasPrefix(resolved, "../") {
			resolved = resolved[3:]
		}
		suffix := ""
		if hasHash && anchor != "" {
			suffix = "#" + anchor
		}
		return "](" + Repository + resolved + suffix + ")"
	})
}

// RewriteReadmes walks root and rewrites escaping links in every README.md.
func RewriteReadmes(root string, stdout io.Writer) error {
	rewritten := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != "README.md" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		before := string(raw)
		relDir, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		after := Rewrite(before, relDir)
		if after != before {
			if err := os.WriteFile(p, []byte(after), 0o644); err != nil {
				return err
			}
			rewritten++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "pack-mirror-links: rewrote %d README(s) under %s\n", rewritten, root)
	}
	return nil
}

// CheckDestination refuses any symlinked ancestor between dest and root.
func CheckDestination(root, dest string) error {
	for parent := filepath.Dir(dest); parent != root; parent = filepath.Dir(parent) {
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		fi, err := os.Lstat(parent)
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlinked destination ancestor: %s", parent)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	return nil
}

// MirrorLoopAdapter mirrors the tools/tillr-loop module (except README.md, owned by pack-docs) into the pack.
func MirrorLoopAdapter(root string, stdout io.Writer) error {
	sourceRel := "tools/" + LoopModule
	dest := filepath.Join(root, filepath.FromSlash(MirrorRoot), LoopModule)
	if err := CheckDestination(root, dest); err != nil {
		return err
	}

	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", sourceRel)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	type liveEntry struct {
		rel string
		src string
	}
	readmePath := sourceRel + "/README.md"
	var live []liveEntry
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p := string(raw)
		if p == readmePath {
			continue
		}
		src := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(src); err == nil {
			live = append(live, liveEntry{rel: p, src: src})
		}
	}

	if fi, err := os.Lstat(dest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			if err := os.Remove(dest); err != nil {
				return err
			}
		} else {
			entries, err := os.ReadDir(dest)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.Name() == "README.md" {
					continue
				}
				entryPath := filepath.Join(dest, entry.Name())
				efi, err := os.Lstat(entryPath)
				if err != nil {
					return err
				}
				if efi.Mode()&os.ModeSymlink != 0 || !efi.IsDir() {
					if err := os.Remove(entryPath); err != nil {
						return err
					}
				} else {
					if err := os.RemoveAll(entryPath); err != nil {
						return err
					}
				}
			}
		}
	}

	if len(live) == 0 {
		return nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	prefix := sourceRel + "/"
	for _, entry := range live {
		relFromSource := strings.TrimPrefix(entry.rel, prefix)
		dst := filepath.Join(dest, filepath.FromSlash(relFromSource))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(entry.src, dst); err != nil {
			return err
		}
	}
	if stdout != nil {
		relDst, _ := filepath.Rel(root, dest)
		fmt.Fprintf(stdout, "pack-sync: mirrored %s -> %s\n", sourceRel, filepath.ToSlash(relDst))
	}
	return nil
}

// MirrorModule mirrors one tools/<module> directory into the pack.
func MirrorModule(root, module string, stdout io.Writer) error {
	sourceRel := "tools/" + module
	dest := filepath.Join(root, filepath.FromSlash(MirrorRoot), module)
	if err := CheckDestination(root, dest); err != nil {
		return err
	}

	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", sourceRel)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	type liveEntry struct {
		rel string
		src string
	}
	var live []liveEntry
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p := string(raw)
		src := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(src); err == nil {
			live = append(live, liveEntry{rel: p, src: src})
		}
	}

	if fi, err := os.Lstat(dest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			if err := os.Remove(dest); err != nil {
				return err
			}
		} else {
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
		}
	}

	if len(live) == 0 {
		return nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	prefix := sourceRel + "/"
	for _, entry := range live {
		relFromSource := strings.TrimPrefix(entry.rel, prefix)
		dst := filepath.Join(dest, filepath.FromSlash(relFromSource))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(entry.src, dst); err != nil {
			return err
		}
	}
	if err := RewriteReadmes(dest, stdout); err != nil {
		return err
	}
	if stdout != nil {
		relDst, _ := filepath.Rel(root, dest)
		fmt.Fprintf(stdout, "pack-sync: mirrored %s -> %s\n", sourceRel, filepath.ToSlash(relDst))
	}
	return nil
}

// MirrorWorkflows mirrors config/tillr/workflows into the pack's workflows directory.
func MirrorWorkflows(root string, stdout io.Writer) error {
	dest := filepath.Join(root, filepath.FromSlash(MirrorRoot), WorkflowsMount)
	if err := CheckDestination(root, dest); err != nil {
		return err
	}

	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", WorkflowsDir)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	type liveEntry struct {
		rel string
		src string
	}
	var live []liveEntry
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p := string(raw)
		src := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(src); err == nil {
			live = append(live, liveEntry{rel: p, src: src})
		}
	}

	if fi, err := os.Lstat(dest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			if err := os.Remove(dest); err != nil {
				return err
			}
		} else {
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
		}
	}

	if len(live) == 0 {
		return nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	prefix := WorkflowsDir + "/"
	for _, entry := range live {
		relFromSource := strings.TrimPrefix(entry.rel, prefix)
		dst := filepath.Join(dest, filepath.FromSlash(relFromSource))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(entry.src, dst); err != nil {
			return err
		}
	}
	if stdout != nil {
		relDst, _ := filepath.Rel(root, dest)
		fmt.Fprintf(stdout, "pack-sync: mirrored %s -> %s\n", WorkflowsDir, filepath.ToSlash(relDst))
	}
	return nil
}

func isShipped(module string) bool {
	if module == LoopModule || module == WorkflowsMount {
		return true
	}
	for _, m := range Shipped {
		if m == module {
			return true
		}
	}
	return false
}

// Sync mirrors the requested modules (or all shipped modules if none specified) under root.
func Sync(root string, modules []string, stdout, stderr io.Writer) int {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "pack-sync: %v\n", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}

	if len(modules) == 0 {
		modules = append(append([]string(nil), Shipped...), LoopModule, WorkflowsMount)
	}
	for _, m := range modules {
		if !isShipped(m) {
			fmt.Fprintf(stderr, "pack-sync: unknown module: %s\n", m)
			return 2
		}
	}
	for _, m := range modules {
		var err error
		switch m {
		case LoopModule:
			err = MirrorLoopAdapter(absRoot, stdout)
		case WorkflowsMount:
			err = MirrorWorkflows(absRoot, stdout)
		default:
			err = MirrorModule(absRoot, m, stdout)
		}
		if err != nil {
			fmt.Fprintf(stderr, "pack-sync: %v\n", err)
			return 1
		}
	}
	return 0
}

// Main is the CLI entrypoint for pack-mirror and pack-mirror-links.
// args should be os.Args[1:].
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) >= 2 && args[0] == "--sync" {
		return Sync(args[1], args[2:], stdout, stderr)
	}
	if len(args) != 1 || args[0] == "--sync" {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	if err := RewriteReadmes(args[0], stdout); err != nil {
		fmt.Fprintf(stderr, "pack-mirror-links: %v\n", err)
		return 1
	}
	return 0
}
