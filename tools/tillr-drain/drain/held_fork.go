package drain

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const forkPatchPrefix = "refs/heads/patch/"

// The base must be an ancestor of the head, so one ref keeps both objects
// reachable. The ref name binds the run to the original delivery ref and base;
// neither changes when the patch branch advances or the lane is reassigned.
func candidatePinRef(c Candidate) string {
	if c.BaseRef == "" {
		return pinRef(c.RunID)
	}
	binding := c.RunID + "\x00" + c.BaseRef + "\x00" + c.BaseSHA
	return fmt.Sprintf("refs/tillr/held-forks/%x", sha256.Sum256([]byte(binding)))
}

func (l *Loop) heldForkTarget(c Candidate, lane string) (ForkTarget, error) {
	name, ok := strings.CutPrefix(c.BaseRef, forkPatchPrefix)
	if !ok || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return ForkTarget{}, Externalf("invalid held fork delivery ref %q", c.BaseRef)
	}
	if lane == "" || lane == "." || lane == ".." || strings.ContainsAny(lane, "/\\") {
		return ForkTarget{}, Externalf("invalid held fork lane %q", lane)
	}
	path := ".forks/lanes/" + lane + "/" + name
	checkout, err := filepath.Abs(filepath.Join(l.cfg.Root, filepath.FromSlash(path)))
	return ForkTarget{Fork: name, Ref: c.BaseRef, Path: path, Checkout: checkout}, err
}

func (l *Loop) pinHeldFork(ctx context.Context, c Candidate) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pinTimeout)
	defer cancel()
	ref := candidatePinRef(c)
	// A crash before recording release intent can leave this ref behind. Never
	// overwrite a conflicting pin.
	refs, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(refs) != "" {
		return l.pinnedCandidate(ctx, c)
	}
	if _, err := l.git(ctx, c.Worktree, "merge-base", "--is-ancestor", c.BaseSHA, c.HeadSHA); err != nil {
		return fmt.Errorf("recorded fork base is not an ancestor of its head: %w", err)
	}
	if _, err := l.git(ctx, l.cfg.Root, "fetch", "--no-tags", "--no-write-fetch-head", c.Worktree, c.HeadSHA); err != nil {
		return err
	}
	_, err = l.git(ctx, l.cfg.Root, "update-ref", "--no-deref", ref, c.HeadSHA, "")
	return err
}

// checkHeldForkCheckout protects against git walking to a parent repo, a
// symlink aliasing another lane, or moving a branch in a shared git directory.
func (l *Loop) checkHeldForkCheckout(ctx context.Context, path string) error {
	gitDir := filepath.Join(path, ".git")
	real, err := filepath.EvalSymlinks(gitDir)
	if info, statErr := os.Stat(gitDir); err != nil || real != gitDir || statErr != nil || !info.IsDir() {
		return fmt.Errorf("fork path %s is missing or aliases another checkout", path)
	}
	for _, check := range []struct {
		args []string
		want string
	}{
		{[]string{"rev-parse", "--show-toplevel"}, path},
		{[]string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, filepath.Join(path, ".git")},
		{[]string{"symbolic-ref", "HEAD"}, "refs/heads/main"},
	} {
		out, err := l.git(ctx, path, check.args...)
		if err != nil || strings.TrimSpace(out) != check.want {
			return fmt.Errorf("fork checkout %s is not an isolated checkout on main", path)
		}
	}
	return nil
}

// Checked under the release lock, before recording intent and again before
// changing either checkout. The root checkout is a boundary watch, not the
// fork candidate. Never reset it based on the fork pin alone.
func (l *Loop) checkHeldForkRelease(ctx context.Context, c Candidate, trunk string, interrupted bool) error {
	fork, err := l.heldForkTarget(c, c.LaneID)
	if err != nil {
		return err
	}
	if c.Worktree != fork.Checkout || l.laneForkTarget(c.LaneID) != fork.Checkout || l.lanePatchRef(c.LaneID) != c.BaseRef || l.laneBase(c.LaneID) != c.BaseSHA {
		return fmt.Errorf("fork lane target or base does not match the recorded candidate")
	}
	if err := l.checkHeldForkCheckout(ctx, c.Worktree); err != nil {
		return err
	}
	head, err := l.revParse(ctx, c.Worktree, "HEAD")
	if err != nil || (head != c.HeadSHA && !(interrupted && head == c.BaseSHA)) {
		return fmt.Errorf("unrecorded fork checkout HEAD needs inspection")
	}
	root := l.lanePath(c.LaneID)
	for _, path := range []string{c.Worktree, root} {
		if path == root && interrupted && strings.HasPrefix(c.LaneID, "desk-") {
			if _, err := os.Lstat(root); os.IsNotExist(err) {
				continue // The previous reset already removed the desk worktree.
			}
		}
		paths, err := l.uncommittedPaths(ctx, path)
		if err != nil {
			return err
		}
		if len(paths) != 0 {
			return fmt.Errorf("checkout %s has uncommitted work", path)
		}
		if path == root {
			if err := l.checkHeldForkRoot(ctx, root); err != nil {
				return err
			}
			head, err := l.revParse(ctx, root, "HEAD")
			if err != nil {
				return err
			}
			if _, err := l.git(ctx, l.cfg.Root, "merge-base", "--is-ancestor", head, trunk); err != nil {
				return fmt.Errorf("unrecorded root lane HEAD needs inspection: %s", head)
			}
		}
	}
	return nil
}

