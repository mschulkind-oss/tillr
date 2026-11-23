package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr-drain/remotesync"
)

// checkRemoteSyncTree requires a single commit atop the current conflict base.
// Outside the paths Git reported conflicted, it must preserve the mechanical merge’s
// exact tree entries (object IDs, modes, and presence).
func (l *Loop) checkRemoteSyncTree(ctx context.Context, candidate Candidate) (string, error) {
	st, found, err := l.readRemoteSyncState(ctx, candidate.ItemID)
	if err != nil {
		return "", err
	}
	msg, err := remotesync.ValidateRemoteSyncTree(ctx, l.cfg.Root, candidate.Worktree, candidate.ItemID, candidate.BaseSHA, candidate.HeadSHA, st, found, boundaryGitEnv(), GateCode)
	if err != nil {
		return "", Externalf("%v", err)
	}
	return msg, nil
}

// protectedRemoteSyncConflict rejects protected or unrepresentable conflict
// paths even if their conflict-marker blobs match the mechanical merge.
func protectedRemoteSyncConflict(itemID string, conflicts []string) string {
	return remotesync.ProtectedRemoteSyncConflict(itemID, conflicts, GateCode)
}
