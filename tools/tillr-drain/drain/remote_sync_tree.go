package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// checkRemoteSyncTree requires a single commit atop the current conflict base.
// Outside the current step's fence, it must preserve the mechanical merge's
// exact tree entries (object IDs, modes, and presence).
func (l *Loop) checkRemoteSyncTree(ctx context.Context, candidate Candidate) (string, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var st remoteSyncState
	var found bool
	var fence []string
	// Read the stage and its fence in one snapshot, not from the lane's text file.
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		st, found, err = readRemoteSyncState(r, candidate.ItemID)
		if err != nil || !found {
			return err
		}
		fence, err = readItemAllowedPaths(r, candidate.ItemID)
		return err
	}); err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("%s: no remote-sync staging record found for %s", ScopeEscape, candidate.ItemID), nil
	}
	if candidate.BaseSHA != st.BaseSHA {
		return fmt.Sprintf("%s: candidate base %s does not match staged conflict base %s", ScopeEscape, shortSHA(candidate.BaseSHA), shortSHA(st.BaseSHA)), nil
	}
	parentsOut, err := rawGit(ctx, candidate.Worktree, nil, "rev-list", "--parents", "-n", "1", candidate.HeadSHA)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(parentsOut)
	if len(fields) != 2 || fields[1] != st.BaseSHA {
		return fmt.Sprintf("%s: %s must be a single linear commit atop %s (got %s)",
			ScopeEscape, shortSHA(candidate.HeadSHA), shortSHA(st.BaseSHA), strings.TrimSpace(parentsOut)), nil
	}
	mt := exec.CommandContext(ctx, "git", "-C", l.cfg.Root, "merge-tree", "--write-tree", "--name-only", "-z", "--merge-base="+st.ConflictParent, st.BaseSHA, st.ConflictCommit)
	mt.Env = boundaryGitEnv()
	var stderr bytes.Buffer
	mt.Stderr = &stderr
	out, err := mt.Output()
	var exit *exec.ExitError
	// Exit 1 is a merge with conflicts, not a failed mechanical merge.
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return "", Externalf("mechanical merge-tree for %s failed (%v): %s", candidate.ItemID, err, strings.TrimSpace(stderr.String()))
	}
	mergeTree, _, _ := strings.Cut(string(out), "\x00")
	if mergeTree == "" {
		return "", Externalf("mechanical merge-tree for %s returned no tree", candidate.ItemID)
	}
	conflicts, _ := parseMergeTreeConflict(string(out))
	if msg := protectedRemoteSyncConflict(candidate.ItemID, conflicts); msg != "" {
		return msg, nil
	}
	for _, path := range conflicts {
		if !insideFence(path, fence) {
			return fmt.Sprintf("%s: mechanical merge left %q conflicted outside the current fence for %s", ScopeEscape, path, candidate.ItemID), nil
		}
	}
	diff, err := rawGit(ctx, l.cfg.Root, nil, "diff-tree", "-r", "--no-renames", "--ignore-submodules=none", "--name-only", "-z", mergeTree, candidate.HeadSHA+"^{tree}")
	if err != nil {
		return "", err
	}
	var outOfFence []string
	for _, path := range strings.Split(diff, "\x00") {
		if path != "" && !insideFence(path, fence) {
			outOfFence = append(outOfFence, path)
		}
	}
	if len(outOfFence) > 0 {
		sort.Strings(outOfFence)
		return fmt.Sprintf("%s: %s altered unfenced path(s) relative to the mechanical merge: %s",
			ScopeEscape, candidate.ItemID, strings.Join(outOfFence, ", ")), nil
	}
	return "", nil
}

func readItemAllowedPaths(r *store.Reader, itemID string) ([]string, error) {
	var raw string
	if err := r.QueryRow(`SELECT COALESCE(NULLIF(allowed_paths, ''), '[]') FROM items WHERE item_id=?`, itemID).Scan(&raw); err != nil {
		return nil, err
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

// protectedRemoteSyncConflict rejects protected or unrepresentable conflict
// paths even if their conflict-marker blobs match the mechanical merge.
func protectedRemoteSyncConflict(itemID string, conflicts []string) string {
	_, gatePaths, literalPaths := remoteSyncSplitFence(conflicts)
	if problem := remoteSyncFenceProblem(gatePaths, literalPaths); problem != "" {
		return fmt.Sprintf("%s: repair %s needs the owner to resolve conflicts on %s", ScopeEscape, itemID, problem)
	}
	return ""
}
