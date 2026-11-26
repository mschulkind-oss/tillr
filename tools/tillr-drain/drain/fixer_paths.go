package drain

import (
	"context"
	"path/filepath"
	"strings"
)

// expandFixerPaths replaces directories that contain gate code with their tracked
// descendants at the red revision, not the potentially advanced checkout. The
// ordinary fence filter then excludes only the gate's own files,
// preserving safe sources without granting permission to edit the gate indirectly.
func (l *Loop) expandFixerPaths(ctx context.Context, redSHA string, paths []string) ([]string, error) {
	gatePaths := l.fixerGateCodePaths()
	var tracked []string
	var out []string
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		unsafe := filepath.IsAbs(path) || strings.HasPrefix(path, "./")
		for _, part := range strings.Split(path, "/") {
			unsafe = unsafe || part == ".."
		}
		if unsafe || path == "" {
			out = append(out, path)
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(path))
		broad := clean == "."
		for _, gate := range gatePaths {
			broad = broad || strings.HasPrefix(gate, clean+"/")
		}
		if !broad {
			out = append(out, path)
			continue
		}
		if tracked == nil {
			raw, err := l.git(ctx, l.cfg.Root, "ls-tree", "-r", "--name-only", "-z", redSHA)
			if err != nil {
				return nil, Externalf("expanding trunk fixer watch %s: %v", path, err)
			}
			tracked = strings.Split(raw, "\x00")
		}
		for _, file := range tracked {
			if file != "" && (clean == "." || strings.HasPrefix(file, clean+"/")) {
				out = append(out, file)
			}
		}
	}
	return out, nil
}
