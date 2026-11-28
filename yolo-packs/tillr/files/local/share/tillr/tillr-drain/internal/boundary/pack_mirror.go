package boundary

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/packmirror"
)

const TillrMirrorRoot = "yolo-packs/tillr/files/local/share/tillr/"

// PackMirrorModule reports whether path is a generated pack-mirror copy under
// TillrMirrorRoot that packmirror.Sync owns, returning its module name. Paths owned by
// pack-docs (such as tillr-loop/README.md or docs/) return false.
func PackMirrorModule(path string) (string, bool) {
	if path == packmirror.SupportRevisionRelPath {
		return "tillr", true
	}
	if !strings.HasPrefix(path, TillrMirrorRoot) {
		return "", false
	}
	rest := strings.TrimPrefix(path, TillrMirrorRoot)
	module, rel, ok := strings.Cut(rest, "/")
	if !ok || rel == "" {
		return "", false
	}
	if module == packmirror.LoopModule {
		if rel == "README.md" {
			return "", false
		}
		return module, true
	}
	if module == packmirror.WorkflowsMount {
		return module, true
	}
	for _, shipped := range packmirror.Shipped {
		if module == shipped {
			return module, true
		}
	}
	return "", false
}

// FilterMirrorConflictReports splits git CONFLICT lines into non-mirror reports and the
// deduplicated pack-mirror module names whose mirrored copies conflicted.
func FilterMirrorConflictReports(reports []string, conflictPath func(string) string) ([]string, []string) {
	var nonMirror []string
	var mirrorModules []string
	seenModule := map[string]bool{}
	for _, report := range reports {
		if module, ok := PackMirrorModule(conflictPath(report)); ok {
			if !seenModule[module] {
				seenModule[module] = true
				mirrorModules = append(mirrorModules, module)
			}
			continue
		}
		nonMirror = append(nonMirror, report)
	}
	return nonMirror, mirrorModules
}

func HasPackManifest(ctx context.Context, worktree, rev string, git func(ctx context.Context, dir string, args ...string) (string, error)) (bool, error) {
	if worktree == "" || rev == "" {
		return false, nil
	}
	if git == nil {
		git = BoundaryGit
	}
	manifest, err := git(ctx, worktree, "ls-tree", rev, "--", "yolo-packs/tillr/pack.json")
	return manifest != "", err
}

// StagePackMirror renders only the shipped modules touched by this candidate, so
// unrelated candidates do not pick up pre-existing drift. The worker's fence has passed.
func StagePackMirror(
	ctx context.Context,
	worktree, baseSHA, headSHA, itemID string,
	git func(ctx context.Context, dir string, args ...string) (string, error),
) (bool, error) {
	defer TimeHandoffPhase(ctx, "pack_mirror")()
	if git == nil {
		git = BoundaryGit
	}
	paths, err := git(ctx, worktree, "diff", "--no-renames", "--name-only", "-z", baseSHA+".."+headSHA, "--", "tools/", TillrMirrorRoot, packmirror.WorkflowsDir)
	if err != nil || paths == "" {
		return false, err
	}
	// Match SHIPPED in the renderer. The standalone loop adapter is selected below.
	var selectedModules []string
	selectedSet := map[string]bool{}
	addModule := func(m string) {
		if !selectedSet[m] {
			selectedSet[m] = true
			selectedModules = append(selectedModules, m)
		}
	}
	changedPaths := strings.Split(paths, "\x00")
	for _, module := range packmirror.Shipped {
		for _, path := range changedPaths {
			if strings.HasPrefix(path, "tools/"+module+"/") || strings.HasPrefix(path, TillrMirrorRoot+module+"/") {
				addModule(module)
				break
			}
		}
	}
	// Mirror tools/tillr-loop/ (except README.md, owned by pack-docs).
	const loopSourcePrefix = "tools/tillr-loop/"
	const loopMirrorPrefix = TillrMirrorRoot + "tillr-loop/"
	for _, path := range changedPaths {
		if (strings.HasPrefix(path, loopSourcePrefix) && path != loopSourcePrefix+"README.md") ||
			(strings.HasPrefix(path, loopMirrorPrefix) && path != loopMirrorPrefix+"README.md") {
			addModule(packmirror.LoopModule)
			break
		}
	}
	// Mirror config/tillr/workflows/ and regenerate support-revision whenever any
	// runtime support file changes.
	for _, path := range changedPaths {
		if strings.HasPrefix(path, packmirror.WorkflowsDir+"/") || strings.HasPrefix(path, TillrMirrorRoot+packmirror.WorkflowsMount+"/") {
			addModule(packmirror.WorkflowsMount)
		}
		if packmirror.IsSupportFile(path) || path == packmirror.SupportRevisionRelPath {
			addModule("tillr")
		}
	}
	if len(selectedModules) == 0 {
		return false, nil
	}
	hasManifest, err := HasPackManifest(ctx, worktree, headSHA, git)
	if err != nil || !hasManifest {
		return false, err
	}
	return syncAndStagePackMirrorModules(ctx, worktree, itemID, selectedModules, candidateChangesPackMirrorSync(changedPaths), git)
}

