package land

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// CheckBuild answers why a squashed tree does not build, or nil when every Go module the
// candidate's change touches builds and vets (test packages included) in that tree.
//
// Two changes that each pass their gate can merge cleanly as text and still not compile
// together: one deletes the last use of an import the other starts using. The gate saw
// each on its own base, so a squash whose tree differs from the gated tree is the one
// moment nothing has built the tree that is about to become the tip (landing principle 3).
//
// Cost is bounded by the candidate: the modules are the ones base..head touches, plus the
// sibling modules their go.mod files replace, so it does not grow with the number of
// landings since the gate ran. The tree is extracted with `git archive`, so no ref, index
// or working tree of the repository is touched.
//
// The returned strings are conflict reports, one per failing module: they name the module
// and carry the compiler's first lines, so the author can read what to fix.
func (t *Trunk) CheckBuild(base, head, tree string) ([]string, error) {
	changed, err := t.git("diff", "--name-only", "-z", base, head)
	if err != nil {
		return nil, Externalf("listing the files %s..%s changed: %v", base, head, err)
	}
	listing, err := t.git("ls-tree", "-r", "--name-only", "-z", tree)
	if err != nil {
		return nil, Externalf("listing tree %s: %v", tree, err)
	}
	var moduleDirs []string
	for _, name := range strings.Split(listing, "\x00") {
		if name == "go.mod" || strings.HasSuffix(name, "/go.mod") {
			moduleDirs = append(moduleDirs, path.Dir(name))
		}
	}
	touched := map[string]bool{}
	for _, name := range strings.Split(changed, "\x00") {
		if name == "" {
			continue
		}
		if dir, ok := owningModule(moduleDirs, name); ok {
			touched[dir] = true
		}
	}
	if len(touched) == 0 {
		return nil, nil
	}
	isModule := map[string]bool{}
	for _, dir := range moduleDirs {
		isModule[dir] = true
	}
	// A module builds against the siblings its go.mod replaces, so they come along.
	extract := map[string]bool{}
	queue := sortedKeys(touched)
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if extract[dir] {
			continue
		}
		extract[dir] = true
		mod, err := t.git("show", tree+":"+path.Join(dir, "go.mod"))
		if err != nil {
			return nil, Externalf("reading %s/go.mod in %s: %v", dir, tree, err)
		}
		for _, target := range replacedPaths(mod) {
			joined := path.Clean(path.Join(dir, target))
			if isModule[joined] && !extract[joined] {
				queue = append(queue, joined)
			}
		}
	}

	work, err := os.MkdirTemp("", "tillr-land-build-")
	if err != nil {
		return nil, Externalf("creating the build directory: %v", err)
	}
	defer os.RemoveAll(work)
	if err := t.extract(tree, sortedKeys(extract), work); err != nil {
		return nil, err
	}
	var failures []string
	for _, dir := range sortedKeys(touched) {
		for _, args := range [][]string{{"build", "-trimpath", "./..."}, {"vet", "-trimpath", "./..."}} {
			if out, err := t.goIn(filepath.Join(work, filepath.FromSlash(dir)), args...); err != nil {
				failures = append(failures, "go "+args[0]+" "+dir+": "+firstLines(out, 8))
				break
			}
		}
	}
	return failures, nil
}

// owningModule is the module directory with the longest prefix of file, if any.
func owningModule(moduleDirs []string, file string) (string, bool) {
	best, found := "", false
	for _, dir := range moduleDirs {
		inside := dir == "." || strings.HasPrefix(file, dir+"/")
		if inside && (!found || len(dir) > len(best)) {
			best, found = dir, true
		}
	}
	return best, found
}

// replacedPaths lists the relative directories a go.mod's replace directives point at.
func replacedPaths(mod string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(mod, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "replace (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case inBlock:
		case strings.HasPrefix(line, "replace "):
			line = strings.TrimPrefix(line, "replace ")
		default:
			continue
		}
		_, target, ok := strings.Cut(line, "=>")
		if !ok {
			continue
		}
		fields := strings.Fields(target)
		if len(fields) > 0 && (strings.HasPrefix(fields[0], "./") || strings.HasPrefix(fields[0], "../")) {
			out = append(out, fields[0])
		}
	}
	return out
}

// extract writes dirs of tree into work, the way `git archive` would.
func (t *Trunk) extract(tree string, dirs []string, work string) error {
	ctx := t.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", t.repo, "archive", "--format=tar", tree, "--"}, dirs...)...)
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Externalf("archiving %s: %v", tree, err)
	}
	if err := cmd.Start(); err != nil {
		return Externalf("archiving %s: %v", tree, err)
	}
	reader := tar.NewReader(out)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cmd.Wait()
			return Externalf("reading the archive of %s: %v", tree, err)
		}
		target := filepath.Join(work, filepath.FromSlash(header.Name))
		if !strings.HasPrefix(target, work+string(filepath.Separator)) {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755)
		case tar.TypeSymlink:
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
				err = os.Symlink(header.Linkname, target)
			}
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
				err = writeFrom(target, reader, os.FileMode(header.Mode)&0o755|0o600)
			}
		}
		if err != nil {
			cmd.Wait()
			return Externalf("extracting %s: %v", header.Name, err)
		}
	}
	// git pads the archive past the tar end marker; drain it or git blocks on its write.
	io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		return Externalf("archiving %s: %s", tree, failureText(stderr.String(), err))
	}
	return nil
}

func writeFrom(name string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// goIn runs the go tool in dir. The module cache and build cache are the user's own
// defaults, so a check shares compiled packages with every other build on the machine.
func (t *Trunk) goIn(dir string, args ...string) (string, error) {
	ctx := t.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), "GOFLAGS=-mod=readonly", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return string(out), err
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, "\n")
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