func (l *Loop) resetHeldFork(ctx context.Context, c Candidate, trunk string) error {
	if err := l.checkHeldForkRelease(ctx, c, trunk, true); err != nil {
		return err
	}
	if _, err := l.git(ctx, c.Worktree, "checkout", "--no-overwrite-ignore", "-B", "main", c.BaseSHA); err != nil {
		return err
	}
	// The main repository's worktree is only a boundary watch for this item.
	// Leave a clean trunk ancestor untouched; ordinary staging will refresh it.
	if !strings.HasPrefix(c.LaneID, "desk-") {
		return nil
	}
	return l.resetLaneToRegistryLocked(ctx, c.LaneID, trunk)
}

func (l *Loop) checkHeldForkRoot(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(abs); err != nil || real != abs {
		return Externalf("root lane %s aliases another path", abs)
	}
	top, err := l.git(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(top) != abs {
		return Externalf("root lane %s is not its own worktree", abs)
	}
	if shared, ok := l.sharesRepository(ctx, abs); !ok || !shared {
		return Externalf("root lane %s does not belong to the root repository", abs)
	}
	return nil
}

func (l *Loop) admitHeldFork(ctx context.Context, c Candidate) (exists bool, err error) {
	if _, err := os.Lstat(c.Worktree); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	refuse := func(reason, detail string) (bool, error) {
		return true, laneRefusalf(c.LaneID, reason, "inspect the fork checkout before clearing it", "fork checkout %s %s", c.Worktree, detail)
	}
	if err := l.checkHeldForkCheckout(ctx, c.Worktree); err != nil {
		return refuse(ReasonLaneUnlandedChange, "is not an isolated checkout on main")
	}
	paths, err := l.uncommittedPaths(ctx, c.Worktree)
	if err != nil {
		return true, err
	}
	if len(paths) > 0 {
		return refuse(ReasonLaneUncommittedWork, "has uncommitted work")
	}
	head, err := l.revParse(ctx, c.Worktree, "HEAD")
	if err != nil {
		return true, err
	}
	if head != c.HeadSHA {
		if _, err := l.git(ctx, l.cfg.Root, "merge-base", "--is-ancestor", head, c.BaseRef); err != nil {
			return refuse(ReasonLaneUnlandedChange, "has an undelivered HEAD")
		}
	}
	return true, nil
}

// Check ancestors before mkdir: a missing leaf can still sit below a symlink
// into a sibling's checkout. No path in another lane is ours to initialize.
func checkHeldStagePath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for path := abs; ; path = filepath.Dir(path) {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if real, err := filepath.EvalSymlinks(path); err != nil || real != path {
			return Externalf("lane staging path %s aliases another path", abs)
		}
		return nil
	}
}

