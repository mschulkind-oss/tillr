package drainmodel

import (
	"context"

	"github.com/mschulkind-oss/tillr-drain/fixer"
)

// CheckTrunkWorktree refuses an override in the trunk's shared Git config.
func CheckTrunkWorktree(ctx context.Context, root string) error {
	return WrapFixerError(fixer.CheckTrunkWorktree(ctx, root))
}
