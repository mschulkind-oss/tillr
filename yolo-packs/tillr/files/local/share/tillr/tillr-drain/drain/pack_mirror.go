package drain

import (
	"context"
	"os/exec"
	"strings"
)

const tillrMirrorRoot = "yolo-packs/tillr/files/local/share/tillr/"

// stagePackMirror uses the same renderer as manual changes, but only for shipped modules
// touched by this candidate. Other repos, fork deliveries and unrelated candidates do not
// depend on the recipe or pick up pre-existing drift. The worker's fence has already passed.
func (l *Loop) stagePackMirror(ctx context.Context, c Candidate) (bool, error) {
	paths, err := l.git(ctx, c.Worktree, "diff", "--no-renames", "--name-only", "-z", c.BaseSHA+".."+c.HeadSHA, "--", "tools/", tillrMirrorRoot)
	if err != nil || paths == "" {
		return false, err
	}
	// Match the module set shipped by the pack-sync recipe; tillr-loop, for example,
	// ships only via the adapter embedded in tillr-drain, not as its own module.
	modules := []string{"tillr", "tillr-cli", "tillr-drain", "tillr-gate", "tillr-item", "tillr-land", "tillr-run", "tillr-store", "tillr-view", "tillr-trace", "tillr-roll"}
	args := []string{"--justfile", ".justfile", "pack-sync"}
	var destinations []string
	changedPaths := strings.Split(paths, "\x00")
	for _, module := range modules {
		for _, path := range changedPaths {
			if strings.HasPrefix(path, "tools/"+module+"/") || strings.HasPrefix(path, tillrMirrorRoot+module+"/") {
				args = append(args, module)
				destinations = append(destinations, tillrMirrorRoot+module)
				break
			}
		}
	}
	if len(destinations) == 0 {
		return false, nil
	}
	manifest, err := l.git(ctx, c.Worktree, "ls-tree", c.HeadSHA, "--", "yolo-packs/tillr/pack.json")
	if err != nil || manifest == "" {
		return false, err
	}
	status := []string{"status", "--porcelain", "--untracked-files=all"}
	if out, err := l.git(ctx, c.Worktree, status...); err != nil {
		return false, err
	} else if out != "" {
		return false, Externalf("pack-sync requires a clean candidate worktree: %s", strings.TrimSpace(out))
	}
	cmd := exec.CommandContext(ctx, "just", args...)
	cmd.Dir = c.Worktree
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, Externalf("pack-sync for %s: %v: %s", c.ItemID, err, strings.TrimSpace(string(out)))
	}
	// The renderer may not widen the maintenance commit beyond the selected mirrors.
	outside := append(status, "--", ".")
	for _, dst := range destinations {
		outside = append(outside, ":(exclude)"+dst)
	}
	if out, err := l.git(ctx, c.Worktree, outside...); err != nil {
		return false, err
	} else if out != "" {
		return false, Externalf("pack-sync changed paths outside the selected mirrors: %s", strings.TrimSpace(out))
	}
	if _, err := l.git(ctx, c.Worktree, append([]string{"add", "--all", "--force", "--"}, destinations...)...); err != nil {
		return false, err
	}
	changed, err := l.git(ctx, c.Worktree, "diff", "--cached", "--name-only")
	return changed != "", err
}