func (l *Loop) materializeHeldFork(ctx context.Context, c Candidate, exists bool, inputs *heldForkInputs) error {
	path := c.Worktree
	if !exists {
		parent := filepath.Dir(path)
		if err := os.MkdirAll(parent, 0755); err != nil {
			return err
		}
		if real, err := filepath.EvalSymlinks(parent); err != nil || real != parent {
			return Externalf("fork lane directory %s aliases another path", parent)
		}
		// Publish only a complete checkout. A crash during fetch or checkout
		// leaves a private temporary directory, not a half-staged lane target.
		stage, err := os.MkdirTemp(parent, ".held-fork-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		path = filepath.Join(stage, "checkout")
		if _, err := l.git(ctx, stage, "init", "--initial-branch=main", path); err != nil {
			return err
		}
	}
	if err := l.restoreHeldForkRefs(ctx, c, path, inputs); err != nil {
		return err
	}
	if _, err := l.git(ctx, path, "checkout", "--no-overwrite-ignore", "-B", "main", c.HeadSHA); err != nil {
		return err
	}
	if base, err := l.revParse(ctx, path, c.BaseSHA+"^{commit}"); err != nil || base != c.BaseSHA {
		return Externalf("restored fork base failed readback: %s (%v)", base, err)
	}
	if !exists {
		if _, err := os.Lstat(c.Worktree); !os.IsNotExist(err) {
			return Externalf("fork checkout %s appeared during staging", c.Worktree)
		}
		return os.Rename(path, c.Worktree)
	}
	return nil
}

// Save the current node scope, not the whole-item scope used by the gate.
func (l *Loop) saveHeldLaneStage(ctx context.Context, c Candidate) (*heldLaneStage, error) {
	body, err := os.ReadFile(filepath.Join(l.laneDirPath(c.LaneID), FenceFile))
	if err != nil {
		return nil, err
	}
	nodeBase := l.laneFile(c.LaneID, BaseSHAFile)
	if nodeBase == "" {
		nodeBase = c.BaseSHA
	}
	s := &heldLaneStage{SourceLane: c.LaneID, NodeFence: string(body), NodeBase: nodeBase}
	if c.BaseRef != "" {
		s.Fork, err = l.saveHeldForkInputs(ctx, c)
	}
	return s, err
}

func rebaseHeldPath(path, from, to string) string {
	if path == from {
		return to
	}
	if suffix, ok := strings.CutPrefix(path, from+string(filepath.Separator)); ok {
		return filepath.Join(to, suffix)
	}
	return path
}

func (l *Loop) stageHeldFork(ctx context.Context, work *pendingWork, c Candidate) error {
	saved := work.HeldStage
	if saved == nil {
		return Externalf("saved fork staging inputs are missing")
	}
	if err := l.verifyHeldForkRefs(ctx, c, saved.Fork); err != nil {
		return err
	}
	from, err := l.heldForkTarget(c, saved.SourceLane)
	if err != nil {
		return err
	}
	to, err := l.heldForkTarget(c, c.LaneID)
	if err != nil {
		return err
	}
	if c.Worktree != to.Checkout {
		return Externalf("assigned fork target does not match its delivery ref")
	}
	fence := rebindForkFence(work.HeldFence, from, to)
	found := false
	for _, entry := range fenceEntries(fence) {
		if entry == to.Path || strings.HasPrefix(entry, to.Path+"/") {
			found = true
			break
		}
	}
	if !found {
		return Externalf("saved fence does not name the fork checkout")
	}
	root := l.lanePath(c.LaneID)
	lane := l.laneDirPath(c.LaneID)
	metadata := map[string]string{
		TargetFile: c.Worktree + "\n", PatchRefFile: c.BaseRef + "\n",
		BaseSHAFile: saved.NodeBase + "\n", ItemBaseSHAFile: c.BaseSHA + "\n",
		FenceFile: rebindForkFence(saved.NodeFence, from, to), ItemFenceFile: fence,
	}
	paths := []string{root, c.Worktree}
	for name := range metadata {
		paths = append(paths, filepath.Join(lane, name))
	}
	for _, path := range paths {
		if err := checkHeldStagePath(path); err != nil {
			return laneRefusalf(c.LaneID, ReasonLaneUnlandedChange, "inspect the lane path", "%v", err)
		}
	}
	exists, err := l.admitHeldFork(ctx, c)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(root); err == nil {
		if err := l.checkHeldForkRoot(ctx, root); err != nil {
			return laneRefusalf(c.LaneID, ReasonLaneUnlandedChange, "inspect the root lane", "%v", err)
		}
		paths, err := l.uncommittedPaths(ctx, root)
		if err != nil {
			return err
		}
		if len(paths) != 0 {
			return laneRefusalf(c.LaneID, ReasonLaneUncommittedWork, "inspect the root worktree before clearing it", "root lane %s has uncommitted work", root)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	trunk, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil {
		return err
	}
	if head, _ := l.revParse(ctx, root, "HEAD"); head != trunk {
		stager := &LaneStager{Root: l.cfg.Root, LaneRoot: l.cfg.LaneRoot, Store: l.cfg.Store, Now: l.cfg.Now}
		spec := StageSpec{LaneID: c.LaneID, ItemID: c.ItemID, RunID: c.RunID, BaseSHA: trunk}
		if err := stager.ensureWorktree(ctx, spec, lane, root); err != nil {
			return err
		}
	}
	if err := l.materializeHeldFork(ctx, c, exists, saved.Fork); err != nil {
		return err
	}
	for name, body := range metadata {
		if err := os.WriteFile(filepath.Join(lane, name), []byte(body), 0600); err != nil {
			return err
		}
	}
	return nil
}

func rebindForkFence(body string, from, to ForkTarget) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		entry := strings.TrimSpace(line)
		if entry == from.Path || strings.HasPrefix(entry, from.Path+"/") {
			lines[i] = strings.Replace(line, from.Path, to.Path, 1)
		}
	}
	return strings.Join(lines, "\n")
}
