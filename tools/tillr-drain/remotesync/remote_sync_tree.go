package remotesync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// ProtectedRemoteSyncConflict rejects protected or unrepresentable conflict
// paths even if their conflict-marker blobs match the mechanical merge.
func ProtectedRemoteSyncConflict(itemID string, conflicts []string, isGateCode func(string) bool) string {
	if problem := RemoteSyncProtectedPaths(conflicts, isGateCode); problem != "" {
		return fmt.Sprintf("%s: repair %s needs the owner to resolve conflicts on %s", ScopeEscape, itemID, problem)
	}
	return ""
}

// ValidateRemoteSyncTree checks that headSHA is a single linear commit atop the
// staged conflict base and preserves all non-conflicting tree entries relative
// to the mechanical merge-tree.
func ValidateRemoteSyncTree(ctx context.Context, root, worktree, itemID, baseSHA, headSHA string, st RemoteSyncState, found bool, boundaryEnv []string, isGateCode func(string) bool) (string, error) {
	if !found {
		return fmt.Sprintf("%s: no remote-sync staging record found for %s", ScopeEscape, itemID), nil
	}
	if baseSHA != st.BaseSHA {
		return fmt.Sprintf("%s: candidate base %s does not match staged conflict base %s", ScopeEscape, shortSHA(baseSHA), shortSHA(st.BaseSHA)), nil
	}
	parentsCmd := exec.CommandContext(ctx, "git", "-C", worktree, "rev-list", "--parents", "-n", "1", headSHA)
	parentsCmd.Env = boundaryEnv
	var parentsStderr bytes.Buffer
	parentsCmd.Stderr = &parentsStderr
	parentsBytes, err := parentsCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-list --parents %s failed (%v): %s", headSHA, err, strings.TrimSpace(parentsStderr.String()))
	}
	parentsOut := string(parentsBytes)
	fields := strings.Fields(parentsOut)
	if len(fields) != 2 || fields[1] != st.BaseSHA {
		return fmt.Sprintf("%s: %s must be a single linear commit atop %s (got %s)",
			ScopeEscape, shortSHA(headSHA), shortSHA(st.BaseSHA), strings.TrimSpace(parentsOut)), nil
	}
	mt := exec.CommandContext(ctx, "git", "-C", root, "merge-tree", "--write-tree", "--name-only", "-z", "--merge-base="+st.ConflictParent, st.BaseSHA, st.ConflictCommit)
	mt.Env = boundaryEnv
	var stderr bytes.Buffer
	mt.Stderr = &stderr
	out, err := mt.Output()
	var exit *exec.ExitError
	// Exit 1 is a merge with conflicts, not a failed mechanical merge.
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return "", fmt.Errorf("mechanical merge-tree for %s failed (%v): %s", itemID, err, strings.TrimSpace(stderr.String()))
	}
	mergeTree, _, _ := strings.Cut(string(out), "\x00")
	if mergeTree == "" {
		return "", fmt.Errorf("mechanical merge-tree for %s returned no tree", itemID)
	}
	conflicts, _ := ParseMergeTreeConflict(string(out))
	if msg := ProtectedRemoteSyncConflict(itemID, conflicts, isGateCode); msg != "" {
		return msg, nil
	}
	conflicted := make(map[string]bool, len(conflicts))
	for _, path := range conflicts {
		conflicted[path] = true
	}
	diffCmd := exec.CommandContext(ctx, "git", "-C", root, "diff-tree", "-r", "--no-renames", "--ignore-submodules=none", "--name-only", "-z", mergeTree, headSHA+"^{tree}")
	diffCmd.Env = boundaryEnv
	var diffStderr bytes.Buffer
	diffCmd.Stderr = &diffStderr
	diffBytes, err := diffCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff-tree %s %s^{tree} failed (%v): %s", mergeTree, headSHA, err, strings.TrimSpace(diffStderr.String()))
	}
	var unrelated []string
	for _, path := range strings.Split(string(diffBytes), "\x00") {
		if path != "" && !conflicted[path] {
			unrelated = append(unrelated, path)
		}
	}
	if len(unrelated) > 0 {
		sort.Strings(unrelated)
		return fmt.Sprintf("%s: %s altered non-conflicting path(s) relative to the mechanical merge: %s",
			ScopeEscape, itemID, strings.Join(unrelated, ", ")), nil
	}
	return "", nil
}
