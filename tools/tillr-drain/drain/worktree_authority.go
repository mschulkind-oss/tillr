package drain

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
)

// AuthorizeCheckoutRoot refuses a checkout root that cannot own lane worktrees or
// branches: a linked worktree sharing another repository's common directory, a
// repository guarded by heavyrun.CommonGuard in tests, or the drain repository
// enclosing an active worker run. Plain non-Git directories return nil here so
// non-landing definitions do not fail startup before reading their definition;
// landing paths call authorizeWorktreeRoot before touching Git.
func AuthorizeCheckoutRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	gitDir, commonDir := gitCheckoutAt(abs)
	if commonDir == "" {
		if resolved := evalNearestSymlinks(abs); resolved != abs {
			gitDir, commonDir = gitCheckoutAt(resolved)
		}
	}
	if commonDir == "" {
		return nil
	}
	return authorizeRepository(root, gitDir, commonDir)
}

func authorizeWorktreeRoot(root string) error {
	gitDir, commonDir := discoverGitRepository(root)
	if commonDir == "" {
		return nil
	}
	return authorizeRepository(root, gitDir, commonDir)
}

func authorizeRepository(root, gitDir, commonDir string) error {
	if gitDir != commonDir {
		return Usagef("worktree registration refused: %s is a linked worktree sharing %s; stage lanes only against a standalone repository (git init in scratch for tests)", root, commonDir)
	}
	if guard := heavyrun.CommonGuard; guard != nil {
		if err := guard(commonDir); err != nil {
			return err
		}
	}
	if workerProtectedCommonDirs()[commonDir] {
		return Usagef("worktree registration refused: worker run may not register worktrees or branches in the drain repository (%s); initialize an isolated repository in scratch (git init) instead", commonDir)
	}
	return nil
}

func discoverGitRepository(start string) (gitDir, commonDir string) {
	if strings.TrimSpace(start) == "" {
		return "", ""
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", ""
	}
	candidates := []string{abs}
	if resolved := evalNearestSymlinks(abs); resolved != abs {
		candidates = append(candidates, resolved)
	}
	for _, cur := range candidates {
		for {
			if gd, cd := gitCheckoutAt(cur); cd != "" {
				return gd, cd
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	return "", ""
}

// realCommonDirs finds every enclosing common git directory above starts — including an
// outer repository enclosing a nested roll clone or its durable scratch — by reading the
// filesystem rather than invoking git, avoiding recursion under test git wrappers.
func realCommonDirs(starts ...string) map[string]bool {
	found := map[string]bool{}
	for _, start := range starts {
		if strings.TrimSpace(start) == "" {
			continue
		}
		abs, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		candidates := []string{abs}
		if resolved := evalNearestSymlinks(abs); resolved != abs {
			candidates = append(candidates, resolved)
		}
		for _, dir := range candidates {
			for {
				if _, common := gitCheckoutAt(dir); common != "" {
					found[common] = true
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		}
	}
	return found
}

func gitCheckoutAt(dir string) (gitDir, commonDir string) {
	dotGit := filepath.Join(dir, ".git")
	if info, err := os.Stat(dotGit); err == nil {
		if info.IsDir() {
			gd := sameDirKey(dotGit)
			return gd, resolveCommonDir(gd)
		}
		raw, err := os.ReadFile(dotGit)
		if err != nil || !strings.HasPrefix(string(raw), "gitdir:") {
			return "", ""
		}
		target := strings.TrimSpace(strings.TrimPrefix(string(raw), "gitdir:"))
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		gd := sameDirKey(target)
		return gd, resolveCommonDir(gd)
	}
	if isBareGitDir(dir) {
		gd := sameDirKey(dir)
		return gd, resolveCommonDir(gd)
	}
	return "", ""
}

func resolveCommonDir(gitDir string) string {
	rel, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	common := strings.TrimSpace(string(rel))
	if common == "" {
		return gitDir
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return sameDirKey(common)
}

func isBareGitDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		return false
	}
	refs, err := os.Stat(filepath.Join(dir, "refs"))
	if err != nil || !refs.IsDir() {
		return false
	}
	objs, err := os.Stat(filepath.Join(dir, "objects"))
	return err == nil && objs.IsDir()
}

func evalNearestSymlinks(abs string) string {
	cur := filepath.Clean(abs)
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(abs)
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func sameDirKey(p string) string {
	return evalNearestSymlinks(p)
}

func workerProtectedCommonDirs() map[string]bool {
	protected := gitGuardProtectedCommonDirs(os.Getenv("PATH"))
	if os.Getenv("TILLR_RUN_ID") != "" || os.Getenv("TILLR_RUN_EXEC_ID") != "" || os.Getenv("BOARD_DRAIN_RUN") != "" {
		for common := range realCommonDirs(
			os.Getenv("TILLR_REPO_ROOT"),
			os.Getenv("TILLR_LOOP_STORE"),
			os.Getenv("BOARD_DRAIN_BRIEF"),
			os.Getenv("YOLO_DURABLE_DIR"),
		) {
			protected[common] = true
		}
	}
	return protected
}

func gitGuardProtectedCommonDirs(pathEnv string) map[string]bool {
	protected := map[string]bool{}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "git")
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		var buf [1024]byte
		n, _ := io.ReadFull(f, buf[:])
		_ = f.Close()
		if n == 0 {
			continue
		}
		head := string(buf[:n])
		if !strings.Contains(head, "# tillr-git-guard\n") {
			continue
		}
		for _, line := range strings.Split(head, "\n") {
			raw, ok := strings.CutPrefix(line, "PROTECTED_COMMON_DIR=")
			if !ok {
				continue
			}
			if unquoted := unquoteShellSingleQuoted(strings.TrimSpace(raw)); unquoted != "" {
				protected[sameDirKey(unquoted)] = true
			}
		}
	}
	return protected
}

func unquoteShellSingleQuoted(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], `'\''`, `'`)
	}
	return strings.Trim(s, `"`)
}
