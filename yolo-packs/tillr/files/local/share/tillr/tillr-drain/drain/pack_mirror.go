package drain

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/packmirror"
)

const tillrMirrorRoot = "yolo-packs/tillr/files/local/share/tillr/"

// packMirrorModule reports whether path is a generated pack-mirror copy under
// tillrMirrorRoot that packmirror.Sync owns, returning its module name. Paths owned by
// pack-docs (such as tillr-loop/README.md or docs/) return false.
func packMirrorModule(path string) (string, bool) {
	if path == packmirror.SupportRevisionRelPath {
		return "tillr", true
	}
	if !strings.HasPrefix(path, tillrMirrorRoot) {
		return "", false
	}
	rest := strings.TrimPrefix(path, tillrMirrorRoot)
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

// filterMirrorConflictReports splits git CONFLICT lines into non-mirror reports and the
// deduplicated pack-mirror module names whose mirrored copies conflicted.
func filterMirrorConflictReports(reports []string) ([]string, []string) {
	var nonMirror []string
	var mirrorModules []string
	seenModule := map[string]bool{}
	for _, report := range reports {
		if module, ok := packMirrorModule(conflictPath(report)); ok {
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

func (l *Loop) hasPackManifest(ctx context.Context, worktree, rev string) (bool, error) {
	if worktree == "" || rev == "" {
		return false, nil
	}
	manifest, err := l.git(ctx, worktree, "ls-tree", rev, "--", "yolo-packs/tillr/pack.json")
	return manifest != "", err
}

// stagePackMirror renders only the shipped modules touched by this candidate, so
// unrelated candidates do not pick up pre-existing drift. The worker's fence has passed.
func (l *Loop) stagePackMirror(ctx context.Context, c Candidate) (bool, error) {
	defer timeHandoffPhase(ctx, "pack_mirror")()
	paths, err := l.git(ctx, c.Worktree, "diff", "--no-renames", "--name-only", "-z", c.BaseSHA+".."+c.HeadSHA, "--", "tools/", tillrMirrorRoot, packmirror.WorkflowsDir)
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
			if strings.HasPrefix(path, "tools/"+module+"/") || strings.HasPrefix(path, tillrMirrorRoot+module+"/") {
				addModule(module)
				break
			}
		}
	}
	// Mirror tools/tillr-loop/ (except README.md, owned by pack-docs).
	const loopSourcePrefix = "tools/tillr-loop/"
	const loopMirrorPrefix = tillrMirrorRoot + "tillr-loop/"
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
		if strings.HasPrefix(path, packmirror.WorkflowsDir+"/") || strings.HasPrefix(path, tillrMirrorRoot+packmirror.WorkflowsMount+"/") {
			addModule(packmirror.WorkflowsMount)
		}
		if packmirror.IsSupportFile(path) || path == packmirror.SupportRevisionRelPath {
			addModule("tillr")
		}
	}
	if len(selectedModules) == 0 {
		return false, nil
	}
	hasManifest, err := l.hasPackManifest(ctx, c.Worktree, c.HeadSHA)
	if err != nil || !hasManifest {
		return false, err
	}
	return l.syncAndStagePackMirrorModules(ctx, c, selectedModules)
}

func (l *Loop) syncAndStagePackMirrorModules(ctx context.Context, c Candidate, selectedModules []string) (bool, error) {
	if len(selectedModules) == 0 {
		return false, nil
	}
	destinations := make([]string, 0, len(selectedModules)+1)
	for _, module := range selectedModules {
		destinations = append(destinations, tillrMirrorRoot+module)
		if module == "tillr" {
			destinations = append(destinations, packmirror.SupportRevisionRelPath)
		}
	}
	status := []string{"status", "--porcelain", "--untracked-files=all"}
	if out, err := l.git(ctx, c.Worktree, status...); err != nil {
		return false, err
	} else if out != "" {
		return false, Externalf("pack-sync requires a clean candidate worktree: %s", strings.TrimSpace(out))
	}
	var out bytes.Buffer
	if rc := packmirror.Sync(c.Worktree, selectedModules, &out, &out); rc != 0 {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, Externalf("pack-sync for %s: exit %d: %s", c.ItemID, rc, strings.TrimSpace(out.String()))
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
	var toStage []string
	for _, dst := range destinations {
		exists, err := l.packMirrorDestinationExists(ctx, c.Worktree, dst)
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
	if _, err := l.git(ctx, c.Worktree, append([]string{"add", "--all", "--force", "--"}, toStage...)...); err != nil {
		return false, err
	}
	changed, err := l.git(ctx, c.Worktree, "diff", "--cached", "--name-only")
	return changed != "", err
}

// packMirrorDestinationExists reports whether dst is present in the worktree or the index.
// A deleted program whose mirror is already gone exists in neither and must not be passed
// to git add, which rejects an unmatched pathspec.
func (l *Loop) packMirrorDestinationExists(ctx context.Context, worktree, dst string) (bool, error) {
	_, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(dst)))
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	indexed, err := l.git(ctx, worktree, "ls-files", "-z", "--", dst)
	if err != nil {
		return false, err
	}
	return indexed != "", nil
}
