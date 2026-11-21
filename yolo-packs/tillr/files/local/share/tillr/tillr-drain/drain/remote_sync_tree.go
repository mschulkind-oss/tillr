package drain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// checkRemoteSyncTree requires a single commit atop the current conflict base.
// Outside the paths Git reported conflicted, it must preserve the mechanical merge’s
// exact tree entries (object IDs, modes, and presence).
func (l *Loop) checkRemoteSyncTree(ctx context.Context, candidate Candidate) (string, error) {
	st, found, err := l.readRemoteSyncState(ctx, candidate.ItemID)
	if err != nil {
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
	conflicted := make(map[string]bool, len(conflicts))
	for _, path := range conflicts {
		conflicted[path] = true
	}
	diff, err := rawGit(ctx, l.cfg.Root, nil, "diff-tree", "-r", "--no-renames", "--ignore-submodules=none", "--name-only", "-z", mergeTree, candidate.HeadSHA+"^{tree}")
	if err != nil {
		return "", err
	}
	var unrelated []string
	for _, path := range strings.Split(diff, "\x00") {
		if path != "" && !conflicted[path] {
			unrelated = append(unrelated, path)
		}
	}
	if len(unrelated) > 0 {
		sort.Strings(unrelated)
		return fmt.Sprintf("%s: %s altered non-conflicting path(s) relative to the mechanical merge: %s",
			ScopeEscape, candidate.ItemID, strings.Join(unrelated, ", ")), nil
	}
	return "", nil
}

// protectedRemoteSyncConflict rejects protected or unrepresentable conflict
// paths even if their conflict-marker blobs match the mechanical merge.
func protectedRemoteSyncConflict(itemID string, conflicts []string) string {
	if problem := remoteSyncProtectedPaths(conflicts); problem != "" {
		return fmt.Sprintf("%s: repair %s needs the owner to resolve conflicts on %s", ScopeEscape, itemID, problem)
	}
	return ""
}