func candidateChangesPackMirrorSync(changedPaths []string) bool {
	for _, path := range changedPaths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		if strings.HasPrefix(path, "tools/tillr/packmirror/") || strings.HasPrefix(path, "tools/tillr/cmd/pack-mirror/") {
			return true
		}
	}
	return false
}

func runCandidatePackMirrorSync(ctx context.Context, worktree, itemID string, selectedModules []string) error {
	absWorktree, err := filepath.Abs(worktree)
	if err != nil {
		return drainmodel.Externalf("candidate pack-sync for %s: resolve worktree %s: %v", itemID, worktree, err)
	}
	modDir := filepath.Join(absWorktree, "tools", "tillr")
	args := make([]string, 0, 5+len(selectedModules))
	args = append(args, "run", "-C", modDir, "./cmd/pack-mirror", "--sync", absWorktree)
	args = append(args, selectedModules...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = absWorktree
	cmd.Env = BoundaryGitEnv()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		detail := strings.TrimSpace(out.String())
		if detail != "" {
			return drainmodel.Externalf("candidate pack-sync for %s failed: %v: %s", itemID, err, detail)
		}
		return drainmodel.Externalf("candidate pack-sync for %s failed: %v", itemID, err)
	}
	return nil
}

func SyncAndStagePackMirrorModules(
	ctx context.Context,
	worktree, itemID string,
	selectedModules []string,
	git func(ctx context.Context, dir string, args ...string) (string, error),
) (bool, error) {
	return syncAndStagePackMirrorModules(ctx, worktree, itemID, selectedModules, false, git)
}

func syncAndStagePackMirrorModules(
	ctx context.Context,
	worktree, itemID string,
	selectedModules []string,
	useCandidateSync bool,
	git func(ctx context.Context, dir string, args ...string) (string, error),
) (bool, error) {
	if len(selectedModules) == 0 {
		return false, nil
	}
	if git == nil {
		git = BoundaryGit
	}
	destinations := make([]string, 0, len(selectedModules)+1)
	for _, module := range selectedModules {
		destinations = append(destinations, TillrMirrorRoot+module)
		if module == "tillr" {
			destinations = append(destinations, packmirror.SupportRevisionRelPath)
		}
	}
	status := []string{"status", "--porcelain", "--untracked-files=all"}
	if out, err := git(ctx, worktree, status...); err != nil {
		return false, err
	} else if out != "" {
		return false, drainmodel.Externalf("pack-sync requires a clean candidate worktree: %s", strings.TrimSpace(out))
	}
	if useCandidateSync {
		if err := runCandidatePackMirrorSync(ctx, worktree, itemID, selectedModules); err != nil {
			return false, err
		}
	} else {
		var out bytes.Buffer
		if rc := packmirror.Sync(worktree, selectedModules, &out, &out); rc != 0 {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, drainmodel.Externalf("pack-sync for %s: exit %d: %s", itemID, rc, strings.TrimSpace(out.String()))
		}
	}
	// The renderer may not widen the maintenance commit beyond the selected mirrors.
	outside := append(status, "--", ".")
	for _, dst := range destinations {
		outside = append(outside, ":(exclude)"+dst)
	}
	if out, err := git(ctx, worktree, outside...); err != nil {
		return false, err
	} else if out != "" {
		return false, drainmodel.Externalf("pack-sync changed paths outside the selected mirrors: %s", strings.TrimSpace(out))
	}
	var toStage []string
	for _, dst := range destinations {
		exists, err := packMirrorDestinationExists(ctx, worktree, dst, git)
		if err != nil {
			return false, err
		}
		if exists {
			toStage = append(toStage, dst)
		}
	}
	if len(toStage) == 0 {
		return false, nil
	}
	if _, err := git(ctx, worktree, append([]string{"add", "--all", "--force", "--"}, toStage...)...); err != nil {
		return false, err
	}
	changed, err := git(ctx, worktree, "diff", "--cached", "--name-only")
	return changed != "", err
}

// packMirrorDestinationExists reports whether dst is present in the worktree or the index.
// A deleted program whose mirror is already gone exists in neither and must not be passed
// to git add, which rejects an unmatched pathspec.
func packMirrorDestinationExists(ctx context.Context, worktree, dst string, git func(ctx context.Context, dir string, args ...string) (string, error)) (bool, error) {
	_, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(dst)))
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	indexed, err := git(ctx, worktree, "ls-files", "-z", "--", dst)
	if err != nil {
		return false, err
	}
	return indexed != "", nil
}
