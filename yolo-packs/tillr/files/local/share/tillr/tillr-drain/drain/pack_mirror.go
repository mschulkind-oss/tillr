package drain

import (
	"context"
	_ "embed"
	"os/exec"
	"strings"
)

const tillrMirrorRoot = "yolo-packs/tillr/files/local/share/tillr/"

// Embed the same renderer that just pack-sync uses. A candidate may predate that recipe
// or change it; the drain runs its own version against the candidate's source tree.
//
//go:embed pack-mirror.py
var packMirrorRenderer string

// stagePackMirror renders only the shipped modules touched by this candidate, so
// unrelated candidates do not pick up pre-existing drift. The worker's fence has passed.
func (l *Loop) stagePackMirror(ctx context.Context, c Candidate) (bool, error) {
	defer timeHandoffPhase(ctx, "pack_mirror")()
	paths, err := l.git(ctx, c.Worktree, "diff", "--no-renames", "--name-only", "-z", c.BaseSHA+".."+c.HeadSHA, "--", "tools/", tillrMirrorRoot)
	if err != nil || paths == "" {
		return false, err
	}
	// Match SHIPPED in the renderer. tillr-loop ships only via the adapter
	// embedded in tillr-drain, not as its own module.
	modules := []string{"tillr", "tillr-cli", "tillr-drain", "tillr-gate", "tillr-item", "tillr-land", "tillr-run", "tillr-store", "tillr-view", "tillr-trace", "tillr-roll"}
	args := []string{"-I", "-c", packMirrorRenderer, "--sync", "."}
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
	cmd := exec.CommandContext(ctx, "python3", args...)
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
