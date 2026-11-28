package pathenv

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// StripExecutableFromPath returns a PATH string in which exeName is no longer
// resolvable. Any PATH directory that contains an executable exeName is
// replaced with a shadow directory containing symlinks to every other entry in
// that directory. Shadow directories are cached under the first writable root
// in candidateRoots so repeated invocations keep a stable PATH value.
func StripExecutableFromPath(pathValue, exeName string, candidateRoots ...string) string {
	if pathValue == "" || exeName == "" {
		return pathValue
	}
	entries := filepath.SplitList(pathValue)
	changed := false
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == "" {
			out = append(out, entry)
			continue
		}
		if !isExecutableFile(filepath.Join(entry, exeName)) {
			out = append(out, entry)
			continue
		}
		shadow, err := ensureShadowDir(entry, exeName, candidateRoots)
		if err != nil {
			// If no candidate root is writable, drop the directory rather than
			// silently leaving exeName resolvable.
			changed = true
			continue
		}
		out = append(out, shadow)
		changed = true
	}
	if !changed {
		return pathValue
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func ensureShadowDir(dir, exeName string, candidateRoots []string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	dirEntries, err := os.ReadDir(absDir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(dirEntries))
	for _, ent := range dirEntries {
		name := ent.Name()
		if name == exeName {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	h.Write([]byte(absDir))
	h.Write([]byte{0})
	h.Write([]byte(exeName))
	for _, name := range names {
		h.Write([]byte{0})
		h.Write([]byte(name))
	}
	digest := hex.EncodeToString(h.Sum(nil))[:16]

	var lastErr error
	for _, rootDir := range candidateRoots {
		if rootDir == "" {
			continue
		}
		parent := filepath.Join(rootDir, "go-test-no-"+exeName+"-bin")
		target := filepath.Join(parent, digest)
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			return target, nil
		}
		if err := os.MkdirAll(parent, 0o755); err != nil {
			lastErr = err
			continue
		}
		tmpDir, err := os.MkdirTemp(parent, ".tmp-"+digest+"-")
		if err != nil {
			lastErr = err
			continue
		}
		linkFailed := false
		for _, name := range names {
			if err := os.Symlink(filepath.Join(absDir, name), filepath.Join(tmpDir, name)); err != nil {
				lastErr = err
				linkFailed = true
				break
			}
		}
		if linkFailed {
			_ = os.RemoveAll(tmpDir)
			continue
		}
		if err := os.Rename(tmpDir, target); err != nil {
			_ = os.RemoveAll(tmpDir)
			if info, serr := os.Stat(target); serr == nil && info.IsDir() {
				return target, nil
			}
			lastErr = err
			continue
		}
		return target, nil
	}
	if lastErr == nil {
		lastErr = os.ErrNotExist
	}
	return "", lastErr
}
